package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) InspectRefreshCredential(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (domain.Endpoint, domain.RefreshCredential, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Endpoint{}, domain.RefreshCredential{}, err
	}
	if zeroHash(tokenHash) || now.IsZero() {
		return domain.Endpoint{}, domain.RefreshCredential{}, domain.NewProblem(domain.CodeInvalidArgument, "refresh credential is invalid", nil)
	}
	var refreshRecord refreshCredentialRow
	if err := store.db.WithContext(ctx).Where("token_hash = ?", hashString(tokenHash)).Take(&refreshRecord).Error; err != nil {
		return domain.Endpoint{}, domain.RefreshCredential{}, gatewayRefreshError(err)
	}
	refresh, err := refreshCredentialFromRow(refreshRecord)
	if err != nil {
		return domain.Endpoint{}, domain.RefreshCredential{}, err
	}
	var endpointRecord endpointRow
	if err := store.db.WithContext(ctx).Where("id = ?", refreshRecord.EndpointID).Take(&endpointRecord).Error; err != nil {
		return domain.Endpoint{}, domain.RefreshCredential{}, gatewayRefreshError(err)
	}
	endpoint, err := endpointFromRow(endpointRecord)
	if err != nil {
		return domain.Endpoint{}, domain.RefreshCredential{}, err
	}
	if endpoint.Status != domain.EndpointStatusActive || endpoint.RevokedAt != nil || refresh.RevokedAt != nil ||
		refresh.Status == domain.CredentialStatusRevoked {
		return domain.Endpoint{}, domain.RefreshCredential{}, domain.NewProblem(domain.CodeRevoked, "refresh credential is unavailable", nil)
	}
	if refresh.Status == domain.CredentialStatusExpired || !now.Before(refresh.ExpiresAt) {
		return domain.Endpoint{}, domain.RefreshCredential{}, domain.NewProblem(domain.CodeExpired, "refresh credential is expired", nil)
	}
	if refresh.EndpointID != endpoint.ID || refresh.FamilyID != endpoint.CredentialFamilyID || refresh.SigningJKT != endpoint.SigningJKT {
		return domain.Endpoint{}, domain.RefreshCredential{}, domain.NewProblem(domain.CodeSecurityViolation, "refresh credential binding is invalid", nil)
	}
	if refresh.Status != domain.CredentialStatusActive && refresh.Status != domain.CredentialStatusRotated {
		return domain.Endpoint{}, domain.RefreshCredential{}, domain.NewProblem(domain.CodeInvalidState, "refresh credential state is invalid", nil)
	}
	return endpoint, refresh, nil
}

func (store *Store) RotateRefreshCredential(
	ctx context.Context,
	presentedHash [32]byte,
	expectedSigningJKT string,
	nextAccess domain.EndpointCredential,
	nextRefresh domain.RefreshCredential,
	audit domain.SecurityAuditEvent,
	now time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if zeroHash(presentedHash) || strings.TrimSpace(expectedSigningJKT) == "" || now.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "refresh rotation input is invalid", nil)
	}
	var reused bool
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Read immutable routing data before taking locks, then use the shared
		// Endpoint -> Credential order used by revocation and frame writes.
		var candidate refreshCredentialRow
		if err := tx.Where("token_hash = ?", hashString(presentedHash)).Take(&candidate).Error; err != nil {
			return gatewayRefreshError(err)
		}
		var endpointRecord endpointRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", candidate.EndpointID).Take(&endpointRecord).Error; err != nil {
			return gatewayRefreshError(err)
		}
		var currentRow refreshCredentialRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND token_hash = ? AND endpoint_id = ?",
			candidate.ID, hashString(presentedHash), endpointRecord.ID,
		).Take(&currentRow).Error; err != nil {
			return gatewayRefreshError(err)
		}
		current, err := refreshCredentialFromRow(currentRow)
		if err != nil {
			return err
		}
		endpoint, err := endpointFromRow(endpointRecord)
		if err != nil {
			return err
		}
		if endpoint.Status != domain.EndpointStatusActive || current.RevokedAt != nil || current.Status == domain.CredentialStatusRevoked {
			return domain.NewProblem(domain.CodeRevoked, "refresh credential is unavailable", nil)
		}
		if !now.Before(current.ExpiresAt) {
			return domain.NewProblem(domain.CodeExpired, "refresh credential is expired", nil)
		}
		if current.SigningJKT != expectedSigningJKT || endpoint.SigningJKT != expectedSigningJKT ||
			current.EndpointID != endpoint.ID || current.FamilyID != endpoint.CredentialFamilyID {
			return domain.NewProblem(domain.CodeSecurityViolation, "refresh credential binding is invalid", nil)
		}
		if current.Status == domain.CredentialStatusRotated {
			reused = true
			if err := revokeCredentialFamily(tx, current.FamilyID, now); err != nil {
				return err
			}
			return nil
		}
		if current.Status != domain.CredentialStatusActive {
			return domain.NewProblem(domain.CodeInvalidState, "refresh credential state is invalid", nil)
		}
		if err := validateCredential(nextAccess, endpoint, now); err != nil {
			return err
		}
		if err := nextRefresh.Validate(endpoint, now); err != nil {
			return err
		}
		if nextRefresh.RotatedFrom != current.ID || nextAccess.ID.IsZero() || nextRefresh.ID.IsZero() {
			return domain.NewProblem(domain.CodeSecurityViolation, "refresh rotation lineage is invalid", nil)
		}
		if err := validateRefreshAudit(audit, endpoint, now); err != nil {
			return err
		}
		metadata, err := audit.CanonicalMetadataJSON()
		if err != nil {
			return err
		}
		var activeAccess credentialRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"family_id = ? AND status = ?", currentRow.FamilyID, string(domain.CredentialStatusActive),
		).Order("created_at DESC").Take(&activeAccess).Error; err != nil {
			return gatewayCredentialError(err)
		}
		nextAccess.RotatedFrom, err = parseID(activeAccess.ID)
		if err != nil {
			return err
		}
		accessRow, err := credentialToRow(nextAccess)
		if err != nil {
			return err
		}
		refreshRow := refreshCredentialToRow(nextRefresh)
		if err := tx.Model(new(credentialRow)).Where(
			"family_id = ? AND status = ?", currentRow.FamilyID, string(domain.CredentialStatusActive),
		).Updates(map[string]any{
			"status": string(domain.CredentialStatusRotated), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("rotate access credential: %w", err)
		}
		result := tx.Model(new(refreshCredentialRow)).Where(
			"id = ? AND status = ? AND rotated_to_id = ''", currentRow.ID, string(domain.CredentialStatusActive),
		).Updates(map[string]any{
			"status": string(domain.CredentialStatusRotated), "used_at": now,
			"rotated_to_id": nextRefresh.ID.String(), "updated_at": now,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "rotate refresh credential")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "refresh credential changed concurrently", nil)
		}
		if err := tx.Create(&accessRow).Error; err != nil {
			return classifyPersistence(err, "create rotated access credential")
		}
		if err := tx.Create(&refreshRow).Error; err != nil {
			return classifyPersistence(err, "create rotated refresh credential")
		}
		auditRow := auditToRow(audit, metadata)
		if err := tx.Create(&auditRow).Error; err != nil {
			return classifyPersistence(err, "append refresh rotation audit")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if reused {
		return domain.NewProblem(domain.CodeRevoked, "refresh credential reuse revoked the credential family", nil)
	}
	return nil
}

func revokeCredentialFamily(tx *gorm.DB, familyID domain.ID, now time.Time) error {
	if err := tx.Model(new(credentialRow)).Where(
		"family_id = ? AND status = ?", familyID.String(), string(domain.CredentialStatusActive),
	).Updates(map[string]any{
		"status": string(domain.CredentialStatusRevoked), "revoked_at": now, "updated_at": now,
	}).Error; err != nil {
		return fmt.Errorf("revoke reused access credential family: %w", err)
	}
	if err := tx.Model(new(refreshCredentialRow)).Where(
		"family_id = ? AND status IN ?", familyID.String(),
		[]string{string(domain.CredentialStatusActive), string(domain.CredentialStatusRotated)},
	).Updates(map[string]any{
		"status": string(domain.CredentialStatusRevoked), "revoked_at": now, "updated_at": now,
	}).Error; err != nil {
		return fmt.Errorf("revoke reused refresh credential family: %w", err)
	}
	return nil
}

func validateRefreshAudit(audit domain.SecurityAuditEvent, endpoint domain.Endpoint, now time.Time) error {
	if err := audit.Validate(); err != nil {
		return err
	}
	if audit.OwnerUserID != endpoint.OwnerUserID || audit.TenantID != endpoint.TenantID ||
		audit.ActorType != domain.AuditActorEndpoint || audit.ActorID != endpoint.ID.String() ||
		audit.Action != "endpoint.token.refresh" || audit.ObjectType != "endpoint" ||
		audit.ObjectID != endpoint.ID.String() || audit.Result != "success" || audit.CreatedAt != now {
		return domain.NewProblem(domain.CodeSecurityViolation, "refresh audit binding is invalid", nil)
	}
	return nil
}

func refreshCredentialFromRow(row refreshCredentialRow) (domain.RefreshCredential, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.RefreshCredential{}, err
	}
	endpointID, err := parseID(row.EndpointID)
	if err != nil {
		return domain.RefreshCredential{}, err
	}
	familyID, err := parseID(row.FamilyID)
	if err != nil {
		return domain.RefreshCredential{}, err
	}
	rotatedFrom, err := parseOptionalID(row.RotatedFrom)
	if err != nil {
		return domain.RefreshCredential{}, err
	}
	rotatedTo, err := parseOptionalID(row.RotatedTo)
	if err != nil {
		return domain.RefreshCredential{}, err
	}
	tokenHash, err := parseHash(row.TokenHash)
	if err != nil {
		return domain.RefreshCredential{}, fmt.Errorf("decode refresh credential hash: %w", err)
	}
	return domain.RefreshCredential{
		ID: id, EndpointID: endpointID, FamilyID: familyID, TokenHash: tokenHash,
		SigningJKT: row.SigningJKT, Status: domain.CredentialStatus(row.Status), ExpiresAt: row.ExpiresAt,
		RotatedFrom: rotatedFrom, RotatedTo: rotatedTo, UsedAt: cloneTime(row.UsedAt),
		RevokedAt: cloneTime(row.RevokedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func gatewayRefreshError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewProblem(domain.CodeNotFound, "refresh credential is unavailable", nil)
	}
	return fmt.Errorf("inspect refresh credential: %w", err)
}

package store

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) CreateHCRegistrationChallenge(
	ctx context.Context,
	challenge domain.HCRegistrationChallenge,
	now time.Time,
	maxTTL time.Duration,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if err := challenge.ValidateNew(now, maxTTL); err != nil {
		return err
	}
	row := hcRegistrationChallengeToRow(challenge)
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create HC registration challenge")
	}
	return nil
}

func (store *Store) ConsumeHCRegistrationChallenge(
	ctx context.Context,
	challengeID domain.ID,
	presentedChallengeHash [32]byte,
	owner string,
	tenant string,
	origin string,
	endpoint domain.Endpoint,
	accessCredential domain.EndpointCredential,
	refreshCredential domain.RefreshCredential,
	audit domain.SecurityAuditEvent,
	now time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)
	origin = strings.TrimSpace(origin)
	if challengeID.IsZero() || owner == "" || origin == "" || zeroHash(presentedChallengeHash) {
		return domain.NewProblem(domain.CodeInvalidArgument, "HC registration challenge binding is invalid", nil)
	}
	if endpoint.Type != domain.EndpointTypeHCWeb || endpoint.OwnerUserID != owner || endpoint.TenantID != tenant {
		return domain.NewProblem(domain.CodeSecurityViolation, "HC endpoint scope is invalid", nil)
	}
	if err := validatePersistedEndpoint(endpoint); err != nil {
		return err
	}
	if err := validateCredential(accessCredential, endpoint, now); err != nil {
		return err
	}
	if err := refreshCredential.Validate(endpoint, now); err != nil {
		return err
	}
	if err := validateHCRegistrationAudit(audit, endpoint, owner, tenant); err != nil {
		return err
	}
	auditMetadata, err := audit.CanonicalMetadataJSON()
	if err != nil {
		return err
	}

	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row hcRegistrationChallengeRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", challengeID.String()).Error; err != nil {
			return notFoundOr("lock HC registration challenge", "HC registration challenge was not found", err)
		}
		challenge, err := hcRegistrationChallengeFromRow(row)
		if err != nil {
			return err
		}
		if challenge.OwnerUserID != owner || challenge.TenantID != tenant || challenge.Origin != origin {
			return domain.NewProblem(domain.CodeNotFound, "HC registration challenge was not found", nil)
		}
		if subtle.ConstantTimeCompare(challenge.ChallengeHash[:], presentedChallengeHash[:]) != 1 {
			return domain.NewProblem(domain.CodeSecurityViolation, "HC registration challenge is invalid", nil)
		}
		switch challenge.Status {
		case domain.HCRegistrationChallengeConsumed:
			return domain.NewProblem(domain.CodeConflict, "HC registration challenge was already consumed", nil)
		case domain.HCRegistrationChallengeExpired:
			return domain.NewProblem(domain.CodeExpired, "HC registration challenge is expired", nil)
		case domain.HCRegistrationChallengePending:
		default:
			return domain.NewProblem(domain.CodeInvalidState, "HC registration challenge state is invalid", nil)
		}
		if !now.Before(challenge.ExpiresAt) {
			return domain.NewProblem(domain.CodeExpired, "HC registration challenge is expired", nil)
		}

		result := tx.Model(new(hcRegistrationChallengeRow)).Where(
			"id = ? AND row_version = ? AND status = ?",
			row.ID,
			row.RowVersion,
			string(domain.HCRegistrationChallengePending),
		).Updates(map[string]any{
			"status":      string(domain.HCRegistrationChallengeConsumed),
			"consumed_at": now,
			"updated_at":  now,
			"row_version": row.RowVersion + 1,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "consume HC registration challenge")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "HC registration challenge changed concurrently", nil)
		}

		endpointRecord := endpointToRow(endpoint)
		accessRecord, err := credentialToRow(accessCredential)
		if err != nil {
			return err
		}
		refreshRecord := refreshCredentialToRow(refreshCredential)
		auditRecord := auditToRow(audit, auditMetadata)
		if err := tx.Create(&endpointRecord).Error; err != nil {
			return classifyPersistence(err, "create HC endpoint")
		}
		if err := tx.Create(&accessRecord).Error; err != nil {
			return classifyPersistence(err, "create HC access credential")
		}
		if err := tx.Create(&refreshRecord).Error; err != nil {
			return classifyPersistence(err, "create HC refresh credential")
		}
		if err := tx.Create(&auditRecord).Error; err != nil {
			return classifyPersistence(err, "append HC registration audit")
		}
		return nil
	})
}

func validateHCRegistrationAudit(
	audit domain.SecurityAuditEvent,
	endpoint domain.Endpoint,
	owner string,
	tenant string,
) error {
	if err := audit.Validate(); err != nil {
		return err
	}
	if audit.OwnerUserID != owner || audit.TenantID != tenant ||
		audit.ActorType != domain.AuditActorHuman || audit.Action != "hc.endpoint.register" ||
		audit.ObjectType != "endpoint" || audit.ObjectID != endpoint.ID.String() || audit.Result != "success" {
		return domain.NewProblem(domain.CodeSecurityViolation, "HC registration audit binding is invalid", nil)
	}
	return nil
}

func hcRegistrationChallengeToRow(value domain.HCRegistrationChallenge) hcRegistrationChallengeRow {
	return hcRegistrationChallengeRow{
		ID: value.ID.String(), OwnerUserID: strings.TrimSpace(value.OwnerUserID), TenantID: strings.TrimSpace(value.TenantID),
		Origin: strings.TrimSpace(value.Origin), ChallengeHash: hashString(value.ChallengeHash), Status: string(value.Status),
		ExpiresAt: value.ExpiresAt, ConsumedAt: cloneTime(value.ConsumedAt), CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, RowVersion: value.RowVersion,
	}
}

func hcRegistrationChallengeFromRow(row hcRegistrationChallengeRow) (domain.HCRegistrationChallenge, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.HCRegistrationChallenge{}, err
	}
	hash, err := parseHash(row.ChallengeHash)
	if err != nil {
		return domain.HCRegistrationChallenge{}, fmt.Errorf("decode HC registration challenge hash: %w", err)
	}
	return domain.HCRegistrationChallenge{
		ID: id, OwnerUserID: row.OwnerUserID, TenantID: row.TenantID, Origin: row.Origin,
		ChallengeHash: hash, Status: domain.HCRegistrationChallengeStatus(row.Status), ExpiresAt: row.ExpiresAt,
		ConsumedAt: cloneTime(row.ConsumedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		RowVersion: row.RowVersion,
	}, nil
}

func refreshCredentialToRow(value domain.RefreshCredential) refreshCredentialRow {
	return refreshCredentialRow{
		ID: value.ID.String(), EndpointID: value.EndpointID.String(), FamilyID: value.FamilyID.String(),
		TokenHash: hashString(value.TokenHash), SigningJKT: value.SigningJKT, Status: string(value.Status),
		ExpiresAt: value.ExpiresAt, RotatedFrom: optionalID(value.RotatedFrom), RotatedTo: optionalID(value.RotatedTo),
		UsedAt: cloneTime(value.UsedAt), RevokedAt: cloneTime(value.RevokedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func optionalID(value domain.ID) string {
	if value.IsZero() {
		return ""
	}
	return value.String()
}

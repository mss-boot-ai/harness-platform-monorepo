package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RevokeEndpointForOwner applies the revocation cascade inside one transaction
// whose lookup and every mutable query are constrained by owner and tenant.
func (store *Store) RevokeEndpointForOwner(
	ctx context.Context,
	id domain.ID,
	owner string,
	tenant string,
	now time.Time,
) (domain.Endpoint, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return domain.Endpoint{}, err
	}
	if id.IsZero() || now.IsZero() {
		return domain.Endpoint{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint revocation is invalid", nil)
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)
	var revoked domain.Endpoint
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock every scoped session before the endpoint. Key-package insertion
		// uses the same session-before-endpoint order, avoiding a lock inversion.
		var lockedSessions []sessionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"owner_user_id = ? AND tenant_id = ? AND (aba_endpoint_id = ? OR hc_endpoint_id = ?)",
			owner, tenant, id.String(), id.String(),
		).Order("id").Find(&lockedSessions).Error; err != nil {
			return fmt.Errorf("lock endpoint sessions: %w", err)
		}
		// SQLite ignores SELECT FOR UPDATE. A conditional no-op write gives the
		// same transaction a write fence; other databases keep the row locks above.
		if tx.Dialector.Name() == "sqlite" {
			for _, session := range lockedSessions {
				result := tx.Model(new(sessionRow)).Where(
					"id = ? AND owner_user_id = ? AND tenant_id = ? AND row_version = ?",
					session.ID, owner, tenant, session.RowVersion,
				).UpdateColumn("row_version", gorm.Expr("row_version"))
				if result.Error != nil {
					return fmt.Errorf("fence endpoint session %s: %w", session.ID, result.Error)
				}
				if result.RowsAffected != 1 {
					return domain.NewProblem(domain.CodeConflict, "endpoint session changed concurrently", nil)
				}
			}
		}

		var row endpointRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
			&row,
			"id = ? AND owner_user_id = ? AND tenant_id = ?",
			id.String(), owner, tenant,
		).Error; err != nil {
			return notFoundOr("lock endpoint", "endpoint was not found", err)
		}
		endpoint, err := endpointFromRow(row)
		if err != nil {
			return err
		}

		sessionColumn := "hc_endpoint_id"
		sessionStatus := domain.SessionStatusRekeyRequired
		updates := map[string]any{
			"status":      string(sessionStatus),
			"updated_at":  now,
			"row_version": gorm.Expr("row_version + 1"),
		}
		if endpoint.Type == domain.EndpointTypeABA {
			sessionColumn = "aba_endpoint_id"
			sessionStatus = domain.SessionStatusABARevoked
			updates["status"] = string(sessionStatus)
			updates["closed_at"] = now
		}
		sessionIDs := make([]string, 0, len(lockedSessions))
		for _, session := range lockedSessions {
			if (sessionColumn == "aba_endpoint_id" && session.ABAEndpointID == row.ID) ||
				(sessionColumn == "hc_endpoint_id" && session.HCEndpointID == row.ID) {
				sessionIDs = append(sessionIDs, session.ID)
			}
		}

		previous := endpoint.RowVersion
		if err := endpoint.Revoke(now); err != nil {
			return err
		}
		result := tx.Model(new(endpointRow)).Where(
			"id = ? AND owner_user_id = ? AND tenant_id = ? AND row_version = ?",
			row.ID, owner, tenant, previous,
		).Updates(map[string]any{
			"status":      string(endpoint.Status),
			"revoked_at":  endpoint.RevokedAt,
			"updated_at":  endpoint.UpdatedAt,
			"row_version": endpoint.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "revoke endpoint")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "endpoint changed concurrently", nil)
		}
		if err := tx.Model(new(credentialRow)).
			Where("endpoint_id = ? AND status = ?", row.ID, string(domain.CredentialStatusActive)).
			Updates(map[string]any{
				"status":     string(domain.CredentialStatusRevoked),
				"revoked_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("revoke credentials: %w", err)
		}
		if err := tx.Model(new(ticketRow)).
			Where("endpoint_id = ? AND status = ?", row.ID, string(domain.TicketStatusIssued)).
			Updates(map[string]any{
				"status":      string(domain.TicketStatusRevoked),
				"row_version": gorm.Expr("row_version + 1"),
			}).Error; err != nil {
			return fmt.Errorf("revoke tickets: %w", err)
		}
		if len(sessionIDs) != 0 {
			if err := tx.Model(new(keyPackageRow)).Where(
				"session_id IN ? AND status IN ?",
				sessionIDs,
				[]string{
					string(domain.KeyPackageStatusPending),
					string(domain.KeyPackageStatusDelivered),
					string(domain.KeyPackageStatusAcknowledged),
				},
			).Update("status", string(domain.KeyPackageStatusRevoked)).Error; err != nil {
				return fmt.Errorf("revoke endpoint key packages: %w", err)
			}
			if err := tx.Model(new(sessionRow)).Where(
				"id IN ? AND owner_user_id = ? AND tenant_id = ? AND "+sessionColumn+" = ? AND status NOT IN ?",
				sessionIDs, owner, tenant, row.ID, []string{
					string(domain.SessionStatusClosed),
					string(domain.SessionStatusABARevoked),
					string(domain.SessionStatusFailed),
				},
			).Updates(updates).Error; err != nil {
				return fmt.Errorf("update sessions after endpoint revocation: %w", err)
			}
		}
		revoked = endpoint
		return nil
	})
	return revoked, normalizeConcurrencyError("endpoint revocation changed concurrently", err)
}

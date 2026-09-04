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
		sessionStatus := domain.SessionStatusRekeyRequired
		column := "hc_endpoint_id"
		updates := map[string]any{
			"status":      string(sessionStatus),
			"updated_at":  now,
			"row_version": gorm.Expr("row_version + 1"),
		}
		if endpoint.Type == domain.EndpointTypeABA {
			sessionStatus = domain.SessionStatusABARevoked
			column = "aba_endpoint_id"
			updates["status"] = string(sessionStatus)
			updates["closed_at"] = now
		}
		if err := tx.Model(new(sessionRow)).Where(
			"owner_user_id = ? AND tenant_id = ? AND "+column+" = ? AND status NOT IN ?",
			owner, tenant, row.ID, []string{
				string(domain.SessionStatusClosed),
				string(domain.SessionStatusABARevoked),
			},
		).Updates(updates).Error; err != nil {
			return fmt.Errorf("update sessions after endpoint revocation: %w", err)
		}
		revoked = endpoint
		return nil
	})
	return revoked, err
}

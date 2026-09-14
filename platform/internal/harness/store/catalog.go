package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) PublishExecutionCatalog(ctx context.Context, catalog domain.PublishedCatalog) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	revision, err := catalog.Catalog.Revision()
	if err != nil {
		return err
	}
	if catalog.EndpointID.IsZero() || catalog.OwnerUserID == "" || catalog.ConnectionGeneration == 0 || catalog.Revision != revision || catalog.PublishedAt.IsZero() || !catalog.ExpiresAt.After(catalog.PublishedAt) || catalog.ExpiresAt.Sub(catalog.PublishedAt) > 15*time.Minute {
		return domain.NewProblem(domain.CodeInvalidArgument, "catalog publication binding is invalid", nil)
	}
	encoded, err := json.Marshal(catalog.Catalog)
	if err != nil {
		return err
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var endpoint endpointRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&endpoint, "id = ? AND owner_user_id = ? AND tenant_id = ?", catalog.EndpointID.String(), catalog.OwnerUserID, catalog.TenantID).Error; err != nil {
			return notFoundOr("lock catalog endpoint", "catalog endpoint was not found", err)
		}
		if endpoint.Type != string(domain.EndpointTypeABA) || endpoint.Status != string(domain.EndpointStatusActive) {
			return domain.NewProblem(domain.CodeSecurityViolation, "catalog publisher is unavailable", nil)
		}
		var generation connectionGenerationRow
		if err := tx.First(&generation, "endpoint_id = ?", endpoint.ID).Error; err != nil {
			return notFoundOr("read catalog generation", "publisher generation was not found", err)
		}
		if generation.Generation != catalog.ConnectionGeneration {
			return domain.NewProblem(domain.CodeConflict, "catalog publisher generation is stale", nil)
		}
		row := catalogRow{EndpointID: endpoint.ID, OwnerUserID: endpoint.OwnerUserID, TenantID: endpoint.TenantID,
			ConnectionGeneration: catalog.ConnectionGeneration, Revision: revision, CatalogJSON: string(encoded), PublishedAt: catalog.PublishedAt, ExpiresAt: catalog.ExpiresAt}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "endpoint_id"}}, DoUpdates: clause.AssignmentColumns([]string{"connection_generation", "revision", "catalog_json", "published_at", "expires_at"})}).Create(&row).Error; err != nil {
			return classifyPersistence(err, "publish execution catalog")
		}
		return nil
	})
}

func (store *Store) GetExecutionCatalog(ctx context.Context, owner, tenant string, id domain.ID, now time.Time) (domain.PublishedCatalog, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.PublishedCatalog{}, err
	}
	var row catalogRow
	err := store.db.WithContext(ctx).Table("harness_execution_catalogs AS catalog").Select("catalog.*").
		Joins("JOIN harness_endpoints AS endpoint ON endpoint.id = catalog.endpoint_id").
		Joins("JOIN harness_connection_generations AS generation ON generation.endpoint_id = endpoint.id AND generation.generation = catalog.connection_generation").
		Where("catalog.endpoint_id = ? AND catalog.owner_user_id = ? AND catalog.tenant_id = ? AND endpoint.owner_user_id = ? AND endpoint.tenant_id = ? AND endpoint.type = ? AND endpoint.status = ?", id.String(), owner, tenant, owner, tenant, string(domain.EndpointTypeABA), string(domain.EndpointStatusActive)).
		Take(&row).Error
	if err != nil {
		return domain.PublishedCatalog{}, notFoundOr("read execution catalog", "execution catalog is unavailable or stale", err)
	}
	if !now.Before(row.ExpiresAt) {
		return domain.PublishedCatalog{}, domain.NewProblem(domain.CodeExpired, "execution catalog expired", nil)
	}
	var catalog domain.ExecutionCatalog
	if err := json.Unmarshal([]byte(row.CatalogJSON), &catalog); err != nil {
		return domain.PublishedCatalog{}, domain.NewProblem(domain.CodeSecurityViolation, "stored execution catalog is invalid", nil)
	}
	revision, err := catalog.Revision()
	if err != nil || revision != row.Revision {
		return domain.PublishedCatalog{}, domain.NewProblem(domain.CodeSecurityViolation, "stored execution catalog revision is invalid", nil)
	}
	return domain.PublishedCatalog{EndpointID: id, OwnerUserID: owner, TenantID: tenant, ConnectionGeneration: row.ConnectionGeneration,
		Revision: row.Revision, Catalog: catalog, PublishedAt: row.PublishedAt, ExpiresAt: row.ExpiresAt}, nil
}

package store

import (
	"errors"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const CatalogMigrationID migration.MigrationID = "20260914010000"

type catalogRow struct {
	EndpointID           string    `gorm:"column:endpoint_id;type:char(32);primaryKey"`
	OwnerUserID          string    `gorm:"column:owner_user_id;size:128;not null"`
	TenantID             string    `gorm:"column:tenant_id;size:128;not null;default:''"`
	ConnectionGeneration uint64    `gorm:"column:connection_generation;not null"`
	Revision             string    `gorm:"column:revision;type:char(64);not null"`
	CatalogJSON          string    `gorm:"column:catalog_json;type:text;not null"`
	PublishedAt          time.Time `gorm:"column:published_at;not null"`
	ExpiresAt            time.Time `gorm:"column:expires_at;not null"`
}

func (catalogRow) TableName() string { return "harness_execution_catalogs" }

func RegisterCatalogMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("catalog migration runner is required")
	}
	return runner.Register(CatalogMigrationID, func(db *gorm.DB, version string) error {
		if version != CatalogMigrationID.String() {
			return errors.New("catalog migration version mismatch")
		}
		if err := CreateCatalogSchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

func CreateCatalogSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("catalog database is required")
	}
	if !db.Migrator().HasTable(new(catalogRow)) {
		if err := db.Migrator().CreateTable(new(catalogRow)); err != nil {
			return err
		}
	}
	return VerifyCatalogSchema(db)
}

func VerifyCatalogSchema(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(new(catalogRow)) {
		return errors.New("Harness execution catalog schema is unavailable")
	}
	for _, column := range []string{"endpoint_id", "owner_user_id", "tenant_id", "connection_generation", "revision", "catalog_json", "published_at", "expires_at"} {
		if !db.Migrator().HasColumn(new(catalogRow), column) {
			return errors.New("Harness execution catalog column is unavailable")
		}
	}
	return nil
}

package store

import (
	"errors"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const M2ConnectionMigrationID migration.MigrationID = "20260905020000"

type connectionGenerationRow struct {
	EndpointID string    `gorm:"column:endpoint_id;type:char(32);primaryKey"`
	Generation uint64    `gorm:"column:generation;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (connectionGenerationRow) TableName() string { return "harness_connection_generations" }

func RegisterM2ConnectionMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness M2 connection migration runner is required")
	}
	return runner.Register(M2ConnectionMigrationID, func(db *gorm.DB, version string) error {
		if version != M2ConnectionMigrationID.String() {
			return errors.New("harness M2 connection migration version mismatch")
		}
		if err := CreateM2ConnectionSchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

func CreateM2ConnectionSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M2 connection schema database is required")
	}
	if !db.Migrator().HasTable(new(connectionGenerationRow)) {
		if err := db.Migrator().CreateTable(new(connectionGenerationRow)); err != nil {
			return err
		}
	}
	return VerifyM2ConnectionSchema(db)
}

func VerifyM2ConnectionSchema(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(new(connectionGenerationRow)) {
		return errors.New("Harness connection generation table is unavailable")
	}
	return nil
}

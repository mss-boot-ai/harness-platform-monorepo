package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const M2GatewayMigrationID migration.MigrationID = "20260905010000"

type dpopReplayRow struct {
	JKT       string    `gorm:"column:jkt;size:64;primaryKey"`
	JTI       string    `gorm:"column:jti;size:64;primaryKey"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null;index:idx_harness_dpop_replay_expiry"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (dpopReplayRow) TableName() string { return "harness_dpop_replays" }

type endpointNonceRow struct {
	EndpointID string    `gorm:"column:endpoint_id;type:char(32);primaryKey"`
	NonceHash  string    `gorm:"column:nonce_hash;type:char(64);not null"`
	ExpiresAt  time.Time `gorm:"column:expires_at;not null;index:idx_harness_endpoint_nonce_expiry"`
	IssuedAt   time.Time `gorm:"column:issued_at;not null"`
	RowVersion uint64    `gorm:"column:row_version;not null;default:0"`
}

func (endpointNonceRow) TableName() string { return "harness_endpoint_nonces" }

var m2GatewaySchemaModels = []any{new(dpopReplayRow), new(endpointNonceRow)}

var requiredM2GatewayIndexes = []struct {
	model any
	name  string
}{
	{new(dpopReplayRow), "idx_harness_dpop_replay_expiry"},
	{new(endpointNonceRow), "idx_harness_endpoint_nonce_expiry"},
}

func RegisterM2GatewayMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness M2 Gateway migration runner is required")
	}
	if err := runner.Register(M2GatewayMigrationID, func(db *gorm.DB, version string) error {
		if version != M2GatewayMigrationID.String() {
			return errors.New("harness M2 Gateway migration version mismatch")
		}
		if err := CreateM2GatewaySchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	}); err != nil {
		return err
	}
	return RegisterM2ConnectionMigration(runner)
}

func CreateM2GatewaySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M2 Gateway schema database is required")
	}
	if !db.Migrator().HasColumn(new(credentialRow), "RotatedFrom") {
		if err := db.Migrator().AddColumn(new(credentialRow), "RotatedFrom"); err != nil {
			return fmt.Errorf("add Harness access credential rotation column: %w", err)
		}
	}
	for _, model := range m2GatewaySchemaModels {
		if !db.Migrator().HasTable(model) {
			if err := db.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("create Harness M2 Gateway table %T: %w", model, err)
			}
		}
	}
	for _, index := range requiredM2GatewayIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			if err := db.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create Harness M2 Gateway index %s: %w", index.name, err)
			}
		}
	}
	return VerifyM2GatewaySchema(db)
}

func VerifyM2GatewaySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M2 Gateway schema database is required")
	}
	if !db.Migrator().HasColumn(new(credentialRow), "RotatedFrom") {
		return errors.New("Harness access credential rotation column is unavailable")
	}
	for _, model := range m2GatewaySchemaModels {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("Harness M2 Gateway table %T is unavailable", model)
		}
	}
	for _, index := range requiredM2GatewayIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("Harness M2 Gateway index %s is unavailable", index.name)
		}
	}
	return nil
}

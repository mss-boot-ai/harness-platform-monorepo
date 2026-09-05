package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const M2IdentityMigrationID migration.MigrationID = "20260904025000"

type hcRegistrationChallengeRow struct {
	ID            string     `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID   string     `gorm:"column:owner_user_id;size:128;not null;index:idx_harness_hc_challenge_scope_status,priority:1"`
	TenantID      string     `gorm:"column:tenant_id;size:128;not null;default:'';index:idx_harness_hc_challenge_scope_status,priority:2"`
	Origin        string     `gorm:"column:origin;size:512;not null"`
	ChallengeHash string     `gorm:"column:challenge_hash;type:char(64);not null;uniqueIndex:ux_harness_hc_challenge_hash"`
	Status        string     `gorm:"column:status;size:24;not null;index:idx_harness_hc_challenge_scope_status,priority:3;index:idx_harness_hc_challenge_expiry,priority:1"`
	ExpiresAt     time.Time  `gorm:"column:expires_at;not null;index:idx_harness_hc_challenge_expiry,priority:2"`
	ConsumedAt    *time.Time `gorm:"column:consumed_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
	RowVersion    uint64     `gorm:"column:row_version;not null;default:0"`
}

func (hcRegistrationChallengeRow) TableName() string { return "harness_hc_registration_challenges" }

type refreshCredentialRow struct {
	ID          string     `gorm:"column:id;type:char(32);primaryKey"`
	EndpointID  string     `gorm:"column:endpoint_id;type:char(32);not null;index:idx_harness_refresh_endpoint_status,priority:1"`
	FamilyID    string     `gorm:"column:family_id;type:char(32);not null;index:idx_harness_refresh_family_status,priority:1"`
	TokenHash   string     `gorm:"column:token_hash;type:char(64);not null;uniqueIndex:ux_harness_refresh_token_hash"`
	SigningJKT  string     `gorm:"column:signing_jkt;size:64;not null"`
	Status      string     `gorm:"column:status;size:24;not null;index:idx_harness_refresh_endpoint_status,priority:2;index:idx_harness_refresh_family_status,priority:2"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null;index:idx_harness_refresh_expiry"`
	RotatedFrom string     `gorm:"column:rotated_from_id;type:char(32);not null;default:''"`
	RotatedTo   string     `gorm:"column:rotated_to_id;type:char(32);not null;default:''"`
	UsedAt      *time.Time `gorm:"column:used_at"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
}

func (refreshCredentialRow) TableName() string { return "harness_refresh_credentials" }

var m2IdentitySchemaModels = []any{
	new(hcRegistrationChallengeRow),
	new(refreshCredentialRow),
}

var requiredM2IdentityIndexes = []struct {
	model any
	name  string
}{
	{new(hcRegistrationChallengeRow), "ux_harness_hc_challenge_hash"},
	{new(hcRegistrationChallengeRow), "idx_harness_hc_challenge_scope_status"},
	{new(hcRegistrationChallengeRow), "idx_harness_hc_challenge_expiry"},
	{new(refreshCredentialRow), "ux_harness_refresh_token_hash"},
	{new(refreshCredentialRow), "idx_harness_refresh_endpoint_status"},
	{new(refreshCredentialRow), "idx_harness_refresh_family_status"},
	{new(refreshCredentialRow), "idx_harness_refresh_expiry"},
}

var criticalM2IdentityUniqueIndexes = []uniqueIndexContract{
	{model: new(hcRegistrationChallengeRow), name: "ux_harness_hc_challenge_hash", columns: []string{"challenge_hash"}},
	{model: new(refreshCredentialRow), name: "ux_harness_refresh_token_hash", columns: []string{"token_hash"}},
}

func RegisterM2IdentityMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness M2 identity migration runner is required")
	}
	return runner.Register(M2IdentityMigrationID, func(db *gorm.DB, version string) error {
		if version != M2IdentityMigrationID.String() {
			return errors.New("harness M2 identity migration version mismatch")
		}
		if err := CreateM2IdentitySchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

func CreateM2IdentitySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M2 identity schema database is required")
	}
	for _, model := range m2IdentitySchemaModels {
		if !db.Migrator().HasTable(model) {
			if err := db.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("create Harness M2 identity table %T: %w", model, err)
			}
		}
	}
	for _, index := range requiredM2IdentityIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			if err := db.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create Harness M2 identity index %s: %w", index.name, err)
			}
		}
	}
	return VerifyM2IdentitySchema(db)
}

func VerifyM2IdentitySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M2 identity schema database is required")
	}
	for _, model := range m2IdentitySchemaModels {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("Harness M2 identity table %T is unavailable", model)
		}
	}
	for _, index := range requiredM2IdentityIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("Harness M2 identity index %s is unavailable", index.name)
		}
	}
	for _, contract := range criticalM2IdentityUniqueIndexes {
		if err := verifyUniqueIndexContract(db, contract); err != nil {
			return err
		}
	}
	return nil
}

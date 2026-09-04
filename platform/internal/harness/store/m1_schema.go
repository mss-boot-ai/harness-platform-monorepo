package store

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const M1PersistenceMigrationID migration.MigrationID = "20260904020000"

type keyPackageRow struct {
	ID                    string     `gorm:"column:id;type:char(32);primaryKey"`
	SessionID             string     `gorm:"column:session_id;type:char(32);not null;uniqueIndex:ux_harness_key_package_recipient,priority:1;index:idx_harness_key_package_session_status,priority:1"`
	Generation            uint64     `gorm:"column:generation;not null;uniqueIndex:ux_harness_key_package_recipient,priority:2"`
	IssuerABAEndpointID   string     `gorm:"column:issuer_aba_endpoint_id;type:char(32);not null"`
	RecipientHCEndpointID string     `gorm:"column:recipient_hc_endpoint_id;type:char(32);not null;uniqueIndex:ux_harness_key_package_recipient,priority:3"`
	CryptoSuite           uint16     `gorm:"column:crypto_suite;not null"`
	EncapsulatedKey       []byte     `gorm:"column:encapsulated_key;type:blob;not null"`
	Ciphertext            []byte     `gorm:"column:ciphertext;type:blob;not null"`
	ContextHash           string     `gorm:"column:context_hash;type:char(64);not null"`
	IssuerSignature       []byte     `gorm:"column:issuer_signature;type:blob;not null"`
	IssuerCredentialID    string     `gorm:"column:issuer_credential_id;type:char(32);not null"`
	Status                string     `gorm:"column:status;size:24;not null;index:idx_harness_key_package_session_status,priority:2"`
	ExpiresAt             time.Time  `gorm:"column:expires_at;not null"`
	AcknowledgedAt        *time.Time `gorm:"column:acknowledged_at"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
}

func (keyPackageRow) TableName() string { return "harness_session_key_packages" }

type auditRow struct {
	ID           string    `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID  string    `gorm:"column:owner_user_id;size:128;not null;index:idx_harness_audit_scope_time,priority:1"`
	TenantID     string    `gorm:"column:tenant_id;size:128;not null;default:'';index:idx_harness_audit_scope_time,priority:2"`
	ActorType    string    `gorm:"column:actor_type;size:24;not null"`
	ActorID      string    `gorm:"column:actor_id;size:128;not null"`
	Action       string    `gorm:"column:action;size:128;not null"`
	ObjectType   string    `gorm:"column:object_type;size:128;not null"`
	ObjectID     string    `gorm:"column:object_id;size:256;not null"`
	Result       string    `gorm:"column:result;size:128;not null"`
	ErrorCode    string    `gorm:"column:error_code;size:128;not null;default:''"`
	MetadataJSON []byte    `gorm:"column:metadata_json;type:blob;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;index:idx_harness_audit_scope_time,priority:3,sort:desc"`
}

func (auditRow) TableName() string { return "harness_audit_events" }

type idempotencyRow struct {
	ID           string    `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID  string    `gorm:"column:owner_user_id;size:128;not null;uniqueIndex:ux_harness_idempotency_scope,priority:1"`
	TenantID     string    `gorm:"column:tenant_id;size:128;not null;default:'';uniqueIndex:ux_harness_idempotency_scope,priority:2"`
	ActorID      string    `gorm:"column:actor_id;size:128;not null;uniqueIndex:ux_harness_idempotency_scope,priority:3"`
	Operation    string    `gorm:"column:operation;size:128;not null;uniqueIndex:ux_harness_idempotency_scope,priority:4"`
	Key          string    `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:ux_harness_idempotency_scope,priority:5"`
	RequestHash  string    `gorm:"column:request_hash;type:char(64);not null"`
	Status       string    `gorm:"column:status;size:24;not null"`
	HTTPStatus   int       `gorm:"column:http_status;not null;default:0"`
	ResponseJSON []byte    `gorm:"column:response_json;type:blob"`
	ErrorCode    string    `gorm:"column:error_code;size:128;not null;default:''"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
	ExpiresAt    time.Time `gorm:"column:expires_at;not null;index:idx_harness_idempotency_expiry"`
	RowVersion   uint64    `gorm:"column:row_version;not null;default:0"`
}

func (idempotencyRow) TableName() string { return "harness_idempotency_records" }

var m1SchemaModels = []any{
	new(keyPackageRow),
	new(auditRow),
	new(idempotencyRow),
}

var requiredM1Indexes = []struct {
	model any
	name  string
}{
	{new(keyPackageRow), "ux_harness_key_package_recipient"},
	{new(keyPackageRow), "idx_harness_key_package_session_status"},
	{new(auditRow), "idx_harness_audit_scope_time"},
	{new(idempotencyRow), "ux_harness_idempotency_scope"},
	{new(idempotencyRow), "idx_harness_idempotency_expiry"},
}

type m1UniqueIndexContract struct {
	model   any
	name    string
	columns []string
}

var criticalM1UniqueIndexes = []m1UniqueIndexContract{
	{
		model: new(keyPackageRow),
		name:  "ux_harness_key_package_recipient",
		columns: []string{
			"session_id",
			"generation",
			"recipient_hc_endpoint_id",
		},
	},
	{
		model: new(idempotencyRow),
		name:  "ux_harness_idempotency_scope",
		columns: []string{
			"owner_user_id",
			"tenant_id",
			"actor_id",
			"operation",
			"idempotency_key",
		},
	},
}

func RegisterAllMigrations(runner *migration.Migration) error {
	if err := RegisterMigrations(runner); err != nil {
		return err
	}
	if err := runner.Register(M1PersistenceMigrationID, func(db *gorm.DB, version string) error {
		if version != M1PersistenceMigrationID.String() {
			return errors.New("harness M1 persistence migration version mismatch")
		}
		if err := CreateM1Schema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	}); err != nil {
		return err
	}
	return RegisterM2IdentityMigration(runner)
}

func CreateAllSchema(db *gorm.DB) error {
	if err := CreateSchema(db); err != nil {
		return err
	}
	if err := CreateM1Schema(db); err != nil {
		return err
	}
	return CreateM2IdentitySchema(db)
}

func CreateM1Schema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M1 schema database is required")
	}
	for _, model := range m1SchemaModels {
		if !db.Migrator().HasTable(model) {
			if err := db.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("create Harness M1 table %T: %w", model, err)
			}
		}
	}
	for _, index := range requiredM1Indexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			if err := db.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create Harness M1 index %s: %w", index.name, err)
			}
		}
	}
	return VerifyM1Schema(db)
}

func VerifyAllSchema(db *gorm.DB) error {
	if err := VerifySchema(db); err != nil {
		return err
	}
	if err := VerifyM1Schema(db); err != nil {
		return err
	}
	return VerifyM2IdentitySchema(db)
}

func VerifyM1Schema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness M1 schema database is required")
	}
	for _, model := range m1SchemaModels {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("Harness M1 table %T is unavailable", model)
		}
	}
	for _, index := range requiredM1Indexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("Harness M1 index %s is unavailable", index.name)
		}
	}
	for _, contract := range criticalM1UniqueIndexes {
		if err := verifyM1UniqueIndex(db, contract); err != nil {
			return err
		}
	}
	return nil
}

func verifyM1UniqueIndex(db *gorm.DB, contract m1UniqueIndexContract) error {
	indexes, err := db.Migrator().GetIndexes(contract.model)
	if err != nil {
		return fmt.Errorf("inspect Harness M1 index %s: %w", contract.name, err)
	}
	for _, index := range indexes {
		if index.Name() != contract.name {
			continue
		}
		unique, known := index.Unique()
		if !known || !unique {
			return fmt.Errorf("Harness M1 index %s must be unique", contract.name)
		}
		columns := index.Columns()
		if !slices.Equal(columns, contract.columns) {
			return fmt.Errorf(
				"Harness M1 index %s columns = %v, want %v",
				contract.name,
				columns,
				contract.columns,
			)
		}
		return nil
	}
	return fmt.Errorf("Harness M1 index %s is unavailable", contract.name)
}

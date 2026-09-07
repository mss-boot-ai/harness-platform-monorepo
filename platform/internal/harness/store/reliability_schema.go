package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const ReliabilityMigrationID migration.MigrationID = "20260905030000"

type controlOutboxRow struct {
	ID                  string     `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID         string     `gorm:"column:owner_user_id;size:128;not null"`
	TenantID            string     `gorm:"column:tenant_id;size:128;not null;default:''"`
	SessionID           string     `gorm:"column:session_id;type:char(32);not null;index:idx_harness_control_outbox_session_status,priority:1"`
	CorrelationID       string     `gorm:"column:correlation_id;type:char(32);not null;uniqueIndex:ux_harness_control_outbox_correlation,priority:2"`
	RecipientEndpointID string     `gorm:"column:recipient_endpoint_id;type:char(32);not null;uniqueIndex:ux_harness_control_outbox_correlation,priority:3;index:idx_harness_control_outbox_recipient_status,priority:1"`
	Kind                string     `gorm:"column:kind;size:40;not null;uniqueIndex:ux_harness_control_outbox_correlation,priority:1"`
	Payload             []byte     `gorm:"column:payload"`
	Packet              []byte     `gorm:"column:packet"`
	Status              string     `gorm:"column:status;size:24;not null;index:idx_harness_control_outbox_recipient_status,priority:2;index:idx_harness_control_outbox_session_status,priority:2"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;index:idx_harness_control_outbox_recipient_status,priority:3"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null"`
	ExpiresAt           time.Time  `gorm:"column:expires_at;not null;index:idx_harness_control_outbox_expiry"`
	CompletedAt         *time.Time `gorm:"column:completed_at"`
	RowVersion          uint64     `gorm:"column:row_version;not null;default:0"`
}

func (controlOutboxRow) TableName() string { return "harness_control_outbox" }

var requiredReliabilityIndexes = []struct {
	model any
	name  string
}{
	{new(controlOutboxRow), "ux_harness_control_outbox_correlation"},
	{new(controlOutboxRow), "idx_harness_control_outbox_recipient_status"},
	{new(controlOutboxRow), "idx_harness_control_outbox_session_status"},
	{new(controlOutboxRow), "idx_harness_control_outbox_expiry"},
}

func RegisterReliabilityMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness reliability migration runner is required")
	}
	if err := runner.Register(ReliabilityMigrationID, func(db *gorm.DB, version string) error {
		if version != ReliabilityMigrationID.String() {
			return errors.New("harness reliability migration version mismatch")
		}
		if err := CreateReliabilitySchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	}); err != nil {
		return err
	}
	return RegisterPortableBinaryMigration(runner)
}

func CreateReliabilitySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness reliability schema database is required")
	}
	if !db.Migrator().HasTable(new(controlOutboxRow)) {
		if err := db.Migrator().CreateTable(new(controlOutboxRow)); err != nil {
			return fmt.Errorf("create Harness control outbox table: %w", err)
		}
	}
	for _, index := range requiredReliabilityIndexes {
		if index.name == "ux_harness_control_outbox_correlation" {
			continue
		}
		if !db.Migrator().HasIndex(index.model, index.name) {
			if err := db.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create Harness reliability index %s: %w", index.name, err)
			}
		}
	}
	for _, contract := range allSecurityUniqueIndexContracts() {
		if err := ensureUniqueIndexContract(db, contract); err != nil {
			return err
		}
	}
	return VerifyReliabilitySchema(db)
}

func VerifyReliabilitySchema(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(new(controlOutboxRow)) {
		return errors.New("Harness control outbox table is unavailable")
	}
	for _, index := range requiredReliabilityIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("Harness reliability index %s is unavailable", index.name)
		}
	}
	for _, contract := range allSecurityUniqueIndexContracts() {
		if err := verifyUniqueIndexContract(db, contract); err != nil {
			return err
		}
	}
	return nil
}

var criticalBaseUniqueIndexes = []uniqueIndexContract{
	{model: new(endpointRow), name: "ux_harness_endpoint_owner_sign", columns: []string{"owner_user_id", "signing_jkt"}},
	{model: new(endpointRow), name: "ux_harness_endpoint_owner_kem", columns: []string{"owner_user_id", "kem_jkt"}},
	{model: new(enrollmentRow), name: "ux_harness_enrollment_device_code", columns: []string{"device_code_hash"}},
	{model: new(enrollmentRow), name: "ux_harness_enrollment_user_code", columns: []string{"user_code_hash"}},
	{model: new(credentialRow), name: "ux_harness_credential_token_hash", columns: []string{"token_hash"}},
	{model: new(ticketRow), name: "ux_harness_ticket_token_hash", columns: []string{"token_hash"}},
	{model: new(frameRow), name: "ux_harness_frame_sequence", columns: []string{"session_id", "key_generation", "sender_endpoint_id", "sequence"}},
}

func allSecurityUniqueIndexContracts() []uniqueIndexContract {
	contracts := make([]uniqueIndexContract, 0, len(criticalBaseUniqueIndexes)+len(criticalM1UniqueIndexes)+len(criticalM2IdentityUniqueIndexes)+1)
	contracts = append(contracts, criticalBaseUniqueIndexes...)
	contracts = append(contracts, criticalM1UniqueIndexes...)
	contracts = append(contracts, criticalM2IdentityUniqueIndexes...)
	contracts = append(contracts, uniqueIndexContract{
		model: new(controlOutboxRow), name: "ux_harness_control_outbox_correlation",
		columns: []string{"kind", "correlation_id", "recipient_endpoint_id"},
	})
	return contracts
}

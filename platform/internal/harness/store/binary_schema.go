package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const PortableBinaryMigrationID migration.MigrationID = "20260907010000"

type binaryColumnContract struct {
	model  any
	field  string
	column string
}

var portableBinaryColumns = []binaryColumnContract{
	{model: new(endpointRow), field: "SigningPublicJWK", column: "signing_public_jwk"},
	{model: new(endpointRow), field: "KEMPublicJWK", column: "kem_public_jwk"},
	{model: new(enrollmentRow), field: "SigningPublicJWK", column: "signing_public_jwk"},
	{model: new(enrollmentRow), field: "KEMPublicJWK", column: "kem_public_jwk"},
	{model: new(frameRow), field: "AAD", column: "aad"},
	{model: new(frameRow), field: "Ciphertext", column: "ciphertext"},
	{model: new(frameRow), field: "Signature", column: "signature"},
	{model: new(keyPackageRow), field: "EncapsulatedKey", column: "encapsulated_key"},
	{model: new(keyPackageRow), field: "Ciphertext", column: "ciphertext"},
	{model: new(keyPackageRow), field: "IssuerSignature", column: "issuer_signature"},
	{model: new(auditRow), field: "MetadataJSON", column: "metadata_json"},
	{model: new(idempotencyRow), field: "ResponseJSON", column: "response_json"},
	{model: new(controlOutboxRow), field: "Payload", column: "payload"},
	{model: new(controlOutboxRow), field: "Packet", column: "packet"},
}

func RegisterPortableBinaryMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness portable binary migration runner is required")
	}
	return runner.Register(PortableBinaryMigrationID, func(db *gorm.DB, version string) error {
		if version != PortableBinaryMigrationID.String() {
			return errors.New("harness portable binary migration version mismatch")
		}
		if err := CreatePortableBinarySchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

func CreatePortableBinarySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness portable binary schema database is required")
	}
	if db.Dialector.Name() == "mysql" {
		for _, contract := range portableBinaryColumns {
			dataType, err := databaseColumnType(db, contract)
			if err != nil {
				return err
			}
			if portableBinaryType(db.Dialector.Name(), dataType) {
				continue
			}
			if err := db.Migrator().AlterColumn(contract.model, contract.field); err != nil {
				return fmt.Errorf("expand Harness binary column %s: %w", contract.column, err)
			}
		}
	}
	return VerifyPortableBinarySchema(db)
}

func VerifyPortableBinarySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness portable binary schema database is required")
	}
	dialect := db.Dialector.Name()
	if dialect != "sqlite" && dialect != "postgres" && dialect != "mysql" {
		return fmt.Errorf("Harness binary schema does not support database dialect %q", dialect)
	}
	for _, contract := range portableBinaryColumns {
		dataType, err := databaseColumnType(db, contract)
		if err != nil {
			return err
		}
		if !portableBinaryType(dialect, dataType) {
			return fmt.Errorf(
				"Harness binary column %s has unsafe %s type %q",
				contract.column,
				dialect,
				dataType,
			)
		}
	}
	return nil
}

func databaseColumnType(db *gorm.DB, contract binaryColumnContract) (string, error) {
	columns, err := db.Migrator().ColumnTypes(contract.model)
	if err != nil {
		return "", fmt.Errorf("inspect Harness binary column %s: %w", contract.column, err)
	}
	for _, column := range columns {
		if column.Name() == contract.column {
			return strings.ToUpper(strings.TrimSpace(column.DatabaseTypeName())), nil
		}
	}
	return "", fmt.Errorf("Harness binary column %s is unavailable", contract.column)
}

func portableBinaryType(dialect, dataType string) bool {
	switch dialect {
	case "sqlite":
		return dataType == "BLOB"
	case "postgres":
		return dataType == "BYTEA"
	case "mysql":
		return dataType == "MEDIUMBLOB" || dataType == "LONGBLOB"
	default:
		return false
	}
}

package store

import (
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBinaryFieldsUsePostgresBytea(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(
		postgres.New(postgres.Config{DSN: "postgres://harness.invalid/harness", PreferSimpleProtocol: true}),
		&gorm.Config{DisableAutomaticPing: true},
	)
	if err != nil {
		t.Fatalf("open PostgreSQL schema compiler: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("access PostgreSQL schema compiler pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	tests := []struct {
		model any
		field string
	}{
		{model: new(endpointRow), field: "SigningPublicJWK"},
		{model: new(frameRow), field: "Ciphertext"},
		{model: new(keyPackageRow), field: "EncapsulatedKey"},
		{model: new(controlOutboxRow), field: "Packet"},
	}
	for _, test := range tests {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(test.model); err != nil {
			t.Fatalf("parse %T: %v", test.model, err)
		}
		field := statement.Schema.LookUpField(test.field)
		if field == nil {
			t.Fatalf("field %T.%s is unavailable", test.model, test.field)
		}
		dataType := strings.ToLower(db.Migrator().FullDataTypeOf(field).SQL)
		if !strings.HasPrefix(dataType, "bytea") {
			t.Fatalf("PostgreSQL type for %T.%s = %q, want bytea", test.model, test.field, dataType)
		}
	}
}

func TestBinaryFieldsUseMySQLLongBlob(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(
		mysql.New(mysql.Config{
			DSN:                       "harness:harness@tcp(127.0.0.1:3306)/harness?charset=utf8mb4&parseTime=true",
			SkipInitializeWithVersion: true,
		}),
		&gorm.Config{DisableAutomaticPing: true},
	)
	if err != nil {
		t.Fatalf("open MySQL schema compiler: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("access MySQL schema compiler pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(new(frameRow)); err != nil {
		t.Fatalf("parse frameRow: %v", err)
	}
	field := statement.Schema.LookUpField("Ciphertext")
	if field == nil {
		t.Fatal("frameRow.Ciphertext is unavailable")
	}
	dataType := strings.ToLower(db.Migrator().FullDataTypeOf(field).SQL)
	if !strings.HasPrefix(dataType, "longblob") {
		t.Fatalf("MySQL frame ciphertext type = %q, want longblob", dataType)
	}
}

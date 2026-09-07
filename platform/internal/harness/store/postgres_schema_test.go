package store

import (
	"strings"
	"testing"

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

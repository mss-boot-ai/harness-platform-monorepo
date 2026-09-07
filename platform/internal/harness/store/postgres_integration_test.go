package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresSchemaMigrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HARNESS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("HARNESS_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	closeGORMDatabase(t, admin)
	if err := admin.Exec("CREATE EXTENSION IF NOT EXISTS timescaledb").Error; err != nil {
		t.Fatalf("create TimescaleDB extension: %v", err)
	}
	var extensionVersion string
	if err := admin.Raw("SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'").Scan(&extensionVersion).Error; err != nil || extensionVersion == "" {
		t.Fatalf("verify TimescaleDB extension: version=%q err=%v", extensionVersion, err)
	}

	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		t.Fatalf("generate test schema suffix: %v", err)
	}
	schema := "harness_test_" + hex.EncodeToString(randomSuffix)
	quotedSchema := `"` + schema + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE").Error; err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
	})

	scopedDSN, err := postgresDSNWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("scope PostgreSQL DSN: %v", err)
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open scoped PostgreSQL integration database: %v", err)
	}
	closeGORMDatabase(t, db)
	if err := CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema on TimescaleDB: %v", err)
	}
	if err := VerifyAllSchema(db); err != nil {
		t.Fatalf("VerifyAllSchema on TimescaleDB: %v", err)
	}
}

func postgresDSNWithSearchPath(dsn, schema string) (string, error) {
	if !safeSQLIdentifier(schema) {
		return "", fmt.Errorf("invalid PostgreSQL schema %q", schema)
	}
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	if strings.ContainsAny(dsn, "\r\n") {
		return "", fmt.Errorf("PostgreSQL DSN contains a line break")
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema, nil
}

func closeGORMDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("access SQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQL database: %v", err)
		}
	})
}

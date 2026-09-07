package store

import (
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"
)

type uniqueIndexContract struct {
	model   any
	name    string
	columns []string
}

func verifyUniqueIndexContract(db *gorm.DB, contract uniqueIndexContract) error {
	if db == nil || contract.model == nil || contract.name == "" || len(contract.columns) == 0 {
		return fmt.Errorf("invalid unique index contract %q", contract.name)
	}
	indexes, err := db.Migrator().GetIndexes(contract.model)
	if err != nil {
		return fmt.Errorf("inspect Harness index %s: %w", contract.name, err)
	}
	for _, index := range indexes {
		if index.Name() != contract.name {
			continue
		}
		unique, known := index.Unique()
		if !known || !unique {
			return fmt.Errorf("Harness index %s must be unique", contract.name)
		}
		columns := index.Columns()
		if !slices.Equal(columns, contract.columns) {
			return fmt.Errorf("Harness index %s columns = %v, want %v", contract.name, columns, contract.columns)
		}
		return nil
	}
	return fmt.Errorf("Harness index %s is unavailable", contract.name)
}

func ensureUniqueIndexContract(db *gorm.DB, contract uniqueIndexContract) error {
	table, err := modelTableName(db, contract.model)
	if err != nil {
		return err
	}
	quotedName, err := quoteSQLIdentifier(db, contract.name)
	if err != nil {
		return fmt.Errorf("invalid Harness unique index contract %q: %w", contract.name, err)
	}
	quotedTable, err := quoteSQLIdentifier(db, table)
	if err != nil {
		return fmt.Errorf("invalid Harness index table %q: %w", table, err)
	}
	if err := verifyUniqueIndexContract(db, contract); err == nil {
		return nil
	}
	if db.Migrator().HasIndex(contract.model, contract.name) {
		var statement string
		switch db.Dialector.Name() {
		case "postgres", "sqlite":
			statement = fmt.Sprintf("DROP INDEX IF EXISTS %s", quotedName)
		case "mysql":
			statement = fmt.Sprintf("DROP INDEX %s ON %s", quotedName, quotedTable)
		default:
			return fmt.Errorf("drop malformed Harness index %s: unsupported database dialect %q", contract.name, db.Dialector.Name())
		}
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("drop malformed Harness index %s: %w", contract.name, err)
		}
	}
	quoted := make([]string, 0, len(contract.columns))
	for _, column := range contract.columns {
		quotedColumn, err := quoteSQLIdentifier(db, column)
		if err != nil {
			return fmt.Errorf("invalid Harness index column %q: %w", column, err)
		}
		quoted = append(quoted, quotedColumn)
	}
	statement := fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s(%s)", quotedName, quotedTable, strings.Join(quoted, ","))
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("create Harness unique index %s: %w", contract.name, err)
	}
	return verifyUniqueIndexContract(db, contract)
}

func quoteSQLIdentifier(db *gorm.DB, value string) (string, error) {
	if db == nil || !safeSQLIdentifier(value) {
		return "", fmt.Errorf("unsafe SQL identifier")
	}
	switch db.Dialector.Name() {
	case "mysql":
		return "`" + value + "`", nil
	case "postgres", "sqlite":
		return `"` + value + `"`, nil
	default:
		return "", fmt.Errorf("unsupported database dialect %q", db.Dialector.Name())
	}
}

func modelTableName(db *gorm.DB, model any) (string, error) {
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return "", fmt.Errorf("parse Harness index model: %w", err)
	}
	if statement.Schema == nil || statement.Schema.Table == "" {
		return "", fmt.Errorf("Harness index model table is unavailable")
	}
	return statement.Schema.Table, nil
}

func safeSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

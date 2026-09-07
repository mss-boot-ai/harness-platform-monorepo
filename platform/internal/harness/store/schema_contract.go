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
	if db.Dialector.Name() == "postgres" {
		return verifyPostgresUniqueIndexContract(db, contract)
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

func verifyPostgresUniqueIndexContract(db *gorm.DB, contract uniqueIndexContract) error {
	table, err := modelTableName(db, contract.model)
	if err != nil {
		return err
	}
	if !safeSQLIdentifier(contract.name) || !safeSQLIdentifier(table) {
		return fmt.Errorf("invalid Harness unique index contract %q", contract.name)
	}
	type indexColumn struct {
		ColumnName string `gorm:"column:column_name"`
		Unique     bool   `gorm:"column:is_unique"`
		Valid      bool   `gorm:"column:is_valid"`
		Ready      bool   `gorm:"column:is_ready"`
	}
	var rows []indexColumn
	if err := db.Raw(`
SELECT attribute.attname AS column_name,
       pg_idx.indisunique AS is_unique,
       pg_idx.indisvalid AS is_valid,
       pg_idx.indisready AS is_ready
FROM pg_index AS pg_idx
JOIN pg_class AS relation ON relation.oid = pg_idx.indrelid
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
JOIN pg_class AS index_relation ON index_relation.oid = pg_idx.indexrelid
JOIN LATERAL unnest(pg_idx.indkey) WITH ORDINALITY AS index_key(attnum, ordinal) ON true
JOIN pg_attribute AS attribute
  ON attribute.attrelid = relation.oid
 AND attribute.attnum = index_key.attnum
WHERE namespace.nspname = CURRENT_SCHEMA()
  AND relation.relname = ?
  AND index_relation.relname = ?
  AND pg_idx.indpred IS NULL
  AND pg_idx.indexprs IS NULL
  AND index_key.ordinal <= pg_idx.indnkeyatts
ORDER BY index_key.ordinal`, table, contract.name).Scan(&rows).Error; err != nil {
		return fmt.Errorf("inspect Harness index %s: %w", contract.name, err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("Harness index %s is unavailable", contract.name)
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		if !row.Unique || !row.Valid || !row.Ready {
			return fmt.Errorf("Harness index %s must be unique, valid, and ready", contract.name)
		}
		columns = append(columns, row.ColumnName)
	}
	if !slices.Equal(columns, contract.columns) {
		return fmt.Errorf("Harness index %s columns = %v, want %v", contract.name, columns, contract.columns)
	}
	return nil
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
	quoted := make([]string, 0, len(contract.columns))
	for _, column := range contract.columns {
		quotedColumn, err := quoteSQLIdentifier(db, column)
		if err != nil {
			return fmt.Errorf("invalid Harness index column %q: %w", column, err)
		}
		quoted = append(quoted, quotedColumn)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var duplicateGroups int64
		duplicateStatement := fmt.Sprintf(
			"SELECT COUNT(*) FROM (SELECT 1 FROM %s GROUP BY %s HAVING COUNT(*) > 1 LIMIT 1) AS harness_duplicate_groups",
			quotedTable,
			strings.Join(quoted, ","),
		)
		if err := tx.Raw(duplicateStatement).Scan(&duplicateGroups).Error; err != nil {
			return fmt.Errorf("inspect Harness index %s duplicates: %w", contract.name, err)
		}
		if duplicateGroups != 0 {
			return fmt.Errorf("repair Harness index %s: duplicate rows violate the required unique contract", contract.name)
		}
		if tx.Migrator().HasIndex(contract.model, contract.name) {
			var dropStatement string
			switch tx.Dialector.Name() {
			case "postgres", "sqlite":
				dropStatement = fmt.Sprintf("DROP INDEX IF EXISTS %s", quotedName)
			case "mysql":
				dropStatement = fmt.Sprintf("DROP INDEX %s ON %s", quotedName, quotedTable)
			default:
				return fmt.Errorf("drop malformed Harness index %s: unsupported database dialect %q", contract.name, tx.Dialector.Name())
			}
			if err := tx.Exec(dropStatement).Error; err != nil {
				return fmt.Errorf("drop malformed Harness index %s: %w", contract.name, err)
			}
		}
		createStatement := fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s(%s)", quotedName, quotedTable, strings.Join(quoted, ","))
		if err := tx.Exec(createStatement).Error; err != nil {
			return fmt.Errorf("create Harness unique index %s: %w", contract.name, err)
		}
		return verifyUniqueIndexContract(tx, contract)
	})
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

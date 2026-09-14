package store

import (
	"errors"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
)

const SessionStartupMigrationID migration.MigrationID = "20260915010000"

func RegisterSessionStartupMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("session startup migration runner is required")
	}
	return runner.Register(SessionStartupMigrationID, func(db *gorm.DB, version string) error {
		if version != SessionStartupMigrationID.String() {
			return errors.New("session startup migration version mismatch")
		}
		if err := CreateSessionStartupSchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

func CreateSessionStartupSchema(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(new(sessionRow)) {
		return errors.New("session table is required")
	}
	if !db.Migrator().HasColumn(new(sessionRow), "StartupFailureCode") {
		if err := db.Migrator().AddColumn(new(sessionRow), "StartupFailureCode"); err != nil {
			return err
		}
	}
	return VerifySessionStartupSchema(db)
}

func VerifySessionStartupSchema(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasColumn(new(sessionRow), "StartupFailureCode") {
		return errors.New("session startup reason schema is unavailable")
	}
	return nil
}

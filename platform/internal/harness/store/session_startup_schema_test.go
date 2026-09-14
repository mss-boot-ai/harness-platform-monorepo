package store

import "testing"

func TestStartupReasonMigrationAddsMissingColumnAndIsRepeatable(t *testing.T) {
	persistence := newTestStore(t)
	if err := persistence.db.Migrator().DropColumn(new(sessionRow), "startup_failure_code"); err != nil {
		t.Fatal(err)
	}
	if err := VerifySessionStartupSchema(persistence.db); err == nil {
		t.Fatal("missing column was accepted")
	}
	for i := 0; i < 2; i++ {
		if err := CreateSessionStartupSchema(persistence.db); err != nil {
			t.Fatal(err)
		}
	}
}

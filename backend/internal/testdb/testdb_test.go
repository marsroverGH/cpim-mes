package testdb

import (
	"testing"
)

func TestNewGivesIsolatedMigratedDatabases(t *testing.T) {
	a := New(t)
	b := New(t)

	var latest int
	if err := a.Get(&latest, `SELECT max(version) FROM schema_migrations`); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if latest != 42 {
		t.Fatalf("latest migration = %d, want 42", latest)
	}

	var nameA, nameB string
	if err := a.Get(&nameA, `SELECT current_database()`); err != nil {
		t.Fatal(err)
	}
	if err := b.Get(&nameB, `SELECT current_database()`); err != nil {
		t.Fatal(err)
	}
	if nameA == nameB {
		t.Fatalf("databases are not isolated: both %s", nameA)
	}
}

package migration

import (
	"strings"
	"testing"
	"time"
)

func TestEmbeddedMigrationsAreSequentialAndComplete(t *testing.T) {
	migs, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 39 {
		t.Fatalf("expected 39 migrations, got %d", len(migs))
	}
	for i, m := range migs {
		want := i + 1
		if m.Version != want {
			t.Fatalf("migration[%d] version=%d want=%d", i, m.Version, want)
		}
		if len(m.Checksum) != 64 {
			t.Fatalf("migration %04d checksum length=%d", m.Version, len(m.Checksum))
		}
	}
}

func TestTopLevelTransactionWrapperNormalization(t *testing.T) {
	input := "BEGIN;\nCREATE TABLE x(id int);\nDO $$\nBEGIN\n  NULL;\nEND$$;\nCOMMIT;\n"
	got := strings.TrimSpace(topLevelTxnRE.ReplaceAllString(input, ""))
	if strings.Contains(got, "BEGIN;") || strings.Contains(got, "COMMIT;") {
		t.Fatalf("top-level wrappers were not stripped: %q", got)
	}
	if !strings.Contains(got, "DO $$\nBEGIN\n") {
		t.Fatalf("PL/pgSQL BEGIN was incorrectly stripped: %q", got)
	}
}

func TestValidateHistoryRejectsChecksumMutation(t *testing.T) {
	migs, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	err = validateHistory(migs, []AppliedMigration{{
		Version: 1, Name: migs[0].Name, Checksum: strings.Repeat("0", 64), Applied: time.Now(),
	}})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestValidateHistoryRejectsUnsafeGap(t *testing.T) {
	migs, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	rows := []AppliedMigration{
		{Version: 1, Name: migs[0].Name, Checksum: migs[0].Checksum},
		{Version: 3, Name: migs[2].Name, Checksum: migs[2].Checksum},
	}
	if err := validateHistory(migs, rows); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("expected unsafe gap rejection, got %v", err)
	}
}

func TestValidateHistoryAllowsReplaySafe0014Gap(t *testing.T) {
	migs, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]AppliedMigration, 0, 14)
	for _, m := range migs[:15] {
		if m.Version == 14 {
			continue
		}
		rows = append(rows, AppliedMigration{Version: m.Version, Name: m.Name, Checksum: m.Checksum})
	}
	if err := validateHistory(migs, rows); err != nil {
		t.Fatalf("0014 is replay-safe and may be backfilled: %v", err)
	}
}

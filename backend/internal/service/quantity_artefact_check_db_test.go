package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

type floatArtefactRow struct {
	Table    string `db:"table_name"`
	Column   string `db:"column_name"`
	Rows     int64  `db:"rows_over_9_decimals"`
	MaxScale int    `db:"max_scale"`
	Example  string `db:"example"`
}

// The operator-facing check in scripts/sql must report values left over from
// releases that stored float64 quantities, and must stay silent on data that
// the current code writes.
func TestQuantityFloatArtefactCheckScript(t *testing.T) {
	t.Parallel()
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "sql", "check_quantity_float_artefacts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	f := newDBFixture(t)
	run := func() []floatArtefactRow {
		t.Helper()
		var rows []floatArtefactRow
		tx, err := f.DB.BeginTxx(f.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(f.Ctx, `SET TRANSACTION READ ONLY`); err != nil {
			t.Fatal(err)
		}
		if err := tx.SelectContext(f.Ctx, &rows, string(script)); err != nil {
			t.Fatalf("check script: %v", err)
		}
		return rows
	}

	item := opsItem(t, f, opsCode("ART"), domain.ItemTypeRawMaterial)
	if rows := run(); len(rows) != 0 {
		t.Fatalf("clean database reported artefacts: %+v", rows)
	}

	// A value as v0.42.0 could have stored it (0.1 + 0.2 in float64), and one
	// at exactly 9 decimal places, which is the precision the code now keeps.
	// Since 0043 the main quantity columns are numeric(20,9) and cannot hold
	// artefacts, so use columns that are still unconstrained.
	if _, err := f.DB.ExecContext(f.Ctx, `UPDATE items SET standard_cost=0.30000000000000004, ordering_cost=0.123456789 WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	rows := run()
	if len(rows) != 1 {
		t.Fatalf("want exactly items.standard_cost, got %+v", rows)
	}
	got := rows[0]
	if got.Table != "items" || got.Column != "standard_cost" || got.Rows != 1 || got.MaxScale != 17 || got.Example != "0.30000000000000004" {
		t.Fatalf("unexpected report %+v", got)
	}
}

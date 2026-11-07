package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// 0043 must round float artefacts left by releases up to v0.42.0 in a
// database that is still at 0042, keep the ledger reconciled, and recreate the
// views and triggers it has to drop for the type change.
func TestMigration0043RoundsQuantityArtefacts(t *testing.T) {
	adminURL := os.Getenv("CPIM_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("CPIM_TEST_DATABASE_URL is not set; skipping database integration test")
	}
	ctx := context.Background()
	db := createEmptyDatabase(t, adminURL)
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	migs, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var m0043 *Migration
	for i := range migs {
		if migs[i].Version == 43 {
			m0043 = &migs[i]
		}
	}
	if m0043 == nil {
		t.Fatal("migration 0043 is not embedded")
	}
	if _, err := ensureMigrationTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.Version >= 43 {
			break
		}
		if err := applyOne(ctx, conn, m, "test", time.Now()); err != nil {
			t.Fatalf("apply %04d: %v", m.Version, err)
		}
	}

	// Data as v0.42.0 could have written it: 0.1+0.2 and 3 x 0.1 x 1.1 in float64.
	const artefact = "0.30000000000000004"
	seed := fmt.Sprintf(`
BEGIN;
INSERT INTO items (id, code, name, type, safety_stock, lot_size)
VALUES ('00000000-0000-0000-0000-0000000000a1', 'M43-FG', 'fg', 'FG', %[1]s, 0.1),
       ('00000000-0000-0000-0000-0000000000a2', 'M43-RM', 'rm', 'RM', 0, 1);
INSERT INTO bom_components (parent_id, child_id, quantity)
VALUES ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a2', 0.33000000000000007);
INSERT INTO lots (id, item_id, lot_no, quantity)
VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a2', 'M43-LOT', %[1]s);
INSERT INTO inventory_txns (id, item_id, quantity, txn_type, ref_doc)
VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a2', %[1]s, 'RECEIPT', 'M43');
INSERT INTO lot_movements (lot_id, txn_id, quantity, movement_type, ref_doc)
VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000c1', %[1]s, 'RECEIPT', 'M43');
COMMIT;`, artefact)
	if _, err := conn.ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed 0042 data: %v", err)
	}

	checkScript, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "sql", "check_quantity_float_artefacts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	artefactColumns := func() int {
		t.Helper()
		var rows []struct {
			Table  string `db:"table_name"`
			Column string `db:"column_name"`
			N      int64  `db:"rows_over_9_decimals"`
			Scale  int    `db:"max_scale"`
			Ex     string `db:"example"`
		}
		if err := sqlx.SelectContext(ctx, conn, &rows, string(checkScript)); err != nil {
			t.Fatalf("check script: %v", err)
		}
		return len(rows)
	}
	if n := artefactColumns(); n != 5 {
		t.Fatalf("before 0043 the check script reported %d columns, want 5", n)
	}

	snapshot := func() (views int, comment bool, trigger string) {
		t.Helper()
		if err := conn.GetContext(ctx, &views, `SELECT count(*) FROM pg_views WHERE schemaname='public'`); err != nil {
			t.Fatal(err)
		}
		if err := conn.GetContext(ctx, &comment, `SELECT obj_description('v_sales_order_open_demand'::regclass,'pg_class') IS NOT NULL`); err != nil {
			t.Fatal(err)
		}
		if err := conn.GetContext(ctx, &trigger, `SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='mps_forecast_provenance_guard_trg'`); err != nil {
			t.Fatal(err)
		}
		return
	}
	viewsBefore, commentBefore, triggerBefore := snapshot()

	if err := applyOne(ctx, conn, *m0043, "test", time.Now()); err != nil {
		t.Fatalf("apply 0043: %v", err)
	}

	viewsAfter, commentAfter, triggerAfter := snapshot()
	if viewsAfter != viewsBefore || commentAfter != commentBefore || triggerAfter != triggerBefore {
		t.Fatalf("views %d->%d, comment %v->%v, trigger\n%s\n->\n%s", viewsBefore, viewsAfter, commentBefore, commentAfter, triggerBefore, triggerAfter)
	}

	var scaled int
	if err := conn.GetContext(ctx, &scaled, `
SELECT count(*) FROM information_schema.columns c
  JOIN information_schema.tables t ON t.table_schema=c.table_schema AND t.table_name=c.table_name
 WHERE c.table_schema='public' AND t.table_type='BASE TABLE'
   AND c.data_type='numeric' AND c.numeric_precision=20 AND c.numeric_scale=9`); err != nil {
		t.Fatal(err)
	}
	if scaled != 25 {
		t.Fatalf("%d columns are numeric(20,9), want 25", scaled)
	}

	for _, q := range []struct{ query, want string }{
		{`SELECT safety_stock::text FROM items WHERE code='M43-FG'`, "0.300000000"},
		{`SELECT quantity::text FROM bom_components WHERE parent_id='00000000-0000-0000-0000-0000000000a1'`, "0.330000000"},
		{`SELECT quantity::text FROM lots WHERE lot_no='M43-LOT'`, "0.300000000"},
		{`SELECT quantity::text FROM inventory_txns WHERE ref_doc='M43'`, "0.300000000"},
		{`SELECT quantity::text FROM lot_movements WHERE ref_doc='M43'`, "0.300000000"},
		{`SELECT on_hand::text FROM v_stock_balance WHERE code='M43-RM'`, "0.300000000"},
		{`SELECT difference::text FROM v_inventory_lot_reconciliation WHERE code='M43-RM'`, "0.000000000"},
	} {
		var got string
		if err := conn.GetContext(ctx, &got, q.query); err != nil {
			t.Fatalf("%s: %v", q.query, err)
		}
		if got != q.want {
			t.Fatalf("%s = %s, want %s", q.query, got, q.want)
		}
	}
	if n := artefactColumns(); n != 0 {
		t.Fatalf("after 0043 the check script still reports %d columns", n)
	}

	// New writes are rounded by the column type itself.
	if _, err := conn.ExecContext(ctx, `UPDATE items SET lot_size=0.1+0.2::float8 WHERE code='M43-RM'`); err != nil {
		t.Fatal(err)
	}
	var lot string
	if err := conn.GetContext(ctx, &lot, `SELECT lot_size::text FROM items WHERE code='M43-RM'`); err != nil {
		t.Fatal(err)
	}
	if lot != "0.300000000" {
		t.Fatalf("lot_size stored as %s", lot)
	}
}

func createEmptyDatabase(t *testing.T, adminURL string) *sqlx.DB {
	t.Helper()
	admin, err := sqlx.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	name := "cpim_m43_" + hex.EncodeToString(b)
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sqlx.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, err := sqlx.Open("pgx", adminURL)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	return db
}

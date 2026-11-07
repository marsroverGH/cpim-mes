package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// Regression: quantities computed in Go (WO quantity x BOM quantity x scrap,
// FIFO remainders) used to be written to the unconstrained numeric ledger
// columns with binary float artefacts such as 0.30000000000000004. They must be
// stored as the decimal values a person would write, and the ledger must still
// reconcile exactly after release, FIFO splits and repeated partial completions.
func TestOpsLedgerQuantitiesHaveNoFloatArtefacts(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	rm := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	opsBOM(t, f, fg.ID, rm.ID, 0.1, 0.1) // 0.11 per unit
	opsRouting(t, f, fg.ID, 1)
	lotA := opsStock(t, f, rm.ID, 0.2, opsCode("LOT-A")) // FIFO: lot A first
	lotB := opsStock(t, f, rm.ID, 0.6, opsCode("LOT-B"))
	actor := opsSFActor(t, f, domain.RoleOperator)

	wo := opsWO(t, f, fg.ID, 3)
	if _, err := f.Svc.Workflow.ReleaseWorkOrder(f.Ctx, wo.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	var reserve string
	if err := f.DB.GetContext(f.Ctx, &reserve, `SELECT quantity::text FROM inventory_txns WHERE item_id=$1 AND txn_type='RESERVE'`, rm.ID); err != nil {
		t.Fatal(err)
	}
	if reserve != "0.33" { // 3 x 0.1 x 1.1 = 0.33000000000000007 in float64
		t.Fatalf("WO reservation stored as %s, want 0.33", reserve)
	}

	ops := opsWOOps(t, f, wo.ID)
	opsRunOp(t, f, ops[len(ops)-1].ID, 3, actor)
	for i := 0; i < 3; i++ {
		if _, err := f.Svc.Workflow.CompleteWorkOrder(f.Ctx, wo.ID, uuid.New(), opsPtr(1.0), ""); err != nil {
			t.Fatalf("partial completion %d: %v", i+1, err)
		}
	}

	// The second completion needs 0.11 but lot A has 0.09 left: FIFO splits it.
	var issues []string
	if err := f.DB.SelectContext(f.Ctx, &issues, `
SELECT lm.quantity::text FROM lot_movements lm JOIN inventory_txns t ON t.id=lm.txn_id
 WHERE t.item_id=$1 AND t.txn_type='ISSUE' ORDER BY t.occurred_at, lm.lot_id=$2 DESC`, rm.ID, lotA); err != nil {
		t.Fatal(err)
	}
	want := []string{"-0.11", "-0.09", "-0.02", "-0.11"}
	if len(issues) != len(want) {
		t.Fatalf("issue movements %v, want %v", issues, want)
	}
	for i := range want {
		if issues[i] != want[i] {
			t.Fatalf("issue movements %v, want %v", issues, want)
		}
	}

	var balA, balB, reserved string
	_ = f.DB.GetContext(f.Ctx, &balA, `SELECT COALESCE(SUM(quantity),0)::text FROM lot_movements WHERE lot_id=$1`, lotA)
	_ = f.DB.GetContext(f.Ctx, &balB, `SELECT COALESCE(SUM(quantity),0)::text FROM lot_movements WHERE lot_id=$1`, lotB)
	_ = f.DB.GetContext(f.Ctx, &reserved, `SELECT reserved::text FROM v_stock_balance WHERE item_id=$1`, rm.ID)
	if balA != "0.00" && balA != "0" {
		t.Fatalf("lot A balance %s, want exactly 0", balA)
	}
	if balB != "0.47" {
		t.Fatalf("lot B balance %s, want 0.47", balB)
	}
	if reserved != "0.00" && reserved != "0" {
		t.Fatalf("reservation left %s after completing the WO, want exactly 0", reserved)
	}

	// No ledger row may carry more than QuantityScale decimal places.
	if n := opsCount(t, f, `
SELECT count(*) FROM (
  SELECT quantity FROM inventory_txns WHERE item_id=$1
  UNION ALL SELECT lm.quantity FROM lot_movements lm JOIN lots l ON l.id=lm.lot_id WHERE l.item_id=$1
) q WHERE length(split_part(abs(quantity)::text, '.', 2)) > 9`, rm.ID); n != 0 {
		t.Fatalf("%d ledger rows carry float artefacts", n)
	}
	opsAssertLedgerReconciled(t, f)
}

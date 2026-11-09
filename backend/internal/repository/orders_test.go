package repository_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestWorkOrderLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "FG")
	wos := f.repos.WorkOrders

	wo := &domain.WorkOrder{OrderNo: uniq("WO"), ItemID: it.ID, Quantity: 10,
		StartDate: date(2026, 11, 1), DueDate: date(2026, 11, 10)}
	if err := wos.Create(f.ctx, wo); err != nil {
		t.Fatal(err)
	}
	if wo.Status != "PLANNED" || wo.ID == uuid.Nil {
		t.Fatalf("defaults: %+v", wo)
	}
	if err := wos.Create(f.ctx, &domain.WorkOrder{OrderNo: wo.OrderNo, ItemID: it.ID, Quantity: 1, StartDate: wo.StartDate, DueDate: wo.DueDate}); err == nil {
		t.Fatal("duplicate order_no must be rejected")
	}
	if err := wos.Create(f.ctx, &domain.WorkOrder{OrderNo: uniq("WO"), ItemID: it.ID, Quantity: 0, StartDate: wo.StartDate, DueDate: wo.DueDate}); err == nil {
		t.Fatal("zero quantity must be rejected")
	}

	got, _ := wos.Get(f.ctx, wo.ID)
	if got.Status != "PLANNED" || got.ReleasedAt != nil {
		t.Fatalf("created WO: %+v", got)
	}

	if err := wos.UpdateStatus(f.ctx, wo.ID, "RELEASED"); err != nil {
		t.Fatal(err)
	}
	got, _ = wos.Get(f.ctx, wo.ID)
	if got.Status != "RELEASED" || got.ReleasedAt == nil || got.CompletedAt != nil {
		t.Fatalf("release: %+v", got)
	}

	if err := wos.UpdateStatus(f.ctx, wo.ID, "COMPLETED"); err != nil {
		t.Fatal(err)
	}
	if got, _ = wos.Get(f.ctx, wo.ID); got.Status != "COMPLETED" || got.CompletedAt == nil {
		t.Fatalf("complete: %+v", got)
	}
	if err := wos.UpdateStatus(f.ctx, wo.ID, "CLOSED"); err != nil {
		t.Fatal(err)
	}
	if err := wos.UpdateStatus(f.ctx, wo.ID, "BOGUS"); err == nil {
		t.Fatal("unknown status must violate CHECK")
	}

	lot, _ := f.receiveLot(t, it.ID, 10)
	if err := wos.SetProducedLot(f.ctx, wo.ID, lot); err != nil {
		t.Fatal(err)
	}
	if got, _ = wos.Get(f.ctx, wo.ID); got.ProducedLotID == nil || *got.ProducedLotID != lot {
		t.Fatalf("produced lot not set: %+v", got.ProducedLotID)
	}

	list, err := wos.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, w := range list {
		if i > 0 && list[i-1].DueDate.After(w.DueDate) {
			t.Fatal("List must be ordered by due date")
		}
		found = found || w.ID == wo.ID
	}
	if !found {
		t.Fatal("work order missing from List")
	}
	if _, err := wos.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}
}

// TestWorkOrderUpdateProgress checks the semantics of
// WorkOrderRepo.UpdateProgress (regression: the query used to fail with
// SQLSTATE 42P08 on every call).
func TestWorkOrderUpdateProgress(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "FG")
	wos := f.repos.WorkOrders
	wo := &domain.WorkOrder{OrderNo: uniq("WO"), ItemID: it.ID, Quantity: 10,
		StartDate: date(2026, 11, 1), DueDate: date(2026, 11, 10)}
	if err := wos.Create(f.ctx, wo); err != nil {
		t.Fatal(err)
	}

	if err := wos.UpdateProgress(f.ctx, wo.ID, 3); err != nil {
		t.Fatal(err)
	}
	// Progress on a PLANNED order does not change status.
	got, _ := wos.Get(f.ctx, wo.ID)
	if got.Status != "PLANNED" || !approxEqual(got.ReportedProgressQty, 3) || got.CompletedQty != 0 {
		t.Fatalf("progress on planned WO: %+v", got)
	}
	if err := wos.UpdateStatus(f.ctx, wo.ID, "RELEASED"); err != nil {
		t.Fatal(err)
	}
	// Zero progress keeps RELEASED; positive progress moves to IN_PROGRESS and
	// never touches completed_qty (inventory-backed completion is separate).
	if err := wos.UpdateProgress(f.ctx, wo.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ = wos.Get(f.ctx, wo.ID); got.Status != "RELEASED" {
		t.Fatalf("zero progress changed status to %s", got.Status)
	}
	if err := wos.UpdateProgress(f.ctx, wo.ID, 4.5); err != nil {
		t.Fatal(err)
	}
	if got, _ = wos.Get(f.ctx, wo.ID); got.Status != "IN_PROGRESS" || got.CompletedQty != 0 || !approxEqual(got.ReportedProgressQty, 4.5) {
		t.Fatalf("progress: %+v", got)
	}
	if err := wos.UpdateProgress(f.ctx, wo.ID, -1); err == nil {
		t.Fatal("negative progress must violate CHECK")
	}
}

func TestPurchaseOrderStateInvariants(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	pos := f.repos.Purchases

	po := &domain.PurchaseOrder{PONo: uniq("PO"), ItemID: it.ID, Supplier: "ACME", Quantity: 10, DueDate: date(2026, 12, 1)}
	if err := pos.Create(f.ctx, po); err != nil {
		t.Fatal(err)
	}
	if po.Status != "OPEN" || po.OrderDate.IsZero() {
		t.Fatalf("defaults: %+v", po)
	}
	got, err := pos.Get(f.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(got.RemainingQty, 10) || got.ReceivedQty != 0 || got.SupplierQualityStatus != "APPROVED" ||
		got.ScheduleStatus != "UNCONFIRMED" || got.ExpectedDeliveryDate == nil {
		t.Fatalf("planning view: %+v", got)
	}

	// RECEIVED without receipt history violates the deferred PO state invariant.
	wantErrContains(t, pos.UpdateStatus(f.ctx, po.ID, "RECEIVED"), "must be OPEN")
	if g, _ := pos.Get(f.ctx, po.ID); g.Status != "OPEN" || g.ReceivedAt != nil {
		t.Fatalf("rejected status change leaked: %+v", g)
	}

	// Record a partial receipt exactly as the workflow would.
	planner := f.user(t, domain.RolePlanner)
	rid, txn, lot := uuid.New(), uuid.New(), uuid.New()
	ref := "PO:" + po.PONo + ":RCPT:" + rid.String()
	tx := f.db.MustBegin()
	tx.MustExec(`INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc) VALUES ($1,$2,4,'RECEIPT',$3)`, txn, it.ID, ref)
	tx.MustExec(`INSERT INTO lots (id,item_id,lot_no,quantity,source_doc) VALUES ($1,$2,$3,4,$4)`, lot, it.ID, uniq("LOT"), po.PONo)
	tx.MustExec(`INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc) VALUES ($1,$2,4,'RECEIPT',$3)`, lot, txn, ref)
	tx.MustExec(`INSERT INTO purchase_receipts (id,purchase_order_id,item_id,quantity,lot_id,inventory_txn_id,received_by_user_id,received_by_username)
	             VALUES ($1,$2,$3,4,$4,$5,$6,$7)`, rid, po.ID, it.ID, lot, txn, planner.ID, planner.Username)
	tx.MustExec(`UPDATE purchase_orders SET received_qty=4, status='PARTIALLY_RECEIVED' WHERE id=$1`, po.ID)
	if err := tx.Commit(); err != nil {
		t.Fatalf("partial receipt: %v", err)
	}

	receipts, err := pos.ListReceipts(f.ctx, po.ID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("ListReceipts: %+v %v", receipts, err)
	}
	if r := receipts[0]; r.PONo != po.PONo || r.LotID != lot || r.InventoryTxnID != txn || !approxEqual(r.Quantity, 4) || r.ReceivedByUsername != planner.Username {
		t.Fatalf("receipt row: %+v", r)
	}
	if g, _ := pos.Get(f.ctx, po.ID); !approxEqual(g.RemainingQty, 6) || !approxEqual(g.ReceivedQty, 4) {
		t.Fatalf("remaining after partial receipt: %+v", g)
	}
	// Receipt evidence is immutable.
	_, err = f.db.Exec(`DELETE FROM purchase_receipts WHERE id=$1`, rid)
	wantErrContains(t, err, "immutable")

	// Partially received orders cannot be flagged RECEIVED, but can be CLOSED.
	wantErrContains(t, pos.UpdateStatus(f.ctx, po.ID, "RECEIVED"), "must be PARTIALLY_RECEIVED")
	wantErrContains(t, pos.UpdateStatus(f.ctx, po.ID, "CLOSED"), "requires a reason")
	closer := f.user(t, domain.RolePlanner)
	if _, err := f.db.Exec(`UPDATE purchase_orders SET status='CLOSED', closed_by_user_id=$2, close_reason='short' WHERE id=$1`, po.ID, closer.ID); err != nil {
		t.Fatalf("close partial PO: %v", err)
	}
	if err := pos.SetReceivedLot(f.ctx, po.ID, lot); err != nil {
		t.Fatal(err)
	}
	if g, _ := pos.Get(f.ctx, po.ID); g.Status != "CLOSED" || g.ReceivedLotID == nil || *g.ReceivedLotID != lot {
		t.Fatalf("closed PO: %+v", g)
	}

	list, err := pos.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		found = found || p.ID == po.ID
	}
	if !found {
		t.Fatal("PO missing from List")
	}
	if _, err := pos.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}
}

func TestPurchaseOrderFullReceiptMarksReceived(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	po := &domain.PurchaseOrder{PONo: uniq("PO"), ItemID: it.ID, Supplier: "ACME", Quantity: 3, DueDate: date(2026, 12, 1)}
	if err := f.repos.Purchases.Create(f.ctx, po); err != nil {
		t.Fatal(err)
	}
	planner := f.user(t, domain.RolePlanner)
	rid, txn, lot := uuid.New(), uuid.New(), uuid.New()
	ref := "PO:" + po.PONo + ":RCPT:" + rid.String()
	tx := f.db.MustBegin()
	tx.MustExec(`INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc) VALUES ($1,$2,3,'RECEIPT',$3)`, txn, it.ID, ref)
	tx.MustExec(`INSERT INTO lots (id,item_id,lot_no,quantity) VALUES ($1,$2,$3,3)`, lot, it.ID, uniq("LOT"))
	tx.MustExec(`INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc) VALUES ($1,$2,3,'RECEIPT',$3)`, lot, txn, ref)
	tx.MustExec(`INSERT INTO purchase_receipts (id,purchase_order_id,item_id,quantity,lot_id,inventory_txn_id,received_by_user_id,received_by_username)
	             VALUES ($1,$2,$3,3,$4,$5,$6,$7)`, rid, po.ID, it.ID, lot, txn, planner.ID, planner.Username)
	tx.MustExec(`UPDATE purchase_orders SET received_qty=3 WHERE id=$1`, po.ID)
	if _, err := tx.Exec(`SET CONSTRAINTS ALL IMMEDIATE`); err == nil {
		t.Fatal("full receipt left OPEN must violate the PO state invariant")
	}
	_ = tx.Rollback()

	// Same receipt, now with the repository marking it RECEIVED inside the flow.
	tx = f.db.MustBegin()
	tx.MustExec(`INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc) VALUES ($1,$2,3,'RECEIPT',$3)`, txn, it.ID, ref)
	tx.MustExec(`INSERT INTO lots (id,item_id,lot_no,quantity) VALUES ($1,$2,$3,3)`, lot, it.ID, uniq("LOT"))
	tx.MustExec(`INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc) VALUES ($1,$2,3,'RECEIPT',$3)`, lot, txn, ref)
	tx.MustExec(`INSERT INTO purchase_receipts (id,purchase_order_id,item_id,quantity,lot_id,inventory_txn_id,received_by_user_id,received_by_username)
	             VALUES ($1,$2,$3,3,$4,$5,$6,$7)`, rid, po.ID, it.ID, lot, txn, planner.ID, planner.Username)
	tx.MustExec(`UPDATE purchase_orders SET received_qty=3, status='RECEIVED', received_at=now() WHERE id=$1`, po.ID)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Purchases.UpdateStatus(f.ctx, po.ID, "RECEIVED"); err != nil {
		t.Fatalf("idempotent RECEIVED: %v", err)
	}
	g, _ := f.repos.Purchases.Get(f.ctx, po.ID)
	if g.Status != "RECEIVED" || g.ReceivedAt == nil || g.RemainingQty != 0 {
		t.Fatalf("received PO: %+v", g)
	}
}

func TestPurchaseOrderBlockedSupplier(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	planner := f.user(t, domain.RolePlanner)
	f.db.MustExec(`INSERT INTO supplier_quality_profiles (supplier_name,status,updated_by_user_id) VALUES ('BADCO','BLOCKED',$1)`, planner.ID)
	err := f.repos.Purchases.Create(f.ctx, &domain.PurchaseOrder{PONo: uniq("PO"), ItemID: it.ID, Supplier: " BADCO ", Quantity: 1, DueDate: date(2026, 12, 1)})
	wantErrContains(t, err, "BLOCKED")
}

// leadTimeSnapshot publishes one COMPLETE supplier lead-time run.
type leadTimeResult struct {
	supplier    string
	itemID      *uuid.UUID
	samples     int
	recommended int
}

func publishLeadTimeRun(t *testing.T, f *fixture, minSamples int, results []leadTimeResult) {
	t.Helper()
	planner := f.user(t, domain.RolePlanner)
	run := uuid.New()
	tx := f.db.MustBegin()
	defer func() { _ = tx.Rollback() }()
	tx.MustExec(`INSERT INTO supplier_lead_time_runs (id,window_start,window_end,min_samples,status,generated_by_user_id,generated_by)
	             VALUES ($1,'2026-01-01','2026-06-30',$2,'RUNNING',$3,$4)`, run, minSamples, planner.ID, planner.Username)
	for _, r := range results {
		tx.MustExec(`INSERT INTO supplier_lead_time_results
		  (id,run_id,supplier_name,item_id,sample_count,average_lead_days,stddev_lead_days,p50_lead_days,p90_lead_days,
		   on_time_rate,average_lateness_days,recommended_lead_days,confidence)
		  VALUES ($1,$2,$3,$4,$5,1,0,1,1,1,0,$6,'HIGH')`, uuid.New(), run, r.supplier, r.itemID, r.samples, r.recommended)
	}
	tx.MustExec(`UPDATE supplier_lead_time_runs SET status='COMPLETE', result_hash=$2, completed_at=now() WHERE id=$1`, run, strings.Repeat("a", 64))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveLeadTimeDays(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	other := f.item(t, "RM")
	pos := f.repos.Purchases

	// No reliability data: nominal is returned, negative nominal floors at 0.
	if d, err := pos.EffectiveLeadTimeDays(f.ctx, it.ID, 5); err != nil || d != 5 {
		t.Fatalf("no data: %d %v", d, err)
	}
	if d, err := pos.EffectiveLeadTimeDays(f.ctx, it.ID, -3); err != nil || d != 0 {
		t.Fatalf("negative nominal: %d %v", d, err)
	}

	itemID := it.ID
	otherID := other.ID
	planner := f.user(t, domain.RolePlanner)
	f.db.MustExec(`INSERT INTO supplier_quality_profiles (supplier_name,status,updated_by_user_id) VALUES ('BLOCKED-SUP','BLOCKED',$1)`, planner.ID)
	publishLeadTimeRun(t, f, 3, []leadTimeResult{
		{"EXACT-WINS", &itemID, 4, 8},    // exact item row preferred over the fallback below
		{"EXACT-WINS", nil, 50, 25},      // supplier-level fallback (ignored: exact exists)
		{"FALLBACK", nil, 5, 12},         // only supplier-level data
		{"SPARSE", &itemID, 2, 60},       // below min_samples
		{"BLOCKED-SUP", &itemID, 10, 90}, // blocked supplier never participates
		{"OTHER-ITEM", &otherID, 10, 70}, // different item
	})

	tests := []struct {
		name    string
		item    uuid.UUID
		nominal int
		want    int
	}{
		{"max of eligible suppliers", it.ID, 5, 12},
		{"nominal is a floor", it.ID, 40, 40},
		{"other item uses own data and fallbacks", other.ID, 1, 70},
	}
	for _, tc := range tests {
		got, err := pos.EffectiveLeadTimeDays(f.ctx, tc.item, tc.nominal)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %d (err %v), want %d", tc.name, got, err, tc.want)
		}
	}

	// Only the latest COMPLETE run is considered.
	time.Sleep(10 * time.Millisecond)
	publishLeadTimeRun(t, f, 1, []leadTimeResult{{"NEW", &itemID, 1, 2}})
	if got, _ := pos.EffectiveLeadTimeDays(f.ctx, it.ID, 1); got != 2 {
		t.Fatalf("latest run must supersede older ones, got %d", got)
	}
}

func TestDemandForecastsAreLegacyReadOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "FG")
	for _, src := range []string{"ORDER", "FORECAST"} {
		err := f.repos.Demand.Create(f.ctx, &domain.DemandForecast{ItemID: it.ID, DueDate: date(2026, 12, 1), Quantity: 1, Source: src})
		wantErrContains(t, err, "legacy read-only")
	}
	if _, err := f.repos.Demand.List(f.ctx); err != nil {
		t.Fatalf("List must still read legacy rows: %v", err)
	}
}

func TestMPSUpsertResetsProvenanceToManual(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "FG")
	mps := f.repos.MPS
	period := date(2027, 1, 4)

	e := &domain.MPSEntry{ItemID: it.ID, Period: period, Planned: 10, Released: 0}
	if err := mps.Upsert(f.ctx, e); err != nil {
		t.Fatal(err)
	}
	firstID := e.ID
	// Upsert on the same (item, period) updates in place, keeping the original row.
	if err := mps.Upsert(f.ctx, &domain.MPSEntry{ItemID: it.ID, Period: period, Planned: 25, Released: 5}); err != nil {
		t.Fatal(err)
	}
	var rows []domain.MPSEntry
	all, err := mps.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.ItemID == it.ID {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("expected one MPS row per item/period, got %d", len(rows))
	}
	r := rows[0]
	if r.ID != firstID || !approxEqual(r.Planned, 25) || !approxEqual(r.Released, 5) || r.DemandBasis != "MANUAL" ||
		r.SourceForecastRunID != nil || r.SourceSOPPlanID != nil {
		t.Fatalf("upserted row: %+v", r)
	}
}

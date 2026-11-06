package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestItemCRUDAndDefaults(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	items := f.repos.Items

	it := &domain.Item{Code: uniq("ITM"), Name: "Widget", Type: "RM", UoM: "EA", LotSize: 5}
	if err := items.Create(f.ctx, it); err != nil {
		t.Fatal(err)
	}
	if it.ID == uuid.Nil || it.CreatedAt.IsZero() || !it.CreatedAt.Equal(it.UpdatedAt) {
		t.Fatalf("Create must assign id and timestamps: %+v", it)
	}
	got, err := items.Get(f.ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LotSizeMethod != "LFL" || got.PoqPeriods != 1 || !approxEqual(got.HoldingCostPct, 0.20) {
		t.Fatalf("defaults not applied: method=%s poq=%d hold=%v", got.LotSizeMethod, got.PoqPeriods, got.HoldingCostPct)
	}

	// Explicit values are preserved.
	explicit := &domain.Item{ID: uuid.New(), Code: uniq("ITM"), Name: "x", Type: "FG", UoM: "EA", LotSize: 1,
		LotSizeMethod: "EOQ", PoqPeriods: 4, HoldingCostPct: 0.35}
	if err := items.Create(f.ctx, explicit); err != nil {
		t.Fatal(err)
	}
	if g, _ := items.Get(f.ctx, explicit.ID); g.LotSizeMethod != "EOQ" || g.PoqPeriods != 4 || !approxEqual(g.HoldingCostPct, 0.35) {
		t.Fatalf("explicit values overwritten: %+v", g)
	}

	// Unique code and type CHECK.
	if err := items.Create(f.ctx, &domain.Item{Code: it.Code, Name: "dup", Type: "RM", UoM: "EA", LotSize: 1}); err == nil {
		t.Fatal("duplicate item code must be rejected")
	}
	if err := items.Create(f.ctx, &domain.Item{Code: uniq("ITM"), Name: "bad", Type: "XX", UoM: "EA", LotSize: 1}); err == nil {
		t.Fatal("invalid item type must be rejected")
	}

	// Update normalizes empty method / non-positive POQ again.
	before := got.UpdatedAt
	got.Name = "Widget v2"
	got.LotSizeMethod = ""
	got.PoqPeriods = 0
	if err := items.Update(f.ctx, got); err != nil {
		t.Fatal(err)
	}
	upd, _ := items.Get(f.ctx, it.ID)
	if upd.Name != "Widget v2" || upd.LotSizeMethod != "LFL" || upd.PoqPeriods != 1 || !upd.UpdatedAt.After(before) {
		t.Fatalf("update not applied: %+v", upd)
	}

	list, err := items.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Code > list[i].Code {
			t.Fatalf("List not ordered by code at %d: %s > %s", i, list[i-1].Code, list[i].Code)
		}
	}

	if err := items.Delete(f.ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := items.Get(f.ctx, it.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted item Get: %v", err)
	}
}

func TestBOMExplodeLLCAndCycleGuard(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fg := f.item(t, "FG")
	sa := f.item(t, "SA")
	rm := f.item(t, "RM")

	// fg -> 2 sa (10% scrap), sa -> 3 rm, fg -> 1 rm
	f.db.MustExec(`INSERT INTO bom_components (parent_id,child_id,quantity,scrap_pct) VALUES ($1,$2,2,0.1),($2,$3,3,0),($1,$3,1,0)`, fg.ID, sa.ID, rm.ID)

	rows, err := f.repos.BOM.Explode(f.ctx, fg.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		level int
		child uuid.UUID
	}
	got := map[key]float64{}
	for _, r := range rows {
		got[key{r.Level, r.ChildID}] = r.TotalQty
	}
	want := map[key]float64{
		{1, sa.ID}: 22, // 10 * 2 * 1.1
		{1, rm.ID}: 10, // 10 * 1
		{2, rm.ID}: 66, // 22 * 3
	}
	if len(got) != len(want) {
		t.Fatalf("explode rows = %+v", rows)
	}
	for k, w := range want {
		if !approxEqual(got[k], w) {
			t.Errorf("level %d child %s: got %v want %v", k.level, k.child, got[k], w)
		}
	}

	comps, err := f.repos.BOM.ComponentsOf(f.ctx, fg.ID)
	if err != nil || len(comps) != 2 {
		t.Fatalf("ComponentsOf: %d rows, %v", len(comps), err)
	}
	edges, err := f.repos.BOM.AllEdges(f.ctx)
	if err != nil || len(edges) < 3 {
		t.Fatalf("AllEdges: %d rows, %v", len(edges), err)
	}

	if err := f.repos.Items.RecomputeLLC(f.ctx); err != nil {
		t.Fatal(err)
	}
	llc := func(id uuid.UUID) int {
		x, err := f.repos.Items.Get(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return x.LowLevelCode
	}
	// rm is reachable at depth 1 and 2: LLC must be the deepest level.
	if llc(fg.ID) != 0 || llc(sa.ID) != 1 || llc(rm.ID) != 2 {
		t.Fatalf("LLC fg=%d sa=%d rm=%d, want 0/1/2", llc(fg.ID), llc(sa.ID), llc(rm.ID))
	}

	// Closing a cycle rm -> fg must be rejected at commit by the deferred guard.
	_, err = f.db.Exec(`INSERT INTO bom_components (parent_id,child_id,quantity) VALUES ($1,$2,1)`, rm.ID, fg.ID)
	wantErrContains(t, err, "BOM cycle detected")
	// Self reference is rejected by CHECK.
	_, err = f.db.Exec(`INSERT INTO bom_components (parent_id,child_id,quantity) VALUES ($1,$1,1)`, rm.ID)
	wantErrContains(t, err, "bom_no_self_reference")
}

func TestInventoryPostOnlyAllowsLogicalTransactions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	inv := f.repos.Inventory

	for _, typ := range []string{"RECEIPT", "ISSUE", "ADJUST", ""} {
		err := inv.Post(f.ctx, &domain.InventoryTxn{ItemID: it.ID, Quantity: 1, TxnType: typ})
		wantErrContains(t, err, "InventoryLedgerService")
	}

	f.receiveLot(t, it.ID, 100)

	res := &domain.InventoryTxn{ItemID: it.ID, Quantity: 30, TxnType: "RESERVE", RefDoc: "WO-1"}
	if err := inv.Post(f.ctx, res); err != nil {
		t.Fatal(err)
	}
	if res.ID == uuid.Nil || res.OccurredAt.IsZero() {
		t.Fatal("Post must assign id and occurred_at")
	}
	if err := inv.Post(f.ctx, &domain.InventoryTxn{ItemID: it.ID, Quantity: 10, TxnType: "UNRESERVE", RefDoc: "WO-1"}); err != nil {
		t.Fatal(err)
	}
	// Reservation quantities must be positive (CHECK inventory_txns_quantity_semantics).
	err := inv.Post(f.ctx, &domain.InventoryTxn{ItemID: it.ID, Quantity: -5, TxnType: "RESERVE"})
	wantErrContains(t, err, "inventory_txns_quantity_semantics")

	bal, err := inv.BalanceFor(f.ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(bal.OnHand, 100) || !approxEqual(bal.Reserved, 20) || !approxEqual(bal.Available(), 80) {
		t.Fatalf("balance on_hand=%v reserved=%v", bal.OnHand, bal.Reserved)
	}
	if _, err := inv.BalanceFor(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("BalanceFor unknown item: %v", err)
	}

	txns, err := inv.Transactions(f.ctx, it.ID)
	if err != nil || len(txns) != 3 {
		t.Fatalf("Transactions: %d %v", len(txns), err)
	}
	for i := 1; i < len(txns); i++ {
		if txns[i-1].OccurredAt.Before(txns[i].OccurredAt) {
			t.Fatal("Transactions must be newest first")
		}
	}

	findOnHand := func() float64 {
		rows, err := inv.OnHand(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ItemID == it.ID {
				return r.OnHand
			}
		}
		t.Fatal("item missing from OnHand")
		return 0
	}
	if v := findOnHand(); !approxEqual(v, 100) {
		t.Fatalf("OnHand = %v", v)
	}
	balances, err := inv.Balance(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range balances {
		if b.ItemID == it.ID {
			found = true
			if !approxEqual(b.Reserved, 20) {
				t.Fatalf("Balance reserved = %v", b.Reserved)
			}
		}
	}
	if !found {
		t.Fatal("item missing from Balance")
	}

	recon, err := inv.Reconciliation(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recon {
		if r.ItemID == it.ID && (!approxEqual(r.Difference, 0) || !approxEqual(r.LotOnHand, r.LedgerOnHand)) {
			t.Fatalf("ledger and lots must reconcile: %+v", r)
		}
	}
}

func TestLedgerRejectsUnallocatedPhysicalReceipt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	// A bare RECEIPT header with no lot allocation fails at COMMIT.
	_, err := f.db.Exec(`INSERT INTO inventory_txns (item_id,quantity,txn_type) VALUES ($1,5,'RECEIPT')`, it.ID)
	wantErrContains(t, err, "not fully lot-allocated")
	// An ISSUE larger than the lot balance would make the lot negative.
	lot, _ := f.receiveLot(t, it.ID, 5)
	tx := f.db.MustBegin()
	txn := uuid.New()
	tx.MustExec(`INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc) VALUES ($1,$2,-6,'ISSUE','X')`, txn, it.ID)
	tx.MustExec(`INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc) VALUES ($1,$2,-6,'ISSUE','X')`, lot, txn)
	wantErrContains(t, tx.Commit(), "negative physical balance")
}

func TestAnnualIssueUsageWindow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	it := f.item(t, "RM")
	other := f.item(t, "RM")
	lot, _ := f.receiveLot(t, it.ID, 1000)

	var tz string
	if err := f.db.Get(&tz, `SELECT eco_business_timezone()`); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Skipf("tzdata for %s unavailable: %v", tz, err)
	}
	asOf := date(2026, 6, 30)
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, loc) }

	f.issueLot(t, it.ID, lot, 1, "in-first-day", at(2025, 7, 1, 0))      // first day of window
	f.issueLot(t, it.ID, lot, 10, "in-last-day", at(2026, 6, 30, 23))    // last day of window
	f.issueLot(t, it.ID, lot, 100, "before-window", at(2025, 6, 30, 23)) // one day too early
	f.issueLot(t, it.ID, lot, 200, "after-window", at(2026, 7, 1, 0))    // one day too late

	rows, err := f.repos.Inventory.AnnualIssueUsage(f.ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	usage := map[uuid.UUID]float64{}
	for _, r := range rows {
		usage[r.ItemID] = r.UsageQty
	}
	if !approxEqual(usage[it.ID], 11) {
		t.Fatalf("usage = %v, want 11 (only in-window ISSUEs)", usage[it.ID])
	}
	if v, ok := usage[other.ID]; !ok || v != 0 {
		t.Fatalf("items without issues must be reported with 0 usage, got %v (present=%v)", v, ok)
	}

	d, err := f.repos.Inventory.BusinessDate(f.ctx)
	if err != nil || d.IsZero() {
		t.Fatalf("BusinessDate: %v %v", d, err)
	}
	if diff := time.Since(d); diff < -48*time.Hour || diff > 48*time.Hour {
		t.Fatalf("business date %v too far from now", d)
	}
}

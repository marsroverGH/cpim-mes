package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
)

// planMRPExpect is one expected (period, gross, scheduled, net, planned, projected, release) MRP bucket.
type planMRPExpect struct {
	day                                   int // day of March 2027
	gross, sched, net, planned, projected float64
	releaseDay                            int // day offset from 2027-03-01 (may be negative); ignored when planned == 0
}

func planCheckMRP(t *testing.T, rows []domain.MRPResult, it domain.Item, want []planMRPExpect) {
	t.Helper()
	got := planMRPRowsFor(rows, it.ID)
	if len(got) != len(want) {
		for _, r := range got {
			t.Logf("%s %s gross=%v sched=%v net=%v planned=%v oh=%v", r.ItemCode, r.Period.Format("2006-01-02"), r.GrossReq, r.ScheduledRcpt, r.NetReq, r.PlannedReceipt, r.OnHand)
		}
		t.Fatalf("%s: %d MRP buckets, want %d", it.Code, len(got), len(want))
	}
	base := planDay(2027, 3, 1)
	for i, w := range want {
		r := got[i]
		day := planDay(2027, 3, w.day)
		planAssertDay(t, it.Code+" period", r.Period, day)
		planAssertNear(t, it.Code+" "+day.Format("01-02")+" gross", r.GrossReq, w.gross)
		planAssertNear(t, it.Code+" "+day.Format("01-02")+" scheduled", r.ScheduledRcpt, w.sched)
		planAssertNear(t, it.Code+" "+day.Format("01-02")+" net", r.NetReq, w.net)
		planAssertNear(t, it.Code+" "+day.Format("01-02")+" planned", r.PlannedReceipt, w.planned)
		planAssertNear(t, it.Code+" "+day.Format("01-02")+" projected", r.OnHand, w.projected)
		if r.PlannedOrder != r.PlannedReceipt {
			t.Fatalf("%s: PlannedOrder %v != PlannedReceipt %v", it.Code, r.PlannedOrder, r.PlannedReceipt)
		}
		if w.planned > 0 {
			if r.PlannedReleaseDate == nil {
				t.Fatalf("%s %s: missing planned release date", it.Code, day.Format("01-02"))
			}
			planAssertDay(t, it.Code+" release", *r.PlannedReleaseDate, base.AddDate(0, 0, w.releaseDay))
		} else if r.PlannedReleaseDate != nil {
			t.Fatalf("%s %s: unexpected release date %v", it.Code, day.Format("01-02"), r.PlannedReleaseDate)
		}
	}
}

// Three-level BOM FG -> SA (x2) -> RM (x3, 10% scrap) with LFL / FOQ / safety stock,
// lead-time offsetting, a past-due release and pegging propagation.
func TestPlanMRPThreeLevelNettingLotSizingAndOffset(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	fg := planItem(t, f, domain.Item{Code: "PLN-FG", Type: domain.ItemTypeFinished, LeadTimeDays: 2, LotSizeMethod: "LFL"})
	sa := planItem(t, f, domain.Item{Code: "PLN-SA", Type: domain.ItemTypeSubAssembly, LeadTimeDays: 3, LotSizeMethod: "FOQ", LotSize: 25})
	rm := planItem(t, f, domain.Item{Code: "PLN-RM", Type: domain.ItemTypeRawMaterial, LeadTimeDays: 5, LotSizeMethod: "LFL", SafetyStock: 4})
	planBOM(t, f, fg.ID, sa.ID, 2, 0)
	planBOM(t, f, sa.ID, rm.ID, 3, 0.1)
	planReceive(t, f, fg.ID, 5)
	planReceive(t, f, sa.ID, 10)
	planMPS(t, f, fg.ID, planDay(2027, 3, 10), 20)
	planMPS(t, f, fg.ID, planDay(2027, 3, 17), 15)
	// Outside the horizon (start 03-01, 28 days => 03-01..03-28): must be ignored.
	planMPS(t, f, fg.ID, planDay(2027, 3, 29), 999)
	planMPS(t, f, fg.ID, planDay(2027, 2, 27), 999)

	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{StartDate: planDay(2027, 3, 1).Add(13 * 3600e9), HorizonDays: 28})
	if err != nil {
		t.Fatalf("MRP run: %v", err)
	}

	// LLC must have been recomputed for the new items.
	items, err := f.Svc.Items.List(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	llc := map[string]int{"PLN-FG": 0, "PLN-SA": 1, "PLN-RM": 2}
	for _, it := range items {
		if want, ok := llc[it.Code]; ok && it.LowLevelCode != want {
			t.Fatalf("LLC(%s) = %d, want %d", it.Code, it.LowLevelCode, want)
		}
	}

	// FG (LFL, LT 2): 5 on hand; 20 due 03-10 nets to 15, 15 due 03-17 nets to 15.
	planCheckMRP(t, rows, fg, []planMRPExpect{
		{day: 10, gross: 20, net: 15, planned: 15, projected: 0, releaseDay: 7},
		{day: 17, gross: 15, net: 15, planned: 15, projected: 0, releaseDay: 14},
	})
	// SA (FOQ 25, LT 3): dependent gross 30 on each FG release date.
	// 03-08: 10 on hand -> net 20 -> 25, ending 5. 03-15: net 25 -> 25, ending 0.
	planCheckMRP(t, rows, sa, []planMRPExpect{
		{day: 8, gross: 30, net: 20, planned: 25, projected: 5, releaseDay: 4},
		{day: 15, gross: 30, net: 25, planned: 25, projected: 0, releaseDay: 11},
	})
	// RM (LFL, SS 4, LT 5): start bucket replenishes safety stock (release is past due,
	// 02-24); each SA release needs 25*3*1.1 = 82.5 on the SA release date.
	planCheckMRP(t, rows, rm, []planMRPExpect{
		{day: 1, gross: 0, net: 4, planned: 4, projected: 4, releaseDay: -5},
		{day: 5, gross: 82.5, net: 82.5, planned: 82.5, projected: 4, releaseDay: -1},
		{day: 12, gross: 82.5, net: 82.5, planned: 82.5, projected: 4, releaseDay: 6},
	})

	r, _ := planMRPRow(rows, rm.ID, planDay(2027, 3, 5))
	if len(r.Pegging) != 1 || r.Pegging[0] != "PLN-FG" {
		t.Fatalf("RM pegging = %v, want [PLN-FG] (top-level MPS item propagated)", r.Pegging)
	}
	if r.LeadTimeSource != "ITEM_MASTER" || r.PlanningLeadTimeDays != 5 || r.LotMethod != "LFL" || r.SafetyStockTarget != 4 {
		t.Fatalf("RM row metadata = %+v", r)
	}
	if r0, _ := planMRPRow(rows, rm.ID, planDay(2027, 3, 1)); len(r0.Pegging) != 0 {
		t.Fatalf("safety-stock bucket must not be pegged to demand, got %v", r0.Pegging)
	}

	// Results are sorted by (period, item code).
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.Period.After(b.Period) || (a.Period.Equal(b.Period) && a.ItemCode > b.ItemCode) {
			t.Fatalf("results not sorted at %d: %s/%s then %s/%s", i, a.Period, a.ItemCode, b.Period, b.ItemCode)
		}
	}

	// Simulate must produce the identical plan without LLC maintenance.
	sim, err := f.Svc.MRP.Simulate(f.Ctx, MRPRequest{StartDate: planDay(2027, 3, 1), HorizonDays: 28})
	if err != nil {
		t.Fatalf("MRP simulate: %v", err)
	}
	planCheckMRP(t, sim, rm, []planMRPExpect{
		{day: 1, gross: 0, net: 4, planned: 4, projected: 4, releaseDay: -5},
		{day: 5, gross: 82.5, net: 82.5, planned: 82.5, projected: 4, releaseDay: -1},
		{day: 12, gross: 82.5, net: 82.5, planned: 82.5, projected: 4, releaseDay: 6},
	})
}

// Open PO remainder and a released WO are firm supply; the released WO also creates
// firm dependent demand for its direct components on its start date.
func TestPlanMRPScheduledReceiptsAndFirmWorkOrderDemand(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	fg := planItem(t, f, domain.Item{Code: "PLS-FG", Type: domain.ItemTypeFinished, LeadTimeDays: 1})
	rm := planItem(t, f, domain.Item{Code: "PLS-RM", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 4})
	planBOM(t, f, fg.ID, rm.ID, 2, 0)
	planMPS(t, f, fg.ID, planDay(2027, 3, 12), 30)

	// Released WO for 10 FG due 03-08, starting 03-05 (inserted as firm supply).
	wo := domain.WorkOrder{OrderNo: "PLS-WO-1", ItemID: fg.ID, Quantity: 10, StartDate: planDay(2027, 3, 5), DueDate: planDay(2027, 3, 8), Status: "RELEASED"}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatalf("create WO: %v", err)
	}
	// A PLANNED WO is not firm supply and must be ignored.
	if err := f.Svc.WorkOrders.Create(f.Ctx, &domain.WorkOrder{OrderNo: "PLS-WO-2", ItemID: fg.ID, Quantity: 500, StartDate: planDay(2027, 3, 2), DueDate: planDay(2027, 3, 3)}); err != nil {
		t.Fatalf("create planned WO: %v", err)
	}
	// Past-due open PO (due 02-20) counts in the start bucket; a later PO on 03-06.
	planPO(t, f, "PLS-PO-1", rm.ID, 7, planDay(2027, 2, 20))
	planPO(t, f, "PLS-PO-2", rm.ID, 50, planDay(2027, 3, 6))

	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{StartDate: planDay(2027, 3, 1), HorizonDays: 28})
	if err != nil {
		t.Fatalf("MRP run: %v", err)
	}
	// FG: WO receipt 10 on 03-08 -> projected 10; 03-12 gross 30 -> net 20, release 03-11.
	planCheckMRP(t, rows, fg, []planMRPExpect{
		{day: 8, sched: 10, projected: 10},
		{day: 12, gross: 30, net: 20, planned: 20, projected: 0, releaseDay: 10},
	})
	// RM: 03-01 PO 7 (past due); 03-05 WO component demand 20 -> net 13, release 03-01;
	// 03-06 PO 50 -> projected 50; 03-11 FG release demand 40 -> projected 10.
	planCheckMRP(t, rows, rm, []planMRPExpect{
		{day: 1, sched: 7, projected: 7},
		{day: 5, gross: 20, net: 13, planned: 13, projected: 0, releaseDay: 0},
		{day: 6, sched: 50, projected: 50},
		{day: 11, gross: 40, projected: 10},
	})
	r, _ := planMRPRow(rows, rm.ID, planDay(2027, 3, 5))
	if len(r.Pegging) != 1 || r.Pegging[0] != "PLS-FG" {
		t.Fatalf("firm WO component demand pegging = %v, want [PLS-FG]", r.Pegging)
	}
}

// POQ groups consecutive requirement buckets; EOQ rounds to multiples of the Wilson EOQ.
func TestPlanMRPPOQAndEOQLotSizing(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	poq := planItem(t, f, domain.Item{Code: "PLQ-POQ", Type: domain.ItemTypeFinished, LotSizeMethod: "POQ", PoqPeriods: 2})
	// EOQ = sqrt(2 * D * S / H) with D = 200 (MPS sum), S = 50, H = 10 * 0.2 = 2 -> 100.
	eoq := planItem(t, f, domain.Item{Code: "PLQ-EOQ", Type: domain.ItemTypeFinished, LotSizeMethod: "EOQ", OrderingCost: 50, StandardCost: 10, HoldingCostPct: 0.2})
	for _, d := range []int{3, 5, 9} {
		planMPS(t, f, poq.ID, planDay(2027, 3, d), float64(d))
	}
	planReceive(t, f, eoq.ID, 30)
	planMPS(t, f, eoq.ID, planDay(2027, 3, 4), 100)
	planMPS(t, f, eoq.ID, planDay(2027, 3, 18), 100)

	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{StartDate: planDay(2027, 3, 1), HorizonDays: 28})
	if err != nil {
		t.Fatalf("MRP run: %v", err)
	}
	// POQ(2): 03-03 + 03-05 aggregated into 03-03 (3+5=8); 03-09 stands alone.
	planCheckMRP(t, rows, poq, []planMRPExpect{
		{day: 3, gross: 8, net: 8, planned: 8, projected: 0, releaseDay: 2},
		{day: 9, gross: 9, net: 9, planned: 9, projected: 0, releaseDay: 8},
	})
	// EOQ 100: 03-04 net 70 -> 100 (ending 30); 03-18 net 70 -> 100.
	planCheckMRP(t, rows, eoq, []planMRPExpect{
		{day: 4, gross: 100, net: 70, planned: 100, projected: 30, releaseDay: 3},
		{day: 18, gross: 100, net: 70, planned: 100, projected: 30, releaseDay: 17},
	})
	r, _ := planMRPRow(rows, eoq.ID, planDay(2027, 3, 4))
	planAssertNear(t, "EOQ", r.EOQ, 100)
	if r.LotMethod != "EOQ" {
		t.Fatalf("lot method = %s", r.LotMethod)
	}
}

// Defaults: zero horizon => 28 days, zero start => today. MRP still runs on seed data.
func TestPlanMRPDefaults(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	rm := planItem(t, f, domain.Item{Code: "PLB-RM", Type: domain.ItemTypeRawMaterial, SafetyStock: 10})
	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{})
	if err != nil {
		t.Fatalf("MRP default run: %v", err)
	}
	got := planMRPRowsFor(rows, rm.ID)
	if len(got) != 1 {
		t.Fatalf("safety-stock only item rows = %d, want 1", len(got))
	}
	planAssertDay(t, "default start", got[0].Period, planToday())
	planAssertNear(t, "safety stock replenishment", got[0].PlannedReceipt, 10)
	for _, r := range rows {
		if r.Period.Before(planToday()) || !r.Period.Before(planToday().AddDate(0, 0, 28)) {
			t.Fatalf("row %s %s outside default 28-day horizon", r.ItemCode, r.Period)
		}
	}
}

// Thin item/MPS/demand/WO/PO/inventory services used by planning.
func TestPlanThinServicesGuardrails(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	it := planItem(t, f, domain.Item{Code: "PLT-1", Type: domain.ItemTypeFinished})
	got, err := f.Svc.Items.Get(f.Ctx, it.ID)
	if err != nil || got.Code != "PLT-1" || got.LotSizeMethod != "LFL" || got.PoqPeriods != 1 || !planNear(got.HoldingCostPct, 0.2) {
		t.Fatalf("item defaults = %+v, %v", got, err)
	}
	got.Name = "renamed"
	if err := f.Svc.Items.Update(f.Ctx, got); err != nil {
		t.Fatalf("update item: %v", err)
	}
	if err := f.Svc.Items.RecomputeLLC(f.Ctx); err != nil {
		t.Fatalf("recompute LLC: %v", err)
	}

	if err := f.Svc.Demand.Create(f.Ctx, &domain.DemandForecast{ItemID: it.ID}); err == nil {
		t.Fatal("legacy demand create must be rejected")
	} else {
		planErrCode(t, "demand create", err, domain.ErrCodeConflict)
	}
	if _, err := f.Svc.Demand.List(f.Ctx); err != nil {
		t.Fatalf("demand list: %v", err)
	}

	planMPS(t, f, it.ID, planDay(2027, 4, 1), 5)
	planMPS(t, f, it.ID, planDay(2027, 4, 1), 7) // upsert on (item, period)
	mps, err := f.Svc.MPS.List(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range mps {
		if m.ItemID == it.ID {
			n++
			planAssertNear(t, "upserted MPS", m.Planned, 7)
			if m.DemandBasis != "MANUAL" {
				t.Fatalf("demand basis = %s", m.DemandBasis)
			}
		}
	}
	if n != 1 {
		t.Fatalf("MPS rows for item = %d, want 1", n)
	}

	wo := domain.WorkOrder{OrderNo: "PLT-WO", ItemID: it.ID, Quantity: 3, StartDate: planDay(2027, 4, 1), DueDate: planDay(2027, 4, 2)}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatal(err)
	}
	planErrCode(t, "generic WO release", f.Svc.WorkOrders.UpdateStatus(f.Ctx, wo.ID, "RELEASED"), domain.ErrCodeConflict)
	if w, err := f.Svc.WorkOrders.Get(f.Ctx, wo.ID); err != nil || w.Status != "PLANNED" {
		t.Fatalf("WO status after rejected transition = %+v, %v", w, err)
	}

	sq := f.user(t, "plt-sq-planner", domain.RolePlanner)
	if _, err := f.DB.Exec(`INSERT INTO supplier_quality_profiles(supplier_name,status,updated_by_user_id) VALUES('PLT-BAD','BLOCKED',$1)`, sq.ID); err != nil {
		t.Fatalf("seed blocked supplier profile: %v", err)
	}
	err = f.Svc.Purchases.Create(f.Ctx, &domain.PurchaseOrder{PONo: "PLT-PO", ItemID: it.ID, Supplier: " PLT-BAD ", Quantity: 1, DueDate: planDay(2027, 4, 1)})
	planErrCode(t, "PO to blocked supplier", err, domain.ErrCodeConflict)
	planErrCode(t, "nil PO", f.Svc.Purchases.Create(f.Ctx, nil), domain.ErrCodeBadRequest)

	// Logical reservations bypass the lot ledger; physical receipts go through it.
	// RESERVE quantities are positive (inventory_txns_quantity_semantics).
	planReceive(t, f, it.ID, 12)
	if err := f.Svc.Inventory.Post(f.Ctx, &domain.InventoryTxn{ItemID: it.ID, Quantity: 2, TxnType: "RESERVE", RefDoc: "PLT"}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	bal, err := f.Svc.Inventory.Balance(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bal {
		if b.ItemID == it.ID {
			planAssertNear(t, "on hand", b.OnHand, 12)
			planAssertNear(t, "reserved", b.Reserved, 2)
		}
	}
}

// BOM explosion (recursive, with scrap) and the remaining thin read services.
func TestPlanBOMExplodeAndThinReads(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	fg := planItem(t, f, domain.Item{Code: "PLE-FG"})
	sa := planItem(t, f, domain.Item{Code: "PLE-SA", Type: domain.ItemTypeSubAssembly})
	rm := planItem(t, f, domain.Item{Code: "PLE-RM", Type: domain.ItemTypeRawMaterial})
	planBOM(t, f, fg.ID, sa.ID, 2, 0.5)
	planBOM(t, f, sa.ID, rm.ID, 4, 0)
	planBOM(t, f, fg.ID, rm.ID, 1, 0)

	comps, err := f.Svc.BOM.ComponentsOf(f.Ctx, fg.ID)
	if err != nil || len(comps) != 2 {
		t.Fatalf("components = %+v, %v", comps, err)
	}
	rows, err := f.Svc.BOM.Explode(f.Ctx, fg.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		level int
		code  string
	}
	got := map[key]float64{}
	for _, r := range rows {
		got[key{r.Level, r.ChildCode}] = r.TotalQty
	}
	// Level 1: SA = 10*2*1.5 = 30, RM = 10. Level 2: RM via SA = 30*4 = 120.
	want := map[key]float64{{1, "PLE-SA"}: 30, {1, "PLE-RM"}: 10, {2, "PLE-RM"}: 120}
	if len(got) != len(want) {
		t.Fatalf("explode = %v", got)
	}
	for k, w := range want {
		planAssertNear(t, "explode "+k.code, got[k], w)
	}

	planReceive(t, f, rm.ID, 6)
	txns, err := f.Svc.Inventory.Transactions(f.Ctx, rm.ID)
	if err != nil || len(txns) != 1 || txns[0].TxnType != "RECEIPT" || txns[0].Quantity != 6 {
		t.Fatalf("transactions = %+v, %v", txns, err)
	}
	oh, err := f.Svc.Inventory.OnHand(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range oh {
		if r.ItemID == rm.ID {
			planAssertNear(t, "on hand", r.OnHand, 6)
		}
	}
	rec, err := f.Svc.Inventory.Reconciliation(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rec {
		if r.ItemID == rm.ID && !planNear(r.Difference, 0) {
			t.Fatalf("ledger/lot reconciliation difference = %v", r.Difference)
		}
	}

	wo := domain.WorkOrder{OrderNo: "PLE-WO", ItemID: fg.ID, Quantity: 5, StartDate: planDay(2027, 3, 1), DueDate: planDay(2027, 3, 2), Status: "RELEASED"}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatal(err)
	}
	// Regression: UpdateProgress used to fail with SQLSTATE 42P08 on every call.
	if err := f.Svc.WorkOrders.UpdateProgress(f.Ctx, wo.ID, 2); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	if w, err := f.Svc.WorkOrders.Get(f.Ctx, wo.ID); err != nil || w.Status != "IN_PROGRESS" || w.ReportedProgressQty != 2 || w.CompletedQty != 0 {
		t.Fatalf("progress = %+v, %v (progress must not complete quantity)", w, err)
	}
	if wos, err := f.Svc.WorkOrders.List(f.Ctx); err != nil || len(wos) != 1 {
		t.Fatalf("WO list = %d, %v", len(wos), err)
	}
	po := planPO(t, f, "PLE-PO", rm.ID, 9, planDay(2027, 3, 5))
	pos, err := f.Svc.Purchases.List(f.Ctx)
	if err != nil || len(pos) != 1 || pos[0].ID != po.ID || pos[0].Status != "OPEN" || !planNear(pos[0].RemainingQty, 9) {
		t.Fatalf("PO list = %+v, %v", pos, err)
	}
	if rc, err := f.Svc.Purchases.ListReceipts(f.Ctx, po.ID); err != nil || len(rc) != 0 {
		t.Fatalf("receipts = %+v, %v", rc, err)
	}

	tmp := planItem(t, f, domain.Item{Code: "PLE-TMP"})
	if err := f.Svc.Items.Delete(f.Ctx, tmp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.Items.Get(f.Ctx, tmp.ID); err == nil {
		t.Fatal("deleted item still readable")
	}
}

// MRP bucket keys are time.Time values carrying their Location. MPS/PO dates come
// from PostgreSQL DATE columns as UTC midnight, while the start bucket (and the
// horizon end) are derived from req.StartDate in its own Location (time.Now() =
// time.Local when omitted). Regression: with a non-UTC start the same calendar day
// used to become two buckets and an MPS on the last horizon day was dropped.
func TestPlanMRPNonUTCStartDateBuckets(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	it := planItem(t, f, domain.Item{Code: "PLZ-FG", SafetyStock: 10})
	planMPS(t, f, it.ID, planDay(2027, 3, 1), 5)
	planMPS(t, f, it.ID, planDay(2027, 3, 7), 3)
	jst := time.FixedZone("JST", 9*3600)
	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{StartDate: time.Date(2027, 3, 1, 9, 0, 0, 0, jst), HorizonDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	got := planMRPRowsFor(rows, it.ID)
	var total float64
	for _, r := range got {
		total += r.PlannedReceipt
	}
	if len(got) != 2 || !planNear(total, 18) {
		days := []string{}
		for _, r := range got {
			days = append(days, r.Period.String())
		}
		// Regression: buckets used to split by the server time zone.
		t.Fatalf("MRP buckets: %d %v, planned total %.2f (want 2 buckets, 18)", len(got), days, total)
	}
}

package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestOpsBackorderPreviewPublish(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RolePlanner)
	bop := f.Svc.Backorders
	c := opsCustomer(t, f, actor)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	opsStock(t, f, fg.ID, 5, opsCode("L"))

	// --- Input validation.
	_, err := bop.Preview(f.Ctx, BackorderPreviewInput{}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 367}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "366")
	nilID := uuid.Nil
	_, err = bop.Preview(f.Ctx, BackorderPreviewInput{FilterItemID: &nilID}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "filterItemId")
	// No committed demand for the item: the run is recorded as FAILED.
	_, err = bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 14, FilterItemID: &fg.ID}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "no committed open demand")
	runs, err := bop.ListRuns(f.Ctx)
	if err != nil || len(runs) != 1 || runs[0].Status != "FAILED" {
		t.Fatalf("failed run must be recorded: %+v err=%v", runs, err)
	}
	_, err = bop.Publish(f.Ctx, BackorderPublishInput{RunID: runs[0].ID}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "only a successful")
	_, err = bop.Publish(f.Ctx, BackorderPublishInput{}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "runId")
	_, err = bop.Publish(f.Ctx, BackorderPublishInput{RunID: uuid.New()}, actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "backorder run")
	_, err = bop.Publish(f.Ctx, BackorderPublishInput{RunID: runs[0].ID}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")

	// --- Two confirmed orders compete for 5 units: earlier request ranks first.
	mk := func(qty float64, day int) *domain.SalesOrderDetail {
		so, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
			OrderNo: opsCode("SO"), CustomerID: c.ID, RequestedDate: opsDay(day),
			Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: qty}},
		}, actor)
		if err != nil {
			t.Fatal(err)
		}
		if so, err = f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, actor); err != nil {
			t.Fatal(err)
		}
		return so
	}
	so2 := mk(4, 4)
	so1 := mk(4, 2)
	// 1 unit of so2 is already allocated (fixed supply for that line).
	if _, err := f.Svc.SalesOrders.Allocate(f.Ctx, so2.Lines[0].ID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 1}, actor); err != nil {
		t.Fatal(err)
	}

	res, err := bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 14, FilterItemID: &fg.ID}, actor)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Run.Status != "SUCCEEDED" || res.Run.ResultHash == nil || len(res.Lines) != 2 || res.Publication != nil {
		t.Fatalf("preview run: %+v lines=%d", res.Run, len(res.Lines))
	}
	l1, l2 := res.Lines[0], res.Lines[1]
	if l1.SalesOrderLineID != so1.Lines[0].ID || l1.RankNo != 1 || l2.SalesOrderLineID != so2.Lines[0].ID {
		t.Fatalf("ranking by requested date: %+v / %+v", l1, l2)
	}
	// Free ATP = 5 on hand - 1 reserved = 4: line 1 takes all 4, line 2 keeps its
	// fixed allocation of 1 and gets no further ATP.
	if !opsNear(l1.ATPQty, 4) || !opsNear(l1.BackorderQty, 0) || l1.Decision != "NEW_PROMISE" || l1.ProposedPromisedDate == nil ||
		l1.ProposedPromisedDate.Format("2006-01-02") != opsDay(2) {
		t.Fatalf("line 1: %+v", l1)
	}
	if !opsNear(l2.AllocatedQty, 1) || !opsNear(l2.ATPQty, 0) {
		t.Fatalf("line 2: %+v", l2)
	}
	// Coverage invariant: fixed + ATP + CTP + backorder == open for every line.
	confByLine := map[uuid.UUID]float64{}
	for _, cf := range res.Confirmations {
		confByLine[cf.SalesOrderLineID] += cf.Quantity
	}
	for _, l := range res.Lines {
		if !opsNear(l.AllocatedQty+l.ATPQty+l.CTPQty+l.BackorderQty, l.OpenQty) {
			t.Errorf("line %d coverage %.2f+%.2f+%.2f+%.2f != open %.2f", l.RankNo, l.AllocatedQty, l.ATPQty, l.CTPQty, l.BackorderQty, l.OpenQty)
		}
		if !opsNear(confByLine[l.SalesOrderLineID], l.AllocatedQty+l.ATPQty+l.CTPQty) {
			t.Errorf("line %d confirmations %.2f do not match confirmed supply", l.RankNo, confByLine[l.SalesOrderLineID])
		}
		if l.BackorderQty > 1e-9 && (l.Decision != "BACKORDER" || l.ProposedPromisedDate != nil) {
			t.Errorf("backordered line must not carry a promise: %+v", l)
		}
	}

	// --- A supply change makes the preview stale.
	stale, err := bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 14, FilterItemID: &fg.ID}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if *stale.Run.ResultHash != *res.Run.ResultHash {
		t.Fatalf("identical inputs must give identical hash")
	}
	opsStock(t, f, fg.ID, 10, opsCode("L"))
	_, err = bop.Publish(f.Ctx, BackorderPublishInput{RunID: stale.Run.ID}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "STALE_BOP")

	// --- Fresh preview with ample stock promises both orders; publish writes dates.
	fresh, err := bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 14, FilterItemID: &fg.ID}, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range fresh.Lines {
		if l.Decision != "NEW_PROMISE" || !opsNear(l.BackorderQty, 0) {
			t.Fatalf("with stock every line should be promised: %+v", l)
		}
	}
	pub, err := bop.Publish(f.Ctx, BackorderPublishInput{RunID: fresh.Run.ID}, actor)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.Publication == nil || pub.Publication.ResultHash != *fresh.Run.ResultHash || pub.Publication.PublishedBy != actor.Username {
		t.Fatalf("publication: %+v", pub.Publication)
	}
	d1, _ := f.Svc.SalesOrders.Get(f.Ctx, so1.Order.ID)
	d2, _ := f.Svc.SalesOrders.Get(f.Ctx, so2.Order.ID)
	if d1.Lines[0].PromisedDate == nil || d1.Lines[0].PromisedDate.Format("2006-01-02") != opsDay(2) ||
		d1.Order.PromisedDate == nil || d2.Lines[0].PromisedDate == nil || d2.Lines[0].PromisedDate.Format("2006-01-02") != opsDay(4) {
		t.Fatalf("published promise dates: so1=%v/%v so2=%v", d1.Lines[0].PromisedDate, d1.Order.PromisedDate, d2.Lines[0].PromisedDate)
	}
	// Publishing again is idempotent.
	again, err := bop.Publish(f.Ctx, BackorderPublishInput{RunID: fresh.Run.ID}, actor)
	if err != nil || again.Publication == nil || again.Publication.ID != pub.Publication.ID {
		t.Fatalf("idempotent publish: %+v err=%v", again.Publication, err)
	}
	// After publishing, re-running the same plan reports UNCHANGED promises.
	re, err := bop.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 14, FilterItemID: &fg.ID}, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range re.Lines {
		if l.Decision != "UNCHANGED" {
			t.Fatalf("post-publication decision: %+v", l)
		}
	}
	// Promised dates on confirmed lines can only change through BOP publication.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE sales_order_lines SET promised_date=promised_date+1 WHERE id=$1`, so1.Lines[0].ID)
	opsWantDBErr(t, err, "immutable")
	if _, err := bop.GetRun(f.Ctx, uuid.New()); err == nil {
		t.Fatalf("GetRun unknown must fail")
	}
}

func TestOpsBackorderProductAllocationAndPriority(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RoleAdmin)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	opsStock(t, f, fg.ID, 10, opsCode("L"))
	pa := f.Svc.ProductAllocation

	customer := func(class string) *domain.Customer {
		c := opsCustomer(t, f, actor)
		if class != "STANDARD" {
			if _, err := pa.SetCustomerServiceClass(f.Ctx, c.ID, CustomerServiceClassInput{ServiceClassCode: class}, actor); err != nil {
				t.Fatalf("set class %s: %v", class, err)
			}
		}
		return c
	}
	order := func(c *domain.Customer, qty float64, day int) *domain.SalesOrderDetail {
		so := opsSalesOrder(t, f, actor, c.ID, SalesOrderLineInput{ItemID: fg.ID, Quantity: qty, RequestedDate: opsDay(day)})
		so, err := f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, actor)
		if err != nil {
			t.Fatal(err)
		}
		return so
	}
	soA := order(customer("STANDARD"), 9, 2)
	soB := order(customer("STRATEGIC"), 5, 3)
	soC := order(customer("OTHER"), 2, 1)
	if _, err := pa.SetSalesOrderPriority(f.Ctx, soA.Order.ID, SalesOrderPriorityInput{Priority: "expedite"}, actor); err != nil {
		t.Fatalf("priority: %v", err)
	}
	plan, err := pa.CreatePlan(f.Ctx, ProductAllocationPlanInput{
		ItemID: fg.ID, Name: "split", EffectiveFrom: opsDay(-1), EffectiveTo: opsDay(60),
		Buckets: []ProductAllocationBucketInput{
			{ServiceClassCode: "STRATEGIC", AllocationPct: 30, PriorityRank: 1},
			{ServiceClassCode: "STANDARD", AllocationPct: 70, PriorityRank: 2},
		},
	}, actor)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := pa.ActivatePlan(f.Ctx, plan.Plan.ID, actor); err != nil {
		t.Fatalf("activate plan: %v", err)
	}

	res, err := f.Svc.Backorders.Preview(f.Ctx, BackorderPreviewInput{HorizonDays: 10, FilterItemID: &fg.ID}, actor)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(res.Lines) != 3 {
		t.Fatalf("lines: %+v", res.Lines)
	}
	a, b, c := res.Lines[0], res.Lines[1], res.Lines[2]
	// EXPEDITE beats service class; STRATEGIC beats OTHER despite later date.
	if a.SalesOrderID != soA.Order.ID || b.SalesOrderID != soB.Order.ID || c.SalesOrderID != soC.Order.ID {
		t.Fatalf("rank order: %s %s %s", a.SalesOrderNo, b.SalesOrderNo, c.SalesOrderNo)
	}
	// Pool 10: STANDARD capped at 70% = 7 although 9 are free.
	if !opsNear(a.ATPQty, 7) || a.AllocationPlanID == nil || *a.AllocationPlanID != plan.Plan.ID || a.AllocationBucketPct == nil || *a.AllocationBucketPct != 70 {
		t.Fatalf("STANDARD line: %+v", a)
	}
	if a.ConstraintType != "PRODUCT_ALLOCATION" && a.ConstraintType != "HORIZON" {
		t.Fatalf("STANDARD line constraint = %s", a.ConstraintType)
	}
	// STRATEGIC bucket 3 and 3 free units left.
	if !opsNear(b.ATPQty, 3) || b.AllocationBucketPct == nil || *b.AllocationBucketPct != 30 {
		t.Fatalf("STRATEGIC line: %+v", b)
	}
	// OTHER has no bucket: no ATP at all.
	if !opsNear(c.ATPQty, 0) || c.AllocationBucketPct != nil || c.AllocationPlanID == nil {
		t.Fatalf("OTHER line: %+v", c)
	}
	total := 0.0
	for _, l := range res.Lines {
		total += l.ATPQty
		if !opsNear(l.AllocatedQty+l.ATPQty+l.CTPQty+l.BackorderQty, l.OpenQty) {
			t.Errorf("coverage mismatch on rank %d: %+v", l.RankNo, l)
		}
		if l.BackorderQty > 1e-9 && l.Decision != "BACKORDER" {
			t.Errorf("rank %d decision %s with backorder %.2f", l.RankNo, l.Decision, l.BackorderQty)
		}
	}
	if total > 10+1e-9 {
		t.Fatalf("ATP over-promised: %.2f > 10 on hand", total)
	}
}

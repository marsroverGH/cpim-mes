package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// CTP material readiness: free stock vs requirement, firm PO/WO receipts vs
// lead-time-offset hypothetical replenishment, source classification and
// run-scoped cumulative usage.
func TestPlanCTPMaterialReady(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	start := planDay(2027, 3, 1)
	p := planItem(t, f, domain.Item{Code: "PLX-P"})
	c1 := planItem(t, f, domain.Item{Code: "PLX-C1", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 5})
	c2 := planItem(t, f, domain.Item{Code: "PLX-C2", Type: domain.ItemTypeRawMaterial, LeadTimeDays: 10})
	c3 := planItem(t, f, domain.Item{Code: "PLX-C3", Type: domain.ItemTypeSubAssembly, LeadTimeDays: 2})
	leaf := planItem(t, f, domain.Item{Code: "PLX-LEAF"})
	planBOM(t, f, p.ID, c1.ID, 1, 0)
	planBOM(t, f, p.ID, c2.ID, 2, 0)
	planBOM(t, f, p.ID, c3.ID, 1, 0)
	planReceive(t, f, c1.ID, 3)
	planReceive(t, f, c2.ID, 100)
	// Firm WO supply of the sub-assembly due 03-04 (later than its 2-day lead time).
	if err := f.Svc.WorkOrders.Create(f.Ctx, &domain.WorkOrder{OrderNo: "PLX-WO", ItemID: c3.ID, Quantity: 10, StartDate: start, DueDate: planDay(2027, 3, 4), Status: "RELEASED"}); err != nil {
		t.Fatal(err)
	}

	res, err := f.Svc.Backorders.ctp.MaterialReady(f.Ctx, p.ID, 5, start.Add(10*time.Hour), 30)
	if err != nil {
		t.Fatalf("material ready: %v", err)
	}
	// C1: 5 needed, 3 free, no PO -> start + 5 = 03-06. C2: covered. C3: min(start+2, WO 03-04) = 03-03.
	planAssertDay(t, "ready date", res.ReadyDate, planDay(2027, 3, 6))
	if res.Source != "CTP_MIXED" {
		t.Fatalf("source = %s, want CTP_MIXED (purchase + production shortage)", res.Source)
	}
	comps, _ := res.Detail["components"].([]map[string]any)
	if len(comps) != 3 {
		t.Fatalf("component details = %#v", res.Detail)
	}
	for _, c := range comps {
		switch c["itemCode"] {
		case "PLX-C1":
			planAssertNear(t, "C1 shortage", c["shortage"].(float64), 2)
			if c["readyDate"] != "2027-03-06" {
				t.Fatalf("C1 ready = %v", c["readyDate"])
			}
		case "PLX-C2":
			if c["readyDate"] != "2027-03-01" || c["shortage"] != 0 {
				t.Fatalf("C2 detail = %v", c)
			}
		case "PLX-C3":
			if c["readyDate"] != "2027-03-03" {
				t.Fatalf("C3 ready = %v", c["readyDate"])
			}
		}
	}

	// A PO for C1 arriving 03-04 beats the 5-day hypothetical lead time.
	planPO(t, f, "PLX-PO", c1.ID, 10, planDay(2027, 3, 4))
	res, err = f.Svc.Backorders.ctp.MaterialReady(f.Ctx, p.ID, 5, start, 30)
	if err != nil {
		t.Fatal(err)
	}
	planAssertDay(t, "ready date with PO", res.ReadyDate, planDay(2027, 3, 4))

	// Run-scoped usage: 99 units of C2 already promised by an earlier line.
	usage := map[uuid.UUID]float64{c2.ID: 99}
	res, err = f.Svc.Backorders.ctp.MaterialReadyWithUsage(f.Ctx, p.ID, 1, start, 30, usage)
	if err != nil {
		t.Fatal(err)
	}
	planAssertDay(t, "ready date with usage", res.ReadyDate, planDay(2027, 3, 11))
	planAssertNear(t, "usage accumulated", usage[c2.ID], 101)
	planAssertNear(t, "C1 usage accumulated", usage[c1.ID], 1)

	// No BOM: production can start immediately; default horizon.
	res, err = f.Svc.Backorders.ctp.MaterialReady(f.Ctx, leaf.ID, 1, start, 0)
	if err != nil || res.Source != "CTP_PRODUCTION" || !res.ReadyDate.Equal(start) {
		t.Fatalf("leaf = %+v, %v", res, err)
	}
	// Pure purchase shortage.
	pp := planItem(t, f, domain.Item{Code: "PLX-PP"})
	planBOM(t, f, pp.ID, c2.ID, 200, 0)
	res, err = f.Svc.Backorders.ctp.MaterialReady(f.Ctx, pp.ID, 1, start, 30)
	if err != nil || res.Source != "CTP_PURCHASE" {
		t.Fatalf("purchase-only shortage = %+v, %v", res, err)
	}
}

// A component used at two BOM levels must be netted against its total requirement,
// not checked per level against the same free stock.
func TestPlanCTPComponentAtTwoLevels(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	start := planDay(2027, 3, 1)
	p := planItem(t, f, domain.Item{Code: "PLX2-P"})
	s := planItem(t, f, domain.Item{Code: "PLX2-S", Type: domain.ItemTypeSubAssembly})
	x := planItem(t, f, domain.Item{Code: "PLX2-X", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 9})
	planBOM(t, f, p.ID, s.ID, 1, 0)
	planBOM(t, f, p.ID, x.ID, 1, 0)
	planBOM(t, f, s.ID, x.ID, 1, 0)
	planReceive(t, f, x.ID, 1)
	res, err := f.Svc.Backorders.ctp.MaterialReady(f.Ctx, p.ID, 1, start, 30)
	if err != nil {
		t.Fatal(err)
	}
	// One P needs 1 X directly + 1 X through S = 2 X, but only 1 is free.
	if res.ReadyDate.Before(start.AddDate(0, 0, 9)) {
		t.Fatalf("component at two BOM levels checked twice against the same free stock; ready=%s source=%s",
			res.ReadyDate.Format("2006-01-02"), res.Source)
	}
}

// ATP-then-CTP order promising with capacity simulation, acceptance with hash
// re-validation, and the CAPACITY / HORIZON constraint outcomes.
func TestPlanOrderPromisingCheckAndAccept(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-promise", domain.RolePlanner)
	cust := planCustomer(t, f, actor, "PLO-C")
	cal := planCalendar(t, f, "PLO-CAL", 480)
	wc := planWC(t, f, "PLO-WC", 480, 1, 1, &cal.ID)
	fg := planItem(t, f, domain.Item{Code: "PLO-FG"})
	comp := planItem(t, f, domain.Item{Code: "PLO-C", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 3})
	planBOM(t, f, fg.ID, comp.ID, 1, 0)
	planRouting(t, f, fg.ID, domain.RoutingOperation{WorkCenterID: wc.ID, RunMinutesPerUnit: 1})
	planReceive(t, f, fg.ID, 4)
	planReceive(t, f, comp.ID, 100)
	today := planToday()
	requested := today.AddDate(0, 0, 5)

	so := planConfirmedOrder(t, f, actor, cust.ID, "PLO-SO-1", fg.ID, 10, requested)
	res, err := f.Svc.OrderPromising.Check(f.Ctx, so.Order.ID, OrderPromiseCheckInput{HorizonDays: 30}, actor)
	if err != nil {
		t.Fatalf("promise check: %v", err)
	}
	if res.Run.Status != "SUCCEEDED" || res.Run.ResultHash == nil || res.Run.Strategy != "ATP_THEN_CTP" {
		t.Fatalf("run = %+v", res.Run)
	}
	if len(res.Lines) != 1 {
		t.Fatalf("lines = %d", len(res.Lines))
	}
	l := res.Lines[0]
	// 4 from ATP (on hand), 6 by CTP: material ready today, capacity today -> promise on requested date.
	planAssertNear(t, "ATP qty", l.ATPQty, 4)
	planAssertNear(t, "CTP qty", l.CTPQty, 6)
	if l.PromiseMethod != "ATP_CTP" || l.ConstraintType != "NONE" || l.EarliestFullDate == nil {
		t.Fatalf("line = %+v", l)
	}
	planAssertDay(t, "earliest full", *l.EarliestFullDate, requested)
	planAssertDay(t, "material ready", *l.MaterialReadyDate, today)
	if len(res.Confirmations) != 2 || res.Confirmations[0].Source != "ATP" || res.Confirmations[1].Source != "CTP_PRODUCTION" {
		t.Fatalf("confirmations = %+v", res.Confirmations)
	}
	planAssertNear(t, "conf 1", res.Confirmations[0].Quantity, 4)
	planAssertNear(t, "conf 2", res.Confirmations[1].Quantity, 6)

	_, err = f.Svc.OrderPromising.Accept(f.Ctx, so.Order.ID, OrderPromiseAcceptInput{}, actor)
	planErrCode(t, "accept without run", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.OrderPromising.Accept(f.Ctx, uuid.New(), OrderPromiseAcceptInput{RunID: res.Run.ID}, actor)
	planErrCode(t, "accept other order", err, domain.ErrCodeConflict)
	acc, err := f.Svc.OrderPromising.Accept(f.Ctx, so.Order.ID, OrderPromiseAcceptInput{RunID: res.Run.ID}, actor)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.Acceptance == nil || acc.Acceptance.ResultHash != *res.Run.ResultHash {
		t.Fatalf("acceptance = %+v", acc.Acceptance)
	}
	again, err := f.Svc.OrderPromising.Accept(f.Ctx, so.Order.ID, OrderPromiseAcceptInput{RunID: res.Run.ID}, actor)
	if err != nil || again.Acceptance == nil || again.Acceptance.ID != acc.Acceptance.ID {
		t.Fatalf("idempotent accept = %+v, %v", again, err)
	}
	detail, err := f.Svc.SalesOrders.Get(f.Ctx, so.Order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Order.PromisedDate == nil || detail.Lines[0].PromisedDate == nil {
		t.Fatalf("promised dates not written: %+v", detail.Order)
	}
	planAssertDay(t, "header promised", *detail.Order.PromisedDate, requested)
	planAssertDay(t, "line promised", *detail.Lines[0].PromisedDate, requested)
	if runs, err := f.Svc.OrderPromising.ListRuns(f.Ctx, so.Order.ID); err != nil || len(runs) != 1 {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}

	// A run that cannot be accepted: 5000 minutes of work in a 5-day horizon -> CAPACITY.
	big := planConfirmedOrder(t, f, actor, cust.ID, "PLO-SO-2", fg.ID, 5000, today.AddDate(0, 0, 2))
	res2, err := f.Svc.OrderPromising.Check(f.Ctx, big.Order.ID, OrderPromiseCheckInput{HorizonDays: 5}, actor)
	if err != nil {
		t.Fatal(err)
	}
	l2 := res2.Lines[0]
	if l2.ConstraintType != "CAPACITY" || l2.CTPQty != 0 || l2.EarliestFullDate != nil {
		t.Fatalf("capacity-constrained line = %+v", l2)
	}
	_, err = f.Svc.OrderPromising.Accept(f.Ctx, big.Order.ID, OrderPromiseAcceptInput{RunID: res2.Run.ID}, actor)
	planErrCode(t, "accept partial promise", err, domain.ErrCodeConflict)

	// Material lead time beyond the horizon -> HORIZON.
	fg2 := planItem(t, f, domain.Item{Code: "PLO-FG2"})
	slow := planItem(t, f, domain.Item{Code: "PLO-SLOW", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 30})
	planBOM(t, f, fg2.ID, slow.ID, 1, 0)
	planRouting(t, f, fg2.ID, domain.RoutingOperation{WorkCenterID: wc.ID, RunMinutesPerUnit: 1})
	so3 := planConfirmedOrder(t, f, actor, cust.ID, "PLO-SO-3", fg2.ID, 1, today.AddDate(0, 0, 3))
	res3, err := f.Svc.OrderPromising.Check(f.Ctx, so3.Order.ID, OrderPromiseCheckInput{HorizonDays: 10}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if l3 := res3.Lines[0]; l3.ConstraintType != "HORIZON" || l3.PromiseMethod != "UNAVAILABLE" || l3.MaterialReadyDate == nil {
		t.Fatalf("horizon-constrained line = %+v", l3)
	}

	_, err = f.Svc.OrderPromising.Check(f.Ctx, so.Order.ID, OrderPromiseCheckInput{HorizonDays: 367}, actor)
	planErrCode(t, "horizon > 366", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.OrderPromising.Check(f.Ctx, so.Order.ID, OrderPromiseCheckInput{}, SalesOrderActor{})
	planErrCode(t, "anonymous", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.OrderPromising.GetRun(f.Ctx, uuid.New())
	planErrCode(t, "unknown run", err, domain.ErrCodeNotFound)
	if _, err := f.Svc.SalesOrders.Cancel(f.Ctx, so3.Order.ID, actor); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_, err = f.Svc.OrderPromising.Check(f.Ctx, so3.Order.ID, OrderPromiseCheckInput{}, actor)
	planErrCode(t, "cancelled order", err, domain.ErrCodeConflict)
}

func TestPlanProductAllocationPlansAndPriorities(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-alloc", domain.RolePlanner)
	classes, err := f.Svc.ProductAllocation.ListServiceClasses(f.Ctx)
	if err != nil || len(classes) < 2 {
		t.Fatalf("service classes = %+v, %v", classes, err)
	}
	for i := 1; i < len(classes); i++ {
		if classes[i-1].PriorityRank > classes[i].PriorityRank {
			t.Fatal("service classes not ordered by rank")
		}
	}
	c1, c2 := classes[0].Code, classes[1].Code
	fg := planItem(t, f, domain.Item{Code: "PLA-FG"})
	rm := planItem(t, f, domain.Item{Code: "PLA-RM", Type: domain.ItemTypeRawMaterial})
	valid := ProductAllocationPlanInput{ItemID: fg.ID, Name: " Q2 split ", EffectiveFrom: "2027-04-01", EffectiveTo: "2027-06-30",
		Buckets: []ProductAllocationBucketInput{{ServiceClassCode: c2, AllocationPct: 30, PriorityRank: 2}, {ServiceClassCode: " " + c1 + " ", AllocationPct: 70, PriorityRank: 1}}}
	bad := []struct {
		name string
		mut  func(*ProductAllocationPlanInput)
	}{
		{"no item", func(in *ProductAllocationPlanInput) { in.ItemID = uuid.Nil }},
		{"no name", func(in *ProductAllocationPlanInput) { in.Name = " " }},
		{"bad from", func(in *ProductAllocationPlanInput) { in.EffectiveFrom = "2027/04/01" }},
		{"missing to", func(in *ProductAllocationPlanInput) { in.EffectiveTo = "" }},
		{"to before from", func(in *ProductAllocationPlanInput) { in.EffectiveTo = "2027-03-31" }},
		{"no buckets", func(in *ProductAllocationPlanInput) { in.Buckets = nil }},
		{"pct > 100", func(in *ProductAllocationPlanInput) {
			in.Buckets = []ProductAllocationBucketInput{{ServiceClassCode: c1, AllocationPct: 101, PriorityRank: 1}}
		}},
		{"dup rank", func(in *ProductAllocationPlanInput) {
			in.Buckets = []ProductAllocationBucketInput{{ServiceClassCode: c1, AllocationPct: 50, PriorityRank: 1}, {ServiceClassCode: c2, AllocationPct: 50, PriorityRank: 1}}
		}},
		{"total 90", func(in *ProductAllocationPlanInput) {
			in.Buckets = []ProductAllocationBucketInput{{ServiceClassCode: c1, AllocationPct: 60, PriorityRank: 1}, {ServiceClassCode: c2, AllocationPct: 30, PriorityRank: 2}}
		}},
		{"unknown class", func(in *ProductAllocationPlanInput) {
			in.Buckets = []ProductAllocationBucketInput{{ServiceClassCode: "NOPE", AllocationPct: 100, PriorityRank: 1}}
		}},
		{"raw material", func(in *ProductAllocationPlanInput) { in.ItemID = rm.ID }},
	}
	for _, b := range bad {
		in := valid
		in.Buckets = append([]ProductAllocationBucketInput(nil), valid.Buckets...)
		b.mut(&in)
		_, err := f.Svc.ProductAllocation.CreatePlan(f.Ctx, in, actor)
		planErrCode(t, b.name, err, domain.ErrCodeBadRequest)
	}
	_, err = f.Svc.ProductAllocation.CreatePlan(f.Ctx, valid, SalesOrderActor{})
	planErrCode(t, "anonymous", err, domain.ErrCodeUnauthorized)

	plan, err := f.Svc.ProductAllocation.CreatePlan(f.Ctx, valid, actor)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.Plan.Status != "DRAFT" || plan.Plan.Name != "Q2 split" || len(plan.Buckets) != 2 || plan.Buckets[0].ServiceClassCode != c1 || !planNear(plan.Buckets[0].AllocationPct, 70) {
		t.Fatalf("plan = %+v", plan)
	}
	if pol, err := f.Svc.ProductAllocation.ActivePolicy(f.Ctx, fg.ID, planDay(2027, 5, 1)); err != nil || pol != nil {
		t.Fatalf("draft plan must not be active policy: %+v, %v", pol, err)
	}
	act, err := f.Svc.ProductAllocation.ActivatePlan(f.Ctx, plan.Plan.ID, actor)
	if err != nil || act.Plan.Status != "ACTIVE" || act.Plan.ActivatedBy == nil || *act.Plan.ActivatedBy != actor.Username {
		t.Fatalf("activate = %+v, %v", act, err)
	}
	_, err = f.Svc.ProductAllocation.ActivatePlan(f.Ctx, plan.Plan.ID, actor)
	planErrCode(t, "re-activate", err, domain.ErrCodeConflict)
	pol, err := f.Svc.ProductAllocation.ActivePolicy(f.Ctx, fg.ID, planDay(2027, 6, 30).Add(20*time.Hour))
	if err != nil || pol == nil || pol.Plan.ID != plan.Plan.ID || !planNear(pol.Buckets[c2].AllocationPct, 30) || pol.Buckets[c1].PriorityRank != 1 {
		t.Fatalf("active policy = %+v, %v", pol, err)
	}
	if pol, err := f.Svc.ProductAllocation.ActivePolicy(f.Ctx, fg.ID, planDay(2027, 7, 1)); err != nil || pol != nil {
		t.Fatalf("policy outside effectivity = %+v, %v", pol, err)
	}
	if list, err := f.Svc.ProductAllocation.ListPlans(f.Ctx); err != nil || len(list) != 1 || len(list[0].Buckets) != 2 {
		t.Fatalf("list plans = %+v, %v", list, err)
	}
	de, err := f.Svc.ProductAllocation.DeactivatePlan(f.Ctx, plan.Plan.ID, actor)
	if err != nil || de.Plan.Status != "INACTIVE" {
		t.Fatalf("deactivate = %+v, %v", de, err)
	}
	_, err = f.Svc.ProductAllocation.DeactivatePlan(f.Ctx, plan.Plan.ID, actor)
	planErrCode(t, "re-deactivate", err, domain.ErrCodeConflict)
	_, err = f.Svc.ProductAllocation.ActivatePlan(f.Ctx, plan.Plan.ID, SalesOrderActor{})
	planErrCode(t, "anonymous activate", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.ProductAllocation.DeactivatePlan(f.Ctx, plan.Plan.ID, SalesOrderActor{})
	planErrCode(t, "anonymous deactivate", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.ProductAllocation.GetPlan(f.Ctx, uuid.New())
	planErrCode(t, "unknown plan", err, domain.ErrCodeNotFound)

	// Customer service class.
	cust := planCustomer(t, f, actor, "PLA-C")
	_, err = f.Svc.ProductAllocation.SetCustomerServiceClass(f.Ctx, cust.ID, CustomerServiceClassInput{ServiceClassCode: " "}, actor)
	planErrCode(t, "empty class", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.ProductAllocation.SetCustomerServiceClass(f.Ctx, cust.ID, CustomerServiceClassInput{ServiceClassCode: "NOPE"}, actor)
	planErrCode(t, "unknown class", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.ProductAllocation.SetCustomerServiceClass(f.Ctx, uuid.New(), CustomerServiceClassInput{ServiceClassCode: c1}, actor)
	planErrCode(t, "unknown customer", err, domain.ErrCodeNotFound)
	_, err = f.Svc.ProductAllocation.SetCustomerServiceClass(f.Ctx, cust.ID, CustomerServiceClassInput{ServiceClassCode: c1}, SalesOrderActor{})
	planErrCode(t, "anonymous class", err, domain.ErrCodeUnauthorized)
	cu, err := f.Svc.ProductAllocation.SetCustomerServiceClass(f.Ctx, cust.ID, CustomerServiceClassInput{ServiceClassCode: " " + c2}, actor)
	if err != nil || cu.ServiceClassCode != c2 {
		t.Fatalf("set class = %+v, %v", cu, err)
	}

	// Sales order priority.
	so := planConfirmedOrder(t, f, actor, cust.ID, "PLA-SO", fg.ID, 1, planDay(2027, 4, 10))
	_, err = f.Svc.ProductAllocation.SetSalesOrderPriority(f.Ctx, so.Order.ID, SalesOrderPriorityInput{Priority: "urgent"}, actor)
	planErrCode(t, "bad priority", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.ProductAllocation.SetSalesOrderPriority(f.Ctx, uuid.New(), SalesOrderPriorityInput{Priority: "HIGH"}, actor)
	planErrCode(t, "unknown order", err, domain.ErrCodeNotFound)
	_, err = f.Svc.ProductAllocation.SetSalesOrderPriority(f.Ctx, so.Order.ID, SalesOrderPriorityInput{Priority: "HIGH"}, SalesOrderActor{})
	planErrCode(t, "anonymous priority", err, domain.ErrCodeUnauthorized)
	d, err := f.Svc.ProductAllocation.SetSalesOrderPriority(f.Ctx, so.Order.ID, SalesOrderPriorityInput{Priority: " expedite "}, actor)
	if err != nil || d.Order.Priority != "EXPEDITE" {
		t.Fatalf("priority = %+v, %v", d, err)
	}
	if _, err := f.Svc.SalesOrders.Cancel(f.Ctx, so.Order.ID, actor); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.ProductAllocation.SetSalesOrderPriority(f.Ctx, so.Order.ID, SalesOrderPriorityInput{Priority: "NORMAL"}, actor)
	planErrCode(t, "cancelled order priority", err, domain.ErrCodeConflict)
}

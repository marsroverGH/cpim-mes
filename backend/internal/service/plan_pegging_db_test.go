package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// Full pegging: a 10-unit sales order line is covered by FG inventory (3), a planned
// WO (5, late) and leaves 2 short; the WO's component requirement (2 x 5 = 10) is
// covered by component inventory (4) and a late PO (4) and leaves 2 short.
// Expected exceptions: 2 x MATERIAL_SHORTAGE, LATE_WORK_ORDER(2d), LATE_PURCHASE_ORDER(5d)
// and CAPACITY_UNSCHEDULED (the WO has no detailed schedule).
func TestPlanPeggingGraphExceptionsAndActions(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-pegging", domain.RolePlanner)
	cust := planCustomer(t, f, actor, "PLG-C")
	fg := planItem(t, f, domain.Item{Code: "PLG-FG"})
	rm := planItem(t, f, domain.Item{Code: "PLG-RM", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 5})
	planBOM(t, f, fg.ID, rm.ID, 2, 0)
	planReceive(t, f, fg.ID, 3)
	planReceive(t, f, rm.ID, 4)
	today := planToday()
	need := planDay(today.Year(), today.Month(), today.Day()).AddDate(0, 0, 5)
	wo := domain.WorkOrder{OrderNo: "PLG-WO", ItemID: fg.ID, Quantity: 5, StartDate: need.AddDate(0, 0, -3), DueDate: need.AddDate(0, 0, 2)}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatal(err)
	}
	po := planPO(t, f, "PLG-PO", rm.ID, 4, need.AddDate(0, 0, 5))
	so := planConfirmedOrder(t, f, actor, cust.ID, "PLG-SO", fg.ID, 10, need)

	res, err := f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{HorizonDays: 30}, actor)
	if err != nil {
		t.Fatalf("pegging run: %v", err)
	}
	if res.Run.Status != "SUCCEEDED" || res.Run.ResultHash == nil || res.Run.HorizonDays != 30 {
		t.Fatalf("run = %+v", res.Run)
	}
	nodeByKey := map[string]domain.PeggingNode{}
	keyByID := map[uuid.UUID]string{}
	for _, n := range res.Nodes {
		nodeByKey[n.NodeKey] = n
		keyByID[n.ID] = n.NodeKey
	}
	line := so.Lines[0]
	keys := struct{ so, sol, invFG, invRM, wo, req, po, shortTop, shortRM string }{
		so: "SO:" + so.Order.ID.String(), sol: "SOL:" + line.ID.String(),
		invFG: "INV:" + fg.ID.String(), invRM: "INV:" + rm.ID.String(), wo: "WO:" + wo.ID.String(),
		req: "REQ:WO:" + wo.ID.String() + ":" + rm.ID.String(), po: "PO:" + po.ID.String(),
		shortTop: "SHORT:TOP:" + line.ID.String(), shortRM: "SHORT:WO:" + wo.ID.String() + ":" + rm.ID.String(),
	}
	for _, k := range []string{keys.so, keys.sol, keys.invFG, keys.invRM, keys.wo, keys.req, keys.po, keys.shortTop, keys.shortRM} {
		if _, ok := nodeByKey[k]; !ok {
			t.Fatalf("missing node %s; nodes=%v", k, keyByID)
		}
	}
	type ek struct{ from, to, typ string }
	edges := map[ek]float64{}
	for _, e := range res.Edges {
		q := -1.0
		if e.Quantity != nil {
			q = *e.Quantity
		}
		edges[ek{keyByID[e.FromNodeID], keyByID[e.ToNodeID], e.EdgeType}] = q
	}
	wantEdges := map[ek]float64{
		{keys.so, keys.sol, "HAS_LINE"}:           10,
		{keys.sol, keys.invFG, "SUPPLIED_BY"}:     3,
		{keys.sol, keys.wo, "SUPPLIED_BY"}:        5,
		{keys.wo, keys.req, "REQUIRES_COMPONENT"}: 10,
		{keys.req, keys.invRM, "SUPPLIED_BY"}:     4,
		{keys.req, keys.po, "PURCHASED_BY"}:       4,
		{keys.req, keys.shortRM, "SHORT_BY"}:      2,
		{keys.sol, keys.shortTop, "SHORT_BY"}:     2,
	}
	for k, q := range wantEdges {
		got, ok := edges[k]
		if !ok {
			t.Fatalf("missing edge %v; edges=%v", k, edges)
		}
		planAssertNear(t, "edge "+k.typ+" "+k.to, got, q)
	}

	type exk struct{ typ, sev string }
	exc := map[exk]domain.PlanningException{}
	for _, e := range res.Exceptions {
		exc[exk{e.ExceptionType, e.Severity}] = e
		if e.CurrentStatus != "OPEN" {
			t.Fatalf("new exception status = %s", e.CurrentStatus)
		}
	}
	if e, ok := exc[exk{"LATE_WORK_ORDER", "WARNING"}]; !ok || e.ImpactDays != 2 {
		t.Fatalf("LATE_WORK_ORDER = %+v (present=%v); all=%v", e, ok, exc)
	}
	if e, ok := exc[exk{"LATE_PURCHASE_ORDER", "WARNING"}]; !ok || e.ImpactDays != 5 {
		t.Fatalf("LATE_PURCHASE_ORDER = %+v (present=%v)", e, ok)
	}
	shortages := 0
	for _, e := range res.Exceptions {
		if e.ExceptionType == "MATERIAL_SHORTAGE" {
			shortages++
			if e.Severity != "CRITICAL" {
				t.Fatalf("shortage severity = %s", e.Severity)
			}
		}
	}
	// The WO has no Detailed Scheduling evidence yet -> capacity cannot be confirmed.
	if _, ok := exc[exk{"CAPACITY_UNSCHEDULED", "CRITICAL"}]; !ok {
		t.Fatalf("CAPACITY_UNSCHEDULED missing; all=%v", exc)
	}
	if shortages != 2 || len(res.Exceptions) != 5 {
		t.Fatalf("exceptions = %d (shortages %d), want 5 (2 shortages)", len(res.Exceptions), shortages)
	}
	// Critical exceptions are listed first.
	if res.Exceptions[0].Severity != "CRITICAL" {
		t.Fatalf("first exception severity = %s", res.Exceptions[0].Severity)
	}

	// Re-running on unchanged data with the same horizon yields the same canonical hash.
	res2, err := f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{HorizonDays: 30}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if *res2.Run.ResultHash != *res.Run.ResultHash || res2.Run.ID == res.Run.ID {
		t.Fatalf("hash changed on identical data: %s vs %s", *res2.Run.ResultHash, *res.Run.ResultHash)
	}
	res3, err := f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Run.HorizonDays != defaultPeggingHorizonDays {
		t.Fatalf("default horizon = %d", res3.Run.HorizonDays)
	}
	runs, err := f.Svc.Pegging.ListRuns(f.Ctx, so.Order.ID)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}

	// Exception workflow: OPEN -> ACKNOWLEDGED -> RESOLVED -> OPEN.
	target := exc[exk{"LATE_PURCHASE_ORDER", "WARNING"}]
	cur, err := f.Svc.Pegging.ListCurrentExceptions(f.Ctx, "open", "warning", "late_purchase_order")
	if err != nil || len(cur) == 0 {
		t.Fatalf("filtered current exceptions = %d, %v", len(cur), err)
	}
	for _, c := range cur {
		if c.ExceptionType != "LATE_PURCHASE_ORDER" || c.Severity != "WARNING" {
			t.Fatalf("filter leak: %+v", c)
		}
	}
	targetID := cur[0].ID
	_, err = f.Svc.Pegging.ActOnException(f.Ctx, targetID, ExceptionActionInput{ActionType: "snooze"}, actor)
	planErrCode(t, "bad action", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.Pegging.ActOnException(f.Ctx, targetID, ExceptionActionInput{ActionType: "REOPEN"}, actor)
	planErrCode(t, "reopen open", err, domain.ErrCodeConflict)
	steps := []struct{ action, from, to string }{
		{"acknowledge", "OPEN", "ACKNOWLEDGED"},
		{"RESOLVE", "ACKNOWLEDGED", "RESOLVED"},
		{"REOPEN", "RESOLVED", "OPEN"},
	}
	for _, s := range steps {
		a, err := f.Svc.Pegging.ActOnException(f.Ctx, targetID, ExceptionActionInput{ActionType: s.action, Comment: " note "}, actor)
		if err != nil {
			t.Fatalf("%s: %v", s.action, err)
		}
		if a.FromStatus != s.from || a.ToStatus != s.to {
			t.Fatalf("%s recorded %s -> %s, want %s -> %s", s.action, a.FromStatus, a.ToStatus, s.from, s.to)
		}
	}
	_ = target
	_, err = f.Svc.Pegging.ActOnException(f.Ctx, uuid.New(), ExceptionActionInput{ActionType: "RESOLVE"}, actor)
	planErrCode(t, "unknown exception", err, domain.ErrCodeNotFound)
	_, err = f.Svc.Pegging.ActOnException(f.Ctx, targetID, ExceptionActionInput{ActionType: "RESOLVE"}, SalesOrderActor{})
	planErrCode(t, "anonymous action", err, domain.ErrCodeUnauthorized)

	// Guard rails.
	_, err = f.Svc.Pegging.Run(f.Ctx, uuid.Nil, PeggingRunInput{}, actor)
	planErrCode(t, "nil order", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{}, SalesOrderActor{})
	planErrCode(t, "anonymous run", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.Pegging.GetRun(f.Ctx, uuid.New())
	planErrCode(t, "unknown run", err, domain.ErrCodeNotFound)
	if _, err := f.Svc.SalesOrders.Cancel(f.Ctx, so.Order.ID, actor); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{}, actor)
	planErrCode(t, "cancelled order", err, domain.ErrCodeConflict)
	runs, _ = f.Svc.Pegging.ListRuns(f.Ctx, so.Order.ID)
	if len(runs) != 4 || runs[0].Status != "FAILED" {
		t.Fatalf("failed run not recorded: %+v", runs)
	}
}

// A promised date later than the requested date raises LATE_PROMISE; Scan pegs every
// open committed order and returns the current exception list.
func TestPlanPeggingLatePromiseAndScan(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-scan", domain.RolePlanner)
	cust := planCustomer(t, f, actor, "PLG2-C")
	fg := planItem(t, f, domain.Item{Code: "PLG2-FG"})
	planReceive(t, f, fg.ID, 100)
	req := planToday().AddDate(0, 0, 3)
	d, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: "PLG2-SO", CustomerID: cust.ID, RequestedDate: req.Format("2006-01-02"),
		Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: 2, UnitPrice: 1, PromisedDate: req.AddDate(0, 0, 8).Format("2006-01-02")}},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.SalesOrders.Confirm(f.Ctx, d.Order.ID, actor); err != nil {
		t.Fatal(err)
	}
	scan, err := f.Svc.Pegging.Scan(f.Ctx, ExceptionScanInput{HorizonDays: 999}, actor)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var mine *domain.PeggingRun
	for i := range scan.PeggingRuns {
		r := scan.PeggingRuns[i]
		if r.SalesOrderID == d.Order.ID {
			mine = &r
		}
		if r.HorizonDays != 366 {
			t.Fatalf("horizon not clamped to 366: %d", r.HorizonDays)
		}
	}
	if mine == nil {
		t.Fatal("scan did not peg the confirmed order")
	}
	found := false
	for _, e := range scan.Exceptions {
		if e.SalesOrderID == d.Order.ID {
			if e.ExceptionType != "LATE_PROMISE" || e.Severity != "CRITICAL" || e.ImpactDays != 8 {
				t.Fatalf("unexpected exception for fully stocked order: %+v", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("LATE_PROMISE exception missing from scan result")
	}
	_, err = f.Svc.Pegging.Scan(f.Ctx, ExceptionScanInput{}, SalesOrderActor{})
	planErrCode(t, "anonymous scan", err, domain.ErrCodeUnauthorized)
}

// Planned (MRP) supply pegging through the active Detailed Schedule: the planned
// order is late because a breakdown removes a whole day of capacity, so the graph
// carries PLANNED_ORDER -> DETAILED_SCHEDULE -> WORK_CENTER -> MAINTENANCE_EVENT
// evidence plus CAPACITY_LATE and BREAKDOWN_CAPACITY exceptions; the planned
// order's live BOM requirement is covered by an on-time PO.
func TestPlanPeggingPlannedSupplyWithScheduleEvidence(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-peg-plan", domain.RolePlanner)
	cust := planCustomer(t, f, actor, "PLG3-C")
	cal := planCalendar(t, f, "PLG3-CAL", 480)
	wc := planWC(t, f, "PLG3-WC", 480, 1, 1, &cal.ID)
	fg := planItem(t, f, domain.Item{Code: "PLG3-FG"})
	rm := planItem(t, f, domain.Item{Code: "PLG3-RM", Type: domain.ItemTypePurchasedPart, LeadTimeDays: 1})
	planBOM(t, f, fg.ID, rm.ID, 1, 0)
	planRouting(t, f, fg.ID, domain.RoutingOperation{WorkCenterID: wc.ID, RunMinutesPerUnit: 60})
	now := time.Now().UTC()
	day0 := planDay(now.Year(), now.Month(), now.Day())
	planMPS(t, f, fg.ID, day0.AddDate(0, 0, 3), 10)
	po := planPO(t, f, "PLG3-PO", rm.ID, 100, day0.AddDate(0, 0, 1))
	planReceive(t, f, rm.ID, 5)
	// Firm, released WO for 2 FG on day+1 (reserves 2 RM via its BOM snapshot).
	wo := domain.WorkOrder{OrderNo: "PLG3-WO", ItemID: fg.ID, Quantity: 2, StartDate: day0.AddDate(0, 0, 1), DueDate: day0.AddDate(0, 0, 1)}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.Workflow.ReleaseWorkOrder(f.Ctx, wo.ID); err != nil {
		t.Fatalf("release WO: %v", err)
	}
	// Breakdown for the whole (UTC) day of the planned order's release.
	if _, err := f.Svc.Maintenance.Create(f.Ctx, MaintenanceEventInput{WorkCenterID: wc.ID, EventType: "BREAKDOWN", StartAt: day0.AddDate(0, 0, 3), EndAt: day0.AddDate(0, 0, 4), UnavailableMachines: 1}, actor); err != nil {
		t.Fatal(err)
	}
	ds, err := f.Svc.CRP.DetailedSchedule(f.Ctx, DetailedScheduleRequest{StartDate: day0, HorizonDays: 14}, CRPActor{UserID: actor.UserID, Username: actor.Username})
	if err != nil {
		t.Fatalf("detailed schedule: %v", err)
	}
	var planned, firm domain.DetailedScheduleOrder
	for _, o := range ds.Orders {
		if o.ItemID == fg.ID && o.SourceType == "MRP_PLANNED" {
			planned = o
		}
		if o.ItemID == fg.ID && o.SourceType == "FIRM_WO" {
			firm = o
		}
	}
	// Firm WO: 120 minutes on day+1 08:00-10:00.
	planAssertTime(t, "firm end", firm.ScheduledEnd, day0.AddDate(0, 0, 1).Add(10*time.Hour))
	if ds.Summary.FirmOrders != 1 || firm.ScheduleStatus != "ON_TIME" {
		t.Fatalf("firm WO schedule = %+v / %+v", firm, ds.Summary)
	}
	// MRP nets the WO: 10 - 2 = 8 planned = 480 minutes; day+3 is lost to the breakdown,
	// so it runs day+4 08:00-16:00.
	planAssertNear(t, "planned qty", planned.Quantity, 8)
	planAssertTime(t, "planned end", planned.ScheduledEnd, day0.AddDate(0, 0, 4).Add(16*time.Hour))

	need := day0.AddDate(0, 0, 2)
	so := planConfirmedOrder(t, f, actor, cust.ID, "PLG3-SO", fg.ID, 10, need)
	res, err := f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{HorizonDays: 30}, actor)
	if err != nil {
		t.Fatalf("pegging: %v", err)
	}
	types := map[string]int{}
	keyByID := map[uuid.UUID]string{}
	for _, n := range res.Nodes {
		types[n.NodeType]++
		keyByID[n.ID] = n.NodeKey
	}
	if types["DETAILED_SCHEDULE"] != 2 || types["WORK_ORDER"] != 1 {
		t.Fatalf("expected WO and planned-order schedule evidence; node types = %v", types)
	}
	for _, typ := range []string{"PLANNED_ORDER", "DETAILED_SCHEDULE", "WORK_CENTER", "MAINTENANCE_EVENT", "PURCHASE_ORDER", "ITEM"} {
		if types[typ] == 0 {
			t.Fatalf("missing %s node; node types = %v", typ, types)
		}
	}
	if types["SHORTAGE"] != 0 {
		t.Fatalf("unexpected shortage nodes: %v", types)
	}
	var plannedQty, purchased, fromWO float64
	for _, e := range res.Edges {
		switch e.EdgeType {
		case "SUPPLIED_BY":
			if keyByID[e.ToNodeID] == "WO:"+wo.ID.String() {
				fromWO = *e.Quantity
			}
		case "PLANNED_SUPPLY":
			plannedQty = *e.Quantity
		case "PURCHASED_BY":
			purchased = *e.Quantity
			if keyByID[e.ToNodeID] != "PO:"+po.ID.String() {
				t.Fatalf("purchased from %s", keyByID[e.ToNodeID])
			}
		}
	}
	// 10 = 2 from the firm WO + 8 from the planned order.
	planAssertNear(t, "firm WO pegged", fromWO, 2)
	planAssertNear(t, "planned supply pegged", plannedQty, 8)
	// RM usable 5 - reserved 2 = 3 available; WO snapshot requirement 2 takes 2, the planned
	// order's 8 takes the remaining 1 from stock and 7 from the PO.
	planAssertNear(t, "component purchased", purchased, 7)
	exc := map[string]domain.PlanningException{}
	for _, e := range res.Exceptions {
		exc[e.ExceptionType] = e
	}
	// Impact days are counted on the calendar date of the scheduled end as read back from
	// PostgreSQL (time.Local): 2 days under TZ=UTC, 3 under e.g. Asia/Tokyo (see report).
	endLocal := planned.ScheduledEnd.In(time.Local)
	wantImpact := int(planDay(endLocal.Year(), endLocal.Month(), endLocal.Day()).Sub(need).Hours() / 24)
	if e, ok := exc["CAPACITY_LATE"]; !ok || e.ImpactDays != wantImpact || e.Severity != "WARNING" {
		t.Fatalf("CAPACITY_LATE = %+v (present=%v); all=%v", e, ok, exc)
	}
	if e, ok := exc["BREAKDOWN_CAPACITY"]; !ok || e.Severity != "CRITICAL" {
		t.Fatalf("BREAKDOWN_CAPACITY = %+v (present=%v)", e, ok)
	}
	if _, ok := exc["LATE_PURCHASE_ORDER"]; ok {
		t.Fatal("on-time PO must not raise LATE_PURCHASE_ORDER")
	}
}

// An active FIXED inventory policy drives both MRP netting (safety-stock target) and
// the pegging SAFETY_STOCK_BREACH root cause.
func TestPlanInventoryPolicyInMRPAndPegging(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := planSOActor(t, f, "plan-peg-policy", domain.RolePlanner)
	cust := planCustomer(t, f, actor, "PLG4-C")
	fg := planItem(t, f, domain.Item{Code: "PLG4-FG", SafetyStock: 1})
	planReceive(t, f, fg.ID, 10)
	today := planToday().Format("2006-01-02")
	ss := 50.0
	v, err := f.Svc.InventoryPolicy.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: fg.ID, PolicyMethod: "FIXED", ReplenishmentMethod: "SAFETY_STOCK", ServiceLevel: 0.95, DemandWindowDays: 7, MinHistoryDays: 1, OrderCycleDays: 7, FixedSafetyStock: &ss, EffectiveFrom: today}, actor)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if _, err := f.Svc.InventoryPolicy.ActivateVersion(f.Ctx, v.ID, actor); err != nil {
		t.Fatalf("activate policy: %v", err)
	}
	if _, err := f.Svc.InventoryPolicy.Refresh(f.Ctx, InventoryPolicyRefreshInput{AsOfDate: today}, actor); err != nil {
		t.Fatalf("refresh policy: %v", err)
	}

	// MRP: the policy (50), not the item master (1), is the safety-stock target: 50 - 10 = 40.
	rows, err := f.Svc.MRP.Run(f.Ctx, MRPRequest{StartDate: planToday(), HorizonDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	got := planMRPRowsFor(rows, fg.ID)
	if len(got) != 1 {
		t.Fatalf("MRP rows = %d", len(got))
	}
	planAssertNear(t, "policy safety stock", got[0].SafetyStockTarget, 50)
	planAssertNear(t, "planned receipt", got[0].PlannedReceipt, 40)
	if got[0].InventoryPolicyID == nil || *got[0].InventoryPolicyID != v.ID {
		t.Fatalf("policy id = %v, want %s", got[0].InventoryPolicyID, v.ID)
	}

	so := planConfirmedOrder(t, f, actor, cust.ID, "PLG4-SO", fg.ID, 1, planToday().AddDate(0, 0, 7))
	res, err := f.Svc.Pegging.Run(f.Ctx, so.Order.ID, PeggingRunInput{HorizonDays: 30}, actor)
	if err != nil {
		t.Fatal(err)
	}
	policyNode := false
	for _, n := range res.Nodes {
		if n.NodeType == "INVENTORY_POLICY" && n.EntityID != nil && *n.EntityID == v.ID {
			policyNode = true
		}
	}
	if !policyNode {
		t.Fatal("INVENTORY_POLICY node missing")
	}
	protected := false
	for _, e := range res.Edges {
		if e.EdgeType == "PROTECTED_BY" && e.Quantity != nil && *e.Quantity == 50 {
			protected = true
		}
	}
	if !protected {
		t.Fatal("PROTECTED_BY edge (50) missing")
	}
	breach := false
	for _, e := range res.Exceptions {
		if e.ExceptionType == "SAFETY_STOCK_BREACH" && e.Severity == "CRITICAL" {
			breach = true
		}
	}
	if !breach {
		t.Fatalf("SAFETY_STOCK_BREACH missing: %+v", res.Exceptions)
	}
}

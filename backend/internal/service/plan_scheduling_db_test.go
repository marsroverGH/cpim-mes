package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func planAt(day time.Time, hh, mm int) time.Time {
	return day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

func planAssertTime(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s is nil, want %s", what, want.Format(time.RFC3339))
	}
	if !got.Equal(want) {
		t.Fatalf("%s = %s, want %s", what, got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// Finite forward CRP: firm WO first, then MRP planned orders by due date, with
// carry-over into the next day, routing precedence, LATE/ON_TIME/PARTIAL statuses
// and finite load rows that never exceed 100%.
func TestPlanFiniteScheduleForwardAllocation(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := f.user(t, "plan-finite", domain.RolePlanner)
	cal := planCalendar(t, f, "PLF-CAL", 480)
	// eff 1 * util 0.5 -> 240 load minutes per 480 clock minutes (rate 0.5).
	wcA := planWC(t, f, "PLF-A", 480, 1, 0.5, &cal.ID)
	wcB := planWC(t, f, "PLF-B", 480, 1, 1, &cal.ID)
	wcC := planWC(t, f, "PLF-C", 480, 1, 1, &cal.ID)
	fg := planItem(t, f, domain.Item{Code: "PLF-FG"})
	planRouting(t, f, fg.ID, domain.RoutingOperation{WorkCenterID: wcA.ID, RunMinutesPerUnit: 1, Description: "assemble"})
	g2 := planItem(t, f, domain.Item{Code: "PLF-G2"})
	planRouting(t, f, g2.ID,
		domain.RoutingOperation{WorkCenterID: wcB.ID, SetupMinutes: 60, RunMinutesPerUnit: 1},
		domain.RoutingOperation{WorkCenterID: wcC.ID, RunMinutesPerUnit: 2},
	)
	d := func(n int) time.Time { return planDay(2027, 3, n) }

	// Firm WO (30 units, 03-01) released through the canonical workflow (creates wo_operations).
	wo := domain.WorkOrder{OrderNo: "PLF-WO-1", ItemID: fg.ID, Quantity: 30, StartDate: d(1), DueDate: d(1)}
	if err := f.Svc.WorkOrders.Create(f.Ctx, &wo); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.Workflow.ReleaseWorkOrder(f.Ctx, wo.ID); err != nil {
		t.Fatalf("release WO: %v", err)
	}
	planMPS(t, f, fg.ID, d(2), 300)
	planMPS(t, f, fg.ID, d(3), 100)
	planMPS(t, f, fg.ID, d(6), 2000)
	planMPS(t, f, g2.ID, d(4), 60)

	res, err := f.Svc.CRP.FiniteSchedule(f.Ctx, CRPFiniteRequest{StartDate: d(1), HorizonDays: 7}, CRPActor{UserID: planner.ID, Username: planner.Username})
	if err != nil {
		t.Fatalf("finite schedule: %v", err)
	}
	if res.Run.Status != "COMPLETE" || res.Run.Mode != crpFiniteMode || res.Run.HorizonDays != 7 {
		t.Fatalf("run = %+v", res.Run)
	}
	orders := map[string]domain.CRPScheduleOrder{}
	for _, o := range res.Orders {
		orders[o.SourceRef] = o
	}
	// Firm WO: 30 load at rate 0.5 -> 60 clock minutes, 08:00-09:00 on 03-01.
	firm := orders["PLF-WO-1"]
	if firm.SourceType != "FIRM_WO" || firm.ScheduleStatus != "ON_TIME" || firm.Priority != 20 {
		t.Fatalf("firm order = %+v", firm)
	}
	planAssertTime(t, "firm start", firm.ScheduledStart, planAt(d(1), 8, 0))
	planAssertTime(t, "firm end", firm.ScheduledEnd, planAt(d(1), 9, 0))

	// MRP nets the firm WO receipt: 03-02 planned = 300 - 30 = 270.
	o1 := orders["MRP:PLF-FG:2027-03-02"]
	planAssertNear(t, "planned qty 03-02", o1.Quantity, 270)
	// 240 on 03-02 (08:00-16:00) + 30 on 03-03 (08:00-09:00) -> LATE by 9h + 1s.
	if o1.ScheduleStatus != "LATE" {
		t.Fatalf("order 03-02 status = %s", o1.ScheduleStatus)
	}
	planAssertTime(t, "o1 start", o1.ScheduledStart, planAt(d(2), 8, 0))
	planAssertTime(t, "o1 end", o1.ScheduledEnd, planAt(d(3), 9, 0))
	planAssertNear(t, "o1 tardy", o1.TardyMinutes, 9*60+1.0/60)
	planAssertNear(t, "o1 scheduled", o1.ScheduledMinutes, 270)
	// 100 load -> 200 clock minutes right after o1 on 03-03: 09:00-12:20, ON_TIME.
	o2 := orders["MRP:PLF-FG:2027-03-03"]
	if o2.ScheduleStatus != "ON_TIME" {
		t.Fatalf("order 03-03 status = %s", o2.ScheduleStatus)
	}
	planAssertTime(t, "o2 start", o2.ScheduledStart, planAt(d(3), 9, 0))
	planAssertTime(t, "o2 end", o2.ScheduledEnd, planAt(d(3), 12, 20))
	// 2000 due 03-06 but only 03-06/03-07 (2 x 240) remain inside the horizon -> PARTIAL.
	o3 := orders["MRP:PLF-FG:2027-03-06"]
	if o3.ScheduleStatus != "PARTIAL" {
		t.Fatalf("order 03-06 status = %s", o3.ScheduleStatus)
	}
	planAssertNear(t, "o3 scheduled", o3.ScheduledMinutes, 480)
	planAssertNear(t, "o3 unscheduled", o3.UnscheduledMinutes, 1520)
	// Two-operation routing: op20 cannot start before op10 ends.
	g := orders["MRP:PLF-G2:2027-03-04"]
	if g.ScheduleStatus != "ON_TIME" {
		t.Fatalf("G2 status = %s", g.ScheduleStatus)
	}
	planAssertNear(t, "G2 required", g.RequiredMinutes, 120+120)
	var op10End, op20Start time.Time
	for _, s := range res.Segments {
		if s.SourceRef == g.SourceRef && s.OperationSeq == 10 {
			op10End = s.EndAt
		}
		if s.SourceRef == g.SourceRef && s.OperationSeq == 20 {
			op20Start = s.StartAt
		}
	}
	if !op10End.Equal(planAt(d(4), 10, 0)) || !op20Start.Equal(op10End) {
		t.Fatalf("routing precedence: op10 end %s, op20 start %s", op10End, op20Start)
	}

	// Finite loads on PLF-A: 03-01 30, 03-02 240 (100%), 03-03 130.
	loads := map[string]domain.CapacityLoadRow{}
	for _, l := range res.Loads {
		if l.WorkCenterID == wcA.ID {
			loads[l.Date.Format("01-02")] = l
		}
		if l.LoadPct > 100+1e-6 {
			t.Fatalf("finite load exceeds 100%%: %+v", l)
		}
	}
	planAssertNear(t, "A 03-01", loads["03-01"].RequiredMinutes, 30)
	planAssertNear(t, "A 03-02", loads["03-02"].RequiredMinutes, 240)
	planAssertNear(t, "A 03-02 pct", loads["03-02"].LoadPct, 100)
	planAssertNear(t, "A 03-03", loads["03-03"].RequiredMinutes, 130)
	planAssertNear(t, "A avail", loads["03-03"].AvailableMinutes, 240)

	// The immutable snapshot can be reloaded with the same summary/loads.
	got, err := f.Svc.CRP.GetFiniteRun(f.Ctx, res.Run.ID)
	if err != nil {
		t.Fatalf("get finite run: %v", err)
	}
	if got.Summary != res.Summary {
		t.Fatalf("reloaded summary %+v != %+v", got.Summary, res.Summary)
	}
	if len(got.Orders) != len(res.Orders) || len(got.Segments) != len(res.Segments) || len(got.Loads) != len(res.Loads) {
		t.Fatalf("reloaded sizes orders %d/%d segs %d/%d loads %d/%d", len(got.Orders), len(res.Orders), len(got.Segments), len(res.Segments), len(got.Loads), len(res.Loads))
	}
	if res.Summary.LateOrders < 1 || res.Summary.UnscheduledOrders < 1 || res.Summary.FirmOrders != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	runs, err := f.Svc.CRP.ListFiniteRuns(f.Ctx)
	if err != nil || len(runs) != 1 || runs[0].ID != res.Run.ID {
		t.Fatalf("list finite runs = %+v, %v", runs, err)
	}
	if _, err := f.Svc.CRP.GetFiniteRun(f.Ctx, uuid.New()); err == nil {
		t.Fatal("unknown finite run must error")
	}
	if _, err := f.Svc.CRP.FiniteSchedule(f.Ctx, CRPFiniteRequest{StartDate: d(1), HorizonDays: 367}, CRPActor{}); err == nil {
		t.Fatal("horizon > 366 must be rejected")
	} else {
		planErrCode(t, "finite horizon", err, domain.ErrCodeBadRequest)
	}
}

// Detailed (heuristic) scheduling: sequence-dependent setup from the setup matrix,
// maintenance downtime blocking a single-machine work center, transfer-batch overlap
// across two operations, persistence + reload.
func TestPlanDetailedScheduleSetupMaintenanceAndOverlap(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := f.user(t, "plan-detailed", domain.RolePlanner)
	actor := SalesOrderActor{UserID: planner.ID, Username: planner.Username, Role: planner.Role}
	cal := planCalendar(t, f, "PLD-CAL", 480)
	wcA := planWC(t, f, "PLD-A", 480, 1, 1, &cal.ID)
	wcB := planWC(t, f, "PLD-B", 480, 1, 1, &cal.ID)
	wcC := planWC(t, f, "PLD-C", 480, 1, 1, &cal.ID)
	x := planItem(t, f, domain.Item{Code: "PLD-X"})
	y := planItem(t, f, domain.Item{Code: "PLD-Y"})
	z := planItem(t, f, domain.Item{Code: "PLD-Z"})
	planRouting(t, f, x.ID, domain.RoutingOperation{WorkCenterID: wcA.ID, SetupMinutes: 30, RunMinutesPerUnit: 2, SetupFamily: "FX"})
	planRouting(t, f, y.ID, domain.RoutingOperation{WorkCenterID: wcA.ID, SetupMinutes: 30, RunMinutesPerUnit: 1, SetupFamily: "FY"})
	planRouting(t, f, z.ID,
		domain.RoutingOperation{WorkCenterID: wcB.ID, RunMinutesPerUnit: 1, OverlapEnabled: true, TransferBatchQty: 10},
		domain.RoutingOperation{WorkCenterID: wcC.ID, RunMinutesPerUnit: 1, OverlapEnabled: true, TransferBatchQty: 10},
	)
	if err := f.Svc.WorkCenters.UpsertSetupMatrix(f.Ctx, &domain.WorkCenterSetupMatrixRow{WorkCenterID: wcA.ID, FromSetupFamily: "FX", ToSetupFamily: "FY", SetupMinutes: 45}); err != nil {
		t.Fatal(err)
	}
	d := func(n int) time.Time { return planDay(2027, 3, n) }
	planMPS(t, f, x.ID, d(2), 60)
	planMPS(t, f, y.ID, d(2), 60)
	planMPS(t, f, z.ID, d(3), 20)
	// One-machine WC A is down for the whole of 03-02. (The window deliberately spans the
	// full day: maintenance timestamps come back from PostgreSQL in time.Local, and an
	// end instant falling inside a shift would move the scheduler onto Local-day windows
	// when TZ != UTC - see report.)
	if _, err := f.Svc.Maintenance.Create(f.Ctx, MaintenanceEventInput{WorkCenterID: wcA.ID, EventType: "BREAKDOWN", StartAt: d(2), EndAt: d(3), UnavailableMachines: 1, Reason: "breakdown"}, actor); err != nil {
		t.Fatalf("create maintenance: %v", err)
	}

	res, err := f.Svc.CRP.DetailedSchedule(f.Ctx, DetailedScheduleRequest{StartDate: d(1), HorizonDays: 7, CandidateOnly: true}, CRPActor{UserID: planner.ID, Username: planner.Username})
	if err != nil {
		t.Fatalf("detailed schedule: %v", err)
	}
	if res.Run.Status != "COMPLETE" || res.Run.Mode != detailedScheduleMode {
		t.Fatalf("run = %+v", res.Run)
	}
	orders := map[string]domain.DetailedScheduleOrder{}
	for _, o := range res.Orders {
		orders[o.SourceRef] = o
	}
	ox, oy, oz := orders["MRP:PLD-X:2027-03-02"], orders["MRP:PLD-Y:2027-03-02"], orders["MRP:PLD-Z:2027-03-03"]
	// X: pushed to 03-03 by the breakdown; base setup 30 (no previous family) 08:00-08:30,
	// run 120 -> 10:30, i.e. LATE against its 03-02 due date.
	planAssertTime(t, "X start", ox.ScheduledStart, planAt(d(3), 8, 0))
	planAssertTime(t, "X end", ox.ScheduledEnd, planAt(d(3), 10, 30))
	planAssertNear(t, "X tardy", ox.TardyMinutes, 10*60+30+1.0/60)
	// Y: matrix FX->FY 45 min setup, then 60 run -> 10:30-12:15.
	planAssertTime(t, "Y start", oy.ScheduledStart, planAt(d(3), 10, 30))
	planAssertTime(t, "Y end", oy.ScheduledEnd, planAt(d(3), 12, 15))
	// Z: op10 two transfer batches of 10 (08:00-08:10, 08:10-08:20); op20 batch 1 starts
	// as soon as op10 batch 1 is done (08:10) and the order finishes at 08:30.
	planAssertTime(t, "Z start", oz.ScheduledStart, planAt(d(3), 8, 0))
	planAssertTime(t, "Z end", oz.ScheduledEnd, planAt(d(3), 8, 30))
	for o, want := range map[*domain.DetailedScheduleOrder]string{&ox: "LATE", &oy: "LATE", &oz: "ON_TIME"} {
		if o.ScheduleStatus != want || o.SourceType != "MRP_PLANNED" {
			t.Fatalf("order %s = %s/%s, want %s", o.SourceRef, o.SourceType, o.ScheduleStatus, want)
		}
	}
	if res.Summary.LateOrders < 2 || res.Summary.SetupMinutes < 75 || res.Summary.TransferBatches < 4 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	var ySetup float64
	zBatches := 0
	for _, b := range res.Batches {
		if b.ScheduleOrderID == oy.ID {
			ySetup = b.SequenceSetupMinutes
		}
		if b.ScheduleOrderID == oz.ID {
			zBatches++
			if b.OperationSeq == 20 && b.BatchNo == 1 {
				planAssertTime(t, "Z op20 batch1 start", b.ScheduledStart, planAt(d(3), 8, 10))
			}
		}
	}
	planAssertNear(t, "Y sequence setup", ySetup, 45)
	if zBatches != 4 {
		t.Fatalf("Z batches = %d, want 4 (2 ops x 2 transfer batches)", zBatches)
	}
	setupTypes := 0
	for _, s := range res.Segments {
		if s.ScheduleOrderID == oy.ID && s.SegmentType == "SETUP" {
			setupTypes++
			if s.FromSetupFamily != "FX" || s.SetupFamily != "FY" {
				t.Fatalf("Y setup families = %s -> %s", s.FromSetupFamily, s.SetupFamily)
			}
		}
	}
	if setupTypes != 1 {
		t.Fatalf("Y setup segments = %d", setupTypes)
	}
	if len(res.Maintenance) != 1 || res.Maintenance[0].WorkCenterID != wcA.ID {
		t.Fatalf("maintenance snapshot = %+v", res.Maintenance)
	}
	// Load on A for 03-03: 30+120+45+60 = 255 used of 480; nothing on the breakdown day.
	aLoads := 0
	for _, l := range res.Loads {
		if l.WorkCenterID == wcA.ID {
			aLoads++
			if l.Date.Format("01-02") != "03-03" {
				t.Fatalf("unexpected WC A load on %s", l.Date)
			}
			planAssertNear(t, "A used", l.RequiredMinutes, 255)
			planAssertNear(t, "A available", l.AvailableMinutes, 480)
		}
	}
	if aLoads != 1 {
		t.Fatalf("WC A load rows = %d", aLoads)
	}

	got, err := f.Svc.CRP.GetDetailedRun(f.Ctx, res.Run.ID)
	if err != nil {
		t.Fatalf("get detailed run: %v", err)
	}
	if len(got.Orders) != len(res.Orders) || len(got.Batches) != len(res.Batches) || len(got.Segments) != len(res.Segments) {
		t.Fatalf("reloaded sizes orders %d/%d batches %d/%d segs %d/%d", len(got.Orders), len(res.Orders), len(got.Batches), len(res.Batches), len(got.Segments), len(res.Segments))
	}
	runs, err := f.Svc.CRP.ListDetailedRuns(f.Ctx)
	if err != nil || len(runs) == 0 {
		t.Fatalf("list detailed runs = %d, %v", len(runs), err)
	}
	if _, err := f.Svc.CRP.GetDetailedRun(f.Ctx, uuid.New()); err == nil {
		t.Fatal("unknown detailed run must error")
	} else {
		planErrCode(t, "unknown detailed run", err, domain.ErrCodeNotFound)
	}
	_, err = f.Svc.CRP.DetailedSchedule(f.Ctx, DetailedScheduleRequest{StartDate: d(1), HorizonDays: 400}, CRPActor{})
	planErrCode(t, "detailed horizon", err, domain.ErrCodeBadRequest)

	// Activating (non-candidate) schedule with NotBefore pushes everything after 03-03 12:00.
	res2, err := f.Svc.CRP.DetailedSchedule(f.Ctx, DetailedScheduleRequest{StartDate: d(1), HorizonDays: 7, NotBefore: planAt(d(3), 12, 0), SimulateMRP: true}, CRPActor{UserID: planner.ID, Username: planner.Username})
	if err != nil {
		t.Fatalf("activated detailed schedule: %v", err)
	}
	for _, o := range res2.Orders {
		if o.ScheduledStart != nil && o.ScheduledStart.Before(planAt(d(3), 12, 0)) {
			t.Fatalf("order %s starts %s before NotBefore", o.SourceRef, o.ScheduledStart)
		}
	}
}

// SimulateCTPOrder schedules a hypothetical order on top of existing load without persisting.
func TestPlanSimulateCTPOrder(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	cal := planCalendar(t, f, "PLS-CAL", 480)
	wc := planWC(t, f, "PLS-WC", 480, 1, 1, &cal.ID)
	it := planItem(t, f, domain.Item{Code: "PLS-CTP"})
	planRouting(t, f, it.ID, domain.RoutingOperation{WorkCenterID: wc.ID, RunMinutesPerUnit: 2})
	noRouting := planItem(t, f, domain.Item{Code: "PLS-NR"})

	tomorrow := planToday().AddDate(0, 0, 1)
	o, err := f.Svc.CRP.SimulateCTPOrder(f.Ctx, it.ID, 30, tomorrow, tomorrow, 30)
	if err != nil {
		t.Fatalf("simulate CTP: %v", err)
	}
	if o.ScheduleStatus != "ON_TIME" || o.SourceType != "CTP_WHAT_IF" {
		t.Fatalf("CTP order = %+v", o)
	}
	planAssertTime(t, "CTP start", o.ScheduledStart, planAt(tomorrow, 8, 0))
	planAssertTime(t, "CTP end", o.ScheduledEnd, planAt(tomorrow, 9, 0))

	// 300 units = 600 minutes. A zero/past earliest is clamped to the start of today
	// (not to "now"): 480 min today 08:00-16:00 + 120 min tomorrow -> LATE vs today.
	o, err = f.Svc.CRP.SimulateCTPOrder(f.Ctx, it.ID, 300, time.Time{}, planToday(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if o.ScheduleStatus != "LATE" {
		t.Fatalf("large CTP status = %s", o.ScheduleStatus)
	}
	planAssertTime(t, "large CTP start", o.ScheduledStart, planAt(planToday(), 8, 0))
	planAssertTime(t, "large CTP end", o.ScheduledEnd, planAt(tomorrow, 10, 0))
	planAssertNear(t, "large CTP tardy", o.TardyMinutes, 10*60+1.0/60)
	// Nothing persisted.
	var n int
	if err := f.DB.Get(&n, `SELECT count(*) FROM detailed_schedule_runs`); err != nil || n != 0 {
		t.Fatalf("detailed runs persisted by CTP simulation: %d, %v", n, err)
	}

	_, err = f.Svc.CRP.SimulateCTPOrder(f.Ctx, it.ID, 0, tomorrow, tomorrow, 30)
	planErrCode(t, "zero qty", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.CRP.SimulateCTPOrder(f.Ctx, it.ID, 1, tomorrow, tomorrow, 367)
	planErrCode(t, "horizon", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.CRP.SimulateCTPOrder(f.Ctx, uuid.New(), 1, tomorrow, tomorrow, 0)
	planErrCode(t, "unknown item", err, domain.ErrCodeNotFound)
	_, err = f.Svc.CRP.SimulateCTPOrder(f.Ctx, noRouting.ID, 1, tomorrow, tomorrow, 30)
	planErrCode(t, "no routing", err, domain.ErrCodeConflict)
}

func TestPlanMaintenanceLifecycleAndValidation(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := planSOActor(t, f, "plan-maint", domain.RolePlanner)
	viewer := planSOActor(t, f, "plan-maint-viewer", domain.RoleViewer)
	wc := domain.WorkCenter{Code: "PLM-WC", Name: "maint", MachineCount: 2, WorkerCount: 3}
	if err := f.Svc.WorkCenters.Create(f.Ctx, &wc); err != nil {
		t.Fatal(err)
	}
	s, e := planAt(planDay(2027, 3, 2), 8, 0), planAt(planDay(2027, 3, 2), 12, 0)
	in := MaintenanceEventInput{WorkCenterID: wc.ID, EventType: "preventive_maintenance", StartAt: s, EndAt: e, UnavailableMachines: 1, Reason: " PM "}

	_, err := f.Svc.Maintenance.Create(f.Ctx, in, SalesOrderActor{})
	planErrCode(t, "anonymous", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.Maintenance.Create(f.Ctx, in, viewer)
	planErrCode(t, "viewer", err, domain.ErrCodeForbidden)
	bad := []struct {
		name string
		mut  func(*MaintenanceEventInput)
		code domain.ErrorCode
	}{
		{"no wc", func(x *MaintenanceEventInput) { x.WorkCenterID = uuid.Nil }, domain.ErrCodeBadRequest},
		{"bad type", func(x *MaintenanceEventInput) { x.EventType = "COFFEE" }, domain.ErrCodeBadRequest},
		{"bad status", func(x *MaintenanceEventInput) { x.Status = "MAYBE" }, domain.ErrCodeBadRequest},
		{"terminal create", func(x *MaintenanceEventInput) { x.Status = "COMPLETED" }, domain.ErrCodeBadRequest},
		{"inverted window", func(x *MaintenanceEventInput) { x.EndAt = s }, domain.ErrCodeBadRequest},
		{"negative", func(x *MaintenanceEventInput) { x.UnavailableWorkers = -1 }, domain.ErrCodeBadRequest},
		{"no reduction", func(x *MaintenanceEventInput) { x.UnavailableMachines = 0 }, domain.ErrCodeBadRequest},
		{"too many machines", func(x *MaintenanceEventInput) { x.UnavailableMachines = 3 }, domain.ErrCodeBadRequest},
		{"too many workers", func(x *MaintenanceEventInput) { x.UnavailableWorkers = 4 }, domain.ErrCodeBadRequest},
		{"unknown wc", func(x *MaintenanceEventInput) { x.WorkCenterID = uuid.New() }, domain.ErrCodeNotFound},
	}
	for _, b := range bad {
		x := in
		b.mut(&x)
		_, err := f.Svc.Maintenance.Create(f.Ctx, x, planner)
		planErrCode(t, b.name, err, b.code)
	}

	pm, err := f.Svc.Maintenance.Create(f.Ctx, in, planner)
	if err != nil {
		t.Fatalf("create PM: %v", err)
	}
	if pm.Event.EventType != "PREVENTIVE_MAINTENANCE" || pm.Current == nil || pm.Current.Status != "PLANNED" || len(pm.Revisions) != 1 || pm.Revisions[0].Reason != "PM" {
		t.Fatalf("PM detail = %+v", pm)
	}
	bd, err := f.Svc.Maintenance.Create(f.Ctx, MaintenanceEventInput{WorkCenterID: wc.ID, EventType: "BREAKDOWN", StartAt: planAt(planDay(2027, 3, 3), 8, 0), EndAt: planAt(planDay(2027, 3, 3), 9, 0), UnavailableWorkers: 2}, planner)
	if err != nil {
		t.Fatalf("create breakdown: %v", err)
	}
	if bd.Current == nil || bd.Current.Status != "ACTIVE" {
		t.Fatalf("breakdown default status = %+v", bd.Current)
	}

	// Overlap query: only the PM overlaps [03-02 10:00, 03-02 11:00).
	evs, err := f.Svc.Maintenance.CapacityEvents(f.Ctx, planAt(planDay(2027, 3, 2), 10, 0), planAt(planDay(2027, 3, 2), 11, 0))
	if err != nil || len(evs) != 1 || evs[0].ID != pm.Event.ID {
		t.Fatalf("capacity events = %+v, %v", evs, err)
	}

	// Revise: extend PM, then ACTIVE, then ACTIVE->PLANNED is rejected.
	newEnd := planAt(planDay(2027, 3, 2), 14, 0)
	two := 2
	rev, err := f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{EndAt: &newEnd, UnavailableMachines: &two}, planner)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if len(rev.Revisions) != 2 || !rev.Current.EndAt.Equal(newEnd) || rev.Current.UnavailableMachines != 2 || rev.Current.RevisionNo != 2 {
		t.Fatalf("revised = %+v", rev.Current)
	}
	three := 3
	_, err = f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{UnavailableMachines: &three}, planner)
	planErrCode(t, "revise too many machines", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{Status: "bogus"}, planner)
	planErrCode(t, "revise bad status", err, domain.ErrCodeBadRequest)
	if _, err := f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{Status: "active"}, planner); err != nil {
		t.Fatalf("revise to ACTIVE: %v", err)
	}
	_, err = f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{Status: "PLANNED"}, planner)
	planErrCode(t, "ACTIVE->PLANNED", err, domain.ErrCodeConflict)
	reason, src := " done ", " WO-1 "
	done, err := f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{Status: "COMPLETED", Reason: &reason, SourceRef: &src}, planner)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Current == nil || done.Current.Status != "COMPLETED" || done.Current.Reason != "done" || done.Current.SourceRef != "WO-1" {
		t.Fatalf("completed = %+v", done.Current)
	}
	_, err = f.Svc.Maintenance.Revise(f.Ctx, pm.Event.ID, MaintenanceRevisionInput{Status: "ACTIVE"}, planner)
	planErrCode(t, "terminal revise", err, domain.ErrCodeConflict)

	// Cancel zeroes the reduction automatically.
	canc, err := f.Svc.Maintenance.Revise(f.Ctx, bd.Event.ID, MaintenanceRevisionInput{Status: "CANCELLED"}, planner)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canc.Current.UnavailableMachines != 0 || canc.Current.UnavailableWorkers != 0 {
		t.Fatalf("cancelled reductions = %+v", canc.Current)
	}

	open, err := f.Svc.Maintenance.List(f.Ctx, &wc.ID, false)
	if err != nil || len(open) != 0 {
		t.Fatalf("open maintenance = %+v, %v", open, err)
	}
	all, err := f.Svc.Maintenance.List(f.Ctx, &wc.ID, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("all maintenance = %d, %v", len(all), err)
	}
	if _, err := f.Svc.Maintenance.List(f.Ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.Maintenance.Get(f.Ctx, uuid.New())
	planErrCode(t, "get unknown", err, domain.ErrCodeNotFound)
	_, err = f.Svc.Maintenance.Revise(f.Ctx, uuid.New(), MaintenanceRevisionInput{}, planner)
	planErrCode(t, "revise unknown", err, domain.ErrCodeNotFound)
	_, err = f.Svc.Maintenance.Revise(f.Ctx, uuid.Nil, MaintenanceRevisionInput{}, planner)
	planErrCode(t, "revise nil id", err, domain.ErrCodeBadRequest)

	for typ, want := range map[string]string{
		"PREVENTIVE_MAINTENANCE": "PREVENTIVE_MAINTENANCE_CAPACITY",
		"BREAKDOWN":              "BREAKDOWN_CAPACITY",
		"UNPLANNED_DOWNTIME":     "UNPLANNED_DOWNTIME_CAPACITY",
		"PLANNED_DOWNTIME":       "PLANNED_DOWNTIME_CAPACITY",
	} {
		if got := MaintenanceExceptionType(typ); got != want {
			t.Fatalf("MaintenanceExceptionType(%s) = %s", typ, got)
		}
	}
}

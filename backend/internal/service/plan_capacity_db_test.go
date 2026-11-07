package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
)

// Infinite-capacity CRP: backward operation placement from the planned receipt date,
// holiday skipping on the work-center calendar, and load% = required / (minutes*eff*util).
func TestPlanCRPRunBackwardLoadWithCalendarExceptions(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	cal := planCalendar(t, f, "PLC-CAL", 480)
	// 03-09 is a holiday; 03-08 is a short 120-minute day.
	if err := f.Svc.Calendar.AddException(f.Ctx, &domain.CalendarException{CalendarID: cal.ID, ExceptionDate: planDay(2027, 3, 9), Kind: "HOLIDAY", Description: "plan holiday"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Svc.Calendar.AddException(f.Ctx, &domain.CalendarException{CalendarID: cal.ID, ExceptionDate: planDay(2027, 3, 8), Kind: "WORKDAY", Minutes: 120, Description: "short day"}); err != nil {
		t.Fatal(err)
	}
	wcA := planWC(t, f, "PLC-A", 480, 1, 0.5, &cal.ID)
	wcB := planWC(t, f, "PLC-B", 480, 1, 1, &cal.ID)
	fg := planItem(t, f, domain.Item{Code: "PLC-FG", Type: domain.ItemTypeFinished})
	planRouting(t, f, fg.ID,
		domain.RoutingOperation{WorkCenterID: wcA.ID, SetupMinutes: 30, RunMinutesPerUnit: 2},
		domain.RoutingOperation{WorkCenterID: wcB.ID, SetupMinutes: 0, RunMinutesPerUnit: 1},
	)
	planMPS(t, f, fg.ID, planDay(2027, 3, 10), 50)
	planMPS(t, f, fg.ID, planDay(2027, 3, 20), 10)

	rows, err := f.Svc.CRP.Run(f.Ctx, CRPRequest{StartDate: planDay(2027, 3, 1), HorizonDays: 28})
	if err != nil {
		t.Fatalf("CRP run: %v", err)
	}
	type key struct {
		wc  string
		day string
	}
	got := map[key]domain.CapacityLoadRow{}
	for _, r := range rows {
		if r.WorkCenterCode == "PLC-A" || r.WorkCenterCode == "PLC-B" {
			got[key{r.WorkCenterCode, r.Date.Format("01-02")}] = r
		}
	}
	want := []struct {
		k                   key
		req, avail, loadPct float64
	}{
		// Order 1 (50 due 03-10): last op on 03-10; first op would be 03-09 (holiday) -> 03-08 (120 min).
		{key{"PLC-B", "03-10"}, 50, 480, 50.0 / 480 * 100},
		{key{"PLC-A", "03-08"}, 130, 60, 130.0 / 60 * 100},
		// Order 2 (10 due 03-20): last op 03-20, first op 03-19.
		{key{"PLC-B", "03-20"}, 10, 480, 10.0 / 480 * 100},
		{key{"PLC-A", "03-19"}, 50, 240, 50.0 / 240 * 100},
	}
	if len(got) != len(want) {
		t.Fatalf("CRP rows for plan WCs = %v, want %d rows", got, len(want))
	}
	for _, w := range want {
		r, ok := got[w.k]
		if !ok {
			t.Fatalf("missing CRP row %v; got %v", w.k, got)
		}
		planAssertNear(t, "required "+w.k.wc+" "+w.k.day, r.RequiredMinutes, w.req)
		planAssertNear(t, "available "+w.k.wc+" "+w.k.day, r.AvailableMinutes, w.avail)
		planAssertNear(t, "load% "+w.k.wc+" "+w.k.day, r.LoadPct, w.loadPct)
		if r.IsHoliday {
			t.Fatalf("%v flagged as holiday", w.k)
		}
		if r.WorkCenterID != wcA.ID && r.WorkCenterID != wcB.ID {
			t.Fatalf("unexpected WC id on %v", w.k)
		}
	}
	if got[key{"PLC-A", "03-08"}].LoadPct <= 100 {
		t.Fatal("short day must show overload (>100%) in infinite CRP")
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Date.After(rows[i].Date) {
			t.Fatal("CRP rows not sorted by date")
		}
	}
}

// Cost rollup: leaf material at standard cost, parent material = child total * qty * (1+scrap),
// labor/overhead from own routing minutes (setup + run for one unit).
func TestPlanCostRollup(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	wc := domain.WorkCenter{Code: "PLR-WC", Name: "cost wc", LaborRatePerMinute: 1, OverheadRatePerMinute: 0.5}
	if err := f.Svc.WorkCenters.Create(f.Ctx, &wc); err != nil {
		t.Fatal(err)
	}
	rm := planItem(t, f, domain.Item{Code: "PLR-RM", Type: domain.ItemTypeRawMaterial, StandardCost: 5})
	sa := planItem(t, f, domain.Item{Code: "PLR-SA", Type: domain.ItemTypeSubAssembly, StandardCost: 999})
	fg := planItem(t, f, domain.Item{Code: "PLR-FG", Type: domain.ItemTypeFinished, StandardCost: 999})
	planBOM(t, f, sa.ID, rm.ID, 2, 0.1)
	planBOM(t, f, fg.ID, sa.ID, 3, 0)
	planRouting(t, f, sa.ID, domain.RoutingOperation{WorkCenterID: wc.ID, SetupMinutes: 10, RunMinutesPerUnit: 2})
	planRouting(t, f, fg.ID, domain.RoutingOperation{WorkCenterID: wc.ID, SetupMinutes: 0, RunMinutesPerUnit: 4})

	rows, err := f.Svc.CostRollup.Rollup(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]domain.CostRollupRow{}
	for _, r := range rows {
		by[r.ItemCode] = r
	}
	// RM leaf: material 5.
	planAssertNear(t, "RM total", by["PLR-RM"].TotalCost, 5)
	// SA: material 5*2*1.1 = 11; labor 12*1 = 12; overhead 12*0.5 = 6 -> 29.
	planAssertNear(t, "SA material", by["PLR-SA"].MaterialCost, 11)
	planAssertNear(t, "SA labor", by["PLR-SA"].LaborCost, 12)
	planAssertNear(t, "SA overhead", by["PLR-SA"].OverheadCost, 6)
	planAssertNear(t, "SA total", by["PLR-SA"].TotalCost, 29)
	// FG: material 29*3 = 87; labor 4; overhead 2 -> 93. Item master standard cost is ignored for parents.
	planAssertNear(t, "FG material", by["PLR-FG"].MaterialCost, 87)
	planAssertNear(t, "FG labor", by["PLR-FG"].LaborCost, 4)
	planAssertNear(t, "FG overhead", by["PLR-FG"].OverheadCost, 2)
	planAssertNear(t, "FG total", by["PLR-FG"].TotalCost, 93)
}

// RCCP: monthly minutes = sum(MPS qty * profile minutes) vs capacity * machines * days * eff * util.
func TestPlanRCCPRun(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	wc := planWC(t, f, "PLP-WC", 480, 0.5, 0.5, nil)
	wc2 := domain.WorkCenter{Code: "PLP-WC2", Name: "two machines", CapacityMinutesPerDay: 100, Efficiency: 1, Utilization: 1, MachineCount: 2}
	if err := f.Svc.WorkCenters.Create(f.Ctx, &wc2); err != nil {
		t.Fatal(err)
	}
	it := planItem(t, f, domain.Item{Code: "PLP-FG"})
	for _, p := range []domain.RCCPProfile{{ItemID: it.ID, WorkCenterID: wc.ID, MinutesPerUnit: 3}, {ItemID: it.ID, WorkCenterID: wc2.ID, MinutesPerUnit: 1}} {
		p := p
		if err := f.Svc.RCCP.UpsertProfile(f.Ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	// Upsert overwrites minutes for the same (item, wc).
	p := domain.RCCPProfile{ItemID: it.ID, WorkCenterID: wc2.ID, MinutesPerUnit: 2}
	if err := f.Svc.RCCP.UpsertProfile(f.Ctx, &p); err != nil {
		t.Fatal(err)
	}
	profs, err := f.Svc.RCCP.ListProfiles(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, pr := range profs {
		if pr.ItemID == it.ID {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("profiles for item = %d, want 2", n)
	}
	planMPS(t, f, it.ID, planDay(2027, 5, 3), 100)
	planMPS(t, f, it.ID, planDay(2027, 5, 20), 50)
	planMPS(t, f, it.ID, planDay(2027, 6, 1), 10)

	rows, err := f.Svc.RCCP.Run(f.Ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ wc, month string }
	got := map[key]domain.RCCPLoadRow{}
	for _, r := range rows {
		got[key{r.WorkCenterCode, r.Month.Format("2006-01")}] = r
	}
	check := func(k key, req, avail float64) {
		t.Helper()
		r, ok := got[k]
		if !ok {
			t.Fatalf("missing RCCP row %v", k)
		}
		planAssertNear(t, "required", r.RequiredMinutes, req)
		planAssertNear(t, "available", r.AvailableMinutes, avail)
		planAssertNear(t, "load%", r.LoadPct, req/avail*100)
	}
	check(key{"PLP-WC", "2027-05"}, 450, 480*20*0.25)
	check(key{"PLP-WC", "2027-06"}, 30, 480*20*0.25)
	check(key{"PLP-WC2", "2027-05"}, 300, 100*2*20)
	// workingDays <= 0 falls back to 22.
	rows, err = f.Svc.RCCP.Run(f.Ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.WorkCenterCode == "PLP-WC" && r.Month.Format("2006-01") == "2027-05" {
			planAssertNear(t, "available (22 days)", r.AvailableMinutes, 480*22*0.25)
		}
	}
}

func TestPlanWorkCenterRoutingServices(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	w := domain.WorkCenter{Code: "PLW-1", Name: "defaults", ShiftStartMinute: 2000, MachineCount: -1, WorkerCount: -5}
	if err := f.Svc.WorkCenters.Create(f.Ctx, &w); err != nil {
		t.Fatal(err)
	}
	if w.Efficiency != 1 || w.Utilization != 0.85 || w.CapacityMinutesPerDay != 480 || w.ShiftStartMinute != 480 || w.MachineCount != 1 || w.WorkerCount != 1 {
		t.Fatalf("work center defaults = %+v", w)
	}
	planAssertNear(t, "effective capacity", w.EffectiveCapacityMin(), 480*0.85)
	w.ShiftStartMinute = -1
	w.MachineCount = 0
	w.WorkerCount = -2
	w.Name = "updated"
	if err := f.Svc.WorkCenters.Update(f.Ctx, &w); err != nil {
		t.Fatal(err)
	}
	if w.ShiftStartMinute != 480 || w.MachineCount != 1 || w.WorkerCount != 0 {
		t.Fatalf("update normalization = %+v", w)
	}
	list, err := f.Svc.WorkCenters.List(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		if x.ID == w.ID {
			found = x.Name == "updated" && x.WorkerCount == 0
		}
	}
	if !found {
		t.Fatal("updated work center not listed")
	}

	// Setup matrix validation and round trip.
	planErrCode(t, "empty to-family", f.Svc.WorkCenters.UpsertSetupMatrix(f.Ctx, &domain.WorkCenterSetupMatrixRow{WorkCenterID: w.ID, ToSetupFamily: " "}), domain.ErrCodeBadRequest)
	planErrCode(t, "negative setup", f.Svc.WorkCenters.UpsertSetupMatrix(f.Ctx, &domain.WorkCenterSetupMatrixRow{WorkCenterID: w.ID, ToSetupFamily: "B", SetupMinutes: -1}), domain.ErrCodeBadRequest)
	sm := domain.WorkCenterSetupMatrixRow{WorkCenterID: w.ID, FromSetupFamily: "A", ToSetupFamily: "B", SetupMinutes: 15}
	if err := f.Svc.WorkCenters.UpsertSetupMatrix(f.Ctx, &sm); err != nil {
		t.Fatal(err)
	}
	m, err := f.Svc.WorkCenters.SetupMatrix(f.Ctx, w.ID)
	if err != nil || len(m) != 1 || m[0].SetupMinutes != 15 {
		t.Fatalf("setup matrix = %+v, %v", m, err)
	}
	if err := f.Svc.WorkCenters.DeleteSetupMatrix(f.Ctx, m[0].ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.Svc.WorkCenters.SetupMatrix(f.Ctx, w.ID); len(m) != 0 {
		t.Fatalf("setup matrix after delete = %+v", m)
	}

	it := planItem(t, f, domain.Item{Code: "PLW-FG"})
	rt := domain.Routing{ItemID: it.ID, Description: "r", IsActive: true}
	if err := f.Svc.Routings.Create(f.Ctx, &rt); err != nil {
		t.Fatal(err)
	}
	planErrCode(t, "negative transfer batch", f.Svc.Routings.AddOperation(f.Ctx, &domain.RoutingOperation{RoutingID: rt.ID, SeqNo: 10, WorkCenterID: w.ID, TransferBatchQty: -1}), domain.ErrCodeBadRequest)
	planErrCode(t, "overlap without batch", f.Svc.Routings.AddOperation(f.Ctx, &domain.RoutingOperation{RoutingID: rt.ID, SeqNo: 10, WorkCenterID: w.ID, OverlapEnabled: true}), domain.ErrCodeBadRequest)
	op := domain.RoutingOperation{RoutingID: rt.ID, SeqNo: 10, WorkCenterID: w.ID, Description: "cut", RunMinutesPerUnit: 1, MachinesRequired: 0, WorkersRequired: -3}
	if err := f.Svc.Routings.AddOperation(f.Ctx, &op); err != nil {
		t.Fatal(err)
	}
	if op.MachinesRequired != 1 || op.WorkersRequired != 0 {
		t.Fatalf("operation normalization = %+v", op)
	}
	op.RunMinutesPerUnit = 3
	op.OverlapEnabled = true
	planErrCode(t, "update overlap without batch", f.Svc.Routings.UpdateOperation(f.Ctx, &op), domain.ErrCodeBadRequest)
	op.TransferBatchQty = 5
	if err := f.Svc.Routings.UpdateOperation(f.Ctx, &op); err != nil {
		t.Fatal(err)
	}
	ops, err := f.Svc.Routings.Operations(f.Ctx, rt.ID)
	if err != nil || len(ops) != 1 || ops[0].RunMinutesPerUnit != 3 || !ops[0].OverlapEnabled || ops[0].TransferBatchQty != 5 {
		t.Fatalf("operations = %+v, %v", ops, err)
	}
	active, err := f.Svc.Routings.ActiveForItem(f.Ctx, it.ID)
	if err != nil || active.ID != rt.ID {
		t.Fatalf("active routing = %+v, %v", active, err)
	}
	if rts, err := f.Svc.Routings.List(f.Ctx); err != nil || len(rts) == 0 {
		t.Fatalf("routing list = %d, %v", len(rts), err)
	}

	alt := planWC(t, f, "PLW-ALT", 480, 1, 1, nil)
	planErrCode(t, "zero multiplier", f.Svc.Routings.AddAlternative(f.Ctx, &domain.RoutingOperationAlternative{RoutingOperationID: op.ID, WorkCenterID: alt.ID, Priority: 1, RunTimeMultiplier: 0, SetupTimeMultiplier: 1}), domain.ErrCodeBadRequest)
	a := domain.RoutingOperationAlternative{RoutingOperationID: op.ID, WorkCenterID: alt.ID, Priority: 1, RunTimeMultiplier: 1.5, SetupTimeMultiplier: 1, IsActive: true}
	if err := f.Svc.Routings.AddAlternative(f.Ctx, &a); err != nil {
		t.Fatal(err)
	}
	alts, err := f.Svc.Routings.Alternatives(f.Ctx, op.ID)
	if err != nil || len(alts) != 1 || alts[0].RunTimeMultiplier != 1.5 {
		t.Fatalf("alternatives = %+v, %v", alts, err)
	}
	if err := f.Svc.Routings.DeleteAlternative(f.Ctx, alts[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Svc.Routings.DeleteOperation(f.Ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if ops, _ := f.Svc.Routings.Operations(f.Ctx, rt.ID); len(ops) != 0 {
		t.Fatalf("operations after delete = %+v", ops)
	}
	if err := f.Svc.WorkCenters.Delete(f.Ctx, alt.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPlanCalendarServiceAndSnapshot(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	def, err := f.Svc.Calendar.Default(f.Ctx)
	if err != nil || def == nil || !def.IsDefault {
		t.Fatalf("default calendar = %+v, %v", def, err)
	}
	c := domain.WorkCalendar{Code: "PLK", Name: "plan cal", MondayMin: 480, TuesdayMin: 480, WednesdayMin: 480, ThursdayMin: 480, FridayMin: 240}
	if err := f.Svc.Calendar.Create(f.Ctx, &c); err != nil {
		t.Fatal(err)
	}
	c.Name = "renamed"
	if err := f.Svc.Calendar.Update(f.Ctx, &c); err != nil {
		t.Fatal(err)
	}
	got, err := f.Svc.Calendar.Get(f.Ctx, c.ID)
	if err != nil || got.Name != "renamed" {
		t.Fatalf("get calendar = %+v, %v", got, err)
	}
	if all, err := f.Svc.Calendar.List(f.Ctx); err != nil || len(all) < 2 {
		t.Fatalf("calendar list = %d, %v", len(all), err)
	}
	// 2027-03-01 is a Monday.
	hol := domain.CalendarException{CalendarID: c.ID, ExceptionDate: planDay(2027, 3, 1), Kind: "HOLIDAY", Description: "h"}
	if err := f.Svc.Calendar.AddException(f.Ctx, &hol); err != nil {
		t.Fatal(err)
	}
	if err := f.Svc.Calendar.AddException(f.Ctx, &domain.CalendarException{CalendarID: c.ID, ExceptionDate: planDay(2027, 3, 6), Kind: "WORKDAY", Minutes: 200}); err != nil {
		t.Fatal(err)
	}
	// Outside the snapshot range: must not be loaded.
	if err := f.Svc.Calendar.AddException(f.Ctx, &domain.CalendarException{CalendarID: c.ID, ExceptionDate: planDay(2027, 4, 1), Kind: "HOLIDAY"}); err != nil {
		t.Fatal(err)
	}
	exs, err := f.Svc.Calendar.Exceptions(f.Ctx, c.ID)
	if err != nil || len(exs) != 3 {
		t.Fatalf("exceptions = %d, %v", len(exs), err)
	}
	snap, err := f.Svc.Calendar.LoadSnapshot(f.Ctx, c.ID, planDay(2027, 3, 1), planDay(2027, 3, 31))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Exceptions) != 2 {
		t.Fatalf("snapshot exceptions = %d, want 2 (range-limited)", len(snap.Exceptions))
	}
	cases := []struct {
		day  time.Time
		mins int
	}{
		{planDay(2027, 3, 1), 0},   // Monday holiday
		{planDay(2027, 3, 2), 480}, // Tuesday
		{planDay(2027, 3, 5), 240}, // Friday
		{planDay(2027, 3, 6), 200}, // Saturday WORKDAY exception
		{planDay(2027, 3, 7), 0},   // Sunday
		{planDay(2027, 3, 4).Add(15 * time.Hour), 480},
	}
	for _, cs := range cases {
		if got := snap.MinutesAvailable(cs.day); got != cs.mins {
			t.Fatalf("MinutesAvailable(%s) = %d, want %d", cs.day, got, cs.mins)
		}
	}
	planAssertDay(t, "previous workday of Sunday", snap.PreviousWorkDay(planDay(2027, 3, 7), 10), planDay(2027, 3, 6))
	planAssertDay(t, "previous workday of holiday Monday", snap.PreviousWorkDay(planDay(2027, 3, 1), 10), planDay(2027, 2, 26))
	planAssertDay(t, "lookback exhausted returns input", snap.PreviousWorkDay(planDay(2027, 3, 1), 0), planDay(2027, 3, 1))

	if err := f.Svc.Calendar.DeleteException(f.Ctx, hol.ID); err != nil {
		t.Fatal(err)
	}
	snap, _ = f.Svc.Calendar.LoadSnapshot(f.Ctx, c.ID, planDay(2027, 3, 1), planDay(2027, 3, 31))
	if !snap.IsWorkDay(planDay(2027, 3, 1)) {
		t.Fatal("deleted holiday still applied")
	}
	if ds, err := f.Svc.Calendar.LoadDefaultSnapshot(f.Ctx, planDay(2027, 3, 1), planDay(2027, 3, 31)); err != nil || ds == nil || !ds.Calendar.IsDefault {
		t.Fatalf("default snapshot = %+v, %v", ds, err)
	}
	if err := f.Svc.Calendar.Delete(f.Ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.Calendar.Get(f.Ctx, c.ID); err == nil {
		t.Fatal("deleted calendar still readable")
	}
}

// The API calls CRP.Run without a start date when the client omits it. CRP must
// still honour holidays near today (regression: it loaded calendar exceptions for
// a window starting at the zero time, year 1, so they were ignored).
func TestPlanCRPRunDefaultStartHonoursHolidays(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	cal := planCalendar(t, f, "PLCD-CAL", 480)
	now := time.Now()
	hol := planDay(now.Year(), now.Month(), now.Day()).AddDate(0, 0, 3)
	if err := f.Svc.Calendar.AddException(f.Ctx, &domain.CalendarException{CalendarID: cal.ID, ExceptionDate: hol, Kind: "HOLIDAY"}); err != nil {
		t.Fatal(err)
	}
	wc := planWC(t, f, "PLCD-WC", 480, 1, 1, &cal.ID)
	fg := planItem(t, f, domain.Item{Code: "PLCD-FG"})
	planRouting(t, f, fg.ID, domain.RoutingOperation{WorkCenterID: wc.ID, RunMinutesPerUnit: 1})
	planMPS(t, f, fg.ID, hol, 10)

	rows, err := f.Svc.CRP.Run(f.Ctx, CRPRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var day string
	for _, r := range rows {
		if r.WorkCenterID == wc.ID {
			day = r.Date.Format("2006-01-02")
		}
	}
	// Regression: with a zero StartDate the calendar window used to start at
	// year 1, so the holiday was ignored and load was placed on it.
	if want := hol.AddDate(0, 0, -1).Format("2006-01-02"); day != want {
		t.Fatalf("load placed on %q, want %s (previous workday)", day, want)
	}
}

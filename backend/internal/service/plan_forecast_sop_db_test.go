package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func planForecastActor(t *testing.T, f *dbFixture, name string) (ForecastActor, SalesOrderActor) {
	t.Helper()
	so := planSOActor(t, f, name, domain.RolePlanner)
	return ForecastActor{UserID: so.UserID, Username: so.Username}, so
}

// Forecast from weekly-bucketed confirmed order history (SMA / EXPO / HW fallback),
// accuracy metrics, versioning/activation, forecast consumption and MPS publication.
func TestPlanForecastRunVersionConsumeAndPublish(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	fa, sa := planForecastActor(t, f, "plan-forecast")
	cust := planCustomer(t, f, sa, "PLFC-C")
	it := planItem(t, f, domain.Item{Code: "PLFC-FG"})
	empty := planItem(t, f, domain.Item{Code: "PLFC-NONE"})
	// Weekly history (Mondays): 10, 20, 30, 40.
	for i, q := range []float64{10, 20, 30, 40} {
		planConfirmedOrder(t, f, sa, cust.ID, "PLFC-H"+string(rune('1'+i)), it.ID, q, planDay(2026, 1, 5).AddDate(0, 0, 7*i))
	}

	res, err := f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, Method: "sma", Window: 2, HorizonPeriods: 3, BucketDays: 7, AsOfDate: "2026-01-27", SaveAsVersion: true}, fa)
	if err != nil {
		t.Fatalf("SMA forecast: %v", err)
	}
	if res.Method != "SMA" || res.RunID == nil || res.Version != 1 || res.Scenario != "BASE" || res.Status != "DRAFT" {
		t.Fatalf("forecast result header = %+v", res)
	}
	if len(res.Points) != 7 {
		t.Fatalf("points = %d, want 4 history + 3 future", len(res.Points))
	}
	// fitted = [10, 10, 15, 25]; MAE = (10+15+15)/3; MAPE = (0.5+0.5+0.375)/3*100.
	planAssertNear(t, "MAE", res.MAE, 40.0/3)
	planAssertNear(t, "MAPE", res.MAPE, 1.375/3*100)
	wantFuture := []float64{35, 37.5, 36.25}
	for i, w := range wantFuture {
		p := res.Points[4+i]
		if !p.IsFuture || p.Forecast == nil {
			t.Fatalf("future point %d = %+v", i, p)
		}
		planAssertNear(t, "future forecast", *p.Forecast, w)
		planAssertDay(t, "future period", p.Period, planDay(2026, 2, 2).AddDate(0, 0, 7*i))
	}
	if a := res.Points[3].Actual; a == nil || *a != 40 {
		t.Fatalf("last actual = %v", a)
	}

	// EXPO alpha 0.5: fitted [10,10,15,22.5], next = 0.5*40 + 0.5*22.5 = 31.25 (flat).
	ex, err := f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, Method: "EXPO", Alpha: 0.5, HorizonPeriods: 2, AsOfDate: "2026-01-27"}, fa)
	if err != nil {
		t.Fatal(err)
	}
	planAssertNear(t, "EXPO MAE", ex.MAE, 42.5/3)
	planAssertNear(t, "EXPO future", *ex.Points[4].Forecast, 31.25)
	planAssertNear(t, "EXPO future 2", *ex.Points[5].Forecast, 31.25)
	if ex.RunID != nil {
		t.Fatal("unsaved forecast must not create a run")
	}
	// HW needs 2 seasons of history; with 4 buckets and season 4 it falls back to EXPO.
	hw, err := f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, Method: "HW", SeasonLength: 4, AsOfDate: "2026-01-27"}, fa)
	if err != nil || hw.Method != "EXPO" {
		t.Fatalf("HW fallback = %+v, %v", hw, err)
	}
	// HW with enough history (season 2, 4 buckets).
	hw2, err := f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, Method: "HW", SeasonLength: 2, HorizonPeriods: 2, AsOfDate: "2026-01-27"}, fa)
	if err != nil || hw2.Method != "HW" || len(hw2.Points) != 6 {
		t.Fatalf("HW = %+v, %v", hw2, err)
	}

	// Validation.
	_, err = f.Svc.Forecast.Run(f.Ctx, ForecastRequest{}, fa)
	planErrCode(t, "no item", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID}, ForecastActor{})
	planErrCode(t, "no actor", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, AsOfDate: "27/01/2026"}, fa)
	planErrCode(t, "bad asOf", err, domain.ErrCodeBadRequest)
	_, err = f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: empty.ID, AsOfDate: "2026-01-27"}, fa)
	planErrCode(t, "no history", err, domain.ErrCodeBadRequest)

	// Second saved version; activation archives the previous ACTIVE one.
	res2, err := f.Svc.Forecast.Run(f.Ctx, ForecastRequest{ItemID: it.ID, Window: 2, HorizonPeriods: 3, AsOfDate: "2026-01-27", SaveAsForecast: true}, fa)
	if err != nil || res2.Version != 2 {
		t.Fatalf("second version = %+v, %v", res2, err)
	}
	planErrCode(t, "publish draft", func() error { _, e := f.Svc.Forecast.ApplyConsumptionToMPS(f.Ctx, *res.RunID, fa); return e }(), domain.ErrCodeConflict)
	if err := f.Svc.Forecast.ActivateRun(f.Ctx, *res.RunID, fa); err != nil {
		t.Fatal(err)
	}
	if err := f.Svc.Forecast.ActivateRun(f.Ctx, *res.RunID, fa); err != nil {
		t.Fatalf("re-activating ACTIVE run must be idempotent: %v", err)
	}
	if err := f.Svc.Forecast.ActivateRun(f.Ctx, *res2.RunID, fa); err != nil {
		t.Fatal(err)
	}
	v1, err := f.Svc.Forecast.GetRun(f.Ctx, *res.RunID)
	if err != nil || v1.Run.Status != "ARCHIVED" || len(v1.Values) != 3 {
		t.Fatalf("v1 after v2 activation = %+v, %v", v1, err)
	}
	planErrCode(t, "activate archived", f.Svc.Forecast.ActivateRun(f.Ctx, *res.RunID, fa), domain.ErrCodeConflict)
	planErrCode(t, "activate unknown", f.Svc.Forecast.ActivateRun(f.Ctx, uuid.New(), fa), domain.ErrCodeNotFound)
	planErrCode(t, "activate anonymous", f.Svc.Forecast.ActivateRun(f.Ctx, *res2.RunID, ForecastActor{}), domain.ErrCodeUnauthorized)
	_, err = f.Svc.Forecast.GetRun(f.Ctx, uuid.New())
	planErrCode(t, "get unknown", err, domain.ErrCodeNotFound)
	runs, err := f.Svc.Forecast.ListRuns(f.Ctx, &it.ID)
	if err != nil || len(runs) != 2 || runs[0].Version != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if all, err := f.Svc.Forecast.ListRuns(f.Ctx, nil); err != nil || len(all) < 2 {
		t.Fatalf("all runs = %d, %v", len(all), err)
	}

	// Open confirmed orders inside future buckets consume the forecast.
	planConfirmedOrder(t, f, sa, cust.ID, "PLFC-O1", it.ID, 50, planDay(2026, 2, 3))
	planConfirmedOrder(t, f, sa, cust.ID, "PLFC-O2", it.ID, 10, planDay(2026, 2, 10))
	cons, err := f.Svc.Forecast.Consumption(f.Ctx, *res2.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if cons.ItemCode != "PLFC-FG" || cons.Version != 2 || len(cons.Buckets) != 3 {
		t.Fatalf("consumption header = %+v", cons)
	}
	want := []struct{ fc, ord, consumed, remaining, above, total float64 }{
		{35, 50, 35, 0, 15, 50},
		{37.5, 10, 10, 27.5, 0, 37.5},
		{36.25, 0, 0, 36.25, 0, 36.25},
	}
	for i, w := range want {
		b := cons.Buckets[i]
		planAssertNear(t, "forecast", b.ForecastQty, w.fc)
		planAssertNear(t, "orders", b.OrderQty, w.ord)
		planAssertNear(t, "consumed", b.ConsumedForecast, w.consumed)
		planAssertNear(t, "remaining", b.RemainingForecast, w.remaining)
		planAssertNear(t, "above", b.OrderAboveForecast, w.above)
		planAssertNear(t, "total", b.TotalDemand, w.total)
	}
	n, err := f.Svc.Forecast.ApplyConsumptionToMPS(f.Ctx, *res2.RunID, fa)
	if err != nil || n != 3 {
		t.Fatalf("publish to MPS = %d, %v", n, err)
	}
	mps, _ := f.Svc.MPS.List(f.Ctx)
	got := map[string]domain.MPSEntry{}
	for _, m := range mps {
		if m.ItemID == it.ID {
			got[m.Period.Format("2006-01-02")] = m
		}
	}
	for i, w := range want {
		m, ok := got[planDay(2026, 2, 2).AddDate(0, 0, 7*i).Format("2006-01-02")]
		if !ok || m.DemandBasis != "FORECAST_CONSUMPTION" || m.SourceForecastRunID == nil || *m.SourceForecastRunID != *res2.RunID {
			t.Fatalf("MPS bucket %d = %+v (present=%v)", i, m, ok)
		}
		planAssertNear(t, "MPS planned", m.Planned, w.total)
	}
	_, err = f.Svc.Forecast.ApplyConsumptionToMPS(f.Ctx, uuid.New(), fa)
	planErrCode(t, "publish unknown", err, domain.ErrCodeNotFound)
	_, err = f.Svc.Forecast.ApplyConsumptionToMPS(f.Ctx, *res2.RunID, ForecastActor{})
	planErrCode(t, "publish anonymous", err, domain.ErrCodeUnauthorized)
	_, err = f.Svc.Forecast.Consumption(f.Ctx, uuid.New())
	planErrCode(t, "consumption unknown", err, domain.ErrCodeNotFound)
}

// planSOPNullActivatedByWorkaround works around the ActivateProductMixVersion /
// ListProductMixVersions scan bug (activated_by is NULL for DRAFT rows but
// domain.SOPProductMixVersion.ActivatedBy is a plain string) so the rest of the
// S&OP flow can still be exercised. See TestPlanSOPActivateDraftMixScan.
func planSOPNullActivatedByWorkaround(t *testing.T, f *dbFixture) {
	t.Helper()
	if _, err := f.DB.Exec(`UPDATE sop_product_mix_versions SET activated_by='' WHERE activated_by IS NULL`); err != nil {
		t.Fatalf("workaround: %v", err)
	}
}

// Regression: activating a freshly created DRAFT product mix used to fail with
// "converting NULL to string is unsupported" (sop.go scanned the NULL
// activated_by into a string).
func TestPlanSOPActivateDraftMixScan(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	u := f.user(t, "plan-sop-bug", domain.RolePlanner)
	actor := SOPActor{UserID: u.ID, Username: u.Username}
	grp := domain.ItemGroup{Code: "PLSOPB-G", Name: "family"}
	if err := f.Svc.SOP.CreateGroup(f.Ctx, &grp); err != nil {
		t.Fatal(err)
	}
	it := planItem(t, f, domain.Item{Code: "PLSOPB-A", GroupID: &grp.ID})
	mix, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, grp.ID, "v1", []ProductMixInputLine{{ItemID: it.ID, MixPct: 100}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	// Regression: a DRAFT row (NULL activated_by) used to break both calls.
	if drafts, err := f.Svc.SOP.ListProductMixVersions(f.Ctx, &grp.ID); err != nil || len(drafts) != 1 || drafts[0].Status != "DRAFT" {
		t.Fatalf("list with a DRAFT mix = %+v, %v", drafts, err)
	}
	if err := f.Svc.SOP.ActivateProductMixVersion(f.Ctx, mix.ID, actor); err != nil {
		t.Fatalf("activate DRAFT mix: %v", err)
	}
	vs, err := f.Svc.SOP.ListProductMixVersions(f.Ctx, &grp.ID)
	if err != nil || len(vs) != 1 || vs[0].Status != "ACTIVE" || vs[0].ActivatedBy != u.Username {
		t.Fatalf("activated mix = %+v, %v", vs, err)
	}
}

// S&OP family plan -> product mix -> weekly disaggregation into MPS.
func TestPlanSOPProductMixDisaggregationToMPS(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	u := f.user(t, "plan-sop", domain.RolePlanner)
	actor := SOPActor{UserID: u.ID, Username: u.Username}
	grp := domain.ItemGroup{Code: "PLSOP-G", Name: "family"}
	if err := f.Svc.SOP.CreateGroup(f.Ctx, &grp); err != nil {
		t.Fatal(err)
	}
	other := domain.ItemGroup{Code: "PLSOP-O", Name: "other"}
	if err := f.Svc.SOP.CreateGroup(f.Ctx, &other); err != nil {
		t.Fatal(err)
	}
	if gs, err := f.Svc.SOP.ListGroups(f.Ctx); err != nil || len(gs) < 2 {
		t.Fatalf("groups = %d, %v", len(gs), err)
	}
	a := planItem(t, f, domain.Item{Code: "PLSOP-A", GroupID: &grp.ID})
	b := planItem(t, f, domain.Item{Code: "PLSOP-B", GroupID: &grp.ID})
	c := planItem(t, f, domain.Item{Code: "PLSOP-C", GroupID: &other.ID})

	bad := []struct {
		name  string
		group uuid.UUID
		lines []ProductMixInputLine
		code  domain.ErrorCode
	}{
		{"no lines", grp.ID, nil, domain.ErrCodeBadRequest},
		{"zero pct", grp.ID, []ProductMixInputLine{{ItemID: a.ID, MixPct: 0}}, domain.ErrCodeBadRequest},
		{"dup item", grp.ID, []ProductMixInputLine{{ItemID: a.ID, MixPct: 50}, {ItemID: a.ID, MixPct: 50}}, domain.ErrCodeBadRequest},
		{"not 100", grp.ID, []ProductMixInputLine{{ItemID: a.ID, MixPct: 50}, {ItemID: b.ID, MixPct: 40}}, domain.ErrCodeBadRequest},
		{"unknown group", uuid.New(), []ProductMixInputLine{{ItemID: a.ID, MixPct: 100}}, domain.ErrCodeNotFound},
	}
	for _, c := range bad {
		_, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, c.group, "x", c.lines, actor)
		planErrCode(t, c.name, err, c.code)
	}
	_, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, grp.ID, "x", []ProductMixInputLine{{ItemID: a.ID, MixPct: 100}}, SOPActor{})
	planErrCode(t, "anonymous mix", err, domain.ErrCodeUnauthorized)

	mix, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, grp.ID, " v1 ", []ProductMixInputLine{{ItemID: a.ID, MixPct: 60}, {ItemID: b.ID, MixPct: 40}}, actor)
	if err != nil || mix.Version != 1 || mix.Status != "DRAFT" || mix.Name != "v1" || len(mix.Lines) != 2 {
		t.Fatalf("mix v1 = %+v, %v", mix, err)
	}
	otherMix, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, other.ID, "o", []ProductMixInputLine{{ItemID: c.ID, MixPct: 100}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	planSOPNullActivatedByWorkaround(t, f)
	if err := f.Svc.SOP.ActivateProductMixVersion(f.Ctx, otherMix.ID, actor); err != nil {
		t.Fatal(err)
	}

	plan := domain.SOPPlan{GroupID: grp.ID, PlanMonth: planDay(2027, 3, 1), DemandQty: 120, SupplyQty: 100}
	if err := f.Svc.SOP.UpsertPlan(f.Ctx, &plan); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.SOP.PreviewDisaggregation(f.Ctx, plan.ID, mix.ID)
	planErrCode(t, "draft mix preview", err, domain.ErrCodeConflict)
	_, err = f.Svc.SOP.PreviewDisaggregation(f.Ctx, plan.ID, otherMix.ID)
	planErrCode(t, "family mismatch", err, domain.ErrCodeConflict)
	_, err = f.Svc.SOP.PreviewDisaggregation(f.Ctx, uuid.New(), mix.ID)
	planErrCode(t, "unknown plan", err, domain.ErrCodeNotFound)
	_, err = f.Svc.SOP.PreviewDisaggregation(f.Ctx, plan.ID, uuid.New())
	planErrCode(t, "unknown mix", err, domain.ErrCodeNotFound)

	if err := f.Svc.SOP.ActivateProductMixVersion(f.Ctx, mix.ID, actor); err != nil {
		t.Fatal(err)
	}
	planErrCode(t, "re-activate", f.Svc.SOP.ActivateProductMixVersion(f.Ctx, mix.ID, actor), domain.ErrCodeConflict)
	planErrCode(t, "activate unknown", f.Svc.SOP.ActivateProductMixVersion(f.Ctx, uuid.New(), actor), domain.ErrCodeNotFound)

	prev, err := f.Svc.SOP.PreviewDisaggregation(f.Ctx, plan.ID, mix.ID)
	if err != nil {
		t.Fatal(err)
	}
	// March 2027 = 31 days -> periods of 7,7,7,7,3 days starting 03-01/08/15/22/29.
	if len(prev.Lines) != 10 {
		t.Fatalf("preview lines = %d, want 2 items x 5 periods", len(prev.Lines))
	}
	totals := map[uuid.UUID]float64{}
	for i, l := range prev.Lines {
		j := i % 5
		planAssertDay(t, "period", l.Period, planDay(2027, 3, 1+7*j))
		target := 60.0
		if l.ItemID == b.ID {
			target = 40
		}
		days := 7.0
		if j == 4 {
			days = 3
		}
		planAssertNear(t, "time weight", l.TimeWeight, days/31)
		if j < 4 {
			planAssertNear(t, "weekly qty", l.PlannedQty, round6(target*7/31))
		}
		totals[l.ItemID] += l.PlannedQty
	}
	// The last period absorbs rounding so each item total is exactly its mix share.
	planAssertNear(t, "A total", totals[a.ID], 60)
	planAssertNear(t, "B total", totals[b.ID], 40)

	run, err := f.Svc.SOP.ApplyDisaggregationToMPS(f.Ctx, plan.ID, mix.ID, actor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if run.Status != "APPLIED" || run.TimePhasing != "CALENDAR_DAYS_7D" || run.SupplyQtySnapshot != 100 {
		t.Fatalf("run = %+v", run)
	}
	mps, _ := f.Svc.MPS.List(f.Ctx)
	var aSum float64
	for _, m := range mps {
		if m.ItemID == a.ID {
			aSum += m.Planned
			if m.DemandBasis != "SOP_DISAGGREGATION" || m.SourceSOPDisaggregationRunID == nil || *m.SourceSOPDisaggregationRunID != run.ID {
				t.Fatalf("MPS provenance = %+v", m)
			}
		}
	}
	planAssertNear(t, "A MPS sum", aSum, 60)
	if runs, err := f.Svc.SOP.ListDisaggregationRuns(f.Ctx, &plan.ID); err != nil || len(runs) != 1 {
		t.Fatalf("disaggregation runs = %d, %v", len(runs), err)
	}
	if runs, err := f.Svc.SOP.ListDisaggregationRuns(f.Ctx, nil); err != nil || len(runs) != 1 {
		t.Fatalf("all disaggregation runs = %d, %v", len(runs), err)
	}

	// Re-applying must not reduce MPS below already released quantity.
	if _, err := f.DB.Exec(`UPDATE mps_entries SET released=1000 WHERE item_id=$1 AND period=$2`, a.ID, planDay(2027, 3, 1)); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.SOP.ApplyDisaggregationToMPS(f.Ctx, plan.ID, mix.ID, actor)
	planErrCode(t, "below released", err, domain.ErrCodeConflict)
	_, err = f.Svc.SOP.ApplyDisaggregationToMPS(f.Ctx, plan.ID, mix.ID, SOPActor{})
	planErrCode(t, "anonymous apply", err, domain.ErrCodeUnauthorized)

	// A new version archives v1 on activation; listing returns lines.
	mix2, err := f.Svc.SOP.CreateProductMixVersion(f.Ctx, grp.ID, "v2", []ProductMixInputLine{{ItemID: a.ID, MixPct: 100}}, actor)
	if err != nil || mix2.Version != 2 {
		t.Fatalf("mix v2 = %+v, %v", mix2, err)
	}
	planSOPNullActivatedByWorkaround(t, f)
	if err := f.Svc.SOP.ActivateProductMixVersion(f.Ctx, mix2.ID, actor); err != nil {
		t.Fatal(err)
	}
	vs, err := f.Svc.SOP.ListProductMixVersions(f.Ctx, &grp.ID)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[0].Status != "ACTIVE" || vs[1].Status != "ARCHIVED" || len(vs[1].Lines) != 2 {
		t.Fatalf("mix versions = %+v, %v", vs, err)
	}
	if all, err := f.Svc.SOP.ListProductMixVersions(f.Ctx, nil); err != nil || len(all) != 3 {
		t.Fatalf("all mix versions = %d, %v", len(all), err)
	}

	_, err = CalcSOPDisaggregation(domain.SOPPlan{SupplyQty: -1, PlanMonth: planDay(2027, 3, 1)}, mix.ID, []domain.SOPProductMixLine{{MixPct: 100}})
	planErrCode(t, "negative supply", err, domain.ErrCodeBadRequest)
	_, err = CalcSOPDisaggregation(domain.SOPPlan{SupplyQty: 1, PlanMonth: planDay(2027, 3, 1)}, mix.ID, nil)
	planErrCode(t, "empty mix", err, domain.ErrCodeBadRequest)
	planAssertDay(t, "TruncateMonth", TruncateMonth(time.Date(2027, 3, 17, 13, 0, 0, 0, time.UTC)), planDay(2027, 3, 1))

	extra := domain.SOPPlan{GroupID: grp.ID, PlanMonth: planDay(2027, 4, 1), SupplyQty: 5}
	if err := f.Svc.SOP.UpsertPlan(f.Ctx, &extra); err != nil {
		t.Fatal(err)
	}
	if plans, err := f.Svc.SOP.ListPlans(f.Ctx); err != nil || len(plans) != 2 {
		t.Fatalf("plans = %d, %v", len(plans), err)
	}
	if err := f.Svc.SOP.DeletePlan(f.Ctx, extra.ID); err != nil {
		t.Fatal(err)
	}
	if plans, _ := f.Svc.SOP.ListPlans(f.Ctx); len(plans) != 1 || plans[0].ID != plan.ID {
		t.Fatalf("plans after delete = %+v", plans)
	}
}

// Cycle counts are scheduled from ABC classes for stocked items; recorded variances
// are reconciled through the lot ledger.
func TestPlanCycleCountScheduleAndReconcile(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	a := planItem(t, f, domain.Item{Code: "PLCC-A", StandardCost: 10})
	b := planItem(t, f, domain.Item{Code: "PLCC-B", StandardCost: 10})
	none := planItem(t, f, domain.Item{Code: "PLCC-NONE", StandardCost: 10})
	planReceive(t, f, a.ID, 10)
	planReceive(t, f, b.ID, 10)

	n, err := f.Svc.CycleCount.GenerateSchedule(f.Ctx)
	if err != nil || n < 2 {
		t.Fatalf("generate = %d, %v", n, err)
	}
	pending, err := f.Svc.CycleCount.List(f.Ctx, "PENDING")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]domain.CycleCountWithItem{}
	for _, c := range pending {
		ids[c.ItemCode] = c
	}
	for _, code := range []string{"PLCC-A", "PLCC-B"} {
		c, ok := ids[code]
		if !ok || c.ExpectedQty == nil || *c.ExpectedQty != 10 {
			t.Fatalf("cycle count for %s = %+v (present=%v)", code, c, ok)
		}
		planAssertDay(t, "scheduled", c.ScheduledDate, planToday())
	}
	if _, ok := ids["PLCC-NONE"]; ok {
		t.Fatal("item without stock must not be scheduled")
	}
	_ = none
	// Re-generating inside the interval creates nothing new for these items.
	if _, err := f.Svc.CycleCount.GenerateSchedule(f.Ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := f.Svc.CycleCount.List(f.Ctx, "")
	cnt := 0
	for _, c := range again {
		if c.ItemCode == "PLCC-A" {
			cnt++
		}
	}
	if cnt != 1 {
		t.Fatalf("PLCC-A cycle counts after regenerate = %d", cnt)
	}

	// Exact count: reconciled without adjustment.
	if err := f.Svc.CycleCount.RecordCount(f.Ctx, ids["PLCC-A"].ID, 10, "ok"); err != nil {
		t.Fatal(err)
	}
	// Short count: ADJUST -3 through the ledger.
	if err := f.Svc.CycleCount.RecordCount(f.Ctx, ids["PLCC-B"].ID, 7, "short"); err != nil {
		t.Fatal(err)
	}
	bal, _ := f.Svc.Inventory.Balance(f.Ctx)
	for _, x := range bal {
		switch x.ItemID {
		case a.ID:
			planAssertNear(t, "A on hand", x.OnHand, 10)
		case b.ID:
			planAssertNear(t, "B on hand after count", x.OnHand, 7)
		}
	}
	rec, _ := f.Svc.CycleCount.List(f.Ctx, "RECONCILED")
	got := 0
	for _, c := range rec {
		if c.ItemCode == "PLCC-A" || c.ItemCode == "PLCC-B" {
			got++
			if c.CountedQty == nil {
				t.Fatalf("counted qty missing: %+v", c)
			}
		}
	}
	if got != 2 {
		t.Fatalf("reconciled counts = %d", got)
	}
	if err := f.Svc.CycleCount.RecordCount(f.Ctx, uuid.New(), 1, ""); err == nil {
		t.Fatal("unknown cycle count must error")
	}
}

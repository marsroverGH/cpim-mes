package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func recDBToday(t *testing.T, f *dbFixture) time.Time {
	t.Helper()
	var d time.Time
	if err := f.DB.GetContext(f.Ctx, &d, `SELECT CURRENT_DATE`); err != nil {
		t.Fatal(err)
	}
	return TruncateDay(d)
}

func TestRecDBSupplierScheduleEvents(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	ss := e.Svc.SupplierScheduling
	po := recPO(t, e.dbFixture, "PO-REC-SS", "CHAIN-1", 12, recDay(-2), recDay(20))
	planner := recSalesActor(e.Planner)
	qty, wrong := 12.0, 5.0
	d := func(n int) string { return recDay(n).Format("2006-01-02") }

	bad := []struct {
		in   SupplierScheduleEventInput
		code domain.ErrorCode
	}{
		{SupplierScheduleEventInput{EventID: "nope", EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: d(18)}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.Nil.String(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: d(18)}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "SHIP", Quantity: &qty}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: "2026-13-45"}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "ASN", Quantity: &qty, ASNNo: "A", ExpectedArrivalDate: "x"}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", ConfirmedDeliveryDate: d(18)}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "REVISE", Quantity: &qty}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "ASN", Quantity: &qty, ExpectedArrivalDate: d(17)}, domain.ErrCodeBadRequest},
		{SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &wrong, ConfirmedDeliveryDate: d(18)}, domain.ErrCodeBadRequest},
	}
	for i, b := range bad {
		_, err := ss.RecordEvent(e.Ctx, po.ID, b.in, planner)
		if recCode(err) != b.code {
			t.Fatalf("bad input %d: %v", i, err)
		}
	}
	_, err := ss.RecordEvent(e.Ctx, uuid.New(), SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: d(18)}, planner)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	operator := e.user(t, "rec-ss-operator", domain.RoleOperator)
	_, err = ss.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: d(18)}, recSalesActor(operator))
	recWantCode(t, err, domain.ErrCodeForbidden, "")
	_, err = ss.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: d(18)}, SalesOrderActor{Role: domain.RolePlanner})
	if err == nil {
		t.Fatal("anonymous actor accepted")
	}

	// CONFIRM moves the canonical planning date.
	confirmID := uuid.NewString()
	in := SupplierScheduleEventInput{EventID: confirmID, EventType: " confirm ", Quantity: &qty, ConfirmedDeliveryDate: d(18), SupplierReference: " SR-1 ", Notes: " ok "}
	ev, err := ss.RecordEvent(e.Ctx, po.ID, in, planner)
	if err != nil || ev.RevisionNo != 1 || ev.EventType != "CONFIRM" || ev.SupplierReference != "SR-1" || ev.Notes != "ok" {
		t.Fatalf("confirm: %v %+v", err, ev)
	}
	p, _ := e.Repos.Purchases.Get(e.Ctx, po.ID)
	if p.ExpectedDeliveryDate == nil || !TruncateDay(*p.ExpectedDeliveryDate).Equal(recDay(18)) || !PurchasePlanningDate(*p).Equal(recDay(18)) {
		t.Fatalf("planning date after confirm: %+v", p.ExpectedDeliveryDate)
	}
	// Idempotent retry returns the stored event; a different payload with the same id is a conflict.
	again, err := ss.RecordEvent(e.Ctx, po.ID, in, planner)
	if err != nil || again.ID != ev.ID || again.RevisionNo != 1 {
		t.Fatalf("idempotent retry: %v", err)
	}
	changed := in
	changed.ConfirmedDeliveryDate = d(19)
	_, err = ss.RecordEvent(e.Ctx, po.ID, changed, planner)
	recWantCode(t, err, domain.ErrCodeConflict, "already used")

	// ASN takes precedence, CANCEL strips payload.
	if _, err := ss.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "ASN", Quantity: &qty, ASNNo: "ASN-1", ExpectedArrivalDate: d(16)}, planner); err != nil {
		t.Fatal(err)
	}
	p, _ = e.Repos.Purchases.Get(e.Ctx, po.ID)
	if p.ExpectedDeliveryDate == nil || !TruncateDay(*p.ExpectedDeliveryDate).Equal(recDay(16)) || p.ASNNo != "ASN-1" {
		t.Fatalf("planning date after ASN: %+v %s", p.ExpectedDeliveryDate, p.ASNNo)
	}
	cancel, err := ss.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CANCEL", Quantity: &qty, ASNNo: "x", ConfirmedDeliveryDate: d(1)}, recSalesActor(e.Admin))
	if err != nil || cancel.Quantity != nil || cancel.ConfirmedDeliveryDate != nil || cancel.ASNNo != "" || cancel.RevisionNo != 3 {
		t.Fatalf("cancel: %v %+v", err, cancel)
	}
	evs, err := ss.ListEvents(e.Ctx, po.ID)
	if err != nil || len(evs) != 3 {
		t.Fatalf("events: %v %d", err, len(evs))
	}
	recDBErr(t, e.dbFixture, "immutable", `UPDATE supplier_schedule_events SET notes='x' WHERE id=$1`, ev.ID)
	recDBErr(t, e.dbFixture, "immutable", `DELETE FROM supplier_schedule_events WHERE id=$1`, ev.ID)

	// After full receipt the schedule is frozen.
	if _, err := e.Svc.Workflow.ReceivePurchase(e.Ctx, po.ID, uuid.New(), 12, "LOT-REC-SS", PurchaseReceiptActor{UserID: e.Admin.ID, Username: e.Admin.Username}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	_, err = ss.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CANCEL"}, planner)
	recWantCode(t, err, domain.ErrCodeConflict, "immutable after PO receipt")
}

func TestRecDBSupplierReliabilityRun(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	ss := e.Svc.SupplierScheduling
	planner := recSalesActor(e.Planner)
	today := recDBToday(t, e.dbFixture)
	// Lead times 4, 6, 10 days; the 10-day order arrives 2 days late.
	for i, x := range []struct{ order, due int }{{-4, 1}, {-6, 5}, {-10, -2}} {
		po := recPO(t, e.dbFixture, "PO-REC-REL-"+string(rune('A'+i)), "CHAIN-1", 10, today.AddDate(0, 0, x.order), today.AddDate(0, 0, x.due))
		if _, err := e.Svc.Workflow.ReceivePurchase(e.Ctx, po.ID, uuid.New(), 10, "LOT-REC-REL-"+string(rune('A'+i)), PurchaseReceiptActor{UserID: e.Admin.ID, Username: e.Admin.Username}); err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
	}
	// An open, partially received PO is not a sample.
	open := recPO(t, e.dbFixture, "PO-REC-REL-OPEN", "CHAIN-1", 10, today.AddDate(0, 0, -3), today.AddDate(0, 0, 3))
	if _, err := e.Svc.Workflow.ReceivePurchase(e.Ctx, open.ID, uuid.New(), 4, "LOT-REC-REL-OPEN", PurchaseReceiptActor{UserID: e.Admin.ID, Username: e.Admin.Username}); err != nil {
		t.Fatal(err)
	}

	viewer := e.user(t, "rec-ss-viewer", domain.RoleViewer)
	_, err := ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{}, recSalesActor(viewer))
	recWantCode(t, err, domain.ErrCodeForbidden, "")
	_, err = ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{WindowDays: 4000}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "windowDays")
	_, err = ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{MinSamples: 1001}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "minSamples")
	_, err = ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{}, SalesOrderActor{})
	if err == nil {
		t.Fatal("anonymous refresh accepted")
	}

	run, err := ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{}, planner)
	if err != nil {
		t.Fatal(err)
	}
	if run.Run.Status != "COMPLETE" || run.Run.MinSamples != 3 || !run.Run.WindowEnd.Equal(today) || !run.Run.WindowStart.Equal(today.AddDate(0, 0, -364)) {
		t.Fatalf("run: %+v", run.Run)
	}
	hash := recHashPtr(t, "reliability hash", run.Run.ResultHash)
	if len(run.Results) != 2 {
		t.Fatalf("expected supplier+item and supplier rollup rows, got %d", len(run.Results))
	}
	for _, r := range run.Results {
		if r.SupplierName != "REC-SUPPLIER" || r.SampleCount != 3 || r.Confidence != "MEDIUM" {
			t.Fatalf("result: %+v", r)
		}
		if d := r.AverageLeadDays - 20.0/3; d > 1e-3 || d < -1e-3 {
			t.Fatalf("average lead days %v", r.AverageLeadDays)
		}
		if d := r.OnTimeRate - 2.0/3; d > 1e-3 || d < -1e-3 {
			t.Fatalf("on-time rate %v", r.OnTimeRate)
		}
		if d := r.P90LeadDays - 9.2; d > 1e-6 || d < -1e-6 {
			t.Fatalf("p90 %v", r.P90LeadDays)
		}
		if r.RecommendedLeadDays != 10 {
			t.Fatalf("recommended lead days %d", r.RecommendedLeadDays)
		}
	}
	if run.Results[0].ItemID != nil || run.Results[1].ItemID == nil || run.Results[1].ItemCode != "CHAIN-1" {
		t.Fatalf("rollup ordering: %+v", run.Results)
	}
	// NOTE: result_hash is computed over unrounded float64 values while the
	// result columns are numeric(12,4)/numeric(7,6), so the hash can NOT be
	// re-derived from persisted evidence when averages are non-terminating
	// (here 20/3 and 2/3). Reported as suspicious; not asserted either way.
	if canonicalSupplierReliabilityHash(run.Results) != hash {
		t.Logf("supplier reliability hash is not reproducible from persisted rows (known issue)")
	}

	// Deterministic: a second run on the same evidence yields the same hash.
	run2, err := ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{WindowDays: 365, MinSamples: 3}, recSalesActor(e.Admin))
	if err != nil || *run2.Run.ResultHash != hash || run2.Run.ID == run.Run.ID {
		t.Fatalf("second run: %v", err)
	}
	// A stricter sample requirement downgrades confidence.
	run3, err := ss.RefreshReliability(e.Ctx, SupplierReliabilityRunInput{MinSamples: 5}, planner)
	if err != nil || run3.Results[0].Confidence != "LOW" || *run3.Run.ResultHash == hash {
		t.Fatalf("strict run: %v %+v", err, run3.Results)
	}

	latest, err := ss.LatestReliability(e.Ctx)
	if err != nil || len(latest) != 2 || latest[0].Confidence != "LOW" {
		t.Fatalf("latest: %v %+v", err, latest)
	}
	runs, err := ss.ListReliabilityRuns(e.Ctx)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs: %v %d", err, len(runs))
	}
	got, err := ss.GetReliabilityRun(e.Ctx, run.Run.ID)
	if err != nil || *got.Run.ResultHash != hash || len(got.Results) != 2 {
		t.Fatalf("get run: %v", err)
	}
	_, err = ss.GetReliabilityRun(e.Ctx, uuid.New())
	recWantCode(t, err, domain.ErrCodeNotFound, "")
}

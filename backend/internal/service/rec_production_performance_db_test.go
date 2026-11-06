package service

import (
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func recPPActor(u *domain.User) ProductionPerformanceActor {
	return ProductionPerformanceActor{UserID: u.ID, Username: u.Username, Role: u.Role}
}

// recPPProduce releases a WHEEL-1 WO (single WC-ASSY operation) and records
// start, pause/resume, scrap and completion actuals on its operation.
func recPPProduce(t *testing.T, e *recEnv, orderNo string, good, scrap float64) uuid.UUID {
	t.Helper()
	wo := recWO(t, e.dbFixture, orderNo, "WHEEL-1", good, recDay(0), recDay(2))
	if _, err := e.Svc.Workflow.ReleaseWorkOrder(e.Ctx, wo.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	var opID uuid.UUID
	if err := e.DB.GetContext(e.Ctx, &opID, `SELECT id FROM wo_operations WHERE wo_id=$1 ORDER BY seq_no LIMIT 1`, wo.ID); err != nil {
		t.Fatal(err)
	}
	sf := e.Svc.ShopFloor
	actor := ShopFloorActor{UserID: e.Admin.ID, Username: e.Admin.Username}
	if err := sf.Start(e.Ctx, opID, actor); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := sf.Stop(e.Ctx, opID, actor, "pause"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := sf.Start(e.Ctx, opID, actor); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if scrap > 0 {
		if err := sf.ReportScrap(e.Ctx, opID, scrap, actor, "loss"); err != nil {
			t.Fatalf("scrap: %v", err)
		}
	}
	if err := sf.Complete(e.Ctx, opID, good, actor, "done"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	return opID
}

func TestRecDBProdPerfRunFeedbackLifecycle(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	pp := e.Svc.ProductionPerformance
	planner := recPPActor(e.Planner)

	// Actor and window validation.
	if _, err := pp.Run(e.Ctx, ProductionPerformanceRequest{}, ProductionPerformanceActor{}); recCode(err) != domain.ErrCodeUnauthorized {
		t.Fatalf("anonymous: %v", err)
	}
	if _, err := pp.Run(e.Ctx, ProductionPerformanceRequest{}, ProductionPerformanceActor{UserID: uuid.New(), Username: "op", Role: domain.RoleOperator}); recCode(err) != domain.ErrCodeForbidden {
		t.Fatalf("operator: %v", err)
	}
	_, err := pp.Run(e.Ctx, ProductionPerformanceRequest{WindowStart: recDay(0), WindowEnd: recDay(-1)}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "on/after")
	_, err = pp.Run(e.Ctx, ProductionPerformanceRequest{WindowStart: recDay(-400), WindowEnd: recDay(0)}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "366 days")

	// Empty plant: run completes, no feedback, master values recommended.
	empty, err := pp.Run(e.Ctx, ProductionPerformanceRequest{}, planner)
	if err != nil || empty.Run.Status != "COMPLETE" || len(empty.Feedback) != 0 || empty.Run.MinCompletedOps != 3 {
		t.Fatalf("empty run: %v %+v", err, empty)
	}
	if empty.Run.ResultHash == nil || !recIsHash(*empty.Run.ResultHash) || len(empty.Results) != 4 {
		t.Fatalf("empty run hash/results: %+v", empty.Run)
	}
	for _, r := range empty.Results {
		if r.SampleCount != 0 || r.Confidence != "LOW" || r.OEE != 0 {
			t.Fatalf("empty result: %+v", r)
		}
	}

	// Actuals on WC-ASSY plus a past breakdown.
	recPPProduce(t, e, "WO-PP-1", 2, 0.5)
	recPPProduce(t, e, "WO-PP-2", 1, 0)
	assyID := recWCID(t, e.dbFixture, "WC-ASSY")
	if _, err := e.Svc.Maintenance.Create(e.Ctx, MaintenanceEventInput{WorkCenterID: assyID, EventType: "BREAKDOWN", Status: "ACTIVE", StartAt: recDay(0), EndAt: recDay(0).Add(time.Hour), UnavailableMachines: 1, UnavailableWorkers: 0, Reason: "rec test"}, recSalesActor(e.Admin)); err != nil {
		t.Fatalf("breakdown: %v", err)
	}

	run, err := pp.Run(e.Ctx, ProductionPerformanceRequest{WindowStart: recDay(0), WindowEnd: recDay(0), MinCompletedOps: 1}, planner)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	hash := *run.Run.ResultHash
	if !recIsHash(hash) || run.Run.WindowStart != recDay(0) {
		t.Fatalf("run: %+v", run.Run)
	}
	var assy domain.ProductionPerformanceResult
	for _, r := range run.Results {
		if r.WorkCenterCode == "WC-ASSY" {
			assy = r
		} else if r.SampleCount != 0 {
			t.Fatalf("unexpected samples on %s", r.WorkCenterCode)
		}
	}
	if assy.SampleCount != 2 || math.Abs(assy.GoodQuantity-3) > 1e-9 || math.Abs(assy.RejectQuantity-0.5) > 1e-9 {
		t.Fatalf("assy quantities: %+v", assy)
	}
	if math.Abs(assy.Quality-3/3.5) > 1e-6 || assy.Quality >= 1 || assy.OEE < 0 || assy.OEE > 1 || assy.Confidence != "MEDIUM" {
		t.Fatalf("assy ratios: q=%v oee=%v conf=%s", assy.Quality, assy.OEE, assy.Confidence)
	}
	if assy.BreakdownCount != 1 || math.Abs(assy.UnplannedDowntimeMinutes-60) > 0.5 || math.Abs(assy.MTTRMinutes-60) > 0.5 || assy.DowntimeMinutes < assy.UnplannedDowntimeMinutes {
		t.Fatalf("breakdown evidence: %+v", assy)
	}
	if assy.RecommendedEfficiency < 0.5 || assy.RecommendedEfficiency > 1.2 || assy.RecommendedUtilization < 0.5 || assy.RecommendedUtilization > 1 {
		t.Fatalf("recommendations out of clamp: %+v", assy)
	}
	if len(run.Feedback) != 1 || run.Feedback[0].WorkCenterID != assyID || run.Feedback[0].Status != "DRAFT" || run.Feedback[0].VersionNo != 1 || run.Feedback[0].SourceResultID != assy.ID {
		t.Fatalf("feedback: %+v", run.Feedback)
	}
	// The hash is canonical over stored results.
	got, err := pp.GetRun(e.Ctx, run.Run.ID)
	if err != nil || len(got.Results) != 4 || len(got.Feedback) != 1 || canonicalProductionPerformanceHash(got.Results) != hash {
		t.Fatalf("get run / hash stability: %v", err)
	}
	if _, err := pp.GetRun(e.Ctx, uuid.New()); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("missing run: %v", err)
	}
	runs, err := pp.ListRuns(e.Ctx)
	if err != nil || len(runs) != 2 {
		t.Fatalf("list runs: %v %d", err, len(runs))
	}
	recDBErr(t, e.dbFixture, "", `UPDATE production_performance_results SET oee=1 WHERE run_id=$1`, run.Run.ID)

	// Feedback activation rules.
	draft := run.Feedback[0]
	if _, err := pp.ActivateFeedback(e.Ctx, draft.ID, time.Time{}, "", ProductionPerformanceActor{}); recCode(err) != domain.ErrCodeUnauthorized {
		t.Fatalf("anon activate: %v", err)
	}
	_, err = pp.ActivateFeedback(e.Ctx, uuid.New(), time.Time{}, "", planner)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = pp.ActivateFeedback(e.Ctx, draft.ID, recDay(-5), "", planner)
	recWantCode(t, err, domain.ErrCodeConflict, "effectiveFrom is frozen")
	act, err := pp.ActivateFeedback(e.Ctx, draft.ID, time.Time{}, " empirical ", planner)
	if err != nil || act.Status != "ACTIVE" || act.Notes != "empirical" || act.WorkCenterCode != "WC-ASSY" {
		t.Fatalf("activate: %v %+v", err, act)
	}
	_, err = pp.ActivateFeedback(e.Ctx, draft.ID, time.Time{}, "", planner)
	recWantCode(t, err, domain.ErrCodeConflict, "only DRAFT")

	// The active feedback calibrates work centers without mutating the master.
	wcs, err := e.Repos.WorkCenters.List(e.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	applied, snaps, err := ApplyCapacityFeedbackToWorkCenters(e.Ctx, e.DB, wcs, time.Time{})
	if err != nil || len(snaps) != 1 || snaps[0].FeedbackVersionID != act.ID {
		t.Fatalf("apply feedback: %v %+v", err, snaps)
	}
	for i, w := range applied {
		if w.ID == assyID {
			if w.Efficiency != act.EffectiveEfficiency || w.Utilization != act.EffectiveUtilization {
				t.Fatalf("feedback not applied: %+v", w)
			}
		} else if w.Efficiency != wcs[i].Efficiency {
			t.Fatalf("other WC changed")
		}
	}
	if master, _ := e.Repos.WorkCenters.Get(e.Ctx, assyID); master.Efficiency != 0.95 || master.Utilization != 0.85 {
		t.Fatalf("work center master must not be mutated: %+v", master)
	}
	if _, s2, _ := ApplyCapacityFeedbackToWorkCenters(e.Ctx, e.DB, wcs, recDay(-10)); len(s2) != 0 {
		t.Fatalf("feedback must not apply before its effective date")
	}

	// A second run produces version 2; activating it archives version 1.
	run2, err := pp.Run(e.Ctx, ProductionPerformanceRequest{WindowStart: recDay(0), WindowEnd: recDay(0), MinCompletedOps: 1}, planner)
	if err != nil || len(run2.Feedback) != 1 || run2.Feedback[0].VersionNo != 2 {
		t.Fatalf("run2: %v", err)
	}
	if *run2.Run.ResultHash != hash {
		t.Fatalf("same evidence must hash identically: %s vs %s", *run2.Run.ResultHash, hash)
	}
	if _, err := pp.ActivateFeedback(e.Ctx, run2.Feedback[0].ID, time.Time{}, "", planner); err != nil {
		t.Fatal(err)
	}
	fb, err := pp.ListFeedback(e.Ctx)
	if err != nil || len(fb) != 2 || fb[0].VersionNo != 2 || fb[0].Status != "ACTIVE" || fb[1].Status != "ARCHIVED" {
		t.Fatalf("feedback list: %v %+v", err, fb)
	}
	arch, err := pp.ArchiveFeedback(e.Ctx, run2.Feedback[0].ID, " retire ", planner)
	if err != nil || arch.Status != "ARCHIVED" || arch.Notes != "retire" {
		t.Fatalf("archive: %v", err)
	}
	_, err = pp.ArchiveFeedback(e.Ctx, run2.Feedback[0].ID, "", planner)
	recWantCode(t, err, domain.ErrCodeConflict, "already ARCHIVED")
	_, err = pp.ArchiveFeedback(e.Ctx, uuid.New(), "", planner)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	if _, err := pp.ArchiveFeedback(e.Ctx, draft.ID, "", ProductionPerformanceActor{UserID: uuid.New(), Username: "v", Role: domain.RoleViewer}); recCode(err) != domain.ErrCodeForbidden {
		t.Fatalf("viewer archive: %v", err)
	}

	// Future-effective feedback must stay DRAFT.
	future, err := pp.Run(e.Ctx, ProductionPerformanceRequest{WindowStart: recDay(0), WindowEnd: recDay(3), MinCompletedOps: 1}, planner)
	if err != nil || len(future.Feedback) != 1 {
		t.Fatalf("future run: %v", err)
	}
	_, err = pp.ActivateFeedback(e.Ctx, future.Feedback[0].ID, time.Time{}, "", planner)
	recWantCode(t, err, domain.ErrCodeConflict, "future-effective")
}

package service

import (
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// recOperationalFingerprint captures operational tables that 0041 simulation
// and publication must never touch.
func recOperationalFingerprint(t *testing.T, f *dbFixture) string {
	t.Helper()
	var s string
	err := f.DB.GetContext(f.Ctx, &s, `SELECT md5(
  COALESCE((SELECT string_agg(id::text||status||start_date::text||due_date::text, ',' ORDER BY id) FROM work_orders),'') ||
  COALESCE((SELECT string_agg(id::text||status||due_date::text, ',' ORDER BY id) FROM purchase_orders),'') ||
  (SELECT count(*) FROM supplier_schedule_events)::text ||
  (SELECT count(*) FROM recovery_capacity_overrides)::text ||
  (SELECT count(*) FROM schedule_reschedule_signals)::text ||
  (SELECT count(*) FROM inventory_txns)::text)`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecDBScenarioLifecycleWithControlTowerBaseline(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rp := e.Svc.RecoveryPlanning
	base := recControlTowerBaseline(t, e)
	recWO(t, e.dbFixture, "WO-REC-SCN", "WHEEL-1", 1, recDay(10), recDay(15))
	recPO(t, e.dbFixture, "PO-REC-SCN", "CHAIN-1", 10, recDay(-1), recDay(20))
	fp := recOperationalFingerprint(t, e.dbFixture)

	// Input validation.
	_, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "  "}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "name is required")
	_, err = rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: strings.Repeat("x", 201)}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "200 characters")
	_, err = rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "x"}, RecoveryPlanningActor{UserID: uuid.New(), Username: "op", Role: "operator"})
	recWantCode(t, err, domain.ErrCodeUnauthorized, "")

	sc, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "  Recovery A ", Description: " desc "}, e.PlannerActor)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Status != "DRAFT" || sc.Name != "Recovery A" || sc.Description != "desc" || !strings.HasPrefix(sc.ScenarioNo, "REC-") || sc.CreatedByUserID == nil || *sc.CreatedByUserID != e.Planner.ID {
		t.Fatalf("created scenario: %+v", sc)
	}
	upd, err := rp.UpdateScenario(e.Ctx, sc.ID, RecoveryScenarioUpdateInput{Name: "Recovery A2", Description: "d2"}, e.PlannerActor)
	if err != nil || upd.Name != "Recovery A2" || upd.Description != "d2" {
		t.Fatalf("update scenario: %v %+v", err, upd)
	}
	_, err = rp.UpdateScenario(e.Ctx, uuid.New(), RecoveryScenarioUpdateInput{Name: "n"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = rp.UpdateScenario(e.Ctx, sc.ID, RecoveryScenarioUpdateInput{Name: ""}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "")

	// Simulation requires at least one action.
	_, err = rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "at least one action")
	_, err = rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: uuid.New()}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID, HorizonDays: 1000}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "horizonDays")

	// Action validation.
	bad := []RecoveryScenarioActionInput{
		{ActionType: "TELEPORT", TargetType: "WORK_ORDER", TargetRef: "*"},
		{ActionType: "RELEASE_WO", TargetType: "SPACESHIP", TargetRef: "*"},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: " "},
		{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "*"},
		{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "*", Parameters: recParams(t, map[string]any{"recoveryDays": 1.5})},
		{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "*", Parameters: recParams(t, map[string]any{"expediteDays": 999})},
		{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "*", Parameters: recParams(t, map[string]any{"recoveryDays": 1})},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*", EstimatedCost: -1},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*", SequenceNo: -2},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*", Parameters: []byte(`[1,2]`)},
	}
	for i, in := range bad {
		if _, err := rp.AddScenarioAction(e.Ctx, sc.ID, in, e.PlannerActor); recCode(err) != domain.ErrCodeBadRequest {
			t.Fatalf("bad action %d accepted or wrong error: %v", i, err)
		}
	}
	_, err = rp.AddScenarioAction(e.Ctx, uuid.New(), RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")

	a1, err := rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{ActionType: "expedite_po", TargetType: "purchase_order", TargetRef: " * ", Parameters: recParams(t, map[string]any{"expediteDays": 3}), EstimatedCost: 100.256, Note: " n "}, e.PlannerActor)
	if err != nil {
		t.Fatal(err)
	}
	if a1.SequenceNo != 1 || a1.ActionType != "EXPEDITE_PO" || a1.TargetType != "PURCHASE_ORDER" || a1.TargetRef != "*" || a1.EstimatedCost != 100.26 || a1.Note != "n" {
		t.Fatalf("normalized action: %+v", a1)
	}
	a2, err := rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "*", Parameters: recParams(t, map[string]any{"overtimeMinutes": 120})}, e.PlannerActor)
	if err != nil || a2.SequenceNo != 2 {
		t.Fatalf("auto sequence: %v %+v", err, a2)
	}
	a3, err := rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{SequenceNo: 10, ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	if err != nil || a3.SequenceNo != 10 {
		t.Fatalf("explicit sequence: %v", err)
	}
	a2u, err := rp.UpdateScenarioAction(e.Ctx, sc.ID, a2.ID, RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "*", Parameters: recParams(t, map[string]any{"recoveryDays": 4}), EstimatedCost: 5}, e.PlannerActor)
	if err != nil || a2u.ID != a2.ID || a2u.ActionType != "RESCHEDULE_WO" || a2u.SequenceNo != 2 {
		t.Fatalf("update action: %v %+v", err, a2u)
	}
	_, err = rp.UpdateScenarioAction(e.Ctx, sc.ID, uuid.New(), RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = rp.UpdateScenarioAction(e.Ctx, sc.ID, a2.ID, RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "recoveryDays")
	if err := rp.DeleteScenarioAction(e.Ctx, sc.ID, a3.ID, e.PlannerActor); err != nil {
		t.Fatal(err)
	}
	recWantCode(t, rp.DeleteScenarioAction(e.Ctx, sc.ID, a3.ID, e.PlannerActor), domain.ErrCodeNotFound, "")
	recWantCode(t, rp.DeleteScenarioAction(e.Ctx, uuid.New(), a3.ID, e.PlannerActor), domain.ErrCodeNotFound, "")
	acts, err := rp.ListScenarioActions(e.Ctx, sc.ID)
	if err != nil || len(acts) != 2 || acts[0].ID != a1.ID || acts[1].ID != a2.ID {
		t.Fatalf("list actions: %v %d", err, len(acts))
	}

	// Simulate against the Control Tower baseline.
	sim, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID, HorizonDays: 60}, e.PlannerActor)
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if sim.Reused || sim.Run.Status != "SUCCEEDED" || sim.Run.HorizonDays != 60 || sim.Run.CompletedAt == nil {
		t.Fatalf("run: %+v", sim.Run)
	}
	runHash := recHashPtr(t, "run result hash", sim.Run.ResultHash)
	if !recIsHash(sim.Run.BaselineHash) || !recIsHash(sim.Run.RequestHash) || !recIsHash(sim.Summary.ResultHash) {
		t.Fatal("run/summary hashes must be canonical")
	}
	if len(sim.Cases) != base.CasesTouched || sim.Summary.BaselineOpenCases != base.CasesTouched {
		t.Fatalf("baseline cases: %d/%d vs %d", len(sim.Cases), sim.Summary.BaselineOpenCases, base.CasesTouched)
	}
	// Seeded cases are zero-impact MATERIAL_SHORTAGE: a wildcard EXPEDITE_PO resolves all of them.
	if len(sim.Actions) != 2 || sim.Summary.SimulatedOpenCases != 0 || sim.Summary.OpenCaseReduction != base.CasesTouched || sim.Summary.EstimatedActionCost != 105.26 {
		t.Fatalf("summary: %+v actions=%d", sim.Summary, len(sim.Actions))
	}
	for _, c := range sim.Cases {
		if !recIsHash(c.ResultHash) || c.SimulatedImpactDays > c.BaselineImpactDays {
			t.Fatalf("case result: %+v", c)
		}
	}
	got, _ := rp.GetScenario(e.Ctx, sc.ID)
	if got.Status != "SIMULATED" {
		t.Fatalf("status after simulate: %s", got.Status)
	}
	if recOperationalFingerprint(t, e.dbFixture) != fp {
		t.Fatal("simulation must not mutate operational state")
	}

	// Canonical reuse of identical requests; a different horizon is a new run.
	again, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID, HorizonDays: 60}, e.PlannerActor)
	if err != nil || !again.Reused || again.Run.ID != sim.Run.ID || *again.Run.ResultHash != runHash || len(again.Cases) != len(sim.Cases) {
		t.Fatalf("reuse: %v %+v", err, again.Run)
	}
	sim2, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID, HorizonDays: 90}, e.PlannerActor)
	if err != nil || sim2.Reused || sim2.Run.ID == sim.Run.ID || sim2.Run.RequestHash == sim.Run.RequestHash || sim2.Run.BaselineHash != sim.Run.BaselineHash {
		t.Fatalf("new horizon: %v", err)
	}

	// SIMULATED definitions are frozen (service + DB).
	_, err = rp.UpdateScenario(e.Ctx, sc.ID, RecoveryScenarioUpdateInput{Name: "late"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only DRAFT")
	_, err = rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only while DRAFT")
	_, err = rp.UpdateScenarioAction(e.Ctx, sc.ID, a1.ID, RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only while DRAFT")
	recWantCode(t, rp.DeleteScenarioAction(e.Ctx, sc.ID, a1.ID, e.PlannerActor), domain.ErrCodeConflict, "only while DRAFT")
	recDBErr(t, e.dbFixture, "immutable", `UPDATE recovery_scenarios SET name='tamper' WHERE id=$1`, sc.ID)
	recDBErr(t, e.dbFixture, "immutable recovery simulation evidence", `UPDATE recovery_scenario_summaries SET net_value=0 WHERE run_id=$1`, sim.Run.ID)
	recDBErr(t, e.dbFixture, "immutable recovery simulation evidence", `DELETE FROM recovery_scenario_case_results WHERE run_id=$1`, sim.Run.ID)

	// Compare view.
	cmp, err := rp.CompareScenarios(e.Ctx, sim.Run.BaselineHash)
	if err != nil || len(cmp) == 0 {
		t.Fatalf("compare: %v %d", err, len(cmp))
	}
	all, err := rp.CompareScenarios(e.Ctx, "")
	if err != nil || len(all) < len(cmp) {
		t.Fatalf("compare all: %v", err)
	}
	_, err = rp.CompareScenarios(e.Ctx, "abc")
	recWantCode(t, err, domain.ErrCodeBadRequest, "64-character")

	// Publication rules.
	_, err = rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "runId is required")
	_, err = rp.PublishScenario(e.Ctx, uuid.Nil, RecoveryScenarioPublishInput{RunID: sim.Run.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "scenarioId is required")
	_, err = rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{RunID: uuid.New()}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "successful run")
	_, err = rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{RunID: sim.Run.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "latest successful")
	_, err = rp.PublishScenario(e.Ctx, uuid.New(), RecoveryScenarioPublishInput{RunID: sim2.Run.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	pub, err := rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{RunID: sim2.Run.ID, Comment: " go "}, e.PlannerActor)
	if err != nil || pub.RunID != sim2.Run.ID || !recIsHash(pub.PublicationHash) || pub.Comment != "go" {
		t.Fatalf("publish: %v %+v", err, pub)
	}
	_, err = rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{RunID: sim2.Run.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only SIMULATED")
	_, err = rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "published")
	if recOperationalFingerprint(t, e.dbFixture) != fp {
		t.Fatal("publication must not mutate operational state")
	}
	got, _ = rp.GetScenario(e.Ctx, sc.ID)
	if got.Status != "PUBLISHED" || got.PublishedAt == nil {
		t.Fatalf("published scenario: %+v", got)
	}

	// Listing and lookup.
	sc2, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "B"}, e.AdminActor)
	if err != nil {
		t.Fatal(err)
	}
	for status, want := range map[string]int{"": 2, "published": 1, "DRAFT": 1, "SIMULATED": 0} {
		rows, err := rp.ListScenarios(e.Ctx, status)
		if err != nil || len(rows) != want {
			t.Fatalf("list %q: %v %d", status, err, len(rows))
		}
	}
	_, err = rp.ListScenarios(e.Ctx, "nope")
	recWantCode(t, err, domain.ErrCodeBadRequest, "")
	_, err = rp.GetScenario(e.Ctx, uuid.Nil)
	recWantCode(t, err, domain.ErrCodeBadRequest, "")
	_, err = rp.GetScenario(e.Ctx, uuid.New())
	recWantCode(t, err, domain.ErrCodeNotFound, "")

	// Archive is terminal.
	arch, err := rp.ArchiveScenario(e.Ctx, sc2.ID, e.PlannerActor)
	if err != nil || arch.Status != "ARCHIVED" {
		t.Fatalf("archive: %v", err)
	}
	_, err = rp.ArchiveScenario(e.Ctx, sc2.ID, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "already archived")
	_, err = rp.ArchiveScenario(e.Ctx, uuid.New(), e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc2.ID}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "archived")
	_, err = rp.AddScenarioAction(e.Ctx, sc2.ID, RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "")
	if arch2, err := rp.ArchiveScenario(e.Ctx, sc.ID, e.AdminActor); err != nil || arch2.Status != "ARCHIVED" {
		t.Fatalf("archive published: %v", err)
	}
	_, err = e.Svc.RecoveryExecution.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "k"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only PUBLISHED")
}

// A targeted (non-wildcard) action only affects cases whose root cause
// references it; simulation is deterministic across scenarios.
func TestRecDBScenarioSimulationIsDeterministic(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	recControlTowerBaseline(t, e)
	rp := e.Svc.RecoveryPlanning
	actions := []RecoveryScenarioActionInput{
		{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "*", Parameters: recParams(t, map[string]any{"recoveryDays": 5})},
		{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "*", Parameters: recParams(t, map[string]any{"alternateWorkCenterRef": "WC-PACK", "recoveryDays": 2})},
	}
	var summaries []domain.RecoveryScenarioSummary
	for _, name := range []string{"det-1", "det-2"} {
		_, sim := recPublishScenario(t, e, name, actions)
		summaries = append(summaries, sim.Summary)
	}
	a, b := summaries[0], summaries[1]
	if a.SimulatedOpenCases != b.SimulatedOpenCases || a.RecoveredRevenue != b.RecoveredRevenue || a.ImpactDaysRecovered != b.ImpactDaysRecovered || a.RecoveryScore != b.RecoveryScore {
		t.Fatalf("same actions, same baseline must simulate identically: %+v vs %+v", a, b)
	}
	var rootRef string
	if err := e.DB.GetContext(e.Ctx, &rootRef, `SELECT root_cause_ref FROM v_current_control_tower_cases WHERE exception_type='MATERIAL_SHORTAGE' ORDER BY case_id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	ref := rootRef[strings.LastIndex(rootRef, ":")+1:]
	for _, tc := range []struct {
		target   string
		resolved int
	}{{ref, 1}, {strings.ToLower(rootRef), 1}, {"PO:does-not-exist", 0}} {
		sc, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "targeted " + tc.target}, e.PlannerActor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: tc.target, Parameters: recParams(t, map[string]any{"expediteDays": 2})}, e.PlannerActor); err != nil {
			t.Fatal(err)
		}
		sim, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID}, e.PlannerActor)
		if err != nil {
			t.Fatal(err)
		}
		if sim.Summary.OpenCaseReduction != tc.resolved || sim.Summary.SimulatedOpenCases != sim.Summary.BaselineOpenCases-tc.resolved {
			t.Fatalf("target %q: %+v", tc.target, sim.Summary)
		}
	}
	if a.SimulatedOpenCases != a.BaselineOpenCases {
		t.Fatalf("capacity actions must not resolve material shortages: %+v", a)
	}
}

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// recReleasedOpWithAlternative releases a fresh WO for a routed item after
// registering an alternative work center on its first routing operation, and
// returns the released WO, its first operation and the alternative WC code.
func recReleasedOpWithAlternative(t *testing.T, e *recEnv, orderNo string) (*domain.WorkOrder, domain.WOOperation, string) {
	t.Helper()
	item := recRoutedItem(t, e.dbFixture)
	ops, err := e.Repos.Routings.OperationsForItem(e.Ctx, recItemID(t, e.dbFixture, item))
	if err != nil || len(ops) == 0 {
		t.Fatalf("routing ops for %s: %v", item, err)
	}
	var altID uuid.UUID
	var altCode string
	if err := e.DB.GetContext(e.Ctx, &altID, `SELECT id FROM work_centers WHERE id<>$1 ORDER BY code LIMIT 1`, ops[0].WorkCenterID); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.GetContext(e.Ctx, &altCode, `SELECT code FROM work_centers WHERE id=$1`, altID); err != nil {
		t.Fatal(err)
	}
	if err := e.Repos.Routings.AddAlternative(e.Ctx, &domain.RoutingOperationAlternative{RoutingOperationID: ops[0].ID, WorkCenterID: altID, Priority: 1, IsActive: true}); err != nil {
		t.Fatalf("add alternative: %v", err)
	}
	wo := recWO(t, e.dbFixture, orderNo, item, 1, recDay(10), recDay(14))
	if _, err := e.Svc.Workflow.ReleaseWorkOrder(e.Ctx, wo.ID); err != nil {
		t.Fatalf("release %s: %v", orderNo, err)
	}
	var op domain.WOOperation
	if err := e.DB.GetContext(e.Ctx, &op, `SELECT * FROM wo_operations WHERE wo_id=$1 ORDER BY seq_no LIMIT 1`, wo.ID); err != nil {
		t.Fatalf("wo operation: %v", err)
	}
	return wo, op, altCode
}

func TestRecDBExecutionAllActionTypesSucceedAndAreIdempotent(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution

	po := recPO(t, e.dbFixture, "PO-REC-1", "CHAIN-1", 40, recDay(-1), recDay(20))
	woRes := recWO(t, e.dbFixture, "WO-REC-RESCHED", "WHEEL-1", 1, recDay(10), recDay(15))
	item := recRoutedItem(t, e.dbFixture)
	woRel := recWO(t, e.dbFixture, "WO-REC-RELEASE", item, 1, recDay(12), recDay(18))
	_, op, altCode := recReleasedOpWithAlternative(t, e, "WO-REC-ALT")
	effective := recDay(7).Format("2006-01-02")

	actions := []RecoveryScenarioActionInput{
		{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:PO-REC-1", Parameters: recParams(t, map[string]any{"expediteDays": 5}), EstimatedCost: 10},
		{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-RESCHED", Parameters: recParams(t, map[string]any{"recoveryDays": 2}), EstimatedCost: 20},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: woRel.ID.String()},
		{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_ORDER_OPERATION", TargetRef: "WOOP:" + op.ID.String(), Parameters: recParams(t, map[string]any{"alternateWorkCenterRef": "WC:" + altCode, "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})},
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-ASSY", Parameters: recParams(t, map[string]any{"overtimeMinutes": 60, "effectiveDate": effective})},
	}
	prepared := recPrepare(t, e, "all-types", actions)
	if prepared.Execution.Status != "PREPARED" || prepared.Execution.ActionCount != 5 || len(prepared.Actions) != 5 {
		t.Fatalf("unexpected prepared execution: %+v", prepared.Execution)
	}
	for _, h := range []string{prepared.Execution.PublicationHash, prepared.Execution.RunResultHash, prepared.Execution.ActionSetHash} {
		if !recIsHash(h) {
			t.Fatalf("prepared snapshot hash not canonical: %q", h)
		}
	}
	for _, a := range prepared.Actions {
		if a.PreflightStatus != "PENDING" || a.ExecutionStatus != "PENDING" || !recIsHash(a.ActionSnapshotHash) {
			t.Fatalf("prepared action %d: %+v", a.SequenceNo, a)
		}
	}

	// Same idempotency key returns the same execution (even with another comment).
	again, err := rx.Prepare(e.Ctx, prepared.Execution.ScenarioID, RecoveryExecutionPrepareInput{IdempotencyKey: "  key-all-types ", Comment: "other"}, e.PlannerActor)
	if err != nil || again.Execution.ID != prepared.Execution.ID {
		t.Fatalf("idempotent prepare: %v %v", err, again)
	}

	pf, err := rx.Preflight(e.Ctx, prepared.Execution.ID, e.PlannerActor)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.Execution.Status != "PREFLIGHT_PASSED" {
		for _, a := range pf.Actions {
			t.Logf("%d %s %s %s", a.SequenceNo, a.ActionType, a.PreflightStatus, a.ErrorText)
		}
		t.Fatalf("preflight status %s", pf.Execution.Status)
	}
	preflightHash := recHashPtr(t, "preflight hash", pf.Execution.PreflightHash)
	// Preflight is deterministic: re-running it on unchanged state yields the same hash.
	pf2, err := rx.Preflight(e.Ctx, prepared.Execution.ID, e.PlannerActor)
	if err != nil || recHashPtr(t, "preflight hash 2", pf2.Execution.PreflightHash) != preflightHash {
		t.Fatalf("preflight not stable: %v", err)
	}
	for _, a := range pf.Actions {
		if a.PreflightStatus != "READY" {
			t.Fatalf("action %d not READY: %s", a.SequenceNo, a.ErrorText)
		}
		recHashPtr(t, "action preflight state hash", a.PreflightStateHash)
	}

	// Nothing operational happened yet.
	if got := recGetWO(t, e.dbFixture, woRes.ID); !got.StartDate.Equal(recDay(10)) {
		t.Fatalf("preflight mutated WO dates: %v", got.StartDate)
	}

	ap, err := rx.Approve(e.Ctx, prepared.Execution.ID, RecoveryExecutionApprovalInput{Comment: " independent "}, e.AdminActor)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if ap.Execution.Status != "APPROVED" || ap.Execution.ApprovalComment != "independent" || ap.Execution.ApprovedByUsername == nil || *ap.Execution.ApprovedByUsername != e.Admin.Username {
		t.Fatalf("approved execution: %+v", ap.Execution)
	}
	recHashPtr(t, "approval hash", ap.Execution.ApprovalHash)

	done, err := rx.Execute(e.Ctx, prepared.Execution.ID, e.AdminActor)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if done.Execution.Status != "SUCCEEDED" || done.Execution.SucceededActionCount != 5 || done.Execution.FailedActionCount != 0 || done.Execution.CompletedAt == nil {
		t.Fatalf("execution result: %+v", done.Execution)
	}
	resultHash := recHashPtr(t, "execution result hash", done.Execution.ExecutionResultHash)
	if done.Execution.ExecutedByUsername == nil || *done.Execution.ExecutedByUsername != e.Admin.Username {
		t.Fatalf("executor not recorded")
	}
	prefixes := []string{"SUPPLIER_SCHEDULE:", "WO:WO-REC-RESCHED", "WO:WO-REC-RELEASE", "WOOP:" + op.ID.String(), "RECOVERY_CAPACITY_OVERRIDE:"}
	for i, a := range done.Actions {
		if a.ExecutionStatus != "SUCCEEDED" || !strings.HasPrefix(a.OperationalRef, prefixes[i]) {
			t.Fatalf("action %d: status=%s ref=%s", a.SequenceNo, a.ExecutionStatus, a.OperationalRef)
		}
		recHashPtr(t, "action result hash", a.ResultHash)
		var before, after map[string]any
		if json.Unmarshal(a.BeforeState, &before) != nil || json.Unmarshal(a.AfterState, &after) != nil || before["ready"] != true || len(after) == 0 {
			t.Fatalf("action %d evidence before=%s after=%s", a.SequenceNo, a.BeforeState, a.AfterState)
		}
	}

	// Operational outcomes.
	got := recGetWO(t, e.dbFixture, woRes.ID)
	if !TruncateDay(got.StartDate).Equal(recDay(8)) || !TruncateDay(got.DueDate).Equal(recDay(13)) {
		t.Fatalf("reschedule: start=%v due=%v", got.StartDate, got.DueDate)
	}
	if rel := recGetWO(t, e.dbFixture, woRel.ID); rel.Status != "RELEASED" {
		t.Fatalf("release: status=%s", rel.Status)
	}
	var opWC uuid.UUID
	if err := e.DB.GetContext(e.Ctx, &opWC, `SELECT work_center_id FROM wo_operations WHERE id=$1`, op.ID); err != nil || opWC != recWCID(t, e.dbFixture, altCode) {
		t.Fatalf("alternate WC not applied: %v %v", opWC, err)
	}
	events, err := e.Svc.SupplierScheduling.ListEvents(e.Ctx, po.ID)
	if err != nil || len(events) != 1 || events[0].EventType != "REVISE" || events[0].ConfirmedDeliveryDate == nil || !TruncateDay(*events[0].ConfirmedDeliveryDate).Equal(recDay(15)) {
		t.Fatalf("expedite event: %v %+v", err, events)
	}
	if p, _ := e.Repos.Purchases.Get(e.Ctx, po.ID); !TruncateDay(p.DueDate).Equal(recDay(20)) {
		t.Fatalf("expedite must not rewrite PO due date: %v", p.DueDate)
	}
	wcASSY := recWCID(t, e.dbFixture, "WC-ASSY")
	ot, err := loadRecoveryOvertime(e.Ctx, e.DB, recDay(0), recDay(30))
	if err != nil || ot[dayStateKey(wcASSY, recDay(7))] != 60 || len(ot) != 1 {
		t.Fatalf("overtime override: %v %v", err, ot)
	}
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM schedule_reschedule_signals WHERE source_type='RECOVERY_EXECUTION' AND detail->>'executionId'=$1`, prepared.Execution.ID.String()); n != 5 {
		t.Fatalf("reschedule signals: %d", n)
	}

	// Re-executing a terminal execution is read-only and returns the same evidence.
	again2, err := rx.Execute(e.Ctx, prepared.Execution.ID, e.Admin2Actor)
	if err != nil || recHashPtr(t, "repeat hash", again2.Execution.ExecutionResultHash) != resultHash {
		t.Fatalf("repeat execute: %v", err)
	}
	if got2 := recGetWO(t, e.dbFixture, woRes.ID); !got2.StartDate.Equal(got.StartDate) || !got2.DueDate.Equal(got.DueDate) {
		t.Fatalf("repeat execute shifted dates again")
	}
	if ev, _ := e.Svc.SupplierScheduling.ListEvents(e.Ctx, po.ID); len(ev) != 1 {
		t.Fatalf("repeat execute appended supplier events: %d", len(ev))
	}

	// List/Get.
	all, err := rx.List(e.Ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %v %d", err, len(all))
	}
	if s, _ := rx.List(e.Ctx, " succeeded "); len(s) != 1 {
		t.Fatalf("list by status: %d", len(s))
	}
	if s, _ := rx.List(e.Ctx, "CANCELLED"); len(s) != 0 {
		t.Fatalf("list cancelled: %d", len(s))
	}
	if _, err := rx.Get(e.Ctx, uuid.New()); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("get missing: %v", err)
	}

	// Terminal immutability: service + DB triggers.
	_, err = rx.Cancel(e.Ctx, prepared.Execution.ID, RecoveryExecutionCancelInput{Comment: "late"}, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "cannot be cancelled")
	_, err = rx.Preflight(e.Ctx, prepared.Execution.ID, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "before approval")
	recDBErr(t, e.dbFixture, "terminal recovery execution is immutable", `UPDATE recovery_plan_executions SET failure_reason='tamper' WHERE id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "identity/snapshot fields are immutable", `UPDATE recovery_plan_executions SET prepare_comment='tamper' WHERE id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "terminal recovery execution action is immutable", `UPDATE recovery_plan_execution_actions SET error_text='tamper' WHERE execution_id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "snapshot is immutable", `UPDATE recovery_plan_execution_actions SET target_ref='WO:other' WHERE execution_id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "append-only", `DELETE FROM recovery_plan_execution_actions WHERE execution_id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "append-only", `DELETE FROM recovery_plan_executions WHERE id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "append-only", `UPDATE recovery_capacity_overrides SET overtime_minutes=1 WHERE execution_id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "append-only", `DELETE FROM recovery_capacity_overrides WHERE execution_id=$1`, prepared.Execution.ID)
	recDBErr(t, e.dbFixture, "must start PREPARED", `INSERT INTO recovery_plan_executions(id,scenario_id,publication_id,run_id,idempotency_key,status,publication_hash,run_result_hash,action_set_hash,action_count,prepared_by_user_id,prepared_by_username)
SELECT $1,scenario_id,publication_id,run_id,'forged','APPROVED',publication_hash,run_result_hash,action_set_hash,action_count,prepared_by_user_id,prepared_by_username FROM recovery_plan_executions WHERE id=$2`, uuid.New(), prepared.Execution.ID)
}

func TestRecDBExecutionSegregationOfDuties(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution
	recWO(t, e.dbFixture, "WO-REC-SOD", "WHEEL-1", 1, recDay(10), recDay(15))
	actions := []RecoveryScenarioActionInput{{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-SOD", Parameters: recParams(t, map[string]any{"recoveryDays": 1})}}
	sc, _ := recPublishScenario(t, e, "sod", actions)

	// Prepare input validation.
	_, err := rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "  "}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "idempotencyKey is required")
	_, err = rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: strings.Repeat("k", 201)}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeBadRequest, "200 characters")
	_, err = rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "k"}, RecoveryPlanningActor{UserID: uuid.New(), Username: "v", Role: "viewer"})
	recWantCode(t, err, domain.ErrCodeUnauthorized, "")
	_, err = rx.Prepare(e.Ctx, uuid.New(), RecoveryExecutionPrepareInput{IdempotencyKey: "k"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")

	// An admin prepares; the same admin must not approve it.
	d, err := rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "admin-prepared"}, e.AdminActor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.Admin2Actor)
	recWantCode(t, err, domain.ErrCodeConflict, "must pass preflight")
	_, err = rx.Execute(e.Ctx, d.Execution.ID, e.Admin2Actor)
	recWantCode(t, err, domain.ErrCodeConflict, "must be APPROVED")
	if _, err := rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor); err != nil {
		t.Fatal(err)
	}
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeForbidden, "approver must differ")
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeForbidden, "requires admin")
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, RecoveryPlanningActor{Role: "admin"})
	recWantCode(t, err, domain.ErrCodeUnauthorized, "")
	_, err = rx.Approve(e.Ctx, uuid.New(), RecoveryExecutionApprovalInput{}, e.Admin2Actor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")

	// The DB enforces segregation independently of the application.
	recDBErr(t, e.dbFixture, "approver must differ", `UPDATE recovery_plan_executions SET status='APPROVED',approved_by_user_id=prepared_by_user_id,approved_by_username=prepared_by_username,approved_at=now(),approval_hash=repeat('a',64) WHERE id=$1`, d.Execution.ID)
	// ...and requires a real, matching, active admin.
	recDBErr(t, e.dbFixture, "requires admin actor", `UPDATE recovery_plan_executions SET status='APPROVED',approved_by_user_id=$2,approved_by_username=$3,approved_at=now(),approval_hash=repeat('a',64) WHERE id=$1`, d.Execution.ID, e.Planner.ID, e.Planner.Username)
	recDBErr(t, e.dbFixture, "matching active actor", `UPDATE recovery_plan_executions SET status='APPROVED',approved_by_user_id=$2,approved_by_username='impostor',approved_at=now(),approval_hash=repeat('a',64) WHERE id=$1`, d.Execution.ID, e.Admin2.ID)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE users SET is_active=false WHERE id=$1`, e.Admin2.ID); err != nil {
		t.Fatal(err)
	}
	recDBErr(t, e.dbFixture, "matching active actor", `UPDATE recovery_plan_executions SET status='APPROVED',approved_by_user_id=$2,approved_by_username=$3,approved_at=now(),approval_hash=repeat('a',64) WHERE id=$1`, d.Execution.ID, e.Admin2.ID, e.Admin2.Username)
	// An inactive admin cannot approve through the service either (DB guard).
	if _, err := rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.Admin2Actor); err == nil {
		t.Fatal("inactive admin approval must be rejected by DB actor guard")
	}
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE users SET is_active=true WHERE id=$1`, e.Admin2.ID); err != nil {
		t.Fatal(err)
	}
	// Invalid lifecycle jumps are rejected by the DB.
	recDBErr(t, e.dbFixture, "invalid recovery execution transition", `UPDATE recovery_plan_executions SET status='SUCCEEDED',completed_at=now(),execution_result_hash=repeat('a',64) WHERE id=$1`, d.Execution.ID)

	// A different admin approves; planners cannot execute.
	ap, err := rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{Comment: "ok"}, e.Admin2Actor)
	if err != nil || ap.Execution.Status != "APPROVED" || *ap.Execution.ApprovedByUserID != e.Admin2.ID {
		t.Fatalf("approve: %v", err)
	}
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.Admin2Actor)
	recWantCode(t, err, domain.ErrCodeConflict, "")
	_, err = rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "before approval")
	_, err = rx.Execute(e.Ctx, d.Execution.ID, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeForbidden, "")
	_, err = rx.Execute(e.Ctx, uuid.New(), e.AdminActor)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	// The preparer may execute (only approval is segregated).
	done, err := rx.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	if err != nil || done.Execution.Status != "SUCCEEDED" {
		t.Fatalf("execute: %v", err)
	}
}

func TestRecDBExecutionPrepareRequiresPublishedEvidence(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution
	rp := e.Svc.RecoveryPlanning
	recWO(t, e.dbFixture, "WO-REC-DRAFT", "WHEEL-1", 1, recDay(10), recDay(15))
	sc, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: "draft"}, e.PlannerActor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "k"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only PUBLISHED")
	if _, err := rp.AddScenarioAction(e.Ctx, sc.ID, RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-DRAFT", Parameters: recParams(t, map[string]any{"recoveryDays": 1})}, e.PlannerActor); err != nil {
		t.Fatal(err)
	}
	if _, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID}, e.PlannerActor); err != nil {
		t.Fatal(err)
	}
	_, err = rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "k"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeConflict, "only PUBLISHED")
}

func TestRecDBExecutionWildcardIsBlockedAndCancellable(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution
	d := recPrepare(t, e, "wildcard", []RecoveryScenarioActionInput{
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "*", Note: "simulation only"},
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "*", Parameters: recParams(t, map[string]any{"overtimeMinutes": 30, "effectiveDate": recDay(3).Format("2006-01-02")})},
	})
	pf, err := rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Execution.Status != "PREFLIGHT_BLOCKED" || pf.Execution.FailureReason == "" {
		t.Fatalf("wildcard preflight: %+v", pf.Execution)
	}
	recHashPtr(t, "blocked preflight hash", pf.Execution.PreflightHash)
	for _, a := range pf.Actions {
		if a.PreflightStatus != "BLOCKED" || !strings.Contains(a.ErrorText, "wildcard targetRef is simulation-only") {
			t.Fatalf("action %d: %s %s", a.SequenceNo, a.PreflightStatus, a.ErrorText)
		}
	}
	_, err = rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{}, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "must pass preflight")
	_, err = rx.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "must be APPROVED")
	// Blocked preflight can be re-run.
	if pf2, err := rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor); err != nil || pf2.Execution.Status != "PREFLIGHT_BLOCKED" || *pf2.Execution.PreflightHash != *pf.Execution.PreflightHash {
		t.Fatalf("re-preflight: %v", err)
	}
	_, err = rx.Cancel(e.Ctx, d.Execution.ID, RecoveryExecutionCancelInput{Comment: "x"}, e.PlannerActor)
	recWantCode(t, err, domain.ErrCodeForbidden, "")
	c, err := rx.Cancel(e.Ctx, d.Execution.ID, RecoveryExecutionCancelInput{Comment: " not executable "}, e.AdminActor)
	if err != nil || c.Execution.Status != "CANCELLED" || c.Execution.FailureReason != "not executable" || c.Execution.CompletedAt == nil {
		t.Fatalf("cancel: %v %+v", err, c)
	}
	_, err = rx.Cancel(e.Ctx, d.Execution.ID, RecoveryExecutionCancelInput{}, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "")
	// Execute on a cancelled execution returns persisted evidence, no mutation.
	if r, err := rx.Execute(e.Ctx, d.Execution.ID, e.AdminActor); err != nil || r.Execution.Status != "CANCELLED" {
		t.Fatalf("execute cancelled: %v", err)
	}
	recDBErr(t, e.dbFixture, "terminal recovery execution is immutable", `UPDATE recovery_plan_executions SET status='PREPARED' WHERE id=$1`, d.Execution.ID)
}

func TestRecDBExecutionPreflightBlockReasons(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution

	recPO(t, e.dbFixture, "PO-REC-RCVD", "CHAIN-1", 10, recDay(-5), recDay(10))
	closer := e.dbFixture.user(t, "rec-po-closer", domain.RolePlanner)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE purchase_orders SET status='CLOSED', closed_by_user_id=$1, close_reason='test' WHERE po_no='PO-REC-RCVD'`, closer.ID); err != nil {
		t.Fatal(err)
	}
	recPO(t, e.dbFixture, "PO-REC-EARLY", "CHAIN-1", 10, recDay(5), recDay(8))
	recWO(t, e.dbFixture, "WO-REC-DONE", "WHEEL-1", 1, recDay(5), recDay(9))
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET status='CLOSED' WHERE order_no='WO-REC-DONE'`); err != nil {
		if _, err2 := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET status='CANCELLED' WHERE order_no='WO-REC-DONE'`); err2 != nil {
			t.Fatalf("cannot move WO out of PLANNED: %v / %v", err, err2)
		}
	}
	recWO(t, e.dbFixture, "WO-REC-NOROUTE", "TIRE-1", 1, recDay(5), recDay(9))
	routed := recRoutedItem(t, e.dbFixture)
	recWO(t, e.dbFixture, "WO-REC-HUGE", routed, 1e6, recDay(5), recDay(9))
	wo, op, altCode := recReleasedOpWithAlternative(t, e, "WO-REC-RELEASED")
	var otherCode string
	if err := e.DB.GetContext(e.Ctx, &otherCode, `SELECT code FROM work_centers WHERE id<>$1 AND code<>$2 ORDER BY code LIMIT 1`, op.WorkCenterID, altCode); err != nil {
		t.Fatal(err)
	}
	var curCode string
	if err := e.DB.GetContext(e.Ctx, &curCode, `SELECT code FROM work_centers WHERE id=$1`, op.WorkCenterID); err != nil {
		t.Fatal(err)
	}
	date := recDay(4).Format("2006-01-02")
	p := func(m map[string]any) []byte { return recParams(t, m) }

	cases := []struct {
		in     RecoveryScenarioActionInput
		reason string // "" = READY
	}{
		{RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-DONE", Parameters: p(map[string]any{"expediteDays": 1})}, "EXPEDITE_PO requires PURCHASE_ORDER target"},
		{RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:NOPE", Parameters: p(map[string]any{"expediteDays": 1})}, "not found"},
		{RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:PO-REC-RCVD", Parameters: p(map[string]any{"expediteDays": 1})}, "already received/closed"},
		{RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:PO-REC-EARLY", Parameters: p(map[string]any{"expediteDays": 10})}, "earlier than purchase order orderDate"},
		{RecoveryScenarioActionInput{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "po:PO-REC-EARLY", Parameters: p(map[string]any{"expediteDays": 3})}, ""},
		{RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "PURCHASE_ORDER", TargetRef: "WO:WO-REC-DONE", Parameters: p(map[string]any{"recoveryDays": 1})}, "requires WORK_ORDER target"},
		{RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:MISSING", Parameters: p(map[string]any{"recoveryDays": 1})}, "not found"},
		{RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-DONE", Parameters: p(map[string]any{"recoveryDays": 1})}, "requires PLANNED or RELEASED"},
		{RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: wo.ID.String(), Parameters: p(map[string]any{"recoveryDays": 1})}, ""},
		{RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-RELEASED"}, "requires PLANNED work order"},
		{RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-NOROUTE"}, "exactly one active routing"},
		{RecoveryScenarioActionInput{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-HUGE"}, "insufficient component availability"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1})}, "requires workOrderOperationRef"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": "not-a-uuid"})}, "workOrderOperationRef must be"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": uuid.NewString()})}, "not found"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": "WC:NOPE", "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})}, "not found"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + otherCode, Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})}, "does not match current operation work center"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_ORDER_OPERATION", TargetRef: "WOOP:" + uuid.NewString(), Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})}, "does not match workOrderOperationRef"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "PURCHASE_ORDER", TargetRef: "PO:x", Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})}, "target must be WORK_CENTER or WORK_ORDER_OPERATION"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": otherCode, "recoveryDays": 1, "workOrderOperationRef": op.ID.String()})}, "not in release-time operation alternatives"},
		{RecoveryScenarioActionInput{ActionType: "ALTERNATE_WORK_CENTER", TargetType: "WORK_CENTER", TargetRef: "WC:" + curCode, Parameters: p(map[string]any{"alternateWorkCenterRef": altCode, "recoveryDays": 1, "workOrderOperationRef": "WOOP:" + op.ID.String()})}, ""},
		{RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_ORDER", TargetRef: "WC:WC-ASSY", Parameters: p(map[string]any{"overtimeMinutes": 30})}, "requires WORK_CENTER target"},
		{RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:NOPE", Parameters: p(map[string]any{"overtimeMinutes": 30})}, "not found"},
		{RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-ASSY", Parameters: p(map[string]any{"overtimeMinutes": 30, "effectiveDate": "2026/01/01"})}, "requires effectiveDate YYYY-MM-DD"},
		{RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-ASSY", Parameters: p(map[string]any{"overtimeMinutes": 5000, "effectiveDate": date})}, "beyond the same calendar day"},
		{RecoveryScenarioActionInput{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC-PACK", Parameters: p(map[string]any{"overtimeMinutes": 30, "effectiveDate": date})}, ""},
	}
	inputs := make([]RecoveryScenarioActionInput, len(cases))
	for i, c := range cases {
		inputs[i] = c.in
	}
	d := recPrepare(t, e, "blocked-reasons", inputs)
	pf, err := rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.Execution.Status != "PREFLIGHT_BLOCKED" || len(pf.Actions) != len(cases) {
		t.Fatalf("status %s actions %d", pf.Execution.Status, len(pf.Actions))
	}
	for i, a := range pf.Actions {
		want := cases[i].reason
		recHashPtr(t, "state hash", a.PreflightStateHash)
		if want == "" {
			if a.PreflightStatus != "READY" {
				t.Errorf("case %d (%s %s): expected READY, got %s %q", i, a.ActionType, a.TargetRef, a.PreflightStatus, a.ErrorText)
			}
			continue
		}
		if a.PreflightStatus != "BLOCKED" || !strings.Contains(a.ErrorText, want) {
			t.Errorf("case %d (%s %s): expected BLOCKED %q, got %s %q", i, a.ActionType, a.TargetRef, want, a.PreflightStatus, a.ErrorText)
		}
	}
}

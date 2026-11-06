package service

import (
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

func recReschedule(t *testing.T, orderNo string, days int) RecoveryScenarioActionInput {
	return RecoveryScenarioActionInput{ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:" + orderNo, Parameters: recParams(t, map[string]any{"recoveryDays": days})}
}

// recForceExecuting moves an APPROVED execution to EXECUTING exactly as Tx A
// of Execute would, simulating a process that died after the start gate.
func recForceExecuting(t *testing.T, e *recEnv, d *domain.RecoveryPlanExecutionDetail) {
	t.Helper()
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE recovery_plan_executions SET status='EXECUTING',executed_by_user_id=$2,executed_by_username=$3,started_at=now() WHERE id=$1`, d.Execution.ID, e.Admin.ID, e.Admin.Username); err != nil {
		t.Fatalf("force EXECUTING: %v", err)
	}
}

func TestRecDBExecutionDriftBeforeStartKeepsApproved(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	wo := recWO(t, e.dbFixture, "WO-REC-DRIFT", "WHEEL-1", 1, recDay(10), recDay(15))
	d := recApproved(t, e, "drift", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-DRIFT", 3)})

	// The plant changes after approval.
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET due_date=$2 WHERE id=$1`, wo.ID, recDay(16)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "operational state drift detected; prepare a new recovery execution")
	got, _ := e.Svc.RecoveryExecution.Get(e.Ctx, d.Execution.ID)
	if got.Execution.Status != "APPROVED" || got.Execution.ExecutedByUserID != nil || got.Actions[0].ExecutionStatus != "PENDING" {
		t.Fatalf("drift must leave APPROVED untouched: %+v", got.Execution)
	}
	if w := recGetWO(t, e.dbFixture, wo.ID); !w.StartDate.Equal(recDay(10)) || !w.DueDate.Equal(recDay(16)) {
		t.Fatalf("drifted WO must not be mutated: %v %v", w.StartDate, w.DueDate)
	}
	// Restoring the approved facts makes the approval executable again.
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET due_date=$2 WHERE id=$1`, wo.ID, recDay(15)); err != nil {
		t.Fatal(err)
	}
	r, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	if err != nil || r.Execution.Status != "SUCCEEDED" {
		t.Fatalf("execute after restore: %v", err)
	}
	if w := recGetWO(t, e.dbFixture, wo.ID); !w.StartDate.Equal(recDay(7)) || !w.DueDate.Equal(recDay(12)) {
		t.Fatalf("dates: %v %v", w.StartDate, w.DueDate)
	}
}

func TestRecDBExecutionResumeWithMidwayDriftIsPartialFailed(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	a := recWO(t, e.dbFixture, "WO-REC-A", "WHEEL-1", 1, recDay(10), recDay(15))
	b := recWO(t, e.dbFixture, "WO-REC-B", "WHEEL-1", 1, recDay(10), recDay(15))
	c := recWO(t, e.dbFixture, "WO-REC-C", "WHEEL-1", 1, recDay(10), recDay(15))
	d := recApproved(t, e, "partial", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-A", 1), recReschedule(t, "WO-REC-B", 1), recReschedule(t, "WO-REC-C", 1)})
	recForceExecuting(t, e, d)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET start_date=$2 WHERE id=$1`, b.ID, recDay(11)); err != nil {
		t.Fatal(err)
	}

	res, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "drift detected before action execution")
	if res == nil {
		t.Fatal("failed execute must still return persisted evidence")
	}
	ex := res.Execution
	if ex.Status != "PARTIAL_FAILED" || ex.SucceededActionCount != 1 || ex.FailedActionCount != 1 || !strings.Contains(ex.FailureReason, "CONFLICT: operational state drift") {
		t.Fatalf("partial failure: %+v", ex)
	}
	hash := recHashPtr(t, "partial result hash", ex.ExecutionResultHash)
	want := []string{"SUCCEEDED", "FAILED", "SKIPPED"}
	for i, act := range res.Actions {
		if act.ExecutionStatus != want[i] {
			t.Fatalf("action %d: %s (want %s)", i, act.ExecutionStatus, want[i])
		}
	}
	if res.Actions[2].ErrorText != "skipped after earlier action failure" {
		t.Fatalf("skip reason: %q", res.Actions[2].ErrorText)
	}
	// Successful action stays committed (no automatic compensation); later ones untouched.
	if w := recGetWO(t, e.dbFixture, a.ID); !w.StartDate.Equal(recDay(9)) {
		t.Fatalf("A not rescheduled: %v", w.StartDate)
	}
	if w := recGetWO(t, e.dbFixture, b.ID); !w.StartDate.Equal(recDay(11)) {
		t.Fatalf("B must not be rescheduled: %v", w.StartDate)
	}
	if w := recGetWO(t, e.dbFixture, c.ID); !w.StartDate.Equal(recDay(10)) {
		t.Fatalf("C must not be rescheduled: %v", w.StartDate)
	}
	// Terminal evidence is stable on re-read / repeated execute.
	again, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	if err != nil || *again.Execution.ExecutionResultHash != hash || again.Execution.Status != "PARTIAL_FAILED" {
		t.Fatalf("repeat: %v", err)
	}
	if w := recGetWO(t, e.dbFixture, a.ID); !w.StartDate.Equal(recDay(9)) {
		t.Fatalf("A re-applied: %v", w.StartDate)
	}
}

func TestRecDBExecutionFirstActionDriftIsFailed(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	a := recWO(t, e.dbFixture, "WO-REC-F1", "WHEEL-1", 1, recDay(10), recDay(15))
	recWO(t, e.dbFixture, "WO-REC-F2", "WHEEL-1", 1, recDay(10), recDay(15))
	d := recApproved(t, e, "failed", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-F1", 1), recReschedule(t, "WO-REC-F2", 1)})
	recForceExecuting(t, e, d)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE work_orders SET quantity=2 WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "drift")
	if res.Execution.Status != "FAILED" || res.Execution.SucceededActionCount != 0 || res.Execution.FailedActionCount != 1 {
		t.Fatalf("failed: %+v", res.Execution)
	}
	recHashPtr(t, "failed result hash", res.Execution.ExecutionResultHash)
	if res.Actions[0].ExecutionStatus != "FAILED" || res.Actions[1].ExecutionStatus != "SKIPPED" {
		t.Fatalf("actions: %s %s", res.Actions[0].ExecutionStatus, res.Actions[1].ExecutionStatus)
	}
	recDBErr(t, e.dbFixture, "terminal recovery execution is immutable", `UPDATE recovery_plan_executions SET status='EXECUTING' WHERE id=$1`, d.Execution.ID)
}

func TestRecDBExecutionResumeReconcilesPersistedEvidence(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	for _, no := range []string{"WO-REC-R1", "WO-REC-R2", "WO-REC-R3", "WO-REC-R4"} {
		recWO(t, e.dbFixture, no, "WHEEL-1", 1, recDay(10), recDay(15))
	}

	// 1) EXECUTING with a durable FAILED action: parent is reconciled, nothing replayed.
	d1 := recApproved(t, e, "reconcile-failed", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-R1", 1), recReschedule(t, "WO-REC-R2", 1)})
	recForceExecuting(t, e, d1)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE recovery_plan_execution_actions SET execution_status='FAILED',error_text='crashed',completed_at=now() WHERE execution_id=$1 AND sequence_no=1`, d1.Execution.ID); err != nil {
		t.Fatal(err)
	}
	r1, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d1.Execution.ID, e.AdminActor)
	if err != nil || r1.Execution.Status != "FAILED" || r1.Execution.FailureReason != "reconciled persisted failed action evidence" || r1.Execution.FailedActionCount != 1 {
		t.Fatalf("reconcile: %v %+v", err, r1)
	}
	recHashPtr(t, "reconciled hash", r1.Execution.ExecutionResultHash)
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM work_orders WHERE order_no IN ('WO-REC-R1','WO-REC-R2') AND start_date<>$1`, recDay(10)); n != 0 {
		t.Fatalf("reconciliation replayed actions: %d", n)
	}

	// 2) EXECUTING with a durable SUCCEEDED first action: resume skips it.
	d2 := recApproved(t, e, "resume", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-R3", 2), recReschedule(t, "WO-REC-R4", 2)})
	recForceExecuting(t, e, d2)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE recovery_plan_execution_actions SET execution_status='SUCCEEDED',operational_ref='WO:manual',result_hash=repeat('b',64),completed_at=now() WHERE execution_id=$1 AND sequence_no=1`, d2.Execution.ID); err != nil {
		t.Fatal(err)
	}
	r2, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d2.Execution.ID, e.AdminActor)
	if err != nil || r2.Execution.Status != "SUCCEEDED" || r2.Execution.SucceededActionCount != 2 {
		t.Fatalf("resume: %v %+v", err, r2)
	}
	if w := recCount(t, e.dbFixture, `SELECT count(*) FROM work_orders WHERE order_no='WO-REC-R3' AND start_date=$1`, recDay(10)); w != 1 {
		t.Fatal("durably SUCCEEDED action must not be replayed on resume")
	}
	if w := recCount(t, e.dbFixture, `SELECT count(*) FROM work_orders WHERE order_no='WO-REC-R4' AND start_date=$1`, recDay(8)); w != 1 {
		t.Fatal("pending action must execute on resume")
	}

	// 3) A persisted RUNNING action needs an operator.
	recWO(t, e.dbFixture, "WO-REC-R5", "WHEEL-1", 1, recDay(10), recDay(15))
	d3 := recApproved(t, e, "running", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-R5", 1)})
	recForceExecuting(t, e, d3)
	if _, err := e.DB.ExecContext(e.Ctx, `UPDATE recovery_plan_execution_actions SET execution_status='RUNNING',started_at=now() WHERE execution_id=$1`, d3.Execution.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.Svc.RecoveryExecution.Execute(e.Ctx, d3.Execution.ID, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "operator investigation")
	_, err = e.Svc.RecoveryExecution.Cancel(e.Ctx, d3.Execution.ID, RecoveryExecutionCancelInput{}, e.AdminActor)
	recWantCode(t, err, domain.ErrCodeConflict, "cannot be cancelled")
}

// Two RELEASE_WO actions for work orders that share components. The first
// release reserves components, which changes the "available" balance captured
// in the second action's preflight state hash, so the second action is
// rejected as operational drift and the execution ends PARTIAL_FAILED even
// though nothing outside the execution changed. Same for two overtime actions
// on one work center/day ("existingRecoveryOvertimeMinutes"). This pins the
// current (questionable) behaviour; see the coverage report.
func TestRecDBExecutionSequentialActionsSharingState(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	item := recRoutedItem(t, e.dbFixture)
	recWO(t, e.dbFixture, "WO-REC-REL1", item, 1, recDay(10), recDay(15))
	recWO(t, e.dbFixture, "WO-REC-REL2", item, 1, recDay(10), recDay(15))
	date := recDay(5).Format("2006-01-02")
	check := func(name string, actions []RecoveryScenarioActionInput) {
		d := recApproved(t, e, name, actions)
		res, err := e.Svc.RecoveryExecution.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
		recWantCode(t, err, domain.ErrCodeConflict, "drift detected before action execution")
		if res.Execution.Status != "PARTIAL_FAILED" || res.Actions[0].ExecutionStatus != "SUCCEEDED" || res.Actions[1].ExecutionStatus != "FAILED" {
			t.Fatalf("%s: %+v", name, res.Execution)
		}
	}
	check("shared-components", []RecoveryScenarioActionInput{
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-REL1"},
		{ActionType: "RELEASE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:WO-REC-REL2"},
	})
	check("shared-overtime", []RecoveryScenarioActionInput{
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-WELD", Parameters: recParams(t, map[string]any{"overtimeMinutes": 30, "effectiveDate": date})},
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-WELD", Parameters: recParams(t, map[string]any{"overtimeMinutes": 30, "effectiveDate": date})},
	})
}

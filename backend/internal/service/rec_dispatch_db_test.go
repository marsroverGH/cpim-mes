package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func recDspActor(u *domain.User) ScheduleExecutionActor {
	return ScheduleExecutionActor{UserID: u.ID, Username: u.Username, Role: u.Role}
}

func recDspReleasedWO(t *testing.T, e *recEnv, orderNo string, due int) *domain.WorkOrder {
	t.Helper()
	wo := recWO(t, e.dbFixture, orderNo, "WHEEL-1", 1, recDay(0), recDay(due))
	if _, err := e.Svc.Workflow.ReleaseWorkOrder(e.Ctx, wo.ID); err != nil {
		t.Fatalf("release %s: %v", orderNo, err)
	}
	return wo
}

func TestRecDBDispatchPolicyVersions(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	se := e.Svc.ScheduleExecution
	planner := recDspActor(e.Planner)

	seed, err := se.CurrentPolicy(e.Ctx)
	if err != nil || seed.VersionNo != 1 || seed.Status != "ACTIVE" || seed.FreezeMinutes != 240 {
		t.Fatalf("seeded policy: %v %+v", err, seed)
	}
	bad := []DispatchPolicyInput{
		{FreezeMinutes: -1, FirmMinutes: 10},
		{FreezeMinutes: 60, FirmMinutes: 30},
		{FirmMinutes: 10, StartLateThresholdMinutes: -1},
		{FirmMinutes: 10, SetupMatchBonus: 101},
	}
	for i, in := range bad {
		if _, err := se.CreatePolicy(e.Ctx, in, planner); recCode(err) != domain.ErrCodeBadRequest {
			t.Fatalf("bad policy %d: %v", i, err)
		}
	}
	if _, err := se.CreatePolicy(e.Ctx, DispatchPolicyInput{FirmMinutes: 10}, ScheduleExecutionActor{}); recCode(err) != domain.ErrCodeUnauthorized {
		t.Fatalf("anonymous: %v", err)
	}
	if _, err := se.CreatePolicy(e.Ctx, DispatchPolicyInput{FirmMinutes: 10}, ScheduleExecutionActor{UserID: uuid.New(), Username: "op", Role: domain.RoleOperator}); recCode(err) != domain.ErrCodeForbidden {
		t.Fatalf("operator: %v", err)
	}
	p, err := se.CreatePolicy(e.Ctx, DispatchPolicyInput{FreezeMinutes: 0, FirmMinutes: 60, AutoReschedule: true, SetupMatchBonus: 20}, planner)
	if err != nil || p.Status != "DRAFT" || p.VersionNo != 2 || p.CreatedBy != e.Planner.Username {
		t.Fatalf("create: %v %+v", err, p)
	}
	if cur, _ := se.CurrentPolicy(e.Ctx); cur.ID != seed.ID {
		t.Fatal("DRAFT must not become current")
	}
	act, err := se.ActivatePolicy(e.Ctx, p.ID, planner)
	if err != nil || act.Status != "ACTIVE" || act.ActivatedAt == nil {
		t.Fatalf("activate: %v %+v", err, act)
	}
	_, err = se.ActivatePolicy(e.Ctx, p.ID, planner)
	recWantCode(t, err, domain.ErrCodeConflict, "only DRAFT")
	_, err = se.ActivatePolicy(e.Ctx, uuid.New(), planner)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	_, err = se.ActivatePolicy(e.Ctx, p.ID, ScheduleExecutionActor{})
	recWantCode(t, err, domain.ErrCodeUnauthorized, "")
	all, err := se.ListPolicies(e.Ctx)
	if err != nil || len(all) != 2 || all[0].ID != p.ID || all[1].Status != "ARCHIVED" {
		t.Fatalf("list: %v %+v", err, all)
	}
	_, err = se.ActivatePolicy(e.Ctx, seed.ID, planner)
	recWantCode(t, err, domain.ErrCodeConflict, "")

	// DB guards: configuration is immutable, archived is terminal, no deletes,
	// and a user-created policy must start DRAFT.
	recDBErr(t, e.dbFixture, "immutable", `UPDATE dispatch_policy_versions SET freeze_minutes=1 WHERE id=$1`, p.ID)
	recDBErr(t, e.dbFixture, "archived dispatch policy is immutable", `UPDATE dispatch_policy_versions SET status='ACTIVE' WHERE id=$1`, seed.ID)
	recDBErr(t, e.dbFixture, "audit evidence", `DELETE FROM dispatch_policy_versions WHERE id=$1`, p.ID)
	recDBErr(t, e.dbFixture, "", `INSERT INTO dispatch_policy_versions(id,version_no,status,freeze_minutes,firm_minutes,start_late_threshold_minutes,completion_late_threshold_minutes,auto_reschedule,min_auto_interval_minutes,setup_match_bonus,created_by_user_id,created_by) VALUES($1,99,'ACTIVE',0,0,0,0,false,0,0,$2,$3)`, uuid.New(), e.Planner.ID, e.Planner.Username)
}

func TestRecDBDispatchBoardAdherenceAndReschedule(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	se := e.Svc.ScheduleExecution
	planner := recDspActor(e.Planner)

	// No active execution schedule yet.
	if _, err := se.ExecutionState(e.Ctx); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("state: %v", err)
	}
	if _, err := se.Dispatch(e.Ctx, nil, time.Time{}); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := se.Reschedule(e.Ctx, DynamicRescheduleRequest{}, planner); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("reschedule without schedule: %v", err)
	}
	// Signals cannot be processed without an execution schedule: no-op.
	if r, err := se.ProcessPendingSignals(e.Ctx); err != nil || r != nil {
		t.Fatalf("process without schedule: %v %v", r, err)
	}

	// Deterministic policy: nothing frozen, 60-minute firm zone, manual reschedule only.
	p, err := se.CreatePolicy(e.Ctx, DispatchPolicyInput{FreezeMinutes: 0, FirmMinutes: 60, StartLateThresholdMinutes: 0, CompletionLateThresholdMinutes: 0, AutoReschedule: false, SetupMatchBonus: 20}, planner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := se.ActivatePolicy(e.Ctx, p.ID, planner); err != nil {
		t.Fatal(err)
	}

	wo1 := recDspReleasedWO(t, e, "WO-DSP-A", 5)
	src, err := e.Svc.CRP.DetailedSchedule(e.Ctx, DetailedScheduleRequest{StartDate: recDay(0), HorizonDays: 28}, CRPActor{UserID: e.Planner.ID, Username: e.Planner.Username})
	if err != nil {
		t.Fatalf("detailed schedule: %v", err)
	}
	st, err := se.ExecutionState(e.Ctx)
	if err != nil || st.ActiveRunID != src.Run.ID || st.PolicyVersionID != p.ID {
		t.Fatalf("execution state: %v %+v", err, st)
	}

	asOf := time.Now()
	board, err := se.Dispatch(e.Ctx, nil, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if board.Execution.ActiveRunID != src.Run.ID || board.Policy.ID != p.ID || len(board.Items) == 0 {
		t.Fatalf("board: %+v", board)
	}
	found := false
	for i, it := range board.Items {
		if it.WorkOrderID == wo1.ID {
			found = true
			if it.OrderNo != "WO-DSP-A" || it.ItemCode != "WHEEL-1" || it.PlannedStart == nil {
				t.Fatalf("item: %+v", it)
			}
			switch it.DispatchStatus {
			case "READY", "QUEUED", "LATE_START", "BLOCKED":
			default:
				t.Fatalf("unexpected dispatch status %s", it.DispatchStatus)
			}
		}
		if i > 0 && board.Items[i-1].DispatchScore < it.DispatchScore {
			t.Fatal("board must be sorted by dispatch score desc")
		}
	}
	if !found {
		t.Fatal("released WO missing from dispatch board")
	}
	wc := board.Items[0].WorkCenterID
	filtered, err := se.Dispatch(e.Ctx, &wc, asOf)
	if err != nil || len(filtered.Items) == 0 {
		t.Fatalf("filtered: %v", err)
	}
	for _, it := range filtered.Items {
		if it.WorkCenterID != wc {
			t.Fatal("work center filter leaked")
		}
	}
	other := uuid.New()
	if none, err := se.Dispatch(e.Ctx, &other, asOf); err != nil || len(none.Items) != 0 {
		t.Fatalf("unknown wc filter: %v", err)
	}

	// Adherence: deterministic hash for the same asOf; snapshot is persisted and immutable.
	a1, err := se.CurrentAdherence(e.Ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := se.CurrentAdherence(e.Ctx, asOf)
	if !recIsHash(a1.Snapshot.ResultHash) || a1.Snapshot.ResultHash != a2.Snapshot.ResultHash || a1.Summary.TotalOperations != len(board.Items) {
		t.Fatalf("adherence: %+v", a1.Summary)
	}
	if a1.Summary.OnTimeStartPct < 0 || a1.Summary.OnTimeStartPct > 100 {
		t.Fatalf("pct: %v", a1.Summary.OnTimeStartPct)
	}
	if _, err := se.SnapshotAdherence(e.Ctx, asOf, ScheduleExecutionActor{UserID: uuid.New(), Username: "v", Role: domain.RoleViewer}); recCode(err) != domain.ErrCodeForbidden {
		t.Fatalf("viewer snapshot: %v", err)
	}
	snap, err := se.SnapshotAdherence(e.Ctx, asOf, planner)
	if err != nil || snap.Snapshot.ResultHash != a1.Snapshot.ResultHash || snap.Snapshot.GeneratedBy != e.Planner.Username {
		t.Fatalf("snapshot: %v", err)
	}
	gotSnap, err := se.GetAdherenceSnapshot(e.Ctx, snap.Snapshot.ID)
	if err != nil || len(gotSnap.Rows) != len(snap.Rows) || gotSnap.Summary.TotalOperations != snap.Summary.TotalOperations {
		t.Fatalf("get snapshot: %v", err)
	}
	if _, err := se.GetAdherenceSnapshot(e.Ctx, uuid.New()); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("missing snapshot: %v", err)
	}
	if snaps, err := se.ListAdherenceSnapshots(e.Ctx); err != nil || len(snaps) != 1 {
		t.Fatalf("list snapshots: %v", err)
	}
	recDBErr(t, e.dbFixture, "append-only", `UPDATE schedule_adherence_snapshots SET status='X' WHERE id=$1`, snap.Snapshot.ID)
	recDBErr(t, e.dbFixture, "append-only", `DELETE FROM schedule_adherence_rows WHERE snapshot_id=$1`, snap.Snapshot.ID)

	// Request validation.
	_, err = se.Reschedule(e.Ctx, DynamicRescheduleRequest{TriggerType: "COSMIC_RAY"}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "triggerType")
	_, err = se.Reschedule(e.Ctx, DynamicRescheduleRequest{HorizonDays: 400}, planner)
	recWantCode(t, err, domain.ErrCodeBadRequest, "horizonDays")
	_, err = se.Reschedule(e.Ctx, DynamicRescheduleRequest{}, ScheduleExecutionActor{})
	recWantCode(t, err, domain.ErrCodeUnauthorized, "")

	// New released demand -> ADDED change -> controlled activation.
	wo2 := recDspReleasedWO(t, e, "WO-DSP-B", 6)
	res, err := se.Reschedule(e.Ctx, DynamicRescheduleRequest{TriggerType: " priority_change ", TriggerRef: wo2.ID.String(), Reason: " new work ", HorizonDays: 28}, planner)
	if err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if res.Run.Status != "ACTIVATED" || res.Run.TriggerType != "PRIORITY_CHANGE" || res.Run.Reason != "new work" || res.Run.ResultHash == nil || !recIsHash(*res.Run.ResultHash) || res.Activation == nil {
		t.Fatalf("run: %+v", res.Run)
	}
	added := false
	for _, c := range res.Changes {
		if c.WorkOrderID != nil && *c.WorkOrderID == wo2.ID && c.ChangeType == "ADDED" {
			added = true
		}
		if c.FrozenConflict || c.ExecutionConflict {
			t.Fatalf("unexpected conflict: %+v", c)
		}
	}
	if !added || res.Run.ImpactedWorkOrders < 1 || res.Run.FirmChanges+res.Run.FlexibleChanges != len(res.Changes) {
		t.Fatalf("changes: %+v", res.Run)
	}
	if h, _ := canonicalRescheduleHash(res.Changes); h != *res.Run.ResultHash {
		t.Fatal("reschedule hash must be canonical over changes")
	}
	st2, _ := se.ExecutionState(e.Ctx)
	if st2.ActiveRunID != *res.Run.CandidateRunID || res.Activation.PreviousRunID == nil || *res.Activation.PreviousRunID != src.Run.ID {
		t.Fatalf("activation: %+v", st2)
	}
	got, err := se.GetRescheduleRun(e.Ctx, res.Run.ID)
	if err != nil || got.Run.Status != "ACTIVATED" || len(got.Changes) != len(res.Changes) || got.Activation == nil || got.Activation.ID != res.Activation.ID {
		t.Fatalf("get run: %v", err)
	}
	if _, err := se.GetRescheduleRun(e.Ctx, uuid.New()); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("missing run: %v", err)
	}
	recDBErr(t, e.dbFixture, "", `UPDATE dynamic_reschedule_runs SET reason='tamper' WHERE id=$1`, res.Run.ID)
	recDBErr(t, e.dbFixture, "append-only", `DELETE FROM dynamic_reschedule_changes WHERE reschedule_run_id=$1`, res.Run.ID)
	recDBErr(t, e.dbFixture, "append-only", `UPDATE detailed_schedule_activation_history SET activation_reason='x' WHERE id=$1`, res.Activation.ID)

	// Start an operation of WO2: it becomes EXECUTED / IN_PROCESS on the board.
	var opID uuid.UUID
	if err := e.DB.GetContext(e.Ctx, &opID, `SELECT id FROM wo_operations WHERE wo_id=$1 ORDER BY seq_no LIMIT 1`, wo2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.ShopFloor.Start(e.Ctx, opID, ShopFloorActor{UserID: e.Admin.ID, Username: e.Admin.Username}); err != nil {
		t.Fatalf("start op: %v", err)
	}
	b2, err := se.Dispatch(e.Ctx, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range b2.Items {
		if it.WOOperationID == opID {
			if it.TimeFence != "EXECUTED" || (it.DispatchStatus != "IN_PROCESS" && it.DispatchStatus != "LATE_COMPLETE") || it.ActualStart == nil {
				t.Fatalf("started op: %+v", it)
			}
		}
	}
	// Auto-reschedule is off: pending signals stay pending.
	pending, err := se.PendingSignals(e.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := se.ProcessPendingSignals(e.Ctx); err != nil || r != nil {
		t.Fatalf("auto off must be a no-op: %v %v", r, err)
	}
	if again, _ := se.PendingSignals(e.Ctx); len(again) != len(pending) {
		t.Fatal("signals consumed while auto-reschedule disabled")
	}
	// Re-plan after execution started: executed work must never be silently rewritten.
	res2, err := se.Reschedule(e.Ctx, DynamicRescheduleRequest{TriggerType: "SHOP_FLOOR_PROGRESS", HorizonDays: 28}, planner)
	if err != nil {
		t.Fatalf("reschedule after start: %v", err)
	}
	if res2.Run.Status != "BLOCKED" || res2.Run.ExecutionConflicts < 1 || res2.Activation != nil {
		t.Fatalf("executed work must block activation: %+v", res2.Run)
	}
	execConflict := false
	for _, c := range res2.Changes {
		if c.ExecutionConflict && c.TimeFence == "EXECUTED" {
			execConflict = true
		}
	}
	if !execConflict {
		t.Fatal("expected an EXECUTED change flagged as execution conflict")
	}
	if st3, _ := se.ExecutionState(e.Ctx); st3.ActiveRunID != *res.Run.CandidateRunID {
		t.Fatal("blocked run changed the active schedule")
	}
	if g, _ := se.GetRescheduleRun(e.Ctx, res2.Run.ID); g.Activation != nil {
		t.Fatal("blocked run has activation evidence")
	}
	runs, err := se.ListRescheduleRuns(e.Ctx)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs: %v %d", err, len(runs))
	}
}

// With auto-reschedule enabled, pending execution signals are processed by the
// SYSTEM actor and marked processed with the resulting run.
func TestRecDBDispatchAutonomousSignalProcessing(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	se := e.Svc.ScheduleExecution
	planner := recDspActor(e.Planner)
	p, err := se.CreatePolicy(e.Ctx, DispatchPolicyInput{FreezeMinutes: 0, FirmMinutes: 60, AutoReschedule: true, MinAutoIntervalMinutes: 0}, planner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := se.ActivatePolicy(e.Ctx, p.ID, planner); err != nil {
		t.Fatal(err)
	}
	if r, err := se.ProcessPendingSignals(e.Ctx); err != nil || r != nil {
		t.Fatalf("no signals: %v %v", r, err)
	}
	recDspReleasedWO(t, e, "WO-DSP-AUTO-1", 5)
	if _, err := e.Svc.CRP.DetailedSchedule(e.Ctx, DetailedScheduleRequest{StartDate: recDay(0), HorizonDays: 28}, CRPActor{UserID: e.Planner.ID, Username: e.Planner.Username}); err != nil {
		t.Fatal(err)
	}
	wo := recDspReleasedWO(t, e, "WO-DSP-AUTO-2", 6)
	wcID := recWCID(t, e.dbFixture, "WC-ASSY")
	for _, trig := range []string{"PRIORITY_CHANGE", "BREAKDOWN"} {
		if err := recDspInsertSignal(e, trig, wcID, wo.ID); err != nil {
			t.Fatalf("signal: %v", err)
		}
	}
	res, err := se.ProcessPendingSignals(e.Ctx)
	if err != nil || res == nil {
		t.Fatalf("process: %v %v", res, err)
	}
	if res.Run.ActorType != "SYSTEM" || res.Run.ActorUsername != "SYSTEM" || res.Run.TriggerType != "BREAKDOWN" {
		t.Fatalf("system run: %+v", res.Run)
	}
	if res.Run.Status != "ACTIVATED" && res.Run.Status != "NO_CHANGE" && res.Run.Status != "BLOCKED" {
		t.Fatalf("status %s", res.Run.Status)
	}
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM schedule_reschedule_signals WHERE processed_at IS NULL`); n != 0 {
		t.Fatalf("unprocessed signals: %d", n)
	}
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM schedule_reschedule_signals WHERE processed_run_id=$1`, res.Run.ID); n < 2 {
		t.Fatalf("processed signals linked to run: %d", n)
	}
}

func recDspInsertSignal(e *recEnv, trigger string, wcID, woID uuid.UUID) error {
	_, err := e.DB.ExecContext(e.Ctx, `INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,work_center_id,work_order_id,detail) VALUES($1,$2,'RECOVERY_EXECUTION',$3,$4,$5,'{}')`, uuid.New(), trigger, "TEST:"+trigger, wcID, woID)
	return err
}

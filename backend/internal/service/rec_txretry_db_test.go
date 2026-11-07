package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// These tests deliberately do NOT call t.Parallel(): they wrap the package
// level txRetrySleep hook to count real retries. Sequential top-level tests
// finish before any parallel test starts, so the swap cannot race.

// recCountRetries wraps txRetrySleep (keeping a short real sleep) and returns
// a counter of retry attempts performed by retrySerializable.
func recCountRetries(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	orig := txRetrySleep
	txRetrySleep = func(ctx context.Context, d time.Duration) error {
		n.Add(1)
		return orig(ctx, d/4)
	}
	t.Cleanup(func() { txRetrySleep = orig })
	return &n
}

// recBlockThenCommit opens a READ COMMITTED transaction that updates a row
// (via stmt), starts fn in a goroutine, waits until fn's backend is blocked
// on that row lock, then commits. A SERIALIZABLE fn whose snapshot predates
// the commit must then fail with SQLSTATE 40001 on its SELECT ... FOR UPDATE.
func recBlockThenCommit[T any](t *testing.T, f *dbFixture, stmt string, args []any, fn func() (T, error)) (T, error) {
	t.Helper()
	tx, err := f.DB.BeginTxx(f.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if res, err := tx.ExecContext(f.Ctx, stmt, args...); err != nil {
		t.Fatalf("blocking statement: %v", err)
	} else if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("blocking statement touched %d rows", n)
	}
	type out struct {
		v   T
		err error
	}
	ch := make(chan out, 1)
	go func() {
		v, err := fn()
		ch <- out{v, err}
	}()
	recWaitForLockWaiter(t, f.DB)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(30 * time.Second):
		t.Fatal("blocked call did not finish")
	}
	var zero T
	return zero, nil
}

func recWaitForLockWaiter(t *testing.T, db *sqlx.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.Get(&n, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid()`); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no backend ever waited on the row lock")
}

// Deterministic real-DB proof: each guarded entry point hits a genuine
// PostgreSQL 40001, is retried by retrySerializable and then succeeds.
func TestRecDBTxRetryDeterministicSerializationFailure(t *testing.T) {
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution
	retries := recCountRetries(t)
	wo := recWO(t, e.dbFixture, "WO-REC-RETRY", "WHEEL-1", 1, recDay(10), recDay(15))
	d := recPrepare(t, e, "retry", []RecoveryScenarioActionInput{recReschedule(t, "WO-REC-RETRY", 2)})
	touch := `UPDATE recovery_plan_executions SET failure_reason='' WHERE id=$1`
	args := []any{d.Execution.ID}

	before := retries.Load()
	pf, err := recBlockThenCommit(t, e.dbFixture, touch, args, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return rx.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor)
	})
	if err != nil || pf.Execution.Status != "PREFLIGHT_PASSED" {
		t.Fatalf("preflight under contention: %v", err)
	}
	if retries.Load() == before {
		t.Fatal("Preflight: expected a real 40001 retry")
	}

	before = retries.Load()
	ap, err := recBlockThenCommit(t, e.dbFixture, touch, args, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return rx.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{Comment: "contended"}, e.AdminActor)
	})
	if err != nil || ap.Execution.Status != "APPROVED" {
		t.Fatalf("approve under contention: %v", err)
	}
	if retries.Load() == before {
		t.Fatal("Approve: expected a real 40001 retry")
	}

	before = retries.Load()
	ex, err := recBlockThenCommit(t, e.dbFixture, touch, args, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return rx.Execute(e.Ctx, d.Execution.ID, e.AdminActor)
	})
	if err != nil || ex.Execution.Status != "SUCCEEDED" {
		t.Fatalf("execute under contention: %v", err)
	}
	if retries.Load() == before {
		t.Fatal("Execute: expected a real 40001 retry")
	}
	if w := recGetWO(t, e.dbFixture, wo.ID); !w.StartDate.Equal(recDay(8)) || !w.DueDate.Equal(recDay(13)) {
		t.Fatalf("date shift must be applied exactly once: %v %v", w.StartDate, w.DueDate)
	}

	po := recPO(t, e.dbFixture, "PO-REC-RETRY", "CHAIN-1", 25, recDay(-1), recDay(20))
	qty := 25.0
	before = retries.Load()
	ev, err := recBlockThenCommit(t, e.dbFixture, `UPDATE purchase_orders SET supplier=supplier WHERE id=$1`, []any{po.ID}, func() (*domain.SupplierScheduleEvent, error) {
		return e.Svc.SupplierScheduling.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: recDay(18).Format("2006-01-02")}, recSalesActor(e.Planner))
	})
	if err != nil || ev.RevisionNo != 1 {
		t.Fatalf("record event under contention: %v", err)
	}
	if retries.Load() == before {
		t.Fatal("RecordEvent: expected a real 40001 retry")
	}
	t.Logf("real serialization-failure retries observed: %d", retries.Load())
}

// Many goroutines race on the same rows. No 40001/40P01 may escape, the final
// state must be exactly-once, and Execute must never produce FAILED /
// PARTIAL_FAILED merely because of the race.
func TestRecDBTxRetryConcurrentContention(t *testing.T) {
	e := recNewEnv(t)
	rx := e.Svc.RecoveryExecution
	retries := recCountRetries(t)
	const workers = 8

	phase := ""
	race := func(n int, fn func(i int) error) []error {
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = fn(i)
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if IsRetryableTxError(err) {
				t.Fatalf("%s worker %d: retryable error escaped: %v", phase, i, err)
			}
		}
		return errs
	}

	// --- SupplierScheduling.RecordEvent: distinct events on one PO ---
	po := recPO(t, e.dbFixture, "PO-REC-RACE", "CHAIN-1", 30, recDay(-1), recDay(25))
	qty := 30.0
	// Writers of one PO queue on a session-level advisory lock before their
	// transaction starts, so none inherits a stale snapshot (#15).
	const writers = txRetryMaxAttempts
	phase = "record-distinct"
	errs := race(writers, func(i int) error {
		_, err := e.Svc.SupplierScheduling.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "REVISE", Quantity: &qty, ConfirmedDeliveryDate: recDay(10 + i).Format("2006-01-02"), Notes: fmt.Sprint(i)}, recSalesActor(e.Planner))
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("record event %d: %v", i, err)
		}
	}
	checkRevisions := func(poID uuid.UUID, want int) {
		t.Helper()
		events, err := e.Svc.SupplierScheduling.ListEvents(e.Ctx, poID)
		if err != nil || len(events) != want {
			t.Fatalf("events: %v %d (want %d)", err, len(events), want)
		}
		for i, ev := range events {
			if ev.RevisionNo != i+1 {
				t.Fatalf("revision numbers must be gapless 1..%d, got %d at %d", want, ev.RevisionNo, i)
			}
		}
	}
	checkRevisions(po.ID, writers)

	// Heavier burst on another PO, well beyond txRetryMaxAttempts: every
	// writer must succeed and the revisions must stay gapless (#15).
	po2 := recPO(t, e.dbFixture, "PO-REC-RACE-HEAVY", "CHAIN-1", 30, recDay(-1), recDay(25))
	phase = "record-distinct-heavy"
	const heavyWriters = 3 * workers
	errs = race(heavyWriters, func(i int) error {
		_, err := e.Svc.SupplierScheduling.RecordEvent(e.Ctx, po2.ID, SupplierScheduleEventInput{EventID: uuid.NewString(), EventType: "REVISE", Quantity: &qty, ConfirmedDeliveryDate: recDay(10 + i).Format("2006-01-02")}, recSalesActor(e.Planner))
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("heavy record event %d: %v", i, err)
		}
	}
	checkRevisions(po2.ID, heavyWriters)

	// --- RecordEvent: the same eventId retried concurrently is idempotent ---
	eventID := uuid.NewString()
	ids := make([]uuid.UUID, workers)
	phase = "record-same"
	race(workers, func(i int) error {
		ev, err := e.Svc.SupplierScheduling.RecordEvent(e.Ctx, po.ID, SupplierScheduleEventInput{EventID: eventID, EventType: "CONFIRM", Quantity: &qty, ConfirmedDeliveryDate: recDay(20).Format("2006-01-02")}, recSalesActor(e.Planner))
		if err != nil {
			t.Errorf("idempotent record %d: %v", i, err)
			return err
		}
		ids[i] = ev.ID
		return nil
	})
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM supplier_schedule_events WHERE id=$1`, uuid.MustParse(eventID)); n != 1 {
		t.Fatalf("idempotent event stored %d times", n)
	}

	// --- Prepare: same idempotency key from many requests ---
	recWO(t, e.dbFixture, "WO-REC-RACE", "WHEEL-1", 1, recDay(10), recDay(15))
	sc, _ := recPublishScenario(t, e, "race", []RecoveryScenarioActionInput{
		recReschedule(t, "WO-REC-RACE", 3),
		{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:PO-REC-RACE", Parameters: recParams(t, map[string]any{"expediteDays": 2})},
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-PAINT", Parameters: recParams(t, map[string]any{"overtimeMinutes": 45, "effectiveDate": recDay(6).Format("2006-01-02")})},
	})
	execIDs := make([]uuid.UUID, workers)
	phase = "prepare"
	errs = race(workers, func(i int) error {
		d, err := rx.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "same-key"}, e.PlannerActor)
		if err == nil {
			execIDs[i] = d.Execution.ID
		}
		return err
	})
	for i, err := range errs {
		if err != nil || execIDs[i] != execIDs[0] {
			t.Fatalf("prepare %d: %v (%v vs %v)", i, err, execIDs[i], execIDs[0])
		}
	}
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM recovery_plan_executions WHERE scenario_id=$1`, sc.ID); n != 1 {
		t.Fatalf("idempotent prepare created %d executions", n)
	}
	id := execIDs[0]
	if _, err := rx.Preflight(e.Ctx, id, e.PlannerActor); err != nil {
		t.Fatal(err)
	}

	// --- Approve: many distinct admins race; exactly one wins ---
	admins := make([]RecoveryPlanningActor, workers)
	for i := range admins {
		admins[i] = recActor(e.user(t, fmt.Sprintf("rec-race-admin-%d", i), domain.RoleAdmin))
	}
	phase = "approve"
	errs = race(workers, func(i int) error {
		_, err := rx.Approve(e.Ctx, id, RecoveryExecutionApprovalInput{Comment: fmt.Sprint(i)}, admins[i])
		return err
	})
	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case recCode(err) == domain.ErrCodeConflict:
		default:
			t.Fatalf("approve %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one approval must win, got %d", wins)
	}

	// --- Execute: concurrent admins race on one APPROVED execution ---
	execRace := func(id uuid.UUID, n int, woNo string, wantStart, wantDue any) {
		t.Helper()
		results := make([]*domain.RecoveryPlanExecutionDetail, n)
		errs := race(n, func(i int) error {
			r, err := rx.Execute(e.Ctx, id, admins[i%len(admins)])
			results[i] = r
			return err
		})
		for i, err := range errs {
			if err != nil {
				t.Fatalf("execute %d: %v", i, err)
			}
			if results[i].Execution.Status != "SUCCEEDED" {
				t.Fatalf("execute %d returned %s", i, results[i].Execution.Status)
			}
		}
		final, err := rx.Get(e.Ctx, id)
		if err != nil || final.Execution.Status != "SUCCEEDED" || final.Execution.SucceededActionCount != 3 || final.Execution.FailedActionCount != 0 {
			t.Fatalf("final: %v %+v", err, final.Execution)
		}
		hash := recHashPtr(t, "result hash", final.Execution.ExecutionResultHash)
		for i, r := range results {
			if r != nil && r.Execution.ExecutionResultHash != nil && *r.Execution.ExecutionResultHash != hash {
				t.Fatalf("execute %d saw a different result hash", i)
			}
		}
		// Exactly-once operational effects.
		if n := recCount(t, e.dbFixture, `SELECT count(*) FROM work_orders WHERE order_no=$1 AND start_date=$2 AND due_date=$3`, woNo, wantStart, wantDue); n != 1 {
			t.Fatalf("%s dates must be shifted exactly once", woNo)
		}
		if n := recCount(t, e.dbFixture, `SELECT count(*) FROM supplier_schedule_events WHERE supplier_reference=$1`, "RECOVERY:"+id.String()); n != 1 {
			t.Fatalf("expedite events: %d", n)
		}
		if n := recCount(t, e.dbFixture, `SELECT count(*) FROM recovery_capacity_overrides WHERE execution_id=$1`, id); n != 1 {
			t.Fatalf("capacity overrides: %d", n)
		}
		if n := recCount(t, e.dbFixture, `SELECT count(*) FROM schedule_reschedule_signals WHERE source_type='RECOVERY_EXECUTION' AND detail->>'executionId'=$1`, id.String()); n != 3 {
			t.Fatalf("signals: %d", n)
		}
		if n := recCount(t, e.dbFixture, `SELECT count(*) FROM recovery_plan_execution_actions WHERE execution_id=$1 AND execution_status<>'SUCCEEDED'`, id); n != 0 {
			t.Fatalf("non-succeeded actions: %d", n)
		}
	}
	// A realistic burst (double click): strictly no escaped 40001/40P01.
	phase = "execute-2"
	execRace(id, 2, "WO-REC-RACE", recDay(7), recDay(12))

	// Heavy burst: concurrent callers queue on the execution lock instead of
	// deadlocking, so every caller sees SUCCEEDED (#14).
	recWO(t, e.dbFixture, "WO-REC-RACE2", "WHEEL-1", 1, recDay(20), recDay(25))
	recPO(t, e.dbFixture, "PO-REC-RACE2", "CHAIN-1", 30, recDay(-1), recDay(25))
	d2 := recApproved(t, e, "race-heavy", []RecoveryScenarioActionInput{
		recReschedule(t, "WO-REC-RACE2", 3),
		{ActionType: "EXPEDITE_PO", TargetType: "PURCHASE_ORDER", TargetRef: "PO:PO-REC-RACE2", Parameters: recParams(t, map[string]any{"expediteDays": 2})},
		{ActionType: "ADD_OVERTIME_CAPACITY", TargetType: "WORK_CENTER", TargetRef: "WC:WC-PAINT", Parameters: recParams(t, map[string]any{"overtimeMinutes": 45, "effectiveDate": recDay(9).Format("2006-01-02")})},
	})
	phase = "execute-heavy"
	execRace(d2.Execution.ID, 3*workers, "WO-REC-RACE2", recDay(17), recDay(22))
	t.Logf("retries performed under contention: %d", retries.Load())
}

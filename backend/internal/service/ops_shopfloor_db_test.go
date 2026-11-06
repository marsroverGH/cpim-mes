package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// opsReleasedWO creates and releases a WO for an item without components.
func opsReleasedWO(t *testing.T, f *dbFixture, nOps int, qty float64, overlapBatch float64) (*domain.WorkOrder, []domain.WOOperation) {
	t.Helper()
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	r := opsRouting(t, f, fg.ID, nOps)
	if overlapBatch > 0 {
		if _, err := f.DB.ExecContext(f.Ctx, `UPDATE routing_operations SET overlap_enabled=true, transfer_batch_qty=$1 WHERE routing_id=$2`, overlapBatch, r.ID); err != nil {
			t.Fatalf("enable overlap: %v", err)
		}
	}
	wo := opsWO(t, f, fg.ID, qty)
	rel, err := f.Svc.Workflow.ReleaseWorkOrder(f.Ctx, wo.ID)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(rel.Reservations) != 0 {
		t.Fatalf("no-BOM WO must not reserve: %+v", rel.Reservations)
	}
	return wo, opsWOOps(t, f, wo.ID)
}

func opsOpStatus(t *testing.T, f *dbFixture, id uuid.UUID) *domain.WOOperation {
	t.Helper()
	op, err := f.Svc.ShopFloor.Get(f.Ctx, id)
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	return op
}

func TestOpsShopFloorStateMachine(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSFActor(t, f, domain.RoleOperator)
	wo, ops := opsReleasedWO(t, f, 2, 10, 0)
	op1, op2 := ops[0].ID, ops[1].ID
	sf := f.Svc.ShopFloor

	// Identity is mandatory for every actual.
	opsWantErr(t, sf.Start(f.Ctx, op1, ShopFloorActor{}), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, sf.Stop(f.Ctx, op1, ShopFloorActor{UserID: actor.UserID, Username: "  "}, ""), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 1, ShopFloorActor{}, ""), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, sf.ReportScrap(f.Ctx, op1, 1, ShopFloorActor{}, ""), domain.ErrCodeUnauthorized, "")
	// Unknown operation.
	opsWantErr(t, sf.Start(f.Ctx, uuid.New(), actor), domain.ErrCodeNotFound, "operation")
	opsWantErr(t, sf.Stop(f.Ctx, uuid.New(), actor, ""), domain.ErrCodeNotFound, "operation")
	opsWantErr(t, sf.Complete(f.Ctx, uuid.New(), 1, actor, ""), domain.ErrCodeNotFound, "operation")
	opsWantErr(t, sf.ReportScrap(f.Ctx, uuid.New(), 1, actor, ""), domain.ErrCodeNotFound, "operation")
	if _, err := sf.Get(f.Ctx, uuid.New()); err == nil {
		t.Fatalf("get unknown op must fail")
	}

	// READY op cannot be stopped/completed/scrapped; PENDING op cannot start.
	opsWantErr(t, sf.Stop(f.Ctx, op1, actor, ""), domain.ErrCodeConflict, "")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 1, actor, ""), domain.ErrCodeConflict, "")
	opsWantErr(t, sf.ReportScrap(f.Ctx, op1, 1, actor, ""), domain.ErrCodeConflict, "IN_PROGRESS or PAUSED")
	opsWantErr(t, sf.Start(f.Ctx, op2, actor), domain.ErrCodeConflict, "")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 0, actor, ""), domain.ErrCodeBadRequest, "> 0")
	opsWantErr(t, sf.ReportScrap(f.Ctx, op1, 0, actor, ""), domain.ErrCodeBadRequest, "> 0")

	// START -> STOP -> START.
	if err := sf.Start(f.Ctx, op1, actor); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := opsGetWO(t, f, wo.ID); got.Status != "IN_PROGRESS" {
		t.Fatalf("WO status after first start = %s", got.Status)
	}
	o := opsOpStatus(t, f, op1)
	if o.Status != OperationStatusInProgress || o.ActiveStartedAt == nil || o.Operator != actor.Username || o.OperatorUserID == nil || *o.OperatorUserID != actor.UserID {
		t.Fatalf("op after start: %+v", o)
	}
	opsWantErr(t, sf.Start(f.Ctx, op1, actor), domain.ErrCodeConflict, "")
	if err := sf.Stop(f.Ctx, op1, actor, "break"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if o := opsOpStatus(t, f, op1); o.Status != OperationStatusPaused || o.ActiveStartedAt != nil || o.ActualMinutes < 0 {
		t.Fatalf("op after stop: %+v", o)
	}
	opsWantErr(t, sf.Stop(f.Ctx, op1, actor, ""), domain.ErrCodeConflict, "")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 1, actor, ""), domain.ErrCodeConflict, "")
	// Scrap is allowed while PAUSED and does not change good quantity.
	if err := sf.ReportScrap(f.Ctx, op1, 2, actor, "dropped"); err != nil {
		t.Fatalf("scrap while paused: %v", err)
	}
	if err := sf.Start(f.Ctx, op1, actor); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Cumulative reports: partial, then a decreasing report is rejected.
	if err := sf.Complete(f.Ctx, op1, 4, actor, "first"); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if o := opsOpStatus(t, f, op1); o.Status != OperationStatusInProgress || !opsNear(o.CompletedQty, 4) {
		t.Fatalf("op after partial: %+v", o)
	}
	opsWantErr(t, sf.Complete(f.Ctx, op1, 3, actor, ""), domain.ErrCodeConflict, "cannot decrease")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 4, actor, ""), domain.ErrCodeConflict, "increase")
	opsWantErr(t, sf.Complete(f.Ctx, op1, 11, actor, ""), domain.ErrCodeConflict, "between 0 and")
	if o := opsOpStatus(t, f, op2); o.Status != OperationStatusPending {
		t.Fatalf("successor must stay PENDING without overlap, got %s", o.Status)
	}

	// Completing to the planned quantity promotes the successor.
	if err := sf.Complete(f.Ctx, op1, 10, actor, "done"); err != nil {
		t.Fatalf("complete op1: %v", err)
	}
	if o := opsOpStatus(t, f, op1); o.Status != OperationStatusCompleted || o.CompletedAt == nil || o.ActiveStartedAt != nil {
		t.Fatalf("op1 after completion: %+v", o)
	}
	if o := opsOpStatus(t, f, op2); o.Status != OperationStatusReady {
		t.Fatalf("op2 must be READY after op1 completion, got %s", o.Status)
	}
	opsWantErr(t, sf.Start(f.Ctx, op1, actor), domain.ErrCodeConflict, "")

	// Active operations list shows only IN_PROGRESS/PAUSED/READY work.
	if err := sf.Start(f.Ctx, op2, actor); err != nil {
		t.Fatalf("start op2: %v", err)
	}
	active, err := sf.Active(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range active {
		if a.ID == op1 {
			t.Fatalf("completed op listed as active")
		}
		found = found || a.ID == op2
	}
	if !found {
		t.Fatalf("op2 missing from active list")
	}
	byWO, err := sf.ListByWO(f.Ctx, wo.ID)
	if err != nil || len(byWO) != 2 {
		t.Fatalf("list by WO: %d err=%v", len(byWO), err)
	}

	logs, err := sf.Logs(f.Ctx, op1)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	var scrap float64
	for _, l := range logs {
		events = append(events, l.EventType)
		if l.EventType == "SCRAP" {
			scrap += l.Quantity
		}
	}
	if len(logs) != 6 || !opsNear(scrap, 2) {
		t.Fatalf("op1 logs: %v (scrap %.2f)", events, scrap)
	}
	// Logs are append-only.
	_, err = f.DB.ExecContext(f.Ctx, `DELETE FROM operation_logs WHERE wo_op_id=$1`, op1)
	if err == nil {
		t.Fatalf("operation logs must be append-only")
	}
	// Direct state-machine violations are rejected by the database.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE wo_operations SET status='PENDING' WHERE id=$1`, op1)
	opsWantDBErr(t, err, "")
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE wo_operations SET completed_qty=5 WHERE id=$1`, op1)
	opsWantDBErr(t, err, "cannot decrease")
}

func TestOpsShopFloorOverlapAndPredecessorCap(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSFActor(t, f, domain.RolePlanner)
	_, ops := opsReleasedWO(t, f, 2, 10, 4)
	op1, op2 := ops[0].ID, ops[1].ID
	sf := f.Svc.ShopFloor

	if err := sf.Start(f.Ctx, op1, actor); err != nil {
		t.Fatal(err)
	}
	if err := sf.Complete(f.Ctx, op1, 3, actor, ""); err != nil {
		t.Fatal(err)
	}
	if o := opsOpStatus(t, f, op2); o.Status != OperationStatusPending {
		t.Fatalf("below the transfer batch op2 must stay PENDING, got %s", o.Status)
	}
	// Reaching the transfer batch (4) releases the successor while op1 runs.
	if err := sf.Complete(f.Ctx, op1, 4, actor, ""); err != nil {
		t.Fatal(err)
	}
	if o := opsOpStatus(t, f, op2); o.Status != OperationStatusReady {
		t.Fatalf("transfer batch reached: op2 = %s, want READY", o.Status)
	}
	if err := sf.Start(f.Ctx, op2, actor); err != nil {
		t.Fatalf("start overlapped op2: %v", err)
	}
	// The successor cannot overtake its predecessor.
	opsWantErr(t, sf.Complete(f.Ctx, op2, 5, actor, ""), domain.ErrCodeConflict, "predecessor")
	if err := sf.Complete(f.Ctx, op2, 4, actor, ""); err != nil {
		t.Fatalf("op2 to 4: %v", err)
	}
	if err := sf.Complete(f.Ctx, op1, 10, actor, ""); err != nil {
		t.Fatal(err)
	}
	if err := sf.Complete(f.Ctx, op2, 10, actor, ""); err != nil {
		t.Fatalf("op2 to 10: %v", err)
	}
	if o := opsOpStatus(t, f, op2); o.Status != OperationStatusCompleted {
		t.Fatalf("final op status = %s", o.Status)
	}
	// An identity that does not exist in users is rejected by the DB guard.
	ghost := ShopFloorActor{UserID: uuid.New(), Username: "ghost"}
	_, ops2 := opsReleasedWO(t, f, 1, 1, 0)
	opsWantDBErr(t, sf.Start(f.Ctx, ops2[0].ID, ghost), "operator identity")
	if got := AutoCalcMinutesFromStart(opsOpStatus(t, f, op2).CreatedAt, opsOpStatus(t, f, op2).CreatedAt); got != 0 {
		t.Fatalf("zero elapsed = %v", got)
	}
}

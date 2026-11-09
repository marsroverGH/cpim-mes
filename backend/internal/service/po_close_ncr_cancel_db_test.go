package service

import (
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestPurchaseCloseOpenAndPartial(t *testing.T) {
	t.Parallel()
	e := opsNewSQEnv(t)
	f := e.f
	purchases := f.Svc.Purchases

	open := opsPO(t, f, e.item.ID, e.supplier, 10)
	_, err := purchases.Close(f.Ctx, open.ID, "x", PurchaseReceiptActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = purchases.Close(f.Ctx, open.ID, "   ", e.recv)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "reason")
	_, err = purchases.Close(f.Ctx, uuid.New(), "x", e.recv)
	opsWantErr(t, err, domain.ErrCodeNotFound, "purchase order")

	got, err := purchases.Close(f.Ctx, open.ID, " 仕入先都合で中止 ", e.recv)
	if err != nil {
		t.Fatalf("close open PO: %v", err)
	}
	if got.Status != "CLOSED" || got.CloseReason != "仕入先都合で中止" || got.ClosedBy != e.recv.Username || got.ClosedAt == nil {
		t.Fatalf("closed PO = %+v", got)
	}
	if rem := PurchaseScheduledRemaining(got.Status, got.Quantity, got.ReceivedQty); rem != 0 {
		t.Fatalf("closed PO still supplies %v", rem)
	}

	// Final: no second close, no receipt, no reopening.
	_, err = purchases.Close(f.Ctx, open.ID, "again", e.recv)
	opsWantErr(t, err, domain.ErrCodeConflict, "CLOSED")
	_, err = f.Svc.Workflow.ReceivePurchase(f.Ctx, open.ID, uuid.New(), 1, "", e.recv)
	opsWantErr(t, err, domain.ErrCodeConflict, "CLOSED")
	if _, err := f.DB.ExecContext(f.Ctx, `UPDATE purchase_orders SET status='OPEN' WHERE id=$1`, open.ID); err == nil || !strings.Contains(err.Error(), "final") {
		t.Fatalf("reopen err = %v", err)
	}

	partial := opsPO(t, f, e.item.ID, e.supplier, 10)
	if _, err := f.Svc.Workflow.ReceivePurchase(f.Ctx, partial.ID, uuid.New(), 4, "", e.recv); err != nil {
		t.Fatal(err)
	}
	got, err = purchases.Close(f.Ctx, partial.ID, "短納で打ち切り", e.recv)
	if err != nil || got.Status != "CLOSED" || got.ReceivedQty != 4 {
		t.Fatalf("close partial: %+v err=%v", got, err)
	}

	full := opsPO(t, f, e.item.ID, e.supplier, 2)
	if _, err := f.Svc.Workflow.ReceivePurchase(f.Ctx, full.ID, uuid.New(), 2, "", e.recv); err != nil {
		t.Fatal(err)
	}
	_, err = purchases.Close(f.Ctx, full.ID, "x", e.recv)
	opsWantErr(t, err, domain.ErrCodeConflict, "RECEIVED")
}

func TestPurchaseCloseDatabaseGuards(t *testing.T) {
	t.Parallel()
	e := opsNewSQEnv(t)
	f := e.f
	po := opsPO(t, f, e.item.ID, e.supplier, 10)
	for _, c := range []struct {
		name, want string
		actor      uuid.UUID
		reason     string
	}{
		{"operator", "planner/admin", e.operator.UserID, "x"},
		{"no reason", "reason", e.planner.UserID, " "},
	} {
		_, err := f.DB.ExecContext(f.Ctx, `UPDATE purchase_orders SET status='CLOSED', closed_by_user_id=$2, close_reason=$3 WHERE id=$1`, po.ID, c.actor, c.reason)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v", c.name, err)
		}
	}
	if _, err := f.DB.ExecContext(f.Ctx, `UPDATE purchase_orders SET status='CLOSED', closed_by_user_id=$2, close_reason='x' WHERE id=$1`, po.ID, e.planner.UserID); err != nil {
		t.Fatal(err)
	}
	_, err := f.DB.ExecContext(f.Ctx, `
		INSERT INTO purchase_orders (po_no, item_id, supplier, quantity, due_date, status)
		VALUES ('PO-BORN-CLOSED', $1, 'S', 1, now(), 'CLOSED')`, e.item.ID)
	if err == nil || !strings.Contains(err.Error(), "created CLOSED") {
		t.Fatalf("insert CLOSED err = %v", err)
	}
}

func TestNCRCancelKeepsLotHeldUntilPassInspection(t *testing.T) {
	t.Parallel()
	e := opsNewSQEnv(t)
	f := e.f
	sq := f.Svc.SupplierQuality
	mk := func(qty float64) (uuid.UUID, *domain.SupplierNCR) {
		lot := e.receive(t, qty)
		n, err := sq.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lot, AffectedQty: qty, Severity: "CRITICAL", Description: "d"}, e.operator)
		if err != nil {
			t.Fatal(err)
		}
		return lot, n
	}
	lot, ncr := mk(5)

	_, err := sq.Cancel(f.Ctx, ncr.ID, "x", SupplierQualityActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = sq.Cancel(f.Ctx, ncr.ID, "x", e.operator)
	opsWantErr(t, err, domain.ErrCodeForbidden, "planner/admin")
	_, err = sq.Cancel(f.Ctx, ncr.ID, " ", e.planner)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "reason")
	_, err = sq.Cancel(f.Ctx, uuid.New(), "x", e.planner)
	opsWantErr(t, err, domain.ErrCodeNotFound, "NCR")

	out, err := sq.Cancel(f.Ctx, ncr.ID, "誤起票", e.planner)
	if err != nil || out.Status != "CANCELLED" || out.CancelReason != "誤起票" || out.ClosedBy != e.planner.Username {
		t.Fatalf("cancel: %+v err=%v", out, err)
	}
	if st := e.lotStatus(t, lot); st != "HOLD" {
		t.Fatalf("lot after cancel = %s, want HOLD", st)
	}
	hist, err := sq.NCRHistory(f.Ctx, ncr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := hist[len(hist)-1]; last.EventType != "CANCELLED" || last.Notes != "誤起票" {
		t.Fatalf("history = %+v", last)
	}
	_, err = sq.Cancel(f.Ctx, ncr.ID, "again", e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "CANCELLED")

	var count, critical int
	if err := f.DB.QueryRowContext(f.Ctx, `SELECT ncr_count, critical_ncr_count FROM v_supplier_quality_scorecard WHERE supplier=$1`, e.supplier).Scan(&count, &critical); err != nil {
		t.Fatal(err)
	}
	if count != 0 || critical != 0 {
		t.Fatalf("scorecard counts a cancelled NCR: %d/%d", count, critical)
	}

	if _, err := f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "PASS"}, e.qa); err != nil {
		t.Fatal(err)
	}
	if st := e.lotStatus(t, lot); st != "OK" {
		t.Fatalf("lot after PASS = %s, want OK", st)
	}

	// IN_REWORK can be cancelled; the disposition stays as evidence.
	lot2, rework := mk(3)
	if _, err := sq.Disposition(f.Ctx, rework.ID, SupplierNCRDispositionInput{Disposition: "REWORK", Quantity: 3}, e.planner); err != nil {
		t.Fatal(err)
	}
	if out, err := sq.Cancel(f.Ctx, rework.ID, "手直し不要と判明", e.admn); err != nil || out.Status != "CANCELLED" {
		t.Fatalf("cancel rework: %+v err=%v", out, err)
	}
	if st := e.lotStatus(t, lot2); st != "HOLD" {
		t.Fatalf("lot after rework cancel = %s, want HOLD", st)
	}

	// CLOSED is final.
	_, closed := mk(2)
	if _, err := sq.Disposition(f.Ctx, closed.ID, SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 1}, e.planner); err != nil {
		t.Fatal(err)
	}
	_, err = sq.Cancel(f.Ctx, closed.ID, "x", e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "CLOSED")

	// The database also requires a reason.
	_, open := mk(1)
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE supplier_ncrs SET status='CANCELLED', closed_by_user_id=$2 WHERE id=$1`, open.ID, e.planner.UserID)
	if err == nil || !strings.Contains(err.Error(), "requires a reason") {
		t.Fatalf("db cancel without reason err = %v", err)
	}
}

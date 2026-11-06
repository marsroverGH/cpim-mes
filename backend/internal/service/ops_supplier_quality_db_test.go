package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

type opsSQEnv struct {
	f                       *dbFixture
	item                    *domain.Item
	supplier                string
	operator, planner, admn SupplierQualityActor
	recv                    PurchaseReceiptActor
	qa                      QualityActor
}

func opsNewSQEnv(t *testing.T) *opsSQEnv {
	t.Helper()
	f := newDBFixture(t)
	e := &opsSQEnv{f: f, supplier: opsCode("SUP")}
	e.item = opsItem(t, f, opsCode("PP"), domain.ItemTypePurchasedPart)
	mk := func(role domain.Role) SupplierQualityActor {
		u := f.user(t, opsCode("sq-"+string(role)), role)
		return SupplierQualityActor{UserID: u.ID, Username: u.Username, Role: role}
	}
	e.operator, e.planner, e.admn = mk(domain.RoleOperator), mk(domain.RolePlanner), mk(domain.RoleAdmin)
	e.recv = PurchaseReceiptActor{UserID: e.planner.UserID, Username: e.planner.Username}
	e.qa = QualityActor{UserID: e.operator.UserID, Username: e.operator.Username}
	return e
}

// receive posts a PO receipt and returns its lot.
func (e *opsSQEnv) receive(t *testing.T, qty float64) uuid.UUID {
	t.Helper()
	po := opsPO(t, e.f, e.item.ID, e.supplier, qty)
	r, err := e.f.Svc.Workflow.ReceivePurchase(e.f.Ctx, po.ID, uuid.New(), qty, "", e.recv)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return r.LotID
}

func (e *opsSQEnv) lotStatus(t *testing.T, lot uuid.UUID) string {
	t.Helper()
	var s string
	if err := e.f.DB.GetContext(e.f.Ctx, &s, `SELECT quality_status FROM lots WHERE id=$1`, lot); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpsSupplierQualityProfiles(t *testing.T) {
	t.Parallel()
	e := opsNewSQEnv(t)
	f := e.f
	_, err := f.Svc.SupplierQuality.UpsertProfile(f.Ctx, SupplierQualityProfileInput{SupplierName: "X"}, SupplierQualityActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = f.Svc.SupplierQuality.UpsertProfile(f.Ctx, SupplierQualityProfileInput{SupplierName: "X"}, e.operator)
	opsWantErr(t, err, domain.ErrCodeForbidden, "planner/admin")
	for _, in := range []SupplierQualityProfileInput{
		{SupplierName: "  "},
		{SupplierName: "X", Status: "MAYBE"},
		{SupplierName: "X", TargetPPM: -1},
	} {
		_, err := f.Svc.SupplierQuality.UpsertProfile(f.Ctx, in, e.planner)
		opsWantErr(t, err, domain.ErrCodeBadRequest, "")
	}
	p, err := f.Svc.SupplierQuality.UpsertProfile(f.Ctx, SupplierQualityProfileInput{SupplierName: " " + e.supplier + " ", TargetPPM: 500}, e.planner)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.SupplierName != e.supplier || p.Status != "APPROVED" || p.UpdatedBy != e.planner.Username {
		t.Fatalf("profile: %+v", p)
	}
	p, err = f.Svc.SupplierQuality.UpsertProfile(f.Ctx, SupplierQualityProfileInput{SupplierName: e.supplier, Status: "conditional", InspectionRequired: true}, e.admn)
	if err != nil || p.Status != "CONDITIONAL" || !p.InspectionRequired || p.UpdatedBy != e.admn.Username {
		t.Fatalf("profile update: %+v err=%v", p, err)
	}
	profiles, err := f.Svc.SupplierQuality.Profiles(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range profiles {
		if x.SupplierName == e.supplier {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("upsert must keep one profile row, got %d", n)
	}

	// Inspection-required supplier: receipts are quarantined on HOLD and a
	// HOLD lot cannot be issued.
	lot := e.receive(t, 10)
	if st := e.lotStatus(t, lot); st != "HOLD" {
		t.Fatalf("incoming lot status = %s, want HOLD", st)
	}
	_, err = f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: e.item.ID, Quantity: -1, TxnType: "ISSUE"})
	opsWantErr(t, err, domain.ErrCodeConflict, "insufficient lot stock")
	if _, err := f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "PASS"}, e.qa); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if _, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: e.item.ID, Quantity: -1, TxnType: "ISSUE"}); err != nil {
		t.Fatalf("issue after PASS: %v", err)
	}

	// The DB guard also blocks direct PO inserts for a BLOCKED supplier.
	if _, err := f.Svc.SupplierQuality.UpsertProfile(f.Ctx, SupplierQualityProfileInput{SupplierName: e.supplier, Status: "BLOCKED"}, e.planner); err != nil {
		t.Fatal(err)
	}
	err = f.Repos.Purchases.Create(f.Ctx, &domain.PurchaseOrder{PONo: opsCode("PO"), ItemID: e.item.ID, Supplier: e.supplier, Quantity: 1})
	opsWantDBErr(t, err, "BLOCKED")
}

func TestOpsSupplierNCRLifecycle(t *testing.T) {
	t.Parallel()
	e := opsNewSQEnv(t)
	f := e.f

	// --- FAIL inspection on a supplier lot auto-creates an NCR linked to the PO.
	lotA := e.receive(t, 20)
	insp, err := f.Svc.Quality.Record(f.Ctx, lotA, QualityRecordInput{Result: "FAIL", DefectQty: 6, Notes: "burrs"}, e.qa)
	if err != nil {
		t.Fatalf("fail inspection: %v", err)
	}
	ncrs, err := f.Svc.SupplierQuality.ListNCRs(f.Ctx, "open", e.supplier)
	if err != nil || len(ncrs) != 1 {
		t.Fatalf("auto NCR: %+v err=%v", ncrs, err)
	}
	auto := ncrs[0]
	if auto.LotID != lotA || !opsNear(auto.AffectedQty, 6) || auto.PurchaseOrderID == nil || auto.PONo == "" || auto.InspectionID == nil || *auto.InspectionID != insp.ID {
		t.Fatalf("auto NCR fields: %+v", auto)
	}
	// Creating an NCR for the same inspection is idempotent.
	same, err := f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotA, InspectionID: &insp.ID, AffectedQty: 6}, e.operator)
	if err != nil || same.ID != auto.ID {
		t.Fatalf("idempotent NCR: %+v err=%v", same, err)
	}

	// --- Disposition validation.
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 1}, e.operator)
	opsWantErr(t, err, domain.ErrCodeForbidden, "planner/admin")
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "USE_AS_IS", Quantity: 1}, e.planner)
	opsWantErr(t, err, domain.ErrCodeForbidden, "admin")
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "BURN", Quantity: 1}, e.planner)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "invalid disposition")
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 7}, e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "exceeds affected")
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, uuid.New(), SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 1}, e.planner)
	opsWantErr(t, err, domain.ErrCodeNotFound, "NCR")
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{}, SupplierQualityActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")

	// --- RETURN_TO_SUPPLIER physically removes stock from the REJECTED lot.
	d, err := f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "return_to_supplier", Quantity: 6, Notes: "RMA-1"}, e.planner)
	if err != nil {
		t.Fatalf("return disposition: %v", err)
	}
	if d.Disposition != "RETURN_TO_SUPPLIER" || d.InventoryTxnID == nil || d.DecidedBy != e.planner.Username {
		t.Fatalf("disposition: %+v", d)
	}
	if got := opsLotBalance(t, f, lotA); !opsNear(got, 14) {
		t.Fatalf("lot A balance after return = %.2f, want 14", got)
	}
	closed, _ := f.Svc.SupplierQuality.ListNCRs(f.Ctx, "CLOSED", "")
	if len(closed) != 1 || closed[0].Disposition != "RETURN_TO_SUPPLIER" || !opsNear(closed[0].DispositionQty, 6) {
		t.Fatalf("closed NCR list: %+v", closed)
	}
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, auto.ID, SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 1}, e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "must be OPEN")
	hist, err := f.Svc.SupplierQuality.NCRHistory(f.Ctx, auto.ID)
	if err != nil || len(hist) != 2 || hist[1].EventType != "DISPOSITION_RETURN_TO_SUPPLIER" {
		t.Fatalf("NCR history: %+v err=%v", hist, err)
	}

	// --- Manual NCR -> REWORK -> PASS -> close.
	lotB := e.receive(t, 5)
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotB, Severity: "trivial"}, e.operator)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "severity")
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotB, AffectedQty: -1}, e.operator)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "affectedQty")
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{}, e.operator)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "lotId")
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: uuid.New()}, e.operator)
	opsWantErr(t, err, domain.ErrCodeNotFound, "lot")
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotB, InspectionID: &insp.ID}, e.operator)
	opsWantErr(t, err, domain.ErrCodeConflict, "does not belong")
	viewer := f.user(t, opsCode("v"), domain.RoleViewer)
	_, err = f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotB}, SupplierQualityActor{UserID: viewer.ID, Username: viewer.Username, Role: domain.RoleViewer})
	opsWantErr(t, err, domain.ErrCodeForbidden, "")

	ncrB, err := f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotB, AffectedQty: 5, Severity: "critical", Description: "wrong spec"}, e.operator)
	if err != nil {
		t.Fatalf("create NCR: %v", err)
	}
	if ncrB.Status != "OPEN" || ncrB.Severity != "CRITICAL" || ncrB.Supplier != e.supplier {
		t.Fatalf("manual NCR: %+v", ncrB)
	}
	if st := e.lotStatus(t, lotB); st != "HOLD" {
		t.Fatalf("opening an NCR must hold the lot, got %s", st)
	}
	// A PASS inspection cannot release a lot with an OPEN NCR.
	q, err := f.Svc.Quality.Record(f.Ctx, lotB, QualityRecordInput{Result: "PASS"}, e.qa)
	if err != nil || q.ResultingStatus != "HOLD" {
		t.Fatalf("PASS with open NCR: %+v err=%v", q, err)
	}
	_, err = f.Svc.SupplierQuality.CloseRework(f.Ctx, ncrB.ID, e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "not IN_REWORK")
	if _, err := f.Svc.SupplierQuality.Disposition(f.Ctx, ncrB.ID, SupplierNCRDispositionInput{Disposition: "REWORK", Quantity: 5}, e.planner); err != nil {
		t.Fatalf("rework: %v", err)
	}
	_, err = f.Svc.SupplierQuality.CloseRework(f.Ctx, ncrB.ID, e.operator)
	opsWantErr(t, err, domain.ErrCodeForbidden, "")
	_, err = f.Svc.SupplierQuality.CloseRework(f.Ctx, ncrB.ID, SupplierQualityActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = f.Svc.SupplierQuality.CloseRework(f.Ctx, ncrB.ID, e.planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "PASS inspection")
	_, err = f.Svc.SupplierQuality.CloseRework(f.Ctx, uuid.New(), e.planner)
	opsWantErr(t, err, domain.ErrCodeNotFound, "NCR")
	if _, err := f.Svc.Quality.Record(f.Ctx, lotB, QualityRecordInput{Result: "PASS"}, e.qa); err != nil {
		t.Fatalf("pass after rework: %v", err)
	}
	out, err := f.Svc.SupplierQuality.CloseRework(f.Ctx, ncrB.ID, e.planner)
	if err != nil || out.Status != "CLOSED" {
		t.Fatalf("close rework: %+v err=%v", out, err)
	}
	if st := e.lotStatus(t, lotB); st != "OK" {
		t.Fatalf("lot after rework close = %s, want OK", st)
	}

	// --- USE_AS_IS is blocked while another NCR on the lot is active.
	lotC := e.receive(t, 4)
	n1, err := f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotC, AffectedQty: 4}, e.operator)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := f.Svc.SupplierQuality.CreateNCR(f.Ctx, SupplierNCRCreateInput{LotID: lotC, AffectedQty: 4, Severity: "MINOR"}, e.planner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.SupplierQuality.Disposition(f.Ctx, n1.ID, SupplierNCRDispositionInput{Disposition: "USE_AS_IS", Quantity: 4}, e.admn)
	opsWantErr(t, err, domain.ErrCodeConflict, "another active NCR")
	if _, err := f.Svc.SupplierQuality.Disposition(f.Ctx, n2.ID, SupplierNCRDispositionInput{Disposition: "SCRAP", Quantity: 1}, e.planner); err != nil {
		t.Fatalf("scrap: %v", err)
	}
	if _, err := f.Svc.SupplierQuality.Disposition(f.Ctx, n1.ID, SupplierNCRDispositionInput{Disposition: "USE_AS_IS", Quantity: 3}, e.admn); err != nil {
		t.Fatalf("use as is: %v", err)
	}
	if st := e.lotStatus(t, lotC); st != "OK" {
		t.Fatalf("USE_AS_IS must release lot, got %s", st)
	}
	if got := opsLotBalance(t, f, lotC); !opsNear(got, 3) {
		t.Fatalf("lot C balance = %.2f, want 3 after 1 scrapped", got)
	}

	// --- Scorecard aggregates the supplier's history.
	cards, err := f.Svc.SupplierQuality.Scorecards(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	var card *domain.SupplierQualityScorecard
	for i := range cards {
		if cards[i].Supplier == e.supplier {
			card = &cards[i]
		}
	}
	if card == nil || card.ReceiptCount != 3 || !opsNear(card.ReceivedQty, 29) || card.NCRCount != 4 || card.CriticalNCRCount != 1 ||
		!opsNear(card.ReturnedQty, 6) || !opsNear(card.ScrappedQty, 1) || card.FailInspectionCount != 1 {
		t.Fatalf("scorecard: %+v", card)
	}
	// Dispositions and NCR history are append-only.
	_, err = f.DB.ExecContext(f.Ctx, `DELETE FROM supplier_ncr_dispositions WHERE ncr_id=$1`, auto.ID)
	opsWantDBErr(t, err, "append-only")
	opsAssertLedgerReconciled(t, f)
}

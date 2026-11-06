package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// opsStockFrom posts a RECEIPT into a new lot with an explicit supplier and
// receipt time (lots are FIFO-ordered by received_at).
func opsStockFrom(t *testing.T, f *dbFixture, itemID uuid.UUID, qty float64, lotNo, supplier string, at time.Time) uuid.UUID {
	t.Helper()
	res, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{
		ItemID: itemID, Quantity: qty, TxnType: "RECEIPT", RefDoc: "TEST:" + lotNo,
		LotNo: lotNo, Supplier: supplier, OccurredAt: at,
	})
	if err != nil {
		t.Fatalf("stock %s: %v", lotNo, err)
	}
	return res.Allocations[0].LotID
}

func TestOpsLedgerPostValidation(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	other := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	lot := opsStock(t, f, item.ID, 10, opsCode("L"))
	otherLot := opsStock(t, f, other.ID, 10, opsCode("L"))

	tests := []struct {
		name string
		req  PhysicalInventoryRequest
		code domain.ErrorCode
		want string
	}{
		{"missing item", PhysicalInventoryRequest{Quantity: 1, TxnType: "RECEIPT"}, domain.ErrCodeBadRequest, "itemId"},
		{"zero qty", PhysicalInventoryRequest{ItemID: item.ID, TxnType: "RECEIPT"}, domain.ErrCodeBadRequest, "non-zero"},
		{"negative receipt", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -1, TxnType: "receipt"}, domain.ErrCodeBadRequest, "RECEIPT quantity must be positive"},
		{"positive issue", PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: " ISSUE "}, domain.ErrCodeBadRequest, "ISSUE quantity must be negative"},
		{"logical type", PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RESERVE"}, domain.ErrCodeBadRequest, "RECEIPT, ISSUE, or ADJUST"},
		{"unknown item", PhysicalInventoryRequest{ItemID: uuid.New(), Quantity: 1, TxnType: "RECEIPT"}, domain.ErrCodeNotFound, "item"},
		{"fifo shortage", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -11, TxnType: "ISSUE"}, domain.ErrCodeConflict, "insufficient lot stock"},
		{"allocation sum mismatch", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{LotID: lot, Quantity: -4}}}, domain.ErrCodeConflict, "does not equal"},
		{"allocation sign mismatch", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{LotID: lot, Quantity: 5}}}, domain.ErrCodeBadRequest, "sign"},
		{"zero allocation", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{LotID: lot, Quantity: 0}, {LotID: lot, Quantity: -5}}}, domain.ErrCodeBadRequest, "non-zero"},
		{"allocation without lot", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{Quantity: -5}}}, domain.ErrCodeBadRequest, "requires lotId"},
		{"unknown lot", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{LotID: uuid.New(), Quantity: -5}}}, domain.ErrCodeNotFound, "lot"},
		{"lot of another item", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -5, TxnType: "ISSUE",
			Allocations: []LotAllocationInput{{LotID: otherLot, Quantity: -5}}}, domain.ErrCodeConflict, "does not match"},
		{"lot over-issue", PhysicalInventoryRequest{ItemID: item.ID, Quantity: -11, TxnType: "ADJUST",
			Allocations: []LotAllocationInput{{LotID: lot, Quantity: -11}}}, domain.ErrCodeConflict, "available"},
		{"receipt into unknown lot id", PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotID: uuid.New()}, domain.ErrCodeNotFound, "lot"},
		{"receipt into other item's lot", PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotID: otherLot}, domain.ErrCodeConflict, "does not match"},
	}
	before := opsCount(t, f, `SELECT count(*) FROM inventory_txns`)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Svc.Inventory.ledger.Post(f.Ctx, tc.req)
			opsWantErr(t, err, tc.code, tc.want)
		})
	}
	if after := opsCount(t, f, `SELECT count(*) FROM inventory_txns`); after != before {
		t.Fatalf("rejected postings wrote %d inventory rows", after-before)
	}
	if got := opsLotBalance(t, f, lot); !opsNear(got, 10) {
		t.Fatalf("lot balance changed by rejected postings: %.2f", got)
	}
	if _, err := f.Svc.Inventory.ledger.PostTx(f.Ctx, nil, PhysicalInventoryRequest{}); err == nil {
		t.Fatalf("PostTx without tx must fail")
	}
}

func TestOpsLedgerFIFOAndQualityRules(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	base := time.Now().Add(-72 * time.Hour)
	l1 := opsStockFrom(t, f, item.ID, 5, opsCode("L1"), "", base)
	l2 := opsStockFrom(t, f, item.ID, 5, opsCode("L2"), "", base.Add(time.Hour))
	l3 := opsStockFrom(t, f, item.ID, 5, opsCode("L3"), "", base.Add(2*time.Hour))

	// Put the oldest lot on HOLD: FIFO issue must skip it.
	inspector := f.user(t, opsCode("qa"), domain.RoleOperator)
	qa := QualityActor{UserID: inspector.ID, Username: inspector.Username}
	if _, err := f.Svc.Quality.Record(f.Ctx, l1, QualityRecordInput{Result: "HOLD"}, qa); err != nil {
		t.Fatalf("hold lot: %v", err)
	}
	res, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: -7, TxnType: "ISSUE", RefDoc: "ISS-1"})
	if err != nil {
		t.Fatalf("FIFO issue: %v", err)
	}
	if len(res.Allocations) != 2 || res.Allocations[0].LotID != l2 || !opsNear(res.Allocations[0].Quantity, -5) ||
		res.Allocations[1].LotID != l3 || !opsNear(res.Allocations[1].Quantity, -2) {
		t.Fatalf("FIFO allocations: %+v", res.Allocations)
	}
	for _, a := range res.Allocations {
		if a.MovementType != "ISSUE" || a.RefDoc != "ISS-1" || a.TxnID == nil || *a.TxnID != res.Txn.ID {
			t.Fatalf("movement metadata: %+v", a)
		}
	}
	// Only HOLD stock (5) and 3 OK remain: an 4-unit ISSUE fails, explicit ISSUE from HOLD fails.
	_, err = f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: -4, TxnType: "ISSUE"})
	opsWantErr(t, err, domain.ErrCodeConflict, "insufficient lot stock")
	_, err = f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: -1, TxnType: "ISSUE",
		Allocations: []LotAllocationInput{{LotID: l1, Quantity: -1}}})
	opsWantErr(t, err, domain.ErrCodeConflict, "HOLD/REJECTED")
	// A negative ADJUST with IncludeNonOK reconciles HOLD stock too (FIFO from l1).
	adj, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: -6, TxnType: "ADJUST", IncludeNonOK: true, RefDoc: "CC"})
	if err != nil {
		t.Fatalf("adjust incl. non-OK: %v", err)
	}
	if adj.Allocations[0].LotID != l1 || adj.Allocations[0].MovementType != "ADJUST" {
		t.Fatalf("adjust allocations: %+v", adj.Allocations)
	}
	if got := opsLotBalance(t, f, l1); !opsNear(got, 0) {
		t.Fatalf("l1 balance = %.2f", got)
	}
	if got := opsLotBalance(t, f, l3); !opsNear(got, 2) {
		t.Fatalf("l3 balance = %.2f", got)
	}

	// Positive explicit allocation appends to an existing lot and bumps lots.quantity.
	if _, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 4, TxnType: "ADJUST",
		Allocations: []LotAllocationInput{{LotID: l3, Quantity: 4}}}); err != nil {
		t.Fatalf("positive explicit adjust: %v", err)
	}
	var lotQty float64
	if err := f.DB.GetContext(f.Ctx, &lotQty, `SELECT quantity FROM lots WHERE id=$1`, l3); err != nil || !opsNear(lotQty, 9) {
		t.Fatalf("lot quantity = %.2f err=%v, want 9", lotQty, err)
	}
	// Receipt with LotID extends the lot; RequireSourceDocMatch guards foreign lots.
	if _, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotID: l3}); err != nil {
		t.Fatalf("receipt into lot id: %v", err)
	}
	_, err = f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotID: l3,
		SourceDoc: "WO:OTHER", RequireSourceDocMatch: true})
	opsWantErr(t, err, domain.ErrCodeConflict, "belongs to source")
	var l3No string
	if err := f.DB.GetContext(f.Ctx, &l3No, `SELECT lot_no FROM lots WHERE id=$1`, l3); err != nil {
		t.Fatal(err)
	}
	_, err = f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotNo: l3No,
		SourceDoc: "WO:OTHER", RequireSourceDocMatch: true})
	opsWantErr(t, err, domain.ErrCodeConflict, "belongs to source")
	// Same lot number without the guard appends.
	r, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", LotNo: l3No})
	if err != nil || r.Allocations[0].LotID != l3 {
		t.Fatalf("append by lot number: %v %+v", err, r)
	}
	// Generated lot numbers are sanitized and derived from the reference.
	g, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT", RefDoc: "PO 1/2"})
	if err != nil || len(g.Lots) != 1 || g.Lots[0].LotNo[:6] != "PO-1-2" {
		t.Fatalf("generated lot: %v %+v", err, g)
	}

	onHand, _ := opsBalance(t, f, item.ID)
	if !opsNear(onHand, 15-7-6+4+1+1+1) {
		t.Fatalf("on hand = %.2f", onHand)
	}
	opsAssertLedgerReconciled(t, f)
}

func TestOpsLedgerDatabaseGuards(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	lot := opsStock(t, f, item.ID, 5, opsCode("L"))

	// The repository refuses physical writes that bypass the ledger.
	if err := f.Repos.Inventory.Post(f.Ctx, &domain.InventoryTxn{ItemID: item.ID, Quantity: 1, TxnType: "RECEIPT"}); err == nil {
		t.Fatalf("repository must refuse physical RECEIPT")
	}
	if err := f.Repos.Lots.Create(f.Ctx, &domain.Lot{}); err == nil {
		t.Fatalf("direct lot creation must be refused")
	}
	if err := f.Repos.Lots.AddMovement(f.Ctx, &domain.LotMovement{}); err == nil {
		t.Fatalf("direct lot movement must be refused")
	}

	// A physical txn without lot allocation is rejected at COMMIT by the deferred trigger.
	_, err := f.DB.ExecContext(f.Ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,1,'RECEIPT','RAW',now())`, uuid.New(), item.ID)
	opsWantDBErr(t, err, "lot-allocated")
	// A logical reservation must not carry a lot movement.
	resID := uuid.New()
	tx := f.DB.MustBeginTx(f.Ctx, nil)
	_, err = tx.ExecContext(f.Ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,1,'RESERVE','R',now())`, resID, item.ID)
	if err == nil {
		_, err = tx.ExecContext(f.Ctx, `INSERT INTO lot_movements(id,lot_id,txn_id,quantity,movement_type,ref_doc,occurred_at) VALUES($1,$2,$3,1,'RECEIPT','R',now())`, uuid.New(), lot, resID)
	}
	_ = tx.Rollback()
	opsWantDBErr(t, err, "cannot have a physical lot movement")
	// Lot movement ref_doc must match the transaction.
	txnID := uuid.New()
	tx = f.DB.MustBeginTx(f.Ctx, nil)
	_, err = tx.ExecContext(f.Ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,-1,'ISSUE','A',now())`, txnID, item.ID)
	if err == nil {
		_, err = tx.ExecContext(f.Ctx, `INSERT INTO lot_movements(id,lot_id,txn_id,quantity,movement_type,ref_doc,occurred_at) VALUES($1,$2,$3,-1,'ISSUE','B',now())`, uuid.New(), lot, txnID)
	}
	_ = tx.Rollback()
	opsWantDBErr(t, err, "ref_doc")
	// A lot can never go negative, even through a balanced ADJUST.
	txnID = uuid.New()
	tx = f.DB.MustBeginTx(f.Ctx, nil)
	_, err = tx.ExecContext(f.Ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,-6,'ADJUST','NEG',now())`, txnID, item.ID)
	if err == nil {
		_, err = tx.ExecContext(f.Ctx, `INSERT INTO lot_movements(id,lot_id,txn_id,quantity,movement_type,ref_doc,occurred_at) VALUES($1,$2,$3,-6,'ADJUST','NEG',now())`, uuid.New(), lot, txnID)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	opsWantDBErr(t, err, "negative")
	// Lot item is immutable.
	other := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE lots SET item_id=$1 WHERE id=$2`, other.ID, lot)
	opsWantDBErr(t, err, "immutable")
	if got := opsLotBalance(t, f, lot); !opsNear(got, 5) {
		t.Fatalf("guards must leave lot untouched, balance %.2f", got)
	}
	opsAssertLedgerReconciled(t, f)
}

func TestOpsInventoryServicePost(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)

	rcpt := &domain.InventoryTxn{ItemID: item.ID, Quantity: 10, TxnType: "RECEIPT", RefDoc: "MANUAL-1", LotNo: "M-LOT-1"}
	if err := f.Svc.Inventory.Post(f.Ctx, rcpt); err != nil {
		t.Fatalf("manual receipt: %v", err)
	}
	if rcpt.LotID == nil || rcpt.LotNo != "M-LOT-1" || rcpt.ID == uuid.Nil {
		t.Fatalf("receipt result: %+v", rcpt)
	}
	// Issue from that specific lot.
	iss := &domain.InventoryTxn{ItemID: item.ID, Quantity: -3, TxnType: "ISSUE", RefDoc: "MANUAL-2", LotID: rcpt.LotID}
	if err := f.Svc.Inventory.Post(f.Ctx, iss); err != nil {
		t.Fatalf("manual issue: %v", err)
	}
	if iss.LotID == nil || *iss.LotID != *rcpt.LotID {
		t.Fatalf("issue lot: %+v", iss)
	}
	// Logical reservations bypass the lot ledger.
	res := &domain.InventoryTxn{ItemID: item.ID, Quantity: 2, TxnType: "RESERVE", RefDoc: "HOLD-FOR-X"}
	if err := f.Svc.Inventory.Post(f.Ctx, res); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := f.Svc.Inventory.Post(f.Ctx, &domain.InventoryTxn{ItemID: item.ID, Quantity: 1, TxnType: "UNRESERVE", RefDoc: "HOLD-FOR-X"}); err != nil {
		t.Fatalf("unreserve: %v", err)
	}
	if err := f.Svc.Inventory.Post(f.Ctx, &domain.InventoryTxn{ItemID: item.ID, Quantity: 0, TxnType: "RECEIPT"}); err == nil {
		t.Fatalf("zero receipt must fail")
	}

	onHand, reserved := opsBalance(t, f, item.ID)
	if !opsNear(onHand, 7) || !opsNear(reserved, 1) {
		t.Fatalf("balance on_hand=%.2f reserved=%.2f, want 7/1", onHand, reserved)
	}
	txns, err := f.Svc.Inventory.Transactions(f.Ctx, item.ID)
	if err != nil || len(txns) != 4 {
		t.Fatalf("transactions: %d err=%v", len(txns), err)
	}
	oh, err := f.Svc.Inventory.OnHand(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range oh {
		if r.ItemID == item.ID {
			found = opsNear(r.OnHand, 7)
		}
	}
	if !found {
		t.Fatalf("OnHand view missing item or wrong quantity")
	}
	bal, err := f.Svc.Inventory.Balance(f.Ctx)
	if err != nil || len(bal) == 0 {
		t.Fatalf("balance list: %v", err)
	}
	opsAssertLedgerReconciled(t, f)
}

func TestOpsLotServiceAndAudit(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)

	opsWantErr(t, f.Svc.Lots.Create(f.Ctx, &domain.Lot{ItemID: item.ID}), domain.ErrCodeBadRequest, "> 0")
	exp := BusinessToday().AddDate(0, 6, 0)
	lot := &domain.Lot{ItemID: item.ID, LotNo: "LS-1", Quantity: 8, Supplier: "ACME", SourceDoc: "ASN-9", ExpiryDate: &exp}
	if err := f.Svc.Lots.Create(f.Ctx, lot); err != nil {
		t.Fatalf("lot create: %v", err)
	}
	if lot.ID == uuid.Nil || lot.QualityStatus != "OK" || lot.SourceDoc != "ASN-9" {
		t.Fatalf("created lot: %+v", lot)
	}

	opsWantErr(t, f.Svc.Lots.AddMovement(f.Ctx, &domain.LotMovement{LotID: lot.ID, Quantity: -1, MovementType: "TELEPORT"}), domain.ErrCodeBadRequest, "unsupported")
	if err := f.Svc.Lots.AddMovement(f.Ctx, &domain.LotMovement{LotID: uuid.New(), Quantity: -1, MovementType: "ISSUE"}); err == nil {
		t.Fatalf("movement on unknown lot must fail")
	}
	mv := &domain.LotMovement{LotID: lot.ID, Quantity: -3, MovementType: "CONSUMED", RefDoc: "WO:X"}
	if err := f.Svc.Lots.AddMovement(f.Ctx, mv); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if mv.ID == uuid.Nil || mv.TxnID == nil || mv.MovementType != "CONSUMED" {
		t.Fatalf("movement: %+v", mv)
	}
	for _, m := range []*domain.LotMovement{
		{LotID: lot.ID, Quantity: 2, MovementType: "PRODUCED", RefDoc: "R"},
		{LotID: lot.ID, Quantity: -1, MovementType: "ADJUST", RefDoc: "CC"},
	} {
		if err := f.Svc.Lots.AddMovement(f.Ctx, m); err != nil {
			t.Fatalf("movement %s: %v", m.MovementType, err)
		}
	}
	mvs, err := f.Svc.Lots.Movements(f.Ctx, lot.ID)
	if err != nil || len(mvs) != 4 {
		t.Fatalf("movements: %d err=%v", len(mvs), err)
	}
	used, err := f.Svc.Lots.WhereUsed(f.Ctx, lot.ID)
	if err != nil || len(used) == 0 {
		t.Fatalf("where-used: %d err=%v", len(used), err)
	}
	byItem, err := f.Svc.Lots.ByItem(f.Ctx, item.ID)
	if err != nil || len(byItem) != 1 || !opsNear(byItem[0].Balance, 6) {
		t.Fatalf("by item: %+v err=%v", byItem, err)
	}
	all, err := f.Svc.Lots.List(f.Ctx)
	if err != nil || len(all) < 1 {
		t.Fatalf("list lots: %v", err)
	}

	// Audit log append and filtered listing.
	e := &domain.AuditLogEntry{Username: "auditor", UserRole: "admin", Action: "POST", Resource: "ops-test", ResourceID: lot.ID.String(),
		HTTPStatus: 201, IPAddress: "127.0.0.1", Payload: json.RawMessage(`{"k":1}`)}
	if err := f.Svc.Audit.Record(f.Ctx, e); err != nil {
		t.Fatalf("audit record: %v", err)
	}
	rows, err := f.Svc.Audit.List(f.Ctx, repository.AuditFilter{Resource: "ops-test", Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Username != "auditor" || rows[0].HTTPStatus != 201 {
		t.Fatalf("audit list: %+v err=%v", rows, err)
	}
	if rows, _ := f.Svc.Audit.List(f.Ctx, repository.AuditFilter{Username: "nobody"}); len(rows) != 0 {
		t.Fatalf("audit username filter returned %d rows", len(rows))
	}
	opsAssertLedgerReconciled(t, f)
}

func TestOpsQualityInspections(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	item := opsItem(t, f, opsCode("SA"), domain.ItemTypeSubAssembly)
	lot := opsStockFrom(t, f, item.ID, 10, opsCode("QL"), "", time.Now()) // internal lot (no supplier)
	op := f.user(t, opsCode("insp"), domain.RoleOperator)
	actor := QualityActor{UserID: op.ID, Username: op.Username}

	_, err := f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "PASS"}, QualityActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "MAYBE"}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "PASS / FAIL / HOLD")
	_, err = f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "FAIL", DefectQty: -1}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, ">= 0")
	_, err = f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "FAIL", DefectQty: 11}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "exceed lot quantity")
	_, err = f.Svc.Quality.Record(f.Ctx, uuid.New(), QualityRecordInput{Result: "PASS"}, actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "lot")
	viewer := f.user(t, opsCode("view"), domain.RoleViewer)
	_, err = f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: "PASS"}, QualityActor{UserID: viewer.ID, Username: viewer.Username})
	opsWantDBErr(t, err, "not allowed to record quality inspections")

	steps := []struct {
		result, prev, want string
	}{
		{"hold", "OK", "HOLD"},
		{"FAIL", "HOLD", "REJECTED"},
		{"PASS", "REJECTED", "OK"},
	}
	for _, st := range steps {
		q, err := f.Svc.Quality.Record(f.Ctx, lot, QualityRecordInput{Result: st.result, DefectQty: 1, Notes: " n "}, actor)
		if err != nil {
			t.Fatalf("record %s: %v", st.result, err)
		}
		if q.PreviousStatus == nil || *q.PreviousStatus != st.prev || q.ResultingStatus != st.want || q.Inspector != op.Username || q.Notes != "n" {
			t.Fatalf("inspection %s: %+v", st.result, q)
		}
	}
	hist, err := f.Svc.Quality.StatusHistory(f.Ctx, lot)
	if err != nil || len(hist) != 3 || hist[0].ToStatus != "OK" || hist[2].ToStatus != "HOLD" {
		t.Fatalf("history: %+v err=%v", hist, err)
	}
	byLot, err := f.Svc.Quality.ListByLot(f.Ctx, lot)
	if err != nil || len(byLot) != 3 {
		t.Fatalf("list by lot: %d err=%v", len(byLot), err)
	}
	recent, err := f.Svc.Quality.Recent(f.Ctx, 2)
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent: %d err=%v", len(recent), err)
	}
	// Internal lots never auto-create supplier NCRs.
	if n := opsCount(t, f, `SELECT count(*) FROM supplier_ncrs WHERE lot_id=$1`, lot); n != 0 {
		t.Fatalf("internal lot produced %d NCRs", n)
	}
	// Inspection evidence is append-only; lot status cannot be changed directly.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE quality_inspections SET result='PASS' WHERE lot_id=$1`, lot)
	opsWantDBErr(t, err, "append-only")
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE lots SET quality_status='HOLD' WHERE id=$1`, lot)
	if err == nil {
		t.Fatalf("direct lot quality_status mutation must be rejected")
	}
	for _, r := range []string{"PASS", "fail", " Hold "} {
		if _, err := QualityStatusForResult(r); err != nil {
			t.Fatalf("QualityStatusForResult(%q): %v", r, err)
		}
	}
}

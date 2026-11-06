package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// Shared helpers for the operations / inventory / execution DB integration
// tests (files named ops_*_db_test.go). Every helper is prefixed with "ops" to
// avoid collisions with other test files in this package.

var opsSeq atomic.Int64

// opsCode returns a unique, readable code with the given prefix.
func opsCode(prefix string) string {
	return fmt.Sprintf("%s-%04d", prefix, opsSeq.Add(1))
}

// opsItem creates an item of the given type with sane planning defaults.
func opsItem(t *testing.T, f *dbFixture, code string, typ domain.ItemType) *domain.Item {
	t.Helper()
	it := &domain.Item{
		Code: code, Name: "Item " + code, Type: typ, UoM: "EA",
		LeadTimeDays: 2, LotSize: 1, StandardCost: 10,
	}
	if err := f.Svc.Items.Create(f.Ctx, it); err != nil {
		t.Fatalf("create item %s: %v", code, err)
	}
	return it
}

// opsBOM adds a direct BOM edge through the service (cycle check + LLC).
func opsBOM(t *testing.T, f *dbFixture, parent, child uuid.UUID, qty, scrap float64) *domain.BOMComponent {
	t.Helper()
	c := &domain.BOMComponent{ParentID: parent, ChildID: child, Quantity: qty, ScrapPct: scrap}
	if err := f.Svc.BOM.Add(f.Ctx, c); err != nil {
		t.Fatalf("add BOM %s->%s: %v", parent, child, err)
	}
	return c
}

// opsStock posts a physical RECEIPT into a (new or existing) lot through the
// unified ledger and returns the lot ID.
func opsStock(t *testing.T, f *dbFixture, itemID uuid.UUID, qty float64, lotNo string) uuid.UUID {
	t.Helper()
	res, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{
		ItemID: itemID, Quantity: qty, TxnType: "RECEIPT",
		RefDoc: "TEST-STOCK:" + lotNo, LotNo: lotNo, Supplier: "TEST-SUPPLIER",
	})
	if err != nil {
		t.Fatalf("stock %s qty %.2f: %v", lotNo, qty, err)
	}
	if len(res.Allocations) != 1 {
		t.Fatalf("stock %s: expected one allocation, got %d", lotNo, len(res.Allocations))
	}
	return res.Allocations[0].LotID
}

// opsBalance returns (on_hand, reserved) from v_stock_balance.
func opsBalance(t *testing.T, f *dbFixture, itemID uuid.UUID) (float64, float64) {
	t.Helper()
	var b domain.StockBalance
	err := f.DB.GetContext(f.Ctx, &b, `SELECT item_id, code, name, on_hand, reserved FROM v_stock_balance WHERE item_id=$1`, itemID)
	if err != nil {
		t.Fatalf("stock balance %s: %v", itemID, err)
	}
	return b.OnHand, b.Reserved
}

// opsLotBalance is the physical balance of one lot (sum of lot movements).
func opsLotBalance(t *testing.T, f *dbFixture, lotID uuid.UUID) float64 {
	t.Helper()
	var bal float64
	if err := f.DB.GetContext(f.Ctx, &bal, `SELECT COALESCE(SUM(quantity),0) FROM lot_movements WHERE lot_id=$1`, lotID); err != nil {
		t.Fatalf("lot balance: %v", err)
	}
	return bal
}

// opsAssertLedgerReconciled checks the core ledger invariant: for every item the
// inventory-transaction on-hand equals the lot-movement on-hand.
func opsAssertLedgerReconciled(t *testing.T, f *dbFixture) {
	t.Helper()
	rows, err := f.Svc.Inventory.Reconciliation(f.Ctx)
	if err != nil {
		t.Fatalf("reconciliation: %v", err)
	}
	for _, r := range rows {
		if math.Abs(r.Difference) > 1e-6 {
			t.Errorf("ledger/lot mismatch for %s: ledger=%.4f lots=%.4f", r.ItemCode, r.LedgerOnHand, r.LotOnHand)
		}
	}
}

// opsWorkCenter creates a simple work center.
func opsWorkCenter(t *testing.T, f *dbFixture) *domain.WorkCenter {
	t.Helper()
	wc := &domain.WorkCenter{
		Code: opsCode("WC"), Name: "Test WC", CapacityMinutesPerDay: 480,
		Efficiency: 1, Utilization: 1, MachineCount: 1, WorkerCount: 1,
	}
	if err := f.Repos.WorkCenters.Create(f.Ctx, wc); err != nil {
		t.Fatalf("create work center: %v", err)
	}
	return wc
}

// opsRouting creates an active routing with nOps sequential operations.
func opsRouting(t *testing.T, f *dbFixture, itemID uuid.UUID, nOps int) *domain.Routing {
	t.Helper()
	wc := opsWorkCenter(t, f)
	r := &domain.Routing{ItemID: itemID, Description: "Test routing", IsActive: true}
	if err := f.Repos.Routings.Create(f.Ctx, r); err != nil {
		t.Fatalf("create routing: %v", err)
	}
	for i := 1; i <= nOps; i++ {
		op := &domain.RoutingOperation{
			RoutingID: r.ID, SeqNo: i * 10, WorkCenterID: wc.ID,
			Description: fmt.Sprintf("Op %d", i), SetupMinutes: 5, RunMinutesPerUnit: 1,
			MachinesRequired: 1, WorkersRequired: 1,
		}
		if err := f.Repos.Routings.AddOperation(f.Ctx, op); err != nil {
			t.Fatalf("add routing op: %v", err)
		}
	}
	return r
}

// opsWO creates a PLANNED work order.
func opsWO(t *testing.T, f *dbFixture, itemID uuid.UUID, qty float64) *domain.WorkOrder {
	t.Helper()
	now := TruncateDay(time.Now())
	w := &domain.WorkOrder{
		OrderNo: opsCode("WO"), ItemID: itemID, Quantity: qty,
		StartDate: now, DueDate: now.AddDate(0, 0, 7),
	}
	if err := f.Svc.WorkOrders.Create(f.Ctx, w); err != nil {
		t.Fatalf("create WO: %v", err)
	}
	return w
}

// opsPO creates an OPEN purchase order through the service.
func opsPO(t *testing.T, f *dbFixture, itemID uuid.UUID, supplier string, qty float64) *domain.PurchaseOrder {
	t.Helper()
	p := &domain.PurchaseOrder{
		PONo: opsCode("PO"), ItemID: itemID, Supplier: supplier, Quantity: qty,
		DueDate: TruncateDay(time.Now()).AddDate(0, 0, 5),
	}
	if err := f.Svc.Purchases.Create(f.Ctx, p); err != nil {
		t.Fatalf("create PO: %v", err)
	}
	return p
}

// opsWOOps returns the WO's shop-floor operations ordered by sequence.
func opsWOOps(t *testing.T, f *dbFixture, woID uuid.UUID) []domain.WOOperation {
	t.Helper()
	var ops []domain.WOOperation
	if err := f.DB.SelectContext(f.Ctx, &ops, `SELECT * FROM wo_operations WHERE wo_id=$1 ORDER BY seq_no`, woID); err != nil {
		t.Fatalf("list wo operations: %v", err)
	}
	return ops
}

// opsGetWO re-reads a work order.
func opsGetWO(t *testing.T, f *dbFixture, id uuid.UUID) *domain.WorkOrder {
	t.Helper()
	w, err := f.Svc.WorkOrders.Get(f.Ctx, id)
	if err != nil {
		t.Fatalf("get WO: %v", err)
	}
	return w
}

// opsSFActor creates an operator user and returns a Shop Floor actor for it.
func opsSFActor(t *testing.T, f *dbFixture, role domain.Role) ShopFloorActor {
	t.Helper()
	u := f.user(t, opsCode("sf-"+string(role)), role)
	return ShopFloorActor{UserID: u.ID, Username: u.Username}
}

// opsSOActor creates a planner/admin user for sales/policy services.
func opsSOActor(t *testing.T, f *dbFixture, role domain.Role) SalesOrderActor {
	t.Helper()
	u := f.user(t, opsCode("so-"+string(role)), role)
	return SalesOrderActor{UserID: u.ID, Username: u.Username, Role: role}
}

// opsCount runs a COUNT(*) query.
func opsCount(t *testing.T, f *dbFixture, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.DB.GetContext(f.Ctx, &n, q, args...); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// opsWantErr asserts err is a domain.AppError with the given code and that its
// message contains want (case-insensitive) when want != "".
func opsWantErr(t *testing.T, err error, code domain.ErrorCode, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error containing %q, got nil", code, want)
	}
	var ae *domain.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("expected domain.AppError(%s), got %T: %v", code, err, err)
	}
	if ae.Code != code {
		t.Fatalf("expected code %s, got %s: %v", code, ae.Code, err)
	}
	if want != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

// opsWantDBErr asserts a raw (non-domain) error mentioning want, typically from
// a PL/pgSQL trigger.
func opsWantDBErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected database error containing %q, got nil", want)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

func opsNear(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func opsPtr[T any](v T) *T { return &v }

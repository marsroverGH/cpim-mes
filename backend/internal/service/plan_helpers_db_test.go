package service

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// Shared helpers for the planning-area database integration tests
// (plan_*_db_test.go). Every helper is prefixed with "plan" to avoid name
// collisions with other test files in this package.

const planTol = 1e-6

// planDay returns midnight UTC for the given calendar date.
func planDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// planToday is the business day used by services that plan from time.Now().
func planToday() time.Time { return BusinessToday() }

func planNear(a, b float64) bool { return math.Abs(a-b) <= planTol }

func planAssertNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if !planNear(got, want) {
		t.Fatalf("%s = %.9f, want %.9f", what, got, want)
	}
}

func planAssertDay(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if got.Format("2006-01-02") != want.Format("2006-01-02") {
		t.Fatalf("%s = %s, want %s", what, got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// planErrCode asserts err is a *domain.AppError with the given code.
func planErrCode(t *testing.T, what string, err error, code domain.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %s error, got nil", what, code)
	}
	var ae *domain.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("%s: expected *domain.AppError(%s), got %T: %v", what, code, err, err)
	}
	if ae.Code != code {
		t.Fatalf("%s: error code = %s (%v), want %s", what, ae.Code, err, code)
	}
}

// planItem creates an item; zero-valued master fields get sensible defaults.
func planItem(t *testing.T, f *dbFixture, it domain.Item) domain.Item {
	t.Helper()
	if it.Name == "" {
		it.Name = it.Code + " name"
	}
	if it.UoM == "" {
		it.UoM = "EA"
	}
	if it.Type == "" {
		it.Type = domain.ItemTypeFinished
	}
	if it.LotSize == 0 {
		it.LotSize = 1
	}
	if err := f.Svc.Items.Create(f.Ctx, &it); err != nil {
		t.Fatalf("create item %s: %v", it.Code, err)
	}
	return it
}

func planBOM(t *testing.T, f *dbFixture, parent, child uuid.UUID, qty, scrap float64) {
	t.Helper()
	if err := f.Svc.BOM.Add(f.Ctx, &domain.BOMComponent{ParentID: parent, ChildID: child, Quantity: qty, ScrapPct: scrap}); err != nil {
		t.Fatalf("add BOM %s->%s: %v", parent, child, err)
	}
}

// planReceive posts quality-OK physical stock through the unified ledger.
func planReceive(t *testing.T, f *dbFixture, itemID uuid.UUID, qty float64) {
	t.Helper()
	tx := &domain.InventoryTxn{ItemID: itemID, Quantity: qty, TxnType: "RECEIPT", RefDoc: "PLAN-INIT"}
	if err := f.Svc.Inventory.Post(f.Ctx, tx); err != nil {
		t.Fatalf("receive %v of %s: %v", qty, itemID, err)
	}
}

func planMPS(t *testing.T, f *dbFixture, itemID uuid.UUID, day time.Time, qty float64) {
	t.Helper()
	if err := f.Svc.MPS.Upsert(f.Ctx, &domain.MPSEntry{ItemID: itemID, Period: day, Planned: qty}); err != nil {
		t.Fatalf("upsert MPS: %v", err)
	}
}

func planPO(t *testing.T, f *dbFixture, no string, itemID uuid.UUID, qty float64, due time.Time) domain.PurchaseOrder {
	t.Helper()
	po := domain.PurchaseOrder{PONo: no, ItemID: itemID, Supplier: "PLAN-SUPPLIER", Quantity: qty, OrderDate: due.AddDate(0, 0, -30), DueDate: due}
	if err := f.Svc.Purchases.Create(f.Ctx, &po); err != nil {
		t.Fatalf("create PO %s: %v", no, err)
	}
	return po
}

// planCalendar creates a non-default calendar with the same minutes every day.
func planCalendar(t *testing.T, f *dbFixture, code string, minutesPerDay int) domain.WorkCalendar {
	t.Helper()
	c := domain.WorkCalendar{Code: code, Name: code, MondayMin: minutesPerDay, TuesdayMin: minutesPerDay, WednesdayMin: minutesPerDay,
		ThursdayMin: minutesPerDay, FridayMin: minutesPerDay, SaturdayMin: minutesPerDay, SundayMin: minutesPerDay}
	if err := f.Svc.Calendar.Create(f.Ctx, &c); err != nil {
		t.Fatalf("create calendar %s: %v", code, err)
	}
	return c
}

// planWC creates a work center bound to calID with explicit capacity factors.
func planWC(t *testing.T, f *dbFixture, code string, capMin, eff, util float64, calID *uuid.UUID) domain.WorkCenter {
	t.Helper()
	w := domain.WorkCenter{Code: code, Name: code + " name", CapacityMinutesPerDay: capMin, Efficiency: eff, Utilization: util,
		CalendarID: calID, ShiftStartMinute: 480, MachineCount: 1, WorkerCount: 1}
	if err := f.Svc.WorkCenters.Create(f.Ctx, &w); err != nil {
		t.Fatalf("create work center %s: %v", code, err)
	}
	return w
}

// planRouting creates an active routing with the given operations (SeqNo/RoutingID filled in).
func planRouting(t *testing.T, f *dbFixture, itemID uuid.UUID, ops ...domain.RoutingOperation) []domain.RoutingOperation {
	t.Helper()
	rt := domain.Routing{ItemID: itemID, Description: "plan routing", IsActive: true}
	if err := f.Svc.Routings.Create(f.Ctx, &rt); err != nil {
		t.Fatalf("create routing: %v", err)
	}
	out := make([]domain.RoutingOperation, 0, len(ops))
	for i := range ops {
		op := ops[i]
		op.RoutingID = rt.ID
		if op.SeqNo == 0 {
			op.SeqNo = (i + 1) * 10
		}
		if op.Description == "" {
			op.Description = "op"
		}
		if err := f.Svc.Routings.AddOperation(f.Ctx, &op); err != nil {
			t.Fatalf("add routing op: %v", err)
		}
		out = append(out, op)
	}
	return out
}

func planSOActor(t *testing.T, f *dbFixture, name string, role domain.Role) SalesOrderActor {
	t.Helper()
	u := f.user(t, name, role)
	return SalesOrderActor{UserID: u.ID, Username: u.Username, Role: u.Role}
}

func planCustomer(t *testing.T, f *dbFixture, actor SalesOrderActor, no string) *domain.Customer {
	t.Helper()
	c, err := f.Svc.SalesOrders.CreateCustomer(f.Ctx, CustomerInput{CustomerNo: no, Name: no + " Inc"}, actor)
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return c
}

// planConfirmedOrder creates and confirms a one-line sales order.
func planConfirmedOrder(t *testing.T, f *dbFixture, actor SalesOrderActor, customerID uuid.UUID, no string, itemID uuid.UUID, qty float64, requested time.Time) *domain.SalesOrderDetail {
	t.Helper()
	d, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: no, CustomerID: customerID, OrderDate: requested.AddDate(0, 0, -10).Format("2006-01-02"), RequestedDate: requested.Format("2006-01-02"),
		Lines: []SalesOrderLineInput{{ItemID: itemID, Quantity: qty, UnitPrice: 10}},
	}, actor)
	if err != nil {
		t.Fatalf("create sales order %s: %v", no, err)
	}
	d, err = f.Svc.SalesOrders.Confirm(f.Ctx, d.Order.ID, actor)
	if err != nil {
		t.Fatalf("confirm sales order %s: %v", no, err)
	}
	return d
}

// planMRPRow finds the MRP result for (item, day).
func planMRPRow(rows []domain.MRPResult, itemID uuid.UUID, day time.Time) (domain.MRPResult, bool) {
	for _, r := range rows {
		if r.ItemID == itemID && TruncateDay(r.Period).Equal(TruncateDay(day)) {
			return r, true
		}
	}
	return domain.MRPResult{}, false
}

func planMRPRowsFor(rows []domain.MRPResult, itemID uuid.UUID) []domain.MRPResult {
	var out []domain.MRPResult
	for _, r := range rows {
		if r.ItemID == itemID {
			out = append(out, r)
		}
	}
	return out
}

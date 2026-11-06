package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func opsCustomer(t *testing.T, f *dbFixture, actor SalesOrderActor) *domain.Customer {
	t.Helper()
	c, err := f.Svc.SalesOrders.CreateCustomer(f.Ctx, CustomerInput{CustomerNo: opsCode("C"), Name: " Test Customer "}, actor)
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return c
}

func opsDay(offset int) string {
	return TruncateDay(time.Now()).AddDate(0, 0, offset).Format("2006-01-02")
}

func opsSalesOrder(t *testing.T, f *dbFixture, actor SalesOrderActor, customerID uuid.UUID, lines ...SalesOrderLineInput) *domain.SalesOrderDetail {
	t.Helper()
	so, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: opsCode("SO"), CustomerID: customerID, RequestedDate: opsDay(10), Lines: lines,
	}, actor)
	if err != nil {
		t.Fatalf("create SO: %v", err)
	}
	return so
}

func TestOpsSalesOrderCustomers(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RolePlanner)

	_, err := f.Svc.SalesOrders.CreateCustomer(f.Ctx, CustomerInput{CustomerNo: "X", Name: "Y"}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	tests := []struct {
		name string
		in   CustomerInput
		want string
	}{
		{"missing number", CustomerInput{Name: "n"}, "required"},
		{"missing name", CustomerInput{CustomerNo: "n"}, "required"},
		{"bad status", CustomerInput{CustomerNo: "n", Name: "n", Status: "gone"}, "ACTIVE or BLOCKED"},
	}
	for _, tc := range tests {
		_, err := f.Svc.SalesOrders.CreateCustomer(f.Ctx, tc.in, actor)
		opsWantErr(t, err, domain.ErrCodeBadRequest, tc.want)
	}

	c := opsCustomer(t, f, actor)
	if c.Status != "ACTIVE" || c.Name != "Test Customer" || c.CreatedBy != actor.Username {
		t.Fatalf("customer defaults/trim: %+v", c)
	}
	// Duplicate customer number violates the unique key.
	if _, err := f.Svc.SalesOrders.CreateCustomer(f.Ctx, CustomerInput{CustomerNo: c.CustomerNo, Name: "dup"}, actor); err == nil {
		t.Fatalf("duplicate customerNo must fail")
	}

	upd, err := f.Svc.SalesOrders.UpdateCustomer(f.Ctx, c.ID, CustomerInput{CustomerNo: c.CustomerNo, Name: "Renamed", Status: "blocked", ShipTo: " Dock 1 "}, actor)
	if err != nil {
		t.Fatalf("update customer: %v", err)
	}
	if upd.Status != "BLOCKED" || upd.Name != "Renamed" || upd.ShipTo != "Dock 1" {
		t.Fatalf("updated customer: %+v", upd)
	}
	_, err = f.Svc.SalesOrders.UpdateCustomer(f.Ctx, uuid.New(), CustomerInput{CustomerNo: "a", Name: "b", Status: "ACTIVE"}, actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "customer")
	_, err = f.Svc.SalesOrders.UpdateCustomer(f.Ctx, uuid.Nil, CustomerInput{}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "id required")
	_, err = f.Svc.SalesOrders.UpdateCustomer(f.Ctx, c.ID, CustomerInput{CustomerNo: "a", Name: "b", Status: "x"}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "invalid")
	_, err = f.Svc.SalesOrders.UpdateCustomer(f.Ctx, c.ID, CustomerInput{}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")

	all, err := f.Svc.SalesOrders.ListCustomers(f.Ctx)
	if err != nil {
		t.Fatalf("list customers: %v", err)
	}
	found := false
	for _, x := range all {
		found = found || x.ID == c.ID
	}
	if !found {
		t.Fatalf("created customer missing from list")
	}

	// A BLOCKED customer cannot receive new orders.
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	_, err = f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: opsCode("SO"), CustomerID: c.ID, RequestedDate: opsDay(3),
		Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: 1}},
	}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "BLOCKED")
}

func TestOpsSalesOrderCreateValidation(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RolePlanner)
	c := opsCustomer(t, f, actor)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	rm := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	line := SalesOrderLineInput{ItemID: fg.ID, Quantity: 1}

	tests := []struct {
		name string
		in   SalesOrderCreateInput
		code domain.ErrorCode
		want string
	}{
		{"no lines", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1)}, domain.ErrCodeBadRequest, "at least one line"},
		{"no order no", SalesOrderCreateInput{CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{line}}, domain.ErrCodeBadRequest, "orderNo"},
		{"no requested date", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, Lines: []SalesOrderLineInput{line}}, domain.ErrCodeBadRequest, "date is required"},
		{"rfc3339 date", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: "2026-11-11T00:00:00Z", Lines: []SalesOrderLineInput{line}}, domain.ErrCodeBadRequest, "YYYY-MM-DD"},
		{"bad order date", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, OrderDate: "x", RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{line}}, domain.ErrCodeBadRequest, "YYYY-MM-DD"},
		{"bad promised date", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, PromisedDate: "x", RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{line}}, domain.ErrCodeBadRequest, "YYYY-MM-DD"},
		{"unknown customer", SalesOrderCreateInput{OrderNo: "A", CustomerID: uuid.New(), RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{line}}, domain.ErrCodeNotFound, "customer"},
		{"zero qty", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{{ItemID: fg.ID}}}, domain.ErrCodeBadRequest, "line 1 is invalid"},
		{"negative price", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: 1, UnitPrice: -1}}}, domain.ErrCodeBadRequest, "invalid"},
		{"raw material item", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{{ItemID: rm.ID, Quantity: 1}}}, domain.ErrCodeBadRequest, "FG or SA"},
		{"bad line date", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: 1, RequestedDate: "bad"}}}, domain.ErrCodeBadRequest, "YYYY-MM-DD"},
		{"bad line promise", SalesOrderCreateInput{OrderNo: "A", CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{{ItemID: fg.ID, Quantity: 1, PromisedDate: "bad"}}}, domain.ErrCodeBadRequest, "YYYY-MM-DD"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Svc.SalesOrders.Create(f.Ctx, tc.in, actor)
			opsWantErr(t, err, tc.code, tc.want)
		})
	}
	_, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	// Failed creates leave nothing behind.
	if n := opsCount(t, f, `SELECT count(*) FROM sales_orders WHERE customer_id=$1`, c.ID); n != 0 {
		t.Fatalf("failed creates left %d orders", n)
	}

	// Line-level dates override the header.
	so, err := f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: opsCode("SO"), CustomerID: c.ID, OrderDate: opsDay(0), RequestedDate: opsDay(5), PromisedDate: opsDay(6),
		Lines: []SalesOrderLineInput{
			{ItemID: fg.ID, Quantity: 2, UnitPrice: 3},
			{ItemID: fg.ID, Quantity: 1, RequestedDate: opsDay(8), PromisedDate: opsDay(9)},
		},
	}, actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if so.Order.Status != "DRAFT" || len(so.Lines) != 2 || !opsNear(so.Order.TotalQty, 3) || !opsNear(so.Order.OpenQty, 3) {
		t.Fatalf("created order: %+v", so.Order)
	}
	if got := so.Lines[1].RequestedDate.Format("2006-01-02"); got != opsDay(8) {
		t.Fatalf("line requested date = %s, want %s", got, opsDay(8))
	}
	if got := so.Lines[0].PromisedDate.Format("2006-01-02"); got != opsDay(6) {
		t.Fatalf("line inherits header promise: %s", got)
	}
	// Duplicate order number.
	_, err = f.Svc.SalesOrders.Create(f.Ctx, SalesOrderCreateInput{
		OrderNo: so.Order.OrderNo, CustomerID: c.ID, RequestedDate: opsDay(1), Lines: []SalesOrderLineInput{line},
	}, actor)
	if err == nil {
		t.Fatalf("duplicate order number must fail")
	}
	// DRAFT orders are not demand.
	dem, err := f.Svc.SalesOrders.SalesOrderDemand(f.Ctx, fg.ID, nil, nil, false)
	if err != nil || len(dem) != 0 {
		t.Fatalf("DRAFT demand: %+v err=%v", dem, err)
	}
	_, err = f.Svc.SalesOrders.Get(f.Ctx, uuid.New())
	opsWantErr(t, err, domain.ErrCodeNotFound, "sales order")
}

func TestOpsSalesOrderLifecycle(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RolePlanner)
	c := opsCustomer(t, f, actor)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	lotA := opsStock(t, f, fg.ID, 6, opsCode("FGLOT-A"))
	lotB := opsStock(t, f, fg.ID, 10, opsCode("FGLOT-B"))
	so := opsSalesOrder(t, f, actor, c.ID, SalesOrderLineInput{ItemID: fg.ID, Quantity: 10, UnitPrice: 5})
	lineID := so.Lines[0].ID

	// Allocation before confirmation is rejected.
	_, err := f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "CONFIRMED")
	_, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: uuid.New(), Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "not shippable")

	conf, err := f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, actor)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if conf.Order.Status != "CONFIRMED" || conf.Order.ConfirmedAt == nil || len(conf.History) != 1 || conf.History[0].ToStatus != "CONFIRMED" {
		t.Fatalf("confirmed order: %+v history=%+v", conf.Order, conf.History)
	}
	_, err = f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "only DRAFT")
	_, err = f.Svc.SalesOrders.Confirm(f.Ctx, uuid.New(), actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "sales order")
	// Confirmed lines are immutable at the database level.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE sales_order_lines SET quantity=99 WHERE id=$1`, lineID)
	opsWantDBErr(t, err, "immutable")

	// Confirmed demand shows up.
	dem, err := f.Svc.SalesOrders.SalesOrderDemand(f.Ctx, fg.ID, nil, nil, false)
	if err != nil || len(dem) != 1 || !opsNear(dem[0].Quantity, 10) || dem[0].Source != "ORDER" {
		t.Fatalf("confirmed demand: %+v err=%v", dem, err)
	}
	from, to := TruncateDay(time.Now()), TruncateDay(time.Now()).AddDate(0, 0, 5)
	if dem, _ := f.Svc.SalesOrders.SalesOrderDemand(f.Ctx, fg.ID, &from, &to, false); len(dem) != 0 {
		t.Fatalf("demand window should exclude day+10 order: %+v", dem)
	}

	// --- Allocate: validation, over-allocation, insufficient stock, idempotency.
	_, err = f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "allocationId")
	_, err = f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 11}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "exceeds open")
	_, err = f.Svc.SalesOrders.Allocate(f.Ctx, uuid.New(), SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "line")

	allocID := uuid.New()
	d, err := f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: allocID, Quantity: 8}, actor)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if !opsNear(d.Lines[0].AllocatedQty, 8) {
		t.Fatalf("allocated = %.2f", d.Lines[0].AllocatedQty)
	}
	if _, reserved := opsBalance(t, f, fg.ID); !opsNear(reserved, 8) {
		t.Fatalf("reserved = %.2f, want 8", reserved)
	}
	// Retry with the same allocation ID is a no-op.
	if d, err = f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: allocID, Quantity: 8}, actor); err != nil || !opsNear(d.Lines[0].AllocatedQty, 8) {
		t.Fatalf("idempotent allocate: %v %+v", err, d)
	}
	_, err = f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: allocID, Quantity: 7}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "different allocation parameters")

	// Stock reserved for this order is not available to a second order.
	so2 := opsSalesOrder(t, f, actor, c.ID, SalesOrderLineInput{ItemID: fg.ID, Quantity: 10})
	if _, err := f.Svc.SalesOrders.Confirm(f.Ctx, so2.Order.ID, actor); err != nil {
		t.Fatalf("confirm so2: %v", err)
	}
	_, err = f.Svc.SalesOrders.Allocate(f.Ctx, so2.Lines[0].ID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 9}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "insufficient available stock")

	// --- Release part of the allocation (idempotent).
	relID := uuid.New()
	d, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, lineID, SalesOrderReleaseInput{ReleaseID: relID, Quantity: 2}, actor)
	if err != nil || !opsNear(d.Lines[0].AllocatedQty, 6) {
		t.Fatalf("release allocation: %v %+v", err, d)
	}
	if d, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, lineID, SalesOrderReleaseInput{ReleaseID: relID, Quantity: 2}, actor); err != nil || !opsNear(d.Lines[0].AllocatedQty, 6) {
		t.Fatalf("idempotent release: %v", err)
	}
	_, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, lineID, SalesOrderReleaseInput{ReleaseID: allocID, Quantity: 8}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "different release parameters")
	_, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, lineID, SalesOrderReleaseInput{ReleaseID: uuid.New(), Quantity: 7}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "exceeds allocated")
	_, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, lineID, SalesOrderReleaseInput{Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "releaseId")
	if _, reserved := opsBalance(t, f, fg.ID); !opsNear(reserved, 6) {
		t.Fatalf("reserved after release = %.2f, want 6", reserved)
	}

	// --- Ship: cannot exceed allocation; partial shipment issues FIFO.
	_, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: uuid.New(), Quantity: 7}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "exceeds allocated")
	_, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "shipmentId")
	shipID := uuid.New()
	d, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: shipID, Quantity: 4, Carrier: " UPS ", TrackingNo: "1Z"}, actor)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if d.Order.Status != "PARTIALLY_SHIPPED" || !opsNear(d.Lines[0].ShippedQty, 4) || !opsNear(d.Lines[0].AllocatedQty, 2) || len(d.Shipments) != 1 || d.Shipments[0].Carrier != "UPS" {
		t.Fatalf("after partial ship: %+v lines=%+v", d.Order, d.Lines)
	}
	if got := opsLotBalance(t, f, lotA); !opsNear(got, 2) {
		t.Fatalf("FIFO: lot A balance = %.2f, want 2", got)
	}
	if got := opsLotBalance(t, f, lotB); !opsNear(got, 10) {
		t.Fatalf("FIFO: lot B untouched, balance = %.2f", got)
	}
	if onHand, reserved := opsBalance(t, f, fg.ID); !opsNear(onHand, 12) || !opsNear(reserved, 2) {
		t.Fatalf("after ship on_hand=%.2f reserved=%.2f, want 12/2", onHand, reserved)
	}
	// Idempotent shipment retry; mismatched retry rejected.
	if d, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: shipID, Quantity: 4, Carrier: "UPS", TrackingNo: "1Z"}, actor); err != nil || len(d.Shipments) != 1 {
		t.Fatalf("idempotent ship: %v", err)
	}
	_, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: shipID, Quantity: 4, Carrier: "DHL", TrackingNo: "1Z"}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "different shipment parameters")
	// Shipment-bound ledger rows are immutable.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE inventory_txns SET quantity=-1 WHERE id=$1`, d.Shipments[0].InventoryTxnID)
	if err == nil {
		t.Fatalf("shipment ledger row must be immutable")
	}

	// Allocate and ship the rest -> SHIPPED (spans lots A and B).
	if _, err := f.Svc.SalesOrders.Allocate(f.Ctx, lineID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 4}, actor); err != nil {
		t.Fatalf("allocate rest: %v", err)
	}
	d, err = f.Svc.SalesOrders.Ship(f.Ctx, lineID, SalesOrderShipmentInput{ShipmentID: uuid.New(), Quantity: 6}, actor)
	if err != nil {
		t.Fatalf("ship rest: %v", err)
	}
	if d.Order.Status != "SHIPPED" || !opsNear(d.Order.OpenQty, 0) || !opsNear(d.Order.ShippedQty, 10) {
		t.Fatalf("after full ship: %+v", d.Order)
	}
	if got := opsLotBalance(t, f, lotA); !opsNear(got, 0) {
		t.Fatalf("lot A should be exhausted, got %.2f", got)
	}
	if onHand, reserved := opsBalance(t, f, fg.ID); !opsNear(onHand, 6) || !opsNear(reserved, 0) {
		t.Fatalf("after full ship on_hand=%.2f reserved=%.2f, want 6/0", onHand, reserved)
	}
	_, err = f.Svc.SalesOrders.Cancel(f.Ctx, so.Order.ID, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "terminal")

	// Shipped history is still visible as demand only when requested.
	if dem, _ := f.Svc.SalesOrders.SalesOrderDemand(f.Ctx, fg.ID, nil, nil, true); len(dem) != 2 {
		t.Fatalf("includeShipped demand rows = %d, want 2", len(dem))
	}

	// --- Cancel the second order with an allocation -> reservation released.
	if _, err := f.Svc.SalesOrders.Allocate(f.Ctx, so2.Lines[0].ID, SalesOrderAllocationInput{AllocationID: uuid.New(), Quantity: 5}, actor); err != nil {
		t.Fatalf("allocate so2: %v", err)
	}
	cx, err := f.Svc.SalesOrders.Cancel(f.Ctx, so2.Order.ID, actor)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cx.Order.Status != "CANCELLED" || !opsNear(cx.Lines[0].CancelledQty, 10) || !opsNear(cx.Lines[0].AllocatedQty, 0) || cx.Order.CancelledAt == nil {
		t.Fatalf("cancelled order: %+v %+v", cx.Order, cx.Lines)
	}
	if _, reserved := opsBalance(t, f, fg.ID); !opsNear(reserved, 0) {
		t.Fatalf("cancel must release reservation, reserved=%.2f", reserved)
	}
	_, err = f.Svc.SalesOrders.Cancel(f.Ctx, so2.Order.ID, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "terminal")
	_, err = f.Svc.SalesOrders.Cancel(f.Ctx, uuid.New(), actor)
	opsWantErr(t, err, domain.ErrCodeNotFound, "sales order")
	_, err = f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, so2.Lines[0].ID, SalesOrderReleaseInput{ReleaseID: uuid.New(), Quantity: 1}, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "not allocatable")

	// DRAFT orders can be cancelled directly.
	so3 := opsSalesOrder(t, f, actor, c.ID, SalesOrderLineInput{ItemID: fg.ID, Quantity: 1})
	if cx, err := f.Svc.SalesOrders.Cancel(f.Ctx, so3.Order.ID, actor); err != nil || cx.Order.Status != "CANCELLED" {
		t.Fatalf("cancel draft: %v", err)
	}

	list, err := f.Svc.SalesOrders.List(f.Ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := 0
	for _, o := range list {
		if o.CustomerID == c.ID {
			seen++
		}
	}
	if seen != 3 {
		t.Fatalf("list shows %d of our 3 orders", seen)
	}
	// Status/history and allocation-event audit tables are append-only.
	_, err = f.DB.ExecContext(f.Ctx, `DELETE FROM sales_order_status_history WHERE sales_order_id=$1`, so.Order.ID)
	opsWantDBErr(t, err, "append-only")
	opsAssertLedgerReconciled(t, f)
}

func TestOpsSalesOrderConfirmBlockedCustomer(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	actor := opsSOActor(t, f, domain.RoleAdmin)
	c := opsCustomer(t, f, actor)
	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeSubAssembly)
	so := opsSalesOrder(t, f, actor, c.ID, SalesOrderLineInput{ItemID: fg.ID, Quantity: 1})
	if _, err := f.Svc.SalesOrders.UpdateCustomer(f.Ctx, c.ID, CustomerInput{CustomerNo: c.CustomerNo, Name: c.Name, Status: "BLOCKED"}, actor); err != nil {
		t.Fatalf("block: %v", err)
	}
	_, err := f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, actor)
	opsWantErr(t, err, domain.ErrCodeConflict, "BLOCKED")
	for _, fn := range []func() error{
		func() error { _, e := f.Svc.SalesOrders.Confirm(f.Ctx, so.Order.ID, SalesOrderActor{}); return e },
		func() error { _, e := f.Svc.SalesOrders.Cancel(f.Ctx, so.Order.ID, SalesOrderActor{}); return e },
		func() error {
			_, e := f.Svc.SalesOrders.Allocate(f.Ctx, so.Lines[0].ID, SalesOrderAllocationInput{}, SalesOrderActor{})
			return e
		},
		func() error {
			_, e := f.Svc.SalesOrders.ReleaseAllocation(f.Ctx, so.Lines[0].ID, SalesOrderReleaseInput{}, SalesOrderActor{})
			return e
		},
		func() error {
			_, e := f.Svc.SalesOrders.Ship(f.Ctx, so.Lines[0].ID, SalesOrderShipmentInput{}, SalesOrderActor{})
			return e
		},
	} {
		opsWantErr(t, fn(), domain.ErrCodeUnauthorized, "")
	}
}

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type SalesOrderActor struct {
	UserID   uuid.UUID
	Username string
	Role     domain.Role
}

func (a SalesOrderActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated sales-order actor required")
	}
	return nil
}

type CustomerInput struct {
	CustomerNo string `json:"customerNo"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ShipTo     string `json:"shipTo"`
	Notes      string `json:"notes"`
}

type SalesOrderLineInput struct {
	ItemID        uuid.UUID `json:"itemId"`
	Quantity      float64   `json:"quantity"`
	UnitPrice     float64   `json:"unitPrice"`
	RequestedDate string    `json:"requestedDate"`
	PromisedDate  string    `json:"promisedDate"`
	Notes         string    `json:"notes"`
}

type SalesOrderCreateInput struct {
	OrderNo       string                `json:"orderNo"`
	CustomerID    uuid.UUID             `json:"customerId"`
	OrderDate     string                `json:"orderDate"`
	RequestedDate string                `json:"requestedDate"`
	PromisedDate  string                `json:"promisedDate"`
	Notes         string                `json:"notes"`
	Lines         []SalesOrderLineInput `json:"lines"`
}

type SalesOrderAllocationInput struct {
	AllocationID uuid.UUID `json:"allocationId"`
	Quantity     float64   `json:"quantity"`
}

type SalesOrderReleaseInput struct {
	ReleaseID uuid.UUID `json:"releaseId"`
	Quantity  float64   `json:"quantity"`
}

type SalesOrderShipmentInput struct {
	ShipmentID uuid.UUID `json:"shipmentId"`
	Quantity   float64   `json:"quantity"`
	Carrier    string    `json:"carrier"`
	TrackingNo string    `json:"trackingNo"`
}

type SalesOrderService struct {
	db     *sqlx.DB
	ledger *InventoryLedgerService
}

const salesOrderSelect = `
SELECT so.id,so.order_no,so.customer_id,c.customer_no,c.name AS customer_name,
       so.order_date,so.requested_date,so.promised_date,so.status,so.priority,so.notes,
       so.created_by_user_id,so.created_by,so.confirmed_by_user_id,so.confirmed_by,so.confirmed_at,
       so.cancelled_by_user_id,so.cancelled_by,so.cancelled_at,so.created_at,so.updated_at,
       COALESCE(SUM(l.quantity),0)::double precision AS total_qty,
       COALESCE(SUM(l.allocated_qty),0)::double precision AS allocated_qty,
       COALESCE(SUM(l.shipped_qty),0)::double precision AS shipped_qty,
       COALESCE(SUM(l.cancelled_qty),0)::double precision AS cancelled_qty,
       COALESCE(SUM(GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0)),0)::double precision AS open_qty
  FROM sales_orders so JOIN customers c ON c.id=so.customer_id
  LEFT JOIN sales_order_lines l ON l.sales_order_id=so.id`

func parseISODate(raw string, required bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return nil, domain.NewBadRequest("date is required", nil)
		}
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, domain.NewBadRequest("date must be YYYY-MM-DD", err)
	}
	return &t, nil
}

func (s *SalesOrderService) ListCustomers(ctx context.Context) ([]domain.Customer, error) {
	var rows []domain.Customer
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM customers ORDER BY customer_no`)
	return rows, err
}

func (s *SalesOrderService) CreateCustomer(ctx context.Context, in CustomerInput, actor SalesOrderActor) (*domain.Customer, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	in.CustomerNo = strings.TrimSpace(in.CustomerNo)
	in.Name = strings.TrimSpace(in.Name)
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	if in.CustomerNo == "" || in.Name == "" {
		return nil, domain.NewBadRequest("customerNo and name are required", nil)
	}
	if in.Status == "" {
		in.Status = "ACTIVE"
	}
	if in.Status != "ACTIVE" && in.Status != "BLOCKED" {
		return nil, domain.NewBadRequest("status must be ACTIVE or BLOCKED", nil)
	}
	var row domain.Customer
	err := s.db.GetContext(ctx, &row, `
INSERT INTO customers(id,customer_no,name,status,ship_to,notes,created_by_user_id,created_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *`, uuid.New(), in.CustomerNo, in.Name, in.Status, strings.TrimSpace(in.ShipTo), strings.TrimSpace(in.Notes), actor.UserID, actor.Username)
	return &row, err
}

func (s *SalesOrderService) UpdateCustomer(ctx context.Context, id uuid.UUID, in CustomerInput, actor SalesOrderActor) (*domain.Customer, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, domain.NewBadRequest("customer id required", nil)
	}
	in.CustomerNo = strings.TrimSpace(in.CustomerNo)
	in.Name = strings.TrimSpace(in.Name)
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	if in.CustomerNo == "" || in.Name == "" || (in.Status != "ACTIVE" && in.Status != "BLOCKED") {
		return nil, domain.NewBadRequest("customerNo/name/status are invalid", nil)
	}
	var row domain.Customer
	err := s.db.GetContext(ctx, &row, `
UPDATE customers SET customer_no=$2,name=$3,status=$4,ship_to=$5,notes=$6,updated_at=now()
WHERE id=$1 RETURNING *`, id, in.CustomerNo, in.Name, in.Status, strings.TrimSpace(in.ShipTo), strings.TrimSpace(in.Notes))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound("customer")
	}
	return &row, err
}

func (s *SalesOrderService) List(ctx context.Context) ([]domain.SalesOrder, error) {
	var rows []domain.SalesOrder
	err := s.db.SelectContext(ctx, &rows, salesOrderSelect+`
 GROUP BY so.id,c.customer_no,c.name ORDER BY so.order_date DESC,so.order_no DESC`)
	return rows, err
}

func (s *SalesOrderService) Get(ctx context.Context, id uuid.UUID) (*domain.SalesOrderDetail, error) {
	var order domain.SalesOrder
	if err := s.db.GetContext(ctx, &order, salesOrderSelect+`
 WHERE so.id=$1 GROUP BY so.id,c.customer_no,c.name`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("sales order")
		}
		return nil, err
	}
	var lines []domain.SalesOrderLine
	if err := s.db.SelectContext(ctx, &lines, `
SELECT l.id,l.sales_order_id,l.line_no,l.item_id,i.code AS item_code,i.name AS item_name,
       l.quantity::double precision,l.allocated_qty::double precision,l.shipped_qty::double precision,l.cancelled_qty::double precision,
       GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0)::double precision AS open_qty,
       l.unit_price::double precision,l.requested_date,l.promised_date,l.notes
  FROM sales_order_lines l JOIN items i ON i.id=l.item_id
 WHERE l.sales_order_id=$1 ORDER BY l.line_no`, id); err != nil {
		return nil, err
	}
	var hist []domain.SalesOrderStatusHistory
	if err := s.db.SelectContext(ctx, &hist, `SELECT * FROM sales_order_status_history WHERE sales_order_id=$1 ORDER BY occurred_at,id`, id); err != nil {
		return nil, err
	}
	var ships []domain.SalesOrderShipment
	if err := s.db.SelectContext(ctx, &ships, `SELECT * FROM sales_order_shipments WHERE sales_order_id=$1 ORDER BY shipped_at,id`, id); err != nil {
		return nil, err
	}
	return &domain.SalesOrderDetail{Order: order, Lines: lines, History: hist, Shipments: ships}, nil
}

func (s *SalesOrderService) Create(ctx context.Context, in SalesOrderCreateInput, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	in.OrderNo = strings.TrimSpace(in.OrderNo)
	if in.OrderNo == "" || in.CustomerID == uuid.Nil || len(in.Lines) == 0 {
		return nil, domain.NewBadRequest("orderNo, customerId and at least one line are required", nil)
	}
	orderDate, err := parseISODate(in.OrderDate, false)
	if err != nil {
		return nil, err
	}
	if orderDate == nil {
		t := TruncateDay(time.Now())
		orderDate = &t
	}
	requested, err := parseISODate(in.RequestedDate, true)
	if err != nil {
		return nil, err
	}
	promised, err := parseISODate(in.PromisedDate, false)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var customerStatus string
	if err := tx.GetContext(ctx, &customerStatus, `SELECT status FROM customers WHERE id=$1 FOR SHARE`, in.CustomerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("customer")
		}
		return nil, err
	}
	if customerStatus == "BLOCKED" {
		return nil, domain.NewConflict("customer is BLOCKED")
	}
	orderID := uuid.New()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sales_orders(id,order_no,customer_id,order_date,requested_date,promised_date,status,notes,created_by_user_id,created_by)
VALUES($1,$2,$3,$4,$5,$6,'DRAFT',$7,$8,$9)`, orderID, in.OrderNo, in.CustomerID, *orderDate, *requested, promised, strings.TrimSpace(in.Notes), actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	for idx, li := range in.Lines {
		if li.ItemID == uuid.Nil || li.Quantity <= 0 || li.UnitPrice < 0 {
			return nil, domain.NewBadRequest(fmt.Sprintf("line %d is invalid", idx+1), nil)
		}
		var typ string
		if err := tx.GetContext(ctx, &typ, `SELECT type FROM items WHERE id=$1`, li.ItemID); err != nil {
			return nil, err
		}
		if typ != "FG" && typ != "SA" {
			return nil, domain.NewBadRequest("sales order items must be FG or SA", nil)
		}
		lr := requested
		if strings.TrimSpace(li.RequestedDate) != "" {
			lr, err = parseISODate(li.RequestedDate, true)
			if err != nil {
				return nil, err
			}
		}
		lp := promised
		if strings.TrimSpace(li.PromisedDate) != "" {
			lp, err = parseISODate(li.PromisedDate, false)
			if err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO sales_order_lines(id,sales_order_id,line_no,item_id,quantity,unit_price,requested_date,promised_date,notes)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid.New(), orderID, idx+1, li.ItemID, li.Quantity, li.UnitPrice, *lr, lp, strings.TrimSpace(li.Notes)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, orderID)
}

func (s *SalesOrderService) Confirm(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var row struct {
		Status         string `db:"status"`
		CustomerStatus string `db:"customer_status"`
		LineCount      int    `db:"line_count"`
	}
	if err := tx.GetContext(ctx, &row, `
SELECT so.status,c.status AS customer_status,(SELECT count(*) FROM sales_order_lines WHERE sales_order_id=so.id) AS line_count
FROM sales_orders so JOIN customers c ON c.id=so.customer_id WHERE so.id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("sales order")
		}
		return nil, err
	}
	if row.Status != "DRAFT" {
		return nil, domain.NewConflict("only DRAFT sales orders can be confirmed")
	}
	if row.CustomerStatus == "BLOCKED" {
		return nil, domain.NewConflict("customer is BLOCKED")
	}
	if row.LineCount == 0 {
		return nil, domain.NewConflict("sales order has no lines")
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE sales_orders SET status='CONFIRMED',confirmed_by_user_id=$2,confirmed_by=$3,confirmed_at=$4,updated_at=$4 WHERE id=$1`, id, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_status_history(id,sales_order_id,from_status,to_status,actor_user_id,actor_username,occurred_at) VALUES($1,$2,'DRAFT','CONFIRMED',$3,$4,$5)`, uuid.New(), id, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

type lockedSalesLine struct {
	LineID       uuid.UUID `db:"line_id"`
	OrderID      uuid.UUID `db:"order_id"`
	OrderNo      string    `db:"order_no"`
	OrderStatus  string    `db:"order_status"`
	ItemID       uuid.UUID `db:"item_id"`
	Quantity     float64   `db:"quantity"`
	AllocatedQty float64   `db:"allocated_qty"`
	ShippedQty   float64   `db:"shipped_qty"`
	CancelledQty float64   `db:"cancelled_qty"`
}

func lockSalesLine(ctx context.Context, tx *sqlx.Tx, lineID uuid.UUID) (*lockedSalesLine, error) {
	var l lockedSalesLine
	err := tx.GetContext(ctx, &l, `
SELECT l.id AS line_id,so.id AS order_id,so.order_no,so.status AS order_status,l.item_id,
       l.quantity::double precision,l.allocated_qty::double precision,l.shipped_qty::double precision,l.cancelled_qty::double precision
FROM sales_order_lines l JOIN sales_orders so ON so.id=l.sales_order_id
WHERE l.id=$1 FOR UPDATE OF so,l`, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound("sales order line")
	}
	return &l, err
}

func salesReserveRef(orderNo string, lineID uuid.UUID) string {
	return fmt.Sprintf("SO:%s:LINE:%s", orderNo, lineID.String())
}

func (s *SalesOrderService) Allocate(ctx context.Context, lineID uuid.UUID, in SalesOrderAllocationInput, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if in.AllocationID == uuid.Nil || in.Quantity <= 0 {
		return nil, domain.NewBadRequest("allocationId and positive quantity are required", nil)
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var existing struct {
		LineID    uuid.UUID `db:"sales_order_line_id"`
		EventType string    `db:"event_type"`
		Quantity  float64   `db:"quantity"`
	}
	if err := tx.GetContext(ctx, &existing, `SELECT sales_order_line_id,event_type,quantity::double precision AS quantity FROM sales_order_allocation_events WHERE id=$1`, in.AllocationID); err == nil {
		if existing.LineID != lineID || existing.EventType != "ALLOCATE" || abs(existing.Quantity-in.Quantity) > 1e-9 {
			return nil, domain.NewConflict("allocationId was already used for different allocation parameters")
		}
		l, e := lockSalesLine(ctx, tx, existing.LineID)
		if e != nil {
			return nil, e
		}
		_ = tx.Commit()
		return s.Get(ctx, l.OrderID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	l, err := lockSalesLine(ctx, tx, lineID)
	if err != nil {
		return nil, err
	}
	if l.OrderStatus != "CONFIRMED" && l.OrderStatus != "PARTIALLY_SHIPPED" {
		return nil, domain.NewConflict("sales order must be CONFIRMED or PARTIALLY_SHIPPED")
	}
	remaining := l.Quantity - l.ShippedQty - l.CancelledQty - l.AllocatedQty
	if in.Quantity > remaining+1e-9 {
		return nil, domain.NewConflict("allocation exceeds open unallocated quantity")
	}
	var locked uuid.UUID
	if err := tx.GetContext(ctx, &locked, `SELECT id FROM items WHERE id=$1 FOR UPDATE`, l.ItemID); err != nil {
		return nil, err
	}
	var b domain.StockBalance
	if err := tx.GetContext(ctx, &b, `SELECT item_id,code,name,on_hand,reserved FROM v_stock_balance WHERE item_id=$1`, l.ItemID); err != nil {
		return nil, err
	}
	if b.Available()+1e-9 < in.Quantity {
		return nil, domain.NewConflict(fmt.Sprintf("insufficient available stock: need %.2f, available %.2f", in.Quantity, b.Available()))
	}
	now := time.Now().UTC()
	txnID := uuid.New()
	ref := salesReserveRef(l.OrderNo, l.LineID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,$3,'RESERVE',$4,$5)`, txnID, l.ItemID, in.Quantity, ref, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_allocation_events(id,sales_order_line_id,event_type,quantity,inventory_txn_id,actor_user_id,actor_username,occurred_at) VALUES($1,$2,'ALLOCATE',$3,$4,$5,$6,$7)`, in.AllocationID, l.LineID, in.Quantity, txnID, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET allocated_qty=allocated_qty+$2 WHERE id=$1`, l.LineID, in.Quantity); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, l.OrderID)
}

func (s *SalesOrderService) ReleaseAllocation(ctx context.Context, lineID uuid.UUID, in SalesOrderReleaseInput, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if in.ReleaseID == uuid.Nil || in.Quantity <= 0 {
		return nil, domain.NewBadRequest("releaseId and positive quantity are required", nil)
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing struct {
		LineID    uuid.UUID `db:"sales_order_line_id"`
		EventType string    `db:"event_type"`
		Quantity  float64   `db:"quantity"`
	}
	if err := tx.GetContext(ctx, &existing, `SELECT sales_order_line_id,event_type,quantity::double precision AS quantity FROM sales_order_allocation_events WHERE id=$1`, in.ReleaseID); err == nil {
		if existing.LineID != lineID || existing.EventType != "RELEASE" || abs(existing.Quantity-in.Quantity) > 1e-9 {
			return nil, domain.NewConflict("releaseId was already used for different release parameters")
		}
		l, e := lockSalesLine(ctx, tx, existing.LineID)
		if e != nil {
			return nil, e
		}
		_ = tx.Commit()
		return s.Get(ctx, l.OrderID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	l, err := lockSalesLine(ctx, tx, lineID)
	if err != nil {
		return nil, err
	}
	if l.OrderStatus != "CONFIRMED" && l.OrderStatus != "PARTIALLY_SHIPPED" {
		return nil, domain.NewConflict("sales order is not allocatable")
	}
	if in.Quantity > l.AllocatedQty+1e-9 {
		return nil, domain.NewConflict("release exceeds allocated quantity")
	}
	now := time.Now().UTC()
	txnID := uuid.New()
	ref := salesReserveRef(l.OrderNo, l.LineID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,$3,'UNRESERVE',$4,$5)`, txnID, l.ItemID, in.Quantity, ref, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_allocation_events(id,sales_order_line_id,event_type,quantity,inventory_txn_id,actor_user_id,actor_username,occurred_at) VALUES($1,$2,'RELEASE',$3,$4,$5,$6,$7)`, in.ReleaseID, l.LineID, in.Quantity, txnID, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET allocated_qty=allocated_qty-$2 WHERE id=$1`, l.LineID, in.Quantity); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, l.OrderID)
}

func (s *SalesOrderService) Ship(ctx context.Context, lineID uuid.UUID, in SalesOrderShipmentInput, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if in.ShipmentID == uuid.Nil || in.Quantity <= 0 {
		return nil, domain.NewBadRequest("shipmentId and positive quantity are required", nil)
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existingShipment struct {
		OrderID    uuid.UUID `db:"sales_order_id"`
		LineID     uuid.UUID `db:"sales_order_line_id"`
		Quantity   float64   `db:"quantity"`
		Carrier    string    `db:"carrier"`
		TrackingNo string    `db:"tracking_no"`
	}
	if err := tx.GetContext(ctx, &existingShipment, `SELECT sales_order_id,sales_order_line_id,quantity::double precision AS quantity,carrier,tracking_no FROM sales_order_shipments WHERE id=$1`, in.ShipmentID); err == nil {
		if existingShipment.LineID != lineID || abs(existingShipment.Quantity-in.Quantity) > 1e-9 ||
			existingShipment.Carrier != strings.TrimSpace(in.Carrier) || existingShipment.TrackingNo != strings.TrimSpace(in.TrackingNo) {
			return nil, domain.NewConflict("shipmentId was already used for different shipment parameters")
		}
		_ = tx.Commit()
		return s.Get(ctx, existingShipment.OrderID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	l, err := lockSalesLine(ctx, tx, lineID)
	if err != nil {
		return nil, err
	}
	if l.OrderStatus != "CONFIRMED" && l.OrderStatus != "PARTIALLY_SHIPPED" {
		return nil, domain.NewConflict("sales order is not shippable")
	}
	if in.Quantity > l.AllocatedQty+1e-9 {
		return nil, domain.NewConflict("shipment exceeds allocated quantity")
	}
	if in.Quantity > l.Quantity-l.ShippedQty-l.CancelledQty+1e-9 {
		return nil, domain.NewConflict("shipment exceeds open quantity")
	}
	now := time.Now().UTC()
	shipRef := fmt.Sprintf("SO:%s:SHIP:%s", l.OrderNo, in.ShipmentID.String())
	issue, err := s.ledger.PostTx(ctx, tx, PhysicalInventoryRequest{ItemID: l.ItemID, Quantity: -in.Quantity, TxnType: "ISSUE", RefDoc: shipRef, OccurredAt: now, MovementType: "ISSUE"})
	if err != nil {
		return nil, err
	}
	unresID := uuid.New()
	reserveRef := salesReserveRef(l.OrderNo, l.LineID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,$3,'UNRESERVE',$4,$5)`, unresID, l.ItemID, in.Quantity, reserveRef, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_allocation_events(id,sales_order_line_id,event_type,quantity,inventory_txn_id,actor_user_id,actor_username,occurred_at) VALUES($1,$2,'SHIP_RELEASE',$3,$4,$5,$6,$7)`, uuid.New(), l.LineID, in.Quantity, unresID, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_shipments(id,sales_order_id,sales_order_line_id,quantity,inventory_txn_id,shipped_at,shipped_by_user_id,shipped_by_username,carrier,tracking_no) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, in.ShipmentID, l.OrderID, l.LineID, in.Quantity, issue.Txn.ID, now, actor.UserID, actor.Username, strings.TrimSpace(in.Carrier), strings.TrimSpace(in.TrackingNo)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET allocated_qty=allocated_qty-$2,shipped_qty=shipped_qty+$2 WHERE id=$1`, l.LineID, in.Quantity); err != nil {
		return nil, err
	}
	var totalOpen, totalShipped float64
	if err := tx.GetContext(ctx, &totalOpen, `SELECT COALESCE(SUM(GREATEST(quantity-shipped_qty-cancelled_qty,0)),0)::double precision FROM sales_order_lines WHERE sales_order_id=$1`, l.OrderID); err != nil {
		return nil, err
	}
	if err := tx.GetContext(ctx, &totalShipped, `SELECT COALESCE(SUM(shipped_qty),0)::double precision FROM sales_order_lines WHERE sales_order_id=$1`, l.OrderID); err != nil {
		return nil, err
	}
	newStatus := "PARTIALLY_SHIPPED"
	if totalOpen <= 1e-9 && totalShipped > 0 {
		newStatus = "SHIPPED"
	}
	if newStatus != l.OrderStatus {
		if _, err := tx.ExecContext(ctx, `UPDATE sales_orders SET status=$2,updated_at=$3 WHERE id=$1`, l.OrderID, newStatus, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_status_history(id,sales_order_id,from_status,to_status,actor_user_id,actor_username,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.New(), l.OrderID, l.OrderStatus, newStatus, actor.UserID, actor.Username, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, l.OrderID)
}

func (s *SalesOrderService) Cancel(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var order struct {
		OrderNo string `db:"order_no"`
		Status  string `db:"status"`
	}
	if err := tx.GetContext(ctx, &order, `SELECT order_no,status FROM sales_orders WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("sales order")
		}
		return nil, err
	}
	if order.Status == "SHIPPED" || order.Status == "CANCELLED" {
		return nil, domain.NewConflict("sales order is already terminal")
	}
	var lines []lockedSalesLine
	if err := tx.SelectContext(ctx, &lines, `SELECT l.id AS line_id,so.id AS order_id,so.order_no,so.status AS order_status,l.item_id,l.quantity::double precision,l.allocated_qty::double precision,l.shipped_qty::double precision,l.cancelled_qty::double precision FROM sales_order_lines l JOIN sales_orders so ON so.id=l.sales_order_id WHERE so.id=$1 ORDER BY l.line_no FOR UPDATE OF l`, id); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, l := range lines {
		if l.AllocatedQty > 1e-9 {
			txnID := uuid.New()
			if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_txns(id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES($1,$2,$3,'UNRESERVE',$4,$5)`, txnID, l.ItemID, l.AllocatedQty, salesReserveRef(order.OrderNo, l.LineID), now); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_allocation_events(id,sales_order_line_id,event_type,quantity,inventory_txn_id,actor_user_id,actor_username,occurred_at) VALUES($1,$2,'CANCEL_RELEASE',$3,$4,$5,$6,$7)`, uuid.New(), l.LineID, l.AllocatedQty, txnID, actor.UserID, actor.Username, now); err != nil {
				return nil, err
			}
		}
		cancelQty := l.Quantity - l.ShippedQty
		if cancelQty < 0 {
			cancelQty = 0
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET allocated_qty=0,cancelled_qty=$2 WHERE id=$1`, l.LineID, cancelQty); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_orders SET status='CANCELLED',cancelled_by_user_id=$2,cancelled_by=$3,cancelled_at=$4,updated_at=$4 WHERE id=$1`, id, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_status_history(id,sales_order_id,from_status,to_status,actor_user_id,actor_username,occurred_at) VALUES($1,$2,$3,'CANCELLED',$4,$5,$6)`, uuid.New(), id, order.Status, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// SalesOrderDemand returns committed demand rows from formal Sales Orders.
// includeShipped controls whether historical fully-shipped orders are included.
func (s *SalesOrderService) SalesOrderDemand(ctx context.Context, itemID uuid.UUID, from, to *time.Time, includeShipped bool) ([]domain.DemandForecast, error) {
	where := ` WHERE l.item_id=$1 AND so.status IN ('CONFIRMED','PARTIALLY_SHIPPED'`
	if includeShipped {
		where += `,'SHIPPED'`
	}
	where += `)`
	args := []any{itemID}
	n := 2
	if from != nil {
		where += fmt.Sprintf(` AND COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) >= $%d`, n)
		args = append(args, *from)
		n++
	}
	if to != nil {
		where += fmt.Sprintf(` AND COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) <= $%d`, n)
		args = append(args, *to)
	}
	qtyExpr := `l.quantity-l.cancelled_qty`
	if !includeShipped {
		qtyExpr = `l.quantity-l.shipped_qty-l.cancelled_qty`
	}
	var rows []domain.DemandForecast
	q := `SELECT l.id AS id,l.item_id,COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) AS due_date,(` + qtyExpr + `)::double precision AS quantity,'ORDER' AS source,so.created_at FROM sales_order_lines l JOIN sales_orders so ON so.id=l.sales_order_id` + where + ` AND (` + qtyExpr + `) > 0 ORDER BY due_date,l.id`
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

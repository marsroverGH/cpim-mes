package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// Workflow Service — 業務一貫フロー (受注→購買→製造→完成)
// ====================================================================
//
// 3つのオーケストレーション操作:
//   1. ReleaseWorkOrder   — Release時の直下BOMをSnapshot化し、その固定所要量を予約 (RESERVE)
//   2. ReceivePurchase    — PO 入荷時に inventory_txns + lots + lot_movements を一括起票
//   3. CompleteWorkOrder  — Release時BOM Snapshotを使い、予約解除→ISSUE→親 RECEIPT を一括起票
//
// 全ての操作は単一の DB トランザクションで包み、失敗時はロールバック。

type WorkflowService struct {
	db     *sqlx.DB
	repos  *repository.Repositories
	ledger *InventoryLedgerService
}

func NewWorkflowService(db *sqlx.DB, r *repository.Repositories, ledger *InventoryLedgerService) *WorkflowService {
	return &WorkflowService{db: db, repos: r, ledger: ledger}
}

// --------------------------------------------------------------------
// Reservation calculation (pure function for testability)
// --------------------------------------------------------------------

// ReservationLine — 1つの子部品に対する所要量
type ReservationLine struct {
	ChildID    uuid.UUID `json:"childId"`
	ChildCode  string    `json:"childCode"`
	Required   float64   `json:"required"`
	Available  float64   `json:"available"`
	Sufficient bool      `json:"sufficient"`
}

// ComponentRequirement — WO execution用の直下構成品所要量。
// MRP用の再帰展開結果とは型を分け、実行系が誤って多段BOMを消費しないようにする。
type ComponentRequirement struct {
	ChildID  uuid.UUID `json:"childId"`
	Required float64   `json:"required"`
}

// BOMSnapshot identifies the immutable direct-BOM state captured when a WO is released.
// The snapshot ID is the WO's manufacturing revision reference even if the live BOM
// is later modified or replaced by an ECO.
type BOMSnapshot struct {
	ID           uuid.UUID `db:"id"             json:"id"`
	WorkOrderID  uuid.UUID `db:"work_order_id"  json:"workOrderId"`
	ParentItemID uuid.UUID `db:"parent_item_id" json:"parentItemId"`
	CapturedAt   time.Time `db:"captured_at"    json:"capturedAt"`
	Source       string    `db:"source"         json:"source"`
	Notes        string    `db:"notes"          json:"notes"`
}

// BOMSnapshotLine is an immutable copy of one direct BOM edge at WO release.
// Item descriptive fields and standard cost are copied as audit attributes;
// execution uses ChildID, QuantityPer and ScrapPct from this row only.
type BOMSnapshotLine struct {
	ID                   uuid.UUID  `db:"id"                      json:"id"`
	SnapshotID           uuid.UUID  `db:"snapshot_id"             json:"snapshotId"`
	LineNo               int        `db:"line_no"                 json:"lineNo"`
	SourceBOMComponentID *uuid.UUID `db:"source_bom_component_id" json:"sourceBomComponentId,omitempty"`
	ChildID              uuid.UUID  `db:"child_item_id"           json:"childId"`
	ChildCode            string     `db:"child_code"              json:"childCode"`
	ChildName            string     `db:"child_name"              json:"childName"`
	ChildUoM             string     `db:"child_uom"               json:"childUom"`
	QuantityPer          float64    `db:"quantity_per"            json:"quantityPer"`
	ScrapPct             float64    `db:"scrap_pct"               json:"scrapPct"`
	RequiredQty          float64    `db:"required_qty"            json:"requiredQty"`
	StandardCostSnapshot float64    `db:"standard_cost_snapshot"  json:"standardCostSnapshot"`
}

// SnapshotRequirements calculates material demand from the frozen WO snapshot.
// This is deliberately independent of bom_components so later BOM/ECO changes
// cannot alter an already released WO.
func SnapshotRequirements(lines []BOMSnapshotLine, parentQty float64) []ComponentRequirement {
	out := make([]ComponentRequirement, 0, len(lines))
	for _, line := range lines {
		required := parentQty * line.QuantityPer * (1 + line.ScrapPct)
		if required <= 0 {
			continue
		}
		out = append(out, ComponentRequirement{ChildID: line.ChildID, Required: required})
	}
	return out
}

// CalcReservation — 直下構成品所要量と利用可能在庫から予約計画を組み立てる純粋関数
func CalcReservation(
	requirements []ComponentRequirement,
	balances map[uuid.UUID]float64, // child id → available qty
	codes map[uuid.UUID]string,
) []ReservationLine {
	out := make([]ReservationLine, 0, len(requirements))
	for _, req := range requirements {
		avail := balances[req.ChildID]
		out = append(out, ReservationLine{
			ChildID:    req.ChildID,
			ChildCode:  codes[req.ChildID],
			Required:   req.Required,
			Available:  avail,
			Sufficient: avail >= req.Required,
		})
	}
	return out
}

// DirectBOMRequirements converts live direct-BOM rows into requirements.
// It is retained as a pure planning/regression helper. Released WO execution
// MUST use SnapshotRequirements so later BOM/ECO changes cannot alter the WO.
// Neither helper recurses into subassembly BOMs.
func DirectBOMRequirements(components []domain.BOMComponent, parentQty float64) []ComponentRequirement {
	out := make([]ComponentRequirement, 0, len(components))
	for _, c := range components {
		required := parentQty * c.Quantity * (1 + c.ScrapPct)
		if required <= 0 {
			continue
		}
		out = append(out, ComponentRequirement{
			ChildID:  c.ChildID,
			Required: required,
		})
	}
	return out
}

// CalcCompletionBatch validates one incremental physical completion and returns
// the resulting cumulative state. requested=nil means "complete all remaining"
// for backward compatibility with the old full-completion API.
func CalcCompletionBatch(planned, completed float64, requested *float64) (
	batch, cumulative, remaining float64, status string, err error,
) {
	remainingBefore := planned - completed
	if planned <= 0 || remainingBefore <= 1e-9 {
		return 0, completed, 0, "", fmt.Errorf("work order has no remaining quantity to complete")
	}
	batch = remainingBefore
	if requested != nil {
		batch = *requested
		if batch <= 0 {
			return 0, completed, remainingBefore, "", fmt.Errorf("completion quantity must be > 0")
		}
	}
	if batch > remainingBefore+1e-9 {
		return 0, completed, remainingBefore, "", fmt.Errorf(
			"completion quantity %.2f exceeds remaining WO quantity %.2f", batch, remainingBefore)
	}
	if remainingBefore-batch < 1e-9 {
		batch = remainingBefore
	}
	cumulative = completed + batch
	remaining = planned - cumulative
	status = "IN_PROGRESS"
	if remaining < 1e-9 {
		remaining = 0
		cumulative = planned
		status = "COMPLETED"
	}
	return batch, cumulative, remaining, status, nil
}

// --------------------------------------------------------------------
// 1. Release Work Order
// --------------------------------------------------------------------

type ReleaseResult struct {
	WorkOrderID   uuid.UUID         `json:"workOrderId"`
	OrderNo       string            `json:"orderNo"`
	BOMSnapshotID uuid.UUID         `json:"bomSnapshotId"`
	BOMSnapshotAt time.Time         `json:"bomSnapshotAt"`
	Reservations  []ReservationLine `json:"reservations"`
}

func (s *WorkflowService) ReleaseWorkOrder(ctx context.Context, woID uuid.UUID) (*ReleaseResult, error) {
	// IMPORTANT: every read that participates in the reservation decision is
	// performed inside this transaction.  Reading WO status or inventory before
	// BEGIN would leave a classic check-then-act race.
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// 1) Serialize releases for the same work order.
	// The second concurrent caller waits here, then re-reads the committed
	// RELEASED status and is rejected before creating any reservation rows.
	var wo domain.WorkOrder
	if err := tx.GetContext(ctx, &wo, `
		SELECT *
		  FROM work_orders
		 WHERE id=$1
		 FOR UPDATE
	`, woID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("work order")
		}
		return nil, err
	}
	if wo.Status != "PLANNED" {
		return nil, domain.NewConflict(
			fmt.Sprintf("WO must be in PLANNED status (current: %s)", wo.Status))
	}

	// 2) Capture the currently committed direct BOM as an immutable WO snapshot.
	// The SELECT is one PostgreSQL statement snapshot: it observes either the
	// committed BOM before or after a concurrent ECO transaction, never an
	// uncommitted intermediate state.  Subsequent WO execution never re-reads
	// bom_components.
	var snapshotLines []BOMSnapshotLine
	if err := tx.SelectContext(ctx, &snapshotLines, `
		SELECT b.id AS source_bom_component_id,
		       b.child_id AS child_item_id,
		       i.code AS child_code, i.name AS child_name, i.uom AS child_uom,
		       b.quantity AS quantity_per, b.scrap_pct,
		       i.standard_cost AS standard_cost_snapshot
		  FROM bom_components b
		  JOIN items i ON i.id=b.child_id
		 WHERE b.parent_id=$1
		 ORDER BY b.child_id
	`, wo.ItemID); err != nil {
		return nil, err
	}

	snapshotID := uuid.New()
	snapshotAt := time.Now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_order_bom_snapshots
		  (id, work_order_id, parent_item_id, captured_at, source, notes)
		VALUES ($1, $2, $3, $4, 'RELEASE',
		        'Immutable direct BOM captured by ReleaseWorkOrder')
	`, snapshotID, wo.ID, wo.ItemID, snapshotAt); err != nil {
		return nil, fmt.Errorf("create WO BOM snapshot: %w", err)
	}

	for i := range snapshotLines {
		line := &snapshotLines[i]
		line.ID = uuid.New()
		line.SnapshotID = snapshotID
		line.LineNo = i + 1
		line.RequiredQty = wo.Quantity * line.QuantityPer * (1 + line.ScrapPct)
		if line.QuantityPer <= 0 || line.ScrapPct < 0 || line.RequiredQty <= 0 {
			return nil, domain.NewConflict(fmt.Sprintf(
				"invalid BOM line for component %s: qty/parent=%.6f scrap=%.6f",
				line.ChildCode, line.QuantityPer, line.ScrapPct))
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_order_bom_snapshot_lines
			  (id, snapshot_id, line_no, source_bom_component_id,
			   child_item_id, child_code, child_name, child_uom,
			   quantity_per, scrap_pct, required_qty, standard_cost_snapshot)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		`, line.ID, line.SnapshotID, line.LineNo, line.SourceBOMComponentID,
			line.ChildID, line.ChildCode, line.ChildName, line.ChildUoM,
			line.QuantityPer, line.ScrapPct, line.RequiredQty, line.StandardCostSnapshot); err != nil {
			return nil, fmt.Errorf("snapshot BOM component %s: %w", line.ChildCode, err)
		}
	}

	requirements := SnapshotRequirements(snapshotLines, wo.Quantity)

	// 3) Lock every snapshotted component item row BEFORE calculating available stock.
	// All callers use a deterministic UUID order. Therefore:
	//   * same-WO releases serialize on work_orders;
	//   * different WOs sharing a component serialize on that item row;
	//   * multi-component WOs do not deadlock merely because their BOM order differs.
	lockIDs := SortedUniqueRequirementIDs(requirements)
	for _, itemID := range lockIDs {
		var locked uuid.UUID
		if err := tx.GetContext(ctx, &locked, `
			SELECT id
			  FROM items
			 WHERE id=$1
			 FOR UPDATE
		`, itemID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.NewConflict(fmt.Sprintf("component item %s no longer exists", itemID))
			}
			return nil, fmt.Errorf("lock component %s: %w", itemID, err)
		}
	}

	// 4) After ALL item locks have been obtained, calculate balances from the
	// committed ledger. A competing release cannot pass the same item lock until
	// this transaction commits/rolls back, so it will see our RESERVE rows.
	balances := make(map[uuid.UUID]float64, len(requirements))
	codes := make(map[uuid.UUID]string, len(requirements))
	for _, line := range snapshotLines {
		codes[line.ChildID] = line.ChildCode
	}
	for _, req := range requirements {
		var b domain.StockBalance
		err := tx.GetContext(ctx, &b, `
			SELECT item_id, code, name, on_hand, reserved
			  FROM v_stock_balance
			 WHERE item_id=$1
		`, req.ChildID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				balances[req.ChildID] = 0
				continue
			}
			return nil, err
		}
		balances[req.ChildID] = b.Available()
		if codes[req.ChildID] == "" {
			codes[req.ChildID] = b.Code
		}
	}

	lines := CalcReservation(requirements, balances, codes)

	var shortages []ReservationLine
	for _, l := range lines {
		if !l.Sufficient {
			shortages = append(shortages, l)
		}
	}
	if len(shortages) > 0 {
		return nil, domain.NewConflict(
			fmt.Sprintf("insufficient stock for %d component(s); first: %s (need %.2f, have %.2f)",
				len(shortages), shortages[0].ChildCode,
				shortages[0].Required, shortages[0].Available))
	}

	// 5) Reservation posting and WO state transition are atomic. The partial
	// unique index added by migration 0016 is defense-in-depth against accidental
	// duplicate RESERVE rows for the same WO/component.
	now := snapshotAt
	refDoc := "WO:" + wo.OrderNo
	for _, l := range lines {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO inventory_txns (id, item_id, quantity, txn_type, ref_doc, occurred_at)
			VALUES ($1, $2, $3, 'RESERVE', $4, $5)
			ON CONFLICT DO NOTHING
		`, uuid.New(), l.ChildID, l.Required, refDoc, now)
		if err != nil {
			return nil, fmt.Errorf("reserve %s: %w", l.ChildCode, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("reserve %s rows affected: %w", l.ChildCode, err)
		}
		if n != 1 {
			return nil, domain.NewConflict(
				fmt.Sprintf("WO %s already has a reservation for component %s", wo.OrderNo, l.ChildCode))
		}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE work_orders
		   SET status='RELEASED', released_at=$1
		 WHERE id=$2 AND status='PLANNED'
	`, now, woID)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, domain.NewConflict("work order status changed while releasing")
	}

	// Shop Floor: routing operations are copied in the SAME transaction. The
	// first routing step becomes READY. Successors remain PENDING until either
	// the predecessor completes or its snapshotted transfer-batch quantity is
	// available. A routing-copy failure rolls back
	// reservations and WO status as well.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wo_operations
		  (id, wo_id, seq_no, work_center_id, description,
		   planned_setup_min, planned_run_per_unit, routing_operation_id,
		   setup_family, overlap_enabled, transfer_batch_qty, machines_required, workers_required, status)
		SELECT gen_random_uuid(), $1, x.seq_no, x.work_center_id, x.description,
		       x.setup_minutes, x.run_minutes_per_unit, x.id,
		       x.setup_family, x.overlap_enabled, x.transfer_batch_qty, x.machines_required, x.workers_required,
		       CASE WHEN x.rn=1 THEN 'READY' ELSE 'PENDING' END
		  FROM (
		    SELECT ro.*, row_number() OVER (ORDER BY ro.seq_no) AS rn
		      FROM routing_operations ro
		      JOIN routings r ON r.id=ro.routing_id
		      JOIN work_orders w ON w.item_id=r.item_id
		     WHERE w.id=$1 AND r.is_active=true
		  ) x
		ON CONFLICT (wo_id, seq_no) DO NOTHING
	`, woID); err != nil {
		return nil, fmt.Errorf("create wo_operations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO wo_operation_alternatives
		  (id,wo_operation_id,work_center_id,priority,run_time_multiplier,setup_time_multiplier,source)
		SELECT gen_random_uuid(), woop.id, a.work_center_id, a.priority,
		       a.run_time_multiplier, a.setup_time_multiplier, 'RELEASE_SNAPSHOT'
		  FROM wo_operations woop
		  JOIN routing_operation_alternatives a ON a.routing_operation_id=woop.routing_operation_id
		 WHERE woop.wo_id=$1 AND a.is_active=true
		ON CONFLICT (wo_operation_id,work_center_id) DO NOTHING
	`, woID); err != nil {
		return nil, fmt.Errorf("snapshot wo operation alternatives: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ReleaseResult{
		WorkOrderID:   woID,
		OrderNo:       wo.OrderNo,
		BOMSnapshotID: snapshotID,
		BOMSnapshotAt: snapshotAt,
		Reservations:  lines,
	}, nil
}

// BOMSnapshotResult is the auditable released-BOM representation for one WO.
type BOMSnapshotResult struct {
	Snapshot BOMSnapshot       `json:"snapshot"`
	Lines    []BOMSnapshotLine `json:"lines"`
}

// GetWorkOrderBOMSnapshot returns the frozen BOM used by release/completion.
func (s *WorkflowService) GetWorkOrderBOMSnapshot(ctx context.Context, woID uuid.UUID) (*BOMSnapshotResult, error) {
	var snap BOMSnapshot
	if err := s.db.GetContext(ctx, &snap, `
		SELECT id, work_order_id, parent_item_id, captured_at, source, notes
		  FROM work_order_bom_snapshots
		 WHERE work_order_id=$1
	`, woID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("work order BOM snapshot")
		}
		return nil, err
	}
	var lines []BOMSnapshotLine
	if err := s.db.SelectContext(ctx, &lines, `
		SELECT id, snapshot_id, line_no, source_bom_component_id,
		       child_item_id, child_code, child_name, child_uom,
		       quantity_per, scrap_pct, required_qty, standard_cost_snapshot
		  FROM work_order_bom_snapshot_lines
		 WHERE snapshot_id=$1
		 ORDER BY line_no
	`, snap.ID); err != nil {
		return nil, err
	}
	return &BOMSnapshotResult{Snapshot: snap, Lines: lines}, nil
}

// SortedUniqueRequirementIDs returns component IDs in a stable lock order.
// PostgreSQL row locks are taken in this order to avoid A->B / B->A deadlocks
// when two work orders share multiple components.
func SortedUniqueRequirementIDs(requirements []ComponentRequirement) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(requirements))
	ids := make([]uuid.UUID, 0, len(requirements))
	for _, req := range requirements {
		if req.ChildID == uuid.Nil {
			continue
		}
		if _, ok := seen[req.ChildID]; ok {
			continue
		}
		seen[req.ChildID] = struct{}{}
		ids = append(ids, req.ChildID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

// --------------------------------------------------------------------
// 2. Receive Purchase Order
// --------------------------------------------------------------------

type PurchaseReceiptActor struct {
	UserID   uuid.UUID
	Username string
}

func (a PurchaseReceiptActor) validate() error {
	if a.UserID == uuid.Nil || a.Username == "" {
		return domain.NewUnauthorized("authenticated receiving user is required")
	}
	return nil
}

type ReceiveResult struct {
	ReceiptID       uuid.UUID `json:"receiptId"`
	PurchaseOrderID uuid.UUID `json:"purchaseOrderId"`
	PONo            string    `json:"poNo"`
	LotID           uuid.UUID `json:"lotId"`
	LotNo           string    `json:"lotNo"`
	InventoryTxnID  uuid.UUID `json:"inventoryTxnId"`
	Quantity        float64   `json:"quantity"`
	OrderedQty      float64   `json:"orderedQty"`
	ReceivedQty     float64   `json:"receivedQty"`
	RemainingQty    float64   `json:"remainingQty"`
	Status          string    `json:"status"`
	ReceivedAt      time.Time `json:"receivedAt"`
	ReceivedBy      string    `json:"receivedBy"`
	IdempotentHit   bool      `json:"idempotentHit"`
}

// ReceivePurchase posts one partial/full PO receipt. receiptID is the idempotency
// key: retrying the exact same request returns the existing result without moving
// inventory a second time. The PO row is locked before remaining quantity is checked,
// so concurrent receipts cannot over-receive the order.
func (s *WorkflowService) ReceivePurchase(
	ctx context.Context,
	poID uuid.UUID,
	receiptID uuid.UUID,
	quantity float64,
	lotNo string,
	actor PurchaseReceiptActor,
) (*ReceiveResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if receiptID == uuid.Nil {
		return nil, domain.NewBadRequest("receiptId is required", nil)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// A globally identical receiptId is serialized even if a buggy client reuses it
	// against another PO. The PO row lock then serializes all receipts for one order.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, receiptID.String()); err != nil {
		return nil, fmt.Errorf("lock receipt id: %w", err)
	}

	var po domain.PurchaseOrder
	if err := tx.GetContext(ctx, &po, `SELECT * FROM purchase_orders WHERE id=$1 FOR UPDATE`, poID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("purchase order")
		}
		return nil, err
	}

	var supplierQualityStatus string
	err = tx.GetContext(ctx, &supplierQualityStatus, `SELECT status FROM supplier_quality_profiles WHERE supplier_name=btrim($1)`, po.Supplier)
	if err == nil && supplierQualityStatus == "BLOCKED" {
		return nil, domain.NewConflict("supplier is BLOCKED by Supplier Quality")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// Idempotency is checked after both locks. A concurrent first request will have
	// committed before this query is allowed to proceed under READ COMMITTED.
	var existing domain.PurchaseReceipt
	err = tx.GetContext(ctx, &existing, `
		SELECT pr.id, pr.purchase_order_id, po.po_no, pr.item_id, pr.quantity,
		       pr.lot_id, l.lot_no, pr.inventory_txn_id, pr.received_at,
		       pr.received_by_user_id, pr.received_by_username, pr.source
		  FROM purchase_receipts pr
		  JOIN purchase_orders po ON po.id=pr.purchase_order_id
		  JOIN lots l ON l.id=pr.lot_id
		 WHERE pr.id=$1
	`, receiptID)
	if err == nil {
		if existing.PurchaseOrderID != poID {
			return nil, domain.NewConflict("receiptId is already used by another purchase order")
		}
		if abs(existing.Quantity-quantity) > purchaseQtyEpsilon {
			return nil, domain.NewConflict("receiptId retry quantity does not match the original receipt")
		}
		if lotNo != "" && existing.LotNo != lotNo {
			return nil, domain.NewConflict("receiptId retry lotNo does not match the original receipt")
		}
		remaining := po.Quantity - po.ReceivedQty
		if remaining < purchaseQtyEpsilon {
			remaining = 0
		}
		return &ReceiveResult{
			ReceiptID: receiptID, PurchaseOrderID: poID, PONo: po.PONo,
			LotID: existing.LotID, LotNo: existing.LotNo, InventoryTxnID: existing.InventoryTxnID,
			Quantity: existing.Quantity, OrderedQty: po.Quantity, ReceivedQty: po.ReceivedQty,
			RemainingQty: remaining, Status: po.Status, ReceivedAt: existing.ReceivedAt,
			ReceivedBy: existing.ReceivedByUsername, IdempotentHit: true,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if po.Status != "OPEN" && po.Status != "PARTIALLY_RECEIVED" {
		return nil, domain.NewConflict(
			fmt.Sprintf("PO cannot receive in status %s", po.Status))
	}
	state, err := CalcPurchaseReceiptState(po.Quantity, po.ReceivedQty, quantity)
	if err != nil {
		return nil, domain.NewConflict(err.Error())
	}

	now := time.Now()
	if lotNo == "" {
		shortID := receiptID.String()
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		lotNo = fmt.Sprintf("PO-%s-%s-%s", now.Format("20060102"), po.PONo, shortID)
	}
	ref := fmt.Sprintf("PO:%s:RCPT:%s", po.PONo, receiptID.String())

	// Physical item ledger + lot allocation are one atomic operation.
	invRes, err := s.ledger.PostTx(ctx, tx, PhysicalInventoryRequest{
		ItemID: po.ItemID, Quantity: quantity, TxnType: "RECEIPT",
		RefDoc: ref, OccurredAt: now, LotNo: lotNo, Supplier: po.Supplier,
		SourceDoc: ref, Notes: "PO 部分入荷で自動生成", MovementType: "RECEIPT",
	})
	if err != nil {
		return nil, fmt.Errorf("post purchase receipt: %w", err)
	}
	if len(invRes.Allocations) != 1 {
		return nil, fmt.Errorf("purchase receipt expected one lot allocation, got %d", len(invRes.Allocations))
	}
	lotID := invRes.Allocations[0].LotID

	// Immutable receipt history links the business event to the unified ledger.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO purchase_receipts(
			id, purchase_order_id, item_id, quantity, lot_id, inventory_txn_id,
			received_at, received_by_user_id, received_by_username, source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'API')
	`, receiptID, poID, po.ItemID, quantity, lotID, invRes.Txn.ID,
		now, actor.UserID, actor.Username); err != nil {
		return nil, fmt.Errorf("insert purchase receipt history: %w", err)
	}

	// received_at / received_lot_id remain compatibility fields and represent the
	// latest receipt. received_qty and status are the planning/execution state.
	if _, err := tx.ExecContext(ctx, `
		UPDATE purchase_orders
		   SET received_qty=$1, status=$2, received_at=$3, received_lot_id=$4
		 WHERE id=$5
	`, state.NewReceived, state.Status, now, lotID, poID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ReceiveResult{
		ReceiptID: receiptID, PurchaseOrderID: poID, PONo: po.PONo,
		LotID: lotID, LotNo: lotNo, InventoryTxnID: invRes.Txn.ID,
		Quantity: quantity, OrderedQty: po.Quantity, ReceivedQty: state.NewReceived,
		RemainingQty: state.Remaining, Status: state.Status, ReceivedAt: now,
		ReceivedBy: actor.Username, IdempotentHit: false,
	}, nil
}

// --------------------------------------------------------------------
// 3. Complete Work Order
// --------------------------------------------------------------------

type CompletionLine struct {
	ChildID   uuid.UUID `json:"childId"`
	ChildCode string    `json:"childCode"`
	Quantity  float64   `json:"quantity"`
	LotID     uuid.UUID `json:"lotId"`
	LotNo     string    `json:"lotNo"`
}

type CompletionResult struct {
	CompletionID               uuid.UUID        `json:"completionId"`
	WorkOrderID                uuid.UUID        `json:"workOrderId"`
	OrderNo                    string           `json:"orderNo"`
	BOMSnapshotID              uuid.UUID        `json:"bomSnapshotId"`
	BOMSnapshotAt              time.Time        `json:"bomSnapshotAt"`
	CompletedNow               float64          `json:"completedNow"`
	CompletedQty               float64          `json:"completedQty"`
	PlannedQty                 float64          `json:"plannedQty"`
	RemainingQty               float64          `json:"remainingQty"`
	Status                     string           `json:"status"`
	FinalOperationSeqNo        int              `json:"finalOperationSeqNo"`
	FinalOperationCompletedQty float64          `json:"finalOperationCompletedQty"`
	FinalOperationAvailableQty float64          `json:"finalOperationAvailableQty"`
	ConsumedLots               []CompletionLine `json:"consumedLots"`
	ProducedLot                CompletionLine   `json:"producedLot"`
	IdempotentHit              bool             `json:"idempotentHit"`
}

type completionRecord struct {
	ID            uuid.UUID  `db:"id"`
	WorkOrderID   uuid.UUID  `db:"work_order_id"`
	Quantity      float64    `db:"quantity"`
	ProducedLotID uuid.UUID  `db:"produced_lot_id"`
	BOMSnapshotID *uuid.UUID `db:"bom_snapshot_id"`
}

// CompleteWorkOrder records one physical completion batch.
// completionQty is the quantity completed in THIS call, not a cumulative value.
// When completionQty is nil (legacy client), the remaining WO quantity is completed.
// completionID provides idempotency: retrying the same request ID never creates a
// second material issue or finished-goods receipt.
func (s *WorkflowService) CompleteWorkOrder(
	ctx context.Context,
	woID uuid.UUID,
	completionID uuid.UUID,
	completionQty *float64,
	lotNo string,
) (*CompletionResult, error) {
	if completionID == uuid.Nil {
		completionID = uuid.New()
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Serialize completion calls for the same WO. This prevents two simultaneous
	// 20-unit reports from both reading the same completed_qty.
	var wo domain.WorkOrder
	if err := tx.GetContext(ctx, &wo, `SELECT * FROM work_orders WHERE id=$1 FOR UPDATE`, woID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("work order")
		}
		return nil, err
	}

	// Idempotent retry: if this completion ID was already committed, return the
	// recorded result without moving inventory again.
	var existing completionRecord
	err = tx.GetContext(ctx, &existing, `
		SELECT id, work_order_id, quantity, produced_lot_id, bom_snapshot_id
		  FROM work_order_completions
		 WHERE id=$1
	`, completionID)
	if err == nil {
		if existing.WorkOrderID != woID {
			return nil, domain.NewConflict("completionId already belongs to another work order")
		}
		if completionQty != nil {
			d := existing.Quantity - *completionQty
			if d > 1e-9 || d < -1e-9 {
				return nil, domain.NewConflict("completionId was already used with a different quantity")
			}
		}
		res, err := s.loadCompletionResultTx(ctx, tx, &wo, existing, true)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return res, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if wo.Status != "RELEASED" && wo.Status != "IN_PROGRESS" {
		return nil, domain.NewConflict(
			fmt.Sprintf("WO must be RELEASED or IN_PROGRESS (current: %s)", wo.Status))
	}

	qty, newCompleted, remainingAfter, newStatus, calcErr :=
		CalcCompletionBatch(wo.Quantity, wo.CompletedQty, completionQty)
	if calcErr != nil {
		return nil, domain.NewBadRequest(calcErr.Error(), nil)
	}

	// Finished-goods receipts are gated by the actual cumulative good quantity at
	// the final routing operation. Lock the final operation after the WO row, using
	// the same lock order as ShopFloorService.Complete, so concurrent operation
	// reporting and WO receipt cannot race past each other.
	finalOp, err := lockFinalOperationTx(ctx, tx, wo.ID)
	if err != nil {
		return nil, err
	}
	if finalOp.CompletedQty <= quantityEpsilon {
		return nil, domain.NewConflict("final operation has no confirmed Shop Floor good-quantity actual")
	}
	if err := ValidateFinishedGoodsAgainstFinalOperation(
		wo.Quantity, wo.CompletedQty, finalOp.CompletedQty, qty,
	); err != nil {
		return nil, domain.NewConflict(err.Error())
	}

	// Parent item is read inside the same transaction for a consistent response.
	var parent domain.Item
	if err := tx.GetContext(ctx, &parent, `SELECT * FROM items WHERE id=$1`, wo.ItemID); err != nil {
		return nil, err
	}

	// Load the immutable BOM snapshot created at Release.  Completion must never
	// consult the live bom_components table: an ECO after release must affect only
	// future WOs, not this one.
	var snapshot BOMSnapshot
	if err := tx.GetContext(ctx, &snapshot, `
		SELECT id, work_order_id, parent_item_id, captured_at, source, notes
		  FROM work_order_bom_snapshots
		 WHERE work_order_id=$1
	`, wo.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewConflict(
				"WO has no release-time BOM snapshot; do not complete it until migration/backfill is reconciled")
		}
		return nil, err
	}
	if snapshot.ParentItemID != wo.ItemID {
		return nil, domain.NewConflict("WO BOM snapshot parent item does not match the work order item")
	}

	var snapshotLines []BOMSnapshotLine
	if err := tx.SelectContext(ctx, &snapshotLines, `
		SELECT id, snapshot_id, line_no, source_bom_component_id,
		       child_item_id, child_code, child_name, child_uom,
		       quantity_per, scrap_pct, required_qty, standard_cost_snapshot
		  FROM work_order_bom_snapshot_lines
		 WHERE snapshot_id=$1
		 ORDER BY line_no
	`, snapshot.ID); err != nil {
		return nil, err
	}
	requirements := SnapshotRequirements(snapshotLines, qty)
	snapshotCodes := make(map[uuid.UUID]string, len(snapshotLines))
	for _, line := range snapshotLines {
		snapshotCodes[line.ChildID] = line.ChildCode
	}

	now := time.Now()
	reserveRef := "WO:" + wo.OrderNo
	completionRef := fmt.Sprintf("WO:%s:COMP:%s", wo.OrderNo, completionID.String())
	consumed := make([]CompletionLine, 0, len(requirements))

	for _, req := range requirements {
		// Ensure the release reservation still covers this partial completion.
		// This also detects WOs whose status was manually changed to RELEASED
		// without using the release workflow.
		var reserved float64
		if err := tx.GetContext(ctx, &reserved, `
			SELECT COALESCE(SUM(CASE
			  WHEN txn_type='RESERVE' THEN ABS(quantity)
			  WHEN txn_type='UNRESERVE' THEN -ABS(quantity)
			  ELSE 0 END), 0)
			  FROM inventory_txns
			 WHERE item_id=$1 AND ref_doc=$2
		`, req.ChildID, reserveRef); err != nil {
			return nil, err
		}
		if reserved+1e-9 < req.Required {
			childCode := snapshotCodes[req.ChildID]
			if childCode == "" {
				childCode = req.ChildID.String()
			}
			return nil, domain.NewConflict(fmt.Sprintf(
				"insufficient WO reservation for snapshotted component %s: need %.2f, reserved %.2f",
				childCode, req.Required, reserved))
		}

		childCode := snapshotCodes[req.ChildID]
		if childCode == "" {
			childCode = req.ChildID.String()
		}

		// Unified ledger owns FIFO selection and locking. It always locks the item
		// row before lot rows, keeping the lock order consistent with manual stock
		// movements and preventing item<->lot deadlocks.
		issueRes, err := s.ledger.PostTx(ctx, tx, PhysicalInventoryRequest{
			ItemID: req.ChildID, Quantity: -req.Required, TxnType: "ISSUE",
			RefDoc: completionRef, OccurredAt: now, MovementType: "CONSUMED",
		})
		if err != nil {
			return nil, fmt.Errorf("issue component through inventory ledger: %w", err)
		}

		// Release only the reservation associated with the successfully allocated
		// physical issue. Both operations are still in this same DB transaction.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inventory_txns (id, item_id, quantity, txn_type, ref_doc, occurred_at)
			VALUES ($1, $2, $3, 'UNRESERVE', $4, $5)
		`, uuid.New(), req.ChildID, req.Required, reserveRef, now); err != nil {
			return nil, fmt.Errorf("unreserve: %w", err)
		}

		for _, mv := range issueRes.Allocations {
			var lotNoUsed string
			if err := tx.GetContext(ctx, &lotNoUsed, `SELECT lot_no FROM lots WHERE id=$1`, mv.LotID); err != nil {
				return nil, err
			}
			consumed = append(consumed, CompletionLine{
				ChildID: req.ChildID, ChildCode: childCode,
				Quantity: -mv.Quantity, LotID: mv.LotID, LotNo: lotNoUsed,
			})
		}

	}

	sort.Slice(consumed, func(i, j int) bool {
		if consumed[i].ChildCode != consumed[j].ChildCode {
			return consumed[i].ChildCode < consumed[j].ChildCode
		}
		return consumed[i].LotNo < consumed[j].LotNo
	})

	// Each partial completion may create a separate lot. If the operator supplies
	// the same lot number again for this WO, append the new quantity to that lot.
	if lotNo == "" {
		lotNo = fmt.Sprintf("WO-%s-%s-%s", now.Format("20060102"), wo.OrderNo, completionID.String()[:8])
	}
	// Produce the finished quantity through the same unified ledger. The ledger
	// locks item -> lot in a consistent order and refuses to append to a lot that
	// belongs to a different WO source document.
	parentRes, err := s.ledger.PostTx(ctx, tx, PhysicalInventoryRequest{
		ItemID: wo.ItemID, Quantity: qty, TxnType: "RECEIPT",
		RefDoc: completionRef, OccurredAt: now, LotNo: lotNo,
		Supplier: "INTERNAL", SourceDoc: reserveRef, Notes: "WO 部分完成で自動生成",
		MovementType: "PRODUCED", RequireSourceDocMatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("parent receipt through inventory ledger: %w", err)
	}
	if len(parentRes.Allocations) != 1 {
		return nil, fmt.Errorf("WO receipt expected one lot allocation, got %d", len(parentRes.Allocations))
	}
	parentLotID := parentRes.Allocations[0].LotID

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_order_completions
		  (id, work_order_id, quantity, produced_lot_id, completed_at, bom_snapshot_id, receipt_txn_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, completionID, woID, qty, parentLotID, now, snapshot.ID, parentRes.Txn.ID); err != nil {
		return nil, fmt.Errorf("record WO completion: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE work_orders
		   SET completed_qty=$1,
		       reported_progress_qty=GREATEST(reported_progress_qty, $1),
		       status=$2,
		       completed_at=CASE WHEN $2='COMPLETED' THEN $3 ELSE NULL END,
		       produced_lot_id=$4
		 WHERE id=$5
	`, newCompleted, newStatus, now, parentLotID, woID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &CompletionResult{
		CompletionID:               completionID,
		WorkOrderID:                woID,
		OrderNo:                    wo.OrderNo,
		BOMSnapshotID:              snapshot.ID,
		BOMSnapshotAt:              snapshot.CapturedAt,
		CompletedNow:               qty,
		CompletedQty:               newCompleted,
		PlannedQty:                 wo.Quantity,
		RemainingQty:               remainingAfter,
		Status:                     newStatus,
		FinalOperationSeqNo:        finalOp.SeqNo,
		FinalOperationCompletedQty: finalOp.CompletedQty,
		FinalOperationAvailableQty: maxFloat(0, finalOp.CompletedQty-newCompleted),
		ConsumedLots:               consumed,
		ProducedLot: CompletionLine{
			ChildID: parent.ID, ChildCode: parent.Code,
			Quantity: qty, LotID: parentLotID, LotNo: lotNo,
		},
	}, nil
}

func (s *WorkflowService) loadCompletionResultTx(
	ctx context.Context,
	tx *sqlx.Tx,
	wo *domain.WorkOrder,
	rec completionRecord,
	idempotent bool,
) (*CompletionResult, error) {
	ref := fmt.Sprintf("WO:%s:COMP:%s", wo.OrderNo, rec.ID.String())
	var consumed []CompletionLine
	if err := tx.SelectContext(ctx, &consumed, `
		SELECT i.id AS child_id, COALESCE(bl.child_code, i.code) AS child_code,
		       ABS(lm.quantity) AS quantity, l.id AS lot_id, l.lot_no
		  FROM lot_movements lm
		  JOIN lots l  ON l.id = lm.lot_id
		  JOIN items i ON i.id = l.item_id
		  LEFT JOIN work_order_bom_snapshots bs ON bs.work_order_id=$2
		  LEFT JOIN work_order_bom_snapshot_lines bl
		    ON bl.snapshot_id=bs.id AND bl.child_item_id=l.item_id
		 WHERE lm.ref_doc=$1 AND lm.movement_type='CONSUMED'
		 ORDER BY child_code, l.lot_no
	`, ref, wo.ID); err != nil {
		return nil, err
	}

	var produced struct {
		LotID uuid.UUID `db:"lot_id"`
		LotNo string    `db:"lot_no"`
		Code  string    `db:"code"`
	}
	if err := tx.GetContext(ctx, &produced, `
		SELECT l.id AS lot_id, l.lot_no, i.code
		  FROM lots l JOIN items i ON i.id=l.item_id
		 WHERE l.id=$1
	`, rec.ProducedLotID); err != nil {
		return nil, err
	}

	// Refresh cumulative state because later completion batches may have occurred.
	var current domain.WorkOrder
	if err := tx.GetContext(ctx, &current, `SELECT * FROM work_orders WHERE id=$1`, wo.ID); err != nil {
		return nil, err
	}
	var snapshot BOMSnapshot
	if err := tx.GetContext(ctx, &snapshot, `
		SELECT id, work_order_id, parent_item_id, captured_at, source, notes
		  FROM work_order_bom_snapshots
		 WHERE work_order_id=$1
	`, wo.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewConflict("WO completion exists but its BOM snapshot is missing")
		}
		return nil, err
	}
	if rec.BOMSnapshotID != nil && *rec.BOMSnapshotID != snapshot.ID {
		return nil, domain.NewConflict("WO completion references a different BOM snapshot than the work order")
	}
	remaining := current.Quantity - current.CompletedQty
	if remaining < 1e-9 {
		remaining = 0
	}
	finalOp, err := getFinalOperationTx(ctx, tx, current.ID)
	if err != nil {
		return nil, err
	}
	return &CompletionResult{
		CompletionID:               rec.ID,
		WorkOrderID:                current.ID,
		OrderNo:                    current.OrderNo,
		BOMSnapshotID:              snapshot.ID,
		BOMSnapshotAt:              snapshot.CapturedAt,
		CompletedNow:               rec.Quantity,
		CompletedQty:               current.CompletedQty,
		PlannedQty:                 current.Quantity,
		RemainingQty:               remaining,
		Status:                     current.Status,
		FinalOperationSeqNo:        finalOp.SeqNo,
		FinalOperationCompletedQty: finalOp.CompletedQty,
		FinalOperationAvailableQty: maxFloat(0, finalOp.CompletedQty-current.CompletedQty),
		ConsumedLots:               consumed,
		ProducedLot: CompletionLine{
			ChildID: current.ItemID, ChildCode: produced.Code,
			Quantity: rec.Quantity, LotID: produced.LotID, LotNo: produced.LotNo,
		},
		IdempotentHit: idempotent,
	}, nil
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

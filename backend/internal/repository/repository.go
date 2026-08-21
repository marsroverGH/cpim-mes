package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

func NewDB(url string) (*sqlx.DB, error) {
	db, err := sqlx.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

type Repositories struct {
	Items       *ItemRepo
	BOM         *BOMRepo
	Demand      *DemandRepo
	MPS         *MPSRepo
	Inventory   *InventoryRepo
	WorkOrders  *WorkOrderRepo
	Purchases   *PurchaseRepo
	WorkCenters *WorkCenterRepo
	Routings    *RoutingRepo
	Users       *UserRepo
	Lots        *LotRepo
	Audit       *AuditRepo
	CycleCounts *CycleCountRepo
	Calendars   *CalendarRepo
	Quality     *QualityRepo
	ShopFloor   *ShopFloorRepo
	SOP         *SOPRepo
	ECO         *ECORepo
}

func NewRepositories(db *sqlx.DB) *Repositories {
	return &Repositories{
		Items:       &ItemRepo{db: db},
		BOM:         &BOMRepo{db: db},
		Demand:      &DemandRepo{db: db},
		MPS:         &MPSRepo{db: db},
		Inventory:   &InventoryRepo{db: db},
		WorkOrders:  &WorkOrderRepo{db: db},
		Purchases:   &PurchaseRepo{db: db},
		WorkCenters: &WorkCenterRepo{db: db},
		Routings:    &RoutingRepo{db: db},
		Users:       &UserRepo{db: db},
		Lots:        &LotRepo{db: db},
		Audit:       &AuditRepo{db: db},
		CycleCounts: &CycleCountRepo{db: db},
		Calendars:   &CalendarRepo{db: db},
		Quality:     &QualityRepo{db: db},
		ShopFloor:   &ShopFloorRepo{db: db},
		SOP:         &SOPRepo{db: db},
		ECO:         &ECORepo{db: db},
	}
}

// ==================== Item ====================

type ItemRepo struct{ db *sqlx.DB }

func (r *ItemRepo) List(ctx context.Context) ([]domain.Item, error) {
	var rows []domain.Item
	err := r.db.SelectContext(ctx, &rows, `SELECT * FROM items ORDER BY code`)
	return rows, err
}

func (r *ItemRepo) Get(ctx context.Context, id uuid.UUID) (*domain.Item, error) {
	var x domain.Item
	err := r.db.GetContext(ctx, &x, `SELECT * FROM items WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *ItemRepo) Create(ctx context.Context, it *domain.Item) error {
	if it.ID == uuid.Nil {
		it.ID = uuid.New()
	}
	now := time.Now()
	it.CreatedAt, it.UpdatedAt = now, now
	if it.LotSizeMethod == "" {
		it.LotSizeMethod = "LFL"
	}
	if it.PoqPeriods <= 0 {
		it.PoqPeriods = 1
	}
	if it.HoldingCostPct <= 0 {
		it.HoldingCostPct = 0.20
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO items (id, code, name, type, uom, lead_time_days, safety_stock, lot_size, standard_cost,
		                   lot_size_method, poq_periods, ordering_cost, holding_cost_pct, group_id,
		                   created_at, updated_at)
		VALUES (:id, :code, :name, :type, :uom, :lead_time_days, :safety_stock, :lot_size, :standard_cost,
		        :lot_size_method, :poq_periods, :ordering_cost, :holding_cost_pct, :group_id,
		        :created_at, :updated_at)
	`, it)
	return err
}

func (r *ItemRepo) Update(ctx context.Context, it *domain.Item) error {
	it.UpdatedAt = time.Now()
	if it.LotSizeMethod == "" {
		it.LotSizeMethod = "LFL"
	}
	if it.PoqPeriods <= 0 {
		it.PoqPeriods = 1
	}
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE items SET code=:code, name=:name, type=:type, uom=:uom,
		    lead_time_days=:lead_time_days, safety_stock=:safety_stock,
		    lot_size=:lot_size, standard_cost=:standard_cost,
		    lot_size_method=:lot_size_method, poq_periods=:poq_periods,
		    ordering_cost=:ordering_cost, holding_cost_pct=:holding_cost_pct,
		    group_id=:group_id, updated_at=:updated_at
		WHERE id=:id
	`, it)
	return err
}

func (r *ItemRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM items WHERE id=$1`, id)
	return err
}

// RecomputeLLC — 全品目の low_level_code を再計算 (BOM 変更後に呼び出す)
func (r *ItemRepo) RecomputeLLC(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `SELECT recompute_low_level_codes()`)
	return err
}

// ==================== BOM ====================

type BOMRepo struct{ db *sqlx.DB }

func (r *BOMRepo) ComponentsOf(ctx context.Context, parentID uuid.UUID) ([]domain.BOMComponent, error) {
	var rows []domain.BOMComponent
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM bom_components WHERE parent_id=$1`, parentID)
	return rows, err
}

// BOM writes are intentionally owned by service.BOMService so topology mutation,
// cycle validation and LLC recomputation cannot be separated. Repository exposes reads only.

// AllEdges — 全 BOM 構成行を取得 (Cost Rollup などで一括処理する用)
func (r *BOMRepo) AllEdges(ctx context.Context) ([]domain.BOMComponent, error) {
	var rows []domain.BOMComponent
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, parent_id, child_id, quantity, scrap_pct FROM bom_components`)
	return rows, err
}

// 再帰CTEで多段BOM展開 — CPIM の MRP explosion で利用
type ExplodedRow struct {
	Level     int       `db:"level"     json:"level"`
	ChildID   uuid.UUID `db:"child_id"  json:"childId"`
	ChildCode string    `db:"child_code" json:"childCode"`
	ChildName string    `db:"child_name" json:"childName"`
	TotalQty  float64   `db:"total_qty" json:"totalQuantity"`
}

func (r *BOMRepo) Explode(ctx context.Context, parentID uuid.UUID, qty float64) ([]ExplodedRow, error) {
	q := `
WITH RECURSIVE bom_tree AS (
  SELECT 1 AS level, b.child_id,
         b.quantity * (1 + b.scrap_pct) * $2 AS total_qty
    FROM bom_components b
   WHERE b.parent_id = $1
  UNION ALL
  SELECT t.level + 1, b.child_id,
         b.quantity * (1 + b.scrap_pct) * t.total_qty
    FROM bom_components b
    JOIN bom_tree t ON t.child_id = b.parent_id
)
SELECT t.level, t.child_id, i.code AS child_code, i.name AS child_name,
       SUM(t.total_qty) AS total_qty
  FROM bom_tree t JOIN items i ON i.id = t.child_id
 GROUP BY t.level, t.child_id, i.code, i.name
 ORDER BY t.level, child_code;
`
	var rows []ExplodedRow
	err := r.db.SelectContext(ctx, &rows, q, parentID, qty)
	return rows, err
}

// ==================== Demand ====================

type DemandRepo struct{ db *sqlx.DB }

func (r *DemandRepo) List(ctx context.Context) ([]domain.DemandForecast, error) {
	var rows []domain.DemandForecast
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM demand_forecasts ORDER BY due_date`)
	return rows, err
}

func (r *DemandRepo) Create(ctx context.Context, d *domain.DemandForecast) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.CreatedAt = time.Now()
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO demand_forecasts (id, item_id, due_date, quantity, source, created_at)
		VALUES (:id, :item_id, :due_date, :quantity, :source, :created_at)
	`, d)
	return err
}

// ==================== MPS ====================

type MPSRepo struct{ db *sqlx.DB }

func (r *MPSRepo) List(ctx context.Context) ([]domain.MPSEntry, error) {
	var rows []domain.MPSEntry
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM mps_entries ORDER BY period, item_id`)
	return rows, err
}

func (r *MPSRepo) Upsert(ctx context.Context, m *domain.MPSEntry) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO mps_entries (id, item_id, period, planned, released, source_forecast_run_id, demand_basis,
		                         source_sop_plan_id, source_sop_disaggregation_run_id, source_product_mix_version_id)
		VALUES (:id, :item_id, :period, :planned, :released, NULL, 'MANUAL', NULL, NULL, NULL)
		ON CONFLICT (item_id, period) DO UPDATE
		   SET planned = EXCLUDED.planned, released = EXCLUDED.released,
		       source_forecast_run_id = NULL, demand_basis = 'MANUAL',
		       source_sop_plan_id = NULL, source_sop_disaggregation_run_id = NULL, source_product_mix_version_id = NULL
	`, m)
	return err
}

// ==================== Inventory ====================

type InventoryRepo struct{ db *sqlx.DB }

func (r *InventoryRepo) Transactions(ctx context.Context, itemID uuid.UUID) ([]domain.InventoryTxn, error) {
	var rows []domain.InventoryTxn
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM inventory_txns WHERE item_id=$1 ORDER BY occurred_at DESC`, itemID)
	return rows, err
}

func (r *InventoryRepo) Post(ctx context.Context, t *domain.InventoryTxn) error {
	// Physical RECEIPT/ISSUE/ADJUST must never be posted without mandatory lot
	// allocations. Those writes belong to service.InventoryLedgerService.
	if t.TxnType != "RESERVE" && t.TxnType != "UNRESERVE" {
		return fmt.Errorf("physical inventory writes must use InventoryLedgerService")
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.OccurredAt.IsZero() {
		t.OccurredAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO inventory_txns (id, item_id, quantity, txn_type, ref_doc, occurred_at)
		VALUES (:id, :item_id, :quantity, :txn_type, :ref_doc, :occurred_at)
	`, t)
	return err
}

type StockOnHand struct {
	ItemID   uuid.UUID `db:"item_id"  json:"itemId"`
	ItemCode string    `db:"code"     json:"itemCode"`
	ItemName string    `db:"name"     json:"itemName"`
	OnHand   float64   `db:"on_hand"  json:"onHand"`
}

// AnnualIssueUsage is the rolling 12-month physical consumption quantity used
// by CPIM-style ABC analysis. Only ISSUE transactions count as usage; receipts,
// reservations, inventory adjustments, supplier returns and scrap adjustments
// are deliberately excluded.
type AnnualIssueUsage struct {
	ItemID   uuid.UUID `db:"item_id"`
	UsageQty float64   `db:"usage_qty"`
}

// BusinessDate returns the application's business date using the same timezone
// convention as ECO effective-date processing (Asia/Tokyo fallback).
func (r *InventoryRepo) BusinessDate(ctx context.Context) (time.Time, error) {
	var d time.Time
	err := r.db.GetContext(ctx, &d, `SELECT eco_business_date(now())`)
	return d, err
}

// AnnualIssueUsage returns ISSUE quantities for the 12-calendar-month window
// ending on asOf (inclusive). The timestamp bounds are constructed in the
// configured business timezone so the partial ISSUE index on occurred_at remains
// usable and day-boundary behavior is deterministic.
func (r *InventoryRepo) AnnualIssueUsage(ctx context.Context, asOf time.Time) ([]AnnualIssueUsage, error) {
	var rows []AnnualIssueUsage
	err := r.db.SelectContext(ctx, &rows, `
SELECT i.id AS item_id,
       COALESCE(SUM(-t.quantity), 0)::double precision AS usage_qty
  FROM items i
  LEFT JOIN inventory_txns t
    ON t.item_id = i.id
   AND t.txn_type = 'ISSUE'
   AND t.occurred_at >= (((($1::date - INTERVAL '1 year') + INTERVAL '1 day')::date)::timestamp AT TIME ZONE eco_business_timezone())
   AND t.occurred_at <  ((($1::date + 1)::date)::timestamp AT TIME ZONE eco_business_timezone())
 GROUP BY i.id`, asOf.Format("2006-01-02"))
	return rows, err
}

func (r *InventoryRepo) OnHand(ctx context.Context) ([]StockOnHand, error) {
	// On-hand is lot-backed. v_stock_balance derives physical stock from
	// lot_movements; deferred DB constraints guarantee the matching item-level
	// inventory transaction has the exact same quantity.
	var rows []StockOnHand
	err := r.db.SelectContext(ctx, &rows, `
		SELECT item_id, code, name, on_hand FROM v_stock_balance ORDER BY code
	`)
	return rows, err
}

// Balance — 物理在庫 + 予約 + 利用可能在庫 (v_stock_balance ビュー使用)
func (r *InventoryRepo) Balance(ctx context.Context) ([]domain.StockBalance, error) {
	var rows []domain.StockBalance
	err := r.db.SelectContext(ctx, &rows,
		`SELECT item_id, code, name, on_hand, reserved FROM v_stock_balance ORDER BY code`)
	return rows, err
}

// BalanceFor — 1品目の在庫サマリ (在庫引当判定用)
func (r *InventoryRepo) BalanceFor(ctx context.Context, itemID uuid.UUID) (*domain.StockBalance, error) {
	var b domain.StockBalance
	err := r.db.GetContext(ctx, &b,
		`SELECT item_id, code, name, on_hand, reserved FROM v_stock_balance WHERE item_id=$1`, itemID)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

type InventoryLotReconciliation struct {
	ItemID       uuid.UUID `db:"item_id"        json:"itemId"`
	ItemCode     string    `db:"code"           json:"itemCode"`
	ItemName     string    `db:"name"           json:"itemName"`
	LedgerOnHand float64   `db:"ledger_on_hand" json:"ledgerOnHand"`
	LotOnHand    float64   `db:"lot_on_hand"    json:"lotOnHand"`
	Difference   float64   `db:"difference"     json:"difference"`
}

func (r *InventoryRepo) Reconciliation(ctx context.Context) ([]InventoryLotReconciliation, error) {
	var rows []InventoryLotReconciliation
	err := r.db.SelectContext(ctx, &rows, `
		SELECT item_id, code, name, ledger_on_hand, lot_on_hand, difference
		  FROM v_inventory_lot_reconciliation ORDER BY code
	`)
	return rows, err
}

// ==================== Work Orders ====================

type WorkOrderRepo struct{ db *sqlx.DB }

func (r *WorkOrderRepo) List(ctx context.Context) ([]domain.WorkOrder, error) {
	var rows []domain.WorkOrder
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM work_orders ORDER BY due_date`)
	return rows, err
}

func (r *WorkOrderRepo) Create(ctx context.Context, w *domain.WorkOrder) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	w.CreatedAt = time.Now()
	if w.Status == "" {
		w.Status = "PLANNED"
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO work_orders (id, order_no, item_id, quantity, start_date, due_date, status, created_at)
		VALUES (:id, :order_no, :item_id, :quantity, :start_date, :due_date, :status, :created_at)
	`, w)
	return err
}

func (r *WorkOrderRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	now := time.Now()
	switch status {
	case "RELEASED":
		_, err := r.db.ExecContext(ctx,
			`UPDATE work_orders SET status=$1, released_at=$2 WHERE id=$3`, status, now, id)
		return err
	case "COMPLETED":
		_, err := r.db.ExecContext(ctx,
			`UPDATE work_orders SET status=$1, completed_at=$2 WHERE id=$3`, status, now, id)
		return err
	default:
		_, err := r.db.ExecContext(ctx,
			`UPDATE work_orders SET status=$1 WHERE id=$2`, status, id)
		return err
	}
}

func (r *WorkOrderRepo) Get(ctx context.Context, id uuid.UUID) (*domain.WorkOrder, error) {
	var w domain.WorkOrder
	err := r.db.GetContext(ctx, &w, `SELECT * FROM work_orders WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *WorkOrderRepo) SetProducedLot(ctx context.Context, woID, lotID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE work_orders SET produced_lot_id=$1 WHERE id=$2`, lotID, woID)
	return err
}

// UpdateProgress records shop-floor reported progress only.
// It deliberately does NOT change completed_qty or inventory. Physical completion
// must go through WorkflowService.CompleteWorkOrder so ISSUE/RECEIPT stay aligned.
func (r *WorkOrderRepo) UpdateProgress(ctx context.Context, id uuid.UUID, reportedQty float64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE work_orders
		   SET reported_progress_qty = $1,
		       status = CASE
		         WHEN $1 > 0 AND status = 'RELEASED' THEN 'IN_PROGRESS'
		         ELSE status
		       END
		 WHERE id = $2
	`, reportedQty, id)
	return err
}

// ==================== Purchases ====================

type PurchaseRepo struct{ db *sqlx.DB }

func (r *PurchaseRepo) List(ctx context.Context) ([]domain.PurchaseOrder, error) {
	var rows []domain.PurchaseOrder
	err := r.db.SelectContext(ctx, &rows, `
		SELECT * FROM v_purchase_order_planning_schedule
		 ORDER BY expected_delivery_date, po_no`)
	return rows, err
}

func (r *PurchaseRepo) Create(ctx context.Context, p *domain.PurchaseOrder) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Status == "" {
		p.Status = "OPEN"
	}
	if p.OrderDate.IsZero() {
		p.OrderDate = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO purchase_orders (id, po_no, item_id, supplier, quantity, order_date, due_date, status)
		VALUES (:id, :po_no, :item_id, :supplier, :quantity, :order_date, :due_date, :status)
	`, p)
	return err
}

func (r *PurchaseRepo) Get(ctx context.Context, id uuid.UUID) (*domain.PurchaseOrder, error) {
	var p domain.PurchaseOrder
	err := r.db.GetContext(ctx, &p, `
		SELECT * FROM v_purchase_order_planning_schedule WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PurchaseRepo) ListReceipts(ctx context.Context, poID uuid.UUID) ([]domain.PurchaseReceipt, error) {
	var rows []domain.PurchaseReceipt
	err := r.db.SelectContext(ctx, &rows, `
		SELECT pr.id, pr.purchase_order_id, po.po_no, pr.item_id, pr.quantity,
		       pr.lot_id, l.lot_no, pr.inventory_txn_id, pr.received_at,
		       pr.received_by_user_id, pr.received_by_username, pr.source
		  FROM purchase_receipts pr
		  JOIN purchase_orders po ON po.id=pr.purchase_order_id
		  JOIN lots l ON l.id=pr.lot_id
		 WHERE pr.purchase_order_id=$1
		 ORDER BY pr.received_at, pr.id
	`, poID)
	return rows, err
}

func (r *PurchaseRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	if status == "RECEIVED" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE purchase_orders SET status=$1, received_at=$2 WHERE id=$3`,
			status, time.Now(), id)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE purchase_orders SET status=$1 WHERE id=$2`, status, id)
	return err
}

func (r *PurchaseRepo) SetReceivedLot(ctx context.Context, poID, lotID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE purchase_orders SET received_lot_id=$1 WHERE id=$2`, lotID, poID)
	return err
}

// EffectiveLeadTimeDays returns a conservative procurement lead time for an
// item from the latest 0035 reliability snapshot. Only sufficiently sampled,
// non-blocked suppliers participate; the item master lead time is the floor and
// fallback. MAX is intentional because CTP/MRP have no supplier-selection
// decision yet and must not promise using an optimistic supplier cherry-pick.
func (r *PurchaseRepo) EffectiveLeadTimeDays(ctx context.Context, itemID uuid.UUID, nominal int) (int, error) {
	if nominal < 0 {
		nominal = 0
	}
	var observed sql.NullInt64
	err := r.db.GetContext(ctx, &observed, `
WITH eligible AS (
  SELECT v.*,
         ROW_NUMBER() OVER (
           PARTITION BY v.supplier_name
           ORDER BY CASE WHEN v.item_id=$1 THEN 0 ELSE 1 END, v.sample_count DESC
         ) AS rn
    FROM v_current_supplier_lead_time v
   WHERE (v.item_id=$1 OR v.item_id IS NULL)
     AND v.sample_count>=v.min_samples
), supplier_choice AS (
  SELECT e.*
    FROM eligible e
    LEFT JOIN supplier_quality_profiles q ON q.supplier_name=e.supplier_name
   WHERE e.rn=1 AND COALESCE(q.status,'APPROVED')<>'BLOCKED'
)
SELECT MAX(recommended_lead_days)::bigint FROM supplier_choice`, itemID)
	if err != nil {
		return nominal, err
	}
	if observed.Valid && int(observed.Int64) > nominal {
		return int(observed.Int64), nil
	}
	return nominal, nil
}

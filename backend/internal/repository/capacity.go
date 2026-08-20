package repository

import (
	"context"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ==================== Work Centers ====================

type WorkCenterRepo struct{ db *sqlx.DB }

func (r *WorkCenterRepo) List(ctx context.Context) ([]domain.WorkCenter, error) {
	var rows []domain.WorkCenter
	err := r.db.SelectContext(ctx, &rows, `SELECT * FROM work_centers ORDER BY code`)
	return rows, err
}

func (r *WorkCenterRepo) Get(ctx context.Context, id uuid.UUID) (*domain.WorkCenter, error) {
	var x domain.WorkCenter
	err := r.db.GetContext(ctx, &x, `SELECT * FROM work_centers WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *WorkCenterRepo) Create(ctx context.Context, w *domain.WorkCenter) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO work_centers (id, code, name, capacity_minutes_per_day, efficiency, utilization,
		                          labor_rate_per_minute, overhead_rate_per_minute, calendar_id, shift_start_minute,
		                          machine_count, worker_count, created_at)
		VALUES (:id, :code, :name, :capacity_minutes_per_day, :efficiency, :utilization,
		        :labor_rate_per_minute, :overhead_rate_per_minute, :calendar_id, :shift_start_minute,
		        :machine_count, :worker_count, :created_at)
	`, w)
	return err
}

func (r *WorkCenterRepo) Update(ctx context.Context, w *domain.WorkCenter) error {
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE work_centers SET
		  code=:code, name=:name,
		  capacity_minutes_per_day=:capacity_minutes_per_day,
		  efficiency=:efficiency, utilization=:utilization,
		  labor_rate_per_minute=:labor_rate_per_minute,
		  overhead_rate_per_minute=:overhead_rate_per_minute,
		  calendar_id=:calendar_id,
		  shift_start_minute=:shift_start_minute,
		  machine_count=:machine_count,
		  worker_count=:worker_count
		WHERE id=:id
	`, w)
	return err
}

func (r *WorkCenterRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM work_centers WHERE id=$1`, id)
	return err
}

// ==================== Routings ====================

type RoutingRepo struct{ db *sqlx.DB }

func (r *RoutingRepo) ActiveForItem(ctx context.Context, itemID uuid.UUID) (*domain.Routing, error) {
	var x domain.Routing
	err := r.db.GetContext(ctx, &x,
		`SELECT * FROM routings WHERE item_id=$1 AND is_active=true LIMIT 1`, itemID)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *RoutingRepo) List(ctx context.Context) ([]domain.Routing, error) {
	var rows []domain.Routing
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM routings ORDER BY created_at DESC`)
	return rows, err
}

func (r *RoutingRepo) Create(ctx context.Context, x *domain.Routing) error {
	if x.ID == uuid.Nil {
		x.ID = uuid.New()
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO routings (id, item_id, description, is_active, created_at)
		VALUES (:id, :item_id, :description, :is_active, :created_at)
	`, x)
	return err
}

func (r *RoutingRepo) Operations(ctx context.Context, routingID uuid.UUID) ([]domain.RoutingOperation, error) {
	var rows []domain.RoutingOperation
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM routing_operations WHERE routing_id=$1 ORDER BY seq_no`, routingID)
	return rows, err
}

func (r *RoutingRepo) AddOperation(ctx context.Context, op *domain.RoutingOperation) error {
	if op.ID == uuid.Nil {
		op.ID = uuid.New()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO routing_operations
		(id, routing_id, seq_no, work_center_id, description, setup_minutes, run_minutes_per_unit,
		 setup_family, overlap_enabled, transfer_batch_qty, machines_required, workers_required)
		VALUES (:id, :routing_id, :seq_no, :work_center_id, :description, :setup_minutes, :run_minutes_per_unit,
		        :setup_family, :overlap_enabled, :transfer_batch_qty, :machines_required, :workers_required)
	`, op)
	return err
}

func (r *RoutingRepo) UpdateOperation(ctx context.Context, op *domain.RoutingOperation) error {
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE routing_operations SET
		  seq_no=:seq_no, work_center_id=:work_center_id, description=:description,
		  setup_minutes=:setup_minutes, run_minutes_per_unit=:run_minutes_per_unit,
		  setup_family=:setup_family, overlap_enabled=:overlap_enabled,
		  transfer_batch_qty=:transfer_batch_qty, machines_required=:machines_required,
		  workers_required=:workers_required
		WHERE id=:id
	`, op)
	return err
}

func (r *RoutingRepo) DeleteOperation(ctx context.Context, opID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM routing_operations WHERE id=$1`, opID)
	return err
}

// OperationsForItem — 品目から有効ルーティングの工程を直接取得 (CRP/原価で多用)
func (r *RoutingRepo) OperationsForItem(ctx context.Context, itemID uuid.UUID) ([]domain.RoutingOperation, error) {
	var rows []domain.RoutingOperation
	err := r.db.SelectContext(ctx, &rows, `
		SELECT op.*
		  FROM routing_operations op
		  JOIN routings r ON r.id = op.routing_id
		 WHERE r.item_id = $1 AND r.is_active = true
		 ORDER BY op.seq_no`, itemID)
	return rows, err
}

func (r *RoutingRepo) Alternatives(ctx context.Context, opID uuid.UUID) ([]domain.RoutingOperationAlternative, error) {
	var rows []domain.RoutingOperationAlternative
	err := r.db.SelectContext(ctx, &rows, `
		SELECT * FROM routing_operation_alternatives
		 WHERE routing_operation_id=$1
		 ORDER BY priority, created_at, id`, opID)
	return rows, err
}

func (r *RoutingRepo) AddAlternative(ctx context.Context, x *domain.RoutingOperationAlternative) error {
	if x.ID == uuid.Nil {
		x.ID = uuid.New()
	}
	if x.RunTimeMultiplier <= 0 {
		x.RunTimeMultiplier = 1
	}
	if x.SetupTimeMultiplier <= 0 {
		x.SetupTimeMultiplier = 1
	}
	if x.Priority < 0 {
		x.Priority = 100
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO routing_operation_alternatives
		(id,routing_operation_id,work_center_id,priority,run_time_multiplier,setup_time_multiplier,is_active,created_at)
		VALUES (:id,:routing_operation_id,:work_center_id,:priority,:run_time_multiplier,:setup_time_multiplier,:is_active,:created_at)`, x)
	return err
}

func (r *RoutingRepo) DeleteAlternative(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM routing_operation_alternatives WHERE id=$1`, id)
	return err
}

func (r *WorkCenterRepo) SetupMatrix(ctx context.Context, wcID uuid.UUID) ([]domain.WorkCenterSetupMatrixRow, error) {
	var rows []domain.WorkCenterSetupMatrixRow
	err := r.db.SelectContext(ctx, &rows, `SELECT * FROM work_center_setup_matrix WHERE work_center_id=$1 ORDER BY to_setup_family,from_setup_family`, wcID)
	return rows, err
}

func (r *WorkCenterRepo) UpsertSetupMatrix(ctx context.Context, x *domain.WorkCenterSetupMatrixRow) error {
	if x.ID == uuid.Nil {
		x.ID = uuid.New()
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now()
	}
	if x.FromSetupFamily == "" {
		x.FromSetupFamily = "*"
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO work_center_setup_matrix(id,work_center_id,from_setup_family,to_setup_family,setup_minutes,created_at)
		VALUES (:id,:work_center_id,:from_setup_family,:to_setup_family,:setup_minutes,:created_at)
		ON CONFLICT(work_center_id,from_setup_family,to_setup_family)
		DO UPDATE SET setup_minutes=EXCLUDED.setup_minutes`, x)
	return err
}

func (r *WorkCenterRepo) DeleteSetupMatrix(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM work_center_setup_matrix WHERE id=$1`, id)
	return err
}

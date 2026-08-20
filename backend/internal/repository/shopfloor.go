package repository

import (
	"context"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ShopFloorRepo struct{ db *sqlx.DB }

// CreateOpsForWO is retained for compatibility with older callers. New release
// workflow creates operations inside its own transaction so reservations, WO
// status and Shop Floor rows commit atomically.
func (r *ShopFloorRepo) CreateOpsForWO(ctx context.Context, woID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO wo_operations
		  (id, wo_id, seq_no, work_center_id, description,
		   planned_setup_min, planned_run_per_unit, routing_operation_id,
		   setup_family, overlap_enabled, transfer_batch_qty, machines_required, workers_required, status)
		SELECT gen_random_uuid(), $1, x.seq_no, x.work_center_id, x.description,
		       x.setup_minutes, x.run_minutes_per_unit, x.id,
		       x.setup_family, x.overlap_enabled, x.transfer_batch_qty, x.machines_required, x.workers_required,
		       CASE WHEN x.rn=1 THEN 'READY' ELSE 'PENDING' END
		  FROM (
		    SELECT ro.*,
		           row_number() OVER (ORDER BY ro.seq_no) AS rn
		      FROM routing_operations ro
		      JOIN routings rt ON rt.id=ro.routing_id
		      JOIN work_orders w ON w.item_id=rt.item_id
		     WHERE w.id=$1 AND rt.is_active=true
		  ) x
		ON CONFLICT (wo_id, seq_no) DO NOTHING
	`, woID)
	return err
}

func (r *ShopFloorRepo) ListByWO(ctx context.Context, woID uuid.UUID) ([]domain.WOOperationDetail, error) {
	var rows []domain.WOOperationDetail
	err := r.db.SelectContext(ctx, &rows, `
		SELECT op.*, wc.code AS wc_code, wc.name AS wc_name,
		       w.order_no, i.code AS item_code, i.name AS item_name,
		       w.quantity AS wo_quantity
		  FROM wo_operations op
		  JOIN work_centers wc ON wc.id = op.work_center_id
		  JOIN work_orders w   ON w.id  = op.wo_id
		  JOIN items i         ON i.id  = w.item_id
		 WHERE op.wo_id = $1
		 ORDER BY op.seq_no
	`, woID)
	return rows, err
}

// Active returns all unfinished operations so the UI can show READY work,
// currently running/paused work, and PENDING successors waiting for predecessors.
func (r *ShopFloorRepo) Active(ctx context.Context) ([]domain.WOOperationDetail, error) {
	var rows []domain.WOOperationDetail
	err := r.db.SelectContext(ctx, &rows, `
		SELECT op.*, wc.code AS wc_code, wc.name AS wc_name,
		       w.order_no, i.code AS item_code, i.name AS item_name,
		       w.quantity AS wo_quantity
		  FROM wo_operations op
		  JOIN work_centers wc ON wc.id = op.work_center_id
		  JOIN work_orders w   ON w.id  = op.wo_id
		  JOIN items i         ON i.id  = w.item_id
		 WHERE op.status IN ('PENDING', 'READY', 'IN_PROGRESS', 'PAUSED')
		   AND w.status IN ('RELEASED', 'IN_PROGRESS')
		 ORDER BY w.order_no, op.seq_no
	`)
	return rows, err
}

func (r *ShopFloorRepo) Get(ctx context.Context, opID uuid.UUID) (*domain.WOOperation, error) {
	var o domain.WOOperation
	err := r.db.GetContext(ctx, &o, `SELECT * FROM wo_operations WHERE id=$1`, opID)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *ShopFloorRepo) Logs(ctx context.Context, opID uuid.UUID) ([]domain.OperationLog, error) {
	var rows []domain.OperationLog
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM operation_logs WHERE wo_op_id=$1 ORDER BY event_at, id`, opID)
	return rows, err
}

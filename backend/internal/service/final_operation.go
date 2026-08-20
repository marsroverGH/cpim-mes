package service

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// lockFinalOperationTx returns the highest-sequence operation for the WO and
// locks that row. Workflow completion and Shop Floor completion both lock rows
// in the same order (work_orders -> final/target wo_operations), so a final
// operation report cannot race a finished-goods receipt.
func lockFinalOperationTx(ctx context.Context, tx *sqlx.Tx, woID uuid.UUID) (*domain.WOOperation, error) {
	var op domain.WOOperation
	err := tx.GetContext(ctx, &op, `
		SELECT *
		  FROM wo_operations
		 WHERE wo_id=$1
		 ORDER BY seq_no DESC
		 LIMIT 1
		 FOR UPDATE
	`, woID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewConflict("WO has no Shop Floor operations; finished-goods receipt requires a final operation actual")
		}
		return nil, err
	}
	return &op, nil
}

func getFinalOperationTx(ctx context.Context, tx *sqlx.Tx, woID uuid.UUID) (*domain.WOOperation, error) {
	var op domain.WOOperation
	err := tx.GetContext(ctx, &op, `
		SELECT *
		  FROM wo_operations
		 WHERE wo_id=$1
		 ORDER BY seq_no DESC
		 LIMIT 1
	`, woID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewConflict("WO has no Shop Floor operations; finished-goods receipt requires a final operation actual")
		}
		return nil, err
	}
	return &op, nil
}

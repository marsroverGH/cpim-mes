package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ShopFloorService struct {
	db *sqlx.DB
	r  *repository.ShopFloorRepo
}

// ShopFloorActor is resolved from the authenticated JWT by the HTTP layer.
// The client never supplies operator identity for Shop Floor actuals.
type ShopFloorActor struct {
	UserID   uuid.UUID
	Username string
}

func (a ShopFloorActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated Shop Floor user is required")
	}
	return nil
}

func (s *ShopFloorService) ListByWO(ctx context.Context, woID uuid.UUID) ([]domain.WOOperationDetail, error) {
	return s.r.ListByWO(ctx, woID)
}
func (s *ShopFloorService) Active(ctx context.Context) ([]domain.WOOperationDetail, error) {
	return s.r.Active(ctx)
}
func (s *ShopFloorService) Get(ctx context.Context, opID uuid.UUID) (*domain.WOOperation, error) {
	return s.r.Get(ctx, opID)
}

// Start performs READY -> IN_PROGRESS or PAUSED -> IN_PROGRESS atomically with
// its audit log. A PENDING operation can never be started: all predecessors must
// first be COMPLETED and the service promotes the next operation to READY when
// its predecessor completes.
func (s *ShopFloorService) Start(ctx context.Context, opID uuid.UUID, actor ShopFloorActor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	wo, op, err := lockWOAndOperationTx(ctx, tx, opID)
	if err != nil {
		return err
	}
	if err := validateShopFloorWOStatus(wo.Status); err != nil {
		return err
	}
	if err := ValidateOperationStartStatus(op.Status); err != nil {
		return domain.NewConflict(err.Error())
	}
	if err := ensurePredecessorTransferReadyTx(ctx, tx, op.WOID, op.SeqNo); err != nil {
		return err
	}
	if err := ValidateOperationTransition(op.Status, OperationStatusInProgress); err != nil {
		return domain.NewConflict(err.Error())
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE wo_operations
		   SET status='IN_PROGRESS',
		       operator=$1,
		       operator_user_id=$2,
		       started_at=COALESCE(started_at, $3),
		       active_started_at=$3,
		       completed_at=NULL
		 WHERE id=$4
	`, actor.Username, actor.UserID, now, op.ID); err != nil {
		return err
	}
	if err := insertOperationLogTx(ctx, tx, op.ID, "START", now, actor, 0, ""); err != nil {
		return err
	}
	if wo.Status == "RELEASED" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE work_orders SET status='IN_PROGRESS' WHERE id=$1 AND status='RELEASED'
		`, wo.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Stop performs IN_PROGRESS -> PAUSED and records elapsed server-side minutes
// together with the STOP log in the same transaction.
func (s *ShopFloorService) Stop(ctx context.Context, opID uuid.UUID, actor ShopFloorActor, notes string) error {
	if err := actor.validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	wo, op, err := lockWOAndOperationTx(ctx, tx, opID)
	if err != nil {
		return err
	}
	if err := validateShopFloorWOStatus(wo.Status); err != nil {
		return err
	}
	if err := ValidateOperationStopStatus(op.Status); err != nil {
		return domain.NewConflict(err.Error())
	}
	if err := ValidateOperationTransition(op.Status, OperationStatusPaused); err != nil {
		return domain.NewConflict(err.Error())
	}
	if op.ActiveStartedAt == nil {
		return domain.NewConflict("IN_PROGRESS operation has no active server-side start timestamp")
	}

	now := time.Now()
	elapsed := AutoCalcMinutesFromStart(*op.ActiveStartedAt, now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE wo_operations
		   SET status='PAUSED',
		       actual_minutes=actual_minutes+$1,
		       operator=$2,
		       operator_user_id=$3,
		       active_started_at=NULL
		 WHERE id=$4
	`, elapsed, actor.Username, actor.UserID, op.ID); err != nil {
		return err
	}
	if err := insertOperationLogTx(ctx, tx, op.ID, "STOP", now, actor, 0, notes); err != nil {
		return err
	}
	return tx.Commit()
}

// Complete records a cumulative good-quantity actual for one routing operation.
// completedQty is cumulative for this operation, not the quantity in this call.
// Partial reports keep the operation IN_PROGRESS. When planned quantity is
// reached, the operation becomes COMPLETED and the next sequence is promoted
// from PENDING to READY in the same transaction.
func (s *ShopFloorService) Complete(
	ctx context.Context,
	opID uuid.UUID,
	completedQty float64,
	actor ShopFloorActor,
	notes string,
) error {
	if err := actor.validate(); err != nil {
		return err
	}
	if completedQty <= 0 {
		return domain.NewBadRequest("completedQty must be > 0", nil)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	wo, op, err := lockWOAndOperationTx(ctx, tx, opID)
	if err != nil {
		return err
	}
	if err := validateShopFloorWOStatus(wo.Status); err != nil {
		return err
	}
	if err := ValidateOperationCompleteStatus(op.Status); err != nil {
		return domain.NewConflict(err.Error())
	}
	if op.ActiveStartedAt == nil {
		return domain.NewConflict("IN_PROGRESS operation has no active server-side start timestamp")
	}

	var finalOpID uuid.UUID
	if err := tx.GetContext(ctx, &finalOpID, `
		SELECT id FROM wo_operations
		 WHERE wo_id=$1
		 ORDER BY seq_no DESC
		 LIMIT 1
	`, wo.ID); err != nil {
		return err
	}
	isFinal := op.ID == finalOpID

	// With transfer-batch overlap, a successor may start before the predecessor is
	// fully complete, but its cumulative good quantity may never overtake the
	// immediately preceding operation.
	var predecessorQty *float64
	var pred float64
	if err := tx.GetContext(ctx, &pred, `
		SELECT completed_qty FROM wo_operations
		 WHERE wo_id=$1 AND seq_no<$2 ORDER BY seq_no DESC LIMIT 1
	`, wo.ID, op.SeqNo); err == nil {
		predecessorQty = &pred
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if predecessorQty != nil && completedQty > *predecessorQty+1e-9 {
		return domain.NewConflict("completed quantity exceeds the immediate predecessor's completed quantity")
	}

	delta, newStatus, calcErr := CalcOperationCumulative(
		wo.Quantity, op.CompletedQty, completedQty, wo.CompletedQty, isFinal,
	)
	if calcErr != nil {
		return domain.NewConflict(calcErr.Error())
	}

	now := time.Now()
	elapsed := AutoCalcMinutesFromStart(*op.ActiveStartedAt, now)
	var completedAt any
	var activeStartedAt any = now
	if newStatus == OperationStatusCompleted {
		if err := ValidateOperationTransition(op.Status, OperationStatusCompleted); err != nil {
			return domain.NewConflict(err.Error())
		}
		completedAt = now
		activeStartedAt = nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE wo_operations
		   SET status=$1,
		       completed_qty=$2,
		       actual_minutes=actual_minutes+$3,
		       operator=$4,
		       operator_user_id=$5,
		       completed_at=$6,
		       active_started_at=$7
		 WHERE id=$8
	`, newStatus, completedQty, elapsed, actor.Username, actor.UserID,
		completedAt, activeStartedAt, op.ID); err != nil {
		return err
	}

	if err := insertOperationLogTx(ctx, tx, op.ID, "COMPLETE", now, actor, delta, notes); err != nil {
		return err
	}

	transferReady := newStatus == OperationStatusCompleted ||
		(op.OverlapEnabled && op.TransferBatchQty > 0 && completedQty+1e-9 >= math.Min(op.TransferBatchQty, wo.Quantity))
	if transferReady {
		var next domain.WOOperation
		err := tx.GetContext(ctx, &next, `
			SELECT * FROM wo_operations
			 WHERE wo_id=$1 AND seq_no>$2
			 ORDER BY seq_no
			 LIMIT 1
			 FOR UPDATE
		`, wo.ID, op.SeqNo)
		switch {
		case err == nil:
			if next.Status == OperationStatusPending {
				if err := ValidateOperationTransition(next.Status, OperationStatusReady); err != nil {
					return domain.NewConflict(err.Error())
				}
				if _, err := tx.ExecContext(ctx, `UPDATE wo_operations SET status='READY' WHERE id=$1`, next.ID); err != nil {
					return err
				}
			}
		case errors.Is(err, sql.ErrNoRows):
			// Final operation: there is no successor to promote.
		default:
			return err
		}
	}

	if wo.Status == "RELEASED" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE work_orders SET status='IN_PROGRESS' WHERE id=$1 AND status='RELEASED'
		`, wo.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ShopFloorService) Logs(ctx context.Context, opID uuid.UUID) ([]domain.OperationLog, error) {
	return s.r.Logs(ctx, opID)
}

func lockWOAndOperationTx(ctx context.Context, tx *sqlx.Tx, opID uuid.UUID) (*domain.WorkOrder, *domain.WOOperation, error) {
	var wo domain.WorkOrder
	if err := tx.GetContext(ctx, &wo, `
		SELECT w.*
		  FROM work_orders w
		  JOIN wo_operations op ON op.wo_id=w.id
		 WHERE op.id=$1
		 FOR UPDATE OF w
	`, opID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.NewNotFound("work order operation")
		}
		return nil, nil, err
	}
	var op domain.WOOperation
	if err := tx.GetContext(ctx, &op, `SELECT * FROM wo_operations WHERE id=$1 FOR UPDATE`, opID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.NewNotFound("work order operation")
		}
		return nil, nil, err
	}
	return &wo, &op, nil
}

func validateShopFloorWOStatus(status string) error {
	if status != "RELEASED" && status != "IN_PROGRESS" {
		return domain.NewConflict("Shop Floor actuals require a RELEASED or IN_PROGRESS work order")
	}
	return nil
}

func ensurePredecessorTransferReadyTx(ctx context.Context, tx *sqlx.Tx, woID uuid.UUID, seqNo int) error {
	var ready bool
	if err := tx.GetContext(ctx, &ready, `SELECT wo_predecessor_transfer_ready($1,$2)`, woID, seqNo); err != nil {
		return err
	}
	if !ready {
		return domain.NewConflict("previous operation has not completed the required transfer batch")
	}
	return nil
}

func insertOperationLogTx(
	ctx context.Context,
	tx *sqlx.Tx,
	opID uuid.UUID,
	eventType string,
	eventAt time.Time,
	actor ShopFloorActor,
	qty float64,
	notes string,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO operation_logs
		  (id, wo_op_id, event_type, event_at, operator, operator_user_id, quantity, notes)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)
	`, opID, eventType, eventAt, actor.Username, actor.UserID, qty, notes)
	return err
}

// AutoCalcMinutesFromStart returns elapsed server-side minutes for the current
// active session. Each STOP or partial COMPLETE closes/snapshots that session so
// the same interval is never counted twice.
func AutoCalcMinutesFromStart(startedAt time.Time, now time.Time) float64 {
	if startedAt.IsZero() {
		return 0
	}
	d := now.Sub(startedAt)
	if d <= 0 {
		return 0
	}
	return d.Minutes()
}

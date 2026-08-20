package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// QualityService owns all quality mutations. Read methods delegate to the
// repository, while Record uses an explicit transaction so inspection evidence,
// lot state, and immutable status history commit or roll back together.
type QualityService struct {
	db    *sqlx.DB
	repos *repository.Repositories
}

type QualityActor struct {
	UserID   uuid.UUID
	Username string
}

func (a QualityActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated quality inspector is required")
	}
	return nil
}

type QualityRecordInput struct {
	Result    string
	DefectQty float64
	Notes     string
}

func QualityStatusForResult(result string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(result)) {
	case "PASS":
		return "OK", nil
	case "HOLD":
		return "HOLD", nil
	case "FAIL":
		return "REJECTED", nil
	default:
		return "", domain.NewBadRequest("result must be PASS / FAIL / HOLD", nil)
	}
}

func (s *QualityService) ListByLot(ctx context.Context, lotID uuid.UUID) ([]domain.QualityInspection, error) {
	return s.repos.Quality.ListByLot(ctx, lotID)
}

func (s *QualityService) Recent(ctx context.Context, limit int) ([]domain.QualityInspection, error) {
	return s.repos.Quality.Recent(ctx, limit)
}

func (s *QualityService) StatusHistory(ctx context.Context, lotID uuid.UUID) ([]domain.QualityStatusHistory, error) {
	return s.repos.Quality.StatusHistory(ctx, lotID)
}

// Record atomically records one inspection. The lot row is locked before the
// evidence row is inserted, serializing concurrent inspections for the same lot.
// Database triggers normalize the inspector identity/time, transition the lot's
// quality_status, and append quality_status_history in the same transaction.
func (s *QualityService) Record(
	ctx context.Context,
	lotID uuid.UUID,
	in QualityRecordInput,
	actor QualityActor,
) (*domain.QualityInspection, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	in.Result = strings.ToUpper(strings.TrimSpace(in.Result))
	if _, err := QualityStatusForResult(in.Result); err != nil {
		return nil, err
	}
	if in.DefectQty < 0 {
		return nil, domain.NewBadRequest("defectQty must be >= 0", nil)
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Lock before validation so two inspectors cannot both derive the same prior
	// status and then race their state transitions.
	var lot struct {
		ID            uuid.UUID `db:"id"`
		Quantity      float64   `db:"quantity"`
		QualityStatus string    `db:"quality_status"`
	}
	if err := tx.GetContext(ctx, &lot, `
		SELECT id, quantity, quality_status
		  FROM lots
		 WHERE id=$1
		 FOR UPDATE
	`, lotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("lot")
		}
		return nil, err
	}
	if in.DefectQty > lot.Quantity {
		return nil, domain.NewBadRequest("defectQty cannot exceed lot quantity", nil)
	}

	inspectionID := uuid.New()
	now := time.Now()
	var q domain.QualityInspection
	if err := tx.GetContext(ctx, &q, `
		INSERT INTO quality_inspections(
		  id, lot_id, inspector_user_id, inspector, inspected_at,
		  result, defect_qty, notes
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, lot_id, inspector_user_id, inspector, inspected_at,
		          result, defect_qty, notes, previous_status, resulting_status
	`, inspectionID, lotID, actor.UserID, actor.Username, now,
		in.Result, in.DefectQty, strings.TrimSpace(in.Notes)); err != nil {
		return nil, err
	}

	// The DB trigger must have produced exactly one matching audit row before we
	// commit. This is an application-level assertion in addition to DB constraints.
	var historyCount int
	if err := tx.GetContext(ctx, &historyCount, `
		SELECT count(*) FROM quality_status_history WHERE inspection_id=$1
	`, q.ID); err != nil {
		return nil, err
	}
	if historyCount != 1 {
		return nil, domain.NewConflict("quality inspection did not produce exactly one status-history row")
	}

	var actualStatus string
	if err := tx.GetContext(ctx, &actualStatus, `SELECT quality_status FROM lots WHERE id=$1`, lotID); err != nil {
		return nil, err
	}
	if actualStatus != q.ResultingStatus {
		return nil, domain.NewConflict("lot quality status does not match inspection result")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &q, nil
}

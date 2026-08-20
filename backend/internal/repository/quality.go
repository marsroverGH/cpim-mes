package repository

import (
	"context"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type QualityRepo struct{ db *sqlx.DB }

func (r *QualityRepo) ListByLot(ctx context.Context, lotID uuid.UUID) ([]domain.QualityInspection, error) {
	var rows []domain.QualityInspection
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM quality_inspections WHERE lot_id=$1 ORDER BY inspected_at DESC, id DESC`, lotID)
	return rows, err
}

// Recent — 直近の検査記録 (品質ダッシュボード用)
func (r *QualityRepo) Recent(ctx context.Context, limit int) ([]domain.QualityInspection, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []domain.QualityInspection
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM quality_inspections ORDER BY inspected_at DESC, id DESC LIMIT $1`, limit)
	return rows, err
}

// StatusHistory returns the immutable lot-quality transition history.
func (r *QualityRepo) StatusHistory(ctx context.Context, lotID uuid.UUID) ([]domain.QualityStatusHistory, error) {
	var rows []domain.QualityStatusHistory
	err := r.db.SelectContext(ctx, &rows, `
		SELECT *
		  FROM quality_status_history
		 WHERE lot_id=$1
		 ORDER BY changed_at DESC, id DESC
	`, lotID)
	return rows, err
}

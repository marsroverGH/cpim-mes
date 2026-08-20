package repository

import (
	"context"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type CycleCountRepo struct{ db *sqlx.DB }

func (r *CycleCountRepo) List(ctx context.Context, statusFilter string) ([]domain.CycleCountWithItem, error) {
	q := `
SELECT cc.*, i.code AS item_code, i.name AS item_name
  FROM cycle_counts cc
  JOIN items i ON i.id = cc.item_id
 WHERE ($1 = '' OR cc.status = $1)
 ORDER BY cc.scheduled_date, i.code`
	var rows []domain.CycleCountWithItem
	err := r.db.SelectContext(ctx, &rows, q, statusFilter)
	return rows, err
}

func (r *CycleCountRepo) Create(ctx context.Context, c *domain.CycleCount) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.Status == "" {
		c.Status = "PENDING"
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO cycle_counts (id, item_id, abc_class, scheduled_date, expected_qty, status, notes, created_at)
		VALUES (:id, :item_id, :abc_class, :scheduled_date, :expected_qty, :status, :notes, :created_at)
	`, c)
	return err
}

func (r *CycleCountRepo) Update(ctx context.Context, c *domain.CycleCount) error {
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE cycle_counts SET
		  counted_date  = :counted_date,
		  counted_qty   = :counted_qty,
		  status        = :status,
		  notes         = :notes
		WHERE id = :id
	`, c)
	return err
}

func (r *CycleCountRepo) Get(ctx context.Context, id uuid.UUID) (*domain.CycleCount, error) {
	var c domain.CycleCount
	err := r.db.GetContext(ctx, &c, `SELECT * FROM cycle_counts WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// LastScheduledFor — 同じ品目の最近の予定日 (重複生成防止用)
func (r *CycleCountRepo) LastScheduledFor(ctx context.Context, itemID uuid.UUID) (*time.Time, error) {
	var t *time.Time
	err := r.db.GetContext(ctx, &t,
		`SELECT MAX(scheduled_date) FROM cycle_counts WHERE item_id=$1`, itemID)
	return t, err
}

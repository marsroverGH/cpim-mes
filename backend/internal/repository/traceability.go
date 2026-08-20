package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// Lots
// ====================================================================

type LotRepo struct{ db *sqlx.DB }

func (r *LotRepo) Get(ctx context.Context, id uuid.UUID) (*domain.Lot, error) {
	var lot domain.Lot
	if err := r.db.GetContext(ctx, &lot, `SELECT * FROM lots WHERE id=$1`, id); err != nil {
		return nil, err
	}
	return &lot, nil
}

func (r *LotRepo) List(ctx context.Context) ([]domain.LotWithBalance, error) {
	q := `
SELECT l.id, l.item_id, l.lot_no, l.quantity, l.received_at, l.expiry_date,
       l.supplier, l.source_doc, l.notes, l.quality_status,
       i.code AS item_code, i.name AS item_name,
       COALESCE(SUM(lm.quantity), 0) AS balance
  FROM lots l
  JOIN items i ON i.id = l.item_id
  LEFT JOIN lot_movements lm ON lm.lot_id = l.id
 GROUP BY l.id, i.code, i.name
 ORDER BY l.received_at DESC, i.code`
	var rows []domain.LotWithBalance
	err := r.db.SelectContext(ctx, &rows, q)
	return rows, err
}

func (r *LotRepo) ByItem(ctx context.Context, itemID uuid.UUID) ([]domain.LotWithBalance, error) {
	q := `
SELECT l.id, l.item_id, l.lot_no, l.quantity, l.received_at, l.expiry_date,
       l.supplier, l.source_doc, l.notes, l.quality_status,
       i.code AS item_code, i.name AS item_name,
       COALESCE(SUM(lm.quantity), 0) AS balance
  FROM lots l
  JOIN items i ON i.id = l.item_id
  LEFT JOIN lot_movements lm ON lm.lot_id = l.id
 WHERE l.item_id = $1
 GROUP BY l.id, i.code, i.name
 ORDER BY l.received_at DESC`
	var rows []domain.LotWithBalance
	err := r.db.SelectContext(ctx, &rows, q, itemID)
	return rows, err
}

func (r *LotRepo) Create(ctx context.Context, l *domain.Lot) error {
	return fmt.Errorf("direct lot creation is forbidden; use InventoryLedgerService")
}

func (r *LotRepo) Movements(ctx context.Context, lotID uuid.UUID) ([]domain.LotMovement, error) {
	var rows []domain.LotMovement
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM lot_movements WHERE lot_id=$1 ORDER BY occurred_at`, lotID)
	return rows, err
}

func (r *LotRepo) AddMovement(ctx context.Context, m *domain.LotMovement) error {
	return fmt.Errorf("direct lot movement posting is forbidden; use InventoryLedgerService")
}

// WhereUsed — 指定ロットを参照する全ての ref_doc (WO/PO番号など) を返す
func (r *LotRepo) WhereUsed(ctx context.Context, lotID uuid.UUID) ([]domain.LotMovement, error) {
	var rows []domain.LotMovement
	err := r.db.SelectContext(ctx, &rows, `
		SELECT * FROM lot_movements
		 WHERE lot_id = $1 AND ref_doc <> '' AND movement_type IN ('ISSUE','CONSUMED','PRODUCED')
		 ORDER BY occurred_at DESC`, lotID)
	return rows, err
}

// ====================================================================
// Audit Log
// ====================================================================

type AuditRepo struct{ db *sqlx.DB }

type AuditFilter struct {
	Username string
	Resource string
	Limit    int
}

func (r *AuditRepo) List(ctx context.Context, f AuditFilter) ([]domain.AuditLogEntry, error) {
	q := `SELECT id, username, user_role, action, resource, resource_id,
                 http_status, ip_address, occurred_at, payload
            FROM audit_log
           WHERE ($1 = '' OR username = $1)
             AND ($2 = '' OR resource = $2)
        ORDER BY occurred_at DESC
           LIMIT $3`
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows []domain.AuditLogEntry
	err := r.db.SelectContext(ctx, &rows, q, f.Username, f.Resource, limit)
	return rows, err
}

func (r *AuditRepo) Insert(ctx context.Context, e *domain.AuditLogEntry) error {
	var payload any = e.Payload
	if len(e.Payload) == 0 {
		payload = nil
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (username, user_role, action, resource, resource_id,
		                       http_status, ip_address, occurred_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.Username, e.UserRole, e.Action, e.Resource, e.ResourceID,
		e.HTTPStatus, e.IPAddress,
		nilTime(e.OccurredAt), payload,
	)
	return err
}

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil // let DB default
	}
	return t
}

// MarshalPayload — helper for handlers/middleware
func MarshalPayload(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

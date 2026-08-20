package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// Engineering Change Order (ECO/ECN) Service
// ====================================================================
//
// ECO は「BOM 変更要求」のラッパー。ステータスは:
//   DRAFT     → 作成直後
//   APPROVED  → 承認済 (effective_date 到来で適用可能)
//   APPLIED   → BOM に反映済 (取り消し不可)
//   CANCELLED → 取り消し
//
// Apply は単一トランザクションで、ECO に紐づく全 ADD/REMOVE/MODIFY を
// bom_components に反映してから ECO を APPLIED に遷移させる。

type ECOService struct {
	db    *sqlx.DB
	repos *repository.Repositories
}

func NewECOService(db *sqlx.DB, repos *repository.Repositories) *ECOService {
	return &ECOService{db: db, repos: repos}
}

type ECOActor struct {
	UserID   uuid.UUID
	Username string
}

func (s *ECOService) List(ctx context.Context) ([]domain.EngineeringChange, error) {
	return s.repos.ECO.List(ctx)
}
func (s *ECOService) Get(ctx context.Context, id uuid.UUID) (*domain.EngineeringChange, error) {
	return s.repos.ECO.Get(ctx, id)
}

// Create always creates a DRAFT ECO and derives the requester from the
// authenticated principal. Client-supplied status/requestedBy values are never trusted.
func (s *ECOService) Create(ctx context.Context, e *domain.EngineeringChange, actor ECOActor) error {
	if actor.UserID == uuid.Nil || actor.Username == "" {
		return domain.NewUnauthorized("authenticated ECO requester is required")
	}
	if e.ECONo == "" || e.Title == "" || e.EffectiveDate.IsZero() {
		return domain.NewBadRequest("ecoNo, title, and effectiveDate are required", nil)
	}
	e.Status = "DRAFT"
	e.RequestedBy = actor.Username
	e.RequestedByUserID = &actor.UserID
	e.ApprovedBy = ""
	e.ApprovedByUserID = nil
	e.ApprovedAt = nil
	e.AppliedBy = ""
	e.AppliedByUserID = nil
	e.AppliedAt = nil
	e.CancelledBy = ""
	e.CancelledByUserID = nil
	e.CancelledAt = nil
	return s.repos.ECO.Create(ctx, e)
}

// Approve locks the ECO, verifies it is still a DRAFT and has at least one
// change row, then records an immutable approval identity/time in one transaction.
func (s *ECOService) Approve(ctx context.Context, id uuid.UUID, actor ECOActor) error {
	if actor.UserID == uuid.Nil || actor.Username == "" {
		return domain.NewUnauthorized("authenticated ECO approver is required")
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin ECO approval transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var eco domain.EngineeringChange
	if err := tx.GetContext(ctx, &eco, `SELECT * FROM engineering_changes WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("eco")
		}
		return fmt.Errorf("lock ECO for approval: %w", err)
	}
	if eco.Status != "DRAFT" {
		return domain.NewConflict(fmt.Sprintf("ECO must be DRAFT (current: %s)", eco.Status))
	}
	var n int
	if err := tx.GetContext(ctx, &n, `SELECT count(*) FROM eco_components WHERE eco_id=$1`, id); err != nil {
		return fmt.Errorf("count ECO components: %w", err)
	}
	if n == 0 {
		return domain.NewBadRequest("ECO has no components to approve", nil)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE engineering_changes
		   SET status='APPROVED', approved_by=$1, approved_by_user_id=$2, approved_at=now()
		 WHERE id=$3 AND status='DRAFT'
	`, actor.Username, actor.UserID, id)
	if err != nil {
		return fmt.Errorf("approve ECO: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return fmt.Errorf("check ECO approval update: %w", err)
		}
		return domain.NewConflict("ECO status changed concurrently; approval aborted")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ECO approval: %w", err)
	}
	return nil
}

func (s *ECOService) Cancel(ctx context.Context, id uuid.UUID, actor ECOActor) error {
	if actor.UserID == uuid.Nil || actor.Username == "" {
		return domain.NewUnauthorized("authenticated ECO canceller is required")
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin ECO cancel transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var eco domain.EngineeringChange
	if err := tx.GetContext(ctx, &eco, `SELECT * FROM engineering_changes WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("eco")
		}
		return err
	}
	if !validECOTransition(eco.Status, "CANCELLED") {
		return domain.NewConflict(fmt.Sprintf("ECO cannot be cancelled from %s", eco.Status))
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE engineering_changes
		   SET status='CANCELLED', cancelled_by=$1, cancelled_by_user_id=$2, cancelled_at=now()
		 WHERE id=$3
	`, actor.Username, actor.UserID, id)
	if err != nil {
		return fmt.Errorf("cancel ECO: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ECO cancel: %w", err)
	}
	return nil
}

func (s *ECOService) ListComponents(ctx context.Context, ecoID uuid.UUID) ([]domain.ECOComponent, error) {
	return s.repos.ECO.ListComponents(ctx, ecoID)
}

// AddComponent is serialized with approval by locking the ECO row. Once an ECO
// leaves DRAFT its approved content is immutable.
func (s *ECOService) AddComponent(ctx context.Context, c *domain.ECOComponent) error {
	if c.Action != "ADD" && c.Action != "REMOVE" && c.Action != "MODIFY" {
		return domain.NewBadRequest("action must be ADD/REMOVE/MODIFY", nil)
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin ECO component transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var status string
	if err := tx.GetContext(ctx, &status, `SELECT status FROM engineering_changes WHERE id=$1 FOR UPDATE`, c.ECOID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("eco")
		}
		return err
	}
	if status != "DRAFT" {
		return domain.NewConflict("ECO components are immutable after approval")
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	_, err = tx.NamedExecContext(ctx, `
		INSERT INTO eco_components (id, eco_id, action, parent_id, child_id,
		                             new_quantity, new_scrap_pct, notes)
		VALUES (:id, :eco_id, :action, :parent_id, :child_id,
		        :new_quantity, :new_scrap_pct, :notes)
	`, c)
	if err != nil {
		return fmt.Errorf("add ECO component: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ECO component: %w", err)
	}
	return nil
}

func (s *ECOService) ListHistory(ctx context.Context, ecoID uuid.UUID) ([]domain.ECOStatusHistory, error) {
	return s.repos.ECO.ListHistory(ctx, ecoID)
}

// ecoBusinessDate reads the exact business date enforced by PostgreSQL. The
// migration defaults to Asia/Tokyo and can be overridden per DB session with
// SET app.business_timezone = 'Region/City'.
func ecoBusinessDate(ctx context.Context, tx *sqlx.Tx) (time.Time, error) {
	var d time.Time
	if err := tx.GetContext(ctx, &d, `SELECT eco_business_date(now())`); err != nil {
		return time.Time{}, fmt.Errorf("read ECO business date: %w", err)
	}
	return d, nil
}

// Apply — APPROVED な ECO の変更内容を BOM に反映する。
//
// ECO 読み込み、全 BOM 変更、循環検査、LLC 再計算、APPLIED 遷移を
// すべて同一トランザクションで実行する。BOM topology advisory lock により、
// 通常 BOM 編集と別 ECO の同時実行も直列化される。どの段階でも失敗すれば
// ECO status と BOM の双方がロールバックされる。
func (s *ECOService) Apply(ctx context.Context, ecoID uuid.UUID, actor ECOActor) error {
	if actor.UserID == uuid.Nil || actor.Username == "" {
		return domain.NewUnauthorized("authenticated ECO applier is required")
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin ECO apply transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Serialize every BOM topology writer before reading the ECO/BOM state.
	if err := lockBOMTopologyTx(ctx, tx); err != nil {
		return err
	}

	var eco domain.EngineeringChange
	if err := tx.GetContext(ctx, &eco, `
		SELECT * FROM engineering_changes WHERE id=$1 FOR UPDATE
	`, ecoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("eco")
		}
		return fmt.Errorf("lock ECO: %w", err)
	}
	if eco.Status != "APPROVED" {
		return domain.NewConflict(fmt.Sprintf("ECO must be APPROVED to apply (current: %s)", eco.Status))
	}
	if eco.ApprovedAt == nil || eco.ApprovedByUserID == nil || eco.ApprovedBy == "" {
		return domain.NewConflict("ECO approval audit fields are incomplete")
	}
	businessDate, err := ecoBusinessDate(ctx, tx)
	if err != nil {
		return err
	}
	if !ecoEffectiveOn(eco.EffectiveDate, businessDate) {
		return domain.NewConflict(fmt.Sprintf("ECO is not effective until %s (business date: %s)",
			eco.EffectiveDate.Format("2006-01-02"), businessDate.Format("2006-01-02")))
	}

	var comps []domain.ECOComponent
	if err := tx.SelectContext(ctx, &comps, `
		SELECT * FROM eco_components WHERE eco_id=$1 ORDER BY id
	`, ecoID); err != nil {
		return fmt.Errorf("load ECO components: %w", err)
	}
	if len(comps) == 0 {
		return domain.NewBadRequest("ECO has no components to apply", nil)
	}

	for _, c := range comps {
		if c.ParentID == uuid.Nil || c.ChildID == uuid.Nil {
			return domain.NewBadRequest("ECO component parentId and childId are required", nil)
		}
		if c.ParentID == c.ChildID && c.Action != "REMOVE" {
			return domain.NewConflict("ECO would create a BOM self-reference")
		}

		switch c.Action {
		case "ADD":
			if c.NewQuantity <= 0 {
				return domain.NewBadRequest("ECO ADD newQuantity must be > 0", nil)
			}
			if c.NewScrapPct < 0 {
				return domain.NewBadRequest("ECO ADD newScrapPct must be >= 0", nil)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO bom_components (id, parent_id, child_id, quantity, scrap_pct)
				VALUES ($1, $2, $3, $4, $5)
			`, uuid.New(), c.ParentID, c.ChildID, c.NewQuantity, c.NewScrapPct); err != nil {
				return fmt.Errorf("ECO ADD %v->%v: %w", c.ParentID, c.ChildID, err)
			}

		case "REMOVE":
			res, err := tx.ExecContext(ctx, `
				DELETE FROM bom_components
				 WHERE parent_id=$1 AND child_id=$2
			`, c.ParentID, c.ChildID)
			if err != nil {
				return fmt.Errorf("ECO REMOVE %v->%v: %w", c.ParentID, c.ChildID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("ECO REMOVE affected rows: %w", err)
			}
			if n != 1 {
				return domain.NewConflict(fmt.Sprintf("ECO REMOVE target does not exist: %v->%v", c.ParentID, c.ChildID))
			}

		case "MODIFY":
			if c.NewQuantity <= 0 {
				return domain.NewBadRequest("ECO MODIFY newQuantity must be > 0", nil)
			}
			if c.NewScrapPct < 0 {
				return domain.NewBadRequest("ECO MODIFY newScrapPct must be >= 0", nil)
			}
			res, err := tx.ExecContext(ctx, `
				UPDATE bom_components
				   SET quantity=$1, scrap_pct=$2
				 WHERE parent_id=$3 AND child_id=$4
			`, c.NewQuantity, c.NewScrapPct, c.ParentID, c.ChildID)
			if err != nil {
				return fmt.Errorf("ECO MODIFY %v->%v: %w", c.ParentID, c.ChildID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("ECO MODIFY affected rows: %w", err)
			}
			if n != 1 {
				return domain.NewConflict(fmt.Sprintf("ECO MODIFY target does not exist: %v->%v", c.ParentID, c.ChildID))
			}

		default:
			return domain.NewBadRequest("ECO component action must be ADD/REMOVE/MODIFY", nil)
		}
	}

	// Validate the final graph, not each intermediate ECO step. This permits an ECO
	// to remove one edge and add another atomically while still forbidding a cyclic
	// final BOM. The validation runs before LLC recomputation.
	if err := validateBOMAcyclicTx(ctx, tx); err != nil {
		return err
	}
	if err := recomputeLLCTx(ctx, tx); err != nil {
		return fmt.Errorf("ECO applied BOM changes but LLC recompute failed; rolling back: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE engineering_changes
		   SET status='APPLIED', applied_by=$1, applied_by_user_id=$2, applied_at=now()
		 WHERE id=$3 AND status='APPROVED'
	`, actor.Username, actor.UserID, ecoID)
	if err != nil {
		return fmt.Errorf("mark ECO applied: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("check ECO status update: %w", err)
	} else if n != 1 {
		return domain.NewConflict("ECO status changed concurrently; apply aborted")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ECO apply: %w", err)
	}
	return nil
}

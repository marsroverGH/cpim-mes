package repository

import (
	"context"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// Item Group + S&OP Plan + RCCP Profile
// ====================================================================

type SOPRepo struct{ db *sqlx.DB }

func (r *SOPRepo) ListGroups(ctx context.Context) ([]domain.ItemGroup, error) {
	var rows []domain.ItemGroup
	err := r.db.SelectContext(ctx, &rows, `SELECT * FROM item_groups ORDER BY code`)
	return rows, err
}

func (r *SOPRepo) CreateGroup(ctx context.Context, g *domain.ItemGroup) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO item_groups (id, code, name, description, created_at)
		VALUES (:id, :code, :name, :description, :created_at)
	`, g)
	return err
}

func (r *SOPRepo) ListPlans(ctx context.Context) ([]domain.SOPPlan, error) {
	var rows []domain.SOPPlan
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM sop_plans ORDER BY plan_month, group_id`)
	return rows, err
}

func (r *SOPRepo) PlansByGroup(ctx context.Context, groupID uuid.UUID) ([]domain.SOPPlan, error) {
	var rows []domain.SOPPlan
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM sop_plans WHERE group_id=$1 ORDER BY plan_month`, groupID)
	return rows, err
}

func (r *SOPRepo) UpsertPlan(ctx context.Context, p *domain.SOPPlan) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO sop_plans (id, group_id, plan_month, demand_qty, supply_qty,
		                       inventory_target, notes, created_at)
		VALUES (:id, :group_id, :plan_month, :demand_qty, :supply_qty,
		        :inventory_target, :notes, :created_at)
		ON CONFLICT (group_id, plan_month) DO UPDATE SET
		  demand_qty       = EXCLUDED.demand_qty,
		  supply_qty       = EXCLUDED.supply_qty,
		  inventory_target = EXCLUDED.inventory_target,
		  notes            = EXCLUDED.notes
	`, p)
	return err
}

func (r *SOPRepo) DeletePlan(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sop_plans WHERE id=$1`, id)
	return err
}

// ----- RCCP Profile -----

func (r *SOPRepo) ListProfiles(ctx context.Context) ([]domain.RCCPProfile, error) {
	var rows []domain.RCCPProfile
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM rccp_profiles ORDER BY item_id, work_center_id`)
	return rows, err
}

func (r *SOPRepo) UpsertProfile(ctx context.Context, p *domain.RCCPProfile) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO rccp_profiles (id, item_id, work_center_id, minutes_per_unit)
		VALUES (:id, :item_id, :work_center_id, :minutes_per_unit)
		ON CONFLICT (item_id, work_center_id) DO UPDATE SET
		  minutes_per_unit = EXCLUDED.minutes_per_unit
	`, p)
	return err
}

// ====================================================================
// Engineering Change Order (ECO)
// ====================================================================

type ECORepo struct{ db *sqlx.DB }

func (r *ECORepo) List(ctx context.Context) ([]domain.EngineeringChange, error) {
	var rows []domain.EngineeringChange
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM engineering_changes ORDER BY effective_date DESC, eco_no`)
	return rows, err
}

func (r *ECORepo) Get(ctx context.Context, id uuid.UUID) (*domain.EngineeringChange, error) {
	var e domain.EngineeringChange
	err := r.db.GetContext(ctx, &e, `SELECT * FROM engineering_changes WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *ECORepo) Create(ctx context.Context, e *domain.EngineeringChange) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.Status == "" {
		e.Status = "DRAFT"
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO engineering_changes (id, eco_no, title, description, status,
		                                  effective_date, requested_by, requested_by_user_id,
		                                  approved_by, created_at)
		VALUES (:id, :eco_no, :title, :description, :status,
		        :effective_date, :requested_by, :requested_by_user_id,
		        :approved_by, :created_at)
	`, e)
	return err
}

func (r *ECORepo) ListHistory(ctx context.Context, ecoID uuid.UUID) ([]domain.ECOStatusHistory, error) {
	var rows []domain.ECOStatusHistory
	err := r.db.SelectContext(ctx, &rows, `
		SELECT id, eco_id, from_status, to_status, actor_user_id, actor_username,
		       occurred_at, effective_date_snapshot
		  FROM eco_status_history
		 WHERE eco_id=$1
		 ORDER BY occurred_at, id
	`, ecoID)
	return rows, err
}

func (r *ECORepo) ListComponents(ctx context.Context, ecoID uuid.UUID) ([]domain.ECOComponent, error) {
	var rows []domain.ECOComponent
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM eco_components WHERE eco_id=$1`, ecoID)
	return rows, err
}

func (r *ECORepo) AddComponent(ctx context.Context, c *domain.ECOComponent) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO eco_components (id, eco_id, action, parent_id, child_id,
		                             new_quantity, new_scrap_pct, notes)
		VALUES (:id, :eco_id, :action, :parent_id, :child_id,
		        :new_quantity, :new_scrap_pct, :notes)
	`, c)
	return err
}

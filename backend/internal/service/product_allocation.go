package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ProductAllocationBucketInput struct {
	ServiceClassCode string  `json:"serviceClassCode"`
	AllocationPct    float64 `json:"allocationPct"`
	PriorityRank     int     `json:"priorityRank"`
}

type ProductAllocationPlanInput struct {
	ItemID        uuid.UUID                      `json:"itemId"`
	Name          string                         `json:"name"`
	EffectiveFrom string                         `json:"effectiveFrom"`
	EffectiveTo   string                         `json:"effectiveTo"`
	Buckets       []ProductAllocationBucketInput `json:"buckets"`
}

type CustomerServiceClassInput struct {
	ServiceClassCode string `json:"serviceClassCode"`
}

type SalesOrderPriorityInput struct {
	Priority string `json:"priority"`
}

type ProductAllocationService struct {
	db *sqlx.DB
}

type ProductAllocationPolicy struct {
	Plan    domain.ProductAllocationPlan
	Buckets map[string]domain.ProductAllocationBucket
}

func (s *ProductAllocationService) ListServiceClasses(ctx context.Context) ([]domain.CustomerServiceClass, error) {
	var rows []domain.CustomerServiceClass
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM customer_service_classes ORDER BY priority_rank,code`)
	return rows, err
}

func (s *ProductAllocationService) SetCustomerServiceClass(ctx context.Context, customerID uuid.UUID, in CustomerServiceClassInput, actor SalesOrderActor) (*domain.Customer, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	code := strings.ToUpper(strings.TrimSpace(in.ServiceClassCode))
	if code == "" {
		return nil, domain.NewBadRequest("serviceClassCode is required", nil)
	}
	var active bool
	if err := s.db.GetContext(ctx, &active, `SELECT is_active FROM customer_service_classes WHERE code=$1`, code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewBadRequest("unknown customer service class", nil)
		}
		return nil, err
	}
	if !active {
		return nil, domain.NewConflict("customer service class is inactive")
	}
	var row domain.Customer
	if err := s.db.GetContext(ctx, &row, `UPDATE customers SET service_class_code=$2,updated_at=now() WHERE id=$1 RETURNING *`, customerID, code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("customer")
		}
		return nil, err
	}
	return &row, nil
}

func (s *ProductAllocationService) SetSalesOrderPriority(ctx context.Context, orderID uuid.UUID, in SalesOrderPriorityInput, actor SalesOrderActor) (*domain.SalesOrderDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	priority := strings.ToUpper(strings.TrimSpace(in.Priority))
	if priority != "NORMAL" && priority != "HIGH" && priority != "EXPEDITE" {
		return nil, domain.NewBadRequest("priority must be NORMAL, HIGH or EXPEDITE", nil)
	}
	var status string
	if err := s.db.GetContext(ctx, &status, `SELECT status FROM sales_orders WHERE id=$1`, orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("sales order")
		}
		return nil, err
	}
	if status == "SHIPPED" || status == "CANCELLED" {
		return nil, domain.NewConflict("terminal sales order priority cannot be changed")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sales_orders SET priority=$2,updated_at=now() WHERE id=$1`, orderID, priority); err != nil {
		return nil, err
	}
	// Reuse the same projection as SalesOrderService without creating a dependency cycle.
	sales := &SalesOrderService{db: s.db}
	return sales.Get(ctx, orderID)
}

func (s *ProductAllocationService) CreatePlan(ctx context.Context, in ProductAllocationPlanInput, actor SalesOrderActor) (*domain.ProductAllocationPlanDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if in.ItemID == uuid.Nil || strings.TrimSpace(in.Name) == "" {
		return nil, domain.NewBadRequest("itemId and name are required", nil)
	}
	from, err := parseISODate(in.EffectiveFrom, true)
	if err != nil {
		return nil, err
	}
	to, err := parseISODate(in.EffectiveTo, true)
	if err != nil {
		return nil, err
	}
	if to.Before(*from) {
		return nil, domain.NewBadRequest("effectiveTo must be on or after effectiveFrom", nil)
	}
	if len(in.Buckets) == 0 {
		return nil, domain.NewBadRequest("at least one allocation bucket is required", nil)
	}
	seenClass, seenRank := map[string]bool{}, map[int]bool{}
	total := 0.0
	for i := range in.Buckets {
		b := &in.Buckets[i]
		b.ServiceClassCode = strings.ToUpper(strings.TrimSpace(b.ServiceClassCode))
		if b.ServiceClassCode == "" || b.AllocationPct <= 0 || b.AllocationPct > 100 || b.PriorityRank <= 0 {
			return nil, domain.NewBadRequest("allocation buckets require class, percentage (0,100], and positive rank", nil)
		}
		if seenClass[b.ServiceClassCode] || seenRank[b.PriorityRank] {
			return nil, domain.NewBadRequest("allocation bucket service classes and ranks must be unique", nil)
		}
		seenClass[b.ServiceClassCode], seenRank[b.PriorityRank] = true, true
		total += b.AllocationPct
	}
	if math.Abs(total-100) > 1e-6 {
		return nil, domain.NewBadRequest("allocation bucket percentages must total 100", nil)
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var exists bool
	if err := tx.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM items WHERE id=$1 AND type IN ('FG','SA'))`, in.ItemID); err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.NewBadRequest("product allocation item must be FG or SA", nil)
	}
	var classCount int
	classes := make([]string, 0, len(in.Buckets))
	for _, b := range in.Buckets {
		classes = append(classes, b.ServiceClassCode)
	}
	// Avoid driver-specific array helpers: validate each class in the same transaction.
	for _, code := range classes {
		var ok bool
		if err := tx.GetContext(ctx, &ok, `SELECT EXISTS(SELECT 1 FROM customer_service_classes WHERE code=$1 AND is_active)`, code); err != nil {
			return nil, err
		}
		if ok {
			classCount++
		}
	}
	if classCount != len(classes) {
		return nil, domain.NewBadRequest("allocation bucket contains unknown or inactive service class", nil)
	}

	planID := uuid.New()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO product_allocation_plans(id,item_id,name,effective_from,effective_to,status,created_by_user_id,created_by)
VALUES($1,$2,$3,$4,$5,'DRAFT',$6,$7)`, planID, in.ItemID, strings.TrimSpace(in.Name), *from, *to, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	sort.Slice(in.Buckets, func(i, j int) bool { return in.Buckets[i].PriorityRank < in.Buckets[j].PriorityRank })
	for _, b := range in.Buckets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_allocation_buckets(id,plan_id,service_class_code,allocation_pct,priority_rank) VALUES($1,$2,$3,$4,$5)`, uuid.New(), planID, b.ServiceClassCode, b.AllocationPct, b.PriorityRank); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, planID)
}

func (s *ProductAllocationService) ActivatePlan(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.ProductAllocationPlanDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE product_allocation_plans SET status='ACTIVE',activated_by_user_id=$2,activated_by=$3,activated_at=now(),updated_at=now() WHERE id=$1 AND status='DRAFT'`, id, actor.UserID, actor.Username)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, domain.NewConflict("only a DRAFT product allocation plan can be activated")
	}
	return s.GetPlan(ctx, id)
}

func (s *ProductAllocationService) DeactivatePlan(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.ProductAllocationPlanDetail, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE product_allocation_plans SET status='INACTIVE',deactivated_by_user_id=$2,deactivated_by=$3,deactivated_at=now(),updated_at=now() WHERE id=$1 AND status='ACTIVE'`, id, actor.UserID, actor.Username)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, domain.NewConflict("only an ACTIVE product allocation plan can be deactivated")
	}
	return s.GetPlan(ctx, id)
}

const productAllocationPlanSelect = `
SELECT p.id,p.item_id,i.code AS item_code,i.name AS item_name,p.name,p.effective_from,p.effective_to,p.status,
       p.created_by_user_id,p.created_by,p.activated_by_user_id,p.activated_by,p.activated_at,
       p.deactivated_by_user_id,p.deactivated_by,p.deactivated_at,p.created_at,p.updated_at
  FROM product_allocation_plans p JOIN items i ON i.id=p.item_id`

func (s *ProductAllocationService) GetPlan(ctx context.Context, id uuid.UUID) (*domain.ProductAllocationPlanDetail, error) {
	var plan domain.ProductAllocationPlan
	if err := s.db.GetContext(ctx, &plan, productAllocationPlanSelect+` WHERE p.id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("product allocation plan")
		}
		return nil, err
	}
	var buckets []domain.ProductAllocationBucket
	if err := s.db.SelectContext(ctx, &buckets, `SELECT id,plan_id,service_class_code,allocation_pct::double precision AS allocation_pct,priority_rank FROM product_allocation_buckets WHERE plan_id=$1 ORDER BY priority_rank,service_class_code`, id); err != nil {
		return nil, err
	}
	return &domain.ProductAllocationPlanDetail{Plan: plan, Buckets: buckets}, nil
}

func (s *ProductAllocationService) ListPlans(ctx context.Context) ([]domain.ProductAllocationPlanDetail, error) {
	var plans []domain.ProductAllocationPlan
	if err := s.db.SelectContext(ctx, &plans, productAllocationPlanSelect+` ORDER BY p.effective_from DESC,i.code,p.name`); err != nil {
		return nil, err
	}
	out := make([]domain.ProductAllocationPlanDetail, 0, len(plans))
	for _, p := range plans {
		var buckets []domain.ProductAllocationBucket
		if err := s.db.SelectContext(ctx, &buckets, `SELECT id,plan_id,service_class_code,allocation_pct::double precision AS allocation_pct,priority_rank FROM product_allocation_buckets WHERE plan_id=$1 ORDER BY priority_rank,service_class_code`, p.ID); err != nil {
			return nil, err
		}
		out = append(out, domain.ProductAllocationPlanDetail{Plan: p, Buckets: buckets})
	}
	return out, nil
}

func (s *ProductAllocationService) ActivePolicy(ctx context.Context, itemID uuid.UUID, asOf time.Time) (*ProductAllocationPolicy, error) {
	var plan domain.ProductAllocationPlan
	err := s.db.GetContext(ctx, &plan, productAllocationPlanSelect+`
 WHERE p.item_id=$1 AND p.status='ACTIVE' AND $2::date BETWEEN p.effective_from AND p.effective_to
 ORDER BY p.effective_from DESC,p.id DESC LIMIT 1`, itemID, TruncateDay(asOf))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []domain.ProductAllocationBucket
	if err := s.db.SelectContext(ctx, &rows, `SELECT id,plan_id,service_class_code,allocation_pct::double precision AS allocation_pct,priority_rank FROM product_allocation_buckets WHERE plan_id=$1 ORDER BY priority_rank`, plan.ID); err != nil {
		return nil, err
	}
	policy := &ProductAllocationPolicy{Plan: plan, Buckets: map[string]domain.ProductAllocationBucket{}}
	for _, b := range rows {
		policy.Buckets[b.ServiceClassCode] = b
	}
	return policy, nil
}

package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	defaultInventoryPolicyServiceLevel = 0.95
	defaultInventoryDemandWindowDays   = 90
	defaultInventoryMinHistoryDays     = 30
	defaultInventoryOrderCycleDays     = 14
)

type InventoryPolicyVersionInput struct {
	ItemID              uuid.UUID `json:"itemId"`
	PolicyMethod        string    `json:"policyMethod"`
	ReplenishmentMethod string    `json:"replenishmentMethod"`
	ServiceLevel        float64   `json:"serviceLevel"`
	DemandWindowDays    int       `json:"demandWindowDays"`
	MinHistoryDays      int       `json:"minHistoryDays"`
	OrderCycleDays      int       `json:"orderCycleDays"`
	FixedSafetyStock    *float64  `json:"fixedSafetyStock"`
	EffectiveFrom       string    `json:"effectiveFrom"`
	Notes               string    `json:"notes"`
}

type InventoryPolicyRefreshInput struct {
	AsOfDate string `json:"asOfDate"`
}

type InventoryPolicyService struct {
	db *sqlx.DB
}

type demandStats struct {
	ObservationDays int     `db:"observation_days"`
	NonzeroDays     int     `db:"nonzero_days"`
	Average         float64 `db:"average_daily_demand"`
	Stddev          float64 `db:"stddev_daily_demand"`
}

type leadTimeStats struct {
	Mean   float64
	Stddev float64
	Source string
}

func validateInventoryPolicyActor(actor SalesOrderActor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	if actor.Role != domain.RolePlanner && actor.Role != domain.RoleAdmin {
		return domain.NewForbidden("inventory policy management requires planner/admin")
	}
	return nil
}

func normalizeInventoryPolicyInput(in InventoryPolicyVersionInput) (InventoryPolicyVersionInput, error) {
	in.PolicyMethod = strings.ToUpper(strings.TrimSpace(in.PolicyMethod))
	if in.PolicyMethod == "" {
		in.PolicyMethod = "STATISTICAL"
	}
	if in.PolicyMethod != "STATISTICAL" && in.PolicyMethod != "FIXED" {
		return in, domain.NewBadRequest("policyMethod must be STATISTICAL or FIXED", nil)
	}
	in.ReplenishmentMethod = strings.ToUpper(strings.TrimSpace(in.ReplenishmentMethod))
	if in.ReplenishmentMethod == "" {
		in.ReplenishmentMethod = "MIN_MAX"
	}
	if in.ReplenishmentMethod != "MIN_MAX" && in.ReplenishmentMethod != "SAFETY_STOCK" {
		return in, domain.NewBadRequest("replenishmentMethod must be MIN_MAX or SAFETY_STOCK", nil)
	}
	if in.ServiceLevel == 0 {
		in.ServiceLevel = defaultInventoryPolicyServiceLevel
	}
	if in.ServiceLevel < 0.5 || in.ServiceLevel > 0.9999 {
		return in, domain.NewBadRequest("serviceLevel must be between 0.5 and 0.9999", nil)
	}
	if in.DemandWindowDays == 0 {
		in.DemandWindowDays = defaultInventoryDemandWindowDays
	}
	if in.DemandWindowDays < 7 || in.DemandWindowDays > 730 {
		return in, domain.NewBadRequest("demandWindowDays must be between 7 and 730", nil)
	}
	if in.MinHistoryDays == 0 {
		in.MinHistoryDays = defaultInventoryMinHistoryDays
	}
	if in.MinHistoryDays < 1 || in.MinHistoryDays > in.DemandWindowDays {
		return in, domain.NewBadRequest("minHistoryDays must be between 1 and demandWindowDays", nil)
	}
	if in.OrderCycleDays == 0 {
		in.OrderCycleDays = defaultInventoryOrderCycleDays
	}
	if in.OrderCycleDays < 0 || in.OrderCycleDays > 365 {
		return in, domain.NewBadRequest("orderCycleDays must be between 0 and 365", nil)
	}
	if in.PolicyMethod == "FIXED" && (in.FixedSafetyStock == nil || *in.FixedSafetyStock < 0) {
		return in, domain.NewBadRequest("fixedSafetyStock is required and must be >= 0 for FIXED policy", nil)
	}
	if in.FixedSafetyStock != nil && *in.FixedSafetyStock < 0 {
		return in, domain.NewBadRequest("fixedSafetyStock must be >= 0", nil)
	}
	return in, nil
}

func (s *InventoryPolicyService) CreateVersion(ctx context.Context, in InventoryPolicyVersionInput, actor SalesOrderActor) (*domain.InventoryPolicyVersion, error) {
	if err := validateInventoryPolicyActor(actor); err != nil {
		return nil, err
	}
	if in.ItemID == uuid.Nil {
		return nil, domain.NewBadRequest("itemId is required", nil)
	}
	in, err := normalizeInventoryPolicyInput(in)
	if err != nil {
		return nil, err
	}
	effective, err := parseISODate(in.EffectiveFrom, false)
	if err != nil {
		return nil, err
	}
	if effective == nil {
		var d time.Time
		if err := s.db.GetContext(ctx, &d, `SELECT eco_business_date(now())`); err != nil {
			return nil, err
		}
		effective = &d
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var itemCode string
	if err := tx.GetContext(ctx, &itemCode, `SELECT code FROM items WHERE id=$1 FOR UPDATE`, in.ItemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("item")
		}
		return nil, err
	}
	var version int
	if err := tx.GetContext(ctx, &version, `SELECT COALESCE(MAX(version_no),0)+1 FROM inventory_policy_versions WHERE item_id=$1`, in.ItemID); err != nil {
		return nil, err
	}
	row := domain.InventoryPolicyVersion{ID: uuid.New(), ItemID: in.ItemID, VersionNo: version, Status: "DRAFT", PolicyMethod: in.PolicyMethod, ReplenishmentMethod: in.ReplenishmentMethod, ServiceLevel: in.ServiceLevel, DemandWindowDays: in.DemandWindowDays, MinHistoryDays: in.MinHistoryDays, OrderCycleDays: in.OrderCycleDays, FixedSafetyStock: in.FixedSafetyStock, EffectiveFrom: TruncateDay(*effective), Notes: strings.TrimSpace(in.Notes), CreatedByUserID: actor.UserID, CreatedBy: actor.Username}
	if err := tx.GetContext(ctx, &row, `
INSERT INTO inventory_policy_versions(
 id,item_id,version_no,status,policy_method,replenishment_method,service_level,demand_window_days,min_history_days,order_cycle_days,
 fixed_safety_stock,effective_from,notes,created_by_user_id,created_by)
VALUES($1,$2,$3,'DRAFT',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING *`, row.ID, row.ItemID, row.VersionNo, row.PolicyMethod, row.ReplenishmentMethod, row.ServiceLevel, row.DemandWindowDays, row.MinHistoryDays, row.OrderCycleDays, row.FixedSafetyStock, row.EffectiveFrom, row.Notes, row.CreatedByUserID, row.CreatedBy); err != nil {
		return nil, err
	}
	row.ItemCode = itemCode
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *InventoryPolicyService) ActivateVersion(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.InventoryPolicyVersion, error) {
	if err := validateInventoryPolicyActor(actor); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var row domain.InventoryPolicyVersion
	if err := tx.GetContext(ctx, &row, `SELECT * FROM inventory_policy_versions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("inventory policy version")
		}
		return nil, err
	}
	if row.Status != "DRAFT" {
		return nil, domain.NewConflict("only DRAFT inventory policy can be activated")
	}
	var businessDate time.Time
	if err := tx.GetContext(ctx, &businessDate, `SELECT eco_business_date(now())`); err != nil {
		return nil, err
	}
	if row.EffectiveFrom.After(TruncateDay(businessDate)) {
		return nil, domain.NewConflict("future-effective inventory policy must remain DRAFT until effective date")
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM items WHERE id=$1 FOR UPDATE`, row.ItemID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE inventory_policy_versions
   SET status='ARCHIVED',archived_by_user_id=$2,archived_by=$3,archived_at=now()
 WHERE item_id=$1 AND status='ACTIVE'`, row.ItemID, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	if err := tx.GetContext(ctx, &row, `
UPDATE inventory_policy_versions
   SET status='ACTIVE',activated_by_user_id=$2,activated_by=$3,activated_at=now()
 WHERE id=$1
RETURNING *`, id, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetVersion(ctx, row.ID)
}

func (s *InventoryPolicyService) ArchiveVersion(ctx context.Context, id uuid.UUID, actor SalesOrderActor) (*domain.InventoryPolicyVersion, error) {
	if err := validateInventoryPolicyActor(actor); err != nil {
		return nil, err
	}
	var row domain.InventoryPolicyVersion
	err := s.db.GetContext(ctx, &row, `
UPDATE inventory_policy_versions
   SET status='ARCHIVED',archived_by_user_id=$2,archived_by=$3,archived_at=now()
 WHERE id=$1 AND status='ACTIVE'
RETURNING *`, id, actor.UserID, actor.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewConflict("only ACTIVE inventory policy can be archived")
	}
	if err != nil {
		return nil, err
	}
	return s.GetVersion(ctx, row.ID)
}

func (s *InventoryPolicyService) ListVersions(ctx context.Context, itemID *uuid.UUID) ([]domain.InventoryPolicyVersion, error) {
	var rows []domain.InventoryPolicyVersion
	q := `SELECT v.*,i.code AS item_code FROM inventory_policy_versions v JOIN items i ON i.id=v.item_id`
	args := []any{}
	if itemID != nil && *itemID != uuid.Nil {
		q += ` WHERE v.item_id=$1`
		args = append(args, *itemID)
	}
	q += ` ORDER BY i.code,v.version_no DESC`
	err := s.db.SelectContext(ctx, &rows, q, args...)
	return rows, err
}

func (s *InventoryPolicyService) GetVersion(ctx context.Context, id uuid.UUID) (*domain.InventoryPolicyVersion, error) {
	var row domain.InventoryPolicyVersion
	if err := s.db.GetContext(ctx, &row, `SELECT v.*,i.code AS item_code FROM inventory_policy_versions v JOIN items i ON i.id=v.item_id WHERE v.id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("inventory policy version")
		}
		return nil, err
	}
	return &row, nil
}

// inverseStandardNormalCDF uses Peter Acklam's rational approximation.
func inverseStandardNormalCDF(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := [...]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [...]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := [...]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [...]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	const phigh = 1 - plow
	if p < plow {
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	if p > phigh {
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	q := p - 0.5
	r := q * q
	return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
}

func statisticalSafetyStock(serviceLevel, meanDemand, demandStddev, leadMean, leadStddev float64) (z, safety float64) {
	z = inverseStandardNormalCDF(serviceLevel)
	variance := leadMean*demandStddev*demandStddev + meanDemand*meanDemand*leadStddev*leadStddev
	if variance < 0 || math.IsNaN(variance) {
		variance = 0
	}
	safety = z * math.Sqrt(variance)
	if safety < 0 || math.IsNaN(safety) || math.IsInf(safety, 0) {
		safety = 0
	}
	return z, safety
}

func inventoryPolicyConfidence(obs, minHistory, nonzero int, leadSource string) string {
	if obs < minHistory || nonzero == 0 {
		return "LOW"
	}
	if obs >= minHistory*3 && nonzero >= 10 && leadSource == "SUPPLIER_RELIABILITY" {
		return "HIGH"
	}
	return "MEDIUM"
}

func (s *InventoryPolicyService) demandStatistics(ctx context.Context, tx *sqlx.Tx, itemID uuid.UUID, asOf time.Time, windowDays int) (demandStats, error) {
	start := asOf.AddDate(0, 0, -(windowDays - 1))
	var out demandStats
	err := tx.GetContext(ctx, &out, `
WITH days AS (
  SELECT generate_series($2::date,$3::date,'1 day'::interval)::date AS day
), daily AS (
  SELECT (occurred_at AT TIME ZONE eco_business_timezone())::date AS day,COALESCE(SUM(-quantity),0)::double precision AS qty
    FROM inventory_txns
   WHERE item_id=$1 AND txn_type='ISSUE' AND (occurred_at AT TIME ZONE eco_business_timezone())::date BETWEEN $2 AND $3
   GROUP BY (occurred_at AT TIME ZONE eco_business_timezone())::date
), x AS (
  SELECT d.day,COALESCE(v.qty,0)::double precision AS qty FROM days d LEFT JOIN daily v USING(day)
)
SELECT COUNT(*)::int AS observation_days,
       COUNT(*) FILTER (WHERE qty>0)::int AS nonzero_days,
       COALESCE(AVG(qty),0)::double precision AS average_daily_demand,
       COALESCE(STDDEV_POP(qty),0)::double precision AS stddev_daily_demand
  FROM x`, itemID, start, asOf)
	return out, err
}

func (s *InventoryPolicyService) leadTimeStatistics(ctx context.Context, tx *sqlx.Tx, itemID uuid.UUID, itemType domain.ItemType, nominal int) (leadTimeStats, error) {
	if itemType != domain.ItemTypeRawMaterial && itemType != domain.ItemTypePurchasedPart {
		return leadTimeStats{Mean: float64(maxInt(nominal, 0)), Stddev: 0, Source: "ITEM_MASTER"}, nil
	}
	var row struct {
		Mean   sql.NullFloat64 `db:"mean_days"`
		Stddev sql.NullFloat64 `db:"stddev_days"`
		Count  int             `db:"supplier_count"`
	}
	err := tx.GetContext(ctx, &row, `
WITH eligible AS (
  SELECT v.*,ROW_NUMBER() OVER (
    PARTITION BY v.supplier_name ORDER BY CASE WHEN v.item_id=$1 THEN 0 ELSE 1 END,v.sample_count DESC
  ) AS rn
  FROM v_current_supplier_lead_time v
  WHERE (v.item_id=$1 OR v.item_id IS NULL) AND v.sample_count>=v.min_samples
), chosen AS (
  SELECT e.* FROM eligible e LEFT JOIN supplier_quality_profiles q ON q.supplier_name=e.supplier_name
  WHERE e.rn=1 AND COALESCE(q.status,'APPROVED')<>'BLOCKED'
)
SELECT MAX(average_lead_days)::double precision AS mean_days,
       MAX(stddev_lead_days)::double precision AS stddev_days,
       COUNT(*)::int AS supplier_count FROM chosen`, itemID)
	if err != nil {
		return leadTimeStats{}, err
	}
	mean := float64(maxInt(nominal, 0))
	stddev := 0.0
	source := "ITEM_MASTER"
	if row.Count > 0 && row.Mean.Valid {
		if row.Mean.Float64 > mean {
			mean = row.Mean.Float64
		}
		if row.Stddev.Valid {
			stddev = math.Max(row.Stddev.Float64, 0)
		}
		source = "SUPPLIER_RELIABILITY"
	}
	return leadTimeStats{Mean: mean, Stddev: stddev, Source: source}, nil
}

func (s *InventoryPolicyService) Refresh(ctx context.Context, in InventoryPolicyRefreshInput, actor SalesOrderActor) (*domain.InventoryPolicyRunResult, error) {
	if err := validateInventoryPolicyActor(actor); err != nil {
		return nil, err
	}
	asOfPtr, err := parseISODate(in.AsOfDate, false)
	if err != nil {
		return nil, err
	}
	var asOf time.Time
	if asOfPtr == nil {
		if err := s.db.GetContext(ctx, &asOf, `SELECT eco_business_date(now())`); err != nil {
			return nil, err
		}
	} else {
		asOf = *asOfPtr
	}
	asOf = TruncateDay(asOf)

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run := domain.InventoryPolicyRun{ID: uuid.New(), AsOfDate: asOf, Status: "RUNNING", GeneratedByUserID: actor.UserID, GeneratedBy: actor.Username}
	if err := tx.GetContext(ctx, &run, `INSERT INTO inventory_policy_runs(id,as_of_date,status,generated_by_user_id,generated_by) VALUES($1,$2,'RUNNING',$3,$4) RETURNING *`, run.ID, run.AsOfDate, run.GeneratedByUserID, run.GeneratedBy); err != nil {
		return nil, err
	}
	var policies []domain.InventoryPolicyVersion
	if err := tx.SelectContext(ctx, &policies, `SELECT v.*,i.code AS item_code FROM inventory_policy_versions v JOIN items i ON i.id=v.item_id WHERE v.status='ACTIVE' AND v.effective_from<=$1 ORDER BY i.code,v.id FOR SHARE OF v`, asOf); err != nil {
		return nil, err
	}
	results := make([]domain.InventoryPolicyResult, 0, len(policies))
	for _, p := range policies {
		var item domain.Item
		if err := tx.GetContext(ctx, &item, `SELECT * FROM items WHERE id=$1`, p.ItemID); err != nil {
			return nil, err
		}
		demand, err := s.demandStatistics(ctx, tx, p.ItemID, asOf, p.DemandWindowDays)
		if err != nil {
			return nil, err
		}
		lead, err := s.leadTimeStatistics(ctx, tx, p.ItemID, item.Type, item.LeadTimeDays)
		if err != nil {
			return nil, err
		}
		z := inverseStandardNormalCDF(p.ServiceLevel)
		safety := item.SafetyStock
		demandSource := "ISSUE_HISTORY"
		if p.PolicyMethod == "FIXED" && p.FixedSafetyStock != nil {
			safety = *p.FixedSafetyStock
			demandSource = "FIXED_POLICY"
		} else if demand.ObservationDays >= p.MinHistoryDays {
			_, safety = statisticalSafetyStock(p.ServiceLevel, demand.Average, demand.Stddev, lead.Mean, lead.Stddev)
		} else {
			demandSource = "ITEM_MASTER_FALLBACK"
		}
		reorder := demand.Average*lead.Mean + safety
		minQty := reorder
		maxQty := minQty
		if p.ReplenishmentMethod == "MIN_MAX" {
			maxQty += demand.Average * float64(p.OrderCycleDays)
		}
		if maxQty < minQty {
			maxQty = minQty
		}
		result := domain.InventoryPolicyResult{
			ID: uuid.New(), RunID: run.ID, PolicyVersionID: p.ID, ItemID: p.ItemID, ItemCode: p.ItemCode,
			DemandObservationDays: demand.ObservationDays, NonzeroDemandDays: demand.NonzeroDays,
			AverageDailyDemand: demand.Average, StddevDailyDemand: demand.Stddev,
			LeadTimeMeanDays: lead.Mean, LeadTimeStddevDays: lead.Stddev, ServiceLevel: p.ServiceLevel, ZValue: z,
			SafetyStock: safety, ReorderPoint: reorder, MinQty: minQty, MaxQty: maxQty,
			DemandSource: demandSource, LeadTimeSource: lead.Source,
			Confidence: inventoryPolicyConfidence(demand.ObservationDays, p.MinHistoryDays, demand.NonzeroDays, lead.Source),
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO inventory_policy_results(
 id,run_id,policy_version_id,item_id,demand_observation_days,nonzero_demand_days,average_daily_demand,stddev_daily_demand,
 lead_time_mean_days,lead_time_stddev_days,service_level,z_value,safety_stock,reorder_point,min_qty,max_qty,demand_source,lead_time_source,confidence)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, result.ID, result.RunID, result.PolicyVersionID, result.ItemID, result.DemandObservationDays, result.NonzeroDemandDays, result.AverageDailyDemand, result.StddevDailyDemand, result.LeadTimeMeanDays, result.LeadTimeStddevDays, result.ServiceLevel, result.ZValue, result.SafetyStock, result.ReorderPoint, result.MinQty, result.MaxQty, result.DemandSource, result.LeadTimeSource, result.Confidence); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	hash := canonicalInventoryPolicyHash(results)
	if err := tx.GetContext(ctx, &run, `UPDATE inventory_policy_runs SET status='COMPLETE',result_hash=$2,completed_at=now() WHERE id=$1 RETURNING *`, run.ID, hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, run.ID)
}

func canonicalInventoryPolicyHash(rows []domain.InventoryPolicyResult) string {
	type stable struct {
		Policy, Item                                                         string
		Obs, Nonzero                                                         int
		Avg, Stddev, LeadMean, LeadStddev, Service, Z, Safety, ROP, Min, Max float64
		DemandSource, LeadSource, Confidence                                 string
	}
	out := make([]stable, 0, len(rows))
	for _, r := range rows {
		out = append(out, stable{r.PolicyVersionID.String(), r.ItemID.String(), r.DemandObservationDays, r.NonzeroDemandDays, r.AverageDailyDemand, r.StddevDailyDemand, r.LeadTimeMeanDays, r.LeadTimeStddevDays, r.ServiceLevel, r.ZValue, r.SafetyStock, r.ReorderPoint, r.MinQty, r.MaxQty, r.DemandSource, r.LeadTimeSource, r.Confidence})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })
	b, _ := json.Marshal(out)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *InventoryPolicyService) Current(ctx context.Context) ([]domain.EffectiveInventoryPolicy, error) {
	var rows []domain.EffectiveInventoryPolicy
	err := s.db.SelectContext(ctx, &rows, `
SELECT p.policy_version_id,p.item_id,i.code AS item_code,p.version_no,p.policy_method,p.replenishment_method,
       p.service_level,p.demand_window_days,p.min_history_days,p.order_cycle_days,p.safety_stock,p.reorder_point,p.min_qty,p.max_qty,
       COALESCE(p.average_daily_demand,0)::double precision AS average_daily_demand,
       COALESCE(p.stddev_daily_demand,0)::double precision AS stddev_daily_demand,
       COALESCE(p.lead_time_mean_days,i.lead_time_days)::double precision AS lead_time_mean_days,
       COALESCE(p.lead_time_stddev_days,0)::double precision AS lead_time_stddev_days,
       p.confidence,p.calculation_status,p.demand_source,p.lead_time_source,p.calculated_as_of
  FROM v_current_inventory_policy p JOIN items i ON i.id=p.item_id ORDER BY i.code`)
	return rows, err
}

func (s *InventoryPolicyService) Effective(ctx context.Context, item domain.Item) (domain.EffectiveInventoryPolicy, error) {
	var p domain.EffectiveInventoryPolicy
	err := s.db.GetContext(ctx, &p, `
SELECT p.policy_version_id,p.item_id,i.code AS item_code,p.version_no,p.policy_method,p.replenishment_method,
       p.service_level,p.demand_window_days,p.min_history_days,p.order_cycle_days,p.safety_stock,p.reorder_point,p.min_qty,p.max_qty,
       COALESCE(p.average_daily_demand,0)::double precision AS average_daily_demand,
       COALESCE(p.stddev_daily_demand,0)::double precision AS stddev_daily_demand,
       COALESCE(p.lead_time_mean_days,i.lead_time_days)::double precision AS lead_time_mean_days,
       COALESCE(p.lead_time_stddev_days,0)::double precision AS lead_time_stddev_days,
       p.confidence,p.calculation_status,p.demand_source,p.lead_time_source,p.calculated_as_of
  FROM v_current_inventory_policy p JOIN items i ON i.id=p.item_id WHERE p.item_id=$1`, item.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EffectiveInventoryPolicy{ItemID: item.ID, ItemCode: item.Code, PolicyMethod: "LEGACY_FIXED", ReplenishmentMethod: "SAFETY_STOCK", SafetyStock: item.SafetyStock, ReorderPoint: item.SafetyStock, MinQty: item.SafetyStock, MaxQty: item.SafetyStock, LeadTimeMeanDays: float64(item.LeadTimeDays), Confidence: "LOW", CalculationStatus: "ITEM_MASTER", DemandSource: "ITEM_MASTER", LeadTimeSource: "ITEM_MASTER"}, nil
	}
	if err != nil {
		return p, err
	}
	// A newly activated statistical policy is deliberately conservative until its
	// first calculation snapshot completes: preserve legacy safety-stock netting.
	if p.CalculationStatus != "CALCULATED" {
		p.ReplenishmentMethod = "SAFETY_STOCK"
	}
	return p, nil
}

func (s *InventoryPolicyService) ListRuns(ctx context.Context) ([]domain.InventoryPolicyRun, error) {
	var rows []domain.InventoryPolicyRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM inventory_policy_runs ORDER BY created_at DESC,id DESC LIMIT 100`)
	return rows, err
}

func (s *InventoryPolicyService) GetRun(ctx context.Context, id uuid.UUID) (*domain.InventoryPolicyRunResult, error) {
	var out domain.InventoryPolicyRunResult
	if err := s.db.GetContext(ctx, &out.Run, `SELECT * FROM inventory_policy_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("inventory policy run")
		}
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &out.Results, `
SELECT r.*,i.code AS item_code FROM inventory_policy_results r JOIN items i ON i.id=r.item_id
WHERE r.run_id=$1 ORDER BY i.code,r.id`, id); err != nil {
		return nil, err
	}
	return &out, nil
}

// netMRPBucketWithInventoryPolicy preserves legacy safety-stock behavior unless a
// calculated ACTIVE MIN_MAX policy explicitly asks MRP to replenish to the order-up-to level.
func netMRPBucketWithInventoryPolicy(opening, gross, scheduled float64, policy domain.EffectiveInventoryPolicy, lotSize, eoq float64, method LotSizeMethod) (net, plannedReceipt, projected float64) {
	if policy.ReplenishmentMethod != "MIN_MAX" || policy.CalculationStatus != "CALCULATED" {
		return netMRPBucket(opening, gross, scheduled, policy.SafetyStock, lotSize, eoq, method)
	}
	projectedBefore := opening + scheduled - gross
	if projectedBefore+1e-9 >= policy.ReorderPoint {
		return 0, 0, projectedBefore
	}
	net = policy.MaxQty - projectedBefore
	if net < 0 {
		net = 0
	}
	plannedReceipt = ApplyLotSize(net, 0, lotSize, eoq, method)
	projected = projectedBefore + plannedReceipt
	return net, plannedReceipt, projected
}

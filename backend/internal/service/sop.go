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
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// S&OP (Sales & Operations Planning)
// ====================================================================
//
// 月次・品目グループ単位の需給バランスを管理する戦略レイヤー。
// MPS (週次・品目単位) よりも上位の経営計画。

type SOPService struct {
	db    *sqlx.DB
	repos *repository.Repositories
}

type SOPActor struct {
	UserID   uuid.UUID
	Username string
}

func (a SOPActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated planner required")
	}
	return nil
}

type ProductMixInputLine struct {
	ItemID uuid.UUID `json:"itemId"`
	MixPct float64   `json:"mixPct"`
}

type sopQueryer interface {
	GetContext(context.Context, any, string, ...any) error
	SelectContext(context.Context, any, string, ...any) error
}

func (s *SOPService) ListGroups(ctx context.Context) ([]domain.ItemGroup, error) {
	return s.repos.SOP.ListGroups(ctx)
}
func (s *SOPService) CreateGroup(ctx context.Context, g *domain.ItemGroup) error {
	return s.repos.SOP.CreateGroup(ctx, g)
}
func (s *SOPService) ListPlans(ctx context.Context) ([]domain.SOPPlan, error) {
	return s.repos.SOP.ListPlans(ctx)
}
func (s *SOPService) UpsertPlan(ctx context.Context, p *domain.SOPPlan) error {
	return s.repos.SOP.UpsertPlan(ctx, p)
}
func (s *SOPService) DeletePlan(ctx context.Context, id uuid.UUID) error {
	return s.repos.SOP.DeletePlan(ctx, id)
}

func (s *SOPService) ListProductMixVersions(ctx context.Context, groupID *uuid.UUID) ([]domain.SOPProductMixVersion, error) {
	q := `SELECT * FROM sop_product_mix_versions`
	args := []any{}
	if groupID != nil && *groupID != uuid.Nil {
		q += ` WHERE group_id=$1`
		args = append(args, *groupID)
	}
	q += ` ORDER BY group_id, version DESC`
	var rows []domain.SOPProductMixVersion
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	for i := range rows {
		if err := s.db.SelectContext(ctx, &rows[i].Lines,
			`SELECT * FROM sop_product_mix_lines WHERE mix_version_id=$1 ORDER BY item_id`, rows[i].ID); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (s *SOPService) CreateProductMixVersion(ctx context.Context, groupID uuid.UUID, name string, lines []ProductMixInputLine, actor SOPActor) (*domain.SOPProductMixVersion, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if groupID == uuid.Nil || len(lines) == 0 {
		return nil, domain.NewBadRequest("group and product mix lines are required", nil)
	}
	var total float64
	seen := map[uuid.UUID]bool{}
	for _, l := range lines {
		if l.ItemID == uuid.Nil || l.MixPct <= 0 || l.MixPct > 100 {
			return nil, domain.NewBadRequest("each mix line requires itemId and mixPct in (0,100]", nil)
		}
		if seen[l.ItemID] {
			return nil, domain.NewBadRequest("duplicate product mix item", nil)
		}
		seen[l.ItemID] = true
		total += l.MixPct
	}
	if math.Abs(total-100) > 0.000001 {
		return nil, domain.NewBadRequest("product mix total must equal 100%", nil)
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM item_groups WHERE id=$1 FOR UPDATE)`, groupID); err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.NewNotFound("item group")
	}
	var version int
	if err := tx.GetContext(ctx, &version, `SELECT COALESCE(MAX(version),0)+1 FROM sop_product_mix_versions WHERE group_id=$1`, groupID); err != nil {
		return nil, err
	}
	id := uuid.New()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sop_product_mix_versions(id,group_id,version,name,status,created_by_user_id,created_by)
VALUES($1,$2,$3,$4,'DRAFT',$5,$6)`, id, groupID, version, strings.TrimSpace(name), actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	for _, l := range lines {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sop_product_mix_lines(id,mix_version_id,item_id,mix_pct) VALUES($1,$2,$3,$4)`, uuid.New(), id, l.ItemID, l.MixPct); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := &domain.SOPProductMixVersion{ID: id, GroupID: groupID, Version: version, Name: strings.TrimSpace(name), Status: "DRAFT", CreatedByUserID: &actor.UserID, CreatedBy: actor.Username}
	for _, l := range lines {
		out.Lines = append(out.Lines, domain.SOPProductMixLine{ItemID: l.ItemID, MixPct: l.MixPct, MixVersionID: id})
	}
	return out, nil
}

func (s *SOPService) ActivateProductMixVersion(ctx context.Context, id uuid.UUID, actor SOPActor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mix domain.SOPProductMixVersion
	if err := tx.GetContext(ctx, &mix, `SELECT * FROM sop_product_mix_versions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("product mix version")
		}
		return err
	}
	if mix.Status != "DRAFT" {
		return domain.NewConflict("only DRAFT product mix can be activated")
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM item_groups WHERE id=$1 FOR UPDATE`, mix.GroupID); err != nil {
		return err
	}
	var total float64
	if err := tx.GetContext(ctx, &total, `SELECT COALESCE(SUM(mix_pct),0) FROM sop_product_mix_lines WHERE mix_version_id=$1`, id); err != nil {
		return err
	}
	if math.Abs(total-100) > 0.000001 {
		return domain.NewConflict("product mix total must equal 100%")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sop_product_mix_versions SET status='ARCHIVED' WHERE group_id=$1 AND status='ACTIVE'`, mix.GroupID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sop_product_mix_versions SET status='ACTIVE',activated_at=now(),activated_by_user_id=$2,activated_by=$3 WHERE id=$1 AND status='DRAFT'`, id, actor.UserID, actor.Username); err != nil {
		return err
	}
	return tx.Commit()
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func CalcSOPDisaggregation(plan domain.SOPPlan, mixID uuid.UUID, mix []domain.SOPProductMixLine) (domain.SOPDisaggregationPreview, error) {
	if plan.SupplyQty < 0 {
		return domain.SOPDisaggregationPreview{}, domain.NewBadRequest("S&OP supply quantity cannot be negative", nil)
	}
	if len(mix) == 0 {
		return domain.SOPDisaggregationPreview{}, domain.NewBadRequest("product mix is empty", nil)
	}
	var totalMix float64
	for _, l := range mix {
		totalMix += l.MixPct
	}
	if math.Abs(totalMix-100) > 0.000001 {
		return domain.SOPDisaggregationPreview{}, domain.NewBadRequest("product mix total must equal 100%", nil)
	}
	month := time.Date(plan.PlanMonth.Year(), plan.PlanMonth.Month(), 1, 0, 0, 0, 0, plan.PlanMonth.Location())
	next := month.AddDate(0, 1, 0)
	daysInMonth := int(next.Sub(month).Hours() / 24)
	if daysInMonth <= 0 {
		return domain.SOPDisaggregationPreview{}, domain.NewBadRequest("invalid S&OP plan month", nil)
	}
	var periods []struct {
		start time.Time
		days  int
	}
	for d := 0; d < daysInMonth; d += 7 {
		days := 7
		if d+days > daysInMonth {
			days = daysInMonth - d
		}
		periods = append(periods, struct {
			start time.Time
			days  int
		}{month.AddDate(0, 0, d), days})
	}
	out := domain.SOPDisaggregationPreview{SOPPlanID: plan.ID, MixVersionID: mixID, GroupID: plan.GroupID, PlanMonth: month, SupplyQty: plan.SupplyQty}
	for _, ml := range mix {
		target := round6(plan.SupplyQty * ml.MixPct / 100)
		allocated := 0.0
		for j, p := range periods {
			weight := float64(p.days) / float64(daysInMonth)
			qty := round6(target * weight)
			if j == len(periods)-1 {
				qty = round6(target - allocated)
			}
			allocated = round6(allocated + qty)
			out.Lines = append(out.Lines, domain.SOPDisaggregationLine{ItemID: ml.ItemID, Period: p.start, MixPct: ml.MixPct, TimeWeight: weight, PlannedQty: qty})
		}
	}
	return out, nil
}

func (s *SOPService) loadPlanMix(ctx context.Context, q sopQueryer, planID, mixID uuid.UUID, lock bool) (domain.SOPPlan, domain.SOPProductMixVersion, []domain.SOPProductMixLine, error) {
	var plan domain.SOPPlan
	var mix domain.SOPProductMixVersion
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	if err := q.GetContext(ctx, &plan, `SELECT * FROM sop_plans WHERE id=$1`+suffix, planID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return plan, mix, nil, domain.NewNotFound("S&OP plan")
		}
		return plan, mix, nil, err
	}
	if err := q.GetContext(ctx, &mix, `SELECT * FROM sop_product_mix_versions WHERE id=$1`+suffix, mixID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return plan, mix, nil, domain.NewNotFound("product mix version")
		}
		return plan, mix, nil, err
	}
	if mix.Status != "ACTIVE" {
		return plan, mix, nil, domain.NewConflict("only ACTIVE product mix can be used for disaggregation")
	}
	if mix.GroupID != plan.GroupID {
		return plan, mix, nil, domain.NewConflict("product mix family does not match S&OP plan family")
	}
	var lines []domain.SOPProductMixLine
	if err := q.SelectContext(ctx, &lines, `SELECT * FROM sop_product_mix_lines WHERE mix_version_id=$1 ORDER BY item_id`, mixID); err != nil {
		return plan, mix, nil, err
	}
	return plan, mix, lines, nil
}

func (s *SOPService) PreviewDisaggregation(ctx context.Context, planID, mixID uuid.UUID) (domain.SOPDisaggregationPreview, error) {
	plan, _, lines, err := s.loadPlanMix(ctx, s.db, planID, mixID, false)
	if err != nil {
		return domain.SOPDisaggregationPreview{}, err
	}
	return CalcSOPDisaggregation(plan, mixID, lines)
}

func (s *SOPService) ApplyDisaggregationToMPS(ctx context.Context, planID, mixID uuid.UUID, actor SOPActor) (*domain.SOPDisaggregationRun, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, mix, lines, err := s.loadPlanMix(ctx, tx, planID, mixID, true)
	if err != nil {
		return nil, err
	}
	preview, err := CalcSOPDisaggregation(plan, mixID, lines)
	if err != nil {
		return nil, err
	}
	runID := uuid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sop_disaggregation_runs(id,sop_plan_id,mix_version_id,group_id,plan_month,supply_qty_snapshot,time_phasing,status,applied_by_user_id,applied_by) VALUES($1,$2,$3,$4,$5,$6,'CALENDAR_DAYS_7D','APPLIED',$7,$8)`, runID, plan.ID, mix.ID, plan.GroupID, preview.PlanMonth, plan.SupplyQty, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	for _, l := range preview.Lines {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sop_disaggregation_lines(id,run_id,item_id,period,mix_pct,time_weight,planned_qty) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.New(), runID, l.ItemID, l.Period, l.MixPct, l.TimeWeight, l.PlannedQty); err != nil {
			return nil, err
		}
		var released float64
		err := tx.GetContext(ctx, &released, `SELECT released FROM mps_entries WHERE item_id=$1 AND period=$2 FOR UPDATE`, l.ItemID, l.Period)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && released > l.PlannedQty+0.000001 {
			return nil, domain.NewConflict("S&OP disaggregation would reduce MPS planned below already released quantity")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO mps_entries(id,item_id,period,planned,released,source_forecast_run_id,demand_basis,source_sop_plan_id,source_sop_disaggregation_run_id,source_product_mix_version_id)
VALUES($1,$2,$3,$4,0,NULL,'SOP_DISAGGREGATION',$5,$6,$7)
ON CONFLICT(item_id,period) DO UPDATE SET
 planned=EXCLUDED.planned,
 source_forecast_run_id=NULL,
 demand_basis='SOP_DISAGGREGATION',
 source_sop_plan_id=EXCLUDED.source_sop_plan_id,
 source_sop_disaggregation_run_id=EXCLUDED.source_sop_disaggregation_run_id,
 source_product_mix_version_id=EXCLUDED.source_product_mix_version_id`, uuid.New(), l.ItemID, l.Period, l.PlannedQty, plan.ID, runID, mix.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.SOPDisaggregationRun{ID: runID, SOPPlanID: plan.ID, MixVersionID: mix.ID, GroupID: plan.GroupID, PlanMonth: preview.PlanMonth, SupplyQtySnapshot: plan.SupplyQty, TimePhasing: "CALENDAR_DAYS_7D", Status: "APPLIED", AppliedByUserID: actor.UserID, AppliedBy: actor.Username}, nil
}

func (s *SOPService) ListDisaggregationRuns(ctx context.Context, planID *uuid.UUID) ([]domain.SOPDisaggregationRun, error) {
	q := `SELECT * FROM sop_disaggregation_runs`
	args := []any{}
	if planID != nil && *planID != uuid.Nil {
		q += ` WHERE sop_plan_id=$1`
		args = append(args, *planID)
	}
	q += ` ORDER BY applied_at DESC`
	var rows []domain.SOPDisaggregationRun
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// ====================================================================
// RCCP (Rough-Cut Capacity Planning)
// ====================================================================
//
// MPS (週次品目計画) を入力に、品目→作業区の単位負荷係数 (rccp_profiles)
// を掛けて月次×作業区の所要時間を粗算する。MRP/CRP より速いがざっくり。

type RCCPService struct {
	repos *repository.Repositories
	cal   *CalendarService
}

// CalcRCCPLoad — 純粋関数: 計画値 + プロファイル + 作業区マスタ → 月次負荷
type RCCPInput struct {
	MPSEntries  []domain.MPSEntry
	Profiles    []domain.RCCPProfile
	WorkCenters []domain.WorkCenter
	// 月初 (TruncateMonth) ごとの利用可能分数 = WC × Days × 効率 × 稼働率
	// 簡易化のため: 月の稼働日数 = 22日 × 標準カレンダー (細かい休日反映は CRP に委ねる)
	WorkingDaysPerMonth int
}

// TruncateMonth — t と同じ年月の 1日 0時 を返す純粋関数
func TruncateMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func CalcRCCPLoad(in RCCPInput) []domain.RCCPLoadRow {
	if in.WorkingDaysPerMonth <= 0 {
		in.WorkingDaysPerMonth = 22
	}

	// プロファイル lookup: itemID → []{wcID, mins}
	profByItem := make(map[uuid.UUID][]domain.RCCPProfile)
	for _, p := range in.Profiles {
		profByItem[p.ItemID] = append(profByItem[p.ItemID], p)
	}
	wcByID := make(map[uuid.UUID]domain.WorkCenter)
	for _, w := range in.WorkCenters {
		wcByID[w.ID] = w
	}

	type bk struct {
		Month time.Time
		WC    uuid.UUID
	}
	required := make(map[bk]float64)

	for _, m := range in.MPSEntries {
		profs := profByItem[m.ItemID]
		month := TruncateMonth(m.Period)
		for _, p := range profs {
			required[bk{Month: month, WC: p.WorkCenterID}] += p.MinutesPerUnit * m.Planned
		}
	}

	out := make([]domain.RCCPLoadRow, 0, len(required))
	for k, mins := range required {
		w := wcByID[k.WC]
		eff := w.Efficiency
		if eff <= 0 {
			eff = 1
		}
		util := w.Utilization
		if util <= 0 {
			util = 1
		}
		// 月次利用可能 = 1日分能力 × 稼働日数 × 効率 × 稼働率
		machines := w.MachineCount
		if machines <= 0 {
			machines = 1
		}
		avail := w.CapacityMinutesPerDay * float64(machines) * float64(in.WorkingDaysPerMonth) * eff * util
		loadPct := 0.0
		if avail > 0 {
			loadPct = mins / avail * 100
		}
		out = append(out, domain.RCCPLoadRow{
			WorkCenterID:     k.WC,
			WorkCenterCode:   w.Code,
			WorkCenterName:   w.Name,
			Month:            k.Month,
			RequiredMinutes:  mins,
			AvailableMinutes: avail,
			LoadPct:          loadPct,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Month.Equal(out[j].Month) {
			return out[i].WorkCenterCode < out[j].WorkCenterCode
		}
		return out[i].Month.Before(out[j].Month)
	})
	return out
}

func (s *RCCPService) Run(ctx context.Context, workingDays int) ([]domain.RCCPLoadRow, error) {
	mps, err := s.repos.MPS.List(ctx)
	if err != nil {
		return nil, err
	}
	profs, err := s.repos.SOP.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	return CalcRCCPLoad(RCCPInput{
		MPSEntries:          mps,
		Profiles:            profs,
		WorkCenters:         wcs,
		WorkingDaysPerMonth: workingDays,
	}), nil
}

func (s *RCCPService) ListProfiles(ctx context.Context) ([]domain.RCCPProfile, error) {
	return s.repos.SOP.ListProfiles(ctx)
}
func (s *RCCPService) UpsertProfile(ctx context.Context, p *domain.RCCPProfile) error {
	return s.repos.SOP.UpsertProfile(ctx, p)
}

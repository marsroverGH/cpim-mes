package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
// Forecasting / Versioning / Consumption
// ====================================================================

type ForecastService struct {
	db    *sqlx.DB
	repos *repository.Repositories
}

type ForecastActor struct {
	UserID   uuid.UUID
	Username string
}

func (a ForecastActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated forecast actor required")
	}
	return nil
}

type ForecastRequest struct {
	ItemID         uuid.UUID `json:"itemId"`
	Method         string    `json:"method"`
	Window         int       `json:"window"`
	Alpha          float64   `json:"alpha"`
	Beta           float64   `json:"beta"`
	Gamma          float64   `json:"gamma"`
	SeasonLength   int       `json:"seasonLength"`
	HorizonPeriods int       `json:"horizonPeriods"`
	BucketDays     int       `json:"bucketDays"`
	Scenario       string    `json:"scenario"`
	AsOfDate       string    `json:"asOfDate"`
	SaveAsVersion  bool      `json:"saveAsVersion"`
	// SaveAsForecast is retained for backward API compatibility. It now means
	// "save as a versioned forecast run" and never writes unversioned FORECAST demand rows.
	SaveAsForecast bool `json:"saveAsForecast"`
}

func normalizeScenario(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return "BASE"
	}
	return v
}

func forecastAsOfDate(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return TruncateDay(time.Now()), nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, domain.NewBadRequest("asOfDate must be YYYY-MM-DD", err)
	}
	return TruncateDay(t), nil
}

func normalizeForecastRequest(req ForecastRequest) ForecastRequest {
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" {
		req.Method = "SMA"
	}
	if req.Window <= 0 {
		req.Window = 4
	}
	if req.Alpha <= 0 || req.Alpha > 1 {
		req.Alpha = 0.3
	}
	if req.Beta <= 0 || req.Beta > 1 {
		req.Beta = 0.1
	}
	if req.Gamma <= 0 || req.Gamma > 1 {
		req.Gamma = 0.3
	}
	if req.SeasonLength <= 0 {
		req.SeasonLength = 4
	}
	if req.HorizonPeriods <= 0 {
		req.HorizonPeriods = 4
	}
	if req.BucketDays <= 0 {
		req.BucketDays = 7
	}
	req.Scenario = normalizeScenario(req.Scenario)
	return req
}

func (s *ForecastService) Run(ctx context.Context, req ForecastRequest, actor ForecastActor) (*domain.ForecastResult, error) {
	if req.ItemID == uuid.Nil {
		return nil, domain.NewBadRequest("itemId required", nil)
	}
	if err := actor.validate(); err != nil {
		return nil, err
	}
	req = normalizeForecastRequest(req)
	asOfDate, err := forecastAsOfDate(req.AsOfDate)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.AsOfDate) == "" {
		if err := s.db.GetContext(ctx, &asOfDate, `SELECT eco_business_date(now())`); err != nil {
			return nil, fmt.Errorf("resolve forecast business date: %w", err)
		}
		asOfDate = TruncateDay(asOfDate)
	}

	it, err := s.repos.Items.Get(ctx, req.ItemID)
	if err != nil {
		return nil, err
	}

	demands, err := s.repos.Demand.List(ctx)
	if err != nil {
		return nil, err
	}
	type histPoint struct {
		t   time.Time
		qty float64
	}
	hist := make([]histPoint, 0)
	for _, d := range demands {
		if d.ItemID == req.ItemID && strings.EqualFold(d.Source, "ORDER") && TruncateDay(d.DueDate).Before(asOfDate) {
			hist = append(hist, histPoint{t: d.DueDate, qty: d.Quantity})
		}
	}
	if len(hist) == 0 {
		return nil, domain.NewBadRequest("no historical customer orders found for item", nil)
	}

	sort.Slice(hist, func(i, j int) bool { return hist[i].t.Before(hist[j].t) })
	bucketSize := time.Duration(req.BucketDays) * 24 * time.Hour
	first := TruncateDay(hist[0].t)
	last := asOfDate.AddDate(0, 0, -1)
	if histLast := TruncateDay(hist[len(hist)-1].t); histLast.After(last) {
		last = histLast
	}
	buckets := make(map[time.Time]float64)
	for cur := first; !cur.After(last); cur = cur.Add(bucketSize) {
		buckets[cur] = 0
	}
	for _, h := range hist {
		days := int(TruncateDay(h.t).Sub(first).Hours() / 24)
		bIdx := days / req.BucketDays
		bucketStart := first.Add(time.Duration(bIdx) * bucketSize)
		buckets[bucketStart] += h.qty
	}

	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	actuals := make([]float64, len(keys))
	for i, k := range keys {
		actuals[i] = buckets[k]
	}

	method := req.Method
	var fitted []float64
	var hwState HoltWintersState
	switch method {
	case "EXPO":
		fitted = FitEXPO(actuals, req.Alpha)
	case "HW":
		if len(actuals) < req.SeasonLength*2 {
			method = "EXPO"
			fitted = FitEXPO(actuals, req.Alpha)
		} else {
			hwState = FitHoltWintersAdditive(actuals, req.SeasonLength, req.Alpha, req.Beta, req.Gamma)
			fitted = make([]float64, len(actuals))
			for t := 0; t < len(actuals); t++ {
				idx := t % hwState.SeasonLength
				fitted[t] = hwState.Level + hwState.Trend*float64(t-len(actuals)+1) + hwState.Seasonal[idx]
			}
		}
	default:
		method = "SMA"
		fitted = FitSMA(actuals, req.Window)
	}

	var future []float64
	switch method {
	case "EXPO":
		future = ForecastEXPO(actuals, fitted, req.Alpha, req.HorizonPeriods)
	case "HW":
		future = ForecastHoltWinters(hwState, req.HorizonPeriods, len(actuals)%hwState.SeasonLength)
	default:
		future = ForecastSMA(actuals, req.Window, req.HorizonPeriods)
	}

	mae, mape := AccuracyMetrics(actuals, fitted)
	pts := make([]domain.ForecastPoint, 0, len(actuals)+req.HorizonPeriods)
	for i, k := range keys {
		a, f := actuals[i], fitted[i]
		pts = append(pts, domain.ForecastPoint{Period: k, Actual: &a, Forecast: &f, IsFuture: false})
	}
	lastBucket := keys[len(keys)-1]
	futureValues := make([]domain.ForecastValue, 0, req.HorizonPeriods)
	for i := 0; i < req.HorizonPeriods; i++ {
		t := lastBucket.Add(time.Duration(i+1) * bucketSize)
		f := math.Max(future[i], 0)
		pts = append(pts, domain.ForecastPoint{Period: t, Forecast: &f, IsFuture: true})
		futureValues = append(futureValues, domain.ForecastValue{Period: t, Quantity: f})
	}

	res := &domain.ForecastResult{
		ItemID: req.ItemID, ItemCode: it.Code, Method: method,
		MAE: mae, MAPE: mape, Points: pts,
	}
	if req.SaveAsVersion || req.SaveAsForecast {
		run, err := s.saveVersion(ctx, req, method, mae, mape, asOfDate, futureValues, actor)
		if err != nil {
			return nil, err
		}
		res.RunID = &run.ID
		res.Version = run.Version
		res.Scenario = run.Scenario
		res.Status = run.Status
	}
	return res, nil
}

func (s *ForecastService) saveVersion(ctx context.Context, req ForecastRequest, method string, mae, mape float64, asOfDate time.Time, values []domain.ForecastValue, actor ForecastActor) (*domain.ForecastRun, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	scenario := normalizeScenario(req.Scenario)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "forecast-version:"+req.ItemID.String()+":"+scenario); err != nil {
		return nil, err
	}
	var nextVersion int
	if err := tx.GetContext(ctx, &nextVersion, `SELECT COALESCE(MAX(version),0)+1 FROM forecast_runs WHERE item_id=$1 AND scenario=$2`, req.ItemID, scenario); err != nil {
		return nil, err
	}
	params, _ := json.Marshal(map[string]any{
		"window": req.Window, "alpha": req.Alpha, "beta": req.Beta, "gamma": req.Gamma,
		"seasonLength": req.SeasonLength,
	})
	run := &domain.ForecastRun{
		ID: uuid.New(), ItemID: req.ItemID, Version: nextVersion, Scenario: scenario,
		Method: method, BucketDays: req.BucketDays, HorizonPeriods: req.HorizonPeriods, AsOfDate: asOfDate,
		MAE: mae, MAPE: mape, Status: "DRAFT", GeneratedAt: time.Now().UTC(),
		GeneratedByUserID: &actor.UserID, GeneratedBy: actor.Username,
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO forecast_runs(id,item_id,version,scenario,method,bucket_days,horizon_periods,as_of_date,parameters,mae,mape,status,generated_at,generated_by_user_id,generated_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,'DRAFT',$12,$13,$14)`,
		run.ID, run.ItemID, run.Version, run.Scenario, run.Method, run.BucketDays, run.HorizonPeriods, run.AsOfDate,
		string(params), run.MAE, run.MAPE, run.GeneratedAt, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	for _, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO forecast_values(id,forecast_run_id,period,quantity) VALUES ($1,$2,$3,$4)`, uuid.New(), run.ID, TruncateDay(v.Period), v.Quantity); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

const forecastRunSelect = `
SELECT id,item_id,version,scenario,method,bucket_days,horizon_periods,as_of_date,
       parameters::text AS parameters_json,mae,mape,status,generated_at,
       generated_by_user_id,generated_by,activated_at,activated_by_user_id,activated_by
  FROM forecast_runs`

func (s *ForecastService) ListRuns(ctx context.Context, itemID *uuid.UUID) ([]domain.ForecastRun, error) {
	var rows []domain.ForecastRun
	var err error
	if itemID != nil && *itemID != uuid.Nil {
		err = s.db.SelectContext(ctx, &rows, forecastRunSelect+` WHERE item_id=$1 ORDER BY scenario,version DESC`, *itemID)
	} else {
		err = s.db.SelectContext(ctx, &rows, forecastRunSelect+` ORDER BY generated_at DESC,version DESC`)
	}
	return rows, err
}

func (s *ForecastService) GetRun(ctx context.Context, id uuid.UUID) (*domain.ForecastRunDetail, error) {
	var run domain.ForecastRun
	if err := s.db.GetContext(ctx, &run, forecastRunSelect+` WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("forecast run")
		}
		return nil, err
	}
	var values []domain.ForecastValue
	if err := s.db.SelectContext(ctx, &values, `SELECT id,forecast_run_id,period,quantity FROM forecast_values WHERE forecast_run_id=$1 ORDER BY period`, id); err != nil {
		return nil, err
	}
	return &domain.ForecastRunDetail{Run: run, Values: values}, nil
}

func (s *ForecastService) ActivateRun(ctx context.Context, id uuid.UUID, actor ForecastActor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var run domain.ForecastRun
	if err := tx.GetContext(ctx, &run, forecastRunSelect+` WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewNotFound("forecast run")
		}
		return err
	}
	if run.Status == "ACTIVE" {
		return tx.Commit()
	}
	if run.Status != "DRAFT" {
		return domain.NewConflict("only DRAFT forecast versions can be activated")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "forecast-active:"+run.ItemID.String()+":"+run.Scenario); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE forecast_runs SET status='ARCHIVED' WHERE item_id=$1 AND scenario=$2 AND status='ACTIVE'`, run.ItemID, run.Scenario); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE forecast_runs SET status='ACTIVE',activated_at=$2,activated_by_user_id=$3,activated_by=$4 WHERE id=$1 AND status='DRAFT'`, id, now, actor.UserID, actor.Username); err != nil {
		return err
	}
	return tx.Commit()
}

func consumeForecastQty(forecastQty, orderQty float64) (consumed, remaining, above, total float64) {
	forecastQty = math.Max(forecastQty, 0)
	orderQty = math.Max(orderQty, 0)
	consumed = math.Min(forecastQty, orderQty)
	remaining = math.Max(forecastQty-orderQty, 0)
	above = math.Max(orderQty-forecastQty, 0)
	total = orderQty + remaining
	return
}

func buildConsumptionBuckets(values []domain.ForecastValue, orders []domain.DemandForecast, bucketDays int) []domain.ForecastConsumptionBucket {
	if bucketDays <= 0 {
		bucketDays = 7
	}
	out := make([]domain.ForecastConsumptionBucket, 0, len(values))
	for _, v := range values {
		start := TruncateDay(v.Period)
		end := start.AddDate(0, 0, bucketDays)
		orderQty := 0.0
		for _, o := range orders {
			if !strings.EqualFold(o.Source, "ORDER") {
				continue
			}
			d := TruncateDay(o.DueDate)
			if !d.Before(start) && d.Before(end) {
				orderQty += o.Quantity
			}
		}
		consumed, remaining, above, total := consumeForecastQty(v.Quantity, orderQty)
		out = append(out, domain.ForecastConsumptionBucket{
			Period: start, ForecastQty: v.Quantity, OrderQty: orderQty,
			ConsumedForecast: consumed, RemainingForecast: remaining,
			OrderAboveForecast: above, TotalDemand: total,
		})
	}
	return out
}

func (s *ForecastService) Consumption(ctx context.Context, id uuid.UUID) (*domain.ForecastConsumptionResult, error) {
	detail, err := s.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	it, err := s.repos.Items.Get(ctx, detail.Run.ItemID)
	if err != nil {
		return nil, err
	}
	orders, err := s.repos.Demand.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.DemandForecast, 0)
	for _, o := range orders {
		if o.ItemID == detail.Run.ItemID && strings.EqualFold(o.Source, "ORDER") {
			filtered = append(filtered, o)
		}
	}
	buckets := buildConsumptionBuckets(detail.Values, filtered, detail.Run.BucketDays)
	return &domain.ForecastConsumptionResult{
		RunID: detail.Run.ID, ItemID: detail.Run.ItemID, ItemCode: it.Code,
		Version: detail.Run.Version, Scenario: detail.Run.Scenario,
		BucketDays: detail.Run.BucketDays, Status: detail.Run.Status, Buckets: buckets,
	}, nil
}

func (s *ForecastService) ApplyConsumptionToMPS(ctx context.Context, id uuid.UUID, actor ForecastActor) (int, error) {
	if err := actor.validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var run domain.ForecastRun
	if err := tx.GetContext(ctx, &run, forecastRunSelect+` WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.NewNotFound("forecast run")
		}
		return 0, err
	}
	if run.Status != "ACTIVE" {
		return 0, domain.NewConflict("only ACTIVE forecast versions can be published to MPS")
	}
	var values []domain.ForecastValue
	if err := tx.SelectContext(ctx, &values, `SELECT id,forecast_run_id,period,quantity FROM forecast_values WHERE forecast_run_id=$1 ORDER BY period`, id); err != nil {
		return 0, err
	}
	var orders []domain.DemandForecast
	if err := tx.SelectContext(ctx, &orders, `SELECT * FROM demand_forecasts WHERE item_id=$1 AND source='ORDER' ORDER BY due_date`, run.ItemID); err != nil {
		return 0, err
	}
	buckets := buildConsumptionBuckets(values, orders, run.BucketDays)
	count := 0
	for _, b := range buckets {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO mps_entries(id,item_id,period,planned,released,source_forecast_run_id,demand_basis,source_sop_plan_id,source_sop_disaggregation_run_id,source_product_mix_version_id)
VALUES ($1,$2,$3,$4,0,$5,'FORECAST_CONSUMPTION',NULL,NULL,NULL)
ON CONFLICT (item_id,period) DO UPDATE SET
  planned=EXCLUDED.planned,
  source_forecast_run_id=EXCLUDED.source_forecast_run_id,
  demand_basis='FORECAST_CONSUMPTION',
  source_sop_plan_id=NULL,
  source_sop_disaggregation_run_id=NULL,
  source_product_mix_version_id=NULL`, uuid.New(), run.ItemID, b.Period, b.TotalDemand, run.ID); err != nil {
			return count, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

// ====================================================================
// Cycle Count
// ====================================================================
//
// ABC分析と連動した棚卸計画を生成・記録する。
//
// 推奨頻度: A=週次 (7日), B=月次 (30日), C=四半期 (90日)
// 同じ品目について最後にスケジュール済みの日から適切な間隔以内なら
// 重複生成しない。

type CycleCountService struct {
	repos  *repository.Repositories
	abc    *ABCService
	ledger *InventoryLedgerService
}

func intervalDays(class string) int {
	switch class {
	case "A":
		return 7
	case "B":
		return 30
	default:
		return 90
	}
}

// GenerateSchedule — ABC分析を実行し、PENDING のサイクルカウントを必要数だけ生成
func (s *CycleCountService) GenerateSchedule(ctx context.Context) (int, error) {
	abcRows, err := s.abc.Run(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	created := 0
	for _, a := range abcRows {
		if a.OnHand <= 0 {
			continue // 在庫が無い品目はスキップ
		}
		last, err := s.repos.CycleCounts.LastScheduledFor(ctx, a.ItemID)
		if err != nil {
			return created, err
		}
		interval := intervalDays(a.ABCClass)
		if last != nil && now.Sub(*last) < time.Duration(interval)*24*time.Hour {
			continue // 期間内に既存スケジュールあり
		}
		// 次回予定日 = last + interval (last 不在なら 今日)
		var sched time.Time
		if last != nil {
			sched = last.Add(time.Duration(interval) * 24 * time.Hour)
		} else {
			sched = TruncateDay(now)
		}
		expected := a.OnHand
		c := &domain.CycleCount{
			ItemID:        a.ItemID,
			ABCClass:      a.ABCClass,
			ScheduledDate: sched,
			ExpectedQty:   &expected,
			Status:        "PENDING",
		}
		if err := s.repos.CycleCounts.Create(ctx, c); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (s *CycleCountService) List(ctx context.Context, status string) ([]domain.CycleCountWithItem, error) {
	return s.repos.CycleCounts.List(ctx, status)
}

// RecordCount — 棚卸結果を登録し、差異が0でなければ自動で在庫調整トランザクションを起票
func (s *CycleCountService) RecordCount(ctx context.Context, id uuid.UUID, countedQty float64, notes string) error {
	cc, err := s.repos.CycleCounts.Get(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now()
	cc.CountedDate = &now
	cc.CountedQty = &countedQty
	cc.Notes = notes

	expected := 0.0
	if cc.ExpectedQty != nil {
		expected = *cc.ExpectedQty
	}
	variance := countedQty - expected

	if math.Abs(variance) < 0.0001 {
		cc.Status = "RECONCILED" // 差異なし
	} else {
		cc.Status = "COUNTED"
	}

	if err := s.repos.CycleCounts.Update(ctx, cc); err != nil {
		return err
	}

	// 差異があれば統合在庫台帳を通して調整する。
	// 正差異は棚卸専用ロットとして生成し、負差異は既存ロットへFIFO配賦する。
	// これにより item-level ADJUST だけが残り、lot balance が追随しない状態を作らない。
	if math.Abs(variance) >= 0.0001 {
		ref := "CC-" + id.String()[:8]
		_, err := s.ledger.Post(ctx, PhysicalInventoryRequest{
			ItemID:       cc.ItemID,
			Quantity:     variance,
			TxnType:      "ADJUST",
			RefDoc:       ref,
			SourceDoc:    ref,
			LotNo:        ref,
			Notes:        "Cycle count reconciliation",
			MovementType: "ADJUST",
			IncludeNonOK: true,
		})
		if err != nil {
			return err
		}
		cc.Status = "RECONCILED"
		if err := s.repos.CycleCounts.Update(ctx, cc); err != nil {
			return err
		}
	}

	return nil
}

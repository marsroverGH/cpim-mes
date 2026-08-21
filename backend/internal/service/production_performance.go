package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ProductionPerformanceService derives OEE and capacity recommendations from
// immutable Shop Floor / Maintenance evidence. It never rewrites Work Center
// master parameters; planners activate versioned feedback explicitly.
type ProductionPerformanceService struct {
	db          *sqlx.DB
	rescheduler *ScheduleExecutionService
}

type ProductionPerformanceActor struct {
	UserID   uuid.UUID
	Username string
	Role     domain.Role
}

func (a ProductionPerformanceActor) validatePlanner() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated production-performance user is required")
	}
	if a.Role != domain.RoleAdmin && a.Role != domain.RolePlanner {
		return domain.NewForbidden("production performance / capacity feedback requires planner/admin")
	}
	return nil
}

type ProductionPerformanceRequest struct {
	WindowStart     time.Time `json:"windowStart"`
	WindowEnd       time.Time `json:"windowEnd"`
	MinCompletedOps int       `json:"minCompletedOps"`
}

type feedbackActionInput struct {
	EffectiveFrom time.Time
	Notes         string
}

type rawPerformanceMetrics struct {
	ActiveMinutes float64 `db:"active_minutes"`
	PauseMinutes  float64 `db:"pause_minutes"`
	GoodQty       float64 `db:"good_qty"`
	RejectQty     float64 `db:"reject_qty"`
	IdealRun      float64 `db:"ideal_run"`
	SetupMinutes  float64 `db:"setup_minutes"`
	SampleCount   int     `db:"sample_count"`
}

type maintenancePerformanceEvidence struct {
	EventType           string    `db:"event_type"`
	Status              string    `db:"status"`
	StartAt             time.Time `db:"start_at"`
	EndAt               time.Time `db:"end_at"`
	UnavailableMachines int       `db:"unavailable_machines"`
	UnavailableWorkers  int       `db:"unavailable_workers"`
}

func perfClamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func overlapMinutes(a0, a1, b0, b1 time.Time) float64 {
	start := a0
	if b0.After(start) {
		start = b0
	}
	end := a1
	if b1.Before(end) {
		end = b1
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start).Minutes()
}

func calculateOEERatios(active, pause, unplanned, setup, ideal, good, reject float64) (availability, performance, quality, oee, speedLoss float64) {
	active = math.Max(active, 0)
	pause = math.Max(pause, 0)
	unplanned = math.Max(unplanned, 0)
	setup = math.Max(setup, 0)
	ideal = math.Max(ideal, 0)
	good = math.Max(good, 0)
	reject = math.Max(reject, 0)
	if active+pause+unplanned > 1e-9 {
		availability = perfClamp(active/(active+pause+unplanned), 0, 1)
	}
	runExSetup := math.Max(active-setup, 0)
	if runExSetup > 1e-9 {
		performance = perfClamp(ideal/runExSetup, 0, 1.5)
	}
	quality = 1
	if good+reject > 1e-9 {
		quality = perfClamp(good/(good+reject), 0, 1)
	}
	oee = perfClamp(availability*math.Min(performance, 1)*quality, 0, 1)
	speedLoss = math.Max(runExSetup-ideal, 0)
	return
}

func (s *ProductionPerformanceService) Run(ctx context.Context, req ProductionPerformanceRequest, actor ProductionPerformanceActor) (*domain.ProductionPerformanceRunResult, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	if req.WindowStart.IsZero() {
		req.WindowStart = time.Now().AddDate(0, 0, -30)
	}
	if req.WindowEnd.IsZero() {
		req.WindowEnd = time.Now()
	}
	start := TruncateDay(req.WindowStart)
	endDay := TruncateDay(req.WindowEnd)
	if endDay.Before(start) {
		return nil, domain.NewBadRequest("windowEnd must be on/after windowStart", nil)
	}
	if endDay.Sub(start) > 366*24*time.Hour {
		return nil, domain.NewBadRequest("performance window must be <= 366 days", nil)
	}
	if req.MinCompletedOps <= 0 {
		req.MinCompletedOps = 3
	}
	endExclusive := endDay.AddDate(0, 0, 1)

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	run := domain.ProductionPerformanceRun{
		ID: uuid.New(), WindowStart: start, WindowEnd: endDay, MinCompletedOps: req.MinCompletedOps,
		Status: "RUNNING", GeneratedByUserID: actor.UserID, GeneratedBy: actor.Username, CreatedAt: time.Now(),
	}
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO production_performance_runs(id,window_start,window_end,min_completed_ops,status,generated_by_user_id,generated_by,created_at)
VALUES(:id,:window_start,:window_end,:min_completed_ops,:status,:generated_by_user_id,:generated_by,:created_at)`, &run); err != nil {
		return nil, err
	}

	var wcs []domain.WorkCenter
	if err := tx.SelectContext(ctx, &wcs, `SELECT * FROM work_centers ORDER BY code`); err != nil {
		return nil, err
	}
	results := make([]domain.ProductionPerformanceResult, 0, len(wcs))
	for _, wc := range wcs {
		row, err := s.calculateWorkCenterPerformanceTx(ctx, tx, run.ID, wc, start, endExclusive, req.MinCompletedOps)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO production_performance_results
(id,run_id,work_center_id,work_center_code,sample_count,planned_production_minutes,run_time_minutes,downtime_minutes,active_session_minutes,planned_setup_minutes,ideal_run_minutes,pause_minutes,planned_downtime_minutes,unplanned_downtime_minutes,setup_loss_minutes,speed_loss_minutes,good_quantity,reject_quantity,availability,performance,quality,oee,breakdown_count,mtbf_minutes,mttr_minutes,recommended_efficiency,recommended_utilization,confidence,created_at)
VALUES(:id,:run_id,:work_center_id,:work_center_code,:sample_count,:planned_production_minutes,:run_time_minutes,:downtime_minutes,:active_session_minutes,:planned_setup_minutes,:ideal_run_minutes,:pause_minutes,:planned_downtime_minutes,:unplanned_downtime_minutes,:setup_loss_minutes,:speed_loss_minutes,:good_quantity,:reject_quantity,:availability,:performance,:quality,:oee,:breakdown_count,:mtbf_minutes,:mttr_minutes,:recommended_efficiency,:recommended_utilization,:confidence,:created_at)`, &row); err != nil {
			return nil, err
		}
	}

	hash := canonicalProductionPerformanceHash(results)
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE production_performance_runs SET status='COMPLETE',result_hash=$1,completed_at=$2 WHERE id=$3`, hash, now, run.ID); err != nil {
		return nil, err
	}
	run.Status = "COMPLETE"
	run.ResultHash = &hash
	run.CompletedAt = &now

	// Feedback references a completed immutable performance snapshot. Keeping this
	// after run completion lets the database validate provenance atomically.
	feedback := []domain.CapacityFeedbackVersion{}
	for _, row := range results {
		if row.SampleCount < req.MinCompletedOps {
			continue
		}
		f, err := createCapacityFeedbackDraftTx(ctx, tx, row, actor, endDay)
		if err != nil {
			return nil, err
		}
		feedback = append(feedback, f)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.ProductionPerformanceRunResult{Run: run, Results: results, Feedback: feedback}, nil
}

func (s *ProductionPerformanceService) calculateWorkCenterPerformanceTx(ctx context.Context, tx *sqlx.Tx, runID uuid.UUID, wc domain.WorkCenter, start, end time.Time, minSamples int) (domain.ProductionPerformanceResult, error) {
	var m rawPerformanceMetrics
	err := tx.GetContext(ctx, &m, `
WITH all_logs AS (
  SELECT l.id,l.wo_op_id,l.event_type,l.event_at,l.quantity,
         o.planned_setup_min::double precision AS setup_min,
         o.planned_run_per_unit::double precision AS run_per_unit
    FROM operation_logs l
    JOIN wo_operations o ON o.id=l.wo_op_id
   WHERE o.work_center_id=$1
), transitions AS (
  SELECT *,
         LEAD(event_at) OVER (PARTITION BY wo_op_id ORDER BY event_at,id) AS next_at,
         LEAD(event_type) OVER (PARTITION BY wo_op_id ORDER BY event_at,id) AS next_type
    FROM all_logs
   WHERE event_type IN ('START','STOP','COMPLETE')
), started AS (
  SELECT DISTINCT wo_op_id,setup_min FROM all_logs
   WHERE event_type='START' AND event_at >= $2 AND event_at < $3
), time_metrics AS (
  SELECT
   COALESCE(SUM(CASE WHEN event_type='START' AND next_at IS NOT NULL AND next_at>$2 AND event_at<$3
          THEN EXTRACT(EPOCH FROM (LEAST(next_at,$3)-GREATEST(event_at,$2)))/60 ELSE 0 END),0)::double precision AS active_minutes,
   COALESCE(SUM(CASE WHEN event_type='STOP' AND next_type='START' AND next_at IS NOT NULL AND next_at>$2 AND event_at<$3
          THEN EXTRACT(EPOCH FROM (LEAST(next_at,$3)-GREATEST(event_at,$2)))/60 ELSE 0 END),0)::double precision AS pause_minutes
    FROM transitions
), qty_metrics AS (
  SELECT
   COALESCE(SUM(CASE WHEN event_type='COMPLETE' AND event_at >= $2 AND event_at < $3 THEN quantity ELSE 0 END),0)::double precision AS good_qty,
   COALESCE(SUM(CASE WHEN event_type='SCRAP' AND event_at >= $2 AND event_at < $3 THEN quantity ELSE 0 END),0)::double precision AS reject_qty,
   COALESCE(SUM(CASE WHEN event_type IN ('COMPLETE','SCRAP') AND event_at >= $2 AND event_at < $3 THEN quantity*run_per_unit ELSE 0 END),0)::double precision AS ideal_run,
   COUNT(DISTINCT wo_op_id) FILTER (WHERE event_type='COMPLETE' AND event_at >= $2 AND event_at < $3)::int AS sample_count
    FROM all_logs
)
SELECT t.active_minutes,t.pause_minutes,q.good_qty,q.reject_qty,q.ideal_run,
       COALESCE((SELECT SUM(setup_min) FROM started),0)::double precision AS setup_minutes,
       q.sample_count
  FROM time_metrics t CROSS JOIN qty_metrics q`, wc.ID, start, end)
	if err != nil {
		return domain.ProductionPerformanceResult{}, err
	}

	var maintenance []maintenancePerformanceEvidence
	if err := tx.SelectContext(ctx, &maintenance, `
WITH latest AS (
  SELECT DISTINCT ON (r.maintenance_event_id)
         r.maintenance_event_id,r.status,r.start_at,r.end_at,r.unavailable_machines,r.unavailable_workers
    FROM maintenance_event_revisions r
   ORDER BY r.maintenance_event_id,r.revision_no DESC
)
SELECT e.event_type,l.status,l.start_at,l.end_at,l.unavailable_machines,l.unavailable_workers
  FROM maintenance_events e JOIN latest l ON l.maintenance_event_id=e.id
 WHERE e.work_center_id=$1 AND l.status<>'CANCELLED' AND l.start_at<$3 AND l.end_at>$2`, wc.ID, start, end); err != nil {
		return domain.ProductionPerformanceResult{}, err
	}

	machines := wc.MachineCount
	if machines <= 0 {
		machines = 1
	}
	workers := wc.WorkerCount
	if workers <= 0 {
		workers = 1
	}
	plannedDown, unplannedDown, breakdownMinutes := 0.0, 0.0, 0.0
	breakdowns := 0
	for _, ev := range maintenance {
		mins := overlapMinutes(ev.StartAt, ev.EndAt, start, end)
		if mins <= 0 {
			continue
		}
		mf := perfClamp(float64(ev.UnavailableMachines)/float64(machines), 0, 1)
		wf := perfClamp(float64(ev.UnavailableWorkers)/float64(workers), 0, 1)
		fraction := math.Max(mf, wf)
		if fraction <= 0 {
			continue
		}
		equiv := mins * fraction
		switch ev.EventType {
		case "PREVENTIVE_MAINTENANCE", "PLANNED_DOWNTIME":
			plannedDown += equiv
		case "BREAKDOWN", "UNPLANNED_DOWNTIME":
			unplannedDown += equiv
			if ev.EventType == "BREAKDOWN" {
				breakdowns++
				breakdownMinutes += equiv
			}
		}
	}

	active := math.Max(0, m.ActiveMinutes)
	pause := math.Max(0, m.PauseMinutes)
	good := math.Max(0, m.GoodQty)
	reject := math.Max(0, m.RejectQty)
	setup := math.Max(0, m.SetupMinutes)
	ideal := math.Max(0, m.IdealRun)
	availability, performance, quality, oee, speedLoss := calculateOEERatios(active, pause, unplannedDown, setup, ideal, good, reject)
	mtbf, mttr := 0.0, 0.0
	if breakdowns > 0 {
		mtbf = active / float64(breakdowns)
		mttr = breakdownMinutes / float64(breakdowns)
	}
	confidence := "LOW"
	if m.SampleCount >= maxInt(minSamples*3, 10) {
		confidence = "HIGH"
	} else if m.SampleCount >= minSamples {
		confidence = "MEDIUM"
	}
	recEff := wc.Efficiency
	recUtil := wc.Utilization
	if m.SampleCount > 0 {
		recEff = perfClamp(performance, 0.5, 1.2)
		recUtil = perfClamp(availability, 0.5, 1.0)
	}
	if recEff <= 0 {
		recEff = 1
	}
	if recUtil <= 0 {
		recUtil = 0.85
	}
	plannedProduction := active + pause + unplannedDown
	downtime := pause + unplannedDown
	return domain.ProductionPerformanceResult{
		ID: uuid.New(), RunID: runID, WorkCenterID: wc.ID, WorkCenterCode: wc.Code, SampleCount: m.SampleCount,
		PlannedProductionMinutes: plannedProduction, RunTimeMinutes: active, DowntimeMinutes: downtime,
		ActiveSessionMinutes: active, PlannedSetupMinutes: setup, IdealRunMinutes: ideal, PauseMinutes: pause,
		PlannedDowntimeMinutes: plannedDown, UnplannedDowntimeMinutes: unplannedDown, SetupLossMinutes: setup, SpeedLossMinutes: speedLoss,
		GoodQuantity: good, RejectQuantity: reject, Availability: availability, Performance: performance, Quality: quality, OEE: oee,
		BreakdownCount: breakdowns, MTBFMinutes: mtbf, MTTRMinutes: mttr, RecommendedEfficiency: recEff, RecommendedUtilization: recUtil,
		Confidence: confidence, CreatedAt: time.Now(),
	}, nil
}

func createCapacityFeedbackDraftTx(ctx context.Context, tx *sqlx.Tx, row domain.ProductionPerformanceResult, actor ProductionPerformanceActor, effectiveFrom time.Time) (domain.CapacityFeedbackVersion, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "capacity-feedback:"+row.WorkCenterID.String()); err != nil {
		return domain.CapacityFeedbackVersion{}, err
	}
	var version int
	if err := tx.GetContext(ctx, &version, `SELECT COALESCE(MAX(version_no),0)+1 FROM capacity_feedback_versions WHERE work_center_id=$1`, row.WorkCenterID); err != nil {
		return domain.CapacityFeedbackVersion{}, err
	}
	f := domain.CapacityFeedbackVersion{
		ID: uuid.New(), WorkCenterID: row.WorkCenterID, VersionNo: version, SourceRunID: row.RunID, SourceResultID: row.ID,
		Status: "DRAFT", EffectiveEfficiency: row.RecommendedEfficiency, EffectiveUtilization: row.RecommendedUtilization,
		SourceOEE: row.OEE, SourceAvailability: row.Availability, SourcePerformance: row.Performance, SourceQuality: row.Quality,
		SampleCount: row.SampleCount, Confidence: row.Confidence, EffectiveFrom: TruncateDay(effectiveFrom),
		Notes: "Generated from immutable production performance run", CreatedByUserID: actor.UserID, CreatedBy: actor.Username, CreatedAt: time.Now(),
	}
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO capacity_feedback_versions(id,work_center_id,version_no,source_run_id,source_result_id,status,effective_efficiency,effective_utilization,source_oee,source_availability,source_performance,source_quality,sample_count,confidence,effective_from,notes,created_by_user_id,created_by,created_at)
VALUES(:id,:work_center_id,:version_no,:source_run_id,:source_result_id,:status,:effective_efficiency,:effective_utilization,:source_oee,:source_availability,:source_performance,:source_quality,:sample_count,:confidence,:effective_from,:notes,:created_by_user_id,:created_by,:created_at)`, &f); err != nil {
		return domain.CapacityFeedbackVersion{}, err
	}
	return f, nil
}

func canonicalProductionPerformanceHash(rows []domain.ProductionPerformanceResult) string {
	cp := append([]domain.ProductionPerformanceResult(nil), rows...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].WorkCenterID.String() < cp[j].WorkCenterID.String() })
	h := sha256.New()
	for _, r := range cp {
		_, _ = fmt.Fprintf(h, "%s|%d|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f|%d|%.6f|%.6f|%.6f|%.6f|%s\n",
			r.WorkCenterID, r.SampleCount, r.PlannedProductionMinutes, r.RunTimeMinutes, r.DowntimeMinutes, r.ActiveSessionMinutes, r.PlannedSetupMinutes, r.IdealRunMinutes, r.PauseMinutes,
			r.PlannedDowntimeMinutes, r.UnplannedDowntimeMinutes, r.GoodQuantity, r.RejectQuantity, r.Availability, r.Performance,
			r.Quality, r.OEE, r.BreakdownCount, r.MTBFMinutes, r.MTTRMinutes, r.RecommendedEfficiency, r.RecommendedUtilization, r.Confidence)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *ProductionPerformanceService) ListRuns(ctx context.Context) ([]domain.ProductionPerformanceRun, error) {
	var rows []domain.ProductionPerformanceRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM production_performance_runs ORDER BY created_at DESC LIMIT 100`)
	return rows, err
}

func (s *ProductionPerformanceService) GetRun(ctx context.Context, id uuid.UUID) (*domain.ProductionPerformanceRunResult, error) {
	var run domain.ProductionPerformanceRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM production_performance_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("production performance run")
		}
		return nil, err
	}
	var results []domain.ProductionPerformanceResult
	if err := s.db.SelectContext(ctx, &results, `SELECT * FROM production_performance_results WHERE run_id=$1 ORDER BY work_center_code`, id); err != nil {
		return nil, err
	}
	var feedback []domain.CapacityFeedbackVersion
	if err := s.db.SelectContext(ctx, &feedback, `SELECT f.*,w.code AS work_center_code,w.name AS work_center_name FROM capacity_feedback_versions f JOIN work_centers w ON w.id=f.work_center_id WHERE f.source_run_id=$1 ORDER BY w.code,f.version_no`, id); err != nil {
		return nil, err
	}
	return &domain.ProductionPerformanceRunResult{Run: run, Results: results, Feedback: feedback}, nil
}

func (s *ProductionPerformanceService) ListFeedback(ctx context.Context) ([]domain.CapacityFeedbackVersion, error) {
	var rows []domain.CapacityFeedbackVersion
	err := s.db.SelectContext(ctx, &rows, `SELECT f.*,w.code AS work_center_code,w.name AS work_center_name FROM capacity_feedback_versions f JOIN work_centers w ON w.id=f.work_center_id ORDER BY w.code,f.version_no DESC`)
	return rows, err
}

func (s *ProductionPerformanceService) ActivateFeedback(ctx context.Context, id uuid.UUID, effectiveFrom time.Time, notes string, actor ProductionPerformanceActor) (*domain.CapacityFeedbackVersion, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var f domain.CapacityFeedbackVersion
	if err := tx.GetContext(ctx, &f, `SELECT * FROM capacity_feedback_versions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("capacity feedback version")
		}
		return nil, err
	}
	if f.Status != "DRAFT" {
		return nil, domain.NewConflict("only DRAFT capacity feedback can be activated")
	}
	if effectiveFrom.IsZero() {
		effectiveFrom = f.EffectiveFrom
	}
	effectiveFrom = TruncateDay(effectiveFrom)
	if !effectiveFrom.Equal(TruncateDay(f.EffectiveFrom)) {
		return nil, domain.NewConflict("effectiveFrom is frozen when the feedback version is generated; create a new performance run for a different effective date")
	}
	var businessDate time.Time
	if err := tx.GetContext(ctx, &businessDate, `SELECT (now() AT TIME ZONE eco_business_timezone())::date`); err != nil {
		return nil, err
	}
	if effectiveFrom.After(TruncateDay(businessDate)) {
		return nil, domain.NewConflict("future-effective capacity feedback must remain DRAFT until its effective date")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "capacity-feedback:"+f.WorkCenterID.String()); err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE capacity_feedback_versions SET status='ARCHIVED',archived_by_user_id=$1,archived_by=$2,archived_at=$3 WHERE work_center_id=$4 AND status='ACTIVE'`, actor.UserID, actor.Username, now, f.WorkCenterID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE capacity_feedback_versions SET status='ACTIVE',notes=$1,activated_by_user_id=$2,activated_by=$3,activated_at=$4 WHERE id=$5`, strings.TrimSpace(notes), actor.UserID, actor.Username, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.rescheduler != nil {
		s.rescheduler.notifyPending(ctx)
	}
	return s.getFeedback(ctx, id)
}

func (s *ProductionPerformanceService) ArchiveFeedback(ctx context.Context, id uuid.UUID, notes string, actor ProductionPerformanceActor) (*domain.CapacityFeedbackVersion, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var f domain.CapacityFeedbackVersion
	if err := tx.GetContext(ctx, &f, `SELECT * FROM capacity_feedback_versions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("capacity feedback version")
		}
		return nil, err
	}
	if f.Status == "ARCHIVED" {
		return nil, domain.NewConflict("capacity feedback is already ARCHIVED")
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE capacity_feedback_versions SET status='ARCHIVED',notes=$1,archived_by_user_id=$2,archived_by=$3,archived_at=$4 WHERE id=$5`, strings.TrimSpace(notes), actor.UserID, actor.Username, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getFeedback(ctx, id)
}

func (s *ProductionPerformanceService) getFeedback(ctx context.Context, id uuid.UUID) (*domain.CapacityFeedbackVersion, error) {
	var f domain.CapacityFeedbackVersion
	if err := s.db.GetContext(ctx, &f, `SELECT f.*,w.code AS work_center_code,w.name AS work_center_name FROM capacity_feedback_versions f JOIN work_centers w ON w.id=f.work_center_id WHERE f.id=$1`, id); err != nil {
		return nil, err
	}
	return &f, nil
}

// ApplyCapacityFeedbackToWorkCenters substitutes planner-activated empirical
// efficiency/utilization values without mutating the Work Center master. The
// returned snapshots are frozen by persisted Detailed Scheduling runs.
func ApplyCapacityFeedbackToWorkCenters(ctx context.Context, db sqlx.QueryerContext, wcs []domain.WorkCenter, asOf time.Time) ([]domain.WorkCenter, []domain.DetailedScheduleCapacityFeedbackSnapshot, error) {
	if asOf.IsZero() {
		asOf = time.Now()
	}
	var rows []domain.CapacityFeedbackVersion
	q := `SELECT f.*,w.code AS work_center_code,w.name AS work_center_name
          FROM capacity_feedback_versions f JOIN work_centers w ON w.id=f.work_center_id
         WHERE f.status='ACTIVE' AND f.effective_from <= $1
         ORDER BY f.work_center_id,f.version_no DESC`
	if err := sqlx.SelectContext(ctx, db, &rows, q, TruncateDay(asOf)); err != nil {
		return nil, nil, err
	}
	latest := map[uuid.UUID]domain.CapacityFeedbackVersion{}
	for _, f := range rows {
		if _, ok := latest[f.WorkCenterID]; !ok {
			latest[f.WorkCenterID] = f
		}
	}
	out := append([]domain.WorkCenter(nil), wcs...)
	snaps := []domain.DetailedScheduleCapacityFeedbackSnapshot{}
	for i := range out {
		f, ok := latest[out[i].ID]
		if !ok {
			continue
		}
		out[i].Efficiency = f.EffectiveEfficiency
		out[i].Utilization = f.EffectiveUtilization
		snaps = append(snaps, domain.DetailedScheduleCapacityFeedbackSnapshot{
			FeedbackVersionID: f.ID, WorkCenterID: f.WorkCenterID, VersionNo: f.VersionNo, SourceRunID: f.SourceRunID, SourceResultID: f.SourceResultID,
			EffectiveEfficiency: f.EffectiveEfficiency, EffectiveUtilization: f.EffectiveUtilization, SourceOEE: f.SourceOEE,
			SourceAvailability: f.SourceAvailability, SourcePerformance: f.SourcePerformance, SourceQuality: f.SourceQuality,
			SampleCount: f.SampleCount, Confidence: f.Confidence, EffectiveFrom: f.EffectiveFrom,
		})
	}
	return out, snaps, nil
}

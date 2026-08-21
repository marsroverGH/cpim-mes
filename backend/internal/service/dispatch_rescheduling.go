package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

// ScheduleExecutionService owns the active execution schedule, real-time
// dispatch projection, adherence evidence, and controlled dynamic rescheduling.
// Detailed Scheduling stays immutable: a reschedule creates a new candidate run
// and only switches the active pointer after frozen-horizon validation succeeds.
type ScheduleExecutionService struct {
	db  *sqlx.DB
	crp *CRPService
}

type ScheduleExecutionActor struct {
	UserID   uuid.UUID
	Username string
	Role     domain.Role
	System   bool
}

func (a ScheduleExecutionActor) validatePlanner() error {
	if a.System {
		return nil
	}
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated dispatch/rescheduling actor required")
	}
	if a.Role != domain.RoleAdmin && a.Role != domain.RolePlanner {
		return domain.NewForbidden("dispatch/rescheduling mutation requires planner/admin")
	}
	return nil
}

func systemScheduleActor() ScheduleExecutionActor {
	return ScheduleExecutionActor{Username: "SYSTEM", System: true}
}

type DispatchPolicyInput struct {
	FreezeMinutes                  int     `json:"freezeMinutes"`
	FirmMinutes                    int     `json:"firmMinutes"`
	StartLateThresholdMinutes      int     `json:"startLateThresholdMinutes"`
	CompletionLateThresholdMinutes int     `json:"completionLateThresholdMinutes"`
	AutoReschedule                 bool    `json:"autoReschedule"`
	MinAutoIntervalMinutes         int     `json:"minAutoIntervalMinutes"`
	SetupMatchBonus                float64 `json:"setupMatchBonus"`
}

type DynamicRescheduleRequest struct {
	TriggerType string    `json:"triggerType"`
	TriggerRef  string    `json:"triggerRef"`
	Reason      string    `json:"reason"`
	AsOf        time.Time `json:"asOf"`
	HorizonDays int       `json:"horizonDays"`
}

type sqlContextReader interface {
	GetContext(context.Context, any, string, ...any) error
	SelectContext(context.Context, any, string, ...any) error
}

func (s *ScheduleExecutionService) CurrentPolicy(ctx context.Context) (*domain.DispatchPolicyVersion, error) {
	var p domain.DispatchPolicyVersion
	if err := s.db.GetContext(ctx, &p, `SELECT * FROM v_current_dispatch_policy`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewConflict("no ACTIVE dispatch policy")
		}
		return nil, err
	}
	return &p, nil
}

func (s *ScheduleExecutionService) ListPolicies(ctx context.Context) ([]domain.DispatchPolicyVersion, error) {
	var rows []domain.DispatchPolicyVersion
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM dispatch_policy_versions ORDER BY version_no DESC`)
	return rows, err
}

func normalizeDispatchPolicyInput(in DispatchPolicyInput) (DispatchPolicyInput, error) {
	if in.FreezeMinutes < 0 || in.FirmMinutes < in.FreezeMinutes {
		return in, domain.NewBadRequest("firmMinutes must be >= freezeMinutes >= 0", nil)
	}
	if in.StartLateThresholdMinutes < 0 || in.CompletionLateThresholdMinutes < 0 || in.MinAutoIntervalMinutes < 0 {
		return in, domain.NewBadRequest("dispatch thresholds must be >= 0", nil)
	}
	if in.SetupMatchBonus < 0 || in.SetupMatchBonus > 100 {
		return in, domain.NewBadRequest("setupMatchBonus must be between 0 and 100", nil)
	}
	return in, nil
}

func (s *ScheduleExecutionService) CreatePolicy(ctx context.Context, in DispatchPolicyInput, actor ScheduleExecutionActor) (*domain.DispatchPolicyVersion, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	in, err := normalizeDispatchPolicyInput(in)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('dispatch-policy-version'))`); err != nil {
		return nil, err
	}
	var version int
	if err := tx.GetContext(ctx, &version, `SELECT COALESCE(MAX(version_no),0)+1 FROM dispatch_policy_versions`); err != nil {
		return nil, err
	}
	row := domain.DispatchPolicyVersion{
		ID: uuid.New(), VersionNo: version, Status: "DRAFT", FreezeMinutes: in.FreezeMinutes, FirmMinutes: in.FirmMinutes,
		StartLateThresholdMinutes: in.StartLateThresholdMinutes, CompletionLateThresholdMinutes: in.CompletionLateThresholdMinutes,
		AutoReschedule: in.AutoReschedule, MinAutoIntervalMinutes: in.MinAutoIntervalMinutes, SetupMatchBonus: in.SetupMatchBonus,
		CreatedByUserID: &actor.UserID, CreatedBy: actor.Username, CreatedAt: time.Now(),
	}
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO dispatch_policy_versions(id,version_no,status,freeze_minutes,firm_minutes,start_late_threshold_minutes,
 completion_late_threshold_minutes,auto_reschedule,min_auto_interval_minutes,setup_match_bonus,created_by_user_id,created_by,created_at)
VALUES(:id,:version_no,:status,:freeze_minutes,:firm_minutes,:start_late_threshold_minutes,
 :completion_late_threshold_minutes,:auto_reschedule,:min_auto_interval_minutes,:setup_match_bonus,:created_by_user_id,:created_by,:created_at)`, &row); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getPolicy(ctx, row.ID)
}

func (s *ScheduleExecutionService) getPolicy(ctx context.Context, id uuid.UUID) (*domain.DispatchPolicyVersion, error) {
	var row domain.DispatchPolicyVersion
	if err := s.db.GetContext(ctx, &row, `SELECT * FROM dispatch_policy_versions WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("dispatch policy version")
		}
		return nil, err
	}
	return &row, nil
}

func (s *ScheduleExecutionService) ActivatePolicy(ctx context.Context, id uuid.UUID, actor ScheduleExecutionActor) (*domain.DispatchPolicyVersion, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('dispatch-policy-version'))`); err != nil {
		return nil, err
	}
	var status string
	if err := tx.GetContext(ctx, &status, `SELECT status FROM dispatch_policy_versions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("dispatch policy version")
		}
		return nil, err
	}
	if status != "DRAFT" {
		return nil, domain.NewConflict("only DRAFT dispatch policy can be activated")
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch_policy_versions SET status='ARCHIVED',archived_by_user_id=$1,archived_by=$2,archived_at=$3 WHERE status='ACTIVE'`, actor.UserID, actor.Username, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dispatch_policy_versions SET status='ACTIVE',activated_by_user_id=$1,activated_by=$2,activated_at=$3 WHERE id=$4`, actor.UserID, actor.Username, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getPolicy(ctx, id)
}

func (s *ScheduleExecutionService) ExecutionState(ctx context.Context) (*domain.DetailedScheduleExecutionState, error) {
	var st domain.DetailedScheduleExecutionState
	if err := s.db.GetContext(ctx, &st, `SELECT active_run_id,policy_version_id,activation_history_id,activated_at,updated_at FROM detailed_schedule_execution_state WHERE singleton=true`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("active execution schedule")
		}
		return nil, err
	}
	return &st, nil
}

func actorDBFields(actor ScheduleExecutionActor) (string, *uuid.UUID, string) {
	if actor.System || actor.UserID == uuid.Nil {
		return "SYSTEM", nil, "SYSTEM"
	}
	id := actor.UserID
	return "USER", &id, actor.Username
}

func activePolicyTx(ctx context.Context, tx *sqlx.Tx) (domain.DispatchPolicyVersion, error) {
	var p domain.DispatchPolicyVersion
	err := tx.GetContext(ctx, &p, `SELECT * FROM v_current_dispatch_policy`)
	return p, err
}

func activateExecutionScheduleTx(ctx context.Context, tx *sqlx.Tx, runID uuid.UUID, rescheduleID *uuid.UUID, expectedPrevious *uuid.UUID, reason string, actor ScheduleExecutionActor) (*domain.DetailedScheduleActivationHistory, error) {
	policy, err := activePolicyTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var previous *uuid.UUID
	var current uuid.UUID
	err = tx.GetContext(ctx, &current, `SELECT active_run_id FROM detailed_schedule_execution_state WHERE singleton=true FOR UPDATE`)
	if err == nil {
		previous = &current
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if expectedPrevious != nil {
		if previous == nil || *previous != *expectedPrevious {
			return nil, domain.NewConflict("active execution schedule changed during rescheduling; retry from latest schedule")
		}
	}
	actorType, actorID, actorName := actorDBFields(actor)
	now := time.Now()
	h := domain.DetailedScheduleActivationHistory{
		ID: uuid.New(), PreviousRunID: previous, ActiveRunID: runID, RescheduleRunID: rescheduleID,
		PolicyVersionID: policy.ID, ActivationReason: strings.TrimSpace(reason), ActorType: actorType,
		ActorUserID: actorID, ActorUsername: actorName, ActivatedAt: now,
	}
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO detailed_schedule_activation_history(id,previous_run_id,active_run_id,reschedule_run_id,policy_version_id,
 activation_reason,actor_type,actor_user_id,actor_username,activated_at)
VALUES(:id,:previous_run_id,:active_run_id,:reschedule_run_id,:policy_version_id,
 :activation_reason,:actor_type,:actor_user_id,:actor_username,:activated_at)`, &h); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO detailed_schedule_execution_state(singleton,active_run_id,policy_version_id,activation_history_id,activated_at,updated_at)
VALUES(true,$1,$2,$3,$4,$4)
ON CONFLICT(singleton) DO UPDATE SET active_run_id=EXCLUDED.active_run_id,policy_version_id=EXCLUDED.policy_version_id,
 activation_history_id=EXCLUDED.activation_history_id,activated_at=EXCLUDED.activated_at,updated_at=EXCLUDED.updated_at`, runID, policy.ID, h.ID, now); err != nil {
		return nil, err
	}
	return &h, nil
}

type dispatchSQLRow struct {
	ScheduleOrderID uuid.UUID  `db:"schedule_order_id"`
	WorkOrderID     uuid.UUID  `db:"work_order_id"`
	WOOperationID   uuid.UUID  `db:"wo_operation_id"`
	OrderNo         string     `db:"order_no"`
	ItemCode        string     `db:"item_code"`
	ItemName        string     `db:"item_name"`
	OperationSeq    int        `db:"operation_seq"`
	OperationDesc   string     `db:"operation_desc"`
	WorkCenterID    uuid.UUID  `db:"work_center_id"`
	WorkCenterCode  string     `db:"work_center_code"`
	WorkCenterName  string     `db:"work_center_name"`
	SetupFamily     string     `db:"setup_family"`
	Priority        int        `db:"priority"`
	DueAt           time.Time  `db:"due_at"`
	PlannedStart    *time.Time `db:"planned_start"`
	PlannedEnd      *time.Time `db:"planned_end"`
	ActualStart     *time.Time `db:"actual_start"`
	ActualEnd       *time.Time `db:"actual_end"`
	OperationStatus string     `db:"operation_status"`
}

const dispatchRowsSQL = `
WITH active AS (
  SELECT active_run_id FROM detailed_schedule_execution_state WHERE singleton=true
), sched AS (
  SELECT d.id AS schedule_order_id,d.work_order_id,d.priority,d.due_at,
         b.operation_seq,MAX(b.operation_desc) AS operation_desc,
         MIN(b.scheduled_start) AS planned_start,MAX(b.scheduled_end) AS planned_end,
         MIN(b.work_center_id::text)::uuid AS work_center_id,MAX(b.setup_family) AS setup_family
    FROM active a
    JOIN detailed_schedule_orders d ON d.run_id=a.active_run_id AND d.work_order_id IS NOT NULL
    JOIN detailed_schedule_batches b ON b.schedule_order_id=d.id
   GROUP BY d.id,d.work_order_id,d.priority,d.due_at,b.operation_seq
)
SELECT s.schedule_order_id,s.work_order_id,op.id AS wo_operation_id,wo.order_no,i.code AS item_code,i.name AS item_name,
       s.operation_seq,s.operation_desc,s.work_center_id,wc.code AS work_center_code,wc.name AS work_center_name,
       s.setup_family,s.priority,s.due_at,s.planned_start,s.planned_end,op.started_at AS actual_start,
       op.completed_at AS actual_end,op.status AS operation_status
  FROM sched s
  JOIN wo_operations op ON op.wo_id=s.work_order_id AND op.seq_no=s.operation_seq
  JOIN work_orders wo ON wo.id=s.work_order_id
  JOIN items i ON i.id=wo.item_id
  JOIN work_centers wc ON wc.id=s.work_center_id
 WHERE ($1::uuid IS NULL OR s.work_center_id=$1)
 ORDER BY wc.code,s.planned_start NULLS LAST,s.priority,wo.order_no,s.operation_seq`

func timeFenceFor(start *time.Time, status string, asOf time.Time, p domain.DispatchPolicyVersion) string {
	if status == "IN_PROGRESS" || status == "PAUSED" || status == "COMPLETED" {
		return "EXECUTED"
	}
	if start == nil {
		return "FLEXIBLE"
	}
	freeze := asOf.Add(time.Duration(p.FreezeMinutes) * time.Minute)
	firm := asOf.Add(time.Duration(p.FirmMinutes) * time.Minute)
	// Missed starts are recovery work rather than immutable frozen future work.
	if start.Before(asOf) {
		return "FIRM"
	}
	if start.Before(freeze) {
		return "FROZEN"
	}
	if start.Before(firm) {
		return "FIRM"
	}
	return "FLEXIBLE"
}

func varianceMinutes(actual *time.Time, planned *time.Time, asOf time.Time, final bool) float64 {
	if planned == nil {
		return 0
	}
	if actual != nil {
		return actual.Sub(*planned).Minutes()
	}
	if final || asOf.After(*planned) {
		return asOf.Sub(*planned).Minutes()
	}
	return 0
}

func dispatchStatusFor(r dispatchSQLRow, asOf time.Time, p domain.DispatchPolicyVersion) (string, string) {
	switch r.OperationStatus {
	case "COMPLETED":
		return "COMPLETED", ""
	case "IN_PROGRESS":
		if r.PlannedEnd != nil && asOf.After(r.PlannedEnd.Add(time.Duration(p.CompletionLateThresholdMinutes)*time.Minute)) {
			return "LATE_COMPLETE", ""
		}
		return "IN_PROCESS", ""
	case "PAUSED":
		return "PAUSED", "operation paused"
	case "PENDING":
		return "BLOCKED", "predecessor/transfer batch not ready"
	case "READY":
		if r.PlannedStart == nil {
			return "BLOCKED", "active schedule has no planned start"
		}
		if asOf.After(r.PlannedStart.Add(time.Duration(p.StartLateThresholdMinutes) * time.Minute)) {
			return "LATE_START", ""
		}
		if !asOf.Before(*r.PlannedStart) {
			return "READY", ""
		}
		return "QUEUED", ""
	default:
		return "BLOCKED", "operation is not executable"
	}
}

func dispatchBaseScore(status, fence string, priority int, setupMatch bool, setupBonus float64, due time.Time, asOf time.Time) float64 {
	base := map[string]float64{
		"LATE_COMPLETE": 11000, "IN_PROCESS": 10000, "PAUSED": 9500, "LATE_START": 9000,
		"READY": 7000, "QUEUED": 3000, "BLOCKED": -1000, "COMPLETED": -5000,
	}[status]
	switch fence {
	case "FROZEN":
		base += 500
	case "FIRM":
		base += 250
	}
	base += math.Max(0, 100-float64(priority))
	if setupMatch {
		base += setupBonus
	}
	if !due.IsZero() {
		days := due.Sub(asOf).Hours() / 24
		if days < 0 {
			base += 500 + math.Min(500, -days*20)
		} else {
			base += math.Max(0, 100-days*5)
		}
	}
	return base
}

func loadDispatchBoard(ctx context.Context, q sqlContextReader, workCenterID *uuid.UUID, asOf time.Time) (*domain.DispatchBoard, error) {
	if asOf.IsZero() {
		asOf = time.Now()
	}
	var p domain.DispatchPolicyVersion
	if err := q.GetContext(ctx, &p, `SELECT * FROM v_current_dispatch_policy`); err != nil {
		return nil, err
	}
	var exec domain.DetailedScheduleExecutionState
	if err := q.GetContext(ctx, &exec, `SELECT active_run_id,policy_version_id,activation_history_id,activated_at,updated_at FROM detailed_schedule_execution_state WHERE singleton=true`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("active execution schedule")
		}
		return nil, err
	}
	var rows []dispatchSQLRow
	var wcArg any
	if workCenterID != nil {
		wcArg = *workCenterID
	}
	if err := q.SelectContext(ctx, &rows, dispatchRowsSQL, wcArg); err != nil {
		return nil, err
	}
	var fams []struct {
		WorkCenterID uuid.UUID `db:"work_center_id"`
		SetupFamily  string    `db:"setup_family"`
	}
	if err := q.SelectContext(ctx, &fams, `
SELECT DISTINCT ON (work_center_id) work_center_id,setup_family
  FROM wo_operations
 WHERE status IN ('IN_PROGRESS','PAUSED','COMPLETED') AND COALESCE(setup_family,'')<>''
 ORDER BY work_center_id,COALESCE(completed_at,active_started_at,started_at,created_at) DESC,id DESC`); err != nil {
		return nil, err
	}
	lastFamily := map[uuid.UUID]string{}
	for _, f := range fams {
		lastFamily[f.WorkCenterID] = strings.TrimSpace(f.SetupFamily)
	}
	out := &domain.DispatchBoard{AsOf: asOf, Policy: p, Execution: exec, Items: []domain.DispatchItem{}}
	for _, r := range rows {
		status, blocked := dispatchStatusFor(r, asOf, p)
		fence := timeFenceFor(r.PlannedStart, r.OperationStatus, asOf, p)
		setupMatch := r.SetupFamily != "" && lastFamily[r.WorkCenterID] == strings.TrimSpace(r.SetupFamily)
		item := domain.DispatchItem{
			ActiveRunID: exec.ActiveRunID, ScheduleOrderID: r.ScheduleOrderID, WorkOrderID: r.WorkOrderID, WOOperationID: r.WOOperationID,
			OrderNo: r.OrderNo, ItemCode: r.ItemCode, ItemName: r.ItemName, OperationSeq: r.OperationSeq, OperationDesc: r.OperationDesc,
			WorkCenterID: r.WorkCenterID, WorkCenterCode: r.WorkCenterCode, WorkCenterName: r.WorkCenterName, SetupFamily: r.SetupFamily,
			Priority: r.Priority, DueAt: r.DueAt, PlannedStart: r.PlannedStart, PlannedEnd: r.PlannedEnd, ActualStart: r.ActualStart, ActualEnd: r.ActualEnd,
			OperationStatus: r.OperationStatus, TimeFence: fence, DispatchStatus: status, BlockedReason: blocked,
			StartVarianceMin:      varianceMinutes(r.ActualStart, r.PlannedStart, asOf, false),
			CompletionVarianceMin: varianceMinutes(r.ActualEnd, r.PlannedEnd, asOf, r.OperationStatus == "COMPLETED"), SetupMatch: setupMatch,
		}
		item.DispatchScore = dispatchBaseScore(status, fence, r.Priority, setupMatch, p.SetupMatchBonus, r.DueAt, asOf)
		out.Items = append(out.Items, item)
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if math.Abs(out.Items[i].DispatchScore-out.Items[j].DispatchScore) > 1e-9 {
			return out.Items[i].DispatchScore > out.Items[j].DispatchScore
		}
		if out.Items[i].PlannedStart == nil {
			return false
		}
		if out.Items[j].PlannedStart == nil {
			return true
		}
		return out.Items[i].PlannedStart.Before(*out.Items[j].PlannedStart)
	})
	return out, nil
}

func (s *ScheduleExecutionService) Dispatch(ctx context.Context, workCenterID *uuid.UUID, asOf time.Time) (*domain.DispatchBoard, error) {
	return loadDispatchBoard(ctx, s.db, workCenterID, asOf)
}

func adherenceSummary(rows []domain.ScheduleAdherenceRow) domain.ScheduleAdherenceSummary {
	var out domain.ScheduleAdherenceSummary
	var startSum, completionSum float64
	var startVarianceDen, completionVarianceDen int
	var startEligible, completionEligible int
	out.TotalOperations = len(rows)
	for _, r := range rows {
		// Future untouched operations are not yet adherence observations. Count an
		// operation only once it actually starts/completes or once its threshold is
		// missed. This prevents distant future work from inflating on-time rates.
		if r.PlannedStart != nil && (r.ActualStart != nil || !r.StartOnTime) {
			startEligible++
		}
		if r.PlannedEnd != nil && (r.ActualEnd != nil || !r.CompletionOnTime) {
			completionEligible++
		}
		if r.ActualStart != nil {
			out.StartedOperations++
			startSum += r.StartVarianceMinutes
			startVarianceDen++
		}
		if r.ActualEnd != nil {
			out.CompletedOperations++
			completionSum += r.CompletionVarianceMinutes
			completionVarianceDen++
		}
		if !r.StartOnTime {
			out.LateStarts++
		}
		if !r.CompletionOnTime {
			out.LateCompletions++
		}
		if r.DispatchStatus == "BLOCKED" || r.DispatchStatus == "PAUSED" {
			out.BlockedOperations++
		}
	}
	if startEligible > 0 {
		out.OnTimeStartPct = 100 * float64(maxInt(startEligible-out.LateStarts, 0)) / float64(startEligible)
	}
	if completionEligible > 0 {
		out.OnTimeCompletionPct = 100 * float64(maxInt(completionEligible-out.LateCompletions, 0)) / float64(completionEligible)
	}
	if startVarianceDen > 0 {
		out.AverageStartVariance = startSum / float64(startVarianceDen)
	}
	if completionVarianceDen > 0 {
		out.AverageCompletionVariance = completionSum / float64(completionVarianceDen)
	}
	return out
}

func canonicalAdherenceHash(activeRun uuid.UUID, asOf time.Time, rows []domain.ScheduleAdherenceRow) (string, error) {
	type canonical struct {
		WorkOrderID        uuid.UUID  `json:"workOrderId"`
		OperationSeq       int        `json:"operationSeq"`
		PlannedStart       *time.Time `json:"plannedStart,omitempty"`
		PlannedEnd         *time.Time `json:"plannedEnd,omitempty"`
		ActualStart        *time.Time `json:"actualStart,omitempty"`
		ActualEnd          *time.Time `json:"actualEnd,omitempty"`
		Status             string     `json:"status"`
		StartVariance      float64    `json:"startVariance"`
		CompletionVariance float64    `json:"completionVariance"`
	}
	xs := make([]canonical, 0, len(rows))
	for _, r := range rows {
		xs = append(xs, canonical{r.WorkOrderID, r.OperationSeq, r.PlannedStart, r.PlannedEnd, r.ActualStart, r.ActualEnd, r.OperationStatus, r.StartVarianceMinutes, r.CompletionVarianceMinutes})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].WorkOrderID != xs[j].WorkOrderID {
			return xs[i].WorkOrderID.String() < xs[j].WorkOrderID.String()
		}
		return xs[i].OperationSeq < xs[j].OperationSeq
	})
	b, err := json.Marshal(struct {
		ActiveRun uuid.UUID   `json:"activeRun"`
		AsOf      string      `json:"asOf"`
		Rows      []canonical `json:"rows"`
	}{activeRun, asOf.UTC().Format(time.RFC3339Nano), xs})
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func currentAdherenceWithReader(ctx context.Context, q sqlContextReader, asOf time.Time) (*domain.ScheduleAdherenceResult, error) {
	board, err := loadDispatchBoard(ctx, q, nil, asOf)
	if err != nil {
		return nil, err
	}
	rows := make([]domain.ScheduleAdherenceRow, 0, len(board.Items))
	for _, x := range board.Items {
		startOnTime := x.ActualStart == nil || x.StartVarianceMin <= float64(board.Policy.StartLateThresholdMinutes)
		if x.ActualStart == nil && x.PlannedStart != nil && board.AsOf.After(x.PlannedStart.Add(time.Duration(board.Policy.StartLateThresholdMinutes)*time.Minute)) {
			startOnTime = false
		}
		completionOnTime := x.ActualEnd == nil || x.CompletionVarianceMin <= float64(board.Policy.CompletionLateThresholdMinutes)
		if x.ActualEnd == nil && x.PlannedEnd != nil && board.AsOf.After(x.PlannedEnd.Add(time.Duration(board.Policy.CompletionLateThresholdMinutes)*time.Minute)) {
			completionOnTime = false
		}
		rows = append(rows, domain.ScheduleAdherenceRow{
			ID: uuid.New(), ScheduleOrderID: x.ScheduleOrderID, WorkOrderID: x.WorkOrderID, WOOperationID: x.WOOperationID,
			WorkCenterID: x.WorkCenterID, OperationSeq: x.OperationSeq, PlannedStart: x.PlannedStart, PlannedEnd: x.PlannedEnd,
			ActualStart: x.ActualStart, ActualEnd: x.ActualEnd, OperationStatus: x.OperationStatus,
			StartVarianceMinutes: x.StartVarianceMin, CompletionVarianceMinutes: x.CompletionVarianceMin,
			StartOnTime: startOnTime, CompletionOnTime: completionOnTime, TimeFence: x.TimeFence, DispatchStatus: x.DispatchStatus, BlockedReason: x.BlockedReason,
		})
	}
	hash, err := canonicalAdherenceHash(board.Execution.ActiveRunID, board.AsOf, rows)
	if err != nil {
		return nil, err
	}
	return &domain.ScheduleAdherenceResult{
		Snapshot: domain.ScheduleAdherenceSnapshot{ActiveRunID: board.Execution.ActiveRunID, PolicyVersionID: board.Policy.ID, AsOf: board.AsOf, Status: "COMPLETE", ResultHash: hash},
		Summary:  adherenceSummary(rows), Rows: rows,
	}, nil
}

func (s *ScheduleExecutionService) CurrentAdherence(ctx context.Context, asOf time.Time) (*domain.ScheduleAdherenceResult, error) {
	return currentAdherenceWithReader(ctx, s.db, asOf)
}

func (s *ScheduleExecutionService) SnapshotAdherence(ctx context.Context, asOf time.Time, actor ScheduleExecutionActor) (*domain.ScheduleAdherenceResult, error) {
	if !actor.System {
		if err := actor.validatePlanner(); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := currentAdherenceWithReader(ctx, tx, asOf)
	if err != nil {
		return nil, err
	}
	res.Snapshot.ID = uuid.New()
	if actor.System {
		res.Snapshot.GeneratedBy = "SYSTEM"
	} else {
		id := actor.UserID
		res.Snapshot.GeneratedByUserID = &id
		res.Snapshot.GeneratedBy = actor.Username
	}
	res.Snapshot.CreatedAt = time.Now()
	for i := range res.Rows {
		res.Rows[i].SnapshotID = res.Snapshot.ID
	}
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO schedule_adherence_snapshots(id,active_run_id,policy_version_id,as_of,status,result_hash,generated_by_user_id,generated_by,created_at)
VALUES(:id,:active_run_id,:policy_version_id,:as_of,:status,:result_hash,:generated_by_user_id,:generated_by,:created_at)`, &res.Snapshot); err != nil {
		return nil, err
	}
	for i := range res.Rows {
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO schedule_adherence_rows(id,snapshot_id,schedule_order_id,work_order_id,wo_operation_id,work_center_id,operation_seq,
 planned_start,planned_end,actual_start,actual_end,operation_status,start_variance_minutes,completion_variance_minutes,
 start_on_time,completion_on_time,time_fence,dispatch_status,blocked_reason)
VALUES(:id,:snapshot_id,:schedule_order_id,:work_order_id,:wo_operation_id,:work_center_id,:operation_seq,
 :planned_start,:planned_end,:actual_start,:actual_end,:operation_status,:start_variance_minutes,:completion_variance_minutes,
 :start_on_time,:completion_on_time,:time_fence,:dispatch_status,:blocked_reason)`, &res.Rows[i]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *ScheduleExecutionService) ListAdherenceSnapshots(ctx context.Context) ([]domain.ScheduleAdherenceSnapshot, error) {
	var rows []domain.ScheduleAdherenceSnapshot
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM schedule_adherence_snapshots ORDER BY as_of DESC,id DESC LIMIT 100`)
	return rows, err
}

func (s *ScheduleExecutionService) GetAdherenceSnapshot(ctx context.Context, id uuid.UUID) (*domain.ScheduleAdherenceResult, error) {
	var snap domain.ScheduleAdherenceSnapshot
	if err := s.db.GetContext(ctx, &snap, `SELECT * FROM schedule_adherence_snapshots WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("schedule adherence snapshot")
		}
		return nil, err
	}
	var rows []domain.ScheduleAdherenceRow
	if err := s.db.SelectContext(ctx, &rows, `SELECT * FROM schedule_adherence_rows WHERE snapshot_id=$1 ORDER BY work_center_id,operation_seq,work_order_id`, id); err != nil {
		return nil, err
	}
	return &domain.ScheduleAdherenceResult{Snapshot: snap, Summary: adherenceSummary(rows), Rows: rows}, nil
}

type operationScheduleEvidence struct {
	SourceRef    string     `db:"source_ref"`
	WorkOrderID  *uuid.UUID `db:"work_order_id"`
	OperationSeq int        `db:"operation_seq"`
	WorkCenterID *uuid.UUID `db:"work_center_id"`
	Start        *time.Time `db:"scheduled_start"`
	End          *time.Time `db:"scheduled_end"`
}

func loadOperationScheduleEvidence(ctx context.Context, q sqlContextReader, runID uuid.UUID) ([]operationScheduleEvidence, error) {
	var rows []operationScheduleEvidence
	err := q.SelectContext(ctx, &rows, `
SELECT d.source_ref,d.work_order_id,b.operation_seq,
       MIN(b.work_center_id::text)::uuid AS work_center_id,MIN(b.scheduled_start) AS scheduled_start,MAX(b.scheduled_end) AS scheduled_end
  FROM detailed_schedule_orders d JOIN detailed_schedule_batches b ON b.schedule_order_id=d.id
 WHERE d.run_id=$1
 GROUP BY d.source_ref,d.work_order_id,b.operation_seq
 ORDER BY d.source_ref,b.operation_seq`, runID)
	return rows, err
}

func scheduleEvidenceKey(x operationScheduleEvidence) string {
	if x.WorkOrderID != nil {
		return "WO:" + x.WorkOrderID.String() + ":" + fmt.Sprint(x.OperationSeq)
	}
	return "REF:" + x.SourceRef + ":" + fmt.Sprint(x.OperationSeq)
}

func timesEqualMinute(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(a.Sub(*b).Minutes()) < 0.5
}

func uuidPtrsEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func timeShiftMinutes(old, neu *time.Time) float64 {
	if old == nil || neu == nil {
		return 0
	}
	return neu.Sub(*old).Minutes()
}

func canonicalRescheduleHash(changes []domain.DynamicRescheduleChange) (string, error) {
	type c struct {
		SourceRef         string `json:"sourceRef"`
		OperationSeq      int    `json:"operationSeq"`
		ChangeType        string `json:"changeType"`
		TimeFence         string `json:"timeFence"`
		OldWC             string `json:"oldWc"`
		NewWC             string `json:"newWc"`
		OldStart          string `json:"oldStart"`
		OldEnd            string `json:"oldEnd"`
		NewStart          string `json:"newStart"`
		NewEnd            string `json:"newEnd"`
		FrozenConflict    bool   `json:"frozenConflict"`
		ExecutionConflict bool   `json:"executionConflict"`
	}
	strTime := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	strUUID := func(v *uuid.UUID) string {
		if v == nil {
			return ""
		}
		return v.String()
	}
	xs := make([]c, 0, len(changes))
	for _, x := range changes {
		xs = append(xs, c{x.SourceRef, x.OperationSeq, x.ChangeType, x.TimeFence, strUUID(x.OldWorkCenterID), strUUID(x.NewWorkCenterID), strTime(x.OldStart), strTime(x.OldEnd), strTime(x.NewStart), strTime(x.NewEnd), x.FrozenConflict, x.ExecutionConflict})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].SourceRef != xs[j].SourceRef {
			return xs[i].SourceRef < xs[j].SourceRef
		}
		return xs[i].OperationSeq < xs[j].OperationSeq
	})
	b, err := json.Marshal(xs)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (s *ScheduleExecutionService) compareSchedules(ctx context.Context, sourceRun, candidateRun uuid.UUID, asOf time.Time, p domain.DispatchPolicyVersion, rescheduleID uuid.UUID) ([]domain.DynamicRescheduleChange, error) {
	oldRows, err := loadOperationScheduleEvidence(ctx, s.db, sourceRun)
	if err != nil {
		return nil, err
	}
	newRows, err := loadOperationScheduleEvidence(ctx, s.db, candidateRun)
	if err != nil {
		return nil, err
	}
	oldMap, newMap := map[string]operationScheduleEvidence{}, map[string]operationScheduleEvidence{}
	keys := map[string]bool{}
	for _, x := range oldRows {
		k := scheduleEvidenceKey(x)
		oldMap[k] = x
		keys[k] = true
	}
	for _, x := range newRows {
		k := scheduleEvidenceKey(x)
		newMap[k] = x
		keys[k] = true
	}
	var opStatuses []struct {
		WorkOrderID uuid.UUID `db:"wo_id"`
		Seq         int       `db:"seq_no"`
		Status      string    `db:"status"`
	}
	if err := s.db.SelectContext(ctx, &opStatuses, `SELECT wo_id,seq_no,status FROM wo_operations`); err != nil {
		return nil, err
	}
	statuses := map[string]string{}
	for _, x := range opStatuses {
		statuses["WO:"+x.WorkOrderID.String()+":"+fmt.Sprint(x.Seq)] = x.Status
	}
	freezeUntil := asOf.Add(time.Duration(p.FreezeMinutes) * time.Minute)
	firmUntil := asOf.Add(time.Duration(p.FirmMinutes) * time.Minute)
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	changes := []domain.DynamicRescheduleChange{}
	for _, k := range ordered {
		o, ook := oldMap[k]
		n, nok := newMap[k]
		if ook && nok && uuidPtrsEqual(o.WorkCenterID, n.WorkCenterID) && timesEqualMinute(o.Start, n.Start) && timesEqualMinute(o.End, n.End) {
			continue
		}
		x := domain.DynamicRescheduleChange{ID: uuid.New(), RescheduleRunID: rescheduleID, SourceRef: o.SourceRef, OperationSeq: o.OperationSeq, Detail: json.RawMessage(`{}`), CreatedAt: time.Now()}
		if !ook {
			x.SourceRef = n.SourceRef
			x.OperationSeq = n.OperationSeq
			x.WorkOrderID = n.WorkOrderID
			x.NewWorkCenterID = n.WorkCenterID
			x.NewStart = n.Start
			x.NewEnd = n.End
			x.ChangeType = "ADDED"
		} else if !nok {
			x.WorkOrderID = o.WorkOrderID
			x.OldWorkCenterID = o.WorkCenterID
			x.OldStart = o.Start
			x.OldEnd = o.End
			x.ChangeType = "REMOVED"
		} else {
			x.WorkOrderID = o.WorkOrderID
			x.OldWorkCenterID = o.WorkCenterID
			x.NewWorkCenterID = n.WorkCenterID
			x.OldStart = o.Start
			x.OldEnd = o.End
			x.NewStart = n.Start
			x.NewEnd = n.End
			wcChanged := !uuidPtrsEqual(o.WorkCenterID, n.WorkCenterID)
			timeChanged := !timesEqualMinute(o.Start, n.Start) || !timesEqualMinute(o.End, n.End)
			switch {
			case wcChanged && timeChanged:
				x.ChangeType = "TIME_AND_WORK_CENTER"
			case wcChanged:
				x.ChangeType = "WORK_CENTER_CHANGE"
			default:
				x.ChangeType = "TIME_SHIFT"
			}
			x.StartShiftMinutes = timeShiftMinutes(o.Start, n.Start)
			x.EndShiftMinutes = timeShiftMinutes(o.End, n.End)
		}
		status := statuses[k]
		if status == "IN_PROGRESS" || status == "PAUSED" || status == "COMPLETED" {
			x.TimeFence = "EXECUTED"
		} else {
			anchor := o.Start
			if anchor == nil {
				anchor = n.Start
			}
			switch {
			case anchor != nil && !anchor.Before(asOf) && anchor.Before(freezeUntil):
				x.TimeFence = "FROZEN"
			case anchor != nil && anchor.Before(firmUntil):
				x.TimeFence = "FIRM"
			default:
				x.TimeFence = "FLEXIBLE"
			}
		}
		x.FrozenConflict = ook && x.TimeFence == "FROZEN"
		x.ExecutionConflict = ook && x.TimeFence == "EXECUTED"
		changes = append(changes, x)
	}
	return changes, nil
}

var allowedRescheduleTriggers = map[string]bool{
	"MANUAL": true, "SHOP_FLOOR_PROGRESS": true, "LATE_OPERATION": true, "BREAKDOWN": true, "UNPLANNED_DOWNTIME": true,
	"MAINTENANCE_CHANGE": true, "CAPACITY_FEEDBACK_CHANGE": true, "QUALITY_HOLD": true, "MATERIAL_SHORTAGE": true, "PRIORITY_CHANGE": true,
}

func normalizeRescheduleTrigger(v string) (string, error) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		v = "MANUAL"
	}
	if !allowedRescheduleTriggers[v] {
		return "", domain.NewBadRequest("invalid reschedule triggerType", nil)
	}
	return v, nil
}

func (s *ScheduleExecutionService) markRescheduleFailed(ctx context.Context, id uuid.UUID) {
	now := time.Now()
	_, _ = s.db.ExecContext(ctx, `UPDATE dynamic_reschedule_runs SET status='FAILED',finished_at=$2 WHERE id=$1 AND status='EVALUATING'`, id, now)
}

func (s *ScheduleExecutionService) Reschedule(ctx context.Context, in DynamicRescheduleRequest, actor ScheduleExecutionActor) (*domain.DynamicRescheduleResult, error) {
	if err := actor.validatePlanner(); err != nil {
		return nil, err
	}
	trigger, err := normalizeRescheduleTrigger(in.TriggerType)
	if err != nil {
		return nil, err
	}
	if in.AsOf.IsZero() {
		in.AsOf = time.Now()
	}
	if in.HorizonDays <= 0 {
		in.HorizonDays = 28
	}
	if in.HorizonDays > 366 {
		return nil, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	state, err := s.ExecutionState(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := s.CurrentPolicy(ctx)
	if err != nil {
		return nil, err
	}
	adherence, err := s.SnapshotAdherence(ctx, in.AsOf, actor)
	if err != nil {
		return nil, err
	}
	actorType, actorID, actorName := actorDBFields(actor)
	run := domain.DynamicRescheduleRun{ID: uuid.New(), SourceRunID: state.ActiveRunID, PolicyVersionID: policy.ID, AdherenceSnapshotID: &adherence.Snapshot.ID,
		TriggerType: trigger, TriggerRef: strings.TrimSpace(in.TriggerRef), Reason: strings.TrimSpace(in.Reason), AsOf: in.AsOf,
		FreezeUntil: in.AsOf.Add(time.Duration(policy.FreezeMinutes) * time.Minute), FirmUntil: in.AsOf.Add(time.Duration(policy.FirmMinutes) * time.Minute), HorizonDays: in.HorizonDays,
		Status: "EVALUATING", ActorType: actorType, ActorUserID: actorID, ActorUsername: actorName, CreatedAt: time.Now()}
	if _, err := s.db.NamedExecContext(ctx, `INSERT INTO dynamic_reschedule_runs(id,source_run_id,policy_version_id,adherence_snapshot_id,trigger_type,trigger_ref,reason,as_of,freeze_until,firm_until,horizon_days,status,actor_type,actor_user_id,actor_username,created_at)
VALUES(:id,:source_run_id,:policy_version_id,:adherence_snapshot_id,:trigger_type,:trigger_ref,:reason,:as_of,:freeze_until,:firm_until,:horizon_days,:status,:actor_type,:actor_user_id,:actor_username,:created_at)`, &run); err != nil {
		return nil, err
	}
	candidate, err := s.crp.DetailedSchedule(ctx, DetailedScheduleRequest{StartDate: TruncateDay(in.AsOf), NotBefore: in.AsOf, HorizonDays: in.HorizonDays, SimulateMRP: true, CandidateOnly: true}, CRPActor{Username: actorName})
	if err != nil {
		s.markRescheduleFailed(ctx, run.ID)
		return nil, err
	}
	run.CandidateRunID = &candidate.Run.ID
	changes, err := s.compareSchedules(ctx, state.ActiveRunID, candidate.Run.ID, in.AsOf, *policy, run.ID)
	if err != nil {
		s.markRescheduleFailed(ctx, run.ID)
		return nil, err
	}
	frozen, executed, firm, flex := 0, 0, 0, 0
	impacted := map[uuid.UUID]bool{}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		s.markRescheduleFailed(ctx, run.ID)
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	failTx := func(cause error) (*domain.DynamicRescheduleResult, error) {
		_ = tx.Rollback()
		s.markRescheduleFailed(ctx, run.ID)
		return nil, cause
	}
	for i := range changes {
		x := &changes[i]
		if x.FrozenConflict {
			frozen++
		}
		if x.ExecutionConflict {
			executed++
		}
		switch x.TimeFence {
		case "FIRM":
			firm++
		case "FLEXIBLE":
			flex++
		}
		if x.WorkOrderID != nil {
			impacted[*x.WorkOrderID] = true
		}
		if _, err := tx.NamedExecContext(ctx, `INSERT INTO dynamic_reschedule_changes(id,reschedule_run_id,work_order_id,source_ref,operation_seq,change_type,time_fence,old_work_center_id,new_work_center_id,old_start,old_end,new_start,new_end,start_shift_minutes,end_shift_minutes,frozen_conflict,execution_conflict,detail,created_at)
VALUES(:id,:reschedule_run_id,:work_order_id,:source_ref,:operation_seq,:change_type,:time_fence,:old_work_center_id,:new_work_center_id,:old_start,:old_end,:new_start,:new_end,:start_shift_minutes,:end_shift_minutes,:frozen_conflict,:execution_conflict,:detail,:created_at)`, x); err != nil {
			return failTx(err)
		}
	}
	hash, err := canonicalRescheduleHash(changes)
	if err != nil {
		return failTx(err)
	}
	now := time.Now()
	status := "NO_CHANGE"
	if len(changes) > 0 && (frozen > 0 || executed > 0) {
		status = "BLOCKED"
	} else if len(changes) > 0 {
		status = "ACTIVATED"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dynamic_reschedule_runs SET candidate_run_id=$2,status=$3,frozen_conflicts=$4,execution_conflicts=$5,firm_changes=$6,flexible_changes=$7,impacted_work_orders=$8,result_hash=$9,finished_at=$10 WHERE id=$1`, run.ID, candidate.Run.ID, status, frozen, executed, firm, flex, len(impacted), hash, now); err != nil {
		return failTx(err)
	}
	var activation *domain.DetailedScheduleActivationHistory
	if status == "ACTIVATED" {
		activation, err = activateExecutionScheduleTx(ctx, tx, candidate.Run.ID, &run.ID, &state.ActiveRunID, "DYNAMIC_RESCHEDULE:"+trigger, actor)
		if err != nil {
			return failTx(err)
		}
	}
	if err := tx.Commit(); err != nil {
		s.markRescheduleFailed(ctx, run.ID)
		return nil, err
	}
	run.CandidateRunID = &candidate.Run.ID
	run.Status = status
	run.FrozenConflicts = frozen
	run.ExecutionConflicts = executed
	run.FirmChanges = firm
	run.FlexibleChanges = flex
	run.ImpactedWorkOrders = len(impacted)
	run.ResultHash = &hash
	run.FinishedAt = &now
	return &domain.DynamicRescheduleResult{Run: run, Changes: changes, Adherence: adherence, Activation: activation}, nil
}

func (s *ScheduleExecutionService) ListRescheduleRuns(ctx context.Context) ([]domain.DynamicRescheduleRun, error) {
	var rows []domain.DynamicRescheduleRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM dynamic_reschedule_runs ORDER BY created_at DESC,id DESC LIMIT 100`)
	return rows, err
}

func (s *ScheduleExecutionService) GetRescheduleRun(ctx context.Context, id uuid.UUID) (*domain.DynamicRescheduleResult, error) {
	var run domain.DynamicRescheduleRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM dynamic_reschedule_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("dynamic reschedule run")
		}
		return nil, err
	}
	var changes []domain.DynamicRescheduleChange
	if err := s.db.SelectContext(ctx, &changes, `SELECT * FROM dynamic_reschedule_changes WHERE reschedule_run_id=$1 ORDER BY time_fence,source_ref,operation_seq`, id); err != nil {
		return nil, err
	}
	var activation domain.DetailedScheduleActivationHistory
	var ap *domain.DetailedScheduleActivationHistory
	if err := s.db.GetContext(ctx, &activation, `SELECT * FROM detailed_schedule_activation_history WHERE reschedule_run_id=$1 ORDER BY activated_at DESC LIMIT 1`, id); err == nil {
		ap = &activation
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &domain.DynamicRescheduleResult{Run: run, Changes: changes, Activation: ap}, nil
}

func chooseSignalTrigger(xs []domain.ScheduleRescheduleSignal) string {
	rank := map[string]int{"BREAKDOWN": 1, "UNPLANNED_DOWNTIME": 2, "LATE_OPERATION": 3, "MAINTENANCE_CHANGE": 4, "CAPACITY_FEEDBACK_CHANGE": 5, "MATERIAL_SHORTAGE": 6, "QUALITY_HOLD": 7, "PRIORITY_CHANGE": 8, "SHOP_FLOOR_PROGRESS": 9}
	best := "SHOP_FLOOR_PROGRESS"
	br := 99
	for _, x := range xs {
		if r, ok := rank[x.TriggerType]; ok && r < br {
			best = x.TriggerType
			br = r
		}
	}
	return best
}

func (s *ScheduleExecutionService) PendingSignals(ctx context.Context) ([]domain.ScheduleRescheduleSignal, error) {
	var rows []domain.ScheduleRescheduleSignal
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM schedule_reschedule_signals WHERE processed_at IS NULL ORDER BY detected_at,id LIMIT 100`)
	return rows, err
}

// ProcessPendingSignals is the autonomous bridge from Shop Floor / maintenance /
// capacity-feedback transactions to a new execution schedule. Callers deliberately
// ignore its error after their primary transaction commits: the immutable pending
// signal remains available for retry and no production actual is rolled back.
func (s *ScheduleExecutionService) ProcessPendingSignals(ctx context.Context) (*domain.DynamicRescheduleResult, error) {
	p, err := s.CurrentPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if !p.AutoReschedule {
		return nil, nil
	}
	xs, err := s.PendingSignals(ctx)
	if err != nil || len(xs) == 0 {
		return nil, err
	}
	if _, err := s.ExecutionState(ctx); err != nil {
		return nil, nil
	}
	var last sql.NullTime
	_ = s.db.GetContext(ctx, &last, `SELECT MAX(finished_at) FROM dynamic_reschedule_runs WHERE status='ACTIVATED' AND actor_type='SYSTEM'`)
	if last.Valid && time.Since(last.Time) < time.Duration(p.MinAutoIntervalMinutes)*time.Minute {
		return nil, nil
	}
	refs := make([]string, 0, len(xs))
	for _, x := range xs {
		refs = append(refs, x.SourceRef)
	}
	res, err := s.Reschedule(ctx, DynamicRescheduleRequest{TriggerType: chooseSignalTrigger(xs), TriggerRef: strings.Join(refs, ","), Reason: "automatic reschedule from pending execution signals", AsOf: time.Now(), HorizonDays: 28}, systemScheduleActor())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, x := range xs {
		if _, err := tx.ExecContext(ctx, `UPDATE schedule_reschedule_signals SET processed_at=$2,processed_run_id=$3 WHERE id=$1 AND processed_at IS NULL`, x.ID, now, res.Run.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *ScheduleExecutionService) notifyPending(ctx context.Context) {
	_, _ = s.ProcessPendingSignals(ctx)
}

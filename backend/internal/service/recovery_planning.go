package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// RecoveryPlanningService orchestrates side-effect-free What-if simulation.
//
// It reads operational planning evidence but writes only recovery_* evidence
// and the recovery scenario lifecycle itself.
type RecoveryPlanningService struct {
	db *sqlx.DB
}

func NewRecoveryPlanningService(
	db *sqlx.DB,
) *RecoveryPlanningService {
	return &RecoveryPlanningService{
		db: db,
	}
}

type RecoveryPlanningActor struct {
	UserID   uuid.UUID
	Username string
	Role     string
}

type RecoverySimulationRequest struct {
	ScenarioID  uuid.UUID `json:"scenarioId"`
	HorizonDays int       `json:"horizonDays"`
}

type RecoverySimulationExecution struct {
	Run domain.RecoveryScenarioRun `json:"run"`

	Summary domain.RecoveryScenarioSummary `json:"summary"`

	Cases []domain.RecoveryScenarioCaseResult `json:"cases"`

	Actions []domain.RecoveryScenarioActionResult `json:"actions"`

	Reused bool `json:"reused"`
}

func validateRecoveryPlanningActor(
	actor RecoveryPlanningActor,
) error {
	role := strings.ToLower(
		strings.TrimSpace(actor.Role),
	)

	if role != "planner" &&
		role != "admin" {
		return domain.NewUnauthorized(
			"recovery planning requires planner/admin",
		)
	}

	if strings.TrimSpace(actor.Username) == "" {
		return domain.NewUnauthorized(
			"authenticated recovery planning actor is required",
		)
	}

	return nil
}

func normalizeRecoverySimulationRequest(
	in RecoverySimulationRequest,
) (RecoverySimulationRequest, error) {
	if in.ScenarioID == uuid.Nil {
		return in, domain.NewBadRequest(
			"scenarioId is required",
			nil,
		)
	}

	if in.HorizonDays == 0 {
		in.HorizonDays = 90
	}

	if in.HorizonDays < 1 ||
		in.HorizonDays > 730 {
		return in, domain.NewBadRequest(
			"horizonDays must be between 1 and 730",
			nil,
		)
	}

	return in, nil
}

func (s *RecoveryPlanningService) loadExecutionTx(
	ctx context.Context,
	tx *sqlx.Tx,
	run domain.RecoveryScenarioRun,
	reused bool,
) (*RecoverySimulationExecution, error) {
	var summary domain.RecoveryScenarioSummary

	if err := tx.GetContext(
		ctx,
		&summary,
		`
SELECT *
FROM recovery_scenario_summaries
WHERE run_id=$1
`,
		run.ID,
	); err != nil {
		return nil, err
	}

	cases := []domain.RecoveryScenarioCaseResult{}

	if err := tx.SelectContext(
		ctx,
		&cases,
		`
SELECT *
FROM recovery_scenario_case_results
WHERE run_id=$1
ORDER BY case_id
`,
		run.ID,
	); err != nil {
		return nil, err
	}

	actions := []domain.RecoveryScenarioActionResult{}

	if err := tx.SelectContext(
		ctx,
		&actions,
		`
SELECT *
FROM recovery_scenario_action_results
WHERE run_id=$1
ORDER BY action_id
`,
		run.ID,
	); err != nil {
		return nil, err
	}

	return &RecoverySimulationExecution{
		Run:     run,
		Summary: summary,
		Cases:   cases,
		Actions: actions,
		Reused:  reused,
	}, nil
}

// Simulate executes one immutable What-if evaluation.
//
// Operational tables are read only. The transaction writes exclusively:
//
//	recovery_scenario_runs
//	recovery_scenario_case_results
//	recovery_scenario_action_results
//	recovery_scenario_summaries
//	recovery_scenarios (lifecycle only)
func (s *RecoveryPlanningService) Simulate(
	ctx context.Context,
	in RecoverySimulationRequest,
	actor RecoveryPlanningActor,
) (*RecoverySimulationExecution, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err := validateRecoveryPlanningActor(
		actor,
	); err != nil {
		return nil, err
	}

	in, err := normalizeRecoverySimulationRequest(
		in,
	)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		&sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
		},
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var scenario domain.RecoveryScenario

	err = tx.GetContext(
		ctx,
		&scenario,
		`
SELECT *
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		in.ScenarioID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound(
				"Recovery Scenario",
			)
		}

		return nil, err
	}

	switch scenario.Status {
	case "DRAFT", "SIMULATED":
		// allowed

	case "PUBLISHED":
		return nil, domain.NewBadRequest(
			"published recovery scenario cannot be simulated again",
			nil,
		)

	case "ARCHIVED":
		return nil, domain.NewBadRequest(
			"archived recovery scenario cannot be simulated",
			nil,
		)

	default:
		return nil, domain.NewBadRequest(
			"invalid recovery scenario status",
			nil,
		)
	}

	actions := []domain.RecoveryScenarioAction{}

	if err := tx.SelectContext(
		ctx,
		&actions,
		`
SELECT *
FROM recovery_scenario_actions
WHERE scenario_id=$1
ORDER BY sequence_no,id
`,
		scenario.ID,
	); err != nil {
		return nil, err
	}

	if len(actions) == 0 {
		return nil, domain.NewBadRequest(
			"recovery scenario requires at least one action",
			nil,
		)
	}

	baselineRows := []recoveryBaselineCase{}

	// v_current_control_tower_cases exposes latest immutable Control Tower
	// snapshot evidence. Only unresolved current cases enter the baseline.
	if err := tx.SelectContext(
		ctx,
		&baselineRows,
		`
SELECT
    case_id,
    planning_exception_id,
    current_status,
    priority_band,
    priority_score,
    revenue_at_risk,
    impact_days,
    exception_type,
    COALESCE(root_cause_type,'') AS root_cause_type,
    COALESCE(root_cause_ref,'') AS root_cause_ref,
    result_hash
FROM v_current_control_tower_cases
WHERE snapshot_id IS NOT NULL
  AND current_status NOT IN (
      'RESOLVED',
      'CLOSED'
  )
ORDER BY case_id
`,
	); err != nil {
		return nil, err
	}

	baselineHash, err :=
		recoveryBaselineHash(
			scenario,
			baselineRows,
		)
	if err != nil {
		return nil, err
	}

	requestHash, err :=
		recoveryRequestHash(
			baselineHash,
			in.HorizonDays,
			actions,
		)
	if err != nil {
		return nil, err
	}

	// Same baseline + same actions + same horizon is canonical.
	// Reuse successful immutable evidence.
	var existing domain.RecoveryScenarioRun

	err = tx.GetContext(
		ctx,
		&existing,
		`
SELECT *
FROM recovery_scenario_runs
WHERE scenario_id=$1
  AND request_hash=$2
  AND status='SUCCEEDED'
ORDER BY completed_at DESC,id DESC
LIMIT 1
`,
		scenario.ID,
		requestHash,
	)

	if err == nil {
		execution, err :=
			s.loadExecutionTx(
				ctx,
				tx,
				existing,
				true,
			)
		if err != nil {
			return nil, err
		}

		if err := tx.Commit(); err != nil {
			return nil, err
		}

		return execution, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	inputs := make(
		[]RecoverySimulationCaseInput,
		0,
		len(baselineRows),
	)

	for _, row := range baselineRows {
		inputs = append(
			inputs,
			RecoverySimulationCaseInput{
				CaseID: row.CaseID,

				CurrentStatus: row.CurrentStatus,

				PriorityBand: row.PriorityBand,

				PriorityScore: row.PriorityScore,

				RevenueAtRisk: row.RevenueAtRisk,

				ImpactDays: row.ImpactDays,

				ExceptionType: row.ExceptionType,

				RootCauseType: row.RootCauseType,

				RootCauseRef: row.RootCauseRef,
			},
		)
	}

	// Pure engine. No DB handle is supplied.
	simulated, err :=
		SimulateRecoveryScenario(
			inputs,
			actions,
		)
	if err != nil {
		return nil, domain.NewBadRequest(
			err.Error(),
			nil,
		)
	}

	now := time.Now().UTC()

	run := domain.RecoveryScenarioRun{
		ID:         uuid.New(),
		ScenarioID: scenario.ID,

		Status: "RUNNING",

		BaselineAsOf: scenario.BaselineAsOf,

		HorizonDays: in.HorizonDays,

		BaselineHash: baselineHash,

		RequestHash: requestHash,

		CreatedByUsername: strings.TrimSpace(
			actor.Username,
		),

		StartedAt: now,
	}

	if actor.UserID != uuid.Nil {
		id := actor.UserID
		run.CreatedByUserID = &id
	}

	_, err = tx.ExecContext(
		ctx,
		`
INSERT INTO recovery_scenario_runs(
    id,
    scenario_id,
    status,
    baseline_as_of,
    horizon_days,
    baseline_hash,
    request_hash,
    created_by_user_id,
    created_by_username,
    started_at
)
VALUES(
    $1,$2,$3,$4,$5,
    $6,$7,$8,$9,$10
)
`,
		run.ID,
		run.ScenarioID,
		run.Status,
		run.BaselineAsOf,
		run.HorizonDays,
		run.BaselineHash,
		run.RequestHash,
		run.CreatedByUserID,
		run.CreatedByUsername,
		run.StartedAt,
	)
	if err != nil {
		return nil, err
	}

	caseRows := make(
		[]domain.RecoveryScenarioCaseResult,
		0,
		len(simulated.Cases),
	)

	for _, projected := range simulated.Cases {
		matchedJSON, err :=
			json.Marshal(
				projected.MatchedActionIDs,
			)
		if err != nil {
			return nil, err
		}

		explanationJSON, err :=
			json.Marshal(
				projected.Explanation,
			)
		if err != nil {
			return nil, err
		}

		row := domain.RecoveryScenarioCaseResult{
			ID:     uuid.New(),
			RunID:  run.ID,
			CaseID: projected.CaseID,

			BaselinePriorityBand: projected.BaselinePriorityBand,

			BaselinePriorityScore: projected.BaselinePriorityScore,

			BaselineRevenueAtRisk: projected.BaselineRevenueAtRisk,

			BaselineImpactDays: projected.BaselineImpactDays,

			SimulatedResolved: projected.SimulatedResolved,

			SimulatedPriorityBand: projected.SimulatedPriorityBand,

			SimulatedPriorityScore: projected.SimulatedPriorityScore,

			SimulatedRevenueAtRisk: projected.SimulatedRevenueAtRisk,

			SimulatedImpactDays: projected.SimulatedImpactDays,

			RecoveryDays: projected.RecoveryDays,

			RevenueRecovered: projected.RevenueRecovered,

			MatchedActionIDs: json.RawMessage(matchedJSON),

			Explanation: json.RawMessage(
				explanationJSON,
			),

			CreatedAt: now,
		}

		row.ResultHash, err =
			recoveryCaseResultHash(row)
		if err != nil {
			return nil, err
		}

		_, err = tx.ExecContext(
			ctx,
			`
INSERT INTO recovery_scenario_case_results(
    id,
    run_id,
    case_id,
    baseline_priority_band,
    baseline_priority_score,
    baseline_revenue_at_risk,
    baseline_impact_days,
    simulated_resolved,
    simulated_priority_band,
    simulated_priority_score,
    simulated_revenue_at_risk,
    simulated_impact_days,
    recovery_days,
    revenue_recovered,
    matched_action_ids,
    explanation,
    result_hash,
    created_at
)
VALUES(
    $1,$2,$3,$4,$5,$6,
    $7,$8,$9,$10,$11,$12,
    $13,$14,$15::jsonb,$16::jsonb,
    $17,$18
)
`,
			row.ID,
			row.RunID,
			row.CaseID,
			row.BaselinePriorityBand,
			row.BaselinePriorityScore,
			row.BaselineRevenueAtRisk,
			row.BaselineImpactDays,
			row.SimulatedResolved,
			row.SimulatedPriorityBand,
			row.SimulatedPriorityScore,
			row.SimulatedRevenueAtRisk,
			row.SimulatedImpactDays,
			row.RecoveryDays,
			row.RevenueRecovered,
			string(row.MatchedActionIDs),
			string(row.Explanation),
			row.ResultHash,
			row.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		caseRows = append(
			caseRows,
			row,
		)
	}

	actionDefinition := make(
		map[uuid.UUID]domain.RecoveryScenarioAction,
		len(actions),
	)

	for _, action := range actions {
		actionDefinition[action.ID] =
			action
	}

	actionRows := make(
		[]domain.RecoveryScenarioActionResult,
		0,
		len(simulated.Actions),
	)

	for _, projected := range simulated.Actions {
		definition, ok := actionDefinition[projected.ActionID]

		if !ok {
			return nil, fmt.Errorf(
				"simulation returned unknown action %s",
				projected.ActionID,
			)
		}

		params, err :=
			canonicalRecoveryParameters(
				definition.Parameters,
			)
		if err != nil {
			return nil, err
		}

		evidenceJSON, err :=
			json.Marshal(
				struct {
					ActionType string          `json:"actionType"`
					TargetType string          `json:"targetType"`
					TargetRef  string          `json:"targetRef"`
					Parameters json.RawMessage `json:"parameters"`
				}{
					ActionType: definition.ActionType,

					TargetType: definition.TargetType,

					TargetRef: definition.TargetRef,

					Parameters: params,
				},
			)
		if err != nil {
			return nil, err
		}

		row := domain.RecoveryScenarioActionResult{
			ID: uuid.New(),

			RunID: run.ID,

			ActionID: projected.ActionID,

			AffectedCases: projected.AffectedCases,

			ImpactDaysRecovered: projected.ImpactDaysRecovered,

			RevenueRecovered: projected.RevenueRecovered,

			EstimatedCost: projected.EstimatedCost,

			Evidence: json.RawMessage(
				evidenceJSON,
			),

			CreatedAt: now,
		}

		row.ResultHash, err =
			recoveryActionResultHash(row)
		if err != nil {
			return nil, err
		}

		_, err = tx.ExecContext(
			ctx,
			`
INSERT INTO recovery_scenario_action_results(
    id,
    run_id,
    action_id,
    affected_cases,
    impact_days_recovered,
    revenue_recovered,
    estimated_cost,
    evidence,
    result_hash,
    created_at
)
VALUES(
    $1,$2,$3,$4,$5,
    $6,$7,$8::jsonb,$9,$10
)
`,
			row.ID,
			row.RunID,
			row.ActionID,
			row.AffectedCases,
			row.ImpactDaysRecovered,
			row.RevenueRecovered,
			row.EstimatedCost,
			string(row.Evidence),
			row.ResultHash,
			row.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		actionRows = append(
			actionRows,
			row,
		)
	}

	summary := domain.RecoveryScenarioSummary{
		ID: uuid.New(),

		RunID: run.ID,

		BaselineOpenCases: simulated.Summary.
			BaselineOpenCases,

		SimulatedOpenCases: simulated.Summary.
			SimulatedOpenCases,

		BaselineP1Cases: simulated.Summary.
			BaselineP1Cases,

		SimulatedP1Cases: simulated.Summary.
			SimulatedP1Cases,

		BaselineP2Cases: simulated.Summary.
			BaselineP2Cases,

		SimulatedP2Cases: simulated.Summary.
			SimulatedP2Cases,

		BaselineRevenueAtRisk: simulated.Summary.
			BaselineRevenueAtRisk,

		SimulatedRevenueAtRisk: simulated.Summary.
			SimulatedRevenueAtRisk,

		BaselineImpactDays: simulated.Summary.
			BaselineImpactDays,

		SimulatedImpactDays: simulated.Summary.
			SimulatedImpactDays,

		RecoveredRevenue: simulated.Summary.
			RecoveredRevenue,

		P1Reduction: simulated.Summary.
			P1Reduction,

		OpenCaseReduction: simulated.Summary.
			OpenCaseReduction,

		ImpactDaysRecovered: simulated.Summary.
			ImpactDaysRecovered,

		EstimatedActionCost: simulated.Summary.
			EstimatedActionCost,

		NetValue: simulated.Summary.
			NetValue,

		RecoveryScore: simulated.Summary.
			RecoveryScore,

		CreatedAt: now,
	}

	summary.ResultHash, err =
		recoverySummaryHash(summary)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(
		ctx,
		`
INSERT INTO recovery_scenario_summaries(
    id,
    run_id,
    baseline_open_cases,
    simulated_open_cases,
    baseline_p1_cases,
    simulated_p1_cases,
    baseline_p2_cases,
    simulated_p2_cases,
    baseline_revenue_at_risk,
    simulated_revenue_at_risk,
    baseline_impact_days,
    simulated_impact_days,
    recovered_revenue,
    p1_reduction,
    open_case_reduction,
    impact_days_recovered,
    estimated_action_cost,
    net_value,
    recovery_score,
    result_hash,
    created_at
)
VALUES(
    $1,$2,$3,$4,$5,$6,$7,
    $8,$9,$10,$11,$12,$13,
    $14,$15,$16,$17,$18,
    $19,$20,$21
)
`,
		summary.ID,
		summary.RunID,
		summary.BaselineOpenCases,
		summary.SimulatedOpenCases,
		summary.BaselineP1Cases,
		summary.SimulatedP1Cases,
		summary.BaselineP2Cases,
		summary.SimulatedP2Cases,
		summary.BaselineRevenueAtRisk,
		summary.SimulatedRevenueAtRisk,
		summary.BaselineImpactDays,
		summary.SimulatedImpactDays,
		summary.RecoveredRevenue,
		summary.P1Reduction,
		summary.OpenCaseReduction,
		summary.ImpactDaysRecovered,
		summary.EstimatedActionCost,
		summary.NetValue,
		summary.RecoveryScore,
		summary.ResultHash,
		summary.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	runHash, err :=
		recoveryRunResultHash(
			caseRows,
			actionRows,
			summary,
		)
	if err != nil {
		return nil, err
	}

	completedAt := time.Now().UTC()

	result, err := tx.ExecContext(
		ctx,
		`
UPDATE recovery_scenario_runs
SET
    status='SUCCEEDED',
    result_hash=$2,
    completed_at=$3
WHERE id=$1
  AND status='RUNNING'
`,
		run.ID,
		runHash,
		completedAt,
	)
	if err != nil {
		return nil, err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}

	if n != 1 {
		return nil, fmt.Errorf(
			"recovery scenario run terminalization lost",
		)
	}

	if scenario.Status == "DRAFT" {
		result, err = tx.ExecContext(
			ctx,
			`
UPDATE recovery_scenarios
SET
    status='SIMULATED',
    updated_at=$2
WHERE id=$1
  AND status='DRAFT'
`,
			scenario.ID,
			completedAt,
		)
		if err != nil {
			return nil, err
		}

		n, err = result.RowsAffected()
		if err != nil {
			return nil, err
		}

		if n != 1 {
			return nil, fmt.Errorf(
				"recovery scenario lifecycle transition lost",
			)
		}
	}

	run.Status = "SUCCEEDED"

	run.ResultHash =
		&runHash

	run.CompletedAt =
		&completedAt

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &RecoverySimulationExecution{
		Run: run,

		Summary: summary,

		Cases: caseRows,

		Actions: actionRows,

		Reused: false,
	}, nil
}

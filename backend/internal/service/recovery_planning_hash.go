package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

const recoverySimulationEngineVersion = "0041-v1"

type recoveryBaselineCase struct {
	CaseID              uuid.UUID `db:"case_id"`
	PlanningExceptionID uuid.UUID `db:"planning_exception_id"`
	CurrentStatus       string    `db:"current_status"`

	PriorityBand  string  `db:"priority_band"`
	PriorityScore float64 `db:"priority_score"`

	RevenueAtRisk float64 `db:"revenue_at_risk"`
	ImpactDays    int     `db:"impact_days"`

	ExceptionType string `db:"exception_type"`
	RootCauseType string `db:"root_cause_type"`
	RootCauseRef  string `db:"root_cause_ref"`

	SnapshotResultHash string `db:"result_hash"`
}

func recoverySHA256(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:]), nil
}

func canonicalRecoveryParameters(
	raw json.RawMessage,
) (json.RawMessage, error) {
	params, err := recoveryParams(raw)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(b), nil
}

func recoveryBaselineHash(
	scenario domain.RecoveryScenario,
	rows []recoveryBaselineCase,
) (string, error) {
	type canonicalCase struct {
		CaseID              string  `json:"caseId"`
		PlanningExceptionID string  `json:"planningExceptionId"`
		CurrentStatus       string  `json:"currentStatus"`
		PriorityBand        string  `json:"priorityBand"`
		PriorityScore       float64 `json:"priorityScore"`
		RevenueAtRisk       float64 `json:"revenueAtRisk"`
		ImpactDays          int     `json:"impactDays"`
		ExceptionType       string  `json:"exceptionType"`
		RootCauseType       string  `json:"rootCauseType"`
		RootCauseRef        string  `json:"rootCauseRef"`
		SnapshotResultHash  string  `json:"snapshotResultHash"`
	}

	xs := append(
		[]recoveryBaselineCase(nil),
		rows...,
	)

	sort.Slice(
		xs,
		func(i, j int) bool {
			return xs[i].CaseID.String() <
				xs[j].CaseID.String()
		},
	)

	out := make(
		[]canonicalCase,
		0,
		len(xs),
	)

	for _, row := range xs {
		out = append(
			out,
			canonicalCase{
				CaseID: row.CaseID.String(),

				PlanningExceptionID: row.PlanningExceptionID.String(),

				CurrentStatus: strings.ToUpper(
					strings.TrimSpace(
						row.CurrentStatus,
					),
				),

				PriorityBand: strings.ToUpper(
					strings.TrimSpace(
						row.PriorityBand,
					),
				),

				PriorityScore: recoveryRound3(
					row.PriorityScore,
				),

				RevenueAtRisk: recoveryRoundMoney(
					row.RevenueAtRisk,
				),

				ImpactDays: recoveryMaxInt(
					row.ImpactDays,
					0,
				),

				ExceptionType: strings.ToUpper(
					strings.TrimSpace(
						row.ExceptionType,
					),
				),

				RootCauseType: strings.ToUpper(
					strings.TrimSpace(
						row.RootCauseType,
					),
				),

				RootCauseRef: strings.TrimSpace(
					row.RootCauseRef,
				),

				SnapshotResultHash: row.SnapshotResultHash,
			},
		)
	}

	return recoverySHA256(
		struct {
			EngineVersion string          `json:"engineVersion"`
			BaselineAsOf  string          `json:"baselineAsOf"`
			Cases         []canonicalCase `json:"cases"`
		}{
			EngineVersion: recoverySimulationEngineVersion,

			BaselineAsOf: scenario.BaselineAsOf.
				UTC().
				Format(time.RFC3339Nano),

			Cases: out,
		},
	)
}

func recoveryRequestHash(
	baselineHash string,
	horizonDays int,
	actions []domain.RecoveryScenarioAction,
) (string, error) {
	type canonicalAction struct {
		SequenceNo int `json:"sequenceNo"`

		ActionType string `json:"actionType"`
		TargetType string `json:"targetType"`
		TargetRef  string `json:"targetRef"`

		Parameters json.RawMessage `json:"parameters"`

		EstimatedCost float64 `json:"estimatedCost"`
	}

	xs := append(
		[]domain.RecoveryScenarioAction(nil),
		actions...,
	)

	sort.Slice(
		xs,
		func(i, j int) bool {
			if xs[i].SequenceNo == xs[j].SequenceNo {
				return xs[i].ID.String() <
					xs[j].ID.String()
			}

			return xs[i].SequenceNo <
				xs[j].SequenceNo
		},
	)

	canonical := make(
		[]canonicalAction,
		0,
		len(xs),
	)

	for _, action := range xs {
		params, err :=
			canonicalRecoveryParameters(
				action.Parameters,
			)

		if err != nil {
			return "", err
		}

		canonical = append(
			canonical,
			canonicalAction{
				SequenceNo: action.SequenceNo,

				ActionType: strings.ToUpper(
					strings.TrimSpace(
						action.ActionType,
					),
				),

				TargetType: strings.ToUpper(
					strings.TrimSpace(
						action.TargetType,
					),
				),

				TargetRef: strings.TrimSpace(
					action.TargetRef,
				),

				Parameters: params,

				EstimatedCost: recoveryRoundMoney(
					action.EstimatedCost,
				),
			},
		)
	}

	return recoverySHA256(
		struct {
			EngineVersion string            `json:"engineVersion"`
			BaselineHash  string            `json:"baselineHash"`
			HorizonDays   int               `json:"horizonDays"`
			Actions       []canonicalAction `json:"actions"`
		}{
			EngineVersion: recoverySimulationEngineVersion,

			BaselineHash: baselineHash,

			HorizonDays: horizonDays,

			Actions: canonical,
		},
	)
}

func recoveryCaseResultHash(
	row domain.RecoveryScenarioCaseResult,
) (string, error) {
	return recoverySHA256(
		struct {
			CaseID                 string          `json:"caseId"`
			BaselinePriorityBand   string          `json:"baselinePriorityBand"`
			BaselinePriorityScore  float64         `json:"baselinePriorityScore"`
			BaselineRevenueAtRisk  float64         `json:"baselineRevenueAtRisk"`
			BaselineImpactDays     int             `json:"baselineImpactDays"`
			SimulatedResolved      bool            `json:"simulatedResolved"`
			SimulatedPriorityBand  string          `json:"simulatedPriorityBand"`
			SimulatedPriorityScore float64         `json:"simulatedPriorityScore"`
			SimulatedRevenueAtRisk float64         `json:"simulatedRevenueAtRisk"`
			SimulatedImpactDays    int             `json:"simulatedImpactDays"`
			RecoveryDays           int             `json:"recoveryDays"`
			RevenueRecovered       float64         `json:"revenueRecovered"`
			MatchedActionIDs       json.RawMessage `json:"matchedActionIds"`
			Explanation            json.RawMessage `json:"explanation"`
		}{
			CaseID: row.CaseID.String(),

			BaselinePriorityBand: row.BaselinePriorityBand,

			BaselinePriorityScore: recoveryRound3(
				row.BaselinePriorityScore,
			),

			BaselineRevenueAtRisk: recoveryRoundMoney(
				row.BaselineRevenueAtRisk,
			),

			BaselineImpactDays: row.BaselineImpactDays,

			SimulatedResolved: row.SimulatedResolved,

			SimulatedPriorityBand: row.SimulatedPriorityBand,

			SimulatedPriorityScore: recoveryRound3(
				row.SimulatedPriorityScore,
			),

			SimulatedRevenueAtRisk: recoveryRoundMoney(
				row.SimulatedRevenueAtRisk,
			),

			SimulatedImpactDays: row.SimulatedImpactDays,

			RecoveryDays: row.RecoveryDays,

			RevenueRecovered: recoveryRoundMoney(
				row.RevenueRecovered,
			),

			MatchedActionIDs: row.MatchedActionIDs,

			Explanation: row.Explanation,
		},
	)
}

func recoveryActionResultHash(
	row domain.RecoveryScenarioActionResult,
) (string, error) {
	return recoverySHA256(
		struct {
			ActionID            string          `json:"actionId"`
			AffectedCases       int             `json:"affectedCases"`
			ImpactDaysRecovered int             `json:"impactDaysRecovered"`
			RevenueRecovered    float64         `json:"revenueRecovered"`
			EstimatedCost       float64         `json:"estimatedCost"`
			Evidence            json.RawMessage `json:"evidence"`
		}{
			ActionID: row.ActionID.String(),

			AffectedCases: row.AffectedCases,

			ImpactDaysRecovered: row.ImpactDaysRecovered,

			RevenueRecovered: recoveryRoundMoney(
				row.RevenueRecovered,
			),

			EstimatedCost: recoveryRoundMoney(
				row.EstimatedCost,
			),

			Evidence: row.Evidence,
		},
	)
}

func recoverySummaryHash(
	row domain.RecoveryScenarioSummary,
) (string, error) {
	return recoverySHA256(
		struct {
			BaselineOpenCases      int     `json:"baselineOpenCases"`
			SimulatedOpenCases     int     `json:"simulatedOpenCases"`
			BaselineP1Cases        int     `json:"baselineP1Cases"`
			SimulatedP1Cases       int     `json:"simulatedP1Cases"`
			BaselineP2Cases        int     `json:"baselineP2Cases"`
			SimulatedP2Cases       int     `json:"simulatedP2Cases"`
			BaselineRevenueAtRisk  float64 `json:"baselineRevenueAtRisk"`
			SimulatedRevenueAtRisk float64 `json:"simulatedRevenueAtRisk"`
			BaselineImpactDays     int     `json:"baselineImpactDays"`
			SimulatedImpactDays    int     `json:"simulatedImpactDays"`
			RecoveredRevenue       float64 `json:"recoveredRevenue"`
			P1Reduction            int     `json:"p1Reduction"`
			OpenCaseReduction      int     `json:"openCaseReduction"`
			ImpactDaysRecovered    int     `json:"impactDaysRecovered"`
			EstimatedActionCost    float64 `json:"estimatedActionCost"`
			NetValue               float64 `json:"netValue"`
			RecoveryScore          float64 `json:"recoveryScore"`
		}{
			BaselineOpenCases: row.BaselineOpenCases,

			SimulatedOpenCases: row.SimulatedOpenCases,

			BaselineP1Cases: row.BaselineP1Cases,

			SimulatedP1Cases: row.SimulatedP1Cases,

			BaselineP2Cases: row.BaselineP2Cases,

			SimulatedP2Cases: row.SimulatedP2Cases,

			BaselineRevenueAtRisk: recoveryRoundMoney(
				row.BaselineRevenueAtRisk,
			),

			SimulatedRevenueAtRisk: recoveryRoundMoney(
				row.SimulatedRevenueAtRisk,
			),

			BaselineImpactDays: row.BaselineImpactDays,

			SimulatedImpactDays: row.SimulatedImpactDays,

			RecoveredRevenue: recoveryRoundMoney(
				row.RecoveredRevenue,
			),

			P1Reduction: row.P1Reduction,

			OpenCaseReduction: row.OpenCaseReduction,

			ImpactDaysRecovered: row.ImpactDaysRecovered,

			EstimatedActionCost: recoveryRoundMoney(
				row.EstimatedActionCost,
			),

			NetValue: recoveryRoundMoney(
				row.NetValue,
			),

			RecoveryScore: recoveryRound3(
				row.RecoveryScore,
			),
		},
	)
}

func recoveryRunResultHash(
	cases []domain.RecoveryScenarioCaseResult,
	actions []domain.RecoveryScenarioActionResult,
	summary domain.RecoveryScenarioSummary,
) (string, error) {
	caseHashes := make(
		[]string,
		0,
		len(cases),
	)

	for _, row := range cases {
		caseHashes = append(
			caseHashes,
			row.ResultHash,
		)
	}

	sort.Strings(caseHashes)

	actionHashes := make(
		[]string,
		0,
		len(actions),
	)

	for _, row := range actions {
		actionHashes = append(
			actionHashes,
			row.ResultHash,
		)
	}

	sort.Strings(actionHashes)

	return recoverySHA256(
		struct {
			EngineVersion string   `json:"engineVersion"`
			CaseHashes    []string `json:"caseHashes"`
			ActionHashes  []string `json:"actionHashes"`
			SummaryHash   string   `json:"summaryHash"`
		}{
			EngineVersion: recoverySimulationEngineVersion,

			CaseHashes: caseHashes,

			ActionHashes: actionHashes,

			SummaryHash: summary.ResultHash,
		},
	)
}

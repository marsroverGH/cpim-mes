package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// RecoverySimulationCaseInput is immutable baseline Control Tower evidence
// projected into the What-if engine.
//
// The engine is deliberately pure: it does not receive a DB handle and cannot
// mutate Purchase Orders, Work Orders, Work Centers, Sales Orders or inventory.
type RecoverySimulationCaseInput struct {
	CaseID        uuid.UUID
	CurrentStatus string
	PriorityBand  string
	PriorityScore float64
	RevenueAtRisk float64
	ImpactDays    int
	ExceptionType string
	RootCauseType string
	RootCauseRef  string
}

// RecoverySimulatedCase is the calculated before/after projection for one
// Control Tower case.
type RecoverySimulatedCase struct {
	CaseID                uuid.UUID
	BaselinePriorityBand  string
	BaselinePriorityScore float64
	BaselineRevenueAtRisk float64
	BaselineImpactDays    int

	SimulatedResolved      bool
	SimulatedPriorityBand  string
	SimulatedPriorityScore float64
	SimulatedRevenueAtRisk float64
	SimulatedImpactDays    int

	RecoveryDays     int
	RevenueRecovered float64

	MatchedActionIDs []uuid.UUID
	Explanation      map[string]any
}

// RecoverySimulatedAction aggregates the benefit attributed to one
// hypothetical action.
type RecoverySimulatedAction struct {
	ActionID            uuid.UUID
	AffectedCases       int
	ImpactDaysRecovered int
	RevenueRecovered    float64
	EstimatedCost       float64
}

// RecoverySimulationSummary contains scenario-level before/after KPIs.
type RecoverySimulationSummary struct {
	BaselineOpenCases  int
	SimulatedOpenCases int

	BaselineP1Cases  int
	SimulatedP1Cases int

	BaselineP2Cases  int
	SimulatedP2Cases int

	BaselineRevenueAtRisk  float64
	SimulatedRevenueAtRisk float64

	BaselineImpactDays  int
	SimulatedImpactDays int

	RecoveredRevenue    float64
	P1Reduction         int
	OpenCaseReduction   int
	ImpactDaysRecovered int

	EstimatedActionCost float64
	NetValue            float64
	RecoveryScore       float64
}

// RecoverySimulationResult is the complete side-effect-free What-if result.
type RecoverySimulationResult struct {
	Cases   []RecoverySimulatedCase
	Actions []RecoverySimulatedAction
	Summary RecoverySimulationSummary
}

type recoveryActionEffect struct {
	ActionIndex  int
	ActionID     uuid.UUID
	RecoveryDays int
	RiskRelief   float64
	Resolve      bool
	Reason       string
}

func recoveryClamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func recoveryRound3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func recoveryRoundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

func recoveryMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func recoveryMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func recoveryIsOpenStatus(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "RESOLVED", "CLOSED":
		return false
	default:
		return true
	}
}

func recoveryBand(score float64) string {
	switch {
	case score >= 75:
		return "P1"
	case score >= 55:
		return "P2"
	case score >= 30:
		return "P3"
	default:
		return "P4"
	}
}

func recoveryParams(
	raw json.RawMessage,
) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}

	var out map[string]any

	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf(
			"invalid recovery action parameters: %w",
			err,
		)
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

func recoveryIntegerParam(
	params map[string]any,
	key string,
	minValue int,
	maxValue int,
) (int, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf(
			"recovery action requires parameter %s",
			key,
		)
	}

	n, ok := v.(float64)
	if !ok || math.Trunc(n) != n {
		return 0, fmt.Errorf(
			"recovery action parameter %s must be an integer",
			key,
		)
	}

	i := int(n)

	if i < minValue || i > maxValue {
		return 0, fmt.Errorf(
			"recovery action parameter %s must be between %d and %d",
			key,
			minValue,
			maxValue,
		)
	}

	return i, nil
}

func recoveryStringParam(
	params map[string]any,
	key string,
) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf(
			"recovery action requires parameter %s",
			key,
		)
	}

	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf(
			"recovery action parameter %s must be a non-empty string",
			key,
		)
	}

	return strings.TrimSpace(s), nil
}

func validateRecoveryAction(
	a domain.RecoveryScenarioAction,
) (map[string]any, error) {
	if a.ID == uuid.Nil {
		return nil, fmt.Errorf(
			"recovery action ID is required",
		)
	}

	if strings.TrimSpace(a.TargetRef) == "" {
		return nil, fmt.Errorf(
			"recovery action targetRef is required",
		)
	}

	params, err := recoveryParams(a.Parameters)
	if err != nil {
		return nil, err
	}

	switch strings.ToUpper(
		strings.TrimSpace(a.ActionType),
	) {
	case "EXPEDITE_PO":
		if _, err := recoveryIntegerParam(
			params,
			"expediteDays",
			1,
			365,
		); err != nil {
			return nil, err
		}

	case "ALTERNATE_WORK_CENTER":
		if _, err := recoveryStringParam(
			params,
			"alternateWorkCenterRef",
		); err != nil {
			return nil, err
		}

		if _, err := recoveryIntegerParam(
			params,
			"recoveryDays",
			1,
			365,
		); err != nil {
			return nil, err
		}

	case "ADD_OVERTIME_CAPACITY":
		if _, err := recoveryIntegerParam(
			params,
			"overtimeMinutes",
			1,
			10080,
		); err != nil {
			return nil, err
		}

	case "RESCHEDULE_WO":
		if _, err := recoveryIntegerParam(
			params,
			"recoveryDays",
			1,
			365,
		); err != nil {
			return nil, err
		}

	case "RELEASE_WO":
		// No action-specific parameter is mandatory.

	default:
		return nil, fmt.Errorf(
			"unsupported recovery action type %q",
			a.ActionType,
		)
	}

	return params, nil
}

func recoveryTargetMatches(
	c RecoverySimulationCaseInput,
	a domain.RecoveryScenarioAction,
) bool {
	target := strings.TrimSpace(a.TargetRef)
	root := strings.TrimSpace(c.RootCauseRef)

	if target == "*" {
		return true
	}

	if root == "" || target == "" {
		return false
	}

	if strings.EqualFold(root, target) {
		return true
	}

	// Root cause references frequently contain typed prefixes such as:
	//
	//   PO:<uuid>
	//   WO:<uuid>
	//   WC:<uuid>:<operation>
	//
	// Therefore an exact UUID or typed reference can match inside the
	// canonical Control Tower root reference.
	return strings.Contains(
		strings.ToUpper(root),
		strings.ToUpper(target),
	)
}

func recoverySupplierException(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case
		"LATE_PURCHASE_ORDER",
		"SUPPLIER_CONFIRMATION_LATE",
		"SUPPLIER_RELIABILITY_RISK",
		"MATERIAL_SHORTAGE":
		return true
	default:
		return false
	}
}

func recoveryCapacityException(v string) bool {
	x := strings.ToUpper(strings.TrimSpace(v))

	switch x {
	case
		"CAPACITY_LATE",
		"CAPACITY_UNSCHEDULED",
		"OEE_CAPACITY_RISK",
		"SCHEDULE_START_LATE",
		"SCHEDULE_COMPLETION_LATE",
		"DISPATCH_BLOCKED",
		"RESCHEDULE_REQUIRED",
		"LATE_WORK_ORDER",
		"FIRM_HORIZON_CHANGE":
		return true
	}

	return strings.Contains(x, "MAINTENANCE") ||
		strings.Contains(x, "BREAKDOWN") ||
		strings.Contains(x, "DOWNTIME")
}

func recoveryReleaseException(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case
		"CAPACITY_UNSCHEDULED",
		"DISPATCH_BLOCKED",
		"LATE_WORK_ORDER":
		return true
	default:
		return false
	}
}

func recoveryActionEffectForCase(
	c RecoverySimulationCaseInput,
	a domain.RecoveryScenarioAction,
	actionIndex int,
	params map[string]any,
) (recoveryActionEffect, bool, error) {
	if !recoveryTargetMatches(c, a) {
		return recoveryActionEffect{}, false, nil
	}

	typ := strings.ToUpper(
		strings.TrimSpace(a.ActionType),
	)

	exceptionType := strings.ToUpper(
		strings.TrimSpace(c.ExceptionType),
	)

	effect := recoveryActionEffect{
		ActionIndex: actionIndex,
		ActionID:    a.ID,
	}

	switch typ {
	case "EXPEDITE_PO":
		if !recoverySupplierException(exceptionType) {
			return recoveryActionEffect{}, false, nil
		}

		days, err := recoveryIntegerParam(
			params,
			"expediteDays",
			1,
			365,
		)
		if err != nil {
			return recoveryActionEffect{}, false, err
		}

		effect.RecoveryDays = days
		effect.RiskRelief = 0.55
		effect.Reason =
			"Earlier supply reduces supplier/material constraint exposure"

		if exceptionType == "MATERIAL_SHORTAGE" &&
			c.ImpactDays == 0 {
			effect.Resolve = true
		}

	case "ALTERNATE_WORK_CENTER":
		if !recoveryCapacityException(exceptionType) {
			return recoveryActionEffect{}, false, nil
		}

		days, err := recoveryIntegerParam(
			params,
			"recoveryDays",
			1,
			365,
		)
		if err != nil {
			return recoveryActionEffect{}, false, err
		}

		if _, err := recoveryStringParam(
			params,
			"alternateWorkCenterRef",
		); err != nil {
			return recoveryActionEffect{}, false, err
		}

		effect.RecoveryDays = days
		effect.RiskRelief = 0.65
		effect.Reason =
			"Alternate work center removes or reduces the active capacity constraint"

		if exceptionType == "CAPACITY_UNSCHEDULED" ||
			exceptionType == "DISPATCH_BLOCKED" {
			effect.Resolve = true
		}

	case "ADD_OVERTIME_CAPACITY":
		if !recoveryCapacityException(exceptionType) {
			return recoveryActionEffect{}, false, nil
		}

		minutes, err := recoveryIntegerParam(
			params,
			"overtimeMinutes",
			1,
			10080,
		)
		if err != nil {
			return recoveryActionEffect{}, false, err
		}

		// 480 minutes represents one nominal production day.
		days := int(
			math.Ceil(float64(minutes) / 480.0),
		)

		effect.RecoveryDays = recoveryMaxInt(days, 1)
		effect.RiskRelief = 0.45
		effect.Reason =
			"Overtime adds temporary capacity without changing operational master data"

	case "RESCHEDULE_WO":
		// Frozen/executed commitment conflicts must not be silently
		// resolved by What-if scheduling.
		if exceptionType == "FROZEN_HORIZON_CONFLICT" ||
			exceptionType == "EXECUTION_COMMITMENT_CONFLICT" {
			return recoveryActionEffect{}, false, nil
		}

		if !recoveryCapacityException(exceptionType) {
			return recoveryActionEffect{}, false, nil
		}

		days, err := recoveryIntegerParam(
			params,
			"recoveryDays",
			1,
			365,
		)
		if err != nil {
			return recoveryActionEffect{}, false, err
		}

		effect.RecoveryDays = days
		effect.RiskRelief = 0.50
		effect.Reason =
			"Work-order rescheduling improves timing while preserving committed execution constraints"

	case "RELEASE_WO":
		if !recoveryReleaseException(exceptionType) {
			return recoveryActionEffect{}, false, nil
		}

		effect.RecoveryDays = 1
		effect.RiskRelief = 0.60
		effect.Reason =
			"Releasing eligible work removes an execution readiness constraint"

		if exceptionType == "CAPACITY_UNSCHEDULED" ||
			exceptionType == "DISPATCH_BLOCKED" {
			effect.Resolve = true
		}

	default:
		return recoveryActionEffect{}, false, fmt.Errorf(
			"unsupported recovery action type %q",
			a.ActionType,
		)
	}

	return effect, true, nil
}

func recoveryBenefitRatio(
	reduction int,
	baseline int,
) float64 {
	if baseline <= 0 || reduction <= 0 {
		return 0
	}

	return recoveryClamp(
		float64(reduction) /
			float64(baseline) *
			100,
	)
}

func recoveryMoneyBenefitRatio(
	recovered float64,
	baseline float64,
) float64 {
	if baseline <= 0 || recovered <= 0 {
		return 0
	}

	return recoveryClamp(
		recovered /
			baseline *
			100,
	)
}

func recoveryScenarioScore(
	s RecoverySimulationSummary,
) float64 {
	p1Benefit := recoveryBenefitRatio(
		s.P1Reduction,
		s.BaselineP1Cases,
	)

	revenueBenefit := recoveryMoneyBenefitRatio(
		s.RecoveredRevenue,
		s.BaselineRevenueAtRisk,
	)

	delayBenefit := recoveryBenefitRatio(
		s.ImpactDaysRecovered,
		s.BaselineImpactDays,
	)

	caseBenefit := recoveryBenefitRatio(
		s.OpenCaseReduction,
		s.BaselineOpenCases,
	)

	costEfficiency := 0.0

	if s.RecoveredRevenue > 0 {
		costEfficiency = recoveryClamp(
			s.NetValue /
				s.RecoveredRevenue *
				100,
		)
	}

	score :=
		p1Benefit*0.35 +
			revenueBenefit*0.30 +
			delayBenefit*0.20 +
			caseBenefit*0.10 +
			costEfficiency*0.05

	return recoveryRound3(
		recoveryClamp(score),
	)
}

// SimulateRecoveryScenario performs a deterministic, side-effect-free
// scenario projection.
//
// This function intentionally has no DB handle and cannot update operational
// production state.
func SimulateRecoveryScenario(
	cases []RecoverySimulationCaseInput,
	actions []domain.RecoveryScenarioAction,
) (RecoverySimulationResult, error) {
	var result RecoverySimulationResult

	if len(actions) == 0 {
		return result, fmt.Errorf(
			"recovery scenario requires at least one action",
		)
	}

	params := make(
		[]map[string]any,
		len(actions),
	)

	actionResults := make(
		[]RecoverySimulatedAction,
		len(actions),
	)

	for i, action := range actions {
		p, err := validateRecoveryAction(action)
		if err != nil {
			return result, err
		}

		params[i] = p

		actionResults[i] = RecoverySimulatedAction{
			ActionID: action.ID,
			EstimatedCost: recoveryRoundMoney(
				action.EstimatedCost,
			),
		}

		result.Summary.EstimatedActionCost +=
			action.EstimatedCost
	}

	result.Summary.EstimatedActionCost =
		recoveryRoundMoney(
			result.Summary.EstimatedActionCost,
		)

	result.Cases = make(
		[]RecoverySimulatedCase,
		0,
		len(cases),
	)

	for _, c := range cases {
		open := recoveryIsOpenStatus(
			c.CurrentStatus,
		)

		projected := RecoverySimulatedCase{
			CaseID:                 c.CaseID,
			BaselinePriorityBand:   c.PriorityBand,
			BaselinePriorityScore:  recoveryRound3(c.PriorityScore),
			BaselineRevenueAtRisk:  recoveryRoundMoney(c.RevenueAtRisk),
			BaselineImpactDays:     recoveryMaxInt(c.ImpactDays, 0),
			SimulatedResolved:      !open,
			SimulatedPriorityBand:  c.PriorityBand,
			SimulatedPriorityScore: recoveryRound3(c.PriorityScore),
			SimulatedRevenueAtRisk: recoveryRoundMoney(c.RevenueAtRisk),
			SimulatedImpactDays:    recoveryMaxInt(c.ImpactDays, 0),
			MatchedActionIDs:       []uuid.UUID{},
			Explanation: map[string]any{
				"exceptionType": c.ExceptionType,
				"rootCauseType": c.RootCauseType,
				"rootCauseRef":  c.RootCauseRef,
			},
		}

		if open {
			result.Summary.BaselineOpenCases++
			result.Summary.BaselineRevenueAtRisk +=
				c.RevenueAtRisk
			result.Summary.BaselineImpactDays +=
				recoveryMaxInt(c.ImpactDays, 0)

			switch strings.ToUpper(
				strings.TrimSpace(c.PriorityBand),
			) {
			case "P1":
				result.Summary.BaselineP1Cases++
			case "P2":
				result.Summary.BaselineP2Cases++
			}
		}

		effects := []recoveryActionEffect{}

		if open {
			for i, action := range actions {
				effect, applies, err :=
					recoveryActionEffectForCase(
						c,
						action,
						i,
						params[i],
					)
				if err != nil {
					return result, err
				}

				if !applies {
					continue
				}

				effects = append(
					effects,
					effect,
				)

				projected.MatchedActionIDs =
					append(
						projected.MatchedActionIDs,
						action.ID,
					)
			}
		}

		if len(effects) > 0 {
			recoveredDays := 0
			remainingRiskFactor := 1.0
			directResolution := false

			for _, effect := range effects {
				recoveredDays +=
					effect.RecoveryDays

				remainingRiskFactor *=
					1 - recoveryClamp(
						effect.RiskRelief*100,
					)/100

				if effect.Resolve {
					directResolution = true
				}
			}

			recoveredDays = recoveryMinInt(
				recoveredDays,
				projected.BaselineImpactDays,
			)

			combinedRiskRelief :=
				1 - remainingRiskFactor

			dayRelief := 0.0

			if projected.BaselineImpactDays > 0 {
				dayRelief =
					float64(recoveredDays) /
						float64(
							projected.BaselineImpactDays,
						)
			}

			totalRelief := math.Max(
				combinedRiskRelief,
				dayRelief,
			)

			if totalRelief > 1 {
				totalRelief = 1
			}

			resolved := directResolution ||
				(projected.BaselineImpactDays > 0 &&
					recoveredDays >= projected.BaselineImpactDays)

			if resolved {
				projected.SimulatedResolved = true
				projected.SimulatedPriorityScore = 0
				projected.SimulatedPriorityBand = "P4"
				projected.SimulatedRevenueAtRisk = 0
				projected.SimulatedImpactDays = 0
			} else {
				projected.SimulatedPriorityScore =
					recoveryRound3(
						projected.BaselinePriorityScore *
							(1 - totalRelief),
					)

				projected.SimulatedPriorityBand =
					recoveryBand(
						projected.SimulatedPriorityScore,
					)

				projected.SimulatedRevenueAtRisk =
					recoveryRoundMoney(
						projected.BaselineRevenueAtRisk *
							(1 - totalRelief),
					)

				projected.SimulatedImpactDays =
					recoveryMaxInt(
						projected.BaselineImpactDays-
							recoveredDays,
						0,
					)
			}

			projected.RecoveryDays =
				projected.BaselineImpactDays -
					projected.SimulatedImpactDays

			projected.RevenueRecovered =
				recoveryRoundMoney(
					projected.BaselineRevenueAtRisk -
						projected.SimulatedRevenueAtRisk,
				)

			reasons := make(
				[]string,
				0,
				len(effects),
			)

			totalWeight := 0.0

			for _, effect := range effects {
				totalWeight += effect.RiskRelief
				reasons = append(
					reasons,
					effect.Reason,
				)
			}

			projected.Explanation["reasons"] =
				reasons
			projected.Explanation["combinedRiskRelief"] =
				recoveryRound3(
					totalRelief * 100,
				)
			projected.Explanation["resolved"] =
				projected.SimulatedResolved

			// Attribute realized benefit across matched actions without
			// double-counting scenario-level recovered revenue.
			for _, effect := range effects {
				share := 1.0 /
					float64(len(effects))

				if totalWeight > 0 {
					share =
						effect.RiskRelief /
							totalWeight
				}

				ar := &actionResults[effect.ActionIndex]

				ar.AffectedCases++

				ar.RevenueRecovered =
					recoveryRoundMoney(
						ar.RevenueRecovered +
							projected.RevenueRecovered*
								share,
					)

				ar.ImpactDaysRecovered +=
					int(
						math.Round(
							float64(
								projected.RecoveryDays,
							) * share,
						),
					)
			}
		}

		if open && !projected.SimulatedResolved {
			result.Summary.SimulatedOpenCases++

			switch projected.SimulatedPriorityBand {
			case "P1":
				result.Summary.SimulatedP1Cases++
			case "P2":
				result.Summary.SimulatedP2Cases++
			}

			result.Summary.SimulatedRevenueAtRisk +=
				projected.SimulatedRevenueAtRisk

			result.Summary.SimulatedImpactDays +=
				projected.SimulatedImpactDays
		}

		result.Cases = append(
			result.Cases,
			projected,
		)
	}

	result.Actions = actionResults

	result.Summary.BaselineRevenueAtRisk =
		recoveryRoundMoney(
			result.Summary.BaselineRevenueAtRisk,
		)

	result.Summary.SimulatedRevenueAtRisk =
		recoveryRoundMoney(
			result.Summary.SimulatedRevenueAtRisk,
		)

	result.Summary.RecoveredRevenue =
		recoveryRoundMoney(
			result.Summary.BaselineRevenueAtRisk -
				result.Summary.SimulatedRevenueAtRisk,
		)

	result.Summary.P1Reduction =
		recoveryMaxInt(
			result.Summary.BaselineP1Cases-
				result.Summary.SimulatedP1Cases,
			0,
		)

	result.Summary.OpenCaseReduction =
		recoveryMaxInt(
			result.Summary.BaselineOpenCases-
				result.Summary.SimulatedOpenCases,
			0,
		)

	result.Summary.ImpactDaysRecovered =
		recoveryMaxInt(
			result.Summary.BaselineImpactDays-
				result.Summary.SimulatedImpactDays,
			0,
		)

	result.Summary.NetValue =
		recoveryRoundMoney(
			result.Summary.RecoveredRevenue -
				result.Summary.EstimatedActionCost,
		)

	result.Summary.RecoveryScore =
		recoveryScenarioScore(
			result.Summary,
		)

	return result, nil
}

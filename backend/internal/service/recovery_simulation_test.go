package service

import (
	"encoding/json"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func recoveryJSON(v string) json.RawMessage {
	return json.RawMessage(v)
}

func TestRecoverySimulationExpeditePOResolvesSupplierDelay(
	t *testing.T,
) {
	caseID := uuid.New()
	actionID := uuid.New()

	got, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        caseID,
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 92,
				RevenueAtRisk: 6_000_000,
				ImpactDays:    6,
				ExceptionType: "SUPPLIER_CONFIRMATION_LATE",
				RootCauseType: "LATE_PURCHASE_ORDER",
				RootCauseRef:  "PO:11111111-1111-1111-1111-111111111111",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         actionID,
				ActionType: "EXPEDITE_PO",
				TargetType: "PURCHASE_ORDER",
				TargetRef:  "PO:11111111-1111-1111-1111-111111111111",
				Parameters: recoveryJSON(
					`{"expediteDays":6}`,
				),
				EstimatedCost: 150000,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Cases) != 1 {
		t.Fatalf(
			"expected one case, got %d",
			len(got.Cases),
		)
	}

	c := got.Cases[0]

	if !c.SimulatedResolved {
		t.Fatal(
			"expedite equal to lateness should resolve case",
		)
	}

	if c.SimulatedRevenueAtRisk != 0 {
		t.Fatalf(
			"expected zero simulated revenue at risk, got %v",
			c.SimulatedRevenueAtRisk,
		)
	}

	if got.Summary.P1Reduction != 1 {
		t.Fatalf(
			"expected P1 reduction 1, got %d",
			got.Summary.P1Reduction,
		)
	}

	if got.Summary.OpenCaseReduction != 1 {
		t.Fatalf(
			"expected open case reduction 1, got %d",
			got.Summary.OpenCaseReduction,
		)
	}

	if got.Summary.RecoveredRevenue != 6_000_000 {
		t.Fatalf(
			"expected recovered revenue 6000000, got %v",
			got.Summary.RecoveredRevenue,
		)
	}

	if got.Summary.NetValue != 5_850_000 {
		t.Fatalf(
			"expected net value 5850000, got %v",
			got.Summary.NetValue,
		)
	}
}

func TestRecoverySimulationAlternateWCAndOvertime(
	t *testing.T,
) {
	caseID := uuid.New()
	alternateID := uuid.New()
	overtimeID := uuid.New()

	got, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        caseID,
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 88,
				RevenueAtRisk: 3_000_000,
				ImpactDays:    4,
				ExceptionType: "CAPACITY_LATE",
				RootCauseType: "CAPACITY_LATE",
				RootCauseRef:  "WC:WC-CUT-01",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         alternateID,
				ActionType: "ALTERNATE_WORK_CENTER",
				TargetType: "WORK_CENTER",
				TargetRef:  "WC:WC-CUT-01",
				Parameters: recoveryJSON(
					`{
						"alternateWorkCenterRef":"WC:WC-CUT-02",
						"recoveryDays":3
					}`,
				),
				EstimatedCost: 250000,
			},
			{
				ID:         overtimeID,
				ActionType: "ADD_OVERTIME_CAPACITY",
				TargetType: "WORK_CENTER",
				TargetRef:  "WC:WC-CUT-01",
				Parameters: recoveryJSON(
					`{"overtimeMinutes":480}`,
				),
				EstimatedCost: 80000,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	c := got.Cases[0]

	if !c.SimulatedResolved {
		t.Fatal(
			"combined alternate WC and overtime should recover all four days",
		)
	}

	if c.RecoveryDays != 4 {
		t.Fatalf(
			"expected four recovered days, got %d",
			c.RecoveryDays,
		)
	}

	if len(c.MatchedActionIDs) != 2 {
		t.Fatalf(
			"expected two matched actions, got %d",
			len(c.MatchedActionIDs),
		)
	}

	if got.Summary.EstimatedActionCost != 330000 {
		t.Fatalf(
			"expected action cost 330000, got %v",
			got.Summary.EstimatedActionCost,
		)
	}
}

func TestRecoverySimulationTargetMismatchHasNoEffect(
	t *testing.T,
) {
	got, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        uuid.New(),
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 80,
				RevenueAtRisk: 1_000_000,
				ImpactDays:    5,
				ExceptionType: "LATE_PURCHASE_ORDER",
				RootCauseRef:  "PO:EXPECTED",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         uuid.New(),
				ActionType: "EXPEDITE_PO",
				TargetType: "PURCHASE_ORDER",
				TargetRef:  "PO:OTHER",
				Parameters: recoveryJSON(
					`{"expediteDays":5}`,
				),
				EstimatedCost: 100000,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	c := got.Cases[0]

	if c.SimulatedResolved {
		t.Fatal(
			"unmatched action must not resolve case",
		)
	}

	if c.SimulatedPriorityScore !=
		c.BaselinePriorityScore {
		t.Fatal(
			"unmatched action changed priority score",
		)
	}

	if c.SimulatedRevenueAtRisk !=
		c.BaselineRevenueAtRisk {
		t.Fatal(
			"unmatched action changed revenue at risk",
		)
	}

	if got.Summary.RecoveredRevenue != 0 {
		t.Fatalf(
			"expected no recovered revenue, got %v",
			got.Summary.RecoveredRevenue,
		)
	}

	if got.Summary.NetValue != -100000 {
		t.Fatalf(
			"unproductive action cost must remain visible, got net value %v",
			got.Summary.NetValue,
		)
	}
}

func TestRecoverySimulationFrozenConflictNotSilentlyRescheduled(
	t *testing.T,
) {
	got, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        uuid.New(),
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 90,
				RevenueAtRisk: 2_000_000,
				ImpactDays:    3,
				ExceptionType: "FROZEN_HORIZON_CONFLICT",
				RootCauseRef:  "WO:WO-100",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         uuid.New(),
				ActionType: "RESCHEDULE_WO",
				TargetType: "WORK_ORDER",
				TargetRef:  "WO:WO-100",
				Parameters: recoveryJSON(
					`{"recoveryDays":3}`,
				),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	c := got.Cases[0]

	if c.SimulatedResolved {
		t.Fatal(
			"frozen horizon conflict must not be silently resolved",
		)
	}

	if got.Summary.P1Reduction != 0 {
		t.Fatalf(
			"frozen commitment should remain P1, reduction=%d",
			got.Summary.P1Reduction,
		)
	}
}

func TestRecoverySimulationRejectsInvalidParameters(
	t *testing.T,
) {
	_, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        uuid.New(),
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 80,
				RevenueAtRisk: 1,
				ImpactDays:    1,
				ExceptionType: "LATE_PURCHASE_ORDER",
				RootCauseRef:  "PO:1",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         uuid.New(),
				ActionType: "EXPEDITE_PO",
				TargetType: "PURCHASE_ORDER",
				TargetRef:  "PO:1",
				Parameters: recoveryJSON(
					`{"expediteDays":0}`,
				),
			},
		},
	)

	if err == nil {
		t.Fatal(
			"expected invalid expediteDays error",
		)
	}
}

func TestRecoverySimulationScoreIsBounded(
	t *testing.T,
) {
	got, err := SimulateRecoveryScenario(
		[]RecoverySimulationCaseInput{
			{
				CaseID:        uuid.New(),
				CurrentStatus: "OPEN",
				PriorityBand:  "P1",
				PriorityScore: 100,
				RevenueAtRisk: 100_000_000,
				ImpactDays:    365,
				ExceptionType: "CAPACITY_LATE",
				RootCauseRef:  "WC:1",
			},
		},
		[]domain.RecoveryScenarioAction{
			{
				ID:         uuid.New(),
				ActionType: "ADD_OVERTIME_CAPACITY",
				TargetType: "WORK_CENTER",
				TargetRef:  "WC:1",
				Parameters: recoveryJSON(
					`{"overtimeMinutes":10080}`,
				),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Summary.RecoveryScore < 0 ||
		got.Summary.RecoveryScore > 100 {
		t.Fatalf(
			"recovery score outside 0..100: %v",
			got.Summary.RecoveryScore,
		)
	}
}

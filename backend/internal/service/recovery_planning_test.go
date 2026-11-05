package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRecoveryPlanningActorAllowsPlanner(
	t *testing.T,
) {
	err := validateRecoveryPlanningActor(
		RecoveryPlanningActor{
			UserID:   uuid.New(),
			Username: "planner",
			Role:     "planner",
		},
	)

	if err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryPlanningActorAllowsAdmin(
	t *testing.T,
) {
	err := validateRecoveryPlanningActor(
		RecoveryPlanningActor{
			UserID:   uuid.New(),
			Username: "admin",
			Role:     "admin",
		},
	)

	if err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryPlanningActorRejectsOperator(
	t *testing.T,
) {
	err := validateRecoveryPlanningActor(
		RecoveryPlanningActor{
			UserID:   uuid.New(),
			Username: "operator",
			Role:     "operator",
		},
	)

	if err == nil {
		t.Fatal(
			"operator must not run Recovery Planning simulation",
		)
	}
}

func TestRecoverySimulationRequestDefaultsHorizon(
	t *testing.T,
) {
	id := uuid.New()

	got, err :=
		normalizeRecoverySimulationRequest(
			RecoverySimulationRequest{
				ScenarioID: id,
			},
		)

	if err != nil {
		t.Fatal(err)
	}

	if got.HorizonDays != 90 {
		t.Fatalf(
			"default horizon: want 90 got %d",
			got.HorizonDays,
		)
	}
}

func TestRecoverySimulationRequestRejectsInvalidHorizon(
	t *testing.T,
) {
	_, err :=
		normalizeRecoverySimulationRequest(
			RecoverySimulationRequest{
				ScenarioID: uuid.New(),

				HorizonDays: 731,
			},
		)

	if err == nil {
		t.Fatal(
			"expected invalid horizon error",
		)
	}
}

func TestRecoverySimulationRequestRequiresScenarioID(
	t *testing.T,
) {
	_, err :=
		normalizeRecoverySimulationRequest(
			RecoverySimulationRequest{
				HorizonDays: 90,
			},
		)

	if err == nil {
		t.Fatal(
			"expected missing scenarioId error",
		)
	}
}

func TestRecoveryFailureReasonDefaultsAndBounds(t *testing.T) {
	if got := recoveryFailureReason(nil); got != "recovery simulation failed" {
		t.Fatalf("default failure reason = %q", got)
	}

	long := strings.Repeat("失", recoveryFailureReasonMaxRunes+50)
	got := recoveryFailureReason(errors.New(long))
	if len([]rune(got)) != recoveryFailureReasonMaxRunes {
		t.Fatalf(
			"failure reason rune length: want %d got %d",
			recoveryFailureReasonMaxRunes,
			len([]rune(got)),
		)
	}
}

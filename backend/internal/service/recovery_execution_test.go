package service

import (
	"encoding/json"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestRecoveryExecutionActorSeparation(t *testing.T) {
	planner := RecoveryPlanningActor{UserID: uuid.New(), Username: "planner", Role: "planner"}
	admin := RecoveryPlanningActor{UserID: uuid.New(), Username: "admin", Role: "admin"}
	if err := validateRecoveryExecutionPrepareActor(planner); err != nil {
		t.Fatalf("planner prepare rejected: %v", err)
	}
	if err := validateRecoveryExecutionAdmin(planner); err == nil {
		t.Fatal("planner must not approve/execute")
	}
	if err := validateRecoveryExecutionAdmin(admin); err != nil {
		t.Fatalf("admin approve/execute rejected: %v", err)
	}
}

func TestRecoveryExecutionActionSnapshotHashIsCanonical(t *testing.T) {
	id := uuid.New()
	base := domain.RecoveryScenarioAction{
		ID: id, ScenarioID: uuid.New(), SequenceNo: 10,
		ActionType: "RESCHEDULE_WO", TargetType: "WORK_ORDER", TargetRef: "WO:42",
		Parameters:    json.RawMessage(`{"recoveryDays":2,"nested":{"b":2,"a":1}}`),
		EstimatedCost: 100.25, Note: "controlled",
	}
	other := base
	other.Parameters = json.RawMessage(`{"nested":{"a":1,"b":2},"recoveryDays":2}`)
	h1, err := recoveryExecutionActionSnapshotHash(base)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := recoveryExecutionActionSnapshotHash(other)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("canonical JSON hashes differ: %s != %s", h1, h2)
	}
	other.TargetRef = "WO:43"
	h3, _ := recoveryExecutionActionSnapshotHash(other)
	if h3 == h1 {
		t.Fatal("target change must change action snapshot hash")
	}
}

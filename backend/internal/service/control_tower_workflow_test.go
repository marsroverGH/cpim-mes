package service

import (
	"testing"

	"github.com/google/uuid"
)

func TestControlTowerWorkflowNormalizesAction(t *testing.T) {
	got, err := normalizeControlTowerCaseAction(
		ControlTowerCaseActionInput{
			ActionType: " acknowledge ",
			Comment:    " investigate ",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.ActionType != "ACKNOWLEDGE" {
		t.Fatalf("action=%s", got.ActionType)
	}

	if got.Comment != "investigate" {
		t.Fatalf("comment=%q", got.Comment)
	}
}

func TestControlTowerWorkflowAssignRequiresUser(t *testing.T) {
	_, err := normalizeControlTowerCaseAction(
		ControlTowerCaseActionInput{
			ActionType: "ASSIGN",
		},
	)
	if err == nil {
		t.Fatal("ASSIGN without user must fail")
	}
}

func TestControlTowerWorkflowAssignKeepsUser(t *testing.T) {
	id := uuid.New()

	got, err := normalizeControlTowerCaseAction(
		ControlTowerCaseActionInput{
			ActionType:       "ASSIGN",
			AssignedToUserID: &id,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.AssignedToUserID == nil ||
		*got.AssignedToUserID != id {
		t.Fatal("assigned user lost")
	}
}

func TestControlTowerWorkflowNonAssignDropsUser(t *testing.T) {
	id := uuid.New()

	got, err := normalizeControlTowerCaseAction(
		ControlTowerCaseActionInput{
			ActionType:       "RESOLVE",
			AssignedToUserID: &id,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.AssignedToUserID != nil {
		t.Fatal("non-ASSIGN action must not carry assignee")
	}
}

func TestControlTowerWorkflowRejectsOperator(t *testing.T) {
	err := validateControlTowerActor(ControlTowerActor{
		UserID:   uuid.New(),
		Username: "operator",
		Role:     "operator",
	})
	if err == nil {
		t.Fatal("operator must not mutate Control Tower workflow")
	}
}

func TestControlTowerWorkflowAllowsPlanner(t *testing.T) {
	err := validateControlTowerActor(ControlTowerActor{
		UserID:   uuid.New(),
		Username: "planner",
		Role:     "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestControlTowerDashboardFilterValidation(t *testing.T) {
	got, err := normalizeControlTowerDashboardFilter(
		ControlTowerDashboardFilter{
			Status:       " assigned ",
			PriorityBand: "p1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != "ASSIGNED" {
		t.Fatalf("status=%s", got.Status)
	}

	if got.PriorityBand != "P1" {
		t.Fatalf("band=%s", got.PriorityBand)
	}
}

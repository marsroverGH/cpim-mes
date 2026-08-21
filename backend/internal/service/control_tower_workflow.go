package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

type ControlTowerActor struct {
	UserID   uuid.UUID
	Username string
	Role     string
}

type ControlTowerCaseActionInput struct {
	ActionType       string     `json:"actionType"`
	AssignedToUserID *uuid.UUID `json:"assignedToUserId,omitempty"`
	Comment          string     `json:"comment"`
}

func normalizeControlTowerCaseAction(
	in ControlTowerCaseActionInput,
) (ControlTowerCaseActionInput, error) {
	in.ActionType = strings.ToUpper(strings.TrimSpace(in.ActionType))
	in.Comment = strings.TrimSpace(in.Comment)

	switch in.ActionType {
	case "ACKNOWLEDGE",
		"ASSIGN",
		"START",
		"RESOLVE",
		"REOPEN",
		"CLOSE":
	default:
		return in, domain.NewBadRequest(
			"invalid Control Tower action", nil,
		)
	}

	if in.ActionType == "ASSIGN" && in.AssignedToUserID == nil {
		return in, domain.NewBadRequest(
			"ASSIGN requires assignedToUserId", nil,
		)
	}

	if in.ActionType != "ASSIGN" {
		in.AssignedToUserID = nil
	}

	return in, nil
}

func validateControlTowerActor(actor ControlTowerActor) error {
	if actor.UserID == uuid.Nil ||
		strings.TrimSpace(actor.Username) == "" {
		return domain.NewUnauthorized(
			"authenticated Control Tower actor is required",
		)
	}

	switch strings.ToLower(strings.TrimSpace(actor.Role)) {
	case "admin", "planner":
		return nil
	default:
		return domain.NewUnauthorized(
			"Control Tower actions require planner/admin actor",
		)
	}
}

// ActOnCase appends lifecycle/assignment evidence. The database trigger owns
// the authoritative state-machine transition and actor/assignee validation.
func (s *ControlTowerService) ActOnCase(
	ctx context.Context,
	caseID uuid.UUID,
	in ControlTowerCaseActionInput,
	actor ControlTowerActor,
) (*domain.ControlTowerCaseAction, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	if caseID == uuid.Nil {
		return nil, domain.NewBadRequest(
			"caseId is required", nil,
		)
	}

	if err := validateControlTowerActor(actor); err != nil {
		return nil, err
	}

	in, err := normalizeControlTowerCaseAction(in)
	if err != nil {
		return nil, err
	}

	var row domain.ControlTowerCaseAction

	err = s.db.GetContext(ctx, &row, `
INSERT INTO control_tower_case_actions(
  id,
  case_id,
  action_type,
  assigned_to_user_id,
  actor_user_id,
  actor_username,
  comment
)
VALUES($1,$2,$3,$4,$5,$6,$7)
RETURNING *
`,
		uuid.New(),
		caseID,
		in.ActionType,
		in.AssignedToUserID,
		actor.UserID,
		strings.TrimSpace(actor.Username),
		in.Comment,
	)
	if err != nil {
		return nil, err
	}

	return &row, nil
}

func (s *ControlTowerService) CaseActions(
	ctx context.Context,
	caseID uuid.UUID,
) ([]domain.ControlTowerCaseAction, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	var rows []domain.ControlTowerCaseAction

	err := s.db.SelectContext(ctx, &rows, `
SELECT *
FROM control_tower_case_actions
WHERE case_id=$1
ORDER BY occurred_at,id
`, caseID)

	return rows, err
}

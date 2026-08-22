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
)

type RecoveryScenarioCreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RecoveryScenarioUpdateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RecoveryScenarioActionInput struct {
	SequenceNo int `json:"sequenceNo"`

	ActionType string `json:"actionType"`
	TargetType string `json:"targetType"`
	TargetRef  string `json:"targetRef"`

	Parameters json.RawMessage `json:"parameters"`

	EstimatedCost float64 `json:"estimatedCost"`
	Note          string  `json:"note"`
}

type RecoveryScenarioPublishInput struct {
	RunID   uuid.UUID `json:"runId"`
	Comment string    `json:"comment"`
}

func recoveryScenarioNo(
	now time.Time,
	id uuid.UUID,
) string {
	raw := strings.ReplaceAll(
		id.String(),
		"-",
		"",
	)

	return fmt.Sprintf(
		"REC-%s-%s",
		now.UTC().Format("20060102"),
		strings.ToUpper(raw[:8]),
	)
}

func normalizeRecoveryScenarioName(
	v string,
) (string, error) {
	v = strings.TrimSpace(v)

	if v == "" {
		return "", domain.NewBadRequest(
			"scenario name is required",
			nil,
		)
	}

	if len(v) > 200 {
		return "", domain.NewBadRequest(
			"scenario name must be 200 characters or fewer",
			nil,
		)
	}

	return v, nil
}

func normalizeRecoveryScenarioStatus(
	v string,
) (string, error) {
	v = strings.ToUpper(
		strings.TrimSpace(v),
	)

	if v == "" {
		return "", nil
	}

	switch v {
	case "DRAFT",
		"SIMULATED",
		"PUBLISHED",
		"ARCHIVED":
		return v, nil
	default:
		return "", domain.NewBadRequest(
			"invalid recovery scenario status",
			nil,
		)
	}
}

func normalizeRecoveryTargetType(
	v string,
) (string, error) {
	v = strings.ToUpper(
		strings.TrimSpace(v),
	)

	switch v {
	case "PURCHASE_ORDER",
		"WORK_ORDER",
		"WORK_ORDER_OPERATION",
		"WORK_CENTER":
		return v, nil
	default:
		return "", domain.NewBadRequest(
			"invalid recovery action targetType",
			nil,
		)
	}
}

func normalizeRecoveryScenarioAction(
	scenarioID uuid.UUID,
	actionID uuid.UUID,
	sequenceNo int,
	in RecoveryScenarioActionInput,
) (domain.RecoveryScenarioAction, error) {
	if scenarioID == uuid.Nil {
		return domain.RecoveryScenarioAction{},
			domain.NewBadRequest(
				"scenarioId is required",
				nil,
			)
	}

	if actionID == uuid.Nil {
		actionID = uuid.New()
	}

	if sequenceNo <= 0 {
		return domain.RecoveryScenarioAction{},
			domain.NewBadRequest(
				"sequenceNo must be greater than zero",
				nil,
			)
	}

	targetType, err :=
		normalizeRecoveryTargetType(
			in.TargetType,
		)
	if err != nil {
		return domain.RecoveryScenarioAction{}, err
	}

	if in.EstimatedCost < 0 {
		return domain.RecoveryScenarioAction{},
			domain.NewBadRequest(
				"estimatedCost must be >= 0",
				nil,
			)
	}

	params := in.Parameters

	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}

	action := domain.RecoveryScenarioAction{
		ID:         actionID,
		ScenarioID: scenarioID,
		SequenceNo: sequenceNo,

		ActionType: strings.ToUpper(
			strings.TrimSpace(
				in.ActionType,
			),
		),

		TargetType: targetType,

		TargetRef: strings.TrimSpace(
			in.TargetRef,
		),

		Parameters: params,

		EstimatedCost: recoveryRoundMoney(
			in.EstimatedCost,
		),

		Note: strings.TrimSpace(
			in.Note,
		),
	}

	if _, err :=
		validateRecoveryAction(action); err != nil {
		return domain.RecoveryScenarioAction{},
			domain.NewBadRequest(
				err.Error(),
				nil,
			)
	}

	return action, nil
}

func (s *RecoveryPlanningService) CreateScenario(
	ctx context.Context,
	in RecoveryScenarioCreateInput,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenario, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	name, err :=
		normalizeRecoveryScenarioName(
			in.Name,
		)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	id := uuid.New()

	row := domain.RecoveryScenario{
		ID: id,

		ScenarioNo: recoveryScenarioNo(
			now,
			id,
		),

		Name: name,

		Description: strings.TrimSpace(
			in.Description,
		),

		Status: "DRAFT",

		BaselineAsOf: now,

		CreatedByUsername: strings.TrimSpace(
			actor.Username,
		),

		CreatedAt: now,
		UpdatedAt: now,
	}

	if actor.UserID != uuid.Nil {
		userID := actor.UserID
		row.CreatedByUserID = &userID
	}

	err = s.db.GetContext(
		ctx,
		&row,
		`
INSERT INTO recovery_scenarios(
    id,
    scenario_no,
    name,
    description,
    status,
    baseline_as_of,
    created_by_user_id,
    created_by_username,
    created_at,
    updated_at
)
VALUES(
    $1,$2,$3,$4,'DRAFT',
    $5,$6,$7,$8,$9
)
RETURNING *
`,
		row.ID,
		row.ScenarioNo,
		row.Name,
		row.Description,
		row.BaselineAsOf,
		row.CreatedByUserID,
		row.CreatedByUsername,
		row.CreatedAt,
		row.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &row, nil
}

func (s *RecoveryPlanningService) ListScenarios(
	ctx context.Context,
	status string,
) ([]domain.RecoveryScenario, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	status, err :=
		normalizeRecoveryScenarioStatus(
			status,
		)
	if err != nil {
		return nil, err
	}

	rows := []domain.RecoveryScenario{}

	if status == "" {
		err = s.db.SelectContext(
			ctx,
			&rows,
			`
SELECT *
FROM recovery_scenarios
ORDER BY updated_at DESC,id DESC
`,
		)
	} else {
		err = s.db.SelectContext(
			ctx,
			&rows,
			`
SELECT *
FROM recovery_scenarios
WHERE status=$1
ORDER BY updated_at DESC,id DESC
`,
			status,
		)
	}

	return rows, err
}

func (s *RecoveryPlanningService) GetScenario(
	ctx context.Context,
	id uuid.UUID,
) (*domain.RecoveryScenario, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if id == uuid.Nil {
		return nil, domain.NewBadRequest(
			"scenarioId is required",
			nil,
		)
	}

	var row domain.RecoveryScenario

	err := s.db.GetContext(
		ctx,
		&row,
		`
SELECT *
FROM recovery_scenarios
WHERE id=$1
`,
		id,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}

	if err != nil {
		return nil, err
	}

	return &row, nil
}

func (s *RecoveryPlanningService) UpdateScenario(
	ctx context.Context,
	id uuid.UUID,
	in RecoveryScenarioUpdateInput,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenario, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	name, err :=
		normalizeRecoveryScenarioName(
			in.Name,
		)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		nil,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var current domain.RecoveryScenario

	err = tx.GetContext(
		ctx,
		&current,
		`
SELECT *
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return nil, err
	}

	if current.Status != "DRAFT" {
		return nil, domain.NewConflict(
			"only DRAFT recovery scenario can be edited",
		)
	}

	now := time.Now().UTC()

	var row domain.RecoveryScenario

	err = tx.GetContext(
		ctx,
		&row,
		`
UPDATE recovery_scenarios
SET
    name=$2,
    description=$3,
    updated_at=$4
WHERE id=$1
RETURNING *
`,
		id,
		name,
		strings.TrimSpace(
			in.Description,
		),
		now,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &row, nil
}

func (s *RecoveryPlanningService) ArchiveScenario(
	ctx context.Context,
	id uuid.UUID,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenario, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		nil,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var current domain.RecoveryScenario

	err = tx.GetContext(
		ctx,
		&current,
		`
SELECT *
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return nil, err
	}

	if current.Status == "ARCHIVED" {
		return nil, domain.NewConflict(
			"recovery scenario is already archived",
		)
	}

	now := time.Now().UTC()

	var row domain.RecoveryScenario

	err = tx.GetContext(
		ctx,
		&row,
		`
UPDATE recovery_scenarios
SET
    status='ARCHIVED',
    updated_at=$2
WHERE id=$1
RETURNING *
`,
		id,
		now,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &row, nil
}

func (s *RecoveryPlanningService) ListScenarioActions(
	ctx context.Context,
	scenarioID uuid.UUID,
) ([]domain.RecoveryScenarioAction, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	rows := []domain.RecoveryScenarioAction{}

	err := s.db.SelectContext(
		ctx,
		&rows,
		`
SELECT *
FROM recovery_scenario_actions
WHERE scenario_id=$1
ORDER BY sequence_no,id
`,
		scenarioID,
	)

	return rows, err
}

func (s *RecoveryPlanningService) AddScenarioAction(
	ctx context.Context,
	scenarioID uuid.UUID,
	in RecoveryScenarioActionInput,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenarioAction, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		nil,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var status string

	err = tx.GetContext(
		ctx,
		&status,
		`
SELECT status
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return nil, err
	}

	if status != "DRAFT" {
		return nil, domain.NewConflict(
			"recovery scenario actions are editable only while DRAFT",
		)
	}

	sequenceNo := in.SequenceNo

	if sequenceNo == 0 {
		err = tx.GetContext(
			ctx,
			&sequenceNo,
			`
SELECT COALESCE(MAX(sequence_no),0)+1
FROM recovery_scenario_actions
WHERE scenario_id=$1
`,
			scenarioID,
		)
		if err != nil {
			return nil, err
		}
	}

	action, err :=
		normalizeRecoveryScenarioAction(
			scenarioID,
			uuid.New(),
			sequenceNo,
			in,
		)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	action.CreatedAt = now
	action.UpdatedAt = now

	err = tx.GetContext(
		ctx,
		&action,
		`
INSERT INTO recovery_scenario_actions(
    id,
    scenario_id,
    sequence_no,
    action_type,
    target_type,
    target_ref,
    parameters,
    estimated_cost,
    note,
    created_at,
    updated_at
)
VALUES(
    $1,$2,$3,$4,$5,$6,
    $7::jsonb,$8,$9,$10,$11
)
RETURNING *
`,
		action.ID,
		action.ScenarioID,
		action.SequenceNo,
		action.ActionType,
		action.TargetType,
		action.TargetRef,
		string(action.Parameters),
		action.EstimatedCost,
		action.Note,
		action.CreatedAt,
		action.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &action, nil
}

func (s *RecoveryPlanningService) UpdateScenarioAction(
	ctx context.Context,
	scenarioID uuid.UUID,
	actionID uuid.UUID,
	in RecoveryScenarioActionInput,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenarioAction, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		nil,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var status string

	err = tx.GetContext(
		ctx,
		&status,
		`
SELECT status
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return nil, err
	}

	if status != "DRAFT" {
		return nil, domain.NewConflict(
			"recovery scenario actions are editable only while DRAFT",
		)
	}

	var current domain.RecoveryScenarioAction

	err = tx.GetContext(
		ctx,
		&current,
		`
SELECT *
FROM recovery_scenario_actions
WHERE id=$1
  AND scenario_id=$2
FOR UPDATE
`,
		actionID,
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario Action",
		)
	}
	if err != nil {
		return nil, err
	}

	sequenceNo := in.SequenceNo

	if sequenceNo == 0 {
		sequenceNo = current.SequenceNo
	}

	action, err :=
		normalizeRecoveryScenarioAction(
			scenarioID,
			actionID,
			sequenceNo,
			in,
		)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	err = tx.GetContext(
		ctx,
		&action,
		`
UPDATE recovery_scenario_actions
SET
    sequence_no=$3,
    action_type=$4,
    target_type=$5,
    target_ref=$6,
    parameters=$7::jsonb,
    estimated_cost=$8,
    note=$9,
    updated_at=$10
WHERE id=$1
  AND scenario_id=$2
RETURNING *
`,
		actionID,
		scenarioID,
		action.SequenceNo,
		action.ActionType,
		action.TargetType,
		action.TargetRef,
		string(action.Parameters),
		action.EstimatedCost,
		action.Note,
		now,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &action, nil
}

func (s *RecoveryPlanningService) DeleteScenarioAction(
	ctx context.Context,
	scenarioID uuid.UUID,
	actionID uuid.UUID,
	actor RecoveryPlanningActor,
) error {
	if s == nil || s.db == nil {
		return fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return err
	}

	tx, err := s.db.BeginTxx(
		ctx,
		nil,
	)
	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var status string

	err = tx.GetContext(
		ctx,
		&status,
		`
SELECT status
FROM recovery_scenarios
WHERE id=$1
FOR UPDATE
`,
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return err
	}

	if status != "DRAFT" {
		return domain.NewConflict(
			"recovery scenario actions are editable only while DRAFT",
		)
	}

	result, err := tx.ExecContext(
		ctx,
		`
DELETE FROM recovery_scenario_actions
WHERE id=$1
  AND scenario_id=$2
`,
		actionID,
		scenarioID,
	)
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if n != 1 {
		return domain.NewNotFound(
			"Recovery Scenario Action",
		)
	}

	return tx.Commit()
}

func (s *RecoveryPlanningService) CompareScenarios(
	ctx context.Context,
	baselineHash string,
) ([]domain.RecoveryScenarioComparison, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	baselineHash =
		strings.TrimSpace(
			baselineHash,
		)

	rows := []domain.RecoveryScenarioComparison{}

	if baselineHash == "" {
		err := s.db.SelectContext(
			ctx,
			&rows,
			`
SELECT *
FROM v_recovery_scenario_comparison
ORDER BY
    baseline_hash,
    comparison_rank,
    scenario_id
`,
		)

		return rows, err
	}

	if len(baselineHash) != 64 {
		return nil, domain.NewBadRequest(
			"baselineHash must be a 64-character SHA-256 hash",
			nil,
		)
	}

	err := s.db.SelectContext(
		ctx,
		&rows,
		`
SELECT *
FROM v_recovery_scenario_comparison
WHERE baseline_hash=$1
ORDER BY
    comparison_rank,
    scenario_id
`,
		baselineHash,
	)

	return rows, err
}

func recoveryPublicationHash(
	scenario domain.RecoveryScenario,
	run domain.RecoveryScenarioRun,
	summary domain.RecoveryScenarioSummary,
	comment string,
) (string, error) {
	runResultHash := ""

	if run.ResultHash != nil {
		runResultHash = *run.ResultHash
	}

	return recoverySHA256(
		struct {
			ScenarioID string `json:"scenarioId"`
			RunID      string `json:"runId"`

			BaselineHash string `json:"baselineHash"`
			RequestHash  string `json:"requestHash"`
			ResultHash   string `json:"resultHash"`

			SummaryHash string `json:"summaryHash"`
			Comment     string `json:"comment"`
		}{
			ScenarioID: scenario.ID.String(),

			RunID: run.ID.String(),

			BaselineHash: run.BaselineHash,

			RequestHash: run.RequestHash,

			ResultHash: runResultHash,

			SummaryHash: summary.ResultHash,

			Comment: strings.TrimSpace(
				comment,
			),
		},
	)
}

func (s *RecoveryPlanningService) PublishScenario(
	ctx context.Context,
	scenarioID uuid.UUID,
	in RecoveryScenarioPublishInput,
	actor RecoveryPlanningActor,
) (*domain.RecoveryScenarioPublication, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"Recovery Planning database is required",
		)
	}

	if err :=
		validateRecoveryPlanningActor(
			actor,
		); err != nil {
		return nil, err
	}

	if scenarioID == uuid.Nil {
		return nil, domain.NewBadRequest(
			"scenarioId is required",
			nil,
		)
	}

	if in.RunID == uuid.Nil {
		return nil, domain.NewBadRequest(
			"runId is required",
			nil,
		)
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
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewNotFound(
			"Recovery Scenario",
		)
	}
	if err != nil {
		return nil, err
	}

	if scenario.Status != "SIMULATED" {
		return nil, domain.NewConflict(
			"only SIMULATED recovery scenario can be published",
		)
	}

	var run domain.RecoveryScenarioRun

	err = tx.GetContext(
		ctx,
		&run,
		`
SELECT *
FROM recovery_scenario_runs
WHERE id=$1
  AND scenario_id=$2
  AND status='SUCCEEDED'
`,
		in.RunID,
		scenarioID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewBadRequest(
			"publication requires a successful run for the same scenario",
			nil,
		)
	}
	if err != nil {
		return nil, err
	}

	var latestRunID uuid.UUID

	err = tx.GetContext(
		ctx,
		&latestRunID,
		`
SELECT id
FROM recovery_scenario_runs
WHERE scenario_id=$1
  AND status='SUCCEEDED'
ORDER BY completed_at DESC,id DESC
LIMIT 1
`,
		scenarioID,
	)
	if err != nil {
		return nil, err
	}

	if latestRunID != run.ID {
		return nil, domain.NewConflict(
			"only the latest successful recovery run can be published",
		)
	}

	var summary domain.RecoveryScenarioSummary

	err = tx.GetContext(
		ctx,
		&summary,
		`
SELECT *
FROM recovery_scenario_summaries
WHERE run_id=$1
`,
		run.ID,
	)
	if err != nil {
		return nil, err
	}

	publicationHash, err :=
		recoveryPublicationHash(
			scenario,
			run,
			summary,
			in.Comment,
		)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	publication :=
		domain.RecoveryScenarioPublication{
			ID: uuid.New(),

			ScenarioID: scenario.ID,

			RunID: run.ID,

			PublicationHash: publicationHash,

			Comment: strings.TrimSpace(
				in.Comment,
			),

			PublishedByUsername: strings.TrimSpace(
				actor.Username,
			),

			PublishedAt: now,
		}

	if actor.UserID != uuid.Nil {
		userID := actor.UserID

		publication.PublishedByUserID =
			&userID
	}

	err = tx.GetContext(
		ctx,
		&publication,
		`
INSERT INTO recovery_scenario_publications(
    id,
    scenario_id,
    run_id,
    publication_hash,
    comment,
    published_by_user_id,
    published_by_username,
    published_at
)
VALUES(
    $1,$2,$3,$4,$5,$6,$7,$8
)
RETURNING *
`,
		publication.ID,
		publication.ScenarioID,
		publication.RunID,
		publication.PublicationHash,
		publication.Comment,
		publication.PublishedByUserID,
		publication.PublishedByUsername,
		publication.PublishedAt,
	)
	if err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(
		ctx,
		`
UPDATE recovery_scenarios
SET
    status='PUBLISHED',
    published_at=$2,
    updated_at=$2
WHERE id=$1
  AND status='SIMULATED'
`,
		scenario.ID,
		now,
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
			"recovery scenario publish transition lost",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &publication, nil
}

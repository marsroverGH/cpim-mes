package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// RecoveryExecutionService is deliberately separate from RecoveryPlanningService.
// 0041 Simulation/Publish remains evidence-only. This service is the only 0042
// boundary allowed to mutate operational state from published recovery evidence.
type RecoveryExecutionService struct {
	db                 *sqlx.DB
	workflow           *WorkflowService
	supplierScheduling *SupplierSchedulingService
}

func NewRecoveryExecutionService(db *sqlx.DB, workflow *WorkflowService, supplier *SupplierSchedulingService) *RecoveryExecutionService {
	return &RecoveryExecutionService{db: db, workflow: workflow, supplierScheduling: supplier}
}

type RecoveryExecutionPrepareInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
	Comment        string `json:"comment"`
}

type RecoveryExecutionApprovalInput struct {
	Comment string `json:"comment"`
}

type RecoveryExecutionCancelInput struct {
	Comment string `json:"comment"`
}

type recoveryExecutionParams struct {
	ExpediteDays          int    `json:"expediteDays"`
	RecoveryDays          int    `json:"recoveryDays"`
	AlternateWorkCenter   string `json:"alternateWorkCenterRef"`
	WorkOrderOperationRef string `json:"workOrderOperationRef"`
	OvertimeMinutes       int    `json:"overtimeMinutes"`
	EffectiveDate         string `json:"effectiveDate"`
}

func validateRecoveryExecutionPrepareActor(actor RecoveryPlanningActor) error {
	return validateRecoveryPlanningActor(actor)
}

func validateRecoveryExecutionAdmin(actor RecoveryPlanningActor) error {
	if strings.ToLower(strings.TrimSpace(actor.Role)) != "admin" {
		return domain.NewForbidden("controlled recovery approval/execution requires admin")
	}
	if actor.UserID == uuid.Nil || strings.TrimSpace(actor.Username) == "" {
		return domain.NewUnauthorized("authenticated recovery execution actor is required")
	}
	return nil
}

func stableRecoveryExecutionHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func normalizedJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func recoveryExecutionActionSnapshotHash(a domain.RecoveryScenarioAction) (string, error) {
	return stableRecoveryExecutionHash(struct {
		ID            string `json:"id"`
		SequenceNo    int    `json:"sequenceNo"`
		ActionType    string `json:"actionType"`
		TargetType    string `json:"targetType"`
		TargetRef     string `json:"targetRef"`
		Parameters    any    `json:"parameters"`
		EstimatedCost string `json:"estimatedCost"`
		Note          string `json:"note"`
	}{
		ID: a.ID.String(), SequenceNo: a.SequenceNo, ActionType: a.ActionType,
		TargetType: a.TargetType, TargetRef: a.TargetRef, Parameters: normalizedJSON(a.Parameters),
		EstimatedCost: fmt.Sprintf("%.2f", a.EstimatedCost), Note: a.Note,
	})
}

func recoveryExecutionActionSetHash(actions []domain.RecoveryScenarioAction) (string, error) {
	type row struct {
		Sequence int    `json:"sequence"`
		Hash     string `json:"hash"`
	}
	rows := make([]row, 0, len(actions))
	for _, a := range actions {
		h, err := recoveryExecutionActionSnapshotHash(a)
		if err != nil {
			return "", err
		}
		rows = append(rows, row{Sequence: a.SequenceNo, Hash: h})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Sequence < rows[j].Sequence })
	return stableRecoveryExecutionHash(rows)
}

func (s *RecoveryExecutionService) List(ctx context.Context, status string) ([]domain.RecoveryPlanExecution, error) {
	status = strings.ToUpper(strings.TrimSpace(status))
	var rows []domain.RecoveryPlanExecution
	if status == "" {
		if err := s.db.SelectContext(ctx, &rows, `SELECT * FROM recovery_plan_executions ORDER BY prepared_at DESC,id DESC`); err != nil {
			return nil, err
		}
		return rows, nil
	}
	if err := s.db.SelectContext(ctx, &rows, `SELECT * FROM recovery_plan_executions WHERE status=$1 ORDER BY prepared_at DESC,id DESC`, status); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *RecoveryExecutionService) Get(ctx context.Context, id uuid.UUID) (*domain.RecoveryPlanExecutionDetail, error) {
	var e domain.RecoveryPlanExecution
	if err := s.db.GetContext(ctx, &e, `SELECT * FROM recovery_plan_executions WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("Recovery Execution")
		}
		return nil, err
	}
	var actions []domain.RecoveryPlanExecutionAction
	if err := s.db.SelectContext(ctx, &actions, `SELECT * FROM recovery_plan_execution_actions WHERE execution_id=$1 ORDER BY sequence_no,id`, id); err != nil {
		return nil, err
	}
	return &domain.RecoveryPlanExecutionDetail{Execution: e, Actions: actions}, nil
}

// Prepare retries the whole operation on serialization failures / deadlocks.
func (s *RecoveryExecutionService) Prepare(ctx context.Context, scenarioID uuid.UUID, in RecoveryExecutionPrepareInput, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	return retrySerializable(ctx, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return s.prepareOnce(ctx, scenarioID, in, actor)
	})
}

func (s *RecoveryExecutionService) prepareOnce(ctx context.Context, scenarioID uuid.UUID, in RecoveryExecutionPrepareInput, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	if err := validateRecoveryExecutionPrepareActor(actor); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return nil, domain.NewBadRequest("idempotencyKey is required", nil)
	}
	if len(key) > 200 {
		return nil, domain.NewBadRequest("idempotencyKey must be 200 characters or fewer", nil)
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recovery-execution:"+scenarioID.String()+":"+key); err != nil {
		return nil, err
	}

	var existing domain.RecoveryPlanExecution
	if err := tx.GetContext(ctx, &existing, `SELECT * FROM recovery_plan_executions WHERE scenario_id=$1 AND idempotency_key=$2`, scenarioID, key); err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.Get(ctx, existing.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var scenario domain.RecoveryScenario
	if err := tx.GetContext(ctx, &scenario, `SELECT * FROM recovery_scenarios WHERE id=$1 FOR SHARE`, scenarioID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("Recovery Scenario")
		}
		return nil, err
	}
	if scenario.Status != "PUBLISHED" {
		return nil, domain.NewConflict("only PUBLISHED recovery scenario can be prepared for execution")
	}

	var pub domain.RecoveryScenarioPublication
	if err := tx.GetContext(ctx, &pub, `SELECT * FROM recovery_scenario_publications WHERE scenario_id=$1`, scenarioID); err != nil {
		return nil, domain.NewConflict("published recovery scenario has no publication evidence")
	}
	var run domain.RecoveryScenarioRun
	if err := tx.GetContext(ctx, &run, `SELECT * FROM recovery_scenario_runs WHERE id=$1 AND scenario_id=$2 AND status='SUCCEEDED'`, pub.RunID, scenarioID); err != nil {
		return nil, domain.NewConflict("published recovery run is not a successful run")
	}
	if run.ResultHash == nil || len(*run.ResultHash) != 64 {
		return nil, domain.NewConflict("published recovery run has no canonical result hash")
	}

	var actions []domain.RecoveryScenarioAction
	if err := tx.SelectContext(ctx, &actions, `SELECT * FROM recovery_scenario_actions WHERE scenario_id=$1 ORDER BY sequence_no,id`, scenarioID); err != nil {
		return nil, err
	}
	if len(actions) == 0 {
		return nil, domain.NewConflict("published recovery scenario has no actions")
	}
	actionSetHash, err := recoveryExecutionActionSetHash(actions)
	if err != nil {
		return nil, err
	}

	exec := domain.RecoveryPlanExecution{
		ID: uuid.New(), ScenarioID: scenarioID, PublicationID: pub.ID, RunID: run.ID,
		IdempotencyKey: key, Status: "PREPARED", PublicationHash: pub.PublicationHash,
		RunResultHash: *run.ResultHash, ActionSetHash: actionSetHash,
		PrepareComment: strings.TrimSpace(in.Comment), ActionCount: len(actions),
		PreparedByUserID: actor.UserID, PreparedByUsername: strings.TrimSpace(actor.Username),
	}
	if err := tx.GetContext(ctx, &exec, `
INSERT INTO recovery_plan_executions(
 id,scenario_id,publication_id,run_id,idempotency_key,status,publication_hash,run_result_hash,action_set_hash,
 prepare_comment,action_count,prepared_by_user_id,prepared_by_username)
VALUES($1,$2,$3,$4,$5,'PREPARED',$6,$7,$8,$9,$10,$11,$12)
RETURNING *`, exec.ID, exec.ScenarioID, exec.PublicationID, exec.RunID, exec.IdempotencyKey,
		exec.PublicationHash, exec.RunResultHash, exec.ActionSetHash, exec.PrepareComment,
		exec.ActionCount, exec.PreparedByUserID, exec.PreparedByUsername); err != nil {
		return nil, err
	}

	for _, a := range actions {
		h, err := recoveryExecutionActionSnapshotHash(a)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO recovery_plan_execution_actions(
 id,execution_id,scenario_action_id,sequence_no,action_type,target_type,target_ref,parameters,estimated_cost,note,action_snapshot_hash)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, uuid.New(), exec.ID, a.ID, a.SequenceNo, a.ActionType, a.TargetType,
			a.TargetRef, a.Parameters, a.EstimatedCost, a.Note, h); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, exec.ID)
}

func decodeRecoveryExecutionParams(raw json.RawMessage) (recoveryExecutionParams, error) {
	var p recoveryExecutionParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, domain.NewBadRequest("invalid recovery action parameters", err)
	}
	return p, nil
}

func stripRefPrefix(ref, prefix string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(strings.ToUpper(ref), prefix) {
		return strings.TrimSpace(ref[len(prefix):])
	}
	return ref
}

func resolveUUIDOrLookup(ctx context.Context, q *sqlx.Tx, ref, prefix, lookupSQL string) (uuid.UUID, error) {
	ref = stripRefPrefix(ref, prefix)
	if id, err := uuid.Parse(ref); err == nil && id != uuid.Nil {
		return id, nil
	}
	var id uuid.UUID
	if err := q.GetContext(ctx, &id, lookupSQL, ref); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, domain.NewNotFound(strings.TrimSuffix(prefix, ":"))
		}
		return uuid.Nil, err
	}
	return id, nil
}

func resolvePO(ctx context.Context, q *sqlx.Tx, ref string) (uuid.UUID, error) {
	return resolveUUIDOrLookup(ctx, q, ref, "PO:", `SELECT id FROM purchase_orders WHERE po_no=$1`)
}
func resolveWO(ctx context.Context, q *sqlx.Tx, ref string) (uuid.UUID, error) {
	return resolveUUIDOrLookup(ctx, q, ref, "WO:", `SELECT id FROM work_orders WHERE order_no=$1`)
}
func resolveWC(ctx context.Context, q *sqlx.Tx, ref string) (uuid.UUID, error) {
	return resolveUUIDOrLookup(ctx, q, ref, "WC:", `SELECT id FROM work_centers WHERE code=$1`)
}
func resolveWOOp(ctx context.Context, q *sqlx.Tx, ref string) (uuid.UUID, error) {
	ref = stripRefPrefix(ref, "WOOP:")
	id, err := uuid.Parse(ref)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, domain.NewBadRequest("workOrderOperationRef must be a WO operation UUID or WOOP:<uuid>", err)
	}
	var exists uuid.UUID
	if err := q.GetContext(ctx, &exists, `SELECT id FROM wo_operations WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, domain.NewNotFound("WO operation")
		}
		return uuid.Nil, err
	}
	return exists, nil
}

func executionPreflightBlocked(reason string, state map[string]any) (json.RawMessage, string, string, error) {
	state["ready"] = false
	state["blockedReason"] = reason
	b, err := json.Marshal(state)
	if err != nil {
		return nil, "", "", err
	}
	h, err := stableRecoveryExecutionHash(state)
	return b, h, reason, err
}
func executionPreflightReady(state map[string]any) (json.RawMessage, string, string, error) {
	state["ready"] = true
	b, err := json.Marshal(state)
	if err != nil {
		return nil, "", "", err
	}
	h, err := stableRecoveryExecutionHash(state)
	return b, h, "", err
}

// preflightActionTx resolves a published hypothetical action to one concrete,
// lockable operational target. No operational state is changed here.
func (s *RecoveryExecutionService) preflightActionTx(ctx context.Context, tx *sqlx.Tx, a domain.RecoveryPlanExecutionAction) (json.RawMessage, string, string, error) {
	if strings.TrimSpace(a.TargetRef) == "*" {
		return executionPreflightBlocked("wildcard targetRef is simulation-only and cannot be executed", map[string]any{"actionType": a.ActionType, "targetRef": a.TargetRef})
	}
	p, err := decodeRecoveryExecutionParams(a.Parameters)
	if err != nil {
		return nil, "", "", err
	}
	switch a.ActionType {
	case "EXPEDITE_PO":
		if a.TargetType != "PURCHASE_ORDER" {
			return executionPreflightBlocked("EXPEDITE_PO requires PURCHASE_ORDER target", map[string]any{"targetType": a.TargetType})
		}
		poID, err := resolvePO(ctx, tx, a.TargetRef)
		if err != nil {
			return executionPreflightBlocked(err.Error(), map[string]any{"targetRef": a.TargetRef})
		}
		var row struct {
			ID          uuid.UUID  `db:"id"`
			PONo        string     `db:"po_no"`
			Quantity    float64    `db:"quantity"`
			ReceivedQty float64    `db:"received_qty"`
			OrderDate   time.Time  `db:"order_date"`
			DueDate     time.Time  `db:"due_date"`
			Status      string     `db:"status"`
			Expected    *time.Time `db:"expected_delivery_date"`
		}
		if err := tx.GetContext(ctx, &row, `SELECT po.id,po.po_no,po.quantity::double precision,po.received_qty::double precision,po.order_date,po.due_date,po.status,v.expected_delivery_date FROM purchase_orders po LEFT JOIN v_purchase_order_planning_schedule v ON v.id=po.id WHERE po.id=$1`, poID); err != nil {
			return nil, "", "", err
		}
		remaining := row.Quantity - row.ReceivedQty
		planning := row.DueDate
		if row.Expected != nil {
			planning = *row.Expected
		}
		target := TruncateDay(planning).AddDate(0, 0, -p.ExpediteDays)
		state := map[string]any{"purchaseOrderId": row.ID.String(), "poNo": row.PONo, "status": row.Status, "remainingQty": remaining, "planningDate": TruncateDay(planning).Format("2006-01-02"), "targetDate": target.Format("2006-01-02"), "expediteDays": p.ExpediteDays}
		if p.ExpediteDays < 1 || p.ExpediteDays > 365 {
			return executionPreflightBlocked("expediteDays must be between 1 and 365", state)
		}
		if row.Status == "RECEIVED" || row.Status == "CLOSED" || remaining <= 1e-9 {
			return executionPreflightBlocked("purchase order is already received/closed or has no remaining quantity", state)
		}
		if target.Before(TruncateDay(row.OrderDate)) {
			return executionPreflightBlocked("expedited date would be earlier than purchase order orderDate", state)
		}
		return executionPreflightReady(state)

	case "RESCHEDULE_WO", "RELEASE_WO":
		if a.TargetType != "WORK_ORDER" {
			return executionPreflightBlocked(a.ActionType+" requires WORK_ORDER target", map[string]any{"targetType": a.TargetType})
		}
		woID, err := resolveWO(ctx, tx, a.TargetRef)
		if err != nil {
			return executionPreflightBlocked(err.Error(), map[string]any{"targetRef": a.TargetRef})
		}
		var wo domain.WorkOrder
		if err := tx.GetContext(ctx, &wo, `SELECT * FROM work_orders WHERE id=$1`, woID); err != nil {
			return nil, "", "", err
		}
		state := map[string]any{"workOrderId": wo.ID.String(), "orderNo": wo.OrderNo, "status": wo.Status, "startDate": TruncateDay(wo.StartDate).Format("2006-01-02"), "dueDate": TruncateDay(wo.DueDate).Format("2006-01-02"), "quantity": wo.Quantity, "completedQty": wo.CompletedQty}
		if a.ActionType == "RELEASE_WO" {
			if wo.Status != "PLANNED" {
				return executionPreflightBlocked("RELEASE_WO requires PLANNED work order", state)
			}
			var routingIDs []uuid.UUID
			if err := tx.SelectContext(ctx, &routingIDs, `SELECT id FROM routings WHERE item_id=$1 AND is_active=true ORDER BY id`, wo.ItemID); err != nil {
				return nil, "", "", err
			}
			state["activeRoutingCount"] = len(routingIDs)
			if len(routingIDs) != 1 {
				return executionPreflightBlocked("RELEASE_WO requires exactly one active routing", state)
			}
			state["activeRoutingId"] = routingIDs[0].String()
			state["workOrderItemId"] = wo.ItemID.String()

			// Freeze the routing definition that ReleaseWorkOrderTx will copy. A routing
			// swap or operation/alternative edit after approval must change the hash.
			var routingOps []struct {
				ID               uuid.UUID `db:"id"`
				SeqNo            int       `db:"seq_no"`
				WorkCenterID     uuid.UUID `db:"work_center_id"`
				Description      string    `db:"description"`
				SetupMinutes     float64   `db:"setup_minutes"`
				RunMinutes       float64   `db:"run_minutes_per_unit"`
				SetupFamily      string    `db:"setup_family"`
				OverlapEnabled   bool      `db:"overlap_enabled"`
				TransferBatchQty float64   `db:"transfer_batch_qty"`
				MachinesRequired int       `db:"machines_required"`
				WorkersRequired  int       `db:"workers_required"`
			}
			if err := tx.SelectContext(ctx, &routingOps, `SELECT id,seq_no,work_center_id,description,setup_minutes::double precision,run_minutes_per_unit::double precision,setup_family,overlap_enabled,transfer_batch_qty::double precision,machines_required,workers_required FROM routing_operations WHERE routing_id=$1 ORDER BY seq_no,id`, routingIDs[0]); err != nil {
				return nil, "", "", err
			}
			routingState := make([]map[string]any, 0, len(routingOps))
			for _, ro := range routingOps {
				var alts []struct {
					WorkCenterID uuid.UUID `db:"work_center_id"`
					Priority     int       `db:"priority"`
					RunMult      float64   `db:"run_time_multiplier"`
					SetupMult    float64   `db:"setup_time_multiplier"`
				}
				if err := tx.SelectContext(ctx, &alts, `SELECT work_center_id,priority,run_time_multiplier::double precision,setup_time_multiplier::double precision FROM routing_operation_alternatives WHERE routing_operation_id=$1 AND is_active=true ORDER BY priority,work_center_id`, ro.ID); err != nil {
					return nil, "", "", err
				}
				altState := make([]map[string]any, 0, len(alts))
				for _, alt := range alts {
					altState = append(altState, map[string]any{"workCenterId": alt.WorkCenterID.String(), "priority": alt.Priority, "runTimeMultiplier": alt.RunMult, "setupTimeMultiplier": alt.SetupMult})
				}
				routingState = append(routingState, map[string]any{"id": ro.ID.String(), "sequenceNo": ro.SeqNo, "workCenterId": ro.WorkCenterID.String(), "description": ro.Description, "setupMinutes": ro.SetupMinutes, "runMinutesPerUnit": ro.RunMinutes, "setupFamily": ro.SetupFamily, "overlapEnabled": ro.OverlapEnabled, "transferBatchQty": ro.TransferBatchQty, "machinesRequired": ro.MachinesRequired, "workersRequired": ro.WorkersRequired, "alternatives": altState})
			}
			state["routing"] = routingState

			// Include the direct BOM definition and current available balance in the
			// preflight hash. ReleaseWorkOrderTx repeats the authoritative lock/reserve
			// checks inside the action transaction.
			var bom []struct {
				SourceID     uuid.UUID `db:"source_id"`
				ItemID       uuid.UUID `db:"item_id"`
				Code         string    `db:"code"`
				QuantityPer  float64   `db:"quantity_per"`
				ScrapPct     float64   `db:"scrap_pct"`
				Required     float64   `db:"required"`
				Available    float64   `db:"available"`
				StandardCost float64   `db:"standard_cost"`
			}
			if err := tx.SelectContext(ctx, &bom, `SELECT b.id AS source_id,b.child_id AS item_id,i.code,b.quantity::double precision AS quantity_per,b.scrap_pct::double precision AS scrap_pct,(($1*b.quantity*(1+b.scrap_pct)))::double precision AS required,COALESCE(v.on_hand-v.reserved,0)::double precision AS available,i.standard_cost::double precision AS standard_cost FROM bom_components b JOIN items i ON i.id=b.child_id LEFT JOIN v_stock_balance v ON v.item_id=b.child_id WHERE b.parent_id=$2 ORDER BY b.child_id,b.id`, wo.Quantity, wo.ItemID); err != nil {
				return nil, "", "", err
			}
			bomState := make([]map[string]any, 0, len(bom))
			for _, x := range bom {
				bomState = append(bomState, map[string]any{"sourceBomComponentId": x.SourceID.String(), "itemId": x.ItemID.String(), "code": x.Code, "quantityPer": x.QuantityPer, "scrapPct": x.ScrapPct, "required": x.Required, "available": x.Available, "standardCost": x.StandardCost})
				if x.Available+1e-9 < x.Required {
					state["bom"] = bomState
					return executionPreflightBlocked("insufficient component availability for RELEASE_WO", state)
				}
			}
			state["bom"] = bomState
			return executionPreflightReady(state)
		}
		state["recoveryDays"] = p.RecoveryDays
		if p.RecoveryDays < 1 || p.RecoveryDays > 365 {
			return executionPreflightBlocked("recoveryDays must be between 1 and 365", state)
		}
		if wo.Status != "PLANNED" && wo.Status != "RELEASED" {
			return executionPreflightBlocked("RESCHEDULE_WO requires PLANNED or RELEASED work order", state)
		}
		if wo.Status == "RELEASED" {
			var active int
			if err := tx.GetContext(ctx, &active, `SELECT count(*) FROM wo_operations WHERE wo_id=$1 AND (status IN ('IN_PROGRESS','COMPLETED') OR started_at IS NOT NULL OR completed_qty>0)`, wo.ID); err != nil {
				return nil, "", "", err
			}
			state["startedOperationCount"] = active
			if active > 0 {
				return executionPreflightBlocked("released work order already has started/completed operation", state)
			}
		}
		newStart := TruncateDay(wo.StartDate).AddDate(0, 0, -p.RecoveryDays)
		newDue := TruncateDay(wo.DueDate).AddDate(0, 0, -p.RecoveryDays)
		state["newStartDate"] = newStart.Format("2006-01-02")
		state["newDueDate"] = newDue.Format("2006-01-02")
		return executionPreflightReady(state)

	case "ALTERNATE_WORK_CENTER":
		if strings.TrimSpace(p.WorkOrderOperationRef) == "" {
			return executionPreflightBlocked("ALTERNATE_WORK_CENTER requires workOrderOperationRef for controlled execution", map[string]any{"targetRef": a.TargetRef})
		}
		opID, err := resolveWOOp(ctx, tx, p.WorkOrderOperationRef)
		if err != nil {
			return executionPreflightBlocked(err.Error(), map[string]any{"workOrderOperationRef": p.WorkOrderOperationRef})
		}
		var op domain.WOOperation
		if err := tx.GetContext(ctx, &op, `SELECT * FROM wo_operations WHERE id=$1`, opID); err != nil {
			return nil, "", "", err
		}
		altID, err := resolveWC(ctx, tx, p.AlternateWorkCenter)
		if err != nil {
			return executionPreflightBlocked(err.Error(), map[string]any{"alternateWorkCenterRef": p.AlternateWorkCenter})
		}
		var currentCode, altCode string
		if err := tx.GetContext(ctx, &currentCode, `SELECT code FROM work_centers WHERE id=$1`, op.WorkCenterID); err != nil {
			return nil, "", "", err
		}
		if err := tx.GetContext(ctx, &altCode, `SELECT code FROM work_centers WHERE id=$1`, altID); err != nil {
			return nil, "", "", err
		}
		state := map[string]any{"workOrderOperationId": op.ID.String(), "woId": op.WOID.String(), "sequenceNo": op.SeqNo, "status": op.Status, "currentWorkCenterId": op.WorkCenterID.String(), "currentWorkCenterCode": currentCode, "alternateWorkCenterId": altID.String(), "alternateWorkCenterCode": altCode}
		if op.Status != "PENDING" && op.Status != "READY" {
			return executionPreflightBlocked("alternate work center requires PENDING/READY operation", state)
		}
		if a.TargetType == "WORK_CENTER" {
			targetWC, err := resolveWC(ctx, tx, a.TargetRef)
			if err != nil || targetWC != op.WorkCenterID {
				return executionPreflightBlocked("targetRef does not match current operation work center", state)
			}
		} else if a.TargetType == "WORK_ORDER_OPERATION" {
			targetOp, err := resolveWOOp(ctx, tx, a.TargetRef)
			if err != nil || targetOp != op.ID {
				return executionPreflightBlocked("targetRef does not match workOrderOperationRef", state)
			}
		} else {
			return executionPreflightBlocked("ALTERNATE_WORK_CENTER target must be WORK_CENTER or WORK_ORDER_OPERATION", state)
		}
		var allowed int
		if err := tx.GetContext(ctx, &allowed, `SELECT count(*) FROM wo_operation_alternatives WHERE wo_operation_id=$1 AND work_center_id=$2`, op.ID, altID); err != nil {
			return nil, "", "", err
		}
		state["snapshottedAlternative"] = allowed == 1
		if allowed != 1 {
			return executionPreflightBlocked("alternate work center is not in release-time operation alternatives", state)
		}
		return executionPreflightReady(state)

	case "ADD_OVERTIME_CAPACITY":
		if a.TargetType != "WORK_CENTER" {
			return executionPreflightBlocked("ADD_OVERTIME_CAPACITY requires WORK_CENTER target", map[string]any{"targetType": a.TargetType})
		}
		wcID, err := resolveWC(ctx, tx, a.TargetRef)
		if err != nil {
			return executionPreflightBlocked(err.Error(), map[string]any{"targetRef": a.TargetRef})
		}
		date, err := time.Parse("2006-01-02", strings.TrimSpace(p.EffectiveDate))
		if err != nil {
			return executionPreflightBlocked("ADD_OVERTIME_CAPACITY requires effectiveDate YYYY-MM-DD", map[string]any{"effectiveDate": p.EffectiveDate})
		}
		var wc domain.WorkCenter
		if err := tx.GetContext(ctx, &wc, `SELECT * FROM work_centers WHERE id=$1`, wcID); err != nil {
			return nil, "", "", err
		}
		var existing int
		if err := tx.GetContext(ctx, &existing, `SELECT COALESCE(sum(overtime_minutes),0)::int FROM recovery_capacity_overrides WHERE work_center_id=$1 AND effective_date=$2`, wcID, date); err != nil {
			return nil, "", "", err
		}
		base := int(wc.CapacityMinutesPerDay)
		if base <= 0 {
			base = 480
		}
		maxClock := 1440 - wc.ShiftStartMinute
		if wc.ShiftStartMinute < 0 || wc.ShiftStartMinute > 1439 {
			maxClock = 960
		}
		state := map[string]any{"workCenterId": wc.ID.String(), "workCenterCode": wc.Code, "effectiveDate": date.Format("2006-01-02"), "baseMinutes": base, "existingRecoveryOvertimeMinutes": existing, "overtimeMinutes": p.OvertimeMinutes, "maxClockMinutes": maxClock}
		if p.OvertimeMinutes < 1 || p.OvertimeMinutes > 10080 {
			return executionPreflightBlocked("overtimeMinutes must be between 1 and 10080", state)
		}
		if base+existing+p.OvertimeMinutes > maxClock {
			return executionPreflightBlocked("overtime would extend work-center clock window beyond the same calendar day", state)
		}
		return executionPreflightReady(state)
	default:
		return executionPreflightBlocked("unsupported recovery action type", map[string]any{"actionType": a.ActionType})
	}
}

// simulatedActionState is the preflight evidence of one action evaluated
// after the effects of the preceding actions of the same execution.
type simulatedActionState struct {
	State  json.RawMessage
	Hash   string
	Reason string
}

// simulateActionStatesTx evaluates every action in sequence order as executeOne
// will see it: each ready action's operational effect is applied before the
// next action is evaluated, and everything is rolled back at the end. Without
// this, an action whose preflight state depends on what an earlier action of
// the same execution changes (component availability after a release, the
// recovery overtime already added to a work-center/day) would be rejected at
// execution time as drift caused by the execution itself.
func (s *RecoveryExecutionService) simulateActionStatesTx(ctx context.Context, tx *sqlx.Tx, e domain.RecoveryPlanExecution, actions []domain.RecoveryPlanExecutionAction, actor RecoveryPlanningActor) ([]simulatedActionState, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT recovery_preflight_simulation`); err != nil {
		return nil, err
	}
	out := make([]simulatedActionState, 0, len(actions))
	for _, a := range actions {
		state, h, reason, err := s.preflightActionTx(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			if _, err := tx.ExecContext(ctx, `SAVEPOINT recovery_preflight_action`); err != nil {
				return nil, err
			}
			if _, _, err := s.applyRecoveryActionTx(ctx, tx, e, a, state, actor); err != nil {
				if IsRetryableTxError(err) {
					return nil, err
				}
				msg, ok := recoveryActionBusinessError(err)
				if !ok {
					return nil, err
				}
				if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT recovery_preflight_action`); rbErr != nil {
					return nil, rbErr
				}
				st := parsePreflightState(state)
				state, h, reason, err = executionPreflightBlocked("action cannot be applied after the preceding actions of this execution: "+msg, st)
				if err != nil {
					return nil, err
				}
			} else if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT recovery_preflight_action`); err != nil {
				return nil, err
			}
		}
		out = append(out, simulatedActionState{State: state, Hash: h, Reason: reason})
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT recovery_preflight_simulation`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT recovery_preflight_simulation`); err != nil {
		return nil, err
	}
	return out, nil
}

// recoveryActionBusinessError reports whether err is an expected business
// rejection (application error or a RAISEd / integrity PostgreSQL error)
// rather than an infrastructure failure, and returns its message.
func recoveryActionBusinessError(err error) (string, bool) {
	var app *domain.AppError
	if errors.As(err, &app) {
		return string(app.Code) + ": " + app.Message, true
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "P0") || strings.HasPrefix(pg.Code, "23")) {
		return pg.Message, true
	}
	return "", false
}

// Preflight retries the whole operation on serialization failures / deadlocks.
func (s *RecoveryExecutionService) Preflight(ctx context.Context, id uuid.UUID, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	return retrySerializable(ctx, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return s.preflightOnce(ctx, id, actor)
	})
}

func (s *RecoveryExecutionService) preflightOnce(ctx context.Context, id uuid.UUID, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	if err := validateRecoveryExecutionPrepareActor(actor); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var e domain.RecoveryPlanExecution
	if err := tx.GetContext(ctx, &e, `SELECT * FROM recovery_plan_executions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("Recovery Execution")
		}
		return nil, err
	}
	if e.Status != "PREPARED" && e.Status != "PREFLIGHT_BLOCKED" && e.Status != "PREFLIGHT_PASSED" {
		return nil, domain.NewConflict("preflight is allowed only before approval")
	}
	var pubHash string
	var runHash *string
	if err := tx.GetContext(ctx, &pubHash, `SELECT publication_hash FROM recovery_scenario_publications WHERE id=$1 AND scenario_id=$2 AND run_id=$3`, e.PublicationID, e.ScenarioID, e.RunID); err != nil {
		return nil, err
	}
	if err := tx.GetContext(ctx, &runHash, `SELECT result_hash FROM recovery_scenario_runs WHERE id=$1 AND status='SUCCEEDED'`, e.RunID); err != nil {
		return nil, err
	}
	if runHash == nil || pubHash != e.PublicationHash || *runHash != e.RunResultHash {
		return nil, domain.NewConflict("published recovery evidence hash drift detected")
	}
	var actions []domain.RecoveryPlanExecutionAction
	if err := tx.SelectContext(ctx, &actions, `SELECT * FROM recovery_plan_execution_actions WHERE execution_id=$1 ORDER BY sequence_no,id`, id); err != nil {
		return nil, err
	}
	blocked := false
	type hashRow struct {
		Sequence int    `json:"sequence"`
		Hash     string `json:"hash"`
		Status   string `json:"status"`
	}
	hashes := make([]hashRow, 0, len(actions))
	sims, err := s.simulateActionStatesTx(ctx, tx, e, actions, actor)
	if err != nil {
		return nil, err
	}
	for i, a := range actions {
		state, h, reason := sims[i].State, sims[i].Hash, sims[i].Reason
		status := "READY"
		if reason != "" {
			status = "BLOCKED"
			blocked = true
		}
		if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_execution_actions SET preflight_status=$2,preflight_state=$3,preflight_state_hash=$4,error_text=$5 WHERE id=$1`, a.ID, status, state, h, reason); err != nil {
			return nil, err
		}
		hashes = append(hashes, hashRow{a.SequenceNo, h, status})
	}
	preflightHash, err := stableRecoveryExecutionHash(hashes)
	if err != nil {
		return nil, err
	}
	newStatus := "PREFLIGHT_PASSED"
	if blocked {
		newStatus = "PREFLIGHT_BLOCKED"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_executions SET status=$2,preflight_hash=$3,preflighted_at=now(),failure_reason=$4 WHERE id=$1`, id, newStatus, preflightHash, func() string {
		if blocked {
			return "one or more actions are blocked"
		}
		return ""
	}()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Approve retries the whole operation on serialization failures / deadlocks.
func (s *RecoveryExecutionService) Approve(ctx context.Context, id uuid.UUID, in RecoveryExecutionApprovalInput, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	return retrySerializable(ctx, func() (*domain.RecoveryPlanExecutionDetail, error) {
		return s.approveOnce(ctx, id, in, actor)
	})
}

func (s *RecoveryExecutionService) approveOnce(ctx context.Context, id uuid.UUID, in RecoveryExecutionApprovalInput, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	if err := validateRecoveryExecutionAdmin(actor); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var e domain.RecoveryPlanExecution
	if err := tx.GetContext(ctx, &e, `SELECT * FROM recovery_plan_executions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("Recovery Execution")
		}
		return nil, err
	}
	if e.Status != "PREFLIGHT_PASSED" || e.PreflightHash == nil {
		return nil, domain.NewConflict("recovery execution must pass preflight before approval")
	}
	if e.PreparedByUserID == actor.UserID {
		return nil, domain.NewForbidden("approver must differ from recovery execution preparer")
	}
	approvalHash, err := stableRecoveryExecutionHash(struct{ ExecutionID, PreflightHash, Approver, Comment string }{id.String(), *e.PreflightHash, actor.UserID.String(), strings.TrimSpace(in.Comment)})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_executions SET status='APPROVED',approval_hash=$2,approval_comment=$3,approved_by_user_id=$4,approved_by_username=$5,approved_at=now(),failure_reason='' WHERE id=$1`, id, approvalHash, strings.TrimSpace(in.Comment), actor.UserID, strings.TrimSpace(actor.Username)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *RecoveryExecutionService) Cancel(ctx context.Context, id uuid.UUID, in RecoveryExecutionCancelInput, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	if err := validateRecoveryExecutionAdmin(actor); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE recovery_plan_executions SET status='CANCELLED',failure_reason=$2,completed_at=now() WHERE id=$1 AND status IN ('PREPARED','PREFLIGHT_PASSED','PREFLIGHT_BLOCKED','APPROVED')`, id, strings.TrimSpace(in.Comment))
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, domain.NewConflict("recovery execution cannot be cancelled in its current state")
	}
	return s.Get(ctx, id)
}

func parsePreflightState(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}
func mapString(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func mapFloat(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}
func mapInt(m map[string]any, k string) int { return int(mapFloat(m, k)) }

func insertRecoveryRescheduleSignal(ctx context.Context, tx *sqlx.Tx, trigger, sourceRef string, wcID, woID *uuid.UUID, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,work_center_id,work_order_id,detail) VALUES($1,$2,'RECOVERY_EXECUTION',$3,$4,$5,$6)`, uuid.New(), trigger, sourceRef, wcID, woID, b)
	return err
}

func (s *RecoveryExecutionService) executeOne(ctx context.Context, conn txConn, e domain.RecoveryPlanExecution, a domain.RecoveryPlanExecutionAction, actor RecoveryPlanningActor) error {
	tx, err := conn.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var locked domain.RecoveryPlanExecutionAction
	if err := tx.GetContext(ctx, &locked, `SELECT * FROM recovery_plan_execution_actions WHERE id=$1 AND execution_id=$2 FOR UPDATE`, a.ID, e.ID); err != nil {
		return err
	}
	if locked.ExecutionStatus == "SUCCEEDED" {
		return tx.Commit()
	}
	if locked.ExecutionStatus != "PENDING" {
		return domain.NewConflict("recovery execution action is not pending")
	}
	state, h, reason, err := s.preflightActionTx(ctx, tx, locked)
	if err != nil {
		return err
	}
	if reason != "" || locked.PreflightStateHash == nil || h != *locked.PreflightStateHash {
		return domain.NewConflict("operational state drift detected before action execution")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_execution_actions SET execution_status='RUNNING',started_at=now(),before_state=$2,error_text='' WHERE id=$1`, locked.ID, state); err != nil {
		return err
	}
	after, opRef, err := s.applyRecoveryActionTx(ctx, tx, e, locked, state, actor)
	if err != nil {
		return err
	}
	resultHash, err := stableRecoveryExecutionHash(struct {
		ActionID       string `json:"actionId"`
		Before         any    `json:"before"`
		After          any    `json:"after"`
		OperationalRef string `json:"operationalRef"`
	}{locked.ID.String(), normalizedJSON(state), after, opRef})
	if err != nil {
		return err
	}
	afterJSON, _ := json.Marshal(after)
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_execution_actions SET execution_status='SUCCEEDED',after_state=$2,operational_ref=$3,result_hash=$4,completed_at=now(),error_text='' WHERE id=$1`, locked.ID, afterJSON, opRef, resultHash); err != nil {
		return err
	}
	return tx.Commit()
}

// applyRecoveryActionTx performs the operational effect of one action inside
// tx, using the before-state captured by preflightActionTx. It is used by
// executeOne and, inside a rolled-back savepoint, by simulateActionStatesTx.
func (s *RecoveryExecutionService) applyRecoveryActionTx(ctx context.Context, tx *sqlx.Tx, e domain.RecoveryPlanExecution, locked domain.RecoveryPlanExecutionAction, state json.RawMessage, actor RecoveryPlanningActor) (map[string]any, string, error) {
	p, err := decodeRecoveryExecutionParams(locked.Parameters)
	if err != nil {
		return nil, "", err
	}
	before := parsePreflightState(state)
	after := map[string]any{}
	opRef := ""
	switch locked.ActionType {
	case "EXPEDITE_PO":
		poID, _ := uuid.Parse(mapString(before, "purchaseOrderId"))
		remaining := mapFloat(before, "remainingQty")
		target := mapString(before, "targetDate")
		event, err := s.supplierScheduling.recordEventTx(ctx, tx, poID, SupplierScheduleEventInput{EventID: locked.ID.String(), EventType: "REVISE", Quantity: &remaining, ConfirmedDeliveryDate: target, SupplierReference: "RECOVERY:" + e.ID.String(), Notes: "Controlled Recovery Plan Execution action " + locked.ID.String()}, SalesOrderActor{UserID: actor.UserID, Username: actor.Username, Role: domain.RoleAdmin})
		if err != nil {
			return nil, "", err
		}
		after = map[string]any{"supplierScheduleEventId": event.ID.String(), "revisionNo": event.RevisionNo, "confirmedDeliveryDate": target, "quantity": remaining}
		opRef = "SUPPLIER_SCHEDULE:" + event.ID.String()
		if err := insertRecoveryRescheduleSignal(ctx, tx, "MATERIAL_SHORTAGE", opRef, nil, nil, map[string]any{"executionId": e.ID.String(), "actionId": locked.ID.String(), "purchaseOrderId": poID.String()}); err != nil {
			return nil, "", err
		}
	case "RESCHEDULE_WO":
		woID, _ := uuid.Parse(mapString(before, "workOrderId"))
		newStart := mapString(before, "newStartDate")
		newDue := mapString(before, "newDueDate")
		res, err := tx.ExecContext(ctx, `UPDATE work_orders SET start_date=$2,due_date=$3 WHERE id=$1 AND status IN ('PLANNED','RELEASED')`, woID, newStart, newDue)
		if err != nil {
			return nil, "", err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, "", domain.NewConflict("work order changed before reschedule")
		}
		after = map[string]any{"workOrderId": woID.String(), "startDate": newStart, "dueDate": newDue}
		opRef = "WO:" + mapString(before, "orderNo")
		if err := insertRecoveryRescheduleSignal(ctx, tx, "PRIORITY_CHANGE", opRef, nil, &woID, map[string]any{"executionId": e.ID.String(), "actionId": locked.ID.String(), "recoveryDays": p.RecoveryDays}); err != nil {
			return nil, "", err
		}
	case "ALTERNATE_WORK_CENTER":
		opID, _ := uuid.Parse(mapString(before, "workOrderOperationId"))
		altID, _ := uuid.Parse(mapString(before, "alternateWorkCenterId"))
		woID, _ := uuid.Parse(mapString(before, "woId"))
		res, err := tx.ExecContext(ctx, `UPDATE wo_operations SET work_center_id=$2 WHERE id=$1 AND status IN ('PENDING','READY')`, opID, altID)
		if err != nil {
			return nil, "", err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, "", domain.NewConflict("WO operation changed before alternate work center execution")
		}
		after = map[string]any{"workOrderOperationId": opID.String(), "workCenterId": altID.String(), "workCenterCode": mapString(before, "alternateWorkCenterCode")}
		opRef = "WOOP:" + opID.String()
		if err := insertRecoveryRescheduleSignal(ctx, tx, "PRIORITY_CHANGE", opRef, &altID, &woID, map[string]any{"executionId": e.ID.String(), "actionId": locked.ID.String()}); err != nil {
			return nil, "", err
		}
	case "ADD_OVERTIME_CAPACITY":
		wcID, _ := uuid.Parse(mapString(before, "workCenterId"))
		date := mapString(before, "effectiveDate")
		overrideID := uuid.New()
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_capacity_overrides(id,execution_action_id,execution_id,work_center_id,effective_date,overtime_minutes,created_by_user_id,created_by_username) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, overrideID, locked.ID, e.ID, wcID, date, p.OvertimeMinutes, actor.UserID, actor.Username); err != nil {
			return nil, "", err
		}
		after = map[string]any{"capacityOverrideId": overrideID.String(), "workCenterId": wcID.String(), "effectiveDate": date, "overtimeMinutes": p.OvertimeMinutes}
		opRef = "RECOVERY_CAPACITY_OVERRIDE:" + overrideID.String()
		if err := insertRecoveryRescheduleSignal(ctx, tx, "CAPACITY_FEEDBACK_CHANGE", opRef, &wcID, nil, map[string]any{"executionId": e.ID.String(), "actionId": locked.ID.String(), "effectiveDate": date, "overtimeMinutes": p.OvertimeMinutes}); err != nil {
			return nil, "", err
		}
	case "RELEASE_WO":
		woID, _ := uuid.Parse(mapString(before, "workOrderId"))
		rr, err := s.workflow.releaseWorkOrderTx(ctx, tx, woID)
		if err != nil {
			return nil, "", err
		}
		after = map[string]any{"workOrderId": rr.WorkOrderID.String(), "orderNo": rr.OrderNo, "bomSnapshotId": rr.BOMSnapshotID.String(), "reservationCount": len(rr.Reservations)}
		opRef = "WO:" + rr.OrderNo
		if err := insertRecoveryRescheduleSignal(ctx, tx, "PRIORITY_CHANGE", opRef, nil, &woID, map[string]any{"executionId": e.ID.String(), "actionId": locked.ID.String(), "released": true}); err != nil {
			return nil, "", err
		}
	default:
		return nil, "", domain.NewBadRequest("unsupported recovery execution action", nil)
	}
	return after, opRef, nil
}

func recoveryExecutionDetachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

func (s *RecoveryExecutionService) persistentExecutionActionStatus(ctx context.Context, executionID, actionID uuid.UUID) (string, error) {
	dctx, cancel := recoveryExecutionDetachedContext(ctx)
	defer cancel()
	var status string
	if err := s.db.GetContext(dctx, &status, `SELECT execution_status FROM recovery_plan_execution_actions WHERE id=$1 AND execution_id=$2`, actionID, executionID); err != nil {
		return "", err
	}
	return status, nil
}

func recoveryExecutionFailureReason(err error) string {
	var app *domain.AppError
	if errors.As(err, &app) {
		reason := string(app.Code) + ": " + app.Message
		if len(reason) > 2000 {
			return reason[:2000]
		}
		return reason
	}
	return "INTERNAL: controlled recovery action failed"
}

func recoveryExecutionResultHashTx(ctx context.Context, tx *sqlx.Tx, e domain.RecoveryPlanExecution, terminalStatus string) (string, error) {
	type actionEvidence struct {
		SequenceNo      int     `json:"sequenceNo"`
		ActionID        string  `json:"actionId"`
		ExecutionStatus string  `json:"executionStatus"`
		OperationalRef  string  `json:"operationalRef"`
		ResultHash      *string `json:"resultHash,omitempty"`
		ErrorText       string  `json:"errorText"`
	}
	var rows []struct {
		ID              uuid.UUID `db:"id"`
		SequenceNo      int       `db:"sequence_no"`
		ExecutionStatus string    `db:"execution_status"`
		OperationalRef  string    `db:"operational_ref"`
		ResultHash      *string   `db:"result_hash"`
		ErrorText       string    `db:"error_text"`
	}
	if err := tx.SelectContext(ctx, &rows, `SELECT id,sequence_no,execution_status,operational_ref,result_hash,error_text FROM recovery_plan_execution_actions WHERE execution_id=$1 ORDER BY sequence_no,id`, e.ID); err != nil {
		return "", err
	}
	actions := make([]actionEvidence, 0, len(rows))
	for _, r := range rows {
		actions = append(actions, actionEvidence{
			SequenceNo: r.SequenceNo, ActionID: r.ID.String(), ExecutionStatus: r.ExecutionStatus,
			OperationalRef: r.OperationalRef, ResultHash: r.ResultHash, ErrorText: r.ErrorText,
		})
	}
	approvalHash := ""
	if e.ApprovalHash != nil {
		approvalHash = *e.ApprovalHash
	}
	preflightHash := ""
	if e.PreflightHash != nil {
		preflightHash = *e.PreflightHash
	}
	return stableRecoveryExecutionHash(struct {
		ExecutionID     string           `json:"executionId"`
		ScenarioID      string           `json:"scenarioId"`
		PublicationHash string           `json:"publicationHash"`
		RunResultHash   string           `json:"runResultHash"`
		ActionSetHash   string           `json:"actionSetHash"`
		PreflightHash   string           `json:"preflightHash"`
		ApprovalHash    string           `json:"approvalHash"`
		Status          string           `json:"status"`
		Actions         []actionEvidence `json:"actions"`
	}{
		ExecutionID: e.ID.String(), ScenarioID: e.ScenarioID.String(), PublicationHash: e.PublicationHash,
		RunResultHash: e.RunResultHash, ActionSetHash: e.ActionSetHash, PreflightHash: preflightHash,
		ApprovalHash: approvalHash, Status: terminalStatus, Actions: actions,
	})
}

func (s *RecoveryExecutionService) markExecutionFailure(ctx context.Context, conn txConn, e domain.RecoveryPlanExecution, a domain.RecoveryPlanExecutionAction, cause error) error {
	dctx, cancel := recoveryExecutionDetachedContext(ctx)
	defer cancel()
	tx, err := conn.BeginTxx(dctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	reason := recoveryExecutionFailureReason(cause)
	if _, err := tx.ExecContext(dctx, `UPDATE recovery_plan_execution_actions SET execution_status='FAILED',error_text=$2,completed_at=now() WHERE id=$1 AND execution_status='PENDING'`, a.ID, reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(dctx, `UPDATE recovery_plan_execution_actions SET execution_status='SKIPPED',error_text='skipped after earlier action failure',completed_at=now() WHERE execution_id=$1 AND sequence_no>$2 AND execution_status='PENDING'`, e.ID, a.SequenceNo); err != nil {
		return err
	}
	var succeeded, failed int
	if err := tx.GetContext(dctx, &succeeded, `SELECT count(*) FROM recovery_plan_execution_actions WHERE execution_id=$1 AND execution_status='SUCCEEDED'`, e.ID); err != nil {
		return err
	}
	if err := tx.GetContext(dctx, &failed, `SELECT count(*) FROM recovery_plan_execution_actions WHERE execution_id=$1 AND execution_status='FAILED'`, e.ID); err != nil {
		return err
	}
	status := "FAILED"
	if succeeded > 0 {
		status = "PARTIAL_FAILED"
	}
	resultHash, err := recoveryExecutionResultHashTx(dctx, tx, e, status)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(dctx, `UPDATE recovery_plan_executions SET status=$2,failure_reason=$3,succeeded_action_count=$4,failed_action_count=$5,execution_result_hash=$6,completed_at=now() WHERE id=$1 AND status='EXECUTING'`, e.ID, status, reason, succeeded, failed, resultHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.NewConflict("recovery execution failure could not be terminalized")
	}
	return tx.Commit()
}

func (s *RecoveryExecutionService) finalizeExecutionSuccess(ctx context.Context, conn txConn, id uuid.UUID) error {
	dctx, cancel := recoveryExecutionDetachedContext(ctx)
	defer cancel()
	tx, err := conn.BeginTxx(dctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var e domain.RecoveryPlanExecution
	if err := tx.GetContext(dctx, &e, `SELECT * FROM recovery_plan_executions WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	if e.Status == "SUCCEEDED" && e.ExecutionResultHash != nil {
		return tx.Commit()
	}
	if e.Status != "EXECUTING" {
		return domain.NewConflict("recovery execution is not EXECUTING")
	}
	var notSucceeded int
	if err := tx.GetContext(dctx, &notSucceeded, `SELECT count(*) FROM recovery_plan_execution_actions WHERE execution_id=$1 AND execution_status<>'SUCCEEDED'`, id); err != nil {
		return err
	}
	if notSucceeded != 0 {
		return domain.NewConflict("recovery execution cannot be finalized because one or more actions are not SUCCEEDED")
	}
	resultHash, err := recoveryExecutionResultHashTx(dctx, tx, e, "SUCCEEDED")
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(dctx, `UPDATE recovery_plan_executions SET status='SUCCEEDED',succeeded_action_count=action_count,failed_action_count=0,execution_result_hash=$2,completed_at=now(),failure_reason='' WHERE id=$1 AND status='EXECUTING'`, id, resultHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.NewConflict("recovery execution success could not be terminalized")
	}
	return tx.Commit()
}

// Execute runs one execution at a time: concurrent callers for the same
// execution (double clicks, client retries, several operators) queue on a
// session-level advisory lock taken before any transaction starts. Without it
// the start gate of one caller (execution row FOR UPDATE, then the action
// rows) and the action transaction of another (action row, then a key-share
// lock on the execution row through the foreign key of the rows it inserts)
// lock in opposite orders and deadlock. A queued caller starts with a fresh
// snapshot and simply observes the finished execution. The whole operation is
// still retried on serialization failures / deadlocks caused by other writers
// of the same operational rows.
//
// The detail is read after the lock is released so that the lock holder never
// needs a second pool connection.
func (s *RecoveryExecutionService) Execute(ctx context.Context, id uuid.UUID, actor RecoveryPlanningActor) (*domain.RecoveryPlanExecutionDetail, error) {
	if err := validateRecoveryExecutionAdmin(actor); err != nil {
		return nil, err
	}
	withDetail, err := withSessionAdvisoryLock(ctx, s.db, sessionLockKey("recovery-execution-run", id), func(conn *sqlx.Conn) (bool, error) {
		return retrySerializable(ctx, func() (bool, error) {
			return s.executeOnce(ctx, conn, id, actor)
		})
	})
	if !withDetail {
		return nil, err
	}
	detail, getErr := s.Get(context.WithoutCancel(ctx), id)
	if err != nil {
		return detail, err
	}
	if getErr != nil {
		return nil, getErr
	}
	return detail, nil
}

// executeOnce performs one attempt of Execute on conn. withDetail reports
// whether the caller should return the execution detail (alongside err, which
// is then a business failure of an action).
func (s *RecoveryExecutionService) executeOnce(ctx context.Context, conn txConn, id uuid.UUID, actor RecoveryPlanningActor) (withDetail bool, err error) {

	// Tx A is a durable execution-start gate. APPROVED executions revalidate every
	// approved preflight snapshot before becoming EXECUTING. An already EXECUTING
	// execution is resumable after a process/request failure; each still-PENDING
	// action revalidates its own state again in executeOne.
	tx, err := conn.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck
	var e domain.RecoveryPlanExecution
	if err := tx.GetContext(ctx, &e, `SELECT * FROM recovery_plan_executions WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, domain.NewNotFound("Recovery Execution")
		}
		return false, err
	}
	if e.Status == "SUCCEEDED" || e.Status == "FAILED" || e.Status == "PARTIAL_FAILED" || e.Status == "CANCELLED" {
		_ = tx.Commit()
		return true, nil
	}
	if e.Status != "APPROVED" && e.Status != "EXECUTING" {
		return false, domain.NewConflict("recovery execution must be APPROVED before execute")
	}

	var actions []domain.RecoveryPlanExecutionAction
	if err := tx.SelectContext(ctx, &actions, `SELECT * FROM recovery_plan_execution_actions WHERE execution_id=$1 ORDER BY sequence_no,id FOR UPDATE`, id); err != nil {
		return false, err
	}

	// If terminal action evidence exists while the parent is still EXECUTING
	// (for example the process died between action failure evidence and parent
	// finalization), reconcile the parent rather than replaying anything.
	if e.Status == "EXECUTING" {
		failed := 0
		succeeded := 0
		for _, a := range actions {
			switch a.ExecutionStatus {
			case "SUCCEEDED":
				succeeded++
			case "FAILED":
				failed++
			case "RUNNING":
				return false, domain.NewConflict("persisted RUNNING recovery action requires operator investigation")
			}
		}
		if failed > 0 {
			status := "FAILED"
			if succeeded > 0 {
				status = "PARTIAL_FAILED"
			}
			resultHash, err := recoveryExecutionResultHashTx(ctx, tx, e, status)
			if err != nil {
				return false, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_executions SET status=$2,succeeded_action_count=$3,failed_action_count=$4,execution_result_hash=$5,completed_at=COALESCE(completed_at,now()),failure_reason=CASE WHEN failure_reason='' THEN 'reconciled persisted failed action evidence' ELSE failure_reason END WHERE id=$1`, id, status, succeeded, failed, resultHash); err != nil {
				return false, err
			}
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	if e.Status == "APPROVED" {
		for _, a := range actions {
			if a.ExecutionStatus != "PENDING" {
				return false, domain.NewConflict("approved recovery execution contains non-pending action evidence")
			}
		}
		sims, err := s.simulateActionStatesTx(ctx, tx, e, actions, actor)
		if err != nil {
			return false, err
		}
		for i, a := range actions {
			h, reason := sims[i].Hash, sims[i].Reason
			if reason != "" || a.PreflightStateHash == nil || h != *a.PreflightStateHash {
				return false, domain.NewConflict("operational state drift detected; prepare a new recovery execution")
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE recovery_plan_executions SET status='EXECUTING',executed_by_user_id=$2,executed_by_username=$3,started_at=now(),failure_reason='' WHERE id=$1`, id, actor.UserID, strings.TrimSpace(actor.Username)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	e.Status = "EXECUTING"

	for _, a := range actions {
		if a.ExecutionStatus == "SUCCEEDED" {
			continue
		}
		if a.ExecutionStatus != "PENDING" {
			return false, domain.NewConflict("recovery execution contains a non-resumable action state")
		}
		if err := s.executeOne(ctx, conn, e, a, actor); err != nil {
			// COMMIT may return an ambiguous transport error even if PostgreSQL
			// committed. Re-read durable action evidence before declaring failure.
			status, statusErr := s.persistentExecutionActionStatus(ctx, e.ID, a.ID)
			if statusErr != nil {
				// Leave the parent EXECUTING. A later Execute retry is safe and can
				// determine whether this action committed or remained PENDING.
				return false, fmt.Errorf("recovery action execution error (%v); durable status could not be confirmed: %w", err, statusErr)
			}
			if status == "SUCCEEDED" {
				continue
			}
			if status == "PENDING" && IsRetryableTxError(err) {
				// A serialization failure / deadlock rolled the action back; it
				// is not a business failure. Keep the parent EXECUTING so the
				// retry (or a later Execute) resumes from this action.
				return false, err
			}
			if status != "PENDING" && status != "FAILED" {
				return false, fmt.Errorf("recovery action execution error (%v); unexpected durable status %s", err, status)
			}
			if finalizeErr := s.markExecutionFailure(ctx, conn, e, a, err); finalizeErr != nil {
				return false, fmt.Errorf("recovery action failed: %v; failure evidence finalization also failed: %w", err, finalizeErr)
			}
			return true, err
		}
	}

	if err := s.finalizeExecutionSuccess(ctx, conn, id); err != nil {
		// All action results may already be durable. Keep EXECUTING so a retry can
		// safely finalize without replaying successful actions.
		return false, err
	}
	return true, nil
}

// loadRecoveryOvertime returns executed, date-scoped overtime minutes for CRP
// and Detailed Scheduling. The values are additive to calendar/master clock time.
func loadRecoveryOvertime(ctx context.Context, db *sqlx.DB, start, end time.Time) (map[string]int, error) {
	rows := []struct {
		WorkCenterID  uuid.UUID `db:"work_center_id"`
		EffectiveDate time.Time `db:"effective_date"`
		Minutes       int       `db:"minutes"`
	}{}
	if err := db.SelectContext(ctx, &rows, `SELECT work_center_id,effective_date,COALESCE(sum(overtime_minutes),0)::int AS minutes FROM recovery_capacity_overrides WHERE effective_date BETWEEN $1 AND $2 GROUP BY work_center_id,effective_date`, TruncateDay(start), TruncateDay(end)); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[dayStateKey(r.WorkCenterID, r.EffectiveDate)] = r.Minutes
	}
	return out, nil
}

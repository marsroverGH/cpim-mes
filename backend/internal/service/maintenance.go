package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var maintenanceTypes = map[string]bool{
	"PREVENTIVE_MAINTENANCE": true,
	"BREAKDOWN":              true,
	"PLANNED_DOWNTIME":       true,
	"UNPLANNED_DOWNTIME":     true,
}

var maintenanceStatuses = map[string]bool{
	"PLANNED": true, "ACTIVE": true, "COMPLETED": true, "CANCELLED": true,
}

type MaintenanceEventInput struct {
	WorkCenterID        uuid.UUID `json:"workCenterId"`
	EventType           string    `json:"eventType"`
	Status              string    `json:"status"`
	StartAt             time.Time `json:"startAt"`
	EndAt               time.Time `json:"endAt"`
	UnavailableMachines int       `json:"unavailableMachines"`
	UnavailableWorkers  int       `json:"unavailableWorkers"`
	Reason              string    `json:"reason"`
	SourceRef           string    `json:"sourceRef"`
}

type MaintenanceRevisionInput struct {
	Status              string     `json:"status"`
	StartAt             *time.Time `json:"startAt,omitempty"`
	EndAt               *time.Time `json:"endAt,omitempty"`
	UnavailableMachines *int       `json:"unavailableMachines,omitempty"`
	UnavailableWorkers  *int       `json:"unavailableWorkers,omitempty"`
	Reason              *string    `json:"reason,omitempty"`
	SourceRef           *string    `json:"sourceRef,omitempty"`
}

type MaintenanceService struct{ db *sqlx.DB }

func validateMaintenanceActor(actor SalesOrderActor) error {
	if err := actor.validate(); err != nil {
		return err
	}
	if actor.Role != domain.RolePlanner && actor.Role != domain.RoleAdmin {
		return domain.NewForbidden("maintenance mutation requires planner/admin")
	}
	return nil
}

func normalizeMaintenanceType(v string) (string, error) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if !maintenanceTypes[v] {
		return "", domain.NewBadRequest("invalid maintenance eventType", nil)
	}
	return v, nil
}

func normalizeMaintenanceStatus(v, eventType string) (string, error) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		if eventType == "BREAKDOWN" || eventType == "UNPLANNED_DOWNTIME" {
			v = "ACTIVE"
		} else {
			v = "PLANNED"
		}
	}
	if !maintenanceStatuses[v] {
		return "", domain.NewBadRequest("invalid maintenance status", nil)
	}
	return v, nil
}

func validateMaintenanceWindow(start, end time.Time, machines, workers int, status string) error {
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return domain.NewBadRequest("startAt and endAt must define a positive window", nil)
	}
	if machines < 0 || workers < 0 {
		return domain.NewBadRequest("unavailable resource counts must be >= 0", nil)
	}
	if status == "CANCELLED" {
		if machines != 0 || workers != 0 {
			return domain.NewBadRequest("CANCELLED revision must have zero capacity reduction", nil)
		}
	} else if status != "COMPLETED" && machines == 0 && workers == 0 {
		return domain.NewBadRequest("maintenance must reduce at least one machine or worker", nil)
	}
	return nil
}

func (s *MaintenanceService) Create(ctx context.Context, in MaintenanceEventInput, actor SalesOrderActor) (*domain.MaintenanceEventDetail, error) {
	if err := validateMaintenanceActor(actor); err != nil {
		return nil, err
	}
	if in.WorkCenterID == uuid.Nil {
		return nil, domain.NewBadRequest("workCenterId is required", nil)
	}
	typ, err := normalizeMaintenanceType(in.EventType)
	if err != nil {
		return nil, err
	}
	status, err := normalizeMaintenanceStatus(in.Status, typ)
	if err != nil {
		return nil, err
	}
	if status == "COMPLETED" || status == "CANCELLED" {
		return nil, domain.NewBadRequest("new maintenance event must start PLANNED or ACTIVE", nil)
	}
	if err := validateMaintenanceWindow(in.StartAt, in.EndAt, in.UnavailableMachines, in.UnavailableWorkers, status); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var mc, wc int
	if err := tx.QueryRowxContext(ctx, `SELECT machine_count,worker_count FROM work_centers WHERE id=$1 FOR SHARE`, in.WorkCenterID).Scan(&mc, &wc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("work center")
		}
		return nil, err
	}
	if in.UnavailableMachines > maxInt(mc, 1) || in.UnavailableWorkers > maxInt(wc, 0) {
		return nil, domain.NewBadRequest("capacity reduction exceeds work center resources", nil)
	}
	eid, rid := uuid.New(), uuid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_events(id,work_center_id,event_type,created_by_user_id,created_by) VALUES($1,$2,$3,$4,$5)`, eid, in.WorkCenterID, typ, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_event_revisions(id,maintenance_event_id,revision_no,status,start_at,end_at,unavailable_machines,unavailable_workers,reason,source_ref,actor_user_id,actor_username) VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, rid, eid, status, in.StartAt, in.EndAt, in.UnavailableMachines, in.UnavailableWorkers, strings.TrimSpace(in.Reason), strings.TrimSpace(in.SourceRef), actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, eid)
}

func (s *MaintenanceService) Revise(ctx context.Context, id uuid.UUID, in MaintenanceRevisionInput, actor SalesOrderActor) (*domain.MaintenanceEventDetail, error) {
	if err := validateMaintenanceActor(actor); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, domain.NewBadRequest("maintenance event id is required", nil)
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	// Serialize revisions on the immutable event row.
	var eventType string
	var wcID uuid.UUID
	if err := tx.QueryRowxContext(ctx, `SELECT event_type,work_center_id FROM maintenance_events WHERE id=$1 FOR UPDATE`, id).Scan(&eventType, &wcID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("maintenance event")
		}
		return nil, err
	}
	var prev domain.MaintenanceEventRevision
	if err := tx.GetContext(ctx, &prev, `SELECT * FROM maintenance_event_revisions WHERE maintenance_event_id=$1 ORDER BY revision_no DESC LIMIT 1`, id); err != nil {
		return nil, err
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status == "" {
		status = prev.Status
	}
	if !maintenanceStatuses[status] {
		return nil, domain.NewBadRequest("invalid maintenance status", nil)
	}
	start, end := prev.StartAt, prev.EndAt
	machines, workers := prev.UnavailableMachines, prev.UnavailableWorkers
	reason, source := prev.Reason, prev.SourceRef
	if in.StartAt != nil {
		start = *in.StartAt
	}
	if in.EndAt != nil {
		end = *in.EndAt
	}
	if in.UnavailableMachines != nil {
		machines = *in.UnavailableMachines
	}
	if in.UnavailableWorkers != nil {
		workers = *in.UnavailableWorkers
	}
	if in.Reason != nil {
		reason = strings.TrimSpace(*in.Reason)
	}
	if in.SourceRef != nil {
		source = strings.TrimSpace(*in.SourceRef)
	}
	if status == "CANCELLED" {
		machines, workers = 0, 0
	}
	if err := validateMaintenanceWindow(start, end, machines, workers, status); err != nil {
		return nil, err
	}
	var mc, wc int
	if err := tx.QueryRowxContext(ctx, `SELECT machine_count,worker_count FROM work_centers WHERE id=$1 FOR SHARE`, wcID).Scan(&mc, &wc); err != nil {
		return nil, err
	}
	if machines > maxInt(mc, 1) || workers > maxInt(wc, 0) {
		return nil, domain.NewBadRequest("capacity reduction exceeds work center resources", nil)
	}
	if prev.Status == "COMPLETED" || prev.Status == "CANCELLED" {
		return nil, domain.NewConflict("maintenance event is terminal")
	}
	if prev.Status == "ACTIVE" && status == "PLANNED" {
		return nil, domain.NewConflict("ACTIVE maintenance cannot return to PLANNED")
	}
	rid := uuid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_event_revisions(id,maintenance_event_id,revision_no,status,start_at,end_at,unavailable_machines,unavailable_workers,reason,source_ref,actor_user_id,actor_username) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, rid, id, prev.RevisionNo+1, status, start, end, machines, workers, reason, source, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = eventType
	return s.Get(ctx, id)
}

func (s *MaintenanceService) List(ctx context.Context, workCenterID *uuid.UUID, includeTerminal bool) ([]domain.CurrentMaintenanceEvent, error) {
	q := `SELECT * FROM v_current_maintenance_events WHERE 1=1`
	args := []any{}
	if workCenterID != nil {
		args = append(args, *workCenterID)
		q += fmt.Sprintf(" AND work_center_id=$%d", len(args))
	}
	if !includeTerminal {
		q += ` AND status IN ('PLANNED','ACTIVE')`
	}
	q += ` ORDER BY start_at,work_center_code,id`
	var rows []domain.CurrentMaintenanceEvent
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *MaintenanceService) Get(ctx context.Context, id uuid.UUID) (*domain.MaintenanceEventDetail, error) {
	var ev domain.MaintenanceEvent
	if err := s.db.GetContext(ctx, &ev, `SELECT * FROM maintenance_events WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("maintenance event")
		}
		return nil, err
	}
	var revs []domain.MaintenanceEventRevision
	if err := s.db.SelectContext(ctx, &revs, `SELECT * FROM maintenance_event_revisions WHERE maintenance_event_id=$1 ORDER BY revision_no`, id); err != nil {
		return nil, err
	}
	var cur domain.CurrentMaintenanceEvent
	var cp *domain.CurrentMaintenanceEvent
	if err := s.db.GetContext(ctx, &cur, `SELECT * FROM v_current_maintenance_events WHERE id=$1`, id); err == nil {
		cp = &cur
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &domain.MaintenanceEventDetail{Event: ev, Current: cp, Revisions: revs}, nil
}

// CapacityEvents returns current PLANNED/ACTIVE reductions that overlap [from,to).
func (s *MaintenanceService) CapacityEvents(ctx context.Context, from, to time.Time) ([]domain.CurrentMaintenanceEvent, error) {
	var rows []domain.CurrentMaintenanceEvent
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM v_effective_maintenance_capacity WHERE start_at<$2 AND end_at>$1 ORDER BY work_center_id,start_at,id`, from, to)
	return rows, err
}

// MaintenanceExceptionType maps maintenance evidence to the Full Pegging exception vocabulary.
func MaintenanceExceptionType(eventType string) string {
	switch eventType {
	case "PREVENTIVE_MAINTENANCE":
		return "PREVENTIVE_MAINTENANCE_CAPACITY"
	case "BREAKDOWN":
		return "BREAKDOWN_CAPACITY"
	case "UNPLANNED_DOWNTIME":
		return "UNPLANNED_DOWNTIME_CAPACITY"
	default:
		return "PLANNED_DOWNTIME_CAPACITY"
	}
}

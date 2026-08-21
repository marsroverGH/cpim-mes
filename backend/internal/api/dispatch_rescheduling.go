package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func authenticatedScheduleActor(r *http.Request) (service.ScheduleExecutionActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ScheduleExecutionActor{}, domain.NewUnauthorized("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ScheduleExecutionActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.ScheduleExecutionActor{UserID: id, Username: claims.Username, Role: claims.Role}, nil
}

func parseOptionalAsOf(r *http.Request) (time.Time, error) {
	v := strings.TrimSpace(r.URL.Query().Get("asOf"))
	if v == "" {
		return time.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, domain.NewBadRequest("asOf must be RFC3339", err)
	}
	return t, nil
}

func (h *server) currentDispatch(w http.ResponseWriter, r *http.Request) {
	asOf, err := parseOptionalAsOf(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var wcID *uuid.UUID
	if v := strings.TrimSpace(r.URL.Query().Get("workCenterId")); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("workCenterId must be UUID", err))
			return
		}
		wcID = &id
	}
	res, err := h.s.ScheduleExecution.Dispatch(r.Context(), wcID, asOf)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) currentScheduleExecution(w http.ResponseWriter, r *http.Request) {
	res, err := h.s.ScheduleExecution.ExecutionState(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) listDispatchPolicies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ScheduleExecution.ListPolicies(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (h *server) currentDispatchPolicy(w http.ResponseWriter, r *http.Request) {
	row, err := h.s.ScheduleExecution.CurrentPolicy(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}
func (h *server) createDispatchPolicy(w http.ResponseWriter, r *http.Request) {
	actor, err := authenticatedScheduleActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var in service.DispatchPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, 400, err)
		return
	}
	row, err := h.s.ScheduleExecution.CreatePolicy(r.Context(), in, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}
func (h *server) activateDispatchPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedScheduleActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ScheduleExecution.ActivatePolicy(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) currentScheduleAdherence(w http.ResponseWriter, r *http.Request) {
	asOf, err := parseOptionalAsOf(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.ScheduleExecution.CurrentAdherence(r.Context(), asOf)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}
func (h *server) snapshotScheduleAdherence(w http.ResponseWriter, r *http.Request) {
	actor, err := authenticatedScheduleActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		AsOf string `json:"asOf"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	asOf := time.Now()
	if strings.TrimSpace(body.AsOf) != "" {
		asOf, err = time.Parse(time.RFC3339, body.AsOf)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("asOf must be RFC3339", err))
			return
		}
	}
	res, err := h.s.ScheduleExecution.SnapshotAdherence(r.Context(), asOf, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, res)
}
func (h *server) listScheduleAdherenceSnapshots(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ScheduleExecution.ListAdherenceSnapshots(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (h *server) getScheduleAdherenceSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.ScheduleExecution.GetAdherenceSnapshot(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) runDynamicReschedule(w http.ResponseWriter, r *http.Request) {
	actor, err := authenticatedScheduleActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		TriggerType string `json:"triggerType"`
		TriggerRef  string `json:"triggerRef"`
		Reason      string `json:"reason"`
		AsOf        string `json:"asOf"`
		HorizonDays int    `json:"horizonDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	var asOf time.Time
	if strings.TrimSpace(body.AsOf) != "" {
		asOf, err = time.Parse(time.RFC3339, body.AsOf)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("asOf must be RFC3339", err))
			return
		}
	}
	res, err := h.s.ScheduleExecution.Reschedule(r.Context(), service.DynamicRescheduleRequest{TriggerType: body.TriggerType, TriggerRef: body.TriggerRef, Reason: body.Reason, AsOf: asOf, HorizonDays: body.HorizonDays}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, res)
}
func (h *server) processPendingReschedule(w http.ResponseWriter, r *http.Request) {
	res, err := h.s.ScheduleExecution.ProcessPendingSignals(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}
func (h *server) listDynamicRescheduleRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ScheduleExecution.ListRescheduleRuns(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (h *server) getDynamicRescheduleRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.ScheduleExecution.GetRescheduleRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}
func (h *server) listRescheduleSignals(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ScheduleExecution.PendingSignals(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) dispatchPolicyDefaults(w http.ResponseWriter, r *http.Request) {
	// Small convenience endpoint for clients that want explicit documented defaults.
	writeJSON(w, 200, map[string]any{"freezeMinutes": 240, "firmMinutes": 1440, "startLateThresholdMinutes": 30, "completionLateThresholdMinutes": 30, "autoReschedule": true, "minAutoIntervalMinutes": 15, "setupMatchBonus": 20})
}

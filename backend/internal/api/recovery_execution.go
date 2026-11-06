package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
)

func writeRecoveryExecutionError(w http.ResponseWriter, err error) {
	var app *domain.AppError
	if errors.As(err, &app) {
		writeError(w, app.Status, app)
		return
	}
	writeError(w, http.StatusInternalServerError, domain.NewInternal(err))
}

func (h *server) listRecoveryExecutions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.RecoveryExecution.List(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) getRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row, err := h.s.RecoveryExecution.Get(r.Context(), id)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) prepareRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	scenarioID, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.RecoveryExecutionPrepareInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.RecoveryExecution.Prepare(r.Context(), scenarioID, body, actor)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) preflightRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.RecoveryExecution.Preflight(r.Context(), id, actor)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) approveRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.RecoveryExecutionApprovalInput
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	actor, err := authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.RecoveryExecution.Approve(r.Context(), id, body, actor)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) executeRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.RecoveryExecution.Execute(r.Context(), id, actor)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) cancelRecoveryExecution(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.RecoveryExecutionCancelInput
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	actor, err := authenticatedRecoveryPlanningActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.RecoveryExecution.Cancel(r.Context(), id, body, actor)
	if err != nil {
		writeRecoveryExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

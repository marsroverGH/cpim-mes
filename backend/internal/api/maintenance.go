package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func (h *server) listMaintenanceEvents(w http.ResponseWriter, r *http.Request) {
	var wcID *uuid.UUID
	if raw := r.URL.Query().Get("workCenterId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		wcID = &id
	}
	includeTerminal := r.URL.Query().Get("includeTerminal") == "true"
	rows, err := h.s.Maintenance.List(r.Context(), wcID, includeTerminal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) getMaintenanceEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row, err := h.s.Maintenance.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) createMaintenanceEvent(w http.ResponseWriter, r *http.Request) {
	var body service.MaintenanceEventInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.Maintenance.Create(r.Context(), body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) reviseMaintenanceEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.MaintenanceRevisionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.Maintenance.Revise(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

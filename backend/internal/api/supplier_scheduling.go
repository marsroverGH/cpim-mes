package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/service"
)

func (h *server) listSupplierScheduleEvents(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rows, err := h.s.SupplierScheduling.ListEvents(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) recordSupplierScheduleEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.SupplierScheduleEventInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.SupplierScheduling.RecordEvent(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) refreshSupplierReliability(w http.ResponseWriter, r *http.Request) {
	var body service.SupplierReliabilityRunInput
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.SupplierScheduling.RefreshReliability(r.Context(), body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) listSupplierReliability(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SupplierScheduling.LatestReliability(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) listSupplierReliabilityRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SupplierScheduling.ListReliabilityRuns(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) getSupplierReliabilityRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row, err := h.s.SupplierScheduling.GetReliabilityRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

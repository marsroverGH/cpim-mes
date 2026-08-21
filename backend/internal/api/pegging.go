package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/service"
)

func (h *server) runSalesOrderPegging(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.PeggingRunInput
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
	row, err := h.s.Pegging.Run(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) listSalesOrderPeggingRuns(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rows, err := h.s.Pegging.ListRuns(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) getPeggingRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row, err := h.s.Pegging.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) scanPlanningExceptions(w http.ResponseWriter, r *http.Request) {
	var body service.ExceptionScanInput
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
	row, err := h.s.Pegging.Scan(r.Context(), body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) listPlanningExceptions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Pegging.ListCurrentExceptions(
		r.Context(),
		strings.TrimSpace(r.URL.Query().Get("status")),
		strings.TrimSpace(r.URL.Query().Get("severity")),
		strings.TrimSpace(r.URL.Query().Get("type")),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) actOnPlanningException(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body service.ExceptionActionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.Pegging.ActOnException(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

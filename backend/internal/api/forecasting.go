package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func authenticatedForecastActor(r *http.Request) (service.ForecastActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ForecastActor{}, domain.NewUnauthorized("not authenticated")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ForecastActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.ForecastActor{UserID: userID, Username: claims.Username}, nil
}

// POST /api/forecast/run
func (h *server) runForecast(w http.ResponseWriter, r *http.Request) {
	var req service.ForecastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedForecastActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	res, err := h.s.Forecast.Run(r.Context(), req, actor)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// GET /api/forecast/runs?itemId=<uuid>
func (h *server) listForecastRuns(w http.ResponseWriter, r *http.Request) {
	var itemID *uuid.UUID
	if raw := r.URL.Query().Get("itemId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("invalid itemId", err))
			return
		}
		itemID = &id
	}
	rows, err := h.s.Forecast.ListRuns(r.Context(), itemID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// GET /api/forecast/runs/{id}
func (h *server) getForecastRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	row, err := h.s.Forecast.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

// POST /api/forecast/runs/{id}/activate
func (h *server) activateForecastRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedForecastActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.Forecast.ActivateRun(r.Context(), id, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/forecast/runs/{id}/consumption
func (h *server) forecastConsumption(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.Forecast.Consumption(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

// POST /api/forecast/runs/{id}/apply-to-mps
func (h *server) applyForecastConsumptionToMPS(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedForecastActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	count, err := h.s.Forecast.ApplyConsumptionToMPS(r.Context(), id, actor)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]int{"updatedMpsEntries": count})
}

// ====================================================================
// /api/cycle-counts
// ====================================================================

func (h *server) listCycleCounts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	rows, err := h.s.CycleCount.List(r.Context(), status)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) generateCycleCounts(w http.ResponseWriter, r *http.Request) {
	n, err := h.s.CycleCount.GenerateSchedule(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]int{"created": n})
}

func (h *server) recordCycleCount(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		CountedQty float64 `json:"countedQty"`
		Notes      string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.CycleCount.RecordCount(r.Context(), id, body.CountedQty, body.Notes); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

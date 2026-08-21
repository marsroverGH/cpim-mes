package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func authenticatedPerformanceActor(r *http.Request) (service.ProductionPerformanceActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ProductionPerformanceActor{}, domain.NewUnauthorized("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ProductionPerformanceActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.ProductionPerformanceActor{UserID: id, Username: claims.Username, Role: claims.Role}, nil
}

func (h *server) runProductionPerformance(w http.ResponseWriter, r *http.Request) {
	actor, err := authenticatedPerformanceActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		WindowStart     string `json:"windowStart"`
		WindowEnd       string `json:"windowEnd"`
		MinCompletedOps int    `json:"minCompletedOps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var start, end time.Time
	if body.WindowStart != "" {
		start, err = time.Parse("2006-01-02", body.WindowStart)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewBadRequest("windowStart must be YYYY-MM-DD", err))
			return
		}
	}
	if body.WindowEnd != "" {
		end, err = time.Parse("2006-01-02", body.WindowEnd)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewBadRequest("windowEnd must be YYYY-MM-DD", err))
			return
		}
	}
	res, err := h.s.ProductionPerformance.Run(r.Context(), service.ProductionPerformanceRequest{WindowStart: start, WindowEnd: end, MinCompletedOps: body.MinCompletedOps}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *server) listProductionPerformanceRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ProductionPerformance.ListRuns(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getProductionPerformanceRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.ProductionPerformance.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) listCapacityFeedback(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ProductionPerformance.ListFeedback(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) activateCapacityFeedback(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedPerformanceActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		EffectiveFrom string `json:"effectiveFrom"`
		Notes         string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var effective time.Time
	if body.EffectiveFrom != "" {
		effective, err = time.Parse("2006-01-02", body.EffectiveFrom)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("effectiveFrom must be YYYY-MM-DD", err))
			return
		}
	}
	res, err := h.s.ProductionPerformance.ActivateFeedback(r.Context(), id, effective, body.Notes, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) archiveCapacityFeedback(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedPerformanceActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	res, err := h.s.ProductionPerformance.ArchiveFeedback(r.Context(), id, body.Notes, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

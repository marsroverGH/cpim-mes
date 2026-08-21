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

func authenticatedControlTowerActor(
	r *http.Request,
) (service.ControlTowerActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ControlTowerActor{},
			domain.NewUnauthorized("not authenticated")
	}

	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ControlTowerActor{},
			domain.NewUnauthorized("invalid authenticated user id")
	}

	return service.ControlTowerActor{
		UserID:   id,
		Username: claims.Username,
		Role:     string(claims.Role),
	}, nil
}

// POST /control-tower/refresh
func (h *server) refreshControlTower(
	w http.ResponseWriter,
	r *http.Request,
) {
	var body struct {
		AsOf string `json:"asOf"`
	}

	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	asOf := time.Now()

	if strings.TrimSpace(body.AsOf) != "" {
		var err error
		asOf, err = time.Parse(time.RFC3339, body.AsOf)
		if err != nil {
			writeError(
				w,
				http.StatusBadRequest,
				domain.NewBadRequest(
					"asOf must be RFC3339",
					err,
				),
			)
			return
		}
	}

	res, err := h.s.ControlTower.Refresh(
		r.Context(),
		asOf,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, res)
}

// GET /control-tower
func (h *server) controlTowerDashboard(
	w http.ResponseWriter,
	r *http.Request,
) {
	res, err := h.s.ControlTower.Dashboard(
		r.Context(),
		service.ControlTowerDashboardFilter{
			Status: strings.TrimSpace(
				r.URL.Query().Get("status"),
			),
			PriorityBand: strings.TrimSpace(
				r.URL.Query().Get("priorityBand"),
			),
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// GET /control-tower/cases/{id}
func (h *server) getControlTowerCase(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	row, err := h.s.ControlTower.GetCase(
		r.Context(),
		id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// GET /control-tower/cases/{id}/recommendations
func (h *server) controlTowerRecommendations(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	rows, err := h.s.ControlTower.Recommendations(
		r.Context(),
		id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, rows)
}

// GET /control-tower/cases/{id}/actions
func (h *server) controlTowerCaseActions(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	rows, err := h.s.ControlTower.CaseActions(
		r.Context(),
		id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, rows)
}

// POST /control-tower/cases/{id}/actions
func (h *server) actOnControlTowerCase(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var body service.ControlTowerCaseActionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	actor, err := authenticatedControlTowerActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	row, err := h.s.ControlTower.ActOnCase(
		r.Context(),
		id,
		body,
		actor,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, row)
}

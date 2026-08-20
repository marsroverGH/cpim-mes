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

// ---------- Work Centers ----------

func (h *server) listWorkCenters(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.WorkCenters.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createWorkCenter(w http.ResponseWriter, r *http.Request) {
	var x domain.WorkCenter
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.WorkCenters.Create(r.Context(), &x); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, x)
}

func (h *server) updateWorkCenter(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var x domain.WorkCenter
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		writeError(w, 400, err)
		return
	}
	x.ID = id
	if err := h.s.WorkCenters.Update(r.Context(), &x); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, x)
}

func (h *server) deleteWorkCenter(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.WorkCenters.Delete(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- Routings ----------

func (h *server) listRoutings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Routings.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createRouting(w http.ResponseWriter, r *http.Request) {
	var x domain.Routing
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Routings.Create(r.Context(), &x); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, x)
}

func (h *server) routingOps(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Routings.Operations(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addRoutingOp(w http.ResponseWriter, r *http.Request) {
	rid, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var op domain.RoutingOperation
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
		writeError(w, 400, err)
		return
	}
	op.RoutingID = rid
	if err := h.s.Routings.AddOperation(r.Context(), &op); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, op)
}

func (h *server) updateRoutingOp(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var op domain.RoutingOperation
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
		writeError(w, 400, err)
		return
	}
	op.ID = id
	if err := h.s.Routings.UpdateOperation(r.Context(), &op); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, op)
}

func (h *server) deleteRoutingOp(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Routings.DeleteOperation(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- CRP ----------

func decodeCRPWindow(r *http.Request) (int, time.Time, error) {
	var body struct {
		HorizonDays int    `json:"horizonDays"`
		StartDate   string `json:"startDate"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return 0, time.Time{}, err
		}
	}
	var start time.Time
	if strings.TrimSpace(body.StartDate) != "" {
		parsed, err := time.Parse("2006-01-02", body.StartDate)
		if err != nil {
			return 0, time.Time{}, strErr("startDate must be YYYY-MM-DD")
		}
		start = parsed
	}
	return body.HorizonDays, start, nil
}

func (h *server) runCRP(w http.ResponseWriter, r *http.Request) {
	horizon, start, err := decodeCRPWindow(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.CRP.Run(r.Context(), service.CRPRequest{HorizonDays: horizon, StartDate: start})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func crpActorFromRequest(r *http.Request) (service.CRPActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return service.CRPActor{}, strErr("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.CRPActor{}, strErr("invalid authenticated user id")
	}
	return service.CRPActor{UserID: id, Username: claims.Username}, nil
}

func (h *server) runFiniteCRP(w http.ResponseWriter, r *http.Request) {
	horizon, start, err := decodeCRPWindow(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := crpActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	res, err := h.s.CRP.FiniteSchedule(r.Context(), service.CRPFiniteRequest{HorizonDays: horizon, StartDate: start}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) listCRPScheduleRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.CRP.ListFiniteRuns(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getCRPScheduleRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.CRP.GetFiniteRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---------- Cost Rollup ----------

func (h *server) runCostRollup(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.CostRollup.Rollup(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ---------- Detailed Scheduling masters ----------

func (h *server) routingOpAlternatives(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Routings.Alternatives(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addRoutingOpAlternative(w http.ResponseWriter, r *http.Request) {
	opID, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var x domain.RoutingOperationAlternative
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		writeError(w, 400, err)
		return
	}
	x.RoutingOperationID = opID
	if err := h.s.Routings.AddAlternative(r.Context(), &x); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, x)
}

func (h *server) deleteRoutingOpAlternative(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Routings.DeleteAlternative(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) workCenterSetupMatrix(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.WorkCenters.SetupMatrix(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) upsertWorkCenterSetupMatrix(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var x domain.WorkCenterSetupMatrixRow
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		writeError(w, 400, err)
		return
	}
	x.WorkCenterID = id
	if err := h.s.WorkCenters.UpsertSetupMatrix(r.Context(), &x); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, x)
}

func (h *server) deleteWorkCenterSetupMatrix(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.WorkCenters.DeleteSetupMatrix(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- Detailed Scheduling ----------

func (h *server) runDetailedSchedule(w http.ResponseWriter, r *http.Request) {
	horizon, start, err := decodeCRPWindow(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := crpActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	res, err := h.s.CRP.DetailedSchedule(r.Context(), service.DetailedScheduleRequest{HorizonDays: horizon, StartDate: start}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) listDetailedScheduleRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.CRP.ListDetailedRuns(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getDetailedScheduleRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.CRP.GetDetailedRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

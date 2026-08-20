package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

// ====================================================================
// S&OP Handlers
// ====================================================================

func (h *server) listItemGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SOP.ListGroups(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createItemGroup(w http.ResponseWriter, r *http.Request) {
	var g domain.ItemGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.SOP.CreateGroup(r.Context(), &g); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, g)
}

func (h *server) listSOPPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SOP.ListPlans(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) upsertSOPPlan(w http.ResponseWriter, r *http.Request) {
	var p domain.SOPPlan
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.SOP.UpsertPlan(r.Context(), &p); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, p)
}

func (h *server) deleteSOPPlan(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.SOP.DeletePlan(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

func authenticatedSOPActor(r *http.Request) (service.SOPActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.SOPActor{}, domain.NewUnauthorized("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.SOPActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.SOPActor{UserID: id, Username: claims.Username}, nil
}

func (h *server) listSOPProductMixVersions(w http.ResponseWriter, r *http.Request) {
	var groupID *uuid.UUID
	if raw := r.URL.Query().Get("groupId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		groupID = &id
	}
	rows, err := h.s.SOP.ListProductMixVersions(r.Context(), groupID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createSOPProductMixVersion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GroupID uuid.UUID                     `json:"groupId"`
		Name    string                        `json:"name"`
		Lines   []service.ProductMixInputLine `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSOPActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	out, err := h.s.SOP.CreateProductMixVersion(r.Context(), body.GroupID, body.Name, body.Lines, actor)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, out)
}

func (h *server) activateSOPProductMixVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSOPActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.SOP.ActivateProductMixVersion(r.Context(), id, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) previewSOPDisaggregation(w http.ResponseWriter, r *http.Request) {
	planID, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	mixID, err := uuid.Parse(r.URL.Query().Get("mixVersionId"))
	if err != nil {
		writeError(w, 400, domain.NewBadRequest("mixVersionId is required", err))
		return
	}
	out, err := h.s.SOP.PreviewDisaggregation(r.Context(), planID, mixID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *server) applySOPDisaggregation(w http.ResponseWriter, r *http.Request) {
	planID, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		MixVersionID uuid.UUID `json:"mixVersionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSOPActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	out, err := h.s.SOP.ApplyDisaggregationToMPS(r.Context(), planID, body.MixVersionID, actor)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, out)
}

func (h *server) listSOPDisaggregationRuns(w http.ResponseWriter, r *http.Request) {
	var planID *uuid.UUID
	if raw := r.URL.Query().Get("planId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		planID = &id
	}
	rows, err := h.s.SOP.ListDisaggregationRuns(r.Context(), planID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// RCCP Handlers
// ====================================================================

func (h *server) runRCCP(w http.ResponseWriter, r *http.Request) {
	wd, _ := strconv.Atoi(r.URL.Query().Get("workingDays"))
	rows, err := h.s.RCCP.Run(r.Context(), wd)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) listRCCPProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.RCCP.ListProfiles(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) upsertRCCPProfile(w http.ResponseWriter, r *http.Request) {
	var p domain.RCCPProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.RCCP.UpsertProfile(r.Context(), &p); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, p)
}

// ====================================================================
// ECO Handlers
// ====================================================================

func authenticatedECOActor(r *http.Request) (service.ECOActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ECOActor{}, domain.NewUnauthorized("not authenticated")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ECOActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.ECOActor{UserID: userID, Username: claims.Username}, nil
}

func (h *server) listECOs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ECO.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createECO(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ECONo         string `json:"ecoNo"`
		Title         string `json:"title"`
		Description   string `json:"description"`
		EffectiveDate string `json:"effectiveDate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	effectiveDate, err := time.Parse("2006-01-02", body.EffectiveDate)
	if err != nil {
		// Accept an RFC3339 value as well for non-browser API clients.
		if t, e := time.Parse(time.RFC3339, body.EffectiveDate); e == nil {
			effectiveDate = t
		} else {
			writeError(w, 400, domain.NewBadRequest("effectiveDate must be YYYY-MM-DD or RFC3339", err))
			return
		}
	}
	e := domain.EngineeringChange{
		ECONo: body.ECONo, Title: body.Title, Description: body.Description,
		EffectiveDate: effectiveDate,
	}
	actor, err := authenticatedECOActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.ECO.Create(r.Context(), &e, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, e)
}

func (h *server) approveECO(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedECOActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.ECO.Approve(r.Context(), id, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) applyECO(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedECOActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.ECO.Apply(r.Context(), id, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) cancelECO(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedECOActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.ECO.Cancel(r.Context(), id, actor); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) listECOComponents(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.ECO.ListComponents(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addECOComponent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var c domain.ECOComponent
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, err)
		return
	}
	c.ECOID = id
	if err := h.s.ECO.AddComponent(r.Context(), &c); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, c)
}

func (h *server) listECOHistory(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.ECO.ListHistory(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// Agent (AI Assistant) Handler
// ====================================================================

func (h *server) askAgent(w http.ResponseWriter, r *http.Request) {
	var req service.AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.Agent.Ask(r.Context(), req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

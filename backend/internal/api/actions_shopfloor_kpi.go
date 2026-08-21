package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

// ====================================================================
// MRP Action Messages
// ====================================================================

// GET /api/mrp/action-messages?horizonDays=28
func (h *server) listActionMessages(w http.ResponseWriter, r *http.Request) {
	horizon, _ := strconv.Atoi(r.URL.Query().Get("horizonDays"))
	rows, err := h.s.Actions.Run(r.Context(), horizon)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// Shop Floor
// ====================================================================

// GET /api/shop-floor/active — 未完了工程 (PENDING / READY / IN_PROGRESS / PAUSED)
func (h *server) shopFloorActive(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ShopFloor.Active(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// GET /api/work-orders/{id}/operations
func (h *server) listWOOperations(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.ShopFloor.ListByWO(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// authenticatedShopFloorActor derives the operator identity only from the
// verified JWT claims populated by authMiddleware. Request bodies are never
// trusted for operator identity.
func authenticatedShopFloorActor(r *http.Request) (service.ShopFloorActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.ShopFloorActor{}, domain.NewUnauthorized("not authenticated")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.ShopFloorActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.ShopFloorActor{UserID: userID, Username: claims.Username}, nil
}

// POST /api/wo-operations/{opId}/start
// Operator identity is always taken from the authenticated user.
func (h *server) startOperation(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedShopFloorActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	if err := h.s.ShopFloor.Start(r.Context(), id, actor); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// POST /api/wo-operations/{opId}/stop  body: {"notes":"..."}
// Elapsed minutes are measured from the server-side active start timestamp.
func (h *server) stopOperation(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedShopFloorActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.s.ShopFloor.Stop(r.Context(), id, actor, body.Notes); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// POST /api/wo-operations/{opId}/complete
// body: {"completedQty": ..., "notes": "..."}
// completedQty is cumulative good quantity for this operation. Operator and
// elapsed minutes are determined by the backend.
func (h *server) completeOperation(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedShopFloorActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		CompletedQty float64 `json:"completedQty"`
		Notes        string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if body.CompletedQty <= 0 {
		writeError(w, 400, domain.NewBadRequest("completedQty must be > 0", nil))
		return
	}
	if err := h.s.ShopFloor.Complete(r.Context(), id, body.CompletedQty, actor, body.Notes); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// POST /api/wo-operations/{opId}/scrap body: {"quantity":...,"notes":"..."}
func (h *server) scrapOperation(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedShopFloorActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	var body struct {
		Quantity float64 `json:"quantity"`
		Notes    string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.ShopFloor.ReportScrap(r.Context(), id, body.Quantity, actor, body.Notes); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// GET /api/wo-operations/{opId}/logs
func (h *server) operationLogs(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "opId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.ShopFloor.Logs(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// KPI Dashboard
// ====================================================================

// GET /api/kpi/dashboard
func (h *server) kpiDashboard(w http.ResponseWriter, r *http.Request) {
	res, err := h.s.KPI.Compute(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

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
// WIP / Progress
// ====================================================================

// POST /api/work-orders/{id}/progress  body: {"completedQty": 5}
// NOTE: this is non-inventory progress only; physical completion uses /complete.
func (h *server) updateWOProgress(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		CompletedQty float64 `json:"completedQty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if body.CompletedQty < 0 {
		writeError(w, 400, domain.NewBadRequest("completedQty must be >= 0", nil))
		return
	}
	wo, err := h.s.WorkOrders.Get(r.Context(), id)
	if err != nil || wo == nil {
		writeError(w, 404, domain.NewNotFound("work order"))
		return
	}
	if body.CompletedQty > wo.Quantity {
		writeError(w, 400, domain.NewBadRequest("completedQty cannot exceed planned quantity", nil))
		return
	}
	if body.CompletedQty < wo.CompletedQty {
		writeError(w, 400, domain.NewBadRequest("reported progress cannot be below physically completed quantity", nil))
		return
	}
	if err := h.s.WorkOrders.UpdateProgress(r.Context(), id, body.CompletedQty); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":                  id,
		"reportedProgressQty": body.CompletedQty,
		"completedQty":        wo.CompletedQty,
		"plannedQty":          wo.Quantity,
		"percentDone":         pct(body.CompletedQty, wo.Quantity),
	})
}

func pct(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b * 100
}

// ====================================================================
// ATP
// ====================================================================

// GET /api/items/{itemId}/atp?horizonDays=56&bucketDays=7
func (h *server) runATP(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "itemId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	horizon, _ := strconv.Atoi(r.URL.Query().Get("horizonDays"))
	bucket, _ := strconv.Atoi(r.URL.Query().Get("bucketDays"))
	res, err := h.s.ATP.Run(r.Context(), id, horizon, bucket)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// ====================================================================
// Quality
// ====================================================================

// GET /api/lots/{id}/inspections
func (h *server) listLotInspections(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Quality.ListByLot(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// POST /api/lots/{id}/inspections
// Inspector identity and inspection time are derived server-side. Client fields
// with those names are intentionally not part of the accepted request shape.
func (h *server) recordInspection(w http.ResponseWriter, r *http.Request) {
	lotID, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		Result    string  `json:"result"`
		DefectQty float64 `json:"defectQty"`
		Notes     string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		writeError(w, 401, domain.NewUnauthorized("not authenticated"))
		return
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeError(w, 401, domain.NewUnauthorized("invalid authenticated user"))
		return
	}
	q, err := h.s.Quality.Record(r.Context(), lotID, service.QualityRecordInput{
		Result: body.Result, DefectQty: body.DefectQty, Notes: body.Notes,
	}, service.QualityActor{UserID: userID, Username: claims.Username})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, q)
}

// GET /api/lots/{id}/quality-history
func (h *server) lotQualityHistory(w http.ResponseWriter, r *http.Request) {
	lotID, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Quality.StatusHistory(r.Context(), lotID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// GET /api/quality/recent
func (h *server) recentInspections(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.s.Quality.Recent(r.Context(), limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

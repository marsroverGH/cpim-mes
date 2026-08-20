package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func supplierQualityActorFromRequest(r *http.Request) (service.SupplierQualityActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.SupplierQualityActor{}, domain.NewUnauthorized("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.SupplierQualityActor{}, domain.NewUnauthorized("invalid authenticated user")
	}
	return service.SupplierQualityActor{UserID: id, Username: claims.Username, Role: claims.Role}, nil
}

func (h *server) listSupplierQualityProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SupplierQuality.Profiles(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) upsertSupplierQualityProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SupplierName       string  `json:"supplierName"`
		Status             string  `json:"status"`
		InspectionRequired bool    `json:"inspectionRequired"`
		TargetPPM          float64 `json:"targetPpm"`
		Notes              string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := supplierQualityActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SupplierQuality.UpsertProfile(r.Context(), service.SupplierQualityProfileInput{
		SupplierName: body.SupplierName, Status: body.Status, InspectionRequired: body.InspectionRequired,
		TargetPPM: body.TargetPPM, Notes: body.Notes,
	}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) supplierQualityScorecards(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SupplierQuality.Scorecards(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) listSupplierNCRs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SupplierQuality.ListNCRs(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("supplier"))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createSupplierNCR(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LotID        string  `json:"lotId"`
		InspectionID string  `json:"inspectionId"`
		AffectedQty  float64 `json:"affectedQty"`
		Severity     string  `json:"severity"`
		Description  string  `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	lotID, err := uuid.Parse(strings.TrimSpace(body.LotID))
	if err != nil {
		writeError(w, 400, domain.NewBadRequest("invalid lotId", err))
		return
	}
	var inspectionID *uuid.UUID
	if strings.TrimSpace(body.InspectionID) != "" {
		id, err := uuid.Parse(body.InspectionID)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("invalid inspectionId", err))
			return
		}
		inspectionID = &id
	}
	actor, err := supplierQualityActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SupplierQuality.CreateNCR(r.Context(), service.SupplierNCRCreateInput{
		LotID: lotID, InspectionID: inspectionID, AffectedQty: body.AffectedQty,
		Severity: body.Severity, Description: body.Description,
	}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) supplierNCRHistory(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.SupplierQuality.NCRHistory(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) dispositionSupplierNCR(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		Disposition string  `json:"disposition"`
		Quantity    float64 `json:"quantity"`
		Notes       string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := supplierQualityActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SupplierQuality.Disposition(r.Context(), id, service.SupplierNCRDispositionInput{
		Disposition: body.Disposition, Quantity: body.Quantity, Notes: body.Notes,
	}, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) closeSupplierNCRRework(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := supplierQualityActorFromRequest(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SupplierQuality.CloseRework(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

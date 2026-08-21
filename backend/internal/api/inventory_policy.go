package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func (h *server) listInventoryPolicies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.InventoryPolicy.Current(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) listInventoryPolicyVersions(w http.ResponseWriter, r *http.Request) {
	var itemID *uuid.UUID
	if raw := r.URL.Query().Get("itemId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		itemID = &id
	}
	rows, err := h.s.InventoryPolicy.ListVersions(r.Context(), itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) createInventoryPolicyVersion(w http.ResponseWriter, r *http.Request) {
	var body service.InventoryPolicyVersionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.InventoryPolicy.CreateVersion(r.Context(), body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) activateInventoryPolicyVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.InventoryPolicy.ActivateVersion(r.Context(), id, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) archiveInventoryPolicyVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	row, err := h.s.InventoryPolicy.ArchiveVersion(r.Context(), id, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *server) refreshInventoryPolicies(w http.ResponseWriter, r *http.Request) {
	var body service.InventoryPolicyRefreshInput
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
	row, err := h.s.InventoryPolicy.Refresh(r.Context(), body, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *server) listInventoryPolicyRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.InventoryPolicy.ListRuns(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) getInventoryPolicyRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row, err := h.s.InventoryPolicy.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

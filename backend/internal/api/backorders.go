package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/service"
)

func (h *server) listCustomerServiceClasses(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ProductAllocation.ListServiceClasses(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) setCustomerServiceClass(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.CustomerServiceClassInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ProductAllocation.SetCustomerServiceClass(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) setSalesOrderPriority(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.SalesOrderPriorityInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ProductAllocation.SetSalesOrderPriority(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) listProductAllocationPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.ProductAllocation.ListPlans(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createProductAllocationPlan(w http.ResponseWriter, r *http.Request) {
	var body service.ProductAllocationPlanInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ProductAllocation.CreatePlan(r.Context(), body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) activateProductAllocationPlan(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ProductAllocation.ActivatePlan(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) deactivateProductAllocationPlan(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.ProductAllocation.DeactivatePlan(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) previewBackorders(w http.ResponseWriter, r *http.Request) {
	var body service.BackorderPreviewInput
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.Backorders.Preview(r.Context(), body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) publishBackorders(w http.ResponseWriter, r *http.Request) {
	var body service.BackorderPublishInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.Backorders.Publish(r.Context(), body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) listBackorderRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Backorders.ListRuns(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getBackorderRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	row, err := h.s.Backorders.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

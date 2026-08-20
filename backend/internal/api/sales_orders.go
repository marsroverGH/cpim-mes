package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

func authenticatedSalesOrderActor(r *http.Request) (service.SalesOrderActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.SalesOrderActor{}, domain.NewUnauthorized("not authenticated")
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.SalesOrderActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.SalesOrderActor{UserID: id, Username: claims.Username, Role: claims.Role}, nil
}

func (h *server) listCustomers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SalesOrders.ListCustomers(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var body service.CustomerInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.CreateCustomer(r.Context(), body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) updateCustomer(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.CustomerInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.UpdateCustomer(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) listSalesOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.SalesOrders.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getSalesOrder(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	row, err := h.s.SalesOrders.Get(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) createSalesOrder(w http.ResponseWriter, r *http.Request) {
	var body service.SalesOrderCreateInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.Create(r.Context(), body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

func (h *server) confirmSalesOrder(w http.ResponseWriter, r *http.Request) {
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
	row, err := h.s.SalesOrders.Confirm(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) cancelSalesOrder(w http.ResponseWriter, r *http.Request) {
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
	row, err := h.s.SalesOrders.Cancel(r.Context(), id, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) allocateSalesOrderLine(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.SalesOrderAllocationInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.Allocate(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) releaseSalesOrderLine(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.SalesOrderReleaseInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.ReleaseAllocation(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, row)
}

func (h *server) shipSalesOrderLine(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.SalesOrderShipmentInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	row, err := h.s.SalesOrders.Ship(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, row)
}

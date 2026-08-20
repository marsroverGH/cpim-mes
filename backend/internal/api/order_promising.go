package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/service"
)

func (h *server) checkSalesOrderPromise(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.OrderPromiseCheckInput
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
	res, err := h.s.OrderPromising.Check(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, res)
}

func (h *server) acceptSalesOrderPromise(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body service.OrderPromiseAcceptInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	actor, err := authenticatedSalesOrderActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	res, err := h.s.OrderPromising.Accept(r.Context(), id, body, actor)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) listSalesOrderPromiseRuns(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.OrderPromising.ListRuns(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getOrderPromiseRun(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.OrderPromising.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

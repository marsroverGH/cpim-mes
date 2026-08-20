package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/domain"
)

func (h *server) listCalendars(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Calendar.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) getCalendar(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	c, err := h.s.Calendar.Get(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if c == nil {
		writeError(w, 404, domain.NewNotFound("calendar"))
		return
	}
	writeJSON(w, 200, c)
}

func (h *server) createCalendar(w http.ResponseWriter, r *http.Request) {
	var c domain.WorkCalendar
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Calendar.Create(r.Context(), &c); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, c)
}

func (h *server) updateCalendar(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var c domain.WorkCalendar
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, err)
		return
	}
	c.ID = id
	if err := h.s.Calendar.Update(r.Context(), &c); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, c)
}

func (h *server) deleteCalendar(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Calendar.Delete(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) listExceptions(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Calendar.Exceptions(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addException(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var e domain.CalendarException
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, 400, err)
		return
	}
	e.CalendarID = id
	if err := h.s.Calendar.AddException(r.Context(), &e); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, e)
}

func (h *server) deleteException(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "exId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Calendar.DeleteException(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

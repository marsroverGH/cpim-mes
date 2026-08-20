package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/go-chi/chi/v5"
)

// ====================================================================
// Audit Middleware
// ====================================================================

// statusRecorder — wraps ResponseWriter to capture status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// auditMiddleware — POST/PUT/DELETE への mutating リクエストを audit_log に記録
func auditMiddleware(audit *service.AuditService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := r.Method
			// 参照系は記録しない (ログが膨大になるため)
			if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Read & restore body so handler can still consume it
			var bodyBytes []byte
			if r.Body != nil && r.ContentLength > 0 && r.ContentLength < (1<<20) {
				bodyBytes, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			// Skip very long uploads (multipart) and login (sensitive)
			if strings.Contains(r.URL.Path, "/items/import") ||
				strings.HasSuffix(r.URL.Path, "/auth/login") {
				bodyBytes = nil
			}

			c, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
			username, role := "anonymous", ""
			if c != nil {
				username = c.Username
				role = string(c.Role)
			}

			// Sniff resource from path (e.g. /api/items/{id} → items)
			resource := guessResource(r.URL.Path)
			resourceID := chi.URLParam(r, "id")

			var payload json.RawMessage
			if len(bodyBytes) > 0 {
				if json.Valid(bodyBytes) {
					payload = bodyBytes
				} else {
					payload = json.RawMessage(`{"_note":"non-JSON body redacted"}`)
				}
			}

			entry := &domain.AuditLogEntry{
				Username:   username,
				UserRole:   role,
				Action:     method + " " + r.URL.Path,
				Resource:   resource,
				ResourceID: resourceID,
				HTTPStatus: rec.status,
				IPAddress:  clientIP(r),
				Payload:    payload,
			}
			// best-effort; do NOT block the response on failure
			_ = audit.Record(context.Background(), entry)
		})
	}
}

func guessResource(path string) string {
	// /api/<resource>/...
	parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		return strings.SplitN(h, ",", 2)[0]
	}
	return r.RemoteAddr
}

// ====================================================================
// /api/audit-log
// ====================================================================

func (h *server) listAudit(w http.ResponseWriter, r *http.Request) {
	f := repository.AuditFilter{
		Username: r.URL.Query().Get("username"),
		Resource: r.URL.Query().Get("resource"),
	}
	rows, err := h.s.Audit.List(r.Context(), f)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// /api/lots
// ====================================================================

func (h *server) listLots(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Lots.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) lotsByItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "itemId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Lots.ByItem(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createLot(w http.ResponseWriter, r *http.Request) {
	var l domain.Lot
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Lots.Create(r.Context(), &l); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, l)
}

func (h *server) lotMovements(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Lots.Movements(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) lotWhereUsed(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Lots.WhereUsed(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addLotMovement(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var m domain.LotMovement
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, 400, err)
		return
	}
	m.LotID = id
	if err := h.s.Lots.AddMovement(r.Context(), &m); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, m)
}

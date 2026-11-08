package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
			// Deferred so a request that panics is recorded too. The panic keeps
			// propagating to middleware.Recoverer, which answers 500.
			completed := false
			defer func() {
				if !completed {
					rec.status = http.StatusInternalServerError
				}
				recordRequestAudit(audit, r, rec.status, bodyBytes)
			}()
			next.ServeHTTP(rec, r)
			completed = true
		})
	}
}

// recordRequestAudit writes the audit entry for a finished mutating request.
func recordRequestAudit(audit *service.AuditService, r *http.Request, status int, bodyBytes []byte) {
	// Skip very long uploads and every authentication route. Authentication
	// uses dedicated security audit events so passwords/tokens/cookies can
	// never be captured by generic request-body auditing.
	if strings.Contains(r.URL.Path, "/items/import") || strings.HasPrefix(r.URL.Path, "/api/auth/") || strings.HasPrefix(r.URL.Path, "/api/admin/users") {
		bodyBytes = nil
	}
	if strings.HasPrefix(r.URL.Path, "/api/auth/") {
		return
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
		Action:     r.Method + " " + r.URL.Path,
		Resource:   resource,
		ResourceID: resourceID,
		HTTPStatus: status,
		IPAddress:  clientIP(r),
		Payload:    payload,
	}
	// best-effort; do NOT block the response on failure
	recordAudit(context.Background(), audit, entry)
}

// recordAudit stores entry. A failure must not fail the request, but it must
// be visible, because the audit trail then has a gap. The payload is not logged.
func recordAudit(ctx context.Context, audit *service.AuditService, entry *domain.AuditLogEntry) {
	if err := audit.Record(ctx, entry); err != nil {
		log.Printf("audit record failed: username=%q action=%q status=%d: %v", entry.Username, entry.Action, entry.HTTPStatus, err)
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
	// In the supported production topology Nginx overwrites X-Real-IP before
	// forwarding to the internal-only backend. Ignore X-Forwarded-For here so
	// a client-supplied chain cannot select its own rate-limit/audit identity.
	if raw := strings.TrimSpace(r.Header.Get("X-Real-IP")); raw != "" {
		if ip := net.ParseIP(raw); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func recordAuthAudit(audit *service.AuditService, r *http.Request, username, role, event string, status int, details map[string]any) {
	if audit == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	if requestID := middleware.GetReqID(r.Context()); requestID != "" {
		details["requestId"] = requestID
	}
	entry := &domain.AuditLogEntry{
		Username:   strings.TrimSpace(username),
		UserRole:   strings.TrimSpace(role),
		Action:     event,
		Resource:   "authentication",
		HTTPStatus: status,
		IPAddress:  clientIP(r),
		Payload:    repository.MarshalPayload(details),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recordAudit(ctx, audit, entry)
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

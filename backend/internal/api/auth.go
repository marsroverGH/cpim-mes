package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/service"
)

// ====================================================================
// JWT auth middleware + secure browser cookie authentication
// ====================================================================

type ctxKey string

const (
	ctxKeyClaims     ctxKey = "claims"
	ctxKeyAuthSource ctxKey = "auth-source"
	ctxKeyAuthToken  ctxKey = "auth-token"
)

type authSource string

const (
	authSourceBearer authSource = "bearer"
	authSourceCookie authSource = "cookie"
)

const csrfHeaderName = "X-CSRF-Token"

// authMiddleware accepts explicit Bearer JWTs for CLI/API clients and the
// HttpOnly browser session cookie. Bearer takes precedence so non-browser API
// clients remain backward-compatible without participating in browser CSRF.
func authMiddleware(auth *service.AuthService, cookies authCookieConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, source, err := authenticationToken(r, cookies)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err)
				return
			}
			claims, err := auth.VerifyCurrent(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyClaims, claims)
			ctx = context.WithValue(ctx, ctxKeyAuthSource, source)
			ctx = context.WithValue(ctx, ctxKeyAuthToken, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticationToken(r *http.Request, cookies authCookieConfig) (string, authSource, error) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h != "" {
		if !strings.HasPrefix(h, "Bearer ") {
			return "", "", strErr("missing or malformed Authorization header")
		}
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if token == "" {
			return "", "", strErr("missing or malformed Authorization header")
		}
		return token, authSourceBearer, nil
	}

	cookie, err := r.Cookie(cookies.sessionName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", "", strErr("authentication required")
	}
	return cookie.Value, authSourceCookie, nil
}

// csrfMiddleware protects every unsafe request authenticated by browser cookie.
// It uses a signed double-submit token bound to the exact session JWT and also
// rejects browser cross-site requests using Fetch Metadata when available.
func csrfMiddleware(auth *service.AuthService, cookies authCookieConfig, allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) || r.Context().Value(ctxKeyAuthSource) != authSourceCookie {
				next.ServeHTTP(w, r)
				return
			}
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
				writeError(w, http.StatusForbidden, strErr("csrf validation failed"))
				return
			}
			if !trustedBrowserOrigin(r, allowedOrigins) {
				writeError(w, http.StatusForbidden, strErr("csrf validation failed"))
				return
			}
			cookie, err := r.Cookie(cookies.csrfName())
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusForbidden, strErr("csrf validation failed"))
				return
			}
			header := strings.TrimSpace(r.Header.Get(csrfHeaderName))
			if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
				writeError(w, http.StatusForbidden, strErr("csrf validation failed"))
				return
			}
			sessionToken, _ := r.Context().Value(ctxKeyAuthToken).(string)
			if sessionToken == "" || !auth.VerifyCSRFToken(sessionToken, header) {
				writeError(w, http.StatusForbidden, strErr("csrf validation failed"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

type strErr string

func (s strErr) Error() string { return string(s) }

// ====================================================================
// /api/auth/login, /api/auth/token, /api/auth/logout, /api/auth/me
// ====================================================================

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

const maxLoginRequestBytes = 8 << 10

func decodeLoginRequest(w http.ResponseWriter, r *http.Request) (loginRequest, error) {
	var body loginRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	return body, err
}

// login is browser-only authentication. The JWT is never serialized into the
// response body; it is stored only in an HttpOnly host-scoped session cookie.
func (h *server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !trustedBrowserOrigin(r, h.allowedOrigins) {
		writeError(w, http.StatusForbidden, strErr("cross-origin browser login rejected"))
		return
	}
	res, ok := h.authenticateLogin(w, r, "browser")
	if !ok {
		return
	}
	csrfToken, err := h.s.Auth.NewCSRFToken(res.Token)
	if err != nil {
		recordAuthAudit(h.s.Audit, r, res.Username, string(res.Role), "LOGIN_FAILURE", http.StatusInternalServerError, map[string]any{"channel": "browser", "reason": "session_setup"})
		writeError(w, http.StatusInternalServerError, strErr("authentication unavailable"))
		return
	}
	h.cookies.setSession(w, res.Token)
	h.cookies.setCSRF(w, csrfToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":    res.Username,
		"role":        res.Role,
		"permissions": permissionsForRole(res.Role),
	})
}

// tokenLogin is the explicit CLI/API compatibility path. It returns a Bearer
// token and does not set browser session or CSRF cookies.
func (h *server) tokenLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	res, ok := h.authenticateLogin(w, r, "api-token")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// authenticateLogin applies the same throttling and audit semantics to browser
// cookie login and explicit Bearer-token login. Passwords are consumed only by
// AuthService and are never included in audit payloads.
func (h *server) authenticateLogin(w http.ResponseWriter, r *http.Request, channel string) (*service.LoginResponse, bool) {
	body, err := decodeLoginRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil, false
	}

	ip := clientIP(r)
	if decision := h.loginLimiter.Check(ip, body.Username); decision.Blocked {
		h.writeLoginThrottled(w, r, body.Username, channel, decision)
		return nil, false
	}

	res, err := h.s.Auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			decision := h.loginLimiter.RecordFailure(ip, body.Username)
			if decision.Blocked {
				h.writeLoginThrottled(w, r, body.Username, channel, decision)
				return nil, false
			}
			recordAuthAudit(h.s.Audit, r, body.Username, "", "LOGIN_FAILURE", http.StatusUnauthorized, map[string]any{"channel": channel})
			writeError(w, http.StatusUnauthorized, service.ErrInvalidCredentials)
			return nil, false
		}
		recordAuthAudit(h.s.Audit, r, body.Username, "", "LOGIN_FAILURE", http.StatusInternalServerError, map[string]any{"channel": channel, "reason": "internal_error"})
		writeError(w, http.StatusInternalServerError, strErr("authentication unavailable"))
		return nil, false
	}

	h.loginLimiter.RecordSuccess(res.Username)
	recordAuthAudit(h.s.Audit, r, res.Username, string(res.Role), "LOGIN_SUCCESS", http.StatusOK, map[string]any{"channel": channel})
	return res, true
}

func (h *server) writeLoginThrottled(w http.ResponseWriter, r *http.Request, username, channel string, decision loginRateDecision) {
	retrySeconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
	recordAuthAudit(h.s.Audit, r, username, "", "LOGIN_THROTTLED", http.StatusTooManyRequests, map[string]any{
		"channel":           channel,
		"bucket":            decision.Bucket,
		"retryAfterSeconds": retrySeconds,
	})
	writeError(w, http.StatusTooManyRequests, strErr("too many login attempts; try again later"))
}

func (h *server) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	username, role := "anonymous", ""
	if claims != nil {
		username = claims.Username
		role = string(claims.Role)
	}
	source, _ := r.Context().Value(ctxKeyAuthSource).(authSource)
	h.cookies.clear(w)
	recordAuthAudit(h.s.Audit, r, username, role, "LOGOUT", http.StatusNoContent, map[string]any{"authSource": string(source)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if c == nil {
		writeError(w, http.StatusUnauthorized, strErr("not authenticated"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":    c.Username,
		"role":        c.Role,
		"userId":      c.UserID,
		"permissions": permissionsForRole(c.Role),
	})
}

// ====================================================================
// /api/abc-analysis
// ====================================================================

func (h *server) runABC(w http.ResponseWriter, r *http.Request) {
	var (
		rows any
		err  error
	)
	if raw := strings.TrimSpace(r.URL.Query().Get("asOf")); raw != "" {
		asOf, parseErr := service.ParseDateOnly(raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, strErr("asOf must be YYYY-MM-DD or RFC3339"))
			return
		}
		rows, err = h.s.ABC.RunAsOf(r.Context(), asOf)
	} else {
		rows, err = h.s.ABC.Run(r.Context())
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// /api/items/export.csv  &  /api/items/import (multipart)
// ====================================================================

func (h *server) exportItemsCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="items.csv"`)
	if err := h.s.CSV.ExportItems(r.Context(), w); err != nil {
		// header already sent — fallback log
		_ = err
	}
}

func (h *server) importItemsCSV(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, 400, err)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	defer file.Close()
	res, err := h.s.CSV.ImportItems(r.Context(), file)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

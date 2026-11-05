package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/service"
)

func TestProductionCookiesUseHostPrefixAndSecurityAttributes(t *testing.T) {
	cfg := newAuthCookieConfig(true, "Strict")
	rr := httptest.NewRecorder()
	cfg.setSession(rr, "jwt-value")
	cfg.setCSRF(rr, "csrf-value")
	cookies := rr.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d; want 2", len(cookies))
	}
	byName := map[string]*http.Cookie{}
	for _, c := range cookies {
		byName[c.Name] = c
	}
	session := byName[productionSessionCookie]
	if session == nil || !session.HttpOnly || !session.Secure || session.Path != "/" || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", session)
	}
	csrf := byName[productionCSRFCookie]
	if csrf == nil || csrf.HttpOnly || !csrf.Secure || csrf.Path != "/" || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatalf("csrf cookie = %#v", csrf)
	}
}

func TestCSRFMiddlewareRequiresSignedDoubleSubmitForCookieAuth(t *testing.T) {
	auth := service.NewAuthService(nil, "unit-test-secret")
	cfg := newAuthCookieConfig(false, "Lax")
	session := "session-jwt"
	csrf, err := auth.NewCSRFToken(session)
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	protected := csrfMiddleware(auth, cfg, []string{"http://localhost:5173"})(next)

	makeReq := func(header string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "http://backend:8080/api/items", strings.NewReader(`{}`))
		req.Header.Set("Origin", "http://localhost:5173")
		req.AddCookie(&http.Cookie{Name: cfg.csrfName(), Value: csrf})
		if header != "" {
			req.Header.Set(csrfHeaderName, header)
		}
		ctx := context.WithValue(req.Context(), ctxKeyAuthSource, authSourceCookie)
		ctx = context.WithValue(ctx, ctxKeyAuthToken, session)
		return req.WithContext(ctx)
	}

	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, makeReq(csrf))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("valid csrf status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	protected.ServeHTTP(rr, makeReq(""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d", rr.Code)
	}

	req := makeReq(csrf)
	req.Header.Set("Origin", "https://evil.example.com")
	rr = httptest.NewRecorder()
	protected.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin csrf status = %d", rr.Code)
	}
}

func TestCSRFMiddlewareDoesNotApplyToBearerClients(t *testing.T) {
	auth := service.NewAuthService(nil, "unit-test-secret")
	cfg := newAuthCookieConfig(true, "Strict")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	protected := csrfMiddleware(auth, cfg, nil)(next)
	req := httptest.NewRequest(http.MethodPost, "https://mes.example.com/api/items", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyAuthSource, authSourceBearer))
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("bearer status = %d", rr.Code)
	}
}

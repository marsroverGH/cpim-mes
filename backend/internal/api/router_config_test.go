package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterCORSUsesConfiguredCredentialedAllowList(t *testing.T) {
	h := NewRouter(nil, RouterConfig{AllowedOrigins: []string{"https://mes.example.com"}, CookieSecure: true, CookieSameSite: "Strict"})

	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
		req.Header.Set("Origin", "https://mes.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type,x-csrf-token")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://mes.example.com" {
			t.Fatalf("Access-Control-Allow-Origin = %q", got)
		}
		if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Access-Control-Allow-Credentials = %q", got)
		}
		if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
			t.Fatal("Access-Control-Allow-Headers missing")
		}
	})

	t.Run("unlisted origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q; want empty", got)
		}
	})
}

func TestRouterWithoutOriginsDoesNotEnableCORS(t *testing.T) {
	h := NewRouter(nil, RouterConfig{CookieSecure: true, CookieSameSite: "Strict"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://mes.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q; want empty", got)
	}
}

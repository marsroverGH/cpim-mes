package api

// Shared harness for the database-backed HTTP integration tests
// (apiint_*_test.go). Each env owns a private, fully migrated database from
// internal/testdb (skipped when CPIM_TEST_DATABASE_URL is unset) and drives the
// real router exactly like the browser frontend does: cookie login through
// /api/auth/login, then the session cookie plus the double-submit CSRF header
// on every request.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/cpim-mes/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	apiintOrigin    = "http://localhost:5173"
	apiintPassword  = "Test-Password-123!"
	apiintJWTSecret = "apiint-test-jwt-secret-0123456789abcdef"
)

type apiintEnv struct {
	t     *testing.T
	DB    *sqlx.DB
	Repos *repository.Repositories
	Svc   *service.Services
	H     http.Handler
	Ctx   context.Context
}

func apiintNewEnv(t *testing.T) *apiintEnv {
	t.Helper()
	db := testdb.New(t)
	repos := repository.NewRepositories(db)
	svc := service.NewServices(db, repos, service.ServicesConfig{JWTSecret: apiintJWTSecret})
	h := NewRouter(svc, RouterConfig{
		AllowedOrigins:                    []string{apiintOrigin},
		CookieSecure:                      false,
		CookieSameSite:                    "Lax",
		LoginRateLimitWindow:              time.Minute,
		LoginRateLimitIPMaxFailures:       1000,
		LoginRateLimitUsernameMaxFailures: 3,
	})
	return &apiintEnv{t: t, DB: db, Repos: repos, Svc: svc, H: h, Ctx: context.Background()}
}

// user creates an active user with the shared test password.
func (e *apiintEnv) user(username string, role domain.Role) *domain.User {
	e.t.Helper()
	u, err := e.Repos.Users.Create(e.Ctx, username, apiintPassword, role)
	if err != nil {
		e.t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

// apiintClient is a logged-in (or anonymous) browser-like client.
type apiintClient struct {
	env     *apiintEnv
	session string
	csrf    string
	// noCSRF suppresses the X-CSRF-Token header (cookies are still sent).
	noCSRF bool
	origin string
}

func (e *apiintEnv) anon() *apiintClient { return &apiintClient{env: e, origin: apiintOrigin} }

// login creates the user (if role != "") and signs in through /api/auth/login.
func (e *apiintEnv) login(username string, role domain.Role) *apiintClient {
	e.t.Helper()
	if role != "" {
		e.user(username, role)
	}
	c := e.anon()
	rr := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": apiintPassword})
	if rr.Code != http.StatusOK {
		e.t.Fatalf("login %s: status %d body %s", username, rr.Code, rr.Body.String())
	}
	for _, ck := range rr.Result().Cookies() {
		switch ck.Name {
		case developmentSessionCookie:
			c.session = ck.Value
		case developmentCSRFCookie:
			c.csrf = ck.Value
		}
	}
	if c.session == "" || c.csrf == "" {
		e.t.Fatalf("login %s did not set session/csrf cookies", username)
	}
	return c
}

// do sends a request. body may be nil, a string/[]byte (sent verbatim) or any
// value (JSON encoded). Every response is checked for the X-Request-Id header
// and for leaked database internals.
func (c *apiintClient) do(method, path string, body any) *httptest.ResponseRecorder {
	c.env.t.Helper()
	return c.doWith(method, path, body, nil)
}

func (c *apiintClient) doWith(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	c.env.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.env.t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://backend.test"+path, rd)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: developmentSessionCookie, Value: c.session})
	}
	if c.csrf != "" {
		req.AddCookie(&http.Cookie{Name: developmentCSRFCookie, Value: c.csrf})
		if !c.noCSRF {
			req.Header.Set(csrfHeaderName, c.csrf)
		}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	c.env.H.ServeHTTP(rr, req)
	if rr.Header().Get(requestIDHeader) == "" {
		c.env.t.Errorf("%s %s: missing %s header", method, path, requestIDHeader)
	}
	apiintAssertNoLeak(c.env.t, method+" "+path, rr)
	return rr
}

// apiintLeakMarkers are fragments that only appear in raw PostgreSQL / driver
// errors. None may ever reach a client.
var apiintLeakMarkers = []string{
	"SQLSTATE", "pgx", "pq:", "duplicate key value", "violates", "constraint \"",
	"relation \"", "column \"", "syntax error at", "pg_", "goroutine ",
}

func apiintAssertNoLeak(t *testing.T, what string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code < 400 {
		return
	}
	body := rr.Body.String()
	for _, m := range apiintLeakMarkers {
		if strings.Contains(body, m) {
			t.Errorf("%s: status %d leaks internal detail %q: %s", what, rr.Code, m, body)
		}
	}
	if rr.Code >= 500 {
		var m map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Errorf("%s: 5xx body is not JSON: %s", what, body)
			return
		}
		if m["error"] != "internal server error" && m["error"] != "authentication unavailable" {
			t.Errorf("%s: 5xx body is not sanitized: %s", what, body)
		}
	}
}

func apiintJSON[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (status %d body %q)", v, err, rr.Code, rr.Body.String())
	}
	return v
}

func apiintStatus(t *testing.T, rr *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("%s: status = %d; want %d; body %s", what, rr.Code, want, rr.Body.String())
	}
}

// apiintErrMsg returns the client-visible error text (plain {"error"} bodies
// and AppError {"message"} bodies).
func apiintErrMsg(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	m := apiintJSON[map[string]any](t, rr)
	if s, ok := m["error"].(string); ok {
		return s
	}
	if s, ok := m["message"].(string); ok {
		return s
	}
	return fmt.Sprint(m)
}

func (e *apiintEnv) itemID(code string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.DB.GetContext(e.Ctx, &id, `SELECT id FROM items WHERE code=$1`, code); err != nil {
		e.t.Fatalf("item %s: %v", code, err)
	}
	return id
}

func (e *apiintEnv) scalar(dst any, q string, args ...any) {
	e.t.Helper()
	if err := e.DB.GetContext(e.Ctx, dst, q, args...); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
}

func apiintRFC3339(d time.Duration) string {
	return time.Now().UTC().Add(d).Truncate(time.Second).Format(time.RFC3339)
}

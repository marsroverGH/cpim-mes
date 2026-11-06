package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

func TestAPIIntLoginSetsHardenedCookiesAndNeverReturnsToken(t *testing.T) {
	env := apiintNewEnv(t)
	env.user("alice", domain.RolePlanner)

	rr := env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "alice", "password": apiintPassword})
	apiintStatus(t, rr, http.StatusOK, "login")
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	body := apiintJSON[map[string]any](t, rr)
	if body["username"] != "alice" || body["role"] != "planner" {
		t.Fatalf("login body = %v", body)
	}
	if _, ok := body["token"]; ok || strings.Contains(rr.Body.String(), "eyJ") {
		t.Fatalf("browser login must not expose the JWT: %s", rr.Body.String())
	}
	perms, _ := body["permissions"].([]any)
	if len(perms) != len(permissionsForRole(domain.RolePlanner)) {
		t.Fatalf("permissions = %v", perms)
	}

	cookies := map[string]*http.Cookie{}
	for _, c := range rr.Result().Cookies() {
		cookies[c.Name] = c
	}
	s := cookies[developmentSessionCookie]
	if s == nil || !s.HttpOnly || s.Path != "/" || s.SameSite != http.SameSiteLaxMode || s.MaxAge <= 0 || s.Value == "" {
		t.Fatalf("session cookie = %#v", s)
	}
	c := cookies[developmentCSRFCookie]
	if c == nil || c.HttpOnly || c.Value == "" || c.Value == s.Value {
		t.Fatalf("csrf cookie = %#v", c)
	}

	// The audit trail records the success without the password.
	var n int
	env.scalar(&n, `SELECT count(*) FROM audit_log WHERE action='LOGIN_SUCCESS' AND username='alice'`)
	if n != 1 {
		t.Fatalf("LOGIN_SUCCESS audit rows = %d", n)
	}
	var leaked int
	env.scalar(&leaked, `SELECT count(*) FROM audit_log WHERE payload::text LIKE '%'||$1||'%'`, apiintPassword)
	if leaked != 0 {
		t.Fatal("password leaked into audit_log")
	}
}

func TestAPIIntLoginFailures(t *testing.T) {
	env := apiintNewEnv(t)
	env.user("bob", domain.RoleViewer)
	anon := env.anon()

	t.Run("wrong password is a generic 401 without cookies", func(t *testing.T) {
		rr := anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "bob", "password": "nope"})
		apiintStatus(t, rr, http.StatusUnauthorized, "wrong password")
		if len(rr.Result().Cookies()) != 0 {
			t.Fatal("failed login must not set cookies")
		}
		unknown := anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "nobody", "password": "nope"})
		apiintStatus(t, unknown, http.StatusUnauthorized, "unknown user")
		if apiintErrMsg(t, rr) != apiintErrMsg(t, unknown) {
			t.Fatalf("unknown user and wrong password must be indistinguishable: %q vs %q", rr.Body.String(), unknown.Body.String())
		}
	})
	t.Run("unknown JSON field is rejected", func(t *testing.T) {
		rr := anon.do(http.MethodPost, "/api/auth/login", `{"username":"bob","password":"x","admin":true}`)
		apiintStatus(t, rr, http.StatusBadRequest, "unknown field")
	})
	t.Run("malformed JSON", func(t *testing.T) {
		rr := anon.do(http.MethodPost, "/api/auth/login", `{"username":`)
		apiintStatus(t, rr, http.StatusBadRequest, "malformed")
	})
	t.Run("cross-origin browser login is rejected", func(t *testing.T) {
		evil := env.anon()
		evil.origin = "https://evil.example"
		rr := evil.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "bob", "password": apiintPassword})
		apiintStatus(t, rr, http.StatusForbidden, "cross-origin login")
		if len(rr.Result().Cookies()) != 0 {
			t.Fatal("rejected login must not set cookies")
		}
	})
	t.Run("disabled user cannot log in", func(t *testing.T) {
		u := env.user("carol", domain.RoleOperator)
		if _, err := env.Repos.Users.UpdateActive(env.Ctx, u.ID, false); err != nil {
			t.Fatal(err)
		}
		rr := anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "carol", "password": apiintPassword})
		apiintStatus(t, rr, http.StatusUnauthorized, "disabled user")
	})
	t.Run("username throttling returns 429 with Retry-After and audit", func(t *testing.T) {
		env.user("dave", domain.RoleViewer)
		var last int
		for i := 0; i < 4; i++ {
			rr := anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "dave", "password": "bad"})
			last = rr.Code
			if rr.Code == http.StatusTooManyRequests {
				if rr.Header().Get("Retry-After") == "" {
					t.Fatal("429 without Retry-After")
				}
				break
			}
		}
		if last != http.StatusTooManyRequests {
			t.Fatalf("expected throttling after 3 failures, last status %d", last)
		}
		// Even the correct password is throttled now.
		rr := anon.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "dave", "password": apiintPassword})
		apiintStatus(t, rr, http.StatusTooManyRequests, "throttled correct password")
		var n int
		env.scalar(&n, `SELECT count(*) FROM audit_log WHERE action='LOGIN_THROTTLED' AND username='dave'`)
		if n == 0 {
			t.Fatal("LOGIN_THROTTLED not audited")
		}
	})
}

func TestAPIIntTokenLoginAndBearerAuth(t *testing.T) {
	env := apiintNewEnv(t)
	env.user("api", domain.RoleOperator)
	rr := env.anon().do(http.MethodPost, "/api/auth/token", map[string]string{"username": "api", "password": apiintPassword})
	apiintStatus(t, rr, http.StatusOK, "token login")
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("token login must not set browser cookies")
	}
	tok := apiintJSON[map[string]any](t, rr)["token"].(string)

	bearer := &apiintClient{env: env}
	me := bearer.doWith(http.MethodGet, "/api/auth/me", nil, map[string]string{"Authorization": "Bearer " + tok})
	apiintStatus(t, me, http.StatusOK, "bearer me")
	if body := apiintJSON[map[string]any](t, me); body["username"] != "api" || body["role"] != "operator" {
		t.Fatalf("me = %v", body)
	}
	// Bearer clients are not subject to browser CSRF; operator may ship / not plan.
	wo := bearer.doWith(http.MethodPost, "/api/work-orders", `{}`, map[string]string{"Authorization": "Bearer " + tok})
	apiintStatus(t, wo, http.StatusForbidden, "operator create WO via bearer")

	for _, h := range []string{"Token " + tok, "Bearer ", "Bearer not-a-jwt"} {
		rr := bearer.doWith(http.MethodGet, "/api/auth/me", nil, map[string]string{"Authorization": h})
		apiintStatus(t, rr, http.StatusUnauthorized, "bad Authorization "+h[:6])
	}
	bad := env.anon().do(http.MethodPost, "/api/auth/token", map[string]string{"username": "api", "password": "x"})
	apiintStatus(t, bad, http.StatusUnauthorized, "token wrong password")
}

func TestAPIIntSessionLifecycle(t *testing.T) {
	env := apiintNewEnv(t)
	c := env.login("erin", domain.RoleAdmin)

	me := c.do(http.MethodGet, "/api/auth/me", nil)
	apiintStatus(t, me, http.StatusOK, "me")
	body := apiintJSON[map[string]any](t, me)
	if body["username"] != "erin" || body["role"] != "admin" || body["userId"] == "" {
		t.Fatalf("me = %v", body)
	}
	if len(body["permissions"].([]any)) != 54 {
		t.Fatalf("admin permission count = %d; want 54", len(body["permissions"].([]any)))
	}

	t.Run("unauthenticated requests get 401", func(t *testing.T) {
		for _, p := range []string{"/api/auth/me", "/api/items", "/api/admin/users"} {
			apiintStatus(t, env.anon().do(http.MethodGet, p, nil), http.StatusUnauthorized, p)
		}
		forged := &apiintClient{env: env, session: "forged.jwt.value", csrf: c.csrf, origin: apiintOrigin}
		apiintStatus(t, forged.do(http.MethodGet, "/api/items", nil), http.StatusUnauthorized, "forged cookie")
	})

	t.Run("CSRF is required on cookie-authenticated mutations", func(t *testing.T) {
		item := map[string]any{"code": "CSRF-1", "name": "x", "type": "RM", "uom": "EA", "lotSize": 1, "lotSizeMethod": "LFL", "poqPeriods": 1}
		noHeader := *c
		noHeader.noCSRF = true
		apiintStatus(t, noHeader.do(http.MethodPost, "/api/items", item), http.StatusForbidden, "missing CSRF header")
		// Safe methods do not need the header.
		apiintStatus(t, noHeader.do(http.MethodGet, "/api/items", nil), http.StatusOK, "GET without CSRF")

		wrong := *c
		wrong.csrf = c.csrf + "x"
		apiintStatus(t, wrong.do(http.MethodPost, "/api/items", item), http.StatusForbidden, "mismatched CSRF")

		// A CSRF token minted for another session is rejected even when the
		// cookie and header agree.
		other := env.login("frank", domain.RoleAdmin)
		swapped := *c
		swapped.csrf = other.csrf
		apiintStatus(t, swapped.do(http.MethodPost, "/api/items", item), http.StatusForbidden, "CSRF of other session")

		crossSite := c.doWith(http.MethodPost, "/api/items", item, map[string]string{"Sec-Fetch-Site": "cross-site"})
		apiintStatus(t, crossSite, http.StatusForbidden, "Sec-Fetch-Site cross-site")
		evil := *c
		evil.origin = "https://evil.example"
		apiintStatus(t, evil.do(http.MethodPost, "/api/items", item), http.StatusForbidden, "untrusted Origin")

		var n int
		env.scalar(&n, `SELECT count(*) FROM items WHERE code='CSRF-1'`)
		if n != 0 {
			t.Fatal("a CSRF-rejected request created an item")
		}
		apiintStatus(t, c.do(http.MethodPost, "/api/items", item), http.StatusCreated, "valid CSRF")
	})

	t.Run("role change takes effect without re-login", func(t *testing.T) {
		u := env.user("gina", domain.RolePlanner)
		g := env.login("gina", "")
		apiintStatus(t, g.do(http.MethodPost, "/api/mrp/run", nil), http.StatusOK, "planner MRP")
		if _, err := env.Repos.Users.UpdateRole(env.Ctx, u.ID, domain.RoleViewer); err != nil {
			t.Fatal(err)
		}
		apiintStatus(t, g.do(http.MethodPost, "/api/mrp/run", nil), http.StatusForbidden, "demoted MRP")
		if _, err := env.Repos.Users.UpdateActive(env.Ctx, u.ID, false); err != nil {
			t.Fatal(err)
		}
		apiintStatus(t, g.do(http.MethodGet, "/api/items", nil), http.StatusUnauthorized, "disabled session")
	})

	t.Run("logout clears cookies and is audited", func(t *testing.T) {
		rr := c.do(http.MethodPost, "/api/auth/logout", nil)
		apiintStatus(t, rr, http.StatusNoContent, "logout")
		cleared := 0
		for _, ck := range rr.Result().Cookies() {
			if (ck.Name == developmentSessionCookie || ck.Name == developmentCSRFCookie) && ck.MaxAge < 0 && ck.Value == "" {
				cleared++
			}
		}
		if cleared != 2 {
			t.Fatalf("logout cleared %d cookies; want 2", cleared)
		}
		var n int
		env.scalar(&n, `SELECT count(*) FROM audit_log WHERE action='LOGOUT' AND username='erin'`)
		if n != 1 {
			t.Fatalf("LOGOUT audit rows = %d", n)
		}
		// Logout without CSRF is itself a CSRF-protected mutation.
		noHeader := *c
		noHeader.noCSRF = true
		apiintStatus(t, noHeader.do(http.MethodPost, "/api/auth/logout", nil), http.StatusForbidden, "logout without CSRF")
	})
}

func TestAPIIntPublicEndpoints(t *testing.T) {
	env := apiintNewEnv(t)
	a := env.anon()
	hz := a.do(http.MethodGet, "/healthz", nil)
	apiintStatus(t, hz, http.StatusOK, "healthz")
	if apiintJSON[map[string]string](t, hz)["status"] != "ok" {
		t.Fatal(hz.Body.String())
	}
	apiintStatus(t, a.do(http.MethodGet, "/version", nil), http.StatusOK, "version")
	spec := a.do(http.MethodGet, "/api/openapi.json", nil)
	apiintStatus(t, spec, http.StatusOK, "openapi")
	if !strings.Contains(spec.Body.String(), `"openapi"`) {
		t.Fatal("openapi.json body unexpected")
	}
	docs := a.do(http.MethodGet, "/api/docs", nil)
	apiintStatus(t, docs, http.StatusOK, "docs")
	if !strings.Contains(strings.ToLower(docs.Body.String()), "swagger") {
		t.Fatal("docs page unexpected")
	}
	apiintStatus(t, a.do(http.MethodGet, "/api/does-not-exist", nil), http.StatusNotFound, "unknown route")
}

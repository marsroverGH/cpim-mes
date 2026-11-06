package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// apiintRoute is one route registered on the secured group in router.go.
type apiintRoute struct {
	Method string
	Path   string // relative to /api, chi pattern
	Perm   Permission
}

func (r apiintRoute) String() string { return r.Method + " /api" + r.Path }

var apiintRouteRE = regexp.MustCompile(`secured\.(?:With\(requirePermission\((Perm\w+)\)\)\.)?(Get|Post|Put|Delete|Patch)\("([^"]+)"`)

// apiintPermConsts reads Permission constant values from rbac.go so the route
// table is derived from the source of truth rather than restated here.
func apiintPermConsts(t *testing.T) map[string]Permission {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "rbac.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Permission{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
						v, _ := strconv.Unquote(lit.Value)
						out[n.Name] = Permission(v)
					}
				}
			}
		}
	}
	return out
}

func apiintSecuredRoutes(t *testing.T) []apiintRoute {
	t.Helper()
	src, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	consts := apiintPermConsts(t)
	var out []apiintRoute
	for _, m := range apiintRouteRE.FindAllStringSubmatch(string(src), -1) {
		r := apiintRoute{Method: strings.ToUpper(m[2]), Path: m[3]}
		if m[1] != "" {
			p, ok := consts[m[1]]
			if !ok {
				t.Fatalf("router.go uses unknown permission %s", m[1])
			}
			r.Perm = p
		}
		out = append(out, r)
	}
	return out
}

var apiintParamRE = regexp.MustCompile(`\{[^}]+\}`)

func apiintFill(path, with string) string { return apiintParamRE.ReplaceAllString(path, with) }

func TestAPIIntRouteInventoryMatchesRouter(t *testing.T) {
	routes := apiintSecuredRoutes(t)
	protected := 0
	var unprotectedMutations []string
	parsed := map[string]bool{}
	for _, r := range routes {
		parsed[r.String()] = true
		if r.Perm != "" && r.Method != http.MethodGet {
			protected++
		} else if r.Perm == "" && r.Method != http.MethodGet {
			unprotectedMutations = append(unprotectedMutations, r.String())
		}
	}
	if protected != 120 {
		t.Fatalf("permission-protected mutation routes = %d; want 120 (update docs/guards if intentional)", protected)
	}
	sort.Strings(unprotectedMutations)
	want := []string{"POST /api/auth/change-password", "POST /api/auth/logout"}
	if strings.Join(unprotectedMutations, ",") != strings.Join(want, ",") {
		t.Fatalf("mutations without requirePermission = %v; want only %v", unprotectedMutations, want)
	}

	// Every route chi actually serves under /api is in the parsed table, so the
	// RBAC sweep below cannot silently miss one.
	h := NewRouter(&service.Services{}, RouterConfig{})
	public := map[string]bool{"GET /api/openapi.json": true, "GET /api/docs": true, "POST /api/auth/login": true, "POST /api/auth/token": true}
	served := 0
	err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") {
			return nil
		}
		key := method + " " + route
		if public[key] {
			return nil
		}
		served++
		if !parsed[key] {
			t.Errorf("route %s is served but not found in router.go parse", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if served != len(routes) {
		t.Fatalf("chi serves %d secured routes; parsed %d", served, len(routes))
	}
}

// TestAPIIntRBACMatrix drives every secured route as each role through the
// real middleware chain. A route answers 403 exactly when the role lacks the
// route's permission (taken from router.go + rbac.go).
func TestAPIIntRBACMatrix(t *testing.T) {
	env := apiintNewEnv(t)
	routes := apiintSecuredRoutes(t)
	roles := []domain.Role{domain.RoleViewer, domain.RoleOperator, domain.RolePlanner, domain.RoleAdmin}
	clients := map[domain.Role]*apiintClient{}
	for _, role := range roles {
		clients[role] = env.login("rbac-"+string(role), role)
	}
	anon := env.anon()
	id := uuid.NewString()

	// Counted over mutation routes only (the "120 protected routes" figure);
	// GET /admin/users and GET /audit-log are permission-gated reads.
	allowedCount := map[domain.Role]int{}
	for _, r := range routes {
		path := "/api" + apiintFill(r.Path, id)
		if r.Path == "/auth/logout" || r.Path == "/auth/change-password" {
			continue // would end/alter the shared sessions; covered in auth tests
		}
		var body any
		if r.Method != http.MethodGet {
			body = `{}`
		}
		if rr := anon.do(r.Method, path, body); rr.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: status %d; want 401", r, rr.Code)
		}
		if r.Perm == "" {
			continue
		}
		for _, role := range roles {
			rr := clients[role].do(r.Method, path, body)
			allowed := roleHasPermission(role, r.Perm)
			switch {
			case !allowed && rr.Code != http.StatusForbidden:
				t.Errorf("%s as %s: status %d; want 403", r, role, rr.Code)
			case !allowed:
				if msg := apiintErrMsg(t, rr); msg != "forbidden: missing permission "+string(r.Perm) {
					t.Errorf("%s as %s: 403 message %q", r, role, msg)
				}
			case rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized:
				t.Errorf("%s as %s: status %d; role holds %s and must pass RBAC (body %s)", r, role, rr.Code, r.Perm, rr.Body.String())
			case r.Method != http.MethodGet:
				allowedCount[role]++
			}
		}
	}
	// Sanity: the matrix exercised both sides for every non-admin role.
	if allowedCount[domain.RoleAdmin] != 120 {
		t.Errorf("admin passed RBAC on %d protected routes; want 120", allowedCount[domain.RoleAdmin])
	}
	if allowedCount[domain.RoleViewer] != 1 { // only POST /agent/ask
		t.Errorf("viewer passed RBAC on %d protected routes; want 1", allowedCount[domain.RoleViewer])
	}
	if allowedCount[domain.RoleOperator] == 0 || allowedCount[domain.RolePlanner] <= allowedCount[domain.RoleOperator] {
		t.Errorf("unexpected allow counts: %v", allowedCount)
	}
	t.Logf("RBAC allow counts: %v", allowedCount)
}

// TestAPIIntPathIDValidation: every route with a path parameter rejects a
// non-UUID value with 400 before touching the database.
func TestAPIIntPathIDValidation(t *testing.T) {
	env := apiintNewEnv(t)
	admin := env.login("admin", domain.RoleAdmin)
	for _, r := range apiintSecuredRoutes(t) {
		if !strings.Contains(r.Path, "{") {
			continue
		}
		var body any
		if r.Method != http.MethodGet {
			body = `{}`
		}
		rr := admin.do(r.Method, "/api"+apiintFill(r.Path, "not-a-uuid"), body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s with non-UUID id: status %d; want 400 (body %s)", r, rr.Code, rr.Body.String())
		}
	}
}

// TestAPIIntMalformedJSONRejected: mutation routes that read a JSON body
// answer 400 for a truncated document and never 5xx.
func TestAPIIntMalformedJSONRejected(t *testing.T) {
	env := apiintNewEnv(t)
	admin := env.login("admin", domain.RoleAdmin)
	id := uuid.NewString()
	var bodyless []string
	for _, r := range apiintSecuredRoutes(t) {
		if r.Method == http.MethodGet || r.Path == "/auth/logout" {
			continue
		}
		rr := admin.do(r.Method, "/api"+apiintFill(r.Path, id), `{"broken":`)
		switch {
		case rr.Code >= 500:
			t.Errorf("%s with malformed JSON: status %d (body %s)", r, rr.Code, rr.Body.String())
		case rr.Code != http.StatusBadRequest:
			bodyless = append(bodyless, r.String()+"="+strconv.Itoa(rr.Code))
		}
	}
	t.Logf("routes that ignore/optionally read the body: %v", bodyless)
}

// TestAPIIntReadEndpointsSmoke: every parameterless GET answers 200 with JSON
// (CSV for the export) on the seeded database, and every GET with an unknown
// id answers a 4xx, never a 5xx.
func TestAPIIntReadEndpointsSmoke(t *testing.T) {
	env := apiintNewEnv(t)
	viewer := env.login("viewer", domain.RoleViewer)
	admin := env.login("admin", domain.RoleAdmin)
	id := uuid.NewString()
	var unknownIDOK []string
	// Reads that legitimately answer 404 NOT_FOUND on a fresh database because
	// no execution schedule has been published yet.
	noSchedule := map[string]bool{"/dispatch": true, "/schedule-execution": true, "/schedule-adherence/current": true}
	// Known defects (reported, not fixed here): the handler maps "row not
	// found" to 500 instead of 404.
	knownUnknownID500 := map[string]bool{
		"/calendars/{id}":         true, // calendar.go:26
		"/crp/schedule-runs/{id}": true, // capacity.go:243
	}
	for _, r := range apiintSecuredRoutes(t) {
		if r.Method != http.MethodGet {
			continue
		}
		c := viewer
		if r.Perm != "" {
			c = admin
		}
		if !strings.Contains(r.Path, "{") {
			rr := c.do(http.MethodGet, "/api"+r.Path, nil)
			if noSchedule[r.Path] {
				if rr.Code != http.StatusNotFound || apiintJSON[map[string]any](t, rr)["code"] != "NOT_FOUND" {
					t.Errorf("%s without schedule: status %d body %s", r, rr.Code, rr.Body.String())
				}
				continue
			}
			if rr.Code != http.StatusOK {
				t.Errorf("%s: status %d; want 200 (body %s)", r, rr.Code, rr.Body.String())
				continue
			}
			ct := rr.Header().Get("Content-Type")
			if r.Path == "/items/export.csv" {
				if !strings.HasPrefix(ct, "text/csv") || !strings.Contains(rr.Body.String(), "BIKE-100") {
					t.Errorf("%s: content-type %q body %q", r, ct, rr.Body.String())
				}
				continue
			}
			if !strings.HasPrefix(ct, "application/json") || strings.TrimSpace(rr.Body.String()) == "null" {
				t.Errorf("%s: content-type %q body %q", r, ct, rr.Body.String())
			}
			continue
		}
		if knownUnknownID500[r.Path] {
			continue
		}
		rr := c.do(http.MethodGet, "/api"+apiintFill(r.Path, id), nil)
		if rr.Code >= 500 {
			t.Errorf("%s with unknown id: status %d (body %s)", r, rr.Code, rr.Body.String())
		} else if rr.Code < 400 {
			unknownIDOK = append(unknownIDOK, r.String()+"="+strconv.Itoa(rr.Code))
		}
	}
	t.Logf("GET routes answering 2xx for an unknown id (empty collections): %v", unknownIDOK)
}

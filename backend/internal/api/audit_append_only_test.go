package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/go-chi/chi/v5/middleware"
)

func TestAuditLogIsAppendOnly(t *testing.T) {
	env := apiintNewEnv(t)
	entry := &domain.AuditLogEntry{Username: "kept", Action: "POST /api/items", Resource: "items", HTTPStatus: 201}
	if err := env.Repos.Audit.Insert(env.Ctx, entry); err != nil {
		t.Fatalf("insert must keep working: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE audit_log SET username='changed'`,
		`DELETE FROM audit_log`,
		`TRUNCATE audit_log`,
	} {
		_, err := env.DB.ExecContext(env.Ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err = %v, want append-only rejection", stmt, err)
		}
	}
	var n int
	env.scalar(&n, `SELECT count(*) FROM audit_log WHERE username='kept'`)
	if n != 1 {
		t.Fatalf("kept rows = %d", n)
	}
}

func TestAuditMiddlewareRecordsPanickedRequest(t *testing.T) {
	env := apiintNewEnv(t)
	h := middleware.Recoverer(auditMiddleware(env.Svc.Audit)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/panic-test", strings.NewReader(`{"a":1}`)))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d", rr.Code)
	}
	var n int
	env.scalar(&n, `SELECT count(*) FROM audit_log WHERE action='POST /api/panic-test' AND http_status=500 AND payload->>'a'='1'`)
	if n != 1 {
		t.Fatalf("panicked request audit rows = %d, want 1", n)
	}
}

func TestAuditMiddlewareRecordsNormalRequestOnce(t *testing.T) {
	env := apiintNewEnv(t)
	h := auditMiddleware(env.Svc.Audit)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/normal-test", nil))
	var n int
	env.scalar(&n, `SELECT count(*) FROM audit_log WHERE action='POST /api/normal-test' AND http_status=409`)
	if n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
}

func TestAuditRecordFailureIsLogged(t *testing.T) {
	env := apiintNewEnv(t)
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)

	// Invalid jsonb makes the INSERT fail.
	recordAudit(env.Ctx, env.Svc.Audit, &domain.AuditLogEntry{
		Username: "lost", Action: "POST /api/x", Resource: "x", HTTPStatus: 200,
		Payload: json.RawMessage(`{not json`),
	})
	if got := buf.String(); !strings.Contains(got, "audit record failed") || !strings.Contains(got, `"lost"`) {
		t.Fatalf("log output = %q", got)
	}
}

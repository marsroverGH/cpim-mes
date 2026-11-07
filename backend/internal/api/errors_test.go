package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgconn"
)

func callWriteError(t *testing.T, code int, err error) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	rec.Header().Set(requestIDHeader, "host/abc-000001")
	writeError(rec, code, err)
	var body map[string]any
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("response is not JSON: %v (%q)", decodeErr, rec.Body.String())
	}
	return rec, body
}

func assertNoLeak(t *testing.T, rec *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(rec.Body.String(), s) {
			t.Fatalf("response leaks %q: %s", s, rec.Body.String())
		}
	}
}

func TestWriteErrorHidesInternalErrorDetails(t *testing.T) {
	err := fmt.Errorf("load work order: dial tcp 10.0.0.5:5432: connection refused")
	rec, body := callWriteError(t, http.StatusInternalServerError, err)

	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	if body["error"] != "internal server error" || body["requestId"] != "host/abc-000001" {
		t.Fatalf("unexpected body: %v", body)
	}
	assertNoLeak(t, rec, "10.0.0.5", "dial tcp", "work order")
}

func TestWriteErrorHidesBuiltInDatabaseErrors(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "items_code_key"`,
		TableName:      "items",
		ConstraintName: "items_code_key",
	}
	rec, body := callWriteError(t, http.StatusBadRequest, fmt.Errorf("insert item: %w", pgErr))

	if rec.Code != 400 || body["error"] != "a record with the same key already exists" {
		t.Fatalf("unexpected response %d %v", rec.Code, body)
	}
	assertNoLeak(t, rec, "items_code_key", "SQLSTATE", "insert item")
}

func TestWriteErrorHidesDatabaseErrorsOn500(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "42P01", Message: `relation "secret_table" does not exist`}
	rec, body := callWriteError(t, http.StatusInternalServerError, pgErr)

	if body["error"] != "internal server error" {
		t.Fatalf("unexpected body: %v", body)
	}
	assertNoLeak(t, rec, "secret_table", "42P01")
}

func TestWriteErrorKeepsRaisedBusinessRuleMessage(t *testing.T) {
	cases := []struct {
		code       string
		handler    int
		wantStatus int
	}{
		{"P0001", 400, 400},
		{"23514", 400, 400},
		{"P0001", 500, 422},
		{"23514", 500, 422},
		{"23503", 500, 409},
		{"42501", 500, 403},
	}
	for _, tc := range cases {
		pgErr := &pgconn.PgError{Code: tc.code, Message: "work order is not RELEASED"}
		rec, body := callWriteError(t, tc.handler, fmt.Errorf("release: %w", pgErr))
		if rec.Code != tc.wantStatus || body["error"] != "work order is not RELEASED" {
			t.Fatalf("code %s handler %d: unexpected response %d %v", tc.code, tc.handler, rec.Code, body)
		}
		assertNoLeak(t, rec, "SQLSTATE", "release:")
	}
}

func TestWriteErrorMapsConcurrencyConflictsTo409(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		for _, handler := range []int{400, 500} {
			pgErr := &pgconn.PgError{Code: code, Message: "could not serialize access due to read/write dependencies among transactions"}
			rec, body := callWriteError(t, handler, fmt.Errorf("approve: %w", pgErr))
			if rec.Code != 409 || body["error"] != "concurrent update conflict; please retry" || body["requestId"] == nil {
				t.Fatalf("code %s handler %d: unexpected response %d %v", code, handler, rec.Code, body)
			}
			assertNoLeak(t, rec, "serialize", "approve:")
		}
	}
}

func TestWriteErrorTreatsNamedConstraintAsBuiltIn(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23514", Message: `new row violates check constraint "qty_positive"`, ConstraintName: "qty_positive", TableName: "lots"}
	rec, body := callWriteError(t, http.StatusBadRequest, pgErr)

	if body["error"] != "the request violates a data integrity rule" {
		t.Fatalf("unexpected body: %v", body)
	}
	assertNoLeak(t, rec, "qty_positive", "lots")
}

func TestWriteErrorKeepsClientErrorMessages(t *testing.T) {
	_, body := callWriteError(t, http.StatusBadRequest, errors.New("quantity must be positive"))
	if body["error"] != "quantity must be positive" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestWriteErrorAppErrorDoesNotSerializeCause(t *testing.T) {
	rec, body := callWriteError(t, http.StatusBadRequest, domain.NewInternal(errors.New("pq: password authentication failed")))

	if rec.Code != 500 || body["message"] != "internal server error" || body["code"] != "INTERNAL" {
		t.Fatalf("unexpected response %d %v", rec.Code, body)
	}
	assertNoLeak(t, rec, "password")

	rec, body = callWriteError(t, http.StatusInternalServerError, domain.NewConflict("execution already approved"))
	if rec.Code != 409 || body["message"] != "execution already approved" {
		t.Fatalf("unexpected response %d %v", rec.Code, body)
	}
}

func TestExposeRequestIDSetsResponseHeader(t *testing.T) {
	h := assignRequestID(exposeRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusInternalServerError, errors.New("boom"))
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	id := rec.Header().Get(requestIDHeader)
	if id == "" || !strings.Contains(rec.Body.String(), id) {
		t.Fatalf("request id %q missing from header or body: %s", id, rec.Body.String())
	}
	assertNoLeak(t, rec, "boom")
}

func TestAssignRequestIDIgnoresClientHeader(t *testing.T) {
	var seen string
	h := assignRequestID(exposeRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.GetReqID(r.Context())
	})))
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(requestIDHeader, "forged\nlog-line")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get(requestIDHeader)
		if got == "" || got != seen || strings.Contains(got, "forged") {
			t.Fatalf("request id %q (context %q); want a server-generated id", got, seen)
		}
		ids[got] = true
	}
	if len(ids) != 3 {
		t.Fatalf("request ids are not unique: %v", ids)
	}
}

func TestWriteErrorMapsBuiltInKeyViolationsTo409WhenHandlerPassed500(t *testing.T) {
	for code, want := range map[string]string{
		"23505": "a record with the same key already exists",
		"23503": "a referenced record does not exist or is still in use",
	} {
		pgErr := &pgconn.PgError{Code: code, Message: "constraint detail", TableName: "work_centers", ConstraintName: "secret_fkey"}
		rec, body := callWriteError(t, http.StatusInternalServerError, fmt.Errorf("delete: %w", pgErr))
		if rec.Code != http.StatusConflict || body["error"] != want {
			t.Fatalf("%s: unexpected response %d %v", code, rec.Code, body)
		}
		assertNoLeak(t, rec, "secret_fkey", "work_centers", code)
	}
}

func TestWriteErrorMapsNoRowsTo404(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		rec, body := callWriteError(t, code, fmt.Errorf("load calendar: %w", sql.ErrNoRows))
		if rec.Code != http.StatusNotFound || body["error"] != "resource not found" {
			t.Fatalf("handler code %d: unexpected response %d %v", code, rec.Code, body)
		}
		assertNoLeak(t, rec, "sql:", "load calendar")
	}
}

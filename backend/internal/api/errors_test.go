package api

import (
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
	for _, code := range []string{"P0001", "23514", "23503", "42501"} {
		pgErr := &pgconn.PgError{Code: code, Message: "work order is not RELEASED"}
		for _, status := range []int{400, 500} {
			rec, body := callWriteError(t, status, fmt.Errorf("release: %w", pgErr))
			if rec.Code != status || body["error"] != "work order is not RELEASED" {
				t.Fatalf("code %s status %d: unexpected response %d %v", code, status, rec.Code, body)
			}
			assertNoLeak(t, rec, "SQLSTATE", "release:")
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
	h := middleware.RequestID(exposeRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

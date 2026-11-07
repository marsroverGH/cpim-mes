package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgconn"
)

const requestIDHeader = "X-Request-Id"

// assignRequestID gives every request a server-generated ID. Unlike chi's
// middleware.RequestID it never adopts a client-supplied X-Request-Id, so a
// client cannot forge or collide IDs in the server log or in error bodies.
// The ID is stored under middleware.RequestIDKey so middleware.GetReqID (and
// chi's request logger) keep working.
func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, newRequestID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; keep requests
		// flowing with a time-based fallback rather than an empty ID.
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// exposeRequestID copies the chi request ID into the response header so that
// clients can quote it and writeError can tie a sanitized response to the
// full error in the server log. Must run after middleware.RequestID.
func exposeRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set(requestIDHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}

// writeError writes an error response without leaking internal details.
//
//   - *domain.AppError: its public Code/Message/Details (Cause is never serialized).
//   - Business rule rejections raised by PL/pgSQL (RAISE EXCEPTION): the
//     developer-authored message only, without SQLSTATE or Go wrapping text.
//     If the handler passed a 5xx, the status becomes 422 (403 for 42501,
//     409 for 23503) because the request itself was rejected.
//   - Serialization failures / deadlocks (40001, 40P01): 409 with a retry hint.
//   - Built-in unique / foreign key violations (23505, 23503) where the
//     handler passed a 5xx: 409 with a generic message (duplicate key, or a
//     record that is missing or still in use).
//   - sql.ErrNoRows anywhere in the chain: 404 with a generic message.
//   - Any other database error, and any 5xx: a generic message plus the
//     request ID; the full error goes to the server log.
//   - Other 4xx errors (request decoding, parameter validation): err.Error().
func writeError(w http.ResponseWriter, code int, err error) {
	if err == nil {
		err = errors.New("unknown error")
	}

	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		status := appErr.Status
		if status == 0 {
			status = code
		}
		if status >= 500 {
			logAPIError(w, status, err)
		}
		writeJSON(w, status, appErr)
		return
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "40001" || pgErr.Code == "40P01" {
			// Transient concurrency conflict that survived the service-level
			// retries: the client may safely retry the request.
			writeJSON(w, http.StatusConflict, sanitizedBody(w, http.StatusConflict, "concurrent update conflict; please retry", err))
			return
		}
		if isRaisedBusinessError(pgErr) {
			status := code
			if status >= 500 {
				// Handlers often pass 500 for any service error; a business
				// rule rejected by the database is a client-visible outcome.
				status = businessErrorStatus(pgErr)
			}
			writeJSON(w, status, map[string]string{"error": pgErr.Message})
			return
		}
		if code >= 500 && (pgErr.Code == "23505" || pgErr.Code == "23503") {
			// The request conflicts with existing data (duplicate key, or a
			// referenced record that is missing / still in use).
			writeJSON(w, http.StatusConflict, map[string]string{"error": publicDBMessage(http.StatusConflict, pgErr)})
			return
		}
		writeJSON(w, code, sanitizedBody(w, code, publicDBMessage(code, pgErr), err))
		return
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Never expose "sql: no rows in result set"; the handler may also have
		// passed 500 for a lookup by an unknown id.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
		return
	}

	if code >= 500 {
		writeJSON(w, code, sanitizedBody(w, code, "internal server error", err))
		return
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func sanitizedBody(w http.ResponseWriter, code int, msg string, err error) map[string]string {
	body := map[string]string{"error": msg}
	if id := logAPIError(w, code, err); id != "" {
		body["requestId"] = id
	}
	return body
}

func logAPIError(w http.ResponseWriter, code int, err error) string {
	id := w.Header().Get(requestIDHeader)
	log.Printf("api error status=%d request_id=%s: %v", code, id, err)
	return id
}

// isRaisedBusinessError reports whether the error came from an explicit
// RAISE EXCEPTION in our migrations rather than from PostgreSQL itself.
// Built-in constraint errors always name the table/constraint/column;
// RAISE without those options leaves them empty.
func isRaisedBusinessError(e *pgconn.PgError) bool {
	if e.TableName != "" || e.ConstraintName != "" || e.ColumnName != "" || e.SchemaName != "" || e.DataTypeName != "" {
		return false
	}
	switch {
	case strings.HasPrefix(e.Code, "P0"): // raise_exception and friends
		return true
	case e.Code == "23514", e.Code == "23503", e.Code == "42501": // ERRCODEs used by migrations
		return e.Message != ""
	}
	return false
}

// businessErrorStatus maps a RAISEd business rule rejection to the HTTP status
// used when the handler did not choose a client error itself.
func businessErrorStatus(e *pgconn.PgError) int {
	switch e.Code {
	case "42501":
		return http.StatusForbidden
	case "23503":
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}

func publicDBMessage(code int, e *pgconn.PgError) string {
	if code >= 500 {
		return "internal server error"
	}
	switch {
	case e.Code == "23505":
		return "a record with the same key already exists"
	case e.Code == "23503":
		return "a referenced record does not exist or is still in use"
	case e.Code == "23502":
		return "a required value is missing"
	case strings.HasPrefix(e.Code, "23"):
		return "the request violates a data integrity rule"
	case strings.HasPrefix(e.Code, "22"):
		return "invalid input value"
	}
	return "the request could not be processed"
}

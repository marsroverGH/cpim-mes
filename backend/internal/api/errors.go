package api

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgconn"
)

const requestIDHeader = "X-Request-Id"

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
		if isRaisedBusinessError(pgErr) {
			if code >= 500 {
				logAPIError(w, code, err)
			}
			writeJSON(w, code, map[string]string{"error": pgErr.Message})
			return
		}
		writeJSON(w, code, sanitizedBody(w, code, publicDBMessage(code, pgErr), err))
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

func publicDBMessage(code int, e *pgconn.PgError) string {
	if code >= 500 {
		switch e.Code {
		case "40001", "40P01":
			return "concurrent update conflict; please retry"
		}
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
	case e.Code == "40001", e.Code == "40P01":
		return "concurrent update conflict; please retry"
	}
	return "the request could not be processed"
}

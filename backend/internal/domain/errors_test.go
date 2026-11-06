package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestErrorBuilders(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")
	details := map[string]string{"field": "required"}

	tests := []struct {
		name        string
		err         *AppError
		wantCode    ErrorCode
		wantStatus  int
		wantMessage string
		wantCause   error
		wantDetails any
	}{
		{"bad request", NewBadRequest("bad input", cause), ErrCodeBadRequest, 400, "bad input", cause, nil},
		{"bad request without cause", NewBadRequest("bad input", nil), ErrCodeBadRequest, 400, "bad input", nil, nil},
		{"not found", NewNotFound("item"), ErrCodeNotFound, 404, "item not found", nil, nil},
		{"unauthorized", NewUnauthorized("login required"), ErrCodeUnauthorized, 401, "login required", nil, nil},
		{"forbidden", NewForbidden("no permission"), ErrCodeForbidden, 403, "no permission", nil, nil},
		{"conflict", NewConflict("duplicate"), ErrCodeConflict, 409, "duplicate", nil, nil},
		{"validation", NewValidation(details), ErrCodeValidation, 400, "validation failed", nil, details},
		{"internal", NewInternal(cause), ErrCodeInternal, 500, "internal server error", cause, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.err.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", tc.err.Code, tc.wantCode)
			}
			if tc.err.Status != tc.wantStatus {
				t.Errorf("Status = %d, want %d", tc.err.Status, tc.wantStatus)
			}
			if tc.err.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", tc.err.Message, tc.wantMessage)
			}
			if tc.err.Cause != tc.wantCause {
				t.Errorf("Cause = %v, want %v", tc.err.Cause, tc.wantCause)
			}
			if fmt.Sprint(tc.err.Details) != fmt.Sprint(tc.wantDetails) {
				t.Errorf("Details = %v, want %v", tc.err.Details, tc.wantDetails)
			}
		})
	}
}

func TestAppErrorErrorString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  *AppError
		want string
	}{
		{"without cause", NewNotFound("lot"), "NOT_FOUND: lot not found"},
		{"with cause", NewInternal(errors.New("db down")), "INTERNAL: internal server error: db down"},
		{"bad request with nil cause", NewBadRequest("x", nil), "BAD_REQUEST: x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppErrorUnwrapChain(t *testing.T) {
	t.Parallel()
	appErr := NewInternal(fmt.Errorf("query: %w", io.ErrUnexpectedEOF))
	wrapped := fmt.Errorf("handler: %w", appErr)

	if !errors.Is(wrapped, io.ErrUnexpectedEOF) {
		t.Fatal("errors.Is must reach the root cause through AppError.Unwrap")
	}
	var got *AppError
	if !errors.As(wrapped, &got) || got != appErr {
		t.Fatalf("errors.As did not recover the AppError: %v", got)
	}
	if NewNotFound("x").Unwrap() != nil {
		t.Fatal("Unwrap of an error without cause must be nil")
	}
}

func TestAppErrorJSONHidesInternals(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(NewInternal(errors.New("password=secret")))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "secret") || strings.Contains(s, "500") || strings.Contains(s, "details") {
		t.Fatalf("JSON must not expose cause, status or empty details: %s", s)
	}
	if s != `{"code":"INTERNAL","message":"internal server error"}` {
		t.Fatalf("unexpected JSON: %s", s)
	}

	b, err = json.Marshal(NewValidation([]string{"qty must be positive"}))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"code":"VALIDATION","message":"validation failed","details":["qty must be positive"]}` {
		t.Fatalf("unexpected validation JSON: %s", b)
	}
}

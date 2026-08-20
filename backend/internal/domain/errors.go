package domain

import "fmt"

// ErrorCode — 機械処理可能なエラーコード
type ErrorCode string

const (
	ErrCodeBadRequest   ErrorCode = "BAD_REQUEST"
	ErrCodeNotFound     ErrorCode = "NOT_FOUND"
	ErrCodeUnauthorized ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "FORBIDDEN"
	ErrCodeConflict     ErrorCode = "CONFLICT"
	ErrCodeValidation   ErrorCode = "VALIDATION"
	ErrCodeInternal     ErrorCode = "INTERNAL"
)

// AppError — アプリケーション全体で使用する統一エラー型
type AppError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Status  int       `json:"-"`
	Details any       `json:"details,omitempty"`
	Cause   error     `json:"-"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Cause }

// Builders ----------------------------------------------------------

func NewBadRequest(msg string, cause error) *AppError {
	return &AppError{Code: ErrCodeBadRequest, Message: msg, Status: 400, Cause: cause}
}
func NewNotFound(resource string) *AppError {
	return &AppError{Code: ErrCodeNotFound, Message: resource + " not found", Status: 404}
}
func NewUnauthorized(msg string) *AppError {
	return &AppError{Code: ErrCodeUnauthorized, Message: msg, Status: 401}
}
func NewForbidden(msg string) *AppError {
	return &AppError{Code: ErrCodeForbidden, Message: msg, Status: 403}
}
func NewConflict(msg string) *AppError {
	return &AppError{Code: ErrCodeConflict, Message: msg, Status: 409}
}
func NewValidation(details any) *AppError {
	return &AppError{Code: ErrCodeValidation, Message: "validation failed", Status: 400, Details: details}
}
func NewInternal(cause error) *AppError {
	return &AppError{Code: ErrCodeInternal, Message: "internal server error", Status: 500, Cause: cause}
}

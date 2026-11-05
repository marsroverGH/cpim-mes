package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const maxUserAdministrationRequestBytes = 16 << 10

type createUserRequest struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     domain.Role `json:"role"`
}

type updateUserRoleRequest struct {
	Role domain.Role `json:"role"`
}

type updateUserActiveRequest struct {
	IsActive *bool `json:"isActive"`
}

type resetUserPasswordRequest struct {
	NewPassword string `json:"newPassword"`
}

type changeOwnPasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func decodeUserAdministrationJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUserAdministrationRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func (h *server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.UserAdmin.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *server) createUser(w http.ResponseWriter, r *http.Request) {
	var body createUserRequest
	if err := decodeUserAdministrationJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, err := h.s.UserAdmin.Create(r.Context(), service.CreateManagedUserInput{
		Username: body.Username,
		Password: body.Password,
		Role:     body.Role,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	recordUserSecurityAudit(h.s.Audit, r, "USER_CREATED", http.StatusCreated, u.ID, u.Username, map[string]any{
		"targetRole":     u.Role,
		"targetIsActive": u.IsActive,
	})
	writeJSON(w, http.StatusCreated, u)
}

func (h *server) updateUserRole(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body updateUserRoleRequest
	if err := decodeUserAdministrationJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, err := h.s.UserAdmin.SetRole(r.Context(), id, body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	recordUserSecurityAudit(h.s.Audit, r, "ROLE_CHANGED", http.StatusOK, u.ID, u.Username, map[string]any{
		"targetRole":     u.Role,
		"targetIsActive": u.IsActive,
	})
	writeJSON(w, http.StatusOK, u)
}

func (h *server) updateUserActive(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body updateUserActiveRequest
	if err := decodeUserAdministrationJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.IsActive == nil {
		writeError(w, http.StatusBadRequest, domain.NewValidation(map[string]string{"isActive": "isActive is required"}))
		return
	}
	u, err := h.s.UserAdmin.SetActive(r.Context(), id, *body.IsActive)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	event := "USER_DISABLED"
	if u.IsActive {
		event = "USER_ENABLED"
	}
	recordUserSecurityAudit(h.s.Audit, r, event, http.StatusOK, u.ID, u.Username, map[string]any{
		"targetRole":     u.Role,
		"targetIsActive": u.IsActive,
	})
	writeJSON(w, http.StatusOK, u)
}

func (h *server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body resetUserPasswordRequest
	if err := decodeUserAdministrationJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, err := h.s.UserAdmin.ResetPassword(r.Context(), id, body.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	recordUserSecurityAudit(h.s.Audit, r, "PASSWORD_RESET", http.StatusNoContent, u.ID, u.Username, map[string]any{
		"targetRole":     u.Role,
		"targetIsActive": u.IsActive,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, strErr("not authenticated"))
		return
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, strErr("invalid session subject"))
		return
	}
	var body changeOwnPasswordRequest
	if err := decodeUserAdministrationJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.s.UserAdmin.ChangeOwnPassword(r.Context(), userID, body.CurrentPassword, body.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	recordUserSecurityAudit(h.s.Audit, r, "PASSWORD_CHANGED", http.StatusNoContent, userID, claims.Username, map[string]any{
		"selfService": true,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func recordUserSecurityAudit(audit *service.AuditService, r *http.Request, event string, status int, targetID uuid.UUID, targetUsername string, details map[string]any) {
	if audit == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	actor, role := "anonymous", ""
	if claims != nil {
		actor = strings.TrimSpace(claims.Username)
		role = strings.TrimSpace(string(claims.Role))
	}
	details["targetUsername"] = strings.TrimSpace(targetUsername)
	if requestID := middleware.GetReqID(r.Context()); requestID != "" {
		details["requestId"] = requestID
	}
	entry := &domain.AuditLogEntry{
		Username:   actor,
		UserRole:   role,
		Action:     event,
		Resource:   "user-administration",
		ResourceID: targetID.String(),
		HTTPStatus: status,
		IPAddress:  clientIP(r),
		Payload:    repository.MarshalPayload(details),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = audit.Record(ctx, entry)
}

package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

func TestValidateManagedPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"minimum accepted", strings.Repeat("a", ManagedPasswordMinBytes), false},
		{"unicode byte length accepted", "管理者-password-123", false},
		{"too short", "short-password", true},
		{"bcrypt maximum accepted", strings.Repeat("x", ManagedPasswordMaxBytes), false},
		{"bcrypt maximum exceeded", strings.Repeat("x", ManagedPasswordMaxBytes+1), true},
		{"newline rejected", strings.Repeat("x", ManagedPasswordMinBytes) + "\n", true},
		{"nul rejected", strings.Repeat("x", ManagedPasswordMinBytes) + "\x00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateManagedPassword(tt.password)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateManagedPassword() error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateManagedRole(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RolePlanner, domain.RoleOperator, domain.RoleViewer} {
		if err := ValidateManagedRole(role); err != nil {
			t.Fatalf("valid role %q rejected: %v", role, err)
		}
	}
	if err := ValidateManagedRole(domain.Role("superuser")); err == nil {
		t.Fatal("unknown role must be rejected")
	}
}

func TestNormalizeManagedUsername(t *testing.T) {
	got, err := normalizeManagedUsername("  alice.admin  ")
	if err != nil || got != "alice.admin" {
		t.Fatalf("normalizeManagedUsername=%q err=%v", got, err)
	}
	if _, err := normalizeManagedUsername("\n"); err == nil {
		t.Fatal("blank username must be rejected")
	}
	if _, err := normalizeManagedUsername(strings.Repeat("a", ManagedUsernameMaxBytes+1)); err == nil {
		t.Fatal("oversize username must be rejected")
	}
}

func TestMapUserRepositoryError(t *testing.T) {
	err := mapUserRepositoryError(errors.New("database exploded"))
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != domain.ErrCodeInternal {
		t.Fatalf("unexpected error mapping: %#v", err)
	}
}

package service

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	ManagedPasswordMinBytes = 15
	ManagedPasswordMaxBytes = 72
	ManagedUsernameMaxBytes = 128
)

type UserAdminService struct {
	repo *repository.UserRepo
}

func NewUserAdminService(repo *repository.UserRepo) *UserAdminService {
	return &UserAdminService{repo: repo}
}

type CreateManagedUserInput struct {
	Username string
	Password string
	Role     domain.Role
}

func (s *UserAdminService) List(ctx context.Context) ([]domain.User, error) {
	return s.repo.List(ctx)
}

func (s *UserAdminService) Create(ctx context.Context, in CreateManagedUserInput) (*domain.User, error) {
	username, err := normalizeManagedUsername(in.Username)
	if err != nil {
		return nil, err
	}
	if err := ValidateManagedPassword(in.Password); err != nil {
		return nil, err
	}
	if err := ValidateManagedRole(in.Role); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, domain.NewInternal(err)
	}
	u, err := s.repo.CreateManaged(ctx, username, string(hash), in.Role)
	if err != nil {
		return nil, mapUserRepositoryError(err)
	}
	return u, nil
}

func (s *UserAdminService) SetRole(ctx context.Context, id uuid.UUID, role domain.Role) (*domain.User, error) {
	if err := ValidateManagedRole(role); err != nil {
		return nil, err
	}
	u, err := s.repo.UpdateRole(ctx, id, role)
	if err != nil {
		return nil, mapUserRepositoryError(err)
	}
	return u, nil
}

func (s *UserAdminService) SetActive(ctx context.Context, id uuid.UUID, active bool) (*domain.User, error) {
	u, err := s.repo.UpdateActive(ctx, id, active)
	if err != nil {
		return nil, mapUserRepositoryError(err)
	}
	return u, nil
}

func (s *UserAdminService) ResetPassword(ctx context.Context, id uuid.UUID, newPassword string) (*domain.User, error) {
	if err := ValidateManagedPassword(newPassword); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, domain.NewInternal(err)
	}
	u, err := s.repo.ResetPasswordHash(ctx, id, string(hash))
	if err != nil {
		return nil, mapUserRepositoryError(err)
	}
	return u, nil
}

func (s *UserAdminService) ChangeOwnPassword(ctx context.Context, id uuid.UUID, currentPassword, newPassword string) error {
	if currentPassword == "" {
		return domain.NewValidation(map[string]string{"currentPassword": "currentPassword is required"})
	}
	if err := ValidateManagedPassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return domain.NewInternal(err)
	}
	if err := s.repo.ChangeOwnPassword(ctx, id, []byte(currentPassword), string(hash)); err != nil {
		if errors.Is(err, repository.ErrCurrentPasswordMismatch) {
			return domain.NewValidation(map[string]string{"currentPassword": "current password is incorrect"})
		}
		return mapUserRepositoryError(err)
	}
	return nil
}

func ValidateManagedPassword(password string) error {
	n := len([]byte(password))
	if n < ManagedPasswordMinBytes {
		return domain.NewValidation(map[string]string{"password": "password must be at least 15 bytes"})
	}
	if n > ManagedPasswordMaxBytes {
		return domain.NewValidation(map[string]string{"password": "password must be at most 72 bytes for bcrypt"})
	}
	for _, r := range password {
		if r == '\x00' || r == '\n' || r == '\r' {
			return domain.NewValidation(map[string]string{"password": "password must be a single line without NUL bytes"})
		}
	}
	return nil
}

func ValidateManagedRole(role domain.Role) error {
	switch role {
	case domain.RoleAdmin, domain.RolePlanner, domain.RoleOperator, domain.RoleViewer:
		return nil
	default:
		return domain.NewValidation(map[string]string{"role": "role must be admin, planner, operator, or viewer"})
	}
}

func normalizeManagedUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", domain.NewValidation(map[string]string{"username": "username is required"})
	}
	if len([]byte(username)) > ManagedUsernameMaxBytes {
		return "", domain.NewValidation(map[string]string{"username": "username must be at most 128 bytes"})
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return "", domain.NewValidation(map[string]string{"username": "username must not contain control characters"})
		}
	}
	return username, nil
}

func mapUserRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrUserNotFound):
		return domain.NewNotFound("user")
	case errors.Is(err, repository.ErrUsernameExists):
		return domain.NewConflict("username already exists")
	case errors.Is(err, repository.ErrLastActiveAdmin):
		return domain.NewConflict("cannot disable or demote the last active administrator")
	default:
		return domain.NewInternal(err)
	}
}

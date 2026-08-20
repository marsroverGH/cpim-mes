package repository

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
)

type UserRepo struct{ db *sqlx.DB }

func (r *UserRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var u domain.User
	err := r.db.GetContext(ctx, &u, `SELECT * FROM users WHERE id=$1 LIMIT 1`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	var u domain.User
	err := r.db.GetContext(ctx, &u,
		`SELECT * FROM users WHERE username=$1 AND is_active=true LIMIT 1`, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) List(ctx context.Context) ([]domain.User, error) {
	var rows []domain.User
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM users ORDER BY username`)
	return rows, err
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM users`)
	return n, err
}

func (r *UserRepo) Create(ctx context.Context, username, plainPassword string, role domain.Role) (*domain.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := domain.User{
		ID:           uuid.New(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		IsActive:     true,
	}
	_, err = r.db.NamedExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, is_active)
		VALUES (:id, :username, :password_hash, :role, :is_active)
		ON CONFLICT (username) DO NOTHING
	`, &u)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SeedDefaultUsers — users テーブルが空なら admin/planner/operator/viewer を作成
func (r *UserRepo) SeedDefaultUsers(ctx context.Context) error {
	n, err := r.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	defaults := []struct {
		Username string
		Password string
		Role     domain.Role
	}{
		{"admin", "admin123", domain.RoleAdmin},
		{"planner", "planner123", domain.RolePlanner},
		{"operator", "operator123", domain.RoleOperator},
		{"viewer", "viewer123", domain.RoleViewer},
	}
	for _, d := range defaults {
		if _, err := r.Create(ctx, d.Username, d.Password, d.Role); err != nil {
			return err
		}
	}
	log.Println("[auth] seeded default users (admin/planner/operator/viewer with default passwords)")
	return nil
}

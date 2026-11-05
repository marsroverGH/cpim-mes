package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const (
	bootstrapAdminAdvisoryLockID int64 = 410003
	userAdminAdvisoryLockID      int64 = 410007
)

var (
	ErrUserNotFound            = errors.New("user not found")
	ErrUsernameExists          = errors.New("username already exists")
	ErrLastActiveAdmin         = errors.New("cannot remove the last active administrator")
	ErrCurrentPasswordMismatch = errors.New("current password is incorrect")
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

// Create remains for internal/backward compatibility and delegates to the
// managed creation path so username conflicts can never return a phantom user.
func (r *UserRepo) Create(ctx context.Context, username, plainPassword string, role domain.Role) (*domain.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return r.CreateManaged(ctx, username, string(hash), role)
}

// CreateManaged inserts one active user using a caller-provided bcrypt hash.
// ON CONFLICT deliberately returns ErrUsernameExists rather than pretending a
// newly generated UUID was persisted.
func (r *UserRepo) CreateManaged(ctx context.Context, username, passwordHash string, role domain.Role) (*domain.User, error) {
	var u domain.User
	err := r.db.GetContext(ctx, &u, `
		INSERT INTO users (id, username, password_hash, role, is_active)
		VALUES ($1, $2, $3, $4, true)
		ON CONFLICT (username) DO NOTHING
		RETURNING *
	`, uuid.New(), username, passwordHash, role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUsernameExists
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateRole serializes administrator role/active mutations with a PostgreSQL
// advisory transaction lock. This closes the race where two concurrent
// requests both observe two admins and each remove one, leaving zero.
func (r *UserRepo) UpdateRole(ctx context.Context, id uuid.UUID, role domain.Role) (*domain.User, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockUserAdministration(ctx, tx); err != nil {
		return nil, err
	}
	u, err := userForUpdate(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if u.IsActive && u.Role == domain.RoleAdmin && role != domain.RoleAdmin {
		if err := ensureAnotherActiveAdmin(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	if err := tx.GetContext(ctx, u, `UPDATE users SET role=$2 WHERE id=$1 RETURNING *`, id, role); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateActive uses the same serialized boundary as UpdateRole so disable and
// demotion requests cannot race each other around the last-admin invariant.
func (r *UserRepo) UpdateActive(ctx context.Context, id uuid.UUID, active bool) (*domain.User, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockUserAdministration(ctx, tx); err != nil {
		return nil, err
	}
	u, err := userForUpdate(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if u.IsActive && !active && u.Role == domain.RoleAdmin {
		if err := ensureAnotherActiveAdmin(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	if err := tx.GetContext(ctx, u, `UPDATE users SET is_active=$2 WHERE id=$1 RETURNING *`, id, active); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

func (r *UserRepo) ResetPasswordHash(ctx context.Context, id uuid.UUID, passwordHash string) (*domain.User, error) {
	var u domain.User
	err := r.db.GetContext(ctx, &u, `UPDATE users SET password_hash=$2 WHERE id=$1 RETURNING *`, id, passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ChangeOwnPassword verifies and updates the password while holding the user
// row lock, preventing an administrator reset from racing between verification
// and update. Inactive users are rejected even if a stale request reaches here.
func (r *UserRepo) ChangeOwnPassword(ctx context.Context, id uuid.UUID, currentPassword []byte, newPasswordHash string) error {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	u, err := userForUpdate(ctx, tx, id)
	if err != nil {
		return err
	}
	if !u.IsActive {
		return ErrUserNotFound
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), currentPassword); err != nil {
		return ErrCurrentPasswordMismatch
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, newPasswordHash); err != nil {
		return err
	}
	return tx.Commit()
}

func lockUserAdministration(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdminAdvisoryLockID); err != nil {
		return fmt.Errorf("acquire user administration lock: %w", err)
	}
	return nil
}

func userForUpdate(ctx context.Context, tx *sqlx.Tx, id uuid.UUID) (*domain.User, error) {
	var u domain.User
	if err := tx.GetContext(ctx, &u, `SELECT * FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

func ensureAnotherActiveAdmin(ctx context.Context, tx *sqlx.Tx, excluding uuid.UUID) error {
	var n int
	if err := tx.GetContext(ctx, &n, `SELECT COUNT(*) FROM users WHERE role='admin' AND is_active=true AND id<>$1`, excluding); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastActiveAdmin
	}
	return nil
}

// BootstrapAdmin creates the one initial administrator only when the users
// table is empty. A transaction-scoped PostgreSQL advisory lock serializes
// concurrent application instances so two startup attempts cannot both create
// initial accounts. No fixed username or password is embedded in the binary.
func (r *UserRepo) BootstrapAdmin(ctx context.Context, username string, plainPassword []byte) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdminAdvisoryLockID); err != nil {
		return false, fmt.Errorf("acquire bootstrap admin lock: %w", err)
	}

	var count int
	if err := tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM users`); err != nil {
		return false, fmt.Errorf("count users for bootstrap: %w", err)
	}
	if count > 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	hash, err := bcrypt.GenerateFromPassword(plainPassword, bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	user := domain.User{
		ID:           uuid.New(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         domain.RoleAdmin,
		IsActive:     true,
	}
	if _, err := tx.NamedExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, is_active)
		VALUES (:id, :username, :password_hash, :role, :is_active)
	`, &user); err != nil {
		return false, fmt.Errorf("insert bootstrap admin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit bootstrap admin: %w", err)
	}
	return true, nil
}

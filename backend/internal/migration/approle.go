package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jmoiron/sqlx"
)

var appRoleNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// EnsureAppRole creates or updates the least-privilege role the backend connects
// as, and resets its table privileges. It runs after every migration step rather
// than as a numbered migration, because a restore with --no-acl loses grants and
// new tables must be covered without per-migration GRANT statements.
//
// The role is not an owner, so it cannot disable the triggers that protect
// evidence tables. It may only add to audit_log and read schema_migrations.
func EnsureAppRole(ctx context.Context, db *sqlx.DB, role, password string) error {
	if !appRoleNameRE.MatchString(role) {
		return fmt.Errorf("invalid application role name %q", role)
	}
	if password == "" {
		return errors.New("application role password must not be empty")
	}
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var currentUser string
	if err := tx.GetContext(ctx, &currentUser, `SELECT current_user`); err != nil {
		return fmt.Errorf("read current user: %w", err)
	}
	if currentUser == role {
		return fmt.Errorf("application role %q must differ from the migration role", role)
	}

	var exists bool
	if err := tx.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)`, role); err != nil {
		return fmt.Errorf("inspect role: %w", err)
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `CREATE ROLE `+role); err != nil {
			return fmt.Errorf("create application role: %w", err)
		}
	}

	// Utility statements cannot take bind parameters; format() quotes the password.
	var alter string
	if err := tx.GetContext(ctx, &alter,
		`SELECT format('ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOREPLICATION PASSWORD %L', $1::text, $2::text)`,
		role, password); err != nil {
		return fmt.Errorf("format role statement: %w", err)
	}
	if _, err := tx.ExecContext(ctx, alter); err != nil {
		return errors.New("configure application role failed")
	}

	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`REVOKE ALL ON ALL TABLES IN SCHEMA public FROM ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ` + role,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + role,
		`REVOKE UPDATE, DELETE ON audit_log FROM ` + role,
		`REVOKE INSERT, UPDATE, DELETE ON schema_migrations FROM ` + role,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("grant privileges: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ConnectionCanDisableTriggers reports whether the connected role is a superuser
// or owns audit_log. Either one can disable the triggers that protect evidence.
func ConnectionCanDisableTriggers(ctx context.Context, db *sqlx.DB) (bool, error) {
	var can bool
	err := db.GetContext(ctx, &can, `
SELECT pg_has_role(current_user, relowner, 'USAGE')
  FROM pg_class WHERE oid = 'public.audit_log'::regclass`)
	if err != nil {
		return false, fmt.Errorf("inspect connection role: %w", err)
	}
	return can, nil
}

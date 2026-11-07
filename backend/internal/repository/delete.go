package repository

import (
	"context"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// deleteOne runs a single-row DELETE by id and reports a missing row as
// NOT_FOUND, so that DELETE of an unknown id is a 404 rather than a 204.
func deleteOne(ctx context.Context, db *sqlx.DB, query string, id uuid.UUID, what string) error {
	res, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewNotFound(what)
	}
	return nil
}

package service

import (
	"context"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/testdb"
	"github.com/jmoiron/sqlx"
)

// dbFixture is the shared entry point for database integration tests in this
// package. Each call gets a private, fully migrated database (see
// internal/testdb); tests are skipped when CPIM_TEST_DATABASE_URL is unset.
type dbFixture struct {
	DB    *sqlx.DB
	Repos *repository.Repositories
	Svc   *Services
	Ctx   context.Context
}

func newDBFixture(t *testing.T) *dbFixture {
	t.Helper()
	db := testdb.New(t)
	repos := repository.NewRepositories(db)
	return &dbFixture{
		DB:    db,
		Repos: repos,
		Svc:   NewServices(db, repos, ServicesConfig{JWTSecret: "test-only-jwt-secret-0123456789abcdef"}),
		Ctx:   context.Background(),
	}
}

// user creates an active user with the given role.
func (f *dbFixture) user(t *testing.T, username string, role domain.Role) *domain.User {
	t.Helper()
	u, err := f.Repos.Users.Create(f.Ctx, username, "Test-Password-123!", role)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

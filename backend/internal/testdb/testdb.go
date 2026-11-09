// Package testdb gives tests an isolated, fully migrated PostgreSQL database.
//
// Set CPIM_TEST_DATABASE_URL to a superuser connection URL of a disposable
// PostgreSQL 16 server, e.g.
//
//	postgres://cpim:testpw@127.0.0.1:55499/postgres?sslmode=disable
//
// When it is unset, New skips the test, so `go test ./...` still works
// without a database. The first caller (across all test binaries) migrates a
// template database once per migration set; every test then gets its own
// copy via CREATE DATABASE … TEMPLATE, which takes milliseconds and is
// dropped when the test ends.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/migration"
	"github.com/cpim-mes/backend/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// EnvVar names the admin connection URL used by the integration tests.
const EnvVar = "CPIM_TEST_DATABASE_URL"

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// appRole is the role test connections use, so that a query needing more than
// the production application role fails in tests too.
const (
	appRole     = "cpim_app"
	appPassword = "cpim-app-test-only"
)

func appURL(t testing.TB, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(appRole, appPassword)
	return u.String()
}

// Admin returns a privileged connection to the same database as db, for test
// setup that the application role must not be able to do.
func Admin(t testing.TB, db *sqlx.DB) *sqlx.DB {
	t.Helper()
	var name string
	if err := db.Get(&name, `SELECT current_database()`); err != nil {
		t.Fatal(err)
	}
	admin := mustOpen(t, withDatabase(t, os.Getenv(EnvVar), name))
	t.Cleanup(func() { _ = admin.Close() })
	return admin
}

// New returns a connection, as the application role, to a fresh, migrated
// database private to t.
func New(t testing.TB) *sqlx.DB {
	t.Helper()
	adminURL := os.Getenv(EnvVar)
	if adminURL == "" {
		t.Skipf("%s is not set; skipping database integration test", EnvVar)
	}
	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(adminURL) })
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	admin := mustOpen(t, adminURL)
	defer admin.Close()

	name := "cpim_t_" + randomHex(8)
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		_, err = admin.Exec(fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, templateName))
		if err == nil || !strings.Contains(err.Error(), "is being accessed by other users") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}

	db := mustOpen(t, appURL(t, withDatabase(t, adminURL, name)))
	t.Cleanup(func() {
		_ = db.Close()
		cleanup := mustOpen(t, adminURL)
		defer cleanup.Close()
		if _, err := cleanup.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})
	return db
}

func ensureTemplate(adminURL string) (string, error) {
	sum, err := migrationsDigest()
	if err != nil {
		return "", err
	}
	// The prefix changes when the template contents change for a reason other
	// than the migrations (v2: includes the application role grants).
	name := "cpim_tmpl2_" + sum[:16]

	admin, err := sqlx.Open("pgx", adminURL)
	if err != nil {
		return "", err
	}
	defer admin.Close()
	ctx := context.Background()
	conn, err := admin.Connx(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	// Serialize template creation across concurrently running test binaries.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('cpim-mes:testdb-template', 0))`); err != nil {
		return "", err
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended('cpim-mes:testdb-template', 0))`) //nolint:errcheck

	var exists bool
	if err := conn.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name); err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	building := name + "_building"
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, building)); err != nil {
		return "", err
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE %s`, building)); err != nil {
		return "", err
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + building
	db, err := sqlx.Open("pgx", u.String())
	if err != nil {
		return "", err
	}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, migErr := migration.New(db, "testdb", log.New(io.Discard, "", 0)).Migrate(mctx)
	if migErr == nil {
		migErr = migration.EnsureAppRole(mctx, db, appRole, appPassword)
	}
	_ = db.Close()
	if migErr != nil {
		return "", fmt.Errorf("migrate template: %w", migErr)
	}
	// Publish atomically: the template only becomes visible once complete.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, building, name)); err != nil {
		return "", err
	}
	return name, nil
}

func migrationsDigest() (string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func mustOpen(t testing.TB, dsn string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	return db
}

func withDatabase(t testing.TB, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

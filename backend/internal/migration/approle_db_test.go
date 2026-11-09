package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestEnsureAppRoleGivesLeastPrivilegeAndVerifyGatesStartup(t *testing.T) {
	adminURL := os.Getenv("CPIM_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("CPIM_TEST_DATABASE_URL is not set; skipping database integration test")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	role := "app_t_" + hex.EncodeToString(b)
	const password = "it's-a-test" // the quote checks password quoting

	// Registered first so that it runs after the database has been dropped.
	t.Cleanup(func() {
		cleanup, err := sqlx.Open("pgx", adminURL)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP ROLE IF EXISTS ` + role); err != nil {
			t.Logf("drop role: %v", err)
		}
	})

	db := createEmptyDatabase(t, adminURL)
	manager := New(db, "test", log.New(io.Discard, "", 0))

	if _, err := manager.Verify(ctx); err == nil || !strings.Contains(err.Error(), "server migrate") {
		t.Fatalf("Verify before migrate: %v", err)
	}
	if _, err := manager.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Verify(ctx); err != nil {
		t.Fatalf("Verify after migrate: %v", err)
	}

	var adminUser string
	if err := db.Get(&adminUser, `SELECT current_user`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAppRole(ctx, db, adminUser, "x"); err == nil {
		t.Fatal("the migration role must not become the application role")
	}
	for _, bad := range []string{"Robert", "a;drop", ""} {
		if err := EnsureAppRole(ctx, db, bad, "x"); err == nil {
			t.Fatalf("role name %q accepted", bad)
		}
	}
	if err := EnsureAppRole(ctx, db, role, password); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := db.Get(&name, `SELECT current_database()`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	u.User = url.UserPassword(role, password)
	app, err := sqlx.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if can, err := ConnectionCanDisableTriggers(ctx, app); err != nil || can {
		t.Fatalf("application role: can=%v err=%v", can, err)
	}
	if can, err := ConnectionCanDisableTriggers(ctx, db); err != nil || !can {
		t.Fatalf("owner: can=%v err=%v", can, err)
	}
	if _, err := New(app, "test", log.New(io.Discard, "", 0)).Verify(ctx); err != nil {
		t.Fatalf("Verify as application role: %v", err)
	}

	const insert = `INSERT INTO audit_log (username, action, resource, http_status) VALUES ('u','a','r',200)`
	if _, err := app.Exec(insert); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var seen int
	if err := app.Get(&seen, `SELECT count(*) FROM audit_log`); err != nil || seen != 1 {
		t.Fatalf("select: %d %v", seen, err)
	}
	deny := func(stmt, want string) {
		t.Helper()
		if _, err := app.Exec(stmt); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", stmt, err)
		}
	}
	deny(`UPDATE audit_log SET username='x'`, "permission denied")
	deny(`DELETE FROM audit_log`, "permission denied")
	deny(`TRUNCATE audit_log`, "permission denied")
	deny(`DELETE FROM schema_migrations`, "permission denied")
	deny(`INSERT INTO schema_migrations(version,name,checksum) VALUES (999,'x','x')`, "permission denied")
	deny(`ALTER TABLE audit_log DISABLE TRIGGER ALL`, "must be owner")
	deny(`DROP TABLE audit_log`, "must be owner")
	deny(`CREATE TABLE evil(id int)`, "permission denied")
	deny(`COPY audit_log TO PROGRAM 'id'`, "permission denied")

	// Re-running restores lost grants and covers tables added since.
	if _, err := db.Exec(`CREATE TABLE later_table(id int); REVOKE ALL ON items FROM ` + role); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAppRole(ctx, db, role, password); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	for _, q := range []string{`SELECT count(*) FROM later_table`, `SELECT count(*) FROM items`} {
		var n int
		if err := app.Get(&n, q); err != nil {
			t.Fatalf("%s after re-run: %v", q, err)
		}
	}
	deny(`UPDATE audit_log SET username='x'`, "permission denied")

	// A database behind this binary stops the server.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=(SELECT max(version) FROM schema_migrations)`); err != nil {
		t.Fatal(err)
	}
	if _, err := New(app, "test", log.New(io.Discard, "", 0)).Verify(ctx); err == nil || !strings.Contains(err.Error(), "server migrate") {
		t.Fatalf("Verify on an older database: %v", err)
	}
}

package repository_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func TestBootstrapAdminOnlyOnEmptyTableAndIdempotent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	users := f.repos.Users

	if n, err := users.Count(f.ctx); err != nil || n != 0 {
		t.Fatalf("fresh database must have no users (n=%d, err=%v)", n, err)
	}

	created, err := users.BootstrapAdmin(f.ctx, "root", []byte("Initial-Pass-1!"))
	if err != nil || !created {
		t.Fatalf("first bootstrap: created=%v err=%v", created, err)
	}
	u, err := users.FindByUsername(f.ctx, "root")
	if err != nil || u == nil {
		t.Fatalf("bootstrap admin not found: %v", err)
	}
	if u.Role != domain.RoleAdmin || !u.IsActive {
		t.Fatalf("bootstrap user must be an active admin, got role=%s active=%v", u.Role, u.IsActive)
	}
	if u.PasswordHash == "Initial-Pass-1!" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("Initial-Pass-1!")) != nil {
		t.Fatal("bootstrap password must be stored as a bcrypt hash of the given password")
	}

	// Second call must be a no-op, even with a different username.
	created, err = users.BootstrapAdmin(f.ctx, "other", []byte("x"))
	if err != nil || created {
		t.Fatalf("second bootstrap: created=%v err=%v", created, err)
	}
	if n, _ := users.Count(f.ctx); n != 1 {
		t.Fatalf("bootstrap must not add users when table is non-empty, count=%d", n)
	}
}

func TestBootstrapAdminSkipsWhenAnyUserExists(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.user(t, domain.RoleViewer)
	created, err := f.repos.Users.BootstrapAdmin(f.ctx, "root", []byte("pw"))
	if err != nil || created {
		t.Fatalf("bootstrap with existing viewer: created=%v err=%v", created, err)
	}
}

func TestBootstrapAdminConcurrentCreatesExactlyOne(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const n = 6
	var wg sync.WaitGroup
	results := make([]bool, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = f.repos.Users.BootstrapAdmin(f.ctx, "admin-"+uuid.NewString()[:6], []byte("pw"))
		}(i)
	}
	wg.Wait()
	createdCount := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("bootstrap %d: %v", i, errs[i])
		}
		if results[i] {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("advisory lock must serialize bootstrap; %d instances created an admin", createdCount)
	}
	if c, _ := f.repos.Users.Count(f.ctx); c != 1 {
		t.Fatalf("users count = %d, want 1", c)
	}
}

func TestCreateUserHashesPasswordAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	users := f.repos.Users

	u, err := users.Create(f.ctx, "alice", "S3cret-pass", domain.RolePlanner)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == uuid.Nil || u.Username != "alice" || u.Role != domain.RolePlanner || !u.IsActive || u.CreatedAt.IsZero() {
		t.Fatalf("unexpected created user: %+v", u)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("S3cret-pass")) != nil {
		t.Fatal("stored hash does not verify the original password")
	}

	_, err = users.Create(f.ctx, "alice", "another", domain.RoleViewer)
	if !errors.Is(err, repository.ErrUsernameExists) {
		t.Fatalf("duplicate username: got %v, want ErrUsernameExists", err)
	}
	// The conflicting attempt must not have altered the original row.
	got, _ := users.FindByID(f.ctx, u.ID)
	if got == nil || got.Role != domain.RolePlanner || got.PasswordHash != u.PasswordHash {
		t.Fatalf("original user mutated by conflicting insert: %+v", got)
	}

	if _, err := users.CreateManaged(f.ctx, "bob", "h", domain.Role("superuser")); err == nil {
		t.Fatal("DB CHECK must reject unknown roles")
	}
	// bcrypt rejects passwords longer than 72 bytes.
	if _, err := users.Create(f.ctx, "carol", strings.Repeat("x", 73), domain.RoleViewer); err == nil {
		t.Fatal("expected bcrypt error for >72 byte password")
	}
}

func TestFindUsers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	users := f.repos.Users
	b, _ := users.CreateManaged(f.ctx, "b-user", "h", domain.RoleViewer)
	a, _ := users.CreateManaged(f.ctx, "a-user", "h", domain.RoleOperator)

	if u, err := users.FindByID(f.ctx, uuid.New()); u != nil || err != nil {
		t.Fatalf("missing id must return (nil,nil), got %v %v", u, err)
	}
	if u, err := users.FindByUsername(f.ctx, "nobody"); u != nil || err != nil {
		t.Fatalf("missing username must return (nil,nil), got %v %v", u, err)
	}
	if u, err := users.FindByID(f.ctx, a.ID); err != nil || u.Username != "a-user" {
		t.Fatalf("FindByID: %v %v", u, err)
	}

	list, err := users.List(f.ctx)
	if err != nil || len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("List must be ordered by username: %+v %v", list, err)
	}

	// Inactive users are invisible to login lookup but still visible by ID.
	if _, err := users.UpdateActive(f.ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if u, err := users.FindByUsername(f.ctx, "b-user"); u != nil || err != nil {
		t.Fatalf("inactive user must not be found by username: %v %v", u, err)
	}
	if u, _ := users.FindByID(f.ctx, b.ID); u == nil || u.IsActive {
		t.Fatalf("inactive user must still be returned by id with is_active=false: %+v", u)
	}
}

func TestLastActiveAdminInvariant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	users := f.repos.Users
	admin1 := f.user(t, domain.RoleAdmin)

	tests := []struct {
		name string
		op   func() error
	}{
		{"demote sole admin", func() error { _, err := users.UpdateRole(f.ctx, admin1.ID, domain.RoleViewer); return err }},
		{"disable sole admin", func() error { _, err := users.UpdateActive(f.ctx, admin1.ID, false); return err }},
	}
	for _, tc := range tests {
		if err := tc.op(); !errors.Is(err, repository.ErrLastActiveAdmin) {
			t.Fatalf("%s: got %v, want ErrLastActiveAdmin", tc.name, err)
		}
	}

	// An inactive admin does not count as "another active admin".
	admin2 := f.user(t, domain.RoleAdmin)
	if _, err := users.UpdateActive(f.ctx, admin2.ID, false); err != nil {
		t.Fatalf("disabling one of two admins: %v", err)
	}
	if _, err := users.UpdateRole(f.ctx, admin1.ID, domain.RolePlanner); !errors.Is(err, repository.ErrLastActiveAdmin) {
		t.Fatalf("inactive admin must not satisfy invariant: %v", err)
	}

	// Re-activate the second admin; now the first can be demoted.
	if _, err := users.UpdateActive(f.ctx, admin2.ID, true); err != nil {
		t.Fatal(err)
	}
	u, err := users.UpdateRole(f.ctx, admin1.ID, domain.RolePlanner)
	if err != nil || u.Role != domain.RolePlanner {
		t.Fatalf("demote with another admin present: %+v %v", u, err)
	}

	// Mutating non-admins never triggers the invariant.
	if _, err := users.UpdateActive(f.ctx, admin1.ID, false); err != nil {
		t.Fatalf("disable planner: %v", err)
	}
	// Promoting / keeping admin role on the last admin is allowed.
	if _, err := users.UpdateRole(f.ctx, admin2.ID, domain.RoleAdmin); err != nil {
		t.Fatalf("no-op role update on last admin: %v", err)
	}

	// Invalid role is rejected by DB CHECK and leaves state unchanged.
	if _, err := users.UpdateRole(f.ctx, admin1.ID, domain.Role("root")); err == nil {
		t.Fatal("expected CHECK violation for unknown role")
	}

	for _, op := range []func() error{
		func() error { _, err := users.UpdateRole(f.ctx, uuid.New(), domain.RoleViewer); return err },
		func() error { _, err := users.UpdateActive(f.ctx, uuid.New(), true); return err },
	} {
		if err := op(); !errors.Is(err, repository.ErrUserNotFound) {
			t.Fatalf("unknown user: got %v, want ErrUserNotFound", err)
		}
	}
}

func TestConcurrentAdminRemovalLeavesOneAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.user(t, domain.RoleAdmin)
	b := f.user(t, domain.RoleAdmin)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = f.repos.Users.UpdateRole(f.ctx, a.ID, domain.RoleViewer) }()
	go func() { defer wg.Done(); _, errs[1] = f.repos.Users.UpdateActive(f.ctx, b.ID, false) }()
	wg.Wait()

	failed := 0
	for _, err := range errs {
		if err != nil {
			if !errors.Is(err, repository.ErrLastActiveAdmin) {
				t.Fatalf("unexpected error: %v", err)
			}
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("exactly one concurrent removal must fail, got %d failures", failed)
	}
	var active int
	if err := f.db.Get(&active, `SELECT COUNT(*) FROM users WHERE role='admin' AND is_active`); err != nil || active != 1 {
		t.Fatalf("active admins = %d (err %v), want 1", active, err)
	}
}

func TestPasswordChangeAndReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	users := f.repos.Users
	u, err := users.Create(f.ctx, "dave", "old-password", domain.RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	newHash, _ := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)

	if err := users.ChangeOwnPassword(f.ctx, u.ID, []byte("wrong"), string(newHash)); !errors.Is(err, repository.ErrCurrentPasswordMismatch) {
		t.Fatalf("wrong current password: got %v", err)
	}
	if got, _ := users.FindByID(f.ctx, u.ID); got.PasswordHash != u.PasswordHash {
		t.Fatal("failed verification must not change the hash")
	}
	if err := users.ChangeOwnPassword(f.ctx, u.ID, []byte("old-password"), string(newHash)); err != nil {
		t.Fatal(err)
	}
	if got, _ := users.FindByID(f.ctx, u.ID); got.PasswordHash != string(newHash) {
		t.Fatal("hash not updated")
	}
	if err := users.ChangeOwnPassword(f.ctx, uuid.New(), []byte("x"), "h"); !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	// Inactive users cannot change their password even with the right one.
	if _, err := users.UpdateActive(f.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := users.ChangeOwnPassword(f.ctx, u.ID, []byte("new-password"), "h2"); !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("inactive user change: got %v, want ErrUserNotFound", err)
	}

	// Admin reset works regardless of active flag and returns the updated row.
	reset, err := users.ResetPasswordHash(f.ctx, u.ID, "reset-hash")
	if err != nil || reset.PasswordHash != "reset-hash" || reset.ID != u.ID {
		t.Fatalf("reset: %+v %v", reset, err)
	}
	if _, err := users.ResetPasswordHash(f.ctx, uuid.New(), "h"); !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("reset unknown: %v", err)
	}
}

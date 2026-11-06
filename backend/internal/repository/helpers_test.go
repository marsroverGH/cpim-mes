package repository_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// fixture bundles a private migrated database with its repositories.
// Migrations seed sample master data (items, calendars, lots …), so tests
// always create their own uniquely coded rows and never assume empty tables.
type fixture struct {
	ctx   context.Context
	db    *sqlx.DB
	repos *repository.Repositories
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testdb.New(t)
	return &fixture{ctx: context.Background(), db: db, repos: repository.NewRepositories(db)}
}

func uniq(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8]
}

func (f *fixture) item(t *testing.T, typ domain.ItemType) *domain.Item {
	t.Helper()
	it := &domain.Item{Code: uniq("T"), Name: "test item", Type: typ, UoM: "EA", LotSize: 1, StandardCost: 10}
	if err := f.repos.Items.Create(f.ctx, it); err != nil {
		t.Fatalf("create item: %v", err)
	}
	return it
}

func (f *fixture) user(t *testing.T, role domain.Role) *domain.User {
	t.Helper()
	// The hash is opaque to the DB; tests that verify passwords use Create.
	u, err := f.repos.Users.CreateManaged(f.ctx, uniq("u"), "opaque-test-hash", role)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func (f *fixture) workCenter(t *testing.T, machines, workers int) *domain.WorkCenter {
	t.Helper()
	wc := &domain.WorkCenter{
		Code: uniq("WC"), Name: "wc", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1,
		MachineCount: machines, WorkerCount: workers,
	}
	if err := f.repos.WorkCenters.Create(f.ctx, wc); err != nil {
		t.Fatalf("create work center: %v", err)
	}
	return wc
}

// receiveLot posts a fully lot-allocated RECEIPT the same way the ledger
// service does (header + lot + movement in one transaction) so that all
// deferred ledger constraints are satisfied at COMMIT.
func (f *fixture) receiveLot(t *testing.T, itemID uuid.UUID, qty float64) (lotID, txnID uuid.UUID) {
	t.Helper()
	lotID, txnID = uuid.New(), uuid.New()
	ref := uniq("TEST-RCPT")
	tx := f.db.MustBeginTx(f.ctx, nil)
	defer func() { _ = tx.Rollback() }()
	tx.MustExecContext(f.ctx, `INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc) VALUES ($1,$2,$3,'RECEIPT',$4)`, txnID, itemID, qty, ref)
	tx.MustExecContext(f.ctx, `INSERT INTO lots (id,item_id,lot_no,quantity,source_doc) VALUES ($1,$2,$3,$4,$5)`, lotID, itemID, uniq("LOT"), qty, ref)
	tx.MustExecContext(f.ctx, `INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc) VALUES ($1,$2,$3,'RECEIPT',$4)`, lotID, txnID, qty, ref)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit receipt: %v", err)
	}
	return lotID, txnID
}

// issueLot posts a lot-allocated ISSUE of qty (positive number) at occurredAt.
func (f *fixture) issueLot(t *testing.T, itemID, lotID uuid.UUID, qty float64, ref string, occurredAt time.Time) {
	t.Helper()
	txnID := uuid.New()
	tx := f.db.MustBeginTx(f.ctx, nil)
	defer func() { _ = tx.Rollback() }()
	tx.MustExecContext(f.ctx, `INSERT INTO inventory_txns (id,item_id,quantity,txn_type,ref_doc,occurred_at) VALUES ($1,$2,$3,'ISSUE',$4,$5)`, txnID, itemID, -qty, ref, occurredAt)
	tx.MustExecContext(f.ctx, `INSERT INTO lot_movements (lot_id,txn_id,quantity,movement_type,ref_doc,occurred_at) VALUES ($1,$2,$3,'ISSUE',$4,$5)`, lotID, txnID, -qty, ref, occurredAt)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit issue: %v", err)
	}
}

func wantErrContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got %v", substr, err)
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func approxEqual(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

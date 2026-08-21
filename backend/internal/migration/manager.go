package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	embedded "github.com/cpim-mes/backend/migrations"
	"github.com/jmoiron/sqlx"
)

const migrationLockSQL = `SELECT pg_advisory_lock(hashtextextended('cpim-mes:schema-migrations', 0))`
const migrationUnlockSQL = `SELECT pg_advisory_unlock(hashtextextended('cpim-mes:schema-migrations', 0))`

var migrationNameRE = regexp.MustCompile(`^(\d{4})_([a-zA-Z0-9][a-zA-Z0-9_-]*)\.sql$`)
var topLevelTxnRE = regexp.MustCompile(`(?m)^[\t ]*(BEGIN|COMMIT);[\t ]*(?:--[^\n]*)?\r?$`)

type Migration struct {
	Version  int
	Name     string
	Filename string
	SQL      string
	Checksum string
}

type AppliedMigration struct {
	Version  int       `db:"version"`
	Name     string    `db:"name"`
	Checksum string    `db:"checksum"`
	Applied  time.Time `db:"applied_at"`
}

type Result struct {
	LatestVersion int
	AppliedNow    []int
	Baselined     []int
}

// Manager owns startup schema evolution. No HTTP server should be started until
// Migrate returns successfully.
type Manager struct {
	db          *sqlx.DB
	installedBy string
	logger      *log.Logger
}

func New(db *sqlx.DB, installedBy string, logger *log.Logger) *Manager {
	if strings.TrimSpace(installedBy) == "" {
		installedBy = "backend-startup"
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{db: db, installedBy: installedBy, logger: logger}
}

func (m *Manager) Migrate(ctx context.Context) (Result, error) {
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		return Result{}, err
	}
	if len(migrations) == 0 {
		return Result{}, errors.New("no embedded SQL migrations found")
	}

	conn, err := m.db.Connx(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, migrationLockSQL); err != nil {
		return Result{}, fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, migrationUnlockSQL); err != nil {
			m.logger.Printf("[warn] release migration advisory lock: %v", err)
		}
	}()

	_, err = ensureMigrationTable(ctx, conn)
	if err != nil {
		return Result{}, err
	}

	result := Result{LatestVersion: migrations[len(migrations)-1].Version}
	applied, err := readApplied(ctx, conn)
	if err != nil {
		return Result{}, err
	}

	if len(applied) == 0 {
		baselined, err := bootstrapLegacyDatabase(ctx, conn, migrations, m.installedBy, m.logger)
		if err != nil {
			return Result{}, err
		}
		result.Baselined = baselined
		if len(baselined) > 0 {
			applied, err = readApplied(ctx, conn)
			if err != nil {
				return Result{}, err
			}
		}
	}

	if err := validateHistory(migrations, applied); err != nil {
		return Result{}, err
	}

	appliedByVersion := make(map[int]AppliedMigration, len(applied))
	for _, a := range applied {
		appliedByVersion[a.Version] = a
	}

	for _, mig := range migrations {
		if _, ok := appliedByVersion[mig.Version]; ok {
			continue
		}
		started := time.Now()
		m.logger.Printf("[migration] applying %04d %s", mig.Version, mig.Name)
		if err := applyOne(ctx, conn, mig, m.installedBy, started); err != nil {
			return result, fmt.Errorf("migration %04d_%s failed: %w", mig.Version, mig.Name, err)
		}
		result.AppliedNow = append(result.AppliedNow, mig.Version)
		m.logger.Printf("[migration] applied %04d %s (%s)", mig.Version, mig.Name, time.Since(started).Round(time.Millisecond))
	}

	if err := verifyLatestState(ctx, conn, migrations); err != nil {
		return result, err
	}
	m.logger.Printf("[migration] schema ready at version %04d", result.LatestVersion)
	return result, nil
}

func loadEmbeddedMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(embedded.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migs := make([]Migration, 0, len(entries))
	seen := map[int]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := migrationNameRE.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q; expected NNNN_name.sql", entry.Name())
		}
		v, _ := strconv.Atoi(match[1])
		if prev, ok := seen[v]; ok {
			return nil, fmt.Errorf("duplicate migration version %04d: %s and %s", v, prev, entry.Name())
		}
		seen[v] = entry.Name()
		body, err := fs.ReadFile(embedded.FS, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %s: %w", entry.Name(), err)
		}
		hash := sha256.Sum256(body)
		migs = append(migs, Migration{
			Version: v, Name: match[2], Filename: entry.Name(), SQL: string(body), Checksum: hex.EncodeToString(hash[:]),
		})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	for i, mig := range migs {
		expected := i + 1
		if mig.Version != expected {
			return nil, fmt.Errorf("embedded migration sequence has a gap: expected %04d, found %04d", expected, mig.Version)
		}
	}
	return migs, nil
}

func ensureMigrationTable(ctx context.Context, conn *sqlx.Conn) (bool, error) {
	var existed bool
	if err := conn.GetContext(ctx, &existed, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`); err != nil {
		return false, fmt.Errorf("inspect schema_migrations: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version       integer PRIMARY KEY,
  name          text NOT NULL,
  checksum      char(64) NOT NULL,
  applied_at    timestamptz NOT NULL DEFAULT now(),
  execution_ms  bigint NOT NULL DEFAULT 0,
  installed_by  text NOT NULL DEFAULT 'backend-startup',
  baseline      boolean NOT NULL DEFAULT false
)`); err != nil {
		return false, fmt.Errorf("create schema_migrations: %w", err)
	}
	return !existed, nil
}

func readApplied(ctx context.Context, conn *sqlx.Conn) ([]AppliedMigration, error) {
	var rows []AppliedMigration
	if err := conn.SelectContext(ctx, &rows, `SELECT version, name, checksum, applied_at FROM schema_migrations ORDER BY version`); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return rows, nil
}

func validateHistory(migrations []Migration, applied []AppliedMigration) error {
	known := make(map[int]Migration, len(migrations))
	for _, mig := range migrations {
		known[mig.Version] = mig
	}
	appliedSet := map[int]bool{}
	for _, row := range applied {
		mig, ok := known[row.Version]
		if !ok {
			return fmt.Errorf("database migration version %04d is newer/unknown to this application; refusing to start an older binary", row.Version)
		}
		if row.Name != mig.Name {
			return fmt.Errorf("migration %04d name mismatch: database=%q binary=%q", row.Version, row.Name, mig.Name)
		}
		if strings.TrimSpace(row.Checksum) != mig.Checksum {
			return fmt.Errorf("migration %04d checksum mismatch: an already-applied migration file was modified", row.Version)
		}
		appliedSet[row.Version] = true
	}

	// Historical gaps are not allowed, except replay-safe migration 0014. A gap
	// usually means a migration was manually skipped and later DDL was applied.
	maxApplied := 0
	for v := range appliedSet {
		if v > maxApplied {
			maxApplied = v
		}
	}
	for v := 1; v <= maxApplied; v++ {
		if appliedSet[v] || isReplaySafeLegacyMigration(v) {
			continue
		}
		return fmt.Errorf("schema_migrations has a gap at version %04d while later migrations are recorded; repair migration history before startup", v)
	}
	return nil
}

func applyOne(ctx context.Context, conn *sqlx.Conn, mig Migration, installedBy string, started time.Time) error {
	tx, err := conn.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// Migrations 0018+ historically contained explicit top-level BEGIN/COMMIT
	// because they were executed by postgres init scripts. Strip only standalone
	// transaction wrapper lines and let this manager own the atomic transaction.
	script := strings.TrimSpace(topLevelTxnRE.ReplaceAllString(mig.SQL, ""))
	if script != "" {
		if _, err := tx.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("execute SQL: %w", err)
		}
	}
	executionMS := time.Since(started).Milliseconds()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations(version, name, checksum, applied_at, execution_ms, installed_by, baseline)
VALUES ($1,$2,$3,now(),$4,$5,false)`, mig.Version, mig.Name, mig.Checksum, executionMS, installedBy); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func verifyLatestState(ctx context.Context, conn *sqlx.Conn, migrations []Migration) error {
	var count int
	if err := conn.GetContext(ctx, &count, `SELECT count(*) FROM schema_migrations`); err != nil {
		return fmt.Errorf("verify migration count: %w", err)
	}
	if count != len(migrations) {
		return fmt.Errorf("schema migration verification failed: database records=%d embedded=%d", count, len(migrations))
	}
	return nil
}

// legacyCheck describes an observable schema fingerprint produced by an old
// /docker-entrypoint-initdb.d deployment. Migration 0014 is intentionally not
// fingerprinted because it is data-only and safe to replay (INSERT ... ON CONFLICT DO NOTHING).
type legacyCheck struct {
	Version int
	Query   string
}

var legacyChecks = []legacyCheck{
	{1, `SELECT to_regclass('public.items') IS NOT NULL AND to_regclass('public.mps_entries') IS NOT NULL`},
	{2, `SELECT to_regclass('public.work_centers') IS NOT NULL AND to_regclass('public.routing_operations') IS NOT NULL`},
	{3, `SELECT to_regclass('public.users') IS NOT NULL`},
	{4, `SELECT to_regclass('public.lots') IS NOT NULL AND to_regclass('public.audit_log') IS NOT NULL`},
	{5, `SELECT to_regclass('public.cycle_counts') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_centers' AND column_name='overhead_rate_per_minute')`},
	{6, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='items' AND column_name='lot_size_method')`},
	{7, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_orders' AND column_name='produced_lot_id') AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='purchase_orders' AND column_name='received_lot_id')`},
	{8, `SELECT to_regclass('public.work_calendars') IS NOT NULL AND to_regclass('public.calendar_exceptions') IS NOT NULL`},
	{9, `SELECT to_regclass('public.quality_inspections') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='lots' AND column_name='quality_status')`},
	{10, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='items' AND column_name='low_level_code')`},
	{11, `SELECT to_regclass('public.wo_operations') IS NOT NULL AND to_regclass('public.operation_logs') IS NOT NULL`},
	{12, `SELECT to_regclass('public.sop_plans') IS NOT NULL AND to_regclass('public.rccp_profiles') IS NOT NULL`},
	{13, `SELECT to_regclass('public.engineering_changes') IS NOT NULL AND to_regclass('public.eco_components') IS NOT NULL`},
	{15, `SELECT to_regclass('public.work_order_completions') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_orders' AND column_name='reported_progress_qty')`},
	{16, `SELECT to_regclass('public.ux_inventory_txns_wo_reserve_item') IS NOT NULL`},
	{17, `SELECT to_regclass('public.work_order_bom_snapshots') IS NOT NULL AND to_regclass('public.work_order_bom_snapshot_lines') IS NOT NULL`},
	{18, `SELECT to_regclass('public.v_inventory_lot_reconciliation') IS NOT NULL`},
	{19, `SELECT to_regprocedure('public.assert_bom_acyclic()') IS NOT NULL`},
	{20, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_order_completions' AND column_name='receipt_txn_id')`},
	{21, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='wo_operations' AND column_name='active_started_at')`},
	{22, `SELECT to_regclass('public.purchase_receipts') IS NOT NULL`},
	{23, `SELECT to_regclass('public.eco_status_history') IS NOT NULL`},
	{24, `SELECT to_regclass('public.quality_status_history') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='quality_inspections' AND column_name='inspector_user_id')`},
	{25, `SELECT to_regclass('public.forecast_runs') IS NOT NULL AND to_regclass('public.forecast_values') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='mps_entries' AND column_name='source_forecast_run_id')`},
	{26, `SELECT to_regclass('public.sop_product_mix_versions') IS NOT NULL AND to_regclass('public.sop_disaggregation_runs') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='mps_entries' AND column_name='source_sop_disaggregation_run_id') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conname='mps_planning_provenance_chk')`},
	{27, `SELECT to_regclass('public.supplier_ncrs') IS NOT NULL AND to_regclass('public.supplier_ncr_dispositions') IS NOT NULL AND to_regclass('public.supplier_quality_profiles') IS NOT NULL AND to_regclass('public.v_supplier_quality_scorecard') IS NOT NULL`},
	{28, `SELECT to_regclass('public.idx_inventory_txns_abc_issue_period') IS NOT NULL`},
	{29, `SELECT to_regclass('public.crp_schedule_runs') IS NOT NULL AND to_regclass('public.crp_schedule_segments') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_centers' AND column_name='shift_start_minute')`},
	{30, `SELECT to_regclass('public.detailed_schedule_runs') IS NOT NULL AND to_regclass('public.routing_operation_alternatives') IS NOT NULL AND to_regclass('public.work_center_setup_matrix') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='work_centers' AND column_name='machine_count')`},
	{31, `SELECT to_regclass('public.sales_orders') IS NOT NULL AND to_regclass('public.sales_order_lines') IS NOT NULL AND to_regclass('public.sales_order_shipments') IS NOT NULL AND to_regclass('public.customers') IS NOT NULL AND to_regclass('public.v_sales_order_open_demand') IS NOT NULL`},
	{32, `SELECT to_regclass('public.order_promise_runs') IS NOT NULL AND to_regclass('public.order_promise_line_results') IS NOT NULL AND to_regclass('public.order_promise_confirmations') IS NOT NULL AND to_regclass('public.order_promise_acceptances') IS NOT NULL`},
	{33, `SELECT to_regclass('public.backorder_runs') IS NOT NULL AND to_regclass('public.backorder_run_lines') IS NOT NULL AND to_regclass('public.backorder_publications') IS NOT NULL AND to_regclass('public.product_allocation_plans') IS NOT NULL AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='sales_orders' AND column_name='priority')`},
	{34, `SELECT to_regclass('public.pegging_runs') IS NOT NULL AND to_regclass('public.pegging_nodes') IS NOT NULL AND to_regclass('public.pegging_edges') IS NOT NULL AND to_regclass('public.planning_exceptions') IS NOT NULL AND to_regclass('public.planning_exception_actions') IS NOT NULL AND to_regclass('public.v_current_planning_exceptions') IS NOT NULL`},
	{35, `SELECT to_regclass('public.supplier_schedule_events') IS NOT NULL AND to_regclass('public.supplier_lead_time_runs') IS NOT NULL AND to_regclass('public.supplier_lead_time_results') IS NOT NULL AND to_regclass('public.v_purchase_order_planning_schedule') IS NOT NULL`},
	{36, `SELECT to_regclass('public.inventory_policy_versions') IS NOT NULL AND to_regclass('public.inventory_policy_runs') IS NOT NULL AND to_regclass('public.inventory_policy_results') IS NOT NULL AND to_regclass('public.v_current_inventory_policy') IS NOT NULL`},
	{37, `SELECT to_regclass('public.maintenance_events') IS NOT NULL AND to_regclass('public.maintenance_event_revisions') IS NOT NULL AND to_regclass('public.v_current_maintenance_events') IS NOT NULL AND to_regclass('public.detailed_schedule_maintenance_snapshots') IS NOT NULL`},
	{38, `SELECT to_regclass('public.production_performance_runs') IS NOT NULL AND to_regclass('public.production_performance_results') IS NOT NULL AND to_regclass('public.capacity_feedback_versions') IS NOT NULL AND to_regclass('public.v_current_capacity_feedback') IS NOT NULL AND to_regclass('public.detailed_schedule_capacity_feedback_snapshots') IS NOT NULL`},
	{39, `SELECT to_regclass('public.dispatch_policy_versions') IS NOT NULL AND to_regclass('public.detailed_schedule_execution_state') IS NOT NULL AND to_regclass('public.dynamic_reschedule_runs') IS NOT NULL AND to_regclass('public.schedule_adherence_snapshots') IS NOT NULL`},
	{40, `SELECT to_regclass('public.control_tower_cases') IS NOT NULL AND to_regclass('public.control_tower_case_snapshots') IS NOT NULL AND to_regclass('public.control_tower_recommendations') IS NOT NULL AND to_regclass('public.control_tower_case_actions') IS NOT NULL AND to_regclass('public.v_current_control_tower_cases') IS NOT NULL`},
}

func bootstrapLegacyDatabase(ctx context.Context, conn *sqlx.Conn, migrations []Migration, installedBy string, logger *log.Logger) ([]int, error) {
	var hasAppSchema bool
	if err := conn.GetContext(ctx, &hasAppSchema, `SELECT to_regclass('public.items') IS NOT NULL OR to_regclass('public.work_orders') IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("inspect legacy schema: %w", err)
	}
	if !hasAppSchema {
		return nil, nil // brand-new database: apply 0001..latest normally.
	}

	migByVersion := map[int]Migration{}
	for _, mig := range migrations {
		migByVersion[mig.Version] = mig
	}

	detected := map[int]bool{}
	missingSeen := false
	for _, check := range legacyChecks {
		var exists bool
		if err := conn.GetContext(ctx, &exists, check.Query); err != nil {
			return nil, fmt.Errorf("legacy migration fingerprint %04d: %w", check.Version, err)
		}
		detected[check.Version] = exists
		if !exists {
			missingSeen = true
			continue
		}
		if missingSeen {
			return nil, fmt.Errorf("legacy database has non-contiguous migration fingerprints: version %04d exists after an earlier migration fingerprint is missing; refusing automatic baseline", check.Version)
		}
	}
	if !detected[1] {
		return nil, errors.New("existing CPIM-MES tables detected but 0001 schema fingerprint is incomplete; refusing automatic baseline")
	}

	tx, err := conn.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin legacy baseline: %w", err)
	}
	defer tx.Rollback()

	var baselined []int
	for _, check := range legacyChecks {
		if !detected[check.Version] {
			break
		}
		mig, ok := migByVersion[check.Version]
		if !ok {
			return nil, fmt.Errorf("legacy fingerprint refers to missing embedded migration %04d", check.Version)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations(version, name, checksum, applied_at, execution_ms, installed_by, baseline)
VALUES ($1,$2,$3,now(),0,$4,true)
ON CONFLICT (version) DO NOTHING`, mig.Version, mig.Name, mig.Checksum, "legacy-auto-baseline:"+installedBy); err != nil {
			return nil, fmt.Errorf("baseline migration %04d: %w", mig.Version, err)
		}
		baselined = append(baselined, mig.Version)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit legacy baseline: %w", err)
	}
	if len(baselined) > 0 {
		logger.Printf("[migration] legacy database auto-baselined versions: %s", formatVersions(baselined))
	}
	return baselined, nil
}

func isReplaySafeLegacyMigration(version int) bool {
	return version == 14
}

func formatVersions(vs []int) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, fmt.Sprintf("%04d", v))
	}
	return strings.Join(parts, ",")
}

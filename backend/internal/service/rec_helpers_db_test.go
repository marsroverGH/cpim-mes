package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// Helpers shared by the rec_*_db_test.go files (recovery planning/execution,
// control tower, dispatch, supplier scheduling, admin/auth DB tests).

type recEnv struct {
	*dbFixture
	Planner      *domain.User
	Admin        *domain.User
	Admin2       *domain.User
	PlannerActor RecoveryPlanningActor
	AdminActor   RecoveryPlanningActor
	Admin2Actor  RecoveryPlanningActor
}

func recNewEnv(t *testing.T) *recEnv {
	t.Helper()
	f := newDBFixture(t)
	e := &recEnv{dbFixture: f}
	e.Planner = f.user(t, "rec-planner", domain.RolePlanner)
	e.Admin = f.user(t, "rec-admin", domain.RoleAdmin)
	e.Admin2 = f.user(t, "rec-admin-2", domain.RoleAdmin)
	e.PlannerActor = recActor(e.Planner)
	e.AdminActor = recActor(e.Admin)
	e.Admin2Actor = recActor(e.Admin2)
	return e
}

func recActor(u *domain.User) RecoveryPlanningActor {
	return RecoveryPlanningActor{UserID: u.ID, Username: u.Username, Role: string(u.Role)}
}

func recSalesActor(u *domain.User) SalesOrderActor {
	return SalesOrderActor{UserID: u.ID, Username: u.Username, Role: u.Role}
}

func recToday() time.Time { return TruncateDay(time.Now().UTC()) }

func recDay(offset int) time.Time { return recToday().AddDate(0, 0, offset) }

func recItemID(t *testing.T, f *dbFixture, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.DB.GetContext(f.Ctx, &id, `SELECT id FROM items WHERE code=$1`, code); err != nil {
		t.Fatalf("item %s: %v", code, err)
	}
	return id
}

func recWCID(t *testing.T, f *dbFixture, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.DB.GetContext(f.Ctx, &id, `SELECT id FROM work_centers WHERE code=$1`, code); err != nil {
		t.Fatalf("work center %s: %v", code, err)
	}
	return id
}

func recWO(t *testing.T, f *dbFixture, orderNo, itemCode string, qty float64, start, due time.Time) *domain.WorkOrder {
	t.Helper()
	w := &domain.WorkOrder{OrderNo: orderNo, ItemID: recItemID(t, f, itemCode), Quantity: qty, StartDate: start, DueDate: due, Status: "PLANNED"}
	if err := f.Repos.WorkOrders.Create(f.Ctx, w); err != nil {
		t.Fatalf("create WO %s: %v", orderNo, err)
	}
	return w
}

func recPO(t *testing.T, f *dbFixture, poNo, itemCode string, qty float64, orderDate, due time.Time) *domain.PurchaseOrder {
	t.Helper()
	p := &domain.PurchaseOrder{PONo: poNo, ItemID: recItemID(t, f, itemCode), Supplier: "REC-SUPPLIER", Quantity: qty, OrderDate: orderDate, DueDate: due, Status: "OPEN"}
	if err := f.Repos.Purchases.Create(f.Ctx, p); err != nil {
		t.Fatalf("create PO %s: %v", poNo, err)
	}
	return p
}

func recGetWO(t *testing.T, f *dbFixture, id uuid.UUID) domain.WorkOrder {
	t.Helper()
	w, err := f.Repos.WorkOrders.Get(f.Ctx, id)
	if err != nil {
		t.Fatalf("get WO: %v", err)
	}
	return *w
}

// recRoutedItem returns an item code that has exactly one active routing with
// at least one operation (a releasable item in the seed data).
func recRoutedItem(t *testing.T, f *dbFixture) string {
	t.Helper()
	var code string
	err := f.DB.GetContext(f.Ctx, &code, `
SELECT i.code FROM items i
 WHERE (SELECT count(*) FROM routings r WHERE r.item_id=i.id AND r.is_active) = 1
   AND EXISTS (SELECT 1 FROM routings r JOIN routing_operations o ON o.routing_id=r.id WHERE r.item_id=i.id AND r.is_active)
 ORDER BY i.code LIMIT 1`)
	if err != nil {
		t.Fatalf("no routed item in seed data: %v", err)
	}
	return code
}

func recParams(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recPublishScenario drives the 0041 lifecycle DRAFT -> SIMULATED -> PUBLISHED
// and returns the scenario and the published run.
func recPublishScenario(t *testing.T, e *recEnv, name string, actions []RecoveryScenarioActionInput) (*domain.RecoveryScenario, *RecoverySimulationExecution) {
	t.Helper()
	rp := e.Svc.RecoveryPlanning
	sc, err := rp.CreateScenario(e.Ctx, RecoveryScenarioCreateInput{Name: name, Description: "rec test"}, e.PlannerActor)
	if err != nil {
		t.Fatalf("create scenario: %v", err)
	}
	for i, a := range actions {
		if _, err := rp.AddScenarioAction(e.Ctx, sc.ID, a, e.PlannerActor); err != nil {
			t.Fatalf("add action %d (%s %s): %v", i, a.ActionType, a.TargetRef, err)
		}
	}
	sim, err := rp.Simulate(e.Ctx, RecoverySimulationRequest{ScenarioID: sc.ID, HorizonDays: 90}, e.PlannerActor)
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if sim.Run.Status != "SUCCEEDED" {
		t.Fatalf("simulation status %s", sim.Run.Status)
	}
	if _, err := rp.PublishScenario(e.Ctx, sc.ID, RecoveryScenarioPublishInput{RunID: sim.Run.ID, Comment: "publish " + name}, e.PlannerActor); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return sc, sim
}

// recPrepare publishes a scenario and prepares an execution as the planner.
func recPrepare(t *testing.T, e *recEnv, name string, actions []RecoveryScenarioActionInput) *domain.RecoveryPlanExecutionDetail {
	t.Helper()
	sc, _ := recPublishScenario(t, e, name, actions)
	d, err := e.Svc.RecoveryExecution.Prepare(e.Ctx, sc.ID, RecoveryExecutionPrepareInput{IdempotencyKey: "key-" + name, Comment: "prepare " + name}, e.PlannerActor)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return d
}

// recApproved prepares, preflights (must pass) and approves with Admin.
func recApproved(t *testing.T, e *recEnv, name string, actions []RecoveryScenarioActionInput) *domain.RecoveryPlanExecutionDetail {
	t.Helper()
	d := recPrepare(t, e, name, actions)
	pf, err := e.Svc.RecoveryExecution.Preflight(e.Ctx, d.Execution.ID, e.PlannerActor)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.Execution.Status != "PREFLIGHT_PASSED" {
		for _, a := range pf.Actions {
			t.Logf("action %d %s %s: %s", a.SequenceNo, a.ActionType, a.PreflightStatus, a.ErrorText)
		}
		t.Fatalf("preflight status %s", pf.Execution.Status)
	}
	ap, err := e.Svc.RecoveryExecution.Approve(e.Ctx, d.Execution.ID, RecoveryExecutionApprovalInput{Comment: "approve"}, e.AdminActor)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return ap
}

func recCode(err error) domain.ErrorCode {
	var ae *domain.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func recWantCode(t *testing.T, err error, code domain.ErrorCode, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error containing %q, got nil", code, contains)
	}
	if got := recCode(err); got != code {
		t.Fatalf("expected %s error, got %v", code, err)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected error containing %q, got %v", contains, err)
	}
}

func recIsHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func recHashPtr(t *testing.T, label string, p *string) string {
	t.Helper()
	if p == nil || !recIsHash(*p) {
		t.Fatalf("%s: expected 64-char hex hash, got %v", label, p)
	}
	return *p
}

// recDBErr asserts a raw SQL statement is rejected by a DB guard.
func recDBErr(t *testing.T, f *dbFixture, contains, q string, args ...any) {
	t.Helper()
	_, err := f.DB.ExecContext(f.Ctx, q, args...)
	if err == nil {
		t.Fatalf("expected DB rejection (%s) for %s", contains, q)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected DB error containing %q, got %v", contains, err)
	}
}

func recCount(t *testing.T, f *dbFixture, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.DB.GetContext(f.Ctx, &n, q, args...); err != nil {
		t.Fatalf("count %s: %v", q, err)
	}
	return n
}

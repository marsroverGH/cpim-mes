package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func apiintDay(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
}

func TestAPIIntSalesOrderLifecycle(t *testing.T) {
	env := apiintNewEnv(t)
	planner := env.login("planner", domain.RolePlanner)
	operator := env.login("op", domain.RoleOperator)
	frame := env.itemID("FRAME-1")

	cust := map[string]any{"customerNo": "C-1", "name": "Customer One", "status": "ACTIVE", "shipTo": "Tokyo"}
	apiintStatus(t, operator.do(http.MethodPost, "/api/customers", cust), http.StatusForbidden, "operator create customer")
	rr := planner.do(http.MethodPost, "/api/customers", cust)
	apiintStatus(t, rr, http.StatusCreated, "create customer")
	customer := apiintJSON[map[string]any](t, rr)
	custID := customer["id"].(string)

	cust["name"] = "Customer Uno"
	up := planner.do(http.MethodPut, "/api/customers/"+custID, cust)
	apiintStatus(t, up, http.StatusOK, "update customer")
	var cname string
	env.scalar(&cname, `SELECT name FROM customers WHERE id=$1`, custID)
	if cname != "Customer Uno" {
		t.Fatalf("customer name in db = %q", cname)
	}
	dup := planner.do(http.MethodPost, "/api/customers", map[string]any{"customerNo": "C-1", "name": "Dup", "status": "ACTIVE"})
	if dup.Code < 400 {
		t.Fatalf("duplicate customerNo accepted: %d", dup.Code)
	}

	promised := apiintDay(7)
	order := map[string]any{
		"orderNo": "SO-T-1", "customerId": custID, "requestedDate": promised, "promisedDate": promised,
		"lines": []map[string]any{{"itemId": frame, "quantity": 2, "unitPrice": 1000, "promisedDate": promised}},
	}
	rr = planner.do(http.MethodPost, "/api/sales-orders", order)
	apiintStatus(t, rr, http.StatusCreated, "create SO")
	detail := apiintJSON[map[string]any](t, rr)
	so := detail["order"].(map[string]any)
	if so["status"] != "DRAFT" {
		t.Fatalf("new SO status = %v", so["status"])
	}
	soID := so["id"].(string)
	lineID := detail["lines"].([]any)[0].(map[string]any)["id"].(string)

	bad := map[string]any{"orderNo": "SO-BAD", "customerId": custID, "requestedDate": "07/11/2026",
		"lines": []map[string]any{{"itemId": frame, "quantity": 1}}}
	if rr := planner.do(http.MethodPost, "/api/sales-orders", bad); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad requestedDate: %d %s", rr.Code, rr.Body.String())
	}
	noLines := planner.do(http.MethodPost, "/api/sales-orders", map[string]any{"orderNo": "SO-EMPTY", "customerId": custID})
	if noLines.Code < 400 || noLines.Code >= 500 {
		t.Fatalf("SO without lines: %d %s", noLines.Code, noLines.Body.String())
	}

	// Allocation before confirmation is a state conflict.
	pre := planner.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/allocate", map[string]any{"allocationId": uuid.NewString(), "quantity": 1})
	if pre.Code < 400 || pre.Code >= 500 {
		t.Fatalf("allocate DRAFT line: %d %s", pre.Code, pre.Body.String())
	}

	rr = planner.do(http.MethodPost, "/api/sales-orders/"+soID+"/confirm", nil)
	apiintStatus(t, rr, http.StatusOK, "confirm")
	if apiintJSON[map[string]any](t, rr)["order"].(map[string]any)["status"] != "CONFIRMED" {
		t.Fatal(rr.Body.String())
	}

	allocID := uuid.NewString()
	for i := 0; i < 2; i++ { // second call is an idempotent retry
		rr = planner.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/allocate", map[string]any{"allocationId": allocID, "quantity": 2})
		apiintStatus(t, rr, http.StatusOK, "allocate")
		if q := apiintJSON[map[string]any](t, rr)["lines"].([]any)[0].(map[string]any)["allocatedQty"]; q != 2.0 {
			t.Fatalf("allocatedQty after attempt %d = %v", i+1, q)
		}
	}
	rel := planner.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/release-allocation", map[string]any{"releaseId": uuid.NewString(), "quantity": 1})
	if rel.Code != http.StatusOK {
		t.Fatalf("release allocation: %d %s", rel.Code, rel.Body.String())
	}

	// Promise check / accept (ATP/CTP) are planner-only.
	apiintStatus(t, operator.do(http.MethodPost, "/api/sales-orders/"+soID+"/promise/check", map[string]any{}), http.StatusForbidden, "operator promise")
	pc := planner.do(http.MethodPost, "/api/sales-orders/"+soID+"/promise/check", map[string]any{})
	if pc.Code >= 500 {
		t.Fatalf("promise check: %d %s", pc.Code, pc.Body.String())
	}
	apiintStatus(t, planner.do(http.MethodGet, "/api/sales-orders/"+soID+"/promise-runs", nil), http.StatusOK, "promise runs")
	pg := planner.do(http.MethodPost, "/api/sales-orders/"+soID+"/pegging/run", map[string]any{})
	if pg.Code >= 500 {
		t.Fatalf("pegging run: %d %s", pg.Code, pg.Body.String())
	}

	// Shipping (operator) consumes the allocation through the lot ledger.
	shipID := uuid.NewString()
	ship := map[string]any{"shipmentId": shipID, "quantity": 2, "carrier": "Test", "trackingNo": "T1"}
	rr = operator.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/ship", ship)
	if rr.Code != http.StatusCreated {
		// Only one unit is still allocated after the release above; ship that.
		ship["quantity"] = 1
		rr = operator.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/ship", ship)
	}
	apiintStatus(t, rr, http.StatusCreated, "ship")
	shipped := apiintJSON[map[string]any](t, rr)
	if len(shipped["shipments"].([]any)) != 1 {
		t.Fatalf("shipments = %v", shipped["shipments"])
	}
	retry := operator.do(http.MethodPost, "/api/sales-order-lines/"+lineID+"/ship", ship)
	apiintStatus(t, retry, http.StatusCreated, "ship retry")
	var shipments int
	env.scalar(&shipments, `SELECT count(*) FROM sales_order_shipments WHERE sales_order_line_id=$1`, lineID)
	if shipments != 1 {
		t.Fatalf("shipments in db = %d; want 1 (idempotent retry)", shipments)
	}

	got := operator.do(http.MethodGet, "/api/sales-orders/"+soID, nil)
	apiintStatus(t, got, http.StatusOK, "get SO")
	list := operator.do(http.MethodGet, "/api/sales-orders", nil)
	apiintStatus(t, list, http.StatusOK, "list SO")
	if !strings.Contains(list.Body.String(), "SO-T-1") {
		t.Fatal(list.Body.String())
	}
	unknown := operator.do(http.MethodGet, "/api/sales-orders/"+uuid.NewString(), nil)
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown SO: status %d; want 404 (body %s)", unknown.Code, unknown.Body.String())
	}

	// A second order can be cancelled; cancelling twice conflicts.
	order["orderNo"] = "SO-T-2"
	rr = planner.do(http.MethodPost, "/api/sales-orders", order)
	apiintStatus(t, rr, http.StatusCreated, "create SO 2")
	so2 := apiintJSON[map[string]any](t, rr)["order"].(map[string]any)["id"].(string)
	can := planner.do(http.MethodPost, "/api/sales-orders/"+so2+"/cancel", map[string]any{"reason": "test"})
	apiintStatus(t, can, http.StatusOK, "cancel")
	var st string
	env.scalar(&st, `SELECT status FROM sales_orders WHERE id=$1`, so2)
	if st != "CANCELLED" {
		t.Fatalf("SO-T-2 status = %s", st)
	}
	again := planner.do(http.MethodPost, "/api/sales-orders/"+so2+"/confirm", nil)
	if again.Code < 400 || again.Code >= 500 {
		t.Fatalf("confirm cancelled SO: %d %s", again.Code, again.Body.String())
	}
	dupSO := planner.do(http.MethodPost, "/api/sales-orders", order)
	if dupSO.Code < 400 {
		t.Fatalf("duplicate orderNo accepted: %d", dupSO.Code)
	}
	if dupSO.Code != http.StatusConflict {
		t.Logf("duplicate SO orderNo -> %d %s", dupSO.Code, dupSO.Body.String())
	}

	// Priority / service class (backorder processing).
	pr := planner.do(http.MethodPut, "/api/sales-orders/"+soID+"/priority", map[string]any{"priority": 10})
	if pr.Code >= 500 {
		t.Fatalf("set priority: %d %s", pr.Code, pr.Body.String())
	}
	bo := planner.do(http.MethodPost, "/api/backorders/preview", map[string]any{})
	if bo.Code >= 500 {
		t.Fatalf("backorder preview: %d %s", bo.Code, bo.Body.String())
	}
}

func TestAPIIntRecoveryPlanningAndExecution(t *testing.T) {
	env := apiintNewEnv(t)
	planner := env.login("planner", domain.RolePlanner)
	admin := env.login("admin", domain.RoleAdmin)
	wheel := env.itemID("WHEEL-1")

	start := time.Now().UTC().AddDate(0, 0, 5).Truncate(24 * time.Hour)
	due := start.AddDate(0, 0, 3)
	rr := admin.do(http.MethodPost, "/api/work-orders", map[string]any{
		"orderNo": "WO-REC-1", "itemId": wheel, "quantity": 1,
		"startDate": start.Format(time.RFC3339), "dueDate": due.Format(time.RFC3339), "status": "PLANNED",
	})
	apiintStatus(t, rr, http.StatusCreated, "create WO")
	woID := apiintJSON[domain.WorkOrder](t, rr).ID

	rr = planner.do(http.MethodPost, "/api/recovery-scenarios", map[string]any{"name": "Recover WO", "description": "api test"})
	apiintStatus(t, rr, http.StatusCreated, "create scenario")
	scID := apiintJSON[map[string]any](t, rr)["id"].(string)
	scPath := "/api/recovery-scenarios/" + scID

	apiintStatus(t, planner.do(http.MethodPost, "/api/recovery-scenarios", map[string]any{"name": ""}), http.StatusBadRequest, "scenario without name")
	upd := planner.do(http.MethodPut, scPath, map[string]any{"name": "Recover WO v2", "description": "updated"})
	apiintStatus(t, upd, http.StatusOK, "update scenario")
	apiintStatus(t, planner.do(http.MethodGet, scPath, nil), http.StatusOK, "get scenario")

	// Draft action that is edited and deleted again.
	tmp := planner.do(http.MethodPost, scPath+"/actions", map[string]any{
		"actionType": "RESCHEDULE_WO", "targetType": "WORK_ORDER", "targetRef": "WO:WO-REC-1",
		"parameters": map[string]any{"recoveryDays": 2}, "estimatedCost": 1, "note": "tmp",
	})
	apiintStatus(t, tmp, http.StatusCreated, "add tmp action")
	tmpID := apiintJSON[map[string]any](t, tmp)["id"].(string)
	apiintStatus(t, planner.do(http.MethodPut, scPath+"/actions/"+tmpID, map[string]any{
		"actionType": "RESCHEDULE_WO", "targetType": "WORK_ORDER", "targetRef": "WO:WO-REC-1",
		"parameters": map[string]any{"recoveryDays": 3}, "estimatedCost": 2, "note": "tmp2",
	}), http.StatusOK, "update action")
	del := planner.do(http.MethodDelete, scPath+"/actions/"+tmpID, nil)
	if del.Code != http.StatusNoContent && del.Code != http.StatusOK {
		t.Fatalf("delete action: %d %s", del.Code, del.Body.String())
	}
	badAction := planner.do(http.MethodPost, scPath+"/actions", map[string]any{"actionType": "TELEPORT", "targetType": "WORK_ORDER", "targetRef": "WO:x"})
	if badAction.Code < 400 || badAction.Code >= 500 {
		t.Fatalf("invalid action type: %d %s", badAction.Code, badAction.Body.String())
	}

	act := planner.do(http.MethodPost, scPath+"/actions", map[string]any{
		"actionType": "RESCHEDULE_WO", "targetType": "WORK_ORDER", "targetRef": "WO:WO-REC-1",
		"parameters": map[string]any{"recoveryDays": 1}, "estimatedCost": 1000, "note": "controlled",
	})
	apiintStatus(t, act, http.StatusCreated, "add action")
	acts := planner.do(http.MethodGet, scPath+"/actions", nil)
	apiintStatus(t, acts, http.StatusOK, "list actions")
	if n := len(apiintJSON[[]any](t, acts)); n != 1 {
		t.Fatalf("actions = %d; want 1", n)
	}

	sim := planner.do(http.MethodPost, scPath+"/simulate", map[string]any{"horizonDays": 90})
	apiintStatus(t, sim, http.StatusCreated, "simulate")
	run := apiintJSON[map[string]any](t, sim)["run"].(map[string]any)
	if run["status"] != "SUCCEEDED" {
		t.Fatalf("simulation run = %v", run)
	}
	apiintStatus(t, planner.do(http.MethodGet, "/api/recovery-scenario-comparison", nil), http.StatusOK, "compare")

	pub := planner.do(http.MethodPost, scPath+"/publish", map[string]any{"runId": run["id"], "comment": "evidence"})
	apiintStatus(t, pub, http.StatusCreated, "publish")
	// Publishing is planning evidence only: the WO is untouched.
	var gotStart time.Time
	env.scalar(&gotStart, `SELECT start_date FROM work_orders WHERE id=$1`, woID)
	if !gotStart.Equal(start) {
		t.Fatalf("publish changed WO start: %v != %v", gotStart, start)
	}

	key := "api-" + uuid.NewString()
	prep := planner.do(http.MethodPost, scPath+"/executions", map[string]any{"idempotencyKey": key, "comment": "please"})
	apiintStatus(t, prep, http.StatusCreated, "prepare")
	exec := apiintJSON[map[string]any](t, prep)["execution"].(map[string]any)
	execID := exec["id"].(string)
	if exec["status"] != "PREPARED" {
		t.Fatalf("execution = %v", exec)
	}
	rep := planner.do(http.MethodPost, scPath+"/executions", map[string]any{"idempotencyKey": key, "comment": "again"})
	if rep.Code >= 300 || apiintJSON[map[string]any](t, rep)["execution"].(map[string]any)["id"] != execID {
		t.Fatalf("prepare retry: %d %s", rep.Code, rep.Body.String())
	}
	execPath := "/api/recovery-executions/" + execID

	// Execute before approval violates the state machine.
	early := admin.do(http.MethodPost, execPath+"/execute", map[string]any{})
	if early.Code < 400 || early.Code >= 500 {
		t.Fatalf("execute before approval: %d %s", early.Code, early.Body.String())
	}

	pf := planner.do(http.MethodPost, execPath+"/preflight", map[string]any{})
	apiintStatus(t, pf, http.StatusOK, "preflight")
	if s := apiintJSON[map[string]any](t, pf)["execution"].(map[string]any)["status"]; s != "PREFLIGHT_PASSED" {
		t.Fatalf("preflight status = %v", s)
	}
	apiintStatus(t, planner.do(http.MethodPost, execPath+"/approve", map[string]any{"comment": "self"}), http.StatusForbidden, "planner approve")
	ap := admin.do(http.MethodPost, execPath+"/approve", map[string]any{"comment": "independent"})
	apiintStatus(t, ap, http.StatusOK, "approve")
	if e := apiintJSON[map[string]any](t, ap)["execution"].(map[string]any); e["status"] != "APPROVED" || e["approvedByUsername"] != "admin" {
		t.Fatalf("approved execution = %v", e)
	}

	ex := admin.do(http.MethodPost, execPath+"/execute", nil)
	apiintStatus(t, ex, http.StatusOK, "execute")
	exBody := apiintJSON[map[string]any](t, ex)
	if e := exBody["execution"].(map[string]any); e["status"] != "SUCCEEDED" || e["succeededActionCount"] != 1.0 {
		t.Fatalf("executed = %v", e)
	}
	env.scalar(&gotStart, `SELECT start_date FROM work_orders WHERE id=$1`, woID)
	if want := start.AddDate(0, 0, -1); !gotStart.Equal(want) {
		t.Fatalf("WO start after execution = %v; want %v", gotStart, want)
	}
	// Re-executing is idempotent and does not move the WO again.
	apiintStatus(t, admin.do(http.MethodPost, execPath+"/execute", nil), http.StatusOK, "re-execute")
	env.scalar(&gotStart, `SELECT start_date FROM work_orders WHERE id=$1`, woID)
	if want := start.AddDate(0, 0, -1); !gotStart.Equal(want) {
		t.Fatalf("WO moved twice: %v", gotStart)
	}
	cancel := admin.do(http.MethodPost, execPath+"/cancel", map[string]any{"comment": "too late"})
	if cancel.Code < 400 || cancel.Code >= 500 {
		t.Fatalf("cancel SUCCEEDED execution: %d %s", cancel.Code, cancel.Body.String())
	}

	apiintStatus(t, admin.do(http.MethodGet, execPath, nil), http.StatusOK, "get execution")
	lst := admin.do(http.MethodGet, "/api/recovery-executions?status=SUCCEEDED", nil)
	apiintStatus(t, lst, http.StatusOK, "list executions")
	if !strings.Contains(lst.Body.String(), execID) {
		t.Fatal(lst.Body.String())
	}
	apiintStatus(t, admin.do(http.MethodGet, "/api/recovery-executions/"+uuid.NewString(), nil), http.StatusNotFound, "unknown execution")

	// A second, cancellable execution path.
	sc2 := planner.do(http.MethodPost, "/api/recovery-scenarios", map[string]any{"name": "Archive me"})
	apiintStatus(t, sc2, http.StatusCreated, "scenario 2")
	sc2ID := apiintJSON[map[string]any](t, sc2)["id"].(string)
	arch := planner.do(http.MethodPost, "/api/recovery-scenarios/"+sc2ID+"/archive", map[string]any{"comment": "x"})
	apiintStatus(t, arch, http.StatusOK, "archive scenario")
	editArchived := planner.do(http.MethodPut, "/api/recovery-scenarios/"+sc2ID, map[string]any{"name": "nope"})
	if editArchived.Code < 400 || editArchived.Code >= 500 {
		t.Fatalf("edit archived scenario: %d %s", editArchived.Code, editArchived.Body.String())
	}
	lstS := planner.do(http.MethodGet, "/api/recovery-scenarios?status=ARCHIVED", nil)
	apiintStatus(t, lstS, http.StatusOK, "list archived")
	if !strings.Contains(lstS.Body.String(), sc2ID) {
		t.Fatal(lstS.Body.String())
	}
}

func TestAPIIntUserAdministration(t *testing.T) {
	env := apiintNewEnv(t)
	admin := env.login("root", domain.RoleAdmin)
	planner := env.login("planner", domain.RolePlanner)

	apiintStatus(t, planner.do(http.MethodGet, "/api/admin/users", nil), http.StatusForbidden, "planner list users")
	newUser := map[string]any{"username": "newbie", "password": "Str0ng-Passw0rd!", "role": "operator"}
	apiintStatus(t, planner.do(http.MethodPost, "/api/admin/users", newUser), http.StatusForbidden, "planner create user")

	rr := admin.do(http.MethodPost, "/api/admin/users", newUser)
	apiintStatus(t, rr, http.StatusCreated, "create user")
	if strings.Contains(rr.Body.String(), "password") || strings.Contains(rr.Body.String(), "$2a$") {
		t.Fatalf("create user response exposes password data: %s", rr.Body.String())
	}
	u := apiintJSON[domain.User](t, rr)
	if u.Role != domain.RoleOperator || !u.IsActive {
		t.Fatalf("created user = %+v", u)
	}
	uPath := "/api/admin/users/" + u.ID.String()

	t.Run("validation", func(t *testing.T) {
		for name, body := range map[string]any{
			"duplicate":     newUser,
			"weak password": map[string]any{"username": "weak", "password": "x", "role": "viewer"},
			"bad role":      map[string]any{"username": "r", "password": "Str0ng-Passw0rd!", "role": "root"},
			"unknown field": `{"username":"u","password":"Str0ng-Passw0rd!","role":"viewer","isAdmin":true}`,
		} {
			rr := admin.do(http.MethodPost, "/api/admin/users", body)
			if rr.Code < 400 || rr.Code >= 500 {
				t.Errorf("%s: status %d body %s", name, rr.Code, rr.Body.String())
			}
		}
		apiintStatus(t, admin.do(http.MethodPut, uPath+"/active", map[string]any{}), http.StatusBadRequest, "isActive missing")
		apiintStatus(t, admin.do(http.MethodPut, uPath+"/role", map[string]any{"role": "superuser"}), http.StatusBadRequest, "invalid role")
		unknown := admin.do(http.MethodPut, "/api/admin/users/"+uuid.NewString()+"/role", map[string]any{"role": "viewer"})
		if unknown.Code != http.StatusNotFound && unknown.Code != http.StatusBadRequest {
			t.Errorf("unknown user role change: %d %s", unknown.Code, unknown.Body.String())
		}
	})

	// The new user can log in, then admin changes role / password / active.
	apiintStatus(t, env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "newbie", "password": "Str0ng-Passw0rd!"}), http.StatusOK, "new user login")
	rr = admin.do(http.MethodPut, uPath+"/role", map[string]any{"role": "planner"})
	apiintStatus(t, rr, http.StatusOK, "role change")
	if apiintJSON[domain.User](t, rr).Role != domain.RolePlanner {
		t.Fatal(rr.Body.String())
	}

	apiintStatus(t, admin.do(http.MethodPost, uPath+"/reset-password", map[string]any{"newPassword": "An0ther-Str0ng-Pass!"}), http.StatusNoContent, "reset password")
	old := env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "newbie", "password": "Str0ng-Passw0rd!"})
	apiintStatus(t, old, http.StatusUnauthorized, "old password after reset")
	fresh := env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "newbie", "password": "An0ther-Str0ng-Pass!"})
	apiintStatus(t, fresh, http.StatusOK, "new password after reset")

	rr = admin.do(http.MethodPut, uPath+"/active", map[string]any{"isActive": false})
	apiintStatus(t, rr, http.StatusOK, "disable")
	if apiintJSON[domain.User](t, rr).IsActive {
		t.Fatal("still active")
	}
	apiintStatus(t, env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "newbie", "password": "An0ther-Str0ng-Pass!"}), http.StatusUnauthorized, "disabled login")
	apiintStatus(t, admin.do(http.MethodPut, uPath+"/active", map[string]any{"isActive": true}), http.StatusOK, "enable")

	// Admins cannot lock themselves out.
	var rootID uuid.UUID
	env.scalar(&rootID, `SELECT id FROM users WHERE username='root'`)
	self := admin.do(http.MethodPut, "/api/admin/users/"+rootID.String()+"/active", map[string]any{"isActive": false})
	if self.Code < 400 || self.Code >= 500 {
		t.Errorf("admin disabling own account: %d %s", self.Code, self.Body.String())
	}

	list := admin.do(http.MethodGet, "/api/admin/users", nil)
	apiintStatus(t, list, http.StatusOK, "list users")
	if strings.Contains(list.Body.String(), "$2a$") || strings.Contains(list.Body.String(), "passwordHash") {
		t.Fatal("user list exposes password hashes")
	}

	var events []string
	if err := env.DB.SelectContext(env.Ctx, &events, `SELECT action FROM audit_log WHERE resource='user-administration' ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(events, ",")
	for _, ev := range []string{"USER_CREATED", "ROLE_CHANGED", "PASSWORD_RESET", "USER_DISABLED", "USER_ENABLED"} {
		if !strings.Contains(joined, ev) {
			t.Errorf("security audit missing %s (have %s)", ev, joined)
		}
	}

	t.Run("change own password", func(t *testing.T) {
		c := env.login("selfservice", domain.RoleViewer)
		apiintStatus(t, c.do(http.MethodPost, "/api/auth/change-password", map[string]any{"currentPassword": "wrong", "newPassword": "Br4nd-New-Passw0rd!"}), http.StatusBadRequest, "wrong current")
		apiintStatus(t, c.do(http.MethodPost, "/api/auth/change-password", map[string]any{"currentPassword": apiintPassword, "newPassword": "x"}), http.StatusBadRequest, "weak new")
		apiintStatus(t, c.do(http.MethodPost, "/api/auth/change-password", `{"currentPassword":`), http.StatusBadRequest, "bad json")
		apiintStatus(t, c.do(http.MethodPost, "/api/auth/change-password", map[string]any{"currentPassword": apiintPassword, "newPassword": "Br4nd-New-Passw0rd!"}), http.StatusNoContent, "change own password")
		apiintStatus(t, env.anon().do(http.MethodPost, "/api/auth/login", map[string]string{"username": "selfservice", "password": "Br4nd-New-Passw0rd!"}), http.StatusOK, "login with new password")
	})
}

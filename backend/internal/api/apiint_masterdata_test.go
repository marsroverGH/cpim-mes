package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

// TestAPIIntCapacityMasterData drives calendars, work centers and routings
// (admin-only capacity/routing master permissions) and checks persistence.
func TestAPIIntCapacityMasterData(t *testing.T) {
	env := apiintNewEnv(t)
	admin := env.login("admin", domain.RoleAdmin)
	planner := env.login("planner", domain.RolePlanner)

	// --- Calendar with an exception (dates are RFC3339).
	cal := map[string]any{"code": "CAL-T", "name": "Test cal", "mondayMin": 480, "tuesdayMin": 480, "wednesdayMin": 480, "thursdayMin": 480, "fridayMin": 480}
	apiintStatus(t, planner.do(http.MethodPost, "/api/calendars", cal), http.StatusForbidden, "planner create calendar")
	rr := admin.do(http.MethodPost, "/api/calendars", cal)
	apiintStatus(t, rr, http.StatusCreated, "create calendar")
	calID := apiintJSON[domain.WorkCalendar](t, rr).ID.String()
	cal["fridayMin"] = 240
	apiintStatus(t, admin.do(http.MethodPut, "/api/calendars/"+calID, cal), http.StatusOK, "update calendar")
	var fri int
	env.scalar(&fri, `SELECT friday_min FROM work_calendars WHERE id=$1`, calID)
	if fri != 240 {
		t.Fatalf("friday_min = %d", fri)
	}
	got := admin.do(http.MethodGet, "/api/calendars/"+calID, nil)
	apiintStatus(t, got, http.StatusOK, "get calendar")

	apiintStatus(t, admin.do(http.MethodPost, "/api/calendars/"+calID+"/exceptions", map[string]any{"exceptionDate": "2026-12-31", "kind": "HOLIDAY"}), http.StatusBadRequest, "YYYY-MM-DD exception date")
	ex := admin.do(http.MethodPost, "/api/calendars/"+calID+"/exceptions", map[string]any{"exceptionDate": "2026-12-31T00:00:00Z", "kind": "HOLIDAY", "description": "NYE"})
	apiintStatus(t, ex, http.StatusCreated, "add exception")
	exID := apiintJSON[domain.CalendarException](t, ex).ID.String()
	list := admin.do(http.MethodGet, "/api/calendars/"+calID+"/exceptions", nil)
	if !strings.Contains(list.Body.String(), "NYE") {
		t.Fatalf("exceptions = %s", list.Body.String())
	}
	apiintStatus(t, admin.do(http.MethodDelete, "/api/calendar-exceptions/"+exID, nil), http.StatusNoContent, "delete exception")

	// --- Work center + setup matrix.
	wc := map[string]any{"code": "WC-T", "name": "Test WC", "capacityMinutesPerDay": 480, "efficiency": 1, "utilization": 0.9, "machineCount": 1, "workerCount": 1, "calendarId": calID}
	rr = admin.do(http.MethodPost, "/api/work-centers", wc)
	apiintStatus(t, rr, http.StatusCreated, "create WC")
	wcID := apiintJSON[domain.WorkCenter](t, rr).ID.String()
	wc["capacityMinutesPerDay"] = 600
	apiintStatus(t, admin.do(http.MethodPut, "/api/work-centers/"+wcID, wc), http.StatusOK, "update WC")
	var capMin float64
	env.scalar(&capMin, `SELECT capacity_minutes_per_day FROM work_centers WHERE id=$1`, wcID)
	if capMin != 600 {
		t.Fatalf("capacity = %v", capMin)
	}
	sm := admin.do(http.MethodPost, "/api/work-centers/"+wcID+"/setup-matrix", map[string]any{"fromFamily": "A", "toFamily": "B", "setupMinutes": 15})
	if sm.Code >= 500 {
		t.Fatalf("setup matrix: %d %s", sm.Code, sm.Body.String())
	}
	apiintStatus(t, admin.do(http.MethodGet, "/api/work-centers/"+wcID+"/setup-matrix", nil), http.StatusOK, "get setup matrix")

	// --- Routing using the new work center.
	rr = admin.do(http.MethodPost, "/api/routings", map[string]any{"itemId": env.itemID("SADDLE-1"), "description": "saddle", "isActive": true})
	apiintStatus(t, rr, http.StatusCreated, "create routing")
	rtID := apiintJSON[domain.Routing](t, rr).ID.String()
	rr = admin.do(http.MethodPost, "/api/routings/"+rtID+"/operations", map[string]any{"seqNo": 10, "workCenterId": wcID, "description": "op10", "setupMinutes": 5, "runMinutesPerUnit": 1, "machinesRequired": 1, "workersRequired": 1})
	apiintStatus(t, rr, http.StatusCreated, "add routing op")
	opID := apiintJSON[domain.RoutingOperation](t, rr).ID.String()
	apiintStatus(t, admin.do(http.MethodPut, "/api/routing-operations/"+opID, map[string]any{"seqNo": 10, "workCenterId": wcID, "description": "op10b", "setupMinutes": 6, "runMinutesPerUnit": 2, "machinesRequired": 1, "workersRequired": 1}), http.StatusOK, "update routing op")
	var desc string
	env.scalar(&desc, `SELECT description FROM routing_operations WHERE id=$1`, opID)
	if desc != "op10b" {
		t.Fatalf("routing op description = %q", desc)
	}
	ops := admin.do(http.MethodGet, "/api/routings/"+rtID+"/operations", nil)
	if !strings.Contains(ops.Body.String(), "op10b") {
		t.Fatal(ops.Body.String())
	}

	// Deleting a work center still referenced by a routing must not succeed
	// silently and must not leak the FK constraint.
	del := admin.do(http.MethodDelete, "/api/work-centers/"+wcID, nil)
	var n int
	env.scalar(&n, `SELECT count(*) FROM work_centers WHERE id=$1`, wcID)
	if del.Code < 300 && n == 0 {
		t.Fatal("work center in use was deleted")
	}
	if del.Code < 400 {
		t.Errorf("deleting an in-use work center returned %d", del.Code)
	}

	apiintStatus(t, admin.do(http.MethodDelete, "/api/routing-operations/"+opID, nil), http.StatusNoContent, "delete op")
	apiintStatus(t, admin.do(http.MethodDelete, "/api/work-centers/"+wcID, nil), http.StatusNoContent, "delete WC")
	apiintStatus(t, admin.do(http.MethodDelete, "/api/calendars/"+calID, nil), http.StatusNoContent, "delete calendar")
	env.scalar(&n, `SELECT count(*) FROM work_calendars WHERE id=$1`, calID)
	if n != 0 {
		t.Fatal("calendar not deleted")
	}

	// Cost rollup and CRP read the routing master.
	apiintStatus(t, planner.do(http.MethodGet, "/api/cost-rollup", nil), http.StatusOK, "cost rollup")
	crp := planner.do(http.MethodPost, "/api/crp/run", map[string]any{})
	if crp.Code != http.StatusOK {
		t.Fatalf("crp run: %d %s", crp.Code, crp.Body.String())
	}
}

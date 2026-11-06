package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

// recControlTowerBaseline runs a pegging exception scan over the seeded sales
// orders and refreshes the Control Tower so recovery planning has a baseline.
func recControlTowerBaseline(t *testing.T, e *recEnv) *ControlTowerRefreshResult {
	t.Helper()
	if _, err := e.Svc.Pegging.Scan(e.Ctx, ExceptionScanInput{HorizonDays: 120}, recSalesActor(e.Planner)); err != nil {
		t.Fatalf("pegging scan: %v", err)
	}
	res, err := e.Svc.ControlTower.Refresh(e.Ctx, time.Time{})
	if err != nil {
		t.Fatalf("control tower refresh: %v", err)
	}
	return res
}

func TestRecDBControlTowerRefreshDashboardAndWorkflow(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	ct := e.Svc.ControlTower

	res := recControlTowerBaseline(t, e)
	t.Logf("refresh: %+v", res)
	if res.ExceptionsEvaluated == 0 || res.CasesTouched == 0 || res.SnapshotsCreated != res.ExceptionsEvaluated {
		t.Fatalf("expected seeded exceptions to become cases: %+v", res)
	}

	dash, err := ct.Dashboard(e.Ctx, ControlTowerDashboardFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if dash.Summary.TotalCases != len(dash.Cases) || dash.Summary.TotalCases == 0 || dash.Summary.OpenCases != dash.Summary.TotalCases {
		t.Fatalf("dashboard summary: %+v (%d cases)", dash.Summary, len(dash.Cases))
	}
	var risk float64
	for i, c := range dash.Cases {
		if c.CurrentStatus != "OPEN" || c.SnapshotID == nil || c.PriorityScore == nil || c.PriorityBand == nil {
			t.Fatalf("case %d: %+v", i, c)
		}
		if c.ResultHash == nil || !recIsHash(*c.ResultHash) {
			t.Fatalf("case %d snapshot hash", i)
		}
		if i > 0 && *dash.Cases[i-1].PriorityScore < *c.PriorityScore {
			t.Fatalf("dashboard must be ordered by priority score desc")
		}
		risk += *c.RevenueAtRisk
	}
	if diff := risk - dash.Summary.RevenueAtRisk; diff > 0.01 || diff < -0.01 {
		t.Fatalf("revenue at risk summary %v vs %v", dash.Summary.RevenueAtRisk, risk)
	}

	// Refresh is idempotent with respect to cases (stable case keys).
	res2, err := ct.Refresh(e.Ctx, time.Now().Add(time.Hour))
	if err != nil || res2.CasesTouched != res.CasesTouched {
		t.Fatalf("second refresh: %v %+v", err, res2)
	}
	if n := recCount(t, e.dbFixture, `SELECT count(*) FROM control_tower_cases`); n != res.CasesTouched {
		t.Fatalf("cases duplicated: %d vs %d", n, res.CasesTouched)
	}

	// Filters.
	if _, err := ct.Dashboard(e.Ctx, ControlTowerDashboardFilter{Status: "bogus"}); recCode(err) != domain.ErrCodeBadRequest {
		t.Fatalf("bad status filter: %v", err)
	}
	if _, err := ct.Dashboard(e.Ctx, ControlTowerDashboardFilter{PriorityBand: "P9"}); recCode(err) != domain.ErrCodeBadRequest {
		t.Fatalf("bad band filter: %v", err)
	}
	band := *dash.Cases[0].PriorityBand
	byBand, err := ct.Dashboard(e.Ctx, ControlTowerDashboardFilter{Status: "open", PriorityBand: band})
	if err != nil || len(byBand.Cases) == 0 {
		t.Fatalf("band filter: %v", err)
	}
	for _, c := range byBand.Cases {
		if *c.PriorityBand != band {
			t.Fatalf("filter leaked band %s", *c.PriorityBand)
		}
	}

	top := dash.Cases[0]
	got, err := ct.GetCase(e.Ctx, top.CaseID)
	if err != nil || got.CaseID != top.CaseID {
		t.Fatalf("get case: %v", err)
	}
	if _, err := ct.GetCase(e.Ctx, uuid.New()); recCode(err) != domain.ErrCodeNotFound {
		t.Fatalf("missing case: %v", err)
	}
	recs, err := ct.Recommendations(e.Ctx, top.CaseID)
	if err != nil || len(recs) == 0 {
		t.Fatalf("recommendations: %v %d", err, len(recs))
	}
	for i, r := range recs {
		if r.RankNo != i+1 || r.ActionType == "" || r.Title == "" {
			t.Fatalf("recommendation %d: %+v", i, r)
		}
	}

	// Case workflow.
	actor := ControlTowerActor{UserID: e.Planner.ID, Username: e.Planner.Username, Role: string(e.Planner.Role)}
	if _, err := ct.ActOnCase(e.Ctx, uuid.Nil, ControlTowerCaseActionInput{ActionType: "ACKNOWLEDGE"}, actor); recCode(err) != domain.ErrCodeBadRequest {
		t.Fatalf("nil case: %v", err)
	}
	if _, err := ct.ActOnCase(e.Ctx, top.CaseID, ControlTowerCaseActionInput{ActionType: "ACKNOWLEDGE"}, ControlTowerActor{}); recCode(err) != domain.ErrCodeUnauthorized {
		t.Fatalf("anonymous: %v", err)
	}
	if _, err := ct.ActOnCase(e.Ctx, top.CaseID, ControlTowerCaseActionInput{ActionType: "explode"}, actor); recCode(err) != domain.ErrCodeBadRequest {
		t.Fatalf("bad action: %v", err)
	}
	if _, err := ct.ActOnCase(e.Ctx, top.CaseID, ControlTowerCaseActionInput{ActionType: "ASSIGN"}, actor); recCode(err) != domain.ErrCodeBadRequest {
		t.Fatalf("assign without user: %v", err)
	}
	steps := []struct {
		in   ControlTowerCaseActionInput
		want string
	}{
		{ControlTowerCaseActionInput{ActionType: " acknowledge ", Comment: " seen "}, "ACKNOWLEDGED"},
		{ControlTowerCaseActionInput{ActionType: "ASSIGN", AssignedToUserID: &e.Admin.ID}, "ASSIGNED"},
		{ControlTowerCaseActionInput{ActionType: "START"}, "IN_PROGRESS"},
		{ControlTowerCaseActionInput{ActionType: "RESOLVE", Comment: "fixed"}, "RESOLVED"},
		{ControlTowerCaseActionInput{ActionType: "REOPEN"}, "OPEN"},
		{ControlTowerCaseActionInput{ActionType: "RESOLVE"}, "RESOLVED"},
		{ControlTowerCaseActionInput{ActionType: "CLOSE"}, "CLOSED"},
	}
	for i, st := range steps {
		if i == 5 {
			// The DB lifecycle guard rejects CLOSE from a non-RESOLVED case.
			if _, err := ct.ActOnCase(e.Ctx, top.CaseID, ControlTowerCaseActionInput{ActionType: "CLOSE"}, actor); err == nil {
				t.Fatal("CLOSE from OPEN must be rejected")
			}
		}
		a, err := ct.ActOnCase(e.Ctx, top.CaseID, st.in, actor)
		if err != nil {
			t.Fatalf("%s: %v", st.in.ActionType, err)
		}
		if a.ToStatus != st.want || a.ActorUsername != e.Planner.Username {
			t.Fatalf("%s: from %s to %s (want %s)", st.in.ActionType, a.FromStatus, a.ToStatus, st.want)
		}
	}
	acts, err := ct.CaseActions(e.Ctx, top.CaseID)
	if err != nil || len(acts) != len(steps) || acts[0].Comment != "seen" {
		t.Fatalf("case actions: %v %d", err, len(acts))
	}
	if acts[1].AssignedToUserID == nil || *acts[1].AssignedToUserID != e.Admin.ID {
		t.Fatalf("assignment not recorded")
	}
	closed, err := ct.Dashboard(e.Ctx, ControlTowerDashboardFilter{Status: "CLOSED"})
	if err != nil {
		t.Fatal(err)
	}
	if len(closed.Cases) != 1 || closed.Cases[0].CaseID != top.CaseID {
		t.Fatalf("closed filter returned %d cases", len(closed.Cases))
	}
	after, _ := ct.GetCase(e.Ctx, top.CaseID)
	if after.CurrentStatus != "CLOSED" {
		t.Fatalf("case status after workflow: %s", after.CurrentStatus)
	}
}

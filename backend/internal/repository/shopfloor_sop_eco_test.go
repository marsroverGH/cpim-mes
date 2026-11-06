package repository_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestShopFloorOperationsFromRouting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := f.repos.ShopFloor
	it := f.item(t, "FG")
	wc1 := f.workCenter(t, 2, 2)
	wc2 := f.workCenter(t, 1, 1)

	old := &domain.Routing{ItemID: it.ID, Description: "old", IsActive: false}
	rt := &domain.Routing{ItemID: it.ID, Description: "current", IsActive: true}
	for _, r := range []*domain.Routing{old, rt} {
		if err := f.repos.Routings.Create(f.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	ops := []*domain.RoutingOperation{
		{RoutingID: rt.ID, SeqNo: 20, WorkCenterID: wc2.ID, Description: "pack", RunMinutesPerUnit: 0.5, MachinesRequired: 1, WorkersRequired: 1},
		{RoutingID: rt.ID, SeqNo: 10, WorkCenterID: wc1.ID, Description: "cut", SetupMinutes: 30, RunMinutesPerUnit: 2, SetupFamily: "STEEL", MachinesRequired: 2, WorkersRequired: 1},
		{RoutingID: old.ID, SeqNo: 5, WorkCenterID: wc1.ID, Description: "obsolete", MachinesRequired: 1, WorkersRequired: 1},
	}
	for _, op := range ops {
		if err := f.repos.Routings.AddOperation(f.ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	wo := &domain.WorkOrder{OrderNo: uniq("WO"), ItemID: it.ID, Quantity: 5, StartDate: date(2026, 11, 1), DueDate: date(2026, 11, 5)}
	if err := f.repos.WorkOrders.Create(f.ctx, wo); err != nil {
		t.Fatal(err)
	}
	if err := sf.CreateOpsForWO(f.ctx, wo.ID); err != nil {
		t.Fatal(err)
	}
	// Re-running is idempotent thanks to ON CONFLICT (wo_id, seq_no).
	if err := sf.CreateOpsForWO(f.ctx, wo.ID); err != nil {
		t.Fatal(err)
	}

	list, err := sf.ListByWO(f.ctx, wo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("only the active routing's operations are copied, got %d", len(list))
	}
	first, second := list[0], list[1]
	if first.SeqNo != 10 || first.Status != "READY" || second.SeqNo != 20 || second.Status != "PENDING" {
		t.Fatalf("first op must be READY, successors PENDING: %+v / %+v", first.WOOperation, second.WOOperation)
	}
	if first.WorkCenterCode != wc1.Code || first.OrderNo != wo.OrderNo || first.ItemCode != it.Code || !approxEqual(first.WOQuantity, 5) {
		t.Fatalf("joined detail columns: %+v", first)
	}
	if !approxEqual(first.PlannedSetupMin, 30) || !approxEqual(first.PlannedRunPerUnit, 2) || first.SetupFamily != "STEEL" ||
		first.MachinesRequired != 2 || first.RoutingOperationID == nil || *first.RoutingOperationID != ops[1].ID {
		t.Fatalf("routing attributes must be snapshotted: %+v", first.WOOperation)
	}

	// A planned WO is not on the active shop-floor board; a released one is.
	inActive := func() int {
		rows, err := sf.Active(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range rows {
			if r.WOID == wo.ID {
				n++
			}
		}
		return n
	}
	if n := inActive(); n != 0 {
		t.Fatalf("PLANNED WO ops must not be active, got %d", n)
	}
	if err := f.repos.WorkOrders.UpdateStatus(f.ctx, wo.ID, "RELEASED"); err != nil {
		t.Fatal(err)
	}
	if n := inActive(); n != 2 {
		t.Fatalf("released WO must show READY and PENDING ops, got %d", n)
	}

	// State machine guards.
	_, err = f.db.Exec(`UPDATE wo_operations SET status='READY' WHERE id=$1`, second.ID)
	wantErrContains(t, err, "cannot become READY before predecessor")
	_, err = f.db.Exec(`UPDATE wo_operations SET status='COMPLETED', completed_at=now() WHERE id=$1`, first.ID)
	wantErrContains(t, err, "invalid Shop Floor state transition")
	_, err = f.db.Exec(`UPDATE wo_operations SET completed_qty=6 WHERE id=$1`, first.ID)
	wantErrContains(t, err, "exceeds WO planned quantity")

	operator := f.user(t, domain.RoleOperator)
	f.db.MustExec(`UPDATE wo_operations SET status='IN_PROGRESS', active_started_at=now(), started_at=now(), operator=$2, operator_user_id=$3 WHERE id=$1`,
		first.ID, operator.Username, operator.ID)
	op, err := sf.Get(f.ctx, first.ID)
	if err != nil || op.Status != "IN_PROGRESS" || op.OperatorUserID == nil || *op.OperatorUserID != operator.ID {
		t.Fatalf("Get after start: %+v %v", op, err)
	}
	if _, err := sf.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}

	// Operation logs must carry a real operator identity.
	_, err = f.db.Exec(`INSERT INTO operation_logs (wo_op_id,event_type,operator) VALUES ($1,'START','ghost')`, first.ID)
	wantErrContains(t, err, "operator_user_id")
	_, err = f.db.Exec(`INSERT INTO operation_logs (wo_op_id,event_type,operator,operator_user_id) VALUES ($1,'START','impostor',$2)`, first.ID, operator.ID)
	wantErrContains(t, err, "identity does not match")
	f.db.MustExec(`INSERT INTO operation_logs (wo_op_id,event_type,operator,operator_user_id,event_at) VALUES ($1,'STOP',$2,$3,now()+interval '1 minute')`, first.ID, operator.Username, operator.ID)
	f.db.MustExec(`INSERT INTO operation_logs (wo_op_id,event_type,operator,operator_user_id) VALUES ($1,'START',$2,$3)`, first.ID, operator.Username, operator.ID)
	logs, err := sf.Logs(f.ctx, first.ID)
	if err != nil || len(logs) != 2 || logs[0].EventType != "START" || logs[1].EventType != "STOP" {
		t.Fatalf("Logs must be ordered by event time: %+v %v", logs, err)
	}

	// A WO whose item has no active routing gets no operations.
	bare := f.item(t, "FG")
	wo2 := &domain.WorkOrder{OrderNo: uniq("WO"), ItemID: bare.ID, Quantity: 1, StartDate: date(2026, 11, 1), DueDate: date(2026, 11, 2)}
	if err := f.repos.WorkOrders.Create(f.ctx, wo2); err != nil {
		t.Fatal(err)
	}
	if err := sf.CreateOpsForWO(f.ctx, wo2.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := sf.ListByWO(f.ctx, wo2.ID); len(l) != 0 {
		t.Fatalf("no routing → no ops, got %d", len(l))
	}
}

func TestSOPGroupsPlansAndProfiles(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sop := f.repos.SOP

	g := &domain.ItemGroup{Code: uniq("FAM"), Name: "family"}
	if err := sop.CreateGroup(f.ctx, g); err != nil {
		t.Fatal(err)
	}
	if g.ID == uuid.Nil || g.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", g)
	}
	if err := sop.CreateGroup(f.ctx, &domain.ItemGroup{Code: g.Code, Name: "dup"}); err == nil {
		t.Fatal("duplicate group code must be rejected")
	}
	groups, err := sop.ListGroups(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, x := range groups {
		if i > 0 && groups[i-1].Code > x.Code {
			t.Fatal("ListGroups must be ordered by code")
		}
		found = found || x.ID == g.ID
	}
	if !found {
		t.Fatal("group missing")
	}

	feb := &domain.SOPPlan{GroupID: g.ID, PlanMonth: date(2027, 2, 1), DemandQty: 100, SupplyQty: 90, InventoryTarget: 10}
	jan := &domain.SOPPlan{GroupID: g.ID, PlanMonth: date(2027, 1, 1), DemandQty: 50, SupplyQty: 50}
	for _, p := range []*domain.SOPPlan{feb, jan} {
		if err := sop.UpsertPlan(f.ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// Upsert on (group, month) overwrites quantities and keeps the original id.
	if err := sop.UpsertPlan(f.ctx, &domain.SOPPlan{GroupID: g.ID, PlanMonth: date(2027, 2, 1), DemandQty: 120, SupplyQty: 110, InventoryTarget: 5, Notes: "revised"}); err != nil {
		t.Fatal(err)
	}
	plans, err := sop.PlansByGroup(f.ctx, g.ID)
	if err != nil || len(plans) != 2 {
		t.Fatalf("PlansByGroup: %+v %v", plans, err)
	}
	if plans[0].ID != jan.ID || plans[1].ID != feb.ID || !approxEqual(plans[1].DemandQty, 120) || plans[1].Notes != "revised" {
		t.Fatalf("plans: %+v", plans)
	}
	all, err := sop.ListPlans(f.ctx)
	if err != nil || len(all) < 2 {
		t.Fatalf("ListPlans: %d %v", len(all), err)
	}
	if err := sop.DeletePlan(f.ctx, jan.ID); err != nil {
		t.Fatal(err)
	}
	if plans, _ = sop.PlansByGroup(f.ctx, g.ID); len(plans) != 1 {
		t.Fatalf("after delete: %+v", plans)
	}
	if err := sop.UpsertPlan(f.ctx, &domain.SOPPlan{GroupID: uuid.New(), PlanMonth: date(2027, 3, 1)}); err == nil {
		t.Fatal("plan for unknown group must violate FK")
	}

	it := f.item(t, "FG")
	wc := f.workCenter(t, 1, 1)
	p := &domain.RCCPProfile{ItemID: it.ID, WorkCenterID: wc.ID, MinutesPerUnit: 3}
	if err := sop.UpsertProfile(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := sop.UpsertProfile(f.ctx, &domain.RCCPProfile{ItemID: it.ID, WorkCenterID: wc.ID, MinutesPerUnit: 4.5}); err != nil {
		t.Fatal(err)
	}
	if err := sop.UpsertProfile(f.ctx, &domain.RCCPProfile{ItemID: it.ID, WorkCenterID: wc.ID, MinutesPerUnit: -1}); err == nil {
		t.Fatal("negative minutes per unit must be rejected")
	}
	profiles, err := sop.ListProfiles(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range profiles {
		if x.ItemID == it.ID && x.WorkCenterID == wc.ID {
			n++
			if x.ID != p.ID || !approxEqual(x.MinutesPerUnit, 4.5) {
				t.Fatalf("profile upsert: %+v", x)
			}
		}
	}
	if n != 1 {
		t.Fatalf("expected one profile per item/work center, got %d", n)
	}
}

func TestECOLifecycleGuards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ecos := f.repos.ECO
	requester := f.user(t, domain.RolePlanner)
	admin := f.user(t, domain.RoleAdmin)
	parent := f.item(t, "FG")
	child := f.item(t, "RM")

	e := &domain.EngineeringChange{ECONo: uniq("ECO"), Title: "swap part", EffectiveDate: date(2026, 12, 1),
		RequestedBy: requester.Username, RequestedByUserID: &requester.ID}
	if err := ecos.Create(f.ctx, e); err != nil {
		t.Fatal(err)
	}
	if e.Status != "DRAFT" || e.CreatedAt.IsZero() {
		t.Fatalf("defaults: %+v", e)
	}
	// New ECOs must start as clean DRAFTs.
	wantErrContains(t, ecos.Create(f.ctx, &domain.EngineeringChange{ECONo: uniq("ECO"), Title: "x", Status: "APPROVED", EffectiveDate: date(2026, 12, 1)}), "new ECO must be DRAFT")
	wantErrContains(t, ecos.Create(f.ctx, &domain.EngineeringChange{ECONo: uniq("ECO"), Title: "x", ApprovedBy: "someone", EffectiveDate: date(2026, 12, 1)}), "audit fields")
	if err := ecos.Create(f.ctx, &domain.EngineeringChange{ECONo: e.ECONo, Title: "dup", EffectiveDate: date(2026, 12, 1)}); err == nil {
		t.Fatal("duplicate eco_no must be rejected")
	}

	hist, err := ecos.ListHistory(f.ctx, e.ID)
	if err != nil || len(hist) != 1 || hist[0].FromStatus != "" || hist[0].ToStatus != "DRAFT" || hist[0].ActorUsername != requester.Username {
		t.Fatalf("creation must be audited: %+v %v", hist, err)
	}

	c := &domain.ECOComponent{ECOID: e.ID, Action: "ADD", ParentID: parent.ID, ChildID: child.ID, NewQuantity: 2, NewScrapPct: 0.05}
	if err := ecos.AddComponent(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := ecos.AddComponent(f.ctx, &domain.ECOComponent{ECOID: e.ID, Action: "RENAME", ParentID: parent.ID, ChildID: child.ID}); err == nil {
		t.Fatal("unknown action must be rejected")
	}
	if err := ecos.AddComponent(f.ctx, &domain.ECOComponent{ECOID: e.ID, Action: "MODIFY", ParentID: parent.ID, ChildID: child.ID, NewScrapPct: 1.5}); err == nil {
		t.Fatal("scrap pct > 1 must be rejected")
	}
	comps, err := ecos.ListComponents(f.ctx, e.ID)
	if err != nil || len(comps) != 1 || comps[0].ID != c.ID {
		t.Fatalf("ListComponents: %+v %v", comps, err)
	}

	// Only an active admin whose username matches may approve.
	_, err = f.db.Exec(`UPDATE engineering_changes SET status='APPROVED', approved_by=$2, approved_by_user_id=$3, approved_at=now() WHERE id=$1`,
		e.ID, requester.Username, requester.ID)
	wantErrContains(t, err, "must be admin")
	_, err = f.db.Exec(`UPDATE engineering_changes SET status='APPLIED', applied_by=$2, applied_by_user_id=$3, applied_at=now() WHERE id=$1`,
		e.ID, admin.Username, admin.ID)
	wantErrContains(t, err, "invalid ECO status transition")
	f.db.MustExec(`UPDATE engineering_changes SET status='APPROVED', approved_by=$2, approved_by_user_id=$3, approved_at=now() WHERE id=$1`,
		e.ID, admin.Username, admin.ID)

	got, err := ecos.Get(f.ctx, e.ID)
	if err != nil || got.Status != "APPROVED" || got.ApprovedAt == nil || got.ApprovedBy != admin.Username {
		t.Fatalf("Get approved: %+v %v", got, err)
	}
	// Components and header are frozen after approval; history is append-only.
	wantErrContains(t, ecos.AddComponent(f.ctx, &domain.ECOComponent{ECOID: e.ID, Action: "REMOVE", ParentID: parent.ID, ChildID: child.ID}), "immutable after approval")
	_, err = f.db.Exec(`UPDATE engineering_changes SET title='changed' WHERE id=$1`, e.ID)
	wantErrContains(t, err, "immutable after leaving DRAFT")
	_, err = f.db.Exec(`DELETE FROM eco_status_history WHERE eco_id=$1`, e.ID)
	wantErrContains(t, err, "append-only")

	hist, _ = ecos.ListHistory(f.ctx, e.ID)
	if len(hist) != 2 || hist[1].FromStatus != "DRAFT" || hist[1].ToStatus != "APPROVED" || hist[1].ActorUserID == nil || *hist[1].ActorUserID != admin.ID {
		t.Fatalf("approval history: %+v", hist)
	}

	list, err := ecos.List(f.ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("List: %d %v", len(list), err)
	}
	if _, err := ecos.Get(f.ctx, uuid.New()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown: %v", err)
	}
}

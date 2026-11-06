package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func opsLLC(t *testing.T, f *dbFixture, id uuid.UUID) int {
	t.Helper()
	it, err := f.Svc.Items.Get(f.Ctx, id)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	return it.LowLevelCode
}

func opsBOMEdge(t *testing.T, f *dbFixture, parent, child uuid.UUID) *domain.BOMComponent {
	t.Helper()
	comps, err := f.Svc.BOM.ComponentsOf(f.Ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	for i := range comps {
		if comps[i].ChildID == child {
			return &comps[i]
		}
	}
	return nil
}

func TestOpsBOMAddDeleteCycleAndLLC(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	a := opsItem(t, f, opsCode("A"), domain.ItemTypeFinished)
	b := opsItem(t, f, opsCode("B"), domain.ItemTypeSubAssembly)
	c := opsItem(t, f, opsCode("C"), domain.ItemTypeRawMaterial)

	tests := []struct {
		name string
		in   domain.BOMComponent
		code domain.ErrorCode
	}{
		{"missing ids", domain.BOMComponent{Quantity: 1}, domain.ErrCodeBadRequest},
		{"self reference", domain.BOMComponent{ParentID: a.ID, ChildID: a.ID, Quantity: 1}, domain.ErrCodeConflict},
		{"zero qty", domain.BOMComponent{ParentID: a.ID, ChildID: b.ID}, domain.ErrCodeBadRequest},
		{"negative scrap", domain.BOMComponent{ParentID: a.ID, ChildID: b.ID, Quantity: 1, ScrapPct: -0.1}, domain.ErrCodeBadRequest},
	}
	for _, tc := range tests {
		in := tc.in
		opsWantErr(t, f.Svc.BOM.Add(f.Ctx, &in), tc.code, "")
	}

	ab := opsBOM(t, f, a.ID, b.ID, 2, 0)
	opsBOM(t, f, b.ID, c.ID, 3, 0.5)
	if la, lb, lc := opsLLC(t, f, a.ID), opsLLC(t, f, b.ID), opsLLC(t, f, c.ID); la != 0 || lb != 1 || lc != 2 {
		t.Fatalf("LLC = %d/%d/%d, want 0/1/2", la, lb, lc)
	}
	// A direct edge A->C keeps C at its deepest level.
	ac := opsBOM(t, f, a.ID, c.ID, 1, 0)
	if lc := opsLLC(t, f, c.ID); lc != 2 {
		t.Fatalf("C LLC with shallow edge = %d, want 2", lc)
	}
	// Duplicate edges are rejected by the unique key.
	if err := f.Svc.BOM.Add(f.Ctx, &domain.BOMComponent{ParentID: a.ID, ChildID: b.ID, Quantity: 1}); err == nil {
		t.Fatalf("duplicate BOM edge must fail")
	}

	// C -> A closes a cycle: rejected and not persisted.
	err := f.Svc.BOM.Add(f.Ctx, &domain.BOMComponent{ParentID: c.ID, ChildID: a.ID, Quantity: 1})
	opsWantErr(t, err, domain.ErrCodeConflict, "cycle")
	if e := opsBOMEdge(t, f, c.ID, a.ID); e != nil {
		t.Fatalf("cyclic edge persisted: %+v", e)
	}

	// Multi-level explosion: A(10) -> B 20 -> C 20*3*1.5=90, plus A->C 10.
	rows, err := f.Svc.BOM.Explode(f.Ctx, a.ID, 10)
	if err != nil {
		t.Fatalf("explode: %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("explosion rows: %+v", rows)
	}

	// Delete A->C and A->B; C drops back to level 2 then B becomes top level.
	if err := f.Svc.BOM.Delete(f.Ctx, ac.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := f.Svc.BOM.Delete(f.Ctx, ab.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if lb, lc := opsLLC(t, f, b.ID), opsLLC(t, f, c.ID); lb != 0 || lc != 1 {
		t.Fatalf("LLC after deletes = %d/%d, want 0/1", lb, lc)
	}
	opsWantErr(t, f.Svc.BOM.Delete(f.Ctx, ab.ID), domain.ErrCodeNotFound, "BOM component")
	if err := f.Svc.Items.RecomputeLLC(f.Ctx); err != nil {
		t.Fatalf("recompute LLC: %v", err)
	}
}

func TestOpsItemServiceCRUD(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	it := opsItem(t, f, opsCode("IT"), domain.ItemTypePurchasedPart)
	if it.LotSizeMethod != "LFL" || it.PoqPeriods != 1 || !opsNear(it.HoldingCostPct, 0.2) {
		t.Fatalf("create defaults: %+v", it)
	}
	it.Name = "Renamed"
	it.LotSizeMethod = "POQ"
	it.PoqPeriods = 3
	if err := f.Svc.Items.Update(f.Ctx, it); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := f.Svc.Items.Get(f.Ctx, it.ID)
	if err != nil || got.Name != "Renamed" || got.LotSizeMethod != "POQ" || got.PoqPeriods != 3 {
		t.Fatalf("after update: %+v err=%v", got, err)
	}
	// A DB CHECK rejects an invalid lot-size method.
	got.LotSizeMethod = "MAGIC"
	if err := f.Svc.Items.Update(f.Ctx, got); err == nil {
		t.Fatalf("invalid lot_size_method must be rejected")
	}
	// Duplicate code.
	if err := f.Svc.Items.Create(f.Ctx, &domain.Item{Code: it.Code, Name: "x", Type: domain.ItemTypeRawMaterial, UoM: "EA", LotSize: 1}); err == nil {
		t.Fatalf("duplicate item code must fail")
	}
	// An item referenced by stock cannot be deleted; an unused one can.
	opsStock(t, f, it.ID, 1, opsCode("L"))
	if err := f.Svc.Items.Delete(f.Ctx, it.ID); err == nil {
		t.Fatalf("deleting an item with ledger history must fail")
	}
	spare := opsItem(t, f, opsCode("SPARE"), domain.ItemTypeRawMaterial)
	if err := f.Svc.Items.Delete(f.Ctx, spare.ID); err != nil {
		t.Fatalf("delete unused item: %v", err)
	}
	if _, err := f.Svc.Items.Get(f.Ctx, spare.ID); err == nil {
		t.Fatalf("deleted item still readable")
	}
	list, err := f.Svc.Items.List(f.Ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v", err)
	}

	// Legacy demand forecasts are read-only through the service.
	opsWantErr(t, f.Svc.Demand.Create(f.Ctx, &domain.DemandForecast{}), domain.ErrCodeConflict, "legacy")
	if _, err := f.Svc.Demand.List(f.Ctx); err != nil {
		t.Fatalf("demand list: %v", err)
	}
	if err := f.Svc.MPS.Upsert(f.Ctx, &domain.MPSEntry{ItemID: it.ID, Period: BusinessToday(), Planned: 5}); err != nil {
		t.Fatalf("mps upsert: %v", err)
	}
	mps, err := f.Svc.MPS.List(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range mps {
		found = found || (m.ItemID == it.ID && opsNear(m.Planned, 5))
	}
	if !found {
		t.Fatalf("MPS entry not listed")
	}
}

func TestOpsECOLifecycle(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	admin := f.user(t, opsCode("eco-admin"), domain.RoleAdmin)
	planner := f.user(t, opsCode("eco-planner"), domain.RolePlanner)
	adm := ECOActor{UserID: admin.ID, Username: admin.Username}
	pln := ECOActor{UserID: planner.ID, Username: planner.Username}

	fg := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	old := opsItem(t, f, opsCode("OLD"), domain.ItemTypeRawMaterial)
	keep := opsItem(t, f, opsCode("KEEP"), domain.ItemTypeRawMaterial)
	newPart := opsItem(t, f, opsCode("NEW"), domain.ItemTypeRawMaterial)
	opsBOM(t, f, fg.ID, old.ID, 1, 0)
	opsBOM(t, f, fg.ID, keep.ID, 2, 0)
	past := BusinessToday().AddDate(0, 0, -2)

	// Create validation and requester derivation.
	opsWantErr(t, f.Svc.ECO.Create(f.Ctx, &domain.EngineeringChange{ECONo: "x", Title: "x", EffectiveDate: past}, ECOActor{}), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, f.Svc.ECO.Create(f.Ctx, &domain.EngineeringChange{ECONo: "x"}, pln), domain.ErrCodeBadRequest, "required")
	eco := &domain.EngineeringChange{ECONo: opsCode("ECO"), Title: "Swap part", EffectiveDate: past, Status: "APPLIED", RequestedBy: "spoofed"}
	if err := f.Svc.ECO.Create(f.Ctx, eco, pln); err != nil {
		t.Fatalf("create ECO: %v", err)
	}
	got, err := f.Svc.ECO.Get(f.Ctx, eco.ID)
	if err != nil || got.Status != "DRAFT" || got.RequestedBy != planner.Username {
		t.Fatalf("created ECO must be DRAFT by the actor: %+v err=%v", got, err)
	}

	// Approval needs components; component action is validated.
	opsWantErr(t, f.Svc.ECO.Approve(f.Ctx, eco.ID, adm), domain.ErrCodeBadRequest, "no components")
	opsWantErr(t, f.Svc.ECO.AddComponent(f.Ctx, &domain.ECOComponent{ECOID: eco.ID, Action: "SWAP"}), domain.ErrCodeBadRequest, "ADD/REMOVE/MODIFY")
	opsWantErr(t, f.Svc.ECO.AddComponent(f.Ctx, &domain.ECOComponent{ECOID: uuid.New(), Action: "ADD"}), domain.ErrCodeNotFound, "eco")
	for _, c := range []*domain.ECOComponent{
		{ECOID: eco.ID, Action: "REMOVE", ParentID: fg.ID, ChildID: old.ID},
		{ECOID: eco.ID, Action: "ADD", ParentID: fg.ID, ChildID: newPart.ID, NewQuantity: 3, NewScrapPct: 0.05},
		{ECOID: eco.ID, Action: "MODIFY", ParentID: fg.ID, ChildID: keep.ID, NewQuantity: 4},
	} {
		if err := f.Svc.ECO.AddComponent(f.Ctx, c); err != nil {
			t.Fatalf("add ECO component %s: %v", c.Action, err)
		}
	}
	comps, err := f.Svc.ECO.ListComponents(f.Ctx, eco.ID)
	if err != nil || len(comps) != 3 {
		t.Fatalf("components: %d err=%v", len(comps), err)
	}

	// Apply before approval is rejected; only admins may approve (DB trigger).
	opsWantErr(t, f.Svc.ECO.Apply(f.Ctx, eco.ID, adm), domain.ErrCodeConflict, "must be APPROVED")
	opsWantDBErr(t, f.Svc.ECO.Approve(f.Ctx, eco.ID, pln), "must be admin")
	opsWantErr(t, f.Svc.ECO.Approve(f.Ctx, eco.ID, ECOActor{}), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, f.Svc.ECO.Approve(f.Ctx, uuid.New(), adm), domain.ErrCodeNotFound, "eco")
	if err := f.Svc.ECO.Approve(f.Ctx, eco.ID, adm); err != nil {
		t.Fatalf("approve: %v", err)
	}
	opsWantErr(t, f.Svc.ECO.Approve(f.Ctx, eco.ID, adm), domain.ErrCodeConflict, "must be DRAFT")
	opsWantErr(t, f.Svc.ECO.AddComponent(f.Ctx, &domain.ECOComponent{ECOID: eco.ID, Action: "ADD", ParentID: fg.ID, ChildID: old.ID, NewQuantity: 1}), domain.ErrCodeConflict, "immutable")
	// Approved content is immutable in the DB as well.
	_, err = f.DB.ExecContext(f.Ctx, `UPDATE eco_components SET new_quantity=99 WHERE eco_id=$1`, eco.ID)
	opsWantDBErr(t, err, "immutable")

	// Apply: BOM reflects all three actions atomically.
	opsWantErr(t, f.Svc.ECO.Apply(f.Ctx, eco.ID, ECOActor{}), domain.ErrCodeUnauthorized, "")
	if err := f.Svc.ECO.Apply(f.Ctx, eco.ID, adm); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if e := opsBOMEdge(t, f, fg.ID, old.ID); e != nil {
		t.Fatalf("REMOVE not applied")
	}
	if e := opsBOMEdge(t, f, fg.ID, newPart.ID); e == nil || !opsNear(e.Quantity, 3) || !opsNear(e.ScrapPct, 0.05) {
		t.Fatalf("ADD not applied: %+v", e)
	}
	if e := opsBOMEdge(t, f, fg.ID, keep.ID); e == nil || !opsNear(e.Quantity, 4) {
		t.Fatalf("MODIFY not applied: %+v", e)
	}
	if opsLLC(t, f, newPart.ID) != 1 {
		t.Fatalf("LLC must be recomputed for new component")
	}
	opsWantErr(t, f.Svc.ECO.Apply(f.Ctx, eco.ID, adm), domain.ErrCodeConflict, "must be APPROVED")
	opsWantErr(t, f.Svc.ECO.Cancel(f.Ctx, eco.ID, adm), domain.ErrCodeConflict, "cannot be cancelled")
	hist, err := f.Svc.ECO.ListHistory(f.Ctx, eco.ID)
	if err != nil || len(hist) != 3 {
		t.Fatalf("ECO history: %+v err=%v", hist, err)
	}
	_, err = f.DB.ExecContext(f.Ctx, `DELETE FROM eco_status_history WHERE eco_id=$1`, eco.ID)
	opsWantDBErr(t, err, "append-only")
	list, err := f.Svc.ECO.List(f.Ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ECO list: %d err=%v", len(list), err)
	}

	// Cancel works from DRAFT; canceller identity is required.
	draft := &domain.EngineeringChange{ECONo: opsCode("ECO"), Title: "Drop", EffectiveDate: past}
	if err := f.Svc.ECO.Create(f.Ctx, draft, pln); err != nil {
		t.Fatal(err)
	}
	opsWantErr(t, f.Svc.ECO.Cancel(f.Ctx, draft.ID, ECOActor{}), domain.ErrCodeUnauthorized, "")
	opsWantErr(t, f.Svc.ECO.Cancel(f.Ctx, uuid.New(), adm), domain.ErrCodeNotFound, "eco")
	if err := f.Svc.ECO.Cancel(f.Ctx, draft.ID, adm); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got, _ := f.Svc.ECO.Get(f.Ctx, draft.ID); got.Status != "CANCELLED" || got.CancelledBy != admin.Username {
		t.Fatalf("cancelled ECO: %+v", got)
	}
}

func TestOpsECOApplyRollsBack(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	admin := f.user(t, opsCode("eco-admin"), domain.RoleAdmin)
	adm := ECOActor{UserID: admin.ID, Username: admin.Username}
	a := opsItem(t, f, opsCode("A"), domain.ItemTypeFinished)
	b := opsItem(t, f, opsCode("B"), domain.ItemTypeSubAssembly)
	ghost := opsItem(t, f, opsCode("G"), domain.ItemTypeRawMaterial)
	opsBOM(t, f, a.ID, b.ID, 1, 0)

	approved := func(effective time.Time, comps ...domain.ECOComponent) *domain.EngineeringChange {
		t.Helper()
		e := &domain.EngineeringChange{ECONo: opsCode("ECO"), Title: "t", EffectiveDate: effective}
		if err := f.Svc.ECO.Create(f.Ctx, e, adm); err != nil {
			t.Fatal(err)
		}
		for i := range comps {
			comps[i].ECOID = e.ID
			if err := f.Svc.ECO.AddComponent(f.Ctx, &comps[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Svc.ECO.Approve(f.Ctx, e.ID, adm); err != nil {
			t.Fatal(err)
		}
		return e
	}
	past := BusinessToday().AddDate(0, 0, -2)

	// Negative scrap is rejected by a CHECK constraint before it can be approved.
	draft := &domain.EngineeringChange{ECONo: opsCode("ECO"), Title: "t", EffectiveDate: past}
	if err := f.Svc.ECO.Create(f.Ctx, draft, adm); err != nil {
		t.Fatal(err)
	}
	err := f.Svc.ECO.AddComponent(f.Ctx, &domain.ECOComponent{ECOID: draft.ID, Action: "MODIFY", ParentID: a.ID, ChildID: b.ID, NewQuantity: 1, NewScrapPct: -1})
	opsWantDBErr(t, err, "new_scrap_pct")

	cases := []struct {
		name string
		eco  *domain.EngineeringChange
		code domain.ErrorCode
		want string
	}{
		{"not yet effective", approved(BusinessToday().AddDate(0, 1, 0),
			domain.ECOComponent{Action: "MODIFY", ParentID: a.ID, ChildID: b.ID, NewQuantity: 9}), domain.ErrCodeConflict, "not effective"},
		{"cycle", approved(past,
			domain.ECOComponent{Action: "MODIFY", ParentID: a.ID, ChildID: b.ID, NewQuantity: 9},
			domain.ECOComponent{Action: "ADD", ParentID: b.ID, ChildID: a.ID, NewQuantity: 1}), domain.ErrCodeConflict, "cycle"},
		{"remove missing edge", approved(past,
			domain.ECOComponent{Action: "MODIFY", ParentID: a.ID, ChildID: b.ID, NewQuantity: 9},
			domain.ECOComponent{Action: "REMOVE", ParentID: a.ID, ChildID: ghost.ID}), domain.ErrCodeConflict, "does not exist"},
		{"modify missing edge", approved(past,
			domain.ECOComponent{Action: "MODIFY", ParentID: b.ID, ChildID: ghost.ID, NewQuantity: 2}), domain.ErrCodeConflict, "does not exist"},
		{"add zero qty", approved(past,
			domain.ECOComponent{Action: "ADD", ParentID: b.ID, ChildID: ghost.ID}), domain.ErrCodeBadRequest, "newQuantity"},
		{"self reference", approved(past,
			domain.ECOComponent{Action: "ADD", ParentID: b.ID, ChildID: b.ID, NewQuantity: 1}), domain.ErrCodeConflict, "self-reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opsWantErr(t, f.Svc.ECO.Apply(f.Ctx, tc.eco.ID, adm), tc.code, tc.want)
			got, err := f.Svc.ECO.Get(f.Ctx, tc.eco.ID)
			if err != nil || got.Status != "APPROVED" {
				t.Fatalf("failed apply must leave ECO APPROVED: %+v err=%v", got, err)
			}
			if e := opsBOMEdge(t, f, a.ID, b.ID); e == nil || !opsNear(e.Quantity, 1) {
				t.Fatalf("failed apply must roll back BOM changes: %+v", e)
			}
			if e := opsBOMEdge(t, f, b.ID, a.ID); e != nil {
				t.Fatalf("cyclic edge persisted")
			}
		})
	}
	// An APPROVED ECO can still be cancelled.
	if err := f.Svc.ECO.Cancel(f.Ctx, cases[0].eco.ID, adm); err != nil {
		t.Fatalf("cancel approved: %v", err)
	}
}

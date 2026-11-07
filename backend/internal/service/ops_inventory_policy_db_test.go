package service

import (
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestOpsInventoryPolicyVersionValidation(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := opsSOActor(t, f, domain.RolePlanner)
	viewer := opsSOActor(t, f, domain.RoleViewer)
	item := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	ip := f.Svc.InventoryPolicy

	_, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID}, SalesOrderActor{})
	opsWantErr(t, err, domain.ErrCodeUnauthorized, "")
	_, err = ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID}, viewer)
	opsWantErr(t, err, domain.ErrCodeForbidden, "planner/admin")
	neg := -1.0
	tests := []struct {
		name string
		in   InventoryPolicyVersionInput
		want string
	}{
		{"no item", InventoryPolicyVersionInput{}, "itemId"},
		{"method", InventoryPolicyVersionInput{ItemID: item.ID, PolicyMethod: "GUESS"}, "policyMethod"},
		{"replenishment", InventoryPolicyVersionInput{ItemID: item.ID, ReplenishmentMethod: "KANBAN"}, "replenishmentMethod"},
		{"service level low", InventoryPolicyVersionInput{ItemID: item.ID, ServiceLevel: 0.3}, "serviceLevel"},
		{"service level high", InventoryPolicyVersionInput{ItemID: item.ID, ServiceLevel: 1}, "serviceLevel"},
		{"window", InventoryPolicyVersionInput{ItemID: item.ID, DemandWindowDays: 3}, "demandWindowDays"},
		{"min history", InventoryPolicyVersionInput{ItemID: item.ID, DemandWindowDays: 10, MinHistoryDays: 11}, "minHistoryDays"},
		{"order cycle", InventoryPolicyVersionInput{ItemID: item.ID, OrderCycleDays: 400}, "orderCycleDays"},
		{"fixed without ss", InventoryPolicyVersionInput{ItemID: item.ID, PolicyMethod: "fixed"}, "fixedSafetyStock"},
		{"negative ss", InventoryPolicyVersionInput{ItemID: item.ID, FixedSafetyStock: &neg}, "fixedSafetyStock"},
		{"bad date", InventoryPolicyVersionInput{ItemID: item.ID, EffectiveFrom: "11/06/2026"}, "YYYY-MM-DD"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ip.CreateVersion(f.Ctx, tc.in, planner)
			opsWantErr(t, err, domain.ErrCodeBadRequest, tc.want)
		})
	}
	_, err = ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: uuid.New()}, planner)
	opsWantErr(t, err, domain.ErrCodeNotFound, "item")

	// Defaults are applied and versions number sequentially.
	v1, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID, Notes: " first "}, planner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v1.VersionNo != 1 || v1.Status != "DRAFT" || v1.PolicyMethod != "STATISTICAL" || v1.ReplenishmentMethod != "MIN_MAX" ||
		v1.ServiceLevel != defaultInventoryPolicyServiceLevel || v1.DemandWindowDays != 90 || v1.MinHistoryDays != 30 ||
		v1.OrderCycleDays != 14 || v1.Notes != "first" || v1.ItemCode != item.Code {
		t.Fatalf("defaults: %+v", v1)
	}
	future, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID, EffectiveFrom: opsDay(30)}, planner)
	if err != nil || future.VersionNo != 2 {
		t.Fatalf("future version: %+v err=%v", future, err)
	}
	_, err = ip.ActivateVersion(f.Ctx, future.ID, planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "future-effective")
	_, err = ip.ActivateVersion(f.Ctx, uuid.New(), planner)
	opsWantErr(t, err, domain.ErrCodeNotFound, "inventory policy")
	_, err = ip.ActivateVersion(f.Ctx, v1.ID, viewer)
	opsWantErr(t, err, domain.ErrCodeForbidden, "")
	_, err = ip.ArchiveVersion(f.Ctx, v1.ID, planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "only ACTIVE")
	_, err = ip.ArchiveVersion(f.Ctx, v1.ID, viewer)
	opsWantErr(t, err, domain.ErrCodeForbidden, "")
	_, err = ip.GetVersion(f.Ctx, uuid.New())
	opsWantErr(t, err, domain.ErrCodeNotFound, "inventory policy")
	_, err = ip.GetRun(f.Ctx, uuid.New())
	opsWantErr(t, err, domain.ErrCodeNotFound, "inventory policy run")
	_, err = ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{AsOfDate: "bad"}, planner)
	opsWantErr(t, err, domain.ErrCodeBadRequest, "YYYY-MM-DD")
	_, err = ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{}, viewer)
	opsWantErr(t, err, domain.ErrCodeForbidden, "")
}

func TestOpsInventoryPolicyLifecycleAndRefresh(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := opsSOActor(t, f, domain.RolePlanner)
	admin := opsSOActor(t, f, domain.RoleAdmin)
	ip := f.Svc.InventoryPolicy

	item := opsItem(t, f, opsCode("FG"), domain.ItemTypeFinished)
	item.SafetyStock = 3
	item.LeadTimeDays = 4
	if err := f.Svc.Items.Update(f.Ctx, item); err != nil {
		t.Fatal(err)
	}

	// Without a policy the item master is the fallback.
	eff, err := ip.Effective(f.Ctx, *item)
	if err != nil || eff.PolicyMethod != "LEGACY_FIXED" || eff.SafetyStock != 3 || eff.CalculationStatus != "ITEM_MASTER" {
		t.Fatalf("fallback policy: %+v err=%v", eff, err)
	}

	// Issue history: 2 units on even days, 4 on odd days over the last 20 days.
	opsStock(t, f, item.ID, 1000, opsCode("L"))
	asOf := BusinessToday().AddDate(0, 0, -1)
	for d := 0; d < 20; d++ {
		q := 2.0
		if d%2 == 1 {
			q = 4
		}
		at := asOf.AddDate(0, 0, -d).Add(12 * time.Hour)
		if _, err := f.Svc.Inventory.ledger.Post(f.Ctx, PhysicalInventoryRequest{ItemID: item.ID, Quantity: -q, TxnType: "ISSUE", RefDoc: "HIST", OccurredAt: at}); err != nil {
			t.Fatalf("history issue: %v", err)
		}
	}

	// v1: FIXED safety stock, activated and calculated.
	fixed := 7.0
	v1, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID, PolicyMethod: "FIXED", ReplenishmentMethod: "SAFETY_STOCK", FixedSafetyStock: &fixed, EffectiveFrom: opsDay(-5)}, planner)
	if err != nil {
		t.Fatal(err)
	}
	act, err := ip.ActivateVersion(f.Ctx, v1.ID, planner)
	if err != nil || act.Status != "ACTIVE" || act.ActivatedBy == nil || *act.ActivatedBy != planner.Username {
		t.Fatalf("activate v1: %+v err=%v", act, err)
	}
	_, err = ip.ActivateVersion(f.Ctx, v1.ID, planner)
	opsWantErr(t, err, domain.ErrCodeConflict, "only DRAFT")
	// Activated but not yet calculated: never MIN_MAX.
	if eff, _ := ip.Effective(f.Ctx, *item); eff.ReplenishmentMethod != "SAFETY_STOCK" || eff.PolicyVersionID == nil || *eff.PolicyVersionID != v1.ID {
		t.Fatalf("uncalculated effective policy: %+v", eff)
	}

	asOfStr := asOf.Format("2006-01-02")
	run1, err := ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{AsOfDate: asOfStr}, planner)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	r := opsPolicyResult(t, run1, item.ID)
	if run1.Run.Status != "COMPLETE" || run1.Run.ResultHash == nil || r.DemandSource != "FIXED_POLICY" || r.SafetyStock != 7 {
		t.Fatalf("fixed refresh: run=%+v result=%+v", run1.Run, r)
	}
	// History starts 20 days ago (first transaction), so the 90-day window
	// observes 20 days averaging 3 — below MinHistoryDays (30), hence LOW.
	if !opsNear(r.AverageDailyDemand, 3) || r.DemandObservationDays != 20 || r.NonzeroDemandDays != 20 || r.LeadTimeMeanDays != 4 || r.LeadTimeSource != "ITEM_MASTER" {
		t.Fatalf("demand statistics: %+v", r)
	}
	if !opsNear(r.ReorderPoint, r.AverageDailyDemand*4+7) || !opsNear(r.MaxQty, r.MinQty) || r.Confidence != "LOW" {
		t.Fatalf("SAFETY_STOCK policy ROP/min/max: %+v", r)
	}
	eff, err = ip.Effective(f.Ctx, *item)
	if err != nil || eff.CalculationStatus != "CALCULATED" || eff.SafetyStock != 7 {
		t.Fatalf("calculated effective: %+v err=%v", eff, err)
	}
	// Refresh is deterministic for the same as-of date and inputs.
	run2, err := ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{AsOfDate: asOfStr}, admin)
	if err != nil || run2.Run.ResultHash == nil || *run2.Run.ResultHash != *run1.Run.ResultHash {
		t.Fatalf("non-deterministic refresh: %v vs %v err=%v", run1.Run.ResultHash, run2.Run.ResultHash, err)
	}

	// v2: STATISTICAL MIN_MAX replaces v1 (v1 archived).
	v2, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: item.ID, ServiceLevel: 0.9, DemandWindowDays: 20, MinHistoryDays: 10, OrderCycleDays: 7, EffectiveFrom: opsDay(-5)}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ip.ActivateVersion(f.Ctx, v2.ID, admin); err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	if old, _ := ip.GetVersion(f.Ctx, v1.ID); old.Status != "ARCHIVED" || old.ArchivedBy == nil {
		t.Fatalf("v1 must be archived on v2 activation: %+v", old)
	}
	run3, err := ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{AsOfDate: asOfStr}, planner)
	if err != nil {
		t.Fatal(err)
	}
	r = opsPolicyResult(t, run3, item.ID)
	_, wantSS := statisticalSafetyStock(0.9, r.AverageDailyDemand, r.StddevDailyDemand, 4, 0)
	if r.DemandSource != "ISSUE_HISTORY" || !opsNear(r.AverageDailyDemand, 3) || !opsNear(r.StddevDailyDemand, 1) {
		t.Fatalf("statistical demand: %+v", r)
	}
	if !opsNear(r.SafetyStock, wantSS) || math.Abs(r.SafetyStock-1.2816*2) > 0.01 {
		t.Fatalf("safety stock %.4f, want %.4f (z(0.9)*sigma*sqrt(L))", r.SafetyStock, wantSS)
	}
	if !opsNear(r.ReorderPoint, 3*4+r.SafetyStock) || !opsNear(r.MaxQty, r.ReorderPoint+3*7) {
		t.Fatalf("MIN_MAX ROP/max: %+v", r)
	}
	eff, _ = ip.Effective(f.Ctx, *item)
	if eff.ReplenishmentMethod != "MIN_MAX" || eff.CalculationStatus != "CALCULATED" || !opsNear(eff.ReorderPoint, r.ReorderPoint) {
		t.Fatalf("effective MIN_MAX policy: %+v", eff)
	}
	// MIN_MAX netting: below ROP orders up to MaxQty; at/above ROP no order.
	net, planned, projected := netMRPBucketWithInventoryPolicy(eff.ReorderPoint-1, 0, 0, eff, 1, 0, LotMethodLFL)
	if !opsNear(net, eff.MaxQty-(eff.ReorderPoint-1)) || !opsNear(projected, eff.MaxQty) || planned <= 0 {
		t.Fatalf("MIN_MAX netting below ROP: net=%.2f planned=%.2f projected=%.2f", net, planned, projected)
	}
	if _, planned, _ := netMRPBucketWithInventoryPolicy(eff.ReorderPoint+5, 5, 0, eff, 1, 0, LotMethodLFL); planned != 0 {
		t.Fatalf("at ROP no order expected, got %.2f", planned)
	}

	cur, err := ip.Current(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cur {
		found = found || (c.ItemID == item.ID && c.PolicyVersionID != nil && *c.PolicyVersionID == v2.ID)
	}
	if !found {
		t.Fatalf("Current() missing v2 for item")
	}
	vs, err := ip.ListVersions(f.Ctx, &item.ID)
	if err != nil || len(vs) != 2 || vs[0].VersionNo != 2 {
		t.Fatalf("list versions: %+v err=%v", vs, err)
	}
	if all, err := ip.ListVersions(f.Ctx, nil); err != nil || len(all) < 2 {
		t.Fatalf("list all versions: %d err=%v", len(all), err)
	}
	runs, err := ip.ListRuns(f.Ctx)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs: %d err=%v", len(runs), err)
	}
	arch, err := ip.ArchiveVersion(f.Ctx, v2.ID, planner)
	if err != nil || arch.Status != "ARCHIVED" {
		t.Fatalf("archive: %+v err=%v", arch, err)
	}
	if eff, _ := ip.Effective(f.Ctx, *item); eff.PolicyMethod != "LEGACY_FIXED" {
		t.Fatalf("archived policy must fall back to item master: %+v", eff)
	}
}

func opsPolicyResult(t *testing.T, run *domain.InventoryPolicyRunResult, itemID uuid.UUID) domain.InventoryPolicyResult {
	t.Helper()
	for _, r := range run.Results {
		if r.ItemID == itemID {
			return r
		}
	}
	t.Fatalf("no policy result for item %s in run %+v", itemID, run.Run)
	return domain.InventoryPolicyResult{}
}

func TestOpsInventoryPolicyPurchasedItemWithoutHistory(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	planner := opsSOActor(t, f, domain.RolePlanner)
	ip := f.Svc.InventoryPolicy
	rm := opsItem(t, f, opsCode("RM"), domain.ItemTypeRawMaterial)
	rm.SafetyStock = 5
	rm.LeadTimeDays = 6
	if err := f.Svc.Items.Update(f.Ctx, rm); err != nil {
		t.Fatal(err)
	}
	v, err := ip.CreateVersion(f.Ctx, InventoryPolicyVersionInput{ItemID: rm.ID, ReplenishmentMethod: "safety_stock", EffectiveFrom: opsDay(-1)}, planner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ip.ActivateVersion(f.Ctx, v.ID, planner); err != nil {
		t.Fatal(err)
	}
	run, err := ip.Refresh(f.Ctx, InventoryPolicyRefreshInput{}, planner)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	r := opsPolicyResult(t, run, rm.ID)
	// Purchased item without supplier receipts: item-master lead time, no demand, LOW confidence.
	if r.LeadTimeSource != "ITEM_MASTER" || r.LeadTimeMeanDays != 6 || r.NonzeroDemandDays != 0 || r.Confidence != "LOW" || r.AverageDailyDemand != 0 {
		t.Fatalf("no-history result: %+v", r)
	}
	t.Run("falls back to item-master safety stock", func(t *testing.T) {
		if r.DemandSource != "ITEM_MASTER_FALLBACK" || r.SafetyStock != 5 {
			t.Fatalf("item without issue history must use its item-master safety stock %.2f, got %.2f (source %s, observation days %d)",
				rm.SafetyStock, r.SafetyStock, r.DemandSource, r.DemandObservationDays)
		}
	})
}

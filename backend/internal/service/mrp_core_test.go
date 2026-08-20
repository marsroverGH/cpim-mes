package service

import (
	"testing"
	"time"
)

func TestMRPNetting_UsesScheduledReceiptsAndSafetyStock(t *testing.T) {
	// opening 50 + scheduled 100 - gross 120 = 30 ending without new supply.
	// safety stock 20 is still satisfied, so no planned receipt is needed.
	net, receipt, projected := netMRPBucket(50, 120, 100, 20, 1, 0, LotMethodLFL)
	if net != 0 || receipt != 0 || projected != 30 {
		t.Fatalf("unexpected netting: net=%v receipt=%v projected=%v", net, receipt, projected)
	}
}

func TestMRPNetting_ReplenishesSafetyStock(t *testing.T) {
	// opening 100 - gross 90 = 10, but safety stock is 20 -> net requirement 10.
	net, receipt, projected := netMRPBucket(100, 90, 0, 20, 1, 0, LotMethodLFL)
	if net != 10 || receipt != 10 || projected != 20 {
		t.Fatalf("safety-stock netting failed: net=%v receipt=%v projected=%v", net, receipt, projected)
	}
}

func TestMRPLeadTimeOffset(t *testing.T) {
	receipt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	got := plannedOrderReleaseDate(receipt, 5, 25)
	want := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("release date=%v want=%v", got, want)
	}
	if !plannedOrderReleaseDate(receipt, 5, 0).IsZero() {
		t.Fatal("zero planned receipt must not have a release date")
	}
}

func TestDirectComponentRequirement_IsOneBOMEdgeOnly(t *testing.T) {
	// Parent release 100, direct component usage 2, scrap 5% => 210.
	// No grandchild quantity participates in this function by design.
	got := directComponentRequirement(100, 2, 0.05)
	if got != 210 {
		t.Fatalf("direct component requirement=%v want=210", got)
	}
}

func TestDependentRequirementDate_PastDueMovesToStartBucket(t *testing.T) {
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	pastRelease := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	if got := dependentRequirementDate(pastRelease, start); !got.Equal(start) {
		t.Fatalf("past-due dependent date=%v want=%v", got, start)
	}
	future := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	if got := dependentRequirementDate(future, start); !got.Equal(future) {
		t.Fatalf("future dependent date=%v want=%v", got, future)
	}
}

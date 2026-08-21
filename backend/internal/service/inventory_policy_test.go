package service

import (
	"math"
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestInverseStandardNormalCDF95(t *testing.T) {
	got := inverseStandardNormalCDF(0.95)
	if math.Abs(got-1.6448536269) > 1e-5 {
		t.Fatalf("z(0.95)=%.8f", got)
	}
}

func TestStatisticalSafetyStockCombinesDemandAndLeadVariability(t *testing.T) {
	z, got := statisticalSafetyStock(0.95, 10, 2, 5, 1)
	want := z * math.Sqrt(5*4+100)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("safety=%.8f want %.8f", got, want)
	}
	if got <= 0 {
		t.Fatal("statistical safety stock must be positive")
	}
}

func TestMinMaxMRPOrdersToMaxOnlyBelowROP(t *testing.T) {
	p := domain.EffectiveInventoryPolicy{ReplenishmentMethod: "MIN_MAX", CalculationStatus: "CALCULATED", ReorderPoint: 25, MaxQty: 50, SafetyStock: 10}
	net, planned, projected := netMRPBucketWithInventoryPolicy(30, 10, 0, p, 1, 0, LotMethodLFL)
	if net != 30 || planned != 30 || projected != 50 {
		t.Fatalf("below ROP: net=%v planned=%v projected=%v", net, planned, projected)
	}
	net, planned, projected = netMRPBucketWithInventoryPolicy(40, 10, 0, p, 1, 0, LotMethodLFL)
	if net != 0 || planned != 0 || projected != 30 {
		t.Fatalf("above ROP: net=%v planned=%v projected=%v", net, planned, projected)
	}
}

func TestSafetyStockPolicyPreservesLegacyNetting(t *testing.T) {
	p := domain.EffectiveInventoryPolicy{ReplenishmentMethod: "SAFETY_STOCK", CalculationStatus: "CALCULATED", SafetyStock: 10, ReorderPoint: 30, MaxQty: 50}
	n1, r1, x1 := netMRPBucketWithInventoryPolicy(20, 15, 0, p, 1, 0, LotMethodLFL)
	n2, r2, x2 := netMRPBucket(20, 15, 0, 10, 1, 0, LotMethodLFL)
	if n1 != n2 || r1 != r2 || x1 != x2 {
		t.Fatalf("legacy mismatch got=(%v,%v,%v) want=(%v,%v,%v)", n1, r1, x1, n2, r2, x2)
	}
}

func TestInventoryPolicyHashStableAcrossOrder(t *testing.T) {
	a := domain.InventoryPolicyResult{PolicyVersionID: uuid.MustParse("10000000-0000-0000-0000-000000000001"), ItemID: uuid.MustParse("20000000-0000-0000-0000-000000000001"), SafetyStock: 3, ReorderPoint: 5, MinQty: 5, MaxQty: 8}
	b := domain.InventoryPolicyResult{PolicyVersionID: uuid.MustParse("10000000-0000-0000-0000-000000000002"), ItemID: uuid.MustParse("20000000-0000-0000-0000-000000000002"), SafetyStock: 4, ReorderPoint: 7, MinQty: 7, MaxQty: 10}
	if canonicalInventoryPolicyHash([]domain.InventoryPolicyResult{a, b}) != canonicalInventoryPolicyHash([]domain.InventoryPolicyResult{b, a}) {
		t.Fatal("inventory policy hash must be canonical")
	}
}

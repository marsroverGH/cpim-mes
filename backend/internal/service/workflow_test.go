package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestCalcReservation_AllSufficient(t *testing.T) {
	frame := uuid.New()
	wheel := uuid.New()
	requirements := []ComponentRequirement{
		{ChildID: frame, Required: 10},
		{ChildID: wheel, Required: 20},
	}
	balances := map[uuid.UUID]float64{frame: 50, wheel: 100}
	codes := map[uuid.UUID]string{frame: "FRAME-1", wheel: "WHEEL-1"}

	got := CalcReservation(requirements, balances, codes)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	for _, l := range got {
		if !l.Sufficient {
			t.Errorf("expected sufficient for %s, got insufficient (req=%v avail=%v)",
				l.ChildCode, l.Required, l.Available)
		}
	}
}

func TestCalcReservation_DetectsShortage(t *testing.T) {
	frame := uuid.New()
	wheel := uuid.New()
	requirements := []ComponentRequirement{
		{ChildID: frame, Required: 10},
		{ChildID: wheel, Required: 20},
	}
	balances := map[uuid.UUID]float64{frame: 5, wheel: 100} // FRAME 不足
	codes := map[uuid.UUID]string{frame: "FRAME-1", wheel: "WHEEL-1"}

	got := CalcReservation(requirements, balances, codes)
	if got[0].Sufficient {
		t.Error("FRAME should be insufficient (need 10, have 5)")
	}
	if !got[1].Sufficient {
		t.Error("WHEEL should be sufficient")
	}
	if got[0].Required != 10 || got[0].Available != 5 {
		t.Errorf("FRAME line wrong: %+v", got[0])
	}
}

func TestCalcReservation_ZeroAvailability(t *testing.T) {
	frame := uuid.New()
	requirements := []ComponentRequirement{
		{ChildID: frame, Required: 1},
	}
	got := CalcReservation(requirements, map[uuid.UUID]float64{}, map[uuid.UUID]string{frame: "FRAME-1"})
	if got[0].Sufficient {
		t.Error("missing balance should be treated as 0 → insufficient")
	}
	if got[0].Available != 0 {
		t.Errorf("missing balance should default to 0, got %v", got[0].Available)
	}
}

func TestDirectBOMRequirements_MultiLevelDoesNotConsumeGrandchildren(t *testing.T) {
	fg := uuid.New()
	sa := uuid.New()
	raw := uuid.New()

	// FG -> SA x2, SA -> RAW x3.  Completing the FG WO must issue only SA.
	fgReq := DirectBOMRequirements([]domain.BOMComponent{
		{ParentID: fg, ChildID: sa, Quantity: 2, ScrapPct: 0},
	}, 10)
	if len(fgReq) != 1 {
		t.Fatalf("FG completion should have 1 direct requirement, got %d", len(fgReq))
	}
	if fgReq[0].ChildID != sa || fgReq[0].Required != 20 {
		t.Fatalf("FG should consume SA=20 only, got %+v", fgReq[0])
	}
	if fgReq[0].ChildID == raw {
		t.Fatal("FG WO must not consume RAW grandchild")
	}

	// RAW is consumed later by the SA's own WO, exactly once.
	saReq := DirectBOMRequirements([]domain.BOMComponent{
		{ParentID: sa, ChildID: raw, Quantity: 3, ScrapPct: 0},
	}, 20)
	if len(saReq) != 1 || saReq[0].ChildID != raw || saReq[0].Required != 60 {
		t.Fatalf("SA WO should consume RAW=60, got %+v", saReq)
	}
}

func TestDirectBOMRequirements_AppliesScrapOnlyAtDirectEdge(t *testing.T) {
	parent := uuid.New()
	child := uuid.New()

	got := DirectBOMRequirements([]domain.BOMComponent{
		{ParentID: parent, ChildID: child, Quantity: 2, ScrapPct: 0.10},
	}, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 direct requirement, got %d", len(got))
	}
	if got[0].Required != 11 {
		t.Fatalf("expected 5*2*1.10 = 11, got %v", got[0].Required)
	}
}

func TestCalcCompletionBatch_Partial20Of100(t *testing.T) {
	q := 20.0
	batch, cumulative, remaining, status, err := CalcCompletionBatch(100, 0, &q)
	if err != nil {
		t.Fatal(err)
	}
	if batch != 20 || cumulative != 20 || remaining != 80 || status != "IN_PROGRESS" {
		t.Fatalf("unexpected state: batch=%v cumulative=%v remaining=%v status=%s",
			batch, cumulative, remaining, status)
	}
}

func TestCalcCompletionBatch_SecondPartialUsesDelta(t *testing.T) {
	q := 20.0
	batch, cumulative, remaining, status, err := CalcCompletionBatch(100, 20, &q)
	if err != nil {
		t.Fatal(err)
	}
	if batch != 20 || cumulative != 40 || remaining != 60 || status != "IN_PROGRESS" {
		t.Fatalf("unexpected state: batch=%v cumulative=%v remaining=%v status=%s",
			batch, cumulative, remaining, status)
	}
}

func TestCalcCompletionBatch_FinalBatchCompletesWO(t *testing.T) {
	q := 20.0
	batch, cumulative, remaining, status, err := CalcCompletionBatch(100, 80, &q)
	if err != nil {
		t.Fatal(err)
	}
	if batch != 20 || cumulative != 100 || remaining != 0 || status != "COMPLETED" {
		t.Fatalf("unexpected final state: batch=%v cumulative=%v remaining=%v status=%s",
			batch, cumulative, remaining, status)
	}
}

func TestCalcCompletionBatch_RejectsOverCompletion(t *testing.T) {
	q := 30.0
	_, _, _, _, err := CalcCompletionBatch(100, 80, &q)
	if err == nil {
		t.Fatal("expected over-completion error")
	}
}

func TestDirectBOMRequirements_PartialCompletionConsumesOnlyBatchQty(t *testing.T) {
	parent := uuid.New()
	child := uuid.New()
	got := DirectBOMRequirements([]domain.BOMComponent{
		{ParentID: parent, ChildID: child, Quantity: 2, ScrapPct: 0},
	}, 20)
	if len(got) != 1 || got[0].Required != 40 {
		t.Fatalf("100-unit WO partial completion of 20 should consume 20*2=40, got %+v", got)
	}
}

func TestSortedUniqueRequirementIDs_DeterministicAndUnique(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	b := uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	c := uuid.MustParse("00000000-0000-0000-0000-00000000000c")

	got := SortedUniqueRequirementIDs([]ComponentRequirement{
		{ChildID: c, Required: 1},
		{ChildID: a, Required: 1},
		{ChildID: b, Required: 1},
		{ChildID: a, Required: 2}, // duplicate must not cause a second lock
		{ChildID: uuid.Nil, Required: 1},
	})

	want := []uuid.UUID{a, b, c}
	if len(got) != len(want) {
		t.Fatalf("expected %d lock ids, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lock order mismatch at %d: want %s got %s", i, want[i], got[i])
		}
	}
}

func TestCalcReservation_SecondWOSeesCommittedReservation(t *testing.T) {
	component := uuid.New()
	requirements := []ComponentRequirement{{ChildID: component, Required: 80}}
	codes := map[uuid.UUID]string{component: "COMP-A"}

	// First WO sees 100 available and may reserve 80.
	first := CalcReservation(requirements, map[uuid.UUID]float64{component: 100}, codes)
	if len(first) != 1 || !first[0].Sufficient {
		t.Fatalf("first WO should be releasable: %+v", first)
	}

	// After that reservation commits, the locked/rechecked balance seen by the
	// second WO is 20. It must be rejected instead of also reserving 80.
	second := CalcReservation(requirements, map[uuid.UUID]float64{component: 20}, codes)
	if len(second) != 1 || second[0].Sufficient {
		t.Fatalf("second WO should be rejected after committed reservation: %+v", second)
	}
}

func TestSnapshotRequirements_RemainsFrozenAfterLiveBOMChange(t *testing.T) {
	parent := uuid.New()
	oldChild := uuid.New()
	newChild := uuid.New()

	// This is what was frozen when the WO was released.
	snapshot := []BOMSnapshotLine{
		{ChildID: oldChild, ChildCode: "OLD-A", QuantityPer: 2, ScrapPct: 0.10, RequiredQty: 220},
	}

	// Simulate a later ECO/live-BOM change.  These rows are intentionally not
	// passed to SnapshotRequirements: a released WO must ignore them.
	liveAfterECO := []domain.BOMComponent{
		{ParentID: parent, ChildID: newChild, Quantity: 3, ScrapPct: 0},
	}
	if got := DirectBOMRequirements(liveAfterECO, 20); len(got) != 1 || got[0].ChildID != newChild || got[0].Required != 60 {
		t.Fatalf("sanity check for changed live BOM failed: %+v", got)
	}

	got := SnapshotRequirements(snapshot, 20)
	if len(got) != 1 {
		t.Fatalf("expected one snapshotted component, got %d", len(got))
	}
	if got[0].ChildID != oldChild {
		t.Fatalf("released WO must keep old child %s, got %s", oldChild, got[0].ChildID)
	}
	if got[0].Required != 44 { // 20 * 2 * 1.10
		t.Fatalf("released WO must use frozen qty/scrap: want 44 got %v", got[0].Required)
	}
}

func TestSnapshotRequirements_PartialCompletionUsesFrozenRatio(t *testing.T) {
	child := uuid.New()
	snapshot := []BOMSnapshotLine{
		{ChildID: child, ChildCode: "COMP-1", QuantityPer: 1.5, ScrapPct: 0.20, RequiredQty: 180},
	}
	got := SnapshotRequirements(snapshot, 20)
	if len(got) != 1 || got[0].Required != 36 {
		t.Fatalf("20-unit partial completion should consume 20*1.5*1.20=36, got %+v", got)
	}
}

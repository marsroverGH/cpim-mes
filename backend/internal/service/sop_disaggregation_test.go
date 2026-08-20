package service

import (
	"math"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestCalcSOPDisaggregationPreservesMonthlySupplyAndMix(t *testing.T) {
	groupID := uuid.New()
	plan := domain.SOPPlan{ID: uuid.New(), GroupID: groupID, PlanMonth: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), SupplyQty: 1000}
	mixID := uuid.New()
	items := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	mix := []domain.SOPProductMixLine{
		{MixVersionID: mixID, ItemID: items[0], MixPct: 40},
		{MixVersionID: mixID, ItemID: items[1], MixPct: 35},
		{MixVersionID: mixID, ItemID: items[2], MixPct: 25},
	}
	out, err := CalcSOPDisaggregation(plan, mixID, mix)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Lines) != 15 {
		t.Fatalf("expected 15 lines (3 items x 5 buckets), got %d", len(out.Lines))
	}
	byItem := map[uuid.UUID]float64{}
	var total float64
	for _, l := range out.Lines {
		byItem[l.ItemID] += l.PlannedQty
		total += l.PlannedQty
	}
	if math.Abs(total-1000) > 0.001 {
		t.Fatalf("monthly total mismatch: %.6f", total)
	}
	want := map[uuid.UUID]float64{items[0]: 400, items[1]: 350, items[2]: 250}
	for id, w := range want {
		if math.Abs(byItem[id]-w) > 0.001 {
			t.Fatalf("item %s total %.6f want %.6f", id, byItem[id], w)
		}
	}
	// September has 30 days: last 7-day bucket contains 2 days.
	last := out.Lines[4]
	if math.Abs(last.TimeWeight-(2.0/30.0)) > 1e-9 {
		t.Fatalf("last bucket weight %.9f", last.TimeWeight)
	}
}

func TestCalcSOPDisaggregationRejectsInvalidMix(t *testing.T) {
	plan := domain.SOPPlan{ID: uuid.New(), GroupID: uuid.New(), PlanMonth: time.Now(), SupplyQty: 100}
	_, err := CalcSOPDisaggregation(plan, uuid.New(), []domain.SOPProductMixLine{{ItemID: uuid.New(), MixPct: 90}})
	if err == nil {
		t.Fatal("expected invalid mix error")
	}
}

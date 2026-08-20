package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestSplitTransferBatches(t *testing.T) {
	got := splitTransferBatches(100, true, 30)
	want := []float64{30, 30, 30, 10}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batch[%d]=%v want %v", i, got[i], want[i])
		}
	}
	one := splitTransferBatches(100, false, 30)
	if len(one) != 1 || one[0] != 100 {
		t.Fatalf("non-overlap should keep one process lot: %#v", one)
	}
}

func TestRoutingPredecessorUsesCumulativeTransferQuantity(t *testing.T) {
	prev := []scheduledBatchInfo{
		{batch: detailedBatchForTest(1, 30)},
		{batch: detailedBatchForTest(2, 60)},
		{batch: detailedBatchForTest(3, 90)},
		{batch: detailedBatchForTest(4, 100)},
	}
	if got := routingPredecessor(prev, 55, true).batch.BatchNo; got != 2 {
		t.Fatalf("predecessor batch=%d want 2", got)
	}
	if got := routingPredecessor(prev, 55, false).batch.BatchNo; got != 4 {
		t.Fatalf("non-overlap predecessor batch=%d want final batch 4", got)
	}
}

func TestSequenceDependentSetupExactWildcardAndSameFamily(t *testing.T) {
	wc := uuid.New()
	m := map[setupKey]float64{
		{wc: wc, from: "A", to: "B"}: 22,
		{wc: wc, from: "*", to: "C"}: 15,
		{wc: wc, from: "*", to: "*"}: 9,
	}
	if got := sequenceSetupMinutes(m, wc, "A", "B", 40); got != 22 {
		t.Fatalf("exact setup=%v want 22", got)
	}
	if got := sequenceSetupMinutes(m, wc, "X", "C", 40); got != 15 {
		t.Fatalf("wildcard setup=%v want 15", got)
	}
	if got := sequenceSetupMinutes(m, wc, "X", "Y", 40); got != 9 {
		t.Fatalf("default wildcard setup=%v want 9", got)
	}
	if got := sequenceSetupMinutes(m, wc, "B", "B", 40); got != 0 {
		t.Fatalf("same family setup=%v want 0", got)
	}
}

func detailedBatchForTest(no int, cumulative float64) domain.DetailedScheduleBatch {
	return domain.DetailedScheduleBatch{BatchNo: no, CumulativeQty: cumulative}
}

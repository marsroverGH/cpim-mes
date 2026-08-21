package service

import (
	"testing"
	"time"

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

func TestMaintenanceDowntimeSplitsDetailedClock(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	st := &detailedWCState{
		wc:       domain.WorkCenter{MachineCount: 1, WorkerCount: 1, ShiftStartMinute: 480},
		calendar: &CalendarSnapshot{Calendar: domain.WorkCalendar{MondayMin: 480}},
		end:      day.Add(24*time.Hour - time.Nanosecond),
		maintenance: []maintenanceBlock{{event: domain.CurrentMaintenanceEvent{
			EventType: "BREAKDOWN", Status: "ACTIVE", StartAt: day.Add(9 * time.Hour), EndAt: day.Add(10 * time.Hour), UnavailableMachines: 1,
		}}},
	}
	frags, end, ok := st.planClock(day.Add(8*time.Hour), 120, 0, 1, nil)
	if !ok {
		t.Fatal("expected schedule around downtime")
	}
	if len(frags) != 2 {
		t.Fatalf("fragments=%d want=2: %#v", len(frags), frags)
	}
	if !frags[0].start.Equal(day.Add(8*time.Hour)) || !frags[0].end.Equal(day.Add(9*time.Hour)) {
		t.Fatalf("first fragment=%v..%v", frags[0].start, frags[0].end)
	}
	if !frags[1].start.Equal(day.Add(10*time.Hour)) || !end.Equal(day.Add(11*time.Hour)) {
		t.Fatalf("second/end=%v..%v end=%v", frags[1].start, frags[1].end, end)
	}
}

func TestPartialMaintenanceKeepsRemainingMachineCapacity(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	st := &detailedWCState{
		wc:       domain.WorkCenter{MachineCount: 2, WorkerCount: 1, ShiftStartMinute: 480},
		calendar: &CalendarSnapshot{Calendar: domain.WorkCalendar{MondayMin: 480}},
		end:      day.Add(24*time.Hour - time.Nanosecond),
		maintenance: []maintenanceBlock{{event: domain.CurrentMaintenanceEvent{
			EventType: "PREVENTIVE_MAINTENANCE", Status: "PLANNED", StartAt: day.Add(8 * time.Hour), EndAt: day.Add(12 * time.Hour), UnavailableMachines: 1,
		}}},
	}
	frags, end, ok := st.planClock(day.Add(8*time.Hour), 60, 0, 1, nil)
	if !ok || len(frags) != 1 || !end.Equal(day.Add(9*time.Hour)) {
		t.Fatalf("one remaining machine should stay usable: ok=%v frags=%#v end=%v", ok, frags, end)
	}
	frags2, end2, ok2 := st.planClock(day.Add(8*time.Hour), 60, 0, 2, nil)
	if !ok2 || len(frags2) != 1 || !frags2[0].start.Equal(day.Add(12*time.Hour)) || !end2.Equal(day.Add(13*time.Hour)) {
		t.Fatalf("two-machine operation must wait for maintenance to end: ok=%v frags=%#v end=%v", ok2, frags2, end2)
	}
}

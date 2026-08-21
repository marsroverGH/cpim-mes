package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestTimeFenceForExecutionAndFutureWindows(t *testing.T) {
	base := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	p := domain.DispatchPolicyVersion{FreezeMinutes: 60, FirmMinutes: 240}
	frozen := base.Add(30 * time.Minute)
	firm := base.Add(2 * time.Hour)
	flex := base.Add(6 * time.Hour)
	past := base.Add(-30 * time.Minute)
	if got := timeFenceFor(&frozen, "READY", base, p); got != "FROZEN" {
		t.Fatalf("frozen fence=%s", got)
	}
	if got := timeFenceFor(&firm, "READY", base, p); got != "FIRM" {
		t.Fatalf("firm fence=%s", got)
	}
	if got := timeFenceFor(&flex, "READY", base, p); got != "FLEXIBLE" {
		t.Fatalf("flexible fence=%s", got)
	}
	if got := timeFenceFor(&past, "READY", base, p); got != "FIRM" {
		t.Fatalf("past missed start fence=%s", got)
	}
	if got := timeFenceFor(&flex, "IN_PROGRESS", base, p); got != "EXECUTED" {
		t.Fatalf("executed fence=%s", got)
	}
}

func TestAdherenceSummaryExcludesFutureUntouchedOperations(t *testing.T) {
	planned := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	actual := planned.Add(5 * time.Minute)
	rows := []domain.ScheduleAdherenceRow{
		{WorkOrderID: uuid.New(), OperationSeq: 10, PlannedStart: &planned, StartOnTime: true, CompletionOnTime: true, DispatchStatus: "QUEUED"},
		{WorkOrderID: uuid.New(), OperationSeq: 10, PlannedStart: &planned, ActualStart: &actual, StartOnTime: true, CompletionOnTime: true, DispatchStatus: "IN_PROCESS", StartVarianceMinutes: 5},
		{WorkOrderID: uuid.New(), OperationSeq: 10, PlannedStart: &planned, StartOnTime: false, CompletionOnTime: true, DispatchStatus: "LATE_START", StartVarianceMinutes: 30},
	}
	got := adherenceSummary(rows)
	if got.OnTimeStartPct != 50 {
		t.Fatalf("on-time start pct=%v want 50; future untouched work must not be denominator", got.OnTimeStartPct)
	}
	if got.LateStarts != 1 || got.StartedOperations != 1 {
		t.Fatalf("late=%d started=%d", got.LateStarts, got.StartedOperations)
	}
}

func TestCanonicalRescheduleHashIgnoresIDsAndInputOrder(t *testing.T) {
	base := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	wc := uuid.New()
	mk := func(id uuid.UUID, ref string, seq int, start time.Time) domain.DynamicRescheduleChange {
		return domain.DynamicRescheduleChange{ID: id, RescheduleRunID: uuid.New(), SourceRef: ref, OperationSeq: seq, ChangeType: "TIME_SHIFT", TimeFence: "FIRM", NewWorkCenterID: &wc, NewStart: &start}
	}
	a := []domain.DynamicRescheduleChange{mk(uuid.New(), "WO-B", 20, base.Add(time.Hour)), mk(uuid.New(), "WO-A", 10, base)}
	b := []domain.DynamicRescheduleChange{mk(uuid.New(), "WO-A", 10, base), mk(uuid.New(), "WO-B", 20, base.Add(time.Hour))}
	ha, err := canonicalRescheduleHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := canonicalRescheduleHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb || len(ha) != 64 {
		t.Fatalf("canonical hashes differ: %s vs %s", ha, hb)
	}
}

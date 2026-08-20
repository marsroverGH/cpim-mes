package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestFiniteAllocator_NoOverlapAndSpillsToNextDay(t *testing.T) {
	wcID := uuid.New()
	wc := domain.WorkCenter{ID: wcID, Code: "WC", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1, ShiftStartMinute: 480}
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	a := &finiteAllocator{start: start, end: start.AddDate(0, 0, 2), workCenter: map[uuid.UUID]domain.WorkCenter{wcID: wc}, calendars: map[uuid.UUID]*CalendarSnapshot{}, days: map[string]*capacityDay{}}

	first, rem := a.allocateForward(wcID, start, 400)
	if rem != 0 || len(first) != 1 {
		t.Fatalf("first allocation = segments %d rem %v", len(first), rem)
	}
	second, rem := a.allocateForward(wcID, start, 200)
	if rem != 0 || len(second) != 2 {
		t.Fatalf("second allocation should use remaining 80 min then spill, segments=%d rem=%v", len(second), rem)
	}
	if second[0].StartAt.Before(first[0].EndAt) {
		t.Fatalf("overlap detected: second starts %v before first ends %v", second[0].StartAt, first[0].EndAt)
	}
	if TruncateDay(second[1].StartAt).Equal(start) {
		t.Fatalf("expected spill to next day, got %v", second[1].StartAt)
	}
}

func TestFiniteAllocator_RespectsEfficiencyUtilization(t *testing.T) {
	wcID := uuid.New()
	wc := domain.WorkCenter{ID: wcID, CapacityMinutesPerDay: 480, Efficiency: 0.8, Utilization: 0.75, ShiftStartMinute: 480}
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	a := &finiteAllocator{start: start, end: start, workCenter: map[uuid.UUID]domain.WorkCenter{wcID: wc}, calendars: map[uuid.UUID]*CalendarSnapshot{}, days: map[string]*capacityDay{}}
	segs, rem := a.allocateForward(wcID, start, 288) // 480*0.8*0.75
	if rem > 1e-6 || len(segs) != 1 {
		t.Fatalf("expected exact daily effective capacity, segments=%d rem=%v", len(segs), rem)
	}
	if got := segs[0].EndAt.Sub(segs[0].StartAt).Minutes(); got < 479.9 || got > 480.1 {
		t.Fatalf("expected full 480 clock minutes, got %.2f", got)
	}
}

func TestBusinessDueEnd(t *testing.T) {
	d := time.Date(2026, 8, 19, 10, 30, 0, 0, time.UTC)
	got := businessDueEnd(d)
	if got.Day() != 19 || got.Hour() != 23 || got.Minute() != 59 {
		t.Fatalf("unexpected due end %v", got)
	}
}

func TestFiniteAllocator_MachineCountAddsParallelAggregateCapacity(t *testing.T) {
	wcID := uuid.New()
	wc := domain.WorkCenter{ID: wcID, CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1, ShiftStartMinute: 480, MachineCount: 2}
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	a := &finiteAllocator{start: start, end: start, workCenter: map[uuid.UUID]domain.WorkCenter{wcID: wc}, calendars: map[uuid.UUID]*CalendarSnapshot{}, days: map[string]*capacityDay{}}
	segs, rem := a.allocateForward(wcID, start, 960)
	if rem > 1e-6 || len(segs) != 1 {
		t.Fatalf("two-machine aggregate capacity should accept 960 standard minutes, segments=%d rem=%v", len(segs), rem)
	}
	if got := segs[0].EndAt.Sub(segs[0].StartAt).Minutes(); got < 479.9 || got > 480.1 {
		t.Fatalf("expected 480 clock minutes with two machines, got %.2f", got)
	}
}

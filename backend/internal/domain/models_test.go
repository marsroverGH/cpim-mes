package domain

import (
	"testing"
	"time"
)

func TestWorkCenterEffectiveCapacityMin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		wc   WorkCenter
		want float64
	}{
		{"single machine", WorkCenter{CapacityMinutesPerDay: 480, MachineCount: 1, Efficiency: 0.9, Utilization: 0.8}, 345.6},
		{"multiple machines scale capacity", WorkCenter{CapacityMinutesPerDay: 480, MachineCount: 3, Efficiency: 1, Utilization: 1}, 1440},
		{"zero machines treated as one", WorkCenter{CapacityMinutesPerDay: 480, MachineCount: 0, Efficiency: 1, Utilization: 0.5}, 240},
		{"negative machines treated as one", WorkCenter{CapacityMinutesPerDay: 100, MachineCount: -2, Efficiency: 1, Utilization: 1}, 100},
		{"zero efficiency yields zero", WorkCenter{CapacityMinutesPerDay: 480, MachineCount: 2, Efficiency: 0, Utilization: 1}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.wc.EffectiveCapacityMin()
			if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("EffectiveCapacityMin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkCalendarMinutesForWeekday(t *testing.T) {
	t.Parallel()
	cal := WorkCalendar{
		MondayMin: 1, TuesdayMin: 2, WednesdayMin: 3, ThursdayMin: 4,
		FridayMin: 5, SaturdayMin: 6, SundayMin: 7,
	}
	want := map[time.Weekday]int{
		time.Monday: 1, time.Tuesday: 2, time.Wednesday: 3, time.Thursday: 4,
		time.Friday: 5, time.Saturday: 6, time.Sunday: 7,
	}
	for d, w := range want {
		if got := cal.MinutesForWeekday(d); got != w {
			t.Errorf("MinutesForWeekday(%s) = %d, want %d", d, got, w)
		}
	}
	if got := cal.MinutesForWeekday(time.Weekday(9)); got != 0 {
		t.Errorf("out-of-range weekday = %d, want 0", got)
	}
}

func TestStockBalanceAvailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		onHand, reserved, want float64
	}{
		{100, 30, 70},
		{10, 0, 10},
		{5, 8, -3}, // over-reservation is surfaced, not clamped
	}
	for _, tc := range tests {
		if got := (StockBalance{OnHand: tc.onHand, Reserved: tc.reserved}).Available(); got != tc.want {
			t.Errorf("Available(%v,%v) = %v, want %v", tc.onHand, tc.reserved, got, tc.want)
		}
	}
}

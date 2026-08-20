package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func newSnapshot() CalendarSnapshot {
	return CalendarSnapshot{
		Calendar: domain.WorkCalendar{
			MondayMin: 480, TuesdayMin: 480, WednesdayMin: 480,
			ThursdayMin: 480, FridayMin: 480,
			SaturdayMin: 0, SundayMin: 0,
		},
		Exceptions: map[time.Time]domain.CalendarException{},
	}
}

func TestCalendarSnapshot_StandardWeek(t *testing.T) {
	cs := newSnapshot()
	// 2026-04-27 is a Monday
	monday := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)

	if got := cs.MinutesAvailable(monday); got != 480 {
		t.Errorf("Monday should be 480 min, got %d", got)
	}
	saturday := monday.AddDate(0, 0, 5)
	if got := cs.MinutesAvailable(saturday); got != 0 {
		t.Errorf("Saturday should be 0, got %d", got)
	}
	if cs.IsWorkDay(saturday) {
		t.Error("Saturday should NOT be a work day with default schedule")
	}
}

func TestCalendarSnapshot_HolidayException(t *testing.T) {
	cs := newSnapshot()
	tuesday := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	cs.Exceptions[TruncateDay(tuesday)] = domain.CalendarException{
		ID: uuid.New(), Kind: "HOLIDAY", Description: "Special holiday",
	}
	if cs.MinutesAvailable(tuesday) != 0 {
		t.Errorf("HOLIDAY exception should override to 0")
	}
	if cs.IsWorkDay(tuesday) {
		t.Error("Holiday should not be a work day")
	}
}

func TestCalendarSnapshot_WorkdayException(t *testing.T) {
	cs := newSnapshot()
	sat := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC) // Saturday
	cs.Exceptions[TruncateDay(sat)] = domain.CalendarException{
		ID: uuid.New(), Kind: "WORKDAY", Minutes: 240, Description: "Special open day",
	}
	if got := cs.MinutesAvailable(sat); got != 240 {
		t.Errorf("WORKDAY exception should yield 240, got %d", got)
	}
	if !cs.IsWorkDay(sat) {
		t.Error("WORKDAY exception should make it a work day")
	}
}

func TestCalendarSnapshot_PreviousWorkDay_BackOverWeekend(t *testing.T) {
	cs := newSnapshot()
	// Sunday Apr 26 → should walk back to Friday Apr 24
	sun := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	got := cs.PreviousWorkDay(sun, 7)
	want := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("PreviousWorkDay(Sun) = %v, want Fri %v", got, want)
	}
}

func TestCalendarSnapshot_PreviousWorkDay_SkipHoliday(t *testing.T) {
	cs := newSnapshot()
	// Mark Friday Apr 24 as holiday → Sun Apr 26 should fall back to Thu Apr 23
	fri := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	cs.Exceptions[TruncateDay(fri)] = domain.CalendarException{
		ID: uuid.New(), Kind: "HOLIDAY",
	}
	sun := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	got := cs.PreviousWorkDay(sun, 7)
	want := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("PreviousWorkDay should skip Fri-holiday, got %v want %v", got, want)
	}
}

func TestCalendarSnapshot_PreviousWorkDay_StaysIfWorkDay(t *testing.T) {
	cs := newSnapshot()
	wed := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	got := cs.PreviousWorkDay(wed, 7)
	if !got.Equal(wed) {
		t.Errorf("Wed (work day) should be returned as-is, got %v", got)
	}
}

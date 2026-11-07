package service

import (
	"testing"
	"time"
)

func TestBusinessDateCanonicalForm(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	la, _ := time.LoadLocation("America/Los_Angeles")
	want := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)

	cases := map[string]time.Time{
		"DATE column (UTC midnight)":   time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
		"request date with JST offset": time.Date(2027, 3, 1, 0, 0, 0, 0, tokyo),
		"request date with LA offset":  time.Date(2027, 3, 1, 0, 0, 0, 0, la),
		// Instants are read in the business timezone (Asia/Tokyo by default):
		"00:30 JST is already the 1st":      time.Date(2027, 2, 28, 15, 30, 0, 0, time.UTC),
		"08:59 JST, server clock in UTC":    time.Date(2027, 2, 28, 23, 59, 0, 0, time.UTC),
		"23:59 JST, server clock in LA":     time.Date(2027, 3, 1, 6, 59, 0, 0, la),
		"noon JST":                          time.Date(2027, 3, 1, 12, 0, 0, 0, tokyo),
		"instant expressed in another zone": time.Date(2027, 2, 28, 16, 0, 0, 0, la), // = 09:00 JST 3/1
	}
	for name, in := range cases {
		got := BusinessDate(in)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("%s: BusinessDate(%s) = %s, want %s", name, in, got, want)
		}
	}
	if !BusinessDate(time.Time{}).IsZero() {
		t.Error("zero time must stay zero")
	}
}

func TestBusinessDateValuesAreUsableAsMapKeys(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	m := map[time.Time]int{}
	m[BusinessDate(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))]++
	m[BusinessDate(time.Date(2027, 3, 1, 0, 0, 0, 0, tokyo))]++
	m[BusinessDate(time.Date(2027, 3, 1, 10, 0, 0, 0, tokyo))]++
	if len(m) != 1 {
		t.Fatalf("one business day produced %d distinct keys: %v", len(m), m)
	}
}

func TestBusinessTodayFollowsBusinessTimezoneNotServerClock(t *testing.T) {
	old := nowFunc
	t.Cleanup(func() { nowFunc = old })
	// 2027-03-01 01:00 JST; a UTC server still thinks it is February 28th.
	nowFunc = func() time.Time { return time.Date(2027, 2, 28, 16, 0, 0, 0, time.UTC) }
	if got, want := BusinessToday(), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("BusinessToday() = %s, want %s", got, want)
	}
}

func TestSetBusinessTimezoneRejectsUnknownZone(t *testing.T) {
	if err := SetBusinessTimezone("Mars/Olympus_Mons"); err == nil {
		t.Fatal("expected an error for an unknown zone")
	}
	if BusinessLocation().String() == "Mars/Olympus_Mons" {
		t.Fatal("a rejected zone must not replace the current one")
	}
}

func TestSchedulerAndCalendarDaysDoNotDependOnValueLocation(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	instant := time.Date(2027, 3, 1, 1, 0, 0, 0, tokyo) // 2027-02-28 16:00 UTC
	if got := schedDay(instant); !got.Equal(schedDay(instant.UTC())) {
		t.Fatalf("schedDay depends on the value's location: %s vs %s", got, schedDay(instant.UTC()))
	}
	if got, want := calendarDay(time.Date(2027, 3, 1, 0, 0, 0, 0, tokyo)), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("calendarDay(date value) = %s, want %s", got, want)
	}
}

func TestParseDateOnlyAcceptsDateAndRFC3339(t *testing.T) {
	want := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	for _, in := range []string{"2026-11-11", " 2026-11-11 ", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00+09:00", "2026-11-10T20:00:00Z"} {
		got, err := ParseDateOnly(in)
		if err != nil || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseDateOnly(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "11/11/2026", "2026-13-01", "2026-11-11T00:00"} {
		if _, err := ParseDateOnly(in); err == nil {
			t.Errorf("ParseDateOnly(%q) accepted", in)
		}
	}
}

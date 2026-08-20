package service

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 12, 0, 0, 0, time.UTC) }

func TestECOEffectiveOn(t *testing.T) {
	cases := []struct {
		name     string
		eff, now time.Time
		want     bool
	}{
		{"before", d(2026, 9, 1), d(2026, 8, 31), false},
		{"same day", d(2026, 9, 1), d(2026, 9, 1), true},
		{"after", d(2026, 9, 1), d(2026, 9, 2), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ecoEffectiveOn(tc.eff, tc.now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestValidECOTransition(t *testing.T) {
	allowed := [][2]string{{"DRAFT", "APPROVED"}, {"DRAFT", "CANCELLED"}, {"APPROVED", "APPLIED"}, {"APPROVED", "CANCELLED"}}
	for _, p := range allowed {
		if !validECOTransition(p[0], p[1]) {
			t.Fatalf("expected allowed %v", p)
		}
	}
	blocked := [][2]string{{"DRAFT", "APPLIED"}, {"APPROVED", "DRAFT"}, {"APPLIED", "APPROVED"}, {"CANCELLED", "DRAFT"}}
	for _, p := range blocked {
		if validECOTransition(p[0], p[1]) {
			t.Fatalf("expected blocked %v", p)
		}
	}
}

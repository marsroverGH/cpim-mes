package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestClassifyABC(t *testing.T) {
	cases := []struct {
		name string
		pct  float64
		want string
	}{
		{"top item", 35, "A"},
		{"boundary 70", 70, "A"},
		{"just over 70", 70.0001, "B"},
		{"middle B", 85, "B"},
		{"boundary 90", 90, "B"},
		{"just over 90", 90.0001, "C"},
		{"long tail", 99.5, "C"},
		{"final 100", 100, "C"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyABC(c.pct)
			if got != c.want {
				t.Errorf("ClassifyABC(%v) = %v, want %v", c.pct, got, c.want)
			}
		})
	}
}

func TestBuildAnnualDollarUsageABC(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	items := []domain.Item{
		{ID: a, Code: "A", Name: "A", StandardCost: 10},
		{ID: b, Code: "B", Name: "B", StandardCost: 20},
		{ID: c, Code: "C", Name: "C", StandardCost: 5},
	}
	stock := map[uuid.UUID]float64{a: 100, b: 10, c: 500}
	usage := map[uuid.UUID]float64{a: 100, b: 17.5, c: 30} // ¥1000, ¥350, ¥150
	start := time.Date(2025, 8, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)

	rows := BuildAnnualDollarUsageABC(items, stock, usage, start, end)
	if len(rows) != 3 {
		t.Fatalf("len(rows)=%d, want 3", len(rows))
	}
	if rows[0].ItemCode != "A" || rows[0].AnnualUsageValue != 1000 {
		t.Fatalf("first row=%+v, want A with annual usage value 1000", rows[0])
	}
	if rows[1].ItemCode != "B" || rows[1].AnnualUsageValue != 350 {
		t.Fatalf("second row=%+v, want B with annual usage value 350", rows[1])
	}
	if rows[2].ItemCode != "C" || rows[2].AnnualUsageValue != 150 {
		t.Fatalf("third row=%+v, want C with annual usage value 150", rows[2])
	}
	if rows[0].OnHandValue != 1000 || rows[2].OnHandValue != 2500 {
		t.Fatalf("on-hand value should remain reference-only: %+v", rows)
	}
	if rows[0].ABCClass != "A" || rows[1].ABCClass != "B" || rows[2].ABCClass != "C" {
		t.Fatalf("classes=%s/%s/%s, want A/B/C", rows[0].ABCClass, rows[1].ABCClass, rows[2].ABCClass)
	}
	if rows[0].UsageBasis != "ISSUE" || rows[0].CostBasis != "STANDARD_COST" {
		t.Fatalf("unexpected bases: %+v", rows[0])
	}
}

func TestBuildAnnualDollarUsageABC_NoUsageDefaultsToC(t *testing.T) {
	id := uuid.New()
	rows := BuildAnnualDollarUsageABC(
		[]domain.Item{{ID: id, Code: "NO-USAGE", StandardCost: 999}},
		map[uuid.UUID]float64{id: 100},
		map[uuid.UUID]float64{},
		time.Now().AddDate(-1, 0, 1),
		time.Now(),
	)
	if len(rows) != 1 || rows[0].ABCClass != "C" {
		t.Fatalf("no-usage item should be C, got %+v", rows)
	}
}

// IntervalDays — abc.go と同一の頻度ロジック (テスト用にも公開)
func TestIntervalDays(t *testing.T) {
	cases := []struct {
		cls  string
		want int
	}{
		{"A", 7}, {"B", 30}, {"C", 90}, {"unknown", 90},
	}
	for _, c := range cases {
		got := intervalDays(c.cls)
		if got != c.want {
			t.Errorf("intervalDays(%q) = %d, want %d", c.cls, got, c.want)
		}
	}
}

func TestBuildAnnualDollarUsageABC_TopItemIsA(t *testing.T) {
	end := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(-1, 0, 1)
	classes := func(values ...float64) string {
		items := []domain.Item{}
		usage := map[uuid.UUID]float64{}
		for i, v := range values {
			id := uuid.New()
			items = append(items, domain.Item{ID: id, Code: string(rune('A' + i)), StandardCost: 1})
			usage[id] = v
		}
		out := ""
		for _, r := range BuildAnnualDollarUsageABC(items, map[uuid.UUID]float64{}, usage, start, end) {
			out += r.ABCClass
		}
		return out
	}
	cases := []struct {
		name   string
		values []float64
		want   string
	}{
		{"dominant item is A", []float64{91, 5, 4}, "ACC"},
		{"single item is A", []float64{10}, "A"},
		{"top item crossing 90%", []float64{95, 5}, "AC"},
		{"regular Pareto split unchanged", []float64{60, 30, 10}, "ABC"},
		{"equal shares", []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10}, "AAAAAAABBC"},
	}
	for _, c := range cases {
		if got := classes(c.values...); got != c.want {
			t.Errorf("%s: classes %s, want %s", c.name, got, c.want)
		}
	}
}

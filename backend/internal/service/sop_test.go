package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestCalcRCCP_BasicLoad(t *testing.T) {
	bike := uuid.New()
	wcAssy := uuid.New()
	wcPaint := uuid.New()
	in := RCCPInput{
		MPSEntries: []domain.MPSEntry{
			{ItemID: bike, Period: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), Planned: 100},
		},
		Profiles: []domain.RCCPProfile{
			{ItemID: bike, WorkCenterID: wcAssy, MinutesPerUnit: 30},
			{ItemID: bike, WorkCenterID: wcPaint, MinutesPerUnit: 15},
		},
		WorkCenters: []domain.WorkCenter{
			{ID: wcAssy, Code: "WC-ASSY", Name: "Assy", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1},
			{ID: wcPaint, Code: "WC-PAINT", Name: "Paint", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1},
		},
		WorkingDaysPerMonth: 22,
	}
	rows := CalcRCCPLoad(in)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (one per WC), got %d", len(rows))
	}
	for _, r := range rows {
		switch r.WorkCenterCode {
		case "WC-ASSY":
			// 100 units × 30 min = 3000 min
			if r.RequiredMinutes != 3000 {
				t.Errorf("ASSY required expected 3000, got %v", r.RequiredMinutes)
			}
			// 480 × 22 = 10560 available
			if r.AvailableMinutes != 10560 {
				t.Errorf("ASSY avail expected 10560, got %v", r.AvailableMinutes)
			}
		case "WC-PAINT":
			if r.RequiredMinutes != 1500 {
				t.Errorf("PAINT required expected 1500, got %v", r.RequiredMinutes)
			}
		}
	}
}

func TestCalcRCCP_MultipleMonths(t *testing.T) {
	bike := uuid.New()
	wc := uuid.New()
	in := RCCPInput{
		MPSEntries: []domain.MPSEntry{
			{ItemID: bike, Period: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), Planned: 50},
			{ItemID: bike, Period: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Planned: 80},
		},
		Profiles: []domain.RCCPProfile{
			{ItemID: bike, WorkCenterID: wc, MinutesPerUnit: 10},
		},
		WorkCenters: []domain.WorkCenter{
			{ID: wc, Code: "WC1", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1},
		},
		WorkingDaysPerMonth: 22,
	}
	rows := CalcRCCPLoad(in)
	if len(rows) != 2 {
		t.Fatalf("expected 2 monthly rows, got %d", len(rows))
	}
	if !rows[0].Month.Before(rows[1].Month) {
		t.Errorf("rows should be sorted by month: %+v", rows)
	}
}

func TestTruncateMonth(t *testing.T) {
	in := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	out := TruncateMonth(in)
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !out.Equal(want) {
		t.Errorf("TruncateMonth(%v) = %v, want %v", in, out, want)
	}
}

func TestCalcRCCP_OverloadFlag(t *testing.T) {
	bike := uuid.New()
	wc := uuid.New()
	in := RCCPInput{
		MPSEntries: []domain.MPSEntry{
			{ItemID: bike, Period: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Planned: 1000},
		},
		Profiles: []domain.RCCPProfile{
			{ItemID: bike, WorkCenterID: wc, MinutesPerUnit: 60}, // 1000 × 60 = 60000 min required
		},
		WorkCenters: []domain.WorkCenter{
			{ID: wc, Code: "WC1", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1},
		},
		WorkingDaysPerMonth: 22, // 480 × 22 = 10560 avail → overload!
	}
	rows := CalcRCCPLoad(in)
	if rows[0].LoadPct < 100 {
		t.Errorf("expected overload (>100%%), got %.1f%%", rows[0].LoadPct)
	}
}

func TestCalcRCCP_MachineCountMultipliesAvailableCapacity(t *testing.T) {
	item := uuid.New()
	wc := uuid.New()
	rows := CalcRCCPLoad(RCCPInput{
		MPSEntries:          []domain.MPSEntry{{ItemID: item, Period: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Planned: 1}},
		Profiles:            []domain.RCCPProfile{{ItemID: item, WorkCenterID: wc, MinutesPerUnit: 1}},
		WorkCenters:         []domain.WorkCenter{{ID: wc, Code: "WC2", CapacityMinutesPerDay: 480, Efficiency: 1, Utilization: 1, MachineCount: 2}},
		WorkingDaysPerMonth: 22,
	})
	if len(rows) != 1 || rows[0].AvailableMinutes != 21120 {
		t.Fatalf("two machines should provide 21120 minutes/month, got %+v", rows)
	}
}

package service

import (
	"math"
	"testing"
	"time"
)

func bucket(period time.Time, in, out float64) ATPBucketInput {
	return ATPBucketInput{Period: period, ScheduledIn: in, CommittedOut: out}
}

func TestCalcATP_NoActivity(t *testing.T) {
	d := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	res := CalcATP(ATPInput{
		StartingOnHand: 100,
		Buckets: []ATPBucketInput{
			bucket(d, 0, 0),
			bucket(d.AddDate(0, 0, 7), 0, 0),
		},
	})
	if res[0].ATP != 100 {
		t.Errorf("first bucket ATP should equal starting on hand, got %v", res[0].ATP)
	}
	if res[1].ATP != 0 {
		t.Errorf("subsequent buckets with no activity should yield 0 ATP, got %v", res[1].ATP)
	}
	if res[1].EndingProjected != 100 {
		t.Errorf("ending projected should remain 100, got %v", res[1].EndingProjected)
	}
}

func TestCalcATP_CommittedOnlyConsumesStartingOH(t *testing.T) {
	d := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	res := CalcATP(ATPInput{
		StartingOnHand: 50,
		Buckets: []ATPBucketInput{
			bucket(d, 0, 30),                  // 50 - 30 = 20
			bucket(d.AddDate(0, 0, 7), 0, 25), // 20 - 25 = -5 (短期不足)
		},
	})
	if res[0].ATP != 20 {
		t.Errorf("ATP[0] should be 20 (50 OH - 30 commit), got %v", res[0].ATP)
	}
	if res[1].EndingProjected != -5 {
		t.Errorf("ATP[1] ending should be -5 (insufficient), got %v", res[1].EndingProjected)
	}
	if res[1].ATP != 0 {
		t.Errorf("ATP[1] should be 0 (no scheduled in), got %v", res[1].ATP)
	}
}

func TestCalcATP_ScheduledInReplenishment(t *testing.T) {
	d := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	res := CalcATP(ATPInput{
		StartingOnHand: 0,
		Buckets: []ATPBucketInput{
			bucket(d, 100, 30),                 // 0 + 100 - 30 = 70 ATP
			bucket(d.AddDate(0, 0, 7), 50, 20), // 50 - 20 = 30 ATP
			bucket(d.AddDate(0, 0, 14), 0, 40), // 0 ATP (negative)
		},
	})
	if math.Abs(res[0].ATP-70) > 1e-9 {
		t.Errorf("ATP[0] expected 70, got %v", res[0].ATP)
	}
	if math.Abs(res[1].ATP-30) > 1e-9 {
		t.Errorf("ATP[1] expected 30, got %v", res[1].ATP)
	}
	if res[2].ATP != 0 {
		t.Errorf("ATP[2] should clamp to 0, got %v", res[2].ATP)
	}
	// Cumulative ATP は累積
	if math.Abs(res[1].CumulativeATP-100) > 1e-9 {
		t.Errorf("CumulativeATP[1] expected 100 (70+30), got %v", res[1].CumulativeATP)
	}
}

func TestCalcATP_EndingProjectedRollsOver(t *testing.T) {
	d := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	res := CalcATP(ATPInput{
		StartingOnHand: 10,
		Buckets: []ATPBucketInput{
			bucket(d, 5, 3),                  // ending: 10+5-3 = 12
			bucket(d.AddDate(0, 0, 7), 0, 2), // 12 - 2 = 10
		},
	})
	if res[0].EndingProjected != 12 {
		t.Errorf("EndingProjected[0] expected 12, got %v", res[0].EndingProjected)
	}
	if res[1].StartingOnHand != 12 {
		t.Errorf("StartingOnHand[1] should equal EndingProjected[0]=12, got %v", res[1].StartingOnHand)
	}
	if res[1].EndingProjected != 10 {
		t.Errorf("EndingProjected[1] expected 10, got %v", res[1].EndingProjected)
	}
}

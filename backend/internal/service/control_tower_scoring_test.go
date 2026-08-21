package service

import "testing"

func TestControlTowerMaterialShortageForcedP1(t *testing.T) {
	got := ScoreControlTowerPriority(ControlTowerScoreInput{
		Severity:         "CRITICAL",
		ImpactDays:       0,
		RevenueAtRisk:    50000,
		OrderPriority:    "NORMAL",
		ServiceClassRank: 3,
		ExceptionType:    "MATERIAL_SHORTAGE",
		AgeHours:         1,
	})

	if got.MaterialScore != 100 {
		t.Fatalf("material score: want 100 got %v", got.MaterialScore)
	}
	if !got.ForcedP1 {
		t.Fatalf("material shortage must force P1")
	}
	if got.PriorityBand != "P1" {
		t.Fatalf("band: want P1 got %s score=%v", got.PriorityBand, got.PriorityScore)
	}
	if got.PriorityScore < 75 {
		t.Fatalf("forced P1 score must be >=75, got %v", got.PriorityScore)
	}
}

func TestControlTowerDispatchBlockedForcedP1(t *testing.T) {
	got := ScoreControlTowerPriority(ControlTowerScoreInput{
		Severity:         "CRITICAL",
		OrderPriority:    "NORMAL",
		ServiceClassRank: 3,
		ExceptionType:    "DISPATCH_BLOCKED",
	})

	if got.ExecutionScore != 100 {
		t.Fatalf("execution score: want 100 got %v", got.ExecutionScore)
	}
	if !got.ForcedP1 || got.PriorityBand != "P1" {
		t.Fatalf("dispatch blocked must force P1: %+v", got)
	}
}

func TestControlTowerHighRevenueSupplierDelay(t *testing.T) {
	got := ScoreControlTowerPriority(ControlTowerScoreInput{
		Severity:         "CRITICAL",
		ImpactDays:       12,
		RevenueAtRisk:    6000000,
		OrderPriority:    "EXPEDITE",
		ServiceClassRank: 1,
		ExceptionType:    "SUPPLIER_CONFIRMATION_LATE",
		AgeHours:         400,
	})

	if got.RevenueScore != 100 {
		t.Fatalf("revenue score: want 100 got %v", got.RevenueScore)
	}
	if got.SupplierScore != 90 {
		t.Fatalf("supplier score: want 90 got %v", got.SupplierScore)
	}
	if got.CustomerScore != 100 {
		t.Fatalf("customer score: want 100 got %v", got.CustomerScore)
	}
	if got.PriorityBand != "P1" {
		t.Fatalf("expected P1, got %s score=%v", got.PriorityBand, got.PriorityScore)
	}
}

func TestControlTowerLowRiskIsP4(t *testing.T) {
	got := ScoreControlTowerPriority(ControlTowerScoreInput{
		Severity:         "INFO",
		ImpactDays:       0,
		RevenueAtRisk:    0,
		OrderPriority:    "NORMAL",
		ServiceClassRank: 5,
		ExceptionType:    "LATE_PROMISE",
		AgeHours:         2,
	})

	if got.ForcedP1 {
		t.Fatal("low-risk exception must not be forced P1")
	}
	if got.PriorityBand != "P4" {
		t.Fatalf("expected P4, got %s score=%v", got.PriorityBand, got.PriorityScore)
	}
}

func TestControlTowerScoresRemainBounded(t *testing.T) {
	got := ScoreControlTowerPriority(ControlTowerScoreInput{
		Severity:         "CRITICAL",
		ImpactDays:       999,
		RevenueAtRisk:    999999999,
		OrderPriority:    "EXPEDITE",
		ServiceClassRank: 1,
		ExceptionType:    "MATERIAL_SHORTAGE",
		AgeHours:         99999,
	})

	if got.PriorityScore < 0 || got.PriorityScore > 100 {
		t.Fatalf("priority score outside 0..100: %v", got.PriorityScore)
	}
}

package service

import "testing"

func TestControlTowerRecommendSupplierDelay(t *testing.T) {
	got := RecommendControlTowerActions(ControlTowerRecommendationInput{
		ExceptionType: "SUPPLIER_CONFIRMATION_LATE",
		Message:       "PO-100 confirmation is late",
		RootCauseRef:  "PO:PO-100",
	})

	if len(got) < 2 {
		t.Fatalf("expected >=2 recommendations, got %d", len(got))
	}
	if got[0].ActionType != "EXPEDITE_PO" {
		t.Fatalf("first recommendation=%s", got[0].ActionType)
	}
	if got[1].ActionType != "RECALCULATE_PROMISE" {
		t.Fatalf("second recommendation=%s", got[1].ActionType)
	}
}

func TestControlTowerRecommendCapacity(t *testing.T) {
	got := RecommendControlTowerActions(ControlTowerRecommendationInput{
		ExceptionType: "CAPACITY_UNSCHEDULED",
		RootCauseRef:  "WC:PAINT",
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(got))
	}
	if got[0].ActionType != "RESCHEDULE_WO" {
		t.Fatalf("first recommendation=%s", got[0].ActionType)
	}
	if got[1].ActionType != "ALTERNATE_WORK_CENTER" {
		t.Fatalf("second recommendation=%s", got[1].ActionType)
	}
}

func TestControlTowerRecommendFrozenConflictRequiresApproval(t *testing.T) {
	got := RecommendControlTowerActions(ControlTowerRecommendationInput{
		ExceptionType: "FROZEN_HORIZON_CONFLICT",
		RootCauseRef:  "RESCHEDULE:123",
	})

	if len(got) == 0 {
		t.Fatal("expected recommendation")
	}
	if got[0].ActionType != "REVIEW_FROZEN_CONFLICT" {
		t.Fatalf("unexpected first action %s", got[0].ActionType)
	}
	if !got[0].RequiresApproval {
		t.Fatal("frozen conflict review should require approval")
	}
}

func TestControlTowerRecommendUnknownFallsBackToManualReview(t *testing.T) {
	got := RecommendControlTowerActions(ControlTowerRecommendationInput{
		ExceptionType: "UNKNOWN_EXCEPTION",
	})

	if len(got) != 1 || got[0].ActionType != "MANUAL_REVIEW" {
		t.Fatalf("unexpected recommendations %+v", got)
	}
}

func TestControlTowerRecommendationRanksAreSequential(t *testing.T) {
	got := RecommendControlTowerActions(ControlTowerRecommendationInput{
		ExceptionType: "MATERIAL_SHORTAGE",
	})

	for i, x := range got {
		want := i + 1
		if x.RankNo != want {
			t.Fatalf("recommendation %d rank=%d want=%d", i, x.RankNo, want)
		}
	}
}

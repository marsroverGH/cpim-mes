package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestRecoveryBaselineHashStableAcrossOrdering(
	t *testing.T,
) {
	scenario := domain.RecoveryScenario{
		BaselineAsOf: time.Date(
			2026,
			8,
			22,
			0,
			0,
			0,
			0,
			time.UTC,
		),
	}

	a := recoveryBaselineCase{
		CaseID: uuid.MustParse(
			"11111111-1111-1111-1111-111111111111",
		),

		PlanningExceptionID: uuid.MustParse(
			"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		),

		CurrentStatus: "OPEN",

		PriorityBand: "P1",

		PriorityScore: 80,

		RevenueAtRisk: 1_000_000,

		ImpactDays: 5,

		ExceptionType: "LATE_PURCHASE_ORDER",

		RootCauseType: "LATE_PURCHASE_ORDER",

		RootCauseRef: "PO:A",

		SnapshotResultHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	b := recoveryBaselineCase{
		CaseID: uuid.MustParse(
			"22222222-2222-2222-2222-222222222222",
		),

		PlanningExceptionID: uuid.MustParse(
			"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		),

		CurrentStatus: "OPEN",

		PriorityBand: "P2",

		PriorityScore: 60,

		RevenueAtRisk: 500_000,

		ImpactDays: 2,

		ExceptionType: "CAPACITY_LATE",

		RootCauseType: "CAPACITY_LATE",

		RootCauseRef: "WC:B",

		SnapshotResultHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}

	h1, err := recoveryBaselineHash(
		scenario,
		[]recoveryBaselineCase{
			a,
			b,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	h2, err := recoveryBaselineHash(
		scenario,
		[]recoveryBaselineCase{
			b,
			a,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if h1 != h2 {
		t.Fatalf(
			"baseline hash depends on row order: %s != %s",
			h1,
			h2,
		)
	}

	if len(h1) != 64 {
		t.Fatalf(
			"baseline hash length=%d",
			len(h1),
		)
	}
}

func TestRecoveryRequestHashCanonicalJSON(
	t *testing.T,
) {
	a := domain.RecoveryScenarioAction{
		ID: uuid.MustParse(
			"11111111-1111-1111-1111-111111111111",
		),

		SequenceNo: 1,

		ActionType: "EXPEDITE_PO",

		TargetType: "PURCHASE_ORDER",

		TargetRef: "PO:1",

		Parameters: json.RawMessage(
			`{"expediteDays":5,"note":"x"}`,
		),

		EstimatedCost: 100000,
	}

	b := a

	b.Parameters =
		json.RawMessage(
			`{"note":"x","expediteDays":5}`,
		)

	baseline :=
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	h1, err := recoveryRequestHash(
		baseline,
		90,
		[]domain.RecoveryScenarioAction{
			a,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	h2, err := recoveryRequestHash(
		baseline,
		90,
		[]domain.RecoveryScenarioAction{
			b,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if h1 != h2 {
		t.Fatalf(
			"JSON property order changed request hash: %s != %s",
			h1,
			h2,
		)
	}
}

func TestRecoveryRequestHashChangesWithHorizon(
	t *testing.T,
) {
	action := domain.RecoveryScenarioAction{
		ID: uuid.New(),

		SequenceNo: 1,

		ActionType: "ADD_OVERTIME_CAPACITY",

		TargetType: "WORK_CENTER",

		TargetRef: "WC:1",

		Parameters: json.RawMessage(
			`{"overtimeMinutes":480}`,
		),
	}

	baseline :=
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	h30, err := recoveryRequestHash(
		baseline,
		30,
		[]domain.RecoveryScenarioAction{
			action,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	h90, err := recoveryRequestHash(
		baseline,
		90,
		[]domain.RecoveryScenarioAction{
			action,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if h30 == h90 {
		t.Fatal(
			"different horizons must produce different request hashes",
		)
	}
}

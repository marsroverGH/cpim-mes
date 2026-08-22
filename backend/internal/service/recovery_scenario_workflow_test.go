package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestRecoveryScenarioNoStableFormat(
	t *testing.T,
) {
	id := uuid.MustParse(
		"12345678-1111-2222-3333-444444444444",
	)

	got := recoveryScenarioNo(
		time.Date(
			2026,
			8,
			22,
			10,
			0,
			0,
			0,
			time.UTC,
		),
		id,
	)

	if got != "REC-20260822-12345678" {
		t.Fatalf(
			"unexpected scenario no: %s",
			got,
		)
	}
}

func TestRecoveryScenarioStatusValidation(
	t *testing.T,
) {
	got, err :=
		normalizeRecoveryScenarioStatus(
			" simulated ",
		)
	if err != nil {
		t.Fatal(err)
	}

	if got != "SIMULATED" {
		t.Fatalf(
			"status=%s",
			got,
		)
	}

	if _, err :=
		normalizeRecoveryScenarioStatus(
			"INVALID",
		); err == nil {
		t.Fatal(
			"invalid scenario status must fail",
		)
	}
}

func TestRecoveryActionInputNormalizes(
	t *testing.T,
) {
	got, err :=
		normalizeRecoveryScenarioAction(
			uuid.New(),
			uuid.New(),
			1,
			RecoveryScenarioActionInput{
				ActionType: " expedite_po ",

				TargetType: " purchase_order ",

				TargetRef: " PO:123 ",

				Parameters: json.RawMessage(
					`{"expediteDays":5}`,
				),

				EstimatedCost: 100000,
			},
		)

	if err != nil {
		t.Fatal(err)
	}

	if got.ActionType != "EXPEDITE_PO" {
		t.Fatalf(
			"actionType=%s",
			got.ActionType,
		)
	}

	if got.TargetType != "PURCHASE_ORDER" {
		t.Fatalf(
			"targetType=%s",
			got.TargetType,
		)
	}

	if got.TargetRef != "PO:123" {
		t.Fatalf(
			"targetRef=%s",
			got.TargetRef,
		)
	}
}

func TestRecoveryActionRejectsNegativeCost(
	t *testing.T,
) {
	_, err :=
		normalizeRecoveryScenarioAction(
			uuid.New(),
			uuid.New(),
			1,
			RecoveryScenarioActionInput{
				ActionType: "EXPEDITE_PO",

				TargetType: "PURCHASE_ORDER",

				TargetRef: "PO:1",

				Parameters: json.RawMessage(
					`{"expediteDays":1}`,
				),

				EstimatedCost: -1,
			},
		)

	if err == nil {
		t.Fatal(
			"negative recovery action cost must fail",
		)
	}
}

func TestRecoveryPublicationHashStable(
	t *testing.T,
) {
	resultHash :=
		strings.Repeat(
			"a",
			64,
		)

	scenario := domain.RecoveryScenario{
		ID: uuid.MustParse(
			"11111111-1111-1111-1111-111111111111",
		),
	}

	run := domain.RecoveryScenarioRun{
		ID: uuid.MustParse(
			"22222222-2222-2222-2222-222222222222",
		),

		BaselineHash: strings.Repeat(
			"b",
			64,
		),

		RequestHash: strings.Repeat(
			"c",
			64,
		),

		ResultHash: &resultHash,
	}

	summary := domain.RecoveryScenarioSummary{
		ResultHash: strings.Repeat(
			"d",
			64,
		),
	}

	h1, err :=
		recoveryPublicationHash(
			scenario,
			run,
			summary,
			" approved ",
		)
	if err != nil {
		t.Fatal(err)
	}

	h2, err :=
		recoveryPublicationHash(
			scenario,
			run,
			summary,
			"approved",
		)
	if err != nil {
		t.Fatal(err)
	}

	if h1 != h2 {
		t.Fatal(
			"publication hash must normalize comment whitespace",
		)
	}

	if len(h1) != 64 {
		t.Fatalf(
			"publication hash length=%d",
			len(h1),
		)
	}
}

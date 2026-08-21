package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestControlTowerCaseKeyIsStable(t *testing.T) {
	so := uuid.New()
	line := uuid.New()

	x := controlTowerSourceRow{
		SalesOrderID:     so,
		SalesOrderLineID: &line,
		ExceptionType:    "MATERIAL_SHORTAGE",
		ExceptionKey:     "SHORT:COMP-A",
	}

	a := controlTowerCaseKey(x)
	b := controlTowerCaseKey(x)

	if a == "" || a != b {
		t.Fatalf("case key not stable: %q / %q", a, b)
	}
}

func TestControlTowerCaseKeySeparatesRootCauses(t *testing.T) {
	so := uuid.New()
	line := uuid.New()

	a := controlTowerCaseKey(controlTowerSourceRow{
		SalesOrderID:     so,
		SalesOrderLineID: &line,
		ExceptionType:    "MATERIAL_SHORTAGE",
		ExceptionKey:     "SHORT:COMP-A",
	})

	b := controlTowerCaseKey(controlTowerSourceRow{
		SalesOrderID:     so,
		SalesOrderLineID: &line,
		ExceptionType:    "MATERIAL_SHORTAGE",
		ExceptionKey:     "SHORT:COMP-B",
	})

	if a == b {
		t.Fatal("different root causes must not collapse into one Control Tower case")
	}
}

func TestControlTowerRootRefUsesLastCausalPathNode(t *testing.T) {
	path := json.RawMessage(`[
		"SO:100",
		"WO:200",
		"WC:PAINT"
	]`)

	got := controlTowerRootRef(path, nil)
	if got != "WC:PAINT" {
		t.Fatalf("root ref=%q", got)
	}
}

func TestControlTowerRootRefFallsBackToDetail(t *testing.T) {
	detail := json.RawMessage(`{"poNo":"PO-900"}`)

	got := controlTowerRootRef(nil, detail)
	if got != "PO-900" {
		t.Fatalf("root ref=%q", got)
	}
}

func TestCanonicalControlTowerSnapshotHashIsStable(t *testing.T) {
	ex := uuid.New()
	run := uuid.New()

	in := controlTowerHashInput{
		CaseKey:             "CT-test",
		PlanningExceptionID: ex,
		PeggingRunID:        run,
		Severity:            "CRITICAL",
		ImpactDays:          5,
		OrderValue:          1000000,
		OpenOrderValue:      500000,
		OrderPriority:       "HIGH",
		ServiceClassCode:    "A",
		RevenueAtRisk:       500000,
		RootCauseType:       "CAPACITY_LATE",
		RootCauseRef:        "WC:PAINT",
		Score: ControlTowerScore{
			SeverityScore: 100,
			PriorityScore: 80,
			PriorityBand:  "P1",
		},
	}

	a, err := canonicalControlTowerSnapshotHash(in)
	if err != nil {
		t.Fatal(err)
	}

	b, err := canonicalControlTowerSnapshotHash(in)
	if err != nil {
		t.Fatal(err)
	}

	if len(a) != 64 {
		t.Fatalf("hash length=%d", len(a))
	}

	if a != b {
		t.Fatalf("canonical hash changed: %s / %s", a, b)
	}
}

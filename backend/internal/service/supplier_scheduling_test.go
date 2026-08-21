package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestPurchasePlanningDatePrefersExpectedEvidence(t *testing.T) {
	due := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	expected := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	p := domain.PurchaseOrder{DueDate: due, ExpectedDeliveryDate: &expected}
	if got := PurchasePlanningDate(p); !got.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected reliability/supplier planning date, got %v", got)
	}
}

func TestPurchasePlanningDateFallsBackToPODueDate(t *testing.T) {
	due := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	if got := PurchasePlanningDate(domain.PurchaseOrder{DueDate: due}); !got.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected PO due date fallback, got %v", got)
	}
}

func TestRecommendedSupplierLeadDaysUsesConservativeTail(t *testing.T) {
	if got := recommendedSupplierLeadDays(7.2, 2.4, 8.0); got != 10 {
		t.Fatalf("expected ceil(max(p90,avg+stddev))=10, got %d", got)
	}
	if got := recommendedSupplierLeadDays(4, 0, 6.1); got != 7 {
		t.Fatalf("expected p90 tail rounded up, got %d", got)
	}
}

func TestSupplierReliabilityConfidence(t *testing.T) {
	if got := supplierReliabilityConfidence(2, 3); got != "LOW" {
		t.Fatalf("expected LOW, got %s", got)
	}
	if got := supplierReliabilityConfidence(3, 3); got != "MEDIUM" {
		t.Fatalf("expected MEDIUM, got %s", got)
	}
	if got := supplierReliabilityConfidence(10, 3); got != "HIGH" {
		t.Fatalf("expected HIGH, got %s", got)
	}
}

func TestSameSupplierScheduleEventSupportsIdempotentRetry(t *testing.T) {
	poID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	userID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	qty := 12.0
	day := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	actor := SalesOrderActor{UserID: userID, Username: "planner", Role: domain.RolePlanner}
	e := domain.SupplierScheduleEvent{
		PurchaseOrderID: poID, EventType: "CONFIRM", Quantity: &qty,
		ConfirmedDeliveryDate: &day, SupplierReference: "C-1", Notes: "same",
		ActorUserID: userID, ActorUsername: "planner",
	}
	if !sameSupplierScheduleEvent(e, poID, "CONFIRM", &qty, &day, "", nil, "C-1", "same", actor) {
		t.Fatal("identical retry must be recognized")
	}
	changed := qty + 1
	if sameSupplierScheduleEvent(e, poID, "CONFIRM", &changed, &day, "", nil, "C-1", "same", actor) {
		t.Fatal("changed retry must not be treated as idempotent")
	}
}

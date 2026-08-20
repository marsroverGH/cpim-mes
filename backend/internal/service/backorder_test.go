package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestBOPPriorityRank(t *testing.T) {
	if !(bopPriorityRank("EXPEDITE") < bopPriorityRank("HIGH") && bopPriorityRank("HIGH") < bopPriorityRank("NORMAL")) {
		t.Fatal("expected EXPEDITE < HIGH < NORMAL")
	}
}

func TestBackorderDecision(t *testing.T) {
	today := TruncateDay(time.Now())
	tomorrow := today.AddDate(0, 0, 1)
	yesterday := today.AddDate(0, 0, -1)
	tests := []struct {
		name      string
		current   *time.Time
		proposed  *time.Time
		backorder float64
		want      string
	}{
		{"backorder wins", &today, &today, 1, "BACKORDER"},
		{"nil proposal is backorder", &today, nil, 0, "BACKORDER"},
		{"new promise", nil, &today, 0, "NEW_PROMISE"},
		{"improved", &today, &yesterday, 0, "IMPROVED"},
		{"delayed", &today, &tomorrow, 0, "DELAYED"},
		{"unchanged", &today, &today, 0, "UNCHANGED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backorderDecision(tt.current, tt.proposed, tt.backorder); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestCanonicalBackorderHashDetectsChange(t *testing.T) {
	lineID := uuid.New()
	orderID := uuid.New()
	itemID := uuid.New()
	customerID := uuid.New()
	d := TruncateDay(time.Now()).AddDate(0, 0, 7)
	lines := []domain.BackorderRunLine{{SalesOrderID: orderID, SalesOrderLineID: lineID, ItemID: itemID, CustomerID: customerID, RankNo: 1, OpenQty: 10, ATPQty: 10, ProposedPromisedDate: &d, Decision: "UNCHANGED", ConstraintType: "NONE"}}
	confs := []domain.BackorderRunConfirmation{{SalesOrderLineID: lineID, SequenceNo: 1, Quantity: 10, ConfirmedDate: d, Source: "ATP"}}
	a := canonicalBackorderHash(90, nil, lines, confs)
	b := canonicalBackorderHash(90, nil, lines, confs)
	if a != b || len(a) != 64 {
		t.Fatalf("hash must be stable SHA-256: %q %q", a, b)
	}
	lines[0].ATPQty = 9
	if c := canonicalBackorderHash(90, nil, lines, confs); c == a {
		t.Fatal("hash must change when allocation result changes")
	}
}

func TestUniqueBOPResourcesDeduplicatesAndSorts(t *testing.T) {
	o1, o2 := uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")
	i1, i2 := uuid.MustParse("10000000-0000-0000-0000-000000000001"), uuid.MustParse("10000000-0000-0000-0000-000000000002")
	orders, items := uniqueBOPResources([]domain.BackorderRunLine{{SalesOrderID: o2, ItemID: i2}, {SalesOrderID: o1, ItemID: i1}, {SalesOrderID: o1, ItemID: i1}})
	if len(orders) != 2 || orders[0] != o1 || orders[1] != o2 {
		t.Fatalf("unexpected order resources: %v", orders)
	}
	if len(items) != 2 || items[0] != i1 || items[1] != i2 {
		t.Fatalf("unexpected item resources: %v", items)
	}
}

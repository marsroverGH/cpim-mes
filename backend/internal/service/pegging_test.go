package service

import (
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

func TestPeggingSeverityEscalatesMaterialAndCapacity(t *testing.T) {
	if got := severityFor("MATERIAL_SHORTAGE", 0); got != "CRITICAL" {
		t.Fatalf("material shortage severity=%s", got)
	}
	if got := severityFor("CAPACITY_LATE", 3); got != "WARNING" {
		t.Fatalf("short capacity delay severity=%s", got)
	}
	if got := severityFor("CAPACITY_LATE", 8); got != "CRITICAL" {
		t.Fatalf("long capacity delay severity=%s", got)
	}
}

func TestPeggingCanonicalHashIgnoresUUIDsAndOrdering(t *testing.T) {
	build := func(reverse bool) *graphBuilder {
		run := uuid.New()
		g := newGraphBuilder(run)
		if reverse {
			b := g.node("B", "ITEM", "B", nil, "", nil, "B", floatPtr(2), nil, "REQUIRED", map[string]any{"x": 1})
			a := g.node("A", "SALES_ORDER", "A", nil, "", nil, "", nil, nil, "CONFIRMED", nil)
			g.edge(a, b, "HAS_LINE", floatPtr(2), nil)
		} else {
			a := g.node("A", "SALES_ORDER", "A", nil, "", nil, "", nil, nil, "CONFIRMED", nil)
			b := g.node("B", "ITEM", "B", nil, "", nil, "B", floatPtr(2), nil, "REQUIRED", map[string]any{"x": 1})
			g.edge(a, b, "HAS_LINE", floatPtr(2), nil)
		}
		return g
	}
	if a, b := canonicalPeggingHash(build(false)), canonicalPeggingHash(build(true)); a != b {
		t.Fatalf("canonical hash differs: %s %s", a, b)
	}
}

func TestPeggingGraphDeduplicatesNodeAndEdge(t *testing.T) {
	g := newGraphBuilder(uuid.New())
	a := g.node("A", "SALES_ORDER", "A", nil, "", nil, "", nil, nil, "", nil)
	a2 := g.node("A", "SALES_ORDER", "A2", nil, "", nil, "", nil, nil, "", nil)
	if a != a2 || len(g.nodes) != 1 {
		t.Fatalf("node dedup failed: %v %v len=%d", a, a2, len(g.nodes))
	}
	b := g.node("B", "ITEM", "B", nil, "", nil, "B", nil, nil, "", nil)
	g.edge(a, b, "HAS_LINE", nil, nil)
	g.edge(a, b, "HAS_LINE", nil, nil)
	if len(g.edges) != 1 {
		t.Fatalf("edge dedup failed: %d", len(g.edges))
	}
}

func TestPeggingExceptionDeduplicatesRootCause(t *testing.T) {
	g := newGraphBuilder(uuid.New())
	root := g.node("SHORT:X", "SHORTAGE", "short", nil, "", nil, "X", floatPtr(1), nil, "OPEN", nil)
	o := peggingOrder{ID: uuid.New(), OrderNo: "SO-X"}
	l := peggingLine{ID: uuid.New(), RequestedDate: time.Now()}
	g.exception(o, &l, "MATERIAL_SHORTAGE", "CRITICAL", root, "short", &l.RequestedDate, nil, nil, 0, []string{"SHORT:X"}, nil)
	g.exception(o, &l, "MATERIAL_SHORTAGE", "CRITICAL", root, "short again", &l.RequestedDate, nil, nil, 0, []string{"SHORT:X"}, nil)
	if len(g.exceptions) != 1 {
		t.Fatalf("exception dedup failed: %d", len(g.exceptions))
	}
}

func TestPeggingDateLateUsesCalendarDays(t *testing.T) {
	req := time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC)
	act := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	if got := daysLate(act, req); got != 3 {
		t.Fatalf("daysLate=%d", got)
	}
}

func TestPeggingResultDomainShape(t *testing.T) {
	_ = domain.PeggingResult{Run: domain.PeggingRun{ID: uuid.New()}, Nodes: []domain.PeggingNode{}, Edges: []domain.PeggingEdge{}, Exceptions: []domain.PlanningException{}}
}

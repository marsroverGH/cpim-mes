package service

import (
	"testing"

	"github.com/google/uuid"
)

func TestFindBOMCycle_AcyclicChain(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	edges := []bomGraphEdge{
		{ParentID: a, ChildID: b},
		{ParentID: b, ChildID: c},
		{ParentID: c, ChildID: d},
	}
	if cycle := findBOMCycle(edges); cycle != nil {
		t.Fatalf("unexpected cycle: %v", cycle)
	}
}

func TestFindBOMCycle_DiamondIsNotCycle(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	edges := []bomGraphEdge{
		{ParentID: a, ChildID: b},
		{ParentID: a, ChildID: c},
		{ParentID: b, ChildID: d},
		{ParentID: c, ChildID: d},
	}
	if cycle := findBOMCycle(edges); cycle != nil {
		t.Fatalf("diamond DAG must not be treated as a cycle: %v", cycle)
	}
}

func TestFindBOMCycle_ThreeNodeCycle(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	edges := []bomGraphEdge{
		{ParentID: a, ChildID: b},
		{ParentID: b, ChildID: c},
		{ParentID: c, ChildID: a},
	}
	cycle := findBOMCycle(edges)
	if len(cycle) != 4 {
		t.Fatalf("expected 3-edge cycle with repeated start, got %v", cycle)
	}
	if cycle[0] != cycle[len(cycle)-1] {
		t.Fatalf("cycle path must repeat its start: %v", cycle)
	}
}

func TestFindBOMCycle_ProspectiveBackEdgeDetected(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	// Existing A->B->C is valid. Adding C->A must be rejected.
	edges := []bomGraphEdge{
		{ParentID: a, ChildID: b},
		{ParentID: b, ChildID: c},
		{ParentID: c, ChildID: a},
	}
	if cycle := findBOMCycle(edges); cycle == nil {
		t.Fatal("expected candidate back-edge to create a cycle")
	}
}

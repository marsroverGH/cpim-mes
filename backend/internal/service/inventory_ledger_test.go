package service

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateAllocationSum_ExactMatch(t *testing.T) {
	req := PhysicalInventoryRequest{Quantity: -10, TxnType: "ISSUE"}
	allocs := []LotAllocationInput{
		{LotID: uuid.New(), Quantity: -4, MovementType: "ISSUE"},
		{LotID: uuid.New(), Quantity: -6, MovementType: "ISSUE"},
	}
	if err := validateAllocationSum(req, allocs); err != nil {
		t.Fatalf("exact lot allocation should be valid: %v", err)
	}
}

func TestValidateAllocationSum_RejectsMismatch(t *testing.T) {
	req := PhysicalInventoryRequest{Quantity: 10, TxnType: "RECEIPT"}
	allocs := []LotAllocationInput{{LotID: uuid.New(), Quantity: 9, MovementType: "RECEIPT"}}
	if err := validateAllocationSum(req, allocs); err == nil {
		t.Fatal("expected mismatched allocation sum to be rejected")
	}
}

func TestValidateAllocationSum_RejectsWrongSign(t *testing.T) {
	req := PhysicalInventoryRequest{Quantity: -10, TxnType: "ISSUE"}
	allocs := []LotAllocationInput{{LotID: uuid.New(), Quantity: 10, MovementType: "ISSUE"}}
	if err := validateAllocationSum(req, allocs); err == nil {
		t.Fatal("expected lot allocation sign mismatch to be rejected")
	}
}

func TestDefaultMovementType(t *testing.T) {
	cases := []struct {
		txn, override, want string
	}{
		{"RECEIPT", "", "RECEIPT"},
		{"ISSUE", "", "ISSUE"},
		{"ADJUST", "", "ADJUST"},
		{"ISSUE", "CONSUMED", "CONSUMED"},
		{"RECEIPT", "PRODUCED", "PRODUCED"},
	}
	for _, tc := range cases {
		if got := defaultMovementType(tc.txn, tc.override); got != tc.want {
			t.Fatalf("defaultMovementType(%q,%q): want %q got %q", tc.txn, tc.override, tc.want, got)
		}
	}
}

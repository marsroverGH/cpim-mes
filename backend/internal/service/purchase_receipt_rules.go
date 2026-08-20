package service

import "fmt"

const purchaseQtyEpsilon = 0.000001

// PurchaseReceiptState is the pure quantity/status rule shared by the PO receipt workflow.
type PurchaseReceiptState struct {
	NewReceived float64
	Remaining   float64
	Status      string
}

// CalcPurchaseReceiptState rejects non-positive and over receipts and returns the
// PO state after applying one receipt. It deliberately does not permit receipts
// against a closed/finalized PO; the caller performs status eligibility checks.
func CalcPurchaseReceiptState(ordered, alreadyReceived, receiptQty float64) (PurchaseReceiptState, error) {
	if ordered <= 0 {
		return PurchaseReceiptState{}, fmt.Errorf("ordered quantity must be positive")
	}
	if alreadyReceived < -purchaseQtyEpsilon || alreadyReceived > ordered+purchaseQtyEpsilon {
		return PurchaseReceiptState{}, fmt.Errorf("existing received quantity is outside PO quantity")
	}
	if receiptQty <= purchaseQtyEpsilon {
		return PurchaseReceiptState{}, fmt.Errorf("receipt quantity must be positive")
	}
	remainingBefore := ordered - alreadyReceived
	if receiptQty > remainingBefore+purchaseQtyEpsilon {
		return PurchaseReceiptState{}, fmt.Errorf("receipt quantity %.6g exceeds remaining quantity %.6g", receiptQty, remainingBefore)
	}
	newReceived := alreadyReceived + receiptQty
	if newReceived > ordered && newReceived <= ordered+purchaseQtyEpsilon {
		newReceived = ordered
	}
	remaining := ordered - newReceived
	if remaining < purchaseQtyEpsilon {
		remaining = 0
		newReceived = ordered
		return PurchaseReceiptState{NewReceived: newReceived, Remaining: 0, Status: "RECEIVED"}, nil
	}
	return PurchaseReceiptState{NewReceived: newReceived, Remaining: remaining, Status: "PARTIALLY_RECEIVED"}, nil
}

// PurchaseScheduledRemaining returns the quantity that is still a firm incoming
// supply for MRP/ATP. CLOSED and RECEIVED POs contribute no scheduled supply.
func PurchaseScheduledRemaining(status string, ordered, received float64) float64 {
	if status != "OPEN" && status != "PARTIALLY_RECEIVED" {
		return 0
	}
	remaining := ordered - received
	if remaining <= purchaseQtyEpsilon {
		return 0
	}
	return remaining
}

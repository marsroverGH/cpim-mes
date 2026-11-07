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
	ord, got, qty := dq(ordered), dq(alreadyReceived), dq(receiptQty)
	if got.LessThan(qtyEpsilon.Neg()) || got.GreaterThan(ord.Add(qtyEpsilon)) {
		return PurchaseReceiptState{}, fmt.Errorf("existing received quantity is outside PO quantity")
	}
	if qty.LessThanOrEqual(qtyEpsilon) {
		return PurchaseReceiptState{}, fmt.Errorf("receipt quantity must be positive")
	}
	remainingBefore := ord.Sub(got)
	if qty.GreaterThan(remainingBefore.Add(qtyEpsilon)) {
		return PurchaseReceiptState{}, fmt.Errorf("receipt quantity %.6g exceeds remaining quantity %.6g", receiptQty, fq(remainingBefore))
	}
	newReceived := got.Add(qty)
	if newReceived.GreaterThan(ord) {
		newReceived = ord // within the tolerance checked above
	}
	remaining := ord.Sub(newReceived)
	if remaining.LessThan(qtyEpsilon) {
		return PurchaseReceiptState{NewReceived: fq(ord), Remaining: 0, Status: "RECEIVED"}, nil
	}
	return PurchaseReceiptState{NewReceived: fq(newReceived), Remaining: fq(remaining), Status: "PARTIALLY_RECEIVED"}, nil
}

// PurchaseScheduledRemaining returns the quantity that is still a firm incoming
// supply for MRP/ATP. CLOSED and RECEIVED POs contribute no scheduled supply.
func PurchaseScheduledRemaining(status string, ordered, received float64) float64 {
	if status != "OPEN" && status != "PARTIALLY_RECEIVED" {
		return 0
	}
	remaining := dq(ordered).Sub(dq(received))
	if remaining.LessThanOrEqual(qtyEpsilon) {
		return 0
	}
	return fq(remaining)
}

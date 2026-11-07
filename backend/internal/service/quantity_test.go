package service

import "testing"

// Quantities on the main paths are computed in decimal: repeated small
// quantities must add up exactly, with no float artefacts and no tolerance.
func TestQuantityArithmeticIsDecimal(t *testing.T) {
	tenths := make([]float64, 10)
	for i := range tenths {
		tenths[i] = 0.1
	}
	if got := sumQty(tenths...); got != 1 {
		t.Fatalf("sumQty(10 x 0.1) = %v, want 1", got)
	}
	acc := 0.0
	for range tenths {
		acc = addQty(acc, 0.1)
	}
	if acc != 1 {
		t.Fatalf("addQty accumulated 10 x 0.1 = %v, want 1", acc)
	}
	if got := subQty(1.0, 0.7); got != 0.3 {
		t.Fatalf("subQty(1.0, 0.7) = %v, want 0.3", got)
	}
	if got := bomRequiredQty(3, 0.1, 0.1); got != 0.33 {
		t.Fatalf("bomRequiredQty(3, 0.1, 10%%) = %v, want 0.33", got)
	}
	if got := directComponentRequirement(3, 0.1, 0.1); got != 0.33 {
		t.Fatalf("directComponentRequirement(3, 0.1, 10%%) = %v, want 0.33", got)
	}
	// Half away from zero at the QuantityScale-th place, like numeric rounding.
	if got := RoundQty(0.0000000005); got != 0.000000001 {
		t.Fatalf("RoundQty(5e-10) = %v, want 1e-9", got)
	}
	if got := RoundQty(-0.0000000005); got != -0.000000001 {
		t.Fatalf("RoundQty(-5e-10) = %v, want -1e-9", got)
	}
}

func TestMRPNettingIsDecimal(t *testing.T) {
	// 0.7 on hand + 0.2 scheduled against 1.0 gross: exactly 0.1 short.
	net, planned, projected := netMRPBucket(0.7, 1.0, 0.2, 0, 0.05, 0, LotMethodFOQ)
	if net != 0.1 || planned != 0.1 || projected != 0 {
		t.Fatalf("net=%v planned=%v projected=%v, want 0.1 / 0.1 / 0", net, planned, projected)
	}
	// Not a multiple: 0.11 short with lot 0.05 rounds up to 0.15.
	net, planned, projected = netMRPBucket(0.69, 1.0, 0.2, 0, 0.05, 0, LotMethodFOQ)
	if net != 0.11 || planned != 0.15 || projected != 0.04 {
		t.Fatalf("net=%v planned=%v projected=%v, want 0.11 / 0.15 / 0.04", net, planned, projected)
	}
}

func TestFIFOSplitIsDecimal(t *testing.T) {
	lots := []LotBalance{{LotID: "A", Balance: 0.1}, {LotID: "B", Balance: 0.2}, {LotID: "C", Balance: 0.7}}
	got, shortage := SplitFIFO(lots, 0.6)
	if shortage != 0 || len(got) != 3 || got[0].Consumed != 0.1 || got[1].Consumed != 0.2 || got[2].Consumed != 0.3 {
		t.Fatalf("SplitFIFO = %+v shortage %v, want 0.1 / 0.2 / 0.3", got, shortage)
	}
	if _, shortage := SplitFIFO(lots, 1.1); shortage != 0.1 {
		t.Fatalf("shortage = %v, want exactly 0.1", shortage)
	}
}

func TestPartialCompletionsAndReceiptsCloseExactly(t *testing.T) {
	completed := 0.0
	for i := 1; i <= 10; i++ {
		q := 0.1
		_, cumulative, remaining, status, err := CalcCompletionBatch(1.0, completed, &q)
		if err != nil {
			t.Fatalf("completion %d: %v", i, err)
		}
		completed = cumulative
		wantStatus := "IN_PROGRESS"
		if i == 10 {
			wantStatus = "COMPLETED"
		}
		if status != wantStatus || remaining != subQty(1.0, cumulative) {
			t.Fatalf("completion %d: cumulative=%v remaining=%v status=%s", i, cumulative, remaining, status)
		}
	}
	if completed != 1 {
		t.Fatalf("10 x 0.1 completions = %v, want 1", completed)
	}

	received := 0.0
	for i := 1; i <= 3; i++ {
		st, err := CalcPurchaseReceiptState(0.3, received, 0.1)
		if err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
		received = st.NewReceived
		if i < 3 && st.Status != "PARTIALLY_RECEIVED" {
			t.Fatalf("receipt %d: status %s", i, st.Status)
		}
		if i == 3 && (st.Status != "RECEIVED" || st.Remaining != 0 || st.NewReceived != 0.3) {
			t.Fatalf("last receipt: %+v", st)
		}
	}
	if got := PurchaseScheduledRemaining("PARTIALLY_RECEIVED", 1.0, 0.7); got != 0.3 {
		t.Fatalf("PurchaseScheduledRemaining = %v, want 0.3", got)
	}
}

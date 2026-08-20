package service

import "testing"

func TestCalcPurchaseReceiptStatePartialThenFinal(t *testing.T) {
	s, err := CalcPurchaseReceiptState(100, 0, 20)
	if err != nil || s.NewReceived != 20 || s.Remaining != 80 || s.Status != "PARTIALLY_RECEIVED" {
		t.Fatalf("first partial receipt: state=%+v err=%v", s, err)
	}
	s, err = CalcPurchaseReceiptState(100, 20, 30)
	if err != nil || s.NewReceived != 50 || s.Remaining != 50 || s.Status != "PARTIALLY_RECEIVED" {
		t.Fatalf("second partial receipt: state=%+v err=%v", s, err)
	}
	s, err = CalcPurchaseReceiptState(100, 50, 50)
	if err != nil || s.NewReceived != 100 || s.Remaining != 0 || s.Status != "RECEIVED" {
		t.Fatalf("final receipt: state=%+v err=%v", s, err)
	}
}

func TestCalcPurchaseReceiptStateRejectsOverReceipt(t *testing.T) {
	if _, err := CalcPurchaseReceiptState(100, 80, 21); err == nil {
		t.Fatal("expected over-receipt to be rejected")
	}
}

func TestCalcPurchaseReceiptStateRejectsZero(t *testing.T) {
	if _, err := CalcPurchaseReceiptState(100, 20, 0); err == nil {
		t.Fatal("expected zero receipt to be rejected")
	}
}

func TestPurchaseScheduledRemaining(t *testing.T) {
	cases := []struct {
		status                  string
		ordered, received, want float64
	}{
		{"OPEN", 100, 0, 100},
		{"PARTIALLY_RECEIVED", 100, 20, 80},
		{"RECEIVED", 100, 100, 0},
		{"CLOSED", 100, 20, 0},
	}
	for _, tc := range cases {
		if got := PurchaseScheduledRemaining(tc.status, tc.ordered, tc.received); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.status, got, tc.want)
		}
	}
}

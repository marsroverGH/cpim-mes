package service

import "testing"

func TestValidateOperationTransition(t *testing.T) {
	allowed := [][2]string{
		{OperationStatusPending, OperationStatusReady},
		{OperationStatusReady, OperationStatusInProgress},
		{OperationStatusInProgress, OperationStatusPaused},
		{OperationStatusPaused, OperationStatusInProgress},
		{OperationStatusInProgress, OperationStatusCompleted},
	}
	for _, tt := range allowed {
		if err := ValidateOperationTransition(tt[0], tt[1]); err != nil {
			t.Fatalf("expected %s -> %s to be allowed: %v", tt[0], tt[1], err)
		}
	}

	denied := [][2]string{
		{OperationStatusPending, OperationStatusInProgress},
		{OperationStatusReady, OperationStatusCompleted},
		{OperationStatusPaused, OperationStatusCompleted},
		{OperationStatusCompleted, OperationStatusInProgress},
		{OperationStatusInProgress, OperationStatusReady},
	}
	for _, tt := range denied {
		if err := ValidateOperationTransition(tt[0], tt[1]); err == nil {
			t.Fatalf("expected %s -> %s to be denied", tt[0], tt[1])
		}
	}
}

func TestOperationActionStatuses(t *testing.T) {
	if err := ValidateOperationStartStatus(OperationStatusReady); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationStartStatus(OperationStatusPaused); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationStartStatus(OperationStatusPending); err == nil {
		t.Fatal("PENDING must not START")
	}
	if err := ValidateOperationStopStatus(OperationStatusInProgress); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationStopStatus(OperationStatusReady); err == nil {
		t.Fatal("READY must not STOP")
	}
	if err := ValidateOperationCompleteStatus(OperationStatusInProgress); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationCompleteStatus(OperationStatusPaused); err == nil {
		t.Fatal("PAUSED must not COMPLETE")
	}
}

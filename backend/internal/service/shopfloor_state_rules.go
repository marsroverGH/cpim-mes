package service

import "fmt"

const (
	OperationStatusPending    = "PENDING"
	OperationStatusReady      = "READY"
	OperationStatusInProgress = "IN_PROGRESS"
	OperationStatusPaused     = "PAUSED"
	OperationStatusCompleted  = "COMPLETED"
)

// ValidateOperationTransition defines the only legal Shop Floor status changes.
// Reporting a partial good quantity keeps the operation IN_PROGRESS and therefore
// does not call this function as a status change.
func ValidateOperationTransition(from, to string) error {
	allowed := false
	switch from {
	case OperationStatusPending:
		allowed = to == OperationStatusReady
	case OperationStatusReady:
		allowed = to == OperationStatusInProgress
	case OperationStatusInProgress:
		allowed = to == OperationStatusPaused || to == OperationStatusCompleted
	case OperationStatusPaused:
		allowed = to == OperationStatusInProgress
	case OperationStatusCompleted:
		allowed = false
	}
	if !allowed {
		return fmt.Errorf("invalid operation status transition: %s -> %s", from, to)
	}
	return nil
}

func ValidateOperationStartStatus(status string) error {
	if status != OperationStatusReady && status != OperationStatusPaused {
		return fmt.Errorf("operation must be READY or PAUSED before START (current: %s)", status)
	}
	return nil
}

func ValidateOperationStopStatus(status string) error {
	if status != OperationStatusInProgress {
		return fmt.Errorf("operation must be IN_PROGRESS before STOP (current: %s)", status)
	}
	return nil
}

func ValidateOperationCompleteStatus(status string) error {
	if status != OperationStatusInProgress {
		return fmt.Errorf("operation must be IN_PROGRESS before COMPLETE (current: %s)", status)
	}
	return nil
}

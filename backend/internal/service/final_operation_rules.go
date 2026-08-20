package service

import "fmt"

const quantityEpsilon = 1e-9

// ValidateFinishedGoodsAgainstFinalOperation is a pure rule used by the
// workflow and tests. finalCompletedQty is the cumulative good quantity
// physically completed at the final routing operation; WO receipts may never
// make cumulative received quantity exceed that value.
func ValidateFinishedGoodsAgainstFinalOperation(
	plannedQty, woCompletedQty, finalCompletedQty, completionNow float64,
) error {
	if plannedQty <= 0 {
		return fmt.Errorf("planned quantity must be > 0")
	}
	if woCompletedQty < -quantityEpsilon || finalCompletedQty < -quantityEpsilon || completionNow <= quantityEpsilon {
		return fmt.Errorf("completion quantities must be non-negative and completionNow must be > 0")
	}
	if finalCompletedQty > plannedQty+quantityEpsilon {
		return fmt.Errorf("final operation completed quantity %.6f exceeds WO planned quantity %.6f", finalCompletedQty, plannedQty)
	}
	newWOCompleted := woCompletedQty + completionNow
	if newWOCompleted > plannedQty+quantityEpsilon {
		return fmt.Errorf("WO cumulative completion %.6f exceeds planned quantity %.6f", newWOCompleted, plannedQty)
	}
	if newWOCompleted > finalCompletedQty+quantityEpsilon {
		return fmt.Errorf(
			"finished-goods receipt would exceed final operation actual: WO received %.6f + now %.6f = %.6f, final operation completed %.6f",
			woCompletedQty, completionNow, newWOCompleted, finalCompletedQty,
		)
	}
	return nil
}

// CalcOperationCumulative validates a cumulative Shop Floor good-quantity
// report. Partial reports keep the operation IN_PROGRESS; it becomes COMPLETED
// only when its cumulative quantity reaches the WO planned quantity.
func CalcOperationCumulative(
	plannedQty, currentQty, reportedCumulative, woAlreadyReceived float64,
	isFinal bool,
) (delta float64, status string, err error) {
	if plannedQty <= 0 {
		return 0, "", fmt.Errorf("planned quantity must be > 0")
	}
	if reportedCumulative < -quantityEpsilon || reportedCumulative > plannedQty+quantityEpsilon {
		return 0, "", fmt.Errorf("operation cumulative completed quantity must be between 0 and %.6f", plannedQty)
	}
	if reportedCumulative+quantityEpsilon < currentQty {
		return 0, "", fmt.Errorf("operation cumulative completed quantity cannot decrease from %.6f to %.6f", currentQty, reportedCumulative)
	}
	if isFinal && reportedCumulative+quantityEpsilon < woAlreadyReceived {
		return 0, "", fmt.Errorf(
			"final operation completed quantity %.6f cannot be below already received WO quantity %.6f",
			reportedCumulative, woAlreadyReceived,
		)
	}
	delta = reportedCumulative - currentQty
	if delta <= quantityEpsilon {
		return 0, "", fmt.Errorf("operation completion report must increase cumulative completed quantity")
	}
	status = "IN_PROGRESS"
	if reportedCumulative >= plannedQty-quantityEpsilon {
		status = "COMPLETED"
	}
	return delta, status, nil
}

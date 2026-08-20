package service

import "time"

// netMRPBucket performs one time-phased netting bucket.
// Safety stock is the required ENDING balance. Scheduled receipts are available
// before the gross requirement in the same bucket.
func netMRPBucket(opening, gross, scheduled, safety, lotSize, eoq float64, method LotSizeMethod) (net, plannedReceipt, projected float64) {
	available := opening + scheduled
	net = gross + safety - available
	if net < 0 {
		net = 0
	}
	plannedReceipt = ApplyLotSize(net, 0, lotSize, eoq, method)
	projected = available + plannedReceipt - gross
	return net, plannedReceipt, projected
}

// plannedOrderReleaseDate offsets a planned receipt by the item's lead time.
// A release date may be before the MRP start date; that is intentionally retained
// so planners can see past-due releases instead of having them silently clamped.
func plannedOrderReleaseDate(receiptDate time.Time, leadTimeDays int, plannedReceipt float64) time.Time {
	if plannedReceipt <= 0 {
		return time.Time{}
	}
	if leadTimeDays < 0 {
		leadTimeDays = 0
	}
	return TruncateDay(receiptDate.AddDate(0, 0, -leadTimeDays))
}

// directComponentRequirement calculates one direct BOM edge only. Lower levels
// are intentionally NOT included here; they are generated later when the child
// item's own planned order release is processed.
func directComponentRequirement(parentPlannedRelease, quantityPer, scrapPct float64) float64 {
	if parentPlannedRelease <= 0 || quantityPer <= 0 {
		return 0
	}
	if scrapPct < 0 {
		scrapPct = 0
	}
	return parentPlannedRelease * quantityPer * (1 + scrapPct)
}

// dependentRequirementDate converts a past-due release into the MRP start
// bucket because current on-hand is the opening balance as of that start date.
func dependentRequirementDate(releaseDate, startDate time.Time) time.Time {
	releaseDate = TruncateDay(releaseDate)
	startDate = TruncateDay(startDate)
	if releaseDate.Before(startDate) {
		return startDate
	}
	return releaseDate
}

package service

import (
	"time"

	"github.com/shopspring/decimal"
)

// netMRPBucket performs one time-phased netting bucket.
// Safety stock is the required ENDING balance. Scheduled receipts are available
// before the gross requirement in the same bucket.
func netMRPBucket(opening, gross, scheduled, safety, lotSize, eoq float64, method LotSizeMethod) (net, plannedReceipt, projected float64) {
	available := dq(opening).Add(dq(scheduled))
	netDec := roundQtyDec(dq(gross).Add(dq(safety)).Sub(available))
	if netDec.IsNegative() {
		netDec = decimal.Zero
	}
	net = fq(netDec)
	plannedReceipt = ApplyLotSize(net, 0, lotSize, eoq, method)
	projected = fq(available.Add(dq(plannedReceipt)).Sub(dq(gross)))
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
	return BusinessDate(receiptDate.AddDate(0, 0, -leadTimeDays))
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
	return fq(dq(parentPlannedRelease).Mul(dq(quantityPer)).Mul(decimal.NewFromInt(1).Add(dq(scrapPct))))
}

// dependentRequirementDate converts a past-due release into the MRP start
// bucket because current on-hand is the opening balance as of that start date.
func dependentRequirementDate(releaseDate, startDate time.Time) time.Time {
	releaseDate = BusinessDate(releaseDate)
	startDate = BusinessDate(startDate)
	if releaseDate.Before(startDate) {
		return startDate
	}
	return releaseDate
}

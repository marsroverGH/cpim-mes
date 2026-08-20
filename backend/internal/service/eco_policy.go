package service

import "time"

// ecoEffectiveOn reports whether an ECO may be applied on businessDate.
// Both values are normalized to calendar dates; time-of-day does not affect
// an effective-date policy.
func ecoEffectiveOn(effectiveDate, businessDate time.Time) bool {
	e := time.Date(effectiveDate.Year(), effectiveDate.Month(), effectiveDate.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(businessDate.Year(), businessDate.Month(), businessDate.Day(), 0, 0, 0, 0, time.UTC)
	return !b.Before(e)
}

func validECOTransition(from, to string) bool {
	switch from {
	case "DRAFT":
		return to == "APPROVED" || to == "CANCELLED"
	case "APPROVED":
		return to == "APPLIED" || to == "CANCELLED"
	default:
		return false
	}
}

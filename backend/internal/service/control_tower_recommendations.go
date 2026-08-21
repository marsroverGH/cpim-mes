package service

import "strings"

// ControlTowerRecommendationInput contains the root-cause evidence needed to
// produce deterministic intervention recommendations.
type ControlTowerRecommendationInput struct {
	ExceptionType string
	Message       string
	RootCauseRef  string
}

// ControlTowerRecommendationDraft is generated before a persisted snapshot ID
// exists. Persistence binds these ranked drafts to the immutable snapshot.
type ControlTowerRecommendationDraft struct {
	RankNo           int
	ActionType       string
	TargetType       string
	TargetRef        string
	Title            string
	Reason           string
	EstimatedEffect  map[string]any
	RequiresApproval bool
}

func controlTowerTargetType(exceptionType string) string {
	t := strings.ToUpper(strings.TrimSpace(exceptionType))

	switch {
	case strings.Contains(t, "SUPPLIER"),
		t == "LATE_PURCHASE_ORDER":
		return "PURCHASE_ORDER"

	case strings.Contains(t, "CAPACITY"),
		strings.Contains(t, "SCHEDULE"),
		strings.Contains(t, "DISPATCH"),
		strings.Contains(t, "RESCHEDULE"),
		strings.Contains(t, "HORIZON"),
		strings.Contains(t, "EXECUTION"),
		strings.Contains(t, "MAINTENANCE"),
		strings.Contains(t, "BREAKDOWN"),
		strings.Contains(t, "DOWNTIME"):
		return "WORK_ORDER"

	case t == "QUALITY_HOLD":
		return "INVENTORY_LOT"

	case t == "MATERIAL_SHORTAGE",
		t == "SAFETY_STOCK_BREACH",
		t == "REORDER_POINT_BREACH":
		return "ITEM"

	default:
		return "SALES_ORDER"
	}
}

func recommendation(
	rank int,
	action, targetType, targetRef, title, reason string,
	approval bool,
) ControlTowerRecommendationDraft {
	return ControlTowerRecommendationDraft{
		RankNo:           rank,
		ActionType:       action,
		TargetType:       targetType,
		TargetRef:        targetRef,
		Title:            title,
		Reason:           reason,
		EstimatedEffect:  map[string]any{},
		RequiresApproval: approval,
	}
}

// RecommendControlTowerActions translates one detected root cause into ranked,
// deterministic planner interventions.
func RecommendControlTowerActions(
	in ControlTowerRecommendationInput,
) []ControlTowerRecommendationDraft {
	t := strings.ToUpper(strings.TrimSpace(in.ExceptionType))
	targetType := controlTowerTargetType(t)
	ref := strings.TrimSpace(in.RootCauseRef)
	reason := strings.TrimSpace(in.Message)

	if reason == "" {
		reason = "Production Control Tower detected an operational planning constraint"
	}

	out := []ControlTowerRecommendationDraft{}

	switch t {
	case "SUPPLIER_CONFIRMATION_LATE",
		"SUPPLIER_RELIABILITY_RISK",
		"LATE_PURCHASE_ORDER":
		out = append(out,
			recommendation(
				1, "EXPEDITE_PO", targetType, ref,
				"Expedite purchase order",
				reason, false,
			),
			recommendation(
				2, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recalculate customer promise",
				"Supplier timing changed; re-evaluate ATP/CTP promise evidence.",
				false,
			),
		)

	case "SUPPLIER_BLOCKED":
		out = append(out,
			recommendation(
				1, "MANUAL_REVIEW", targetType, ref,
				"Review blocked supplier",
				reason, true,
			),
			recommendation(
				2, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recalculate customer promise",
				"Blocked supply may invalidate the current committed promise.",
				false,
			),
			recommendation(
				3, "CONTACT_CUSTOMER", "SALES_ORDER", "",
				"Prepare customer communication",
				"Customer communication may be required if alternative supply cannot recover the promise.",
				true,
			),
		)

	case "MATERIAL_SHORTAGE":
		out = append(out,
			recommendation(
				1, "MANUAL_REVIEW", targetType, ref,
				"Review material shortage",
				reason, false,
			),
			recommendation(
				2, "RELEASE_WO", "WORK_ORDER", "",
				"Review production release opportunities",
				"Check whether planned production can be converted or released to cover the shortage.",
				true,
			),
			recommendation(
				3, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recalculate customer promise",
				"Material shortage may change the feasible customer delivery date.",
				false,
			),
		)

	case "SAFETY_STOCK_BREACH", "REORDER_POINT_BREACH":
		out = append(out,
			recommendation(
				1, "MANUAL_REVIEW", targetType, ref,
				"Review inventory policy exception",
				reason, false,
			),
			recommendation(
				2, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recheck promise protection",
				"Verify whether customer demand should consume protected inventory.",
				false,
			),
		)

	case "QUALITY_HOLD":
		out = append(out,
			recommendation(
				1, "REVIEW_QUALITY_HOLD", targetType, ref,
				"Review quality hold",
				reason, true,
			),
			recommendation(
				2, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recalculate customer promise",
				"Quarantined inventory is not usable supply until disposition.",
				false,
			),
		)

	case "CAPACITY_LATE", "CAPACITY_UNSCHEDULED", "OEE_CAPACITY_RISK":
		out = append(out,
			recommendation(
				1, "RESCHEDULE_WO", "WORK_ORDER", ref,
				"Reschedule affected work",
				reason, false,
			),
			recommendation(
				2, "ALTERNATE_WORK_CENTER", "WORK_CENTER", ref,
				"Evaluate alternate work center",
				"Check alternate routing capacity before changing the customer promise.",
				true,
			),
			recommendation(
				3, "REVIEW_CAPACITY", "WORK_CENTER", ref,
				"Review capacity constraint",
				"Validate machine, labor and effective-capacity assumptions.",
				false,
			),
		)

	case "DISPATCH_BLOCKED":
		out = append(out,
			recommendation(
				1, "RESCHEDULE_WO", "WORK_ORDER", ref,
				"Resolve blocked dispatch",
				reason, false,
			),
			recommendation(
				2, "REVIEW_CAPACITY", "WORK_CENTER", ref,
				"Review dispatch constraint",
				"Verify predecessor, material, machine and labor readiness.",
				false,
			),
		)

	case "SCHEDULE_START_LATE",
		"SCHEDULE_COMPLETION_LATE",
		"RESCHEDULE_REQUIRED",
		"FIRM_HORIZON_CHANGE":
		out = append(out,
			recommendation(
				1, "RESCHEDULE_WO", "WORK_ORDER", ref,
				"Evaluate dynamic rescheduling",
				reason, false,
			),
			recommendation(
				2, "RECALCULATE_PROMISE", "SALES_ORDER", "",
				"Recalculate customer promise",
				"Execution deviation may affect the committed completion date.",
				false,
			),
		)

	case "FROZEN_HORIZON_CONFLICT", "EXECUTION_COMMITMENT_CONFLICT":
		out = append(out,
			recommendation(
				1, "REVIEW_FROZEN_CONFLICT", "WORK_ORDER", ref,
				"Review protected execution commitment",
				reason, true,
			),
			recommendation(
				2, "CONTACT_CUSTOMER", "SALES_ORDER", "",
				"Review customer recovery plan",
				"Protected execution commitments prevent automatic schedule recovery.",
				true,
			),
		)

	case "LATE_PROMISE", "BACKORDER", "UNCONVERTED_CTP":
		out = append(out,
			recommendation(
				1, "RECALCULATE_PROMISE", "SALES_ORDER", ref,
				"Recalculate customer promise",
				reason, false,
			),
			recommendation(
				2, "CONTACT_CUSTOMER", "SALES_ORDER", ref,
				"Review customer communication",
				"The current committed demand cannot be satisfied as originally planned.",
				true,
			),
		)

	default:
		if strings.Contains(t, "MAINTENANCE") ||
			strings.Contains(t, "BREAKDOWN") ||
			strings.Contains(t, "DOWNTIME") {
			out = append(out,
				recommendation(
					1, "ALTERNATE_WORK_CENTER", "WORK_CENTER", ref,
					"Evaluate alternate work center",
					reason, true,
				),
				recommendation(
					2, "RESCHEDULE_WO", "WORK_ORDER", ref,
					"Reschedule affected production",
					"Maintenance or downtime has reduced executable capacity.",
					false,
				),
				recommendation(
					3, "REVIEW_CAPACITY", "WORK_CENTER", ref,
					"Review effective capacity",
					"Confirm the downtime duration and remaining available resources.",
					false,
				),
			)
		} else {
			out = append(out,
				recommendation(
					1, "MANUAL_REVIEW", targetType, ref,
					"Review planning exception",
					reason, false,
				),
			)
		}
	}

	return out
}

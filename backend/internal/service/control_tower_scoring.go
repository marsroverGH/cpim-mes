package service

import (
	"math"
	"strings"
)

// ControlTowerScoreInput contains only deterministic inputs needed to score
// one operational planning exception.
type ControlTowerScoreInput struct {
	Severity         string
	ImpactDays       int
	RevenueAtRisk    float64
	OrderPriority    string
	ServiceClassRank int
	ExceptionType    string
	AgeHours         float64
}

// ControlTowerScore contains normalized 0..100 component scores plus the final
// intervention priority.
type ControlTowerScore struct {
	SeverityScore  float64
	LatenessScore  float64
	RevenueScore   float64
	CustomerScore  float64
	MaterialScore  float64
	CapacityScore  float64
	SupplierScore  float64
	ExecutionScore float64
	AgingScore     float64

	PriorityScore float64
	PriorityBand  string
	ForcedP1      bool
	ForceReason   string
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func controlTowerSeverityScore(v string) float64 {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "CRITICAL":
		return 100
	case "WARNING":
		return 60
	case "INFO":
		return 20
	default:
		return 0
	}
}

func controlTowerLatenessScore(days int) float64 {
	switch {
	case days <= 0:
		return 0
	case days <= 2:
		return 25
	case days <= 5:
		return 50
	case days <= 10:
		return 75
	default:
		return 100
	}
}

func controlTowerRevenueScore(v float64) float64 {
	switch {
	case v <= 0:
		return 0
	case v < 100000:
		return 15
	case v < 500000:
		return 35
	case v < 1000000:
		return 55
	case v < 2000000:
		return 70
	case v < 5000000:
		return 85
	default:
		return 100
	}
}

func controlTowerCustomerScore(priority string, serviceRank int) float64 {
	order := 40.0

	switch strings.ToUpper(strings.TrimSpace(priority)) {
	case "EXPEDITE":
		order = 100
	case "HIGH":
		order = 75
	case "NORMAL":
		order = 40
	}

	class := 50.0
	switch serviceRank {
	case 1:
		class = 100
	case 2:
		class = 85
	case 3:
		class = 70
	case 4:
		class = 55
	default:
		if serviceRank >= 5 {
			class = 40
		}
	}

	if class > order {
		return class
	}
	return order
}

func controlTowerAgingScore(hours float64) float64 {
	switch {
	case hours <= 24:
		return 0
	case hours <= 72:
		return 30
	case hours <= 168:
		return 60
	case hours <= 336:
		return 80
	default:
		return 100
	}
}

func controlTowerConstraintScores(exceptionType string) (
	material, capacity, supplier, execution float64,
) {
	t := strings.ToUpper(strings.TrimSpace(exceptionType))

	switch t {
	case "MATERIAL_SHORTAGE":
		material = 100
	case "SAFETY_STOCK_BREACH":
		material = 85
	case "REORDER_POINT_BREACH":
		material = 60
	case "QUALITY_HOLD":
		material = 90
	}

	switch t {
	case "CAPACITY_UNSCHEDULED":
		capacity = 100
	case "CAPACITY_LATE":
		capacity = 85
	case "OEE_CAPACITY_RISK":
		capacity = 80
	}

	if strings.Contains(t, "MAINTENANCE") ||
		strings.Contains(t, "BREAKDOWN") ||
		strings.Contains(t, "DOWNTIME") {
		capacity = math.Max(capacity, 90)
	}

	switch t {
	case "SUPPLIER_BLOCKED":
		supplier = 100
	case "SUPPLIER_CONFIRMATION_LATE":
		supplier = 90
	case "SUPPLIER_RELIABILITY_RISK":
		supplier = 75
	case "LATE_PURCHASE_ORDER":
		supplier = 85
	}

	switch t {
	case "DISPATCH_BLOCKED":
		execution = 100
	case "EXECUTION_COMMITMENT_CONFLICT":
		execution = 100
	case "FROZEN_HORIZON_CONFLICT":
		execution = 100
	case "RESCHEDULE_REQUIRED":
		execution = 80
	case "FIRM_HORIZON_CHANGE":
		execution = 65
	case "SCHEDULE_START_LATE":
		execution = 70
	case "SCHEDULE_COMPLETION_LATE":
		execution = 80
	}

	return
}

func forcedControlTowerP1(exceptionType string) (bool, string) {
	t := strings.ToUpper(strings.TrimSpace(exceptionType))

	switch t {
	case "MATERIAL_SHORTAGE",
		"SUPPLIER_BLOCKED",
		"CAPACITY_UNSCHEDULED",
		"DISPATCH_BLOCKED",
		"FROZEN_HORIZON_CONFLICT",
		"EXECUTION_COMMITMENT_CONFLICT":
		return true, t
	default:
		return false, ""
	}
}

func controlTowerPriorityBand(score float64) string {
	switch {
	case score >= 75:
		return "P1"
	case score >= 55:
		return "P2"
	case score >= 30:
		return "P3"
	default:
		return "P4"
	}
}

// ScoreControlTowerPriority converts operational planning evidence into one
// deterministic 0..100 intervention score.
//
// Material / Capacity / Supplier / Execution remain separate explainability
// dimensions, while the strongest applicable dimension becomes Constraint.
//
// Weights:
//
//	Severity       20%
//	Delivery       15%
//	Revenue        20%
//	Customer       10%
//	Constraint     25%
//	Aging          10%
//
// Certain hard operational constraints are always promoted to at least P1.
func ScoreControlTowerPriority(in ControlTowerScoreInput) ControlTowerScore {
	material, capacity, supplier, execution :=
		controlTowerConstraintScores(in.ExceptionType)

	out := ControlTowerScore{
		SeverityScore:  controlTowerSeverityScore(in.Severity),
		LatenessScore:  controlTowerLatenessScore(in.ImpactDays),
		RevenueScore:   controlTowerRevenueScore(in.RevenueAtRisk),
		CustomerScore:  controlTowerCustomerScore(in.OrderPriority, in.ServiceClassRank),
		MaterialScore:  material,
		CapacityScore:  capacity,
		SupplierScore:  supplier,
		ExecutionScore: execution,
		AgingScore:     controlTowerAgingScore(in.AgeHours),
	}

	// Material / Capacity / Supplier / Execution are alternative root-cause
	// dimensions. Use the strongest applicable constraint in the final
	// intervention score while preserving every dimension for explainability.
	constraintScore := math.Max(
		math.Max(out.MaterialScore, out.CapacityScore),
		math.Max(out.SupplierScore, out.ExecutionScore),
	)

	score :=
		out.SeverityScore*0.20 +
			out.LatenessScore*0.15 +
			out.RevenueScore*0.20 +
			out.CustomerScore*0.10 +
			constraintScore*0.25 +
			out.AgingScore*0.10

	out.ForcedP1, out.ForceReason =
		forcedControlTowerP1(in.ExceptionType)

	if out.ForcedP1 && score < 75 {
		score = 75
	}

	out.PriorityScore = round3(clampScore(score))
	out.PriorityBand = controlTowerPriorityBand(out.PriorityScore)

	return out
}

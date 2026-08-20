package service

import (
	"context"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// ABCService implements CPIM-style ABC inventory classification using annual
// dollar usage rather than current inventory value.
//
//	Annual Dollar Usage = rolling 12-month ISSUE quantity × Standard Cost
//
// Physical ISSUE transactions represent actual material/stock consumption.
// RECEIPT, ADJUST, RESERVE and UNRESERVE are intentionally excluded so cycle
// counts, supplier returns, NCR scrap and corrections do not inflate usage.
//
// Existing policy thresholds are retained:
// cumulative annual dollar usage <= 70% => A, <= 90% => B, remainder => C.
type ABCService struct {
	repos *repository.Repositories
}

// ClassifyABC returns the class for a cumulative annual-dollar-usage share.
func ClassifyABC(cumulativePct float64) string {
	switch {
	case cumulativePct <= 70:
		return "A"
	case cumulativePct <= 90:
		return "B"
	default:
		return "C"
	}
}

// Run uses the application's current business date as the analysis end date.
func (s *ABCService) Run(ctx context.Context) ([]domain.ABCAnalysisRow, error) {
	asOf, err := s.repos.Inventory.BusinessDate(ctx)
	if err != nil {
		return nil, err
	}
	return s.RunAsOf(ctx, asOf)
}

// RunAsOf produces a reproducible rolling-12-month analysis ending on asOf.
func (s *ABCService) RunAsOf(ctx context.Context, asOf time.Time) ([]domain.ABCAnalysisRow, error) {
	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	onHand, err := s.repos.Inventory.OnHand(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.repos.Inventory.AnnualIssueUsage(ctx, asOf)
	if err != nil {
		return nil, err
	}

	stockMap := make(map[uuid.UUID]float64, len(onHand))
	for _, x := range onHand {
		stockMap[x.ItemID] = x.OnHand
	}
	usageMap := make(map[uuid.UUID]float64, len(usage))
	for _, x := range usage {
		usageMap[x.ItemID] = x.UsageQty
	}

	periodEnd := TruncateDay(asOf)
	periodStart := periodEnd.AddDate(-1, 0, 1)
	return BuildAnnualDollarUsageABC(items, stockMap, usageMap, periodStart, periodEnd), nil
}

// BuildAnnualDollarUsageABC is separated from DB access so ranking, totals and
// zero-history behavior can be regression-tested without PostgreSQL.
func BuildAnnualDollarUsageABC(
	items []domain.Item,
	stockMap map[uuid.UUID]float64,
	usageMap map[uuid.UUID]float64,
	periodStart, periodEnd time.Time,
) []domain.ABCAnalysisRow {
	rows := make([]domain.ABCAnalysisRow, 0, len(items))
	totalUsageValue := 0.0
	for _, it := range items {
		oh := stockMap[it.ID]
		usageQty := usageMap[it.ID]
		if usageQty < 0 { // defensive: usage is never negative by definition.
			usageQty = 0
		}
		usageValue := usageQty * it.StandardCost
		rows = append(rows, domain.ABCAnalysisRow{
			ItemID:           it.ID,
			ItemCode:         it.Code,
			ItemName:         it.Name,
			OnHand:           oh,
			StandardCost:     it.StandardCost,
			OnHandValue:      oh * it.StandardCost,
			AnnualUsageQty:   usageQty,
			AnnualUsageValue: usageValue,
			UsagePeriodStart: periodStart,
			UsagePeriodEnd:   periodEnd,
			UsageBasis:       "ISSUE",
			CostBasis:        "STANDARD_COST",
		})
		totalUsageValue += usageValue
	}

	// Rank by annual dollar usage. Code is a stable deterministic tie-breaker.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].AnnualUsageValue == rows[j].AnnualUsageValue {
			return rows[i].ItemCode < rows[j].ItemCode
		}
		return rows[i].AnnualUsageValue > rows[j].AnnualUsageValue
	})

	// No usage history means there is no economic usage basis for A/B ranking.
	// Classify all such items as C instead of the old accidental A classification.
	if totalUsageValue <= 0 {
		for i := range rows {
			rows[i].ABCClass = "C"
		}
		return rows
	}

	cum := 0.0
	for i := range rows {
		rows[i].UsageValuePct = rows[i].AnnualUsageValue / totalUsageValue * 100
		cum += rows[i].AnnualUsageValue
		rows[i].CumulativePct = cum / totalUsageValue * 100
		rows[i].ABCClass = ClassifyABC(rows[i].CumulativePct)
	}
	return rows
}

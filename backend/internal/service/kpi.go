package service

import (
	"context"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
)

type KPIService struct {
	repos *repository.Repositories
	mrp   *MRPService
	am    *ActionMessageService
}

// Compute — KPI Dashboard を1回のAPI呼び出しで集計
func (s *KPIService) Compute(ctx context.Context) (*domain.KPIDashboard, error) {
	now := time.Now()
	today := TruncateDay(now)
	thirtyDaysAgo := today.AddDate(0, 0, -30)

	dash := &domain.KPIDashboard{GeneratedAt: now}

	// ---------------- WO 関連 ----------------
	wos, err := s.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	var wipUnits, throughput30d float64
	dailyMap := make(map[time.Time]float64)
	completed30d, completedOnTime := 0, 0
	for _, w := range wos {
		switch w.Status {
		case "RELEASED", "IN_PROGRESS":
			dash.OpenWOCount++
			wipUnits += (w.Quantity - w.CompletedQty)
			if w.DueDate.Before(today) {
				dash.OverdueWOCount++
			}
		case "COMPLETED":
			if w.CompletedAt != nil && !w.CompletedAt.Before(thirtyDaysAgo) {
				throughput30d += w.Quantity
				dailyMap[TruncateDay(*w.CompletedAt)] += w.Quantity
				completed30d++
				if !w.CompletedAt.After(w.DueDate.Add(24 * time.Hour)) {
					completedOnTime++
				}
			}
		}
	}
	dash.WIPUnits = wipUnits
	dash.ThroughputUnits = throughput30d

	// 日次完成スパークライン (30日分、ゼロ埋め)
	dash.DailyThroughput = make([]domain.KPIPoint, 0, 30)
	for d := thirtyDaysAgo; !d.After(today); d = d.AddDate(0, 0, 1) {
		dash.DailyThroughput = append(dash.DailyThroughput, domain.KPIPoint{
			Date: d, Value: dailyMap[d],
		})
	}

	// On-Time / OTIF (簡易: 数量100%完成 = In-Full、納期内完了 = On-Time)
	if completed30d > 0 {
		dash.OnTimeRate = float64(completedOnTime) / float64(completed30d) * 100
		dash.OTIFRate = dash.OnTimeRate // 完成 WO は数量100%なので OTIF = OnTime
	}

	// ---------------- PO 関連 ----------------
	pos, err := s.repos.Purchases.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pos {
		if p.Status == "OPEN" || p.Status == "PARTIALLY_RECEIVED" {
			dash.OpenPOCount++
			if p.DueDate.Before(today) {
				dash.OverduePOCount++
			}
		}
	}

	// ---------------- Inventory ----------------
	onHand, err := s.repos.Inventory.OnHand(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	costByItem := make(map[string]float64)
	codeByItem := make(map[string]string)
	for _, it := range items {
		costByItem[it.ID.String()] = it.StandardCost
		codeByItem[it.ID.String()] = it.Code
	}
	var invValue, cogs30d float64
	for _, oh := range onHand {
		invValue += oh.OnHand * costByItem[oh.ItemID.String()]
	}
	dash.InventoryValue = invValue

	// 在庫回転 = 30日売上原価 × 12 / 平均在庫額
	// 簡易: 過去30日の WO 完成原価 (parent items の standard cost ベース)
	for _, w := range wos {
		if w.Status == "COMPLETED" && w.CompletedAt != nil && !w.CompletedAt.Before(thirtyDaysAgo) {
			cogs30d += w.Quantity * costByItem[w.ItemID.String()]
		}
	}
	if invValue > 0 {
		dash.InventoryTurnover = (cogs30d * 12) / invValue
	}

	// ---------------- Quality ----------------
	recentQ, err := s.repos.Quality.Recent(ctx, 200)
	if err != nil {
		return nil, err
	}
	if len(recentQ) > 0 {
		passes := 0
		for _, q := range recentQ {
			switch q.Result {
			case "PASS":
				passes++
			case "HOLD":
				dash.QualityHoldCount++
			case "FAIL":
				dash.QualityRejectCount++
			}
		}
		dash.QualityPassRate = float64(passes) / float64(len(recentQ)) * 100
	}

	// ---------------- Action Messages ----------------
	if s.am != nil {
		actions, err := s.am.Run(ctx, 28)
		if err == nil {
			for _, a := range actions {
				switch a.Severity {
				case "CRITICAL":
					dash.CriticalActions++
				case "WARNING":
					dash.WarningActions++
				}
			}
		}
	}

	return dash, nil
}

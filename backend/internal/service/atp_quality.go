package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// ATP (Available-to-Promise)
// ====================================================================
//
// 期間別に「いま受注しても約束できる数量」を算出する。
//   StartingOnHand[t] = EndingProjected[t-1]
//   ScheduledIn[t]    = 計画入庫 (PO の納期 + WO 完成日)
//   CommittedOut[t]   = confirmed Sales Order open quantity by promised/requested date
//   EndingProjected[t]= StartingOnHand[t] + ScheduledIn[t] - CommittedOut[t]
//   ATP[t]            = ScheduledIn[t] - 当該期間以降の最初の補充までの CommittedOut
//
// 簡易版: ATP[t] = max(0, ScheduledIn[t] - CommittedOut[t])
// 累積ATP は ATP の prefix sum で計算する。

type ATPService struct {
	db    *sqlx.DB
	repos *repository.Repositories
}

// ATPInput — 純粋関数 CalcATP の入力
type ATPInput struct {
	StartingOnHand float64
	Buckets        []ATPBucketInput
}

type ATPBucketInput struct {
	Period       time.Time
	ScheduledIn  float64
	CommittedOut float64
}

// CalcATP — 純粋関数。期間順 (古い→新しい) に並んだバケットから ATP を計算。
func CalcATP(in ATPInput) []domain.ATPBucket {
	out := make([]domain.ATPBucket, len(in.Buckets))
	startOH := in.StartingOnHand
	cumATP := startOH // 期首在庫も累積ATPに含める
	for i, b := range in.Buckets {
		ending := startOH + b.ScheduledIn - b.CommittedOut
		atp := b.ScheduledIn - b.CommittedOut
		if i == 0 {
			// 1期目は期首在庫も加味
			atp = startOH + b.ScheduledIn - b.CommittedOut
		}
		if atp < 0 {
			atp = 0
		}
		if i == 0 {
			cumATP = atp
		} else {
			cumATP += atp
		}
		out[i] = domain.ATPBucket{
			Period:          b.Period,
			StartingOnHand:  startOH,
			ScheduledIn:     b.ScheduledIn,
			CommittedOut:    b.CommittedOut,
			EndingProjected: ending,
			ATP:             atp,
			CumulativeATP:   cumATP,
		}
		startOH = ending
	}
	return out
}

// Run — DB から実データを集約し ATP を計算
func (s *ATPService) Run(ctx context.Context, itemID uuid.UUID, horizonDays, bucketDays int) (*domain.ATPResult, error) {
	if itemID == uuid.Nil {
		return nil, errors.New("itemId required")
	}
	if horizonDays <= 0 {
		horizonDays = 56 // 8週
	}
	if bucketDays <= 0 {
		bucketDays = 7 // 週次
	}

	item, err := s.repos.Items.Get(ctx, itemID)
	if err != nil {
		return nil, err
	}

	// 期首在庫 (= on_hand only。reserved は別系統)
	balance, err := s.repos.Inventory.BalanceFor(ctx, itemID)
	if err != nil {
		return nil, err
	}
	startingOH := 0.0
	if balance != nil {
		startingOH = balance.OnHand
	}

	// 集計範囲
	now := TruncateDay(time.Now())
	end := now.AddDate(0, 0, horizonDays)

	// バケット枠を生成
	buckets := []ATPBucketInput{}
	for d := now; !d.After(end); d = d.AddDate(0, 0, bucketDays) {
		buckets = append(buckets, ATPBucketInput{Period: d})
	}
	if len(buckets) == 0 {
		return &domain.ATPResult{ItemID: itemID, ItemCode: item.Code, Buckets: nil}, nil
	}

	bucketStart := func(d time.Time) time.Time {
		offset := int(TruncateDay(d).Sub(now).Hours() / 24)
		if offset < 0 {
			return buckets[0].Period
		}
		idx := offset / bucketDays
		if idx >= len(buckets) {
			idx = len(buckets) - 1
		}
		return buckets[idx].Period
	}
	bucketIdx := make(map[time.Time]int, len(buckets))
	for i, b := range buckets {
		bucketIdx[b.Period] = i
	}

	// Committed customer demand is owned by formal Sales Orders after 0031.
	var demands []domain.DemandForecast
	if err := s.db.SelectContext(ctx, &demands, `
SELECT l.id AS id,l.item_id,
       COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) AS due_date,
       (l.quantity-l.shipped_qty-l.cancelled_qty)::double precision AS quantity,
       'ORDER' AS source,so.created_at
  FROM sales_order_lines l JOIN sales_orders so ON so.id=l.sales_order_id
 WHERE l.item_id=$1 AND so.status IN ('CONFIRMED','PARTIALLY_SHIPPED')
   AND COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) BETWEEN $2 AND $3
   AND (l.quantity-l.shipped_qty-l.cancelled_qty)>0
 ORDER BY due_date,l.id`, itemID, now, end); err != nil {
		return nil, err
	}
	for _, d := range demands {
		bs := bucketStart(d.DueDate)
		buckets[bucketIdx[bs]].CommittedOut += d.Quantity
	}

	// 計画入庫: OPEN/PARTIALLY_RECEIVED PO の未入荷残 + 進行中 WO の残数量
	pos, err := s.repos.Purchases.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pos {
		if p.ItemID != itemID {
			continue
		}
		if p.SupplierQualityStatus == "BLOCKED" {
			continue
		}
		remaining := PurchaseScheduledRemaining(p.Status, p.Quantity, p.ReceivedQty)
		if remaining <= 0 {
			continue
		}
		if p.DueDate.Before(now) || p.DueDate.After(end) {
			continue
		}
		bs := bucketStart(p.DueDate)
		buckets[bucketIdx[bs]].ScheduledIn += remaining
	}

	wos, err := s.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range wos {
		if w.ItemID != itemID {
			continue
		}
		if w.Status != "PLANNED" && w.Status != "RELEASED" && w.Status != "IN_PROGRESS" {
			continue
		}
		if w.DueDate.Before(now) || w.DueDate.After(end) {
			continue
		}
		// 既に部分完成している分は除外
		remaining := w.Quantity - w.CompletedQty
		if remaining <= 0 {
			continue
		}
		bs := bucketStart(w.DueDate)
		buckets[bucketIdx[bs]].ScheduledIn += remaining
	}

	// 期間順にソート (念のため)
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Period.Before(buckets[j].Period) })

	resBuckets := CalcATP(ATPInput{StartingOnHand: startingOH, Buckets: buckets})
	return &domain.ATPResult{
		ItemID:   itemID,
		ItemCode: item.Code,
		Buckets:  resBuckets,
	}, nil
}

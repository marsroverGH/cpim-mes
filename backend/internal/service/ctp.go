package service

import (
	"context"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type CTPMaterialResult struct {
	ReadyDate time.Time
	Source    string
	Detail    map[string]any
}

type CTPEngine struct {
	db    *sqlx.DB
	repos *repository.Repositories
	crp   *CRPService
}

type ctpSupplyEvent struct {
	date time.Time
	qty  float64
}

// MaterialReady calculates a conservative all-materials-ready date for the
// hypothetical finished quantity. It uses the existing MRP netting/lot-sizing
// rules for shortage detection, quality-OK stock only, qualified firm receipts,
// and the item lead-time offset for hypothetical replenishment. Nothing is
// persisted.
func (e *CTPEngine) MaterialReady(ctx context.Context, parentID uuid.UUID, qty float64, start time.Time, horizonDays int) (CTPMaterialResult, error) {
	return e.MaterialReadyWithUsage(ctx, parentID, qty, start, horizonDays, nil)
}

// MaterialReadyWithUsage is the run-scoped variant used by Order Promising.
// cumulativeUsage prevents multiple lines in one promise run from reusing the
// same component stock/receipts as if each line were the only hypothetical demand.
func (e *CTPEngine) MaterialReadyWithUsage(ctx context.Context, parentID uuid.UUID, qty float64, start time.Time, horizonDays int, cumulativeUsage map[uuid.UUID]float64) (CTPMaterialResult, error) {
	start = TruncateDay(start)
	if horizonDays <= 0 {
		horizonDays = 180
	}
	end := start.AddDate(0, 0, horizonDays-1)
	rows, err := e.repos.BOM.Explode(ctx, parentID, qty)
	if err != nil {
		return CTPMaterialResult{}, err
	}
	if len(rows) == 0 {
		return CTPMaterialResult{ReadyDate: start, Source: "CTP_PRODUCTION", Detail: map[string]any{"components": 0}}, nil
	}
	items, err := e.repos.Items.List(ctx)
	if err != nil {
		return CTPMaterialResult{}, err
	}
	itemByID := map[uuid.UUID]domain.Item{}
	for _, it := range items {
		itemByID[it.ID] = it
	}

	ready := start
	purchaseShortage, productionShortage := false, false
	constraints := []map[string]any{}
	for _, row := range rows {
		it, ok := itemByID[row.ChildID]
		if !ok {
			continue
		}
		required := row.TotalQty
		if cumulativeUsage != nil {
			required += cumulativeUsage[row.ChildID]
		}
		componentReady, shortage, detail, err := e.componentReady(ctx, it, required, start, end)
		if err != nil {
			return CTPMaterialResult{}, err
		}
		if componentReady.After(ready) {
			ready = componentReady
		}
		if shortage {
			if it.Type == domain.ItemTypeRawMaterial || it.Type == domain.ItemTypePurchasedPart {
				purchaseShortage = true
			} else {
				productionShortage = true
			}
		}
		detail["runLineRequired"] = row.TotalQty
		constraints = append(constraints, detail)
		if cumulativeUsage != nil {
			cumulativeUsage[row.ChildID] += row.TotalQty
		}
	}
	source := "CTP_PRODUCTION"
	if purchaseShortage && productionShortage {
		source = "CTP_MIXED"
	} else if purchaseShortage {
		source = "CTP_PURCHASE"
	}
	return CTPMaterialResult{ReadyDate: ready, Source: source, Detail: map[string]any{"components": constraints}}, nil
}

func (e *CTPEngine) componentReady(ctx context.Context, it domain.Item, required float64, start, end time.Time) (time.Time, bool, map[string]any, error) {
	var sellable, reserved float64
	if err := e.db.GetContext(ctx, &sellable, `
SELECT COALESCE(SUM(x.qty),0)::double precision FROM (
 SELECT l.id,COALESCE(SUM(lm.quantity),0) qty
 FROM lots l LEFT JOIN lot_movements lm ON lm.lot_id=l.id
 WHERE l.item_id=$1 AND l.quality_status='OK' GROUP BY l.id) x`, it.ID); err != nil {
		return time.Time{}, false, nil, err
	}
	if err := e.db.GetContext(ctx, &reserved, `
SELECT COALESCE(SUM(CASE WHEN txn_type='RESERVE' THEN ABS(quantity) WHEN txn_type='UNRESERVE' THEN -ABS(quantity) ELSE 0 END),0)::double precision
FROM inventory_txns WHERE item_id=$1`, it.ID); err != nil {
		return time.Time{}, false, nil, err
	}
	free := sellable - reserved
	if free < 0 {
		free = 0
	}

	// Reuse MRP's safety-stock and lot-sizing netting semantics for shortage.
	net, planned, _ := netMRPBucket(free, required, 0, it.SafetyStock, it.LotSize, 0, LotSizeMethod(it.LotSizeMethod))
	if net <= 1e-9 {
		return start, false, map[string]any{"itemCode": it.Code, "required": required, "free": free, "readyDate": start.Format("2006-01-02"), "shortage": 0}, nil
	}

	events := []ctpSupplyEvent{}
	pos, err := e.repos.Purchases.List(ctx)
	if err != nil {
		return time.Time{}, false, nil, err
	}
	for _, p := range pos {
		if p.ItemID != it.ID || p.SupplierQualityStatus == "BLOCKED" {
			continue
		}
		remaining := PurchaseScheduledRemaining(p.Status, p.Quantity, p.ReceivedQty)
		if remaining <= 1e-9 {
			continue
		}
		d := PurchasePlanningDate(p)
		if d.Before(start) {
			d = start
		}
		if d.After(end) {
			continue
		}
		events = append(events, ctpSupplyEvent{d, remaining})
	}
	wos, err := e.repos.WorkOrders.List(ctx)
	if err != nil {
		return time.Time{}, false, nil, err
	}
	for _, w := range wos {
		if w.ItemID != it.ID || (w.Status != "RELEASED" && w.Status != "IN_PROGRESS") {
			continue
		}
		remaining := w.Quantity - w.CompletedQty
		if remaining <= 1e-9 {
			continue
		}
		d := TruncateDay(w.DueDate)
		if d.Before(start) {
			d = start
		}
		if d.After(end) {
			continue
		}
		events = append(events, ctpSupplyEvent{d, remaining})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].date.Before(events[j].date) })
	need := required + it.SafetyStock
	cum := free
	existingReady := time.Time{}
	for _, ev := range events {
		cum += ev.qty
		if cum+1e-9 >= need {
			existingReady = ev.date
			break
		}
	}
	leadTimeDays := it.LeadTimeDays
	if it.Type == domain.ItemTypeRawMaterial || it.Type == domain.ItemTypePurchasedPart {
		leadTimeDays, err = e.repos.Purchases.EffectiveLeadTimeDays(ctx, it.ID, it.LeadTimeDays)
		if err != nil {
			return time.Time{}, false, nil, err
		}
	}
	hypoReady := start.AddDate(0, 0, leadTimeDays)
	// planned is the MRP lot-sized replenishment. It is intentionally not persisted.
	_ = planned
	candidate := hypoReady
	if !existingReady.IsZero() && existingReady.Before(candidate) {
		candidate = existingReady
	}
	shortage := need - free
	if shortage < 0 {
		shortage = 0
	}
	return candidate, true, map[string]any{"itemCode": it.Code, "required": required, "free": free, "shortage": shortage, "leadTimeDays": leadTimeDays, "nominalLeadTimeDays": it.LeadTimeDays, "readyDate": candidate.Format("2006-01-02")}, nil
}

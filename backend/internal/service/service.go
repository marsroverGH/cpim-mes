package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type Services struct {
	Items          *ItemService
	BOM            *BOMService
	Demand         *DemandService
	MPS            *MPSService
	Inventory      *InventoryService
	WorkOrders     *WorkOrderService
	Purchases      *PurchaseService
	SalesOrders    *SalesOrderService
	OrderPromising *OrderPromisingService
	MRP            *MRPService

	WorkCenters *WorkCenterService
	Routings    *RoutingService
	CRP         *CRPService
	CostRollup  *CostRollupService

	Auth  *AuthService
	ABC   *ABCService
	CSV   *CSVService
	Lots  *LotService
	Audit *AuditService

	Forecast   *ForecastService
	CycleCount *CycleCountService

	Workflow        *WorkflowService
	Calendar        *CalendarService
	ATP             *ATPService
	Quality         *QualityService
	SupplierQuality *SupplierQualityService
	Actions         *ActionMessageService
	ShopFloor       *ShopFloorService
	KPI             *KPIService
	SOP             *SOPService
	RCCP            *RCCPService
	ECO             *ECOService
	Agent           *AgentService
}

// ServicesConfig — NewServices に渡す設定
type ServicesConfig struct {
	JWTSecret string
}

func NewServices(db *sqlx.DB, r *repository.Repositories, cfg ServicesConfig) *Services {
	mrp := &MRPService{repos: r}
	abc := &ABCService{repos: r}
	ledger := NewInventoryLedgerService(db)
	actions := &ActionMessageService{repos: r, mrp: mrp}
	itemsSvc := &ItemService{r: r.Items}
	kpiSvc := &KPIService{repos: r, mrp: mrp, am: actions}
	salesOrders := &SalesOrderService{db: db, ledger: ledger}
	atp := &ATPService{db: db, repos: r}
	crp := &CRPService{db: db, repos: r, mrp: mrp}
	ctp := &CTPEngine{db: db, repos: r, crp: crp}
	orderPromising := &OrderPromisingService{db: db, sales: salesOrders, atp: atp, ctp: ctp}
	svc := &Services{
		Items:           itemsSvc,
		BOM:             &BOMService{db: db, r: r.BOM},
		Demand:          &DemandService{r: r.Demand},
		MPS:             &MPSService{r: r.MPS},
		Inventory:       &InventoryService{r: r.Inventory, ledger: ledger},
		WorkOrders:      &WorkOrderService{r: r.WorkOrders},
		Purchases:       &PurchaseService{db: db, r: r.Purchases},
		SalesOrders:     salesOrders,
		OrderPromising:  orderPromising,
		MRP:             mrp,
		WorkCenters:     &WorkCenterService{r: r.WorkCenters},
		Routings:        &RoutingService{r: r.Routings},
		CRP:             crp,
		CostRollup:      &CostRollupService{repos: r},
		Auth:            NewAuthService(r.Users, cfg.JWTSecret),
		ABC:             abc,
		CSV:             NewCSVService(r),
		Lots:            &LotService{r: r.Lots, ledger: ledger},
		Audit:           &AuditService{r: r.Audit},
		Forecast:        &ForecastService{db: db, repos: r},
		CycleCount:      &CycleCountService{repos: r, abc: abc, ledger: ledger},
		Workflow:        NewWorkflowService(db, r, ledger),
		Calendar:        &CalendarService{r: r.Calendars},
		ATP:             atp,
		Quality:         &QualityService{db: db, repos: r},
		SupplierQuality: &SupplierQualityService{db: db, ledger: ledger},
		Actions:         actions,
		ShopFloor:       &ShopFloorService{db: db, r: r.ShopFloor},
		KPI:             kpiSvc,
		SOP:             &SOPService{db: db, repos: r},
		RCCP:            &RCCPService{repos: r},
		ECO:             NewECOService(db, r),
		Agent:           NewAgentService(r, mrp, abc, kpiSvc),
	}
	return svc
}

// ==================== thin services ====================

type ItemService struct{ r *repository.ItemRepo }

func (s *ItemService) List(ctx context.Context) ([]domain.Item, error) {
	return s.r.List(ctx)
}
func (s *ItemService) Create(ctx context.Context, i *domain.Item) error { return s.r.Create(ctx, i) }
func (s *ItemService) Update(ctx context.Context, i *domain.Item) error { return s.r.Update(ctx, i) }
func (s *ItemService) Delete(ctx context.Context, id uuid.UUID) error   { return s.r.Delete(ctx, id) }
func (s *ItemService) RecomputeLLC(ctx context.Context) error           { return s.r.RecomputeLLC(ctx) }
func (s *ItemService) Get(ctx context.Context, id uuid.UUID) (*domain.Item, error) {
	return s.r.Get(ctx, id)
}

type BOMService struct {
	db *sqlx.DB
	r  *repository.BOMRepo
}

func (s *BOMService) ComponentsOf(ctx context.Context, parent uuid.UUID) ([]domain.BOMComponent, error) {
	return s.r.ComponentsOf(ctx, parent)
}
func (s *BOMService) Explode(ctx context.Context, parent uuid.UUID, qty float64) ([]repository.ExplodedRow, error) {
	return s.r.Explode(ctx, parent, qty)
}

type DemandService struct{ r *repository.DemandRepo }

func (s *DemandService) List(ctx context.Context) ([]domain.DemandForecast, error) {
	return s.r.List(ctx)
}
func (s *DemandService) Create(ctx context.Context, d *domain.DemandForecast) error {
	return domain.NewConflict("demand_forecasts is legacy read-only; create customer demand through Sales Orders")
}

type MPSService struct{ r *repository.MPSRepo }

func (s *MPSService) List(ctx context.Context) ([]domain.MPSEntry, error) { return s.r.List(ctx) }
func (s *MPSService) Upsert(ctx context.Context, m *domain.MPSEntry) error {
	return s.r.Upsert(ctx, m)
}

type InventoryService struct {
	r      *repository.InventoryRepo
	ledger *InventoryLedgerService
}

func (s *InventoryService) Transactions(ctx context.Context, item uuid.UUID) ([]domain.InventoryTxn, error) {
	return s.r.Transactions(ctx, item)
}
func (s *InventoryService) Post(ctx context.Context, t *domain.InventoryTxn) error {
	// Logical reservations do not move physical stock and therefore intentionally
	// have no lot allocation. Keep the existing API compatible while routing every
	// physical movement through the unified ledger below.
	if t.TxnType == "RESERVE" || t.TxnType == "UNRESERVE" {
		return s.r.Post(ctx, t)
	}

	allocs := []LotAllocationInput(nil)
	lotID := uuid.Nil
	if t.LotID != nil {
		lotID = *t.LotID
		if t.Quantity < 0 {
			allocs = []LotAllocationInput{{LotID: lotID, Quantity: t.Quantity}}
		}
	}
	res, err := s.ledger.Post(ctx, PhysicalInventoryRequest{
		ID: t.ID, ItemID: t.ItemID, Quantity: t.Quantity, TxnType: t.TxnType,
		RefDoc: t.RefDoc, OccurredAt: t.OccurredAt, LotID: lotID, LotNo: t.LotNo,
		Allocations: allocs, SourceDoc: t.RefDoc, Notes: "Manual inventory transaction",
	})
	if err != nil {
		return err
	}
	lotNo := t.LotNo
	*t = res.Txn
	if len(res.Allocations) == 1 {
		id := res.Allocations[0].LotID
		t.LotID = &id
	}
	if len(res.Lots) > 0 {
		lotNo = res.Lots[0].LotNo
	}
	t.LotNo = lotNo
	return nil
}
func (s *InventoryService) OnHand(ctx context.Context) ([]repository.StockOnHand, error) {
	return s.r.OnHand(ctx)
}
func (s *InventoryService) Balance(ctx context.Context) ([]domain.StockBalance, error) {
	return s.r.Balance(ctx)
}
func (s *InventoryService) Reconciliation(ctx context.Context) ([]repository.InventoryLotReconciliation, error) {
	return s.r.Reconciliation(ctx)
}

type WorkOrderService struct{ r *repository.WorkOrderRepo }

func (s *WorkOrderService) List(ctx context.Context) ([]domain.WorkOrder, error) {
	return s.r.List(ctx)
}
func (s *WorkOrderService) Create(ctx context.Context, w *domain.WorkOrder) error {
	return s.r.Create(ctx, w)
}
func (s *WorkOrderService) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	// RELEASED / IN_PROGRESS / COMPLETED are workflow-managed states because they
	// have inventory side effects. The generic status endpoint is intentionally
	// limited to the administrative close step.
	w, err := s.r.Get(ctx, id)
	if err != nil {
		return err
	}
	if status == "CLOSED" && w.Status == "COMPLETED" {
		return s.r.UpdateStatus(ctx, id, status)
	}
	return domain.NewConflict(fmt.Sprintf(
		"status transition %s -> %s is not allowed through the generic endpoint; use release/progress/complete workflow",
		w.Status, status))
}
func (s *WorkOrderService) Get(ctx context.Context, id uuid.UUID) (*domain.WorkOrder, error) {
	return s.r.Get(ctx, id)
}
func (s *WorkOrderService) UpdateProgress(ctx context.Context, id uuid.UUID, completed float64) error {
	return s.r.UpdateProgress(ctx, id, completed)
}

type PurchaseService struct {
	db *sqlx.DB
	r  *repository.PurchaseRepo
}

func (s *PurchaseService) List(ctx context.Context) ([]domain.PurchaseOrder, error) {
	return s.r.List(ctx)
}
func (s *PurchaseService) Create(ctx context.Context, p *domain.PurchaseOrder) error {
	if p == nil {
		return domain.NewBadRequest("purchase order required", nil)
	}
	var status string
	err := s.db.GetContext(ctx, &status, `SELECT status FROM supplier_quality_profiles WHERE supplier_name=btrim($1)`, p.Supplier)
	if err == nil && status == "BLOCKED" {
		return domain.NewConflict("supplier is BLOCKED by Supplier Quality")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.r.Create(ctx, p)
}
func (s *PurchaseService) ListReceipts(ctx context.Context, poID uuid.UUID) ([]domain.PurchaseReceipt, error) {
	return s.r.ListReceipts(ctx, poID)
}

// ==================== MRP ====================

// MRPService implements the CPIM-style planning sequence used by this system:
//
//	MPS -> Netting -> Planned Order Receipt -> Lead-time Offset
//	    -> Planned Order Release -> direct-child BOM explosion
//
// Important rules:
//   - MPS is the independent-demand driver. demand_forecasts is NOT read here.
//   - Open/partially-received PO remainder and released/in-progress WO remainder are scheduled receipts.
//   - Safety stock is part of netting, not an unconditional add-on after a shortage.
//   - A planned receipt is offset by the item's lead time to obtain its release date.
//   - Only direct BOM children are exploded from that release. Recursive BOM.Explode is
//     intentionally not used, preventing lower-level demand from being counted twice.
//   - Items are processed in ascending Low-Level Code (LLC), so all parent-generated
//     dependent demand is accumulated before a lower-level item is netted.
type MRPService struct {
	repos *repository.Repositories
}

type MRPRequest struct {
	HorizonDays int       `json:"horizonDays"`
	StartDate   time.Time `json:"startDate"`
}

func (s *MRPService) Run(ctx context.Context, req MRPRequest) ([]domain.MRPResult, error) {
	return s.run(ctx, req, true)
}

// Simulate performs the same MRP netting without LLC maintenance writes. It is
// used by side-effect-free planning simulations after normal BOM writes have
// already maintained LLC integrity.
func (s *MRPService) Simulate(ctx context.Context, req MRPRequest) ([]domain.MRPResult, error) {
	return s.run(ctx, req, false)
}

func (s *MRPService) run(ctx context.Context, req MRPRequest, recomputeLLC bool) ([]domain.MRPResult, error) {
	if req.StartDate.IsZero() {
		req.StartDate = time.Now()
	}
	if req.HorizonDays <= 0 {
		req.HorizonDays = 28
	}
	startDay := TruncateDay(req.StartDate)
	endDay := startDay.AddDate(0, 0, req.HorizonDays-1)

	// LLC is a correctness prerequisite for one-pass level-by-level planning.
	// Recompute before each MRP run so a stale LLC cannot silently omit dependent demand.
	// A cyclic BOM makes recompute fail, which is safer than producing an invalid plan.
	if recomputeLLC {
		if err := s.repos.Items.RecomputeLLC(ctx); err != nil {
			return nil, err
		}
	}

	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	itemByID := make(map[uuid.UUID]domain.Item, len(items))
	for _, it := range items {
		itemByID[it.ID] = it
	}

	onHand, err := s.repos.Inventory.OnHand(ctx)
	if err != nil {
		return nil, err
	}
	stock := make(map[uuid.UUID]float64, len(onHand))
	for _, row := range onHand {
		stock[row.ItemID] = row.OnHand
	}

	// -----------------------------------------------------------------
	// 1) MPS = independent gross requirements
	// -----------------------------------------------------------------
	mps, err := s.repos.MPS.List(ctx)
	if err != nil {
		return nil, err
	}
	gross := make(map[bucketKey]float64)
	pegging := make(map[bucketKey]map[string]bool)
	addPeg := func(k bucketKey, src string) {
		if src == "" {
			return
		}
		if pegging[k] == nil {
			pegging[k] = make(map[string]bool)
		}
		pegging[k][src] = true
	}
	for _, m := range mps {
		if m.Planned <= 0 {
			continue
		}
		day := TruncateDay(m.Period)
		if day.Before(startDay) || day.After(endDay) {
			continue
		}
		k := bucketKey{Day: day, Item: m.ItemID}
		gross[k] += m.Planned
		if it, ok := itemByID[m.ItemID]; ok {
			addPeg(k, it.Code)
		}
	}

	// -----------------------------------------------------------------
	// 2) Scheduled receipts = open PO + released/in-progress WO remainder
	// -----------------------------------------------------------------
	scheduled := make(map[bucketKey]float64)
	componentsCache := make(map[uuid.UUID][]domain.BOMComponent)

	pos, err := s.repos.Purchases.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, po := range pos {
		if po.SupplierQualityStatus == "BLOCKED" {
			continue // blocked supplier receipts cannot be posted; do not count as firm supply
		}
		remaining := PurchaseScheduledRemaining(po.Status, po.Quantity, po.ReceivedQty)
		if remaining <= 0 {
			continue
		}
		day := TruncateDay(po.DueDate)
		if day.After(endDay) {
			continue
		}
		// Past-due open supply is due immediately for netting purposes.
		if day.Before(startDay) {
			day = startDay
		}
		scheduled[bucketKey{Day: day, Item: po.ItemID}] += remaining
	}

	wos, err := s.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, wo := range wos {
		if wo.Status != "RELEASED" && wo.Status != "IN_PROGRESS" {
			continue
		}
		remaining := wo.Quantity - wo.CompletedQty
		if remaining <= 0 {
			continue
		}
		day := TruncateDay(wo.DueDate)
		if !day.After(endDay) {
			if day.Before(startDay) {
				day = startDay
			}
			scheduled[bucketKey{Day: day, Item: wo.ItemID}] += remaining
		}

		// A released/in-progress WO is firm supply for its parent AND firm
		// dependent demand for its direct components. If the parent scheduled
		// receipt fully nets the MPS, there may be no new planned release, so
		// this direct-child explosion is required to avoid losing component demand.
		componentDay := TruncateDay(wo.StartDate)
		if componentDay.Before(startDay) {
			componentDay = startDay
		}
		if !componentDay.After(endDay) {
			components, ok := componentsCache[wo.ItemID]
			if !ok {
				components, err = s.repos.BOM.ComponentsOf(ctx, wo.ItemID)
				if err != nil {
					return nil, err
				}
				componentsCache[wo.ItemID] = components
			}
			parentCode := ""
			if parent, ok := itemByID[wo.ItemID]; ok {
				parentCode = parent.Code
			}
			for _, c := range components {
				required := directComponentRequirement(remaining, c.Quantity, c.ScrapPct)
				childKey := bucketKey{Day: componentDay, Item: c.ChildID}
				gross[childKey] += required
				addPeg(childKey, parentCode)
			}
		}
	}

	// -----------------------------------------------------------------
	// 3) LLC groups. A component can appear at multiple BOM levels; its
	//    Low-Level Code is the deepest occurrence and it is netted once.
	// -----------------------------------------------------------------
	llcGroups := make(map[int][]uuid.UUID)
	for _, it := range items {
		llcGroups[it.LowLevelCode] = append(llcGroups[it.LowLevelCode], it.ID)
	}
	llcLevels := make([]int, 0, len(llcGroups))
	for lvl := range llcGroups {
		llcLevels = append(llcLevels, lvl)
	}
	sort.Ints(llcLevels)

	// EOQ informational annual-demand base now comes from MPS, not forecasts.
	annualByItem := make(map[uuid.UUID]float64)
	for _, m := range mps {
		if m.Planned > 0 {
			annualByItem[m.ItemID] += m.Planned
		}
	}

	results := make([]domain.MRPResult, 0)

	for _, lvl := range llcLevels {
		ids := llcGroups[lvl]
		sort.Slice(ids, func(i, j int) bool {
			return itemByID[ids[i]].Code < itemByID[ids[j]].Code
		})
		idSet := make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			idSet[id] = true
		}

		// POQ remains a period-grouping rule, but it is applied only to this LLC's
		// gross requirements after all higher-level dependent demand has arrived.
		levelGross := make(map[bucketKey]float64)
		levelPeg := make(map[bucketKey]map[string]bool)
		for k, q := range gross {
			if idSet[k.Item] {
				levelGross[k] = q
				levelPeg[k] = pegging[k]
			}
		}
		levelGross, levelPeg = poqAggregate(levelGross, levelPeg, itemByID)

		for _, itemID := range ids {
			it := itemByID[itemID]
			method := LotSizeMethod(it.LotSizeMethod)
			if method == "" {
				method = LotMethodLFL
			}

			// Build chronological event buckets from gross requirements and scheduled
			// receipts. Also create a start bucket when safety stock itself requires
			// replenishment, even if no demand event exists yet.
			dateSet := make(map[time.Time]bool)
			for k := range levelGross {
				if k.Item == itemID {
					dateSet[k.Day] = true
				}
			}
			for k := range scheduled {
				if k.Item == itemID {
					dateSet[k.Day] = true
				}
			}
			if stock[itemID] < it.SafetyStock {
				dateSet[startDay] = true
			}
			if len(dateSet) == 0 {
				continue
			}
			days := make([]time.Time, 0, len(dateSet))
			for day := range dateSet {
				days = append(days, day)
			}
			sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

			eoq := 0.0
			if method == LotMethodEOQ {
				holding := it.StandardCost * it.HoldingCostPct
				ann := annualByItem[itemID]
				if ann == 0 {
					var sum float64
					for k, q := range levelGross {
						if k.Item == itemID {
							sum += q
						}
					}
					// Annualize the current horizon for dependent-demand items.
					ann = sum * 365 / float64(req.HorizonDays)
				}
				eoq = EOQ(ann, it.OrderingCost, holding)
			}

			for _, day := range days {
				k := bucketKey{Day: day, Item: itemID}
				grossQty := levelGross[k]
				scheduledQty := scheduled[k]

				net, plannedReceipt, projected := netMRPBucket(
					stock[itemID], grossQty, scheduledQty, it.SafetyStock,
					it.LotSize, eoq, method,
				)
				stock[itemID] = projected

				releaseDay := plannedOrderReleaseDate(day, it.LeadTimeDays, plannedReceipt)
				var releaseDate *time.Time
				if !releaseDay.IsZero() {
					rd := releaseDay
					releaseDate = &rd
				}

				var pegs []string
				for code := range levelPeg[k] {
					pegs = append(pegs, code)
				}
				sort.Strings(pegs)

				results = append(results, domain.MRPResult{
					ItemID:             itemID,
					ItemCode:           it.Code,
					Period:             day,
					GrossReq:           grossQty,
					ScheduledRcpt:      scheduledQty,
					OnHand:             projected,
					NetReq:             net,
					PlannedReceipt:     plannedReceipt,
					PlannedOrder:       plannedReceipt,
					PlannedReleaseDate: releaseDate,
					LotMethod:          string(method),
					EOQ:                eoq,
					Pegging:            pegs,
				})

				// -----------------------------------------------------------------
				// 4) Planned Order Release -> DIRECT children only.
				// The child's gross-requirement date is the parent's release date.
				// -----------------------------------------------------------------
				if plannedReceipt <= 0 {
					continue
				}
				components, ok := componentsCache[itemID]
				if !ok {
					components, err = s.repos.BOM.ComponentsOf(ctx, itemID)
					if err != nil {
						return nil, err
					}
					componentsCache[itemID] = components
				}
				for _, c := range components {
					child, ok := itemByID[c.ChildID]
					if !ok {
						continue
					}
					// If LLC is invalid/stale, fail rather than silently dropping demand
					// into an already-processed level.
					if child.LowLevelCode <= it.LowLevelCode {
						return nil, domain.NewValidation(map[string]string{"bom": "invalid low-level code: child must be below parent; recompute LLC and check for BOM cycles"})
					}
					required := directComponentRequirement(plannedReceipt, c.Quantity, c.ScrapPct)
					// Past-due dependent demand is placed in the MRP start bucket;
					// the parent's actual past-due release date remains visible in its result.
					dependentDay := dependentRequirementDate(releaseDay, startDay)
					childKey := bucketKey{Day: dependentDay, Item: c.ChildID}
					gross[childKey] += required

					if len(pegs) > 0 {
						for _, peg := range pegs {
							addPeg(childKey, peg)
						}
					} else {
						addPeg(childKey, it.Code)
					}
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Period.Equal(results[j].Period) {
			return results[i].ItemCode < results[j].ItemCode
		}
		return results[i].Period.Before(results[j].Period)
	})
	return results, nil
}

// bucketKey — MRP の (日付 × 品目) バケットキー
type bucketKey struct {
	Day  time.Time
	Item uuid.UUID
}

// poqAggregate — POQ (Period Order Quantity) 適用: 品目の lot_size_method=POQ かつ
// poq_periods=N の場合、N 個の連続バケットを最先頭バケットにまとめる。
// pegging も合算する。
func poqAggregate(
	gross map[bucketKey]float64,
	pegging map[bucketKey]map[string]bool,
	itemByID map[uuid.UUID]domain.Item,
) (map[bucketKey]float64, map[bucketKey]map[string]bool) {
	// 品目別にバケットを日付昇順で集める
	byItem := make(map[uuid.UUID][]time.Time)
	for k := range gross {
		byItem[k.Item] = append(byItem[k.Item], k.Day)
	}
	for id := range byItem {
		days := byItem[id]
		sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
		byItem[id] = days
	}

	for itemID, days := range byItem {
		it, ok := itemByID[itemID]
		if !ok || it.LotSizeMethod != "POQ" || it.PoqPeriods <= 1 {
			continue
		}
		group := it.PoqPeriods
		// group 個ずつ合算: 先頭にまとめる
		for i := 0; i < len(days); i += group {
			head := bucketKey{Day: days[i], Item: itemID}
			for j := i + 1; j < i+group && j < len(days); j++ {
				k := bucketKey{Day: days[j], Item: itemID}
				gross[head] += gross[k]
				if pegging[head] == nil {
					pegging[head] = make(map[string]bool)
				}
				for c := range pegging[k] {
					pegging[head][c] = true
				}
				delete(gross, k)
				delete(pegging, k)
			}
		}
	}
	return gross, pegging
}

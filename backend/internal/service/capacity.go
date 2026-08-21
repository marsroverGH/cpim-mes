package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ====================================================================
// Work Center / Routing thin services
// ====================================================================

type WorkCenterService struct{ r *repository.WorkCenterRepo }

func (s *WorkCenterService) List(ctx context.Context) ([]domain.WorkCenter, error) {
	return s.r.List(ctx)
}
func (s *WorkCenterService) Create(ctx context.Context, w *domain.WorkCenter) error {
	if w.Efficiency == 0 {
		w.Efficiency = 1
	}
	if w.Utilization == 0 {
		w.Utilization = 0.85
	}
	if w.CapacityMinutesPerDay == 0 {
		w.CapacityMinutesPerDay = 480
	}
	if w.ShiftStartMinute <= 0 || w.ShiftStartMinute > 1439 {
		w.ShiftStartMinute = 480
	}
	if w.MachineCount <= 0 {
		w.MachineCount = 1
	}
	if w.WorkerCount < 0 {
		w.WorkerCount = 0
	}
	if w.WorkerCount == 0 {
		w.WorkerCount = 1
	}
	return s.r.Create(ctx, w)
}
func (s *WorkCenterService) Update(ctx context.Context, w *domain.WorkCenter) error {
	if w.ShiftStartMinute <= 0 || w.ShiftStartMinute > 1439 {
		w.ShiftStartMinute = 480
	}
	if w.MachineCount <= 0 {
		w.MachineCount = 1
	}
	if w.WorkerCount < 0 {
		w.WorkerCount = 0
	}
	return s.r.Update(ctx, w)
}
func (s *WorkCenterService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.r.Delete(ctx, id)
}

type RoutingService struct{ r *repository.RoutingRepo }

func (s *RoutingService) List(ctx context.Context) ([]domain.Routing, error) {
	return s.r.List(ctx)
}
func (s *RoutingService) ActiveForItem(ctx context.Context, itemID uuid.UUID) (*domain.Routing, error) {
	return s.r.ActiveForItem(ctx, itemID)
}
func (s *RoutingService) Operations(ctx context.Context, routingID uuid.UUID) ([]domain.RoutingOperation, error) {
	return s.r.Operations(ctx, routingID)
}
func (s *RoutingService) Create(ctx context.Context, rt *domain.Routing) error {
	return s.r.Create(ctx, rt)
}
func validateDetailedRoutingOperation(op *domain.RoutingOperation) error {
	if op.MachinesRequired <= 0 {
		op.MachinesRequired = 1
	}
	if op.WorkersRequired < 0 {
		op.WorkersRequired = 0
	}
	if op.TransferBatchQty < 0 {
		return domain.NewBadRequest("transferBatchQty must be >= 0", nil)
	}
	if op.OverlapEnabled && op.TransferBatchQty <= 0 {
		return domain.NewBadRequest("transferBatchQty must be > 0 when overlapEnabled is true", nil)
	}
	return nil
}

func (s *RoutingService) AddOperation(ctx context.Context, op *domain.RoutingOperation) error {
	if err := validateDetailedRoutingOperation(op); err != nil {
		return err
	}
	return s.r.AddOperation(ctx, op)
}

func (s *RoutingService) UpdateOperation(ctx context.Context, op *domain.RoutingOperation) error {
	if err := validateDetailedRoutingOperation(op); err != nil {
		return err
	}
	return s.r.UpdateOperation(ctx, op)
}
func (s *RoutingService) Alternatives(ctx context.Context, opID uuid.UUID) ([]domain.RoutingOperationAlternative, error) {
	return s.r.Alternatives(ctx, opID)
}
func (s *RoutingService) AddAlternative(ctx context.Context, x *domain.RoutingOperationAlternative) error {
	if x.RunTimeMultiplier <= 0 || x.SetupTimeMultiplier <= 0 {
		return domain.NewBadRequest("alternative multipliers must be > 0", nil)
	}
	return s.r.AddAlternative(ctx, x)
}
func (s *RoutingService) DeleteAlternative(ctx context.Context, id uuid.UUID) error {
	return s.r.DeleteAlternative(ctx, id)
}
func (s *WorkCenterService) SetupMatrix(ctx context.Context, wcID uuid.UUID) ([]domain.WorkCenterSetupMatrixRow, error) {
	return s.r.SetupMatrix(ctx, wcID)
}
func (s *WorkCenterService) UpsertSetupMatrix(ctx context.Context, x *domain.WorkCenterSetupMatrixRow) error {
	if strings.TrimSpace(x.ToSetupFamily) == "" {
		return domain.NewBadRequest("toSetupFamily is required", nil)
	}
	if x.SetupMinutes < 0 {
		return domain.NewBadRequest("setupMinutes must be >= 0", nil)
	}
	return s.r.UpsertSetupMatrix(ctx, x)
}
func (s *WorkCenterService) DeleteSetupMatrix(ctx context.Context, id uuid.UUID) error {
	return s.r.DeleteSetupMatrix(ctx, id)
}
func (s *RoutingService) DeleteOperation(ctx context.Context, id uuid.UUID) error {
	return s.r.DeleteOperation(ctx, id)
}

// ====================================================================
// CRP — Capacity Requirements Planning
// ====================================================================
//
// MRP の Planned Order を入力に、各 Work Center 毎の必要時間を集計し、
// 利用可能能力 (capacity * efficiency * utilization) と比較して
// 負荷率 (load%) を算出する。
//
// 簡略化の前提:
//   - 工程毎の所要時間は当該日に集中する (リードタイム配賦は無し)
//   - 段取り時間 (setup) はロット毎に1回ではなく1計画オーダ毎に1回
//   - リソース別の代替・優先順位は考慮しない

type CRPService struct {
	db          *sqlx.DB
	repos       *repository.Repositories
	mrp         *MRPService
	maintenance *MaintenanceService
}

type CRPRequest struct {
	HorizonDays int       `json:"horizonDays"`
	StartDate   time.Time `json:"startDate"`
}

func (s *CRPService) Run(ctx context.Context, req CRPRequest) ([]domain.CapacityLoadRow, error) {
	// 1) MRP で Planned Order を取得
	mrpResults, err := s.mrp.Run(ctx, MRPRequest{
		HorizonDays: req.HorizonDays,
		StartDate:   req.StartDate,
	})
	if err != nil {
		return nil, err
	}

	// 2) 作業区一覧
	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	wcs, _, err = ApplyCapacityFeedbackToWorkCenters(ctx, s.db, wcs, req.StartDate)
	if err != nil {
		return nil, err
	}
	wcByID := make(map[uuid.UUID]domain.WorkCenter, len(wcs))
	for _, w := range wcs {
		wcByID[w.ID] = w
	}

	// 2.5) 作業区毎のカレンダースナップショットを構築
	from := TruncateDay(req.StartDate)
	to := from.AddDate(0, 0, req.HorizonDays)
	calSvc := &CalendarService{r: s.repos.Calendars}
	defaultSnap, _ := calSvc.LoadDefaultSnapshot(ctx, from, to)
	snapshotsByWC := make(map[uuid.UUID]*CalendarSnapshot, len(wcs))
	for _, w := range wcs {
		if w.CalendarID != nil {
			if snap, err := calSvc.LoadSnapshot(ctx, *w.CalendarID, from, to); err == nil && snap != nil {
				snapshotsByWC[w.ID] = snap
				continue
			}
		}
		snapshotsByWC[w.ID] = defaultSnap // フォールバック
	}

	// 3) Planned Order ごとに、ルーティング工程を seq_no 昇順で取り、
	//    最終工程を完了日に置いて、稼働日 (休日スキップ) で1日ずつ前倒しする
	type bk struct {
		Day time.Time
		WC  uuid.UUID
	}
	required := make(map[bk]float64)

	opsCache := make(map[uuid.UUID][]domain.RoutingOperation)

	for _, m := range mrpResults {
		if m.PlannedOrder <= 0 {
			continue
		}
		ops, ok := opsCache[m.ItemID]
		if !ok {
			ops, err = s.repos.Routings.OperationsForItem(ctx, m.ItemID)
			if err != nil {
				return nil, err
			}
			sort.Slice(ops, func(i, j int) bool { return ops[i].SeqNo < ops[j].SeqNo })
			opsCache[m.ItemID] = ops
		}

		startDay := TruncateDay(req.StartDate)
		// 最終工程の作業区カレンダーで完了日を稼働日に丸める
		// (シンプルに作業区毎のカレンダーで個別判定する方式)
		// 工程を後ろから順に埋めていく
		dueDay := TruncateDay(m.Period)
		for i := len(ops) - 1; i >= 0; i-- {
			op := ops[i]
			snap := snapshotsByWC[op.WorkCenterID]
			day := dueDay
			if snap != nil {
				day = snap.PreviousWorkDay(dueDay, 30)
			}
			if day.Before(startDay) {
				day = startDay
			}
			minutes := op.SetupMinutes + op.RunMinutesPerUnit*m.PlannedOrder
			required[bk{Day: day, WC: op.WorkCenterID}] += minutes
			// 次 (前) の工程の候補日 = この工程の前日
			dueDay = day.AddDate(0, 0, -1)
		}
	}

	// 4) 結果生成 — 負荷がある作業区×日のみ。capacity はカレンダーから日次で算出
	out := make([]domain.CapacityLoadRow, 0, len(required))
	for k, mins := range required {
		w := wcByID[k.WC]
		// その日の利用可能分数
		var avail float64
		isHoliday := false
		if snap := snapshotsByWC[k.WC]; snap != nil {
			availMin := snap.MinutesAvailable(k.Day)
			// 効率と稼働率を反映
			eff := w.Efficiency
			if eff <= 0 {
				eff = 1
			}
			util := w.Utilization
			if util <= 0 {
				util = 1
			}
			avail = float64(availMin) * eff * util
			if availMin == 0 {
				isHoliday = true
			}
		} else {
			avail = w.EffectiveCapacityMin()
		}
		loadPct := 0.0
		if avail > 0 {
			loadPct = mins / avail * 100
		}
		out = append(out, domain.CapacityLoadRow{
			WorkCenterID:     k.WC,
			WorkCenterCode:   w.Code,
			WorkCenterName:   w.Name,
			Date:             k.Day,
			RequiredMinutes:  mins,
			AvailableMinutes: avail,
			LoadPct:          loadPct,
			IsHoliday:        isHoliday,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].WorkCenterCode < out[j].WorkCenterCode
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out, nil
}

// ====================================================================
// Cost Rollup — 原価積み上げ (Material + Labor + Overhead)
// ====================================================================
//
// FG/SA の標準原価を以下で算出:
//   Material = Σ (子部品のtotalCost × 数量 × (1 + scrap_pct))
//   Labor    = Σ (各工程の (setup + run × 1単位)) × 作業区の laborRatePerMinute
//   Overhead = Σ (各工程の (setup + run × 1単位)) × 作業区の overheadRatePerMinute
//   Total    = Material + Labor + Overhead
//
// 葉ノード (BOM が無い品目) は items.standard_cost をそのまま採用する。

type CostRollupService struct {
	repos *repository.Repositories
}

func (s *CostRollupService) Rollup(ctx context.Context) ([]domain.CostRollupRow, error) {
	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	edges, err := s.repos.BOM.AllEdges(ctx)
	if err != nil {
		return nil, err
	}
	childrenByParent := make(map[uuid.UUID][]domain.BOMComponent)
	for _, e := range edges {
		childrenByParent[e.ParentID] = append(childrenByParent[e.ParentID], e)
	}

	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	type wcRates struct{ labor, overhead float64 }
	rateByWC := make(map[uuid.UUID]wcRates, len(wcs))
	for _, w := range wcs {
		rateByWC[w.ID] = wcRates{labor: w.LaborRatePerMinute, overhead: w.OverheadRatePerMinute}
	}

	itemByID := make(map[uuid.UUID]domain.Item, len(items))
	for _, it := range items {
		itemByID[it.ID] = it
	}

	// 自工程の労務費 + 間接費を計算 (子部品からの伝播分は別途)
	laborOwn := make(map[uuid.UUID]float64, len(items))
	overheadOwn := make(map[uuid.UUID]float64, len(items))
	for _, it := range items {
		ops, err := s.repos.Routings.OperationsForItem(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		var lc, oc float64
		for _, op := range ops {
			minutes := op.SetupMinutes + op.RunMinutesPerUnit
			r := rateByWC[op.WorkCenterID]
			lc += minutes * r.labor
			oc += minutes * r.overhead
		}
		laborOwn[it.ID] = lc
		overheadOwn[it.ID] = oc
	}

	// メモ化付き再帰で材料/労務/間接を集計 (子部品分も含む積み上げ)
	type rollup struct{ mat, lab, oh float64 }
	memo := make(map[uuid.UUID]rollup)
	var totalOf func(id uuid.UUID, depth int) rollup
	totalOf = func(id uuid.UUID, depth int) rollup {
		if v, ok := memo[id]; ok {
			return v
		}
		if depth > 50 {
			return rollup{} // 循環参照ガード
		}
		kids, hasKids := childrenByParent[id]
		out := rollup{lab: laborOwn[id], oh: overheadOwn[id]}
		if !hasKids || len(kids) == 0 {
			// 葉: standard_cost を Material として採用
			it := itemByID[id]
			out.mat = it.StandardCost
		} else {
			for _, e := range kids {
				ct := totalOf(e.ChildID, depth+1)
				factor := e.Quantity * (1 + e.ScrapPct)
				// 子の総原価 (mat+lab+oh) を Material として親に伝播
				out.mat += (ct.mat + ct.lab + ct.oh) * factor
			}
		}
		memo[id] = out
		return out
	}

	out := make([]domain.CostRollupRow, 0, len(items))
	for _, it := range items {
		r := totalOf(it.ID, 0)
		out = append(out, domain.CostRollupRow{
			ItemID:       it.ID,
			ItemCode:     it.Code,
			ItemName:     it.Name,
			ItemType:     it.Type,
			MaterialCost: r.mat,
			LaborCost:    r.lab,
			OverheadCost: r.oh,
			TotalCost:    r.mat + r.lab + r.oh,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemCode < out[j].ItemCode })
	return out, nil
}

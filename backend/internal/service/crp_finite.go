package service

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

const crpFiniteMode = "FINITE_FORWARD"

type CRPActor struct {
	UserID   uuid.UUID
	Username string
}

type CRPFiniteRequest struct {
	HorizonDays int
	StartDate   time.Time
}

type freeInterval struct {
	start float64 // clock minutes from local midnight
	end   float64
}

type capacityDay struct {
	day              time.Time
	clockMinutes     float64
	effectiveLoad    float64
	rate             float64 // standard load minutes processed per clock minute
	shiftStartMinute float64
	free             []freeInterval
}

type scheduleTask struct {
	sourceType  string
	sourceRef   string
	workOrderID *uuid.UUID
	itemID      uuid.UUID
	itemCode    string
	quantity    float64
	priority    int
	earliest    time.Time
	dueAt       time.Time
	firm        bool
	operations  []scheduleTaskOperation
}

type scheduleTaskOperation struct {
	seq         int
	description string
	wcID        uuid.UUID
	loadMinutes float64
}

type finiteAllocator struct {
	start      time.Time
	end        time.Time
	workCenter map[uuid.UUID]domain.WorkCenter
	calendars  map[uuid.UUID]*CalendarSnapshot
	days       map[string]*capacityDay
}

func dayStateKey(wcID uuid.UUID, day time.Time) string {
	return wcID.String() + ":" + TruncateDay(day).Format("2006-01-02")
}

func (a *finiteAllocator) dayState(wcID uuid.UUID, day time.Time) *capacityDay {
	day = TruncateDay(day)
	key := dayStateKey(wcID, day)
	if st, ok := a.days[key]; ok {
		return st
	}
	wc, ok := a.workCenter[wcID]
	if !ok {
		return nil
	}

	clockMinutes := wc.CapacityMinutesPerDay
	if clockMinutes <= 0 {
		clockMinutes = 480
	}
	rawCapacity := wc.CapacityMinutesPerDay
	if rawCapacity <= 0 {
		rawCapacity = clockMinutes
	}
	if snap := a.calendars[wcID]; snap != nil {
		clockMinutes = float64(snap.MinutesAvailable(day))
		if clockMinutes <= 0 {
			rawCapacity = 0
		} else {
			reference := float64(snap.Calendar.MinutesForWeekday(day.Weekday()))
			if reference <= 0 {
				reference = clockMinutes
			}
			rawCapacity = wc.CapacityMinutesPerDay
			if rawCapacity <= 0 {
				rawCapacity = reference
			}
			rawCapacity *= clockMinutes / reference
		}
	}
	machines := wc.MachineCount
	if machines <= 0 {
		machines = 1
	}
	rawCapacity *= float64(machines)

	eff := wc.Efficiency
	if eff <= 0 {
		eff = 1
	}
	util := wc.Utilization
	if util <= 0 {
		util = 1
	}
	effectiveLoad := rawCapacity * eff * util
	rate := 0.0
	if clockMinutes > 0 && effectiveLoad > 0 {
		rate = effectiveLoad / clockMinutes
	}
	shift := float64(wc.ShiftStartMinute)
	if shift < 0 || shift > 1439 {
		shift = 480
	}
	st := &capacityDay{
		day: day, clockMinutes: clockMinutes, effectiveLoad: effectiveLoad,
		rate: rate, shiftStartMinute: shift,
	}
	if clockMinutes > 0 && rate > 0 {
		st.free = []freeInterval{{start: shift, end: shift + clockMinutes}}
	}
	a.days[key] = st
	return st
}

func (a *finiteAllocator) allocateForward(wcID uuid.UUID, earliest time.Time, load float64) ([]domain.CRPScheduleSegment, float64) {
	if load <= 1e-9 {
		return nil, 0
	}
	if earliest.Before(a.start) {
		earliest = a.start
	}
	remaining := load
	segments := make([]domain.CRPScheduleSegment, 0, 2)
	for day := TruncateDay(earliest); !day.After(a.end); day = day.AddDate(0, 0, 1) {
		st := a.dayState(wcID, day)
		if st == nil || st.rate <= 0 || len(st.free) == 0 {
			continue
		}
		for i := 0; i < len(st.free) && remaining > 1e-9; i++ {
			iv := st.free[i]
			startMin := iv.start
			if TruncateDay(earliest).Equal(day) {
				e := earliest.Sub(day).Minutes()
				if e > startMin {
					startMin = e
				}
			}
			if startMin >= iv.end-1e-9 {
				continue
			}
			loadCap := (iv.end - startMin) * st.rate
			alloc := math.Min(remaining, loadCap)
			if alloc <= 1e-9 {
				continue
			}
			clockNeeded := alloc / st.rate
			endMin := startMin + clockNeeded
			segStart := day.Add(time.Duration(math.Round(startMin*60)) * time.Second)
			segEnd := day.Add(time.Duration(math.Round(endMin*60)) * time.Second)
			segments = append(segments, domain.CRPScheduleSegment{
				StartAt: segStart, EndAt: segEnd, LoadMinutes: alloc,
				ClockMinutes: clockNeeded, EffectiveLoadRate: st.rate,
			})

			// Remove [startMin,endMin) from the free interval while preserving any prefix/suffix.
			repl := make([]freeInterval, 0, 2)
			if iv.start < startMin-1e-9 {
				repl = append(repl, freeInterval{start: iv.start, end: startMin})
			}
			if endMin < iv.end-1e-9 {
				repl = append(repl, freeInterval{start: endMin, end: iv.end})
			}
			st.free = append(append(append([]freeInterval{}, st.free[:i]...), repl...), st.free[i+1:]...)
			i += len(repl) - 1
			remaining -= alloc
		}
		if remaining <= 1e-9 {
			break
		}
		earliest = day.AddDate(0, 0, 1)
	}
	return segments, math.Max(remaining, 0)
}

func businessDueEnd(d time.Time) time.Time {
	day := TruncateDay(d)
	return day.Add(24*time.Hour - time.Second)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// FiniteSchedule builds and persists an immutable finite-capacity CRP snapshot.
// Firm released/in-progress WOs reserve capacity first. MRP planned orders are then
// scheduled by due date into the remaining capacity. Operations may span workdays,
// but routing precedence is preserved because each successor starts only after the
// previous operation's last segment ends.
func (s *CRPService) FiniteSchedule(ctx context.Context, req CRPFiniteRequest, actor CRPActor) (*domain.CRPFiniteScheduleResult, error) {
	if req.StartDate.IsZero() {
		req.StartDate = time.Now()
	}
	if req.HorizonDays <= 0 {
		req.HorizonDays = 28
	}
	if req.HorizonDays > 366 {
		return nil, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	startDay := TruncateDay(req.StartDate)
	endDay := startDay.AddDate(0, 0, req.HorizonDays-1)

	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	itemByID := make(map[uuid.UUID]domain.Item, len(items))
	for _, it := range items {
		itemByID[it.ID] = it
	}
	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	wcByID := make(map[uuid.UUID]domain.WorkCenter, len(wcs))
	for _, wc := range wcs {
		wcByID[wc.ID] = wc
	}

	calSvc := &CalendarService{r: s.repos.Calendars}
	defaultSnap, _ := calSvc.LoadDefaultSnapshot(ctx, startDay, endDay.AddDate(0, 0, 1))
	calByWC := make(map[uuid.UUID]*CalendarSnapshot, len(wcs))
	for _, wc := range wcs {
		if wc.CalendarID != nil {
			if snap, e := calSvc.LoadSnapshot(ctx, *wc.CalendarID, startDay, endDay.AddDate(0, 0, 1)); e == nil && snap != nil {
				calByWC[wc.ID] = snap
				continue
			}
		}
		calByWC[wc.ID] = defaultSnap
	}
	alloc := &finiteAllocator{start: startDay, end: endDay, workCenter: wcByID, calendars: calByWC, days: map[string]*capacityDay{}}

	// Firm released/in-progress WOs reserve capacity first.
	workOrders, err := s.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	firmTasks := make([]scheduleTask, 0)
	for _, wo := range workOrders {
		if wo.Status != "RELEASED" && wo.Status != "IN_PROGRESS" {
			continue
		}
		ops, e := s.repos.ShopFloor.ListByWO(ctx, wo.ID)
		if e != nil {
			return nil, e
		}
		taskOps := make([]scheduleTaskOperation, 0, len(ops))
		for _, op := range ops {
			if op.Status == "COMPLETED" {
				continue
			}
			remainingQty := math.Max(wo.Quantity-op.CompletedQty, 0)
			setup := op.PlannedSetupMin
			if op.Status == "IN_PROGRESS" || op.Status == "PAUSED" || op.StartedAt != nil {
				setup = 0
			}
			load := setup + op.PlannedRunPerUnit*remainingQty
			if load <= 1e-9 {
				continue
			}
			taskOps = append(taskOps, scheduleTaskOperation{seq: op.SeqNo, description: op.Description, wcID: op.WorkCenterID, loadMinutes: load})
		}
		if len(taskOps) == 0 {
			continue
		}
		it := itemByID[wo.ItemID]
		wid := wo.ID
		priority := 20
		if wo.Status == "IN_PROGRESS" {
			priority = 10
		}
		firmTasks = append(firmTasks, scheduleTask{
			sourceType: "FIRM_WO", sourceRef: wo.OrderNo, workOrderID: &wid,
			itemID: wo.ItemID, itemCode: it.Code, quantity: math.Max(wo.Quantity-wo.CompletedQty, 0),
			priority: priority, earliest: maxTime(startDay, TruncateDay(wo.StartDate)),
			dueAt: businessDueEnd(wo.DueDate), firm: true, operations: taskOps,
		})
	}
	sort.SliceStable(firmTasks, func(i, j int) bool {
		if firmTasks[i].priority != firmTasks[j].priority {
			return firmTasks[i].priority < firmTasks[j].priority
		}
		if !firmTasks[i].dueAt.Equal(firmTasks[j].dueAt) {
			return firmTasks[i].dueAt.Before(firmTasks[j].dueAt)
		}
		return firmTasks[i].sourceRef < firmTasks[j].sourceRef
	})

	mrpRows, err := s.mrp.Run(ctx, MRPRequest{HorizonDays: req.HorizonDays, StartDate: startDay})
	if err != nil {
		return nil, err
	}
	opsCache := map[uuid.UUID][]domain.RoutingOperation{}
	plannedTasks := make([]scheduleTask, 0)
	for _, row := range mrpRows {
		if row.PlannedOrder <= 1e-9 {
			continue
		}
		ops, ok := opsCache[row.ItemID]
		if !ok {
			ops, err = s.repos.Routings.OperationsForItem(ctx, row.ItemID)
			if err != nil {
				return nil, err
			}
			sort.Slice(ops, func(i, j int) bool { return ops[i].SeqNo < ops[j].SeqNo })
			opsCache[row.ItemID] = ops
		}
		if len(ops) == 0 {
			continue // purchased/non-routed items have no CRP load
		}
		taskOps := make([]scheduleTaskOperation, 0, len(ops))
		for _, op := range ops {
			load := op.SetupMinutes + op.RunMinutesPerUnit*row.PlannedOrder
			if load > 1e-9 {
				taskOps = append(taskOps, scheduleTaskOperation{seq: op.SeqNo, description: op.Description, wcID: op.WorkCenterID, loadMinutes: load})
			}
		}
		if len(taskOps) == 0 {
			continue
		}
		earliest := startDay
		if row.PlannedReleaseDate != nil {
			earliest = maxTime(startDay, TruncateDay(*row.PlannedReleaseDate))
		}
		ref := fmt.Sprintf("MRP:%s:%s", row.ItemCode, TruncateDay(row.Period).Format("2006-01-02"))
		plannedTasks = append(plannedTasks, scheduleTask{
			sourceType: "MRP_PLANNED", sourceRef: ref, itemID: row.ItemID, itemCode: row.ItemCode,
			quantity: row.PlannedOrder, priority: 100, earliest: earliest,
			dueAt: businessDueEnd(row.Period), firm: false, operations: taskOps,
		})
	}
	sort.SliceStable(plannedTasks, func(i, j int) bool {
		if !plannedTasks[i].dueAt.Equal(plannedTasks[j].dueAt) {
			return plannedTasks[i].dueAt.Before(plannedTasks[j].dueAt)
		}
		if plannedTasks[i].itemCode != plannedTasks[j].itemCode {
			return plannedTasks[i].itemCode < plannedTasks[j].itemCode
		}
		return plannedTasks[i].sourceRef < plannedTasks[j].sourceRef
	})

	run := domain.CRPScheduleRun{
		ID: uuid.New(), StartDate: startDay, EndDate: endDay, HorizonDays: req.HorizonDays,
		Mode: crpFiniteMode, Status: "BUILDING", GeneratedAt: time.Now(), GeneratedBy: actor.Username,
	}
	if actor.UserID != uuid.Nil {
		run.GeneratedByUserID = &actor.UserID
	}
	result := &domain.CRPFiniteScheduleResult{Run: run, Orders: []domain.CRPScheduleOrder{}, Segments: []domain.CRPScheduleSegment{}, Loads: []domain.CapacityLoadRow{}}
	allTasks := append(firmTasks, plannedTasks...)
	for _, task := range allTasks {
		order := domain.CRPScheduleOrder{
			ID: uuid.New(), RunID: run.ID, SourceType: task.sourceType, SourceRef: task.sourceRef,
			WorkOrderID: task.workOrderID, ItemID: task.itemID, ItemCode: task.itemCode, Quantity: task.quantity,
			Priority: task.priority, EarliestStart: task.earliest, DueAt: task.dueAt,
		}
		cursor := task.earliest
		var first, last *time.Time
		unscheduled := 0.0
		totalRequired := 0.0
		totalScheduled := 0.0
		segmentNo := 0
		for _, op := range task.operations {
			totalRequired += op.loadMinutes
			segs, remaining := alloc.allocateForward(op.wcID, cursor, op.loadMinutes)
			for _, seg := range segs {
				segmentNo++
				seg.ID = uuid.New()
				seg.RunID = run.ID
				seg.ScheduleOrderID = order.ID
				seg.SourceType = task.sourceType
				seg.SourceRef = task.sourceRef
				seg.ItemID = task.itemID
				seg.ItemCode = task.itemCode
				seg.OperationSeq = op.seq
				seg.OperationDesc = op.description
				seg.WorkCenterID = op.wcID
				if wc, ok := wcByID[op.wcID]; ok {
					seg.WorkCenterCode = wc.Code
					seg.WorkCenterName = wc.Name
				}
				seg.SegmentNo = segmentNo
				seg.Firm = task.firm
				result.Segments = append(result.Segments, seg)
				totalScheduled += seg.LoadMinutes
				st, en := seg.StartAt, seg.EndAt
				if first == nil || st.Before(*first) {
					first = &st
				}
				if last == nil || en.After(*last) {
					last = &en
				}
			}
			unscheduled += remaining
			if remaining > 1e-9 || len(segs) == 0 {
				// Routing precedence means successors cannot be scheduled when a predecessor is incomplete.
				for _, later := range task.operations {
					if later.seq > op.seq {
						unscheduled += later.loadMinutes
						totalRequired += later.loadMinutes
					}
				}
				break
			}
			cursor = segs[len(segs)-1].EndAt
		}
		order.RequiredMinutes = totalRequired
		order.ScheduledMinutes = totalScheduled
		order.UnscheduledMinutes = math.Max(unscheduled, 0)
		order.ScheduledStart = first
		order.ScheduledEnd = last
		if unscheduled > 1e-6 {
			if totalScheduled > 1e-6 {
				order.ScheduleStatus = "PARTIAL"
			} else {
				order.ScheduleStatus = "UNSCHEDULED"
			}
		} else if last != nil && last.After(task.dueAt) {
			order.ScheduleStatus = "LATE"
			order.TardyMinutes = last.Sub(task.dueAt).Minutes()
		} else {
			order.ScheduleStatus = "ON_TIME"
		}
		result.Orders = append(result.Orders, order)
	}

	// Build finite load rows from allocated segments. By construction these rows cannot exceed 100%.
	type loadKey struct {
		wc  uuid.UUID
		day time.Time
	}
	loads := map[loadKey]float64{}
	for _, seg := range result.Segments {
		loads[loadKey{wc: seg.WorkCenterID, day: TruncateDay(seg.StartAt)}] += seg.LoadMinutes
	}
	for k, used := range loads {
		st := alloc.dayState(k.wc, k.day)
		wc := wcByID[k.wc]
		avail := 0.0
		holiday := true
		if st != nil {
			avail = st.effectiveLoad
			holiday = st.clockMinutes <= 0
		}
		pct := 0.0
		if avail > 0 {
			pct = used / avail * 100
		}
		result.Loads = append(result.Loads, domain.CapacityLoadRow{
			WorkCenterID: k.wc, WorkCenterCode: wc.Code, WorkCenterName: wc.Name,
			Date: k.day, RequiredMinutes: used, AvailableMinutes: avail, LoadPct: pct, IsHoliday: holiday,
		})
	}
	sort.Slice(result.Loads, func(i, j int) bool {
		if result.Loads[i].Date.Equal(result.Loads[j].Date) {
			return result.Loads[i].WorkCenterCode < result.Loads[j].WorkCenterCode
		}
		return result.Loads[i].Date.Before(result.Loads[j].Date)
	})

	for _, o := range result.Orders {
		if o.SourceType == "FIRM_WO" {
			result.Summary.FirmOrders++
		} else {
			result.Summary.PlannedOrders++
		}
		if o.ScheduleStatus == "ON_TIME" || o.ScheduleStatus == "LATE" {
			result.Summary.ScheduledOrders++
		}
		if o.ScheduleStatus == "LATE" {
			result.Summary.LateOrders++
		}
		if o.ScheduleStatus == "PARTIAL" || o.ScheduleStatus == "UNSCHEDULED" {
			result.Summary.UnscheduledOrders++
		}
		result.Summary.TotalLoadMinutes += o.ScheduledMinutes
	}
	result.Summary.ScheduledSegments = len(result.Segments)

	if err := s.persistFiniteSchedule(ctx, result); err != nil {
		return nil, err
	}
	result.Run.Status = "COMPLETE"
	return result, nil
}

func (s *CRPService) persistFiniteSchedule(ctx context.Context, result *domain.CRPFiniteScheduleResult) error {
	if s.db == nil {
		return fmt.Errorf("CRP database handle is not configured")
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.NamedExecContext(ctx, `
INSERT INTO crp_schedule_runs
(id,start_date,end_date,horizon_days,mode,status,generated_at,generated_by_user_id,generated_by)
VALUES (:id,:start_date,:end_date,:horizon_days,:mode,:status,:generated_at,:generated_by_user_id,:generated_by)`, &result.Run); err != nil {
		return err
	}
	for i := range result.Orders {
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO crp_schedule_orders
(id,run_id,source_type,source_ref,work_order_id,item_id,item_code,quantity,priority,earliest_start,due_at,
 scheduled_start,scheduled_end,required_minutes,scheduled_minutes,unscheduled_minutes,tardy_minutes,schedule_status)
VALUES (:id,:run_id,:source_type,:source_ref,:work_order_id,:item_id,:item_code,:quantity,:priority,:earliest_start,:due_at,
 :scheduled_start,:scheduled_end,:required_minutes,:scheduled_minutes,:unscheduled_minutes,:tardy_minutes,:schedule_status)`, &result.Orders[i]); err != nil {
			return err
		}
	}
	for i := range result.Segments {
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO crp_schedule_segments
(id,run_id,schedule_order_id,source_type,source_ref,item_id,item_code,operation_seq,operation_desc,
 work_center_id,work_center_code,work_center_name,segment_no,start_at,end_at,load_minutes,clock_minutes,effective_load_rate,firm)
VALUES (:id,:run_id,:schedule_order_id,:source_type,:source_ref,:item_id,:item_code,:operation_seq,:operation_desc,
 :work_center_id,:work_center_code,:work_center_name,:segment_no,:start_at,:end_at,:load_minutes,:clock_minutes,:effective_load_rate,:firm)`, &result.Segments[i]); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE crp_schedule_runs SET status='COMPLETE' WHERE id=$1 AND status='BUILDING'`, result.Run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *CRPService) ListFiniteRuns(ctx context.Context) ([]domain.CRPScheduleRun, error) {
	var rows []domain.CRPScheduleRun
	if err := s.db.SelectContext(ctx, &rows, `SELECT * FROM crp_schedule_runs WHERE status='COMPLETE' ORDER BY generated_at DESC LIMIT 50`); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *CRPService) GetFiniteRun(ctx context.Context, id uuid.UUID) (*domain.CRPFiniteScheduleResult, error) {
	var run domain.CRPScheduleRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM crp_schedule_runs WHERE id=$1 AND status='COMPLETE'`, id); err != nil {
		return nil, err
	}
	var orders []domain.CRPScheduleOrder
	if err := s.db.SelectContext(ctx, &orders, `SELECT * FROM crp_schedule_orders WHERE run_id=$1 ORDER BY priority,due_at,source_ref`, id); err != nil {
		return nil, err
	}
	var segs []domain.CRPScheduleSegment
	if err := s.db.SelectContext(ctx, &segs, `SELECT * FROM crp_schedule_segments WHERE run_id=$1 ORDER BY start_at,work_center_code,segment_no`, id); err != nil {
		return nil, err
	}
	res := &domain.CRPFiniteScheduleResult{Run: run, Orders: orders, Segments: segs}
	// Reconstruct summary and load profile from immutable snapshot.
	type lk struct {
		wc  uuid.UUID
		day time.Time
	}
	loadMap := map[lk]float64{}
	for _, o := range orders {
		if o.SourceType == "FIRM_WO" {
			res.Summary.FirmOrders++
		} else {
			res.Summary.PlannedOrders++
		}
		if o.ScheduleStatus == "ON_TIME" || o.ScheduleStatus == "LATE" {
			res.Summary.ScheduledOrders++
		}
		if o.ScheduleStatus == "LATE" {
			res.Summary.LateOrders++
		}
		if o.ScheduleStatus == "PARTIAL" || o.ScheduleStatus == "UNSCHEDULED" {
			res.Summary.UnscheduledOrders++
		}
		res.Summary.TotalLoadMinutes += o.ScheduledMinutes
	}
	res.Summary.ScheduledSegments = len(segs)
	for _, seg := range segs {
		loadMap[lk{seg.WorkCenterID, TruncateDay(seg.StartAt)}] += seg.LoadMinutes
	}
	wcs, _ := s.repos.WorkCenters.List(ctx)
	wcByID := map[uuid.UUID]domain.WorkCenter{}
	for _, w := range wcs {
		wcByID[w.ID] = w
	}
	calSvc := &CalendarService{r: s.repos.Calendars}
	defaultSnap, _ := calSvc.LoadDefaultSnapshot(ctx, run.StartDate, run.EndDate.AddDate(0, 0, 1))
	calByWC := map[uuid.UUID]*CalendarSnapshot{}
	for _, w := range wcs {
		if w.CalendarID != nil {
			if snap, e := calSvc.LoadSnapshot(ctx, *w.CalendarID, run.StartDate, run.EndDate.AddDate(0, 0, 1)); e == nil {
				calByWC[w.ID] = snap
				continue
			}
		}
		calByWC[w.ID] = defaultSnap
	}
	allocator := &finiteAllocator{start: run.StartDate, end: run.EndDate, workCenter: wcByID, calendars: calByWC, days: map[string]*capacityDay{}}
	for k, used := range loadMap {
		st := allocator.dayState(k.wc, k.day)
		wc := wcByID[k.wc]
		avail := 0.0
		if st != nil {
			avail = st.effectiveLoad
		}
		pct := 0.0
		if avail > 0 {
			pct = used / avail * 100
		}
		res.Loads = append(res.Loads, domain.CapacityLoadRow{WorkCenterID: k.wc, WorkCenterCode: wc.Code, WorkCenterName: wc.Name, Date: k.day, RequiredMinutes: used, AvailableMinutes: avail, LoadPct: pct})
	}
	sort.Slice(res.Loads, func(i, j int) bool {
		if res.Loads[i].Date.Equal(res.Loads[j].Date) {
			return strings.Compare(res.Loads[i].WorkCenterCode, res.Loads[j].WorkCenterCode) < 0
		}
		return res.Loads[i].Date.Before(res.Loads[j].Date)
	})
	return res, nil
}

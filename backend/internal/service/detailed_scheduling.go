package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

const detailedScheduleMode = "DETAILED_HEURISTIC"

type DetailedScheduleRequest struct {
	HorizonDays      int
	StartDate        time.Time
	NotBefore        time.Time
	SimulateMRP      bool
	CandidateOnly    bool
	ActivationReason string
}

type detailedAlternative struct {
	wcID      uuid.UUID
	priority  int
	runMult   float64
	setupMult float64
	primary   bool
}

type detailedOperationSpec struct {
	id                uuid.UUID
	seq               int
	description       string
	primaryWC         uuid.UUID
	baseSetupMinutes  float64
	runMinutesPerUnit float64
	setupFamily       string
	overlapEnabled    bool
	transferBatchQty  float64
	machinesRequired  int
	workersRequired   int
	quantity          float64
	completedBase     float64
	started           bool
	alternatives      []detailedAlternative
}

type detailedOrderTask struct {
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
	operations  []detailedOperationSpec
}

type machineLaneState struct {
	lane        int
	availableAt time.Time
	lastFamily  string
}

type laborReservation struct {
	start   time.Time
	end     time.Time
	workers int
}

type machineReservation struct {
	start    time.Time
	end      time.Time
	machines int
}

type maintenanceBlock struct {
	event domain.CurrentMaintenanceEvent
}

type detailedWCState struct {
	wc          domain.WorkCenter
	calendar    *CalendarSnapshot
	end         time.Time
	lanes       []*machineLaneState
	labor       []laborReservation
	machine     []machineReservation
	maintenance []maintenanceBlock
}

type clockFragment struct{ start, end time.Time }

type detailedCandidatePlan struct {
	alt         detailedAlternative
	wc          domain.WorkCenter
	lanes       []int
	fromFamily  string
	setupMin    float64
	runClockMin float64
	setup       []clockFragment
	run         []clockFragment
	start       time.Time
	end         time.Time
}

type setupKey struct {
	wc       uuid.UUID
	from, to string
}

type scheduledBatchInfo struct {
	batch domain.DetailedScheduleBatch
}

func (s *CRPService) DetailedSchedule(ctx context.Context, req DetailedScheduleRequest, actor CRPActor) (*domain.DetailedScheduleResult, error) {
	if req.StartDate.IsZero() {
		req.StartDate = time.Now()
	}
	if req.HorizonDays <= 0 {
		req.HorizonDays = 28
	}
	if req.HorizonDays > 366 {
		return nil, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	start := TruncateDay(req.StartDate)
	end := start.AddDate(0, 0, req.HorizonDays-1)

	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	itemByID := map[uuid.UUID]domain.Item{}
	for _, it := range items {
		itemByID[it.ID] = it
	}
	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	wcs, feedbackSnapshots, err := ApplyCapacityFeedbackToWorkCenters(ctx, s.db, wcs, start)
	if err != nil {
		return nil, err
	}
	wcByID := map[uuid.UUID]domain.WorkCenter{}
	for _, wc := range wcs {
		if wc.MachineCount <= 0 {
			wc.MachineCount = 1
		}
		if wc.WorkerCount < 0 {
			wc.WorkerCount = 0
		}
		wcByID[wc.ID] = wc
	}

	calSvc := &CalendarService{r: s.repos.Calendars}
	defaultSnap, _ := calSvc.LoadDefaultSnapshot(ctx, start, end.AddDate(0, 0, 1))
	states := map[uuid.UUID]*detailedWCState{}
	for _, wc := range wcs {
		var snap *CalendarSnapshot
		if wc.CalendarID != nil {
			snap, _ = calSvc.LoadSnapshot(ctx, *wc.CalendarID, start, end.AddDate(0, 0, 1))
		}
		if snap == nil {
			snap = defaultSnap
		}
		st := &detailedWCState{wc: wc, calendar: snap, end: end.Add(24*time.Hour - time.Nanosecond)}
		for lane := 1; lane <= wc.MachineCount; lane++ {
			st.lanes = append(st.lanes, &machineLaneState{lane: lane, availableAt: start})
		}
		states[wc.ID] = st
	}
	maintenanceEvents := []domain.CurrentMaintenanceEvent{}
	if s.maintenance != nil {
		maintenanceEvents, err = s.maintenance.CapacityEvents(ctx, start, end.AddDate(0, 0, 1))
		if err != nil {
			return nil, err
		}
		for _, ev := range maintenanceEvents {
			if st := states[ev.WorkCenterID]; st != nil {
				st.maintenance = append(st.maintenance, maintenanceBlock{event: ev})
			}
		}
	}

	setupMatrix, err := s.loadSetupMatrix(ctx)
	if err != nil {
		return nil, err
	}

	tasks, err := s.buildDetailedTasks(ctx, start, req.HorizonDays, itemByID, req.SimulateMRP)
	if err != nil {
		return nil, err
	}
	if !req.NotBefore.IsZero() {
		for i := range tasks {
			tasks[i].earliest = maxTime(tasks[i].earliest, req.NotBefore)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].priority != tasks[j].priority {
			return tasks[i].priority < tasks[j].priority
		}
		if !tasks[i].dueAt.Equal(tasks[j].dueAt) {
			return tasks[i].dueAt.Before(tasks[j].dueAt)
		}
		return tasks[i].sourceRef < tasks[j].sourceRef
	})

	run := domain.DetailedScheduleRun{ID: uuid.New(), StartDate: start, EndDate: end, HorizonDays: req.HorizonDays, Mode: detailedScheduleMode, Status: "BUILDING", GeneratedAt: time.Now(), GeneratedBy: actor.Username}
	if actor.UserID != uuid.Nil {
		run.GeneratedByUserID = &actor.UserID
	}
	res := &domain.DetailedScheduleResult{Run: run, Orders: []domain.DetailedScheduleOrder{}, Batches: []domain.DetailedScheduleBatch{}, Dependencies: []domain.DetailedScheduleDependency{}, Segments: []domain.DetailedScheduleSegment{}, Loads: []domain.CapacityLoadRow{}, Maintenance: []domain.DetailedScheduleMaintenanceSnapshot{}, CapacityFeedback: []domain.DetailedScheduleCapacityFeedbackSnapshot{}}
	for _, f := range feedbackSnapshots {
		f.RunID = run.ID
		res.CapacityFeedback = append(res.CapacityFeedback, f)
	}
	for _, ev := range maintenanceEvents {
		res.Maintenance = append(res.Maintenance, domain.DetailedScheduleMaintenanceSnapshot{RunID: run.ID, MaintenanceEventID: ev.ID, RevisionID: ev.RevisionID, RevisionNo: ev.RevisionNo, WorkCenterID: ev.WorkCenterID, EventType: ev.EventType, Status: ev.Status, StartAt: ev.StartAt, EndAt: ev.EndAt, UnavailableMachines: ev.UnavailableMachines, UnavailableWorkers: ev.UnavailableWorkers, Reason: ev.Reason, SourceRef: ev.SourceRef})
	}

	for _, task := range tasks {
		order := domain.DetailedScheduleOrder{ID: uuid.New(), RunID: run.ID, SourceType: task.sourceType, SourceRef: task.sourceRef, WorkOrderID: task.workOrderID, ItemID: task.itemID, ItemCode: task.itemCode, Quantity: task.quantity, Priority: task.priority, EarliestStart: task.earliest, DueAt: task.dueAt}
		var orderStart, orderEnd *time.Time
		orderUnscheduled := false
		var prevOp []scheduledBatchInfo
		for opIndex, op := range task.operations {
			op.setupFamily = normalizedSetupFamily(op.setupFamily, task.itemCode)
			var selectedAlt *detailedAlternative
			opQty := op.quantity
			if opQty <= 1e-9 {
				opQty = task.quantity
			}
			batchQtys := splitTransferBatches(opQty, op.overlapEnabled, op.transferBatchQty)
			curr := make([]scheduledBatchInfo, 0, len(batchQtys))
			cumulative := op.completedBase
			for bi, qty := range batchQtys {
				cumulative += qty
				b := domain.DetailedScheduleBatch{ID: uuid.New(), RunID: run.ID, ScheduleOrderID: order.ID, OperationSeq: op.seq, OperationDesc: op.description, BatchNo: bi + 1, BatchQty: qty, CumulativeQty: cumulative, SetupFamily: op.setupFamily, ScheduleStatus: "UNSCHEDULED", MachineCapacitySnapshot: maxInt(op.machinesRequired, 1), WorkerCapacitySnapshot: maxInt(op.workersRequired, 0), MachinesRequired: maxInt(op.machinesRequired, 1), WorkersRequired: maxInt(op.workersRequired, 0)}
				// Preserve the primary Work Center even when maintenance/capacity makes the
				// batch unschedulable. Pegging then has a causal capacity anchor instead
				// of an anonymous UNSCHEDULED batch. A successful alternative candidate
				// below replaces these fields with the actually selected Work Center.
				if primary := states[op.primaryWC]; primary != nil {
					wcID := primary.wc.ID
					b.WorkCenterID = &wcID
					b.WorkCenterCode = primary.wc.Code
					b.WorkCenterName = primary.wc.Name
					b.PrimaryWorkCenter = true
					b.MachineCapacitySnapshot = primary.wc.MachineCount
					b.WorkerCapacitySnapshot = primary.wc.WorkerCount
				}
				earliest := task.earliest
				deps := []domain.DetailedScheduleDependency{}
				if bi > 0 {
					pred := curr[bi-1].batch
					deps = append(deps, domain.DetailedScheduleDependency{BatchID: b.ID, PredecessorBatchID: pred.ID, DependencyType: "SAME_OPERATION"})
					if pred.ScheduledEnd == nil {
						orderUnscheduled = true
					} else {
						earliest = maxTime(earliest, *pred.ScheduledEnd)
					}
				}
				if opIndex > 0 {
					prevSpec := task.operations[opIndex-1]
					if cumulative > prevSpec.completedBase+1e-9 {
						if len(prevOp) == 0 {
							orderUnscheduled = true
						} else {
							pred := routingPredecessor(prevOp, cumulative, prevSpec.overlapEnabled)
							deps = append(deps, domain.DetailedScheduleDependency{BatchID: b.ID, PredecessorBatchID: pred.batch.ID, DependencyType: "ROUTING"})
							if pred.batch.ScheduledEnd == nil {
								orderUnscheduled = true
							} else {
								earliest = maxTime(earliest, *pred.batch.ScheduledEnd)
							}
						}
					}
				}
				res.Dependencies = append(res.Dependencies, deps...)
				if !orderUnscheduled {
					plan := s.bestDetailedCandidate(op, qty, earliest, states, setupMatrix, selectedAlt)
					if plan != nil {
						if selectedAlt == nil {
							chosen := plan.alt
							selectedAlt = &chosen
						}
						s.commitDetailedCandidate(states[plan.wc.ID], plan, run.ID, b.ID, order.ID, op.seq, bi+1, op.setupFamily, op.workersRequired, task.firm, &res.Segments)
						wcID := plan.wc.ID
						b.WorkCenterID = &wcID
						b.WorkCenterCode = plan.wc.Code
						b.WorkCenterName = plan.wc.Name
						b.PrimaryWorkCenter = plan.alt.primary
						b.AlternativePriority = plan.alt.priority
						b.MachineCapacitySnapshot = plan.wc.MachineCount
						b.WorkerCapacitySnapshot = plan.wc.WorkerCount
						b.MachinesRequired = op.machinesRequired
						b.WorkersRequired = op.workersRequired
						b.SequenceSetupMinutes = plan.setupMin
						b.RunClockMinutes = plan.runClockMin
						b.ScheduledStart = &plan.start
						b.ScheduledEnd = &plan.end
						b.ScheduleStatus = "SCHEDULED"
						b.MachineLanes = append([]int{}, plan.lanes...)
						if !plan.alt.primary {
							res.Summary.AlternativeUses++
						}
						if orderStart == nil || plan.start.Before(*orderStart) {
							t := plan.start
							orderStart = &t
						}
						if orderEnd == nil || plan.end.After(*orderEnd) {
							t := plan.end
							orderEnd = &t
						}
						res.Summary.SetupMinutes += plan.setupMin
						res.Summary.RunMinutes += plan.runClockMin
					} else {
						orderUnscheduled = true
					}
				}
				res.Batches = append(res.Batches, b)
				curr = append(curr, scheduledBatchInfo{batch: b})
			}
			if len(batchQtys) > 1 {
				res.Summary.TransferBatches += len(batchQtys)
			}
			prevOp = curr
		}
		order.ScheduledStart = orderStart
		order.ScheduledEnd = orderEnd
		if orderUnscheduled || orderEnd == nil {
			order.ScheduleStatus = "UNSCHEDULED"
			res.Summary.UnscheduledOrders++
		} else if orderEnd.After(task.dueAt) {
			order.ScheduleStatus = "LATE"
			order.TardyMinutes = orderEnd.Sub(task.dueAt).Minutes()
			res.Summary.LateOrders++
			res.Summary.ScheduledOrders++
		} else {
			order.ScheduleStatus = "ON_TIME"
			res.Summary.ScheduledOrders++
		}
		if task.firm {
			res.Summary.FirmOrders++
		} else {
			res.Summary.PlannedOrders++
		}
		res.Orders = append(res.Orders, order)
	}

	res.Loads = buildDetailedLoadRows(res.Segments, wcByID, states)
	res.Summary.PeakWorkers = peakDetailedWorkers(res.Segments)
	if err := s.persistDetailedSchedule(ctx, res, req.CandidateOnly, req.ActivationReason, actor); err != nil {
		return nil, err
	}
	res.Run.Status = "COMPLETE"
	return res, nil
}

// SimulateCTPOrder schedules one hypothetical customer-order quantity together
// with existing firm WOs and current MRP planned load. It deliberately does not
// call persistDetailedSchedule, so no schedule/WO/PO/inventory state is changed.
// The same alternative-WC, transfer-batch, setup, machine, labor and calendar
// allocator used by DetailedSchedule is reused here.
func (s *CRPService) SimulateCTPOrder(ctx context.Context, itemID uuid.UUID, qty float64, earliest, dueAt time.Time, horizonDays int) (*domain.DetailedScheduleOrder, error) {
	if itemID == uuid.Nil || qty <= 1e-9 {
		return nil, domain.NewBadRequest("itemId and positive quantity are required", nil)
	}
	if horizonDays <= 0 {
		horizonDays = 180
	}
	if horizonDays > 366 {
		return nil, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	start := TruncateDay(time.Now())
	if earliest.Before(start) {
		earliest = start
	}
	end := start.AddDate(0, 0, horizonDays-1)

	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	itemByID := map[uuid.UUID]domain.Item{}
	for _, it := range items {
		itemByID[it.ID] = it
	}
	it, ok := itemByID[itemID]
	if !ok {
		return nil, domain.NewNotFound("item")
	}

	wcs, err := s.repos.WorkCenters.List(ctx)
	if err != nil {
		return nil, err
	}
	wcs, _, err = ApplyCapacityFeedbackToWorkCenters(ctx, s.db, wcs, start)
	if err != nil {
		return nil, err
	}
	states := map[uuid.UUID]*detailedWCState{}
	calSvc := &CalendarService{r: s.repos.Calendars}
	defaultSnap, _ := calSvc.LoadDefaultSnapshot(ctx, start, end.AddDate(0, 0, 1))
	for _, wc := range wcs {
		if wc.MachineCount <= 0 {
			wc.MachineCount = 1
		}
		if wc.WorkerCount < 0 {
			wc.WorkerCount = 0
		}
		var snap *CalendarSnapshot
		if wc.CalendarID != nil {
			snap, _ = calSvc.LoadSnapshot(ctx, *wc.CalendarID, start, end.AddDate(0, 0, 1))
		}
		if snap == nil {
			snap = defaultSnap
		}
		st := &detailedWCState{wc: wc, calendar: snap, end: end.Add(24*time.Hour - time.Nanosecond)}
		for lane := 1; lane <= wc.MachineCount; lane++ {
			st.lanes = append(st.lanes, &machineLaneState{lane: lane, availableAt: start})
		}
		states[wc.ID] = st
	}
	if s.maintenance != nil {
		maintenanceEvents, e := s.maintenance.CapacityEvents(ctx, start, end.AddDate(0, 0, 1))
		if e != nil {
			return nil, e
		}
		for _, ev := range maintenanceEvents {
			if st := states[ev.WorkCenterID]; st != nil {
				st.maintenance = append(st.maintenance, maintenanceBlock{event: ev})
			}
		}
	}

	setupMatrix, err := s.loadSetupMatrix(ctx)
	if err != nil {
		return nil, err
	}

	tasks, err := s.buildDetailedTasks(ctx, start, horizonDays, itemByID, true)
	if err != nil {
		return nil, err
	}
	ops, err := s.repos.Routings.OperationsForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, domain.NewConflict("no active routing exists for CTP item")
	}
	var specs []detailedOperationSpec
	for _, op := range ops {
		spec := detailedOperationSpec{id: op.ID, seq: op.SeqNo, description: op.Description, primaryWC: op.WorkCenterID, baseSetupMinutes: op.SetupMinutes, runMinutesPerUnit: op.RunMinutesPerUnit, setupFamily: op.SetupFamily, overlapEnabled: op.OverlapEnabled, transferBatchQty: op.TransferBatchQty, machinesRequired: maxInt(op.MachinesRequired, 1), workersRequired: maxInt(op.WorkersRequired, 0), quantity: qty}
		alts, e := s.repos.Routings.Alternatives(ctx, op.ID)
		if e != nil {
			return nil, e
		}
		for _, a := range alts {
			if a.IsActive {
				spec.alternatives = append(spec.alternatives, detailedAlternative{wcID: a.WorkCenterID, priority: a.Priority, runMult: a.RunTimeMultiplier, setupMult: a.SetupTimeMultiplier})
			}
		}
		specs = append(specs, spec)
	}
	ref := "CTP:" + uuid.NewString()
	tasks = append(tasks, detailedOrderTask{sourceType: "CTP_WHAT_IF", sourceRef: ref, itemID: itemID, itemCode: it.Code, quantity: qty, priority: 50, earliest: earliest, dueAt: businessDueEnd(dueAt), operations: specs})
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].priority != tasks[j].priority {
			return tasks[i].priority < tasks[j].priority
		}
		if !tasks[i].dueAt.Equal(tasks[j].dueAt) {
			return tasks[i].dueAt.Before(tasks[j].dueAt)
		}
		return tasks[i].sourceRef < tasks[j].sourceRef
	})

	for _, task := range tasks {
		order := domain.DetailedScheduleOrder{ID: uuid.New(), RunID: uuid.Nil, SourceType: task.sourceType, SourceRef: task.sourceRef, WorkOrderID: task.workOrderID, ItemID: task.itemID, ItemCode: task.itemCode, Quantity: task.quantity, Priority: task.priority, EarliestStart: task.earliest, DueAt: task.dueAt}
		var orderStart, orderEnd *time.Time
		orderUnscheduled := false
		var prevOp []scheduledBatchInfo
		for opIndex, op := range task.operations {
			op.setupFamily = normalizedSetupFamily(op.setupFamily, task.itemCode)
			var selectedAlt *detailedAlternative
			opQty := op.quantity
			if opQty <= 1e-9 {
				opQty = task.quantity
			}
			batchQtys := splitTransferBatches(opQty, op.overlapEnabled, op.transferBatchQty)
			curr := make([]scheduledBatchInfo, 0, len(batchQtys))
			cumulative := op.completedBase
			for bi, batchQty := range batchQtys {
				cumulative += batchQty
				b := domain.DetailedScheduleBatch{ID: uuid.New(), ScheduleOrderID: order.ID, OperationSeq: op.seq, BatchNo: bi + 1, BatchQty: batchQty, CumulativeQty: cumulative, SetupFamily: op.setupFamily, ScheduleStatus: "UNSCHEDULED"}
				if primary := states[op.primaryWC]; primary != nil {
					wcID := primary.wc.ID
					b.WorkCenterID = &wcID
					b.WorkCenterCode = primary.wc.Code
					b.WorkCenterName = primary.wc.Name
					b.PrimaryWorkCenter = true
					b.MachineCapacitySnapshot = primary.wc.MachineCount
					b.WorkerCapacitySnapshot = primary.wc.WorkerCount
					b.MachinesRequired = maxInt(op.machinesRequired, 1)
					b.WorkersRequired = maxInt(op.workersRequired, 0)
				}
				ready := task.earliest
				if bi > 0 {
					pred := curr[bi-1].batch
					if pred.ScheduledEnd == nil {
						orderUnscheduled = true
					} else {
						ready = maxTime(ready, *pred.ScheduledEnd)
					}
				}
				if opIndex > 0 {
					prevSpec := task.operations[opIndex-1]
					if cumulative > prevSpec.completedBase+1e-9 {
						if len(prevOp) == 0 {
							orderUnscheduled = true
						} else {
							pred := routingPredecessor(prevOp, cumulative, prevSpec.overlapEnabled)
							if pred.batch.ScheduledEnd == nil {
								orderUnscheduled = true
							} else {
								ready = maxTime(ready, *pred.batch.ScheduledEnd)
							}
						}
					}
				}
				if !orderUnscheduled {
					plan := s.bestDetailedCandidate(op, batchQty, ready, states, setupMatrix, selectedAlt)
					if plan == nil {
						orderUnscheduled = true
					} else {
						if selectedAlt == nil {
							chosen := plan.alt
							selectedAlt = &chosen
						}
						var sink []domain.DetailedScheduleSegment
						s.commitDetailedCandidate(states[plan.wc.ID], plan, uuid.Nil, b.ID, order.ID, op.seq, bi+1, op.setupFamily, op.workersRequired, task.firm, &sink)
						b.ScheduledStart = &plan.start
						b.ScheduledEnd = &plan.end
						b.CumulativeQty = cumulative
						b.ScheduleStatus = "SCHEDULED"
						if orderStart == nil || plan.start.Before(*orderStart) {
							t := plan.start
							orderStart = &t
						}
						if orderEnd == nil || plan.end.After(*orderEnd) {
							t := plan.end
							orderEnd = &t
						}
					}
				}
				curr = append(curr, scheduledBatchInfo{batch: b})
			}
			prevOp = curr
		}
		order.ScheduledStart = orderStart
		order.ScheduledEnd = orderEnd
		if orderUnscheduled || orderEnd == nil {
			order.ScheduleStatus = "UNSCHEDULED"
		} else if orderEnd.After(task.dueAt) {
			order.ScheduleStatus = "LATE"
			order.TardyMinutes = orderEnd.Sub(task.dueAt).Minutes()
		} else {
			order.ScheduleStatus = "ON_TIME"
		}
		if task.sourceRef == ref {
			return &order, nil
		}
	}
	return nil, domain.NewConflict("CTP hypothetical order was not scheduled")
}

func splitTransferBatches(qty float64, overlap bool, transfer float64) []float64 {
	if qty <= 0 {
		return nil
	}
	if !overlap || transfer <= 1e-9 || transfer >= qty-1e-9 {
		return []float64{qty}
	}
	out := []float64{}
	remaining := qty
	for remaining > 1e-9 {
		q := math.Min(transfer, remaining)
		out = append(out, q)
		remaining -= q
	}
	return out
}

func routingPredecessor(prev []scheduledBatchInfo, cumulative float64, overlap bool) scheduledBatchInfo {
	if len(prev) == 0 {
		return scheduledBatchInfo{}
	}
	if !overlap {
		return prev[len(prev)-1]
	}
	for _, p := range prev {
		if p.batch.CumulativeQty+1e-9 >= cumulative {
			return p
		}
	}
	return prev[len(prev)-1]
}

func normalizedSetupFamily(family, itemCode string) string {
	if strings.TrimSpace(family) != "" {
		return strings.TrimSpace(family)
	}
	return strings.TrimSpace(itemCode)
}

func (s *CRPService) bestDetailedCandidate(op detailedOperationSpec, qty float64, earliest time.Time, states map[uuid.UUID]*detailedWCState, matrix map[setupKey]float64, forced *detailedAlternative) *detailedCandidatePlan {
	alts := append([]detailedAlternative{{wcID: op.primaryWC, priority: 0, runMult: 1, setupMult: 1, primary: true}}, op.alternatives...)
	if forced != nil {
		alts = []detailedAlternative{*forced}
	} else if op.started {
		alts = alts[:1]
	}
	var best *detailedCandidatePlan
	for _, alt := range alts {
		st := states[alt.wcID]
		if st == nil {
			continue
		}
		if op.machinesRequired > st.wc.MachineCount || op.workersRequired > st.wc.WorkerCount {
			continue
		}
		plan := planDetailedOnWC(st, op, qty, earliest, alt, matrix)
		if plan == nil {
			continue
		}
		if best == nil || plan.end.Before(best.end) || (plan.end.Equal(best.end) && alt.priority < best.alt.priority) {
			best = plan
		}
	}
	return best
}

func planDetailedOnWC(st *detailedWCState, op detailedOperationSpec, qty float64, earliest time.Time, alt detailedAlternative, matrix map[setupKey]float64) *detailedCandidatePlan {
	lanes, from, setup, anchor := chooseDetailedLanes(st, op, earliest, alt, matrix)
	if len(lanes) != op.machinesRequired {
		return nil
	}
	if alt.runMult <= 0 {
		alt.runMult = 1
	}
	if alt.setupMult <= 0 {
		alt.setupMult = 1
	}
	setup *= alt.setupMult
	speed := st.wc.Efficiency * st.wc.Utilization
	if speed <= 1e-9 {
		return nil
	}
	runClock := op.runMinutesPerUnit * qty * alt.runMult / speed
	setupFrags, afterSetup, ok := st.planClock(anchor, setup, op.workersRequired, op.machinesRequired, nil)
	if !ok {
		return nil
	}
	runFrags, end, ok := st.planClock(afterSetup, runClock, op.workersRequired, op.machinesRequired, setupFrags)
	if !ok {
		return nil
	}
	start := anchor
	if len(setupFrags) > 0 {
		start = setupFrags[0].start
	} else if len(runFrags) > 0 {
		start = runFrags[0].start
	}
	return &detailedCandidatePlan{alt: alt, wc: st.wc, lanes: lanes, fromFamily: from, setupMin: setup, runClockMin: runClock, setup: setupFrags, run: runFrags, start: start, end: end}
}

func chooseDetailedLanes(st *detailedWCState, op detailedOperationSpec, earliest time.Time, alt detailedAlternative, matrix map[setupKey]float64) ([]int, string, float64, time.Time) {
	type scored struct {
		lane  *machineLaneState
		score time.Time
		setup float64
	}
	to := strings.TrimSpace(op.setupFamily)
	scoredL := []scored{}
	for _, l := range st.lanes {
		base := sequenceSetupMinutes(matrix, st.wc.ID, l.lastFamily, to, op.baseSetupMinutes)
		ready := maxTime(earliest, l.availableAt)
		scoredL = append(scoredL, scored{lane: l, score: ready.Add(time.Duration(base * alt.setupMult * float64(time.Minute))), setup: base})
	}
	sort.Slice(scoredL, func(i, j int) bool {
		if scoredL[i].score.Equal(scoredL[j].score) {
			return scoredL[i].lane.lane < scoredL[j].lane.lane
		}
		return scoredL[i].score.Before(scoredL[j].score)
	})
	if len(scoredL) < op.machinesRequired {
		return nil, "", 0, time.Time{}
	}
	chosen := scoredL[:op.machinesRequired]
	lanes := []int{}
	anchor := earliest
	setup := 0.0
	fams := map[string]bool{}
	for _, c := range chosen {
		lanes = append(lanes, c.lane.lane)
		if c.lane.availableAt.After(anchor) {
			anchor = c.lane.availableAt
		}
		if c.setup > setup {
			setup = c.setup
		}
		if c.lane.lastFamily != "" {
			fams[c.lane.lastFamily] = true
		}
	}
	ff := []string{}
	for f := range fams {
		ff = append(ff, f)
	}
	sort.Strings(ff)
	return lanes, strings.Join(ff, "|"), setup, anchor
}

func sequenceSetupMinutes(matrix map[setupKey]float64, wc uuid.UUID, from, to string, base float64) float64 {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if to != "" && from == to {
		return 0
	}
	keys := []setupKey{{wc, from, to}, {wc, "*", to}, {wc, from, "*"}, {wc, "*", "*"}}
	for _, k := range keys {
		if v, ok := matrix[k]; ok {
			return v
		}
	}
	return math.Max(base, 0)
}

func (st *detailedWCState) planClock(earliest time.Time, minutes float64, workers, machines int, extra []clockFragment) ([]clockFragment, time.Time, bool) {
	if minutes <= 1e-9 {
		return nil, earliest, true
	}
	remaining := minutes
	t := earliest
	out := []clockFragment{}
	for !t.After(st.end) && remaining > 1e-9 {
		ws, we, ok := st.workingWindow(t)
		if !ok {
			break
		}
		t = ws
		for t.Before(we) && remaining > 1e-9 {
			next := we
			usedWorkers, usedMachines := 0, 0
			for _, r := range st.labor {
				if !t.Before(r.start) && t.Before(r.end) {
					usedWorkers += r.workers
				}
				if r.start.After(t) && r.start.Before(next) {
					next = r.start
				}
				if r.end.After(t) && r.end.Before(next) {
					next = r.end
				}
			}
			for _, r := range st.machine {
				if !t.Before(r.start) && t.Before(r.end) {
					usedMachines += r.machines
				}
				if r.start.After(t) && r.start.Before(next) {
					next = r.start
				}
				if r.end.After(t) && r.end.Before(next) {
					next = r.end
				}
			}
			for _, b := range st.maintenance {
				r := b.event
				if !t.Before(r.StartAt) && t.Before(r.EndAt) {
					usedWorkers += r.UnavailableWorkers
					usedMachines += r.UnavailableMachines
				}
				if r.StartAt.After(t) && r.StartAt.Before(next) {
					next = r.StartAt
				}
				if r.EndAt.After(t) && r.EndAt.Before(next) {
					next = r.EndAt
				}
			}
			for _, r := range extra {
				if !t.Before(r.start) && t.Before(r.end) {
					usedWorkers += workers
					usedMachines += machines
				}
				if r.start.After(t) && r.start.Before(next) {
					next = r.start
				}
				if r.end.After(t) && r.end.Before(next) {
					next = r.end
				}
			}
			workerOK := workers == 0 || usedWorkers+workers <= st.wc.WorkerCount
			machineOK := machines == 0 || usedMachines+machines <= maxInt(st.wc.MachineCount, 1)
			if workerOK && machineOK {
				avail := next.Sub(t).Minutes()
				use := math.Min(remaining, avail)
				if use > 1e-9 {
					en := t.Add(time.Duration(use * float64(time.Minute)))
					out = append(out, clockFragment{t, en})
					remaining -= use
					t = en
					continue
				}
			}
			if !next.After(t) {
				next = t.Add(time.Minute)
			}
			t = next
		}
		if remaining > 1e-9 {
			t = TruncateDay(t).AddDate(0, 0, 1)
		}
	}
	if remaining > 1e-6 {
		return nil, time.Time{}, false
	}
	if len(out) == 0 {
		return nil, earliest, true
	}
	return out, out[len(out)-1].end, true
}

func (st *detailedWCState) workingWindow(t time.Time) (time.Time, time.Time, bool) {
	day := TruncateDay(t)
	for !day.After(TruncateDay(st.end)) {
		mins := st.wc.CapacityMinutesPerDay
		if mins <= 0 {
			mins = 480
		}
		if st.calendar != nil {
			mins = float64(st.calendar.MinutesAvailable(day))
		}
		if mins > 0 {
			shift := st.wc.ShiftStartMinute
			if shift < 0 || shift > 1439 {
				shift = 480
			}
			ws := day.Add(time.Duration(shift) * time.Minute)
			we := ws.Add(time.Duration(mins) * time.Minute)
			if t.Before(ws) {
				return ws, we, true
			}
			if t.Before(we) {
				return t, we, true
			}
		}
		day = day.AddDate(0, 0, 1)
		t = day
	}
	return time.Time{}, time.Time{}, false
}

func (s *CRPService) commitDetailedCandidate(st *detailedWCState, p *detailedCandidatePlan, runID, batchID, orderID uuid.UUID, opSeq, batchNo int, setupFamily string, workers int, firm bool, out *[]domain.DetailedScheduleSegment) {
	segNo := 0
	appendPhase := func(kind string, frags []clockFragment, from string) {
		for _, f := range frags {
			segNo++
			seg := domain.DetailedScheduleSegment{ID: uuid.New(), RunID: runID, BatchID: batchID, ScheduleOrderID: orderID, OperationSeq: opSeq, BatchNo: batchNo, SegmentNo: segNo, SegmentType: kind, WorkCenterID: p.wc.ID, WorkCenterCode: p.wc.Code, StartAt: f.start, EndAt: f.end, MachinesRequired: len(p.lanes), WorkersRequired: workers, MachineCapacitySnapshot: p.wc.MachineCount, WorkerCapacitySnapshot: p.wc.WorkerCount, SetupFamily: setupFamily, FromSetupFamily: from, ClockMinutes: f.end.Sub(f.start).Minutes(), Firm: firm, MachineLanes: append([]int{}, p.lanes...)}
			*out = append(*out, seg)
		}
	}
	appendPhase("SETUP", p.setup, p.fromFamily)
	appendPhase("RUN", p.run, p.fromFamily)
	for _, ln := range p.lanes {
		for _, l := range st.lanes {
			if l.lane == ln {
				l.availableAt = p.end
				l.lastFamily = setupFamily
				break
			}
		}
	}
	fragments := append(append([]clockFragment{}, p.setup...), p.run...)
	if workers > 0 {
		for _, f := range fragments {
			st.labor = append(st.labor, laborReservation{f.start, f.end, workers})
		}
	}
	if len(p.lanes) > 0 {
		for _, f := range fragments {
			st.machine = append(st.machine, machineReservation{f.start, f.end, len(p.lanes)})
		}
	}
}

func (s *CRPService) loadSetupMatrix(ctx context.Context) (map[setupKey]float64, error) {
	var rows []domain.WorkCenterSetupMatrixRow
	if err := s.db.SelectContext(ctx, &rows, `SELECT * FROM work_center_setup_matrix`); err != nil {
		return nil, err
	}
	m := map[setupKey]float64{}
	for _, r := range rows {
		m[setupKey{r.WorkCenterID, r.FromSetupFamily, r.ToSetupFamily}] = r.SetupMinutes
	}
	return m, nil
}

func (s *CRPService) buildDetailedTasks(ctx context.Context, start time.Time, horizon int, itemByID map[uuid.UUID]domain.Item, simulateMRP bool) ([]detailedOrderTask, error) {
	var tasks []detailedOrderTask
	wos, err := s.repos.WorkOrders.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, wo := range wos {
		if wo.Status != "RELEASED" && wo.Status != "IN_PROGRESS" {
			continue
		}
		ops, err := s.repos.ShopFloor.ListByWO(ctx, wo.ID)
		if err != nil {
			return nil, err
		}
		var specs []detailedOperationSpec
		for _, op := range ops {
			if op.Status == "COMPLETED" {
				continue
			}
			remaining := math.Max(wo.Quantity-op.CompletedQty, 0)
			if remaining <= 1e-9 {
				continue
			}
			spec := detailedOperationSpec{id: op.ID, seq: op.SeqNo, description: op.Description, primaryWC: op.WorkCenterID, baseSetupMinutes: op.PlannedSetupMin, runMinutesPerUnit: op.PlannedRunPerUnit, setupFamily: op.SetupFamily, overlapEnabled: op.OverlapEnabled, transferBatchQty: op.TransferBatchQty, machinesRequired: maxInt(op.MachinesRequired, 1), workersRequired: maxInt(op.WorkersRequired, 0), quantity: remaining, completedBase: op.CompletedQty, started: op.Status == "IN_PROGRESS" || op.Status == "PAUSED" || op.StartedAt != nil}
			var alts []struct {
				WorkCenterID uuid.UUID `db:"work_center_id"`
				Priority     int       `db:"priority"`
				RunMult      float64   `db:"run_time_multiplier"`
				SetupMult    float64   `db:"setup_time_multiplier"`
			}
			if err := s.db.SelectContext(ctx, &alts, `SELECT work_center_id,priority,run_time_multiplier,setup_time_multiplier FROM wo_operation_alternatives WHERE wo_operation_id=$1 ORDER BY priority`, op.ID); err != nil {
				return nil, err
			}
			for _, a := range alts {
				spec.alternatives = append(spec.alternatives, detailedAlternative{wcID: a.WorkCenterID, priority: a.Priority, runMult: a.RunMult, setupMult: a.SetupMult})
			}
			specs = append(specs, spec)
		}
		if len(specs) == 0 {
			continue
		}
		wid := wo.ID
		it := itemByID[wo.ItemID]
		pri := 20
		if wo.Status == "IN_PROGRESS" {
			pri = 10
		}
		tasks = append(tasks, detailedOrderTask{sourceType: "FIRM_WO", sourceRef: wo.OrderNo, workOrderID: &wid, itemID: wo.ItemID, itemCode: it.Code, quantity: wo.Quantity, priority: pri, earliest: maxTime(start, TruncateDay(wo.StartDate)), dueAt: businessDueEnd(wo.DueDate), firm: true, operations: specs})
	}
	var mrpRows []domain.MRPResult
	if simulateMRP {
		mrpRows, err = s.mrp.Simulate(ctx, MRPRequest{HorizonDays: horizon, StartDate: start})
	} else {
		mrpRows, err = s.mrp.Run(ctx, MRPRequest{HorizonDays: horizon, StartDate: start})
	}
	if err != nil {
		return nil, err
	}
	for _, row := range mrpRows {
		if row.PlannedOrder <= 1e-9 {
			continue
		}
		ops, err := s.repos.Routings.OperationsForItem(ctx, row.ItemID)
		if err != nil {
			return nil, err
		}
		if len(ops) == 0 {
			continue
		}
		var specs []detailedOperationSpec
		for _, op := range ops {
			spec := detailedOperationSpec{id: op.ID, seq: op.SeqNo, description: op.Description, primaryWC: op.WorkCenterID, baseSetupMinutes: op.SetupMinutes, runMinutesPerUnit: op.RunMinutesPerUnit, setupFamily: op.SetupFamily, overlapEnabled: op.OverlapEnabled, transferBatchQty: op.TransferBatchQty, machinesRequired: maxInt(op.MachinesRequired, 1), workersRequired: maxInt(op.WorkersRequired, 0), quantity: row.PlannedOrder}
			alts, err := s.repos.Routings.Alternatives(ctx, op.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range alts {
				if a.IsActive {
					spec.alternatives = append(spec.alternatives, detailedAlternative{wcID: a.WorkCenterID, priority: a.Priority, runMult: a.RunTimeMultiplier, setupMult: a.SetupTimeMultiplier})
				}
			}
			specs = append(specs, spec)
		}
		earliest := start
		if row.PlannedReleaseDate != nil {
			earliest = maxTime(start, TruncateDay(*row.PlannedReleaseDate))
		}
		ref := fmt.Sprintf("MRP:%s:%s", row.ItemCode, TruncateDay(row.Period).Format("2006-01-02"))
		tasks = append(tasks, detailedOrderTask{sourceType: "MRP_PLANNED", sourceRef: ref, itemID: row.ItemID, itemCode: row.ItemCode, quantity: row.PlannedOrder, priority: 100, earliest: earliest, dueAt: businessDueEnd(row.Period), operations: specs})
	}
	return tasks, nil
}

func maxInt(v, d int) int {
	if v < d {
		return d
	}
	return v
}

func buildDetailedLoadRows(segs []domain.DetailedScheduleSegment, wcs map[uuid.UUID]domain.WorkCenter, states map[uuid.UUID]*detailedWCState) []domain.CapacityLoadRow {
	type k struct {
		wc  uuid.UUID
		day time.Time
	}
	used := map[k]float64{}
	for _, s := range segs {
		used[k{s.WorkCenterID, TruncateDay(s.StartAt)}] += s.ClockMinutes * float64(s.MachinesRequired)
	}
	var out []domain.CapacityLoadRow
	for key, u := range used {
		wc := wcs[key.wc]
		mins := wc.CapacityMinutesPerDay
		if mins <= 0 {
			mins = 480
		}
		if st := states[key.wc]; st != nil && st.calendar != nil {
			mins = float64(st.calendar.MinutesAvailable(key.day))
		}
		avail := mins * float64(maxInt(wc.MachineCount, 1))
		if st := states[key.wc]; st != nil {
			avail = maintenanceAdjustedMachineMinutes(st, key.day, mins)
		}
		pct := 0.0
		if avail > 0 {
			pct = u / avail * 100
		}
		out = append(out, domain.CapacityLoadRow{WorkCenterID: key.wc, WorkCenterCode: wc.Code, WorkCenterName: wc.Name, Date: key.day, RequiredMinutes: u, AvailableMinutes: avail, LoadPct: pct, IsHoliday: avail <= 0})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].WorkCenterCode < out[j].WorkCenterCode
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out
}

func maintenanceAdjustedMachineMinutes(st *detailedWCState, day time.Time, baseMinutes float64) float64 {
	machines := maxInt(st.wc.MachineCount, 1)
	if baseMinutes <= 0 {
		return 0
	}
	shift := st.wc.ShiftStartMinute
	if shift < 0 || shift > 1439 {
		shift = 480
	}
	ws := TruncateDay(day).Add(time.Duration(shift) * time.Minute)
	we := ws.Add(time.Duration(baseMinutes) * time.Minute)
	points := []time.Time{ws, we}
	for _, b := range st.maintenance {
		ev := b.event
		if !ev.EndAt.After(ws) || !ev.StartAt.Before(we) {
			continue
		}
		if ev.StartAt.After(ws) && ev.StartAt.Before(we) {
			points = append(points, ev.StartAt)
		}
		if ev.EndAt.After(ws) && ev.EndAt.Before(we) {
			points = append(points, ev.EndAt)
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	avail := 0.0
	for i := 0; i+1 < len(points); i++ {
		a, b := points[i], points[i+1]
		if !b.After(a) {
			continue
		}
		down := 0
		for _, mb := range st.maintenance {
			ev := mb.event
			if !a.Before(ev.StartAt) && a.Before(ev.EndAt) {
				down += ev.UnavailableMachines
			}
		}
		up := machines - minInt(down, machines)
		avail += b.Sub(a).Minutes() * float64(up)
	}
	return math.Max(avail, 0)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func peakDetailedWorkers(segs []domain.DetailedScheduleSegment) int {
	peak := 0
	for _, p := range segs {
		used := 0
		for _, q := range segs {
			if q.WorkCenterID == p.WorkCenterID && !p.StartAt.Before(q.StartAt) && p.StartAt.Before(q.EndAt) {
				used += q.WorkersRequired
			}
		}
		if used > peak {
			peak = used
		}
	}
	return peak
}

func (s *CRPService) persistDetailedSchedule(ctx context.Context, res *domain.DetailedScheduleResult, candidateOnly bool, activationReason string, actor CRPActor) error {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_runs(id,start_date,end_date,horizon_days,mode,status,generated_at,generated_by_user_id,generated_by) VALUES (:id,:start_date,:end_date,:horizon_days,:mode,:status,:generated_at,:generated_by_user_id,:generated_by)`, &res.Run); err != nil {
		return err
	}
	for i := range res.Orders {
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_orders(id,run_id,source_type,source_ref,work_order_id,item_id,item_code,quantity,priority,earliest_start,due_at,scheduled_start,scheduled_end,schedule_status,tardy_minutes) VALUES (:id,:run_id,:source_type,:source_ref,:work_order_id,:item_id,:item_code,:quantity,:priority,:earliest_start,:due_at,:scheduled_start,:scheduled_end,:schedule_status,:tardy_minutes)`, &res.Orders[i]); err != nil {
			return err
		}
	}
	for i := range res.Batches {
		b := &res.Batches[i]
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_batches(id,run_id,schedule_order_id,operation_seq,operation_desc,batch_no,batch_qty,cumulative_qty,setup_family,work_center_id,work_center_code,work_center_name,primary_work_center,alternative_priority,machine_capacity_snapshot,worker_capacity_snapshot,machines_required,workers_required,sequence_setup_minutes,run_clock_minutes,scheduled_start,scheduled_end,schedule_status) VALUES (:id,:run_id,:schedule_order_id,:operation_seq,:operation_desc,:batch_no,:batch_qty,:cumulative_qty,:setup_family,:work_center_id,:work_center_code,:work_center_name,:primary_work_center,:alternative_priority,:machine_capacity_snapshot,:worker_capacity_snapshot,:machines_required,:workers_required,:sequence_setup_minutes,:run_clock_minutes,:scheduled_start,:scheduled_end,:schedule_status)`, b); err != nil {
			return err
		}
	}
	for _, d := range res.Dependencies {
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_batch_dependencies(batch_id,predecessor_batch_id,dependency_type) VALUES (:batch_id,:predecessor_batch_id,:dependency_type)`, &d); err != nil {
			return err
		}
	}
	for i := range res.Segments {
		sg := &res.Segments[i]
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_segments(id,run_id,batch_id,schedule_order_id,operation_seq,batch_no,segment_no,segment_type,work_center_id,start_at,end_at,machines_required,workers_required,machine_capacity_snapshot,worker_capacity_snapshot,setup_family,from_setup_family,clock_minutes,firm) VALUES (:id,:run_id,:batch_id,:schedule_order_id,:operation_seq,:batch_no,:segment_no,:segment_type,:work_center_id,:start_at,:end_at,:machines_required,:workers_required,:machine_capacity_snapshot,:worker_capacity_snapshot,:setup_family,:from_setup_family,:clock_minutes,:firm)`, sg); err != nil {
			return err
		}
		for _, lane := range sg.MachineLanes {
			if _, err = tx.ExecContext(ctx, `INSERT INTO detailed_schedule_machine_allocations(segment_id,run_id,work_center_id,lane_no,start_at,end_at) VALUES ($1,$2,$3,$4,$5,$6)`, sg.ID, res.Run.ID, sg.WorkCenterID, lane, sg.StartAt, sg.EndAt); err != nil {
				return err
			}
		}
	}
	for _, load := range res.Loads {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO detailed_schedule_loads
			(run_id,work_center_id,work_center_code,work_center_name,load_date,required_minutes,available_minutes,load_pct,is_holiday)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			res.Run.ID, load.WorkCenterID, load.WorkCenterCode, load.WorkCenterName, TruncateDay(load.Date),
			load.RequiredMinutes, load.AvailableMinutes, load.LoadPct, load.IsHoliday); err != nil {
			return err
		}
	}
	for i := range res.Maintenance {
		m := &res.Maintenance[i]
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_maintenance_snapshots(run_id,maintenance_event_id,revision_id,revision_no,work_center_id,event_type,status,start_at,end_at,unavailable_machines,unavailable_workers,reason,source_ref) VALUES (:run_id,:maintenance_event_id,:revision_id,:revision_no,:work_center_id,:event_type,:status,:start_at,:end_at,:unavailable_machines,:unavailable_workers,:reason,:source_ref)`, m); err != nil {
			return err
		}
	}
	for i := range res.CapacityFeedback {
		f := &res.CapacityFeedback[i]
		if _, err = tx.NamedExecContext(ctx, `INSERT INTO detailed_schedule_capacity_feedback_snapshots(run_id,feedback_version_id,work_center_id,version_no,source_run_id,source_result_id,effective_efficiency,effective_utilization,source_oee,source_availability,source_performance,source_quality,sample_count,confidence,effective_from) VALUES (:run_id,:feedback_version_id,:work_center_id,:version_no,:source_run_id,:source_result_id,:effective_efficiency,:effective_utilization,:source_oee,:source_availability,:source_performance,:source_quality,:sample_count,:confidence,:effective_from)`, f); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE detailed_schedule_runs SET status='COMPLETE' WHERE id=$1`, res.Run.ID); err != nil {
		return err
	}
	if !candidateOnly {
		reason := strings.TrimSpace(activationReason)
		if reason == "" {
			reason = "MANUAL_DETAILED_SCHEDULE"
		}
		execActor := ScheduleExecutionActor{UserID: actor.UserID, Username: actor.Username, System: actor.UserID == uuid.Nil}
		if _, err := activateExecutionScheduleTx(ctx, tx, res.Run.ID, nil, nil, reason, execActor); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *CRPService) ListDetailedRuns(ctx context.Context) ([]domain.DetailedScheduleRun, error) {
	var rows []domain.DetailedScheduleRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM detailed_schedule_runs WHERE status='COMPLETE' ORDER BY generated_at DESC LIMIT 100`)
	return rows, err
}

func (s *CRPService) GetDetailedRun(ctx context.Context, id uuid.UUID) (*domain.DetailedScheduleResult, error) {
	var run domain.DetailedScheduleRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM detailed_schedule_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("detailed schedule run")
		}
		return nil, err
	}
	res := &domain.DetailedScheduleResult{Run: run}
	if err := s.db.SelectContext(ctx, &res.Orders, `SELECT * FROM detailed_schedule_orders WHERE run_id=$1 ORDER BY priority,due_at,source_ref`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.Batches, `SELECT * FROM detailed_schedule_batches WHERE run_id=$1 ORDER BY schedule_order_id,operation_seq,batch_no`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.Dependencies, `SELECT d.* FROM detailed_schedule_batch_dependencies d JOIN detailed_schedule_batches b ON b.id=d.batch_id WHERE b.run_id=$1`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.Segments, `SELECT s.* FROM detailed_schedule_segments s WHERE s.run_id=$1 ORDER BY start_at,work_center_id,batch_id,segment_no`, id); err != nil {
		return nil, err
	}
	for i := range res.Segments {
		var lanes []int
		if err := s.db.SelectContext(ctx, &lanes, `SELECT lane_no FROM detailed_schedule_machine_allocations WHERE segment_id=$1 ORDER BY lane_no`, res.Segments[i].ID); err != nil {
			return nil, err
		}
		res.Segments[i].MachineLanes = lanes
	}
	batchWCCode := map[uuid.UUID]string{}
	for _, b := range res.Batches {
		batchWCCode[b.ID] = b.WorkCenterCode
	}
	for i := range res.Segments {
		res.Segments[i].WorkCenterCode = batchWCCode[res.Segments[i].BatchID]
	}
	var loadRows []struct {
		WorkCenterID     uuid.UUID `db:"work_center_id"`
		WorkCenterCode   string    `db:"work_center_code"`
		WorkCenterName   string    `db:"work_center_name"`
		Date             time.Time `db:"load_date"`
		RequiredMinutes  float64   `db:"required_minutes"`
		AvailableMinutes float64   `db:"available_minutes"`
		LoadPct          float64   `db:"load_pct"`
		IsHoliday        bool      `db:"is_holiday"`
	}
	if err := s.db.SelectContext(ctx, &loadRows, `SELECT work_center_id,work_center_code,work_center_name,load_date,required_minutes,available_minutes,load_pct,is_holiday FROM detailed_schedule_loads WHERE run_id=$1 ORDER BY load_date,work_center_code`, id); err != nil {
		return nil, err
	}
	for _, x := range loadRows {
		res.Loads = append(res.Loads, domain.CapacityLoadRow{WorkCenterID: x.WorkCenterID, WorkCenterCode: x.WorkCenterCode, WorkCenterName: x.WorkCenterName, Date: x.Date, RequiredMinutes: x.RequiredMinutes, AvailableMinutes: x.AvailableMinutes, LoadPct: x.LoadPct, IsHoliday: x.IsHoliday})
	}
	if err := s.db.SelectContext(ctx, &res.Maintenance, `SELECT * FROM detailed_schedule_maintenance_snapshots WHERE run_id=$1 ORDER BY start_at,work_center_id,maintenance_event_id`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.CapacityFeedback, `SELECT * FROM detailed_schedule_capacity_feedback_snapshots WHERE run_id=$1 ORDER BY work_center_id,version_no`, id); err != nil {
		return nil, err
	}
	for _, o := range res.Orders {
		if o.SourceType == "FIRM_WO" {
			res.Summary.FirmOrders++
		} else {
			res.Summary.PlannedOrders++
		}
		switch o.ScheduleStatus {
		case "ON_TIME":
			res.Summary.ScheduledOrders++
		case "LATE":
			res.Summary.ScheduledOrders++
			res.Summary.LateOrders++
		default:
			res.Summary.UnscheduledOrders++
		}
	}
	for _, b := range res.Batches {
		if !b.PrimaryWorkCenter && b.ScheduleStatus == "SCHEDULED" {
			res.Summary.AlternativeUses++
		}
		if b.BatchNo > 1 {
			res.Summary.TransferBatches++
		}
		res.Summary.SetupMinutes += b.SequenceSetupMinutes
		res.Summary.RunMinutes += b.RunClockMinutes
	}
	res.Summary.PeakWorkers = peakDetailedWorkers(res.Segments)
	return res, nil
}

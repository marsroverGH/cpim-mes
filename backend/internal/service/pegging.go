package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const defaultPeggingHorizonDays = 180
const maxPeggingDepth = 12

type PeggingRunInput struct {
	HorizonDays int `json:"horizonDays"`
}

type ExceptionScanInput struct {
	HorizonDays int `json:"horizonDays"`
}

type ExceptionActionInput struct {
	ActionType string `json:"actionType"`
	Comment    string `json:"comment"`
}

type PeggingService struct {
	db *sqlx.DB
}

type peggingOrder struct {
	ID            uuid.UUID  `db:"id"`
	OrderNo       string     `db:"order_no"`
	CustomerID    uuid.UUID  `db:"customer_id"`
	CustomerNo    string     `db:"customer_no"`
	CustomerName  string     `db:"customer_name"`
	Status        string     `db:"status"`
	Priority      string     `db:"priority"`
	RequestedDate time.Time  `db:"requested_date"`
	PromisedDate  *time.Time `db:"promised_date"`
}

type peggingLine struct {
	ID            uuid.UUID  `db:"id"`
	SalesOrderID  uuid.UUID  `db:"sales_order_id"`
	LineNo        int        `db:"line_no"`
	ItemID        uuid.UUID  `db:"item_id"`
	ItemCode      string     `db:"item_code"`
	ItemName      string     `db:"item_name"`
	ItemType      string     `db:"item_type"`
	Quantity      float64    `db:"quantity"`
	AllocatedQty  float64    `db:"allocated_qty"`
	ShippedQty    float64    `db:"shipped_qty"`
	CancelledQty  float64    `db:"cancelled_qty"`
	OpenQty       float64    `db:"open_qty"`
	RequestedDate time.Time  `db:"requested_date"`
	PromisedDate  *time.Time `db:"promised_date"`
}

type peggingWorkOrder struct {
	ID           uuid.UUID `db:"id"`
	OrderNo      string    `db:"order_no"`
	ItemID       uuid.UUID `db:"item_id"`
	ItemCode     string    `db:"item_code"`
	ItemName     string    `db:"item_name"`
	ItemType     string    `db:"item_type"`
	Quantity     float64   `db:"quantity"`
	CompletedQty float64   `db:"completed_qty"`
	RemainingQty float64   `db:"remaining_qty"`
	StartDate    time.Time `db:"start_date"`
	DueDate      time.Time `db:"due_date"`
	Status       string    `db:"status"`
}

type peggingPO struct {
	ID                      uuid.UUID  `db:"id"`
	PONo                    string     `db:"po_no"`
	ItemID                  uuid.UUID  `db:"item_id"`
	ItemCode                string     `db:"item_code"`
	ItemName                string     `db:"item_name"`
	Supplier                string     `db:"supplier"`
	SupplierQualityStatus   string     `db:"supplier_quality_status"`
	Quantity                float64    `db:"quantity"`
	ReceivedQty             float64    `db:"received_qty"`
	RemainingQty            float64    `db:"remaining_qty"`
	DueDate                 time.Time  `db:"due_date"`
	Status                  string     `db:"status"`
	ScheduleStatus          string     `db:"schedule_status"`
	ConfirmationEventID     *uuid.UUID `db:"confirmation_event_id"`
	ConfirmedQuantity       *float64   `db:"confirmed_quantity"`
	ConfirmedDeliveryDate   *time.Time `db:"confirmed_delivery_date"`
	ASNEventID              *uuid.UUID `db:"asn_event_id"`
	ASNNo                   string     `db:"asn_no"`
	ASNQuantity             *float64   `db:"asn_quantity"`
	ASNExpectedArrivalDate  *time.Time `db:"asn_expected_arrival_date"`
	ExpectedDeliveryDate    time.Time  `db:"expected_delivery_date"`
	ScheduleSource          string     `db:"schedule_source"`
	ReliabilitySampleCount  int        `db:"reliability_sample_count"`
	ReliabilityOnTimeRate   float64    `db:"reliability_on_time_rate"`
	ReliabilityP90Days      float64    `db:"reliability_p90_days"`
	RecommendedLeadTimeDays int        `db:"recommended_lead_time_days"`
}

type stockSnapshot struct {
	ItemID              uuid.UUID  `db:"item_id"`
	OnHand              float64    `db:"on_hand"`
	Reserved            float64    `db:"reserved"`
	Usable              float64    `db:"usable"`
	Hold                float64    `db:"hold_qty"`
	Rejected            float64    `db:"rejected_qty"`
	GrossAvailable      float64    `db:"gross_available"`
	Available           float64    `db:"available"`
	SafetyStock         float64    `db:"safety_stock"`
	ReorderPoint        float64    `db:"reorder_point"`
	MinQty              float64    `db:"min_qty"`
	MaxQty              float64    `db:"max_qty"`
	ServiceLevel        float64    `db:"service_level"`
	PolicyVersionID     *uuid.UUID `db:"policy_version_id"`
	PolicyStatus        string     `db:"policy_status"`
	ReplenishmentMethod string     `db:"replenishment_method"`
}

type bomRequirement struct {
	ChildID  uuid.UUID `db:"child_id"`
	Code     string    `db:"code"`
	Name     string    `db:"name"`
	Type     string    `db:"type"`
	QtyPer   float64   `db:"qty_per"`
	ScrapPct float64   `db:"scrap_pct"`
	Required float64   `db:"required_qty"`
	FromSnap bool      `db:"from_snapshot"`
}

type detailedEvidence struct {
	ID             uuid.UUID  `db:"id"`
	RunID          uuid.UUID  `db:"run_id"`
	SourceType     string     `db:"source_type"`
	SourceRef      string     `db:"source_ref"`
	WorkOrderID    *uuid.UUID `db:"work_order_id"`
	ItemID         uuid.UUID  `db:"item_id"`
	ItemCode       string     `db:"item_code"`
	Quantity       float64    `db:"quantity"`
	DueAt          time.Time  `db:"due_at"`
	ScheduledStart *time.Time `db:"scheduled_start"`
	ScheduledEnd   *time.Time `db:"scheduled_end"`
	ScheduleStatus string     `db:"schedule_status"`
	TardyMinutes   float64    `db:"tardy_minutes"`
	GeneratedAt    time.Time  `db:"generated_at"`
}

type maintenanceEvidence struct {
	MaintenanceEventID  uuid.UUID `db:"maintenance_event_id"`
	RevisionID          uuid.UUID `db:"revision_id"`
	RevisionNo          int       `db:"revision_no"`
	WorkCenterID        uuid.UUID `db:"work_center_id"`
	EventType           string    `db:"event_type"`
	Status              string    `db:"status"`
	StartAt             time.Time `db:"start_at"`
	EndAt               time.Time `db:"end_at"`
	UnavailableMachines int       `db:"unavailable_machines"`
	UnavailableWorkers  int       `db:"unavailable_workers"`
	Reason              string    `db:"reason"`
	SourceRef           string    `db:"source_ref"`
}

type capacityFeedbackEvidence struct {
	FeedbackVersionID    uuid.UUID `db:"feedback_version_id"`
	WorkCenterID         uuid.UUID `db:"work_center_id"`
	VersionNo            int       `db:"version_no"`
	SourceRunID          uuid.UUID `db:"source_run_id"`
	SourceResultID       uuid.UUID `db:"source_result_id"`
	EffectiveEfficiency  float64   `db:"effective_efficiency"`
	EffectiveUtilization float64   `db:"effective_utilization"`
	SourceOEE            float64   `db:"source_oee"`
	SourceAvailability   float64   `db:"source_availability"`
	SourcePerformance    float64   `db:"source_performance"`
	SourceQuality        float64   `db:"source_quality"`
	SampleCount          int       `db:"sample_count"`
	Confidence           string    `db:"confidence"`
	EffectiveFrom        time.Time `db:"effective_from"`
}

type workCenterEvidence struct {
	ID              uuid.UUID  `db:"id"`
	Code            string     `db:"code"`
	Name            string     `db:"name"`
	BatchStatus     string     `db:"batch_status"`
	MachinesReq     int        `db:"machines_required"`
	WorkersReq      int        `db:"workers_required"`
	MachineCapacity int        `db:"machine_capacity_snapshot"`
	WorkerCapacity  int        `db:"worker_capacity_snapshot"`
	ScheduledEnd    *time.Time `db:"scheduled_end"`
}

type promiseEvidence struct {
	RunID      uuid.UUID `db:"run_id"`
	AcceptedAt time.Time `db:"accepted_at"`
	CTPQty     float64   `db:"ctp_qty"`
	ATPQty     float64   `db:"atp_qty"`
}

type bopEvidence struct {
	RunID               uuid.UUID  `db:"run_id"`
	PublishedAt         time.Time  `db:"published_at"`
	BackorderQty        float64    `db:"backorder_qty"`
	Decision            string     `db:"decision"`
	ConstraintType      string     `db:"constraint_type"`
	ServiceClassCode    string     `db:"service_class_code"`
	OrderPriority       string     `db:"order_priority"`
	AllocationPlanID    *uuid.UUID `db:"allocation_plan_id"`
	AllocationBucketPct *float64   `db:"allocation_bucket_pct"`
	ProposedDate        *time.Time `db:"proposed_promised_date"`
}

type graphBuilder struct {
	runID       uuid.UUID
	nodes       []domain.PeggingNode
	edges       []domain.PeggingEdge
	exceptions  []domain.PlanningException
	nodeByKey   map[string]uuid.UUID
	nodeKeyByID map[uuid.UUID]string
	excByKey    map[string]struct{}
}

func newGraphBuilder(runID uuid.UUID) *graphBuilder {
	return &graphBuilder{
		runID: runID, nodeByKey: map[string]uuid.UUID{}, nodeKeyByID: map[uuid.UUID]string{}, excByKey: map[string]struct{}{},
	}
}

func jsonDetail(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func floatPtr(v float64) *float64           { return &v }
func peggingTimePtr(v time.Time) *time.Time { return &v }

func (g *graphBuilder) node(key, typ, label string, entityID *uuid.UUID, entityRef string, itemID *uuid.UUID, itemCode string, qty *float64, due *time.Time, status string, detail any) uuid.UUID {
	if id, ok := g.nodeByKey[key]; ok {
		return id
	}
	id := uuid.New()
	g.nodeByKey[key] = id
	g.nodeKeyByID[id] = key
	g.nodes = append(g.nodes, domain.PeggingNode{
		ID: id, RunID: g.runID, NodeKey: key, NodeType: typ, EntityID: entityID, EntityRef: entityRef,
		ItemID: itemID, ItemCode: itemCode, Label: label, Quantity: qty, DueDate: due, Status: status, Detail: jsonDetail(detail),
	})
	return id
}

func (g *graphBuilder) edge(from, to uuid.UUID, typ string, qty *float64, detail any) {
	if from == uuid.Nil || to == uuid.Nil || from == to {
		return
	}
	for _, e := range g.edges {
		if e.FromNodeID == from && e.ToNodeID == to && e.EdgeType == typ {
			return
		}
	}
	g.edges = append(g.edges, domain.PeggingEdge{ID: uuid.New(), RunID: g.runID, FromNodeID: from, ToNodeID: to, EdgeType: typ, Quantity: qty, Detail: jsonDetail(detail)})
}

func (g *graphBuilder) exception(order peggingOrder, line *peggingLine, typ, severity string, root uuid.UUID, message string, requested, promised, impact *time.Time, impactDays int, path []string, detail any) {
	lineID := "header"
	var sol *uuid.UUID
	if line != nil {
		lineID = line.ID.String()
		id := line.ID
		sol = &id
	}
	rootKey := g.nodeKeyByID[root]
	key := typ + ":" + lineID + ":" + rootKey
	if _, ok := g.excByKey[key]; ok {
		return
	}
	g.excByKey[key] = struct{}{}
	g.exceptions = append(g.exceptions, domain.PlanningException{
		ID: uuid.New(), RunID: g.runID, SalesOrderID: order.ID, SalesOrderLineID: sol,
		ExceptionKey: key, ExceptionType: typ, Severity: severity, RootNodeID: root, Message: message,
		RequestedDate: requested, PromisedDate: promised, ImpactDate: impact, ImpactDays: maxInt(impactDays, 0),
		RootCausePath: jsonDetail(path), Detail: jsonDetail(detail), CurrentStatus: "OPEN",
	})
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func daysLate(actual, required time.Time) int {
	d := int(math.Ceil(dateOnly(actual).Sub(dateOnly(required)).Hours() / 24))
	if d < 0 {
		return 0
	}
	return d
}

func severityFor(typ string, impactDays int) string {
	switch typ {
	case "BACKORDER", "MATERIAL_SHORTAGE", "SUPPLIER_BLOCKED", "CAPACITY_UNSCHEDULED":
		return "CRITICAL"
	case "QUALITY_HOLD":
		if impactDays > 0 {
			return "CRITICAL"
		}
		return "WARNING"
	case "SUPPLIER_RELIABILITY_RISK", "REORDER_POINT_BREACH":
		return "WARNING"
	case "SAFETY_STOCK_BREACH":
		return "CRITICAL"
	case "LATE_PROMISE", "LATE_PURCHASE_ORDER", "LATE_WORK_ORDER", "CAPACITY_LATE", "UNCONVERTED_CTP", "SUPPLIER_CONFIRMATION_LATE":
		if impactDays >= 7 {
			return "CRITICAL"
		}
		return "WARNING"
	default:
		return "INFO"
	}
}

type supplyPools struct {
	inventory  map[uuid.UUID]float64
	stock      map[uuid.UUID]stockSnapshot
	wo         map[uuid.UUID]float64
	po         map[uuid.UUID]float64
	planned    map[uuid.UUID]float64
	horizonEnd time.Time
}

func newSupplyPools(horizonEnd time.Time) *supplyPools {
	return &supplyPools{inventory: map[uuid.UUID]float64{}, stock: map[uuid.UUID]stockSnapshot{}, wo: map[uuid.UUID]float64{}, po: map[uuid.UUID]float64{}, planned: map[uuid.UUID]float64{}, horizonEnd: horizonEnd}
}

func normalizeHorizon(days int) int {
	if days <= 0 {
		return defaultPeggingHorizonDays
	}
	if days > 366 {
		return 366
	}
	return days
}

func (s *PeggingService) Run(ctx context.Context, salesOrderID uuid.UUID, in PeggingRunInput, actor SalesOrderActor) (*domain.PeggingResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if salesOrderID == uuid.Nil {
		return nil, domain.NewBadRequest("salesOrderId is required", nil)
	}
	horizon := normalizeHorizon(in.HorizonDays)
	runID := uuid.New()
	asOf := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO pegging_runs(id,sales_order_id,status,as_of,horizon_days,generated_by_user_id,generated_by) VALUES($1,$2,'RUNNING',$3,$4,$5,$6)`, runID, salesOrderID, asOf, horizon, actor.UserID, actor.Username); err != nil {
		return nil, err
	}
	result, err := s.calculateAndPersist(ctx, runID, salesOrderID, horizon, asOf)
	if err != nil {
		_, _ = s.db.ExecContext(context.Background(), `UPDATE pegging_runs SET status='FAILED',error_text=$2,completed_at=transaction_timestamp() WHERE id=$1 AND status='RUNNING'`, runID, err.Error())
		return nil, err
	}
	return result, nil
}

func (s *PeggingService) calculateAndPersist(ctx context.Context, runID, salesOrderID uuid.UUID, horizon int, asOf time.Time) (*domain.PeggingResult, error) {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	order, lines, err := loadPeggingOrder(ctx, tx, salesOrderID)
	if err != nil {
		return nil, err
	}
	if order.Status == "CANCELLED" {
		return nil, domain.NewConflict("cancelled sales order cannot be pegged")
	}
	if len(lines) == 0 {
		return nil, domain.NewConflict("sales order has no lines")
	}

	g := newGraphBuilder(runID)
	horizonEnd := dateOnly(asOf).AddDate(0, 0, horizon)
	pools := newSupplyPools(horizonEnd)
	orderDue := order.RequestedDate
	if order.PromisedDate != nil {
		orderDue = *order.PromisedDate
	}
	soID := order.ID
	soNode := g.node("SO:"+order.ID.String(), "SALES_ORDER", fmt.Sprintf("%s / %s", order.OrderNo, order.CustomerName), &soID, order.OrderNo, nil, "", nil, &orderDue, order.Status,
		map[string]any{"customerNo": order.CustomerNo, "priority": order.Priority, "requestedDate": order.RequestedDate, "promisedDate": order.PromisedDate})

	for i := range lines {
		line := &lines[i]
		if line.OpenQty <= 1e-9 {
			continue
		}
		lineDue := line.RequestedDate
		if line.PromisedDate != nil {
			lineDue = *line.PromisedDate
		}
		lineID := line.ID
		itemID := line.ItemID
		lineNode := g.node("SOL:"+line.ID.String(), "SALES_ORDER_LINE", fmt.Sprintf("Line %d / %s", line.LineNo, line.ItemCode), &lineID, fmt.Sprintf("%s:%d", order.OrderNo, line.LineNo), &itemID, line.ItemCode, floatPtr(line.OpenQty), &lineDue, order.Status,
			map[string]any{"requestedDate": line.RequestedDate, "promisedDate": line.PromisedDate, "allocatedQty": line.AllocatedQty, "shippedQty": line.ShippedQty})
		g.edge(soNode, lineNode, "HAS_LINE", floatPtr(line.OpenQty), nil)

		promise, _ := latestPromiseEvidence(ctx, tx, line.ID)
		bop, _ := latestBOPEvidence(ctx, tx, line.ID)
		if promise != nil {
			promiseStatus := "EFFECTIVE"
			if bop != nil && bop.PublishedAt.After(promise.AcceptedAt) {
				promiseStatus = "SUPERSEDED"
			}
			pid := promise.RunID
			pn := g.node("PROMISE:"+promise.RunID.String(), "PROMISE", "Accepted ATP/CTP promise", &pid, promise.RunID.String(), &itemID, line.ItemCode, floatPtr(promise.ATPQty+promise.CTPQty), &lineDue, promiseStatus,
				map[string]any{"acceptedAt": promise.AcceptedAt, "atpQty": promise.ATPQty, "ctpQty": promise.CTPQty})
			g.edge(lineNode, pn, "PROMISED_BY", nil, nil)
		}
		if bop != nil {
			bopStatus := "EFFECTIVE"
			if promise != nil && promise.AcceptedAt.After(bop.PublishedAt) {
				bopStatus = "SUPERSEDED"
			}
			bid := bop.RunID
			bn := g.node("BOP:"+bop.RunID.String(), "BACKORDER", "Published Backorder decision", &bid, bop.RunID.String(), &itemID, line.ItemCode, floatPtr(bop.BackorderQty), bop.ProposedDate, bopStatus,
				map[string]any{"publishedAt": bop.PublishedAt, "backorderQty": bop.BackorderQty, "decision": bop.Decision, "constraintType": bop.ConstraintType, "serviceClassCode": bop.ServiceClassCode, "orderPriority": bop.OrderPriority, "allocationPlanId": bop.AllocationPlanID, "allocationBucketPct": bop.AllocationBucketPct})
			g.edge(lineNode, bn, "REPRIORITIZED_BY", nil, nil)
			if bop.BackorderQty > 1e-9 && bopStatus == "EFFECTIVE" {
				path := []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String(), "BOP:" + bop.RunID.String()}
				g.exception(order, line, "BACKORDER", "CRITICAL", bn, fmt.Sprintf("%.2f %s remains backordered after published BOP", bop.BackorderQty, line.ItemCode), &line.RequestedDate, line.PromisedDate, bop.ProposedDate, 0, path, map[string]any{"backorderQty": bop.BackorderQty})
			}
		}
		if line.PromisedDate != nil && line.PromisedDate.After(line.RequestedDate) {
			d := daysLate(*line.PromisedDate, line.RequestedDate)
			root := lineNode
			path := []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String()}
			g.exception(order, line, "LATE_PROMISE", severityFor("LATE_PROMISE", d), root, fmt.Sprintf("Promise is %d day(s) later than customer requested date", d), &line.RequestedDate, line.PromisedDate, line.PromisedDate, d, path, nil)
		}

		// Inventory policy is evaluated once per Sales Order line before consuming shared supply.
		_, policyStock, e := s.inventoryNode(ctx, tx, g, pools, line.ItemID, line.ItemCode)
		if e != nil {
			return nil, e
		}
		s.traceInventoryPolicyRisk(g, order, line, policyStock, line.ItemID, line.ItemCode, []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String()})

		remaining := line.OpenQty
		if line.AllocatedQty > 1e-9 {
			invNode, snap, e := s.inventoryNode(ctx, tx, g, pools, line.ItemID, line.ItemCode)
			if e != nil {
				return nil, e
			}
			fixed := math.Min(line.AllocatedQty, remaining)
			g.edge(lineNode, invNode, "ALLOCATED_FROM", floatPtr(fixed), map[string]any{"fixedReservation": true})
			remaining -= fixed
			_ = snap
		}
		if remaining > 1e-9 {
			invNode, _, e := s.inventoryNode(ctx, tx, g, pools, line.ItemID, line.ItemCode)
			if e != nil {
				return nil, e
			}
			use := math.Min(remaining, pools.inventory[line.ItemID])
			if use > 1e-9 {
				g.edge(lineNode, invNode, "SUPPLIED_BY", floatPtr(use), map[string]any{"sharedAvailable": true})
				pools.inventory[line.ItemID] -= use
				remaining -= use
			}
		}

		formalSupply := 0.0
		if remaining > 1e-9 {
			used, e := s.peggTopManufacturingSupply(ctx, tx, g, pools, order, line, lineNode, remaining, lineDue, []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String()}, 0)
			if e != nil {
				return nil, e
			}
			formalSupply += used
			remaining -= used
		}
		if remaining > 1e-9 {
			used, e := s.peggPlannedSupply(ctx, tx, g, pools, order, line, lineNode, remaining, lineDue, []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String()})
			if e != nil {
				return nil, e
			}
			formalSupply += used
			remaining -= used
		}
		if remaining > 1e-9 {
			shortKey := "SHORT:TOP:" + line.ID.String()
			shortNode := g.node(shortKey, "SHORTAGE", "Uncovered finished-good supply", nil, order.OrderNo, &itemID, line.ItemCode, floatPtr(remaining), &lineDue, "OPEN", map[string]any{"formalSupply": formalSupply, "horizonEnd": pools.horizonEnd.Format("2006-01-02")})
			g.edge(lineNode, shortNode, "SHORT_BY", floatPtr(remaining), nil)
			path := []string{"SO:" + order.ID.String(), "SOL:" + line.ID.String(), shortKey}
			typ := "MATERIAL_SHORTAGE"
			msg := fmt.Sprintf("%.2f %s has no pegged inventory/WO/planned supply within the run horizon", remaining, line.ItemCode)
			if promise != nil && promise.CTPQty > 1e-9 {
				typ = "UNCONVERTED_CTP"
				msg = fmt.Sprintf("%.2f %s remains unconverted after an accepted CTP promise", remaining, line.ItemCode)
			}
			g.exception(order, line, typ, severityFor(typ, 0), shortNode, msg, &line.RequestedDate, line.PromisedDate, nil, 0, path, map[string]any{"remainingQty": remaining, "horizonEnd": pools.horizonEnd.Format("2006-01-02")})
		}
	}

	hash := canonicalPeggingHash(g)
	if err := persistPeggingGraph(ctx, tx, g); err != nil {
		return nil, err
	}
	completed := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE pegging_runs SET status='SUCCEEDED',result_hash=$2,error_text='',completed_at=$3 WHERE id=$1 AND status='RUNNING'`, runID, hash, completed); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, runID)
}

func loadPeggingOrder(ctx context.Context, tx *sqlx.Tx, id uuid.UUID) (peggingOrder, []peggingLine, error) {
	var o peggingOrder
	if err := tx.GetContext(ctx, &o, `
SELECT so.id,so.order_no,so.customer_id,c.customer_no,c.name AS customer_name,so.status,so.priority,
       so.requested_date,so.promised_date
  FROM sales_orders so JOIN customers c ON c.id=so.customer_id
 WHERE so.id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return o, nil, domain.NewNotFound("sales order")
		}
		return o, nil, err
	}
	var lines []peggingLine
	if err := tx.SelectContext(ctx, &lines, `
SELECT l.id,l.sales_order_id,l.line_no,l.item_id,i.code AS item_code,i.name AS item_name,i.type AS item_type,
       l.quantity::double precision AS quantity,l.allocated_qty::double precision AS allocated_qty,
       l.shipped_qty::double precision AS shipped_qty,l.cancelled_qty::double precision AS cancelled_qty,
       (l.quantity-l.shipped_qty-l.cancelled_qty)::double precision AS open_qty,
       l.requested_date,l.promised_date
  FROM sales_order_lines l JOIN items i ON i.id=l.item_id
 WHERE l.sales_order_id=$1
 ORDER BY l.line_no,l.id`, id); err != nil {
		return o, nil, err
	}
	return o, lines, nil
}

func latestPromiseEvidence(ctx context.Context, tx *sqlx.Tx, lineID uuid.UUID) (*promiseEvidence, error) {
	var p promiseEvidence
	err := tx.GetContext(ctx, &p, `
SELECT r.id AS run_id,a.accepted_at,
       lr.ctp_qty::double precision AS ctp_qty,lr.atp_qty::double precision AS atp_qty
  FROM order_promise_acceptances a
  JOIN order_promise_runs r ON r.id=a.run_id
  JOIN order_promise_line_results lr ON lr.run_id=r.id AND lr.sales_order_line_id=$1
 ORDER BY a.accepted_at DESC,a.id DESC LIMIT 1`, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func latestBOPEvidence(ctx context.Context, tx *sqlx.Tx, lineID uuid.UUID) (*bopEvidence, error) {
	var b bopEvidence
	err := tx.GetContext(ctx, &b, `
SELECT r.id AS run_id,p.published_at,l.backorder_qty::double precision AS backorder_qty,
       l.decision,l.constraint_type,l.service_class_code,l.order_priority,l.allocation_plan_id,
       l.allocation_bucket_pct::double precision AS allocation_bucket_pct,l.proposed_promised_date
  FROM backorder_publications p
  JOIN backorder_runs r ON r.id=p.run_id
  JOIN backorder_run_lines l ON l.run_id=r.id AND l.sales_order_line_id=$1
 ORDER BY p.published_at DESC,p.id DESC LIMIT 1`, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *PeggingService) stock(ctx context.Context, tx *sqlx.Tx, pools *supplyPools, itemID uuid.UUID) (stockSnapshot, error) {
	if st, ok := pools.stock[itemID]; ok {
		return st, nil
	}
	var st stockSnapshot
	err := tx.GetContext(ctx, &st, `
WITH physical AS (
  SELECT i.id AS item_id,i.safety_stock::double precision AS legacy_safety_stock,
         COALESCE(v.on_hand,0)::double precision AS on_hand,
         COALESCE(v.reserved,0)::double precision AS reserved
    FROM items i LEFT JOIN v_stock_balance v ON v.item_id=i.id
   WHERE i.id=$1
), quality AS (
  SELECT l.item_id,
         COALESCE(SUM(CASE WHEN l.quality_status='OK' THEN lm.quantity ELSE 0 END),0)::double precision AS usable,
         COALESCE(SUM(CASE WHEN l.quality_status='HOLD' THEN lm.quantity ELSE 0 END),0)::double precision AS hold_qty,
         COALESCE(SUM(CASE WHEN l.quality_status='REJECTED' THEN lm.quantity ELSE 0 END),0)::double precision AS rejected_qty
    FROM lots l LEFT JOIN lot_movements lm ON lm.lot_id=l.id
   WHERE l.item_id=$1 GROUP BY l.item_id
), base AS (
  SELECT p.item_id,p.on_hand,p.reserved,
         GREATEST(COALESCE(q.usable,p.on_hand),0)::double precision AS usable,
         GREATEST(COALESCE(q.hold_qty,0),0)::double precision AS hold_qty,
         GREATEST(COALESCE(q.rejected_qty,0),0)::double precision AS rejected_qty,
         GREATEST(COALESCE(q.usable,p.on_hand)-p.reserved,0)::double precision AS gross_available,
         COALESCE(ip.safety_stock,p.legacy_safety_stock,0)::double precision AS safety_stock,
         COALESCE(ip.reorder_point,ip.safety_stock,p.legacy_safety_stock,0)::double precision AS reorder_point,
         COALESCE(ip.min_qty,ip.reorder_point,ip.safety_stock,p.legacy_safety_stock,0)::double precision AS min_qty,
         COALESCE(ip.max_qty,ip.min_qty,ip.reorder_point,ip.safety_stock,p.legacy_safety_stock,0)::double precision AS max_qty,
         COALESCE(ip.service_level,0)::double precision AS service_level,
         ip.policy_version_id,
         COALESCE(ip.status,'LEGACY') AS policy_status,
         COALESCE(ip.replenishment_method,'SAFETY_STOCK') AS replenishment_method
    FROM physical p
    LEFT JOIN quality q ON q.item_id=p.item_id
    LEFT JOIN v_current_inventory_policy ip ON ip.item_id=p.item_id
)
SELECT item_id,on_hand,reserved,usable,hold_qty,rejected_qty,gross_available,
       GREATEST(gross_available-safety_stock,0)::double precision AS available,
       safety_stock,reorder_point,min_qty,max_qty,service_level,policy_version_id,policy_status,replenishment_method
  FROM base`, itemID)
	if err != nil {
		return st, err
	}
	pools.stock[itemID] = st
	pools.inventory[itemID] = st.Available
	return st, nil
}

func (s *PeggingService) inventoryPolicyNode(g *graphBuilder, st stockSnapshot, itemID uuid.UUID, itemCode string) *uuid.UUID {
	if st.PolicyVersionID == nil {
		return nil
	}
	versionID := *st.PolicyVersionID
	n := g.node("IPOL:"+versionID.String(), "INVENTORY_POLICY", "Inventory policy / "+itemCode, &versionID, versionID.String(), &itemID, itemCode, floatPtr(st.SafetyStock), nil, st.PolicyStatus,
		map[string]any{"safetyStock": st.SafetyStock, "reorderPoint": st.ReorderPoint, "minQty": st.MinQty, "maxQty": st.MaxQty, "serviceLevel": st.ServiceLevel, "replenishmentMethod": st.ReplenishmentMethod, "grossAvailable": st.GrossAvailable})
	return &n
}

func (s *PeggingService) traceInventoryPolicyRisk(g *graphBuilder, order peggingOrder, line *peggingLine, st stockSnapshot, itemID uuid.UUID, itemCode string, path []string) {
	policyNode := s.inventoryPolicyNode(g, st, itemID, itemCode)
	if policyNode == nil {
		return
	}
	policyKey := "IPOL:" + st.PolicyVersionID.String()
	if st.GrossAvailable+1e-9 < st.SafetyStock {
		short := st.SafetyStock - st.GrossAvailable
		g.exception(order, line, "SAFETY_STOCK_BREACH", "CRITICAL", *policyNode,
			fmt.Sprintf("%s gross available %.2f is %.2f below safety stock %.2f", itemCode, st.GrossAvailable, short, st.SafetyStock),
			&line.RequestedDate, line.PromisedDate, nil, 0, append(append([]string{}, path...), policyKey),
			map[string]any{"itemCode": itemCode, "grossAvailable": st.GrossAvailable, "safetyStock": st.SafetyStock, "shortageQty": short, "serviceLevel": st.ServiceLevel})
		return
	}
	if st.GrossAvailable+1e-9 < st.ReorderPoint {
		short := st.ReorderPoint - st.GrossAvailable
		g.exception(order, line, "REORDER_POINT_BREACH", "WARNING", *policyNode,
			fmt.Sprintf("%s gross available %.2f is %.2f below reorder point %.2f", itemCode, st.GrossAvailable, short, st.ReorderPoint),
			&line.RequestedDate, line.PromisedDate, nil, 0, append(append([]string{}, path...), policyKey),
			map[string]any{"itemCode": itemCode, "grossAvailable": st.GrossAvailable, "reorderPoint": st.ReorderPoint, "shortageQty": short, "serviceLevel": st.ServiceLevel})
	}
}

func (s *PeggingService) inventoryNode(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, itemID uuid.UUID, itemCode string) (uuid.UUID, stockSnapshot, error) {
	st, err := s.stock(ctx, tx, pools, itemID)
	if err != nil {
		return uuid.Nil, st, err
	}
	id := itemID
	n := g.node("INV:"+itemID.String(), "INVENTORY", "Usable inventory / "+itemCode, &id, itemCode, &id, itemCode, floatPtr(st.Available), nil, "AVAILABLE",
		map[string]any{"onHand": st.OnHand, "reserved": st.Reserved, "usable": st.Usable, "holdQty": st.Hold, "rejectedQty": st.Rejected, "grossAvailable": st.GrossAvailable, "safetyStockProtected": st.SafetyStock, "availableAfterSafetyStock": st.Available, "reorderPoint": st.ReorderPoint, "minQty": st.MinQty, "maxQty": st.MaxQty, "policyStatus": st.PolicyStatus})
	if p := s.inventoryPolicyNode(g, st, itemID, itemCode); p != nil {
		g.edge(n, *p, "PROTECTED_BY", floatPtr(st.SafetyStock), nil)
	}
	return n, st, nil
}

func (s *PeggingService) peggTopManufacturingSupply(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, order peggingOrder, line *peggingLine, demandNode uuid.UUID, need float64, needDate time.Time, path []string, depth int) (float64, error) {
	var rows []peggingWorkOrder
	if err := tx.SelectContext(ctx, &rows, `
SELECT w.id,w.order_no,w.item_id,i.code AS item_code,i.name AS item_name,i.type AS item_type,
       w.quantity::double precision AS quantity,w.completed_qty::double precision AS completed_qty,
       GREATEST(w.quantity-w.completed_qty,0)::double precision AS remaining_qty,
       w.start_date,w.due_date,w.status
  FROM work_orders w JOIN items i ON i.id=w.item_id
 WHERE w.item_id=$1 AND w.status IN ('PLANNED','RELEASED','IN_PROGRESS') AND w.quantity-w.completed_qty>0
   AND w.due_date <= $2
 ORDER BY w.due_date,w.created_at,w.id`, line.ItemID, pools.horizonEnd); err != nil {
		return 0, err
	}
	usedTotal := 0.0
	for _, wo := range rows {
		if need-usedTotal <= 1e-9 {
			break
		}
		avail, ok := pools.wo[wo.ID]
		if !ok {
			avail = wo.RemainingQty
			pools.wo[wo.ID] = avail
		}
		use := math.Min(need-usedTotal, avail)
		if use <= 1e-9 {
			continue
		}
		pools.wo[wo.ID] -= use
		woID, itemID := wo.ID, wo.ItemID
		wn := g.node("WO:"+wo.ID.String(), "WORK_ORDER", wo.OrderNo+" / "+wo.ItemCode, &woID, wo.OrderNo, &itemID, wo.ItemCode, floatPtr(wo.RemainingQty), &wo.DueDate, wo.Status,
			map[string]any{"quantity": wo.Quantity, "completedQty": wo.CompletedQty, "remainingQty": wo.RemainingQty, "startDate": wo.StartDate})
		g.edge(demandNode, wn, "SUPPLIED_BY", floatPtr(use), nil)
		p2 := append(append([]string{}, path...), "WO:"+wo.ID.String())
		if wo.DueDate.After(needDate) {
			d := daysLate(wo.DueDate, needDate)
			g.exception(order, line, "LATE_WORK_ORDER", severityFor("LATE_WORK_ORDER", d), wn, fmt.Sprintf("WO %s is due %d day(s) after required date", wo.OrderNo, d), &line.RequestedDate, line.PromisedDate, &wo.DueDate, d, p2, map[string]any{"workOrderNo": wo.OrderNo})
		}
		if err := s.addCapacityEvidence(ctx, tx, g, order, line, wn, &wo, needDate, p2); err != nil {
			return usedTotal, err
		}
		if err := s.traceWorkOrderComponents(ctx, tx, g, pools, order, line, wn, wo, use, needDate, p2, depth+1); err != nil {
			return usedTotal, err
		}
		usedTotal += use
	}
	return usedTotal, nil
}

func (s *PeggingService) peggPlannedSupply(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, order peggingOrder, line *peggingLine, demandNode uuid.UUID, need float64, needDate time.Time, path []string) (float64, error) {
	var rows []detailedEvidence
	if err := tx.SelectContext(ctx, &rows, `
WITH latest AS (SELECT id FROM detailed_schedule_runs WHERE status='COMPLETE' ORDER BY generated_at DESC,id DESC LIMIT 1)
SELECT d.id,d.run_id,d.source_type,d.source_ref,d.work_order_id,d.item_id,d.item_code,
       d.quantity::double precision AS quantity,d.due_at,d.scheduled_start,d.scheduled_end,d.schedule_status,
       d.tardy_minutes::double precision AS tardy_minutes,r.generated_at
  FROM detailed_schedule_orders d JOIN detailed_schedule_runs r ON r.id=d.run_id JOIN latest x ON x.id=d.run_id
 WHERE d.source_type='MRP_PLANNED' AND d.item_id=$1 AND d.due_at <= $2
 ORDER BY d.due_at,d.id`, line.ItemID, pools.horizonEnd); err != nil {
		return 0, err
	}
	usedTotal := 0.0
	for _, pl := range rows {
		if need-usedTotal <= 1e-9 {
			break
		}
		avail, ok := pools.planned[pl.ID]
		if !ok {
			avail = pl.Quantity
			pools.planned[pl.ID] = avail
		}
		use := math.Min(need-usedTotal, avail)
		if use <= 1e-9 {
			continue
		}
		pools.planned[pl.ID] -= use
		pid, itemID := pl.ID, pl.ItemID
		pn := g.node("PLAN:"+pl.ID.String(), "PLANNED_ORDER", "MRP planned supply / "+pl.ItemCode, &pid, pl.SourceRef, &itemID, pl.ItemCode, floatPtr(pl.Quantity), &pl.DueAt, pl.ScheduleStatus,
			map[string]any{"sourceRef": pl.SourceRef, "scheduledStart": pl.ScheduledStart, "scheduledEnd": pl.ScheduledEnd, "tardyMinutes": pl.TardyMinutes, "generatedAt": pl.GeneratedAt})
		g.edge(demandNode, pn, "PLANNED_SUPPLY", floatPtr(use), nil)
		p2 := append(append([]string{}, path...), "PLAN:"+pl.ID.String())
		if err := s.addPlannedCapacityEvidence(ctx, tx, g, order, line, pn, &pl, needDate, p2); err != nil {
			return usedTotal, err
		}
		if err := s.traceLiveBOMRequirement(ctx, tx, g, pools, order, line, pn, pl.ItemID, pl.ItemCode, use, needDate, p2, 1); err != nil {
			return usedTotal, err
		}
		usedTotal += use
	}
	return usedTotal, nil
}

func (s *PeggingService) traceWorkOrderComponents(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, order peggingOrder, line *peggingLine, woNode uuid.UUID, wo peggingWorkOrder, usedQty float64, needDate time.Time, path []string, depth int) error {
	if depth > maxPeggingDepth || usedQty <= 1e-9 {
		return nil
	}
	var reqs []bomRequirement
	if err := tx.SelectContext(ctx, &reqs, `
WITH snap AS (
 SELECT s.id FROM work_order_bom_snapshots s WHERE s.work_order_id=$1
), snap_rows AS (
 SELECT l.child_item_id AS child_id,l.child_code AS code,l.child_name AS name,i.type,
        l.quantity_per::double precision AS qty_per,l.scrap_pct::double precision AS scrap_pct,
        (l.required_qty * ($2 / NULLIF(w.quantity,0)))::double precision AS required_qty,true AS from_snapshot
   FROM snap s JOIN work_order_bom_snapshot_lines l ON l.snapshot_id=s.id
   JOIN work_orders w ON w.id=$1 JOIN items i ON i.id=l.child_item_id
), live_rows AS (
 SELECT b.child_id,i.code,i.name,i.type,b.quantity::double precision AS qty_per,b.scrap_pct::double precision AS scrap_pct,
        ($2*b.quantity*(1+b.scrap_pct))::double precision AS required_qty,false AS from_snapshot
   FROM bom_components b JOIN items i ON i.id=b.child_id
  WHERE b.parent_id=$3 AND NOT EXISTS (SELECT 1 FROM snap)
)
SELECT * FROM snap_rows UNION ALL SELECT * FROM live_rows ORDER BY code`, wo.ID, usedQty, wo.ItemID); err != nil {
		return err
	}
	for _, r := range reqs {
		if err := s.traceRequirement(ctx, tx, g, pools, order, line, woNode, "WO:"+wo.ID.String(), r, needDate, path, depth); err != nil {
			return err
		}
	}
	return nil
}

func (s *PeggingService) traceLiveBOMRequirement(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, order peggingOrder, line *peggingLine, parentNode uuid.UUID, parentItem uuid.UUID, parentCode string, qty float64, needDate time.Time, path []string, depth int) error {
	if depth > maxPeggingDepth || qty <= 1e-9 {
		return nil
	}
	var reqs []bomRequirement
	if err := tx.SelectContext(ctx, &reqs, `
SELECT b.child_id,i.code,i.name,i.type,b.quantity::double precision AS qty_per,b.scrap_pct::double precision AS scrap_pct,
       ($2*b.quantity*(1+b.scrap_pct))::double precision AS required_qty,false AS from_snapshot
  FROM bom_components b JOIN items i ON i.id=b.child_id WHERE b.parent_id=$1 ORDER BY i.code`, parentItem, qty); err != nil {
		return err
	}
	parentKey := g.nodeKeyByID[parentNode]
	if parentKey == "" {
		parentKey = "PLAN:" + parentCode
	}
	for _, r := range reqs {
		if err := s.traceRequirement(ctx, tx, g, pools, order, line, parentNode, parentKey, r, needDate, path, depth); err != nil {
			return err
		}
	}
	return nil
}

func (s *PeggingService) traceRequirement(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, pools *supplyPools, order peggingOrder, line *peggingLine, parentNode uuid.UUID, parentKey string, r bomRequirement, needDate time.Time, path []string, depth int) error {
	if depth > maxPeggingDepth || r.Required <= 1e-9 {
		return nil
	}
	itemID := r.ChildID
	reqKey := fmt.Sprintf("REQ:%s:%s", parentKey, r.ChildID)
	reqNode := g.node(reqKey, "ITEM", fmt.Sprintf("%s requirement", r.Code), &itemID, r.Code, &itemID, r.Code, floatPtr(r.Required), &needDate, "REQUIRED", map[string]any{"quantityPer": r.QtyPer, "scrapPct": r.ScrapPct, "fromSnapshot": r.FromSnap})
	g.edge(parentNode, reqNode, "REQUIRES_COMPONENT", floatPtr(r.Required), nil)
	p2 := append(append([]string{}, path...), reqKey)
	remaining := r.Required
	invNode, st, err := s.inventoryNode(ctx, tx, g, pools, r.ChildID, r.Code)
	if err != nil {
		return err
	}
	s.traceInventoryPolicyRisk(g, order, line, st, r.ChildID, r.Code, p2)
	use := math.Min(remaining, pools.inventory[r.ChildID])
	if use > 1e-9 {
		g.edge(reqNode, invNode, "SUPPLIED_BY", floatPtr(use), nil)
		pools.inventory[r.ChildID] -= use
		remaining -= use
	}

	if st.Hold > 1e-9 && remaining > 1e-9 {
		holdKey := "QHOLD:" + r.ChildID.String()
		holdNode := g.node(holdKey, "QUALITY_HOLD", "Quality HOLD / "+r.Code, nil, r.Code, &itemID, r.Code, floatPtr(st.Hold), nil, "HOLD", map[string]any{"holdQty": st.Hold})
		g.edge(reqNode, holdNode, "BLOCKED_BY", floatPtr(math.Min(st.Hold, remaining)), nil)
		g.exception(order, line, "QUALITY_HOLD", severityFor("QUALITY_HOLD", 0), holdNode, fmt.Sprintf("%.2f %s is quarantined on quality HOLD", st.Hold, r.Code), &line.RequestedDate, line.PromisedDate, nil, 0, append(p2, holdKey), map[string]any{"holdQty": st.Hold})
	}

	if remaining > 1e-9 && (r.Type == "RM" || r.Type == "PP") {
		var pos []peggingPO
		if err := tx.SelectContext(ctx, &pos, `
SELECT p.id,p.po_no,p.item_id,i.code AS item_code,i.name AS item_name,p.supplier,
       p.supplier_quality_status,p.quantity::double precision AS quantity,
       p.received_qty::double precision AS received_qty,p.remaining_qty::double precision AS remaining_qty,
       p.due_date,p.status,p.schedule_status,p.confirmation_event_id,
       p.confirmed_quantity::double precision AS confirmed_quantity,p.confirmed_delivery_date,
       p.asn_event_id,p.asn_no,p.asn_quantity::double precision AS asn_quantity,
       p.asn_expected_arrival_date,p.expected_delivery_date,p.schedule_source,
       p.reliability_sample_count,p.reliability_on_time_rate,p.reliability_p90_days,p.recommended_lead_time_days
  FROM v_purchase_order_planning_schedule p JOIN items i ON i.id=p.item_id
 WHERE p.item_id=$1 AND p.status IN ('OPEN','PARTIALLY_RECEIVED') AND p.remaining_qty>0
   AND p.expected_delivery_date <= $2
 ORDER BY p.expected_delivery_date,p.id`, r.ChildID, pools.horizonEnd); err != nil {
			return err
		}
		for _, po := range pos {
			if remaining <= 1e-9 {
				break
			}
			avail, ok := pools.po[po.ID]
			if !ok {
				avail = po.RemainingQty
				pools.po[po.ID] = avail
			}
			if avail <= 1e-9 {
				continue
			}
			poID := po.ID
			pn := g.node("PO:"+po.ID.String(), "PURCHASE_ORDER", po.PONo+" / "+po.Supplier, &poID, po.PONo, &itemID, r.Code, floatPtr(po.RemainingQty), &po.DueDate, po.Status, map[string]any{
				"supplier": po.Supplier, "supplierQualityStatus": po.SupplierQualityStatus, "receivedQty": po.ReceivedQty,
				"scheduleStatus": po.ScheduleStatus, "scheduleSource": po.ScheduleSource,
				"expectedDeliveryDate": po.ExpectedDeliveryDate.Format("2006-01-02"), "recommendedLeadTimeDays": po.RecommendedLeadTimeDays,
			})
			poPath := append(append([]string{}, p2...), "PO:"+po.ID.String())
			if po.ConfirmationEventID != nil && po.ConfirmedDeliveryDate != nil {
				confID := *po.ConfirmationEventID
				confKey := "SCONF:" + confID.String()
				conf := g.node(confKey, "SUPPLIER_CONFIRMATION", "Supplier confirmation / "+po.PONo, &confID, po.Supplier, &itemID, r.Code, po.ConfirmedQuantity, po.ConfirmedDeliveryDate, "CONFIRMED", map[string]any{"supplier": po.Supplier})
				g.edge(pn, conf, "CONFIRMED_BY", po.ConfirmedQuantity, nil)
				if po.ConfirmedDeliveryDate.After(needDate) {
					d := daysLate(*po.ConfirmedDeliveryDate, needDate)
					g.exception(order, line, "SUPPLIER_CONFIRMATION_LATE", severityFor("SUPPLIER_CONFIRMATION_LATE", d), conf, fmt.Sprintf("Supplier %s confirmed PO %s %d day(s) after required date", po.Supplier, po.PONo, d), &line.RequestedDate, line.PromisedDate, po.ConfirmedDeliveryDate, d, append(poPath, confKey), map[string]any{"poNo": po.PONo, "supplier": po.Supplier})
				}
			}
			if po.ASNEventID != nil && po.ASNExpectedArrivalDate != nil {
				asnID := *po.ASNEventID
				asnKey := "ASN:" + asnID.String()
				asn := g.node(asnKey, "SUPPLIER_ASN", "ASN "+po.ASNNo+" / "+po.PONo, &asnID, po.ASNNo, &itemID, r.Code, po.ASNQuantity, po.ASNExpectedArrivalDate, "IN_TRANSIT", map[string]any{"supplier": po.Supplier, "poNo": po.PONo})
				g.edge(pn, asn, "SHIPPED_BY", po.ASNQuantity, nil)
			}
			if po.ScheduleSource == "RELIABILITY" && po.ReliabilitySampleCount > 0 {
				relKey := "LEADTIME:" + strings.ToUpper(strings.TrimSpace(po.Supplier)) + ":" + itemID.String()
				rel := g.node(relKey, "LEAD_TIME_PROFILE", "Lead-time reliability / "+po.Supplier, nil, po.Supplier, &itemID, r.Code, nil, &po.ExpectedDeliveryDate, "ACTIVE", map[string]any{
					"sampleCount": po.ReliabilitySampleCount, "onTimeRate": po.ReliabilityOnTimeRate, "p90LeadDays": po.ReliabilityP90Days, "recommendedLeadTimeDays": po.RecommendedLeadTimeDays,
				})
				g.edge(pn, rel, "PLANNED_USING", nil, nil)
				if po.ReliabilitySampleCount >= defaultReliabilityMinSamples && po.ReliabilityOnTimeRate < 0.80 {
					g.exception(order, line, "SUPPLIER_RELIABILITY_RISK", "WARNING", rel, fmt.Sprintf("Supplier %s on-time rate is %.1f%%; PO %s uses reliability-adjusted date", po.Supplier, po.ReliabilityOnTimeRate*100, po.PONo), &line.RequestedDate, line.PromisedDate, &po.ExpectedDeliveryDate, daysLate(po.ExpectedDeliveryDate, po.DueDate), append(poPath, relKey), map[string]any{"poNo": po.PONo, "supplier": po.Supplier, "sampleCount": po.ReliabilitySampleCount, "onTimeRate": po.ReliabilityOnTimeRate, "p90LeadDays": po.ReliabilityP90Days})
				}
			}
			if po.SupplierQualityStatus == "BLOCKED" {
				supKey := "SUPPLIER:" + po.Supplier
				supNode := g.node(supKey, "SUPPLIER", po.Supplier, nil, po.Supplier, nil, "", nil, nil, "BLOCKED", map[string]any{"supplier": po.Supplier})
				g.edge(pn, supNode, "BLOCKED_BY", nil, nil)
				g.exception(order, line, "SUPPLIER_BLOCKED", "CRITICAL", supNode, fmt.Sprintf("Supplier %s for PO %s is BLOCKED", po.Supplier, po.PONo), &line.RequestedDate, line.PromisedDate, &po.DueDate, 0, append(append([]string{}, p2...), "PO:"+po.ID.String(), supKey), map[string]any{"poNo": po.PONo})
				continue
			}
			consume := math.Min(remaining, avail)
			pools.po[po.ID] -= consume
			remaining -= consume
			g.edge(reqNode, pn, "PURCHASED_BY", floatPtr(consume), nil)
			if po.ExpectedDeliveryDate.After(needDate) {
				d := daysLate(po.ExpectedDeliveryDate, needDate)
				g.exception(order, line, "LATE_PURCHASE_ORDER", severityFor("LATE_PURCHASE_ORDER", d), pn, fmt.Sprintf("PO %s expected arrival is %d day(s) after required date (%s)", po.PONo, d, po.ScheduleSource), &line.RequestedDate, line.PromisedDate, &po.ExpectedDeliveryDate, d, poPath, map[string]any{"poNo": po.PONo, "supplier": po.Supplier, "scheduleSource": po.ScheduleSource, "originalDueDate": po.DueDate.Format("2006-01-02")})
			}
		}
	}

	if remaining > 1e-9 && (r.Type == "FG" || r.Type == "SA") {
		fake := &peggingLine{ID: line.ID, SalesOrderID: line.SalesOrderID, LineNo: line.LineNo, ItemID: r.ChildID, ItemCode: r.Code, ItemName: r.Name, ItemType: r.Type, OpenQty: remaining, RequestedDate: line.RequestedDate, PromisedDate: line.PromisedDate}
		used, err := s.peggTopManufacturingSupply(ctx, tx, g, pools, order, fake, reqNode, remaining, needDate, p2, depth)
		if err != nil {
			return err
		}
		remaining -= used
	}
	if remaining > 1e-9 {
		shortKey := fmt.Sprintf("SHORT:%s:%s", parentKey, r.ChildID)
		shortNode := g.node(shortKey, "SHORTAGE", "Material shortage / "+r.Code, nil, r.Code, &itemID, r.Code, floatPtr(remaining), &needDate, "OPEN", map[string]any{"requiredQty": r.Required, "shortQty": remaining, "horizonEnd": pools.horizonEnd.Format("2006-01-02")})
		g.edge(reqNode, shortNode, "SHORT_BY", floatPtr(remaining), nil)
		g.exception(order, line, "MATERIAL_SHORTAGE", "CRITICAL", shortNode, fmt.Sprintf("%.2f %s cannot be covered by usable inventory or formal supply within the run horizon", remaining, r.Code), &line.RequestedDate, line.PromisedDate, nil, 0, append(p2, shortKey), map[string]any{"requiredQty": r.Required, "shortQty": remaining, "horizonEnd": pools.horizonEnd.Format("2006-01-02")})
	}
	return nil
}

func (s *PeggingService) addPlannedCapacityEvidence(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, order peggingOrder, line *peggingLine, plannedNode uuid.UUID, ev *detailedEvidence, needDate time.Time, path []string) error {
	dsid := ev.ID
	ds := g.node("DS:"+ev.ID.String(), "DETAILED_SCHEDULE", "Detailed Schedule / "+ev.SourceRef, &dsid, ev.SourceRef, &ev.ItemID, ev.ItemCode, floatPtr(ev.Quantity), ev.ScheduledEnd, ev.ScheduleStatus,
		map[string]any{"scheduledStart": ev.ScheduledStart, "scheduledEnd": ev.ScheduledEnd, "tardyMinutes": ev.TardyMinutes, "generatedAt": ev.GeneratedAt, "sourceType": ev.SourceType})
	g.edge(plannedNode, ds, "SCHEDULED_BY", nil, nil)
	var wcs []workCenterEvidence
	if err := tx.SelectContext(ctx, &wcs, `
SELECT DISTINCT wc.id,wc.code,wc.name,b.schedule_status AS batch_status,b.machines_required,b.workers_required,
       b.machine_capacity_snapshot,b.worker_capacity_snapshot,b.scheduled_end
  FROM detailed_schedule_batches b JOIN work_centers wc ON wc.id=b.work_center_id
 WHERE b.schedule_order_id=$1 ORDER BY wc.code`, ev.ID); err != nil {
		return err
	}
	root := ds
	for _, wc := range wcs {
		wid := wc.ID
		wn := g.node("WC:"+ev.ID.String()+":"+wc.ID.String(), "WORK_CENTER", wc.Code+" / "+wc.Name, &wid, wc.Code, nil, "", nil, wc.ScheduledEnd, wc.BatchStatus,
			map[string]any{"machinesRequired": wc.MachinesReq, "workersRequired": wc.WorkersReq, "machineCapacity": wc.MachineCapacity, "workerCapacity": wc.WorkerCapacity})
		g.edge(ds, wn, "USES_WORK_CENTER", nil, nil)
		maintenanceRoot, maintenanceKey, err := s.addMaintenanceCapacityEvidence(ctx, tx, g, order, line, *ev, wc, wn, needDate, append(path, "DS:"+ev.ID.String(), "WC:"+ev.ID.String()+":"+wc.ID.String()))
		if err != nil {
			return err
		}
		feedbackRoot, feedbackKey, err := s.addCapacityFeedbackEvidence(ctx, tx, g, order, line, *ev, wc, wn, needDate, append(path, "DS:"+ev.ID.String(), "WC:"+ev.ID.String()+":"+wc.ID.String()))
		if err != nil {
			return err
		}
		if maintenanceRoot != uuid.Nil && (ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate))) {
			root = maintenanceRoot
			_ = maintenanceKey
		} else if feedbackRoot != uuid.Nil && (ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate))) {
			root = feedbackRoot
			_ = feedbackKey
		}
		if wc.BatchStatus == "UNSCHEDULED" && maintenanceRoot == uuid.Nil && feedbackRoot == uuid.Nil {
			root = wn
		}
	}
	p2 := append(append([]string{}, path...), "DS:"+ev.ID.String())
	if root != ds {
		p2 = append(p2, g.nodeKeyByID[root])
	}
	if ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" {
		g.exception(order, line, "CAPACITY_UNSCHEDULED", "CRITICAL", root, "MRP planned supply is not fully scheduled", &line.RequestedDate, line.PromisedDate, ev.ScheduledEnd, 0, p2, map[string]any{"sourceRef": ev.SourceRef})
	} else if ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate) {
		d := daysLate(*ev.ScheduledEnd, needDate)
		g.exception(order, line, "CAPACITY_LATE", severityFor("CAPACITY_LATE", d), root, fmt.Sprintf("MRP planned supply finishes %d day(s) after required date", d), &line.RequestedDate, line.PromisedDate, ev.ScheduledEnd, d, p2, map[string]any{"sourceRef": ev.SourceRef, "tardyMinutes": ev.TardyMinutes})
	}
	return nil
}

func (s *PeggingService) addCapacityEvidence(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, order peggingOrder, line *peggingLine, woNode uuid.UUID, wo *peggingWorkOrder, needDate time.Time, path []string) error {
	var ev detailedEvidence
	err := tx.GetContext(ctx, &ev, `
SELECT d.id,d.run_id,d.source_type,d.source_ref,d.work_order_id,d.item_id,d.item_code,
       d.quantity::double precision AS quantity,d.due_at,d.scheduled_start,d.scheduled_end,d.schedule_status,
       d.tardy_minutes::double precision AS tardy_minutes,r.generated_at
  FROM detailed_schedule_orders d JOIN detailed_schedule_runs r ON r.id=d.run_id
 WHERE d.work_order_id=$1 AND r.status='COMPLETE'
 ORDER BY r.generated_at DESC,r.id DESC LIMIT 1`, wo.ID)
	if errors.Is(err, sql.ErrNoRows) {
		// A formal WO with no finite detailed schedule is itself actionable capacity evidence.
		key := "DS:MISSING:" + wo.ID.String()
		ds := g.node(key, "DETAILED_SCHEDULE", "No Detailed Schedule for "+wo.OrderNo, nil, wo.OrderNo, &wo.ItemID, wo.ItemCode, nil, &wo.DueDate, "UNSCHEDULED", nil)
		g.edge(woNode, ds, "SCHEDULED_BY", nil, nil)
		g.exception(order, line, "CAPACITY_UNSCHEDULED", "CRITICAL", ds, "Work Order has no completed Detailed Scheduling evidence", &line.RequestedDate, line.PromisedDate, nil, 0, append(path, key), map[string]any{"workOrderNo": wo.OrderNo})
		return nil
	}
	if err != nil {
		return err
	}
	dsid := ev.ID
	ds := g.node("DS:"+ev.ID.String(), "DETAILED_SCHEDULE", "Detailed Schedule / "+wo.OrderNo, &dsid, ev.SourceRef, &wo.ItemID, wo.ItemCode, floatPtr(ev.Quantity), ev.ScheduledEnd, ev.ScheduleStatus, map[string]any{"scheduledStart": ev.ScheduledStart, "scheduledEnd": ev.ScheduledEnd, "tardyMinutes": ev.TardyMinutes, "generatedAt": ev.GeneratedAt})
	g.edge(woNode, ds, "SCHEDULED_BY", nil, nil)
	var wcs []workCenterEvidence
	if err := tx.SelectContext(ctx, &wcs, `
SELECT DISTINCT wc.id,wc.code,wc.name,b.schedule_status AS batch_status,b.machines_required,b.workers_required,
       b.machine_capacity_snapshot,b.worker_capacity_snapshot,b.scheduled_end
  FROM detailed_schedule_batches b JOIN work_centers wc ON wc.id=b.work_center_id
 WHERE b.schedule_order_id=$1 ORDER BY wc.code`, ev.ID); err != nil {
		return err
	}
	root := ds
	for _, wc := range wcs {
		wid := wc.ID
		wn := g.node("WC:"+ev.ID.String()+":"+wc.ID.String(), "WORK_CENTER", wc.Code+" / "+wc.Name, &wid, wc.Code, nil, "", nil, wc.ScheduledEnd, wc.BatchStatus, map[string]any{"machinesRequired": wc.MachinesReq, "workersRequired": wc.WorkersReq, "machineCapacity": wc.MachineCapacity, "workerCapacity": wc.WorkerCapacity})
		g.edge(ds, wn, "USES_WORK_CENTER", nil, nil)
		maintenanceRoot, maintenanceKey, err := s.addMaintenanceCapacityEvidence(ctx, tx, g, order, line, ev, wc, wn, needDate, append(path, "DS:"+ev.ID.String(), "WC:"+ev.ID.String()+":"+wc.ID.String()))
		if err != nil {
			return err
		}
		feedbackRoot, feedbackKey, err := s.addCapacityFeedbackEvidence(ctx, tx, g, order, line, ev, wc, wn, needDate, append(path, "DS:"+ev.ID.String(), "WC:"+ev.ID.String()+":"+wc.ID.String()))
		if err != nil {
			return err
		}
		if maintenanceRoot != uuid.Nil && (ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate))) {
			root = maintenanceRoot
			_ = maintenanceKey
		} else if feedbackRoot != uuid.Nil && (ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate))) {
			root = feedbackRoot
			_ = feedbackKey
		}
		if wc.BatchStatus == "UNSCHEDULED" && maintenanceRoot == uuid.Nil && feedbackRoot == uuid.Nil {
			root = wn
		}
	}
	p2 := append(append([]string{}, path...), "DS:"+ev.ID.String())
	if root != ds {
		p2 = append(p2, g.nodeKeyByID[root])
	}
	if ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" {
		g.exception(order, line, "CAPACITY_UNSCHEDULED", "CRITICAL", root, fmt.Sprintf("Detailed Scheduling status for WO %s is %s", wo.OrderNo, ev.ScheduleStatus), &line.RequestedDate, line.PromisedDate, ev.ScheduledEnd, 0, p2, map[string]any{"workOrderNo": wo.OrderNo, "scheduleStatus": ev.ScheduleStatus})
	} else if ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate) {
		d := daysLate(*ev.ScheduledEnd, needDate)
		g.exception(order, line, "CAPACITY_LATE", severityFor("CAPACITY_LATE", d), root, fmt.Sprintf("Capacity schedule for WO %s finishes %d day(s) late", wo.OrderNo, d), &line.RequestedDate, line.PromisedDate, ev.ScheduledEnd, d, p2, map[string]any{"workOrderNo": wo.OrderNo, "tardyMinutes": ev.TardyMinutes})
	}
	return nil
}

func (s *PeggingService) addCapacityFeedbackEvidence(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, order peggingOrder, line *peggingLine, ev detailedEvidence, wc workCenterEvidence, wcNode uuid.UUID, needDate time.Time, path []string) (uuid.UUID, string, error) {
	var f capacityFeedbackEvidence
	err := tx.GetContext(ctx, &f, `
SELECT feedback_version_id,work_center_id,version_no,source_run_id,source_result_id,effective_efficiency,effective_utilization,
       source_oee,source_availability,source_performance,source_quality,sample_count,confidence,effective_from
  FROM detailed_schedule_capacity_feedback_snapshots
 WHERE run_id=$1 AND work_center_id=$2`, ev.RunID, wc.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, "", nil
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	fid := f.FeedbackVersionID
	key := "CAPFEEDBACK:" + f.FeedbackVersionID.String()
	fn := g.node(key, "CAPACITY_FEEDBACK", "Actual capacity feedback / "+wc.Code, &fid, fmt.Sprintf("%s/v%d", wc.Code, f.VersionNo), nil, "", nil, peggingTimePtr(ev.GeneratedAt), "ACTIVE", map[string]any{
		"versionNo": f.VersionNo, "sourceRunId": f.SourceRunID, "sourceResultId": f.SourceResultID,
		"oee": f.SourceOEE, "availability": f.SourceAvailability, "performance": f.SourcePerformance, "quality": f.SourceQuality,
		"effectiveEfficiency": f.EffectiveEfficiency, "effectiveUtilization": f.EffectiveUtilization,
		"sampleCount": f.SampleCount, "confidence": f.Confidence, "effectiveFrom": f.EffectiveFrom.Format("2006-01-02"),
	})
	g.edge(wcNode, fn, "CALIBRATED_BY", nil, map[string]any{"workCenterCode": wc.Code})
	impacted := ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate))
	if impacted && f.SourceOEE < 0.85 {
		severity := "WARNING"
		if f.SourceOEE < 0.60 {
			severity = "CRITICAL"
		}
		g.exception(order, line, "OEE_CAPACITY_RISK", severity, fn,
			fmt.Sprintf("%s uses empirical capacity feedback from OEE %.1f%% (availability %.1f%% / performance %.1f%%)", wc.Code, f.SourceOEE*100, f.SourceAvailability*100, f.SourcePerformance*100),
			&line.RequestedDate, line.PromisedDate, ev.ScheduledEnd, 0, append(append([]string{}, path...), key), map[string]any{
				"workCenterCode": wc.Code, "feedbackVersionId": f.FeedbackVersionID, "sourceOee": f.SourceOEE,
				"effectiveEfficiency": f.EffectiveEfficiency, "effectiveUtilization": f.EffectiveUtilization, "sampleCount": f.SampleCount,
			})
		return fn, key, nil
	}
	return uuid.Nil, key, nil
}

func (s *PeggingService) addMaintenanceCapacityEvidence(ctx context.Context, tx *sqlx.Tx, g *graphBuilder, order peggingOrder, line *peggingLine, ev detailedEvidence, wc workCenterEvidence, wcNode uuid.UUID, needDate time.Time, path []string) (uuid.UUID, string, error) {
	var rows []maintenanceEvidence
	if err := tx.SelectContext(ctx, &rows, `
SELECT maintenance_event_id,revision_id,revision_no,work_center_id,event_type,status,start_at,end_at,
       unavailable_machines,unavailable_workers,reason,source_ref
  FROM detailed_schedule_maintenance_snapshots
 WHERE run_id=$1 AND work_center_id=$2
 ORDER BY start_at,maintenance_event_id`, ev.RunID, wc.ID); err != nil {
		return uuid.Nil, "", err
	}
	impactEnd := needDate
	if ev.ScheduledEnd != nil && ev.ScheduledEnd.After(impactEnd) {
		impactEnd = *ev.ScheduledEnd
	}
	impactStart := ev.GeneratedAt
	if ev.ScheduledStart != nil && ev.ScheduledStart.Before(impactStart) {
		impactStart = *ev.ScheduledStart
	}
	var root uuid.UUID
	rootKey := ""
	for _, m := range rows {
		if !m.StartAt.Before(impactEnd) || !m.EndAt.After(impactStart) {
			continue
		}
		mid := m.MaintenanceEventID
		key := "MAINT:" + m.MaintenanceEventID.String() + ":R" + fmt.Sprint(m.RevisionNo)
		label := strings.ReplaceAll(m.EventType, "_", " ")
		mn := g.node(key, "MAINTENANCE_EVENT", label, &mid, m.SourceRef, nil, "", nil, peggingTimePtr(m.EndAt), m.Status, map[string]any{
			"eventType": m.EventType, "revisionNo": m.RevisionNo, "startAt": m.StartAt, "endAt": m.EndAt,
			"unavailableMachines": m.UnavailableMachines, "unavailableWorkers": m.UnavailableWorkers,
			"reason": m.Reason, "workCenterCode": wc.Code,
		})
		g.edge(wcNode, mn, "CAPACITY_REDUCED_BY", nil, map[string]any{"workCenterCode": wc.Code})
		if root == uuid.Nil {
			root, rootKey = mn, key
		}
		if ev.ScheduleStatus == "UNSCHEDULED" || ev.ScheduleStatus == "PARTIAL" || (ev.ScheduledEnd != nil && ev.ScheduledEnd.After(needDate)) {
			typ := MaintenanceExceptionType(m.EventType)
			severity := "WARNING"
			if m.EventType == "BREAKDOWN" || m.EventType == "UNPLANNED_DOWNTIME" {
				severity = "CRITICAL"
			}
			msg := fmt.Sprintf("%s reduces %s capacity from %s to %s", label, wc.Code, m.StartAt.Format(time.RFC3339), m.EndAt.Format(time.RFC3339))
			g.exception(order, line, typ, severity, mn, msg, &line.RequestedDate, line.PromisedDate, peggingTimePtr(m.EndAt), daysLate(m.EndAt, needDate), append(append([]string{}, path...), key), map[string]any{
				"maintenanceEventId": m.MaintenanceEventID, "revisionNo": m.RevisionNo, "eventType": m.EventType,
				"unavailableMachines": m.UnavailableMachines, "unavailableWorkers": m.UnavailableWorkers,
			})
		}
	}
	return root, rootKey, nil
}

func canonicalPeggingHash(g *graphBuilder) string {
	type n struct {
		Key, Type, Ref, Item, Status string
		Qty                          *float64
		Due                          string
		Detail                       string
	}
	type e struct {
		From, To, Type string
		Qty            *float64
		Detail         string
	}
	type x struct {
		Key, Type, Severity, Root, Message string
		Impact                             int
		Path, Detail                       string
	}
	ns := make([]n, 0, len(g.nodes))
	for _, v := range g.nodes {
		due := ""
		if v.DueDate != nil {
			due = v.DueDate.Format("2006-01-02")
		}
		ns = append(ns, n{v.NodeKey, v.NodeType, v.EntityRef, v.ItemCode, v.Status, v.Quantity, due, string(v.Detail)})
	}
	es := make([]e, 0, len(g.edges))
	for _, v := range g.edges {
		es = append(es, e{g.nodeKeyByID[v.FromNodeID], g.nodeKeyByID[v.ToNodeID], v.EdgeType, v.Quantity, string(v.Detail)})
	}
	xs := make([]x, 0, len(g.exceptions))
	for _, v := range g.exceptions {
		xs = append(xs, x{v.ExceptionKey, v.ExceptionType, v.Severity, g.nodeKeyByID[v.RootNodeID], v.Message, v.ImpactDays, string(v.RootCausePath), string(v.Detail)})
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Key < ns[j].Key })
	sort.Slice(es, func(i, j int) bool {
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		if es[i].To != es[j].To {
			return es[i].To < es[j].To
		}
		return es[i].Type < es[j].Type
	})
	sort.Slice(xs, func(i, j int) bool { return xs[i].Key < xs[j].Key })
	b, _ := json.Marshal(struct {
		Nodes      []n `json:"nodes"`
		Edges      []e `json:"edges"`
		Exceptions []x `json:"exceptions"`
	}{ns, es, xs})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func persistPeggingGraph(ctx context.Context, tx *sqlx.Tx, g *graphBuilder) error {
	for _, n := range g.nodes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pegging_nodes(id,run_id,node_key,node_type,entity_id,entity_ref,item_id,item_code,label,quantity,due_date,status,detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb)`,
			n.ID, n.RunID, n.NodeKey, n.NodeType, n.EntityID, n.EntityRef, n.ItemID, n.ItemCode, n.Label, n.Quantity, n.DueDate, n.Status, string(n.Detail)); err != nil {
			return err
		}
	}
	for _, e := range g.edges {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pegging_edges(id,run_id,from_node_id,to_node_id,edge_type,quantity,detail) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`,
			e.ID, e.RunID, e.FromNodeID, e.ToNodeID, e.EdgeType, e.Quantity, string(e.Detail)); err != nil {
			return err
		}
	}
	for _, x := range g.exceptions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO planning_exceptions(id,run_id,sales_order_id,sales_order_line_id,exception_key,exception_type,severity,root_node_id,message,requested_date,promised_date,impact_date,impact_days,root_cause_path,detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb)`,
			x.ID, x.RunID, x.SalesOrderID, x.SalesOrderLineID, x.ExceptionKey, x.ExceptionType, x.Severity, x.RootNodeID, x.Message, x.RequestedDate, x.PromisedDate, x.ImpactDate, x.ImpactDays, string(x.RootCausePath), string(x.Detail)); err != nil {
			return err
		}
	}
	return nil
}

func (s *PeggingService) GetRun(ctx context.Context, id uuid.UUID) (*domain.PeggingResult, error) {
	var out domain.PeggingResult
	if err := s.db.GetContext(ctx, &out.Run, `SELECT * FROM pegging_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("pegging run")
		}
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &out.Nodes, `SELECT * FROM pegging_nodes WHERE run_id=$1 ORDER BY node_type,node_key`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &out.Edges, `SELECT * FROM pegging_edges WHERE run_id=$1 ORDER BY edge_type,from_node_id,to_node_id`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &out.Exceptions, `SELECT e.*,planning_exception_current_status(e.id) AS current_status FROM planning_exceptions e WHERE e.run_id=$1 ORDER BY CASE e.severity WHEN 'CRITICAL' THEN 1 WHEN 'WARNING' THEN 2 ELSE 3 END,e.exception_type,e.exception_key`, id); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PeggingService) ListRuns(ctx context.Context, salesOrderID uuid.UUID) ([]domain.PeggingRun, error) {
	var rows []domain.PeggingRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM pegging_runs WHERE sales_order_id=$1 ORDER BY created_at DESC,id DESC`, salesOrderID)
	return rows, err
}

func (s *PeggingService) ListCurrentExceptions(ctx context.Context, status, severity, typ string) ([]domain.PlanningException, error) {
	q := `SELECT * FROM v_current_planning_exceptions WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(status) != "" {
		args = append(args, strings.ToUpper(strings.TrimSpace(status)))
		q += fmt.Sprintf(" AND current_status=$%d", len(args))
	}
	if strings.TrimSpace(severity) != "" {
		args = append(args, strings.ToUpper(strings.TrimSpace(severity)))
		q += fmt.Sprintf(" AND severity=$%d", len(args))
	}
	if strings.TrimSpace(typ) != "" {
		args = append(args, strings.ToUpper(strings.TrimSpace(typ)))
		q += fmt.Sprintf(" AND exception_type=$%d", len(args))
	}
	q += ` ORDER BY CASE severity WHEN 'CRITICAL' THEN 1 WHEN 'WARNING' THEN 2 ELSE 3 END,impact_days DESC,detected_at DESC,id DESC`
	var rows []domain.PlanningException
	err := s.db.SelectContext(ctx, &rows, q, args...)
	return rows, err
}

func (s *PeggingService) Scan(ctx context.Context, in ExceptionScanInput, actor SalesOrderActor) (*domain.ExceptionScanResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	h := normalizeHorizon(in.HorizonDays)
	var ids []uuid.UUID
	if err := s.db.SelectContext(ctx, &ids, `SELECT id FROM sales_orders WHERE status IN ('CONFIRMED','PARTIALLY_SHIPPED') ORDER BY requested_date,order_no,id`); err != nil {
		return nil, err
	}
	out := &domain.ExceptionScanResult{PeggingRuns: []domain.PeggingRun{}, Exceptions: []domain.PlanningException{}}
	for _, id := range ids {
		r, err := s.Run(ctx, id, PeggingRunInput{HorizonDays: h}, actor)
		if err != nil {
			return nil, err
		}
		out.PeggingRuns = append(out.PeggingRuns, r.Run)
	}
	exc, err := s.ListCurrentExceptions(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	out.Exceptions = exc
	return out, nil
}

func (s *PeggingService) ActOnException(ctx context.Context, id uuid.UUID, in ExceptionActionInput, actor SalesOrderActor) (*domain.PlanningExceptionAction, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	action := strings.ToUpper(strings.TrimSpace(in.ActionType))
	to := ""
	switch action {
	case "ACKNOWLEDGE":
		to = "ACKNOWLEDGED"
	case "RESOLVE":
		to = "RESOLVED"
	case "REOPEN":
		to = "OPEN"
	default:
		return nil, domain.NewBadRequest("invalid exception action", nil)
	}
	var current string
	if err := s.db.GetContext(ctx, &current, `SELECT planning_exception_current_status($1) FROM planning_exceptions WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("planning exception")
		}
		return nil, err
	}
	valid := (action == "ACKNOWLEDGE" && current == "OPEN") ||
		(action == "RESOLVE" && (current == "OPEN" || current == "ACKNOWLEDGED")) ||
		(action == "REOPEN" && current == "RESOLVED")
	if !valid {
		return nil, domain.NewConflict(fmt.Sprintf("%s is not valid while exception status is %s", action, current))
	}
	var row domain.PlanningExceptionAction
	err := s.db.GetContext(ctx, &row, `INSERT INTO planning_exception_actions(id,exception_id,action_type,from_status,to_status,actor_user_id,actor_username,comment) VALUES($1,$2,$3,'OPEN',$4,$5,$6,$7) RETURNING *`, uuid.New(), id, action, to, actor.UserID, actor.Username, strings.TrimSpace(in.Comment))
	if err != nil {
		return nil, err
	}
	return &row, nil
}

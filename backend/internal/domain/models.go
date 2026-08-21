package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ItemType — CPIM の品目区分
type ItemType string

const (
	ItemTypeFinished      ItemType = "FG" // Finished Good
	ItemTypeSubAssembly   ItemType = "SA" // Sub-Assembly
	ItemTypeRawMaterial   ItemType = "RM" // Raw Material
	ItemTypePurchasedPart ItemType = "PP" // Purchased Part
)

// Item — 品目マスタ
type Item struct {
	ID             uuid.UUID  `db:"id"               json:"id"`
	Code           string     `db:"code"             json:"code"             validate:"required,max=50"`
	Name           string     `db:"name"             json:"name"             validate:"required,max=200"`
	Type           ItemType   `db:"type"             json:"type"             validate:"required,oneof=FG SA RM PP"`
	UoM            string     `db:"uom"              json:"uom"              validate:"required,max=10"`
	LeadTimeDays   int        `db:"lead_time_days"   json:"leadTimeDays"     validate:"gte=0,lte=3650"`
	SafetyStock    float64    `db:"safety_stock"     json:"safetyStock"      validate:"gte=0"`
	LotSize        float64    `db:"lot_size"         json:"lotSize"          validate:"gt=0"`
	StandardCost   float64    `db:"standard_cost"    json:"standardCost"     validate:"gte=0"`
	LotSizeMethod  string     `db:"lot_size_method"  json:"lotSizeMethod"    validate:"oneof=LFL FOQ POQ EOQ"`
	PoqPeriods     int        `db:"poq_periods"      json:"poqPeriods"       validate:"gte=1,lte=52"`
	OrderingCost   float64    `db:"ordering_cost"    json:"orderingCost"     validate:"gte=0"`
	HoldingCostPct float64    `db:"holding_cost_pct" json:"holdingCostPct"   validate:"gte=0,lte=10"`
	LowLevelCode   int        `db:"low_level_code"   json:"lowLevelCode"`
	GroupID        *uuid.UUID `db:"group_id"         json:"groupId,omitempty"`
	CreatedAt      time.Time  `db:"created_at"       json:"createdAt"`
	UpdatedAt      time.Time  `db:"updated_at"       json:"updatedAt"`
}

// BOMComponent — BOM の1行 (親 → 子の関係)
type BOMComponent struct {
	ID       uuid.UUID `db:"id"         json:"id"`
	ParentID uuid.UUID `db:"parent_id"  json:"parentId"`
	ChildID  uuid.UUID `db:"child_id"   json:"childId"`
	Quantity float64   `db:"quantity"   json:"quantity"`
	ScrapPct float64   `db:"scrap_pct"  json:"scrapPct"`
}

// DemandForecast — 需要予測 / 受注
type DemandForecast struct {
	ID        uuid.UUID `db:"id"          json:"id"`
	ItemID    uuid.UUID `db:"item_id"     json:"itemId"`
	DueDate   time.Time `db:"due_date"    json:"dueDate"`
	Quantity  float64   `db:"quantity"    json:"quantity"`
	Source    string    `db:"source"      json:"source"` // "FORECAST" or "ORDER"
	CreatedAt time.Time `db:"created_at"  json:"createdAt"`
}

// MPSEntry — MPS (Master Production Schedule) の1エントリ
type MPSEntry struct {
	ID                           uuid.UUID  `db:"id"                                json:"id"`
	ItemID                       uuid.UUID  `db:"item_id"                           json:"itemId"`
	Period                       time.Time  `db:"period"                            json:"period"`
	Planned                      float64    `db:"planned"                           json:"planned"`
	Released                     float64    `db:"released"                          json:"released"`
	SourceForecastRunID          *uuid.UUID `db:"source_forecast_run_id"            json:"sourceForecastRunId,omitempty"`
	SourceSOPPlanID              *uuid.UUID `db:"source_sop_plan_id"                 json:"sourceSopPlanId,omitempty"`
	SourceSOPDisaggregationRunID *uuid.UUID `db:"source_sop_disaggregation_run_id"   json:"sourceSopDisaggregationRunId,omitempty"`
	SourceProductMixVersionID    *uuid.UUID `db:"source_product_mix_version_id"      json:"sourceProductMixVersionId,omitempty"`
	DemandBasis                  string     `db:"demand_basis"                      json:"demandBasis"`
}

// InventoryTxn — 在庫トランザクション
type InventoryTxn struct {
	ID         uuid.UUID  `db:"id"         json:"id"`
	ItemID     uuid.UUID  `db:"item_id"    json:"itemId"`
	Quantity   float64    `db:"quantity"   json:"quantity"` // +受入 / -出庫
	TxnType    string     `db:"txn_type"   json:"txnType"`  // RECEIPT,ISSUE,ADJUST
	RefDoc     string     `db:"ref_doc"     json:"refDoc"`
	OccurredAt time.Time  `db:"occurred_at" json:"occurredAt"`
	LotID      *uuid.UUID `db:"-"           json:"lotId,omitempty"`
	LotNo      string     `db:"-"           json:"lotNo,omitempty"`
}

// WorkOrder — 製造指示書
type WorkOrder struct {
	ID                  uuid.UUID  `db:"id"               json:"id"`
	OrderNo             string     `db:"order_no"         json:"orderNo"`
	ItemID              uuid.UUID  `db:"item_id"          json:"itemId"`
	Quantity            float64    `db:"quantity"         json:"quantity"`
	StartDate           time.Time  `db:"start_date"       json:"startDate"`
	DueDate             time.Time  `db:"due_date"         json:"dueDate"`
	Status              string     `db:"status"           json:"status"`
	CompletedQty        float64    `db:"completed_qty"          json:"completedQty"`
	ReportedProgressQty float64    `db:"reported_progress_qty"  json:"reportedProgressQty"`
	CreatedAt           time.Time  `db:"created_at"       json:"createdAt"`
	ProducedLotID       *uuid.UUID `db:"produced_lot_id"  json:"producedLotId,omitempty"`
	ReleasedAt          *time.Time `db:"released_at"      json:"releasedAt,omitempty"`
	CompletedAt         *time.Time `db:"completed_at"     json:"completedAt,omitempty"`
}

// PurchaseOrder — 購買発注
type PurchaseOrder struct {
	ID                      uuid.UUID  `db:"id"                json:"id"`
	PONo                    string     `db:"po_no"             json:"poNo"`
	ItemID                  uuid.UUID  `db:"item_id"           json:"itemId"`
	Supplier                string     `db:"supplier"          json:"supplier"`
	SupplierQualityStatus   string     `db:"supplier_quality_status" json:"supplierQualityStatus"`
	Quantity                float64    `db:"quantity"          json:"quantity"`
	ReceivedQty             float64    `db:"received_qty"      json:"receivedQty"`
	RemainingQty            float64    `db:"remaining_qty"     json:"remainingQty"`
	OrderDate               time.Time  `db:"order_date"        json:"orderDate"`
	DueDate                 time.Time  `db:"due_date"          json:"dueDate"`
	Status                  string     `db:"status"            json:"status"`
	ReceivedLotID           *uuid.UUID `db:"received_lot_id"   json:"receivedLotId,omitempty"`
	ReceivedAt              *time.Time `db:"received_at"       json:"receivedAt,omitempty"`
	ScheduleStatus          string     `db:"schedule_status"   json:"scheduleStatus"`
	ConfirmationEventID     *uuid.UUID `db:"confirmation_event_id" json:"confirmationEventId,omitempty"`
	ConfirmedQuantity       *float64   `db:"confirmed_quantity" json:"confirmedQuantity,omitempty"`
	ConfirmedDeliveryDate   *time.Time `db:"confirmed_delivery_date" json:"confirmedDeliveryDate,omitempty"`
	ASNEventID              *uuid.UUID `db:"asn_event_id"      json:"asnEventId,omitempty"`
	ASNNo                   string     `db:"asn_no"            json:"asnNo"`
	ASNQuantity             *float64   `db:"asn_quantity"      json:"asnQuantity,omitempty"`
	ASNExpectedArrivalDate  *time.Time `db:"asn_expected_arrival_date" json:"asnExpectedArrivalDate,omitempty"`
	ExpectedDeliveryDate    *time.Time `db:"expected_delivery_date" json:"expectedDeliveryDate,omitempty"`
	ScheduleSource          string     `db:"schedule_source"   json:"scheduleSource"`
	ReliabilitySampleCount  int        `db:"reliability_sample_count" json:"reliabilitySampleCount"`
	ReliabilityOnTimeRate   float64    `db:"reliability_on_time_rate" json:"reliabilityOnTimeRate"`
	ReliabilityP90Days      float64    `db:"reliability_p90_days" json:"reliabilityP90Days"`
	RecommendedLeadTimeDays int        `db:"recommended_lead_time_days" json:"recommendedLeadTimeDays"`
}

// SupplierScheduleEvent is append-only supplier commitment / ASN evidence.
type SupplierScheduleEvent struct {
	ID                    uuid.UUID  `db:"id"                      json:"id"`
	PurchaseOrderID       uuid.UUID  `db:"purchase_order_id"       json:"purchaseOrderId"`
	RevisionNo            int        `db:"revision_no"             json:"revisionNo"`
	EventType             string     `db:"event_type"              json:"eventType"`
	Quantity              *float64   `db:"quantity"                json:"quantity,omitempty"`
	ConfirmedDeliveryDate *time.Time `db:"confirmed_delivery_date" json:"confirmedDeliveryDate,omitempty"`
	ASNNo                 string     `db:"asn_no"                  json:"asnNo"`
	ExpectedArrivalDate   *time.Time `db:"expected_arrival_date"   json:"expectedArrivalDate,omitempty"`
	SupplierReference     string     `db:"supplier_reference"      json:"supplierReference"`
	Notes                 string     `db:"notes"                   json:"notes"`
	ActorUserID           uuid.UUID  `db:"actor_user_id"           json:"actorUserId"`
	ActorUsername         string     `db:"actor_username"          json:"actorUsername"`
	OccurredAt            time.Time  `db:"occurred_at"             json:"occurredAt"`
	CreatedAt             time.Time  `db:"created_at"              json:"createdAt"`
}

// SupplierLeadTimeRun is an immutable reliability calculation snapshot.
type SupplierLeadTimeRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	WindowStart       time.Time  `db:"window_start"         json:"windowStart"`
	WindowEnd         time.Time  `db:"window_end"           json:"windowEnd"`
	MinSamples        int        `db:"min_samples"          json:"minSamples"`
	Status            string     `db:"status"               json:"status"`
	ResultHash        *string    `db:"result_hash"          json:"resultHash,omitempty"`
	GeneratedByUserID uuid.UUID  `db:"generated_by_user_id" json:"generatedByUserId"`
	GeneratedBy       string     `db:"generated_by"         json:"generatedBy"`
	CompletedAt       *time.Time `db:"completed_at"         json:"completedAt,omitempty"`
	ErrorText         string     `db:"error_text"           json:"errorText"`
	CreatedAt         time.Time  `db:"created_at"           json:"createdAt"`
}

// SupplierLeadTimeResult contains supplier/item historical lead-time metrics.
type SupplierLeadTimeResult struct {
	ID                  uuid.UUID  `db:"id"                    json:"id"`
	RunID               uuid.UUID  `db:"run_id"                json:"runId"`
	SupplierName        string     `db:"supplier_name"         json:"supplierName"`
	ItemID              *uuid.UUID `db:"item_id"               json:"itemId,omitempty"`
	ItemCode            string     `db:"item_code"             json:"itemCode,omitempty"`
	SampleCount         int        `db:"sample_count"          json:"sampleCount"`
	AverageLeadDays     float64    `db:"average_lead_days"     json:"averageLeadDays"`
	StddevLeadDays      float64    `db:"stddev_lead_days"      json:"stddevLeadDays"`
	P50LeadDays         float64    `db:"p50_lead_days"         json:"p50LeadDays"`
	P90LeadDays         float64    `db:"p90_lead_days"         json:"p90LeadDays"`
	OnTimeRate          float64    `db:"on_time_rate"          json:"onTimeRate"`
	AverageLatenessDays float64    `db:"average_lateness_days" json:"averageLatenessDays"`
	RecommendedLeadDays int        `db:"recommended_lead_days" json:"recommendedLeadDays"`
	Confidence          string     `db:"confidence"            json:"confidence"`
	CreatedAt           time.Time  `db:"created_at"            json:"createdAt"`
}

// SupplierLeadTimeRunResult returns a reliability run plus its immutable rows.
type SupplierLeadTimeRunResult struct {
	Run     SupplierLeadTimeRun      `json:"run"`
	Results []SupplierLeadTimeResult `json:"results"`
}

// PurchaseReceipt is one immutable partial/full receipt event against a PO.
// receiptId is supplied by the client for idempotency and is globally unique.
type PurchaseReceipt struct {
	ID                 uuid.UUID  `db:"id"                   json:"receiptId"`
	PurchaseOrderID    uuid.UUID  `db:"purchase_order_id"    json:"purchaseOrderId"`
	PONo               string     `db:"po_no"                json:"poNo"`
	ItemID             uuid.UUID  `db:"item_id"              json:"itemId"`
	Quantity           float64    `db:"quantity"             json:"quantity"`
	LotID              uuid.UUID  `db:"lot_id"               json:"lotId"`
	LotNo              string     `db:"lot_no"               json:"lotNo"`
	InventoryTxnID     uuid.UUID  `db:"inventory_txn_id"     json:"inventoryTxnId"`
	ReceivedAt         time.Time  `db:"received_at"          json:"receivedAt"`
	ReceivedByUserID   *uuid.UUID `db:"received_by_user_id"  json:"receivedByUserId,omitempty"`
	ReceivedByUsername string     `db:"received_by_username" json:"receivedByUsername"`
	Source             string     `db:"source"               json:"source"`
}

// Customer is a customer master record used by Sales Order Management.
type Customer struct {
	ID               uuid.UUID  `db:"id"                 json:"id"`
	CustomerNo       string     `db:"customer_no"        json:"customerNo"`
	Name             string     `db:"name"               json:"name"`
	Status           string     `db:"status"             json:"status"`
	ServiceClassCode string     `db:"service_class_code" json:"serviceClassCode"`
	ShipTo           string     `db:"ship_to"            json:"shipTo"`
	Notes            string     `db:"notes"              json:"notes"`
	CreatedByUserID  *uuid.UUID `db:"created_by_user_id" json:"createdByUserId,omitempty"`
	CreatedBy        string     `db:"created_by"         json:"createdBy"`
	CreatedAt        time.Time  `db:"created_at"         json:"createdAt"`
	UpdatedAt        time.Time  `db:"updated_at"         json:"updatedAt"`
}

// SalesOrder is the header of a committed customer order. Quantities live on lines.
type SalesOrder struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	OrderNo           string     `db:"order_no"             json:"orderNo"`
	CustomerID        uuid.UUID  `db:"customer_id"          json:"customerId"`
	CustomerNo        string     `db:"customer_no"          json:"customerNo"`
	CustomerName      string     `db:"customer_name"        json:"customerName"`
	OrderDate         time.Time  `db:"order_date"           json:"orderDate"`
	RequestedDate     time.Time  `db:"requested_date"       json:"requestedDate"`
	PromisedDate      *time.Time `db:"promised_date"        json:"promisedDate,omitempty"`
	Status            string     `db:"status"               json:"status"`
	Priority          string     `db:"priority"             json:"priority"`
	Notes             string     `db:"notes"                json:"notes"`
	CreatedByUserID   *uuid.UUID `db:"created_by_user_id"   json:"createdByUserId,omitempty"`
	CreatedBy         string     `db:"created_by"           json:"createdBy"`
	ConfirmedByUserID *uuid.UUID `db:"confirmed_by_user_id" json:"confirmedByUserId,omitempty"`
	ConfirmedBy       *string    `db:"confirmed_by"         json:"confirmedBy,omitempty"`
	ConfirmedAt       *time.Time `db:"confirmed_at"         json:"confirmedAt,omitempty"`
	CancelledByUserID *uuid.UUID `db:"cancelled_by_user_id" json:"cancelledByUserId,omitempty"`
	CancelledBy       *string    `db:"cancelled_by"         json:"cancelledBy,omitempty"`
	CancelledAt       *time.Time `db:"cancelled_at"         json:"cancelledAt,omitempty"`
	CreatedAt         time.Time  `db:"created_at"           json:"createdAt"`
	UpdatedAt         time.Time  `db:"updated_at"           json:"updatedAt"`
	TotalQty          float64    `db:"total_qty"            json:"totalQty"`
	AllocatedQty      float64    `db:"allocated_qty"        json:"allocatedQty"`
	ShippedQty        float64    `db:"shipped_qty"          json:"shippedQty"`
	CancelledQty      float64    `db:"cancelled_qty"        json:"cancelledQty"`
	OpenQty           float64    `db:"open_qty"             json:"openQty"`
}

type SalesOrderLine struct {
	ID            uuid.UUID  `db:"id"              json:"id"`
	SalesOrderID  uuid.UUID  `db:"sales_order_id"  json:"salesOrderId"`
	LineNo        int        `db:"line_no"         json:"lineNo"`
	ItemID        uuid.UUID  `db:"item_id"         json:"itemId"`
	ItemCode      string     `db:"item_code"       json:"itemCode"`
	ItemName      string     `db:"item_name"       json:"itemName"`
	Quantity      float64    `db:"quantity"        json:"quantity"`
	AllocatedQty  float64    `db:"allocated_qty"   json:"allocatedQty"`
	ShippedQty    float64    `db:"shipped_qty"     json:"shippedQty"`
	CancelledQty  float64    `db:"cancelled_qty"   json:"cancelledQty"`
	OpenQty       float64    `db:"open_qty"        json:"openQty"`
	UnitPrice     float64    `db:"unit_price"      json:"unitPrice"`
	RequestedDate time.Time  `db:"requested_date"  json:"requestedDate"`
	PromisedDate  *time.Time `db:"promised_date"   json:"promisedDate,omitempty"`
	Notes         string     `db:"notes"           json:"notes"`
}

type SalesOrderStatusHistory struct {
	ID            uuid.UUID  `db:"id"             json:"id"`
	SalesOrderID  uuid.UUID  `db:"sales_order_id" json:"salesOrderId"`
	FromStatus    *string    `db:"from_status"    json:"fromStatus,omitempty"`
	ToStatus      string     `db:"to_status"      json:"toStatus"`
	ActorUserID   *uuid.UUID `db:"actor_user_id"  json:"actorUserId,omitempty"`
	ActorUsername string     `db:"actor_username" json:"actorUsername"`
	OccurredAt    time.Time  `db:"occurred_at"    json:"occurredAt"`
	Source        string     `db:"source"         json:"source"`
}

type SalesOrderShipment struct {
	ID                uuid.UUID  `db:"id"                  json:"shipmentId"`
	SalesOrderID      uuid.UUID  `db:"sales_order_id"      json:"salesOrderId"`
	SalesOrderLineID  uuid.UUID  `db:"sales_order_line_id" json:"salesOrderLineId"`
	Quantity          float64    `db:"quantity"            json:"quantity"`
	InventoryTxnID    uuid.UUID  `db:"inventory_txn_id"    json:"inventoryTxnId"`
	ShippedAt         time.Time  `db:"shipped_at"          json:"shippedAt"`
	ShippedByUserID   *uuid.UUID `db:"shipped_by_user_id"  json:"shippedByUserId,omitempty"`
	ShippedByUsername string     `db:"shipped_by_username" json:"shippedByUsername"`
	Carrier           string     `db:"carrier"             json:"carrier"`
	TrackingNo        string     `db:"tracking_no"         json:"trackingNo"`
}

type SalesOrderDetail struct {
	Order     SalesOrder                `json:"order"`
	Lines     []SalesOrderLine          `json:"lines"`
	History   []SalesOrderStatusHistory `json:"history"`
	Shipments []SalesOrderShipment      `json:"shipments"`
}

// OrderPromiseRun is one immutable ATP/CTP what-if evaluation request/result.
type OrderPromiseRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	SalesOrderID      uuid.UUID  `db:"sales_order_id"       json:"salesOrderId"`
	Strategy          string     `db:"strategy"             json:"strategy"`
	Status            string     `db:"status"               json:"status"`
	RequestedAt       time.Time  `db:"requested_at"         json:"requestedAt"`
	CompletedAt       *time.Time `db:"completed_at"         json:"completedAt,omitempty"`
	HorizonDays       int        `db:"horizon_days"         json:"horizonDays"`
	ResultHash        *string    `db:"result_hash"          json:"resultHash,omitempty"`
	ErrorText         string     `db:"error_text"           json:"errorText"`
	RequestedByUserID uuid.UUID  `db:"requested_by_user_id" json:"requestedByUserId"`
	RequestedBy       string     `db:"requested_by"         json:"requestedBy"`
	CreatedAt         time.Time  `db:"created_at"           json:"createdAt"`
}

type OrderPromiseLineResult struct {
	ID                uuid.UUID       `db:"id"                  json:"id"`
	RunID             uuid.UUID       `db:"run_id"              json:"runId"`
	SalesOrderLineID  uuid.UUID       `db:"sales_order_line_id" json:"salesOrderLineId"`
	RequestedQty      float64         `db:"requested_qty"       json:"requestedQty"`
	RequestedDate     time.Time       `db:"requested_date"      json:"requestedDate"`
	ATPQty            float64         `db:"atp_qty"             json:"atpQty"`
	CTPQty            float64         `db:"ctp_qty"             json:"ctpQty"`
	EarliestFullDate  *time.Time      `db:"earliest_full_date"  json:"earliestFullDate,omitempty"`
	PromiseMethod     string          `db:"promise_method"      json:"promiseMethod"`
	MaterialReadyDate *time.Time      `db:"material_ready_date" json:"materialReadyDate,omitempty"`
	CapacityReadyDate *time.Time      `db:"capacity_ready_date" json:"capacityReadyDate,omitempty"`
	ConstraintType    string          `db:"constraint_type"     json:"constraintType"`
	ConstraintDetail  json.RawMessage `db:"constraint_detail"   json:"constraintDetail"`
	CreatedAt         time.Time       `db:"created_at"          json:"createdAt"`
}

type OrderPromiseConfirmation struct {
	ID               uuid.UUID `db:"id"                  json:"id"`
	RunID            uuid.UUID `db:"run_id"              json:"runId"`
	SalesOrderLineID uuid.UUID `db:"sales_order_line_id" json:"salesOrderLineId"`
	SequenceNo       int       `db:"sequence_no"         json:"sequenceNo"`
	Quantity         float64   `db:"quantity"            json:"quantity"`
	ConfirmedDate    time.Time `db:"confirmed_date"      json:"confirmedDate"`
	Source           string    `db:"source"              json:"source"`
	CreatedAt        time.Time `db:"created_at"          json:"createdAt"`
}

type OrderPromiseAcceptance struct {
	ID               uuid.UUID `db:"id"                  json:"id"`
	RunID            uuid.UUID `db:"run_id"              json:"runId"`
	SalesOrderID     uuid.UUID `db:"sales_order_id"      json:"salesOrderId"`
	ResultHash       string    `db:"result_hash"         json:"resultHash"`
	AcceptedByUserID uuid.UUID `db:"accepted_by_user_id" json:"acceptedByUserId"`
	AcceptedBy       string    `db:"accepted_by"         json:"acceptedBy"`
	AcceptedAt       time.Time `db:"accepted_at"         json:"acceptedAt"`
}

type OrderPromiseResult struct {
	Run           OrderPromiseRun            `json:"run"`
	Lines         []OrderPromiseLineResult   `json:"lines"`
	Confirmations []OrderPromiseConfirmation `json:"confirmations"`
	Acceptance    *OrderPromiseAcceptance    `json:"acceptance,omitempty"`
}

// CustomerServiceClass ranks customer demand for Backorder Processing.
type CustomerServiceClass struct {
	Code         string    `db:"code"          json:"code"`
	Name         string    `db:"name"          json:"name"`
	PriorityRank int       `db:"priority_rank" json:"priorityRank"`
	IsActive     bool      `db:"is_active"     json:"isActive"`
	CreatedAt    time.Time `db:"created_at"    json:"createdAt"`
}

// ProductAllocationPlan reserves a percentage of scarce ATP for service classes.
type ProductAllocationPlan struct {
	ID                  uuid.UUID  `db:"id"                       json:"id"`
	ItemID              uuid.UUID  `db:"item_id"                  json:"itemId"`
	ItemCode            string     `db:"item_code"                json:"itemCode"`
	ItemName            string     `db:"item_name"                json:"itemName"`
	Name                string     `db:"name"                     json:"name"`
	EffectiveFrom       time.Time  `db:"effective_from"           json:"effectiveFrom"`
	EffectiveTo         time.Time  `db:"effective_to"             json:"effectiveTo"`
	Status              string     `db:"status"                   json:"status"`
	CreatedByUserID     uuid.UUID  `db:"created_by_user_id"       json:"createdByUserId"`
	CreatedBy           string     `db:"created_by"               json:"createdBy"`
	ActivatedByUserID   *uuid.UUID `db:"activated_by_user_id"     json:"activatedByUserId,omitempty"`
	ActivatedBy         *string    `db:"activated_by"             json:"activatedBy,omitempty"`
	ActivatedAt         *time.Time `db:"activated_at"             json:"activatedAt,omitempty"`
	DeactivatedByUserID *uuid.UUID `db:"deactivated_by_user_id"   json:"deactivatedByUserId,omitempty"`
	DeactivatedBy       *string    `db:"deactivated_by"           json:"deactivatedBy,omitempty"`
	DeactivatedAt       *time.Time `db:"deactivated_at"           json:"deactivatedAt,omitempty"`
	CreatedAt           time.Time  `db:"created_at"               json:"createdAt"`
	UpdatedAt           time.Time  `db:"updated_at"               json:"updatedAt"`
}

type ProductAllocationBucket struct {
	ID               uuid.UUID `db:"id"                 json:"id"`
	PlanID           uuid.UUID `db:"plan_id"            json:"planId"`
	ServiceClassCode string    `db:"service_class_code" json:"serviceClassCode"`
	AllocationPct    float64   `db:"allocation_pct"     json:"allocationPct"`
	PriorityRank     int       `db:"priority_rank"      json:"priorityRank"`
}

type ProductAllocationPlanDetail struct {
	Plan    ProductAllocationPlan     `json:"plan"`
	Buckets []ProductAllocationBucket `json:"buckets"`
}

// BackorderRun is one immutable Preview calculation across committed Sales Orders.
type BackorderRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	Status            string     `db:"status"               json:"status"`
	HorizonDays       int        `db:"horizon_days"         json:"horizonDays"`
	FilterItemID      *uuid.UUID `db:"filter_item_id"       json:"filterItemId,omitempty"`
	RequestedAt       time.Time  `db:"requested_at"         json:"requestedAt"`
	CompletedAt       *time.Time `db:"completed_at"         json:"completedAt,omitempty"`
	ResultHash        *string    `db:"result_hash"          json:"resultHash,omitempty"`
	ErrorText         string     `db:"error_text"           json:"errorText"`
	RequestedByUserID uuid.UUID  `db:"requested_by_user_id" json:"requestedByUserId"`
	RequestedBy       string     `db:"requested_by"         json:"requestedBy"`
	CreatedAt         time.Time  `db:"created_at"           json:"createdAt"`
}

type BackorderRunLine struct {
	ID                   uuid.UUID       `db:"id"                    json:"id"`
	RunID                uuid.UUID       `db:"run_id"                json:"runId"`
	SalesOrderID         uuid.UUID       `db:"sales_order_id"        json:"salesOrderId"`
	SalesOrderNo         string          `db:"sales_order_no"        json:"salesOrderNo"`
	SalesOrderLineID     uuid.UUID       `db:"sales_order_line_id"   json:"salesOrderLineId"`
	ItemID               uuid.UUID       `db:"item_id"               json:"itemId"`
	ItemCode             string          `db:"item_code"             json:"itemCode"`
	ItemName             string          `db:"item_name"             json:"itemName"`
	CustomerID           uuid.UUID       `db:"customer_id"           json:"customerId"`
	CustomerNo           string          `db:"customer_no"           json:"customerNo"`
	CustomerName         string          `db:"customer_name"         json:"customerName"`
	ServiceClassCode     string          `db:"service_class_code"    json:"serviceClassCode"`
	OrderPriority        string          `db:"order_priority"        json:"orderPriority"`
	RankNo               int             `db:"rank_no"               json:"rankNo"`
	OpenQty              float64         `db:"open_qty"              json:"openQty"`
	AllocatedQty         float64         `db:"allocated_qty"         json:"allocatedQty"`
	CurrentPromisedDate  *time.Time      `db:"current_promised_date" json:"currentPromisedDate,omitempty"`
	ProposedPromisedDate *time.Time      `db:"proposed_promised_date" json:"proposedPromisedDate,omitempty"`
	ATPQty               float64         `db:"atp_qty"               json:"atpQty"`
	CTPQty               float64         `db:"ctp_qty"               json:"ctpQty"`
	BackorderQty         float64         `db:"backorder_qty"         json:"backorderQty"`
	Decision             string          `db:"decision"              json:"decision"`
	ConstraintType       string          `db:"constraint_type"       json:"constraintType"`
	AllocationPlanID     *uuid.UUID      `db:"allocation_plan_id"    json:"allocationPlanId,omitempty"`
	AllocationBucketPct  *float64        `db:"allocation_bucket_pct" json:"allocationBucketPct,omitempty"`
	Detail               json.RawMessage `db:"detail"                json:"detail"`
	CreatedAt            time.Time       `db:"created_at"            json:"createdAt"`
}

type BackorderRunConfirmation struct {
	ID               uuid.UUID `db:"id"                  json:"id"`
	RunID            uuid.UUID `db:"run_id"              json:"runId"`
	SalesOrderLineID uuid.UUID `db:"sales_order_line_id" json:"salesOrderLineId"`
	SequenceNo       int       `db:"sequence_no"         json:"sequenceNo"`
	Quantity         float64   `db:"quantity"            json:"quantity"`
	ConfirmedDate    time.Time `db:"confirmed_date"      json:"confirmedDate"`
	Source           string    `db:"source"              json:"source"`
	CreatedAt        time.Time `db:"created_at"          json:"createdAt"`
}

type BackorderPublication struct {
	ID                uuid.UUID `db:"id"                   json:"id"`
	RunID             uuid.UUID `db:"run_id"               json:"runId"`
	ResultHash        string    `db:"result_hash"          json:"resultHash"`
	PublishedByUserID uuid.UUID `db:"published_by_user_id" json:"publishedByUserId"`
	PublishedBy       string    `db:"published_by"         json:"publishedBy"`
	PublishedAt       time.Time `db:"published_at"         json:"publishedAt"`
}

type BackorderResult struct {
	Run           BackorderRun               `json:"run"`
	Lines         []BackorderRunLine         `json:"lines"`
	Confirmations []BackorderRunConfirmation `json:"confirmations"`
	Publication   *BackorderPublication      `json:"publication,omitempty"`
}

// PeggingRun is an immutable point-in-time full-pegging snapshot for one Sales Order.
type PeggingRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	SalesOrderID      uuid.UUID  `db:"sales_order_id"       json:"salesOrderId"`
	Status            string     `db:"status"               json:"status"`
	AsOf              time.Time  `db:"as_of"                json:"asOf"`
	HorizonDays       int        `db:"horizon_days"         json:"horizonDays"`
	ResultHash        *string    `db:"result_hash"          json:"resultHash,omitempty"`
	ErrorText         string     `db:"error_text"           json:"errorText"`
	GeneratedByUserID uuid.UUID  `db:"generated_by_user_id" json:"generatedByUserId"`
	GeneratedBy       string     `db:"generated_by"         json:"generatedBy"`
	CompletedAt       *time.Time `db:"completed_at"         json:"completedAt,omitempty"`
	CreatedAt         time.Time  `db:"created_at"           json:"createdAt"`
}

// PeggingNode is a demand, supply, material, quality or capacity node in a pegging graph.
type PeggingNode struct {
	ID        uuid.UUID       `db:"id"         json:"id"`
	RunID     uuid.UUID       `db:"run_id"     json:"runId"`
	NodeKey   string          `db:"node_key"   json:"nodeKey"`
	NodeType  string          `db:"node_type"  json:"nodeType"`
	EntityID  *uuid.UUID      `db:"entity_id"  json:"entityId,omitempty"`
	EntityRef string          `db:"entity_ref" json:"entityRef"`
	ItemID    *uuid.UUID      `db:"item_id"    json:"itemId,omitempty"`
	ItemCode  string          `db:"item_code"  json:"itemCode"`
	Label     string          `db:"label"      json:"label"`
	Quantity  *float64        `db:"quantity"   json:"quantity,omitempty"`
	DueDate   *time.Time      `db:"due_date"   json:"dueDate,omitempty"`
	Status    string          `db:"status"     json:"status"`
	Detail    json.RawMessage `db:"detail"     json:"detail"`
	CreatedAt time.Time       `db:"created_at" json:"createdAt"`
}

// PeggingEdge describes one causal/supply relationship between graph nodes.
type PeggingEdge struct {
	ID         uuid.UUID       `db:"id"           json:"id"`
	RunID      uuid.UUID       `db:"run_id"       json:"runId"`
	FromNodeID uuid.UUID       `db:"from_node_id" json:"fromNodeId"`
	ToNodeID   uuid.UUID       `db:"to_node_id"   json:"toNodeId"`
	EdgeType   string          `db:"edge_type"    json:"edgeType"`
	Quantity   *float64        `db:"quantity"     json:"quantity,omitempty"`
	Detail     json.RawMessage `db:"detail"       json:"detail"`
	CreatedAt  time.Time       `db:"created_at"   json:"createdAt"`
}

// PlanningException is immutable detection evidence produced by a pegging run.
type PlanningException struct {
	ID               uuid.UUID       `db:"id"                    json:"id"`
	RunID            uuid.UUID       `db:"run_id"                json:"runId"`
	SalesOrderID     uuid.UUID       `db:"sales_order_id"        json:"salesOrderId"`
	SalesOrderLineID *uuid.UUID      `db:"sales_order_line_id"   json:"salesOrderLineId,omitempty"`
	ExceptionKey     string          `db:"exception_key"         json:"exceptionKey"`
	ExceptionType    string          `db:"exception_type"        json:"exceptionType"`
	Severity         string          `db:"severity"              json:"severity"`
	RootNodeID       uuid.UUID       `db:"root_node_id"          json:"rootNodeId"`
	Message          string          `db:"message"               json:"message"`
	RequestedDate    *time.Time      `db:"requested_date"        json:"requestedDate,omitempty"`
	PromisedDate     *time.Time      `db:"promised_date"         json:"promisedDate,omitempty"`
	ImpactDate       *time.Time      `db:"impact_date"           json:"impactDate,omitempty"`
	ImpactDays       int             `db:"impact_days"           json:"impactDays"`
	RootCausePath    json.RawMessage `db:"root_cause_path"       json:"rootCausePath"`
	Detail           json.RawMessage `db:"detail"                json:"detail"`
	DetectedAt       time.Time       `db:"detected_at"           json:"detectedAt"`
	CurrentStatus    string          `db:"current_status"        json:"currentStatus,omitempty"`
	SalesOrderNo     string          `db:"sales_order_no"        json:"salesOrderNo,omitempty"`
	CustomerNo       string          `db:"customer_no"           json:"customerNo,omitempty"`
	CustomerName     string          `db:"customer_name"         json:"customerName,omitempty"`
	LineNo           *int            `db:"line_no"               json:"lineNo,omitempty"`
	ItemCode         string          `db:"item_code"             json:"itemCode,omitempty"`
	ItemName         string          `db:"item_name"             json:"itemName,omitempty"`
	LatestActionType *string         `db:"latest_action_type"    json:"latestActionType,omitempty"`
	LatestActor      *string         `db:"latest_actor"          json:"latestActor,omitempty"`
	LatestComment    *string         `db:"latest_comment"        json:"latestComment,omitempty"`
	LatestActionAt   *time.Time      `db:"latest_action_at"      json:"latestActionAt,omitempty"`
}

// PlanningExceptionAction is append-only acknowledgement/resolution evidence.
type PlanningExceptionAction struct {
	ID            uuid.UUID `db:"id"             json:"id"`
	ExceptionID   uuid.UUID `db:"exception_id"   json:"exceptionId"`
	ActionType    string    `db:"action_type"    json:"actionType"`
	FromStatus    string    `db:"from_status"    json:"fromStatus"`
	ToStatus      string    `db:"to_status"      json:"toStatus"`
	ActorUserID   uuid.UUID `db:"actor_user_id"  json:"actorUserId"`
	ActorUsername string    `db:"actor_username" json:"actorUsername"`
	Comment       string    `db:"comment"        json:"comment"`
	OccurredAt    time.Time `db:"occurred_at"    json:"occurredAt"`
}

// PeggingResult is the full immutable graph plus its detected exceptions.
type PeggingResult struct {
	Run        PeggingRun          `json:"run"`
	Nodes      []PeggingNode       `json:"nodes"`
	Edges      []PeggingEdge       `json:"edges"`
	Exceptions []PlanningException `json:"exceptions"`
}

// ExceptionScanResult is returned by a global scan across committed open orders.
type ExceptionScanResult struct {
	PeggingRuns []PeggingRun        `json:"peggingRuns"`
	Exceptions  []PlanningException `json:"exceptions"`
}

// MRPResult — MRP計算結果 (1品目1期間)
type MRPResult struct {
	ItemID               uuid.UUID  `json:"itemId"`
	ItemCode             string     `json:"itemCode"`
	Period               time.Time  `json:"period"` // gross requirement / planned receipt date
	GrossReq             float64    `json:"grossRequirement"`
	ScheduledRcpt        float64    `json:"scheduledReceipts"`
	OnHand               float64    `json:"projectedOnHand"` // period-end projected available balance
	NetReq               float64    `json:"netRequirement"`
	PlannedReceipt       float64    `json:"plannedOrderReceipt"`
	PlannedOrder         float64    `json:"plannedOrderRelease"` // quantity; kept for API compatibility
	PlannedReleaseDate   *time.Time `json:"plannedOrderReleaseDate,omitempty"`
	PlanningLeadTimeDays int        `json:"planningLeadTimeDays"`
	LeadTimeSource       string     `json:"leadTimeSource"`
	LotMethod            string     `json:"lotMethod"`         // LFL/FOQ/POQ/EOQ
	EOQ                  float64    `json:"eoq,omitempty"`     // calculated EOQ (informational)
	Pegging              []string   `json:"pegging,omitempty"` // originating MPS item codes
}

// ====================================================================
// CRP / Routing
// ====================================================================

// WorkCenter — 作業区マスタ
type WorkCenter struct {
	ID                    uuid.UUID  `db:"id"                       json:"id"`
	Code                  string     `db:"code"                     json:"code"`
	Name                  string     `db:"name"                     json:"name"`
	CapacityMinutesPerDay float64    `db:"capacity_minutes_per_day" json:"capacityMinutesPerDay"`
	Efficiency            float64    `db:"efficiency"               json:"efficiency"`
	Utilization           float64    `db:"utilization"              json:"utilization"`
	LaborRatePerMinute    float64    `db:"labor_rate_per_minute"    json:"laborRatePerMinute"`
	OverheadRatePerMinute float64    `db:"overhead_rate_per_minute" json:"overheadRatePerMinute"`
	CalendarID            *uuid.UUID `db:"calendar_id"              json:"calendarId,omitempty"`
	ShiftStartMinute      int        `db:"shift_start_minute"        json:"shiftStartMinute"`
	MachineCount          int        `db:"machine_count"             json:"machineCount"`
	WorkerCount           int        `db:"worker_count"              json:"workerCount"`
	CreatedAt             time.Time  `db:"created_at"               json:"createdAt"`
}

// EffectiveCapacity = capacity * efficiency * utilization
func (w WorkCenter) EffectiveCapacityMin() float64 {
	machines := w.MachineCount
	if machines <= 0 {
		machines = 1
	}
	return w.CapacityMinutesPerDay * float64(machines) * w.Efficiency * w.Utilization
}

// Routing — 1品目に対する有効ルーティング
type Routing struct {
	ID          uuid.UUID `db:"id"          json:"id"`
	ItemID      uuid.UUID `db:"item_id"     json:"itemId"`
	Description string    `db:"description" json:"description"`
	IsActive    bool      `db:"is_active"   json:"isActive"`
	CreatedAt   time.Time `db:"created_at"  json:"createdAt"`
}

// RoutingOperation — ルーティングの1工程
type RoutingOperation struct {
	ID                uuid.UUID `db:"id"                   json:"id"`
	RoutingID         uuid.UUID `db:"routing_id"           json:"routingId"`
	SeqNo             int       `db:"seq_no"               json:"seqNo"`
	WorkCenterID      uuid.UUID `db:"work_center_id"       json:"workCenterId"`
	Description       string    `db:"description"          json:"description"`
	SetupMinutes      float64   `db:"setup_minutes"        json:"setupMinutes"`
	RunMinutesPerUnit float64   `db:"run_minutes_per_unit" json:"runMinutesPerUnit"`
	SetupFamily       string    `db:"setup_family"         json:"setupFamily"`
	OverlapEnabled    bool      `db:"overlap_enabled"      json:"overlapEnabled"`
	TransferBatchQty  float64   `db:"transfer_batch_qty"   json:"transferBatchQty"`
	MachinesRequired  int       `db:"machines_required"    json:"machinesRequired"`
	WorkersRequired   int       `db:"workers_required"     json:"workersRequired"`
}

// CapacityLoadRow — CRP 結果 (作業区×日)
type RoutingOperationAlternative struct {
	ID                  uuid.UUID `db:"id"                     json:"id"`
	RoutingOperationID  uuid.UUID `db:"routing_operation_id"   json:"routingOperationId"`
	WorkCenterID        uuid.UUID `db:"work_center_id"         json:"workCenterId"`
	Priority            int       `db:"priority"               json:"priority"`
	RunTimeMultiplier   float64   `db:"run_time_multiplier"    json:"runTimeMultiplier"`
	SetupTimeMultiplier float64   `db:"setup_time_multiplier"  json:"setupTimeMultiplier"`
	IsActive            bool      `db:"is_active"              json:"isActive"`
	CreatedAt           time.Time `db:"created_at"             json:"createdAt"`
}

type WorkCenterSetupMatrixRow struct {
	ID              uuid.UUID `db:"id"                json:"id"`
	WorkCenterID    uuid.UUID `db:"work_center_id"    json:"workCenterId"`
	FromSetupFamily string    `db:"from_setup_family" json:"fromSetupFamily"`
	ToSetupFamily   string    `db:"to_setup_family"   json:"toSetupFamily"`
	SetupMinutes    float64   `db:"setup_minutes"     json:"setupMinutes"`
	CreatedAt       time.Time `db:"created_at"        json:"createdAt"`
}

type CapacityLoadRow struct {
	WorkCenterID     uuid.UUID `json:"workCenterId"`
	WorkCenterCode   string    `json:"workCenterCode"`
	WorkCenterName   string    `json:"workCenterName"`
	Date             time.Time `json:"date"`
	RequiredMinutes  float64   `json:"requiredMinutes"`
	AvailableMinutes float64   `json:"availableMinutes"`
	LoadPct          float64   `json:"loadPct"`
	IsHoliday        bool      `json:"isHoliday,omitempty"`
}

// CRP finite-capacity scheduling snapshot. The scheduler reserves actual clock
// intervals while accounting for each work center's efficiency/utilization and
// calendar. Released/in-progress WOs are treated as FIRM load; MRP planned
// orders are then placed into the remaining capacity.
type CRPScheduleRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	StartDate         time.Time  `db:"start_date"           json:"startDate"`
	EndDate           time.Time  `db:"end_date"             json:"endDate"`
	HorizonDays       int        `db:"horizon_days"         json:"horizonDays"`
	Mode              string     `db:"mode"                 json:"mode"`
	Status            string     `db:"status"               json:"status"`
	GeneratedAt       time.Time  `db:"generated_at"         json:"generatedAt"`
	GeneratedByUserID *uuid.UUID `db:"generated_by_user_id" json:"generatedByUserId,omitempty"`
	GeneratedBy       string     `db:"generated_by"         json:"generatedBy"`
}

type CRPScheduleOrder struct {
	ID                 uuid.UUID  `db:"id"                    json:"id"`
	RunID              uuid.UUID  `db:"run_id"                json:"runId"`
	SourceType         string     `db:"source_type"           json:"sourceType"`
	SourceRef          string     `db:"source_ref"            json:"sourceRef"`
	WorkOrderID        *uuid.UUID `db:"work_order_id"         json:"workOrderId,omitempty"`
	ItemID             uuid.UUID  `db:"item_id"               json:"itemId"`
	ItemCode           string     `db:"item_code"             json:"itemCode"`
	Quantity           float64    `db:"quantity"              json:"quantity"`
	Priority           int        `db:"priority"              json:"priority"`
	EarliestStart      time.Time  `db:"earliest_start"        json:"earliestStart"`
	DueAt              time.Time  `db:"due_at"                json:"dueAt"`
	ScheduledStart     *time.Time `db:"scheduled_start"       json:"scheduledStart,omitempty"`
	ScheduledEnd       *time.Time `db:"scheduled_end"         json:"scheduledEnd,omitempty"`
	RequiredMinutes    float64    `db:"required_minutes"      json:"requiredMinutes"`
	ScheduledMinutes   float64    `db:"scheduled_minutes"     json:"scheduledMinutes"`
	UnscheduledMinutes float64    `db:"unscheduled_minutes"   json:"unscheduledMinutes"`
	TardyMinutes       float64    `db:"tardy_minutes"         json:"tardyMinutes"`
	ScheduleStatus     string     `db:"schedule_status"       json:"scheduleStatus"`
}

type CRPScheduleSegment struct {
	ID                uuid.UUID `db:"id"                   json:"id"`
	RunID             uuid.UUID `db:"run_id"               json:"runId"`
	ScheduleOrderID   uuid.UUID `db:"schedule_order_id"    json:"scheduleOrderId"`
	SourceType        string    `db:"source_type"          json:"sourceType"`
	SourceRef         string    `db:"source_ref"           json:"sourceRef"`
	ItemID            uuid.UUID `db:"item_id"              json:"itemId"`
	ItemCode          string    `db:"item_code"            json:"itemCode"`
	OperationSeq      int       `db:"operation_seq"        json:"operationSeq"`
	OperationDesc     string    `db:"operation_desc"       json:"operationDescription"`
	WorkCenterID      uuid.UUID `db:"work_center_id"       json:"workCenterId"`
	WorkCenterCode    string    `db:"work_center_code"     json:"workCenterCode"`
	WorkCenterName    string    `db:"work_center_name"     json:"workCenterName"`
	SegmentNo         int       `db:"segment_no"           json:"segmentNo"`
	StartAt           time.Time `db:"start_at"             json:"startAt"`
	EndAt             time.Time `db:"end_at"               json:"endAt"`
	LoadMinutes       float64   `db:"load_minutes"         json:"loadMinutes"`
	ClockMinutes      float64   `db:"clock_minutes"        json:"clockMinutes"`
	EffectiveLoadRate float64   `db:"effective_load_rate"  json:"effectiveLoadRate"`
	Firm              bool      `db:"firm"                 json:"firm"`
}

type CRPFiniteSummary struct {
	FirmOrders        int     `json:"firmOrders"`
	PlannedOrders     int     `json:"plannedOrders"`
	ScheduledOrders   int     `json:"scheduledOrders"`
	LateOrders        int     `json:"lateOrders"`
	UnscheduledOrders int     `json:"unscheduledOrders"`
	ScheduledSegments int     `json:"scheduledSegments"`
	TotalLoadMinutes  float64 `json:"totalLoadMinutes"`
}

type CRPFiniteScheduleResult struct {
	Run      CRPScheduleRun       `json:"run"`
	Summary  CRPFiniteSummary     `json:"summary"`
	Orders   []CRPScheduleOrder   `json:"orders"`
	Segments []CRPScheduleSegment `json:"segments"`
	Loads    []CapacityLoadRow    `json:"loads"`
}

// ====================================================================
// Detailed Scheduling
// ====================================================================

type DetailedScheduleRun struct {
	ID                uuid.UUID  `db:"id"                    json:"id"`
	StartDate         time.Time  `db:"start_date"            json:"startDate"`
	EndDate           time.Time  `db:"end_date"              json:"endDate"`
	HorizonDays       int        `db:"horizon_days"          json:"horizonDays"`
	Mode              string     `db:"mode"                  json:"mode"`
	Status            string     `db:"status"                json:"status"`
	GeneratedAt       time.Time  `db:"generated_at"          json:"generatedAt"`
	GeneratedByUserID *uuid.UUID `db:"generated_by_user_id"  json:"generatedByUserId,omitempty"`
	GeneratedBy       string     `db:"generated_by"          json:"generatedBy"`
}

type DetailedScheduleOrder struct {
	ID             uuid.UUID  `db:"id"                json:"id"`
	RunID          uuid.UUID  `db:"run_id"            json:"runId"`
	SourceType     string     `db:"source_type"       json:"sourceType"`
	SourceRef      string     `db:"source_ref"        json:"sourceRef"`
	WorkOrderID    *uuid.UUID `db:"work_order_id"     json:"workOrderId,omitempty"`
	ItemID         uuid.UUID  `db:"item_id"           json:"itemId"`
	ItemCode       string     `db:"item_code"         json:"itemCode"`
	Quantity       float64    `db:"quantity"          json:"quantity"`
	Priority       int        `db:"priority"          json:"priority"`
	EarliestStart  time.Time  `db:"earliest_start"    json:"earliestStart"`
	DueAt          time.Time  `db:"due_at"            json:"dueAt"`
	ScheduledStart *time.Time `db:"scheduled_start"   json:"scheduledStart,omitempty"`
	ScheduledEnd   *time.Time `db:"scheduled_end"     json:"scheduledEnd,omitempty"`
	ScheduleStatus string     `db:"schedule_status"   json:"scheduleStatus"`
	TardyMinutes   float64    `db:"tardy_minutes"     json:"tardyMinutes"`
}

type DetailedScheduleBatch struct {
	ID                      uuid.UUID  `db:"id"                         json:"id"`
	RunID                   uuid.UUID  `db:"run_id"                     json:"runId"`
	ScheduleOrderID         uuid.UUID  `db:"schedule_order_id"          json:"scheduleOrderId"`
	OperationSeq            int        `db:"operation_seq"              json:"operationSeq"`
	OperationDesc           string     `db:"operation_desc"             json:"operationDescription"`
	BatchNo                 int        `db:"batch_no"                   json:"batchNo"`
	BatchQty                float64    `db:"batch_qty"                  json:"batchQty"`
	CumulativeQty           float64    `db:"cumulative_qty"             json:"cumulativeQty"`
	SetupFamily             string     `db:"setup_family"               json:"setupFamily"`
	WorkCenterID            *uuid.UUID `db:"work_center_id"             json:"workCenterId,omitempty"`
	WorkCenterCode          string     `db:"work_center_code"           json:"workCenterCode"`
	WorkCenterName          string     `db:"work_center_name"           json:"workCenterName"`
	PrimaryWorkCenter       bool       `db:"primary_work_center"        json:"primaryWorkCenter"`
	AlternativePriority     int        `db:"alternative_priority"       json:"alternativePriority"`
	MachineCapacitySnapshot int        `db:"machine_capacity_snapshot" json:"machineCapacitySnapshot"`
	WorkerCapacitySnapshot  int        `db:"worker_capacity_snapshot"  json:"workerCapacitySnapshot"`
	MachinesRequired        int        `db:"machines_required"          json:"machinesRequired"`
	WorkersRequired         int        `db:"workers_required"           json:"workersRequired"`
	SequenceSetupMinutes    float64    `db:"sequence_setup_minutes"     json:"sequenceSetupMinutes"`
	RunClockMinutes         float64    `db:"run_clock_minutes"          json:"runClockMinutes"`
	ScheduledStart          *time.Time `db:"scheduled_start"            json:"scheduledStart,omitempty"`
	ScheduledEnd            *time.Time `db:"scheduled_end"              json:"scheduledEnd,omitempty"`
	ScheduleStatus          string     `db:"schedule_status"            json:"scheduleStatus"`
	MachineLanes            []int      `db:"-"                          json:"machineLanes,omitempty"`
}

type DetailedScheduleDependency struct {
	BatchID            uuid.UUID `db:"batch_id"             json:"batchId"`
	PredecessorBatchID uuid.UUID `db:"predecessor_batch_id" json:"predecessorBatchId"`
	DependencyType     string    `db:"dependency_type"      json:"dependencyType"`
}

type DetailedScheduleSegment struct {
	ID                      uuid.UUID `db:"id"                         json:"id"`
	RunID                   uuid.UUID `db:"run_id"                     json:"runId"`
	BatchID                 uuid.UUID `db:"batch_id"                   json:"batchId"`
	ScheduleOrderID         uuid.UUID `db:"schedule_order_id"          json:"scheduleOrderId"`
	OperationSeq            int       `db:"operation_seq"              json:"operationSeq"`
	BatchNo                 int       `db:"batch_no"                   json:"batchNo"`
	SegmentNo               int       `db:"segment_no"                 json:"segmentNo"`
	SegmentType             string    `db:"segment_type"               json:"segmentType"`
	WorkCenterID            uuid.UUID `db:"work_center_id"             json:"workCenterId"`
	WorkCenterCode          string    `db:"-"                          json:"workCenterCode,omitempty"`
	StartAt                 time.Time `db:"start_at"                   json:"startAt"`
	EndAt                   time.Time `db:"end_at"                     json:"endAt"`
	MachinesRequired        int       `db:"machines_required"          json:"machinesRequired"`
	WorkersRequired         int       `db:"workers_required"           json:"workersRequired"`
	MachineCapacitySnapshot int       `db:"machine_capacity_snapshot" json:"machineCapacitySnapshot"`
	WorkerCapacitySnapshot  int       `db:"worker_capacity_snapshot"  json:"workerCapacitySnapshot"`
	SetupFamily             string    `db:"setup_family"               json:"setupFamily"`
	FromSetupFamily         string    `db:"from_setup_family"          json:"fromSetupFamily"`
	ClockMinutes            float64   `db:"clock_minutes"              json:"clockMinutes"`
	Firm                    bool      `db:"firm"                       json:"firm"`
	MachineLanes            []int     `db:"-"                          json:"machineLanes,omitempty"`
}

type DetailedScheduleSummary struct {
	FirmOrders        int     `json:"firmOrders"`
	PlannedOrders     int     `json:"plannedOrders"`
	ScheduledOrders   int     `json:"scheduledOrders"`
	LateOrders        int     `json:"lateOrders"`
	UnscheduledOrders int     `json:"unscheduledOrders"`
	AlternativeUses   int     `json:"alternativeUses"`
	TransferBatches   int     `json:"transferBatches"`
	SetupMinutes      float64 `json:"setupMinutes"`
	RunMinutes        float64 `json:"runMinutes"`
	PeakWorkers       int     `json:"peakWorkers"`
}

type DetailedScheduleResult struct {
	Run          DetailedScheduleRun          `json:"run"`
	Summary      DetailedScheduleSummary      `json:"summary"`
	Orders       []DetailedScheduleOrder      `json:"orders"`
	Batches      []DetailedScheduleBatch      `json:"batches"`
	Dependencies []DetailedScheduleDependency `json:"dependencies"`
	Segments     []DetailedScheduleSegment    `json:"segments"`
	Loads        []CapacityLoadRow            `json:"loads"`
}

// ====================================================================
// Working Calendar
// ====================================================================

// WorkCalendar — 標準週次パターン (月〜日の稼働分数)
type WorkCalendar struct {
	ID           uuid.UUID `db:"id"            json:"id"`
	Code         string    `db:"code"          json:"code"`
	Name         string    `db:"name"          json:"name"`
	IsDefault    bool      `db:"is_default"    json:"isDefault"`
	MondayMin    int       `db:"monday_min"    json:"mondayMin"`
	TuesdayMin   int       `db:"tuesday_min"   json:"tuesdayMin"`
	WednesdayMin int       `db:"wednesday_min" json:"wednesdayMin"`
	ThursdayMin  int       `db:"thursday_min"  json:"thursdayMin"`
	FridayMin    int       `db:"friday_min"    json:"fridayMin"`
	SaturdayMin  int       `db:"saturday_min"  json:"saturdayMin"`
	SundayMin    int       `db:"sunday_min"    json:"sundayMin"`
	CreatedAt    time.Time `db:"created_at"    json:"createdAt"`
}

// CalendarException — 個別日付の例外 (祝日 / 振替出勤など)
type CalendarException struct {
	ID            uuid.UUID `db:"id"             json:"id"`
	CalendarID    uuid.UUID `db:"calendar_id"    json:"calendarId"`
	ExceptionDate time.Time `db:"exception_date" json:"exceptionDate"`
	Kind          string    `db:"kind"           json:"kind"` // HOLIDAY / WORKDAY
	Minutes       int       `db:"minutes"        json:"minutes"`
	Description   string    `db:"description"    json:"description"`
}

// MinutesForWeekday — 指定の曜日 (time.Weekday) の標準稼働分数を返す
func (c WorkCalendar) MinutesForWeekday(d time.Weekday) int {
	switch d {
	case time.Monday:
		return c.MondayMin
	case time.Tuesday:
		return c.TuesdayMin
	case time.Wednesday:
		return c.WednesdayMin
	case time.Thursday:
		return c.ThursdayMin
	case time.Friday:
		return c.FridayMin
	case time.Saturday:
		return c.SaturdayMin
	case time.Sunday:
		return c.SundayMin
	}
	return 0
}

// ====================================================================
// Cost Rollup
// ====================================================================

// CostRollupRow — 1品目あたりの標準原価明細
type CostRollupRow struct {
	ItemID       uuid.UUID `json:"itemId"`
	ItemCode     string    `json:"itemCode"`
	ItemName     string    `json:"itemName"`
	ItemType     ItemType  `json:"itemType"`
	MaterialCost float64   `json:"materialCost"` // BOM 子部品の積み上げ
	LaborCost    float64   `json:"laborCost"`    // ルーティング積み上げ (労務費)
	OverheadCost float64   `json:"overheadCost"` // 製造間接費 (新規)
	TotalCost    float64   `json:"totalCost"`
}

// ====================================================================
// Demand Forecasting
// ====================================================================

// ForecastPoint — 予測結果の1点 (履歴 or 予測)
type ForecastPoint struct {
	Period   time.Time `json:"period"`
	Actual   *float64  `json:"actual,omitempty"`   // 実績値 (履歴期間)
	Forecast *float64  `json:"forecast,omitempty"` // 予測値 (履歴の当てはめ + 将来予測)
	IsFuture bool      `json:"isFuture"`
}

type ForecastResult struct {
	ItemID   uuid.UUID       `json:"itemId"`
	ItemCode string          `json:"itemCode"`
	Method   string          `json:"method"` // "SMA" | "EXPO" | "HW"
	MAE      float64         `json:"mae"`
	MAPE     float64         `json:"mape"`
	Points   []ForecastPoint `json:"points"`
	RunID    *uuid.UUID      `json:"runId,omitempty"`
	Version  int             `json:"version,omitempty"`
	Scenario string          `json:"scenario,omitempty"`
	Status   string          `json:"status,omitempty"`
}

type ForecastRun struct {
	ID                uuid.UUID  `db:"id"                   json:"id"`
	ItemID            uuid.UUID  `db:"item_id"              json:"itemId"`
	Version           int        `db:"version"              json:"version"`
	Scenario          string     `db:"scenario"             json:"scenario"`
	Method            string     `db:"method"               json:"method"`
	BucketDays        int        `db:"bucket_days"          json:"bucketDays"`
	HorizonPeriods    int        `db:"horizon_periods"      json:"horizonPeriods"`
	AsOfDate          time.Time  `db:"as_of_date"           json:"asOfDate"`
	ParametersJSON    string     `db:"parameters_json"      json:"-"`
	MAE               float64    `db:"mae"                  json:"mae"`
	MAPE              float64    `db:"mape"                 json:"mape"`
	Status            string     `db:"status"               json:"status"`
	GeneratedAt       time.Time  `db:"generated_at"         json:"generatedAt"`
	GeneratedByUserID *uuid.UUID `db:"generated_by_user_id" json:"generatedByUserId,omitempty"`
	GeneratedBy       string     `db:"generated_by"         json:"generatedBy"`
	ActivatedAt       *time.Time `db:"activated_at"         json:"activatedAt,omitempty"`
	ActivatedByUserID *uuid.UUID `db:"activated_by_user_id" json:"activatedByUserId,omitempty"`
	ActivatedBy       *string    `db:"activated_by"         json:"activatedBy,omitempty"`
}

type ForecastValue struct {
	ID            uuid.UUID `db:"id"              json:"id"`
	ForecastRunID uuid.UUID `db:"forecast_run_id" json:"forecastRunId"`
	Period        time.Time `db:"period"          json:"period"`
	Quantity      float64   `db:"quantity"        json:"quantity"`
}

type ForecastRunDetail struct {
	Run    ForecastRun     `json:"run"`
	Values []ForecastValue `json:"values"`
}

type ForecastConsumptionBucket struct {
	Period             time.Time `json:"period"`
	ForecastQty        float64   `json:"forecastQty"`
	OrderQty           float64   `json:"orderQty"`
	ConsumedForecast   float64   `json:"consumedForecast"`
	RemainingForecast  float64   `json:"remainingForecast"`
	OrderAboveForecast float64   `json:"orderAboveForecast"`
	TotalDemand        float64   `json:"totalDemand"`
}

type ForecastConsumptionResult struct {
	RunID      uuid.UUID                   `json:"runId"`
	ItemID     uuid.UUID                   `json:"itemId"`
	ItemCode   string                      `json:"itemCode"`
	Version    int                         `json:"version"`
	Scenario   string                      `json:"scenario"`
	BucketDays int                         `json:"bucketDays"`
	Status     string                      `json:"status"`
	Buckets    []ForecastConsumptionBucket `json:"buckets"`
}

// ====================================================================
// Cycle Count
// ====================================================================

type CycleCount struct {
	ID            uuid.UUID  `db:"id"             json:"id"`
	ItemID        uuid.UUID  `db:"item_id"        json:"itemId"`
	ABCClass      string     `db:"abc_class"      json:"abcClass"`
	ScheduledDate time.Time  `db:"scheduled_date" json:"scheduledDate"`
	CountedDate   *time.Time `db:"counted_date"   json:"countedDate,omitempty"`
	ExpectedQty   *float64   `db:"expected_qty"   json:"expectedQty,omitempty"`
	CountedQty    *float64   `db:"counted_qty"    json:"countedQty,omitempty"`
	Variance      *float64   `db:"variance"       json:"variance,omitempty"`
	Status        string     `db:"status"         json:"status"`
	Notes         string     `db:"notes"          json:"notes"`
	CreatedAt     time.Time  `db:"created_at"     json:"createdAt"`
}

// CycleCountWithItem — 一覧表示用の結合型
type CycleCountWithItem struct {
	CycleCount
	ItemCode string `db:"item_code" json:"itemCode"`
	ItemName string `db:"item_name" json:"itemName"`
}

// ====================================================================
// Auth & RBAC
// ====================================================================

type Role string

const (
	RoleAdmin    Role = "admin"
	RolePlanner  Role = "planner"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// User — 認証ユーザー
type User struct {
	ID           uuid.UUID `db:"id"            json:"id"`
	Username     string    `db:"username"      json:"username"`
	PasswordHash string    `db:"password_hash" json:"-"` // never expose
	Role         Role      `db:"role"          json:"role"`
	IsActive     bool      `db:"is_active"     json:"isActive"`
	CreatedAt    time.Time `db:"created_at"    json:"createdAt"`
}

// ====================================================================
// ABC Analysis
// ====================================================================

// ABCAnalysisRow — 在庫ABC分析の1行
type ABCAnalysisRow struct {
	ItemID           uuid.UUID `json:"itemId"`
	ItemCode         string    `json:"itemCode"`
	ItemName         string    `json:"itemName"`
	OnHand           float64   `json:"onHand"`
	StandardCost     float64   `json:"standardCost"`
	OnHandValue      float64   `json:"onHandValue"`      // OnHand × StandardCost (reference only)
	AnnualUsageQty   float64   `json:"annualUsageQty"`   // rolling 12-month ISSUE quantity
	AnnualUsageValue float64   `json:"annualUsageValue"` // AnnualUsageQty × StandardCost
	UsageValuePct    float64   `json:"usageValuePct"`    // annual dollar usage share (%)
	CumulativePct    float64   `json:"cumulativePct"`    // cumulative annual dollar usage (%)
	ABCClass         string    `json:"abcClass"`         // "A","B","C"
	UsagePeriodStart time.Time `json:"usagePeriodStart"`
	UsagePeriodEnd   time.Time `json:"usagePeriodEnd"`
	UsageBasis       string    `json:"usageBasis"` // ISSUE
	CostBasis        string    `json:"costBasis"`  // STANDARD_COST
}

// ====================================================================
// Lot / Traceability
// ====================================================================

// Lot — 受入ロット (またはWO産出ロット)
type Lot struct {
	ID            uuid.UUID  `db:"id"             json:"id"`
	ItemID        uuid.UUID  `db:"item_id"        json:"itemId"`
	LotNo         string     `db:"lot_no"         json:"lotNo"`
	Quantity      float64    `db:"quantity"       json:"quantity"`
	ReceivedAt    time.Time  `db:"received_at"    json:"receivedAt"`
	ExpiryDate    *time.Time `db:"expiry_date"    json:"expiryDate,omitempty"`
	Supplier      string     `db:"supplier"       json:"supplier"`
	SourceDoc     string     `db:"source_doc"     json:"sourceDoc"`
	Notes         string     `db:"notes"          json:"notes"`
	QualityStatus string     `db:"quality_status" json:"qualityStatus"`
}

// LotMovement — ロットの入出庫履歴
type LotMovement struct {
	ID           uuid.UUID  `db:"id"            json:"id"`
	LotID        uuid.UUID  `db:"lot_id"        json:"lotId"`
	TxnID        *uuid.UUID `db:"txn_id"        json:"txnId,omitempty"`
	Quantity     float64    `db:"quantity"      json:"quantity"`
	MovementType string     `db:"movement_type" json:"movementType"`
	RefDoc       string     `db:"ref_doc"       json:"refDoc"`
	OccurredAt   time.Time  `db:"occurred_at"   json:"occurredAt"`
}

// LotWithBalance — ロット + 残数 (Where-used 検索用)
type LotWithBalance struct {
	Lot
	ItemCode string  `db:"item_code" json:"itemCode"`
	ItemName string  `db:"item_name" json:"itemName"`
	Balance  float64 `db:"balance"   json:"balance"`
}

// ====================================================================
// Audit Log
// ====================================================================

// StockBalance — v_stock_balance ビューから読み出す在庫サマリ
type StockBalance struct {
	ItemID   uuid.UUID `db:"item_id" json:"itemId"`
	Code     string    `db:"code"    json:"itemCode"`
	Name     string    `db:"name"    json:"itemName"`
	OnHand   float64   `db:"on_hand" json:"onHand"`
	Reserved float64   `db:"reserved" json:"reserved"`
}

func (s StockBalance) Available() float64 { return s.OnHand - s.Reserved }

// AuditLogEntry
type AuditLogEntry struct {
	ID         int64           `db:"id"          json:"id"`
	Username   string          `db:"username"    json:"username"`
	UserRole   string          `db:"user_role"   json:"userRole"`
	Action     string          `db:"action"      json:"action"`
	Resource   string          `db:"resource"    json:"resource"`
	ResourceID string          `db:"resource_id" json:"resourceId"`
	HTTPStatus int             `db:"http_status" json:"httpStatus"`
	IPAddress  string          `db:"ip_address"  json:"ipAddress"`
	OccurredAt time.Time       `db:"occurred_at" json:"occurredAt"`
	Payload    json.RawMessage `db:"payload"     json:"payload,omitempty"`
}

// ====================================================================
// Quality Inspection
// ====================================================================

type QualityInspection struct {
	ID              uuid.UUID  `db:"id"                json:"id"`
	LotID           uuid.UUID  `db:"lot_id"            json:"lotId"`
	InspectorUserID *uuid.UUID `db:"inspector_user_id" json:"inspectorUserId,omitempty"`
	Inspector       string     `db:"inspector"         json:"inspector"`
	InspectedAt     time.Time  `db:"inspected_at"      json:"inspectedAt"`
	Result          string     `db:"result"            json:"result"` // PASS / FAIL / HOLD
	DefectQty       float64    `db:"defect_qty"        json:"defectQty"`
	Notes           string     `db:"notes"             json:"notes"`
	PreviousStatus  *string    `db:"previous_status"   json:"previousStatus,omitempty"`
	ResultingStatus string     `db:"resulting_status"  json:"resultingStatus"`
}

// QualityStatusHistory is the immutable audit trail generated by an inspection.
// Legacy reconstructed rows can have nil FromStatus / ChangedByUserID when the
// pre-0024 database could not prove those facts.
type QualityStatusHistory struct {
	ID              uuid.UUID  `db:"id"                 json:"id"`
	LotID           uuid.UUID  `db:"lot_id"             json:"lotId"`
	InspectionID    *uuid.UUID `db:"inspection_id"      json:"inspectionId,omitempty"`
	FromStatus      *string    `db:"from_status"         json:"fromStatus,omitempty"`
	ToStatus        string     `db:"to_status"           json:"toStatus"`
	ChangedByUserID *uuid.UUID `db:"changed_by_user_id"  json:"changedByUserId,omitempty"`
	ChangedBy       string     `db:"changed_by"          json:"changedBy"`
	ChangedAt       time.Time  `db:"changed_at"          json:"changedAt"`
	Source          string     `db:"source"              json:"source"`
	SourceRef       string     `db:"source_ref"          json:"sourceRef"`
	Notes           string     `db:"notes"               json:"notes"`
}

// ====================================================================
// Supplier Quality / NCR
// ====================================================================

type SupplierQualityProfile struct {
	SupplierName       string     `db:"supplier_name"       json:"supplierName"`
	Status             string     `db:"status"              json:"status"`
	InspectionRequired bool       `db:"inspection_required" json:"inspectionRequired"`
	TargetPPM          float64    `db:"target_ppm"          json:"targetPpm"`
	Notes              string     `db:"notes"               json:"notes"`
	UpdatedByUserID    *uuid.UUID `db:"updated_by_user_id"  json:"updatedByUserId,omitempty"`
	UpdatedBy          string     `db:"updated_by"          json:"updatedBy"`
	UpdatedAt          time.Time  `db:"updated_at"          json:"updatedAt"`
}

type SupplierNCR struct {
	ID                uuid.UUID  `db:"id"                  json:"id"`
	NCRNo             string     `db:"ncr_no"              json:"ncrNo"`
	Supplier          string     `db:"supplier"            json:"supplier"`
	PurchaseOrderID   *uuid.UUID `db:"purchase_order_id"   json:"purchaseOrderId,omitempty"`
	PurchaseReceiptID *uuid.UUID `db:"purchase_receipt_id" json:"purchaseReceiptId,omitempty"`
	ItemID            uuid.UUID  `db:"item_id"             json:"itemId"`
	LotID             uuid.UUID  `db:"lot_id"              json:"lotId"`
	InspectionID      *uuid.UUID `db:"inspection_id"       json:"inspectionId,omitempty"`
	AffectedQty       float64    `db:"affected_qty"        json:"affectedQty"`
	Severity          string     `db:"severity"            json:"severity"`
	Description       string     `db:"description"         json:"description"`
	Status            string     `db:"status"              json:"status"`
	CreatedByUserID   uuid.UUID  `db:"created_by_user_id"  json:"createdByUserId"`
	CreatedBy         string     `db:"created_by"          json:"createdBy"`
	CreatedAt         time.Time  `db:"created_at"          json:"createdAt"`
	ClosedByUserID    *uuid.UUID `db:"closed_by_user_id"   json:"closedByUserId,omitempty"`
	ClosedBy          string     `db:"closed_by"           json:"closedBy"`
	ClosedAt          *time.Time `db:"closed_at"           json:"closedAt,omitempty"`
	ItemCode          string     `db:"item_code"           json:"itemCode,omitempty"`
	ItemName          string     `db:"item_name"           json:"itemName,omitempty"`
	LotNo             string     `db:"lot_no"              json:"lotNo,omitempty"`
	PONo              string     `db:"po_no"               json:"poNo,omitempty"`
	Disposition       string     `db:"disposition"         json:"disposition,omitempty"`
	DispositionQty    float64    `db:"disposition_qty"     json:"dispositionQty,omitempty"`
}

type SupplierNCRDisposition struct {
	ID              uuid.UUID  `db:"id"                  json:"id"`
	NCRID           uuid.UUID  `db:"ncr_id"              json:"ncrId"`
	Disposition     string     `db:"disposition"         json:"disposition"`
	Quantity        float64    `db:"quantity"            json:"quantity"`
	Notes           string     `db:"notes"               json:"notes"`
	InventoryTxnID  *uuid.UUID `db:"inventory_txn_id"    json:"inventoryTxnId,omitempty"`
	DecidedByUserID uuid.UUID  `db:"decided_by_user_id"  json:"decidedByUserId"`
	DecidedBy       string     `db:"decided_by"          json:"decidedBy"`
	DecidedAt       time.Time  `db:"decided_at"          json:"decidedAt"`
}

type SupplierNCRHistory struct {
	ID          uuid.UUID  `db:"id"            json:"id"`
	NCRID       uuid.UUID  `db:"ncr_id"        json:"ncrId"`
	FromStatus  *string    `db:"from_status"   json:"fromStatus,omitempty"`
	ToStatus    string     `db:"to_status"     json:"toStatus"`
	EventType   string     `db:"event_type"    json:"eventType"`
	ActorUserID *uuid.UUID `db:"actor_user_id" json:"actorUserId,omitempty"`
	Actor       string     `db:"actor"         json:"actor"`
	OccurredAt  time.Time  `db:"occurred_at"   json:"occurredAt"`
	Notes       string     `db:"notes"         json:"notes"`
}

type SupplierQualityScorecard struct {
	Supplier            string  `db:"supplier"              json:"supplier"`
	ProfileStatus       string  `db:"profile_status"        json:"profileStatus"`
	InspectionRequired  bool    `db:"inspection_required"   json:"inspectionRequired"`
	TargetPPM           float64 `db:"target_ppm"            json:"targetPpm"`
	ReceiptCount        int     `db:"receipt_count"         json:"receiptCount"`
	ReceivedQty         float64 `db:"received_qty"          json:"receivedQty"`
	InspectionCount     int     `db:"inspection_count"      json:"inspectionCount"`
	FailInspectionCount int     `db:"fail_inspection_count" json:"failInspectionCount"`
	RejectedLotCount    int     `db:"rejected_lot_count"    json:"rejectedLotCount"`
	DefectQty           float64 `db:"defect_qty"            json:"defectQty"`
	NCRCount            int     `db:"ncr_count"             json:"ncrCount"`
	OpenNCRCount        int     `db:"open_ncr_count"        json:"openNcrCount"`
	CriticalNCRCount    int     `db:"critical_ncr_count"    json:"criticalNcrCount"`
	ReturnedQty         float64 `db:"returned_qty"          json:"returnedQty"`
	ScrappedQty         float64 `db:"scrapped_qty"          json:"scrappedQty"`
	DefectPPM           float64 `db:"defect_ppm"            json:"defectPpm"`
}

// ====================================================================
// ATP (Available-to-Promise)
// ====================================================================

// ATPBucket — 1期間のATP内訳
type ATPBucket struct {
	Period          time.Time `json:"period"`
	StartingOnHand  float64   `json:"startingOnHand"`
	ScheduledIn     float64   `json:"scheduledIn"`     // PO/WO計画入庫
	CommittedOut    float64   `json:"committedOut"`    // 確定受注
	EndingProjected float64   `json:"endingProjected"` // 期末見込在庫
	ATP             float64   `json:"atp"`             // この期間の引当可能数
	CumulativeATP   float64   `json:"cumulativeAtp"`   // 累積ATP
}

type ATPResult struct {
	ItemID   uuid.UUID   `json:"itemId"`
	ItemCode string      `json:"itemCode"`
	Buckets  []ATPBucket `json:"buckets"`
}

// ====================================================================
// MRP Action Messages
// ====================================================================
//
// MRP 実行後に Planner に対する推奨アクション一覧。
//   RESCHEDULE_IN  既存 PO/WO の納期が必要日より遅い → 前倒し
//   RESCHEDULE_OUT 既存 PO/WO の納期が必要日より早い → 後ろ倒し
//   CANCEL         既存 PO/WO に対応する需要が無い
//   EXPEDITE       純所要があるが既存補充が無い → 緊急手配
//   RELEASE        Planned Order が発行可能日に到達
//   FUTURE_RELEASE 将来の Planned Order (情報提供のみ)

type ActionMessage struct {
	Kind        string     `json:"kind"`
	ItemID      uuid.UUID  `json:"itemId"`
	ItemCode    string     `json:"itemCode"`
	Quantity    float64    `json:"quantity"`
	NeedDate    time.Time  `json:"needDate"`
	CurrentDate *time.Time `json:"currentDate,omitempty"` // 既存伝票の納期 (RESCHEDULE時)
	RefDocType  string     `json:"refDocType"`            // PO / WO / PLANNED
	RefDocNo    string     `json:"refDocNo"`
	RefDocID    *uuid.UUID `json:"refDocId,omitempty"`
	Severity    string     `json:"severity"` // INFO / WARNING / CRITICAL
	Message     string     `json:"message"`
}

// ====================================================================
// Shop Floor Control
// ====================================================================

// WOOperation — WO に紐づく工程実績
type WOOperation struct {
	ID                 uuid.UUID  `db:"id"                    json:"id"`
	WOID               uuid.UUID  `db:"wo_id"                 json:"woId"`
	SeqNo              int        `db:"seq_no"                json:"seqNo"`
	WorkCenterID       uuid.UUID  `db:"work_center_id"        json:"workCenterId"`
	Description        string     `db:"description"           json:"description"`
	PlannedSetupMin    float64    `db:"planned_setup_min"     json:"plannedSetupMin"`
	PlannedRunPerUnit  float64    `db:"planned_run_per_unit"  json:"plannedRunPerUnit"`
	RoutingOperationID *uuid.UUID `db:"routing_operation_id" json:"routingOperationId,omitempty"`
	SetupFamily        string     `db:"setup_family"          json:"setupFamily"`
	OverlapEnabled     bool       `db:"overlap_enabled"       json:"overlapEnabled"`
	TransferBatchQty   float64    `db:"transfer_batch_qty"    json:"transferBatchQty"`
	MachinesRequired   int        `db:"machines_required"     json:"machinesRequired"`
	WorkersRequired    int        `db:"workers_required"      json:"workersRequired"`
	ActualMinutes      float64    `db:"actual_minutes"        json:"actualMinutes"`
	CompletedQty       float64    `db:"completed_qty"         json:"completedQty"`
	Status             string     `db:"status"                json:"status"`
	Operator           string     `db:"operator"              json:"operator"`
	OperatorUserID     *uuid.UUID `db:"operator_user_id"      json:"operatorUserId,omitempty"`
	StartedAt          *time.Time `db:"started_at"            json:"startedAt,omitempty"`
	ActiveStartedAt    *time.Time `db:"active_started_at"     json:"activeStartedAt,omitempty"`
	CompletedAt        *time.Time `db:"completed_at"          json:"completedAt,omitempty"`
	CreatedAt          time.Time  `db:"created_at"            json:"createdAt"`
}

// WOOperationDetail — WC情報を含めた拡張表示用
type WOOperationDetail struct {
	WOOperation
	WorkCenterCode string  `db:"wc_code"   json:"workCenterCode"`
	WorkCenterName string  `db:"wc_name"   json:"workCenterName"`
	OrderNo        string  `db:"order_no"  json:"orderNo"`
	ItemCode       string  `db:"item_code" json:"itemCode"`
	ItemName       string  `db:"item_name" json:"itemName"`
	WOQuantity     float64 `db:"wo_quantity" json:"woQuantity"`
}

// OperationLog — 1イベント
type OperationLog struct {
	ID             uuid.UUID  `db:"id"          json:"id"`
	WOOpID         uuid.UUID  `db:"wo_op_id"    json:"woOpId"`
	EventType      string     `db:"event_type"  json:"eventType"`
	EventAt        time.Time  `db:"event_at"    json:"eventAt"`
	Operator       string     `db:"operator"         json:"operator"`
	OperatorUserID *uuid.UUID `db:"operator_user_id" json:"operatorUserId,omitempty"`
	Quantity       float64    `db:"quantity"         json:"quantity"`
	Notes          string     `db:"notes"       json:"notes"`
}

// ====================================================================
// KPI Dashboard
// ====================================================================

type KPIDashboard struct {
	GeneratedAt        time.Time `json:"generatedAt"`
	OTIFRate           float64   `json:"otifRate"`          // 0-100, On-Time-In-Full
	OnTimeRate         float64   `json:"onTimeRate"`        // 0-100, 納期遵守率
	InventoryTurnover  float64   `json:"inventoryTurnover"` // 年率回転数
	InventoryValue     float64   `json:"inventoryValue"`
	ThroughputUnits    float64   `json:"throughputUnits"` // 直近30日完成数量
	WIPUnits           float64   `json:"wipUnits"`        // 仕掛中合計
	OpenWOCount        int       `json:"openWoCount"`
	OpenPOCount        int       `json:"openPoCount"`
	OverdueWOCount     int       `json:"overdueWoCount"`
	OverduePOCount     int       `json:"overduePoCount"`
	QualityPassRate    float64   `json:"qualityPassRate"` // 0-100
	QualityHoldCount   int       `json:"qualityHoldCount"`
	QualityRejectCount int       `json:"qualityRejectCount"`
	CriticalActions    int       `json:"criticalActions"` // CRITICAL severity アクションメッセージ数
	WarningActions     int       `json:"warningActions"`
	// 直近30日の日次完成数 (sparkline用)
	DailyThroughput []KPIPoint `json:"dailyThroughput"`
}

type KPIPoint struct {
	Date  time.Time `json:"date"`
	Value float64   `json:"value"`
}

// ====================================================================
// S&OP / RCCP
// ====================================================================

type ItemGroup struct {
	ID          uuid.UUID `db:"id"          json:"id"`
	Code        string    `db:"code"        json:"code"`
	Name        string    `db:"name"        json:"name"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at"  json:"createdAt"`
}

type SOPPlan struct {
	ID              uuid.UUID `db:"id"               json:"id"`
	GroupID         uuid.UUID `db:"group_id"         json:"groupId"`
	PlanMonth       time.Time `db:"plan_month"       json:"planMonth"`
	DemandQty       float64   `db:"demand_qty"       json:"demandQty"`
	SupplyQty       float64   `db:"supply_qty"       json:"supplyQty"`
	InventoryTarget float64   `db:"inventory_target" json:"inventoryTarget"`
	Notes           string    `db:"notes"            json:"notes"`
	CreatedAt       time.Time `db:"created_at"       json:"createdAt"`
}

type SOPProductMixVersion struct {
	ID                uuid.UUID           `db:"id"                   json:"id"`
	GroupID           uuid.UUID           `db:"group_id"             json:"groupId"`
	Version           int                 `db:"version"              json:"version"`
	Name              string              `db:"name"                 json:"name"`
	Status            string              `db:"status"               json:"status"`
	CreatedAt         time.Time           `db:"created_at"           json:"createdAt"`
	CreatedByUserID   *uuid.UUID          `db:"created_by_user_id"   json:"createdByUserId,omitempty"`
	CreatedBy         string              `db:"created_by"           json:"createdBy"`
	ActivatedAt       *time.Time          `db:"activated_at"         json:"activatedAt,omitempty"`
	ActivatedByUserID *uuid.UUID          `db:"activated_by_user_id" json:"activatedByUserId,omitempty"`
	ActivatedBy       string              `db:"activated_by"         json:"activatedBy,omitempty"`
	Lines             []SOPProductMixLine `db:"-" json:"lines,omitempty"`
}

type SOPProductMixLine struct {
	ID           uuid.UUID `db:"id"             json:"id"`
	MixVersionID uuid.UUID `db:"mix_version_id" json:"mixVersionId"`
	ItemID       uuid.UUID `db:"item_id"        json:"itemId"`
	MixPct       float64   `db:"mix_pct"        json:"mixPct"`
}

type SOPDisaggregationRun struct {
	ID                uuid.UUID `db:"id"                     json:"id"`
	SOPPlanID         uuid.UUID `db:"sop_plan_id"            json:"sopPlanId"`
	MixVersionID      uuid.UUID `db:"mix_version_id"         json:"mixVersionId"`
	GroupID           uuid.UUID `db:"group_id"               json:"groupId"`
	PlanMonth         time.Time `db:"plan_month"             json:"planMonth"`
	SupplyQtySnapshot float64   `db:"supply_qty_snapshot"    json:"supplyQtySnapshot"`
	TimePhasing       string    `db:"time_phasing"           json:"timePhasing"`
	Status            string    `db:"status"                 json:"status"`
	AppliedAt         time.Time `db:"applied_at"             json:"appliedAt"`
	AppliedByUserID   uuid.UUID `db:"applied_by_user_id"     json:"appliedByUserId"`
	AppliedBy         string    `db:"applied_by"             json:"appliedBy"`
	CreatedAt         time.Time `db:"created_at"             json:"createdAt"`
}

type SOPDisaggregationLine struct {
	ID         uuid.UUID `db:"id"          json:"id"`
	RunID      uuid.UUID `db:"run_id"      json:"runId"`
	ItemID     uuid.UUID `db:"item_id"     json:"itemId"`
	Period     time.Time `db:"period"      json:"period"`
	MixPct     float64   `db:"mix_pct"     json:"mixPct"`
	TimeWeight float64   `db:"time_weight" json:"timeWeight"`
	PlannedQty float64   `db:"planned_qty" json:"plannedQty"`
}

type SOPDisaggregationPreview struct {
	SOPPlanID    uuid.UUID               `json:"sopPlanId"`
	MixVersionID uuid.UUID               `json:"mixVersionId"`
	GroupID      uuid.UUID               `json:"groupId"`
	PlanMonth    time.Time               `json:"planMonth"`
	SupplyQty    float64                 `json:"supplyQty"`
	Lines        []SOPDisaggregationLine `json:"lines"`
}

type RCCPProfile struct {
	ID             uuid.UUID `db:"id"               json:"id"`
	ItemID         uuid.UUID `db:"item_id"          json:"itemId"`
	WorkCenterID   uuid.UUID `db:"work_center_id"   json:"workCenterId"`
	MinutesPerUnit float64   `db:"minutes_per_unit" json:"minutesPerUnit"`
}

// RCCPLoadRow — RCCP 結果 (作業区 × 月)
type RCCPLoadRow struct {
	WorkCenterID     uuid.UUID `json:"workCenterId"`
	WorkCenterCode   string    `json:"workCenterCode"`
	WorkCenterName   string    `json:"workCenterName"`
	Month            time.Time `json:"month"`
	RequiredMinutes  float64   `json:"requiredMinutes"`
	AvailableMinutes float64   `json:"availableMinutes"`
	LoadPct          float64   `json:"loadPct"`
}

// ====================================================================
// Engineering Change (ECO/ECN)
// ====================================================================

type EngineeringChange struct {
	ID                uuid.UUID  `db:"id"                  json:"id"`
	ECONo             string     `db:"eco_no"              json:"ecoNo"`
	Title             string     `db:"title"               json:"title"`
	Description       string     `db:"description"         json:"description"`
	Status            string     `db:"status"              json:"status"`
	EffectiveDate     time.Time  `db:"effective_date"      json:"effectiveDate"`
	RequestedBy       string     `db:"requested_by"        json:"requestedBy"`
	RequestedByUserID *uuid.UUID `db:"requested_by_user_id" json:"requestedByUserId,omitempty"`
	ApprovedBy        string     `db:"approved_by"         json:"approvedBy"`
	ApprovedByUserID  *uuid.UUID `db:"approved_by_user_id" json:"approvedByUserId,omitempty"`
	ApprovedAt        *time.Time `db:"approved_at"         json:"approvedAt,omitempty"`
	AppliedBy         string     `db:"applied_by"          json:"appliedBy"`
	AppliedByUserID   *uuid.UUID `db:"applied_by_user_id"  json:"appliedByUserId,omitempty"`
	AppliedAt         *time.Time `db:"applied_at"          json:"appliedAt,omitempty"`
	CancelledBy       string     `db:"cancelled_by"        json:"cancelledBy"`
	CancelledByUserID *uuid.UUID `db:"cancelled_by_user_id" json:"cancelledByUserId,omitempty"`
	CancelledAt       *time.Time `db:"cancelled_at"        json:"cancelledAt,omitempty"`
	CreatedAt         time.Time  `db:"created_at"          json:"createdAt"`
}

type ECOStatusHistory struct {
	ID                    uuid.UUID  `db:"id"                      json:"id"`
	ECOID                 uuid.UUID  `db:"eco_id"                  json:"ecoId"`
	FromStatus            string     `db:"from_status"             json:"fromStatus"`
	ToStatus              string     `db:"to_status"               json:"toStatus"`
	ActorUserID           *uuid.UUID `db:"actor_user_id"           json:"actorUserId,omitempty"`
	ActorUsername         string     `db:"actor_username"          json:"actorUsername"`
	OccurredAt            time.Time  `db:"occurred_at"             json:"occurredAt"`
	EffectiveDateSnapshot time.Time  `db:"effective_date_snapshot" json:"effectiveDateSnapshot"`
}

type ECOComponent struct {
	ID          uuid.UUID `db:"id"             json:"id"`
	ECOID       uuid.UUID `db:"eco_id"         json:"ecoId"`
	Action      string    `db:"action"         json:"action"` // ADD/REMOVE/MODIFY
	ParentID    uuid.UUID `db:"parent_id"      json:"parentId"`
	ChildID     uuid.UUID `db:"child_id"       json:"childId"`
	NewQuantity float64   `db:"new_quantity"   json:"newQuantity"`
	NewScrapPct float64   `db:"new_scrap_pct"  json:"newScrapPct"`
	Notes       string    `db:"notes"          json:"notes"`
}

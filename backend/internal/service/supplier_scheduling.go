package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const defaultReliabilityWindowDays = 365
const defaultReliabilityMinSamples = 3

type SupplierScheduleEventInput struct {
	EventID               string   `json:"eventId"`
	EventType             string   `json:"eventType"`
	Quantity              *float64 `json:"quantity"`
	ConfirmedDeliveryDate string   `json:"confirmedDeliveryDate"`
	ASNNo                 string   `json:"asnNo"`
	ExpectedArrivalDate   string   `json:"expectedArrivalDate"`
	SupplierReference     string   `json:"supplierReference"`
	Notes                 string   `json:"notes"`
}

type SupplierReliabilityRunInput struct {
	WindowDays int `json:"windowDays"`
	MinSamples int `json:"minSamples"`
}

type SupplierSchedulingService struct {
	db *sqlx.DB
}

// PurchasePlanningDate returns the firmest available supplier date. The
// repository hydrates ExpectedDeliveryDate from the canonical 0035 planning
// view, so MRP/CTP use the same evidence without duplicating precedence rules.
func PurchasePlanningDate(p domain.PurchaseOrder) time.Time {
	if p.ExpectedDeliveryDate != nil && !p.ExpectedDeliveryDate.IsZero() {
		return TruncateDay(*p.ExpectedDeliveryDate)
	}
	return TruncateDay(p.DueDate)
}

func supplierReliabilityConfidence(samples, minSamples int) string {
	if minSamples <= 0 {
		minSamples = defaultReliabilityMinSamples
	}
	if samples >= minSamples*3 && samples >= 10 {
		return "HIGH"
	}
	if samples >= minSamples {
		return "MEDIUM"
	}
	return "LOW"
}

func recommendedSupplierLeadDays(avg, stddev, p90 float64) int {
	candidate := math.Max(p90, avg+stddev)
	if candidate < 0 {
		candidate = 0
	}
	return int(math.Ceil(candidate - 1e-9))
}

func (s *SupplierSchedulingService) ListEvents(ctx context.Context, poID uuid.UUID) ([]domain.SupplierScheduleEvent, error) {
	var rows []domain.SupplierScheduleEvent
	err := s.db.SelectContext(ctx, &rows, `
SELECT * FROM supplier_schedule_events
 WHERE purchase_order_id=$1
 ORDER BY revision_no,occurred_at,id`, poID)
	return rows, err
}

func sameOptionalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) <= 1e-9
}

func sameOptionalDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return TruncateDay(*a).Equal(TruncateDay(*b))
}

func sameSupplierScheduleEvent(existing domain.SupplierScheduleEvent, poID uuid.UUID, eventType string, quantity *float64, confirmedDate *time.Time, asnNo string, expectedArrival *time.Time, supplierReference, notes string, actor SalesOrderActor) bool {
	return existing.PurchaseOrderID == poID &&
		existing.EventType == eventType &&
		sameOptionalFloat(existing.Quantity, quantity) &&
		sameOptionalDate(existing.ConfirmedDeliveryDate, confirmedDate) &&
		existing.ASNNo == asnNo &&
		sameOptionalDate(existing.ExpectedArrivalDate, expectedArrival) &&
		existing.SupplierReference == supplierReference &&
		existing.Notes == notes &&
		existing.ActorUserID == actor.UserID &&
		existing.ActorUsername == actor.Username
}

func (s *SupplierSchedulingService) RecordEvent(ctx context.Context, poID uuid.UUID, in SupplierScheduleEventInput, actor SalesOrderActor) (*domain.SupplierScheduleEvent, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if actor.Role != domain.RoleAdmin && actor.Role != domain.RolePlanner {
		return nil, domain.NewForbidden("supplier scheduling requires planner/admin")
	}
	eventID, err := uuid.Parse(strings.TrimSpace(in.EventID))
	if err != nil || eventID == uuid.Nil {
		return nil, domain.NewBadRequest("eventId must be a valid UUID", err)
	}
	eventType := strings.ToUpper(strings.TrimSpace(in.EventType))
	if eventType != "CONFIRM" && eventType != "REVISE" && eventType != "ASN" && eventType != "CANCEL" {
		return nil, domain.NewBadRequest("eventType must be CONFIRM, REVISE, ASN or CANCEL", nil)
	}
	confirmedDate, err := parseISODate(in.ConfirmedDeliveryDate, false)
	if err != nil {
		return nil, err
	}
	expectedArrival, err := parseISODate(in.ExpectedArrivalDate, false)
	if err != nil {
		return nil, err
	}
	if eventType == "CONFIRM" || eventType == "REVISE" {
		if in.Quantity == nil || *in.Quantity <= 0 || confirmedDate == nil {
			return nil, domain.NewBadRequest("confirmation/revision requires positive quantity and confirmedDeliveryDate", nil)
		}
	}
	if eventType == "ASN" {
		if in.Quantity == nil || *in.Quantity <= 0 || strings.TrimSpace(in.ASNNo) == "" || expectedArrival == nil {
			return nil, domain.NewBadRequest("ASN requires positive quantity, asnNo and expectedArrivalDate", nil)
		}
	}
	if eventType == "CANCEL" {
		in.Quantity = nil
		confirmedDate = nil
		expectedArrival = nil
		in.ASNNo = ""
	}

	asnNo := strings.TrimSpace(in.ASNNo)
	supplierReference := strings.TrimSpace(in.SupplierReference)
	notes := strings.TrimSpace(in.Notes)

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "supplier-schedule:"+eventID.String()); err != nil {
		return nil, err
	}
	var existing domain.SupplierScheduleEvent
	err = tx.GetContext(ctx, &existing, `SELECT * FROM supplier_schedule_events WHERE id=$1`, eventID)
	if err == nil {
		if !sameSupplierScheduleEvent(existing, poID, eventType, in.Quantity, confirmedDate, asnNo, expectedArrival, supplierReference, notes, actor) {
			return nil, domain.NewConflict("eventId is already used by a different supplier schedule event")
		}
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var po struct {
		Quantity    float64 `db:"quantity"`
		ReceivedQty float64 `db:"received_qty"`
		Status      string  `db:"status"`
	}
	if err := tx.GetContext(ctx, &po, `SELECT quantity::double precision AS quantity,received_qty::double precision AS received_qty,status FROM purchase_orders WHERE id=$1 FOR UPDATE`, poID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("purchase order")
		}
		return nil, err
	}
	if po.Status == "RECEIVED" || po.Status == "CLOSED" {
		return nil, domain.NewConflict("supplier schedule is immutable after PO receipt/close")
	}
	remaining := po.Quantity - po.ReceivedQty
	if remaining < 0 {
		remaining = 0
	}
	if in.Quantity != nil && math.Abs(*in.Quantity-remaining) > 1e-9 {
		return nil, domain.NewBadRequest("supplier schedule quantity must equal current PO remaining quantity", nil)
	}
	var revision int
	if err := tx.GetContext(ctx, &revision, `SELECT COALESCE(MAX(revision_no),0)+1 FROM supplier_schedule_events WHERE purchase_order_id=$1`, poID); err != nil {
		return nil, err
	}
	row := domain.SupplierScheduleEvent{
		ID:                    eventID,
		PurchaseOrderID:       poID,
		RevisionNo:            revision,
		EventType:             eventType,
		Quantity:              in.Quantity,
		ConfirmedDeliveryDate: confirmedDate,
		ASNNo:                 asnNo,
		ExpectedArrivalDate:   expectedArrival,
		SupplierReference:     supplierReference,
		Notes:                 notes,
		ActorUserID:           actor.UserID,
		ActorUsername:         actor.Username,
	}
	if err := tx.GetContext(ctx, &row, `
INSERT INTO supplier_schedule_events(
 id,purchase_order_id,revision_no,event_type,quantity,confirmed_delivery_date,
 asn_no,expected_arrival_date,supplier_reference,notes,actor_user_id,actor_username)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING *`, row.ID, row.PurchaseOrderID, row.RevisionNo, row.EventType, row.Quantity,
		row.ConfirmedDeliveryDate, row.ASNNo, row.ExpectedArrivalDate, row.SupplierReference,
		row.Notes, row.ActorUserID, row.ActorUsername); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &row, nil
}

type supplierReliabilityAggregate struct {
	SupplierName        string     `db:"supplier_name"`
	ItemID              *uuid.UUID `db:"item_id"`
	SampleCount         int        `db:"sample_count"`
	AverageLeadDays     float64    `db:"average_lead_days"`
	StddevLeadDays      float64    `db:"stddev_lead_days"`
	P50LeadDays         float64    `db:"p50_lead_days"`
	P90LeadDays         float64    `db:"p90_lead_days"`
	OnTimeRate          float64    `db:"on_time_rate"`
	AverageLatenessDays float64    `db:"average_lateness_days"`
}

func (s *SupplierSchedulingService) RefreshReliability(ctx context.Context, in SupplierReliabilityRunInput, actor SalesOrderActor) (*domain.SupplierLeadTimeRunResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if actor.Role != domain.RoleAdmin && actor.Role != domain.RolePlanner {
		return nil, domain.NewForbidden("supplier reliability refresh requires planner/admin")
	}
	if in.WindowDays <= 0 {
		in.WindowDays = defaultReliabilityWindowDays
	}
	if in.WindowDays > 3650 {
		return nil, domain.NewBadRequest("windowDays must be <= 3650", nil)
	}
	if in.MinSamples <= 0 {
		in.MinSamples = defaultReliabilityMinSamples
	}
	if in.MinSamples > 1000 {
		return nil, domain.NewBadRequest("minSamples must be <= 1000", nil)
	}
	var today time.Time
	if err := s.db.GetContext(ctx, &today, `SELECT CURRENT_DATE`); err != nil {
		return nil, err
	}
	today = TruncateDay(today)
	windowStart := today.AddDate(0, 0, -(in.WindowDays - 1))

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run := domain.SupplierLeadTimeRun{
		ID: uuid.New(), WindowStart: windowStart, WindowEnd: today, MinSamples: in.MinSamples,
		Status: "RUNNING", GeneratedByUserID: actor.UserID, GeneratedBy: actor.Username,
	}
	if err := tx.GetContext(ctx, &run, `
INSERT INTO supplier_lead_time_runs(id,window_start,window_end,min_samples,status,generated_by_user_id,generated_by)
VALUES($1,$2,$3,$4,'RUNNING',$5,$6) RETURNING *`, run.ID, run.WindowStart, run.WindowEnd, run.MinSamples, run.GeneratedByUserID, run.GeneratedBy); err != nil {
		return nil, err
	}

	var aggregates []supplierReliabilityAggregate
	if err := tx.SelectContext(ctx, &aggregates, `
WITH completed_po AS (
  SELECT po.id,btrim(po.supplier) AS supplier_name,po.item_id,po.order_date,po.due_date,
         MAX(pr.received_at)::date AS actual_delivery_date,
         SUM(pr.quantity)::double precision AS received_qty,
         po.quantity::double precision AS ordered_qty
    FROM purchase_orders po
    JOIN purchase_receipts pr ON pr.purchase_order_id=po.id
   WHERE pr.received_at::date BETWEEN $1 AND $2
     AND btrim(po.supplier)<>''
   GROUP BY po.id,po.supplier,po.item_id,po.order_date,po.due_date,po.quantity
  HAVING SUM(pr.quantity) + 0.000001 >= po.quantity
), samples AS (
  SELECT supplier_name,item_id,
         GREATEST(actual_delivery_date-order_date,0)::double precision AS lead_days,
         CASE WHEN actual_delivery_date<=due_date THEN 1.0 ELSE 0.0 END AS on_time,
         GREATEST(actual_delivery_date-due_date,0)::double precision AS lateness_days
    FROM completed_po
)
SELECT supplier_name,item_id,
       COUNT(*)::int AS sample_count,
       COALESCE(AVG(lead_days),0)::double precision AS average_lead_days,
       COALESCE(STDDEV_POP(lead_days),0)::double precision AS stddev_lead_days,
       COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY lead_days),0)::double precision AS p50_lead_days,
       COALESCE(PERCENTILE_CONT(0.90) WITHIN GROUP (ORDER BY lead_days),0)::double precision AS p90_lead_days,
       COALESCE(AVG(on_time),0)::double precision AS on_time_rate,
       COALESCE(AVG(lateness_days),0)::double precision AS average_lateness_days
  FROM samples
 GROUP BY GROUPING SETS ((supplier_name,item_id),(supplier_name))
 ORDER BY supplier_name,item_id NULLS FIRST`, windowStart, today); err != nil {
		return nil, err
	}

	results := make([]domain.SupplierLeadTimeResult, 0, len(aggregates))
	for _, a := range aggregates {
		r := domain.SupplierLeadTimeResult{
			ID: uuid.New(), RunID: run.ID, SupplierName: a.SupplierName, ItemID: a.ItemID,
			SampleCount: a.SampleCount, AverageLeadDays: a.AverageLeadDays, StddevLeadDays: a.StddevLeadDays,
			P50LeadDays: a.P50LeadDays, P90LeadDays: a.P90LeadDays, OnTimeRate: a.OnTimeRate,
			AverageLatenessDays: a.AverageLatenessDays,
			RecommendedLeadDays: recommendedSupplierLeadDays(a.AverageLeadDays, a.StddevLeadDays, a.P90LeadDays),
			Confidence:          supplierReliabilityConfidence(a.SampleCount, in.MinSamples),
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO supplier_lead_time_results(
 id,run_id,supplier_name,item_id,sample_count,average_lead_days,stddev_lead_days,p50_lead_days,p90_lead_days,
 on_time_rate,average_lateness_days,recommended_lead_days,confidence)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, r.ID, r.RunID, r.SupplierName, r.ItemID, r.SampleCount,
			r.AverageLeadDays, r.StddevLeadDays, r.P50LeadDays, r.P90LeadDays, r.OnTimeRate, r.AverageLatenessDays,
			r.RecommendedLeadDays, r.Confidence); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	hash := canonicalSupplierReliabilityHash(results)
	if err := tx.GetContext(ctx, &run, `
UPDATE supplier_lead_time_runs
   SET status='COMPLETE',result_hash=$2,completed_at=now()
 WHERE id=$1
RETURNING *`, run.ID, hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetReliabilityRun(ctx, run.ID)
}

func canonicalSupplierReliabilityHash(rows []domain.SupplierLeadTimeResult) string {
	type stable struct {
		Supplier        string  `json:"supplier"`
		Item            string  `json:"item"`
		Samples         int     `json:"samples"`
		Average         float64 `json:"average"`
		Stddev          float64 `json:"stddev"`
		P50             float64 `json:"p50"`
		P90             float64 `json:"p90"`
		OnTime          float64 `json:"onTime"`
		AverageLateness float64 `json:"averageLateness"`
		Recommended     int     `json:"recommended"`
		Confidence      string  `json:"confidence"`
	}
	out := make([]stable, 0, len(rows))
	for _, r := range rows {
		item := "*"
		if r.ItemID != nil {
			item = r.ItemID.String()
		}
		out = append(out, stable{r.SupplierName, item, r.SampleCount, r.AverageLeadDays, r.StddevLeadDays, r.P50LeadDays, r.P90LeadDays, r.OnTimeRate, r.AverageLatenessDays, r.RecommendedLeadDays, r.Confidence})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Supplier != out[j].Supplier {
			return out[i].Supplier < out[j].Supplier
		}
		return out[i].Item < out[j].Item
	})
	b, _ := json.Marshal(out)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *SupplierSchedulingService) LatestReliability(ctx context.Context) ([]domain.SupplierLeadTimeResult, error) {
	var rows []domain.SupplierLeadTimeResult
	err := s.db.SelectContext(ctx, &rows, `
SELECT r.id,r.run_id,r.supplier_name,r.item_id,COALESCE(i.code,'') AS item_code,
       r.sample_count,r.average_lead_days::double precision AS average_lead_days,
       r.stddev_lead_days::double precision AS stddev_lead_days,
       r.p50_lead_days::double precision AS p50_lead_days,
       r.p90_lead_days::double precision AS p90_lead_days,
       r.on_time_rate::double precision AS on_time_rate,
       r.average_lateness_days::double precision AS average_lateness_days,
       r.recommended_lead_days,r.confidence,r.created_at
  FROM v_current_supplier_lead_time r
  LEFT JOIN items i ON i.id=r.item_id
 ORDER BY r.supplier_name,r.item_id NULLS FIRST`)
	return rows, err
}

func (s *SupplierSchedulingService) ListReliabilityRuns(ctx context.Context) ([]domain.SupplierLeadTimeRun, error) {
	var rows []domain.SupplierLeadTimeRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM supplier_lead_time_runs ORDER BY created_at DESC,id DESC LIMIT 100`)
	return rows, err
}

func (s *SupplierSchedulingService) GetReliabilityRun(ctx context.Context, id uuid.UUID) (*domain.SupplierLeadTimeRunResult, error) {
	var out domain.SupplierLeadTimeRunResult
	if err := s.db.GetContext(ctx, &out.Run, `SELECT * FROM supplier_lead_time_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("supplier lead-time run")
		}
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &out.Results, `
SELECT r.id,r.run_id,r.supplier_name,r.item_id,COALESCE(i.code,'') AS item_code,
       r.sample_count,r.average_lead_days::double precision AS average_lead_days,
       r.stddev_lead_days::double precision AS stddev_lead_days,
       r.p50_lead_days::double precision AS p50_lead_days,
       r.p90_lead_days::double precision AS p90_lead_days,
       r.on_time_rate::double precision AS on_time_rate,
       r.average_lateness_days::double precision AS average_lateness_days,
       r.recommended_lead_days,r.confidence,r.created_at
  FROM supplier_lead_time_results r LEFT JOIN items i ON i.id=r.item_id
 WHERE r.run_id=$1 ORDER BY r.supplier_name,r.item_id NULLS FIRST`, id); err != nil {
		return nil, err
	}
	return &out, nil
}

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
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const defaultBackorderHorizonDays = 90

type BackorderPreviewInput struct {
	HorizonDays  int        `json:"horizonDays"`
	FilterItemID *uuid.UUID `json:"filterItemId,omitempty"`
}

type BackorderPublishInput struct {
	RunID uuid.UUID `json:"runId"`
}

type BackorderService struct {
	db         *sqlx.DB
	atp        *ATPService
	ctp        *CTPEngine
	allocation *ProductAllocationService
}

type bopCandidate struct {
	SalesOrderID        uuid.UUID  `db:"sales_order_id"`
	SalesOrderNo        string     `db:"sales_order_no"`
	SalesOrderLineID    uuid.UUID  `db:"sales_order_line_id"`
	LineNo              int        `db:"line_no"`
	ItemID              uuid.UUID  `db:"item_id"`
	ItemCode            string     `db:"item_code"`
	ItemName            string     `db:"item_name"`
	CustomerID          uuid.UUID  `db:"customer_id"`
	CustomerNo          string     `db:"customer_no"`
	CustomerName        string     `db:"customer_name"`
	ServiceClassCode    string     `db:"service_class_code"`
	ServicePriority     int        `db:"service_priority"`
	OrderPriority       string     `db:"order_priority"`
	OrderDate           time.Time  `db:"order_date"`
	RequestedDate       time.Time  `db:"requested_date"`
	CurrentPromisedDate *time.Time `db:"current_promised_date"`
	OpenQty             float64    `db:"open_qty"`
	AllocatedQty        float64    `db:"allocated_qty"`
}

type calculatedBackorder struct {
	lines         []domain.BackorderRunLine
	confirmations []domain.BackorderRunConfirmation
	hash          string
}

type allocationRuntime struct {
	policy      *ProductAllocationPolicy
	pool        float64
	bucketLimit map[string]float64
	bucketUsed  map[string]float64
}

func normalizeBackorderHorizon(v int) (int, error) {
	if v <= 0 {
		return defaultBackorderHorizonDays, nil
	}
	if v > 366 {
		return 0, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	return v, nil
}

func bopPriorityRank(p string) int {
	switch p {
	case "EXPEDITE":
		return 0
	case "HIGH":
		return 1
	default:
		return 2
	}
}

func (s *BackorderService) Preview(ctx context.Context, in BackorderPreviewInput, actor SalesOrderActor) (*domain.BackorderResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	horizon, err := normalizeBackorderHorizon(in.HorizonDays)
	if err != nil {
		return nil, err
	}
	if in.FilterItemID != nil && *in.FilterItemID == uuid.Nil {
		return nil, domain.NewBadRequest("filterItemId is invalid", nil)
	}
	run := domain.BackorderRun{
		ID: uuid.New(), Status: "RUNNING", HorizonDays: horizon, FilterItemID: in.FilterItemID,
		RequestedAt: time.Now().UTC(), RequestedByUserID: actor.UserID, RequestedBy: actor.Username,
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO backorder_runs(id,status,horizon_days,filter_item_id,requested_at,requested_by_user_id,requested_by)
VALUES($1,'RUNNING',$2,$3,$4,$5,$6)`, run.ID, horizon, in.FilterItemID, run.RequestedAt, actor.UserID, actor.Username); err != nil {
		return nil, err
	}

	calc, err := s.calculate(ctx, horizon, in.FilterItemID)
	if err != nil {
		now := time.Now().UTC()
		_, _ = s.db.ExecContext(ctx, `UPDATE backorder_runs SET status='FAILED',completed_at=$2,error_text=$3 WHERE id=$1 AND status='RUNNING'`, run.ID, now, err.Error())
		return nil, err
	}
	if len(calc.lines) == 0 {
		now := time.Now().UTC()
		_, _ = s.db.ExecContext(ctx, `UPDATE backorder_runs SET status='FAILED',completed_at=$2,error_text='no committed open demand in horizon' WHERE id=$1 AND status='RUNNING'`, run.ID, now)
		return nil, domain.NewConflict("no committed open demand in BOP horizon")
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	for i := range calc.lines {
		l := &calc.lines[i]
		l.ID, l.RunID = uuid.New(), run.ID
		if _, err := tx.ExecContext(ctx, `
INSERT INTO backorder_run_lines(
 id,run_id,sales_order_id,sales_order_line_id,item_id,customer_id,service_class_code,order_priority,rank_no,
 open_qty,allocated_qty,current_promised_date,proposed_promised_date,atp_qty,ctp_qty,backorder_qty,decision,
 constraint_type,allocation_plan_id,allocation_bucket_pct,detail)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::jsonb)`,
			l.ID, l.RunID, l.SalesOrderID, l.SalesOrderLineID, l.ItemID, l.CustomerID, l.ServiceClassCode, l.OrderPriority, l.RankNo,
			l.OpenQty, l.AllocatedQty, l.CurrentPromisedDate, l.ProposedPromisedDate, l.ATPQty, l.CTPQty, l.BackorderQty, l.Decision,
			l.ConstraintType, l.AllocationPlanID, l.AllocationBucketPct, string(l.Detail)); err != nil {
			return nil, err
		}
	}
	for i := range calc.confirmations {
		c := &calc.confirmations[i]
		c.ID, c.RunID = uuid.New(), run.ID
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO backorder_run_confirmations(id,run_id,sales_order_line_id,sequence_no,quantity,confirmed_date,source)
VALUES(:id,:run_id,:sales_order_line_id,:sequence_no,:quantity,:confirmed_date,:source)`, c); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE backorder_runs SET status='SUCCEEDED',completed_at=$2,result_hash=$3 WHERE id=$1 AND status='RUNNING'`, run.ID, now, calc.hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, run.ID)
}

func (s *BackorderService) candidates(ctx context.Context, horizon int, filterItemID *uuid.UUID) ([]bopCandidate, error) {
	start := TruncateDay(time.Now())
	end := start.AddDate(0, 0, horizon-1)
	query := `
SELECT so.id AS sales_order_id,so.order_no AS sales_order_no,l.id AS sales_order_line_id,l.line_no,
       l.item_id,i.code AS item_code,i.name AS item_name,so.customer_id,c.customer_no,c.name AS customer_name,
       c.service_class_code,sc.priority_rank AS service_priority,so.priority AS order_priority,so.order_date,
       l.requested_date,l.promised_date AS current_promised_date,
       GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0)::double precision AS open_qty,
       LEAST(l.allocated_qty,GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0))::double precision AS allocated_qty
  FROM sales_order_lines l
  JOIN sales_orders so ON so.id=l.sales_order_id
  JOIN customers c ON c.id=so.customer_id
  JOIN customer_service_classes sc ON sc.code=c.service_class_code
  JOIN items i ON i.id=l.item_id
 WHERE so.status IN ('CONFIRMED','PARTIALLY_SHIPPED')
   AND GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0)>0
   AND (l.requested_date <= $1 OR COALESCE(l.promised_date,l.requested_date) <= $1)`
	args := []any{end}
	if filterItemID != nil {
		query += ` AND l.item_id=$2`
		args = append(args, *filterItemID)
	}
	var rows []bopCandidate
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *BackorderService) calculate(ctx context.Context, horizon int, filterItemID *uuid.UUID) (*calculatedBackorder, error) {
	rows, err := s.candidates(ctx, horizon, filterItemID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &calculatedBackorder{hash: canonicalBackorderHash(horizon, filterItemID, nil, nil)}, nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		pi, pj := bopPriorityRank(rows[i].OrderPriority), bopPriorityRank(rows[j].OrderPriority)
		if pi != pj {
			return pi < pj
		}
		if rows[i].ServicePriority != rows[j].ServicePriority {
			return rows[i].ServicePriority < rows[j].ServicePriority
		}
		ri, rj := TruncateDay(rows[i].RequestedDate), TruncateDay(rows[j].RequestedDate)
		if !ri.Equal(rj) {
			return ri.Before(rj)
		}
		oi, oj := TruncateDay(rows[i].OrderDate), TruncateDay(rows[j].OrderDate)
		if !oi.Equal(oj) {
			return oi.Before(oj)
		}
		if rows[i].SalesOrderNo != rows[j].SalesOrderNo {
			return rows[i].SalesOrderNo < rows[j].SalesOrderNo
		}
		return rows[i].LineNo < rows[j].LineNo
	})

	lineIDs := make([]uuid.UUID, 0, len(rows))
	items := map[uuid.UUID]struct{}{}
	for _, r := range rows {
		lineIDs = append(lineIDs, r.SalesOrderLineID)
		items[r.ItemID] = struct{}{}
	}
	start := TruncateDay(time.Now())
	horizonEnd := start.AddDate(0, 0, horizon-1)
	runtime := map[uuid.UUID]*allocationRuntime{}
	for itemID := range items {
		pool, err := s.atp.AvailableThrough(ctx, itemID, horizonEnd, ATPAvailabilityOptions{ExcludeSalesOrderLineIDs: lineIDs})
		if err != nil {
			return nil, err
		}
		if pool < 0 {
			pool = 0
		}
		policy, err := s.allocation.ActivePolicy(ctx, itemID, start)
		if err != nil {
			return nil, err
		}
		rt := &allocationRuntime{policy: policy, pool: pool, bucketLimit: map[string]float64{}, bucketUsed: map[string]float64{}}
		if policy != nil {
			for code, bucket := range policy.Buckets {
				rt.bucketLimit[code] = pool * bucket.AllocationPct / 100
			}
		}
		runtime[itemID] = rt
	}

	consumedATP := map[uuid.UUID]float64{}
	materialUsage := map[uuid.UUID]float64{}
	capacityNotBefore := start
	outLines := make([]domain.BackorderRunLine, 0, len(rows))
	confs := []domain.BackorderRunConfirmation{}
	for idx, r := range rows {
		requested := TruncateDay(r.RequestedDate)
		if requested.Before(start) {
			requested = start
		}
		openQty := r.OpenQty
		fixed := math.Min(math.Max(r.AllocatedQty, 0), openQty)
		line := domain.BackorderRunLine{
			SalesOrderID: r.SalesOrderID, SalesOrderNo: r.SalesOrderNo, SalesOrderLineID: r.SalesOrderLineID,
			ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, CustomerID: r.CustomerID, CustomerNo: r.CustomerNo,
			CustomerName: r.CustomerName, ServiceClassCode: r.ServiceClassCode, OrderPriority: r.OrderPriority, RankNo: idx + 1,
			OpenQty: openQty, AllocatedQty: fixed, CurrentPromisedDate: r.CurrentPromisedDate, ConstraintType: "NONE", Detail: json.RawMessage(`{}`),
		}
		seq := 1
		if fixed > 1e-9 {
			confs = append(confs, domain.BackorderRunConfirmation{SalesOrderLineID: r.SalesOrderLineID, SequenceNo: seq, Quantity: fixed, ConfirmedDate: requested, Source: "ALLOCATED"})
			seq++
		}
		remaining := openQty - fixed
		available, err := s.atp.AvailableThrough(ctx, r.ItemID, requested, ATPAvailabilityOptions{ExcludeSalesOrderLineIDs: lineIDs})
		if err != nil {
			return nil, err
		}
		available -= consumedATP[r.ItemID]
		if available < 0 {
			available = 0
		}
		freeBeforeAllocation := math.Min(remaining, available)
		atpQty := freeBeforeAllocation
		rt := runtime[r.ItemID]
		allocationLimited := false
		allocationDetail := map[string]any{}
		if rt != nil && rt.policy != nil {
			line.AllocationPlanID = &rt.policy.Plan.ID
			bucket, ok := rt.policy.Buckets[r.ServiceClassCode]
			if ok {
				pct := bucket.AllocationPct
				line.AllocationBucketPct = &pct
				capRemaining := rt.bucketLimit[r.ServiceClassCode] - rt.bucketUsed[r.ServiceClassCode]
				if capRemaining < 0 {
					capRemaining = 0
				}
				if atpQty > capRemaining {
					atpQty = capRemaining
					allocationLimited = true
				}
				allocationDetail = map[string]any{"planId": rt.policy.Plan.ID, "planName": rt.policy.Plan.Name, "pool": rt.pool, "serviceClass": r.ServiceClassCode, "allocationPct": pct, "bucketLimit": rt.bucketLimit[r.ServiceClassCode], "bucketUsedBefore": rt.bucketUsed[r.ServiceClassCode]}
				rt.bucketUsed[r.ServiceClassCode] += atpQty
			} else {
				atpQty = 0
				allocationLimited = freeBeforeAllocation > 1e-9
				allocationDetail = map[string]any{"planId": rt.policy.Plan.ID, "planName": rt.policy.Plan.Name, "pool": rt.pool, "serviceClass": r.ServiceClassCode, "reason": "service class has no allocation bucket"}
			}
		}
		if atpQty < 0 {
			atpQty = 0
		}
		line.ATPQty = atpQty
		consumedATP[r.ItemID] += atpQty
		if atpQty > 1e-9 {
			confs = append(confs, domain.BackorderRunConfirmation{SalesOrderLineID: r.SalesOrderLineID, SequenceNo: seq, Quantity: atpQty, ConfirmedDate: requested, Source: "ATP"})
			seq++
		}
		remaining -= atpQty

		detail := map[string]any{"atpAvailableBeforeSharedUse": available + consumedATP[r.ItemID] - atpQty, "allocation": allocationDetail}
		if allocationLimited {
			line.ConstraintType = "PRODUCT_ALLOCATION"
		}
		if remaining > 1e-9 {
			mat, err := s.ctp.MaterialReadyWithUsage(ctx, r.ItemID, remaining, start, horizon, materialUsage)
			if err != nil {
				return nil, err
			}
			md := TruncateDay(mat.ReadyDate)
			detail["material"] = mat.Detail
			if md.After(requested) {
				if line.ConstraintType == "NONE" {
					line.ConstraintType = "MATERIAL"
				} else {
					detail["secondaryConstraint"] = "MATERIAL"
				}
			}
			if md.After(horizonEnd) {
				line.BackorderQty = remaining
				line.ConstraintType = "HORIZON"
				detail["reason"] = "material ready date exceeds BOP horizon"
			} else {
				capacityStart := md
				if capacityNotBefore.After(capacityStart) {
					capacityStart = capacityNotBefore
				}
				capOrder, capErr := s.ctp.crp.SimulateCTPOrder(ctx, r.ItemID, remaining, capacityStart, requested, horizon)
				if capErr != nil || capOrder == nil || capOrder.ScheduledEnd == nil || capOrder.ScheduleStatus == "UNSCHEDULED" {
					line.BackorderQty = remaining
					if line.ConstraintType == "PRODUCT_ALLOCATION" {
						detail["secondaryConstraint"] = "CAPACITY"
					} else {
						line.ConstraintType = "CAPACITY"
					}
					detail["reason"] = "no feasible capacity within BOP horizon"
				} else {
					capacityNotBefore = *capOrder.ScheduledEnd
					capDate := TruncateDay(*capOrder.ScheduledEnd)
					promiseDate := capDate
					if promiseDate.Before(requested) {
						promiseDate = requested
					}
					if promiseDate.After(horizonEnd) {
						line.BackorderQty = remaining
						line.ConstraintType = "HORIZON"
						detail["reason"] = "capacity finish exceeds BOP horizon"
					} else {
						line.CTPQty = remaining
						confs = append(confs, domain.BackorderRunConfirmation{SalesOrderLineID: r.SalesOrderLineID, SequenceNo: seq, Quantity: remaining, ConfirmedDate: promiseDate, Source: mat.Source})
						detail["capacityStatus"] = capOrder.ScheduleStatus
						if capDate.After(requested) {
							switch line.ConstraintType {
							case "NONE":
								line.ConstraintType = "CAPACITY"
							case "MATERIAL":
								line.ConstraintType = "MATERIAL_AND_CAPACITY"
							case "PRODUCT_ALLOCATION":
								detail["secondaryConstraint"] = "CAPACITY"
							}
						}
					}
				}
			}
		}
		covered := fixed + line.ATPQty + line.CTPQty
		if line.BackorderQty <= 1e-9 {
			// Rounding protection: the evidence must close exactly to open quantity.
			if math.Abs(covered-openQty) <= 1e-6 {
				covered = openQty
			}
			var latest time.Time
			for _, c := range confs {
				if c.SalesOrderLineID == r.SalesOrderLineID && (latest.IsZero() || c.ConfirmedDate.After(latest)) {
					latest = c.ConfirmedDate
				}
			}
			if !latest.IsZero() {
				d := TruncateDay(latest)
				line.ProposedPromisedDate = &d
			}
		} else {
			line.ProposedPromisedDate = nil // partial confirmation is explicit evidence; header date must not imply full promise.
		}
		if line.BackorderQty <= 1e-9 && covered+1e-6 < openQty {
			line.BackorderQty = openQty - covered
			line.ProposedPromisedDate = nil
		}
		line.Decision = backorderDecision(line.CurrentPromisedDate, line.ProposedPromisedDate, line.BackorderQty)
		detail["fixedAllocatedQty"] = fixed
		detail["rank"] = line.RankNo
		line.Detail, _ = json.Marshal(detail)
		outLines = append(outLines, line)
	}
	hash := canonicalBackorderHash(horizon, filterItemID, outLines, confs)
	return &calculatedBackorder{lines: outLines, confirmations: confs, hash: hash}, nil
}

func backorderDecision(current, proposed *time.Time, backorderQty float64) string {
	if backorderQty > 1e-9 || proposed == nil {
		return "BACKORDER"
	}
	if current == nil {
		return "NEW_PROMISE"
	}
	c, p := TruncateDay(*current), TruncateDay(*proposed)
	if p.Before(c) {
		return "IMPROVED"
	}
	if p.After(c) {
		return "DELAYED"
	}
	return "UNCHANGED"
}

type bopHashLine struct {
	LineID        string  `json:"lineId"`
	Rank          int     `json:"rank"`
	ServiceClass  string  `json:"serviceClass"`
	OrderPriority string  `json:"orderPriority"`
	OpenQty       float64 `json:"openQty"`
	AllocatedQty  float64 `json:"allocatedQty"`
	ATPQty        float64 `json:"atpQty"`
	CTPQty        float64 `json:"ctpQty"`
	BackorderQty  float64 `json:"backorderQty"`
	Current       string  `json:"current"`
	Proposed      string  `json:"proposed"`
	Decision      string  `json:"decision"`
	Constraint    string  `json:"constraint"`
	PlanID        string  `json:"planId"`
	BucketPct     float64 `json:"bucketPct"`
}

type bopHashConfirmation struct {
	LineID   string  `json:"lineId"`
	Sequence int     `json:"sequence"`
	Quantity float64 `json:"quantity"`
	Date     string  `json:"date"`
	Source   string  `json:"source"`
}

func canonicalBackorderHash(horizon int, filterItemID *uuid.UUID, lines []domain.BackorderRunLine, confs []domain.BackorderRunConfirmation) string {
	hl := make([]bopHashLine, 0, len(lines))
	for _, l := range lines {
		planID := ""
		if l.AllocationPlanID != nil {
			planID = l.AllocationPlanID.String()
		}
		pct := 0.0
		if l.AllocationBucketPct != nil {
			pct = *l.AllocationBucketPct
		}
		hl = append(hl, bopHashLine{LineID: l.SalesOrderLineID.String(), Rank: l.RankNo, ServiceClass: l.ServiceClassCode, OrderPriority: l.OrderPriority, OpenQty: l.OpenQty, AllocatedQty: l.AllocatedQty, ATPQty: l.ATPQty, CTPQty: l.CTPQty, BackorderQty: l.BackorderQty, Current: dateHashValue(l.CurrentPromisedDate), Proposed: dateHashValue(l.ProposedPromisedDate), Decision: l.Decision, Constraint: l.ConstraintType, PlanID: planID, BucketPct: pct})
	}
	hc := make([]bopHashConfirmation, 0, len(confs))
	for _, c := range confs {
		hc = append(hc, bopHashConfirmation{LineID: c.SalesOrderLineID.String(), Sequence: c.SequenceNo, Quantity: c.Quantity, Date: TruncateDay(c.ConfirmedDate).Format("2006-01-02"), Source: c.Source})
	}
	sort.Slice(hc, func(i, j int) bool {
		if hc[i].LineID != hc[j].LineID {
			return hc[i].LineID < hc[j].LineID
		}
		return hc[i].Sequence < hc[j].Sequence
	})
	filter := ""
	if filterItemID != nil {
		filter = filterItemID.String()
	}
	payload, _ := json.Marshal(struct {
		Horizon int                   `json:"horizon"`
		Filter  string                `json:"filter"`
		Lines   []bopHashLine         `json:"lines"`
		Confs   []bopHashConfirmation `json:"confirmations"`
	}{horizon, filter, hl, hc})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (s *BackorderService) Publish(ctx context.Context, in BackorderPublishInput, actor SalesOrderActor) (*domain.BackorderResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if in.RunID == uuid.Nil {
		return nil, domain.NewBadRequest("runId is required", nil)
	}
	stored, err := s.GetRun(ctx, in.RunID)
	if err != nil {
		return nil, err
	}
	if stored.Run.Status != "SUCCEEDED" || stored.Run.ResultHash == nil {
		return nil, domain.NewConflict("only a successful BOP preview can be published")
	}
	if stored.Publication != nil {
		return stored, nil
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockBOPPlanningSnapshot(ctx, tx); err != nil {
		return nil, err
	}
	orderIDs, itemIDs := uniqueBOPResources(stored.Lines)
	for _, id := range orderIDs {
		var locked uuid.UUID
		if err := tx.GetContext(ctx, &locked, `SELECT id FROM sales_orders WHERE id=$1 FOR UPDATE`, id); err != nil {
			return nil, err
		}
	}
	for _, id := range itemIDs {
		var locked uuid.UUID
		if err := tx.GetContext(ctx, &locked, `SELECT id FROM items WHERE id=$1 FOR UPDATE`, id); err != nil {
			return nil, err
		}
	}
	var existing domain.BackorderPublication
	if err := tx.GetContext(ctx, &existing, `SELECT * FROM backorder_publications WHERE run_id=$1`, in.RunID); err == nil {
		_ = tx.Commit()
		return s.GetRun(ctx, in.RunID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// Recalculate only after order/item locks are held. Inventory allocation and
	// shipment paths lock items too, so a concurrent scarce-supply change cannot
	// silently pass the stale-result check.
	fresh, err := s.calculate(ctx, stored.Run.HorizonDays, stored.Run.FilterItemID)
	if err != nil {
		return nil, err
	}
	if fresh.hash != *stored.Run.ResultHash {
		return nil, domain.NewConflict("STALE_BOP: demand/supply/capacity or allocation policy changed; run Preview again")
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL cpim.bop_publish='on'`); err != nil {
		return nil, err
	}
	publication := domain.BackorderPublication{ID: uuid.New(), RunID: in.RunID, ResultHash: *stored.Run.ResultHash, PublishedByUserID: actor.UserID, PublishedBy: actor.Username, PublishedAt: time.Now().UTC()}
	if _, err := tx.NamedExecContext(ctx, `INSERT INTO backorder_publications(id,run_id,result_hash,published_by_user_id,published_by,published_at) VALUES(:id,:run_id,:result_hash,:published_by_user_id,:published_by,:published_at)`, &publication); err != nil {
		return nil, err
	}
	for _, l := range stored.Lines {
		if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET promised_date=$2 WHERE id=$1`, l.SalesOrderLineID, l.ProposedPromisedDate); err != nil {
			return nil, err
		}
	}
	for _, orderID := range orderIDs {
		var header *time.Time
		if err := tx.GetContext(ctx, &header, `SELECT MAX(promised_date) FROM sales_order_lines WHERE sales_order_id=$1`, orderID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sales_orders SET promised_date=$2,updated_at=now() WHERE id=$1`, orderID, header); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, in.RunID)
}

// lockBOPPlanningSnapshot freezes the mutable planning inputs for the short
// Publish transaction. calculate() deliberately remains a side-effect-free
// reader on the shared DB handle; these SHARE locks prevent supply, demand,
// quality, routing/capacity, customer priority, or allocation-policy writes
// from slipping between the stale-hash recalculation and the publication.
func lockBOPPlanningSnapshot(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `
LOCK TABLE
  sales_orders, sales_order_lines, customers, customer_service_classes,
  product_allocation_plans, product_allocation_buckets,
  items, inventory_txns, lots, lot_movements,
  purchase_orders, purchase_receipts, work_orders, work_order_completions, wo_operations, wo_operation_alternatives,
  supplier_quality_profiles, bom_components, mps_entries, forecast_runs, forecast_values, demand_forecasts,
  routings, routing_operations, routing_operation_alternatives,
  work_centers, work_center_setup_matrix, work_calendars, calendar_exceptions
IN SHARE MODE`)
	return err
}

func uniqueBOPResources(lines []domain.BackorderRunLine) ([]uuid.UUID, []uuid.UUID) {
	orders, items := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, l := range lines {
		orders[l.SalesOrderID], items[l.ItemID] = true, true
	}
	orderIDs, itemIDs := make([]uuid.UUID, 0, len(orders)), make([]uuid.UUID, 0, len(items))
	for id := range orders {
		orderIDs = append(orderIDs, id)
	}
	for id := range items {
		itemIDs = append(itemIDs, id)
	}
	sort.Slice(orderIDs, func(i, j int) bool { return orderIDs[i].String() < orderIDs[j].String() })
	sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i].String() < itemIDs[j].String() })
	return orderIDs, itemIDs
}

func (s *BackorderService) ListRuns(ctx context.Context) ([]domain.BackorderRun, error) {
	var rows []domain.BackorderRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM backorder_runs ORDER BY requested_at DESC,id DESC LIMIT 100`)
	return rows, err
}

func (s *BackorderService) GetRun(ctx context.Context, id uuid.UUID) (*domain.BackorderResult, error) {
	var run domain.BackorderRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM backorder_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("backorder run")
		}
		return nil, err
	}
	res := &domain.BackorderResult{Run: run, Lines: []domain.BackorderRunLine{}, Confirmations: []domain.BackorderRunConfirmation{}}
	if err := s.db.SelectContext(ctx, &res.Lines, `
SELECT l.*,so.order_no AS sales_order_no,i.code AS item_code,i.name AS item_name,
       c.customer_no,c.name AS customer_name
  FROM backorder_run_lines l
  JOIN sales_orders so ON so.id=l.sales_order_id
  JOIN items i ON i.id=l.item_id
  JOIN customers c ON c.id=l.customer_id
 WHERE l.run_id=$1 ORDER BY l.rank_no,l.sales_order_line_id`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.Confirmations, `SELECT * FROM backorder_run_confirmations WHERE run_id=$1 ORDER BY sales_order_line_id,sequence_no`, id); err != nil {
		return nil, err
	}
	var p domain.BackorderPublication
	if err := s.db.GetContext(ctx, &p, `SELECT * FROM backorder_publications WHERE run_id=$1`, id); err == nil {
		res.Publication = &p
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return res, nil
}

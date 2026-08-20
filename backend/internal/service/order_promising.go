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

const defaultPromiseHorizonDays = 180

type OrderPromiseCheckInput struct {
	HorizonDays int `json:"horizonDays"`
}

type OrderPromiseAcceptInput struct {
	RunID uuid.UUID `json:"runId"`
}

type OrderPromisingService struct {
	db    *sqlx.DB
	sales *SalesOrderService
	atp   *ATPService
	ctp   *CTPEngine
}

type calculatedPromise struct {
	lines         []domain.OrderPromiseLineResult
	confirmations []domain.OrderPromiseConfirmation
	hash          string
}

func normalizePromiseHorizon(v int) (int, error) {
	if v <= 0 {
		return defaultPromiseHorizonDays, nil
	}
	if v > 366 {
		return 0, domain.NewBadRequest("horizonDays must be <= 366", nil)
	}
	return v, nil
}

func (s *OrderPromisingService) Check(ctx context.Context, orderID uuid.UUID, in OrderPromiseCheckInput, actor SalesOrderActor) (*domain.OrderPromiseResult, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	horizon, err := normalizePromiseHorizon(in.HorizonDays)
	if err != nil {
		return nil, err
	}
	detail, err := s.sales.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if detail.Order.Status == "SHIPPED" || detail.Order.Status == "CANCELLED" {
		return nil, domain.NewConflict("terminal sales order cannot be promised")
	}

	run := domain.OrderPromiseRun{
		ID: uuid.New(), SalesOrderID: orderID, Strategy: "ATP_THEN_CTP", Status: "RUNNING",
		RequestedAt: time.Now().UTC(), HorizonDays: horizon, RequestedByUserID: actor.UserID, RequestedBy: actor.Username,
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO order_promise_runs(id,sales_order_id,strategy,status,requested_at,horizon_days,requested_by_user_id,requested_by)
VALUES($1,$2,$3,'RUNNING',$4,$5,$6,$7)`, run.ID, orderID, run.Strategy, run.RequestedAt, horizon, actor.UserID, actor.Username); err != nil {
		return nil, err
	}

	calc, err := s.calculate(ctx, detail, horizon)
	if err != nil {
		now := time.Now().UTC()
		_, _ = s.db.ExecContext(ctx, `UPDATE order_promise_runs SET status='FAILED',completed_at=$2,error_text=$3 WHERE id=$1 AND status='RUNNING'`, run.ID, now, err.Error())
		return nil, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	for i := range calc.lines {
		calc.lines[i].ID = uuid.New()
		calc.lines[i].RunID = run.ID
		l := &calc.lines[i]
		if _, err := tx.ExecContext(ctx, `
INSERT INTO order_promise_line_results(id,run_id,sales_order_line_id,requested_qty,requested_date,atp_qty,ctp_qty,earliest_full_date,promise_method,material_ready_date,capacity_ready_date,constraint_type,constraint_detail)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb)`,
			l.ID, l.RunID, l.SalesOrderLineID, l.RequestedQty, l.RequestedDate, l.ATPQty, l.CTPQty, l.EarliestFullDate, l.PromiseMethod, l.MaterialReadyDate, l.CapacityReadyDate, l.ConstraintType, string(l.ConstraintDetail)); err != nil {
			return nil, err
		}
	}
	for i := range calc.confirmations {
		calc.confirmations[i].ID = uuid.New()
		calc.confirmations[i].RunID = run.ID
		if _, err := tx.NamedExecContext(ctx, `
INSERT INTO order_promise_confirmations(id,run_id,sales_order_line_id,sequence_no,quantity,confirmed_date,source)
VALUES(:id,:run_id,:sales_order_line_id,:sequence_no,:quantity,:confirmed_date,:source)`, &calc.confirmations[i]); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE order_promise_runs SET status='SUCCEEDED',completed_at=$2,result_hash=$3 WHERE id=$1 AND status='RUNNING'`, run.ID, now, calc.hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, run.ID)
}

func (s *OrderPromisingService) calculate(ctx context.Context, detail *domain.SalesOrderDetail, horizon int) (*calculatedPromise, error) {
	lineIDs := make([]uuid.UUID, 0, len(detail.Lines))
	currentAllocatedByItem := map[uuid.UUID]float64{}
	for _, l := range detail.Lines {
		lineIDs = append(lineIDs, l.ID)
		currentAllocatedByItem[l.ItemID] += l.AllocatedQty
	}
	consumedATP := map[uuid.UUID]float64{}
	materialUsage := map[uuid.UUID]float64{}
	lines := []domain.OrderPromiseLineResult{}
	confs := []domain.OrderPromiseConfirmation{}
	start := TruncateDay(time.Now())
	horizonEnd := start.AddDate(0, 0, horizon-1)
	capacityNotBefore := start

	// Promise the earliest requested demand first. This avoids a later same-item
	// line consuming ATP that an earlier requested line should receive.
	calcLines := append([]domain.SalesOrderLine(nil), detail.Lines...)
	sort.SliceStable(calcLines, func(i, j int) bool {
		li, lj := TruncateDay(calcLines[i].RequestedDate), TruncateDay(calcLines[j].RequestedDate)
		if li.Equal(lj) {
			return calcLines[i].LineNo < calcLines[j].LineNo
		}
		return li.Before(lj)
	})

	for _, line := range calcLines {
		openQty := math.Max(line.Quantity-line.ShippedQty-line.CancelledQty, 0)
		if openQty <= 1e-9 {
			continue
		}
		requested := TruncateDay(line.RequestedDate)
		available, err := s.atp.AvailableThrough(ctx, line.ItemID, requested, ATPAvailabilityOptions{ExcludeSalesOrderLineIDs: lineIDs, IncludeAllocatedQty: currentAllocatedByItem[line.ItemID]})
		if err != nil {
			return nil, err
		}
		available -= consumedATP[line.ItemID]
		if available < 0 {
			available = 0
		}
		atpQty := math.Min(openQty, available)
		consumedATP[line.ItemID] += atpQty

		lr := domain.OrderPromiseLineResult{SalesOrderLineID: line.ID, RequestedQty: openQty, RequestedDate: requested, ATPQty: atpQty, PromiseMethod: "UNAVAILABLE", ConstraintType: "NONE", ConstraintDetail: json.RawMessage(`{}`)}
		seq := 1
		if atpQty > 1e-9 {
			confs = append(confs, domain.OrderPromiseConfirmation{SalesOrderLineID: line.ID, SequenceNo: seq, Quantity: atpQty, ConfirmedDate: requested, Source: "ATP"})
			seq++
		}
		remaining := openQty - atpQty
		if remaining <= 1e-9 {
			lr.PromiseMethod = "ATP"
			d := requested
			lr.EarliestFullDate = &d
			lines = append(lines, lr)
			continue
		}

		mat, err := s.ctp.MaterialReadyWithUsage(ctx, line.ItemID, remaining, start, horizon, materialUsage)
		if err != nil {
			return nil, err
		}
		md := TruncateDay(mat.ReadyDate)
		lr.MaterialReadyDate = &md
		constraint := "NONE"
		if md.After(requested) {
			constraint = "MATERIAL"
		}
		if md.After(horizonEnd) {
			lr.ConstraintType = "HORIZON"
			b, _ := json.Marshal(map[string]any{"material": mat.Detail, "reason": "material ready date exceeds horizon"})
			lr.ConstraintDetail = b
			if atpQty > 1e-9 {
				lr.PromiseMethod = "ATP_CTP"
			}
			lines = append(lines, lr)
			continue
		}

		capacityStart := md
		if capacityNotBefore.After(capacityStart) {
			capacityStart = capacityNotBefore
		}
		capOrder, capErr := s.ctp.crp.SimulateCTPOrder(ctx, line.ItemID, remaining, capacityStart, requested, horizon)
		if capErr != nil || capOrder == nil || capOrder.ScheduledEnd == nil || capOrder.ScheduleStatus == "UNSCHEDULED" {
			lr.ConstraintType = "CAPACITY"
			b, _ := json.Marshal(map[string]any{"material": mat.Detail, "reason": "no feasible capacity within horizon"})
			lr.ConstraintDetail = b
			if atpQty > 1e-9 {
				lr.PromiseMethod = "ATP_CTP"
			}
			lines = append(lines, lr)
			continue
		}
		capacityDate := TruncateDay(*capOrder.ScheduledEnd)
		capacityNotBefore = *capOrder.ScheduledEnd
		lr.CapacityReadyDate = &capacityDate
		promiseDate := capacityDate
		if promiseDate.Before(requested) {
			promiseDate = requested
		}
		if promiseDate.After(horizonEnd) {
			lr.ConstraintType = "HORIZON"
			b, _ := json.Marshal(map[string]any{"material": mat.Detail, "capacityStatus": capOrder.ScheduleStatus})
			lr.ConstraintDetail = b
			if atpQty > 1e-9 {
				lr.PromiseMethod = "ATP_CTP"
			}
			lines = append(lines, lr)
			continue
		}
		lr.CTPQty = remaining
		lr.EarliestFullDate = &promiseDate
		if atpQty > 1e-9 {
			lr.PromiseMethod = "ATP_CTP"
		} else {
			lr.PromiseMethod = "CTP"
		}
		if capacityDate.After(requested) {
			if constraint == "MATERIAL" {
				constraint = "MATERIAL_AND_CAPACITY"
			} else {
				constraint = "CAPACITY"
			}
		}
		lr.ConstraintType = constraint
		b, _ := json.Marshal(map[string]any{"material": mat.Detail, "capacityStatus": capOrder.ScheduleStatus})
		lr.ConstraintDetail = b
		confs = append(confs, domain.OrderPromiseConfirmation{SalesOrderLineID: line.ID, SequenceNo: seq, Quantity: remaining, ConfirmedDate: promiseDate, Source: mat.Source})
		lines = append(lines, lr)
	}
	h := canonicalPromiseHash(lines, confs)
	return &calculatedPromise{lines: lines, confirmations: confs, hash: h}, nil
}

type promiseHashLine struct {
	LineID        string  `json:"lineId"`
	RequestedQty  float64 `json:"requestedQty"`
	RequestedDate string  `json:"requestedDate"`
	ATPQty        float64 `json:"atpQty"`
	CTPQty        float64 `json:"ctpQty"`
	Earliest      string  `json:"earliest"`
	Material      string  `json:"material"`
	Capacity      string  `json:"capacity"`
	Method        string  `json:"method"`
	Constraint    string  `json:"constraint"`
}
type promiseHashConfirmation struct {
	LineID   string  `json:"lineId"`
	Sequence int     `json:"sequence"`
	Quantity float64 `json:"quantity"`
	Date     string  `json:"date"`
	Source   string  `json:"source"`
}

func dateHashValue(t *time.Time) string {
	if t == nil {
		return ""
	}
	return TruncateDay(*t).Format("2006-01-02")
}
func canonicalPromiseHash(lines []domain.OrderPromiseLineResult, confs []domain.OrderPromiseConfirmation) string {
	ls := make([]promiseHashLine, 0, len(lines))
	for _, l := range lines {
		ls = append(ls, promiseHashLine{l.SalesOrderLineID.String(), l.RequestedQty, TruncateDay(l.RequestedDate).Format("2006-01-02"), l.ATPQty, l.CTPQty, dateHashValue(l.EarliestFullDate), dateHashValue(l.MaterialReadyDate), dateHashValue(l.CapacityReadyDate), l.PromiseMethod, l.ConstraintType})
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].LineID < ls[j].LineID })
	cs := make([]promiseHashConfirmation, 0, len(confs))
	for _, c := range confs {
		cs = append(cs, promiseHashConfirmation{c.SalesOrderLineID.String(), c.SequenceNo, c.Quantity, TruncateDay(c.ConfirmedDate).Format("2006-01-02"), c.Source})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].LineID == cs[j].LineID {
			return cs[i].Sequence < cs[j].Sequence
		}
		return cs[i].LineID < cs[j].LineID
	})
	b, _ := json.Marshal(struct {
		Lines         []promiseHashLine         `json:"lines"`
		Confirmations []promiseHashConfirmation `json:"confirmations"`
	}{ls, cs})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *OrderPromisingService) Accept(ctx context.Context, orderID uuid.UUID, in OrderPromiseAcceptInput, actor SalesOrderActor) (*domain.OrderPromiseResult, error) {
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
	if stored.Run.SalesOrderID != orderID {
		return nil, domain.NewConflict("promise run belongs to a different sales order")
	}
	if stored.Run.Status != "SUCCEEDED" || stored.Run.ResultHash == nil {
		return nil, domain.NewConflict("only successful promise runs can be accepted")
	}
	if stored.Acceptance != nil {
		return stored, nil
	}
	for _, l := range stored.Lines {
		if l.ATPQty+l.CTPQty+1e-9 < l.RequestedQty || l.EarliestFullDate == nil {
			return nil, domain.NewConflict("promise run does not fully cover open demand")
		}
	}
	detail, err := s.sales.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if detail.Order.Status == "SHIPPED" || detail.Order.Status == "CANCELLED" {
		return nil, domain.NewConflict("terminal sales order cannot accept a promise")
	}
	fresh, err := s.calculate(ctx, detail, stored.Run.HorizonDays)
	if err != nil {
		return nil, err
	}
	if fresh.hash != *stored.Run.ResultHash {
		return nil, domain.NewConflict("STALE_PROMISE: supply/capacity changed; run promise check again")
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var locked uuid.UUID
	if err := tx.GetContext(ctx, &locked, `SELECT id FROM sales_orders WHERE id=$1 FOR UPDATE`, orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("sales order")
		}
		return nil, err
	}
	var existing domain.OrderPromiseAcceptance
	if err := tx.GetContext(ctx, &existing, `SELECT * FROM order_promise_acceptances WHERE run_id=$1`, in.RunID); err == nil {
		_ = tx.Commit()
		return s.GetRun(ctx, in.RunID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL cpim.promise_accept='on'`); err != nil {
		return nil, err
	}
	accept := domain.OrderPromiseAcceptance{ID: uuid.New(), RunID: in.RunID, SalesOrderID: orderID, ResultHash: *stored.Run.ResultHash, AcceptedByUserID: actor.UserID, AcceptedBy: actor.Username, AcceptedAt: time.Now().UTC()}
	if _, err := tx.NamedExecContext(ctx, `INSERT INTO order_promise_acceptances(id,run_id,sales_order_id,result_hash,accepted_by_user_id,accepted_by,accepted_at) VALUES(:id,:run_id,:sales_order_id,:result_hash,:accepted_by_user_id,:accepted_by,:accepted_at)`, &accept); err != nil {
		return nil, err
	}
	lineDate := map[uuid.UUID]time.Time{}
	for _, c := range stored.Confirmations {
		d := TruncateDay(c.ConfirmedDate)
		if cur, ok := lineDate[c.SalesOrderLineID]; !ok || d.After(cur) {
			lineDate[c.SalesOrderLineID] = d
		}
	}
	for _, line := range detail.Lines {
		if d, ok := lineDate[line.ID]; ok {
			if _, err := tx.ExecContext(ctx, `UPDATE sales_order_lines SET promised_date=$2 WHERE id=$1`, line.ID, d); err != nil {
				return nil, err
			}
		}
	}
	if len(lineDate) == 0 {
		return nil, domain.NewConflict("accepted promise has no confirmations")
	}
	var header time.Time
	if err := tx.GetContext(ctx, &header, `SELECT MAX(promised_date) FROM sales_order_lines WHERE sales_order_id=$1`, orderID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_orders SET promised_date=$2,updated_at=now() WHERE id=$1`, orderID, header); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, in.RunID)
}

func (s *OrderPromisingService) ListRuns(ctx context.Context, orderID uuid.UUID) ([]domain.OrderPromiseRun, error) {
	var rows []domain.OrderPromiseRun
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM order_promise_runs WHERE sales_order_id=$1 ORDER BY requested_at DESC,id DESC LIMIT 100`, orderID)
	return rows, err
}

func (s *OrderPromisingService) GetRun(ctx context.Context, id uuid.UUID) (*domain.OrderPromiseResult, error) {
	var run domain.OrderPromiseRun
	if err := s.db.GetContext(ctx, &run, `SELECT * FROM order_promise_runs WHERE id=$1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("order promise run")
		}
		return nil, err
	}
	res := &domain.OrderPromiseResult{Run: run, Lines: []domain.OrderPromiseLineResult{}, Confirmations: []domain.OrderPromiseConfirmation{}}
	if err := s.db.SelectContext(ctx, &res.Lines, `SELECT * FROM order_promise_line_results WHERE run_id=$1 ORDER BY sales_order_line_id`, id); err != nil {
		return nil, err
	}
	if err := s.db.SelectContext(ctx, &res.Confirmations, `SELECT * FROM order_promise_confirmations WHERE run_id=$1 ORDER BY sales_order_line_id,sequence_no`, id); err != nil {
		return nil, err
	}
	var a domain.OrderPromiseAcceptance
	if err := s.db.GetContext(ctx, &a, `SELECT * FROM order_promise_acceptances WHERE run_id=$1`, id); err == nil {
		res.Acceptance = &a
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return res, nil
}

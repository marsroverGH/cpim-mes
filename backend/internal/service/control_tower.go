package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ControlTowerService converts current immutable Planning Exception evidence
// into stable intervention cases and immutable scored snapshots.
type ControlTowerService struct {
	db *sqlx.DB
}

func NewControlTowerService(db *sqlx.DB) *ControlTowerService {
	return &ControlTowerService{db: db}
}

type ControlTowerRefreshResult struct {
	AsOf                   time.Time `json:"asOf"`
	ExceptionsEvaluated    int       `json:"exceptionsEvaluated"`
	CasesTouched           int       `json:"casesTouched"`
	SnapshotsCreated       int       `json:"snapshotsCreated"`
	RecommendationsCreated int       `json:"recommendationsCreated"`
}

type controlTowerSourceRow struct {
	ExceptionID      uuid.UUID       `db:"id"`
	RunID            uuid.UUID       `db:"run_id"`
	SalesOrderID     uuid.UUID       `db:"sales_order_id"`
	SalesOrderLineID *uuid.UUID      `db:"sales_order_line_id"`
	ExceptionKey     string          `db:"exception_key"`
	ExceptionType    string          `db:"exception_type"`
	Severity         string          `db:"severity"`
	Message          string          `db:"message"`
	ImpactDays       int             `db:"impact_days"`
	RootCausePath    json.RawMessage `db:"root_cause_path"`
	Detail           json.RawMessage `db:"detail"`
	DetectedAt       time.Time       `db:"detected_at"`

	OrderPriority    string `db:"order_priority"`
	ServiceClassCode string `db:"service_class_code"`
	ServiceClassRank int    `db:"service_class_rank"`

	OrderValue     float64 `db:"order_value"`
	OpenOrderValue float64 `db:"open_order_value"`
	RevenueAtRisk  float64 `db:"revenue_at_risk"`
}

func controlTowerCaseKey(x controlTowerSourceRow) string {
	line := "ORDER"
	if x.SalesOrderLineID != nil {
		line = x.SalesOrderLineID.String()
	}

	raw := strings.Join([]string{
		x.SalesOrderID.String(),
		line,
		strings.ToUpper(strings.TrimSpace(x.ExceptionType)),
		strings.TrimSpace(x.ExceptionKey),
	}, "|")

	sum := sha256.Sum256([]byte(raw))
	return "CT-" + hex.EncodeToString(sum[:])
}

func controlTowerRootRef(path, detail json.RawMessage) string {
	var xs []string
	if len(path) != 0 && json.Unmarshal(path, &xs) == nil && len(xs) != 0 {
		for i := len(xs) - 1; i >= 0; i-- {
			if strings.TrimSpace(xs[i]) != "" {
				return strings.TrimSpace(xs[i])
			}
		}
	}

	var m map[string]any
	if len(detail) != 0 && json.Unmarshal(detail, &m) == nil {
		keys := []string{
			"poNo",
			"workOrderNo",
			"sourceRef",
			"supplier",
			"itemCode",
			"rescheduleRunId",
		}
		for _, key := range keys {
			if v, ok := m[key]; ok {
				s := strings.TrimSpace(fmt.Sprint(v))
				if s != "" && s != "<nil>" {
					return s
				}
			}
		}
	}

	return ""
}

type controlTowerHashInput struct {
	CaseKey             string
	PlanningExceptionID uuid.UUID
	PeggingRunID        uuid.UUID
	Severity            string
	ImpactDays          int
	OrderValue          float64
	OpenOrderValue      float64
	OrderPriority       string
	ServiceClassCode    string
	RevenueAtRisk       float64
	RootCauseType       string
	RootCauseRef        string
	Score               ControlTowerScore
}

func canonicalControlTowerSnapshotHash(
	in controlTowerHashInput,
) (string, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (s *ControlTowerService) currentExceptionRows(
	ctx context.Context,
	tx *sqlx.Tx,
) ([]controlTowerSourceRow, error) {
	var rows []controlTowerSourceRow

	err := tx.SelectContext(ctx, &rows, `
SELECT
  x.id,
  x.run_id,
  x.sales_order_id,
  x.sales_order_line_id,
  x.exception_key,
  x.exception_type,
  x.severity,
  x.message,
  x.impact_days,
  x.root_cause_path,
  x.detail,
  x.detected_at,

  so.priority AS order_priority,
  c.service_class_code,
  sc.priority_rank AS service_class_rank,

  COALESCE(t.order_value,0)::double precision AS order_value,
  COALESCE(t.open_order_value,0)::double precision AS open_order_value,

  CASE
    WHEN x.sales_order_line_id IS NULL
      THEN COALESCE(t.open_order_value,0)
    ELSE COALESCE(
      GREATEST(
        el.quantity - el.shipped_qty - el.cancelled_qty,
        0
      ) * el.unit_price,
      0
    )
  END::double precision AS revenue_at_risk

FROM v_current_planning_exceptions x

JOIN sales_orders so
  ON so.id=x.sales_order_id

JOIN customers c
  ON c.id=so.customer_id

JOIN customer_service_classes sc
  ON sc.code=c.service_class_code

LEFT JOIN sales_order_lines el
  ON el.id=x.sales_order_line_id

JOIN LATERAL (
  SELECT
    COALESCE(SUM(l.quantity * l.unit_price),0) AS order_value,
    COALESCE(
      SUM(
        GREATEST(
          l.quantity - l.shipped_qty - l.cancelled_qty,
          0
        ) * l.unit_price
      ),
      0
    ) AS open_order_value
  FROM sales_order_lines l
  WHERE l.sales_order_id=x.sales_order_id
) t ON true

WHERE x.current_status <> 'RESOLVED'
  AND so.status IN ('CONFIRMED','PARTIALLY_SHIPPED')

ORDER BY
  CASE x.severity
    WHEN 'CRITICAL' THEN 1
    WHEN 'WARNING' THEN 2
    ELSE 3
  END,
  x.impact_days DESC,
  x.detected_at,
  x.id
`)
	return rows, err
}

func insertOrGetControlTowerCase(
	ctx context.Context,
	tx *sqlx.Tx,
	x controlTowerSourceRow,
	caseKey string,
) (uuid.UUID, error) {
	newID := uuid.New()

	_, err := tx.ExecContext(ctx, `
INSERT INTO control_tower_cases(
  id,
  case_key,
  sales_order_id,
  sales_order_line_id,
  exception_type,
  first_exception_id,
  first_detected_at
)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(case_key) DO NOTHING
`,
		newID,
		caseKey,
		x.SalesOrderID,
		x.SalesOrderLineID,
		x.ExceptionType,
		x.ExceptionID,
		x.DetectedAt,
	)
	if err != nil {
		return uuid.Nil, err
	}

	var id uuid.UUID
	if err := tx.GetContext(
		ctx,
		&id,
		`SELECT id FROM control_tower_cases WHERE case_key=$1`,
		caseKey,
	); err != nil {
		return uuid.Nil, err
	}

	return id, nil
}

func insertControlTowerSnapshot(
	ctx context.Context,
	tx *sqlx.Tx,
	row domain.ControlTowerCaseSnapshot,
) (bool, error) {
	res, err := tx.ExecContext(ctx, `
INSERT INTO control_tower_case_snapshots(
  id,
  case_id,
  planning_exception_id,
  pegging_run_id,
  as_of,
  severity,
  impact_days,
  order_value,
  open_order_value,
  order_priority,
  service_class_code,
  revenue_at_risk,
  severity_score,
  lateness_score,
  revenue_score,
  customer_score,
  material_score,
  capacity_score,
  supplier_score,
  execution_score,
  aging_score,
  priority_score,
  priority_band,
  root_cause_type,
  root_cause_ref,
  result_hash
)
VALUES(
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
  $13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26
)
ON CONFLICT(case_id,result_hash) DO NOTHING
`,
		row.ID,
		row.CaseID,
		row.PlanningExceptionID,
		row.PeggingRunID,
		row.AsOf,
		row.Severity,
		row.ImpactDays,
		row.OrderValue,
		row.OpenOrderValue,
		row.OrderPriority,
		row.ServiceClassCode,
		row.RevenueAtRisk,
		row.SeverityScore,
		row.LatenessScore,
		row.RevenueScore,
		row.CustomerScore,
		row.MaterialScore,
		row.CapacityScore,
		row.SupplierScore,
		row.ExecutionScore,
		row.AgingScore,
		row.PriorityScore,
		row.PriorityBand,
		row.RootCauseType,
		row.RootCauseRef,
		row.ResultHash,
	)
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	return n == 1, nil
}

func insertControlTowerRecommendations(
	ctx context.Context,
	tx *sqlx.Tx,
	snapshotID uuid.UUID,
	drafts []ControlTowerRecommendationDraft,
) (int, error) {
	count := 0

	for _, r := range drafts {
		effect, err := json.Marshal(r.EstimatedEffect)
		if err != nil {
			return count, err
		}

		_, err = tx.ExecContext(ctx, `
INSERT INTO control_tower_recommendations(
  id,
  snapshot_id,
  rank_no,
  action_type,
  target_type,
  target_ref,
  title,
  reason,
  estimated_effect,
  requires_approval
)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10)
`,
			uuid.New(),
			snapshotID,
			r.RankNo,
			r.ActionType,
			r.TargetType,
			r.TargetRef,
			r.Title,
			r.Reason,
			string(effect),
			r.RequiresApproval,
		)
		if err != nil {
			return count, err
		}

		count++
	}

	return count, nil
}

// Refresh evaluates all current unresolved Planning Exceptions in one
// repeatable-read snapshot.
//
// Existing case identities are reused. A new immutable snapshot is written only
// when the canonical evaluated state changes.
func (s *ControlTowerService) Refresh(
	ctx context.Context,
	asOf time.Time,
) (*ControlTowerRefreshResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	if asOf.IsZero() {
		asOf = time.Now()
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
	})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := s.currentExceptionRows(ctx, tx)
	if err != nil {
		return nil, err
	}

	out := &ControlTowerRefreshResult{
		AsOf:                asOf,
		ExceptionsEvaluated: len(rows),
	}

	touched := map[uuid.UUID]struct{}{}

	for _, x := range rows {
		caseKey := controlTowerCaseKey(x)

		caseID, err := insertOrGetControlTowerCase(ctx, tx, x, caseKey)
		if err != nil {
			return nil, err
		}
		touched[caseID] = struct{}{}

		ageHours := asOf.Sub(x.DetectedAt).Hours()
		if ageHours < 0 {
			ageHours = 0
		}

		score := ScoreControlTowerPriority(ControlTowerScoreInput{
			Severity:         x.Severity,
			ImpactDays:       x.ImpactDays,
			RevenueAtRisk:    x.RevenueAtRisk,
			OrderPriority:    x.OrderPriority,
			ServiceClassRank: x.ServiceClassRank,
			ExceptionType:    x.ExceptionType,
			AgeHours:         ageHours,
		})

		rootRef := controlTowerRootRef(x.RootCausePath, x.Detail)

		hash, err := canonicalControlTowerSnapshotHash(controlTowerHashInput{
			CaseKey:             caseKey,
			PlanningExceptionID: x.ExceptionID,
			PeggingRunID:        x.RunID,
			Severity:            x.Severity,
			ImpactDays:          x.ImpactDays,
			OrderValue:          x.OrderValue,
			OpenOrderValue:      x.OpenOrderValue,
			OrderPriority:       x.OrderPriority,
			ServiceClassCode:    x.ServiceClassCode,
			RevenueAtRisk:       x.RevenueAtRisk,
			RootCauseType:       x.ExceptionType,
			RootCauseRef:        rootRef,
			Score:               score,
		})
		if err != nil {
			return nil, err
		}

		snapshot := domain.ControlTowerCaseSnapshot{
			ID:                  uuid.New(),
			CaseID:              caseID,
			PlanningExceptionID: x.ExceptionID,
			PeggingRunID:        x.RunID,
			AsOf:                asOf,

			Severity:         x.Severity,
			ImpactDays:       x.ImpactDays,
			OrderValue:       x.OrderValue,
			OpenOrderValue:   x.OpenOrderValue,
			OrderPriority:    x.OrderPriority,
			ServiceClassCode: x.ServiceClassCode,
			RevenueAtRisk:    x.RevenueAtRisk,

			SeverityScore:  score.SeverityScore,
			LatenessScore:  score.LatenessScore,
			RevenueScore:   score.RevenueScore,
			CustomerScore:  score.CustomerScore,
			MaterialScore:  score.MaterialScore,
			CapacityScore:  score.CapacityScore,
			SupplierScore:  score.SupplierScore,
			ExecutionScore: score.ExecutionScore,
			AgingScore:     score.AgingScore,

			PriorityScore: score.PriorityScore,
			PriorityBand:  score.PriorityBand,

			RootCauseType: x.ExceptionType,
			RootCauseRef:  rootRef,
			ResultHash:    hash,
		}

		created, err := insertControlTowerSnapshot(
			ctx,
			tx,
			snapshot,
		)
		if err != nil {
			return nil, err
		}

		if !created {
			continue
		}

		out.SnapshotsCreated++

		recs := RecommendControlTowerActions(
			ControlTowerRecommendationInput{
				ExceptionType: x.ExceptionType,
				Message:       x.Message,
				RootCauseRef:  rootRef,
			},
		)

		n, err := insertControlTowerRecommendations(
			ctx,
			tx,
			snapshot.ID,
			recs,
		)
		if err != nil {
			return nil, err
		}

		out.RecommendationsCreated += n
	}

	out.CasesTouched = len(touched)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return out, nil
}

package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
)

type ControlTowerDashboardFilter struct {
	Status       string
	PriorityBand string
}

func normalizeControlTowerDashboardFilter(
	in ControlTowerDashboardFilter,
) (ControlTowerDashboardFilter, error) {
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	in.PriorityBand = strings.ToUpper(strings.TrimSpace(in.PriorityBand))

	if in.Status != "" {
		switch in.Status {
		case "OPEN", "ACKNOWLEDGED", "ASSIGNED",
			"IN_PROGRESS", "RESOLVED", "CLOSED":
		default:
			return in, domain.NewBadRequest(
				"invalid Control Tower case status", nil,
			)
		}
	}

	if in.PriorityBand != "" {
		switch in.PriorityBand {
		case "P1", "P2", "P3", "P4":
		default:
			return in, domain.NewBadRequest(
				"invalid Control Tower priority band", nil,
			)
		}
	}

	return in, nil
}

// Dashboard returns current intervention cases backed by the latest Control
// Tower snapshots. Historical case/snapshot evidence remains queryable in the
// audit tables but does not need to pollute the active intervention board.
func (s *ControlTowerService) Dashboard(
	ctx context.Context,
	filter ControlTowerDashboardFilter,
) (*domain.ControlTowerDashboard, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	filter, err := normalizeControlTowerDashboardFilter(filter)
	if err != nil {
		return nil, err
	}

	q := `
SELECT c.*
FROM v_current_control_tower_cases c
WHERE c.snapshot_id IS NOT NULL
  AND EXISTS (
    SELECT 1
      FROM v_current_planning_exceptions e
     WHERE e.id=c.planning_exception_id
       AND e.current_status <> 'RESOLVED'
  )
`
	args := []any{}

	if filter.Status != "" {
		args = append(args, filter.Status)
		q += fmt.Sprintf(
			" AND c.current_status=$%d",
			len(args),
		)
	}

	if filter.PriorityBand != "" {
		args = append(args, filter.PriorityBand)
		q += fmt.Sprintf(
			" AND c.priority_band=$%d",
			len(args),
		)
	}

	q += `
ORDER BY
  CASE c.priority_band
    WHEN 'P1' THEN 1
    WHEN 'P2' THEN 2
    WHEN 'P3' THEN 3
    ELSE 4
  END,
  c.priority_score DESC,
  c.revenue_at_risk DESC,
  c.first_detected_at,
  c.case_id
`

	var rows []domain.ControlTowerCurrentCase
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}

	out := &domain.ControlTowerDashboard{
		AsOf:  time.Now(),
		Cases: rows,
	}

	for _, x := range rows {
		out.Summary.TotalCases++

		status := strings.ToUpper(x.CurrentStatus)
		actionable := status != "RESOLVED" && status != "CLOSED"

		if actionable {
			out.Summary.OpenCases++

			if x.OwnerUserID == nil {
				out.Summary.UnassignedCases++
			}

			if x.PriorityBand != nil {
				switch *x.PriorityBand {
				case "P1":
					out.Summary.P1Cases++
				case "P2":
					out.Summary.P2Cases++
				}
			}

			if x.RevenueAtRisk != nil {
				out.Summary.RevenueAtRisk += *x.RevenueAtRisk
			}
		}
	}

	return out, nil
}

func (s *ControlTowerService) GetCase(
	ctx context.Context,
	id uuid.UUID,
) (*domain.ControlTowerCurrentCase, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	var row domain.ControlTowerCurrentCase

	err := s.db.GetContext(
		ctx,
		&row,
		`SELECT * FROM v_current_control_tower_cases WHERE case_id=$1`,
		id,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFound("Control Tower case")
		}
		return nil, err
	}

	return &row, nil
}

func (s *ControlTowerService) Recommendations(
	ctx context.Context,
	caseID uuid.UUID,
) ([]domain.ControlTowerRecommendation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Control Tower database is required")
	}

	var rows []domain.ControlTowerRecommendation

	err := s.db.SelectContext(ctx, &rows, `
SELECT r.*
FROM control_tower_recommendations r
JOIN (
  SELECT id
  FROM control_tower_case_snapshots
  WHERE case_id=$1
  ORDER BY as_of DESC,created_at DESC,id DESC
  LIMIT 1
) s ON s.id=r.snapshot_id
ORDER BY r.rank_no,r.id
`, caseID)

	return rows, err
}

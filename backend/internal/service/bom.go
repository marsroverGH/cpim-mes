package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// All topology-changing BOM operations take the same PostgreSQL transaction-level
// advisory lock. This is necessary because two individually valid concurrent writes
// (for example A->B and B->A) can otherwise commit into a cycle after both pre-checks
// have passed in separate transactions.
const bomTopologyLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended('cpim-mes:bom-topology', 0))`

type bomGraphEdge struct {
	ParentID uuid.UUID `db:"parent_id"`
	ChildID  uuid.UUID `db:"child_id"`
}

func lockBOMTopologyTx(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, bomTopologyLockSQL); err != nil {
		return fmt.Errorf("lock BOM topology: %w", err)
	}
	return nil
}

// findBOMCycle returns one deterministic cycle path, with the start node repeated
// at the end (A,B,C,A). nil means the directed graph is acyclic.
func findBOMCycle(edges []bomGraphEdge) []uuid.UUID {
	adj := make(map[uuid.UUID][]uuid.UUID)
	nodes := make(map[uuid.UUID]struct{})
	for _, e := range edges {
		adj[e.ParentID] = append(adj[e.ParentID], e.ChildID)
		nodes[e.ParentID] = struct{}{}
		nodes[e.ChildID] = struct{}{}
	}
	for n := range adj {
		sort.Slice(adj[n], func(i, j int) bool { return adj[n][i].String() < adj[n][j].String() })
	}
	ordered := make([]uuid.UUID, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })

	state := make(map[uuid.UUID]uint8, len(nodes)) // 0=unseen, 1=visiting, 2=done
	stack := make([]uuid.UUID, 0, len(nodes))
	stackPos := make(map[uuid.UUID]int, len(nodes))
	var cycle []uuid.UUID

	var visit func(uuid.UUID) bool
	visit = func(n uuid.UUID) bool {
		state[n] = 1
		stackPos[n] = len(stack)
		stack = append(stack, n)

		for _, child := range adj[n] {
			switch state[child] {
			case 0:
				if visit(child) {
					return true
				}
			case 1:
				pos := stackPos[child]
				cycle = append(cycle, stack[pos:]...)
				cycle = append(cycle, child)
				return true
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackPos, n)
		state[n] = 2
		return false
	}

	for _, n := range ordered {
		if state[n] == 0 && visit(n) {
			return cycle
		}
	}
	return nil
}

func validateBOMAcyclicTx(ctx context.Context, tx *sqlx.Tx) error {
	var edges []bomGraphEdge
	if err := tx.SelectContext(ctx, &edges, `
		SELECT parent_id, child_id
		  FROM bom_components
		 ORDER BY parent_id, child_id
	`); err != nil {
		return fmt.Errorf("load BOM graph for cycle validation: %w", err)
	}
	cycle := findBOMCycle(edges)
	if len(cycle) == 0 {
		return nil
	}
	parts := make([]string, len(cycle))
	for i, id := range cycle {
		parts[i] = id.String()
	}
	return domain.NewConflict("BOM cycle detected; change rejected: " + strings.Join(parts, " -> "))
}

// recomputeLLCTx intentionally runs inside the same transaction as the BOM mutation.
// Any database error (including cycle/non-convergence detection) aborts the mutation.
func recomputeLLCTx(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, `SELECT recompute_low_level_codes()`); err != nil {
		return fmt.Errorf("recompute low-level codes: %w", err)
	}
	return nil
}

func validateBOMComponentInput(c *domain.BOMComponent) error {
	if c.ParentID == uuid.Nil || c.ChildID == uuid.Nil {
		return domain.NewBadRequest("parentId and childId are required", nil)
	}
	if c.ParentID == c.ChildID {
		return domain.NewConflict("BOM self-reference is not allowed")
	}
	if c.Quantity <= 0 {
		return domain.NewBadRequest("quantity must be > 0", nil)
	}
	if c.ScrapPct < 0 {
		return domain.NewBadRequest("scrapPct must be >= 0", nil)
	}
	return nil
}

// Add atomically performs:
// topology lock -> insert -> global cycle validation -> LLC recompute -> commit.
// The inserted edge is never visible if cycle validation or LLC recompute fails.
func (s *BOMService) Add(ctx context.Context, c *domain.BOMComponent) error {
	if err := validateBOMComponentInput(c); err != nil {
		return err
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin BOM add transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := lockBOMTopologyTx(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bom_components (id, parent_id, child_id, quantity, scrap_pct)
		VALUES ($1, $2, $3, $4, $5)
	`, c.ID, c.ParentID, c.ChildID, c.Quantity, c.ScrapPct); err != nil {
		return fmt.Errorf("insert BOM component: %w", err)
	}
	if err := validateBOMAcyclicTx(ctx, tx); err != nil {
		return err
	}
	if err := recomputeLLCTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit BOM add: %w", err)
	}
	return nil
}

// Delete is also atomic with LLC recomputation. Deleting an edge cannot introduce a
// cycle, but the global validation is deliberately retained so a legacy-corrupt graph
// cannot be silently accepted and LLC errors can never be ignored.
func (s *BOMService) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin BOM delete transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := lockBOMTopologyTx(ctx, tx); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM bom_components WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete BOM component: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("check deleted BOM row: %w", err)
	} else if n == 0 {
		return domain.NewNotFound("BOM component")
	}
	if err := validateBOMAcyclicTx(ctx, tx); err != nil {
		return err
	}
	if err := recomputeLLCTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit BOM delete: %w", err)
	}
	return nil
}

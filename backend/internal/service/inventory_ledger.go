package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// InventoryLedgerService is the single write path for physical inventory.
// inventory_txns is the canonical item-level ledger and lot_movements are the
// mandatory lot allocations for every physical transaction. RESERVE/UNRESERVE
// remain item-level logical transactions because they do not move physical stock.
type InventoryLedgerService struct {
	db *sqlx.DB
}

func NewInventoryLedgerService(db *sqlx.DB) *InventoryLedgerService {
	return &InventoryLedgerService{db: db}
}

// LotAllocationInput allocates part of one physical inventory transaction to a lot.
// Quantity must have the same sign as the inventory transaction.
type LotAllocationInput struct {
	LotID        uuid.UUID
	Quantity     float64
	MovementType string // RECEIPT/ISSUE/ADJUST/CONSUMED/PRODUCED
}

// PhysicalInventoryRequest describes one atomic physical stock movement.
// For positive receipts/adjustments, LotNo may be supplied to create/extend a lot.
// For negative issues/adjustments, omit Allocations to request automatic FIFO allocation.
type PhysicalInventoryRequest struct {
	ID         uuid.UUID
	ItemID     uuid.UUID
	Quantity   float64
	TxnType    string // RECEIPT / ISSUE / ADJUST
	RefDoc     string
	OccurredAt time.Time

	Allocations []LotAllocationInput

	// Lot metadata used only when a positive transaction has no explicit allocation.
	LotID      uuid.UUID
	LotNo      string
	Supplier   string
	SourceDoc  string
	Notes      string
	ExpiryDate *time.Time

	// MovementType overrides the default movement type for auto-created allocations.
	// WO completion uses PRODUCED/CONSUMED for traceability while the parent ledger
	// transaction remains RECEIPT/ISSUE.
	MovementType string

	// IncludeNonOK allows negative ADJUST to reconcile all physical lots, including
	// HOLD/REJECTED lots. Normal ISSUE is intentionally restricted to quality OK lots.
	IncludeNonOK bool

	// RequireSourceDocMatch prevents appending stock to an existing lot that was
	// created by a different source document (used for WO-produced lots).
	RequireSourceDocMatch bool
}

type PhysicalInventoryResult struct {
	Txn         domain.InventoryTxn  `json:"transaction"`
	Allocations []domain.LotMovement `json:"lotAllocations"`
	Lots        []domain.Lot         `json:"lots,omitempty"`
}

func (s *InventoryLedgerService) Post(ctx context.Context, req PhysicalInventoryRequest) (*PhysicalInventoryResult, error) {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := s.PostTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// PostTx writes one physical inventory transaction and all of its lot allocations
// inside the caller transaction. PostgreSQL deferred constraint triggers provide a
// second line of defense and reject COMMIT unless allocation sum == transaction qty.
func (s *InventoryLedgerService) PostTx(ctx context.Context, tx *sqlx.Tx, req PhysicalInventoryRequest) (*PhysicalInventoryResult, error) {
	if tx == nil {
		return nil, fmt.Errorf("inventory ledger requires a transaction")
	}
	if req.ItemID == uuid.Nil {
		return nil, domain.NewBadRequest("itemId is required", nil)
	}
	if abs(req.Quantity) < 1e-9 {
		return nil, domain.NewBadRequest("inventory quantity must be non-zero", nil)
	}
	req.TxnType = strings.ToUpper(strings.TrimSpace(req.TxnType))
	switch req.TxnType {
	case "RECEIPT":
		if req.Quantity <= 0 {
			return nil, domain.NewBadRequest("RECEIPT quantity must be positive", nil)
		}
	case "ISSUE":
		if req.Quantity >= 0 {
			return nil, domain.NewBadRequest("ISSUE quantity must be negative", nil)
		}
	case "ADJUST":
		// positive or negative is valid
	default:
		return nil, domain.NewBadRequest("physical inventory txnType must be RECEIPT, ISSUE, or ADJUST", nil)
	}

	// Serialize physical movements for this item. This shares the same items-row lock
	// convention used by WO release, so inventory movement and reservation decisions
	// cannot observe a partially committed physical balance.
	var itemExists bool
	if err := tx.GetContext(ctx, &itemExists, `SELECT true FROM items WHERE id=$1 FOR UPDATE`, req.ItemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("item")
		}
		return nil, err
	}

	if req.ID == uuid.Nil {
		req.ID = uuid.New()
	}
	if req.OccurredAt.IsZero() {
		req.OccurredAt = time.Now()
	}
	if req.SourceDoc == "" {
		req.SourceDoc = req.RefDoc
	}

	// Determine/lock allocations before writing the header. For explicit allocations,
	// all lots are locked in UUID order to keep lock ordering deterministic.
	allocs := append([]LotAllocationInput(nil), req.Allocations...)
	createdLots := make([]domain.Lot, 0, 1)

	if len(allocs) == 0 {
		if req.Quantity > 0 {
			lot, err := s.ensureReceiptLotTx(ctx, tx, req)
			if err != nil {
				return nil, err
			}
			createdLots = append(createdLots, *lot)
			mvType := defaultMovementType(req.TxnType, req.MovementType)
			allocs = []LotAllocationInput{{LotID: lot.ID, Quantity: req.Quantity, MovementType: mvType}}
		} else {
			fifo, err := s.allocateNegativeFIFO(ctx, tx, req)
			if err != nil {
				return nil, err
			}
			allocs = fifo
		}
	}

	if err := validateAllocationSum(req, allocs); err != nil {
		return nil, err
	}

	// Lock explicit lot rows and validate item ownership/balance. FIFO allocations are
	// already locked, but re-locking the same rows in a transaction is harmless.
	if err := s.validateAndLockAllocationsTx(ctx, tx, req, allocs); err != nil {
		return nil, err
	}

	// Explicit positive allocations represent additional receipts into existing lots.
	// Auto-created/selected positive lots were already updated by ensureReceiptLotTx.
	if req.Quantity > 0 && len(req.Allocations) > 0 {
		for _, a := range allocs {
			if _, err := tx.ExecContext(ctx, `UPDATE lots SET quantity=quantity+$1 WHERE id=$2`, a.Quantity, a.LotID); err != nil {
				return nil, fmt.Errorf("update lot received quantity: %w", err)
			}
		}
	}

	txn := domain.InventoryTxn{
		ID:         req.ID,
		ItemID:     req.ItemID,
		Quantity:   req.Quantity,
		TxnType:    req.TxnType,
		RefDoc:     req.RefDoc,
		OccurredAt: req.OccurredAt,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO inventory_txns (id, item_id, quantity, txn_type, ref_doc, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, txn.ID, txn.ItemID, txn.Quantity, txn.TxnType, txn.RefDoc, txn.OccurredAt); err != nil {
		return nil, fmt.Errorf("insert inventory transaction: %w", err)
	}

	movements := make([]domain.LotMovement, 0, len(allocs))
	for _, a := range allocs {
		mvType := strings.ToUpper(strings.TrimSpace(a.MovementType))
		if mvType == "" {
			mvType = defaultMovementType(req.TxnType, req.MovementType)
		}
		mv := domain.LotMovement{
			ID:           uuid.New(),
			LotID:        a.LotID,
			TxnID:        &txn.ID,
			Quantity:     a.Quantity,
			MovementType: mvType,
			RefDoc:       req.RefDoc,
			OccurredAt:   req.OccurredAt,
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lot_movements (id, lot_id, txn_id, quantity, movement_type, ref_doc, occurred_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, mv.ID, mv.LotID, txn.ID, mv.Quantity, mv.MovementType, mv.RefDoc, mv.OccurredAt); err != nil {
			return nil, fmt.Errorf("insert lot allocation: %w", err)
		}
		movements = append(movements, mv)
	}

	return &PhysicalInventoryResult{Txn: txn, Allocations: movements, Lots: createdLots}, nil
}

func (s *InventoryLedgerService) ensureReceiptLotTx(ctx context.Context, tx *sqlx.Tx, req PhysicalInventoryRequest) (*domain.Lot, error) {
	if req.LotID != uuid.Nil {
		var lot domain.Lot
		if err := tx.GetContext(ctx, &lot, `SELECT * FROM lots WHERE id=$1 FOR UPDATE`, req.LotID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.NewNotFound("lot")
			}
			return nil, err
		}
		if lot.ItemID != req.ItemID {
			return nil, domain.NewConflict("lot item does not match inventory transaction item")
		}
		if req.RequireSourceDocMatch && req.SourceDoc != "" && lot.SourceDoc != "" && lot.SourceDoc != req.SourceDoc {
			return nil, domain.NewConflict(fmt.Sprintf("lot %s belongs to source %s, not %s", lot.LotNo, lot.SourceDoc, req.SourceDoc))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE lots SET quantity=quantity+$1 WHERE id=$2`, req.Quantity, lot.ID); err != nil {
			return nil, err
		}
		lot.Quantity += req.Quantity
		return &lot, nil
	}

	lotNo := strings.TrimSpace(req.LotNo)
	if lotNo == "" {
		lotNo = generatedLotNo(req.RefDoc, req.ID, req.OccurredAt)
	}

	var existing domain.Lot
	err := tx.GetContext(ctx, &existing, `
		SELECT * FROM lots WHERE item_id=$1 AND lot_no=$2 FOR UPDATE
	`, req.ItemID, lotNo)
	if err == nil {
		if req.RequireSourceDocMatch && req.SourceDoc != "" && existing.SourceDoc != "" && existing.SourceDoc != req.SourceDoc {
			return nil, domain.NewConflict(fmt.Sprintf("lot %s belongs to source %s, not %s", existing.LotNo, existing.SourceDoc, req.SourceDoc))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE lots SET quantity=quantity+$1 WHERE id=$2`, req.Quantity, existing.ID); err != nil {
			return nil, err
		}
		existing.Quantity += req.Quantity
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	lot := &domain.Lot{
		ID:            uuid.New(),
		ItemID:        req.ItemID,
		LotNo:         lotNo,
		Quantity:      req.Quantity,
		ReceivedAt:    req.OccurredAt,
		ExpiryDate:    req.ExpiryDate,
		Supplier:      req.Supplier,
		SourceDoc:     req.SourceDoc,
		Notes:         req.Notes,
		QualityStatus: "OK",
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lots (id, item_id, lot_no, quantity, received_at, expiry_date,
		                  supplier, source_doc, notes, quality_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'OK')
	`, lot.ID, lot.ItemID, lot.LotNo, lot.Quantity, lot.ReceivedAt, lot.ExpiryDate,
		lot.Supplier, lot.SourceDoc, lot.Notes); err != nil {
		return nil, fmt.Errorf("create lot: %w", err)
	}
	return lot, nil
}

func (s *InventoryLedgerService) allocateNegativeFIFO(ctx context.Context, tx *sqlx.Tx, req PhysicalInventoryRequest) ([]LotAllocationInput, error) {
	qualityClause := "AND quality_status='OK'"
	if req.TxnType == "ADJUST" && req.IncludeNonOK {
		qualityClause = ""
	}
	var lotIDs []uuid.UUID
	lockSQL := fmt.Sprintf(`
		SELECT id FROM lots
		 WHERE item_id=$1 %s
		 ORDER BY received_at ASC, id ASC
		 FOR UPDATE
	`, qualityClause)
	if err := tx.SelectContext(ctx, &lotIDs, lockSQL, req.ItemID); err != nil {
		return nil, err
	}

	type lotBalance struct {
		ID      uuid.UUID `db:"id"`
		Balance float64   `db:"balance"`
	}
	var balances []lotBalance
	balanceSQL := fmt.Sprintf(`
		SELECT l.id, COALESCE(SUM(lm.quantity),0) AS balance
		  FROM lots l
		  LEFT JOIN lot_movements lm ON lm.lot_id=l.id
		 WHERE l.item_id=$1 %s
		 GROUP BY l.id, l.received_at
		HAVING COALESCE(SUM(lm.quantity),0) > 0
		 ORDER BY l.received_at ASC, l.id ASC
	`, qualityClause)
	if err := tx.SelectContext(ctx, &balances, balanceSQL, req.ItemID); err != nil {
		return nil, err
	}
	_ = lotIDs

	remaining := -req.Quantity
	allocs := make([]LotAllocationInput, 0, len(balances))
	mvType := defaultMovementType(req.TxnType, req.MovementType)
	for _, b := range balances {
		if remaining <= 1e-9 {
			break
		}
		use := b.Balance
		if use > remaining {
			use = remaining
		}
		if use <= 0 {
			continue
		}
		allocs = append(allocs, LotAllocationInput{LotID: b.ID, Quantity: -use, MovementType: mvType})
		remaining -= use
	}
	if remaining > 1e-9 {
		return nil, domain.NewConflict(fmt.Sprintf("insufficient lot stock: short %.2f", remaining))
	}
	return allocs, nil
}

func (s *InventoryLedgerService) validateAndLockAllocationsTx(ctx context.Context, tx *sqlx.Tx, req PhysicalInventoryRequest, allocs []LotAllocationInput) error {
	ids := make([]uuid.UUID, 0, len(allocs))
	seen := map[uuid.UUID]bool{}
	for _, a := range allocs {
		if a.LotID == uuid.Nil {
			return domain.NewBadRequest("lot allocation requires lotId", nil)
		}
		if !seen[a.LotID] {
			seen[a.LotID] = true
			ids = append(ids, a.LotID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	for _, id := range ids {
		var lot struct {
			ItemID        uuid.UUID `db:"item_id"`
			QualityStatus string    `db:"quality_status"`
		}
		if err := tx.GetContext(ctx, &lot, `SELECT item_id, quality_status FROM lots WHERE id=$1 FOR UPDATE`, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.NewNotFound("lot")
			}
			return err
		}
		if lot.ItemID != req.ItemID {
			return domain.NewConflict("lot allocation item does not match inventory transaction item")
		}
		if req.TxnType == "ISSUE" && lot.QualityStatus != "OK" {
			return domain.NewConflict("ISSUE cannot consume HOLD/REJECTED lot")
		}
	}

	if req.Quantity < 0 {
		needByLot := map[uuid.UUID]float64{}
		for _, a := range allocs {
			needByLot[a.LotID] += -a.Quantity
		}
		for id, needed := range needByLot {
			var balance float64
			if err := tx.GetContext(ctx, &balance, `
				SELECT COALESCE(SUM(quantity),0) FROM lot_movements WHERE lot_id=$1
			`, id); err != nil {
				return err
			}
			if balance+1e-9 < needed {
				return domain.NewConflict(fmt.Sprintf("lot %s has %.2f available but %.2f requested", id, balance, needed))
			}
		}
	}
	return nil
}

func validateAllocationSum(req PhysicalInventoryRequest, allocs []LotAllocationInput) error {
	if len(allocs) == 0 {
		return domain.NewBadRequest("physical inventory transaction requires at least one lot allocation", nil)
	}
	sum := 0.0
	for _, a := range allocs {
		if abs(a.Quantity) < 1e-9 {
			return domain.NewBadRequest("lot allocation quantity must be non-zero", nil)
		}
		if (req.Quantity > 0 && a.Quantity <= 0) || (req.Quantity < 0 && a.Quantity >= 0) {
			return domain.NewBadRequest("lot allocation quantity sign must match inventory transaction", nil)
		}
		sum += a.Quantity
	}
	if abs(sum-req.Quantity) > 1e-6 {
		return domain.NewConflict(fmt.Sprintf("lot allocation sum %.6f does not equal inventory transaction quantity %.6f", sum, req.Quantity))
	}
	return nil
}

func defaultMovementType(txnType, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.ToUpper(strings.TrimSpace(override))
	}
	switch txnType {
	case "RECEIPT":
		return "RECEIPT"
	case "ISSUE":
		return "ISSUE"
	default:
		return "ADJUST"
	}
}

var nonLotNo = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func generatedLotNo(ref string, id uuid.UUID, at time.Time) string {
	base := strings.TrimSpace(ref)
	if base == "" {
		base = "MANUAL"
	}
	base = nonLotNo.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if len(base) > 24 {
		base = base[:24]
	}
	return fmt.Sprintf("%s-%s-%s", base, at.Format("20060102"), id.String()[:8])
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

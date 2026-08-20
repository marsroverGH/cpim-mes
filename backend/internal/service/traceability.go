package service

import (
	"context"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// ====================================================================
// Lot Service
// ====================================================================

type LotService struct {
	r      *repository.LotRepo
	ledger *InventoryLedgerService
}

func (s *LotService) List(ctx context.Context) ([]domain.LotWithBalance, error) {
	return s.r.List(ctx)
}
func (s *LotService) ByItem(ctx context.Context, itemID uuid.UUID) ([]domain.LotWithBalance, error) {
	return s.r.ByItem(ctx, itemID)
}
func (s *LotService) Create(ctx context.Context, l *domain.Lot) error {
	if l.Quantity <= 0 {
		return domain.NewBadRequest("lot receipt quantity must be > 0", nil)
	}
	res, err := s.ledger.Post(ctx, PhysicalInventoryRequest{
		ItemID: l.ItemID, Quantity: l.Quantity, TxnType: "RECEIPT",
		RefDoc: l.SourceDoc, LotNo: l.LotNo, Supplier: l.Supplier,
		SourceDoc: l.SourceDoc, Notes: l.Notes, ExpiryDate: l.ExpiryDate,
		OccurredAt: l.ReceivedAt, MovementType: "RECEIPT",
	})
	if err != nil {
		return err
	}
	if len(res.Lots) > 0 {
		*l = res.Lots[0]
	}
	return nil
}
func (s *LotService) Movements(ctx context.Context, lotID uuid.UUID) ([]domain.LotMovement, error) {
	return s.r.Movements(ctx, lotID)
}
func (s *LotService) AddMovement(ctx context.Context, m *domain.LotMovement) error {
	lot, err := s.r.Get(ctx, m.LotID)
	if err != nil {
		return err
	}
	txnType := ""
	switch m.MovementType {
	case "RECEIPT", "PRODUCED":
		txnType = "RECEIPT"
	case "ISSUE", "CONSUMED":
		txnType = "ISSUE"
	case "ADJUST":
		txnType = "ADJUST"
	default:
		return domain.NewBadRequest("unsupported lot movement type", nil)
	}
	res, err := s.ledger.Post(ctx, PhysicalInventoryRequest{
		ItemID: lot.ItemID, Quantity: m.Quantity, TxnType: txnType,
		RefDoc: m.RefDoc, OccurredAt: m.OccurredAt,
		Allocations: []LotAllocationInput{{
			LotID: m.LotID, Quantity: m.Quantity, MovementType: m.MovementType,
		}},
	})
	if err != nil {
		return err
	}
	if len(res.Allocations) > 0 {
		*m = res.Allocations[0]
	}
	return nil
}
func (s *LotService) WhereUsed(ctx context.Context, lotID uuid.UUID) ([]domain.LotMovement, error) {
	return s.r.WhereUsed(ctx, lotID)
}

// ====================================================================
// Audit Service
// ====================================================================

type AuditService struct{ r *repository.AuditRepo }

func (s *AuditService) List(ctx context.Context, f repository.AuditFilter) ([]domain.AuditLogEntry, error) {
	return s.r.List(ctx, f)
}
func (s *AuditService) Record(ctx context.Context, e *domain.AuditLogEntry) error {
	return s.r.Insert(ctx, e)
}

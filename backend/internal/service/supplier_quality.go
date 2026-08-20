package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// SupplierQualityService coordinates supplier qualification, NCR lifecycle,
// dispositions and physical inventory effects. All mutations are transactional.
type SupplierQualityService struct {
	db     *sqlx.DB
	ledger *InventoryLedgerService
}

type SupplierQualityActor struct {
	UserID   uuid.UUID
	Username string
	Role     domain.Role
}

func (a SupplierQualityActor) validate() error {
	if a.UserID == uuid.Nil || strings.TrimSpace(a.Username) == "" {
		return domain.NewUnauthorized("authenticated supplier-quality actor is required")
	}
	return nil
}

type SupplierQualityProfileInput struct {
	SupplierName       string
	Status             string
	InspectionRequired bool
	TargetPPM          float64
	Notes              string
}

type SupplierNCRCreateInput struct {
	LotID        uuid.UUID
	InspectionID *uuid.UUID
	AffectedQty  float64
	Severity     string
	Description  string
}

type SupplierNCRDispositionInput struct {
	Disposition string
	Quantity    float64
	Notes       string
}

func (s *SupplierQualityService) Profiles(ctx context.Context) ([]domain.SupplierQualityProfile, error) {
	var rows []domain.SupplierQualityProfile
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM supplier_quality_profiles ORDER BY supplier_name`)
	return rows, err
}

func (s *SupplierQualityService) UpsertProfile(ctx context.Context, in SupplierQualityProfileInput, actor SupplierQualityActor) (*domain.SupplierQualityProfile, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if actor.Role != domain.RolePlanner && actor.Role != domain.RoleAdmin {
		return nil, domain.NewForbidden("supplier profile management requires planner/admin")
	}
	in.SupplierName = strings.TrimSpace(in.SupplierName)
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	if in.SupplierName == "" {
		return nil, domain.NewBadRequest("supplierName is required", nil)
	}
	if in.Status == "" {
		in.Status = "APPROVED"
	}
	if in.Status != "APPROVED" && in.Status != "CONDITIONAL" && in.Status != "BLOCKED" {
		return nil, domain.NewBadRequest("status must be APPROVED / CONDITIONAL / BLOCKED", nil)
	}
	if in.TargetPPM < 0 {
		return nil, domain.NewBadRequest("targetPpm must be >= 0", nil)
	}

	var row domain.SupplierQualityProfile
	err := s.db.GetContext(ctx, &row, `
		INSERT INTO supplier_quality_profiles(
		  supplier_name,status,inspection_required,target_ppm,notes,updated_by_user_id,updated_by,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (supplier_name) DO UPDATE SET
		  status=EXCLUDED.status,
		  inspection_required=EXCLUDED.inspection_required,
		  target_ppm=EXCLUDED.target_ppm,
		  notes=EXCLUDED.notes,
		  updated_by_user_id=EXCLUDED.updated_by_user_id,
		  updated_by=EXCLUDED.updated_by,
		  updated_at=now()
		RETURNING *
	`, in.SupplierName, in.Status, in.InspectionRequired, in.TargetPPM, strings.TrimSpace(in.Notes), actor.UserID, actor.Username)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *SupplierQualityService) Scorecards(ctx context.Context) ([]domain.SupplierQualityScorecard, error) {
	var rows []domain.SupplierQualityScorecard
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM v_supplier_quality_scorecard ORDER BY defect_ppm DESC, supplier`)
	return rows, err
}

func (s *SupplierQualityService) ListNCRs(ctx context.Context, status, supplier string) ([]domain.SupplierNCR, error) {
	status = strings.ToUpper(strings.TrimSpace(status))
	supplier = strings.TrimSpace(supplier)
	var rows []domain.SupplierNCR
	err := s.db.SelectContext(ctx, &rows, `
		SELECT n.*,
		       i.code item_code, i.name item_name, l.lot_no,
		       COALESCE(po.po_no,'') po_no,
		       COALESCE(d.disposition,'') disposition,
		       COALESCE(d.quantity,0) disposition_qty
		  FROM supplier_ncrs n
		  JOIN items i ON i.id=n.item_id
		  JOIN lots l ON l.id=n.lot_id
		  LEFT JOIN purchase_orders po ON po.id=n.purchase_order_id
		  LEFT JOIN supplier_ncr_dispositions d ON d.ncr_id=n.id
		 WHERE ($1='' OR n.status=$1)
		   AND ($2='' OR n.supplier=$2)
		 ORDER BY n.created_at DESC, n.ncr_no DESC
	`, status, supplier)
	return rows, err
}

func (s *SupplierQualityService) NCRHistory(ctx context.Context, ncrID uuid.UUID) ([]domain.SupplierNCRHistory, error) {
	var rows []domain.SupplierNCRHistory
	err := s.db.SelectContext(ctx, &rows, `SELECT * FROM supplier_ncr_history WHERE ncr_id=$1 ORDER BY occurred_at,id`, ncrID)
	return rows, err
}

func (s *SupplierQualityService) CreateNCR(ctx context.Context, in SupplierNCRCreateInput, actor SupplierQualityActor) (*domain.SupplierNCR, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if actor.Role != domain.RoleOperator && actor.Role != domain.RolePlanner && actor.Role != domain.RoleAdmin {
		return nil, domain.NewForbidden("NCR creation requires operator/planner/admin")
	}
	if in.LotID == uuid.Nil {
		return nil, domain.NewBadRequest("lotId is required", nil)
	}
	in.Severity = strings.ToUpper(strings.TrimSpace(in.Severity))
	if in.Severity == "" {
		in.Severity = "MAJOR"
	}
	if in.Severity != "MINOR" && in.Severity != "MAJOR" && in.Severity != "CRITICAL" {
		return nil, domain.NewBadRequest("severity must be MINOR / MAJOR / CRITICAL", nil)
	}
	if in.AffectedQty < 0 {
		return nil, domain.NewBadRequest("affectedQty must be >= 0", nil)
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var lot struct {
		ItemID   uuid.UUID `db:"item_id"`
		Supplier string    `db:"supplier"`
	}
	if err := tx.GetContext(ctx, &lot, `SELECT item_id,supplier FROM lots WHERE id=$1 FOR UPDATE`, in.LotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("lot")
		}
		return nil, err
	}
	if strings.TrimSpace(lot.Supplier) == "" {
		return nil, domain.NewConflict("Supplier NCR requires a supplier-derived lot")
	}

	var pr struct {
		ID   uuid.UUID `db:"id"`
		POID uuid.UUID `db:"purchase_order_id"`
	}
	var prID *uuid.UUID
	var poID *uuid.UUID
	if err := tx.GetContext(ctx, &pr, `SELECT id,purchase_order_id FROM purchase_receipts WHERE lot_id=$1 ORDER BY received_at DESC LIMIT 1`, in.LotID); err == nil {
		prID = &pr.ID
		poID = &pr.POID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if in.InspectionID != nil {
		var ok bool
		if err := tx.GetContext(ctx, &ok, `SELECT true FROM quality_inspections WHERE id=$1 AND lot_id=$2`, *in.InspectionID, in.LotID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.NewConflict("inspection does not belong to lot")
			}
			return nil, err
		}
		var existing domain.SupplierNCR
		if err := tx.GetContext(ctx, &existing, `SELECT * FROM supplier_ncrs WHERE inspection_id=$1`, *in.InspectionID); err == nil {
			return &existing, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	id := uuid.New()
	ncrNo := "NCR-" + strings.ToUpper(strings.ReplaceAll(id.String(), "-", "")[:12])
	var out domain.SupplierNCR
	if err := tx.GetContext(ctx, &out, `
		INSERT INTO supplier_ncrs(
		  id,ncr_no,supplier,purchase_order_id,purchase_receipt_id,item_id,lot_id,inspection_id,
		  affected_qty,severity,description,status,created_by_user_id,created_by,created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'OPEN',$12,$13,$14)
		RETURNING *
	`, id, ncrNo, lot.Supplier, poID, prID, lot.ItemID, in.LotID, in.InspectionID,
		in.AffectedQty, in.Severity, strings.TrimSpace(in.Description), actor.UserID, actor.Username, time.Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *SupplierQualityService) Disposition(ctx context.Context, ncrID uuid.UUID, in SupplierNCRDispositionInput, actor SupplierQualityActor) (*domain.SupplierNCRDisposition, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	normalized, ruleErr := ValidateSupplierNCRDisposition(actor.Role, in.Disposition, in.Quantity, 0)
	if ruleErr != nil {
		if strings.Contains(ruleErr.Error(), "requires") {
			return nil, domain.NewForbidden(ruleErr.Error())
		}
		return nil, domain.NewBadRequest(ruleErr.Error(), nil)
	}
	in.Disposition = normalized

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var ncr domain.SupplierNCR
	if err := tx.GetContext(ctx, &ncr, `SELECT * FROM supplier_ncrs WHERE id=$1 FOR UPDATE`, ncrID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("NCR")
		}
		return nil, err
	}
	if ncr.Status != "OPEN" {
		return nil, domain.NewConflict("NCR must be OPEN for disposition")
	}
	if in.Disposition == "USE_AS_IS" {
		var otherActive int
		if err := tx.GetContext(ctx, &otherActive, `
			SELECT count(*) FROM supplier_ncrs
			 WHERE lot_id=$1 AND id<>$2 AND status IN ('OPEN','IN_REWORK')
		`, ncr.LotID, ncr.ID); err != nil {
			return nil, err
		}
		if otherActive > 0 {
			return nil, domain.NewConflict("USE_AS_IS cannot release lot while another active NCR exists")
		}
	}
	if _, err := ValidateSupplierNCRDisposition(actor.Role, in.Disposition, in.Quantity, ncr.AffectedQty); err != nil {
		return nil, domain.NewConflict(err.Error())
	}

	var inventoryTxnID *uuid.UUID
	if in.Disposition == "RETURN_TO_SUPPLIER" || in.Disposition == "SCRAP" {
		ref := fmt.Sprintf("NCR:%s:%s", ncr.NCRNo, in.Disposition)
		res, err := s.ledger.PostTx(ctx, tx, PhysicalInventoryRequest{
			ItemID: ncr.ItemID, Quantity: -in.Quantity, TxnType: "ADJUST", RefDoc: ref,
			OccurredAt: time.Now(), IncludeNonOK: true,
			Allocations:  []LotAllocationInput{{LotID: ncr.LotID, Quantity: -in.Quantity, MovementType: in.Disposition}},
			MovementType: in.Disposition, SourceDoc: ref, Notes: "Supplier NCR disposition",
		})
		if err != nil {
			return nil, err
		}
		inventoryTxnID = &res.Txn.ID
	}

	id := uuid.New()
	var d domain.SupplierNCRDisposition
	if err := tx.GetContext(ctx, &d, `
		INSERT INTO supplier_ncr_dispositions(
		 id,ncr_id,disposition,quantity,notes,inventory_txn_id,decided_by_user_id,decided_by,decided_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING *
	`, id, ncrID, in.Disposition, in.Quantity, strings.TrimSpace(in.Notes), inventoryTxnID, actor.UserID, actor.Username, time.Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *SupplierQualityService) CloseRework(ctx context.Context, ncrID uuid.UUID, actor SupplierQualityActor) (*domain.SupplierNCR, error) {
	if err := actor.validate(); err != nil {
		return nil, err
	}
	if actor.Role != domain.RolePlanner && actor.Role != domain.RoleAdmin {
		return nil, domain.NewForbidden("closing NCR requires planner/admin")
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var ncr domain.SupplierNCR
	if err := tx.GetContext(ctx, &ncr, `SELECT * FROM supplier_ncrs WHERE id=$1 FOR UPDATE`, ncrID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewNotFound("NCR")
		}
		return nil, err
	}
	if ncr.Status != "IN_REWORK" {
		return nil, domain.NewConflict("NCR is not IN_REWORK")
	}
	var passCount int
	if err := tx.GetContext(ctx, &passCount, `
		SELECT count(*) FROM quality_inspections qi
		JOIN supplier_ncr_dispositions d ON d.ncr_id=$1
		WHERE qi.lot_id=$2 AND qi.result='PASS' AND qi.inspected_at>d.decided_at
	`, ncrID, ncr.LotID); err != nil {
		return nil, err
	}
	if passCount == 0 {
		return nil, domain.NewConflict("record a PASS inspection after REWORK before closing NCR")
	}
	if err := tx.GetContext(ctx, &ncr, `
		UPDATE supplier_ncrs SET status='CLOSED',closed_by_user_id=$2 WHERE id=$1 RETURNING *
	`, ncrID, actor.UserID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ncr, nil
}

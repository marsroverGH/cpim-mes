package api

import (
	"encoding/json"
	"net/http"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/google/uuid"
)

// POST /api/work-orders/{id}/release
func (h *server) releaseWorkOrder(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.Workflow.ReleaseWorkOrder(r.Context(), id)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// GET /api/work-orders/{id}/bom-snapshot
func (h *server) getWorkOrderBOMSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.Workflow.GetWorkOrderBOMSnapshot(r.Context(), id)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// POST /api/work-orders/{id}/complete
// body: {"quantity":20,"lotNo":"...","completionId":"uuid"}
// quantity omitted => complete the remaining quantity (legacy compatibility).
func (h *server) completeWorkOrder(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		Quantity     *float64 `json:"quantity"`
		LotNo        string   `json:"lotNo"`
		CompletionID string   `json:"completionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // body remains optional for legacy full completion
	completionID := uuid.New()
	if body.CompletionID != "" {
		completionID, err = uuid.Parse(body.CompletionID)
		if err != nil {
			writeError(w, 400, err)
			return
		}
	}
	res, err := h.s.Workflow.CompleteWorkOrder(r.Context(), id, completionID, body.Quantity, body.LotNo)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

func authenticatedPurchaseReceiptActor(r *http.Request) (service.PurchaseReceiptActor, error) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if claims == nil {
		return service.PurchaseReceiptActor{}, domain.NewUnauthorized("not authenticated")
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return service.PurchaseReceiptActor{}, domain.NewUnauthorized("invalid authenticated user id")
	}
	return service.PurchaseReceiptActor{UserID: userID, Username: claims.Username}, nil
}

// POST /api/purchase-orders/{id}/receive
// body: {"receiptId":"uuid", "quantity":20, "lotNo":"SUPPLIER-LOT-1"}
func (h *server) receivePurchase(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		ReceiptID string  `json:"receiptId"`
		Quantity  float64 `json:"quantity"`
		LotNo     string  `json:"lotNo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	receiptID := uuid.New()
	if body.ReceiptID != "" {
		receiptID, err = uuid.Parse(body.ReceiptID)
		if err != nil {
			writeError(w, 400, domain.NewBadRequest("invalid receiptId", err))
			return
		}
	}
	actor, err := authenticatedPurchaseReceiptActor(r)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	res, err := h.s.Workflow.ReceivePurchase(r.Context(), id, receiptID, body.Quantity, body.LotNo, actor)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// GET /api/purchase-orders/{id}/receipts — immutable partial receipt history
func (h *server) listPurchaseReceipts(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Purchases.ListReceipts(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// GET /api/inventory/balance — 在庫サマリ (on_hand + reserved + available)
func (h *server) inventoryBalance(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Inventory.Balance(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

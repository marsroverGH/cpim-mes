#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
workflow = (root/'backend/internal/service/workflow.go').read_text()
mrp = (root/'backend/internal/service/service.go').read_text()
atp = (root/'backend/internal/service/atp_quality.go').read_text()
api = (root/'backend/internal/api/workflow.go').read_text()
router = (root/'backend/internal/api/router.go').read_text()
mig = (root/'backend/migrations/0022_partial_purchase_receipts.sql').read_text()
frontend = (root/'frontend/src/views/PurchaseOrders.vue').read_text()

checks = {
    'PO row is locked': 'SELECT * FROM purchase_orders WHERE id=$1 FOR UPDATE' in workflow,
    'receiptId advisory lock': 'pg_advisory_xact_lock' in workflow,
    'idempotency history lookup': 'WHERE pr.id=$1' in workflow and 'IdempotentHit: true' in workflow,
    'over receipt rule': 'CalcPurchaseReceiptState' in workflow,
    'unified ledger used': 's.ledger.PostTx' in workflow,
    'immutable receipt history inserted': 'INSERT INTO purchase_receipts' in workflow,
    'JWT receiver actor': 'authenticatedPurchaseReceiptActor' in api and 'claims.Username' in api,
    'history endpoint': '/purchase-orders/{id}/receipts' in router,
    'MRP uses remaining PO supply': 'PurchaseScheduledRemaining(po.Status, po.Quantity, po.ReceivedQty)' in mrp,
    'ATP uses remaining PO supply': 'PurchaseScheduledRemaining(p.Status, p.Quantity, p.ReceivedQty)' in atp,
    'DB cumulative guard': 'assert_purchase_order_receipt_state' in mig,
    'DB inverse orphan guard': 'assert_po_receipt_txn_is_bound' in mig,
    'DB immutable linked ledger': 'prevent_bound_po_receipt_mutation' in mig,
    'partial status': 'PARTIALLY_RECEIVED' in mig,
    'frontend sends receiptId': 'crypto.randomUUID()' in frontend and 'f.receiptId' in frontend,
    'frontend receipt quantity': '今回入荷数量' in frontend,
}
failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
if failed:
    sys.exit(1)

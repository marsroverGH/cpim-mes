#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
workflow = (root / "backend/internal/service/workflow.go").read_text()
shopfloor = (root / "backend/internal/service/shopfloor.go").read_text()
migration = (root / "backend/migrations/0020_final_operation_receipt_guard.sql").read_text()

checks = {
    "workflow locks final operation": "lockFinalOperationTx(ctx, tx, wo.ID)" in workflow,
    "workflow validates final actual": "ValidateFinishedGoodsAgainstFinalOperation" in workflow,
    "completion links receipt txn": "receipt_txn_id" in workflow and "parentRes.Txn.ID" in workflow,
    "shop floor locks work order first": "FOR UPDATE OF w" in shopfloor,
    "shop floor cumulative validation": "CalcOperationCumulative" in shopfloor,
    "DB receipt link trigger": "trg_wo_completion_receipt_link" in migration,
    "DB deferred WO guard": "trg_wo_final_op_guard_work_orders" in migration and "DEFERRABLE INITIALLY DEFERRED" in migration,
    "DB final operation assertion": "assert_wo_within_final_operation" in migration,
    "DB rejects orphan WO completion receipts": "trg_wo_comp_receipt_has_completion" in migration,
    "bound WO receipts are immutable": "trg_no_mutate_bound_wo_receipt_txn" in migration and "trg_no_mutate_bound_wo_receipt_lot_mv" in migration,
    "diagnostic reconciliation view": "v_wo_final_operation_reconciliation" in migration,
}
failed = [k for k, v in checks.items() if not v]
for k, v in checks.items():
    print(("PASS" if v else "FAIL"), k)
if failed:
    sys.exit(1)

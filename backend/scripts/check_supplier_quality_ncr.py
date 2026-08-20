#!/usr/bin/env python3
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[2]
def text(p): return (root/p).read_text(encoding='utf-8')
mig=text('backend/migrations/0027_supplier_quality_ncr.sql')
svc=text('backend/internal/service/supplier_quality.go')
rules=text('backend/internal/service/supplier_quality_rules.go')
routes=text('backend/internal/api/router.go')
rbac=text('backend/internal/api/rbac.go')
workflow=text('backend/internal/service/workflow.go')
mrp=text('backend/internal/service/service.go')
atp=text('backend/internal/service/atp_quality.go')
po_ui=text('frontend/src/views/PurchaseOrders.vue')
checks={
 '0027 migration exists': (root/'backend/migrations/0027_supplier_quality_ncr.sql').exists(),
 'supplier qualification schema': 'supplier_quality_profiles' in mig and "'BLOCKED'" in mig,
 'NCR + disposition immutable evidence': 'supplier_ncrs' in mig and 'supplier_ncr_dispositions' in mig and 'append-only' in mig,
 'FAIL inspection auto-opens NCR': 'auto_supplier_ncr_from_fail' in mig and "NEW.result <> 'FAIL'" in mig,
 'incoming inspection HOLD': 'supplier_receipt_quality_hold' in mig and 'inspection_required' in mig,
 'blocked supplier receipt DB guard': 'guard_blocked_supplier_receipt' in mig,
 'return/scrap uses unified ledger': 'ledger.PostTx' in svc and 'RETURN_TO_SUPPLIER' in svc and 'SCRAP' in svc,
 'ledger validators allow only NCR negative adjust types': 'CREATE OR REPLACE FUNCTION enforce_lot_movement_link()' in mig and 'assert_inventory_txn_lot_balance' in mig and "RETURN_TO_SUPPLIER','SCRAP" in mig,
 'use-as-is admin only': 'USE_AS_IS requires admin' in rules and "v_user.role <> 'admin'" in mig,
 'rework requires later PASS': 'record a PASS inspection after REWORK' in svc and 'REWORK NCR can close only after a later PASS inspection' in mig,
 'supplier scorecard view': 'v_supplier_quality_scorecard' in mig and 'defect_ppm' in mig,
 'NCR routes permission protected': 'PermNCRCreate' in routes and 'PermNCRDisposition' in routes,
 'supplier quality management permission': 'PermSupplierQualityManage' in rbac,
 'workflow rejects blocked supplier': 'supplier is BLOCKED by Supplier Quality' in workflow,
 'MRP excludes blocked supplier supply': 'SupplierQualityStatus == "BLOCKED"' in mrp,
 'ATP excludes blocked supplier supply': 'SupplierQualityStatus == "BLOCKED"' in atp,
 'purchase UI blocks receipt for blocked supplier': "supplierQualityStatus === 'BLOCKED'" in po_ui and 'Supplier Q' in po_ui,
 'migration manager fingerprints 0027': '{27,' in text('backend/internal/migration/manager.go'),
 'frontend page exists': (root/'frontend/src/views/SupplierQuality.vue').exists(),
}
failed=[]
for name,ok in checks.items():
    print(('PASS' if ok else 'FAIL')+': '+name)
    if not ok: failed.append(name)
if failed: sys.exit(1)

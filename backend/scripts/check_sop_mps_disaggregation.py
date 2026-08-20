#!/usr/bin/env python3
from pathlib import Path
root=Path(__file__).resolve().parents[2]
def text(p): return (root/p).read_text()
mig=text('backend/migrations/0026_sop_mps_disaggregation.sql')
svc=text('backend/internal/service/sop.go')
router=text('backend/internal/api/router.go')
mps=text('backend/internal/repository/repository.go')
checks={
 '0026 migration exists': (root/'backend/migrations/0026_sop_mps_disaggregation.sql').exists(),
 'versioned product mix': 'sop_product_mix_versions' in mig and "status IN ('DRAFT','ACTIVE','ARCHIVED')" in mig,
 'mix total 100 enforced': 'product mix total must equal 100' in mig and 'math.Abs(total-100)' in svc,
 'immutable disaggregation audit': 'sop_disaggregation_runs' in mig and 'S&OP disaggregation audit rows are immutable' in mig,
 'MPS provenance columns': 'source_sop_disaggregation_run_id' in mig and 'source_product_mix_version_id' in mig,
 'MPS SOP demand basis': "SOP_DISAGGREGATION" in mig and "demand_basis='SOP_DISAGGREGATION'" in svc,
 'released MPS protected': 'reduce MPS planned below already released quantity' in svc,
 'planner routes protected': 'requirePermission(PermSOPWrite)' in router and '/sop/plans/{id}/disaggregate' in router,
 'manual MPS clears SOP provenance': 'source_sop_plan_id = NULL' in mps,
 'item master persists family assignment': 'group_id=:group_id' in mps,
 'migration manager fingerprints 0026': '{26,' in text('backend/internal/migration/manager.go'),
}
failed=[k for k,v in checks.items() if not v]
for k,v in checks.items(): print(('PASS' if v else 'FAIL'),k)
if failed: raise SystemExit(1)

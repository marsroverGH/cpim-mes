#!/usr/bin/env python3
from pathlib import Path
import json, re, sys

root = Path(__file__).resolve().parents[2]
mig = (root/'backend/migrations/0034_full_pegging_exception_management.sql').read_text()
models = (root/'backend/internal/domain/models.go').read_text()
svc = (root/'backend/internal/service/pegging.go').read_text()
api = (root/'backend/internal/api/pegging.go').read_text()
router = (root/'backend/internal/api/router.go').read_text()
rbac = (root/'backend/internal/api/rbac.go').read_text()
manager = (root/'backend/internal/migration/manager.go').read_text()
front_api = (root/'frontend/src/api/index.ts').read_text()
ui = (root/'frontend/src/views/PeggingExceptions.vue').read_text()
front_router = (root/'frontend/src/router/index.ts').read_text()
app = (root/'frontend/src/App.vue').read_text()
ci = (root/'.github/workflows/ci.yml').read_text()
e2e = (root/'e2e/tests/pegging-exceptions.spec.ts').read_text()
openapi = json.loads((root/'backend/internal/api/openapi.json').read_text())

checks = {
    '0034 migration exists': 'CREATE TABLE pegging_runs' in mig,
    'immutable pegging graph schema': all(x in mig for x in ['CREATE TABLE pegging_nodes','CREATE TABLE pegging_edges','reject_pegging_evidence_mutation']),
    'planning exception evidence and append-only actions': all(x in mig for x in ['CREATE TABLE planning_exceptions','CREATE TABLE planning_exception_actions','planning_exception_actions_append_only_trg']),
    'exception action state machine is DB guarded': all(x in mig for x in ['planning_exception_current_status','ACKNOWLEDGE requires OPEN -> ACKNOWLEDGED','RESOLVE requires OPEN/ACKNOWLEDGED -> RESOLVED','REOPEN requires RESOLVED -> OPEN']),
    'exception action concurrency is serialized': 'pg_advisory_xact_lock' in mig and 'planning-exception:' in mig,
    'latest-run exception dashboard view exists': 'CREATE OR REPLACE VIEW v_current_planning_exceptions' in mig and "WHERE status='SUCCEEDED'" in mig,
    'current exception dashboard excludes terminal Sales Orders': "WHERE so.status IN ('CONFIRMED','PARTIALLY_SHIPPED')" in mig,
    'pegging edge endpoints are same-run guarded': 'pegging edge endpoints must belong to the same run' in mig,
    'pegging child inserts require RUNNING run': 'pegging evidence may only be inserted while run is RUNNING' in mig,
    'planning exception root/order/line consistency is DB guarded': all(x in mig for x in ['planning exception sales_order_id must match pegging run','planning exception root node must belong to the same pegging run','planning exception line must belong to its Sales Order']),
    'exception actions require planner/admin at DB layer': "urole NOT IN ('admin','planner')" in mig,
    'Domain pegging graph models exist': all(x in models for x in ['type PeggingRun struct','type PeggingNode struct','type PeggingEdge struct','type PeggingResult struct']),
    'Domain planning exception models exist': all(x in models for x in ['type PlanningException struct','type PlanningExceptionAction struct','type ExceptionScanResult struct']),
    'Sales Order and line anchor nodes exist': all(x in svc for x in ['"SALES_ORDER"','"SALES_ORDER_LINE"','"HAS_LINE"']),
    'accepted Promise evidence is traced': 'latestPromiseEvidence' in svc and '"PROMISED_BY"' in svc,
    'published BOP evidence is traced': 'latestBOPEvidence' in svc and '"REPRIORITIZED_BY"' in svc,
    'Promise and BOP evidence respect latest-decision supersession': all(x in svc for x in ['promiseStatus := "EFFECTIVE"','bopStatus := "EFFECTIVE"','bopStatus == "EFFECTIVE"']),
    'inventory supply is traced': 'v_stock_balance' in svc and '"INVENTORY"' in svc and '"ALLOCATED_FROM"' in svc,
    'Work Order supply is traced': 'peggTopManufacturingSupply' in svc and '"WORK_ORDER"' in svc,
    'released WO BOM snapshot is preferred': 'work_order_bom_snapshot_lines' in svc and 'from_snapshot' in svc,
    'live BOM fallback is traced': 'bom_components' in svc and 'traceLiveBOMRequirement' in svc,
    'Purchase Order supply is traced': 'purchase_orders' in svc and '"PURCHASE_ORDER"' in svc and '"PURCHASED_BY"' in svc,
    'supplier BLOCKED root cause is traced': 'supplier_quality_profiles' in svc and '"SUPPLIER_BLOCKED"' in svc,
    'quality HOLD root cause is traced': 'quality_status=\'HOLD\'' in svc and '"QUALITY_HOLD"' in svc,
    'Detailed Scheduling evidence is traced': 'detailed_schedule_orders' in svc and 'detailed_schedule_batches' in svc and '"DETAILED_SCHEDULE"' in svc,
    'Work Center capacity nodes are traced': '"WORK_CENTER"' in svc and '"USES_WORK_CENTER"' in svc,
    'formal supply pools prevent double pegging': all(x in svc for x in ['inventory  map[uuid.UUID]float64','wo         map[uuid.UUID]float64','po         map[uuid.UUID]float64','planned    map[uuid.UUID]float64']),
    'Pegging reuses shared maxInt helper without redeclaration': 'func maxInt(' not in svc,
    'material shortage exception exists': '"MATERIAL_SHORTAGE"' in svc and '"SHORTAGE"' in svc,
    'late PO and WO exceptions exist': '"LATE_PURCHASE_ORDER"' in svc and '"LATE_WORK_ORDER"' in svc,
    'capacity late/unscheduled exceptions exist': '"CAPACITY_LATE"' in svc and '"CAPACITY_UNSCHEDULED"' in svc,
    'unconverted CTP exception exists': '"UNCONVERTED_CTP"' in svc,
    'late Promise exception exists': '"LATE_PROMISE"' in svc,
    'root cause path is persisted': 'RootCausePath' in svc and 'root_cause_path' in mig,
    'canonical pegging result hash exists': 'canonicalPeggingHash' in svc and 'sha256.Sum256' in svc,
    'pegging run uses repeatable-read snapshot': 'sql.LevelRepeatableRead' in svc,
    'pegging horizon limits future formal supply': all(x in svc for x in ['horizonEnd time.Time','w.due_date <= $2','d.due_at <= $2','p.due_date <= $2']),
    'JSON graph evidence is inserted explicitly as jsonb': all(x in svc for x in ['$13::jsonb','$7::jsonb','$14::jsonb','$15::jsonb']),
    'global exception scan exists': 'func (s *PeggingService) Scan' in svc,
    'exception acknowledge/resolve/reopen API exists': 'ActOnException' in svc and 'actOnPlanningException' in api,
    'planner permissions exist': 'PermPeggingRun' in rbac and 'PermExceptionManage' in rbac and 'domain.RolePlanner' in rbac,
    'pegging mutation routes are protected': 'requirePermission(PermPeggingRun)' in router and 'requirePermission(PermExceptionManage)' in router,
    'OpenAPI pegging paths exist': all(p in openapi.get('paths',{}) for p in ['/sales-orders/{id}/pegging/run','/pegging-runs/{id}','/planning-exceptions','/planning-exceptions/scan','/planning-exceptions/{id}/actions']),
    'frontend Pegging API exists': 'export const PeggingApi' in front_api and '/planning-exceptions' in front_api,
    'Pegging / Exception UI exists': 'Full Pegging / Exception Management' in ui and 'Root Cause Exceptions' in ui,
    'frontend route and navigation exist': '/pegging-exceptions' in front_router and '/pegging-exceptions' in app,
    'E2E verifies graph and exception action': all(x in e2e for x in ['nodeType === \'SALES_ORDER\'','exceptionType === \'LATE_PROMISE\'','ACKNOWLEDGE','ACKNOWLEDGED']),
    'migration manager fingerprints 0034': '{34,' in manager and 'pegging_runs' in manager and 'planning_exceptions' in manager,
    'CI runs 0034 guard': 'check_full_pegging_exception_management.py' in ci,
}

failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
if failed:
    print(f"Full Pegging static guard failed: {len(failed)} check(s)")
    sys.exit(1)
print(f"Full Pegging / Exception Management static guard: {len(checks)} checks PASS")

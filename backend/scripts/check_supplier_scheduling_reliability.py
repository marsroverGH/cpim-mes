#!/usr/bin/env python3
from pathlib import Path
import json, sys

root = Path(__file__).resolve().parents[2]

def read(p):
    path = root / p
    return path.read_text() if path.exists() else ''

def has_all(text, *needles):
    return all(n in text for n in needles)

mig = read('backend/migrations/0035_supplier_scheduling_lead_time_reliability.sql')
models = read('backend/internal/domain/models.go')
svc = read('backend/internal/service/supplier_scheduling.go')
service = read('backend/internal/service/service.go')
ctp = read('backend/internal/service/ctp.go')
atp = read('backend/internal/service/atp_quality.go')
repo = read('backend/internal/repository/repository.go')
pegging = read('backend/internal/service/pegging.go')
api = read('backend/internal/api/supplier_scheduling.go')
router = read('backend/internal/api/router.go')
rbac = read('backend/internal/api/rbac.go')
rbac_test = read('backend/internal/api/rbac_test.go')
manager = read('backend/internal/migration/manager.go')
manager_test = read('backend/internal/migration/manager_test.go')
manager_guard = read('backend/scripts/check_migration_manager.py')
front_api = read('frontend/src/api/index.ts')
purchase_ui = read('frontend/src/views/PurchaseOrders.vue')
ui = read('frontend/src/views/SupplierScheduling.vue')
front_router = read('frontend/src/router/index.ts')
app = read('frontend/src/App.vue')
ci = read('.github/workflows/ci.yml')
e2e = read('e2e/tests/supplier-scheduling.spec.ts')
openapi = json.loads(read('backend/internal/api/openapi.json'))

checks = {
    '0035 migration exists': bool(mig) and 'CREATE TABLE supplier_schedule_events' in mig,
    'supplier schedule events are append-only evidence': has_all(mig, 'supplier_schedule_events_guard_trg', 'supplier_schedule_events are immutable; append a new revision'),
    'supplier schedule actor is DB validated': has_all(mig, "urole NOT IN ('admin','planner')", 'uname IS DISTINCT FROM NEW.actor_username', 'supplier schedule events require matching planner/admin actor'),
    'supplier schedule revision is unique': 'UNIQUE(purchase_order_id, revision_no)' in mig,
    'supplier ASN revisions are query-indexed': 'supplier_schedule_events_asn_idx' in mig and 'revision_no DESC' in mig,
    'supplier event semantic guard exists': has_all(mig, "event_type IN ('CONFIRM','REVISE','ASN','CANCEL')", "NEW.event_type IN ('CONFIRM','REVISE')", "NEW.event_type='ASN'"),
    'supplier scheduling is locked per PO': 'FOR UPDATE' in svc and 'sql.LevelSerializable' in svc,
    'schedule revision increments under PO lock': 'COALESCE(MAX(revision_no),0)+1' in svc,
    'supplier schedule mutation has client idempotency key': has_all(svc, 'eventId must be a valid UUID', 'pg_advisory_xact_lock', 'eventId is already used by a different supplier schedule event') and 'eventId' in front_api,
    'supplier schedule represents whole current PO remainder': has_all(svc, 'supplier schedule quantity must equal current PO remaining quantity', 'ReceivedQty') and 'current PO remaining quantity' in mig,
    'cancel supersedes prior supplier schedule by revision': has_all(mig, "e.event_type='CANCEL'", 'e.revision_no>cancel_ev.revision_no'),
    'canonical supplier schedule view exists': 'CREATE OR REPLACE VIEW v_purchase_order_supplier_schedule' in mig,
    'reliability run schema exists': has_all(mig, 'CREATE TABLE supplier_lead_time_runs', 'CREATE TABLE supplier_lead_time_results'),
    'reliability run actor is DB validated': has_all(mig, 'guard_supplier_lead_time_run_insert', "urole NOT IN ('admin','planner')", 'supplier lead-time run requires matching planner/admin actor'),
    'reliability results are append-only': 'supplier_lead_time_results_append_only_trg' in mig,
    'completed reliability run is immutable': 'completed supplier lead-time run is immutable' in mig,
    'reliability results insert only while RUNNING': 'supplier lead-time results may only be inserted while run is RUNNING' in mig,
    'reliability calculation uses repeatable-read snapshot': 'sql.LevelRepeatableRead' in svc,
    'reliability uses immutable receipt evidence': has_all(svc, 'JOIN purchase_receipts pr', 'MAX(pr.received_at)::date', 'HAVING SUM(pr.quantity)'),
    'only fully received POs become samples': 'HAVING SUM(pr.quantity) + 0.000001 >= po.quantity' in svc,
    'reliability window is bounded': has_all(svc, 'defaultReliabilityWindowDays', 'WindowDays > 3650'),
    'lead time average and population stddev calculated': has_all(svc, 'AVG(lead_days)', 'STDDEV_POP(lead_days)'),
    'P50 and P90 calculated': has_all(svc, 'PERCENTILE_CONT(0.50)', 'PERCENTILE_CONT(0.90)'),
    'on-time rate and lateness calculated': has_all(svc, 'AVG(on_time)', 'AVG(lateness_days)'),
    'recommended lead uses conservative p90 or mean plus sigma': has_all(svc, 'math.Max(p90, avg+stddev)', 'math.Ceil'),
    'supplier item and supplier fallback profiles exist': 'GROUP BY GROUPING SETS ((supplier_name,item_id),(supplier_name))' in svc,
    'hypothetical procurement uses supplier-level fallback when exact item profile is unavailable': has_all(repo, '(v.item_id=$1 OR v.item_id IS NULL)', 'ROW_NUMBER() OVER', 'CASE WHEN v.item_id=$1 THEN 0 ELSE 1 END'),
    'reliability confidence uses minimum sample threshold': has_all(svc, 'supplierReliabilityConfidence', 'samples >= minSamples'),
    'canonical reliability result hash exists': has_all(svc, 'canonicalSupplierReliabilityHash', 'sha256.Sum256'),
    'latest reliability view exists': 'CREATE OR REPLACE VIEW v_current_supplier_lead_time' in mig,
    'canonical PO planning view exists': 'CREATE OR REPLACE VIEW v_purchase_order_planning_schedule' in mig,
    'ASN outranks confirmation in canonical date': mig.find('WHEN ss.expected_arrival_date IS NOT NULL') < mig.find('WHEN ss.confirmed_delivery_date IS NOT NULL'),
    'confirmation outranks reliability in canonical date': mig.find('WHEN ss.confirmed_delivery_date IS NOT NULL') < mig.find('COALESCE(exact.sample_count,fallback.sample_count,0) >= COALESCE'),
    'reliability-adjusted date never improves original PO due': 'GREATEST(po.due_date, po.order_date + COALESCE' in mig,
    'PO due is canonical fallback': "ELSE po.due_date" in mig and "ELSE 'PO_DUE_DATE'" in mig,
    'Domain supplier schedule models exist': has_all(models, 'type SupplierScheduleEvent struct', 'type SupplierLeadTimeRun struct', 'type SupplierLeadTimeResult struct'),
    'PurchaseOrder exposes canonical supplier planning evidence': has_all(models, 'ExpectedDeliveryDate', 'ScheduleSource', 'RecommendedLeadTimeDays'),
    'PurchaseRepo reads canonical planning view': repo.count('v_purchase_order_planning_schedule') >= 2,
    'MRP firm PO uses canonical supplier planning date': 'day := PurchasePlanningDate(po)' in service,
    'MRP purchased-item lead time uses reliability': has_all(service, 'EffectiveLeadTimeDays', 'SUPPLIER_RELIABILITY', 'PlanningLeadTimeDays'),
    'MRP exposes lead-time provenance': has_all(models, 'PlanningLeadTimeDays', 'LeadTimeSource') and has_all(service, 'LeadTimeSource:', 'PlanningLeadTimeDays:'),
    'CTP firm PO uses canonical supplier planning date': 'd := PurchasePlanningDate(p)' in ctp,
    'CTP hypothetical purchase uses reliability lead time': has_all(ctp, 'EffectiveLeadTimeDays', 'nominalLeadTimeDays', 'leadTimeDays'),
    'ATP firm PO uses canonical supplier planning date': atp.count('PurchasePlanningDate(p)') >= 2,
    'Order Promising ATP cannot count supplier-delayed PO early': 'PurchasePlanningDate(p).After(through)' in atp,
    'blocked suppliers are excluded from reliability lead-time decision': has_all(repo, "COALESCE(q.status,'APPROVED')<>'BLOCKED'", 'EffectiveLeadTimeDays'),
    'MRP/CTP supplier selection remains conservative': has_all(repo, 'MAX(recommended_lead_days)', 'must not promise using an optimistic supplier cherry-pick'),
    'Pegging reads canonical PO planning schedule': 'v_purchase_order_planning_schedule' in pegging,
    'Pegging traces supplier confirmation': has_all(pegging, 'SUPPLIER_CONFIRMATION', 'CONFIRMED_BY'),
    'Pegging traces ASN': has_all(pegging, 'SUPPLIER_ASN', 'SHIPPED_BY'),
    'Pegging traces lead-time profile': has_all(pegging, 'LEAD_TIME_PROFILE', 'PLANNED_USING'),
    'Pegging late PO uses expected delivery evidence': has_all(pegging, 'ExpectedDeliveryDate', 'ScheduleSource', 'LATE_PURCHASE_ORDER'),
    'supplier confirmation late exception exists': 'SUPPLIER_CONFIRMATION_LATE' in pegging and 'SUPPLIER_CONFIRMATION_LATE' in mig,
    'supplier reliability risk exception exists': 'SUPPLIER_RELIABILITY_RISK' in pegging and 'SUPPLIER_RELIABILITY_RISK' in mig,
    'Supplier Scheduling API exists': has_all(api, 'recordSupplierScheduleEvent', 'refreshSupplierReliability', 'listSupplierReliability', 'getSupplierReliabilityRun'),
    'Supplier Scheduling mutation routes are protected': has_all(router, 'requirePermission(PermSupplierScheduleManage)', 'requirePermission(PermSupplierReliabilityRun)'),
    'planner supplier permissions exist': has_all(rbac, 'PermSupplierScheduleManage', 'PermSupplierReliabilityRun') and 'domain.RolePlanner' in rbac,
    'operator/viewer cannot mutate supplier planning': has_all(rbac_test, 'PermSupplierScheduleManage', 'PermSupplierReliabilityRun'),
    'OpenAPI supplier scheduling paths exist': all(p in openapi.get('paths', {}) for p in ['/purchase-orders/{id}/supplier-schedule','/purchase-orders/{id}/supplier-schedule/events','/supplier-scheduling/reliability','/supplier-scheduling/reliability/refresh','/supplier-scheduling/reliability-runs','/supplier-scheduling/reliability-runs/{id}']),
    'OpenAPI MRP exposes supplier reliability lead-time provenance': all(k in openapi.get('components',{}).get('schemas',{}).get('MRPResult',{}).get('properties',{}) for k in ['planningLeadTimeDays','leadTimeSource']),
    'frontend supplier scheduling API exists': has_all(front_api, 'SupplierSchedulingApi', 'supplierSchedule:', 'addSupplierScheduleEvent:'),
    'Purchase Order UI exposes confirmation and ASN workflow': has_all(purchase_ui, 'Supplier Schedule', 'CONFIRM', 'REVISE', 'ASN', 'CANCEL'),
    'Supplier Scheduling reliability UI exists': has_all(ui, 'Supplier Scheduling / Lead-Time Reliability', 'Reliability', 'Planning Date'),
    'frontend supplier scheduling route and navigation exist': '/supplier-scheduling' in front_router and '/supplier-scheduling' in app,
    '0035 extends pegging graph vocabulary': has_all(mig, "'SUPPLIER_CONFIRMATION'", "'SUPPLIER_ASN'", "'LEAD_TIME_PROFILE'", "'CONFIRMED_BY'", "'SHIPPED_BY'", "'PLANNED_USING'"),
    'E2E verifies confirmation ASN reliability and canonical planning date': has_all(e2e, 'SUPPLIER_CONFIRMATION', "scheduleSource).toBe('ASN')", 'refresh', 'recommendedLeadDays', "scheduleSource).toBe('RELIABILITY')"),
    'E2E verifies supplier schedule idempotent retry': has_all(e2e, 'confirmEventId', 'retry.status()).toBe(201)', 'history).toHaveLength(2)'),
    'OpenAPI requires supplier schedule idempotency key': 'eventId' in openapi['paths']['/purchase-orders/{id}/supplier-schedule/events']['post']['requestBody']['content']['application/json']['schema'].get('required', []),
    'migration manager fingerprints 0035': '{35,' in manager and 'supplier_schedule_events' in manager and 'supplier_lead_time_runs' in manager,
    'migration manager includes 0035 under current migration set': '{35,' in manager and 'len(migs) != 41' in manager_test,
    'migration guard advances beyond 0035 without losing it': "'41 ordered SQL migrations exist'" in manager_guard and 'len(files) == 41' in manager_guard,
    'CI runs 0035 guard': 'check_supplier_scheduling_reliability.py' in ci,
}

failed=[]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
    if not ok:
        failed.append(name)
if failed:
    print(f'Supplier Scheduling / Lead-Time Reliability static guard failed: {len(failed)} check(s)')
    sys.exit(1)
print(f'Supplier Scheduling / Lead-Time Reliability static guard: {len(checks)} checks PASS')

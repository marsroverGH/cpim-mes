#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[1]
mig = (root/'migrations/0032_advanced_order_promising.sql').read_text()
svc = (root/'internal/service/order_promising.go').read_text()
ctp = (root/'internal/service/ctp.go').read_text()
atp = (root/'internal/service/atp_quality.go').read_text()
detailed = (root/'internal/service/detailed_scheduling.go').read_text()
mrp = (root/'internal/service/service.go').read_text()
router = (root/'internal/api/router.go').read_text()
rbac = (root/'internal/api/rbac.go').read_text()
openapi = (root/'internal/api/openapi.json').read_text()
ui = (root.parent/'frontend/src/views/SalesOrders.vue').read_text()
api = (root.parent/'frontend/src/api/index.ts').read_text()
manager = (root/'internal/migration/manager.go').read_text()
ci = (root.parent/'.github/workflows/ci.yml').read_text()

checks = {
    '0032 migration exists': (root/'migrations/0032_advanced_order_promising.sql').exists(),
    'promise evidence tables exist': all(x in mig for x in ['order_promise_runs','order_promise_line_results','order_promise_confirmations','order_promise_acceptances']),
    'promise evidence immutable': all(x in mig for x in ['order_promise_line_results_append_only_trg','order_promise_confirmations_append_only_trg','order_promise_acceptances_append_only_trg']),
    'split confirmation schema exists': 'sequence_no' in mig and 'confirmed_date' in mig and 'CTP_MIXED' in mig,
    'ATP self-demand exclusion exists': 'ExcludeSalesOrderLineIDs' in atp and 'l.id NOT IN' in atp and 'ExcludeSalesOrderLineIDs: lineIDs' in svc,
    'ATP reservations are not double-counted': 'l.cancelled_qty-l.allocated_qty' in atp and 'IncludeAllocatedQty' in atp,
    'ATP same-item double-use guard exists': all(x in svc for x in ['consumedATP','calcLines','currentAllocatedByItem']),
    'quality HOLD stock excluded from ATP/CTP': "quality_status='OK'" in atp and "quality_status='OK'" in ctp,
    'blocked suppliers excluded from CTP': 'SupplierQualityStatus == "BLOCKED"' in ctp,
    'CTP material demand is cumulative within run': 'MaterialReadyWithUsage' in ctp and 'materialUsage' in svc and 'cumulativeUsage[row.ChildID]' in ctp,
    'CTP capacity is conservative across lines': 'capacityNotBefore' in svc and 'capacityNotBefore = *capOrder.ScheduledEnd' in svc,
    'CTP reuses MRP netting core': 'netMRPBucket' in ctp and 'MRPService) Simulate' in mrp,
    'MRP simulation avoids LLC writes': 'return s.run(ctx, req, false)' in mrp and 'if recomputeLLC' in mrp,
    'capacity CTP reuses detailed allocator': 'SimulateCTPOrder' in detailed and 'bestDetailedCandidate' in detailed and 'commitDetailedCandidate' in detailed,
    'capacity simulation does not persist schedule': 'SimulateCTPOrder' in detailed and 'persistDetailedSchedule' not in detailed[detailed.index('func (s *CRPService) SimulateCTPOrder'):detailed.index('func splitTransferBatches')],
    'promise check creates immutable what-if run': "status='SUCCEEDED'" in svc and 'order_promise_line_results' in svc,
    'accept revalidates canonical result hash': 'fresh.hash != *stored.Run.ResultHash' in svc and 'STALE_PROMISE' in svc,
    'incomplete promise cannot be accepted': 'promise run does not fully cover open demand' in svc,
    'promised-date DB reconciliation exists': 'assert_sales_order_promise_acceptance' in mig and "cpim.promise_accept" in mig,
    'partial-order promise reconciliation preserves closed lines': 'FROM order_promise_line_results r' in mig and 'SELECT MAX(promised_date) INTO expected_header FROM sales_order_lines' in mig,
    'accepted evidence reconciles hash totals and dates': all(x in mig for x in ['r.result_hash=a.result_hash','COALESCE(SUM(c.quantity),0)','r.earliest_full_date IS DISTINCT FROM MAX(c.confirmed_date)']),
    'planner/admin promise permission exists': 'PermSalesOrderPromise' in rbac and 'PermSalesOrderPromise' in router,
    'promise mutation routes protected': 'requirePermission(PermSalesOrderPromise)' in router,
    'OpenAPI promise paths exist': all(x in openapi for x in ['/sales-orders/{id}/promise/check','/sales-orders/{id}/promise/accept','/order-promise-runs/{id}']),
    'frontend promise API exists': all(x in api for x in ['promiseCheck','promiseAccept','promiseRuns','promiseRun']),
    'Sales Order UI exposes promise workflow': 'Advanced Order Promising / ATP + CTP' in ui and 'Promise確定' in ui and '再計算' in ui,
    'migration manager fingerprints 0032': '{32,' in manager and 'public.order_promise_runs' in manager,
    'CI runs 0032 guard': 'check_advanced_order_promising.py' in ci,
}
failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
if failed:
    print(f'Advanced Order Promising static guard: {len(checks)-len(failed)}/{len(checks)} PASS')
    sys.exit(1)
print(f'Advanced Order Promising static guard: {len(checks)} checks PASS')

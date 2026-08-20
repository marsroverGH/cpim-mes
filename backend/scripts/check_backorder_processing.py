#!/usr/bin/env python3
from pathlib import Path
import json, sys

root = Path(__file__).resolve().parents[2]
mig = (root/'backend/migrations/0033_backorder_processing_product_allocation.sql').read_text() if (root/'backend/migrations/0033_backorder_processing_product_allocation.sql').exists() else ''
domain = (root/'backend/internal/domain/models.go').read_text()
bop = (root/'backend/internal/service/backorder.go').read_text() if (root/'backend/internal/service/backorder.go').exists() else ''
alloc = (root/'backend/internal/service/product_allocation.go').read_text() if (root/'backend/internal/service/product_allocation.go').exists() else ''
api = (root/'backend/internal/api/backorders.go').read_text() if (root/'backend/internal/api/backorders.go').exists() else ''
router = (root/'backend/internal/api/router.go').read_text()
rbac = (root/'backend/internal/api/rbac.go').read_text()
manager = (root/'backend/internal/migration/manager.go').read_text()
frontend_api = (root/'frontend/src/api/index.ts').read_text()
ui = (root/'frontend/src/views/Backorders.vue').read_text() if (root/'frontend/src/views/Backorders.vue').exists() else ''
route_ui = (root/'frontend/src/router/index.ts').read_text()
app = (root/'frontend/src/App.vue').read_text()
ci = (root/'.github/workflows/ci.yml').read_text()
openapi = json.loads((root/'backend/internal/api/openapi.json').read_text())

def has_all(text, *needles): return all(n in text for n in needles)

checks = {
 '0033 migration exists': bool(mig),
 'customer service class and order priority schema': has_all(mig,'CREATE TABLE customer_service_classes','ALTER TABLE customers','service_class_code','ALTER TABLE sales_orders','priority text'),
 'product allocation plan and bucket schema': has_all(mig,'CREATE TABLE product_allocation_plans','CREATE TABLE product_allocation_buckets','allocation_pct'),
 'active allocation plans require 100 percent': has_all(mig,'bucket percentages must total 100','ABS(pct-100)'),
 'overlapping active allocation plans blocked': 'active product allocation plans may not overlap' in mig,
 'allocation plan activation is concurrency-serialized': has_all(mig,'Serialize activations for the same item','FROM items WHERE id=OLD.item_id FOR UPDATE'),
 'active allocation master becomes immutable': has_all(mig,'product allocation buckets are immutable after plan activation','non-DRAFT product allocation plan is immutable'),
 'BOP immutable evidence schema': has_all(mig,'CREATE TABLE backorder_runs','CREATE TABLE backorder_run_lines','CREATE TABLE backorder_run_confirmations','CREATE TABLE backorder_publications','append_only'),
 'BOP result quantities reconcile at DB level': 'allocated_qty + atp_qty + ctp_qty + backorder_qty' in mig and 'ABS((' in mig,
 'Promise and BOP decisions supersede by timestamp': has_all(mig,'p.published_at>a.accepted_at','a.accepted_at>p.published_at'),
 'published BOP reconciles Sales Order promised dates': has_all(mig,'assert_backorder_publication','published BOP proposal does not reconcile','sales order header promised_date does not match published BOP result'),
 'Domain models exist': has_all(domain,'type CustomerServiceClass struct','type ProductAllocationPlan struct','type BackorderRun struct','type BackorderResult struct'),
 'BOP ranks order priority then service class': has_all(bop,'bopPriorityRank','ServicePriority','RequestedDate','sort.SliceStable'),
 'existing inventory allocation is fixed/protected': has_all(bop,'fixed := math.Min','Source: "ALLOCATED"','AllocatedQty: fixed'),
 'BOP excludes target demand from shared ATP': has_all(bop,'ExcludeSalesOrderLineIDs: lineIDs','consumedATP'),
 'Product Allocation caps scarce ATP': has_all(bop,'bucketLimit','bucketUsed','PRODUCT_ALLOCATION'),
 'Product Allocation applies to ATP before CTP': bop.find('line.ATPQty = atpQty') >= 0 and bop.find('MaterialReadyWithUsage') > bop.find('line.ATPQty = atpQty'),
 'BOP reuses material CTP and detailed capacity simulation': has_all(bop,'MaterialReadyWithUsage','SimulateCTPOrder'),
 'BOP material demand is cumulative across orders': 'materialUsage := map[uuid.UUID]float64{}' in bop,
 'BOP capacity is conservative across orders': has_all(bop,'capacityNotBefore := start','capacityNotBefore = *capOrder.ScheduledEnd'),
 'Preview writes evidence but not operations': has_all(bop,'INSERT INTO backorder_run_lines','INSERT INTO backorder_run_confirmations') and 'INSERT INTO work_orders' not in bop and 'INSERT INTO purchase_orders' not in bop and 'INSERT INTO inventory_txns' not in bop and 'INSERT INTO detailed_schedule_runs' not in bop,
 'Publish uses canonical stale hash': has_all(bop,'canonicalBackorderHash','STALE_BOP','fresh.hash != *stored.Run.ResultHash'),
 'Publish locks Sales Orders and Items': has_all(bop,'SELECT id FROM sales_orders WHERE id=$1 FOR UPDATE','SELECT id FROM items WHERE id=$1 FOR UPDATE'),
 'Publish freezes mutable planning snapshot': has_all(bop,'lockBOPPlanningSnapshot','LOCK TABLE','inventory_txns, lots, lot_movements','purchase_orders, purchase_receipts, work_orders','product_allocation_plans, product_allocation_buckets','IN SHARE MODE'),
 'Publish uses narrow DB write context': has_all(bop,"SET LOCAL cpim.bop_publish='on'",'INSERT INTO backorder_publications'),
 'Product Allocation service validates 100 percent': has_all(alloc,'math.Abs(total-100)','allocation bucket percentages must total 100'),
 'Service class and order priority APIs exist': has_all(api,'setCustomerServiceClass','setSalesOrderPriority'),
 'Preview Publish and Product Allocation APIs exist': has_all(api,'previewBackorders','publishBackorders','createProductAllocationPlan','activateProductAllocationPlan'),
 'Planner permissions exist': has_all(rbac,'PermBackorderRun','PermProductAllocation') and has_all(rbac,'PermBackorderRun:','PermProductAllocation:'),
 'BOP mutation routes permission protected': has_all(router,'requirePermission(PermBackorderRun)).Post("/backorders/preview"','requirePermission(PermBackorderRun)).Post("/backorders/publish"','requirePermission(PermProductAllocation)).Post("/product-allocation-plans"'),
 'OpenAPI BOP paths exist': all(x in openapi.get('paths',{}) for x in ['/backorders/preview','/backorders/publish','/backorders/runs','/product-allocation-plans','/customers/{id}/service-class','/sales-orders/{id}/priority']),
 'Frontend BOP API exists': has_all(frontend_api,'export const BackordersApi','preview:','publish:','createPlan:'),
 'Backorder UI exists': has_all(ui,'Backorder Processing / Product Allocation','BOP Preview','Product Allocation Plans','Publish'),
 'Backorder route and navigation exist': has_all(route_ui,"path: '/backorders'") and has_all(app,"to: '/backorders'"),
 'migration manager fingerprints 0033': '{33, `SELECT to_regclass(\'public.backorder_runs\')' in manager,
 'CI runs 0033 guard': 'check_backorder_processing.py' in ci,
 'CI Playwright is serialized to avoid login flake': '--workers=1' in ci,
}
failed=[]
for name,ok in checks.items():
    print(('PASS' if ok else 'FAIL')+': '+name)
    if not ok: failed.append(name)
if failed:
    sys.exit(1)
print(f'Backorder Processing static guard: {len(checks)} checks PASS')

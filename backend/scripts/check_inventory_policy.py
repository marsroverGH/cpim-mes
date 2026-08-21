#!/usr/bin/env python3
"""Static regression guard for migration 0036 Statistical Inventory Policy."""
from pathlib import Path
import json, sys
root=Path(__file__).resolve().parents[2]
read=lambda p:(root/p).read_text(encoding='utf-8')
mig=read('backend/migrations/0036_statistical_inventory_policy.sql')
svc=read('backend/internal/service/inventory_policy.go')
models=read('backend/internal/domain/models.go')
main=read('backend/internal/service/service.go')
atp=read('backend/internal/service/atp_quality.go')
ctp=read('backend/internal/service/ctp.go')
peg=read('backend/internal/service/pegging.go')
api=read('backend/internal/api/inventory_policy.go')
rbac=read('backend/internal/api/rbac.go')
router=read('backend/internal/api/router.go')
front=read('frontend/src/api/index.ts')
ui=read('frontend/src/views/InventoryPolicy.vue')
mrpui=read('frontend/src/views/Mrp.vue')
atpui=read('frontend/src/views/Atp.vue')
routes=read('frontend/src/router/index.ts')
app=read('frontend/src/App.vue')
e2e=read('e2e/tests/inventory-policy.spec.ts')
manager=read('backend/internal/migration/manager.go')
manager_test=read('backend/internal/migration/manager_test.go')
manager_guard=read('backend/scripts/check_migration_manager.py')
ci=read('.github/workflows/ci.yml')
openapi=json.loads(read('backend/internal/api/openapi.json'))

def allin(text,*xs): return all(x in text for x in xs)
checks={
 '0036 migration exists': 'CREATE TABLE inventory_policy_versions' in mig,
 'policy config is versioned': allin(mig,'version_no','UNIQUE(item_id,version_no)','DRAFT','ACTIVE','ARCHIVED'),
 'one ACTIVE policy per item': 'ux_inventory_policy_one_active' in mig and "WHERE status='ACTIVE'" in mig,
 'statistical and fixed policy modes': allin(mig,"STATISTICAL","FIXED","fixed_safety_stock"),
 'service-level range is DB guarded': allin(mig,'service_level','0.500000','0.999900'),
 'demand history window and minimum history guarded': allin(mig,'demand_window_days','min_history_days','min_history_days <= demand_window_days'),
 'Min-Max and Safety-Stock replenishment modes': allin(mig,"'SAFETY_STOCK','MIN_MAX'",'order_cycle_days'),
 'policy actor is DB validated planner/admin': allin(mig,'guard_inventory_policy_actor',"urole NOT IN ('planner','admin')"),
 'policy identity/audit immutable after creation': 'inventory policy identity/audit fields are immutable' in mig,
 'active and archived policy configuration immutable': 'active/archived inventory policy configuration is immutable' in mig,
 'version lifecycle is DB guarded': allin(mig,'DRAFT','ACTIVE','ARCHIVED','ARCHIVED inventory policy is terminal'),
 'future-effective version cannot create coverage gap': allin(mig,'future-effective inventory policy must remain DRAFT','eco_business_date(now())') and 'future-effective inventory policy must remain DRAFT' in svc,
 'calculation run schema exists': allin(mig,'CREATE TABLE inventory_policy_runs','RUNNING','COMPLETE','FAILED'),
 'calculation result schema exists': 'CREATE TABLE inventory_policy_results' in mig,
 'calculation evidence is append-only': 'inventory policy results are append-only evidence' in mig,
 'completed calculation run is immutable': 'completed inventory policy run is immutable' in mig,
 'results only insert while run RUNNING': 'inventory policy results may only be inserted while run is RUNNING' in mig,
 'result must reference active same-item policy': 'result must reference ACTIVE policy for same item' in mig,
 'current canonical policy view exists': 'CREATE OR REPLACE VIEW v_current_inventory_policy' in mig,
 'uncalculated active policy has conservative item-master fallback': allin(mig,"CASE WHEN r.id IS NULL THEN 'FALLBACK' ELSE 'CALCULATED' END",'i.safety_stock'),
 '0034 pegging vocabulary includes inventory policy': allin(mig,"'INVENTORY_POLICY'","'PROTECTED_BY'"),
 '0034 exception vocabulary includes inventory policy breaches': allin(mig,"'SAFETY_STOCK_BREACH'","'REORDER_POINT_BREACH'"),
 'Domain inventory policy models exist': allin(models,'type InventoryPolicyVersion struct','type InventoryPolicyRun struct','type InventoryPolicyResult struct','type EffectiveInventoryPolicy struct'),
 'MRP result exposes policy targets': allin(models,'SafetyStockTarget','ReorderPoint','MinQty','MaxQty','InventoryPolicyMode'),
 'ATP result exposes protected buffer': allin(models,'SafetyStockProtected','PolicyStatus'),
 'policy service defaults statistical 95 percent': allin(svc,'defaultInventoryPolicyServiceLevel = 0.95','STATISTICAL','MIN_MAX'),
 'policy create serializes on item': allin(svc,'SELECT code FROM items WHERE id=$1 FOR UPDATE','COALESCE(MAX(version_no),0)+1'),
 'activation serializes and archives prior ACTIVE': allin(svc,'SELECT * FROM inventory_policy_versions WHERE id=$1 FOR UPDATE',"SET status='ARCHIVED'", "SET status='ACTIVE'"),
 'inverse normal CDF exists': 'inverseStandardNormalCDF' in svc and "Acklam" in svc,
 'statistical safety stock combines demand and lead variability': 'leadMean*demandStddev*demandStddev + meanDemand*meanDemand*leadStddev*leadStddev' in svc,
 'demand statistics use ISSUE only': allin(svc,"txn_type='ISSUE'",'STDDEV_POP(qty)','generate_series'),
 'demand history uses business timezone': allin(svc,'eco_business_timezone()','eco_business_date(now())'),
 'zero-demand days are included': allin(svc,'generate_series','LEFT JOIN daily','COALESCE(v.qty,0)'),
 'supplier lead-time variability integrates 0035': allin(svc,'v_current_supplier_lead_time','stddev_lead_days','SUPPLIER_RELIABILITY'),
 'blocked suppliers excluded from lead-time policy': "COALESCE(q.status,'APPROVED')<>'BLOCKED'" in svc,
 'inventory policy calculation is repeatable-read': 'sql.LevelRepeatableRead' in svc,
 'calculation freezes active policy versions': 'FOR SHARE OF v' in svc,
 'fixed policy fallback is supported': allin(svc,'p.PolicyMethod == "FIXED"','p.FixedSafetyStock'),
 'statistical policy requires configured history minimum': 'demand.ObservationDays >= p.MinHistoryDays' in svc,
 'ROP is lead-time demand plus safety stock': 'reorder := demand.Average*lead.Mean + safety' in svc,
 'Min-Max order-up-to includes order cycle demand': 'maxQty += demand.Average * float64(p.OrderCycleDays)' in svc,
 'calculation hash is canonical SHA256': allin(svc,'canonicalInventoryPolicyHash','sha256.Sum256'),
 'effective policy falls back to legacy item safety stock': allin(svc,'LEGACY_FIXED','item.SafetyStock','ITEM_MASTER'),
 'uncalculated active policy forces safety-stock mode': allin(svc,'p.CalculationStatus != "CALCULATED"','p.ReplenishmentMethod = "SAFETY_STOCK"'),
 'Min-Max MRP replenishes only below ROP': allin(svc,'projectedBefore+1e-9 >= policy.ReorderPoint','net = policy.MaxQty - projectedBefore'),
 'Min-Max MRP lot-sizing still applied': 'ApplyLotSize(net, 0, lotSize, eoq, method)' in svc,
 'MRP owns inventory policy service': allin(main,'inventoryPolicy *InventoryPolicyService','inventoryPolicy.Effective(ctx, it)'),
 'MRP start trigger distinguishes Min-Max from Safety Stock': allin(main,'startThreshold := policy.SafetyStock','policy.ReplenishmentMethod == "MIN_MAX"','startThreshold = policy.ReorderPoint'),
 'MRP netting uses effective policy': 'netMRPBucketWithInventoryPolicy' in main,
 'MRP emits policy provenance': allin(main,'InventoryPolicyID:','InventoryPolicyMode:','InventoryPolicyStatus:','ServiceLevel:'),
 'ATP protects statistical/fixed safety stock': allin(atp,'startingOH -= policy.SafetyStock','available -= policy.SafetyStock'),
 'ATP AvailabilityThrough uses policy protection': allin(atp,'inventoryPolicy.Effective','SafetyStock'),
 'CTP respects effective inventory policy': allin(ctp,'inventoryPolicy.Effective','netMRPBucketWithInventoryPolicy','policy.SafetyStock'),
 'Pegging reserves safety stock from shared supply': allin(peg,'gross_available','GREATEST(gross_available-safety_stock,0)','pools.inventory[itemID] = st.Available'),
 'Pegging creates inventory policy node': allin(peg,'inventoryPolicyNode','"INVENTORY_POLICY"'),
 'Pegging canonical policy view status column matches SQL contract': "COALESCE(ip.status,'LEGACY') AS policy_status" in peg and 'ip.policy_status' not in peg,
 'Pegging links inventory to policy': '"PROTECTED_BY"' in peg,
 'Pegging raises safety-stock breach': allin(peg,'"SAFETY_STOCK_BREACH"','st.GrossAvailable+1e-9 < st.SafetyStock'),
 'Pegging raises reorder-point breach': allin(peg,'"REORDER_POINT_BREACH"','st.GrossAvailable+1e-9 < st.ReorderPoint'),
 'Inventory Policy APIs exist': allin(api,'listInventoryPolicies','createInventoryPolicyVersion','activateInventoryPolicyVersion','archiveInventoryPolicyVersion','refreshInventoryPolicies'),
 'Inventory Policy run-history APIs exist': allin(api,'listInventoryPolicyRuns','getInventoryPolicyRun'),
 'planner permissions exist': allin(rbac,'PermInventoryPolicyManage','PermInventoryPolicyRun','domain.RolePlanner'),
 'inventory policy mutation routes are protected': allin(router,'requirePermission(PermInventoryPolicyManage)','requirePermission(PermInventoryPolicyRun)'),
 'OpenAPI inventory-policy paths exist': all(x in openapi.get('paths',{}) for x in ['/inventory-policies','/inventory-policy-versions','/inventory-policies/refresh','/inventory-policy-runs','/inventory-policy-runs/{id}']),
 'OpenAPI exposes inventory policy schemas': all(x in openapi.get('components',{}).get('schemas',{}) for x in ['InventoryPolicyVersion','EffectiveInventoryPolicy','InventoryPolicyResult','InventoryPolicyRunResult']),
 'Frontend Inventory Policy API exists': allin(front,'export const InventoryPolicyApi','createVersion:','activate:','refresh:'),
 'Inventory Policy UI exists': allin(ui,'Statistical Safety Stock / Inventory Policy','Service Level','Current Effective Policy','Policy Version History','Calculation Run History'),
 'Frontend route and navigation exist': allin(routes,"path: '/inventory-policy'") and allin(app,"to: '/inventory-policy'"),
 'MRP UI exposes SS ROP Min Max': allin(mrpui,"key: 'safetyStockTarget'","key: 'reorderPoint'","key: 'minQty'","key: 'maxQty'"),
 'ATP UI identifies protected safety stock': 'Safety Stock保護' in atpui,
 'unit tests cover z formula and Min-Max': allin(read('backend/internal/service/inventory_policy_test.go'),'TestInverseStandardNormalCDF95','TestStatisticalSafetyStockCombinesDemandAndLeadVariability','TestMinMaxMRPOrdersToMaxOnlyBelowROP'),
 'E2E verifies statistical targets': allin(e2e,"serviceLevel: 0.95",'zValue','safetyStock','reorderPoint','maxQty'),
 'E2E verifies MRP integration': allin(e2e,'inventoryPolicyMode','safetyStockTarget'),
 'E2E verifies ATP buffer protection': allin(e2e,'safetyStockProtected','inventoryPolicyId'),
 'E2E verifies version supersession': allin(e2e,"status).toBe('ARCHIVED')","status).toBe('ACTIVE')"),
 'E2E verifies pegging safety-stock exception': allin(e2e,"nodeType === 'INVENTORY_POLICY'","exceptionType === 'SAFETY_STOCK_BREACH'"),
 'migration manager fingerprints 0036': allin(manager,'{36,','inventory_policy_versions','v_current_inventory_policy'),
 'migration manager includes 0036 under current migration set': allin(manager,'{36,') and allin(manager_test,'len(migs) != 39','expected 39 migrations'),
 'migration guard advances beyond 0036 without losing it': allin(manager_guard,"'39 ordered SQL migrations exist'",'len(files) == 39'),
 'CI runs 0036 guard': 'check_inventory_policy.py' in ci,
}
failed=[]
for name,ok in checks.items():
    print(('PASS' if ok else 'FAIL')+': '+name)
    if not ok: failed.append(name)
if failed:
    print(f'Inventory Policy static guard failed: {len(failed)} check(s)')
    sys.exit(1)
print(f'Statistical Inventory Policy static guard: {len(checks)} checks PASS')

#!/usr/bin/env python3
from pathlib import Path
import json, sys
root=Path(__file__).resolve().parents[2]
def read(p): return (root/p).read_text(encoding='utf-8')
def allin(s,*xs): return all(x in s for x in xs)
mig=read('backend/migrations/0038_oee_production_performance_capacity_feedback.sql')
models=read('backend/internal/domain/models.go')
svc=read('backend/internal/service/production_performance.go')
shop=read('backend/internal/service/shopfloor.go')
detailed=read('backend/internal/service/detailed_scheduling.go')
cap=read('backend/internal/service/capacity.go')
finite=read('backend/internal/service/crp_finite.go')
peg=read('backend/internal/service/pegging.go')
services=read('backend/internal/service/service.go')
api=read('backend/internal/api/production_performance.go')
shopapi=read('backend/internal/api/actions_shopfloor_kpi.go')
router=read('backend/internal/api/router.go')
rbac=read('backend/internal/api/rbac.go')
rbact=read('backend/internal/api/rbac_test.go')
front=read('frontend/src/api/index.ts')
ui=read('frontend/src/views/ProductionPerformance.vue')
dui=read('frontend/src/views/DetailedScheduling.vue')
sui=read('frontend/src/views/ShopFloor.vue')
routes=read('frontend/src/router/index.ts')
app=read('frontend/src/App.vue')
manager=read('backend/internal/migration/manager.go')
manager_test=read('backend/internal/migration/manager_test.go')
manager_guard=read('backend/scripts/check_migration_manager.py')
ci=read('.github/workflows/ci.yml')
e2e=read('e2e/tests/production-performance.spec.ts')
tests=read('backend/internal/service/production_performance_test.go')
openapi=json.loads(read('backend/internal/api/openapi.json'))
checks={
 '0038 migration exists': (root/'backend/migrations/0038_oee_production_performance_capacity_feedback.sql').exists(),
 'performance run/result schema exists': allin(mig,'CREATE TABLE production_performance_runs','CREATE TABLE production_performance_results'),
 'performance results are immutable': 'production performance results are immutable evidence' in mig,
 'performance run actor is DB planner/admin guarded': allin(mig,'validate_production_performance_actor','matching active planner/admin'),
 'performance run must start RUNNING': allin(mig,'production performance run must start RUNNING','production_performance_run_insert_guard_trg'),
 'completed performance run immutable': 'completed production performance run is immutable' in mig,
 'result inserts only while RUNNING': allin(mig,'performance results may only be inserted while run is RUNNING',"s IS DISTINCT FROM 'RUNNING'"),
 'operation logs are append-only OEE evidence': 'operation logs are immutable production evidence' in mig,
 'capacity feedback versions exist': 'CREATE TABLE capacity_feedback_versions' in mig,
 'one ACTIVE feedback per work center': allin(mig,'capacity_feedback_one_active','WHERE status=\'ACTIVE\''),
 'feedback actor is planner/admin guarded': allin(mig,'validate_capacity_feedback_actor','validate_production_performance_actor'),
 'feedback insert is forced DRAFT with provenance reconciliation': allin(mig,'capacity feedback version must be created as unactioned DRAFT','source result/run/work center provenance mismatch','requires a COMPLETE production performance run'),
 'feedback evidence/configuration immutable': 'capacity feedback evidence/configuration is immutable' in mig,
 'feedback lifecycle DRAFT ACTIVE ARCHIVED': allin(mig,"status IN ('DRAFT','ACTIVE','ARCHIVED')",'ACTIVE capacity feedback may only remain ACTIVE or become ARCHIVED'),
 'future effective feedback cannot activate': 'future-effective capacity feedback cannot be activated yet' in mig,
 'current feedback view exists': 'CREATE VIEW v_current_capacity_feedback' in mig,
 'detailed feedback snapshots exist': 'CREATE TABLE detailed_schedule_capacity_feedback_snapshots' in mig,
 'detailed feedback snapshots immutable': 'detailed capacity feedback snapshots are immutable evidence' in mig,
 'pegging vocabulary contains CAPACITY_FEEDBACK': "'CAPACITY_FEEDBACK'" in mig,
 'pegging CALIBRATED_BY edge exists': "'CALIBRATED_BY'" in mig,
 'OEE capacity risk exception vocabulary exists': "'OEE_CAPACITY_RISK'" in mig,
 'domain production performance models exist': allin(models,'type ProductionPerformanceRun struct','type ProductionPerformanceResult struct','type ProductionPerformanceRunResult struct'),
 'domain feedback models exist': allin(models,'type CapacityFeedbackVersion struct','type DetailedScheduleCapacityFeedbackSnapshot struct'),
 'Detailed result exposes feedback snapshots': allin(models,'type DetailedScheduleResult struct','CapacityFeedback []DetailedScheduleCapacityFeedbackSnapshot'),
 'performance service exists': 'type ProductionPerformanceService struct' in svc,
 'performance actor planner/admin': allin(svc,'validatePlanner','RoleAdmin','RolePlanner'),
 'performance run uses repeatable-read snapshot': 'sql.LevelRepeatableRead' in svc,
 'OEE calculation uses Shop Floor logs': allin(svc,'operation_logs','wo_operations','START','STOP','COMPLETE','SCRAP'),
 'Availability formula includes unplanned loss': 'active/(active+pause+unplanned)' in svc,
 'Performance formula excludes setup': allin(svc,'runExSetup := math.Max(active-setup, 0)','ideal/runExSetup'),
 'Quality formula uses good and reject': 'good/(good+reject)' in svc,
 'OEE caps performance factor': 'availability*math.Min(performance, 1)*quality' in svc,
 'setup and speed losses calculated': allin(svc,'SetupLossMinutes: setup','SpeedLossMinutes: speedLoss'),
 'planned maintenance separated from availability loss': allin(svc,'PREVENTIVE_MAINTENANCE','PLANNED_DOWNTIME','plannedDown += equiv'),
 'unplanned maintenance reduces availability': allin(svc,'BREAKDOWN','UNPLANNED_DOWNTIME','unplannedDown += equiv'),
 'MTBF and MTTR calculated from breakdown': allin(svc,'breakdowns++','mtbf = active / float64(breakdowns)','mttr = breakdownMinutes / float64(breakdowns)'),
 'feedback recommendations bounded': allin(svc,'perfClamp(performance, 0.5, 1.2)','perfClamp(availability, 0.5, 1.0)'),
 'feedback remains DRAFT until explicit activation': allin(svc,'Status: "DRAFT"','ActivateFeedback'),
 'feedback drafts are created after run completion': svc.find("UPDATE production_performance_runs SET status='COMPLETE'") < svc.find('createCapacityFeedbackDraftTx(ctx, tx, row, actor, endDay)'),
 'feedback activation uses business timezone date': allin(svc,'eco_business_timezone()','businessDate'),
 'canonical performance result hash exists': allin(svc,'canonicalProductionPerformanceHash','sha256.New()'),
 'Shop Floor scrap service exists': allin(shop,'func (s *ShopFloorService) ReportScrap','event_type','SCRAP') or allin(shop,'ReportScrap','"SCRAP"'),
 'scrap actor remains JWT-derived': allin(shopapi,'authenticatedShopFloorActor','scrapOperation'),
 'scrap route permission protected': 'requirePermission(PermShopFloorExecute)).Post("/wo-operations/{opId}/scrap"' in router,
 'CRP applies active capacity feedback': 'ApplyCapacityFeedbackToWorkCenters' in cap,
 'finite CRP applies active capacity feedback': 'ApplyCapacityFeedbackToWorkCenters' in finite,
 'Detailed Scheduling applies active feedback': detailed.count('ApplyCapacityFeedbackToWorkCenters') >= 2,
 'CTP gets feedback through same Detailed allocator': allin(detailed,'func (s *CRPService) SimulateCTPOrder','ApplyCapacityFeedbackToWorkCenters'),
 'Detailed run persists feedback snapshot': 'INSERT INTO detailed_schedule_capacity_feedback_snapshots' in detailed,
 'Detailed history reloads feedback snapshot': 'SELECT * FROM detailed_schedule_capacity_feedback_snapshots WHERE run_id=$1' in detailed,
 'feedback never rewrites Work Center master': 'UPDATE work_centers SET efficiency' not in svc and 'UPDATE work_centers SET utilization' not in svc,
 'Pegging reads frozen feedback snapshot': 'detailed_schedule_capacity_feedback_snapshots' in peg,
 'Pegging creates CAPACITY_FEEDBACK node': allin(peg,'"CAPACITY_FEEDBACK"','"CAPFEEDBACK:"'),
 'Pegging links calibrated work center': '"CALIBRATED_BY"' in peg,
 'Pegging emits OEE capacity root cause': allin(peg,'"OEE_CAPACITY_RISK"','f.SourceOEE < 0.85'),
 'Production Performance service wired': allin(services,'ProductionPerformance *ProductionPerformanceService','ProductionPerformance: productionPerformance'),
 'Performance APIs exist': allin(api,'runProductionPerformance','listProductionPerformanceRuns','getProductionPerformanceRun','listCapacityFeedback','activateCapacityFeedback','archiveCapacityFeedback'),
 'Performance mutation routes protected': allin(router,'requirePermission(PermProductionPerformanceRun)).Post("/production-performance/runs"','requirePermission(PermCapacityFeedbackManage)).Post("/capacity-feedback/{id}/activate"','requirePermission(PermCapacityFeedbackManage)).Post("/capacity-feedback/{id}/archive"'),
 'planner performance permissions exist': allin(rbac,'PermProductionPerformanceRun','PermCapacityFeedbackManage','domain.RolePlanner'),
 'viewer mutation permission test includes feedback': allin(rbact,'PermProductionPerformanceRun','PermCapacityFeedbackManage'),
 'OpenAPI performance paths exist': all(x in openapi.get('paths',{}) for x in ['/production-performance/runs','/production-performance/runs/{id}','/capacity-feedback','/capacity-feedback/{id}/activate','/capacity-feedback/{id}/archive','/wo-operations/{opId}/scrap']),
 'OpenAPI performance schemas exist': all(x in openapi.get('components',{}).get('schemas',{}) for x in ['ProductionPerformanceRun','ProductionPerformanceResult','ProductionPerformanceRunResult','CapacityFeedbackVersion','DetailedScheduleCapacityFeedbackSnapshot']),
 'Frontend Production Performance API exists': allin(front,'export const ProductionPerformanceApi','/production-performance/runs','/capacity-feedback'),
 'Frontend performance result includes time-loss metrics': allin(front,'plannedProductionMinutes:number','runTimeMinutes:number','downtimeMinutes:number'),
 'Frontend Shop Floor scrap API exists': allin(front,'scrap:','/scrap'),
 'Production Performance UI exists': allin(ui,'OEE / Production Performance','Availability','Performance','Quality','OEE','MTBF','MTTR'),
 'Shop Floor UI exposes scrap': allin(sui,'Scrap','ShopFloorApi.scrap'),
 'frontend route/navigation exists': allin(routes,"path: '/production-performance'") and allin(app,"to: '/production-performance'"),
 'Detailed UI exposes capacity feedback snapshot': allin(dui,'value="feedback"','capacityFeedback'),
 'unit tests cover OEE and canonical hash': allin(tests,'TestCalculateOEERatios','TestPerformanceHashOrderIndependent'),
 'E2E creates Shop Floor scrap evidence': allin(e2e,'/scrap','rejectQuantity','quality'),
 'E2E activates capacity feedback': allin(e2e,'/capacity-feedback/${draft.id}/activate','ACTIVE'),
 'E2E verifies Detailed Scheduling snapshot': allin(e2e,'detailed-scheduling/run','capacityFeedback','feedbackVersionId'),
 'E2E verifies CTP under feedback': allin(e2e,'promise/check','ctpQty'),
 'E2E verifies Pegging feedback root cause': allin(e2e,"nodeType === 'CAPACITY_FEEDBACK'","OEE_CAPACITY_RISK"),
 'migration manager fingerprints 0038': allin(manager,'{38,','production_performance_runs','capacity_feedback_versions','detailed_schedule_capacity_feedback_snapshots'),
 'migration manager tests expect 38': allin(manager_test,'len(migs) != 38','expected 38 migrations'),
 'migration guard expects 38 ordered migrations': allin(manager_guard,"'38 ordered SQL migrations exist'",'range(1,39)'),
 'CI runs 0038 guard': 'check_production_performance.py' in ci,
}
failed=[]
for name,ok in checks.items():
 print(('PASS' if ok else 'FAIL')+': '+name)
 if not ok: failed.append(name)
if failed:
 print(f'OEE / Production Performance static guard failed: {len(failed)} check(s)')
 sys.exit(1)
print(f'OEE / Production Performance static guard: {len(checks)} checks PASS')

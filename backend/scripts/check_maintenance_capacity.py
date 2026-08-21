#!/usr/bin/env python3
from pathlib import Path
import json, sys
root=Path(__file__).resolve().parents[2]
def read(p): return (root/p).read_text(encoding='utf-8')
def allin(s,*xs): return all(x in s for x in xs)
mig=read('backend/migrations/0037_maintenance_capacity_downtime.sql')
models=read('backend/internal/domain/models.go')
svc=read('backend/internal/service/maintenance.go')
detailed=read('backend/internal/service/detailed_scheduling.go')
cap=read('backend/internal/service/capacity.go')
peg=read('backend/internal/service/pegging.go')
services=read('backend/internal/service/service.go')
api=read('backend/internal/api/maintenance.go')
router=read('backend/internal/api/router.go')
rbac=read('backend/internal/api/rbac.go')
rbac_test=read('backend/internal/api/rbac_test.go')
front=read('frontend/src/api/index.ts')
ui=read('frontend/src/views/Maintenance.vue')
dui=read('frontend/src/views/DetailedScheduling.vue')
routes=read('frontend/src/router/index.ts')
app=read('frontend/src/App.vue')
manager=read('backend/internal/migration/manager.go')
manager_test=read('backend/internal/migration/manager_test.go')
manager_guard=read('backend/scripts/check_migration_manager.py')
ci=read('.github/workflows/ci.yml')
e2e=read('e2e/tests/maintenance-capacity.spec.ts') if (root/'e2e/tests/maintenance-capacity.spec.ts').exists() else ''
openapi=json.loads(read('backend/internal/api/openapi.json'))
tests=read('backend/internal/service/detailed_scheduling_test.go')
checks={
 '0037 migration exists': (root/'backend/migrations/0037_maintenance_capacity_downtime.sql').exists(),
 'maintenance event identity schema exists': allin(mig,'CREATE TABLE maintenance_events','work_center_id','event_type'),
 'four maintenance/downtime types modeled': allin(mig,"'PREVENTIVE_MAINTENANCE'","'BREAKDOWN'","'PLANNED_DOWNTIME'","'UNPLANNED_DOWNTIME'"),
 'maintenance revisions are append-only evidence': allin(mig,'CREATE TABLE maintenance_event_revisions','maintenance revisions are append-only evidence'),
 'maintenance identity is immutable': 'maintenance event identity is immutable' in mig,
 'maintenance revision is sequential': allin(mig,'revision must be sequential','MAX(revision_no)'),
 'maintenance terminal state is guarded': allin(mig,'completed/cancelled maintenance event is terminal','COMPLETED','CANCELLED'),
 'maintenance capacity cannot exceed work center': 'maintenance capacity reduction exceeds work center resources' in mig,
 'maintenance actors DB validated': allin(mig,'guard_maintenance_actor',"urole NOT IN ('planner','admin')"),
 'current maintenance view exists': 'CREATE OR REPLACE VIEW v_current_maintenance_events' in mig,
 'effective capacity view excludes terminal events': allin(mig,'CREATE OR REPLACE VIEW v_effective_maintenance_capacity',"status IN ('PLANNED','ACTIVE')"),
 'detailed schedule freezes maintenance revisions': 'CREATE TABLE detailed_schedule_maintenance_snapshots' in mig,
 'detailed maintenance snapshots immutable': 'detailed maintenance snapshots are immutable evidence' in mig,
 '0030 dependency trigger run-id contract repaired in 0037': allin(mig,"TG_TABLE_NAME='detailed_schedule_batch_dependencies'",'SELECT run_id INTO rid FROM detailed_schedule_batches WHERE id=bid') and 'COALESCE(NEW.run_id,OLD.run_id)' not in mig,
 'pegging vocabulary contains maintenance node': "'MAINTENANCE_EVENT'" in mig,
 'pegging edge vocabulary contains capacity reduction': "'CAPACITY_REDUCED_BY'" in mig,
 'pegging exception vocabulary contains preventive maintenance': "'PREVENTIVE_MAINTENANCE_CAPACITY'" in mig,
 'pegging exception vocabulary contains breakdown': "'BREAKDOWN_CAPACITY'" in mig,
 'pegging exception vocabulary contains planned downtime': "'PLANNED_DOWNTIME_CAPACITY'" in mig,
 'pegging exception vocabulary contains unplanned downtime': "'UNPLANNED_DOWNTIME_CAPACITY'" in mig,
 'Domain maintenance event models exist': allin(models,'type MaintenanceEvent struct','type MaintenanceEventRevision struct','type CurrentMaintenanceEvent struct'),
 'Domain detailed maintenance snapshot exists': 'type DetailedScheduleMaintenanceSnapshot struct' in models,
 'Detailed Schedule result exposes maintenance evidence': allin(models,'type DetailedScheduleResult struct','Maintenance      []DetailedScheduleMaintenanceSnapshot'),
 'Maintenance service exists': 'type MaintenanceService struct' in svc,
 'Maintenance service validates planner/admin': allin(svc,'validateMaintenanceActor','RolePlanner','RoleAdmin'),
 'new breakdown/unplanned defaults ACTIVE': allin(svc,'eventType == "BREAKDOWN"','eventType == "UNPLANNED_DOWNTIME"','v = "ACTIVE"'),
 'new preventive/planned defaults PLANNED': 'v = "PLANNED"' in svc,
 'maintenance create locks work center capacity': 'SELECT machine_count,worker_count FROM work_centers WHERE id=$1 FOR SHARE' in svc,
 'maintenance revise serializes on event': 'FROM maintenance_events WHERE id=$1 FOR UPDATE' in svc,
 'maintenance current list exists': allin(svc,'func (s *MaintenanceService) List','v_current_maintenance_events'),
 'maintenance capacity overlap query exists': allin(svc,'func (s *MaintenanceService) CapacityEvents','start_at<$2 AND end_at>$1'),
 'CRP service owns maintenance dependency': allin(cap,'maintenance *MaintenanceService'),
 'Detailed state models machine reservations': allin(detailed,'type machineReservation struct','[]machineReservation'),
 'Detailed state models maintenance blocks': allin(detailed,'type maintenanceBlock struct','maintenance []maintenanceBlock'),
 'Detailed Scheduling loads maintenance capacity': allin(detailed,'s.maintenance.CapacityEvents','st.maintenance = append'),
 'CTP simulation loads same maintenance capacity': detailed.count('s.maintenance.CapacityEvents') >= 2,
 'Detailed allocator counts unavailable machines': allin(detailed,'usedMachines += r.UnavailableMachines','usedMachines+machines <= maxInt(st.wc.MachineCount, 1)'),
 'Detailed allocator counts unavailable workers': allin(detailed,'usedWorkers += r.UnavailableWorkers','usedWorkers+workers <= st.wc.WorkerCount'),
 'Detailed allocator splits on maintenance boundaries': allin(detailed,'r.StartAt.After(t)','r.EndAt.After(t)'),
 'Committed schedule reserves machine fragments': allin(detailed,'st.machine = append','machineReservation{'),
 'Detailed run returns maintenance snapshot': allin(detailed,'res.Maintenance = append','DetailedScheduleMaintenanceSnapshot'),
 'Detailed run persists maintenance snapshot': 'INSERT INTO detailed_schedule_maintenance_snapshots' in detailed,
 'Detailed history reloads maintenance snapshot': 'SELECT * FROM detailed_schedule_maintenance_snapshots WHERE run_id=$1' in detailed,
 'Detailed load available minutes subtract downtime': allin(detailed,'maintenanceAdjustedMachineMinutes','UnavailableMachines'),
 'Maintenance window compares time.Time with methods': allin(detailed,'!ev.EndAt.After(ws)','!ev.StartAt.Before(we)') and 'ev.EndAt <= ws' not in detailed,
 'Pegging planned evidence dereferences pointer contract': 'addMaintenanceCapacityEvidence(ctx, tx, g, order, line, *ev, wc, wn' in peg,
 'CTP reuses Detailed allocator rather than duplicate downtime logic': allin(detailed,'func (s *CRPService) SimulateCTPOrder','bestDetailedCandidate','planDetailedOnWC'),
 'Pegging traces maintenance snapshot': allin(peg,'detailed_schedule_maintenance_snapshots','addMaintenanceCapacityEvidence'),
 'Pegging creates maintenance node': allin(peg,'"MAINTENANCE_EVENT"','"MAINT:"'),
 'Pegging links work center to maintenance': '"CAPACITY_REDUCED_BY"' in peg,
 'Pegging emits maintenance root-cause exception': allin(peg,'MaintenanceExceptionType','g.exception(order, line, typ') and allin(svc,'BREAKDOWN_CAPACITY','UNPLANNED_DOWNTIME_CAPACITY'),
 'Maintenance service is wired into CRP and API services': allin(services,'Maintenance           *MaintenanceService','maintenance := &MaintenanceService','maintenance: maintenance'),
 'Maintenance APIs exist': allin(api,'listMaintenanceEvents','createMaintenanceEvent','reviseMaintenanceEvent','getMaintenanceEvent'),
 'Maintenance mutation routes are protected': allin(router,'requirePermission(PermMaintenanceManage)).Post("/maintenance-events"','requirePermission(PermMaintenanceManage)).Post("/maintenance-events/{id}/revisions"'),
 'Planner maintenance permission exists': allin(rbac,'PermMaintenanceManage','domain.RolePlanner'),
 'Operator cannot manage maintenance': allin(rbac_test,'operator cannot manage maintenance','PermMaintenanceManage, false'),
 'OpenAPI maintenance paths exist': all(x in openapi.get('paths',{}) for x in ['/maintenance-events','/maintenance-events/{id}','/maintenance-events/{id}/revisions']),
 'OpenAPI maintenance schemas exist': all(x in openapi.get('components',{}).get('schemas',{}) for x in ['CurrentMaintenanceEvent','MaintenanceEventInput','MaintenanceRevisionInput','DetailedScheduleMaintenanceSnapshot']),
 'Frontend Maintenance API exists': allin(front,'export const MaintenanceApi','/maintenance-events'),
 'Maintenance UI exists': allin(ui,'Maintenance / Capacity Downtime','PREVENTIVE_MAINTENANCE','BREAKDOWN','PLANNED_DOWNTIME','UNPLANNED_DOWNTIME'),
 'Frontend Maintenance route/navigation exist': allin(routes,"path: '/maintenance'") and allin(app,"to: '/maintenance'"),
 'Detailed Scheduling UI exposes Maintenance snapshots': allin(dui,'value="maintenance"','result.maintenance'),
 'unit test proves downtime splits schedule clock': allin(tests,'TestMaintenanceDowntimeSplitsDetailedClock','TestPartialMaintenanceKeepsRemainingMachineCapacity'),
 'E2E verifies downtime capacity and snapshot': allin(e2e,'maintenance','detailed-scheduling/run','unavailableMachines'),
 'E2E verifies CTP sees maintenance': allin(e2e,'promise/check','maintenance'),
 'E2E verifies Pegging maintenance root cause': allin(e2e,"nodeType === 'MAINTENANCE_EVENT'","BREAKDOWN_CAPACITY"),
 'migration manager fingerprints 0037': allin(manager,'{37,','maintenance_events','detailed_schedule_maintenance_snapshots'),
 'migration manager includes 0037 under current migration set': allin(manager_test,'len(migs) != 40','expected 40 migrations'),
 'migration guard advances beyond 0037 without losing it': allin(manager_guard,"'40 ordered SQL migrations exist'",'len(files) == 40'),
 'CI runs 0037 guard': 'check_maintenance_capacity.py' in ci,
}
failed=[]
for name,ok in checks.items():
    print(('PASS' if ok else 'FAIL')+': '+name)
    if not ok: failed.append(name)
if failed:
    print(f'Maintenance / Capacity Downtime static guard failed: {len(failed)} check(s)')
    sys.exit(1)
print(f'Maintenance / Capacity Downtime static guard: {len(checks)} checks PASS')

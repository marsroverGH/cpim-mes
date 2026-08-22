#!/usr/bin/env python3
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[2]
def read(rel):
 p=root/rel
 return p.read_text() if p.exists() else ''
def allin(s,*xs): return all(x in s for x in xs)
mig=read('backend/migrations/0039_realtime_dispatch_dynamic_rescheduling_schedule_adherence.sql')
dom=read('backend/internal/domain/models.go')
svc=read('backend/internal/service/dispatch_rescheduling.go')
detail=read('backend/internal/service/detailed_scheduling.go')
shop=read('backend/internal/service/shopfloor.go')
maint=read('backend/internal/service/maintenance.go')
perf=read('backend/internal/service/production_performance.go')
pegg=read('backend/internal/service/pegging.go')
services=read('backend/internal/service/service.go')
api=read('backend/internal/api/dispatch_rescheduling.go')
router=read('backend/internal/api/router.go')
rbac=read('backend/internal/api/rbac.go')
openapi=read('backend/internal/api/openapi.json')
manager=read('backend/internal/migration/manager.go')
manager_test=read('backend/internal/migration/manager_test.go')
manager_guard=read('backend/scripts/check_migration_manager.py')
front=read('frontend/src/api/index.ts')
routes=read('frontend/src/router/index.ts')
app=read('frontend/src/App.vue')
ui=read('frontend/src/views/DispatchRescheduling.vue')
doc=read('docs/REALTIME_DISPATCH_DYNAMIC_RESCHEDULING_SCHEDULE_ADHERENCE.md')
readme=read('README.md')
unit=read('backend/internal/service/dispatch_rescheduling_test.go')
e2e=read('e2e/tests/dispatch-rescheduling.spec.ts')
ci=read('.github/workflows/ci.yml')
checks={
 '0039 migration exists': (root/'backend/migrations/0039_realtime_dispatch_dynamic_rescheduling_schedule_adherence.sql').exists(),
 'dispatch policy version schema exists': allin(mig,'CREATE TABLE dispatch_policy_versions','freeze_minutes','firm_minutes','auto_reschedule','setup_match_bonus'),
 'one ACTIVE dispatch policy enforced': 'dispatch_policy_single_active_idx' in mig,
 'dispatch policy system seed exists': allin(mig,'00000000-0000-0000-0000-000000003901','SYSTEM','ACTIVE'),
 'current dispatch policy view exists': 'v_current_dispatch_policy' in mig,
 'schedule adherence immutable snapshot schema exists': allin(mig,'CREATE TABLE schedule_adherence_snapshots','CREATE TABLE schedule_adherence_rows'),
 'schedule adherence rows bind WO operations': allin(mig,'wo_operation_id','UNIQUE(snapshot_id,wo_operation_id)'),
 'dynamic reschedule run/change evidence exists': allin(mig,'CREATE TABLE dynamic_reschedule_runs','CREATE TABLE dynamic_reschedule_changes'),
 'reschedule trigger vocabulary covers execution disruptions': allin(mig,"'SHOP_FLOOR_PROGRESS'","'BREAKDOWN'","'UNPLANNED_DOWNTIME'","'CAPACITY_FEEDBACK_CHANGE'","'QUALITY_HOLD'","'MATERIAL_SHORTAGE'"),
 'reschedule statuses include blocked no-change': allin(mig,"'ACTIVATED'","'BLOCKED'","'NO_CHANGE'","'FAILED'"),
 'frozen firm flexible fences modeled': allin(mig,"'FROZEN'","'FIRM'","'FLEXIBLE'","'EXECUTED'"),
 'activation history and active execution pointer exist': allin(mig,'CREATE TABLE detailed_schedule_activation_history','CREATE TABLE detailed_schedule_execution_state'),
 'only complete schedule can activate': allin(mig,'only COMPLETE detailed schedule may become execution schedule',"run_status IS DISTINCT FROM 'COMPLETE'"),
 'activation requires active policy': allin(mig,'execution schedule requires ACTIVE dispatch policy',"policy_status IS DISTINCT FROM 'ACTIVE'"),
 'execution pointer reconciles immutable activation': allin(mig,'execution state must exactly match activation history','activation_history_id'),
 'reschedule input evidence becomes immutable': allin(mig,'completed reschedule run is immutable','reschedule input evidence is immutable'),
 'reschedule changes immutable': allin(mig,'reschedule_changes_immutable_trg','reject_schedule_execution_evidence_mutation'),
 'adherence evidence immutable': allin(mig,'adherence_snapshot_immutable_trg','adherence_rows_immutable_trg'),
 'execution signals are auditable': allin(mig,'CREATE TABLE schedule_reschedule_signals','processed_at','processed_run_id'),
 'signal original evidence immutable': 'reschedule signal evidence is immutable' in mig,
 'Shop Floor DB trigger queues reschedule signal': allin(mig,'operation_log_reschedule_signal_trg','enqueue_shopfloor_reschedule_signal'),
 'maintenance DB trigger queues capacity signal': allin(mig,'maintenance_reschedule_signal_trg','enqueue_maintenance_reschedule_signal'),
 'capacity feedback DB trigger queues signal': allin(mig,'capacity_feedback_reschedule_signal_trg','CAPACITY_FEEDBACK_CHANGE'),
 'quality HOLD DB trigger queues global reschedule signal': allin(mig,'lot_quality_hold_reschedule_signal_trg','enqueue_quality_hold_reschedule_signal',"'QUALITY_HOLD'","'LOT_QUALITY'"),
 'pegging vocabulary has RESCHEDULE_RUN': allin(mig,"'RESCHEDULE_RUN'","'RESCHEDULED_BY'"),
 'exception vocabulary has schedule adherence types': allin(mig,"'SCHEDULE_START_LATE'","'SCHEDULE_COMPLETION_LATE'"),
 'exception vocabulary has frozen firm executed horizon': allin(mig,"'FROZEN_HORIZON_CONFLICT'","'EXECUTION_COMMITMENT_CONFLICT'","'FIRM_HORIZON_CHANGE'","'RESCHEDULE_REQUIRED'"),
 'domain dispatch policy model exists': 'type DispatchPolicyVersion struct' in dom,
 'domain active execution state exists': 'type DetailedScheduleExecutionState struct' in dom,
 'domain dispatch board models exist': allin(dom,'type DispatchItem struct','type DispatchBoard struct'),
 'domain adherence models exist': allin(dom,'type ScheduleAdherenceSnapshot struct','type ScheduleAdherenceRow struct','type ScheduleAdherenceSummary struct'),
 'domain reschedule models exist': allin(dom,'type DynamicRescheduleRun struct','type DynamicRescheduleChange struct','type DynamicRescheduleResult struct'),
 'domain reschedule signal exists': 'type ScheduleRescheduleSignal struct' in dom,
 'schedule execution service exists': 'type ScheduleExecutionService struct' in svc,
 'schedule mutations require planner admin': allin(svc,'validatePlanner','RoleAdmin','RolePlanner'),
 'dispatch policy validation guards fences': allin(svc,'firmMinutes must be >= freezeMinutes >= 0','SetupMatchBonus'),
 'active execution schedule API service exists': 'func (s *ScheduleExecutionService) ExecutionState' in svc,
 'activation serializes current pointer': allin(svc,'FOR UPDATE','active execution schedule changed during rescheduling'),
 'dispatch reads active run not latest run': allin(svc,'detailed_schedule_execution_state','active_run_id','dispatchRowsSQL'),
 'dispatch uses setup match bonus': allin(svc,'SetupMatchBonus','setupMatch','dispatchBaseScore'),
 'dispatch exposes late start and late complete': allin(svc,'LATE_START','LATE_COMPLETE'),
 'dispatch exposes blocked state': allin(svc,'DispatchStatus','BLOCKED'),
 'adherence calculates start and completion variance': allin(svc,'StartVarianceMinutes','CompletionVarianceMinutes','varianceMinutes'),
 'adherence calculates on-time percentages': allin(svc,'OnTimeStartPct','OnTimeCompletionPct'),
 'adherence excludes untouched future operations from KPI denominator': allin(svc,'Future untouched operations are not yet adherence observations','r.ActualStart != nil || !r.StartOnTime','r.ActualEnd != nil || !r.CompletionOnTime'),
 'adherence snapshot is repeatable-read': allin(svc,'SnapshotAdherence','sql.LevelRepeatableRead','currentAdherenceWithReader'),
 'adherence canonical result hash exists': allin(svc,'canonicalAdherenceHash','sha256.Sum256'),
 'dynamic reschedule creates immutable candidate': allin(svc,'CandidateOnly: true','SimulateMRP: true','DetailedSchedule'),
 'dynamic reschedule schedules only future from asOf': allin(svc,'NotBefore: in.AsOf','maxTime(tasks[i].earliest, req.NotBefore)') or allin(svc,'NotBefore: in.AsOf'),
 'frozen horizon blocks activation': allin(svc,'frozen > 0','status = "BLOCKED"'),
 'executed commitment changes block activation': allin(svc,'ExecutionConflict','executed > 0','frozen > 0 || executed > 0'),
 'DB blocks activation with executed conflicts': allin(mig,'execution_conflicts','reschedule with frozen or executed commitment conflicts cannot activate'),
 'non-frozen changes activate candidate atomically': allin(svc,'status = "ACTIVATED"','activateExecutionScheduleTx'),
 'no-change candidate does not churn pointer': 'status := "NO_CHANGE"' in svc,
 'executed operations are classified and protected': allin(svc,'status == "IN_PROGRESS"','status == "PAUSED"','status == "COMPLETED"','x.TimeFence = "EXECUTED"','x.ExecutionConflict = ook && x.TimeFence == "EXECUTED"'),
 'new work alone is not frozen commitment conflict': 'x.FrozenConflict = ook && x.TimeFence == "FROZEN"' in svc,
 'reschedule canonical hash exists': allin(svc,'canonicalRescheduleHash','sha256.Sum256'),
 'reschedule failures are terminalized': allin(svc,'markRescheduleFailed',"status='FAILED'","WHERE id=$1 AND status='EVALUATING'",'return failTx(err)'),
 'automatic signal processor exists': allin(svc,'ProcessPendingSignals','AutoReschedule','MinAutoIntervalMinutes'),
 'automatic throttle uses nullable latest run time': allin(svc,'sql.NullTime','SELECT MAX(finished_at)'),
 'signal severity chooses breakdown first': allin(svc,'chooseSignalTrigger','"BREAKDOWN": 1'),
 'Detailed request supports candidate/no-earlier-than': allin(detail,'NotBefore','CandidateOnly','SimulateMRP','ActivationReason'),
 'Detailed scheduler applies not-before to tasks': allin(detail,'!req.NotBefore.IsZero()','maxTime(tasks[i].earliest, req.NotBefore)'),
 'Detailed scheduler can simulate MRP for reschedule': 'req.SimulateMRP' in detail,
 'normal Detailed run activates execution schedule': allin(detail,'!candidateOnly','activateExecutionScheduleTx','MANUAL_DETAILED_SCHEDULE'),
 'CTP remains side-effect free': allin(detail,'SimulateCTPOrder','does not','persistDetailedSchedule'),
 'Shop Floor owns autonomous rescheduler callback': allin(shop,'rescheduler *ScheduleExecutionService','notifyPending(ctx)'),
 'maintenance owns autonomous rescheduler callback': allin(maint,'rescheduler *ScheduleExecutionService','notifyPending(ctx)'),
 'capacity feedback activation owns autonomous callback': allin(perf,'rescheduler *ScheduleExecutionService','ActivateFeedback','notifyPending(ctx)'),
 'service container wires scheduler once': allin(services,'ScheduleExecution     *ScheduleExecutionService','scheduleExecution := &ScheduleExecutionService','maintenance.rescheduler = scheduleExecution','productionPerformance.rescheduler = scheduleExecution'),
 'Pegging planned supply prefers active execution run': allin(pegg,'detailed_schedule_execution_state','active_run_id','peggPlannedSupply'),
 'Pegging WO capacity prefers active execution run': allin(pegg,'ORDER BY (r.id=COALESCE((SELECT active_run_id FROM detailed_schedule_execution_state'),
 'Pegging traces dynamic reschedule node': allin(pegg,'addScheduleExecutionEvidence','"RESCHEDULE_RUN"','"RESCHEDULED_BY"'),
 'Pegging emits frozen horizon conflict': '"FROZEN_HORIZON_CONFLICT"' in pegg,
 'Pegging emits executed commitment conflict': '"EXECUTION_COMMITMENT_CONFLICT"' in pegg,
 'Pegging emits firm horizon change': '"FIRM_HORIZON_CHANGE"' in pegg,
 'Pegging emits adherence lateness': allin(pegg,'"SCHEDULE_START_LATE"','"SCHEDULE_COMPLETION_LATE"'),
 'Pegging emits dispatch blocked and reschedule required': allin(pegg,'"DISPATCH_BLOCKED"','"RESCHEDULE_REQUIRED"','blocked_reason'),
 'dispatch API handlers exist': allin(api,'currentDispatch','currentScheduleExecution','currentDispatchPolicy'),
 'schedule adherence APIs exist': allin(api,'currentScheduleAdherence','snapshotScheduleAdherence','listScheduleAdherenceSnapshots'),
 'dynamic reschedule APIs exist': allin(api,'runDynamicReschedule','processPendingReschedule','listDynamicRescheduleRuns'),
 'dispatch mutations are RBAC protected': allin(router,'PermDispatchManage','/dispatch-policy-versions','/schedule-adherence/snapshots'),
 'reschedule mutation is RBAC protected': allin(router,'PermDynamicReschedule','/dynamic-rescheduling/run','/dynamic-rescheduling/process-pending'),
 'planner dispatch permissions exist': allin(rbac,'PermDispatchManage','PermDynamicReschedule','domain.RolePlanner'),
 'OpenAPI dispatch paths exist': allin(openapi,'"/dispatch"','"/schedule-execution"','"/dispatch-policy-versions"'),
 'OpenAPI reschedule paths exist': allin(openapi,'"/dynamic-rescheduling/run"','"/schedule-adherence/current"'),
 'OpenAPI dispatch schemas exist': allin(openapi,'"DispatchBoard"','"DispatchPolicyVersion"','"DynamicRescheduleResult"'),
 'frontend Dispatch API exists': allin(front,'export const DispatchApi','/dynamic-rescheduling/run','/schedule-adherence/current'),
 'frontend dispatch page exists': allin(ui,'Real-Time Dispatch / Dynamic Rescheduling','Dispatch List','Schedule Adherence','Dynamic Reschedule History'),
 'frontend route and navigation exist': allin(routes,"path: '/dispatch-rescheduling'") and allin(app,"to: '/dispatch-rescheduling'"),
 '0039 operations document exists': allin(doc,'Active Execution Schedule','Schedule Adherence','FROZEN','RESCHEDULE_RUN','RESCHEDULED_BY'),
 'README documents 0039 closed-loop execution': allin(readme,'0039 Real-Time Dispatching + Dynamic Rescheduling + Schedule Adherence','REALTIME_DISPATCH_DYNAMIC_RESCHEDULING_SCHEDULE_ADHERENCE.md'),
 'unit tests cover time fences': allin(unit,'TestTimeFenceForExecutionAndFutureWindows','FROZEN','FIRM','FLEXIBLE','EXECUTED'),
 'unit tests cover adherence denominator': allin(unit,'TestAdherenceSummaryExcludesFutureUntouchedOperations','OnTimeStartPct'),
 'unit tests cover canonical reschedule hash': allin(unit,'TestCanonicalRescheduleHashIgnoresIDsAndInputOrder','canonicalRescheduleHash'),
 'E2E verifies dispatch board': allin(e2e,'/api/dispatch','activeRunId'),
 'E2E verifies adherence snapshot': allin(e2e,'/api/schedule-adherence/snapshots','onTimeStartPct'),
 'E2E verifies frozen/firm policy': allin(e2e,'freezeMinutes','firmMinutes','/dispatch-policy-versions'),
 'E2E verifies dynamic activation': allin(e2e,'/api/dynamic-rescheduling/run','ACTIVATED','candidateRunId'),
 'E2E verifies autonomous signal path': allin(e2e,'actorType','SYSTEM','/api/dynamic-rescheduling/runs'),
 'E2E cleans started operation before repeated full-suite run': allin(e2e,'test.afterEach','cleanupStartedOperationId','/complete','0039 E2E cleanup'),
 'E2E verifies Pegging reschedule evidence': allin(e2e,"nodeType === 'RESCHEDULE_RUN'","RESCHEDULED_BY"),
 'migration manager fingerprints 0039': allin(manager,'{39,','dispatch_policy_versions','dynamic_reschedule_runs','schedule_adherence_snapshots'),
 'migration manager tests expect 41': allin(manager_test,'len(migs) != 41','expected 41 migrations'),
 'migration guard expects 41 ordered migrations': allin(manager_guard,"'41 ordered SQL migrations exist'",'range(1,42)'),
 'CI runs 0039 guard': 'check_realtime_dispatch_rescheduling.py' in ci,
}
failed=[]
for name,ok in checks.items():
 print(('PASS' if ok else 'FAIL')+': '+name)
 if not ok: failed.append(name)
if failed:
 print(f'Real-Time Dispatch / Dynamic Rescheduling static guard failed: {len(failed)} check(s)')
 sys.exit(1)
print(f'Real-Time Dispatch / Dynamic Rescheduling static guard: {len(checks)} checks PASS')

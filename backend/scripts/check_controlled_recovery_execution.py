#!/usr/bin/env python3
"""0042 static guard: Controlled Recovery Plan Execution invariants."""
from pathlib import Path
import re, sys, json
root=Path(__file__).resolve().parents[2]

def read(p): return (root/p).read_text()
def has(p,*xs):
    s=read(p); return all(x in s for x in xs)
checks=[]
def check(name,ok,detail=''): checks.append((name,bool(ok),detail))

migs=sorted((root/'backend/migrations').glob('[0-9][0-9][0-9][0-9]_*.sql'))
vers=[int(p.name[:4]) for p in migs]
check('migration sequence is exactly 0001..0043', vers==list(range(1,44)), str(vers[-3:]))
check('0042 migration exists', (root/'backend/migrations/0042_controlled_recovery_plan_execution.sql').exists())
mig=read('backend/migrations/0042_controlled_recovery_plan_execution.sql')
for token in [
 'CREATE TABLE recovery_plan_executions','CREATE TABLE recovery_plan_execution_actions','CREATE TABLE recovery_capacity_overrides',
 "'PREPARED'","'PREFLIGHT_PASSED'","'PREFLIGHT_BLOCKED'","'APPROVED'","'EXECUTING'","'SUCCEEDED'","'FAILED'","'PARTIAL_FAILED'","'CANCELLED'",
 'recovery_execution_separation_check','approved_by_user_id <> prepared_by_user_id',
 'execution_result_hash','recovery_execution_terminal_result_hash_check',
 'validate_recovery_execution_actor','recovery execution approval/execution requires admin actor',
 'recovery_plan_executions_no_delete_trg','recovery_plan_execution_actions_no_delete_trg','recovery_capacity_overrides_immutable_trg',
 'recovery execution action snapshot is immutable','terminal recovery execution is immutable']:
    check('migration contains '+token, token in mig)

svc=read('backend/internal/service/recovery_execution.go')
for token in [
 'type RecoveryExecutionService struct','func (s *RecoveryExecutionService) Prepare','func (s *RecoveryExecutionService) Preflight',
 'func (s *RecoveryExecutionService) Approve','func (s *RecoveryExecutionService) Execute','func (s *RecoveryExecutionService) Cancel',
 'wildcard targetRef is simulation-only and cannot be executed','workOrderOperationRef','effectiveDate',
 'operational state drift detected','recordEventTx','releaseWorkOrderTx','PARTIAL_FAILED','SKIPPED',
 'RECOVERY_EXECUTION','loadRecoveryOvertime']:
    check('execution service contains '+token, token in svc)
check('execute is admin-gated in service', 'validateRecoveryExecutionAdmin(actor)' in svc)
check('preparer cannot approve', 'e.PreparedByUserID == actor.UserID' in svc)
check('action transaction uses serializable isolation', 'Isolation: sql.LevelSerializable' in svc)
check('terminal execute is idempotent', 'e.Status == "SUCCEEDED" || e.Status == "FAILED" || e.Status == "PARTIAL_FAILED"' in svc)
check('EXECUTING execution is resumable', 'e.Status != "APPROVED" && e.Status != "EXECUTING"' in svc and 'already EXECUTING' in svc)
check('ambiguous action commit is re-read before failure', 'persistentExecutionActionStatus' in svc and 'durable status could not be confirmed' in svc)
check('failure finalization detached from request cancellation', 'context.WithoutCancel(ctx)' in svc and '10*time.Second' in svc)
check('success finalization refuses non-succeeded actions', "execution_status<>\'SUCCEEDED\'" in svc)
check('release preflight freezes active routing identity', 'activeRoutingId' in svc and 'routing_operation_alternatives' in svc)
check('release preflight freezes BOM identity and economics', 'sourceBomComponentId' in svc and 'standardCost' in svc)
check('reschedule signal errors are not ignored', '_ = insertRecoveryRescheduleSignal' not in svc)
check('terminal execution result hash is canonicalized', 'recoveryExecutionResultHashTx' in svc and 'execution_result_hash=$' in svc)
check('internal action failures are sanitized', 'recoveryExecutionFailureReason' in svc and 'INTERNAL: controlled recovery action failed' in svc)

workflow=read('backend/internal/service/workflow.go')
supplier=read('backend/internal/service/supplier_scheduling.go')
check('WO release canonical tx helper exists', 'releaseWorkOrderTx' in workflow)
check('normal WO release still commits helper tx', 'result, err := s.releaseWorkOrderTx' in workflow and 'tx.Commit()' in workflow)
check('supplier scheduling canonical tx helper exists', 'recordEventTx' in supplier)
check('normal supplier scheduling still commits helper tx', 'row, err := s.recordEventTx' in supplier and 'tx.Commit()' in supplier)

rbac=read('backend/internal/api/rbac.go'); router=read('backend/internal/api/router.go')
for perm in ['PermRecoveryExecutionPrepare','PermRecoveryExecutionApprove','PermRecoveryExecutionExecute']:
    check('RBAC declares '+perm, perm in rbac)
check('planner receives prepare only', 'PermRecoveryExecutionPrepare: {}' in rbac and 'PermRecoveryExecutionApprove: {}' not in rbac and 'PermRecoveryExecutionExecute: {}' not in rbac)
for route in [
 '/recovery-scenarios/{id}/executions','/recovery-executions/{id}/preflight','/recovery-executions/{id}/approve','/recovery-executions/{id}/execute','/recovery-executions/{id}/cancel']:
    check('router contains '+route, route in router)
check('approve route requires approve permission', 'requirePermission(PermRecoveryExecutionApprove)).Post("/recovery-executions/{id}/approve"' in router)
check('execute route requires execute permission', 'requirePermission(PermRecoveryExecutionExecute)).Post("/recovery-executions/{id}/execute"' in router)

for f in ['backend/internal/service/crp_finite.go','backend/internal/service/detailed_scheduling.go']:
    s=read(f); check(f+' loads recovery overtime', 'loadRecoveryOvertime' in s); check(f+' consumes overtime map', 'overtime' in s and 'dayStateKey' in s)

models=read('backend/internal/domain/models.go')
for typ in ['RecoveryPlanExecution','RecoveryPlanExecutionAction','RecoveryCapacityOverride','RecoveryPlanExecutionDetail']:
    check('domain model '+typ, 'type '+typ+' struct' in models)

api=read('backend/internal/api/recovery_execution.go')
check('execution API hides raw internal errors', 'writeRecoveryExecutionError' in api and 'domain.NewInternal(err)' in api)
for fn in ['listRecoveryExecutions','getRecoveryExecution','prepareRecoveryExecution','preflightRecoveryExecution','approveRecoveryExecution','executeRecoveryExecution','cancelRecoveryExecution']:
    check('API handler '+fn, 'func (h *server) '+fn in api)

ui=read('frontend/src/views/RecoveryExecution.vue'); app=read('frontend/src/App.vue'); fr=read('frontend/src/router/index.ts')
check('Recovery Execution UI exists', 'Controlled Recovery Execution' in ui)
check('UI warns execution mutates operational state', 'Execution changes operational' in ui)
check('UI exposes preflight/approve/execute controls', all(x in ui for x in ['Preflight','Approve','Execute']))
check('frontend route exists', "'/recovery-execution'" in fr)
check('navigation link exists', "'/recovery-execution'" in app)

e2e=read('e2e/tests/recovery-execution.spec.ts')
check('E2E asserts parent execution result hash', 'executionResultHash).toHaveLength(64)' in e2e)
for token in ['0042 Controlled Recovery Plan Execution','PREFLIGHT_PASSED','plannerApprove.status()).toBe(403)','Independent admin approval','SUCCEEDED','simulation is still side-effect free','Publish remains evidence-only']:
    check('E2E contains '+token, token in e2e)

openapi=json.loads(read('backend/internal/api/openapi.json'))
check('OpenAPI version 0.43.0', openapi['info']['version']=='0.43.0')
for path in ['/recovery-executions','/recovery-executions/{id}','/recovery-scenarios/{id}/executions','/recovery-executions/{id}/preflight','/recovery-executions/{id}/approve','/recovery-executions/{id}/execute','/recovery-executions/{id}/cancel']:
    check('OpenAPI path '+path, path in openapi['paths'])

# 0041 must remain side-effect-free in its simulation service.
rp=read('backend/internal/service/recovery_planning.go')
check('0041 simulation service still separate from execution', 'RecoveryExecutionService' not in rp)
check('0041 migration remains present', (root/'backend/migrations/0041_scenario_based_recovery_planning_what_if_simulation.sql').exists())

failed=[x for x in checks if not x[1]]
for name,ok,detail in checks:
    print(('PASS' if ok else 'FAIL')+': '+name+((' -- '+detail) if detail and not ok else ''))
print(f'0042 Controlled Recovery Execution Guard: {len(checks)-len(failed)}/{len(checks)} PASS')
if failed: sys.exit(1)

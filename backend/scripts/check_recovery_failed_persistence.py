#!/usr/bin/env python3
"""Step 8 static guard: 0041 FAILED run persistence hardening invariants."""
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]

def read(rel):
    return (ROOT / rel).read_text(encoding='utf-8')

checks=[]
def check(name, ok):
    checks.append((name, bool(ok)))

svc=read('backend/internal/service/recovery_planning.go')
test=read('backend/internal/service/recovery_planning_test.go')
mig=read('backend/migrations/0041_scenario_based_recovery_planning_what_if_simulation.sql')
integration=read('scripts/test_recovery_failed_persistence.py')
ci=read('.github/workflows/ci.yml')
make=read('Makefile')
doc=read('docs/0041-scenario-based-recovery-planning.md')

migs=sorted((ROOT/'backend/migrations').glob('*.sql'))
check('migration count remains 41', len(migs)==41)
check('latest migration remains 0041', migs[-1].name.startswith('0041_'))
check('no migration 0042', not any(p.name.startswith('0042_') for p in migs))

# Existing 0041 schema is deliberately reused.
for token in ["'RUNNING'", "'SUCCEEDED'", "'FAILED'", 'failure_reason', 'completed_at']:
    check(f'0041 schema retains {token}', token in mig)
check('successful request uniqueness remains partial', 'recovery_scenario_runs_success_request_uidx' in mig and "WHERE status = 'SUCCEEDED'" in mig)
check('FAILED terminal rows may keep result_hash null', "status = 'FAILED'" in mig and 'completed_at IS NOT NULL' in mig)

# Tx A: RUNNING must be durable before engine execution.
run_insert=svc.find('INSERT INTO recovery_scenario_runs(')
tx_a_commit=svc.find('Tx A ends here', run_insert)
engine=svc.find('SimulateRecoveryScenario(', tx_a_commit)
check('Tx A RUNNING insert exists', run_insert >= 0)
check('Tx A commits before simulation engine', run_insert < tx_a_commit < engine)
check('failure audit arms only after Tx A commit', svc.find('failureAuditArmed = true', tx_a_commit) < engine)

# Tx B: evidence/success transaction and stale-definition defense.
tx_b=svc.find('Tx B persists all result evidence', engine)
case_insert=svc.find('INSERT INTO recovery_scenario_case_results(', tx_b)
summary_insert=svc.find('INSERT INTO recovery_scenario_summaries(', tx_b)
success_update=svc.find("status='SUCCEEDED'", tx_b)
check('Tx B begins after pure simulation', engine < tx_b)
check('Tx B persists case evidence', case_insert > tx_b)
check('Tx B persists summary evidence', summary_insert > tx_b)
check('Tx B terminalizes SUCCEEDED after evidence', summary_insert < success_update)
check('Tx B re-locks scenario', 'SELECT status\nFROM recovery_scenarios\nWHERE id=$1\nFOR UPDATE' in svc[tx_b:])
check('Tx B recomputes request hash', 'currentRequestHash, err := recoveryRequestHash(' in svc[tx_b:])
check('Tx B rejects action drift', 'recovery scenario actions changed while simulation was running' in svc)
check('Tx B verifies run still RUNNING', 'recovery scenario run is no longer RUNNING' in svc)

# Tx C: durable FAILED evidence independent from request cancellation.
check('Tx C helper exists', 'func (s *RecoveryPlanningService) markRecoveryRunFailed' in svc)
check('failure finalizer is deferred after RUNNING commit', 's.markRecoveryRunFailed(' in svc and 'failureAuditArmed' in svc)
check('Tx C detaches request cancellation', 'context.WithoutCancel(parent)' in svc)
check('Tx C has bounded timeout', '10*time.Second' in svc)
check('Tx C row-locks run', 'SELECT status\nFROM recovery_scenario_runs\nWHERE id=$1\nFOR UPDATE' in svc)
check('Tx C writes FAILED', "status='FAILED'" in svc)
check('Tx C clears result_hash', 'result_hash=NULL' in svc)
check('Tx C writes completed_at', 'completed_at=$2' in svc)
check('Tx C writes failure_reason', 'failure_reason=$3' in svc)
check('Tx C updates RUNNING only', "AND status='RUNNING'" in svc)
check('Tx C preserves already SUCCEEDED run', 'case "FAILED", "SUCCEEDED":' in svc)
check('failure audit error joins original error', 'errors.Join(' in svc and 'persist recovery simulation FAILED audit' in svc)
check('failure reason is bounded', 'recoveryFailureReasonMaxRunes = 2000' in svc)
check('failure reason helper unit test exists', 'TestRecoveryFailureReasonDefaultsAndBounds' in test)

# Product code must not contain a runtime test-only failpoint.
check('no product E2E failpoint environment', 'E2E_FORCE_RECOVERY' not in svc and 'RECOVERY_FAILPOINT' not in svc)

# Integration gate semantics.
check('integration test uses temporary DB trigger', 'e2e_force_recovery_summary_failure' in integration and 'CREATE TRIGGER' in integration)
check('integration forces Tx B summary failure', 'BEFORE INSERT ON recovery_scenario_summaries' in integration)
check('integration requires FAILED persisted', "failed[\"status\"] != \"FAILED\"" in integration)
check('integration checks completed_at', 'FAILED run completed_at was not persisted' in integration)
check('integration checks result_hash null', 'FAILED run result_hash must remain NULL' in integration)
check('integration checks failure_reason', 'FAILED run failure_reason was not persisted' in integration)
check('integration checks failed child evidence rollback', 'failed Tx B leaked evidence rows' in integration)
check('integration checks FAILED cannot publish', 'FAILED run was publishable' in integration)
check('integration checks failed retry success', 'retry did not succeed' in integration)
check('integration checks successful canonical reuse', 'successful identical retry was not reused' in integration)
check('integration checks operational state unchanged', 'Recovery Simulation changed operational state' in integration)
check('integration snapshots purchase orders', 'purchase_orders' in integration)
check('integration snapshots work orders', 'work_orders' in integration)
check('integration snapshots work centers', 'work_centers' in integration)
check('integration snapshots sales orders', 'sales_orders' in integration)
check('integration snapshots inventory transactions', 'inventory_txns' in integration)

check('CI runs Step 8 static guard', 'check_recovery_failed_persistence.py' in ci)
check('CI runs forced failure integration gate', 'test_recovery_failed_persistence.py' in ci)
check('Makefile Step 8 guard target', 'check-recovery-failure-audit' in make)
check('0041 documentation describes Tx A/B/C', all(x in doc for x in ['Tx A', 'Tx B', 'Tx C']))
check('0041 documentation states FAILED retryable', 'FAILED requests remain retryable' in doc)

failed=[name for name,ok in checks if not ok]
for name,ok in checks:
    print(('PASS' if ok else 'FAIL')+': '+name)
if failed:
    print(f'\nStep 8 Recovery FAILED Persistence Static Guard: {len(checks)-len(failed)}/{len(checks)} PASS')
    print('FAILED: '+', '.join(failed), file=sys.stderr)
    sys.exit(1)
print(f'\nStep 8 Recovery FAILED Persistence Static Guard: {len(checks)}/{len(checks)} PASS')

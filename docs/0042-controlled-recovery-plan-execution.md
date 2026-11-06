# 0042 Controlled Recovery Plan Execution

## 1. Purpose

Migration 0042 adds an explicit operational execution boundary after the side-effect-free Recovery Planning workflow introduced by 0041.

0041 remains unchanged in meaning:

```text
DRAFT Scenario
  -> Simulation (no operational mutation)
  -> SIMULATED
  -> Publish approved recovery evidence (no operational mutation)
  -> PUBLISHED
```

0042 begins only after publication:

```text
PUBLISHED Scenario + published SUCCEEDED Run
  -> Prepare Execution
  -> Preflight
  -> Admin Approval by a different user
  -> Execute
  -> immutable execution evidence
```

Publishing a scenario never executes it automatically.

## 2. Safety model

Controlled execution is deliberately fail-closed.

- Only a `PUBLISHED` scenario with publication evidence and a `SUCCEEDED` published run can be prepared.
- The publication hash and run result hash are snapshotted into the execution record.
- Scenario actions are copied into immutable execution-action snapshots.
- `targetRef="*"` is simulation-only and is always blocked by execution preflight.
- Every executable action must resolve to one concrete operational target.
- Preflight records a canonical operational-state hash for each action.
- Approval requires an active `admin` and the approver must differ from the preparer.
- Execution requires an active `admin`.
- Operational state is re-read and re-hashed immediately before each action executes.
- Operational mutation and the action's SUCCEEDED evidence are committed in the same database transaction.
- Evidence tables are append-only / immutable after terminal states.
- Terminal `SUCCEEDED` / `FAILED` / `PARTIAL_FAILED` executions carry a canonical `execution_result_hash` over the parent evidence and ordered action results.
- Unexpected internal errors are not exposed through the execution API; persisted failure reasons use sanitized application error codes/messages.

## 3. Execution lifecycle

`recovery_plan_executions.status`:

- `PREPARED`
- `PREFLIGHT_PASSED`
- `PREFLIGHT_BLOCKED`
- `APPROVED`
- `EXECUTING`
- `SUCCEEDED`
- `FAILED`
- `PARTIAL_FAILED`
- `CANCELLED`

Normal path:

```text
PREPARED
  -> PREFLIGHT_PASSED
  -> APPROVED
  -> EXECUTING
  -> SUCCEEDED
```

A blocked preflight can be re-run before approval. A prepared/preflight/approved execution may be cancelled by an administrator. Terminal states cannot be rewritten.

## 4. Segregation of duties

Permissions:

| Permission | Planner | Admin |
|---|---:|---:|
| `planning.recovery_execution.prepare` | yes | yes |
| `planning.recovery_execution.approve` | no | yes |
| `planning.recovery_execution.execute` | no | yes |

The database also enforces:

```text
approved_by_user_id <> prepared_by_user_id
```

Application RBAC is therefore not the only protection against self-approval.

## 5. Action semantics

### EXPEDITE_PO

Required:

- `targetType = PURCHASE_ORDER`
- concrete PO target (`PO:<po_no>` or UUID)
- `parameters.expediteDays` between 1 and 365

Execution appends a Supplier Scheduling `REVISE` event. It does not directly rewrite the Purchase Order due date. This preserves the existing supplier-scheduling evidence model and canonical planning-date provenance.

### RESCHEDULE_WO

Required:

- `targetType = WORK_ORDER`
- concrete WO target (`WO:<order_no>` or UUID)
- `parameters.recoveryDays` between 1 and 365

Only `PLANNED` or not-yet-started `RELEASED` work orders are eligible. Execution moves `start_date` and `due_date` earlier and emits an auditable reschedule signal.

### RELEASE_WO

Required:

- `targetType = WORK_ORDER`
- concrete `PLANNED` work order
- exactly one active routing
- sufficient component availability

Execution reuses the canonical Work Order release transaction. BOM snapshot, inventory reservations, status transition and operation creation remain atomic.

### ALTERNATE_WORK_CENTER

Required:

- concrete `parameters.workOrderOperationRef`
- concrete `parameters.alternateWorkCenterRef`
- operation status `PENDING` or `READY`
- alternate Work Center must exist in the Work Order operation's release-time snapshotted alternatives

The operation's work center is changed atomically and a reschedule signal is appended.

### ADD_OVERTIME_CAPACITY

Required:

- `targetType = WORK_CENTER`
- concrete Work Center target
- `parameters.effectiveDate` in `YYYY-MM-DD`
- positive `parameters.overtimeMinutes`

Execution creates an immutable, date-scoped `recovery_capacity_overrides` row. The Work Center master is not overwritten. Finite CRP and Detailed Scheduling include the effective-date overtime in available capacity.

## 6. Preflight and operational drift

Preflight is evidence, not a lock held until execution. Therefore execution must assume the plant may change after approval.

For each action:

1. resolve the concrete target;
2. read the operational fields that determine executability;
3. store `preflight_state` and `preflight_state_hash`;
4. immediately before execution, repeat the same read;
5. require the hash to match.

If the state changed, execution stops rather than applying an approval to different operational facts.

## 7. Atomicity and partial failure

Each action has its own serializable transaction:

```text
lock action
  -> re-preflight/hash check
  -> mark RUNNING inside transaction
  -> operational mutation
  -> write before/after/result evidence
  -> mark action SUCCEEDED
  -> COMMIT
```

If an action fails, that action transaction rolls back. The execution then records the failed action and marks later actions `SKIPPED`.

If earlier actions already committed, the execution becomes `PARTIAL_FAILED`.

**0042 does not automatically compensate or undo previously committed successful actions.** Generic rollback of Work Order release, reservations, supplier schedule revisions, or capacity decisions could be more dangerous than the original failure. A future compensation workflow must be explicit, action-specific, separately approved and audited.

## 8. Idempotency

Prepare requires `idempotencyKey` and enforces uniqueness per scenario.

Executing an already terminal execution does not reapply successful actions. A repeated Execute request returns the persisted terminal evidence.

An `EXECUTING` parent is resumable after process/request interruption. Durable `SUCCEEDED` actions are skipped, remaining `PENDING` actions re-preflight and continue, and ambiguous action commit outcomes are re-read from persisted action evidence before any failure is declared.

## 9. Database objects

Migration 0042 adds:

- `recovery_plan_executions`
- `recovery_plan_execution_actions`
- `recovery_capacity_overrides`
- lifecycle/actor validation triggers
- append-only/immutability triggers

It does not alter the 0041 Recovery Scenario/Run/Publication evidence tables.

## 10. API

```text
GET  /api/recovery-executions
GET  /api/recovery-executions/{id}
POST /api/recovery-scenarios/{id}/executions
POST /api/recovery-executions/{id}/preflight
POST /api/recovery-executions/{id}/approve
POST /api/recovery-executions/{id}/execute
POST /api/recovery-executions/{id}/cancel
```

## 11. UI

`/recovery-execution` provides a separate operational screen for:

- selecting a Published Scenario;
- preparing an execution;
- running preflight;
- reviewing READY/BLOCKED action evidence;
- admin approval;
- explicit execution;
- reviewing immutable before/after/result evidence.

The UI clearly separates 0041 Simulation/Publish from 0042 operational mutation.

## 12. Release gates

v0.42.0 requires:

- exactly migrations `0001` through `0042`;
- 0041 Recovery Planning guard still PASS;
- 0042 Controlled Recovery Execution guard PASS;
- all existing Production Hardening guards PASS;
- Backend race/vet/tests PASS in CI;
- Production topology PASS;
- Security/SBOM gate PASS;
- Playwright suite including 0042 execution flow PASS;
- Backup/Restore drill with migration latest `0042` PASS.

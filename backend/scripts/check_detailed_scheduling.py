#!/usr/bin/env python3
"""Static regression guard for Detailed Scheduling and lot-streaming execution rules."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
def text(rel): return (root / rel).read_text(encoding='utf-8')

svc = text('backend/internal/service/detailed_scheduling.go')
caprepo = text('backend/internal/repository/capacity.go')
workflow = text('backend/internal/service/workflow.go')
shop = text('backend/internal/service/shopfloor.go')
routes = text('backend/internal/api/router.go')
mig = text('backend/migrations/0030_detailed_scheduling.sql')
ui = text('frontend/src/views/DetailedScheduling.vue')
wcui = text('frontend/src/views/WorkCenters.vue')
rtui = text('frontend/src/views/Routings.vue')
mgr = text('backend/internal/migration/manager.go')

checks = {
    'alternative work centers modeled': 'routing_operation_alternatives' in mig and 'bestDetailedCandidate' in svc,
    'one work center is fixed per operation after first batch': 'var selectedAlt *detailedAlternative' in svc and 'forced *detailedAlternative' in svc,
    'transfer batches split operation lots': 'splitTransferBatches' in svc and 'routing_overlap_transfer_qty_chk' in mig,
    'routing overlap uses cumulative predecessor quantity': 'routingPredecessor' in svc and 'CumulativeQty' in svc,
    'sequence-dependent setup matrix': 'work_center_setup_matrix' in mig and 'sequenceSetupMinutes' in svc,
    'machine lanes enforce equipment count': 'machine_count' in mig and 'MachineLanes' in svc and 'detailed_schedule_machine_allocations' in mig,
    'worker capacity enforced': 'worker_count' in mig and 'laborReservation' in svc and 'exceeds worker capacity' in mig,
    'resource reduction is DB guarded': 'enforce_work_center_resource_capacity' in mig,
    'work center resource counts persist on update': 'machine_count=:machine_count' in caprepo and 'worker_count=:worker_count' in caprepo,
    'WO release snapshots active detailed routing master': 'wo_operation_alternatives' in workflow and 'overlap_enabled' in workflow and 'transfer_batch_qty' in workflow and 'r.is_active=true' in workflow,
    'Shop Floor transfer readiness': 'ensurePredecessorTransferReadyTx' in shop and 'wo_predecessor_transfer_ready' in mig,
    'downstream quantity cannot overtake predecessor': 'exceeds predecessor completed quantity' in mig and 'predecessorQty' in shop,
    'detailed snapshot is immutable': 'completed detailed schedule snapshot is immutable' in mig and 'detailed_schedule_runs' in mig,
    'historical capacity loads are snapshotted': 'detailed_schedule_loads' in mig and 'INSERT INTO detailed_schedule_loads' in svc,
    'DB guards machine overlap': 'overlapping use of the same machine lane' in mig and 'tstzrange' in mig,
    'DB guards transfer precedence': 'violates batch precedence / transfer dependency' in mig,
    'detailed run permission protected': 'Post("/detailed-scheduling/run", srv.runDetailedSchedule)' in routes and 'PermCRPRun' in routes,
    'Detailed Scheduling UI exists': 'Detailed Scheduling' in ui and 'Transfer Batch' in ui and 'Peak Workers' in ui,
    'work center UI exposes machines/workers/setup matrix': 'machineCount' in wcui and 'workerCount' in wcui and '段取りマトリクス' in wcui,
    'routing UI exposes overlap/transfer/resources/alternatives': 'overlapEnabled' in rtui and 'transferBatchQty' in rtui and 'machinesRequired' in rtui and '代替Work Center' in rtui,
    'migration manager fingerprints 0030': '{30,' in mgr,
}

failed=[]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
    if not ok: failed.append(name)
if failed:
    print('FAILED: ' + ', '.join(failed), file=sys.stderr)
    sys.exit(1)

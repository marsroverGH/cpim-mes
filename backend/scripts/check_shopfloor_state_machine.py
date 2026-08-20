#!/usr/bin/env python3
"""Static guard for the strict Shop Floor state-machine implementation."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
svc = (root / "backend/internal/service/shopfloor.go").read_text()
repo = (root / "backend/internal/repository/shopfloor.go").read_text()
api = (root / "backend/internal/api/actions_shopfloor_kpi.go").read_text()
wf = (root / "backend/internal/service/workflow.go").read_text()
mig = (root / "backend/migrations/0021_shop_floor_state_machine.sql").read_text()

checks = {
    "start transaction": "BeginTxx" in svc and "func (s *ShopFloorService) Start" in svc,
    "stop transaction": "func (s *ShopFloorService) Stop" in svc and "OperationStatusPaused" in svc,
    "complete transaction": "func (s *ShopFloorService) Complete" in svc and "OperationStatusReady" in svc,
    "predecessor/transfer check": ("ensurePredecessorTransferReadyTx" in svc and "wo_predecessor_transfer_ready" in (root / "backend/migrations/0030_detailed_scheduling.sql").read_text()),
    "actor comes from JWT": "authenticatedShopFloorActor" in api and 'json:"operator"' not in api,
    "repo has no write start": "func (r *ShopFloorRepo) Start" not in repo,
    "repo has no write stop": "func (r *ShopFloorRepo) Stop" not in repo,
    "release first op ready": "CASE WHEN x.rn=1 THEN 'READY' ELSE 'PENDING' END" in wf,
    "db transition trigger": "enforce_wo_operation_transition" in mig,
    "db deferred sequence guard": "DEFERRABLE INITIALLY DEFERRED" in mig and "assert_wo_operation_sequence" in mig,
    "overlap evolution is explicit": "wo_operations_one_active_step_uq" in mig and "DROP INDEX IF EXISTS wo_operations_one_active_step_uq" in (root / "backend/migrations/0030_detailed_scheduling.sql").read_text(),
    "server side session clock": "active_started_at" in mig and "AutoCalcMinutesFromStart" in svc,
    "log actor FK": "operation_logs_operator_user_id_fkey" in mig,
}
failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(f"{'PASS' if ok else 'FAIL'}: {name}")
if failed:
    print("FAILED:", ", ".join(failed), file=sys.stderr)
    sys.exit(1)

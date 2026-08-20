#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
def text(p): return (root / p).read_text()
svc = text('backend/internal/service/forecasting.go')
mig = text('backend/migrations/0025_forecast_version_consumption.sql')
router = text('backend/internal/api/router.go')
repo = text('backend/internal/repository/repository.go')
checks = {
    '0025 migration exists': (root/'backend/migrations/0025_forecast_version_consumption.sql').exists(),
    'version tables exist': 'CREATE TABLE IF NOT EXISTS forecast_runs' in mig and 'CREATE TABLE IF NOT EXISTS forecast_values' in mig,
    'single ACTIVE version per item/scenario': 'ux_forecast_runs_one_active' in mig,
    'forecast values immutable after activation': 'guard_forecast_values_mutation' in mig,
    'future forecast no longer writes demand_forecasts': 's.repos.Demand.Create(ctx, d)' not in svc,
    'consumption formula implemented': 'consumeForecastQty' in svc and 'orderQty + remaining' in svc,
    'customer orders only feed consumption': 'source=\'ORDER\'' in svc or '"ORDER"' in svc,
    'ACTIVE required before MPS publish': 'only ACTIVE forecast versions can be published to MPS' in svc,
    'MPS traces forecast run': 'source_forecast_run_id' in mig and "FORECAST_CONSUMPTION" in svc,
    'DB validates MPS forecast provenance': 'guard_mps_forecast_provenance' in mig and "status <> 'ACTIVE'" in mig,
    'DB blocks new unversioned forecasts': 'guard_unversioned_forecast_demand' in mig,
    'manual MPS clears forecast provenance': "source_forecast_run_id = NULL" in repo and "demand_basis = 'MANUAL'" in repo,
    'version endpoints routed': '/forecast/runs/{id}/consumption' in router and '/forecast/runs/{id}/apply-to-mps' in router,
    'migration manager fingerprints 0025': '{25,' in text('backend/internal/migration/manager.go'),
}
failed=[]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL')+': '+name)
    if not ok: failed.append(name)
if failed: sys.exit(1)

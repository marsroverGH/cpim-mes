#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]

def text(rel):
    return (root / rel).read_text()

abc = text('backend/internal/service/abc.go')
repo = text('backend/internal/repository/repository.go')
model = text('backend/internal/domain/models.go')
api = text('backend/internal/api/auth.go')
ui = text('frontend/src/views/AbcAnalysis.vue')
client = text('frontend/src/api/index.ts')
mig = text('backend/migrations/0028_abc_annual_dollar_usage.sql')
mgr = text('backend/internal/migration/manager.go')
cycle = text('frontend/src/views/CycleCount.vue')

checks = {
    'ABC ranks annual dollar usage': 'AnnualUsageValue' in abc and 'usageQty * it.StandardCost' in abc,
    'usage source is ISSUE only': "t.txn_type = 'ISSUE'" in repo,
    'adjustments excluded from usage query': "t.txn_type = 'ISSUE'" in repo and "txn_type IN" not in repo[repo.find('func (r *InventoryRepo) AnnualIssueUsage'):repo.find('func (r *InventoryRepo) AnnualIssueUsage')+1200],
    'rolling 12-month business-timezone bounds': "INTERVAL '1 year'" in repo and 'eco_business_timezone()' in repo,
    'zero history defaults C': 'totalUsageValue <= 0' in abc and 'ABCClass = "C"' in abc,
    'current inventory value is reference only': 'OnHandValue' in model and 'AnnualUsageValue' in model,
    'historical asOf supported': 'RunAsOf' in abc and 'asOf must be YYYY-MM-DD' in api,
    'cycle count uses ABC service': 's.abc.Run(ctx)' in text('backend/internal/service/forecasting.go'),
    'ABC UI uses annual usage amount': '年間使用金額ベース' in ui and 'annualUsageValue' in ui,
    'cycle count UI identifies annual usage ABC': '年間使用金額ベース' in cycle,
    'frontend API exposes annual usage fields': 'annualUsageQty' in client and 'annualUsageValue' in client,
    '0028 partial ISSUE index exists': 'idx_inventory_txns_abc_issue_period' in mig and "WHERE txn_type = 'ISSUE'" in mig,
    'migration manager fingerprints 0028': '{28,' in mgr,
}
failed = [k for k,v in checks.items() if not v]
for k,v in checks.items():
    print(('PASS' if v else 'FAIL') + ': ' + k)
if failed:
    sys.exit(1)

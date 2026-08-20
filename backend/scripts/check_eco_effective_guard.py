#!/usr/bin/env python3
from pathlib import Path
import sys

checks = []
def need(path, needle, label):
    text = Path(path).read_text()
    ok = needle in text
    checks.append((ok, label))

def absent(path, needle, label):
    text = Path(path).read_text()
    ok = needle not in text
    checks.append((ok, label))

need('backend/internal/service/eco.go', 'e.Status = "DRAFT"', 'Create forces DRAFT')
need('backend/internal/service/eco.go', 'ecoBusinessDate(ctx, tx)', 'Apply reads DB business date')
need('backend/internal/service/eco.go', '!ecoEffectiveOn(eco.EffectiveDate, businessDate)', 'Backend blocks pre-effective Apply')
need('backend/internal/service/eco.go', 'ECO components are immutable after approval', 'Service freezes components')
need('backend/internal/api/sop_eco_agent.go', 'authenticatedECOActor', 'ECO actor derives from JWT')
absent('backend/internal/api/sop_eco_agent.go', 'Approver string', 'No client-supplied approver field')
need('backend/migrations/0023_eco_effective_date_audit.sql', 'eco_business_date(NEW.applied_at) < NEW.effective_date', 'DB blocks pre-effective Apply')
need('backend/migrations/0023_eco_effective_date_audit.sql', 'assert_current_eco_admin', 'DB validates transition actor')
need('backend/migrations/0023_eco_effective_date_audit.sql', 'eco_status_history is append-only', 'History is immutable')
need('backend/migrations/0023_eco_effective_date_audit.sql', 'eco_component_draft_only_trg', 'DB freezes approved component rows')
need('backend/internal/api/router.go', 'Get("/eco/{id}/history"', 'History API is routed')

bad = [label for ok,label in checks if not ok]
for ok,label in checks:
    print(('PASS' if ok else 'FAIL') + ': ' + label)
if bad:
    sys.exit(1)
print(f'PASS: {len(checks)} ECO effective-date/audit checks')

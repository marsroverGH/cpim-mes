#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]

def text(path):
    p = root / path
    return p.read_text(encoding='utf-8') if p.exists() else ''

checks = {
    'quality service owns DB transaction': 'BeginTxx' in text('backend/internal/service/quality.go'),
    'lot locked before inspection': 'FOR UPDATE' in text('backend/internal/service/quality.go'),
    'JWT-derived actor accepted by service': 'QualityActor' in text('backend/internal/service/quality.go') and 'inspector_user_id' in text('backend/internal/service/quality.go'),
    'legacy split repository writes removed': 'SetLotQualityStatus' not in text('backend/internal/repository/quality.go') and 'func (r *QualityRepo) Create' not in text('backend/internal/repository/quality.go'),
    'API does not accept inspector identity': 'Inspector string' not in text('backend/internal/api/wip_atp_quality.go') and 'ctxKeyClaims' in text('backend/internal/api/wip_atp_quality.go'),
    'quality history endpoint exists': '/lots/{id}/quality-history' in text('backend/internal/api/router.go'),
    '0024 migration exists': (root / 'backend/migrations/0024_quality_transaction_audit.sql').exists(),
    'DB inspection trigger transitions lot': 'quality_inspection_after_insert' in text('backend/migrations/0024_quality_transaction_audit.sql') and 'UPDATE lots' in text('backend/migrations/0024_quality_transaction_audit.sql'),
    'immutable status history exists': 'quality_status_history' in text('backend/migrations/0024_quality_transaction_audit.sql') and 'quality_status_history_append_only_trg' in text('backend/migrations/0024_quality_transaction_audit.sql'),
    'direct lot quality updates blocked': 'lots_quality_status_guard_trg' in text('backend/migrations/0024_quality_transaction_audit.sql'),
    'DB validates inspector identity': "v_role NOT IN ('operator','planner','admin')" in text('backend/migrations/0024_quality_transaction_audit.sql'),
    'migration manager fingerprints 0024': '{24,' in text('backend/internal/migration/manager.go'),
}

failed = []
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
    if not ok:
        failed.append(name)
if failed:
    sys.exit(1)

#!/usr/bin/env python3
from pathlib import Path
import re, sys

root = Path(__file__).resolve().parents[2]
main = (root/'backend/cmd/server/main.go').read_text()
mgr = (root/'backend/internal/migration/manager.go').read_text()
compose = (root/'docker-compose.yml').read_text()
embed = (root/'backend/migrations/embed.go').read_text()
files = sorted((root/'backend/migrations').glob('[0-9][0-9][0-9][0-9]_*.sql'))

checks = {
    '34 ordered SQL migrations exist': len(files) == 34 and [p.name[:4] for p in files] == [f'{i:04d}' for i in range(1,35)],
    'migrations embedded in backend binary': '//go:embed *.sql' in embed,
    'startup runs migrator before repository construction': main.find('.Migrate(') >= 0 and main.find('.Migrate(') < main.find('repository.NewRepositories'),
    'startup failure is fatal': 'database migration failed; backend will not start' in main,
    'global advisory lock exists': 'pg_advisory_lock' in mgr and 'schema-migrations' in mgr,
    'schema_migrations ledger exists': 'CREATE TABLE IF NOT EXISTS schema_migrations' in mgr,
    'checksum verification exists': 'checksum mismatch' in mgr and 'sha256.Sum256' in mgr,
    'unknown newer DB version rejected': 'newer/unknown to this application' in mgr,
    'history gap rejected': 'schema_migrations has a gap' in mgr,
    'legacy auto-baseline exists': 'bootstrapLegacyDatabase' in mgr and 'legacy-auto-baseline' in mgr,
    'migration SQL and history are one transaction': 'BeginTxx' in mgr and 'record migration' in mgr and 'tx.Commit()' in mgr,
    'old initdb mount removed': '/docker-entrypoint-initdb.d' not in compose,
}
failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
if failed:
    sys.exit(1)

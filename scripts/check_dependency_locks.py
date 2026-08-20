#!/usr/bin/env python3
from __future__ import annotations
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
errors: list[str] = []

def fail(msg: str) -> None:
    errors.append(msg)

# Go lock
mod = ROOT / 'backend/go.mod'
sumf = ROOT / 'backend/go.sum'
if not mod.exists():
    fail('backend/go.mod is missing')
if not sumf.exists() or not sumf.read_text(errors='ignore').strip():
    fail('backend/go.sum is missing or empty; run ./scripts/generate-lockfiles.sh')
else:
    sums = sumf.read_text(errors='ignore')
    reqs = []
    in_req = False
    for line in mod.read_text().splitlines():
        s=line.strip()
        if s == 'require (': in_req=True; continue
        if in_req and s == ')': in_req=False; continue
        if in_req and s and not s.startswith('//'):
            parts=s.split()
            if len(parts)>=2:
                reqs.append((parts[0],parts[1]))
    for module, ver in reqs:
        if f'{module} {ver} ' not in sums and f'{module} {ver}/go.mod ' not in sums:
            fail(f'go.sum has no checksum entry for direct requirement {module} {ver}')

# npm locks
for folder in ('frontend','e2e'):
    pkgp=ROOT/folder/'package.json'
    lockp=ROOT/folder/'package-lock.json'
    if not lockp.exists():
        fail(f'{folder}/package-lock.json is missing; run ./scripts/generate-lockfiles.sh')
        continue
    try:
        pkg=json.loads(pkgp.read_text())
        lock=json.loads(lockp.read_text())
    except Exception as e:
        fail(f'{folder} lock/package JSON parse error: {e}')
        continue
    if int(lock.get('lockfileVersion',0)) < 2:
        fail(f'{folder}/package-lock.json must use lockfileVersion >= 2')
    rootpkg=(lock.get('packages') or {}).get('',{})
    for group in ('dependencies','devDependencies'):
        want=pkg.get(group,{})
        got=rootpkg.get(group,{})
        for name, ver in want.items():
            if got.get(name) != ver:
                fail(f'{folder}/package-lock.json root {group}.{name}={got.get(name)!r}, expected {ver!r}')
    # no floating direct versions
    for group in ('dependencies','devDependencies'):
        for name, ver in pkg.get(group,{}).items():
            if isinstance(ver,str) and re.match(r'^[~^*]|^(latest|next)$',ver):
                fail(f'{folder}/package.json direct dependency {name} is not pinned: {ver}')

if errors:
    print('Dependency lock check: FAIL', file=sys.stderr)
    for e in errors:
        print(f' - {e}', file=sys.stderr)
    raise SystemExit(1)
print('Dependency lock check: PASS')

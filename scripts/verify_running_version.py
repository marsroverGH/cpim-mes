#!/usr/bin/env python3
from __future__ import annotations
import argparse, json, re, urllib.request

def fail(msg: str) -> None:
    raise SystemExit(f"Running version verification: FAIL\n - {msg}")

p = argparse.ArgumentParser()
p.add_argument("--url", required=True)
p.add_argument("--version", required=True)
p.add_argument("--commit", required=True)
p.add_argument("--migration-latest", type=int, required=True)
a = p.parse_args()
try:
    req = urllib.request.Request(a.url, headers={"Cache-Control": "no-cache"})
    with urllib.request.urlopen(req, timeout=10) as r:
        data = json.load(r)
        cc = r.headers.get("Cache-Control", "")
except Exception as exc:
    fail(str(exc))
if data.get("version") != a.version:
    fail(f"version mismatch: {data.get('version')!r}")
if a.commit != "local" and data.get("commit") != a.commit:
    fail(f"commit mismatch: {data.get('commit')!r}")
if int(data.get("migrationLatest", -1)) != a.migration_latest:
    fail(f"migrationLatest mismatch: {data.get('migrationLatest')!r}")
bt = data.get("buildTime")
if not isinstance(bt, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", bt):
    fail(f"invalid buildTime: {bt!r}")
if "no-store" not in cc.lower():
    fail("Cache-Control must contain no-store")
print("Running version verification: PASS")

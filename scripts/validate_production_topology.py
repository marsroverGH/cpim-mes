#!/usr/bin/env python3
from __future__ import annotations
import json, sys

def fail(msg: str) -> None:
    print(f"Production topology validation: FAIL\n - {msg}", file=sys.stderr)
    raise SystemExit(1)

try:
    data = json.load(sys.stdin)
except Exception as exc:
    fail(f"invalid docker compose config JSON: {exc}")
services = data.get("services") or {}
for required in ("db", "backend", "frontend"):
    if required not in services:
        fail(f"missing service {required}")

def ports(name: str):
    return services[name].get("ports") or []
if ports("db"):
    fail("database service must not publish host ports")
if ports("backend"):
    fail("backend service must not publish host ports")
front = ports("frontend")
if len(front) != 1:
    fail(f"frontend must publish exactly one port, got {front!r}")
port = front[0]
if isinstance(port, dict):
    target = int(port.get("target", 0) or 0)
else:
    target = 0
if target != 8080:
    fail(f"frontend published target must be 8080, got {port!r}")
print("Production topology validation: PASS (DB/backend private; frontend only public)")

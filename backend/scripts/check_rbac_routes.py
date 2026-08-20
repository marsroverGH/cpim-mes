#!/usr/bin/env python3
"""Static guard: every authenticated non-GET route must declare requirePermission()."""
from pathlib import Path
import re
import sys

router = Path(__file__).resolve().parents[1] / "internal" / "api" / "router.go"
text = router.read_text(encoding="utf-8")

raw = []
for n, line in enumerate(text.splitlines(), 1):
    if re.search(r"\bsecured\.(Post|Put|Delete|Patch)\(", line):
        raw.append((n, line.strip()))
if raw:
    print("FAIL: secured mutation route(s) without requirePermission:")
    for n, line in raw:
        print(f"  {n}: {line}")
    sys.exit(1)

protected = re.findall(
    r'secured\.With\(requirePermission\((Perm[A-Za-z0-9_]+)\)\)\.(Post|Put|Delete|Patch)\("([^"]+)"',
    text,
)
if not protected:
    print("FAIL: no protected mutation routes found")
    sys.exit(1)

expected = {
    "/items/{id}/bom": "PermBOMWrite",
    "/inventory/transactions": "PermInventoryAdjust",
    "/work-orders": "PermWOPlan",
    "/work-orders/{id}/release": "PermWOPlan",
    "/work-orders/{id}/complete": "PermWOExecute",
    "/purchase-orders": "PermPOPlan",
    "/purchase-orders/{id}/receive": "PermPOReceive",
    "/eco": "PermECODraft",
    "/eco/{id}/approve": "PermECOApproveApply",
    "/eco/{id}/apply": "PermECOApproveApply",
    "/eco/{id}/cancel": "PermECOApproveApply",
}
actual = {path: perm for perm, method, path in protected}
errors = []
for path, perm in expected.items():
    if actual.get(path) != perm:
        errors.append(f"{path}: expected {perm}, got {actual.get(path)}")
if errors:
    print("FAIL: critical RBAC route mismatch")
    for e in errors:
        print("  " + e)
    sys.exit(1)

print(f"PASS: {len(protected)} authenticated mutation routes are permission-protected")
print("PASS: critical BOM / Inventory / WO / PO / ECO route permissions match policy")

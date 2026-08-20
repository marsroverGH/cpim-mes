#!/usr/bin/env python3
"""Static guardrails for transactional BOM cycle/LLC integrity."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
checks = []

def require(path, needles):
    text = (root / path).read_text()
    for n in needles:
        if n not in text:
            checks.append(f"MISSING {path}: {n}")
    return text

bom = require("backend/internal/service/bom.go", [
    "BeginTxx",
    "pg_advisory_xact_lock",
    "validateBOMAcyclicTx(ctx, tx)",
    "recomputeLLCTx(ctx, tx)",
    "tx.Commit()",
])
eco = require("backend/internal/service/eco.go", [
    "lockBOMTopologyTx(ctx, tx)",
    "validateBOMAcyclicTx(ctx, tx)",
    "recomputeLLCTx(ctx, tx)",
    "status='APPLIED'",
    "tx.Commit()",
])
router = (root / "backend/internal/api/router.go").read_text()
migration = require("backend/migrations/0019_bom_cycle_guard.sql", [
    "CREATE OR REPLACE FUNCTION assert_bom_acyclic()",
    "CREATE CONSTRAINT TRIGGER bom_cycle_guard",
    "DEFERRABLE INITIALLY DEFERRED",
    "CREATE OR REPLACE FUNCTION recompute_low_level_codes()",
    "item_count int",
])


# Raw BOM topology DML is intentionally confined to the two transactional
# service implementations.  Any new write location must be reviewed.
allowed_dml = {
    Path("backend/internal/service/bom.go"),
    Path("backend/internal/service/eco.go"),
}
for path in (root / "backend/internal").rglob("*.go"):
    rel = path.relative_to(root)
    text = path.read_text()
    if any(token in text for token in ("INSERT INTO bom_components", "DELETE FROM bom_components", "UPDATE bom_components")):
        if rel not in allowed_dml:
            checks.append(f"raw BOM DML outside transactional services: {rel}")

# Legacy error swallowing must never return.
for path in (root / "backend/internal").rglob("*.go"):
    text = path.read_text()
    if "_ = h.s.Items.RecomputeLLC" in text or "_ = s.items.RecomputeLLC" in text:
        checks.append(f"ignored RecomputeLLC error remains: {path.relative_to(root)}")

# The HTTP handlers must delegate atomicity to BOMService and must not perform
# a second, out-of-transaction LLC recompute after add/delete.
for fn in ("addBOMComponent", "deleteBOMComponent"):
    idx = router.find(f"func (h *server) {fn}")
    if idx < 0:
        checks.append(f"missing handler {fn}")
        continue
    end = router.find("\nfunc ", idx + 1)
    body = router[idx:] if end < 0 else router[idx:end]
    if "RecomputeLLC" in body:
        checks.append(f"{fn} still recomputes LLC outside BOM transaction")

# Order checks: validation and LLC must precede commit.
for name, text in (("BOM", bom), ("ECO", eco)):
    v = text.find("validateBOMAcyclicTx(ctx, tx)")
    l = text.find("recomputeLLCTx(ctx, tx)", v)
    c = text.find("tx.Commit()", l)
    if not (0 <= v < l < c):
        checks.append(f"{name}: expected cycle validation -> LLC -> commit order")

if checks:
    print("BOM integrity static checks FAILED")
    for x in checks:
        print(" -", x)
    sys.exit(1)

print("BOM integrity static checks PASS")

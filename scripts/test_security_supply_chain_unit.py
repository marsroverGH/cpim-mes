#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def write(path: Path, text: str) -> None:
    path.write_text(text, encoding="utf-8")


def fake_inputs(base: Path) -> tuple[Path, Path]:
    dep = base / "dep"
    con = base / "con"
    dep.mkdir()
    con.mkdir()
    gv = '\n'.join([json.dumps({"config": {"protocol_version": "v1.0.0"}}), json.dumps({"progress": {"message": "done"}})]) + '\n'
    write(dep / "govulncheck.json", gv)
    write(dep / "govulncheck.json.stderr", "")
    write(dep / "govulncheck.json.exitcode", "0\n")
    write(dep / "govulncheck-gate.json", json.dumps({"schemaVersion": 1, "messageCount": 2, "reachableFindingCount": 0, "reachableOSVs": [], "reachableFindings": []}) + "\n")
    write(dep / "govulncheck-gate.json.stdout", "PASS\n")
    write(dep / "govulncheck-gate.json.stderr", "")
    write(dep / "govulncheck-gate.json.exitcode", "0\n")
    for name in ("npm-audit-frontend-production.json", "npm-audit-frontend-all.json", "npm-audit-e2e.json"):
        write(dep / name, json.dumps({"metadata": {"vulnerabilities": {"info": 0, "low": 0, "moderate": 0, "high": 0, "critical": 0}}}) + "\n")
        write(dep / f"{name}.stderr", "")
        write(dep / f"{name}.exitcode", "0\n")
    write(dep / "dependency-security-policy.txt", "policy\n")

    sbom = {"spdxVersion": "SPDX-2.3", "SPDXID": "SPDXRef-DOCUMENT", "creationInfo": {}, "packages": [{"name": "fake", "SPDXID": "SPDXRef-Package-fake"}]}
    for name in ("SBOM.spdx.json", "SBOM-backend-image.spdx.json", "SBOM-frontend-image.spdx.json"):
        write(con / name, json.dumps(sbom) + "\n")
    trivy = {"SchemaVersion": 2, "Results": []}
    for name in ("trivy-backend.json", "trivy-frontend.json"):
        write(con / name, json.dumps(trivy) + "\n")
        write(con / f"{name}.exitcode", "0\n")
    write(con / "container-security-policy.txt", "policy\n")
    return dep, con


def run(*args: str, expect: int = 0) -> subprocess.CompletedProcess[str]:
    cp = subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if cp.returncode != expect:
        raise AssertionError(f"command returned {cp.returncode}, expected {expect}: {' '.join(args)}\n{cp.stdout}")
    return cp



def test_govulncheck_reachability_gate(base: Path) -> None:
    clean = base / "gov-clean.json"
    clean.write_text(
        json.dumps({"finding": {"osv": "GO-TEST-IMPORT", "trace": [{"module": "example.com/m", "package": "example.com/m/p"}]}}) + "\n",
        encoding="utf-8",
    )
    summary = base / "gov-clean-summary.json"
    run("python3", "scripts/evaluate_govulncheck.py", str(clean), "--summary", str(summary))
    data = json.loads(summary.read_text(encoding="utf-8"))
    if data["reachableFindingCount"] != 0:
        raise AssertionError("package/module-only finding must not be classified as reachable")

    reachable = base / "gov-reachable.json"
    reachable.write_text(
        json.dumps({"finding": {"osv": "GO-TEST-REACHABLE", "trace": [{"module": "example.com/m", "package": "example.com/m/p", "function": "Vulnerable"}]}}) + "\n",
        encoding="utf-8",
    )
    cp = subprocess.run(
        ["python3", "scripts/evaluate_govulncheck.py", str(reachable), "--summary", str(base / "gov-reachable-summary.json")],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if cp.returncode != 1 or "BLOCK:" not in cp.stdout:
        raise AssertionError(f"reachable govulncheck finding did not block release:\n{cp.stdout}")

def main() -> int:
    with tempfile.TemporaryDirectory() as td:
        base = Path(td)
        dep, con = fake_inputs(base)
        test_govulncheck_reachability_gate(base)
        bundle = base / "bundle"
        run(
            "python3", "scripts/build_release_bundle.py",
            "--dependency-reports", str(dep),
            "--container-artifacts", str(con),
            "--output", str(bundle),
            "--commit", "a" * 40,
            "--build-time", "2026-11-05T12:34:56Z",
        )
        run("python3", "scripts/verify_release_bundle.py", str(bundle))

        # Any payload tampering must be caught by bundle checksums.
        p = bundle / "SBOM.spdx.json"
        p.write_text(p.read_text(encoding="utf-8") + " ", encoding="utf-8")
        cp = subprocess.run(["python3", "scripts/verify_release_bundle.py", str(bundle)], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if cp.returncode == 0 or "checksum mismatch" not in cp.stdout:
            raise AssertionError(f"tamper detection failed:\n{cp.stdout}")

    env = (ROOT / "scripts/security_tool_versions.env").read_text(encoding="utf-8")
    if "latest" in env.lower():
        raise AssertionError("security tool pins must not contain latest")
    for key in ("GOVULNCHECK_VERSION", "TRIVY_VERSION", "SYFT_VERSION", "TRIVY_CHECKSUM_FILE_SHA256", "SYFT_CHECKSUM_FILE_SHA256"):
        if f"{key}=" not in env:
            raise AssertionError(f"missing pin: {key}")

    print("Security supply-chain unit tests: PASS (govulncheck reachability + bundle verify + tamper detection + tool pins)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

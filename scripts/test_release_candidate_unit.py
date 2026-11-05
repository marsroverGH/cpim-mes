#!/usr/bin/env python3
from __future__ import annotations

import json
import subprocess
import tarfile
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def write(path: Path, text: str) -> None:
    path.write_text(text, encoding="utf-8")


def run(*args: str, expect: int = 0) -> subprocess.CompletedProcess[str]:
    cp = subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if cp.returncode != expect:
        raise AssertionError(f"command returned {cp.returncode}, expected {expect}: {' '.join(args)}\n{cp.stdout}")
    return cp


def fake_security_inputs(base: Path) -> tuple[Path, Path]:
    dep, con = base / "dep", base / "con"
    dep.mkdir(); con.mkdir()
    write(dep / "govulncheck.json", json.dumps({"config": {"protocol_version": "v1.0.0"}}) + "\n")
    write(dep / "govulncheck.json.stderr", "")
    write(dep / "govulncheck.json.exitcode", "0\n")
    write(dep / "govulncheck-gate.json", json.dumps({"schemaVersion": 1, "messageCount": 1, "reachableFindingCount": 0, "reachableOSVs": [], "reachableFindings": []}) + "\n")
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
    for name in ("trivy-backend.json", "trivy-frontend.json"):
        write(con / name, json.dumps({"SchemaVersion": 2, "Results": []}) + "\n")
        write(con / f"{name}.exitcode", "0\n")
    write(con / "container-security-policy.txt", "policy\n")
    return dep, con



def test_topology_validator() -> None:
    good = {"services": {"db": {}, "backend": {}, "frontend": {"ports": [{"target": 8080, "published": "8080"}]}}}
    cp = subprocess.run(["python3", "scripts/validate_production_topology.py"], cwd=ROOT, input=json.dumps(good), text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if cp.returncode != 0:
        raise AssertionError(f"valid production topology rejected:\n{cp.stdout}")
    bad = {"services": {"db": {"ports": [{"target": 5432, "published": "5432"}]}, "backend": {}, "frontend": {"ports": [{"target": 8080, "published": "8080"}]}}}
    cp = subprocess.run(["python3", "scripts/validate_production_topology.py"], cwd=ROOT, input=json.dumps(bad), text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if cp.returncode == 0 or "database service must not publish" not in cp.stdout:
        raise AssertionError(f"unsafe production topology was not rejected:\n{cp.stdout}")


def main() -> int:
    test_topology_validator()
    with tempfile.TemporaryDirectory() as td:
        base = Path(td)
        dep, con = fake_security_inputs(base)
        sec = base / "security"
        run("python3", "scripts/build_release_bundle.py", "--dependency-reports", str(dep), "--container-artifacts", str(con), "--output", str(sec), "--commit", "a" * 40, "--build-time", "2026-11-06T00:00:00Z")
        run("python3", "scripts/verify_release_bundle.py", str(sec))

        result = base / "FINAL-CLEAN-ROOM-RESULT.json"
        result.write_text(json.dumps({
            "schemaVersion": 1,
            "candidateVersion": "v0.41.1-rc.4",
            "targetRelease": "v0.41.1",
            "gitCommit": "a" * 40,
            "buildTime": "2026-11-06T00:00:00Z",
            "status": "PASS",
            "migrationCount": 41,
            "migrationLatest": 41,
            "migration0042Created": False,
            "playwrightTests": 25,
            "productionTopology": {"databaseHostPublished": False, "backendHostPublished": False, "frontendOnlyPublic": True},
            "gates": {"syntheticUnitFixture": "PASS"},
        }, indent=2) + "\n", encoding="utf-8")

        rc = base / "rc"
        run("python3", "scripts/build_release_candidate_bundle.py", "--clean-room-result", str(result), "--security-bundle", str(sec), "--output", str(rc), "--commit", "a" * 40, "--build-time", "2026-11-06T00:00:00Z", "--candidate", "v0.41.1-rc.4")
        run("python3", "scripts/verify_release_candidate_bundle.py", str(rc))

        source = rc / "cpim-mes-v0.41.1-rc.4-source.tar.gz"
        with tarfile.open(source, "r:gz") as tf:
            member = tf.getmember("cpim-mes-v0.41.1-rc.4/scripts/final_clean_room_release_gate.sh")
            if member.mode & 0o111 == 0:
                raise AssertionError("source archive lost executable mode on final clean-room script")

        # Tamper with the nested source archive; outer SHA must reject it.
        with source.open("ab") as f:
            f.write(b"tamper")
        cp = subprocess.run(["python3", "scripts/verify_release_candidate_bundle.py", str(rc)], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if cp.returncode == 0 or "checksum mismatch" not in cp.stdout:
            raise AssertionError(f"RC tamper detection failed:\n{cp.stdout}")

    print("Release Candidate unit tests: PASS (topology fail-closed + nested evidence + source mode/checksum + tamper detection)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

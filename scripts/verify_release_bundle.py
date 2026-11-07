#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path

EXPECTED_RELEASE = "v0.43.0"
EXPECTED_MIGRATION_LATEST = 43
REQUIRED_SBOMS = {
    "SBOM.spdx.json",
    "SBOM-backend-image.spdx.json",
    "SBOM-frontend-image.spdx.json",
}
REQUIRED_TRIVY = {"trivy-backend.json", "trivy-frontend.json"}
REQUIRED_NPM = {
    "npm-audit-frontend-production.json": ("high", "critical"),
    "npm-audit-frontend-all.json": ("critical",),
    "npm-audit-e2e.json": ("critical",),
}


def fail(msg: str) -> None:
    raise SystemExit(f"Release bundle verification: FAIL\n - {msg}")


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def regular_files(root: Path) -> set[str]:
    names: set[str] = set()
    for p in root.iterdir():
        if p.is_symlink():
            fail(f"symlink is forbidden in release bundle: {p.name}")
        if p.is_dir():
            fail(f"nested directory is forbidden in release bundle: {p.name}")
        if p.is_file():
            names.add(p.name)
    return names


def verify_checksums(root: Path, files: set[str]) -> None:
    checksum_name = "RELEASE-BUNDLE-SHA256SUMS.txt"
    if checksum_name not in files:
        fail(f"missing {checksum_name}")
    entries: dict[str, str] = {}
    for raw in (root / checksum_name).read_text(encoding="utf-8").splitlines():
        if not raw.strip():
            continue
        m = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)", raw)
        if not m:
            fail(f"malformed checksum line: {raw!r}")
        sha, name = m.groups()
        if name in entries:
            fail(f"duplicate checksum entry: {name}")
        entries[name] = sha
    expected = files - {checksum_name}
    if set(entries) != expected:
        fail(f"checksum file set mismatch; expected={sorted(expected)} got={sorted(entries)}")
    for name, expected_sha in entries.items():
        actual = digest(root / name)
        if actual != expected_sha:
            fail(f"checksum mismatch: {name}")


def verify_manifest(root: Path) -> None:
    path = root / "RELEASE-BUNDLE-MANIFEST.json"
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        fail(f"invalid manifest JSON: {exc}")
    if manifest.get("schemaVersion") != 1 or manifest.get("releaseVersion") != EXPECTED_RELEASE:
        fail("manifest schema/releaseVersion mismatch")
    listed = manifest.get("files")
    if not isinstance(listed, list) or not listed:
        fail("manifest files list is empty")
    seen: set[str] = set()
    for item in listed:
        if not isinstance(item, dict):
            fail("invalid manifest file entry")
        name = item.get("path")
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9_.-]+", name):
            fail(f"unsafe manifest path: {name!r}")
        if name in seen:
            fail(f"duplicate manifest path: {name}")
        seen.add(name)
        path = root / name
        if not path.is_file() or path.is_symlink():
            fail(f"manifest file missing/unsafe: {name}")
        if path.stat().st_size != item.get("size"):
            fail(f"manifest size mismatch: {name}")
        if digest(path) != item.get("sha256"):
            fail(f"manifest digest mismatch: {name}")
    expected = {
        p.name
        for p in root.iterdir()
        if p.is_file() and p.name not in {"RELEASE-BUNDLE-MANIFEST.json", "RELEASE-BUNDLE-SHA256SUMS.txt"}
    }
    if seen != expected:
        fail("manifest payload file set mismatch")


def verify_security_metadata(root: Path) -> None:
    try:
        data = json.loads((root / "SECURITY-METADATA.json").read_text(encoding="utf-8"))
    except Exception as exc:
        fail(f"invalid SECURITY-METADATA.json: {exc}")
    if data.get("releaseVersion") != EXPECTED_RELEASE:
        fail("security metadata release version mismatch")
    commit = data.get("gitCommit")
    if commit != "local" and not (isinstance(commit, str) and re.fullmatch(r"[0-9a-f]{40}", commit)):
        fail("security metadata gitCommit is not 40-hex/local")
    if data.get("migrationLatest") != EXPECTED_MIGRATION_LATEST or data.get("migrationCount") != 43:
        fail("security metadata migration state mismatch")
    if data.get("migration0043Created") is not True:
        fail("security metadata must record Migration 0042 as created")


def verify_release_metadata(root: Path) -> None:
    text = (root / "RELEASE-METADATA.txt").read_text(encoding="utf-8")
    for needle in (
        "Target release: v0.43.0 Quantity Precision",
        "Migration count: 43",
        "Migration 0042: Controlled Recovery Plan Execution",
        "Migration 0043: Quantity Column Scale",
        "Step 12 Security CI / Supply Chain",
    ):
        if needle not in text:
            fail(f"RELEASE-METADATA.txt missing {needle!r}")


def verify_sboms(root: Path) -> None:
    for name in REQUIRED_SBOMS:
        try:
            data = json.loads((root / name).read_text(encoding="utf-8"))
        except Exception as exc:
            fail(f"invalid {name}: {exc}")
        if not str(data.get("spdxVersion", "")).startswith("SPDX-"):
            fail(f"{name}: missing SPDX version")
        if data.get("SPDXID") != "SPDXRef-DOCUMENT":
            fail(f"{name}: invalid SPDX document id")
        if not isinstance(data.get("creationInfo"), dict):
            fail(f"{name}: missing creationInfo")
        if not isinstance(data.get("packages"), list) or not data["packages"]:
            fail(f"{name}: no packages cataloged")


def verify_trivy(root: Path) -> None:
    for name in REQUIRED_TRIVY:
        exit_file = root / f"{name}.exitcode"
        if exit_file.read_text(encoding="utf-8").strip() != "0":
            fail(f"{name}: gate exit code is not zero")
        try:
            data = json.loads((root / name).read_text(encoding="utf-8"))
        except Exception as exc:
            fail(f"invalid {name}: {exc}")
        for result in data.get("Results") or []:
            for vuln in result.get("Vulnerabilities") or []:
                severity = str(vuln.get("Severity", "")).upper()
                if severity in {"HIGH", "CRITICAL"}:
                    fail(f"{name}: blocking vulnerability remains: {vuln.get('VulnerabilityID')} ({severity})")


def verify_npm(root: Path) -> None:
    for name, forbidden in REQUIRED_NPM.items():
        exit_file = root / f"{name}.exitcode"
        if exit_file.read_text(encoding="utf-8").strip() != "0":
            fail(f"{name}: gate exit code is not zero")
        try:
            data = json.loads((root / name).read_text(encoding="utf-8"))
        except Exception as exc:
            fail(f"invalid {name}: {exc}")
        counts = ((data.get("metadata") or {}).get("vulnerabilities") or {})
        for severity in forbidden:
            if int(counts.get(severity, 0) or 0) != 0:
                fail(f"{name}: {severity} vulnerability count is not zero")


def _decode_govuln_stream(raw: str) -> list[dict]:
    decoder = json.JSONDecoder()
    pos = 0
    messages: list[dict] = []
    while True:
        while pos < len(raw) and raw[pos].isspace():
            pos += 1
        if pos >= len(raw):
            break
        try:
            value, pos = decoder.raw_decode(raw, pos)
        except json.JSONDecodeError as exc:
            fail(f"govulncheck JSON stream invalid: {exc}")
        if not isinstance(value, dict):
            fail("govulncheck stream message is not an object")
        messages.append(value)
    if not messages:
        fail("govulncheck report contained no JSON messages")
    return messages


def _reachable_govuln_findings(messages: list[dict]) -> list[dict]:
    reachable: list[dict] = []
    for msg in messages:
        finding = msg.get("finding")
        if not isinstance(finding, dict):
            continue
        trace = finding.get("trace")
        if not isinstance(trace, list) or not trace or not isinstance(trace[0], dict):
            continue
        function = trace[0].get("function")
        if isinstance(function, str) and function.strip():
            reachable.append(finding)
    return reachable


def verify_govulncheck(root: Path) -> None:
    if (root / "govulncheck.json.exitcode").read_text(encoding="utf-8").strip() != "0":
        fail("govulncheck command exit code is not zero")
    if (root / "govulncheck-gate.json.exitcode").read_text(encoding="utf-8").strip() != "0":
        fail("govulncheck reachable-finding gate exit code is not zero")

    path = root / "govulncheck.json"
    if path.stat().st_size == 0:
        fail("govulncheck report is empty")
    messages = _decode_govuln_stream(path.read_text(encoding="utf-8"))
    reachable = _reachable_govuln_findings(messages)
    if reachable:
        ids = sorted({str(f.get("osv") or "unknown") for f in reachable})
        fail(f"govulncheck report contains reachable symbol-level findings: {ids}")

    try:
        summary = json.loads((root / "govulncheck-gate.json").read_text(encoding="utf-8"))
    except Exception as exc:
        fail(f"invalid govulncheck gate summary: {exc}")
    if int(summary.get("reachableFindingCount", -1)) != 0:
        fail("govulncheck gate summary reports reachable findings")
    if summary.get("reachableOSVs") not in ([], None):
        fail("govulncheck gate summary lists reachable OSVs")


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify CPIM-MES Step 12 security release bundle")
    parser.add_argument("bundle", type=Path)
    args = parser.parse_args()
    root = args.bundle.resolve()
    if not root.is_dir():
        fail(f"bundle directory missing: {root}")
    files = regular_files(root)
    required = {
        "RELEASE-METADATA.txt",
        "SHA256SUMS.txt",
        "SECURITY-METADATA.json",
        "RELEASE-BUNDLE-MANIFEST.json",
        "RELEASE-BUNDLE-SHA256SUMS.txt",
        "security_tool_versions.env",
        "govulncheck.json",
        "govulncheck.json.exitcode",
        "govulncheck-gate.json",
        "govulncheck-gate.json.exitcode",
        "dependency-security-policy.txt",
        "container-security-policy.txt",
        *REQUIRED_SBOMS,
        *REQUIRED_TRIVY,
        *(f"{name}.exitcode" for name in REQUIRED_TRIVY),
        *REQUIRED_NPM.keys(),
        *(f"{name}.exitcode" for name in REQUIRED_NPM),
    }
    missing = required - files
    if missing:
        fail(f"required files missing: {sorted(missing)}")

    verify_checksums(root, files)
    verify_manifest(root)
    verify_security_metadata(root)
    verify_release_metadata(root)
    verify_sboms(root)
    verify_trivy(root)
    verify_npm(root)
    verify_govulncheck(root)
    print("Release bundle verification: PASS")
    print(f"  files={len(files)} release={EXPECTED_RELEASE} migrationLatest={EXPECTED_MIGRATION_LATEST}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EXPECTED_RELEASE = "v0.43.0"
EXPECTED_MIGRATION_LATEST = 44

DEPENDENCY_FILES = [
    "govulncheck.json",
    "govulncheck.json.stderr",
    "govulncheck.json.exitcode",
    "govulncheck-gate.json",
    "govulncheck-gate.json.stdout",
    "govulncheck-gate.json.stderr",
    "govulncheck-gate.json.exitcode",
    "npm-audit-frontend-production.json",
    "npm-audit-frontend-production.json.stderr",
    "npm-audit-frontend-production.json.exitcode",
    "npm-audit-frontend-all.json",
    "npm-audit-frontend-all.json.stderr",
    "npm-audit-frontend-all.json.exitcode",
    "npm-audit-e2e.json",
    "npm-audit-e2e.json.stderr",
    "npm-audit-e2e.json.exitcode",
    "dependency-security-policy.txt",
]
CONTAINER_FILES = [
    "SBOM.spdx.json",
    "SBOM-backend-image.spdx.json",
    "SBOM-frontend-image.spdx.json",
    "trivy-backend.json",
    "trivy-backend.json.exitcode",
    "trivy-frontend.json",
    "trivy-frontend.json.exitcode",
    "container-security-policy.txt",
]


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def require_file(path: Path) -> None:
    if not path.is_file() or path.is_symlink():
        raise SystemExit(f"ERROR: required regular file missing or unsafe: {path}")


def copy_required(src_dir: Path, dst: Path, names: list[str]) -> None:
    for name in names:
        src = src_dir / name
        require_file(src)
        shutil.copyfile(src, dst / name)


def main() -> int:
    parser = argparse.ArgumentParser(description="Build the CPIM-MES v0.43.0 security release bundle")
    parser.add_argument("--dependency-reports", type=Path, required=True)
    parser.add_argument("--container-artifacts", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    args = parser.parse_args()

    if args.commit != "local" and not re.fullmatch(r"[0-9a-fA-F]{40}", args.commit):
        raise SystemExit("ERROR: --commit must be a 40-hex Git commit or 'local'")
    try:
        dt = datetime.fromisoformat(args.build_time.replace("Z", "+00:00"))
    except ValueError as exc:
        raise SystemExit(f"ERROR: --build-time must be RFC3339: {exc}") from exc
    if dt.tzinfo is None:
        raise SystemExit("ERROR: --build-time must include timezone")

    out = args.output.resolve()
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True, mode=0o755)

    # SHA256SUMS.txt is generated at release time from the git-tracked files.
    subprocess.run([sys.executable, str(ROOT / "scripts/generate_source_checksums.py")], cwd=ROOT, check=True)
    for repo_file in ("RELEASE-METADATA.txt", "SHA256SUMS.txt"):
        src = ROOT / repo_file
        require_file(src)
        shutil.copyfile(src, out / repo_file)

    copy_required(args.dependency_reports.resolve(), out, DEPENDENCY_FILES)
    copy_required(args.container_artifacts.resolve(), out, CONTAINER_FILES)

    # Tool pins are release evidence too.
    shutil.copyfile(ROOT / "scripts/security_tool_versions.env", out / "security_tool_versions.env")

    metadata = {
        "schemaVersion": 1,
        "releaseVersion": EXPECTED_RELEASE,
        "gitCommit": args.commit.lower(),
        "buildTime": dt.astimezone(timezone.utc).isoformat().replace("+00:00", "Z"),
        "migrationLatest": EXPECTED_MIGRATION_LATEST,
        "migrationCount": EXPECTED_MIGRATION_LATEST,
        "migration0044Created": True,
        "securityPolicy": {
            "govulncheck": "any reachable known vulnerability blocks release",
            "npmFrontendProduction": "HIGH or CRITICAL blocks release",
            "npmFrontendAll": "CRITICAL blocks release",
            "npmE2E": "CRITICAL blocks release",
            "containerImages": "fixable HIGH or CRITICAL blocks release",
            "containerIgnoreUnfixed": True,
        },
    }
    (out / "SECURITY-METADATA.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")

    payload_names = sorted(
        p.name
        for p in out.iterdir()
        if p.is_file() and p.name not in {"RELEASE-BUNDLE-MANIFEST.json", "RELEASE-BUNDLE-SHA256SUMS.txt"}
    )
    manifest = {
        "schemaVersion": 1,
        "releaseVersion": EXPECTED_RELEASE,
        "files": [
            {"path": name, "size": (out / name).stat().st_size, "sha256": sha256(out / name)}
            for name in payload_names
        ],
    }
    (out / "RELEASE-BUNDLE-MANIFEST.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    checksum_targets = sorted(
        p.name for p in out.iterdir() if p.is_file() and p.name != "RELEASE-BUNDLE-SHA256SUMS.txt"
    )
    with (out / "RELEASE-BUNDLE-SHA256SUMS.txt").open("w", encoding="utf-8", newline="\n") as f:
        for name in checksum_targets:
            f.write(f"{sha256(out / name)}  {name}\n")

    print(f"Release bundle created: {out}")
    print(f"Files: {len(list(out.iterdir()))}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

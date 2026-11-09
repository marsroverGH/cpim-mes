#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import tarfile
import tempfile
from pathlib import Path

EXPECTED_TARGET = "v0.44.0"
EXPECTED_MIGRATION = 45
EXPECTED_E2E = 26


def fail(msg: str) -> None:
    raise SystemExit(f"Release Candidate verification: FAIL\n - {msg}")


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def safe_tar_members(tf: tarfile.TarFile):
    for m in tf.getmembers():
        p = Path(m.name)
        if p.is_absolute() or ".." in p.parts:
            fail(f"unsafe tar path: {m.name}")
        if not m.isfile():
            fail(f"non-regular tar entry forbidden: {m.name}")
        yield m


def verify_source_archive(path: Path, candidate: str) -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        with tarfile.open(path, "r:gz") as tf:
            members = list(safe_tar_members(tf))
            source_root = f"cpim-mes-{candidate}"
            if not members or any(not m.name.startswith(source_root + "/") for m in members):
                fail("source archive contains entries outside candidate root")
            tf.extractall(root, members=members, filter="data")
        source = root / f"cpim-mes-{candidate}"
        checksums = source / "SHA256SUMS.txt"
        if not checksums.is_file():
            fail("source archive missing SHA256SUMS.txt")
        entries: dict[str, str] = {}
        for line in checksums.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            m = re.fullmatch(r"([0-9a-f]{64})  \./([^\n]+)", line)
            if not m:
                fail(f"source archive malformed checksum line: {line!r}")
            entries[m.group(2)] = m.group(1)
        actual = {
            p.relative_to(source).as_posix(): p
            for p in source.rglob("*")
            if p.is_file() and p.name != "SHA256SUMS.txt"
        }
        if set(actual) != set(entries):
            fail("source archive file set does not match SHA256SUMS.txt")
        for name, p in actual.items():
            if digest(p) != entries[name]:
                fail(f"source archive checksum mismatch: {name}")


def verify_security_archive(path: Path, verifier: Path) -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        with tarfile.open(path, "r:gz") as tf:
            members = list(safe_tar_members(tf))
            if not members or any(not m.name.startswith("security-release-bundle/") for m in members):
                fail("security archive contains entries outside security-release-bundle root")
            tf.extractall(root, members=members, filter="data")
        bundle = root / "security-release-bundle"
        proc = subprocess.run(["python3", str(verifier), str(bundle)], text=True, capture_output=True)
        if proc.returncode != 0:
            fail(f"nested security bundle verification failed: {proc.stdout}{proc.stderr}")


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("bundle", type=Path)
    a = p.parse_args()
    root = a.bundle.resolve()
    if not root.is_dir():
        fail("bundle directory missing")
    for pth in root.iterdir():
        if pth.is_symlink() or not pth.is_file():
            fail(f"only regular files allowed in RC bundle: {pth.name}")

    checksum = root / "RELEASE-CANDIDATE-SHA256SUMS.txt"
    manifest_path = root / "RELEASE-CANDIDATE-MANIFEST.json"
    metadata_path = root / "RELEASE-CANDIDATE-METADATA.json"
    result_path = root / "FINAL-CLEAN-ROOM-RESULT.json"
    for pth in (checksum, manifest_path, metadata_path, result_path, root / "RELEASE-CANDIDATE-METADATA.txt"):
        if not pth.is_file():
            fail(f"missing {pth.name}")

    entries: dict[str, str] = {}
    for line in checksum.read_text(encoding="utf-8").splitlines():
        m = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)", line)
        if not m:
            fail(f"malformed RC checksum line: {line!r}")
        entries[m.group(2)] = m.group(1)
    expected = {p.name for p in root.iterdir() if p.is_file() and p.name != checksum.name}
    if set(entries) != expected:
        fail("RC checksum file set mismatch")
    for name, expected_sha in entries.items():
        if digest(root / name) != expected_sha:
            fail(f"RC checksum mismatch: {name}")

    metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    candidate = metadata.get("candidateVersion")
    if not isinstance(candidate, str) or not re.fullmatch(r"v0\.44\.0-rc\.\d+", candidate):
        fail("invalid candidateVersion")
    if metadata.get("targetRelease") != EXPECTED_TARGET:
        fail("targetRelease mismatch")
    if metadata.get("migrationCount") != EXPECTED_MIGRATION or metadata.get("migrationLatest") != EXPECTED_MIGRATION:
        fail("migration metadata mismatch")
    if metadata.get("migration0045Created") is not True or metadata.get("playwrightTests") != EXPECTED_E2E:
        fail("RC invariant mismatch")

    result = json.loads(result_path.read_text(encoding="utf-8"))
    if result.get("status") != "PASS" or result.get("candidateVersion") != candidate:
        fail("clean-room result is not PASS for this candidate")
    if any(v != "PASS" for v in (result.get("gates") or {}).values()):
        fail("clean-room result contains non-PASS gate")

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("candidateVersion") != candidate:
        fail("manifest candidate mismatch")
    listed = manifest.get("files") or []
    seen: set[str] = set()
    for item in listed:
        name = item.get("path")
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9_.-]+", name):
            fail(f"unsafe manifest path: {name!r}")
        if name in seen:
            fail(f"duplicate manifest path: {name}")
        seen.add(name)
        pth = root / name
        if not pth.is_file() or pth.stat().st_size != item.get("size") or digest(pth) != item.get("sha256"):
            fail(f"manifest mismatch: {name}")
    expected_manifest = {
        p.name for p in root.iterdir()
        if p.is_file() and p.name not in {"RELEASE-CANDIDATE-MANIFEST.json", "RELEASE-CANDIDATE-SHA256SUMS.txt"}
    }
    if seen != expected_manifest:
        fail("manifest payload file set mismatch")

    source_name = (metadata.get("sourceArchive") or {}).get("name")
    security_name = (metadata.get("securityEvidenceArchive") or {}).get("name")
    source = root / str(source_name)
    security = root / str(security_name)
    if not source.is_file() or digest(source) != metadata["sourceArchive"].get("sha256"):
        fail("source archive metadata mismatch")
    if not security.is_file() or digest(security) != metadata["securityEvidenceArchive"].get("sha256"):
        fail("security archive metadata mismatch")

    verify_source_archive(source, candidate)
    verifier = source.parent.parent / "scripts" / "verify_release_bundle.py"
    if not verifier.is_file():
        # verifier is expected to run from the repository; fall back to this script's repository.
        verifier = Path(__file__).resolve().parent / "verify_release_bundle.py"
    verify_security_archive(security, verifier)
    print(f"Release Candidate verification: PASS ({candidate}, migrations=45/0045, e2e=26)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

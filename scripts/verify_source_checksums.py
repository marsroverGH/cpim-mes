#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHECKSUMS = ROOT / "SHA256SUMS.txt"
EXCLUDED_DIRS = {".git", ".tools", ".secrets", ".release-candidate-evidence", "security-reports", "security-artifacts", "release-bundle", "release-candidate-bundle", "backups", "node_modules", "dist", "playwright-report", "test-results", "__pycache__"}
EXCLUDED_FILES = {"SHA256SUMS.txt", "cpim-mes-v0.43.0-security-release-bundle.tar.gz"}


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def git_ignored() -> tuple[set[str], tuple[str, ...]]:
    """Files/dirs git ignores in a checkout (none in an extracted source archive)."""
    if not (ROOT / ".git").exists():
        return set(), ()
    out = subprocess.run(
        ["git", "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z"],
        cwd=ROOT, check=True, stdout=subprocess.PIPE,
    ).stdout.decode("utf-8")
    names = [n for n in out.split("\0") if n]
    return {n for n in names if not n.endswith("/")}, tuple(n for n in names if n.endswith("/"))


IGNORED_FILES, IGNORED_DIRS = git_ignored()


def included(path: Path) -> bool:
    rel = path.relative_to(ROOT)
    posix = rel.as_posix()
    if posix in IGNORED_FILES or posix.startswith(IGNORED_DIRS):
        return False
    if path.name in EXCLUDED_FILES:
        return False
    if any(part in EXCLUDED_DIRS for part in rel.parts):
        return False
    if path.suffix == ".pyc":
        return False
    return True


def main() -> int:
    if not CHECKSUMS.is_file():
        print("Source checksum verification: FAIL\n - SHA256SUMS.txt missing", file=sys.stderr)
        return 1
    entries: dict[str, str] = {}
    errors: list[str] = []
    for line in CHECKSUMS.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        m = re.fullmatch(r"([0-9a-f]{64})  \./([^\n]+)", line)
        if not m:
            errors.append(f"malformed checksum line: {line!r}")
            continue
        sha, name = m.groups()
        p = Path(name)
        if p.is_absolute() or ".." in p.parts:
            errors.append(f"unsafe checksum path: {name}")
            continue
        if name in entries:
            errors.append(f"duplicate checksum entry: {name}")
        entries[name] = sha

    actual_files: dict[str, Path] = {}
    for p in ROOT.rglob("*"):
        if p.is_symlink():
            rel = p.relative_to(ROOT).as_posix()
            if included(p):
                errors.append(f"symlink forbidden in source bundle: {rel}")
            continue
        if p.is_file() and included(p):
            actual_files[p.relative_to(ROOT).as_posix()] = p

    missing = set(actual_files) - set(entries)
    stale = set(entries) - set(actual_files)
    if missing:
        errors.append(f"files missing from SHA256SUMS.txt: {sorted(missing)}")
    if stale:
        errors.append(f"checksum entries without files: {sorted(stale)}")
    for name in sorted(set(entries) & set(actual_files)):
        actual = digest(actual_files[name])
        if actual != entries[name]:
            errors.append(f"checksum mismatch: {name}")

    if errors:
        print("Source checksum verification: FAIL")
        for error in errors:
            print(" -", error)
        return 1
    print(f"Source checksum verification: PASS ({len(entries)}/{len(entries)} files)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

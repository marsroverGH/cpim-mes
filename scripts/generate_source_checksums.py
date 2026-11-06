#!/usr/bin/env python3
"""Generate SHA256SUMS.txt for the source tree at release time.

SHA256SUMS.txt is no longer committed: git commit hashes already protect the
repository. It is generated here, from the files git tracks, whenever a
release / release-candidate bundle is built, so that recipients of the
distributed source archive can verify it offline with
scripts/verify_source_checksums.py.

Inside a git checkout the file list comes from `git ls-files`. In an extracted
source archive (no .git) an existing SHA256SUMS.txt is kept as-is.

  --require-clean  fail if any tracked file differs from HEAD, so the
                   checksums describe exactly the checked-out commit.
"""
from __future__ import annotations

import argparse
import hashlib
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHECKSUMS = ROOT / "SHA256SUMS.txt"


def git(*args: str) -> bytes:
    return subprocess.run(["git", *args], cwd=ROOT, check=True, stdout=subprocess.PIPE).stdout


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--require-clean", action="store_true")
    args = parser.parse_args()

    if not (ROOT / ".git").exists():
        if CHECKSUMS.is_file():
            print("Source checksum generation: not a git checkout; keeping existing SHA256SUMS.txt")
            return 0
        print("Source checksum generation: FAIL - not a git checkout and SHA256SUMS.txt is missing", file=sys.stderr)
        return 1

    if args.require_clean:
        dirty = git("status", "--porcelain", "--untracked-files=no").decode("utf-8", "replace").strip()
        if dirty:
            print("Source checksum generation: FAIL - tracked files differ from HEAD:\n" + dirty, file=sys.stderr)
            return 1

    errors: list[str] = []
    lines: list[str] = []
    for raw in git("ls-files", "-z").split(b"\0"):
        if not raw:
            continue
        name = raw.decode("utf-8")
        if name == "SHA256SUMS.txt":
            continue
        path = ROOT / name
        if path.is_symlink():
            errors.append(f"symlink forbidden in source bundle: {name}")
        elif not path.is_file():
            errors.append(f"tracked file missing from working tree: {name}")
        else:
            lines.append(f"{digest(path)}  ./{name}")

    if errors:
        print("Source checksum generation: FAIL", file=sys.stderr)
        for error in errors:
            print(" -", error, file=sys.stderr)
        return 1

    lines.sort(key=lambda line: line[68:].encode("utf-8"))
    with CHECKSUMS.open("w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines) + "\n")
    print(f"Source checksum generation: wrote {len(lines)} entries to SHA256SUMS.txt")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

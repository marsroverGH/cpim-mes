#!/usr/bin/env python3
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import re
import subprocess
import sys
import tarfile
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TARGET_RELEASE = "v0.43.0"
EXPECTED_MIGRATION = 45
EXPECTED_E2E = 26


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def parse_source_files() -> list[Path]:
    # SHA256SUMS.txt is generated at release time from the git-tracked files.
    subprocess.run([sys.executable, str(ROOT / "scripts/generate_source_checksums.py")], cwd=ROOT, check=True)
    checksum = ROOT / "SHA256SUMS.txt"
    if not checksum.is_file():
        raise SystemExit("ERROR: SHA256SUMS.txt missing")
    files: list[Path] = []
    for line in checksum.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        m = re.fullmatch(r"[0-9a-f]{64}  \./([^\n]+)", line)
        if not m:
            raise SystemExit(f"ERROR: malformed SHA256SUMS line: {line!r}")
        rel = Path(m.group(1))
        if rel.is_absolute() or ".." in rel.parts:
            raise SystemExit(f"ERROR: unsafe source path: {rel}")
        path = ROOT / rel
        if not path.is_file() or path.is_symlink():
            raise SystemExit(f"ERROR: source file missing/unsafe: {rel}")
        files.append(path)
    files.append(checksum)
    return sorted(files, key=lambda p: p.relative_to(ROOT).as_posix())


def add_bytes(tf: tarfile.TarFile, arcname: str, payload: bytes, mode: int = 0o644) -> None:
    info = tarfile.TarInfo(arcname)
    info.size = len(payload)
    info.mode = mode
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mtime = 0
    tf.addfile(info, io.BytesIO(payload))


def deterministic_tar_gz(output: Path, entries: list[tuple[str, bytes, int]]) -> None:
    with output.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as tf:
                for name, payload, mode in sorted(entries, key=lambda item: item[0]):
                    add_bytes(tf, name, payload, mode)


def main() -> int:
    p = argparse.ArgumentParser(description="Build verified CPIM-MES v0.43.0 Release Candidate evidence bundle")
    p.add_argument("--clean-room-result", type=Path, required=True)
    p.add_argument("--security-bundle", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--commit", required=True)
    p.add_argument("--build-time", required=True)
    p.add_argument("--candidate", required=True)
    a = p.parse_args()

    if not re.fullmatch(r"v0\.43\.0-rc\.\d+", a.candidate):
        raise SystemExit("ERROR: --candidate must match v0.43.0-rc.N")
    if a.commit != "local" and not re.fullmatch(r"[0-9a-fA-F]{40}", a.commit):
        raise SystemExit("ERROR: --commit must be 40-hex or local")
    try:
        dt = datetime.fromisoformat(a.build_time.replace("Z", "+00:00"))
    except ValueError as exc:
        raise SystemExit(f"ERROR: invalid --build-time: {exc}") from exc
    if dt.tzinfo is None:
        raise SystemExit("ERROR: --build-time must include timezone")

    result = json.loads(a.clean_room_result.read_text(encoding="utf-8"))
    required_result = {
        "status": "PASS",
        "candidateVersion": a.candidate,
        "targetRelease": TARGET_RELEASE,
        "migrationCount": EXPECTED_MIGRATION,
        "migrationLatest": EXPECTED_MIGRATION,
        "migration0045Created": True,
        "playwrightTests": EXPECTED_E2E,
    }
    for key, expected in required_result.items():
        if result.get(key) != expected:
            raise SystemExit(f"ERROR: clean-room result {key}={result.get(key)!r}, expected {expected!r}")
    gates = result.get("gates") or {}
    if not gates or any(v != "PASS" for v in gates.values()):
        raise SystemExit("ERROR: clean-room result contains non-PASS gates")

    security_dir = a.security_bundle.resolve()
    if not security_dir.is_dir():
        raise SystemExit("ERROR: security bundle directory missing")

    out = a.output.resolve()
    if out.exists():
        import shutil
        shutil.rmtree(out)
    out.mkdir(parents=True, mode=0o755)

    root_name = f"cpim-mes-{a.candidate}"
    source_entries: list[tuple[str, bytes, int]] = []
    for path in parse_source_files():
        rel = path.relative_to(ROOT).as_posix()
        mode = 0o755 if path.stat().st_mode & 0o111 else 0o644
        source_entries.append((f"{root_name}/{rel}", path.read_bytes(), mode))
    source_archive = out / f"cpim-mes-{a.candidate}-source.tar.gz"
    deterministic_tar_gz(source_archive, source_entries)

    security_entries: list[tuple[str, bytes, int]] = []
    for path in sorted(security_dir.iterdir(), key=lambda p: p.name):
        if not path.is_file() or path.is_symlink():
            raise SystemExit(f"ERROR: unsafe security bundle entry: {path}")
        security_entries.append((f"security-release-bundle/{path.name}", path.read_bytes(), 0o644))
    security_archive = out / "security-release-bundle.tar.gz"
    deterministic_tar_gz(security_archive, security_entries)

    clean_name = "FINAL-CLEAN-ROOM-RESULT.json"
    (out / clean_name).write_bytes(a.clean_room_result.read_bytes())
    static_meta = ROOT / "RELEASE-CANDIDATE-METADATA.txt"
    if not static_meta.is_file():
        raise SystemExit("ERROR: RELEASE-CANDIDATE-METADATA.txt missing")
    (out / static_meta.name).write_bytes(static_meta.read_bytes())

    metadata = {
        "schemaVersion": 1,
        "candidateVersion": a.candidate,
        "targetRelease": TARGET_RELEASE,
        "gitCommit": a.commit.lower(),
        "buildTime": dt.astimezone(timezone.utc).isoformat().replace("+00:00", "Z"),
        "migrationCount": EXPECTED_MIGRATION,
        "migrationLatest": EXPECTED_MIGRATION,
        "migration0045Created": True,
        "playwrightTests": EXPECTED_E2E,
        "sourceArchive": {"name": source_archive.name, "sha256": sha256(source_archive)},
        "securityEvidenceArchive": {"name": security_archive.name, "sha256": sha256(security_archive)},
        "promotionRule": "Promote to v0.43.0 GA only after this Release Candidate bundle verifies successfully and no code/content changes are introduced.",
    }
    (out / "RELEASE-CANDIDATE-METADATA.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")

    payload = sorted(p for p in out.iterdir() if p.is_file())
    manifest = {
        "schemaVersion": 1,
        "candidateVersion": a.candidate,
        "files": [{"path": p.name, "size": p.stat().st_size, "sha256": sha256(p)} for p in payload],
    }
    (out / "RELEASE-CANDIDATE-MANIFEST.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    checksum_targets = sorted(p for p in out.iterdir() if p.is_file() and p.name != "RELEASE-CANDIDATE-SHA256SUMS.txt")
    with (out / "RELEASE-CANDIDATE-SHA256SUMS.txt").open("w", encoding="utf-8", newline="\n") as f:
        for path in checksum_targets:
            f.write(f"{sha256(path)}  {path.name}\n")

    print(f"Release Candidate bundle created: {out}")
    print(f"candidate={a.candidate} sourceFiles={len(source_entries)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

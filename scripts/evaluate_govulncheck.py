#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from pathlib import Path


def fail(message: str, code: int = 2) -> int:
    print(f"ERROR: {message}")
    return code


def decode_stream(raw: str) -> list[dict]:
    decoder = json.JSONDecoder()
    pos = 0
    messages: list[dict] = []
    while True:
        while pos < len(raw) and raw[pos].isspace():
            pos += 1
        if pos >= len(raw):
            break
        value, pos = decoder.raw_decode(raw, pos)
        if not isinstance(value, dict):
            raise ValueError("govulncheck stream message is not a JSON object")
        messages.append(value)
    if not messages:
        raise ValueError("govulncheck stream contains no JSON messages")
    return messages


def reachable_findings(messages: list[dict]) -> list[dict[str, str]]:
    findings: list[dict[str, str]] = []
    seen: set[tuple[str, str, str, str]] = set()
    for msg in messages:
        finding = msg.get("finding")
        if not isinstance(finding, dict):
            continue
        trace = finding.get("trace")
        if not isinstance(trace, list) or not trace or not isinstance(trace[0], dict):
            continue
        first = trace[0]
        function = first.get("function")
        # govulncheck source scans emit module-level findings without package/function,
        # package-level findings without function, and symbol-level findings with a
        # function in the first trace frame. Only symbol-level findings are reachable.
        if not isinstance(function, str) or not function.strip():
            continue
        osv = str(finding.get("osv") or "unknown")
        module = str(first.get("module") or "")
        package = str(first.get("package") or "")
        symbol = function.strip()
        receiver = first.get("receiver")
        if isinstance(receiver, str) and receiver.strip():
            symbol = f"{receiver.strip()}.{symbol}"
        key = (osv, module, package, symbol)
        if key in seen:
            continue
        seen.add(key)
        findings.append({"osv": osv, "module": module, "package": package, "symbol": symbol})
    return findings


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Fail closed when govulncheck JSON contains reachable (symbol-level) findings"
    )
    parser.add_argument("report", type=Path)
    parser.add_argument("--summary", type=Path)
    args = parser.parse_args()

    try:
        raw = args.report.read_text(encoding="utf-8")
        messages = decode_stream(raw)
        reachable = reachable_findings(messages)
    except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as exc:
        return fail(f"invalid govulncheck JSON stream: {exc}")

    summary = {
        "schemaVersion": 1,
        "messageCount": len(messages),
        "reachableFindingCount": len(reachable),
        "reachableOSVs": sorted({f["osv"] for f in reachable}),
        "reachableFindings": reachable,
    }
    rendered = json.dumps(summary, indent=2, sort_keys=True) + "\n"
    if args.summary:
        try:
            args.summary.write_text(rendered, encoding="utf-8")
        except OSError as exc:
            return fail(f"cannot write summary: {exc}")
    else:
        print(rendered, end="")

    if reachable:
        print(
            "BLOCK: govulncheck reported reachable symbol-level vulnerabilities: "
            + ", ".join(summary["reachableOSVs"])
        )
        return 1
    print("PASS: govulncheck JSON contains no reachable symbol-level vulnerability findings")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

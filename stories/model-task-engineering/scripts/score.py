#!/usr/bin/env python3
"""Run an offline agent-bench score and return host.run stdout_json."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path


def slug(value: str) -> str:
    value = re.sub(r"[^A-Za-z0-9._-]+", "-", value.strip()).strip("-")
    return value or "score"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bench", required=True)
    parser.add_argument("--case", default="")
    parser.add_argument("--trace", default="")
    parser.add_argument("--out-dir", required=True)
    args = parser.parse_args()

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    stem = slug(args.case or Path(args.bench).stem)
    report_json = out_dir / f"{stem}-report.json"
    report_markdown = out_dir / f"{stem}-report.md"
    report_deck = out_dir / f"{stem}-deck.slidey.json"

    cmd = [
        "go",
        "run",
        "./cmd/kitsoki",
        "agent-bench",
        "score",
        args.bench,
        "--json-out",
        os.fspath(report_json),
        "--markdown-out",
        os.fspath(report_markdown),
        "--slidey-out",
        os.fspath(report_deck),
    ]
    if args.case:
        cmd.extend(["--case", args.case])
    if args.trace:
        cmd.extend(["--trace", args.trace])

    proc = subprocess.run(cmd, text=True, capture_output=True, check=False)
    status = "passed" if proc.returncode == 0 else "failed"
    summary = proc.stdout.strip().splitlines()[0] if proc.stdout.strip() else status
    payload = {
        "status": status,
        "summary": summary,
        "error": proc.stderr.strip() if proc.returncode != 0 else "",
        "stdout": proc.stdout,
        "report_json": os.fspath(report_json),
        "report_markdown": os.fspath(report_markdown),
        "report_deck": os.fspath(report_deck),
        "exit_code": proc.returncode,
    }
    print(json.dumps(payload, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Validate an LLM-drafted project profile before init_apply can use it."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path


def main() -> int:
    if len(sys.argv) < 3:
        raise SystemExit("usage: init_validate_profile.py target_path profile_json")
    target = Path(sys.argv[1]).expanduser().resolve()
    profile = json.loads(sys.argv[2])

    with tempfile.TemporaryDirectory(prefix="kitsoki-profile-") as tmp:
        path = Path(tmp) / "project-profile.json"
        path.write_text(json.dumps(profile, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        kitsoki_bin = os.environ.get("KITSOKI_BIN", "kitsoki")
        proc = subprocess.run(
            [kitsoki_bin, "project-profile", "validate", "--json", "--repo-root", str(target), str(path)],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    report = {}
    if proc.stdout.strip():
        try:
            report = json.loads(proc.stdout)
        except json.JSONDecodeError:
            report = {"ok": False, "schema": ["validator returned non-json stdout"]}
    ok = bool(report.get("ok")) and proc.returncode == 0
    print(json.dumps({
        "ok": ok,
        "profile": profile,
        "profile_json": json.dumps(profile, sort_keys=True),
        "schema": report.get("schema", []),
        "semantic": report.get("semantic", []),
        "warnings": report.get("warnings", []),
        "validator_stdout": proc.stdout,
        "validator_stderr": proc.stderr,
        "validator_exit_code": proc.returncode,
    }, sort_keys=True))
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())

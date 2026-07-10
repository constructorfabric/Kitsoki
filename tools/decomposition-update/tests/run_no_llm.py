#!/usr/bin/env python3
"""No-LLM tests for the managed decomposition-update transaction."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
TOOL = ROOT / "tools" / "decomposition-update" / "apply_delta.py"
DATA = ROOT / "tools" / "decomposition-update" / "tests" / "testdata"


def run(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run([sys.executable, str(TOOL), *args], cwd=ROOT, capture_output=True, text=True, check=False)


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory(prefix="decomp-update-") as td:
        tmp = Path(td)
        base = DATA / "base.yaml"
        accepted = DATA / "delta-add-ws.yaml"
        cycle = DATA / "delta-cycle.yaml"
        missing = DATA / "delta-missing-provenance.yaml"
        out = tmp / "out.yaml"
        versions = tmp / "versions"
        events = tmp / "events.jsonl"

        ok = run(str(base), str(accepted), "--out", str(out), "--versions-dir", str(versions), "--event-log", str(events), "--version-id", "test")
        if ok.returncode != 0:
            failures.append(f"accepted delta failed:\n{ok.stdout}{ok.stderr}")
        if not out.exists():
            failures.append("accepted delta did not write output")
        if not (versions / "decomposition.test.yaml").exists():
            failures.append("accepted delta did not version prior graph")
        if not events.exists():
            failures.append("accepted delta did not write event log")
        else:
            event = json.loads(events.read_text(encoding="utf-8").splitlines()[-1])
            if event.get("trigger") != "session-fold-in":
                failures.append(f"event trigger = {event.get('trigger')!r}")
            if event.get("added") != ["WS.1", "WS.2"]:
                failures.append(f"event added = {event.get('added')!r}")

        bad_out = tmp / "bad.yaml"
        bad_versions = tmp / "bad-versions"
        bad_events = tmp / "bad-events.jsonl"
        bad = run(str(base), str(cycle), "--out", str(bad_out), "--versions-dir", str(bad_versions), "--event-log", str(bad_events), "--version-id", "bad")
        if bad.returncode == 0:
            failures.append("cycle delta unexpectedly passed")
        if bad_out.exists() or bad_versions.exists() or bad_events.exists():
            failures.append("cycle delta wrote artifacts despite rejection")
        if "dependency cycle" not in bad.stdout:
            failures.append("cycle delta did not report validator cycle")

        missing_out = tmp / "missing.yaml"
        missing_bad = run(str(base), str(missing), "--out", str(missing_out), "--versions-dir", str(tmp / "missing-versions"), "--event-log", str(tmp / "missing-events.jsonl"))
        if missing_bad.returncode == 0:
            failures.append("missing provenance delta unexpectedly passed")
        if "provenance" not in missing_bad.stdout:
            failures.append("missing provenance delta did not explain rejection")

    if failures:
        print("FAIL: decomposition-update")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("PASS: decomposition-update managed delta transaction")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

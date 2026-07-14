#!/usr/bin/env python3
"""No-LLM tests for the WB.1 corpus validator."""

from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
VALIDATOR = HERE / "validate_corpus.py"


VALID = """\
kind: arena_cost_corpus
version: 1
frozen: true
selection_rule_committed_at: "2026-07-04T00:00:00Z"
archetypes: tools/arena/corpus/archetypes.yaml
tasks:
  - id: bugfix-train
    repo: query-string
    archetype: bug-fix
    baseline_sha: abc123
    oracle: {type: bench.py, project: query-string, bug: qs1}
    ticket: "Fix encoded separator parsing."
    verified_red: true
    verified_green: true
    split: training
  - id: bugfix-heldout
    repo: query-string
    archetype: bug-fix
    baseline_sha: def456
    oracle: {type: bench.py, project: query-string, bug: qs2}
    ticket: "Fix comma array type parsing."
    verified_red: true
    verified_green: true
    split: heldout
"""


INVALID = """\
kind: arena_cost_corpus
version: 1
frozen: false
tasks:
  - id: broken
    repo: query-string
    archetype: bug-fix
    oracle: bench
    verified_red: false
    split: training
"""


def run(path: Path, *extra: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(VALIDATOR), str(path), *extra],
        capture_output=True,
        text=True,
        check=False,
    )


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory(prefix="arena-corpus-test-") as td:
        root = Path(td)
        valid = root / "valid.yaml"
        invalid = root / "invalid.yaml"
        missing = root / "missing.yaml"
        valid.write_text(VALID, encoding="utf-8")
        invalid.write_text(INVALID, encoding="utf-8")

        ok = run(valid, "--allow-small")
        if ok.returncode != 0:
            failures.append(f"valid fixture failed:\n{ok.stdout}{ok.stderr}")

        bad = run(invalid, "--allow-small")
        if bad.returncode == 0:
            failures.append("invalid fixture unexpectedly passed")
        if "frozen must be true" not in bad.stdout:
            failures.append("invalid fixture did not report frozen failure")

        absent = run(missing)
        if absent.returncode == 0:
            failures.append("missing manifest unexpectedly passed")
        if "manifest does not exist" not in absent.stdout:
            failures.append("missing manifest did not report absence")

    if failures:
        print("FAIL: corpus validator tests")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("PASS: corpus validator tests")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

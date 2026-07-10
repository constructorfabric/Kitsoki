#!/usr/bin/env python3
"""Deterministic tests for `python3 -m tools.persona_qa` completion CLI."""

from __future__ import annotations

import contextlib
import io
import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from tools.completion_state import load_completion_state  # noqa: E402
from tools.persona_qa.completion_cli import main  # noqa: E402

TESTDATA = Path(__file__).resolve().parent / "testdata"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


with tempfile.TemporaryDirectory(prefix="persona-qa-completion-cli-") as td:
    out = Path(td) / "ui-qa-completion-state.json"
    code = main([
        "--kind",
        "ui-qa",
        "--input",
        str(TESTDATA / "ui_qa_verdict_pass.json"),
        "--out",
        str(out),
    ])
    check("ui-qa CLI exit code", code, 0)
    payload = load_completion_state(out)
    check("ui-qa CLI verdict", payload["verdict"], "solved")
    check("ui-qa CLI check_type", payload["check_type"], "journey-verdict")
    check("ui-qa CLI metrics checks_passed", payload["metrics"]["checks_passed"], 2)
    check_true(
        "ui-qa CLI evidence includes source verdict",
        str(TESTDATA / "ui_qa_verdict_pass.json") in payload["evidence_refs"],
        payload["evidence_refs"],
    )

    run_dir = Path(td) / "product-journey"
    run_dir.mkdir()
    (run_dir / "review.json").write_text(
        json.dumps({
            "status": "ready",
            "summary_counts": {"passed": 3, "warned": 0, "failed": 0, "total": 3},
        }),
        encoding="utf-8",
    )
    (run_dir / "scenario-outcomes.json").write_text(
        json.dumps({"summary": {"scenarios": 1, "started": 1, "blocked": 0}}),
        encoding="utf-8",
    )
    run_out = Path(td) / "product-journey-completion-state.json"
    code = main([
        "--kind",
        "product-journey-run",
        "--input",
        str(run_dir),
        "--out",
        str(run_out),
    ])
    check("product-journey CLI exit code", code, 0)
    run_payload = load_completion_state(run_out)
    check("product-journey CLI verdict", run_payload["verdict"], "solved")
    check("product-journey CLI checks_total", run_payload["metrics"]["checks_total"], 3)

buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    code = main([
        "--kind",
        "ui-review",
        "--input",
        str(TESTDATA / "ui_review_verdict_blocked.json"),
    ])
check("ui-review stdout CLI exit code", code, 0)
stdout_payload = json.loads(buf.getvalue())
check("ui-review stdout verdict", stdout_payload["verdict"], "blocked")
check("ui-review stdout check_type", stdout_payload["check_type"], "ux-heuristic")
check("ui-review stdout health", stdout_payload["health"], "model:result")

if failures:
    print("FAIL: persona-qa completion CLI")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: persona-qa completion CLI")

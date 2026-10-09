#!/usr/bin/env python3
"""Deterministic tests for the shared persona-QA completion contract."""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from tools.persona_qa import CompletionState, from_product_journey_report, load_product_journey_run, to_scenario_qa_leg_result


failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


ready = from_product_journey_report({
    "review_status": "ready",
    "validation_status": "valid",
    "passed": 19,
    "warnings": 0,
    "failed": 0,
    "total": 19,
    "run_dir": ".artifacts/product-journey/ready",
})
check("ready state", ready.state, "completed")
check("ready verdict", ready.verdict, "solved")
check("ready health", ready.health, "model:result")

partial = from_product_journey_report({
    "review": {"status": "needs_evidence", "summary_counts": {"passed": 15, "warned": 3, "failed": 1, "total": 19}},
    "validation": {"status": "valid"},
    "scenario": {"id": "onboarding"},
    "attached_evidence": [{"path": "cassette://run/onboarding/tui"}],
    "scenario_outcomes_summary": {"scenarios": 5, "started": 1, "blocked": 0},
})
check("partial state", partial.state, "incomplete")
check("partial verdict", partial.verdict, "partial")
check("partial evidence refs", partial.evidence_refs, ["cassette://run/onboarding/tui"])

empty = from_product_journey_report({
    "review": {"status": "needs_evidence", "summary_counts": {"passed": 0, "warned": 0, "failed": 19, "total": 19}},
    "validation": {"status": "valid"},
})
check("empty verdict", empty.verdict, "failed")

# --- load_product_journey_run: evidence.json backfills attached_evidence ---
# (P2.10 "parallel legs via arena": a run bundle loaded straight off disk
# used to always report evidence_refs=[] even when evidence.json had real
# captured proof, because "attached_evidence" was never persisted under that
# key -- see load_product_journey_run's own comment. This is the regression
# guard for that fix; without it, every arena-folded leg would be forced to
# degraded-evidence by record_leg_result.star's _lacks_evidence() backstop
# regardless of what the underlying replay-smoke bundle actually proved.)
with tempfile.TemporaryDirectory() as tmp:
    run_dir = Path(tmp)
    (run_dir / "review.json").write_text(json.dumps({
        "review_status": "ready",
        "summary_counts": {"passed": 5, "warned": 0, "failed": 0, "total": 5},
    }), encoding="utf-8")
    (run_dir / "evidence.json").write_text(json.dumps({
        "items": [
            {"scenario": "bugfix", "kind": "trace", "path": "cassette://bugfix/trace", "status": "captured"},
            {"scenario": "bugfix", "kind": "diff", "path": "cassette://bugfix/diff", "status": "validated"},
            {"scenario": "bugfix", "kind": "png", "path": "cassette://bugfix/png", "status": "rejected"},
            {"scenario": "bugfix", "kind": "empty", "path": "", "status": "captured"},
        ]
    }), encoding="utf-8")
    loaded = load_product_journey_run(run_dir)
check("disk-loaded evidence refs", loaded.evidence_refs, ["cassette://bugfix/diff", "cassette://bugfix/trace"])
check("disk-loaded verdict", loaded.verdict, "solved")

with tempfile.TemporaryDirectory() as tmp:
    run_dir = Path(tmp)
    (run_dir / "review.json").write_text(json.dumps({
        "review_status": "needs_evidence",
        "summary_counts": {"passed": 1, "warned": 0, "failed": 1, "total": 2},
    }), encoding="utf-8")
    loaded_no_evidence = load_product_journey_run(run_dir)
check("disk-loaded with no evidence.json stays empty", loaded_no_evidence.evidence_refs, [])

# --- to_scenario_qa_leg_result: CompletionState -> drive_result/judge_result ---
# The mapping stories/scenario-qa/rooms/parallel.yaml relies on to fold an
# arena cell's verdict into the exact shape scripts/record_leg_result.star
# expects (schemas/drive_leg_result.json, schemas/judge_leg_result.json).
_LEG = {"leg_id": "bugfix::tui", "scenario": "bugfix", "transport": "tui"}

solved = CompletionState(
    state="completed", verdict="solved", health="model:result",
    summary="29/29 checks passed", evidence_refs=["cassette://bugfix/trace"], blockers=[],
)
folded_solved = to_scenario_qa_leg_result(_LEG, solved)
check("solved -> drive status", folded_solved["drive_result"]["status"], "captured")
check("solved -> judge verdict", folded_solved["judge_result"]["verdict"], "pass")
check("solved -> evidence_refs carried through", folded_solved["drive_result"]["evidence_refs"], ["cassette://bugfix/trace"])
check("solved -> harness_used is replay (never fabricated live)", folded_solved["drive_result"]["harness_used"], "replay")

partial_state = CompletionState(
    state="incomplete", verdict="partial", health="model:result",
    summary="15/19 checks passed", evidence_refs=["cassette://bugfix/trace"], blockers=[],
)
folded_partial = to_scenario_qa_leg_result(_LEG, partial_state)
check("partial -> drive status", folded_partial["drive_result"]["status"], "attempted")
check("partial -> judge verdict", folded_partial["judge_result"]["verdict"], "unsupported")

failed_state = CompletionState(
    state="incomplete", verdict="failed", health="model:result",
    summary="0/19 checks passed", evidence_refs=[], blockers=[],
)
folded_failed = to_scenario_qa_leg_result(_LEG, failed_state)
check("failed -> judge verdict", folded_failed["judge_result"]["verdict"], "fail")

blocked_state = CompletionState(
    state="blocked", verdict="blocked", health="model:result",
    summary="missing proof", evidence_refs=[], blockers=["missing proof: bugfix"],
)
folded_blocked = to_scenario_qa_leg_result(_LEG, blocked_state)
check("blocked -> drive status", folded_blocked["drive_result"]["status"], "blocked")
check("blocked -> judge verdict", folded_blocked["judge_result"]["verdict"], "degraded-evidence")
check("blocked -> blockers carried through", folded_blocked["drive_result"]["blockers"], ["missing proof: bugfix"])

error_state = CompletionState(
    state="error", verdict="blocked", health="infra:harness",
    summary="harness crashed", evidence_refs=[], blockers=[],
)
folded_error = to_scenario_qa_leg_result(_LEG, error_state)
check("error state never reported as captured", folded_error["drive_result"]["status"], "blocked")

if failures:
    print("FAIL: persona-qa completion contract")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: persona-qa completion contract")

#!/usr/bin/env python3
"""Deterministic tests for the ui-qa/ui-review verdict.json -> CompletionState
adapter (WS-G G6, verdict unification). No LLM, no docker: every fixture here
is an already-written verdict.json (the judging already happened), read from
disk exactly as an arena journey-verdict/ux-heuristic check would.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from tools.persona_qa import (
    from_ui_qa_verdict,
    from_ui_review_verdict,
    load_ui_qa_verdict,
    load_ui_review_verdict,
)

TESTDATA = Path(__file__).resolve().parent / "testdata"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


def load(name: str) -> dict:
    return json.loads((TESTDATA / name).read_text(encoding="utf-8"))


# ---------------------------------------------------------------------------
# kitsoki-ui-qa -> journey-verdict
# ---------------------------------------------------------------------------

qa_pass = from_ui_qa_verdict(load("ui_qa_verdict_pass.json"))
check("ui-qa pass: verdict solved", qa_pass.verdict, "solved")
check("ui-qa pass: state completed", qa_pass.state, "completed")
check("ui-qa pass: health model:result", qa_pass.health, "model:result")
check("ui-qa pass: check_type journey-verdict", qa_pass.check_type, "journey-verdict")
check("ui-qa pass: checks_passed", qa_pass.checks_passed, 2)
check("ui-qa pass: checks_total", qa_pass.checks_total, 2)
check("ui-qa pass: no blockers", qa_pass.blockers, [])
check(
    "ui-qa pass: evidence refs carry cited frames",
    sorted(qa_pass.evidence_refs),
    ["0001-0ms.png", "0007-5200ms.png"],
)

qa_partial = from_ui_qa_verdict(load("ui_qa_verdict_partial.json"))
check("ui-qa partial: verdict partial", qa_partial.verdict, "partial")
check("ui-qa partial: state incomplete", qa_partial.state, "incomplete")
check("ui-qa partial: checks_warned counts unsupported", qa_partial.checks_warned, 1)
check("ui-qa partial: no required-scenario blockers", qa_partial.blockers, [])

qa_blocked = from_ui_qa_verdict(load("ui_qa_verdict_blocked.json"))
check("ui-qa blocked: a required scenario failing is blocked, not just partial",
      qa_blocked.verdict, "blocked")
check("ui-qa blocked: state blocked", qa_blocked.state, "blocked")
check_true("ui-qa blocked: blockers name the required scenario",
           any("drive" in b for b in qa_blocked.blockers), qa_blocked.blockers)

# Loader reads off disk and stamps run_dir/source path into evidence_refs.
qa_loaded = load_ui_qa_verdict(TESTDATA / "ui_qa_verdict_pass.json")
check("load_ui_qa_verdict: same verdict as from_ui_qa_verdict", qa_loaded.verdict, qa_pass.verdict)
check("load_ui_qa_verdict: run_dir set to the file's directory", qa_loaded.run_dir, str(TESTDATA))
check_true(
    "load_ui_qa_verdict: evidence_refs include the verdict.json path itself",
    any(str(TESTDATA / "ui_qa_verdict_pass.json") == ref for ref in qa_loaded.evidence_refs),
    qa_loaded.evidence_refs,
)

# to_dict() carries check_type explicitly (unlike CellResult, CompletionState
# has no "default" check_type to omit -- every ui-verdict adapter output sets
# one) and is schema-shaped (schema_version/verdict/health/metrics/evidence_refs).
qa_dict = qa_pass.to_dict()
check("to_dict carries check_type", qa_dict.get("check_type"), "journey-verdict")
for required_key in ("schema_version", "verdict", "health", "metrics", "evidence_refs"):
    check_true(f"to_dict has required schema key {required_key!r}", required_key in qa_dict, qa_dict)

# A plain product-journey completion state never sets check_type -- to_dict()
# omits the key entirely so pre-G6 payloads stay byte-identical.
from tools.persona_qa import from_product_journey_report  # noqa: E402

plain = from_product_journey_report({"review_status": "ready", "validation_status": "valid"})
check("plain product-journey state has no check_type", plain.check_type, "")
check("plain product-journey to_dict omits check_type", "check_type" in plain.to_dict(), False)


# ---------------------------------------------------------------------------
# kitsoki-ui-review -> ux-heuristic
# ---------------------------------------------------------------------------

review_pass = from_ui_review_verdict(load("ui_review_verdict_pass.json"))
check("ui-review pass: verdict solved", review_pass.verdict, "solved")
check("ui-review pass: check_type ux-heuristic", review_pass.check_type, "ux-heuristic")
check("ui-review pass: checks_failed 0", review_pass.checks_failed, 0)
check("ui-review pass: no blockers", review_pass.blockers, [])

review_partial = from_ui_review_verdict(load("ui_review_verdict_partial.json"))
check("ui-review partial: warnings-only -> partial", review_partial.verdict, "partial")
check("ui-review partial: checks_warned", review_partial.checks_warned, 2)
check("ui-review partial: no error blockers", review_partial.blockers, [])

review_blocked = from_ui_review_verdict(load("ui_review_verdict_blocked.json"))
check("ui-review blocked: an error finding blocks", review_blocked.verdict, "blocked")
check("ui-review blocked: checks_failed counts errors", review_blocked.checks_failed, 1)
check_true(
    "ui-review blocked: blockers name the failing surface/check",
    any("color-contrast" in b for b in review_blocked.blockers),
    review_blocked.blockers,
)
check(
    "ui-review blocked: evidence refs carry cited frames",
    sorted(review_blocked.evidence_refs),
    ["01-home-welcome@desktop.png", "01-home-welcome@mobile.png"],
)

review_loaded = load_ui_review_verdict(TESTDATA / "ui_review_verdict_blocked.json")
check("load_ui_review_verdict: same verdict as from_ui_review_verdict",
      review_loaded.verdict, review_blocked.verdict)
check("load_ui_review_verdict: run_dir set to the file's directory",
      review_loaded.run_dir, str(TESTDATA))


if failures:
    print("FAIL: ui-qa/ui-review verdict.json -> completion-state adapter")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: ui-qa/ui-review verdict.json -> completion-state adapter")

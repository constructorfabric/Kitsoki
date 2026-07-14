#!/usr/bin/env python3
"""WS-G G6 — arena's `journey-verdict`/`ux-heuristic` check types (no LLM/docker).

Covers:

  1. `run_ui_verdict_check` grading a REAL kitsoki-ui-qa / kitsoki-ui-review
     `verdict.json` fixture off disk (the exact adapter `tools.persona_qa`
     tests exercise directly; here proven through the arena check-dispatch
     seam) — solved/blocked verdicts, check_type stamped, evidence_refs
     carried through.
  2. Honest PENDING when the artifact genuinely doesn't exist: no
     `verdict_path` configured, and a configured path that doesn't exist yet.
     Never a fake green.
  3. `run_cell_checks` (single-cell) and `run_sweep` (placement) both dispatch
     journey-verdict/ux-heuristic through the file adapter — no container call
     for those check types, only for `replay`.
"""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE.parent))

from arena.checks import run_cell_checks, run_ui_verdict_check  # noqa: E402
from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.model import CheckSpec, JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


UI_QA_PASS = "tools/persona_qa/tests/testdata/ui_qa_verdict_pass.json"
UI_QA_BLOCKED = "tools/persona_qa/tests/testdata/ui_qa_verdict_blocked.json"
UI_REVIEW_BLOCKED = "tools/persona_qa/tests/testdata/ui_review_verdict_blocked.json"

BASE_SPEC = {
    "job_type": "bugfix",
    "targets": [{"id": "query-string", "label": "qs", "stack": "javascript"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
}


def one_cell(**overrides) -> object:
    data = {**BASE_SPEC, **overrides}
    spec = JobSpec.from_dict(data)
    return spec.cells()[0]


# ---------------------------------------------------------------------------
# 1. run_ui_verdict_check grades a real verdict.json fixture.
# ---------------------------------------------------------------------------

cell = one_cell()

qa_check = CheckSpec(check_type="journey-verdict", options={"verdict_path": UI_QA_PASS})
qa_result = run_ui_verdict_check(cell, qa_check)
check("ui-qa fixture: check_type stamped", qa_result.check_type, "journey-verdict")
check("ui-qa fixture: verdict solved", qa_result.verdict, "solved")
check("ui-qa fixture: health model:result", qa_result.health, "model:result")
check("ui-qa fixture: cell coords carried", qa_result.cell_id, cell.id)
check_true("ui-qa fixture: evidence_refs non-empty", len(qa_result.evidence_refs) > 0, qa_result.evidence_refs)
check("ui-qa fixture: metrics carry checks_passed", qa_result.metrics.get("checks_passed"), 2)

qa_blocked_check = CheckSpec(check_type="journey-verdict", options={"verdict_path": UI_QA_BLOCKED})
qa_blocked_result = run_ui_verdict_check(cell, qa_blocked_check)
check("ui-qa blocked fixture: verdict blocked", qa_blocked_result.verdict, "blocked")

review_check = CheckSpec(check_type="ux-heuristic", options={"verdict_path": UI_REVIEW_BLOCKED})
review_result = run_ui_verdict_check(cell, review_check)
check("ui-review fixture: check_type stamped", review_result.check_type, "ux-heuristic")
check("ui-review fixture: verdict blocked (error finding)", review_result.verdict, "blocked")
check("ui-review fixture: metrics carry checks_failed", review_result.metrics.get("checks_failed"), 1)


# ---------------------------------------------------------------------------
# 2. Honest PENDING when the artifact genuinely doesn't exist.
# ---------------------------------------------------------------------------

unconfigured = run_ui_verdict_check(cell, CheckSpec(check_type="journey-verdict"))
check("no verdict_path configured: PENDING", unconfigured.verdict, "pending")
check("no verdict_path configured: incomplete health", unconfigured.health, "incomplete")
check_true("no verdict_path configured: honest note",
           "no verdict_path configured" in unconfigured.notes, unconfigured.notes)

missing = run_ui_verdict_check(
    cell, CheckSpec(check_type="ux-heuristic", options={"verdict_path": "tools/persona_qa/tests/testdata/does-not-exist.json"})
)
check("missing verdict.json file: PENDING", missing.verdict, "pending")
check("missing verdict.json file: incomplete health", missing.health, "incomplete")
check_true("missing verdict.json file: honest note",
           "no verdict.json found" in missing.notes, missing.notes)


# ---------------------------------------------------------------------------
# 3. run_cell_checks dispatches journey-verdict/ux-heuristic via the file
#    adapter -- never through the executor/container path.
# ---------------------------------------------------------------------------

multi_spec = JobSpec.from_dict({
    **BASE_SPEC,
    "axes": {"bug": ["qs1"]},
    "checks": [
        "replay",
        {"check_type": "journey-verdict", "options": {"verdict_path": UI_QA_PASS}},
        {"check_type": "ux-heuristic", "options": {"verdict_path": UI_REVIEW_BLOCKED}},
    ],
})
multi_cell = multi_spec.cells()[0]


def responder(_cell, host, _argv):
    return ContainerRun(exit_code=0, stdout="", stderr="", host=host)


backend = FakeBackend(responder)
executor = CellExecutor(backend, mounts_for=lambda c, h: {})

per_cell = run_cell_checks(multi_cell, executor, multi_spec.checks)
check("run_cell_checks: one result per check", len(per_cell), 3)
check("run_cell_checks: replay first", per_cell[0].check_type, "replay")
check("run_cell_checks: journey-verdict graded from fixture", per_cell[1].verdict, "solved")
check("run_cell_checks: ux-heuristic graded from fixture", per_cell[2].verdict, "blocked")
check("run_cell_checks: only ONE container call (replay only)", len(backend.calls), 1)

# run_sweep (the placement scheduler) reaches the same result with retry
# plumbing around it -- and still only spends one container call per cell.
backend2 = FakeBackend(responder)
executor2 = CellExecutor(backend2, mounts_for=lambda c, h: {})
sweep_results = run_sweep(multi_spec, executor2, live=False)
check("run_sweep: 1 cell x 3 checks = 3 results", len(sweep_results), 3)
check("run_sweep: one container call (replay only)", len(backend2.calls), 1)
by_type = {r.check_type: r for r in sweep_results}
check("run_sweep: journey-verdict graded from fixture", by_type["journey-verdict"].verdict, "solved")
check("run_sweep: ux-heuristic graded from fixture", by_type["ux-heuristic"].verdict, "blocked")
check("run_sweep: replay result present and tagged", by_type["replay"].check_type, "replay")


if failures:
    print("FAIL: ui-verdict check types (WS-G G6)")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: ui-verdict check types (WS-G G6 file-adapter journey-verdict/ux-heuristic, no LLM/docker)")

"""Adapter: kitsoki-ui-qa / kitsoki-ui-review `verdict.json` -> CompletionState.

Both judging skills emit a gated `verdict.json` (see their SKILL.md "verdict.json
shape" sections) that is NOT completion-state-shaped — it is per-scenario
(ui-qa) or per-finding (ui-review), scored by a read-only vision agent. This
module is the WS-G G6 bridge: it reads an already-written verdict.json (no LLM
call of its own — the judging already happened) and derives the same
job-agnostic verdict/health/evidence_refs shape every other completion-state
producer emits, so a per-transport UI verdict folds back into an arena rollup
or a persona-qa run's completion state like any other check.

- `kitsoki-ui-qa` grades "does the demo show scenario X" — a per-scenario
  evidence-completeness gate. That is exactly the `check_type: journey-verdict`
  slot (schemas/completion-state.schema.json: "the product-journey review gate
  (evidence completeness, finding floors)") -> `from_ui_qa_verdict`.
- `kitsoki-ui-review` grades a Nielsen heuristic-catalog UX critique over
  captured frames. That is exactly `check_type: ux-heuristic` ("heuristic-
  catalog UX critique over captured frames/transcripts") -> `from_ui_review_verdict`.

Both adapters are pure functions over an already-parsed verdict dict (no I/O),
plus a thin `load_*` wrapper that reads the JSON off disk — mirroring
`from_product_journey_report` / `load_product_journey_run`'s split.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from tools.completion_state import load_json_object

from .completion import CompletionState


def from_ui_qa_verdict(
    verdict: dict[str, Any],
    *,
    source_path: str = "",
    run_dir: str = "",
) -> CompletionState:
    """Build a `journey-verdict` completion state from a kitsoki-ui-qa verdict.json.

    Shape (`.agents/skills/kitsoki-ui-qa/SKILL.md` "verdict.json shape"):
    `{"overall": "pass"|"fail", "summary": {scenarios_total, passed, failed,
    unsupported}, "frames_reviewed": [...], "scenarios": [{"id", "title",
    "required", "status", "steps": [{"evidence": [{"frame", ...}], ...}]}]}`.
    `report.sh` recomputes `overall` from per-scenario status rather than
    trusting the model's own field, so `overall` is already the gated verdict —
    this adapter only needs to also flag a *required* scenario failure as
    `blocked` (a harder floor than a plain `partial`).
    """
    summary = verdict.get("summary", {}) or {}
    scenarios = verdict.get("scenarios", []) or []
    overall = verdict.get("overall", "fail")

    total = int(summary.get("scenarios_total", len(scenarios)) or 0)
    passed = int(summary.get("passed", 0) or 0)
    failed = int(summary.get("failed", 0) or 0)
    unsupported = int(summary.get("unsupported", 0) or 0)

    required_failed = [
        s for s in scenarios if s.get("required") and s.get("status") != "pass"
    ]
    blockers = sorted(
        f"{s.get('id') or s.get('title') or 'scenario'}: {s.get('status', 'fail')}"
        for s in required_failed
    )

    if overall == "pass":
        state, cs_verdict = "completed", "solved"
    elif required_failed:
        state, cs_verdict = "blocked", "blocked"
    elif passed or unsupported:
        state, cs_verdict = "incomplete", "partial"
    else:
        state, cs_verdict = "incomplete", "failed"

    evidence_refs = _ui_qa_evidence_refs(scenarios)
    if source_path:
        evidence_refs.append(source_path)

    summary_text = (
        f"ui-qa {overall}: {passed}/{total} passed, {failed} failed, "
        f"{unsupported} unsupported"
    )

    return CompletionState(
        state=state,
        verdict=cs_verdict,
        health="model:result",
        summary=summary_text,
        check_type="journey-verdict",
        run_dir=run_dir,
        review_status="ready" if overall == "pass" else "needs_evidence",
        checks_passed=passed,
        checks_warned=unsupported,
        checks_failed=failed,
        checks_total=total,
        scenarios_total=total,
        scenarios_started=passed + failed + unsupported,
        scenarios_blocked=len(required_failed),
        evidence_refs=sorted(set(evidence_refs)),
        blockers=blockers,
    )


def from_ui_review_verdict(
    verdict: dict[str, Any],
    *,
    source_path: str = "",
    run_dir: str = "",
) -> CompletionState:
    """Build a `ux-heuristic` completion state from a kitsoki-ui-review verdict.json.

    Shape (`.agents/skills/kitsoki-ui-review/SKILL.md` "verdict.json shape"):
    `{"overall": "pass"|"fail", "strict": bool, "summary": {"error", "warn",
    "info", "blocking", "by_source": {...}}, "findings": [{"source", "check",
    "severity": "error"|"warn"|"info", "surface", "frame", ...}]}`. Unlike
    ui-qa there is no per-scenario pass/fail — the gate is finding severity, so
    a `blocked` verdict follows any blocking/error-severity finding rather than
    a "required scenario" floor.
    """
    summary = verdict.get("summary", {}) or {}
    findings = verdict.get("findings", []) or []
    overall = verdict.get("overall", "fail")

    errors = int(summary.get("error", 0) or 0)
    warnings = int(summary.get("warn", 0) or 0)
    infos = int(summary.get("info", 0) or 0)
    blocking = int(summary.get("blocking", 0) or 0)
    total = errors + warnings + infos

    blockers = sorted(
        {
            f"{f.get('surface') or f.get('viewport') or 'finding'}: "
            f"{f.get('check') or 'ux-heuristic'}"
            for f in findings
            if f.get("severity") == "error"
        }
    )

    if overall == "pass":
        state, cs_verdict = "completed", "solved"
    elif blocking or errors:
        state, cs_verdict = "blocked", "blocked"
    elif warnings:
        state, cs_verdict = "incomplete", "partial"
    else:
        state, cs_verdict = "incomplete", "failed"

    evidence_refs = sorted({str(f["frame"]) for f in findings if f.get("frame")})
    if source_path:
        evidence_refs.append(source_path)

    summary_text = (
        f"ui-review {overall}: {errors} errors, {warnings} warnings, "
        f"{infos} info ({blocking} blocking)"
    )

    return CompletionState(
        state=state,
        verdict=cs_verdict,
        health="model:result",
        summary=summary_text,
        check_type="ux-heuristic",
        run_dir=run_dir,
        review_status="ready" if overall == "pass" else "needs_evidence",
        checks_passed=0,
        checks_warned=warnings,
        checks_failed=errors,
        checks_total=total,
        evidence_refs=sorted(set(evidence_refs)),
        blockers=blockers,
    )


def load_ui_qa_verdict(path: str | Path) -> CompletionState:
    """Read a kitsoki-ui-qa `verdict.json` off disk and adapt it."""
    p = Path(path)
    data = load_json_object(p)
    return from_ui_qa_verdict(data, source_path=str(p), run_dir=str(p.parent))


def load_ui_review_verdict(path: str | Path) -> CompletionState:
    """Read a kitsoki-ui-review `verdict.json` off disk and adapt it."""
    p = Path(path)
    data = load_json_object(p)
    return from_ui_review_verdict(data, source_path=str(p), run_dir=str(p.parent))


def _ui_qa_evidence_refs(scenarios: list[dict[str, Any]]) -> list[str]:
    refs: list[str] = []
    for scenario in scenarios:
        for step in scenario.get("steps", []) or []:
            for ev in step.get("evidence", []) or []:
                frame = ev.get("frame")
                if frame:
                    refs.append(str(frame))
    return refs

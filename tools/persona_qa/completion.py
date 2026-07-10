"""Shared completion-state contract for persona-QA runs.

The product-journey runner has rich native artifacts (`review.json`,
`validation`, `scenario-outcomes.json`, driver journals, decks). The arena needs
one stable, job-agnostic object it can score without knowing those artifact
details. This module is that bridge: product-journey, stories, MCP adapters, and
arena plugins can all exchange the same compact completion state.
"""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

from tools.completion_state import SCHEMA_VERSION, dumps_completion_state

STATES = ("completed", "incomplete", "blocked", "error")


@dataclass(frozen=True)
class CompletionState:
    """Normalized result for a persona-QA cell or run bundle.

    Conforms to the shared arena/persona-QA contract at
    schemas/completion-state.schema.json — `to_dict()`/`to_json()` are safe to
    hand to any consumer of that schema (e.g. an arena job-type plugin), and
    `schema_version` lets a consumer reject a payload written against a future,
    incompatible version of the contract.
    """

    state: str
    verdict: str
    health: str
    summary: str
    schema_version: str = SCHEMA_VERSION
    run_dir: str = ""
    deck_path: str = ""
    review_status: str = "not_reviewed"
    validation_status: str = "unknown"
    checks_passed: int = 0
    checks_warned: int = 0
    checks_failed: int = 0
    checks_total: int = 0
    scenarios_total: int = 0
    scenarios_started: int = 0
    scenarios_blocked: int = 0
    proof_minimum_evidence_count: int = 0
    minimum_evidence_count: int = 0
    evidence_refs: list[str] = field(default_factory=list)
    blockers: list[str] = field(default_factory=list)
    # Which proof class this state grades (schemas/completion-state.schema.json
    # `check_type`, WS-G G1). "" (the default) means "not applicable" — the
    # product-journey report bridge below never sets it (a run bundle IS the
    # journey, not a graded check over one). Adapters that grade an already-
    # judged artifact (e.g. a kitsoki-ui-qa/ui-review verdict.json) set this
    # explicitly to "journey-verdict"/"ux-heuristic" so an arena check-suite
    # consumer knows which check produced the verdict.
    check_type: str = ""

    def __post_init__(self) -> None:
        if self.state not in STATES:
            raise ValueError(f"unknown completion state {self.state!r}; want one of {STATES}")

    def to_dict(self) -> dict[str, Any]:
        data = asdict(self)
        if not self.check_type:
            # Omit rather than emit "" — mirrors CellResult.to_dict()'s
            # omit-the-default convention so a bare completion-state (no
            # declared check_type) stays byte-identical to pre-G6 payloads.
            data.pop("check_type", None)
        # The shared contract (schemas/completion-state.schema.json) requires a
        # `metrics` object on every completion-state, bugfix and persona-qa
        # alike. persona-qa's "metrics" are its check/scenario/proof counters —
        # nest them here so a generic consumer (e.g. an arena job-type plugin)
        # finds them at the same key regardless of workload; the flat fields
        # stay too, for callers that already read them directly.
        data["metrics"] = {
            "checks_passed": self.checks_passed,
            "checks_warned": self.checks_warned,
            "checks_failed": self.checks_failed,
            "checks_total": self.checks_total,
            "scenarios_total": self.scenarios_total,
            "scenarios_started": self.scenarios_started,
            "scenarios_blocked": self.scenarios_blocked,
            "proof_minimum_evidence_count": self.proof_minimum_evidence_count,
            "minimum_evidence_count": self.minimum_evidence_count,
        }
        return data

    def to_json(self) -> str:
        return dumps_completion_state(self.to_dict())


def from_product_journey_report(report: dict[str, Any]) -> CompletionState:
    """Build a completion state from a product-journey report or story summary."""

    review = report.get("review", {}) or {}
    validation = report.get("validation", {}) or {}
    run = report.get("run", {}) or {}
    scenario = report.get("scenario", {}) or {}
    scenario_id = scenario if isinstance(scenario, str) else scenario.get("id", "")

    review_status = (
        report.get("review_status")
        or review.get("review_status")
        or review.get("status")
        or "not_reviewed"
    )
    validation_status = (
        report.get("validation_status")
        or validation.get("status")
        or validation.get("run", {}).get("status")
        or "unknown"
    )
    counts = review.get("summary_counts", {}) or {}
    passed = int(report.get("review_passed", report.get("passed", review.get("review_passed_count", counts.get("passed", 0)))) or 0)
    warned = int(report.get("review_warnings", report.get("warnings", review.get("review_warning_count", counts.get("warned", 0)))) or 0)
    failed = int(report.get("review_failed", report.get("failed", review.get("review_failed_count", counts.get("failed", 0)))) or 0)
    total = int(report.get("review_total", report.get("total", review.get("review_total_count", counts.get("total", 0)))) or 0)

    scenario_summary = report.get("scenario_outcomes_summary", review.get("scenario_outcomes_summary", {})) or {}
    scenarios_total = int(scenario_summary.get("scenarios", 0) or 0)
    scenarios_started = int(scenario_summary.get("started", 0) or 0)
    scenarios_blocked = int(scenario_summary.get("blocked", 0) or 0)

    quality_gates = report.get("quality_gates") or review.get("quality_gates") or []
    proof_minimum = sum(int(gate.get("proof_minimum_evidence_count", 0) or 0) for gate in quality_gates)
    minimum = sum(int(gate.get("minimum_evidence_count", 0) or 0) for gate in quality_gates)
    blockers = _blockers(report, quality_gates)
    evidence_refs = _evidence_refs(report)

    if validation_status == "error" or report.get("status") == "failed" and not review_status:
        state = "error"
        verdict = "blocked"
        health = "infra:harness"
    elif review_status == "ready" and validation_status in {"valid", "unknown"}:
        state = "completed"
        verdict = "solved"
        health = "model:result"
    elif blockers and failed == 0 and scenarios_blocked:
        state = "blocked"
        verdict = "blocked"
        health = "model:result"
    else:
        state = "incomplete"
        verdict = "partial" if evidence_refs or scenarios_started or passed else "failed"
        health = "model:result"

    summary = _summary(
        state=state,
        review_status=review_status,
        validation_status=validation_status,
        passed=passed,
        total=total,
        warned=warned,
        failed=failed,
        scenario_id=scenario_id,
    )
    return CompletionState(
        state=state,
        verdict=verdict,
        health=health,
        summary=summary,
        run_dir=report.get("run_dir") or run.get("run_dir", ""),
        deck_path=report.get("deck_path") or run.get("deck_path", ""),
        review_status=review_status,
        validation_status=validation_status,
        checks_passed=passed,
        checks_warned=warned,
        checks_failed=failed,
        checks_total=total,
        scenarios_total=scenarios_total,
        scenarios_started=scenarios_started,
        scenarios_blocked=scenarios_blocked,
        proof_minimum_evidence_count=proof_minimum,
        minimum_evidence_count=minimum,
        evidence_refs=evidence_refs,
        blockers=blockers,
    )


def load_product_journey_run(run_dir: str | Path) -> CompletionState:
    """Load a run bundle from disk and derive its completion state."""

    path = Path(run_dir)
    report = {
        "run": {
            "run_dir": str(path),
            "deck_path": str(path / "deck.slidey.json"),
        }
    }
    for name, key in [
        ("review.json", "review"),
        ("scenario-outcomes.json", "scenario_outcomes"),
        ("driver-handoff.json", "driver_handoff"),
        ("evidence.json", "evidence"),
    ]:
        candidate = path / name
        if candidate.exists():
            report[key] = json.loads(candidate.read_text(encoding="utf-8"))
    if "scenario_outcomes" in report:
        report["scenario_outcomes_summary"] = report["scenario_outcomes"].get("summary", {})
    handoff = report.get("driver_handoff", {})
    status = handoff.get("status", {})
    if status:
        report["quality_gates"] = handoff.get("quality_gates", [])
        report["proof_minimum_evidence_count"] = status.get("proof_minimum_evidence_count", 0)
        report["minimum_evidence_count"] = status.get("minimum_evidence_count", 0)
    # `_evidence_refs` reads `report["attached_evidence"]` (an item's `path`/
    # `evidence_path`); a bundle loaded straight off disk never had that key
    # populated (it is normally built incrementally in-process by run.py's own
    # `attach_evidence` calls, never persisted under that name), which
    # silently made every disk-loaded CompletionState's `evidence_refs` empty
    # regardless of how much real evidence the bundle actually has on disk —
    # a real gap (not exercised by any existing test), not a policy that
    # "loaded from disk" should mean "no evidence". `evidence.json` (present
    # in every run bundle, see `attach_evidence`/`seed_demo_evidence` in
    # tools/product-journey/run.py) is the durable record of exactly the same
    # data; only carry forward items the bundle itself considers PRESENT
    # (captured/validated), mirroring `review_run_bundle`'s own
    # `present_items` filter, so a rejected/missing evidence slot is still
    # correctly absent from evidence_refs.
    evidence_items = report.get("evidence", {}).get("items", []) if isinstance(report.get("evidence"), dict) else []
    present = [item for item in evidence_items if item.get("status") in {"captured", "validated"} and item.get("path")]
    if present:
        report["attached_evidence"] = [{"path": item["path"]} for item in present]
    return from_product_journey_report(report)


def _blockers(report: dict[str, Any], quality_gates: list[dict[str, Any]]) -> list[str]:
    blockers = []
    for gate in quality_gates:
        if gate.get("blocked"):
            blockers.append(str(gate.get("scenario") or gate.get("label") or "blocked"))
    handoff = report.get("driver_handoff", {}) or {}
    for item in handoff.get("missing_proof_evidence", []) or []:
        scenario = item.get("scenario") or item.get("kind") or "missing-proof"
        blockers.append(f"missing proof: {scenario}")
    return sorted(set(blockers))


def _evidence_refs(report: dict[str, Any]) -> list[str]:
    refs = []
    for item in report.get("attached_evidence", []) or []:
        ref = item.get("path") or item.get("evidence_path")
        if ref:
            refs.append(str(ref))
    for item in report.get("driver_journal_events", []) or []:
        refs.extend(str(ref) for ref in item.get("evidence_refs", []) if ref)
    return sorted(set(refs))


_LEG_JUDGE_VERDICT_FROM_STATE_VERDICT = {
    "solved": "pass",
    "partial": "unsupported",
    "failed": "fail",
    "blocked": "degraded-evidence",
}
_LEG_DRIVE_STATUS_FROM_STATE = {
    "completed": "captured",
    "incomplete": "attempted",
    "blocked": "blocked",
    "error": "blocked",
}


def to_scenario_qa_leg_result(leg: dict[str, Any], state: CompletionState) -> dict[str, Any]:
    """Fold one arena persona-QA cell's `CompletionState` onto the
    `drive_result` / `judge_result` shape a scenario-qa transport leg expects
    (`schemas/drive_leg_result.json`, `schemas/judge_leg_result.json`).

    This is the P2.10 "parallel legs via arena" seam: stories/scenario-qa's
    serial loop drives one leg at a time through a real `product-journey-qa-
    driver` dispatch (execute.yaml) then an independent judge dispatch
    (judge.yaml), and `scripts/record_leg_result.star` folds the pair into
    `world.leg_results`. A parallel run instead scores each leg as an arena
    cell (`CompletionState`, the same contract `tools/arena/arena/plugins/
    persona_qa.py` already scores containerized cells from) and needs to
    reach the exact same `record_leg_result.star` input shape so report.md /
    deck.slidey.json are generated by identical code regardless of how the
    leg was driven — translate the two verdict vocabularies here, once, so
    every caller (a story's host.run glue script, a test, a future arena
    plugin) shares the same mapping instead of each re-deriving it.

    `leg` is accepted (and currently unused beyond documenting intent) so a
    future transport-aware cell can refine this mapping per-leg (e.g. using
    `leg["transport"]` or `leg["transport_evidence_contract"]`) without
    changing any call site's signature.

    Verdict vocabulary: CompletionState.verdict is one of solved/partial/
    failed/blocked (see `from_product_journey_report`); judge_leg_result's
    `verdict` is one of pass/fail/unsupported/degraded-evidence. "partial"
    maps to "unsupported" rather than a bare "fail" — some real evidence
    exists but the run bundle was not fully ready, the same posture
    kitsoki-ui-qa uses for a claim it cannot fully ground. CompletionState.
    state is one of completed/incomplete/blocked/error; drive_leg_result's
    `status` is one of attempted/captured/blocked/degraded-evidence — "error"
    (an infra failure, e.g. a crashed replay-smoke subprocess) maps to
    "blocked" like a genuine blocker, never silently to "captured".

    Nothing here fabricates evidence: `evidence_refs`/`blockers` are carried
    through verbatim from the CompletionState so record_leg_result.star's
    existing `_lacks_evidence`/`_cause` backstops apply unchanged — a leg
    with no real evidence_refs still degrades honestly.
    """

    judge_verdict = _LEG_JUDGE_VERDICT_FROM_STATE_VERDICT.get(state.verdict, "degraded-evidence")
    drive_status = _LEG_DRIVE_STATUS_FROM_STATE.get(state.state, "blocked")
    drive_result = {
        "status": drive_status,
        "evidence_refs": list(state.evidence_refs),
        "blockers": list(state.blockers),
        # This fold never runs a live agent dispatch — see
        # run_scenario_qa_legs_parallel.py's module docstring for the scope
        # note (cassette/replay proof only, zero LLM spend by construction).
        "harness_used": "replay",
        "summary": state.summary,
    }
    judge_result = {
        "verdict": judge_verdict,
        "summary": state.summary,
        # No frame-level evidence is produced by the transport-blind replay
        # path this fold sources from today; leave honestly empty rather than
        # fabricate a citation record_leg_result.star's report table doesn't
        # actually have.
        "cited_frames": [],
    }
    return {"drive_result": drive_result, "judge_result": judge_result}


def _summary(
    *,
    state: str,
    review_status: str,
    validation_status: str,
    passed: int,
    total: int,
    warned: int,
    failed: int,
    scenario_id: str,
) -> str:
    scenario = f" scenario={scenario_id};" if scenario_id else ""
    checks = f"{passed}/{total}" if total else "0/0"
    return (
        f"{state}:{scenario} review={review_status}; validation={validation_status}; "
        f"checks={checks} passed, {warned} warnings, {failed} failures"
    )

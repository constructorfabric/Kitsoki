"""Check-suite execution (WS-G G1): per-cell verdicts per check_type.

A cell's job spec declares a check SUITE (`JobSpec.checks`), not one verdict
shape. Every check emits one `CellResult` tagged with its `check_type`
(schemas/completion-state.schema.json's discriminator; absent == "replay"), so
the matrix rollup aggregates one verdict per cell per check_type.

Three execution strategies exist today:

- `replay` — the existing plugin/container path (`CellExecutor.execute`).
- `journey-verdict` / `ux-heuristic` (WS-G G6, FILE_ADAPTER_CHECK_TYPES) — a
  file-adapter check: it reads an already-written kitsoki-ui-qa /
  kitsoki-ui-review `verdict.json` off disk (path from the check's
  `options.verdict_path`) and adapts it via
  `tools.persona_qa.ui_verdict` into a completion-state. No container spawn,
  no LLM call of its own — the judging already happened; this only folds its
  verdict back into the arena rollup. If no path is configured, or the
  configured path doesn't exist yet, the result is an honest `pending` — never
  a fake green — exactly like a declared-but-unimplemented check type.
- everything else (`docs-fidelity`) — declared-but-not-implemented: accepted
  at spec-validation time, reports an honest `pending` at execution time,
  never touches disk or a container (per the spark-quota / honest-PENDING
  discipline).
"""

from __future__ import annotations

import sys
from pathlib import Path

_REPO_ROOT_FOR_IMPORTS = Path(__file__).resolve().parents[3]
if str(_REPO_ROOT_FOR_IMPORTS) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT_FOR_IMPORTS))

from tools.completion_state import CompletionStateError

from .artifact_adapters import adapt_artifact
from .completion_state import apply_completion_state
from .executor import CellExecutor
from .model import (
    Cell,
    CellResult,
    CheckSpec,
    DEFAULT_CHECK_TYPE,
    _resolve_repo_path,
)

# Check types whose grade comes from reading an already-written verdict.json
# off disk (WS-G G6) rather than spawning a container. Keyed by check_type ->
# the artifact adapter that understands that verdict.json's native shape.
_FILE_ADAPTER_KINDS = {
    "journey-verdict": "ui-qa-verdict",
    "ux-heuristic": "ui-review-verdict",
}
FILE_ADAPTER_CHECK_TYPES = tuple(_FILE_ADAPTER_KINDS)


def unimplemented_check_result(cell: Cell, check_type: str) -> CellResult:
    """The honest grade for a declared-but-not-yet-executable check type.

    `pending` = "the grade was never run" (excluded from solve-rate
    denominators per the schema); `incomplete` health = not a model result and
    not an infra breakage — so the placement scheduler never burns retries on
    it and the rollup never scores it against a variant.
    """
    return CellResult(
        cell_id=cell.id,
        job_type=cell.job_type,
        target_id=cell.target.id,
        variant_id=cell.variant.id,
        axis=dict(cell.axis),
        verdict="pending",
        health="incomplete",
        check_type=check_type,
        notes=(
            f"check_type '{check_type}' declared but not implemented yet; "
            "honest PENDING (never fake green)"
        ),
    )


def run_ui_verdict_check(cell: Cell, check: CheckSpec) -> CellResult:
    """Grade a `journey-verdict`/`ux-heuristic` check from a verdict.json (WS-G G6).

    Never spawns a container or an LLM — the judging skill already produced
    `verdict.json`; this only reads it and folds it into the arena rollup via
    `tools.persona_qa.ui_verdict`. The path is `check.options["verdict_path"]`
    (repo-relative or absolute, same convention as `JobSpec`'s corpus paths).
    Honest `pending` — never a fake green — when no path is configured, the
    configured path doesn't exist yet, or the file can't be parsed as the
    expected verdict.json shape; only a genuinely present, well-formed
    artifact is graded.
    """
    result = CellResult(
        cell_id=cell.id,
        job_type=cell.job_type,
        target_id=cell.target.id,
        variant_id=cell.variant.id,
        axis=dict(cell.axis),
        check_type=check.check_type,
    )
    adapter_kind = _FILE_ADAPTER_KINDS.get(check.check_type)
    if adapter_kind is None:
        raise ValueError(f"no artifact adapter registered for check_type {check.check_type!r}")

    verdict_path = check.options.get("verdict_path") or check.options.get("verdict")
    if not verdict_path:
        result.verdict = "pending"
        result.health = "incomplete"
        result.notes = (
            f"check_type '{check.check_type}' has no verdict_path configured; "
            "honest PENDING (never fake green)"
        )
        return result

    path = _resolve_repo_path(verdict_path)
    if not path.exists():
        result.verdict = "pending"
        result.health = "incomplete"
        result.notes = (
            f"no verdict.json found at {path} for check_type '{check.check_type}'; "
            "honest PENDING (never fake green)"
        )
        return result

    try:
        payload = adapt_artifact(adapter_kind, path)
    except (OSError, ValueError, KeyError, CompletionStateError) as exc:
        result.verdict = "blocked"
        result.health = "infra:completion-state-malformed"
        result.notes = f"could not load verdict.json at {path}: {exc}"
        return result

    return apply_completion_state(result, payload)


def run_cell_checks(
    cell: Cell,
    executor: CellExecutor,
    checks: list[CheckSpec],
    *,
    host: str = "local",
    live: bool = False,
) -> list[CellResult]:
    """Run one cell's declared check suite → one CellResult per check_type.

    The replay check delegates to the existing container path (no behavior
    change when the suite is the default `[replay]`); `journey-verdict`/
    `ux-heuristic` delegate to `run_ui_verdict_check` (a disk read, never a
    container); every other type yields `unimplemented_check_result`. Note:
    callers that need INFRA retry around the replay check (the placement
    scheduler) drive `executor.execute` themselves and dispatch the rest via
    this same per-check_type logic — this helper is the single-cell, no-retry
    composition.
    """
    results: list[CellResult] = []
    for check in checks:
        if check.check_type == DEFAULT_CHECK_TYPE:
            result = executor.execute(cell, host=host, live=live)
            result.check_type = DEFAULT_CHECK_TYPE
            results.append(result)
        elif check.check_type in FILE_ADAPTER_CHECK_TYPES:
            results.append(run_ui_verdict_check(cell, check))
        else:
            results.append(unimplemented_check_result(cell, check.check_type))
    return results

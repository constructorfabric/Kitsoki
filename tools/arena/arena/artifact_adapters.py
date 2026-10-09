"""Artifact adapters that normalize producer outputs to completion-state.

Plugins locate artifacts; adapters understand their native shape and return the
shared completion-state payload. Arena rollup consumers then copy the normalized
payload into `CellResult` without knowing whether the artifact came from a
bugfix oracle, swarm run, UI verdict, or product-journey bundle.
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any, Callable

_REPO_ROOT_FOR_IMPORTS = Path(__file__).resolve().parents[3]
if str(_REPO_ROOT_FOR_IMPORTS) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT_FOR_IMPORTS))

from tools.completion_state import (
    build_completion_state,
    load_completion_state,
    load_json_object,
)

from .model import REPO_ROOT

ArtifactAdapter = Callable[[Path], dict[str, Any]]


def adapt_artifact(kind: str, path: str | Path) -> dict[str, Any]:
    """Load `path` with the registered adapter for `kind`."""
    try:
        adapter = _ADAPTERS[kind]
    except KeyError as exc:
        raise ValueError(f"unknown artifact adapter kind {kind!r}") from exc
    return adapter(Path(path))


def adapt_completion_state(path: str | Path) -> dict[str, Any]:
    return load_completion_state(path)


def adapt_swarm_results(path: str | Path) -> dict[str, Any]:
    return completion_from_swarm_results(load_json_object(path), str(path))


def adapt_ui_qa_verdict(path: str | Path) -> dict[str, Any]:
    if str(REPO_ROOT) not in sys.path:
        sys.path.insert(0, str(REPO_ROOT))
    from tools.persona_qa import load_ui_qa_verdict

    return load_ui_qa_verdict(path).to_dict()


def adapt_ui_review_verdict(path: str | Path) -> dict[str, Any]:
    if str(REPO_ROOT) not in sys.path:
        sys.path.insert(0, str(REPO_ROOT))
    from tools.persona_qa import load_ui_review_verdict

    return load_ui_review_verdict(path).to_dict()


def adapt_product_journey_review(path: str | Path) -> dict[str, Any]:
    if str(REPO_ROOT) not in sys.path:
        sys.path.insert(0, str(REPO_ROOT))
    from tools.persona_qa import load_product_journey_run

    return load_product_journey_run(path).to_dict()


def completion_from_swarm_results(data: dict[str, Any], results_path: str) -> dict[str, Any]:
    """Normalize tools/swarm `SwarmResults` JSON to completion-state."""
    user_count = int(data.get("user_count", 0) or 0)
    users = data.get("users") or []
    all_completed = bool(data.get("all_completed"))
    all_isolated = bool(data.get("all_isolated"))
    all_console_clean = bool(data.get("all_console_clean"))
    all_audit_clean = bool(data.get("all_audit_clean"))

    negative_control = data.get("negative_control") or {}
    negative_control_ran = bool(negative_control.get("shared_session_id"))
    negative_control_ok = (not negative_control_ran) or bool(negative_control.get("detected"))

    completed_count = sum(1 for user in users if user.get("completed"))
    isolated_count = sum(1 for user in users if user.get("isolation_ok"))
    console_clean_count = sum(1 for user in users if user.get("console_errors", 0) == 0)
    audit_clean_count = sum(1 for user in users if user.get("audit_error_count", 0) == 0)

    if all_completed and all_isolated and all_console_clean and all_audit_clean and negative_control_ok:
        verdict = "solved"
        state = "completed"
    elif not negative_control_ok or completed_count == 0:
        verdict = "failed"
        state = "incomplete"
    else:
        verdict = "partial"
        state = "incomplete"

    metrics = {
        "user_count": user_count,
        "completed_count": completed_count,
        "isolated_count": isolated_count,
        "console_clean_count": console_clean_count,
        "audit_clean_count": audit_clean_count,
        "all_completed": all_completed,
        "all_isolated": all_isolated,
        "all_console_clean": all_console_clean,
        "all_audit_clean": all_audit_clean,
        "negative_control_ran": negative_control_ran,
        "negative_control_detected": bool(negative_control.get("detected")),
    }
    summary = (
        f"{verdict}: {completed_count}/{user_count} completed, "
        f"{isolated_count}/{user_count} isolated, "
        f"{console_clean_count}/{user_count} console-clean, "
        f"{audit_clean_count}/{user_count} audit-clean"
        + ("" if negative_control_ok else "; negative control FAILED to detect the seeded cross-talk fault")
    )
    return build_completion_state(
        state=state,
        verdict=verdict,
        health="model:result",
        summary=summary,
        metrics=metrics,
        evidence_refs=[results_path],
        trace_ref=results_path,
    )


_ADAPTERS: dict[str, ArtifactAdapter] = {
    "completion-state": adapt_completion_state,
    "swarm-results": adapt_swarm_results,
    "ui-qa-verdict": adapt_ui_qa_verdict,
    "ui-review-verdict": adapt_ui_review_verdict,
    "product-journey-review": adapt_product_journey_review,
}

"""Arena-specific helpers for applying completion-state payloads to results."""

from __future__ import annotations

from typing import Any

from .model import CellResult


def apply_completion_state(result: CellResult, payload: dict[str, Any]) -> CellResult:
    """Copy a validated completion-state payload onto a `CellResult`."""
    result.verdict = str(payload["verdict"])
    result.health = str(payload["health"])
    result.metrics = dict(payload.get("metrics") or {})
    result.evidence_refs = list(payload.get("evidence_refs") or [])
    result.trace_ref = str(payload.get("trace_ref", "") or payload.get("run_dir", "") or "")
    result.notes = str(payload.get("notes") or payload.get("summary") or "")
    check_type = payload.get("check_type")
    if check_type:
        result.check_type = str(check_type)
    return result

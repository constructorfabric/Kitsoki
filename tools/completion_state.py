"""Shared completion-state JSON helpers.

The completion-state contract is the common result shape used by arena,
persona-QA, and bugfix-bakeoff producers. This module intentionally has no
arena imports so producers can share validation and deterministic JSON writing
without depending on a job runner.
"""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

SCHEMA_VERSION = "1.0.0"
VERDICTS = ("solved", "partial", "failed", "armed", "blocked", "pending")
HEALTH_RE = re.compile(r"^(infra:[a-z0-9_-]+|model:result|incomplete)$")
REQUIRED_FIELDS = ("schema_version", "verdict", "health", "metrics", "evidence_refs")


class CompletionStateError(ValueError):
    """Raised when a completion-state file exists but violates the contract."""


def build_completion_state(
    *,
    verdict: str,
    health: str,
    metrics: dict[str, Any] | None = None,
    evidence_refs: list[str] | None = None,
    schema_version: str = SCHEMA_VERSION,
    **extra: Any,
) -> dict[str, Any]:
    """Build a schema-compatible completion-state payload."""
    payload: dict[str, Any] = {
        "schema_version": schema_version,
        "verdict": verdict,
        "health": health,
        "metrics": metrics or {},
        "evidence_refs": evidence_refs or [],
    }
    payload.update({k: v for k, v in extra.items() if v is not None})
    validate_completion_state(payload)
    return payload


def dumps_completion_state(payload: dict[str, Any]) -> str:
    """Serialize completion-state JSON deterministically."""
    validate_completion_state(payload)
    return json.dumps(payload, indent=2, sort_keys=True) + "\n"


def write_completion_state(
    path: str | Path | None,
    *,
    verdict: str,
    health: str,
    metrics: dict[str, Any] | None = None,
    evidence_refs: list[str] | None = None,
    **extra: Any,
) -> dict[str, Any] | None:
    """Write a completion-state file, returning the payload written.

    A `None` path is a no-op so callers can pass optional CLI values through
    without conditionals.
    """
    if not path:
        return None
    payload = build_completion_state(
        verdict=verdict,
        health=health,
        metrics=metrics,
        evidence_refs=evidence_refs,
        **extra,
    )
    out_path = Path(path)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(dumps_completion_state(payload), encoding="utf-8")
    return payload


def load_json_object(path: str | Path) -> dict[str, Any]:
    """Load a JSON object from disk with a consistent shape error."""
    p = Path(path)
    try:
        data = json.loads(p.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise CompletionStateError(f"{p} is not valid JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise CompletionStateError(f"{p} must contain a JSON object")
    return data


def load_completion_state(path: str | Path) -> dict[str, Any]:
    """Load and validate a completion-state file."""
    payload = load_json_object(path)
    try:
        validate_completion_state(payload)
    except CompletionStateError as exc:
        raise CompletionStateError(f"{Path(path)} {exc}") from exc
    return payload


def validate_completion_state(payload: dict[str, Any]) -> None:
    """Validate the fields shared by every completion-state producer."""
    missing = [field for field in REQUIRED_FIELDS if field not in payload]
    if missing:
        raise CompletionStateError(f"missing field(s): {', '.join(missing)}")
    if payload["verdict"] not in VERDICTS:
        raise CompletionStateError(f"has unknown verdict {payload['verdict']!r}")
    if not HEALTH_RE.match(str(payload["health"])):
        raise CompletionStateError(f"has unrecognized health {payload['health']!r}")
    if not isinstance(payload.get("metrics"), dict) or not isinstance(payload.get("evidence_refs"), list):
        raise CompletionStateError("has malformed metrics/evidence_refs")


def infra_completion_state(
    *,
    health: str,
    notes: str,
    metrics: dict[str, Any] | None = None,
    evidence_refs: list[str] | None = None,
) -> dict[str, Any]:
    """Build a blocked infra completion-state for malformed/missing artifacts."""
    return build_completion_state(
        verdict="blocked",
        health=health,
        metrics=metrics or {},
        evidence_refs=evidence_refs or [],
        notes=notes,
    )

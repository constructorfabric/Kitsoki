"""Strict, offline validation for task-optimization scored-attempt receipts.

The campaign scheduler must never promote a candidate from a caller-authored
``verdict: solved`` alone.  This module binds a scored cell to immutable local
evidence produced by the execution/scoring path.  It deliberately consumes the
stable JSON report emitted by ``kitsoki agent-bench score`` rather than
reimplementing trace accounting in Arena.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any


TASK_OPTIMIZATION_SCORE_SCHEMA = "task-optimization/score/v1"


def _sha256_path(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _mapping(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError(f"scored receipt {label} must be an object")
    return value


def _string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"scored receipt {label} must be a non-empty string")
    return value


def _timestamp(value: Any, label: str) -> datetime:
    text = _string(value, label)
    try:
        parsed = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"scored receipt {label} must be an ISO-8601 timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"scored receipt {label} must include a timezone")
    return parsed.astimezone(timezone.utc)


def _evidence_file(value: Any, label: str, *, attempt_id: str, started: datetime,
                   finished: datetime, receipt_dir: Path) -> tuple[Path, dict[str, Any]]:
    evidence = _mapping(value, label)
    raw_path = Path(_string(evidence.get("path"), f"{label}.path"))
    path = raw_path if raw_path.is_absolute() else (receipt_dir / raw_path)
    path = path.resolve()
    if not path.is_file():
        raise ValueError(f"scored receipt {label}.path does not name a regular file: {path}")
    expected_digest = _string(evidence.get("sha256"), f"{label}.sha256")
    if _sha256_path(path) != expected_digest:
        raise ValueError(f"scored receipt {label} sha256 does not match artifact bytes")
    if evidence.get("attempt_id") != attempt_id:
        raise ValueError(f"scored receipt {label}.attempt_id does not match attempt_id")
    produced = _timestamp(evidence.get("produced_at"), f"{label}.produced_at")
    if produced < started or produced > finished:
        raise ValueError(f"scored receipt {label}.produced_at falls outside the terminal run window")
    # A digest proves bytes did not change after recording; this mtime window
    # additionally prevents an old, previously passing artifact from being
    # relabelled as a fresh attempt.  A small skew allowance covers coarse
    # filesystem timestamp resolution at the container/host boundary.
    mtime = datetime.fromtimestamp(path.stat().st_mtime, tz=timezone.utc)
    skew = timedelta(seconds=2)
    if mtime < started - skew or mtime > finished + skew:
        raise ValueError(f"scored receipt {label} file mtime falls outside the terminal run window")
    return path, evidence


def _nonnegative_number(value: Any, label: str) -> float:
    if not isinstance(value, (int, float)) or isinstance(value, bool) or value < 0:
        raise ValueError(f"scored receipt {label} must be a non-negative number")
    return float(value)


def _require_equal(receipt_value: Any, report_value: Any, label: str) -> None:
    if receipt_value != report_value:
        raise ValueError(f"scored receipt {label} does not match the AgentBench report")


def validate_scored_attempt_receipt(receipt: dict[str, Any], *, receipt_path: str | Path,
                                    preflight_candidate: dict[str, Any],
                                    requires_codeact_runtime: bool = False) -> None:
    """Reject incomplete, stale, or caller-forged evidence for a scored cell.

    Non-scored scheduler states are not execution claims and remain validated by
    the surrounding attempt receipt loader.  Every ``status: scored`` receipt,
    including a failed result, carries the full evidence bundle so aggregate
    token/cost comparisons cannot mix exact and guessed measurements.
    """
    if receipt.get("status") != "scored":
        return
    attempt_id = _string(receipt.get("attempt_id"), "attempt_id")
    verdict = receipt.get("verdict")
    if verdict not in {"solved", "partial", "failed"}:
        raise ValueError("scored receipt verdict must be solved, partial, or failed")
    receipt_dir = Path(receipt_path).resolve().parent

    runtime = _mapping(receipt.get("runtime"), "runtime")
    if runtime.get("status") != "exited":
        raise ValueError("scored receipt runtime.status must be exited")
    if not isinstance(runtime.get("exit_code"), int) or isinstance(runtime.get("exit_code"), bool):
        raise ValueError("scored receipt runtime.exit_code must be an integer")
    _string(runtime.get("runner_commit"), "runtime.runner_commit")
    _string(runtime.get("image_digest"), "runtime.image_digest")
    started = _timestamp(runtime.get("started_at"), "runtime.started_at")
    finished = _timestamp(runtime.get("finished_at"), "runtime.finished_at")
    if finished < started:
        raise ValueError("scored receipt runtime.finished_at precedes runtime.started_at")

    boundary = _mapping(receipt.get("boundary"), "boundary")
    _string(boundary.get("capability_hash"), "boundary.capability_hash")
    _string(boundary.get("sandbox_kind"), "boundary.sandbox_kind")
    _string(boundary.get("sandbox_identity"), "boundary.sandbox_identity")
    _require_equal(boundary.get("profile_hash"), preflight_candidate.get("profile_hash"), "boundary.profile_hash")
    _require_equal(boundary.get("launch_plan_hash"), preflight_candidate.get("launch_plan_hash"), "boundary.launch_plan_hash")

    leakage = _mapping(receipt.get("leakage"), "leakage")
    if leakage.get("verdict") != "clean":
        raise ValueError("scored receipt leakage.verdict must be clean")
    _string(leakage.get("checker"), "leakage.checker")
    _string(leakage.get("policy_hash"), "leakage.policy_hash")

    artifacts = _mapping(receipt.get("artifacts"), "artifacts")
    report_path, report_ref = _evidence_file(artifacts.get("agentbench_report"), "artifacts.agentbench_report",
                                              attempt_id=attempt_id, started=started, finished=finished, receipt_dir=receipt_dir)
    trace_path, _ = _evidence_file(artifacts.get("trace"), "artifacts.trace", attempt_id=attempt_id,
                                   started=started, finished=finished, receipt_dir=receipt_dir)
    oracle_path, oracle_ref = _evidence_file(artifacts.get("oracle"), "artifacts.oracle", attempt_id=attempt_id,
                                               started=started, finished=finished, receipt_dir=receipt_dir)
    suite_path, suite_ref = _evidence_file(artifacts.get("suite"), "artifacts.suite", attempt_id=attempt_id,
                                             started=started, finished=finished, receipt_dir=receipt_dir)
    if not isinstance(oracle_ref.get("passed"), bool) or not isinstance(suite_ref.get("passed"), bool):
        raise ValueError("scored receipt oracle and suite artifacts require boolean passed verdicts")
    if verdict == "solved" and (not oracle_ref["passed"] or not suite_ref["passed"]):
        raise ValueError("solved receipt requires passing oracle and suite artifacts")

    score = _mapping(receipt.get("score"), "score")
    if score.get("schema") != TASK_OPTIMIZATION_SCORE_SCHEMA:
        raise ValueError("scored receipt score has unsupported schema")
    _require_equal(score.get("agentbench_report_sha256"), report_ref.get("sha256"), "score.agentbench_report_sha256")
    _require_equal(score.get("trace_sha256"), _sha256_path(trace_path), "score.trace_sha256")
    try:
        report = json.loads(report_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ValueError("scored receipt AgentBench report must be JSON") from exc
    report = _mapping(report, "AgentBench report")
    report_metrics = _mapping(report.get("metrics"), "AgentBench report.metrics")
    if report.get("trace") and Path(str(report["trace"])).resolve() != trace_path:
        raise ValueError("scored receipt trace artifact does not match AgentBench report trace")
    if report_metrics.get("accounting_status") != "complete":
        raise ValueError("scored receipt AgentBench accounting_status must be complete")
    if requires_codeact_runtime and report_metrics.get("runtime_accounting_status") not in {"complete", "direct_api"}:
        raise ValueError("scored CodeAct receipt requires complete AgentBench runtime receipts")
    started_calls = report_metrics.get("agent_calls_started")
    finished_calls = report_metrics.get("agent_calls_finished")
    errored_calls = report_metrics.get("agent_calls_errored")
    in_flight = report_metrics.get("agent_calls_in_flight")
    if not all(isinstance(value, int) and not isinstance(value, bool) and value >= 0
               for value in (started_calls, finished_calls, errored_calls, in_flight)):
        raise ValueError("scored receipt AgentBench agent call lifecycle metrics must be non-negative integers")
    if started_calls < 1 or in_flight != 0 or finished_calls + errored_calls != started_calls:
        raise ValueError("scored receipt AgentBench agent call lifecycle is not terminal and reconciled")

    metrics = _mapping(receipt.get("metrics"), "metrics")
    metric_pairs = {
        "input_tokens": "input_tokens",
        "output_tokens": "output_tokens",
        "cache_creation_input_tokens": "cache_creation_input_tokens",
        "cache_read_input_tokens": "cache_read_input_tokens",
        "total_tokens": "total_tokens",
        "cost_usd": "cost_usd",
        "wall_s": "wall_seconds",
    }
    for receipt_key, report_key in metric_pairs.items():
        _nonnegative_number(metrics.get(receipt_key), f"metrics.{receipt_key}")
        _require_equal(metrics.get(receipt_key), report_metrics.get(report_key), f"metrics.{receipt_key}")
    if metrics["total_tokens"] <= 0:
        raise ValueError("scored receipt metrics.total_tokens must be positive")
    if verdict == "solved":
        if report.get("outcome") != "solved" or report.get("passed") is not True:
            raise ValueError("solved receipt must be a passing solved AgentBench report")
    elif report.get("outcome") != verdict:
        raise ValueError("scored receipt verdict does not match AgentBench outcome")

    # Keep references live while making lint/type checkers and future callers
    # see that the oracle/suite bytes themselves were checked above.
    del oracle_path, suite_path

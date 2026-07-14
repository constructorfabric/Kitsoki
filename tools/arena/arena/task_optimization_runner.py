"""Fail-closed scheduler for immutable task-optimization campaign cells.

This is deliberately a small orchestration seam.  It selects only cells which
the lifecycle reducer says may resume, creates a per-attempt lease/workspace,
paces provider dispatches, and accepts an *already scored* receipt from an
injected executor.  It does not invent model verdicts or duplicate AgentBench
scoring: every scored result still passes the canonical receipt contract.
"""

from __future__ import annotations

import json
import os
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

from .model import canonical_json, load_task_optimization_receipts, receipt_sha256, task_optimization_status
from .task_optimization_receipt import validate_scored_attempt_receipt


Executor = Callable[[dict[str, Any], dict[str, Any]], dict[str, Any]]


def _write_once(path: Path, value: dict[str, Any]) -> None:
    payload = canonical_json(value)
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    except FileExistsError:
        if path.read_bytes() != payload:
            raise ValueError(f"immutable receipt already exists with different bytes: {path}")
        return
    with os.fdopen(fd, "wb") as out:
        out.write(payload)
        out.flush()
        os.fsync(out.fileno())


def _ready_candidates(preflight: dict[str, Any]) -> dict[str, dict[str, Any]]:
    return {str(item.get("candidate_id")): item for item in preflight.get("candidates", [])
            if isinstance(item, dict) and item.get("status") == "ready"}


def _attempt_receipt(*, plan: dict[str, Any], preflight: dict[str, Any], cell: dict[str, Any],
                     attempt_id: str, result: dict[str, Any]) -> dict[str, Any]:
    """Bind executor output to frozen inputs; executors cannot choose identity."""
    receipt = dict(result)
    receipt.update({
        "schema": "task-optimization/attempt/v1",
        "study_id": plan["study_id"],
        "attempt_id": attempt_id,
        "cell_id": cell["id"],
        "candidate_id": cell["candidate_id"],
        "plan_sha256": receipt_sha256(plan),
        "preflight_sha256": str(preflight.get("preflight_sha256") or receipt_sha256(preflight)),
    })
    return receipt


@dataclass(frozen=True)
class RunSummary:
    dispatched: list[str]
    skipped: list[dict[str, str]]
    dry_run: bool

    def to_dict(self) -> dict[str, Any]:
        return {"schema": "task-optimization/run/v1", "dispatched_cell_ids": self.dispatched,
                "skipped": self.skipped, "dry_run": self.dry_run}


def run(
    *, plan: dict[str, Any], preflight: dict[str, Any], attempts_dir: str | Path,
    out_dir: str | Path, live: bool, executor: Executor | None = None,
    max_cells: int = 0, sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
) -> RunSummary:
    """Schedule frozen cells, or write a no-spend dispatch preview by default.

    ``executor`` is a dependency-injected treatment adapter.  In production the
    CLI adapter invokes an explicitly supplied command.  This keeps the Arena
    scheduler independently deterministic and lets tests use a fake executor.
    """
    if plan.get("schema") != "task-optimization/v1":
        raise ValueError("plan has unsupported task-optimization schema")
    if preflight.get("schema") != "task-optimization/preflight/v1":
        raise ValueError("preflight has unsupported task-optimization schema")
    if preflight.get("study_id") != plan.get("study_id"):
        raise ValueError("preflight study_id does not match immutable plan")
    if live and os.environ.get(str(plan.get("live_gate_env") or "KITSOKI_TASK_OPT_LIVE")) != "1":
        raise ValueError("live task-optimization dispatch requires explicit --live and current live gate environment")
    if live and executor is None:
        raise ValueError("live task-optimization dispatch requires an explicit executor; no provider was called")

    attempts = Path(attempts_dir)
    out = Path(out_dir)
    existing = load_task_optimization_receipts(attempts, plan=plan, preflight=preflight)
    status = task_optimization_status(plan, existing)
    by_id = {str(cell["id"]): cell for cell in status["cells"]}
    ready = _ready_candidates(preflight)
    selected = [by_id[cell_id] for cell_id in status["resume_cell_ids"] if by_id[cell_id]["candidate_id"] in ready]
    skipped = [{"cell_id": cell_id, "reason": "candidate preflight is not ready"}
               for cell_id in status["resume_cell_ids"] if by_id[cell_id]["candidate_id"] not in ready]
    if max_cells > 0:
        selected = selected[:max_cells]

    # A dry preview is deliberately not an attempt receipt: it has not crossed
    # an execution/scoring boundary and must never alter resume state.
    if not live:
        summary = RunSummary([str(cell["id"]) for cell in selected], skipped, True)
        out.mkdir(parents=True, exist_ok=True)
        (out / "dispatch.json").write_bytes(canonical_json({**summary.to_dict(), "cells": selected,
            "provider_dispatched": False, "reason": "dry dispatch; pass --live plus gate to execute"}))
        return summary

    retry_budget = int((plan.get("retry_policy") or {}).get("max_infra_retries", 2))
    dispatched: list[str] = []
    last_provider_dispatch: dict[str, float] = {}
    for cell in selected:
        candidate = ready[str(cell["candidate_id"])]
        provider = str(candidate.get("provider") or candidate.get("backend") or "unknown")
        pacing = candidate.get("pacing") if isinstance(candidate.get("pacing"), dict) else {}
        minimum = float(pacing.get("min_interval_s", 0) or 0)
        elapsed = clock() - last_provider_dispatch.get(provider, float("-inf"))
        if minimum > elapsed:
            sleep(minimum - elapsed)
        # Retry only an explicitly classified infra result.  Each retry owns a
        # distinct path and immutable lease; no shared candidate checkout exists.
        for retry in range(retry_budget + 1):
            attempt_id = f"{cell['id']}--{uuid.uuid4().hex[:12]}"
            workspace = out / "workspaces" / str(cell["id"]) / attempt_id
            workspace.mkdir(parents=True, exist_ok=False)
            lease = attempts / ".leases" / f"{attempt_id}.json"
            request = {"schema": "task-optimization/dispatch/v1", "attempt_id": attempt_id,
                       "cell": cell, "candidate": candidate, "workspace": str(workspace),
                       "lease": str(lease), "retry": retry}
            _write_once(lease, request)
            request_path = workspace / "dispatch-request.json"
            request_path.write_bytes(canonical_json(request))
            request["request_path"] = str(request_path)
            result = executor(cell, request)
            if not isinstance(result, dict):
                raise ValueError("task-optimization executor must return an attempt receipt object")
            receipt = _attempt_receipt(plan=plan, preflight=preflight, cell=cell, attempt_id=attempt_id, result=result)
            if receipt.get("status") == "scored":
                validate_scored_attempt_receipt(
                    receipt, receipt_path=workspace / "receipt.json", preflight_candidate=candidate,
                    requires_codeact_runtime="codeact" in str(cell.get("treatment") or ""),
                )
            # One attempt owns one directory. The receipt is written once and
            # never replaced, so provider retries/resumes cannot inherit or
            # overwrite another attempt's evidence.
            attempt_dir = attempts / str(cell["id"]) / attempt_id
            destination = attempt_dir / "receipt.json"
            _write_once(destination, receipt)
            dispatched.append(str(cell["id"]))
            last_provider_dispatch[provider] = clock()
            infra = receipt.get("status") == "blocked" and (receipt.get("retryable") is True or str(receipt.get("health") or "").startswith("infra:"))
            if not infra:
                break
    summary = RunSummary(dispatched, skipped, False)
    out.mkdir(parents=True, exist_ok=True)
    (out / "dispatch.json").write_bytes(canonical_json({**summary.to_dict(), "provider_dispatched": bool(dispatched)}))
    return summary

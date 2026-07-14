"""paired-task job-type plugin.

Runs one frozen task through multiple treatments (for example kitsoki,
single-briefed, single-naive), then grades every arm with the same oracle output
shape. The no-LLM path arms/scores fixture output; the live path constructs the
later paid driver command but is never used by the deterministic gate.
"""

from __future__ import annotations

import json
import re
from typing import Any

from ..model import Cell, CellResult
from . import base

KITSOKI_MNT = "/workspace/kitsoki"
RUNNER = f"{KITSOKI_MNT}/tools/arena/lib/paired_task_runner.py"
_INFRA_RE = re.compile(
    r"no such tool|worker.never.ran|host[_-]error|connection refused|provider 5\d\d|"
    r"docker endpoint|docker daemon|docker desktop|context .*not found|error during connect|"
    r"error response from daemon|sign in to continue using docker|membership in the .* organization is required|"
    r"failed to initialize: .*docker|permission denied.*docker|is the docker daemon running|"
    r"command not found",
    re.I,
)


class PairedTaskPlugin:
    name = "paired-task"

    def image(self, cell: Cell) -> str:
        return cell.target.meta.get("image") or "kitsoki-arena/paired-task:latest"

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        task = cell.axis.get("task", "")
        treatment = str(cell.variant.meta.get("treatment") or cell.variant.id)
        argv = [
            "python3",
            RUNNER,
            "--task",
            task,
            "--treatment",
            treatment,
            "--target",
            cell.target.id,
        ]
        corpus = _container_repo_path(str(cell.target.meta.get("corpus") or cell.target.meta.get("source") or ""))
        _append_if(argv, "--corpus", corpus)
        if live:
            argv.append("--live")
            _append_if(argv, "--backend", cell.variant.backend)
            _append_if(argv, "--model", cell.variant.model)
            _append_if(argv, "--effort", cell.variant.effort)
            _append_if(argv, "--agent", str(cell.variant.meta.get("agent") or ""))
            _append_if(argv, "--worker-profile", str(cell.variant.meta.get("worker_profile") or ""))
            _append_if(argv, "--orchestrator-model", str(cell.variant.meta.get("orchestrator_model") or ""))
            _append_if(argv, "--implementation-mode", str(cell.variant.meta.get("implementation_mode") or ""))
            _append_if(argv, "--story", str(cell.target.meta.get("story") or cell.variant.meta.get("story") or ""))
            _append_if(argv, "--capability-preset", str(cell.variant.meta.get("capability_preset") or ""))
            presets = (cell.options or {}).get("capability_presets")
            if isinstance(presets, dict):
                argv.extend(["--capability-presets-json", json.dumps(presets, sort_keys=True)])
            _append_if(argv, "--live-gate-env", str((cell.options or {}).get("live_gate_env") or ""))
        else:
            argv.append("--arm-only")
        return argv

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(
            cell_id=cell.id,
            job_type=self.name,
            target_id=cell.target.id,
            variant_id=cell.variant.id,
            axis=dict(cell.axis),
        )
        blob = f"{stdout}\n{stderr}"

        if _INFRA_RE.search(blob):
            result.verdict = "blocked"
            result.health = "infra:harness"
            result.notes = _first_line(blob)
            return result

        payload = _load_json(stdout)
        if payload:
            verdict = str(payload.get("verdict") or payload.get("grade") or "pending")
            result.verdict = _normalize_verdict(verdict)
            result.health = "infra:harness" if result.verdict == "blocked" else "model:result"
            result.metrics.update(_metrics(payload))
            refs = payload.get("evidence_refs")
            if isinstance(refs, list):
                result.evidence_refs = [str(ref) for ref in refs]
            result.trace_ref = str(payload.get("trace_ref") or "")
            result.notes = str(payload.get("notes") or "")
            return result

        result.health = "model:result"
        result.verdict = "failed" if exit_code else "armed"
        result.notes = _first_line(blob)
        return result


def _append_if(argv: list[str], flag: str, value: str) -> None:
    if value:
        argv.extend([flag, value])


def _container_repo_path(value: str) -> str:
    if not value or value.startswith("/"):
        return value
    return f"{KITSOKI_MNT}/{value}"


def _load_json(stdout: str) -> dict[str, Any]:
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            payload = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(payload, dict):
            return payload
    return {}


def _normalize_verdict(verdict: str) -> str:
    lowered = verdict.strip().lower()
    if lowered in {"solved", "partial", "failed", "armed", "blocked", "pending", "unsupported"}:
        return lowered
    if lowered in {"pass", "passed", "green"}:
        return "solved"
    if lowered in {"red", "fail"}:
        return "failed"
    return "pending"


def _metrics(payload: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    nested = payload.get("metrics")
    if isinstance(nested, dict):
        out.update(nested)
    for key in ("cost_usd", "tokens", "wall_s"):
        value = payload.get(key)
        if isinstance(value, (int, float)):
            out[key] = value
    return out


def _first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:200]
    return ""


base.register(PairedTaskPlugin())

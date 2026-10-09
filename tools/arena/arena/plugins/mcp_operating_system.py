"""Replay-only Arena plugin for MCP operating-system promotion evidence."""

from __future__ import annotations

import json
from typing import Any

from ..model import Cell, CellResult
from . import base


KITSOKI_MNT = "/workspace/kitsoki"
RUNNER = f"{KITSOKI_MNT}/tools/arena/arena/mcp_operating_system_report.py"


class MCPOperatingSystemPlugin:
    name = "mcp-operating-system"

    def image(self, cell: Cell) -> str:
        return cell.target.meta.get("image") or "kitsoki-arena/replay:latest"

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        if live:
            raise ValueError("live calibration is separately operator-authorized and is never dispatched through the Arena replay plugin")
        return [
            "python3", RUNNER, "replay", "--spec",
            f"{KITSOKI_MNT}/tools/arena/specs/mcp-operating-system-replay.yaml",
            "--profile", cell.variant.id, "--case", cell.axis["case"],
        ]

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(cell_id=cell.id, job_type=self.name, target_id=cell.target.id, variant_id=cell.variant.id, axis=dict(cell.axis))
        payload = _load_json(stdout)
        if exit_code != 0 or not payload:
            result.verdict, result.health = "blocked", "infra:replay"
            result.notes = _first_line(stderr or stdout) or "missing MCP operating-system replay output"
            return result
        if payload.get("profile") != cell.variant.id or payload.get("case_id") != cell.axis.get("case"):
            result.verdict, result.health, result.notes = "blocked", "infra:replay", "replay output does not match requested coordinate"
            return result
        safety, correctness = payload.get("safety"), payload.get("correctness")
        if safety not in {"pass", "fail"} or correctness not in {"pass", "fail"}:
            result.verdict, result.health, result.notes = "blocked", "infra:replay", "replay output has invalid hard-gate values"
            return result
        result.verdict = "solved" if safety == "pass" and correctness == "pass" else "partial"
        result.health = "fixture:replay"
        result.metrics = {key: payload[key] for key in ("safety", "correctness", "cost_usd", "latency_s")}
        result.evidence_refs = [payload["evidence_ref"]]
        result.notes = "replay-only MCP operating-system matrix cell"
        return result


def _load_json(stdout: str) -> dict[str, Any]:
    for line in reversed(stdout.splitlines()):
        try:
            payload = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(payload, dict):
            return payload
    return {}


def _first_line(value: str) -> str:
    return next((line.strip()[:200] for line in value.splitlines() if line.strip()), "")


base.register(MCPOperatingSystemPlugin())

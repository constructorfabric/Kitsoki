"""Replay-only Arena plugin for the MCP operating-system baseline.

This plugin has no live branch by design.  Slice 0 records current Studio MCP
toolbox behavior from fixtures; it cannot dispatch a provider or modify a repo.
Later slices may compare candidate profiles against this stable baseline.
"""

from __future__ import annotations

import json
from typing import Any

from ..model import Cell, CellResult
from . import base

KITSOKI_MNT = "/workspace/kitsoki"
RUNNER = f"{KITSOKI_MNT}/tools/arena/arena/mcp_os_report.py"


class MCPOSPlugin:
    name = "mcp-os-baseline"

    def image(self, cell: Cell) -> str:
        return cell.target.meta.get("image") or "kitsoki-arena/replay:latest"

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        if live:
            raise ValueError("mcp-os-baseline is replay-only; live provider evaluation is not part of Slice 0")
        corpus = str(cell.target.meta.get("corpus") or "tools/arena/corpus/mcp-os")
        if not corpus.startswith("/"):
            corpus = f"{KITSOKI_MNT}/{corpus}"
        return ["python3", RUNNER, "replay", "--corpus", corpus, "--case", cell.axis["case"]]

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(
            cell_id=cell.id,
            job_type=self.name,
            target_id=cell.target.id,
            variant_id=cell.variant.id,
            axis=dict(cell.axis),
        )
        payload = _load_json(stdout)
        if exit_code != 0 or not payload:
            result.verdict = "blocked"
            result.health = "infra:replay"
            result.notes = _first_line(stderr or stdout) or "missing MCP OS replay output"
            return result
        if payload.get("case_id") != cell.axis.get("case"):
            result.verdict = "blocked"
            result.health = "infra:replay"
            result.notes = "replay output case does not match requested case"
            return result
        safety = str(payload.get("safety") or "")
        result.verdict = "solved" if safety == "safe" else "partial"
        result.health = "fixture:replay"
        result.metrics = {"safety": safety, "outcome": payload.get("outcome")}
        result.evidence_refs = [str(payload["evidence_ref"])] if payload.get("evidence_ref") else []
        result.notes = "current-toolbox baseline observation"
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
    for line in value.splitlines():
        if line.strip():
            return line.strip()[:200]
    return ""


base.register(MCPOSPlugin())

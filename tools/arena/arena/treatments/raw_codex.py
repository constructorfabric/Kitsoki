"""Raw one-shot prompt treatments."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

from .base import DriverResult, DriverServices, TreatmentDriver


class RawPromptDriver(TreatmentDriver):
    name = "raw-codex"
    action_surface = "raw-agent"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        result = services.dispatch_single_prompt(args, task, tree, trace_ref)
        metrics = dict(result.get("metrics") or {})
        metrics.setdefault("action_surface", self.action_surface)
        return DriverResult(
            blocked=bool(result.get("blocked")),
            infra_class=str(result.get("infra_class") or ""),
            notes=str(result.get("notes") or ""),
            trace_ref=services.container_path(trace_ref),
            transcript_ref=services.container_path(str(result.get("transcript_ref") or trace_ref)),
            metrics=metrics,
        )

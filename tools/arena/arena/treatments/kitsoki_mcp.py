"""Kitsoki Studio MCP-backed arena treatments."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

from .base import DriverResult, DriverServices, TreatmentDriver
from .capabilities import CODEACT_CAPABILITY_PRESET, capability_preset_json


class KitsokiMCPDriver(TreatmentDriver):
    name = "kitsoki-mcp"
    action_surface = "kitsoki-studio-mcp"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        if args.backend != "codex":
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes=f"kitsoki live dispatch currently expects backend 'codex', got {args.backend!r}",
                metrics=services.zero_metrics(action_surface=self.action_surface),
            )
        result = services.dispatch_kitsoki(args, task, tree, trace_ref)
        metrics = dict(result.get("metrics") or {})
        metrics.setdefault("action_surface", self.action_surface)
        metrics.setdefault("studio_mcp_driver", "kitsoki-mcp-driver")
        return DriverResult(
            blocked=bool(result.get("blocked")),
            infra_class=str(result.get("infra_class") or ""),
            notes=str(result.get("notes") or ""),
            trace_ref=services.container_path(trace_ref),
            transcript_ref=services.container_path(str(result.get("transcript_ref") or trace_ref)),
            metrics=metrics,
        )


class KitsokiMCPCodeactDriver(KitsokiMCPDriver):
    name = "kitsoki-mcp-codeact"
    action_surface = "kitsoki-studio-mcp+codeact"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        if not args.implementation_mode:
            args.implementation_mode = "codeact"
        result = super().run(args, task, tree, trace_ref, services)
        cap_json, cap_hash = capability_preset_json(args, args.capability_preset or CODEACT_CAPABILITY_PRESET)
        result.metrics.setdefault("implementation_mode", args.implementation_mode)
        result.metrics.setdefault("codeact_surface", "host.agent.codeact")
        result.metrics.setdefault("capability_preset", args.capability_preset or CODEACT_CAPABILITY_PRESET)
        result.metrics.setdefault("capability_hash", cap_hash)
        result.metrics.setdefault("capability_json", cap_json)
        result.metrics.setdefault("action_surface", self.action_surface)
        return result

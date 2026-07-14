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


class KitsokiMCPDirectDriver(KitsokiMCPDriver):
    """The same bugfix story through its bounded direct-submit Studio loop.

    This is deliberately not the standalone ``codex-codeact`` diagnostic.  The
    paired runner's Studio prompt opens the ordinary bugfix session and moves
    it with explicit ``session.submit`` intents, preserving the normal story
    roles while avoiding an alternate action surface for implementation.
    """

    name = "strict-mcp-direct-driver"
    action_surface = "kitsoki-studio-mcp+direct-submit"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        # This arm measures the current agent-task implementation role. It may
        # not silently drift into either broad or decomposed CodeAct.
        args.implementation_mode = "agent_task"
        result = super().run(args, task, tree, trace_ref, services)
        result.metrics.update({
            "treatment_ladder": "strict-direct-submit",
            "session_driver": "studio-direct-submit",
            "implementation_mode": "agent_task",
            "capability_widening": False,
        })
        return result


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


class KitsokiMCPDecomposedCodeactDriver(KitsokiMCPCodeactDriver):
    """Strict per-item CodeAct treatment with no alternative action surface."""

    name = "strict-mcp-codeact-decomposed"
    action_surface = "kitsoki-studio-mcp+codeact-decomposed"
    implementation_mode = "codeact_decomposed"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        # Do not let an omitted variant field select the historical broad
        # CodeAct mode. This is a treatment boundary, not a convenience alias.
        args.implementation_mode = self.implementation_mode
        result = super().run(args, task, tree, trace_ref, services)
        result.metrics.update({
            "treatment_ladder": "strict-decomposed",
            "fallback_policy": "forbidden",
            "capability_widening": False,
        })
        return result


class KitsokiMCPDecomposedFallbackDriver(KitsokiMCPCodeactDriver):
    """Strict per-item CodeAct with one typed, same-grant retry.

    The bugfix story records a fallback only when the implementation artifact
    names an item-allowlisted reason.  It never replaces CodeAct with a broad
    agent task and never changes the capability map for the retried item.
    """

    name = "strict-mcp-decomposed-fallback"
    action_surface = "kitsoki-studio-mcp+codeact-decomposed-fallback"
    implementation_mode = "codeact_decomposed_fallback"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        args.implementation_mode = self.implementation_mode
        result = super().run(args, task, tree, trace_ref, services)
        result.metrics.update({
            "treatment_ladder": "strict-decomposed-fallback",
            "fallback_policy": "typed-allowlisted-same-grant-once",
            "capability_widening": False,
        })
        return result

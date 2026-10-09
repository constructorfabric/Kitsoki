"""Base contracts for arena treatment drivers."""

from __future__ import annotations

import argparse
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable


@dataclass(frozen=True)
class TreatmentCatalogEntry:
    """Public metadata for one arena treatment ID."""

    id: str
    driver: str
    action_surface: str
    summary: str
    aliases: tuple[str, ...] = ()
    required_variant_fields: tuple[str, ...] = ()
    option_fields: tuple[str, ...] = ()


@dataclass
class DriverResult:
    """Outcome from dispatching one treatment arm before oracle scoring."""

    blocked: bool = False
    infra_class: str = ""
    notes: str = ""
    transcript_ref: str = ""
    trace_ref: str = ""
    launch_plan_ref: str = ""
    metrics: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class DriverServices:
    """Runner-owned services used by reusable treatment drivers.

    Treatment modules should stay policy-focused: select the action surface,
    thread treatment-specific options, and normalize metrics. The concrete
    runner remains responsible for local dispatch helpers and repo-specific
    prompt construction.
    """

    kitsoki_root: Path
    dispatch_single_prompt: Callable[[argparse.Namespace, dict[str, Any], Path, str], dict[str, Any]]
    dispatch_kitsoki: Callable[[argparse.Namespace, dict[str, Any], Path, str], dict[str, Any]]
    zero_metrics: Callable[..., dict[str, Any]]
    container_path: Callable[[Any], str]
    write_task_file: Callable[[argparse.Namespace, dict[str, Any], str], Path]
    ensure_kitsoki_binary: Callable[[], Path]
    first_line: Callable[[str], str]
    redact_cmd: Callable[[list[str]], list[str]]
    codex_output_metrics: Callable[[str, str], dict[str, Any]]
    codeact_text_metrics: Callable[[str], dict[str, Any]]


class TreatmentDriver:
    """Executable treatment driver."""

    name = ""
    action_surface = ""

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        raise NotImplementedError

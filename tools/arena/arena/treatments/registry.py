"""Treatment registry and public catalog for arena jobs."""

from __future__ import annotations

import argparse

from .base import TreatmentCatalogEntry, TreatmentDriver
from .capabilities import CODEACT_CAPABILITY_PRESET, DEFAULT_CODEACT_AGENT, capability_preset_json
from .codex_codeact import CodexCodeactDriver
from .kitsoki_mcp import (
    KitsokiMCPCodeactDriver,
    KitsokiMCPDirectDriver,
    KitsokiMCPDecomposedCodeactDriver,
    KitsokiMCPDecomposedFallbackDriver,
    KitsokiMCPDriver,
)
from .raw_codex import RawPromptDriver

TREATMENT_CATALOG: tuple[TreatmentCatalogEntry, ...] = (
    TreatmentCatalogEntry(
        id="raw-agent",
        driver="RawPromptDriver",
        action_surface="raw-agent",
        summary="One raw one-shot prompt through the configured agent backend.",
        aliases=("raw-codex", "single-briefed", "single-naive"),
    ),
    TreatmentCatalogEntry(
        id="strict-mcp-direct-driver",
        driver="KitsokiMCPDirectDriver",
        action_surface="kitsoki-studio-mcp+direct-submit",
        summary="The current bugfix story and roles driven through bounded Studio session.submit intents; isolates the outer driver from CodeAct action-surface changes.",
        option_fields=("worker_profile",),
    ),
    TreatmentCatalogEntry(
        id="codex-codeact",
        driver="CodexCodeactDriver",
        action_surface="kitsoki-codeact-mcp",
        summary="Direct CodeAct MCP launch via kitsoki-codeact-driver; a Codex-only action-surface diagnostic, not a task-optimization headline arm.",
        required_variant_fields=("agent",),
        option_fields=("capability_preset", "capability_presets"),
    ),
    TreatmentCatalogEntry(
        id="strict-mcp-current",
        driver="KitsokiMCPDriver",
        action_surface="kitsoki-studio-mcp",
        summary="kitsoki-mcp-driver orchestrates the normal Studio MCP workflow.",
        aliases=("kitsoki-mcp", "kitsoki"),
        option_fields=("worker_profile",),
    ),
    TreatmentCatalogEntry(
        id="strict-mcp-codeact-broad",
        driver="KitsokiMCPCodeactDriver",
        action_surface="kitsoki-studio-mcp+codeact",
        summary="Studio MCP orchestration with the story's implementation step forced through host.agent.codeact.",
        option_fields=("worker_profile", "implementation_mode", "capability_preset", "capability_presets"),
        aliases=("kitsoki-mcp-codeact",),
    ),
    TreatmentCatalogEntry(
        id="strict-mcp-codeact-decomposed",
        driver="KitsokiMCPDecomposedCodeactDriver",
        action_surface="kitsoki-studio-mcp+codeact-decomposed",
        summary="Studio MCP with compiler-bounded, per-item CodeAct grants and host-owned item gates.",
        option_fields=("worker_profile", "capability_preset", "capability_presets"),
    ),
    TreatmentCatalogEntry(
        id="strict-mcp-decomposed-fallback",
        driver="KitsokiMCPDecomposedFallbackDriver",
        action_surface="kitsoki-studio-mcp+codeact-decomposed-fallback",
        summary="Strict decomposed CodeAct with one recorded allowlisted same-grant retry; no broad fallback.",
        option_fields=("worker_profile", "capability_preset", "capability_presets"),
    ),
)

_DRIVERS_BY_CANONICAL: dict[str, TreatmentDriver] = {
    "raw-agent": RawPromptDriver(),
    "strict-mcp-direct-driver": KitsokiMCPDirectDriver(),
    "codex-codeact": CodexCodeactDriver(),
    "strict-mcp-current": KitsokiMCPDriver(),
    "strict-mcp-codeact-broad": KitsokiMCPCodeactDriver(),
    "strict-mcp-codeact-decomposed": KitsokiMCPDecomposedCodeactDriver(),
    "strict-mcp-decomposed-fallback": KitsokiMCPDecomposedFallbackDriver(),
}
_ALIASES: dict[str, str] = {
    alias: entry.id
    for entry in TREATMENT_CATALOG
    for alias in entry.aliases
}
TREATMENT_DRIVERS: dict[str, TreatmentDriver] = {
    **_DRIVERS_BY_CANONICAL,
    **{alias: _DRIVERS_BY_CANONICAL[target] for alias, target in _ALIASES.items()},
}


def canonical_treatment(treatment: str) -> str:
    return _ALIASES.get(treatment, treatment)


def known_treatments() -> tuple[str, ...]:
    return tuple(sorted(TREATMENT_DRIVERS))


def treatment_catalog(*, include_aliases: bool = False) -> list[dict[str, object]]:
    rows: list[dict[str, object]] = []
    for entry in TREATMENT_CATALOG:
        row = {
            "id": entry.id,
            "driver": entry.driver,
            "action_surface": entry.action_surface,
            "summary": entry.summary,
            "required_variant_fields": list(entry.required_variant_fields),
            "option_fields": list(entry.option_fields),
        }
        if entry.aliases:
            row["aliases"] = list(entry.aliases)
        rows.append(row)
    if include_aliases:
        for alias, target in sorted(_ALIASES.items()):
            rows.append({
                "id": alias,
                "alias_for": target,
                "driver": _DRIVERS_BY_CANONICAL[target].__class__.__name__,
                "action_surface": _DRIVERS_BY_CANONICAL[target].action_surface,
                "summary": f"Alias for {target}.",
                "required_variant_fields": [],
                "option_fields": [],
            })
    return rows


def resolve_treatment_driver(treatment: str) -> TreatmentDriver | None:
    return TREATMENT_DRIVERS.get(treatment)


def validate_driver_errors(args: argparse.Namespace) -> list[str]:
    """Return actionable treatment configuration errors for runner/CLI use."""
    errors: list[str] = []
    treatment = canonical_treatment(args.treatment)
    if treatment not in _DRIVERS_BY_CANONICAL:
        errors.append(f"unknown paired-task treatment {args.treatment!r}; known: {', '.join(known_treatments())}")
        return errors
    if treatment == "codex-codeact":
        if getattr(args, "backend", "") and args.backend != "codex":
            errors.append(f"codex-codeact requires backend 'codex', got {args.backend!r}")
        agent = (args.agent or "").strip()
        if not agent:
            errors.append(f"codex-codeact requires variant.agent={DEFAULT_CODEACT_AGENT!r}")
        elif agent != DEFAULT_CODEACT_AGENT:
            errors.append(f"codex-codeact requires variant.agent={DEFAULT_CODEACT_AGENT!r}, got {agent!r}")
        preset_name = args.capability_preset or CODEACT_CAPABILITY_PRESET
        try:
            capability_preset_json(args, preset_name)
        except Exception as exc:  # noqa: BLE001 - validation string for cell JSON.
            errors.append(str(exc))
    if treatment in {"strict-mcp-codeact-broad", "strict-mcp-codeact-decomposed", "strict-mcp-decomposed-fallback"}:
        if getattr(args, "backend", "") and args.backend != "codex":
            errors.append(f"{treatment} requires backend 'codex', got {args.backend!r}")
        mode = (getattr(args, "implementation_mode", "") or "").strip()
        required_mode = {
            "strict-mcp-codeact-broad": "codeact",
            "strict-mcp-codeact-decomposed": "codeact_decomposed",
            "strict-mcp-decomposed-fallback": "codeact_decomposed_fallback",
        }[treatment]
        if mode and mode != required_mode:
            errors.append(f"{treatment} requires implementation_mode {required_mode!r}, got {mode!r}")
        preset_name = args.capability_preset or CODEACT_CAPABILITY_PRESET
        try:
            capability_preset_json(args, preset_name)
        except Exception as exc:  # noqa: BLE001 - validation string for cell JSON.
            errors.append(str(exc))
    if treatment in {"strict-mcp-current", "strict-mcp-direct-driver"}:
        if getattr(args, "backend", "") and args.backend != "codex":
            errors.append(f"{treatment} requires backend 'codex', got {args.backend!r}")
    return errors


def validate_driver_args(args: argparse.Namespace) -> str:
    errors = validate_driver_errors(args)
    if errors:
        return "; ".join(errors)
    return ""

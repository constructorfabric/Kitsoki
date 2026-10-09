#!/usr/bin/env python3
"""Extracted from run.py: common module (see tools/product-journey/README.md)."""

import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from tools.persona_qa.transports import (
    compact_transport_profile as persona_compact_transport_profile,
    transport_profile as persona_transport_profile,
)


PROJECT_ROOT = ROOT


DRIVERS_DIR = ROOT / "tools" / "product-journey" / "drivers"


DEFAULT_DRIVER_ID = "kitsoki-mcp"


EVIDENCE_SOURCES = {"demo", "retained", "external", "local", "cassette", "unknown"}


# Playback-capable evidence: a typed slot every natural-use scenario declares
# so it can actually be REPLAYED (an rrweb viewer, `kitsoki test flows`, a PNG
# frame sequence) rather than merely referenced. Unlike general proof evidence
# (which accepts a cassette:// URI once it resolves to a backing file), a
# playback slot must be a real LOCAL file — see is_playback_evidence (run.py).
PLAYBACK_EVIDENCE_KINDS = {"rrweb", "trace-replay", "flow-fixture", "png-sequence"}


EVIDENCE_FILE_EXTENSIONS = {
    "browser_screenshot": "png",
    "screenshot_or_tui_png": "png",
    "rendered_tui_frame": "png",
    "key_interaction_video": "mp4",
    "rrweb": "rrweb.json",
    "trace-replay": "jsonl",
    "flow-fixture": "yaml",
    "png-sequence": "frames.json",
    "session_trace": "jsonl",
    "trace_reference": "jsonl",
    "navigation_trace": "json",
    "checkpoint_rating": "json",
    "generated_config_diff": "diff",
    "candidate_diff": "diff",
    "implementation_diff": "diff",
    "onboarding_smoke_result": "json",
    "oracle_result": "json",
    "full_suite_result": "txt",
    "targeted_test_result": "txt",
    "prd_artifact": "md",
    "design_artifact": "md",
    "review_notes": "md",
    "review_summary": "md",
    "bug_report_markdown": "md",
    "reproduction_steps": "md",
    "command_output": "txt",
    "page_url": "txt",
    "ide_context_capture": "json",
}


SCENARIO_ALIASES = {
    "core-use-cases": ["project-onboarding", "prd-design", "bugfix"],
    "core": ["project-onboarding", "prd-design", "bugfix"],
}


STAGES = [
    "discover_product",
    "follow_tutorial",
    "onboard_project",
    "plan_project_work",
    "fix_bug",
    "file_product_issue",
    "score_and_report",
]


def select_persona(personas: list[dict], persona_id: str, seed: str) -> dict:
    if persona_id:
        for persona in personas:
            if persona["id"] == persona_id:
                return persona
        known = ", ".join(persona["id"] for persona in personas)
        raise SystemExit(f"Unknown persona '{persona_id}'. Known: {known}")
    digest = hashlib.sha256(seed.encode("utf-8")).digest()
    return personas[digest[0] % len(personas)]


def format_case_variants(case_variants: list[dict]) -> str:
    if not case_variants:
        return ""
    lines = ["Case variants to rotate through across persona/target runs:"]
    for variant in case_variants:
        lines.append(
            f"- {variant.get('id', 'case')}: \"{variant.get('utterance', '')}\"; "
            f"setup: {variant.get('setup', '')}; success focus: {variant.get('success_focus', '')}"
        )
    return "\n".join(lines)


def transport_profile(transport: str) -> dict:
    try:
        return persona_transport_profile(transport)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc


def compact_transport_profile(profile: dict) -> dict:
    return persona_compact_transport_profile(profile)


_TIER_NOTICES: list[str] = []


def note_tier_synthesis(kind: str, entry_id: str, field: str, tier: str) -> None:
    """Print + buffer a visible notice when a mined-tier entry's `field` is
    being synthesized from defaults rather than authored.

    Printed to stderr so it surfaces on any invocation path (CLI, story
    `check`, live drive); also appended to a module-level buffer that
    build_run_bundle() copies into run.json's `tier_notices` before writing
    the bundle, so a reviewer can see synthesis history without re-running
    anything. A no-op for tier=curated entries.
    """
    if tier != "mined":
        return
    message = f"{kind} {entry_id} is tier=mined: {field} synthesized from defaults"
    print(f"[persona-qa] NOTICE: {message}", file=sys.stderr)
    _TIER_NOTICES.append(message)


CATALOG_TIERS = ("curated", "mined")


def persona_tier(persona: dict) -> str:
    """Return this persona's corpus tier (see scenario_tier)."""
    declared = persona.get("tier")
    if declared in CATALOG_TIERS:
        return declared
    return "curated" if persona.get("persona_lens") else "mined"


def media_kind(evidence_kind: str, artifact_path: str) -> str:
    value = f"{evidence_kind} {artifact_path}".lower()
    suffix = Path(artifact_path).suffix.lower()
    if "video" in value or suffix in {".mp4", ".mov", ".webm", ".gif"}:
        return "video"
    if "screenshot" in value or "png" in value or suffix in {".png", ".jpg", ".jpeg", ".webp"}:
        return "image"
    if "trace" in value or suffix in {".jsonl", ".trace"}:
        return "trace"
    if suffix in {".md", ".txt", ".json", ".yaml", ".yml"}:
        return "document"
    return "artifact"


def evidence_source(artifact_path: str, notes: str = "") -> str:
    combined = f"{artifact_path} {notes}".lower()
    if "demo placeholder" in combined or "deterministic placeholder" in combined:
        return "demo"
    if "cassette" in combined or artifact_path.startswith("cassette://") or "/cassettes/" in artifact_path:
        return "cassette"
    if artifact_path.startswith(("retained://", "image://")):
        return "retained"
    if artifact_path.startswith(("http://", "https://")):
        return "external"
    if artifact_path:
        return "local"
    return "unknown"


def normalize_evidence_source(source: str, artifact_path: str, notes: str = "") -> str:
    normalized = source.strip().lower() if source else evidence_source(artifact_path, notes)
    if normalized not in EVIDENCE_SOURCES:
        known = ", ".join(sorted(EVIDENCE_SOURCES))
        raise SystemExit(f"Evidence source must be one of: {known}")
    return normalized


def persona_lens(persona: dict) -> dict:
    """Return this persona's driver lens: starting surface, first skepticism
    check, evidence emphasis, escalation trigger, and finding bias.

    The lens is now a cataloged field (personas.json `persona_lens`) for the
    curated personas rather than a value computed only at runtime, so arena
    and other read-only consumers of the catalog can see it too. Personas
    that don't carry the field fall back to a lens synthesized from
    surface_preference/risk_focus -- this is the same fallback shape the
    hardcoded lookup used before the field existed. Synthesizing this lens
    for a tier=mined persona prints and records a visible notice -- see
    note_tier_synthesis().
    """
    declared = persona.get("persona_lens")
    if isinstance(declared, dict) and declared:
        return declared
    note_tier_synthesis("persona", persona.get("id", "?"), "persona_lens", persona_tier(persona))
    return {
        "starting_surface": persona.get("surface_preference", "surface chosen by scenario"),
        "first_question": f"What would a {persona.get('label', 'reviewer')} naturally try first, and what evidence proves the result?",
        "evidence_emphasis": ", ".join(persona.get("risk_focus", [])) or "scenario minimum evidence",
        "escalation_trigger": "the scenario cannot produce proof evidence or a clear blocker",
        "finding_bias": "Tie findings to the persona risk focus and scenario success criteria.",
    }


def write_json(path: Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def driver_manifest_path(path_or_id: str) -> Path:
    value = (path_or_id or DEFAULT_DRIVER_ID).strip()
    candidate = Path(value)
    if candidate.is_absolute() or candidate.parent != Path("."):
        return candidate if candidate.is_absolute() else PROJECT_ROOT / candidate
    if candidate.suffix:
        return DRIVERS_DIR / candidate
    return DRIVERS_DIR / f"{candidate.name}.json"


def normalize_capability_tools(value: object) -> list[str]:
    if isinstance(value, str):
        return [value] if value else []
    if isinstance(value, list):
        tools = []
        for item in value:
            if not isinstance(item, str) or not item:
                raise SystemExit("Driver manifest capability values must be non-empty strings or string arrays")
            tools.append(item)
        return tools
    raise SystemExit("Driver manifest capability values must be non-empty strings or string arrays")


def load_driver_manifest(path_or_id: str = "") -> dict:
    path = driver_manifest_path(path_or_id)
    if not path.is_file():
        raise SystemExit(f"Driver manifest not found: {path}")
    manifest = read_json(path)
    manifest["_path"] = str(path)
    capabilities = manifest.get("capabilities", {})
    if not isinstance(capabilities, dict):
        raise SystemExit(f"Driver manifest {path} must contain a capabilities object")
    manifest["_resolved_capabilities"] = {
        capability: normalize_capability_tools(value)
        for capability, value in capabilities.items()
    }
    return manifest


def driver_summary(manifest: dict) -> dict:
    return {
        "id": manifest.get("id", ""),
        "label": manifest.get("label", ""),
        "app_kind": manifest.get("app_kind", ""),
        "manifest_path": manifest.get("_path", ""),
        "capabilities": manifest.get("_resolved_capabilities", {}),
        "affordances": manifest.get("affordances", {}),
        "evidence_contract": manifest.get("evidence_contract", {}),
        "notes": manifest.get("notes", []),
        "oracles": manifest.get("oracles", []),
    }


def run_dir_from_arg(value: str) -> Path:
    path = Path(value)
    if not path.is_absolute():
        path = PROJECT_ROOT / path
    return path

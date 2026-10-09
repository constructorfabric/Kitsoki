#!/usr/bin/env python3
"""Shared deterministic helpers for the punch-list story."""

from __future__ import annotations

import json
import os
import re
from pathlib import Path
from typing import Any
from datetime import datetime, timezone


ROOT = Path(__file__).resolve().parents[3]


def load_yaml_or_json(path: str) -> Any:
    text = Path(path).read_text()
    try:
        import yaml

        return yaml.safe_load(text)
    except ImportError:
        return json.loads(text)


def story_path_exists(path: str) -> bool:
    if not path:
        return False
    p = Path(path)
    if p.is_dir():
        p = p / "app.yaml"
    if not p.is_absolute():
        p = ROOT / p
    return p.exists()


def ensure_parent(path: str) -> None:
    parent = Path(path).parent
    if str(parent):
        parent.mkdir(parents=True, exist_ok=True)


def read_state(path: str) -> dict[str, Any]:
    if not path or not Path(path).exists():
        return {"items": [], "results": {"items": []}, "defaults": {}}
    return json.loads(Path(path).read_text())


def write_state(path: str, state: dict[str, Any]) -> None:
    ensure_parent(path)
    Path(path).write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")


def default_state_path(manifest_path: str) -> str:
    base = Path(manifest_path).stem or "punch-list"
    return str(Path(".artifacts/punch-list") / f"{base}.state.json")


def default_run_id() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def is_llm_spending_command(cmd: str) -> bool:
    lowered = (cmd or "").lower()
    if not lowered.strip():
        return False
    patterns = [
        r"\bclaude\b",
        r"\bcodex\b",
        r"\bopenai\b",
        r"\banthropic\b",
        r"\bkitsoki\s+tour\b",
        r"\bharness:?\s*live\b",
        r"\b--harness\s+live\b",
    ]
    return any(re.search(p, lowered) for p in patterns)


def valid_ladder_policy(value: Any) -> tuple[bool, str]:
    if not isinstance(value, dict):
        return False, "harness_ladder.models must be a non-empty list"
    models = value.get("models") or []
    if not isinstance(models, list) or not models:
        return False, "harness_ladder.models must be a non-empty list"
    for idx, model in enumerate(models):
        if not isinstance(model, dict):
            return False, f"harness_ladder.models[{idx}].model is required"
        if not str(model.get("model") or "").strip():
            return False, f"harness_ladder.models[{idx}].model is required"
        backend = str(model.get("backend") or "").strip()
        if backend not in {"", "claude", "codex", "copilot"}:
            return False, f"harness_ladder.models[{idx}].backend is invalid: {backend}"
    efforts = value.get("efforts") or []
    if not isinstance(efforts, list):
        return False, "harness_ladder.efforts must be a list"
    for effort in efforts:
        if str(effort) not in {"low", "medium", "high", "xhigh", "max"}:
            return False, f"harness_ladder.efforts contains invalid value: {effort}"
    return True, ""


def normalize_manifest(doc: dict[str, Any], run_id: str | None = None) -> tuple[list[dict[str, Any]], list[str], dict[str, Any]]:
    errors: list[str] = []
    if not isinstance(doc, dict):
        return [], ["manifest must be a mapping"], {}
    if doc.get("version") != "punch-list/v1":
        errors.append("version must be punch-list/v1")

    defaults = dict(doc.get("defaults") or {})
    defaults.setdefault("harness", "live")
    defaults.setdefault("profile", "codex-native")
    defaults.setdefault("model", "gpt-5.5")
    defaults.setdefault("trace_root", ".artifacts/punch-list/traces")
    defaults.setdefault("reuse_trace_paths", False)
    defaults.setdefault("require_gpt55", True)
    defaults.setdefault("require_trace_model", True)
    if run_id is None:
        run_id = str(defaults.get("trace_run_id") or default_run_id())
    defaults["trace_run_id"] = run_id

    seen: set[str] = set()
    items: list[dict[str, Any]] = []
    raw_items = doc.get("items") or []
    if not isinstance(raw_items, list) or not raw_items:
        errors.append("items must be a non-empty list")
        raw_items = []

    for idx, raw in enumerate(raw_items):
        if not isinstance(raw, dict):
            errors.append(f"items[{idx}] must be a mapping")
            continue
        item = dict(raw)
        item_id = str(item.get("id") or "").strip()
        if not item_id:
            errors.append(f"items[{idx}].id is required")
            item_id = f"item-{idx + 1}"
        if item_id in seen:
            errors.append(f"duplicate item id: {item_id}")
        seen.add(item_id)

        normalized = dict(defaults)
        normalized.update(item)
        normalized["id"] = item_id
        normalized.setdefault("title", item_id)
        normalized.setdefault("priority", idx + 1)
        normalized.setdefault("mode", "drive")
        normalized.setdefault("prompt", "")
        normalized.setdefault("intent", "")
        normalized.setdefault("slots", {})
        normalized.setdefault("implementation_story", "")
        normalized.setdefault("implementation_prompt", "")
        normalized.setdefault("gate_command", "")
        normalized.setdefault("verify", [])
        normalized.setdefault("findings_policy", {})
        normalized.setdefault("status", "pending")
        normalized.setdefault("last_error", "")

        story = normalized.get("story", "")
        if not story_path_exists(story):
            errors.append(f"{item_id}: story path does not exist: {story}")

        impl_story = normalized.get("implementation_story", "")
        if impl_story and not story_path_exists(impl_story):
            errors.append(f"{item_id}: implementation_story path does not exist: {impl_story}")

        live_impl = normalized.get("harness") in {"live", "ladder"} and (impl_story or normalized.get("mode") == "drive")
        if live_impl and normalized.get("harness") == "ladder":
            ok, msg = valid_ladder_policy(normalized.get("harness_ladder") or {})
            if not ok:
                errors.append(f"{item_id}: {msg}")
        if live_impl and normalized.get("harness") == "live" and normalized.get("require_gpt55", True):
            if normalized.get("profile") != "codex-native":
                errors.append(f"{item_id}: live work must use profile codex-native")
            if normalized.get("model") != "gpt-5.5":
                errors.append(f"{item_id}: live work must use model gpt-5.5")

        verify = normalized.get("verify") or []
        if impl_story and not verify and not normalized.get("gate_command"):
            errors.append(f"{item_id}: implementation items require a deterministic verifier")
        if not isinstance(verify, list):
            errors.append(f"{item_id}: verify must be a list")
            verify = []
            normalized["verify"] = verify
        for v_idx, check in enumerate(verify):
            if not isinstance(check, dict):
                errors.append(f"{item_id}: verify[{v_idx}] must be a mapping")
                continue
            kind = check.get("kind")
            if kind not in {"command", "story_validate", "story_test", "render_tui", "render_web"}:
                errors.append(f"{item_id}: verify[{v_idx}].kind is unsupported: {kind}")
            if kind in {"story_validate", "story_test", "render_tui", "render_web"} and not story_path_exists(check.get("story", "")):
                errors.append(f"{item_id}: verify[{v_idx}].story path does not exist: {check.get('story', '')}")
            if kind == "command" and is_llm_spending_command(check.get("cmd", "")):
                errors.append(f"{item_id}: verify[{v_idx}].cmd appears to invoke an LLM or live run")
        if normalized.get("gate_command") and is_llm_spending_command(normalized.get("gate_command", "")):
            errors.append(f"{item_id}: gate_command appears to invoke an LLM or live run")

        trace_root = Path(str(normalized.get("trace_root") or ".artifacts/punch-list/traces"))
        trace_dir = trace_root if normalized.get("reuse_trace_paths") else trace_root / run_id
        if not normalized.get("trace_path"):
            normalized["trace_path"] = str(trace_dir / f"{item_id}.jsonl")
        if not normalized.get("implementation_trace_path"):
            normalized["implementation_trace_path"] = str(trace_dir / f"{item_id}-implementation.jsonl")
        items.append(normalized)

    items.sort(key=lambda it: (int(it.get("priority") or 0), str(it.get("id") or "")))
    return items, errors, defaults

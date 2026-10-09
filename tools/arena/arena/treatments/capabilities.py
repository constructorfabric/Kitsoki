"""CodeAct capability presets and launch-plan assertions for arena treatments."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any

DEFAULT_CODEACT_AGENT = "kitsoki-codeact-driver"
CODEACT_MCP_SERVER = "kitsoki-codeact"
CODEACT_MCP_TOOL = "mcp__kitsoki-codeact__codeact_eval"
CODEACT_CAPABILITY_PRESET = "repo_patch"
DEFAULT_CAPABILITY_PRESETS: dict[str, dict[str, Any]] = {
    CODEACT_CAPABILITY_PRESET: {
        "fs": {
            "read": ["**"],
            "write": ["**"],
            "max_bytes": 1048576,
        },
        "vcs": "read",
    },
}


def merged_capability_presets(args: argparse.Namespace) -> dict[str, dict[str, Any]]:
    presets = json.loads(json.dumps(DEFAULT_CAPABILITY_PRESETS))
    raw = (getattr(args, "capability_presets_json", "") or "").strip()
    if not raw:
        return presets
    loaded = json.loads(raw)
    if not isinstance(loaded, dict):
        raise ValueError("--capability-presets-json must be a JSON object")
    for name, value in loaded.items():
        if not isinstance(value, dict):
            raise ValueError(f"capability preset {name!r} must be a JSON object")
        presets[str(name)] = value
    return presets


def capability_preset_json(args: argparse.Namespace, preset_name: str) -> tuple[str, str]:
    presets = merged_capability_presets(args)
    if preset_name not in presets:
        known = ", ".join(sorted(presets))
        raise ValueError(f"unknown capability preset {preset_name!r}; known: {known}")
    canonical = canonical_json(presets[preset_name])
    return canonical, capability_hash(canonical)


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def capability_hash(capability_json: str) -> str:
    return "sha256:" + hashlib.sha256(capability_json.encode("utf-8")).hexdigest()


def _command_contains_capability_payload(command: list[str], capability_json: str) -> bool:
    """Find the expected capabilities as a JSON object through TOML escaping.

    ``agent launch`` carries CodeAct arguments in a TOML ``-c`` value.  Its
    rendered launch plan can therefore contain more than one layer of escaped
    JSON quotes.  String equality after one replacement is brittle and can
    falsely reject the exact payload that will reach ``mcp-codeact``.  Decode
    bounded quote-escape layers, then compare a parsed JSON object to the
    canonical expected payload.
    """
    try:
        expected = json.loads(capability_json)
    except json.JSONDecodeError:
        return False
    decoder = json.JSONDecoder()
    candidates = [
        value for value in command
        if f"mcp_servers.{CODEACT_MCP_SERVER}.args=" in value and "--capabilities-json" in value
    ]
    layer = candidates
    for _ in range(4):
        layer = [value.replace(r'\"', '"') for value in layer]
        candidates.extend(layer)
    for value in candidates:
        for start, character in enumerate(value):
            if character != "{":
                continue
            try:
                payload, _ = decoder.raw_decode(value[start:])
            except json.JSONDecodeError:
                continue
            if payload == expected:
                return True
    return False


def assert_codeact_launch_plan(
    plan: dict[str, Any],
    *,
    tree: Path,
    agent: str,
    backend: str,
    capability_json: str,
    capability_hash: str,
) -> dict[str, Any]:
    failures: list[str] = []

    def require(label: str, ok: bool) -> None:
        if not ok:
            failures.append(label)

    command = [str(part) for part in plan.get("command") or []]
    joined = " ".join(command)
    launch_policy = plan.get("launch_policy")
    allowed = launch_policy is None or bool((launch_policy or {}).get("allowed", True))
    require("mode == codeact", plan.get("mode") == "codeact")
    require(f"agent == {agent}", plan.get("agent") == agent)
    require(f"backend == {backend}", plan.get("backend") == backend)
    require("working_dir == cell tree", str(Path(str(plan.get("working_dir") or "")).resolve()) == str(tree.resolve()))
    require("only codeact tool exposed", plan.get("tools") == [CODEACT_MCP_TOOL])
    require("codex shell disabled", "--disable=shell_tool" in command)
    require("codex apps disabled", "--disable=apps" in command)
    require("codex non-interactive MCP bypass flag present", "--dangerously-bypass-approvals-and-sandbox" in command)
    require("codeact mcp server configured", f"mcp_servers.{CODEACT_MCP_SERVER}.command=\"kitsoki\"" in joined)
    require("codeact mcp server enabled", f"mcp_servers.{CODEACT_MCP_SERVER}.enabled=true" in joined)
    require("mcp-codeact command used", "mcp-codeact" in joined)
    require("studio mcp not exposed", "mcp_servers.kitsoki.command=" not in joined and "mcp_servers.kitsoki.enabled=true" not in joined)
    require("direct editor tools absent", all(tool not in joined for tool in ("--allowedTools Write", "--allowedTools Edit", "MultiEdit")))
    require("launch policy allowed or absent", allowed)
    require("capabilities json threaded", _command_contains_capability_payload(command, capability_json))
    return {
        "passed": not failures,
        "failures": failures,
        "shell_disabled": "--disable=shell_tool" in command,
        "apps_disabled": "--disable=apps" in command,
        "only_codeact_tool": plan.get("tools") == [CODEACT_MCP_TOOL],
        "launch_policy_allowed": allowed,
        "capability_hash": capability_hash,
    }

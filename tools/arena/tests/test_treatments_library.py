#!/usr/bin/env python3
"""No-LLM tests for the reusable arena treatment library."""

from __future__ import annotations

import argparse
import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
sys.path.insert(0, str(ARENA_ROOT))

from arena import treatments  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


catalog = {str(row["id"]): row for row in treatments.treatment_catalog()}
check("catalog treatments", sorted(catalog), ["codex-codeact", "kitsoki-mcp", "kitsoki-mcp-codeact", "raw-codex"])
check("raw aliases", catalog["raw-codex"].get("aliases"), ["single-briefed", "single-naive"])
check("kitsoki alias", catalog["kitsoki-mcp"].get("aliases"), ["kitsoki"])
check("codeact action surface", catalog["codex-codeact"]["action_surface"], "kitsoki-codeact-mcp")
require("codeact requires agent", "agent" in catalog["codex-codeact"]["required_variant_fields"])

check("canonical raw alias", treatments.canonical_treatment("single-naive"), "raw-codex")
check("canonical kitsoki alias", treatments.canonical_treatment("kitsoki"), "kitsoki-mcp")
check("known includes aliases", "single-briefed" in treatments.known_treatments(), True)
check("unknown driver", treatments.resolve_treatment_driver("missing"), None)
check("known driver name", treatments.resolve_treatment_driver("kitsoki-mcp-codeact").name, "kitsoki-mcp-codeact")

args = argparse.Namespace(capability_presets_json="", capability_preset="")
cap_json, cap_hash = treatments.capability_preset_json(args, treatments.CODEACT_CAPABILITY_PRESET)
check("capability json", cap_json, '{"fs":{"max_bytes":1048576,"read":["**"],"write":["**"]},"vcs":"read"}')
require("capability hash", cap_hash.startswith("sha256:"))
json.loads(cap_json)

missing_agent = argparse.Namespace(
    treatment="codex-codeact",
    agent="",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
require("missing agent validation", "requires variant.agent" in treatments.validate_driver_args(missing_agent))

wrong_agent = argparse.Namespace(
    treatment="codex-codeact",
    backend="codex",
    agent="kitsoki-mcp-driver",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
require("wrong codeact agent validation", "kitsoki-codeact-driver" in treatments.validate_driver_args(wrong_agent))

bad_preset = argparse.Namespace(
    treatment="kitsoki-mcp-codeact",
    backend="codex",
    agent="",
    implementation_mode="codeact",
    capability_preset="missing",
    capability_presets_json="",
)
require("bad preset validation", "unknown capability preset" in treatments.validate_driver_args(bad_preset))

bad_mode = argparse.Namespace(
    treatment="kitsoki-mcp-codeact",
    backend="codex",
    agent="",
    implementation_mode="patch",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
require("bad implementation mode validation", "implementation_mode 'codeact'" in treatments.validate_driver_args(bad_mode))

with tempfile.TemporaryDirectory(prefix="arena-treatment-") as td:
    tree = Path(td).resolve()
    plan = {
        "mode": "codeact",
        "agent": treatments.DEFAULT_CODEACT_AGENT,
        "backend": "codex",
        "working_dir": str(tree),
        "tools": [treatments.CODEACT_MCP_TOOL],
        "command": [
            "codex",
            "exec",
            "--dangerously-bypass-approvals-and-sandbox",
            "--disable=shell_tool",
            "--disable=apps",
            "-c",
            'mcp_servers.kitsoki-codeact.command="kitsoki"',
            "-c",
            "mcp_servers.kitsoki-codeact.enabled=true",
            "-c",
            f'mcp_servers.kitsoki-codeact.args=["mcp-codeact","--capabilities-json","{cap_json}"]',
        ],
    }
    assertions = treatments.assert_codeact_launch_plan(
        plan,
        tree=tree,
        agent=treatments.DEFAULT_CODEACT_AGENT,
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("valid launch plan", assertions["passed"], True)
    bad = dict(plan, command=[part for part in plan["command"] if part != "--disable=apps"])
    assertions = treatments.assert_codeact_launch_plan(
        bad,
        tree=tree,
        agent=treatments.DEFAULT_CODEACT_AGENT,
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("missing apps disable fails", assertions["passed"], False)

if failures:
    print("FAIL: arena treatments library")
    for f in failures:
        print(f"  - {f}")
    raise SystemExit(1)
print("PASS: arena treatments library (catalog, aliases, capabilities; no LLM)")

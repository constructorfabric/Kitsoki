#!/usr/bin/env python3
"""No-LLM tests for paired-task CodeAct treatments."""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
os.environ.setdefault("KITSOKI_ROOT", str(REPO_ROOT))
sys.path.insert(0, str(ARENA_ROOT))

from arena.model import JobSpec  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402

runner_spec = importlib.util.spec_from_file_location(
    "paired_task_runner", ARENA_ROOT / "lib" / "paired_task_runner.py"
)
if runner_spec is None or runner_spec.loader is None:
    raise SystemExit("could not load paired_task_runner.py")
runner = importlib.util.module_from_spec(runner_spec)
sys.modules[runner_spec.name] = runner
runner_spec.loader.exec_module(runner)

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


check("registry raw alias", runner.resolve_treatment_driver("single-briefed").name, "raw-codex")
check("registry kitsoki alias", runner.resolve_treatment_driver("kitsoki").name, "kitsoki-mcp")
check("registry codeact", runner.resolve_treatment_driver("codex-codeact").name, "codex-codeact")
check("registry unknown", runner.resolve_treatment_driver("not-a-treatment"), None)

args = argparse.Namespace(capability_presets_json="", capability_preset="")
cap_json, cap_hash = runner.capability_preset_json(args, "repo_patch")
check("canonical capability json", cap_json, '{"fs":{"max_bytes":1048576,"read":["**"],"write":["**"]},"vcs":"read"}')
require("capability hash has sha256 prefix", cap_hash.startswith("sha256:"))

bad_args = argparse.Namespace(capability_presets_json='{"narrow":{"vcs":"read"}}', capability_preset="")
try:
    runner.capability_preset_json(bad_args, "missing")
except ValueError as exc:
    require("unknown preset names known presets", "unknown capability preset" in str(exc))
else:
    failures.append("unknown preset did not raise")

with tempfile.TemporaryDirectory(prefix="paired-codeact-") as td:
    tree = Path(td).resolve()
    command = [
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
    ]
    plan = {
        "mode": "codeact",
        "agent": "kitsoki-codeact-driver",
        "backend": "codex",
        "working_dir": str(tree),
        "tools": ["mcp__kitsoki-codeact__codeact_eval"],
        "command": command,
    }
    assertions = runner.assert_codeact_launch_plan(
        plan,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("valid plan passes", assertions["passed"], True)
    bad = dict(plan, tools=["mcp__kitsoki-codeact__codeact_eval", "Bash"])
    assertions = runner.assert_codeact_launch_plan(
        bad,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("extra tool fails", assertions["passed"], False)

spec = JobSpec.from_dict({
    "job_type": "paired-task",
    "targets": [{"id": "fixture"}],
    "variants": [{
        "id": "codex-codeact-gpt55",
        "treatment": "codex-codeact",
        "backend": "codex",
        "model": "gpt-5.5",
        "effort": "medium",
        "agent": "kitsoki-codeact-driver",
        "capability_preset": "repo_patch",
    }],
    "axes": {"task": ["api-routing"]},
    "options": {
        "live_gate_env": "ARENA_PAIRED_TASK_ENABLE_CODEX",
        "capability_presets": {
            "repo_patch": {
                "fs": {"read": ["**"], "write": ["**"], "max_bytes": 1048576},
                "vcs": "read",
            }
        },
    },
})
cell = spec.cells()[0]
plugin = plugins.get("paired-task")
argv = plugin.drive_command(cell, live=True)
for flag, value in {
    "--agent": "kitsoki-codeact-driver",
    "--capability-preset": "repo_patch",
    "--live-gate-env": "ARENA_PAIRED_TASK_ENABLE_CODEX",
}.items():
    require(f"{flag} threaded", flag in argv and argv[argv.index(flag) + 1] == value)
require("capability presets threaded", "--capability-presets-json" in argv)
json.loads(argv[argv.index("--capability-presets-json") + 1])

missing_agent = argparse.Namespace(
    treatment="codex-codeact",
    backend="codex",
    agent="",
    capability_preset="repo_patch",
    capability_presets_json="",
)
require("missing agent validation", "requires variant.agent" in runner.validate_driver_args(missing_agent))

wrong_agent = argparse.Namespace(
    treatment="codex-codeact",
    backend="codex",
    agent="kitsoki-mcp-driver",
    capability_preset="repo_patch",
    capability_presets_json="",
)
require("wrong agent validation", "kitsoki-codeact-driver" in runner.validate_driver_args(wrong_agent))

if failures:
    print("FAIL: paired-task CodeAct")
    for f in failures:
        print(f"  - {f}")
    raise SystemExit(1)
print("PASS: paired-task CodeAct treatments (registry, assertions, argv; no LLM)")

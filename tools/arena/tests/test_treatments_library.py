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
check("catalog treatments", sorted(catalog), [
    "codex-codeact",
    "raw-agent",
    "strict-mcp-codeact-broad",
    "strict-mcp-codeact-decomposed",
    "strict-mcp-current",
    "strict-mcp-decomposed-fallback",
    "strict-mcp-direct-driver",
])
check("raw aliases", catalog["raw-agent"].get("aliases"), ["raw-codex", "single-briefed", "single-naive"])
check("current aliases", catalog["strict-mcp-current"].get("aliases"), ["kitsoki-mcp", "kitsoki"])
check("broad aliases", catalog["strict-mcp-codeact-broad"].get("aliases"), ["kitsoki-mcp-codeact"])
check("strict direct surface", catalog["strict-mcp-direct-driver"]["action_surface"], "kitsoki-studio-mcp+direct-submit")
check("diagnostic CodeAct surface", catalog["codex-codeact"]["action_surface"], "kitsoki-codeact-mcp")
require("diagnostic CodeAct requires agent", "agent" in catalog["codex-codeact"]["required_variant_fields"])

check("canonical raw alias", treatments.canonical_treatment("single-naive"), "raw-agent")
check("canonical raw legacy alias", treatments.canonical_treatment("raw-codex"), "raw-agent")
check("canonical current alias", treatments.canonical_treatment("kitsoki"), "strict-mcp-current")
check("diagnostic CodeAct stays distinct", treatments.canonical_treatment("codex-codeact"), "codex-codeact")
check("canonical broad alias", treatments.canonical_treatment("kitsoki-mcp-codeact"), "strict-mcp-codeact-broad")
check("known includes aliases", "single-briefed" in treatments.known_treatments(), True)
check("unknown driver", treatments.resolve_treatment_driver("missing"), None)
check("known broad driver", treatments.resolve_treatment_driver("strict-mcp-codeact-broad").name, "kitsoki-mcp-codeact")
check("strict decomposed driver", treatments.resolve_treatment_driver("strict-mcp-codeact-decomposed").name, "strict-mcp-codeact-decomposed")
check("typed fallback driver", treatments.resolve_treatment_driver("strict-mcp-decomposed-fallback").name, "strict-mcp-decomposed-fallback")
check("strict decomposed action surface", catalog["strict-mcp-codeact-decomposed"]["action_surface"], "kitsoki-studio-mcp+codeact-decomposed")
check("typed fallback action surface", catalog["strict-mcp-decomposed-fallback"]["action_surface"], "kitsoki-studio-mcp+codeact-decomposed-fallback")

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
    treatment="strict-mcp-codeact-broad",
    backend="codex",
    agent="",
    implementation_mode="codeact",
    capability_preset="missing",
    capability_presets_json="",
)
require("bad preset validation", "unknown capability preset" in treatments.validate_driver_args(bad_preset))

bad_mode = argparse.Namespace(
    treatment="strict-mcp-codeact-broad",
    backend="codex",
    agent="",
    implementation_mode="patch",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
require("bad implementation mode validation", "implementation_mode 'codeact'" in treatments.validate_driver_args(bad_mode))

spark_strict = argparse.Namespace(
    treatment="strict-mcp-codeact-broad",
    backend="codex",
    agent="",
    worker_profile="codex-spark",
    implementation_mode="codeact",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
check("Spark strict CodeAct backend validation", treatments.validate_driver_args(spark_strict), "")

strict_decomposed = argparse.Namespace(
    treatment="strict-mcp-codeact-decomposed",
    backend="codex",
    agent="",
    implementation_mode="codeact_decomposed",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
check("strict decomposed validation", treatments.validate_driver_args(strict_decomposed), "")

bad_strict_decomposed = argparse.Namespace(
    treatment="strict-mcp-codeact-decomposed",
    backend="codex",
    agent="",
    implementation_mode="codeact",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
require("strict decomposed rejects broad mode", "codeact_decomposed" in treatments.validate_driver_args(bad_strict_decomposed))

typed_fallback = argparse.Namespace(
    treatment="strict-mcp-decomposed-fallback",
    backend="codex",
    agent="",
    implementation_mode="codeact_decomposed_fallback",
    capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
    capability_presets_json="",
)
check("typed fallback validation", treatments.validate_driver_args(typed_fallback), "")

# Driver-level metrics are part of the immutable cell receipt. Exercise the
# treatment wrappers without a provider: the shared Studio MCP dispatcher is
# injected and returns a minimal completed result.
driver_services = treatments.DriverServices(
    kitsoki_root=Path("."),
    dispatch_single_prompt=lambda *_: {},
    dispatch_kitsoki=lambda *_: {"blocked": False, "notes": "fixture", "metrics": {}},
    zero_metrics=lambda **kwargs: kwargs,
    container_path=lambda value: str(value),
    write_task_file=lambda *_: Path("unused"),
    ensure_kitsoki_launcher=lambda: Path("."),
    first_line=lambda value: value,
    redact_cmd=lambda value: value,
    codex_output_metrics=lambda *_: {},
    codeact_text_metrics=lambda *_: {},
)
for treatment_id, expected_mode, expected_policy in [
    ("strict-mcp-direct-driver", "agent_task", "studio-direct-submit"),
    ("strict-mcp-codeact-decomposed", "codeact_decomposed", "forbidden"),
    ("strict-mcp-decomposed-fallback", "codeact_decomposed_fallback", "typed-allowlisted-same-grant-once"),
]:
    runtime_args = argparse.Namespace(
        backend="codex",
        implementation_mode="",
        capability_preset=treatments.CODEACT_CAPABILITY_PRESET,
        capability_presets_json="",
    )
    driver_result = treatments.resolve_treatment_driver(treatment_id).run(
        runtime_args, {"id": "fixture"}, Path("."), "fixture.jsonl", driver_services,
    )
    check(f"{treatment_id} forces mode", runtime_args.implementation_mode, expected_mode)
    policy_key = "session_driver" if treatment_id == "strict-mcp-direct-driver" else "fallback_policy"
    check(f"{treatment_id} metrics policy", driver_result.metrics.get(policy_key), expected_policy)
    check(f"{treatment_id} metrics no widening", driver_result.metrics.get("capability_widening"), False)

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

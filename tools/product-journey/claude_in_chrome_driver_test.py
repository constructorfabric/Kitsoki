#!/usr/bin/env python3
"""Runner-level test for the claude-in-chrome driver manifest.

Run directly:  python3 tools/product-journey/claude_in_chrome_driver_test.py

Proves the real-browser driver manifest is schema-valid, resolves every
canonical capability to concrete mcp__claude-in-chrome__* tools, threads its
operating notes into the generated agent brief, and that the default
kitsoki-mcp path is untouched. No live LLM, browser, or network.
"""

import importlib.util
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)

DRIVER_ID = "claude-in-chrome"


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def main():
    manifest = run.load_driver_manifest(DRIVER_ID)
    result = run.validate_driver_manifest(manifest)
    _check("manifest validates clean", result["status"] == "ok" and not result["issues"])

    resolved = manifest["_resolved_capabilities"]
    _check(
        "every canonical capability resolves",
        all(resolved.get(cap) for cap in run.CANONICAL_DRIVER_CAPABILITIES),
    )
    _check(
        "all resolved tools are claude-in-chrome tools",
        all(
            tool.startswith("mcp__claude-in-chrome__")
            for tools in resolved.values()
            for tool in tools
        ),
    )

    summary = run.driver_summary(manifest)
    _check("summary carries notes", summary["notes"] == manifest["notes"] and len(summary["notes"]) >= 5)
    _check(
        "notes encode the file-addressable evidence rule",
        any("gif_creator" in note and "Downloads" in note for note in summary["notes"]),
    )
    _check(
        "evidence contract names gif_creator as primary web proof",
        manifest["evidence_contract"]["web"]["primary_tool"] == "mcp__claude-in-chrome__gif_creator",
    )
    _check(
        "primary web evidence kind is a video media kind",
        run.media_kind(manifest["evidence_contract"]["web"]["evidence_kind"], "probe.gif") == "video",
    )

    bad = dict(manifest)
    bad["notes"] = ["ok", ""]
    bad_result = run.validate_driver_manifest(bad)
    _check(
        "empty note strings are rejected",
        any(issue["id"] == "driver-notes-shape" for issue in bad_result["issues"]),
    )

    catalog = run.load_catalog(run.CATALOG)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.load_scenarios(run.SCENARIOS)
    selected = run.select_scenarios(scenarios, "project-onboarding")

    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.MATRIX_ROOT = run.ARTIFACT_ROOT / "matrices"
        run.TARGET_PROOF_ROOT = run.ARTIFACT_ROOT / "target-proofs"
        run.DOGFOOD_ROOT = run.ARTIFACT_ROOT / "dogfood"

        run_dir, run_json = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            selected,
            "gears-rust",
            "core-maintainer",
            "chrome-driver-test",
            "dry-run",
            None,
            driver_manifest=manifest,
        )
        _check("run json records the driver id", run_json["driver"]["id"] == DRIVER_ID)

        driver_plan = run.read_json(run_dir / "driver-plan.json")
        plan_tools = {
            tool
            for entry in driver_plan["scenarios"]
            for tool in entry.get("resolved_mcp_tools", [])
        }
        _check(
            "driver plan resolves to claude-in-chrome tools",
            plan_tools and all(tool.startswith("mcp__claude-in-chrome__") for tool in plan_tools),
        )

        brief_md = (run_dir / "agent-brief.md").read_text(encoding="utf-8")
        _check("agent brief names the driving surface", f"`{DRIVER_ID}`" in brief_md)
        _check("agent brief carries Driver Notes", "### Driver Notes" in brief_md)
        _check(
            "agent brief carries the gif evidence rule",
            "gif_creator" in brief_md,
        )

        # Default path regression: the kitsoki-mcp brief must not grow a notes
        # section (its manifest declares none).
        run_dir_default, run_json_default = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            selected,
            "gears-rust",
            "core-maintainer",
            "chrome-driver-default",
            "dry-run",
            None,
        )
        _check("default driver unchanged", run_json_default["driver"]["id"] == run.DEFAULT_DRIVER_ID)
        default_brief = (run_dir_default / "agent-brief.md").read_text(encoding="utf-8")
        _check("default brief has no notes section", "### Driver Notes" not in default_brief)

    print("PASS")


if __name__ == "__main__":
    main()

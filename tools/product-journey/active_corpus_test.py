#!/usr/bin/env python3
"""Runner-level test for active/draft product-journey corpus handling.

Run directly:  python3 tools/product-journey/active_corpus_test.py

The test reads local corpus metadata only. It never calls a live LLM or GitHub.

The corpus used to carry an 18-scenario / 6-persona `mined-scn-*`/legacy
backlog alongside the curated set (see the 2026-07-10 persona-qa
productization brief, P2.13 "curate or cut the mined tier"). Every mined
entry's real content was either already folded into a curated scenario's
`natural_utterances`, folded into a curated scenario's `case_variants`
(docs-to-mcp-first-run, remote-worker-campaign), or was thin/duplicative
internal-dev-ops chatter with no distinct product-journey value, so the
backlog was cut rather than promoted wholesale. The active and full corpus
are now the same size -- this test still asserts that invariant so a future
mining merge that reintroduces an unreviewed `tier=mined` entry is caught
here instead of silently expanding the "full" corpus again.
"""

import importlib.util
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def main():
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.load_scenarios(run.SCENARIOS)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    active_personas = run.active_personas(personas)
    active_scenarios = run.active_scenarios(scenarios)

    expected_active_scenarios = {
        "product-discovery",
        "project-onboarding",
        "tui-slash-commands",
        "bugfix",
        "docs-to-mcp-first-run",
        "agent-launch-experience",
        "remote-worker-campaign",
        "campaign-rollup-review",
        "prd-design",
        "feature-implementation",
        "evidence-backed-product-bug",
        "dogfood-marathon-tui",
    }

    _check("full persona corpus has no undeclared draft backlog", len(personas) == 8)
    _check("full scenario corpus has no undeclared mined backlog", len(scenarios) == 12)
    _check("active persona corpus is runnable", len(active_personas) == 8)
    _check("active scenario corpus is runnable", len(active_scenarios) == 12)
    _check("active scenarios are the natural-use contract", {item["id"] for item in active_scenarios} == expected_active_scenarios)
    _check("mined scenarios are draft only", not any(item["id"].startswith("mined-scn-") for item in active_scenarios))
    _check("every scenario declares an explicit tier", all(item.get("tier") == "curated" for item in scenarios))
    _check("every persona declares an explicit tier", all(item.get("tier") == "curated" for item in personas))
    _check("active scenarios carry natural-language prompts", all(len(item["task"].split()) >= 12 for item in active_scenarios))
    _check("active scenarios carry multiple realistic case variants", all(len(item.get("case_variants", [])) >= 3 for item in active_scenarios))
    _check("case variants include user utterance, setup, and success focus", all(
        {"id", "utterance", "setup", "success_focus"} <= set(variant)
        for item in active_scenarios
        for variant in item.get("case_variants", [])
    ))

    result = run.validate_journey_corpus(personas, scenarios, github_targets)
    _check("full corpus validation passes cleanly", result["status"] == "valid")
    _check("full corpus validation has no errors", result["errors"] == 0)
    _check("full corpus validation has no warnings", result["warnings"] == 0)
    _check("validation reports active personas", result["personas"] == 8)
    _check("validation reports active scenarios", result["scenarios"] == 12)
    _check("validation reports all personas", result["all_personas"] == 8)
    _check("validation reports all scenarios", result["all_scenarios"] == 12)
    _check("validation reports no draft personas", result["draft_personas"] == 0)
    _check("validation reports no draft scenarios", result["draft_scenarios"] == 0)
    _check("validation has no draft warnings left to report", not {"draft-personas", "draft-scenarios"} & {issue["id"] for issue in result["issues"]})

    print("PASS")


if __name__ == "__main__":
    main()

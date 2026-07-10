#!/usr/bin/env python3
"""Runner-level test for the story-owned --transport axis and persona lens catalog.

Covers docs/persona-qa.md's scenario-qa "transport as data" contract:
  - select_transports() validation (dup/unknown ids, "all" expansion)
  - resolve_scenario_transports()/scenario_transport_legs() for scenarios that
    declare `transports` vs scenarios that don't (mined scenarios, which get
    an implicit contract derived from required_mcp)
  - build_run_bundle(..., transports=...) leg expansion in execution-plan and
    driver-plan, and byte-compatible output when transports is omitted
  - persona_lens() reading personas.json's cataloged `persona_lens` field for
    curated personas, and falling back to the synthesized default for
    personas that don't carry it (mined personas)

Run directly:  python3 tools/product-journey/transport_axis_test.py

This creates local dry-run bundles only; it never calls a live LLM or GitHub.
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


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def _expect_system_exit(name, fn, expected_text):
    try:
        fn()
    except SystemExit as exc:
        _check(name, expected_text in str(exc))
        return
    print(f"FAIL: {name}")
    sys.exit(1)


def _test_select_transports():
    _check("empty filter means no transport axis", run.select_transports("") == [])
    _check("single transport", run.select_transports("tui") == ["tui"])
    _check("preserves requested order", run.select_transports("web,tui") == ["web", "tui"])
    _check("all expands to every known transport", run.select_transports("all") == list(run.TRANSPORT_IDS))
    _check("all wins even mixed with explicit ids", run.select_transports("tui,all") == list(run.TRANSPORT_IDS))
    _expect_system_exit(
        "rejects duplicate transport ids",
        lambda: run.select_transports("tui,tui"),
        "duplicate transport",
    )
    _expect_system_exit(
        "rejects unknown transport ids",
        lambda: run.select_transports("holodeck"),
        "unknown transport",
    )


def _test_scenario_transport_contracts():
    scenarios = run.load_scenarios(run.SCENARIOS)
    by_id = {scenario["id"]: scenario for scenario in scenarios}

    product_discovery = by_id["product-discovery"]
    contract = run.resolve_scenario_transports(product_discovery)
    _check("declared scenario keeps its allowed set", contract["allowed"] == ["tui", "web"])
    _check("declared scenario keeps its required set", contract["required"] == ["tui"])
    legs = run.scenario_transport_legs(product_discovery, list(run.TRANSPORT_IDS))
    leg_transports = [leg["transport"] for leg in legs]
    _check("non-product-site transports are not allowed for product-discovery", "vscode" not in leg_transports and "cli" not in leg_transports)
    _check("tui and web legs are both produced", leg_transports == ["tui", "web"])
    web_leg = legs[1]
    _check(
        "per-transport override replaces required_mcp for that leg only",
        web_leg["required_mcp"] == ["session.open", "session.inspect", "visual.open", "visual.observe"],
    )
    _check(
        "base scenario required_mcp is untouched by the override",
        product_discovery["required_mcp"] == ["session.open", "session.inspect", "render.tui"],
    )
    tui_leg = legs[0]
    _check("tui leg keeps the base required_mcp (no override declared)", tui_leg["required_mcp"] == product_discovery["required_mcp"])
    _check(
        "tui leg carries the tui evidence contract",
        tui_leg["transport_evidence_contract"] == run.TRANSPORT_EVIDENCE_CONTRACTS["tui"],
    )
    _check("leg_id names scenario and transport", tui_leg["leg_id"] == "product-discovery::tui")

    dogfood_marathon = by_id["dogfood-marathon-tui"]
    dogfood_contract = run.resolve_scenario_transports(dogfood_marathon)
    _check("dogfood marathon is a TUI-only scenario", dogfood_contract["allowed"] == ["tui"])
    _check("dogfood marathon requires TUI coverage", dogfood_contract["required"] == ["tui"])
    dogfood_legs = run.scenario_transport_legs(dogfood_marathon, list(run.TRANSPORT_IDS))
    _check("dogfood marathon produces exactly one TUI leg", [leg["transport"] for leg in dogfood_legs] == ["tui"])
    _check("dogfood marathon keeps all 15 catalog cases", len(dogfood_marathon.get("case_variants", [])) == 15)
    _check("dogfood marathon declares frame playback evidence", "png-sequence" in dogfood_marathon.get("evidence", []))

    # A mined scenario has no declared `transports`; its contract must be
    # derived from required_mcp so nothing breaks for the un-annotated corpus.
    # The live corpus is fully curated today (see the 2026-07-10 persona-qa
    # productization brief, P2.13 "curate or cut the mined tier"), so this
    # exercises the fallback with a synthetic tier=mined scenario instead of
    # depending on the corpus still carrying one.
    mined = {
        "id": "synthetic-mined-web",
        "tier": "mined",
        "primary_story": "stories/product-site/app.yaml",
        "required_mcp": ["visual.open", "visual.observe"],
    }
    _check("synthetic fixture is tier=mined", run.scenario_tier(mined) == "mined")
    mined_contract = run.resolve_scenario_transports(mined)
    expected_surface = run.driver_visual_surface(mined.get("primary_story", ""), mined.get("required_mcp", []))
    if expected_surface == "web":
        _check("derived contract for a web-surface mined scenario", mined_contract["allowed"] == ["web"])
    elif expected_surface == "tui":
        _check("derived contract for a tui-surface mined scenario", mined_contract["allowed"] == ["tui"])
    elif expected_surface == "web-or-tui":
        _check("derived contract for a web-or-tui mined scenario", mined_contract["allowed"] == ["tui", "web"])
    else:
        _check("derived contract for an artifact-only mined scenario is empty", mined_contract["allowed"] == [])

    # A mined scenario with empty required_mcp derives to no allowed transport
    # -- it never produces a leg, transport or not.
    artifact_only = {"id": "synthetic-mined-artifact-only", "tier": "mined", "primary_story": "", "required_mcp": []}
    _check("artifact-only mined scenario has no allowed transport", run.resolve_scenario_transports(artifact_only)["allowed"] == [])
    _check("artifact-only mined scenario produces zero legs even for 'all'", run.scenario_transport_legs(artifact_only, list(run.TRANSPORT_IDS)) == [])


def _test_emit_run_transport_axis():
    catalog = run.load_catalog(run.CATALOG)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.select_scenarios(run.load_scenarios(run.SCENARIOS), "product-discovery,project-onboarding")

    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.MATRIX_ROOT = run.ARTIFACT_ROOT / "matrices"
        run.TARGET_PROOF_ROOT = run.ARTIFACT_ROOT / "target-proofs"
        run.DOGFOOD_ROOT = run.ARTIFACT_ROOT / "dogfood"
        run.PREFLIGHT_ROOT = run.ARTIFACT_ROOT / "preflights"

        # Baseline: omitting transports must stay byte-compatible with the
        # pre-transport shape -- one step/scenario entry per scenario, no
        # transport/leg_id keys anywhere.
        base_dir, base_json = run.build_run_bundle(
            catalog, github_targets, personas, scenarios,
            "gears-rust", "core-maintainer", "no-transport-axis", "dry-run", None,
        )
        base_execution = run.read_json(base_dir / "execution-plan.json")
        base_driver = run.read_json(base_dir / "driver-plan.json")
        _check("no-axis run has no run.json transports key", "transports" not in base_json)
        _check("no-axis execution-plan has one step per scenario", base_execution["summary"]["scenario_count"] == len(scenarios))
        _check("no-axis execution-plan summary has no transports/leg_count keys", "transports" not in base_execution["summary"] and "leg_count" not in base_execution["summary"])
        _check("no-axis execution-plan steps carry no transport key", all("transport" not in step for step in base_execution["steps"]))
        _check("no-axis driver-plan scenarios carry no transport key", all("transport" not in scenario for scenario in base_driver["scenarios"]))
        _check(
            "no-axis driver-plan visual_surface is still inferred",
            base_driver["scenarios"][0]["visual_surface"] == run.driver_visual_surface(
                base_driver["scenarios"][0]["primary_story"], base_driver["scenarios"][0]["required_mcp"],
            ),
        )
        base_route = base_driver["scenarios"][0]["capture_routes"][0]
        _check("driver-plan declares one capture route per evidence slot",
               len(base_driver["scenarios"][0]["capture_routes"]) == len(base_driver["scenarios"][0]["evidence"]))
        _check("capture route pins the scenario id and primary story",
               base_route["scenario"] == base_driver["scenarios"][0]["scenario"]
               and base_route["primary_story"] == base_driver["scenarios"][0]["primary_story"])
        _check("capture route carries the inferred transport profile",
               base_route["transport"] == "tui"
               and base_route["transport_profile"]["id"] == "tui"
               and base_route["transport_evidence_contract"] == run.TRANSPORT_EVIDENCE_CONTRACTS["tui"])
        _check("capture route owns stable setup and recording entrypoints",
               base_route["setup_entrypoint"]["story_load_intent"].startswith("load run_dir=")
               and "session.new app=" in base_route["setup_entrypoint"]["primary_session"]
               and base_route["recording"]["path_template"].startswith(base_driver["scenarios"][0]["evidence_dir"]))
        _check("capture route owns attach/blocker/journal commands",
               base_route["commands"]["attach"] in base_driver["scenarios"][0]["attach_commands"]
               and base_route["commands"]["blocker"] == base_driver["scenarios"][0]["record_blocker_command"]
               and base_route["commands"]["journal"] == base_driver["scenarios"][0]["journal_command"])

        # One explicit transport: one leg per scenario (both scenarios allow tui).
        tui_dir, tui_json = run.build_run_bundle(
            catalog, github_targets, personas, scenarios,
            "gears-rust", "core-maintainer", "tui-only-axis", "dry-run", None,
            20, ["tui"],
        )
        tui_execution = run.read_json(tui_dir / "execution-plan.json")
        _check("run json records the requested transports", tui_json["transports"] == ["tui"])
        _check("tui-only axis produces exactly one leg per scenario", len(tui_execution["steps"]) == len(scenarios))
        _check("tui-only axis steps are all tui legs", all(step["transport"] == "tui" for step in tui_execution["steps"]))
        _check("tui-only leg_ids are well-formed", {step["leg_id"] for step in tui_execution["steps"]} == {f"{s['id']}::tui" for s in scenarios})

        # transport=all: product-discovery allows tui+web (no vscode/cli);
        # project-onboarding allows tui+web+vscode+cli -- 2 + 4 = 6 legs.
        all_dir, all_json = run.build_run_bundle(
            catalog, github_targets, personas, scenarios,
            "gears-rust", "core-maintainer", "all-transports-axis", "dry-run", None,
            20, list(run.TRANSPORT_IDS),
        )
        all_execution = run.read_json(all_dir / "execution-plan.json")
        all_driver = run.read_json(all_dir / "driver-plan.json")
        _check("transport=all expands product-discovery to 2 legs and project-onboarding to 4", len(all_execution["steps"]) == 6)
        _check("transport=all execution-plan summary reports leg_count", all_execution["summary"]["leg_count"] == 6)
        _check("transport=all never produces unsupported product-discovery legs", not any(
            step["scenario"] == "product-discovery" and step["transport"] in {"vscode", "cli"} for step in all_execution["steps"]
        ))
        vscode_step = next(step for step in all_driver["scenarios"] if step["transport"] == "vscode")
        _check("vscode leg is labeled bridge-level", vscode_step["transport_evidence_contract"]["level"] == "bridge-level")
        _check("vscode leg's visual_surface is pinned to vscode, not inferred", vscode_step["visual_surface"] == "vscode")
        vscode_route = vscode_step["capture_routes"][0]
        _check("vscode capture route carries the leg id and bridge visual surface",
               vscode_route["leg_id"] == vscode_step["leg_id"]
               and vscode_route["visual_surface"] == "vscode"
               and vscode_route["transport_profile"]["id"] == "vscode"
               and vscode_route["transport_evidence_contract"]["level"] == "bridge-level")
        cli_step = next(step for step in all_driver["scenarios"] if step["scenario"] == "project-onboarding" and step["transport"] == "cli")
        cli_evidence = [item["kind"] for item in cli_step["evidence"]]
        _check("cli leg uses command-output evidence override",
               "command_output" in cli_evidence and "rendered_tui_frame" not in cli_evidence)
        cli_route = next(route for route in cli_step["capture_routes"] if route["evidence_kind"] == "command_output")
        _check("cli capture route is terminal-level and profile-backed",
               cli_step["transport_profile"]["id"] == "cli"
               and cli_step["transport_evidence_contract"]["level"] == "terminal-level"
               and cli_route["transport"] == "cli"
               and cli_route["transport_profile"]["id"] == "cli"
               and "session.trace" in cli_route["observe"]["capabilities"])
        web_step = next(step for step in all_driver["scenarios"] if step["scenario"] == "product-discovery" and step["transport"] == "web")
        _check(
            "web leg's driver_actions reflect the per-transport required_mcp override",
            "mcp__kitsoki__visual_open" in [tool for action in web_step["driver_actions"] for tool in action["resolved_tools"]],
        )

        all_review = run.render_execution_plan(all_execution)
        _check("execution-plan.md names the transport axis", "Transports: tui, web, vscode, cli" in all_review)
        _check("execution-plan.md labels legs with their transport", "(tui)" in all_review and "(web)" in all_review and "(vscode)" in all_review and "(cli)" in all_review)
        all_driver_md = run.render_driver_plan(all_driver)
        _check("driver-plan.md marks the vscode leg's evidence contract as bridge-level", "bridge-level" in all_driver_md)
        run.review_run_bundle(all_dir, None)
        validation = run.validate_run_bundle(all_dir)
        _check("reviewed transport-axis bundle validates", validation["status"] == "valid")
        frames_path = all_dir / "evidence" / "gears-rust--core-maintainer" / "product-discovery" / "frames.json"
        frames_path.parent.mkdir(parents=True, exist_ok=True)
        frames_path.write_text('{"schema":"test/png-sequence/v1","frames":[]}\n', encoding="utf-8")
        run.attach_evidence(
            all_dir,
            "product-discovery",
            "png-sequence",
            str(frames_path.relative_to(all_dir)),
            "captured",
            "local",
            "transport-axis regression frame manifest",
            None,
        )
        attached_execution = run.read_json(all_dir / "execution-plan.json")
        attached_driver = run.read_json(all_dir / "driver-plan.json")
        _check("attached evidence preserves transport-expanded leg_count", attached_execution["summary"]["leg_count"] == 6)
        _check("attached evidence preserves transport keys in execution plan", all("transport" in step for step in attached_execution["steps"]))
        _check("attached evidence preserves transport keys in driver plan", all("transport" in scenario for scenario in attached_driver["scenarios"]))

        # Scenario QA consumes driver-plan.json's transport-expanded scenarios
        # as leg payloads. Core scenarios must keep transcript-derived natural
        # utterances in that payload so the live driver receives human-shaped
        # wording, not just a catalog id.
        bugfix_scenarios = run.select_scenarios(run.load_scenarios(run.SCENARIOS), "bugfix")
        bugfix_dir, _ = run.build_run_bundle(
            catalog, github_targets, personas, bugfix_scenarios,
            "gears-rust", "core-maintainer", "bugfix-natural-prompts", "dry-run", None,
            20, ["tui"],
        )
        bugfix_driver = run.read_json(bugfix_dir / "driver-plan.json")
        bugfix_leg = bugfix_driver["scenarios"][0]
        bugfix_utterances = bugfix_leg.get("natural_utterances", [])
        action_utterances = next(
            action.get("natural_utterances", [])
            for action in bugfix_leg["driver_actions"]
            if action["id"] == "act_as_persona"
        )
        _check("bugfix tui leg carries transcript-derived natural utterances", len(bugfix_utterances) == 3)
        _check("bugfix tui act_as_persona action carries the same utterances", action_utterances == bugfix_utterances)
        _check(
            "bugfix tui natural utterances cite mined transcript sources",
            all(item.get("source") == "session-transcript" and item.get("source_ref", "").startswith("mined-scn-") for item in bugfix_utterances),
        )
        bugfix_handoff = run.read_json(bugfix_dir / "driver-handoff.json")
        first_slot = bugfix_handoff["missing_proof_evidence"][0]["slots"][0]
        _check("missing proof slots include the deterministic capture route",
               first_slot["capture_route"]["scenario"] == "bugfix"
               and first_slot["capture_route"]["evidence_kind"] == first_slot["kind"]
               and first_slot["capture_route"]["commands"]["attach"] == first_slot["attach_command"])
        summary = run.summarize_run_bundle(bugfix_dir)
        _check("summarize-run exposes the next capture route for MCP drivers",
               summary["next_driver_capture_route"] == first_slot["capture_route"]
               and "last_result.next_driver_capture_route" in summary["driver_contract_summary"])
        _check("handoff prompt points drivers at next_driver_capture_route",
               "last_result.next_driver_capture_route" in bugfix_handoff["suggested_prompt"])


def _test_transport_suite_preview():
    scenarios = run.select_scenarios(run.load_scenarios(run.SCENARIOS), "bugfix")
    suite = run.build_transport_suite(scenarios, list(run.TRANSPORT_IDS), run.load_driver_manifest())
    _check("transport-suite preview uses the public schema", suite["schema"] == "kitsoki/persona-qa-transport-suite/v1")
    _check("transport-suite preview is ready output", suite["status"] == "ready")
    _check("transport-suite lists every canonical profile", [p["id"] for p in suite["transport_profiles"]] == list(run.TRANSPORT_IDS))
    _check("bugfix transport-suite resolves four legs", suite["summary"]["leg_count"] == 4)
    _check("bugfix transport-suite keeps requested transport order", [leg["transport"] for leg in suite["legs"]] == list(run.TRANSPORT_IDS))
    cli_leg = next(leg for leg in suite["legs"] if leg["transport"] == "cli")
    _check("transport-suite cli leg is terminal-level", cli_leg["evidence_contract"]["level"] == "terminal-level")
    _check("transport-suite cli leg records command output", "command_output" in cli_leg["evidence"])
    _check("transport-suite cli observe entrypoint uses session.trace", "session.trace" in cli_leg["entrypoints"]["observe"]["capabilities"])
    _check("transport-suite forbids fake proof substitution", "no_substitution" in cli_leg["capture_policy"])
    vscode_leg = next(leg for leg in suite["legs"] if leg["transport"] == "vscode")
    _check("transport-suite vscode leg stays bridge-level", vscode_leg["evidence_contract"]["level"] == "bridge-level")
    rendered = run.render_transport_suite(suite)
    _check(
        "transport-suite markdown names all legs",
        "bugfix::tui" in rendered
        and "bugfix::web" in rendered
        and "bugfix::vscode" in rendered
        and "bugfix::cli" in rendered,
    )


def _test_persona_lens_promotion():
    personas = run.load_personas(run.PERSONAS)
    by_id = {persona["id"]: persona for persona in personas}

    core_maintainer = by_id["core-maintainer"]
    _check("core-maintainer carries a cataloged persona_lens", "persona_lens" in core_maintainer)
    lens = run.persona_lens(core_maintainer)
    _check("persona_lens() returns the cataloged lens verbatim", lens == core_maintainer["persona_lens"])
    _check(
        "cataloged lens matches the historical synthesized value",
        lens["first_question"] == "Will this produce a minimal, reviewable diff that follows the repository's style?",
    )

    for persona_id in ["dependency-debugger", "docs-minded-contributor", "ide-first-engineer", "hobbyist-contributor"]:
        persona = by_id[persona_id]
        _check(f"{persona_id} carries a cataloged persona_lens", "persona_lens" in persona)
        _check(f"{persona_id} persona_lens() returns the cataloged lens", run.persona_lens(persona) == persona["persona_lens"])

    # Mined personas don't carry persona_lens; they keep the synthesized
    # fallback. The live corpus is fully curated today (see the 2026-07-10
    # persona-qa productization brief, P2.13), so this exercises the
    # fallback with a synthetic tier=mined persona instead of depending on
    # the corpus still carrying one.
    mined = {
        "id": "synthetic-mined-persona",
        "tier": "mined",
        "surface_preference": "terminal-first",
        "risk_focus": ["time budget", "setup friction"],
    }
    _check("synthetic fixture is tier=mined", run.persona_tier(mined) == "mined")
    _check("mined persona has no cataloged persona_lens", "persona_lens" not in mined)
    fallback = run.persona_lens(mined)
    _check("mined persona falls back to surface_preference", fallback["starting_surface"] == mined.get("surface_preference", "surface chosen by scenario"))
    _check("mined persona fallback evidence_emphasis ties to risk_focus", fallback["evidence_emphasis"] == (", ".join(mined.get("risk_focus", [])) or "scenario minimum evidence"))


def main():
    _test_select_transports()
    _test_scenario_transport_contracts()
    _test_emit_run_transport_axis()
    _test_transport_suite_preview()
    _test_persona_lens_promotion()
    print("PASS")


if __name__ == "__main__":
    main()

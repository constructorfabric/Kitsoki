#!/usr/bin/env python3
"""Extracted from run.py: emit module (see tools/product-journey/README.md)."""

import datetime
import json
import sys
from pathlib import Path
from typing import Optional

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from tools.persona_qa.transports import (
    TRANSPORT_PROFILES,
)


from common import (
    CATALOG_TIERS,
    DEFAULT_DRIVER_ID,
    EVIDENCE_FILE_EXTENSIONS,
    STAGES,
    compact_transport_profile,
    driver_summary,
    format_case_variants,
    load_driver_manifest,
    media_kind,
    normalize_evidence_source,
    note_tier_synthesis,
    persona_lens,
    transport_profile,
)


def command_output_text(value) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return str(value)


def _meta_value(project):
    meta = {
        "id": project["id"],
        "label": project.get("label", project["id"]),
        "status": project.get("status", "planned"),
        "notes": project.get("notes", ""),
        "manifest": project.get("manifest"),
    }
    for key in ["repo", "stack", "license_spdx", "bug_query", "open_bug_floor", "source"]:
        if project.get(key) is not None:
            meta[key] = project[key]
    return meta


def resolve_project(catalog: dict, github_targets: dict, project_id: str) -> dict:
    target = next((t for t in catalog["targets"] if t["id"] == project_id), None)
    if target is not None:
        resolved = dict(target)
        resolved.setdefault("source", "catalog")
        return resolved

    target = next((t for t in github_targets["targets"] if t["id"] == project_id), None)
    if target is not None:
        resolved = dict(target)
        resolved.setdefault("source", "github-targets")
        resolved.setdefault("run_mode", "github-matrix")
        return resolved

    known = ", ".join(
        [t["id"] for t in catalog["targets"]]
        + [t["id"] for t in github_targets["targets"]]
    )
    raise SystemExit(f"Unknown project '{project_id}'. Known: {known}")


def target_status(project: dict) -> str:
    if project.get("validation_command"):
        return "ready-heavy-check"
    if project.get("run_mode") == "external-benchmark" and project.get("status") == "validated":
        return "cached_validated"
    if project.get("source") == "github-targets" or project.get("run_mode") == "github-matrix":
        return "planned"
    return project.get("status", "planned")


def stage_plan(project: dict, scenarios: list[dict]) -> list[dict]:
    readiness = target_status(project)
    stages: list[dict] = []
    for stage in STAGES:
        status = "planned"
        evidence: list[str] = []
        stage_scenarios = [scenario["id"] for scenario in scenarios if scenario["stage"] == stage]
        if stage == "score_and_report":
            status = readiness
            evidence.append(project.get("manifest") or project.get("validation_command") or project.get("bug_query") or "catalog target")
        elif stage in {"discover_product", "follow_tutorial", "file_product_issue"}:
            status = "planned"
            evidence.append("requires visual MCP/browser evidence in live or cassette run")
        elif stage == "onboard_project":
            status = "planned"
            evidence.append(project.get("manifest") or project.get("repo") or "project onboarding fixture pending")
        elif stage in {"plan_project_work", "fix_bug"}:
            status = readiness if project.get("manifest") else "planned"
            evidence.append(project.get("manifest") or project.get("bug_query") or "bug/design fixture pending")
        stages.append({"id": stage, "status": status, "evidence": evidence, "scenarios": stage_scenarios})
    return stages


def scenario_plan(scenarios: list[dict]) -> list[dict]:
    planned = []
    for scenario in scenarios:
        item = {
            "id": scenario["id"],
            "label": scenario["label"],
            "stage": scenario["stage"],
            "task": scenario["task"],
            "primary_story": scenario["primary_story"],
            "required_mcp": scenario["required_mcp"],
            "evidence": scenario["evidence"],
            "success_criteria": scenario["success_criteria"],
            "status": "planned",
            "evidence_status": "missing",
            "artifacts": {},
        }
        if scenario.get("natural_utterances"):
            item["natural_utterances"] = scenario["natural_utterances"]
        if scenario.get("case_variants"):
            item["case_variants"] = scenario["case_variants"]
        if scenario.get("transports"):
            item["transports"] = scenario["transports"]
        planned.append(item)
    return planned


def evidence_plan(run_json: dict) -> dict:
    items = []
    for scenario in run_json["scenarios"]:
        for evidence_kind in scenario["evidence"]:
            items.append({
                "scenario": scenario["id"],
                "kind": evidence_kind,
                "status": "missing",
                "path": "",
                "source": "unknown",
                "notes": "Attach from visual MCP, Kitsoki MCP trace, oracle runner, or generated artifact.",
            })
    return {
        "run_id": run_json["run_id"],
        "items": items,
        "summary": {
            "required": len(items),
            "present": 0,
            "missing": len(items),
        },
    }


def build_driver_journal(run_id: str, items: list[dict]) -> dict:
    statuses = {}
    modes = {}
    scenarios = set()
    for item in items:
        status = item.get("status", "attempted")
        mode = item.get("dispatch_mode", "")
        statuses[status] = statuses.get(status, 0) + 1
        if mode:
            modes[mode] = modes.get(mode, 0) + 1
        if item.get("scenario"):
            scenarios.add(item["scenario"])
    return {
        "run_id": run_id,
        "items": items,
        "summary": {
            "events": len(items),
            "scenarios_attempted": len(scenarios),
            "statuses": statuses,
            "dispatch_modes": modes,
        },
    }


def render_driver_journal(journal: dict) -> str:
    lines = [
        "# Product journey driver journal",
        "",
        f"- Run: `{journal['run_id']}`",
        f"- Events: {journal['summary']['events']}",
        f"- Scenarios attempted: {journal['summary']['scenarios_attempted']}",
        "",
    ]
    if not journal["items"]:
        lines.append("- (no driver events recorded)")
        return "\n".join(lines) + "\n"
    for item in journal["items"]:
        lines.extend([
            f"## {item['id']}",
            "",
            f"- Scenario: `{item['scenario']}`",
            f"- Dispatch mode: `{item['dispatch_mode']}`",
            f"- Status: `{item['status']}`",
            f"- Created: {item['created_at']}",
            f"- MCP tools: {', '.join(item.get('mcp_tools', [])) or '(none recorded)'}",
            f"- Evidence refs: {', '.join(item.get('evidence_refs', [])) or '(none)'}",
            f"- Blockers: {', '.join(item.get('blockers', [])) or '(none)'}",
            "",
            item.get("summary", ""),
            "",
        ])
    return "\n".join(lines) + "\n"


def render_capture_preflight_markdown(result: dict) -> str:
    lines = [
        "# Product Journey Capture Preflight",
        "",
        f"- Status: `{result['status']}`",
        f"- Preflight: `{result['preflight_id']}`",
        f"- Created: {result['created_at']}",
        f"- Output: `{result['webshot_output']}`",
        "",
        "## Checks",
        "",
    ]
    for check in result["checks"]:
        lines.append(f"- `{check['id']}`: {check['status']} - {check['summary']}")
    if result.get("stderr"):
        lines.extend(["", "## stderr", "", "```", result["stderr"][-4000:], "```"])
    if result.get("stdout"):
        lines.extend(["", "## stdout", "", "```", result["stdout"][-4000:], "```"])
    return "\n".join(lines) + "\n"


def parse_preflight_time(value: str) -> Optional[datetime.datetime]:
    value = str(value or "").strip()
    if not value:
        return None
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    try:
        return datetime.datetime.fromisoformat(value)
    except ValueError:
        return None


def quota_preflight_check(path: Path, now: Optional[datetime.datetime] = None) -> tuple[bool, str]:
    if now is None:
        now = datetime.datetime.now(datetime.timezone.utc)
    if now.tzinfo is None:
        now = now.replace(tzinfo=datetime.timezone.utc)
    if not path.exists():
        return True, f"{path} (not present; no quota cooldown recorded)"
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as exc:
        return False, f"{path}: read failed: {exc}"
    if not raw.strip():
        return True, f"{path} (empty; no quota cooldown recorded)"
    try:
        state = json.loads(raw)
    except json.JSONDecodeError as exc:
        return False, f"{path}: invalid JSON: {exc}"
    if not isinstance(state, dict):
        return False, f"{path}: expected object"
    if state.get("schema") not in {"", None, "kitsoki/provider-quota/v1"}:
        return False, f"{path}: unexpected schema {state.get('schema')}"
    profiles = state.get("profiles", {})
    if profiles is None:
        profiles = {}
    if not isinstance(profiles, dict):
        return False, f"{path}: profiles must be an object"
    blocked: list[str] = []
    for profile, data in profiles.items():
        if not isinstance(data, dict):
            return False, f"{path}: profile {profile} must be an object"
        for key in ("backoff_until", "last_throttle_until"):
            ts = parse_preflight_time(str(data.get(key, "")))
            if ts is not None and ts.tzinfo is None:
                ts = ts.replace(tzinfo=datetime.timezone.utc)
            if ts is not None and ts > now:
                blocked.append(f"{profile} {key}={data.get(key)}")
    if blocked:
        return False, "provider quota cooldown active: " + "; ".join(blocked[:5])
    return True, f"{path}: {len(profiles)} profile(s), no active cooldown"


def mcp_step(tool: str) -> str:
    steps = {
        "visual.open": "Open the local product site or relevant browser surface.",
        "visual.observe": "Capture the current browser frame or retained screenshot reference.",
        "visual.act": "Perform the next natural browser action for the persona.",
        "session.open": "Open or resume the Kitsoki story session for this scenario.",
        "session.inspect": "Inspect the current Kitsoki session state and trace context.",
        "render.tui": "Capture the rendered TUI or web frame for the current room.",
    }
    return steps.get(tool, f"Use {tool} and capture its output.")


def evidence_capture_hint(kind: str) -> str:
    hints = {
        "browser_screenshot": "Save a retained visual MCP screenshot or PNG reference.",
        "command_output": "Save a command transcript with cwd, command line, exit code, stdout/stderr, and any trace reference needed to replay the result.",
        "page_url": "Record the exact local URL or GitHub page used.",
        "navigation_trace": "Record the browser action sequence that reached the finding.",
        "checkpoint_rating": "Rate whether the persona could proceed without private context.",
        "session_trace": "Save the Kitsoki session trace or trace id.",
        "rendered_tui_frame": "Save the rendered TUI/web frame for the room under review.",
        "generated_config_diff": "Save the generated config diff or a no-change note.",
        "onboarding_smoke_result": "Save the deterministic onboarding smoke result.",
        "candidate_diff": "Save the candidate patch diff.",
        "oracle_result": "Save the hidden or targeted oracle result.",
        "full_suite_result": "Save full-suite output or a classified reason it was skipped.",
        "key_interaction_video": "Save an MP4/GIF clip or retained video reference for Slidey playback.",
        "prd_artifact": "Save the PRD artifact generated during the scenario.",
        "design_artifact": "Save the design artifact generated during the scenario.",
        "review_notes": "Save reviewer notes, objections, and unresolved questions.",
        "implementation_diff": "Save the implementation diff.",
        "targeted_test_result": "Save targeted deterministic test output.",
        "review_summary": "Save the final implementation review summary.",
        "bug_report_markdown": "Save the product bug report markdown.",
        "screenshot_or_tui_png": "Save screenshot or TUI PNG evidence.",
        "trace_reference": "Save the trace reference for reproduction.",
        "reproduction_steps": "Save deterministic reproduction steps.",
        "rrweb": "Save a local rrweb session capture JSON (real recorded browser session, not a cassette:// ref) so the scenario replays in an rrweb viewer.",
        "trace-replay": "Save a local Kitsoki trace file replayable via `kitsoki trace to-flow` / `test flows` (a real path, not cassette://).",
        "flow-fixture": "Save a local flow fixture (`kitsoki test flows <app.yaml> --flows ...`) that replays this scenario no-LLM.",
        "png-sequence": "Save a local directory or manifest of PNG frames captured via render.tui/visual.observe for frame-by-frame playback.",
        "ide_context_capture": "Save the post-drive host.ide.* ide.context_captured trace event JSON (vscode legs' opportunistic editor-level tier) -- leave unattached and report honestly when no real editor was connected/queried.",
        "trace-derived-flow": "Save the flow fixture generated from the real source trace with `kitsoki trace to-flow`.",
        "host-cassette": "Save the host cassette generated beside the trace-derived flow; it must cite the source trace.",
        "provider_config_receipt": "Save the provider setup receipt or the visible blocker that prevented provider setup.",
        "project_profile": "Save the generated project profile and project-local story app path.",
        "artifact_open_evidence": "Save screenshot, trace event, or IDE/open-file evidence proving the generated artifact was opened.",
        "github_issue_url": "Save the selected live GitHub issue URL.",
        "pull_request_url": "Save the real pull request URL opened by the run.",
        "slidey_deck": "Save the Slidey deck source that embeds the accepted replay videos and links the evidence manifest.",
    }
    return hints.get(kind, "Save this evidence artifact and attach it to the run.")


def scenario_quality_gate(scenario_id: str) -> dict:
    gates = {
        "product-discovery": {
            "minimum_evidence": ["rendered_tui_frame", "session_trace", "navigation_trace", "checkpoint_rating", "key_interaction_video"],
            "done_when": "The persona can state what Kitsoki is, who it is for, and one credible next action after walking the product overview story.",
            "block_if": [
                "The product-site story cannot be opened or rendered.",
                "The walkthrough requires live LLM authorization and no cassette exists.",
                "The discovery checkpoint (what it is + next action) cannot be reached deterministically.",
            ],
        },
        "project-onboarding": {
            "minimum_evidence": ["session_trace", "rendered_tui_frame", "generated_config_diff", "onboarding_smoke_result", "key_interaction_video"],
            "done_when": "The persona can identify the generated project profile, the relevant commands/files, and the next Kitsoki story to launch.",
            "block_if": [
                "The onboarding story cannot be opened or rendered.",
                "The path requires live LLM authorization and no cassette exists.",
                "Generated config or smoke output is unavailable for deterministic review.",
            ],
        },
        "gears-first-run-web-demo": {
            "minimum_evidence": [
                "session_trace",
                "trace-replay",
                "trace-derived-flow",
                "host-cassette",
                "provider_config_receipt",
                "project_profile",
                "prd_artifact",
                "artifact_open_evidence",
                "github_issue_url",
                "candidate_diff",
                "oracle_result",
                "full_suite_result",
                "pull_request_url",
                "key_interaction_video",
                "slidey_deck",
            ],
            "done_when": "The web demo can be replayed from real-run-derived fixtures and the deck links provider setup, project onboarding, PRD artifact, selected issue, fix evidence, and PR URL.",
            "block_if": [
                "Any embedded video lacks a source trace and trace-derived replay fixture.",
                "Provider setup, PRD artifact, selected issue, or pull request URL is missing from the evidence manifest.",
                "The selected Gears Rust issue does not have enough reproduction detail to produce credible fix evidence.",
            ],
        },
        "tui-slash-commands": {
            "minimum_evidence": ["rendered_tui_frame", "session_trace", "navigation_trace", "checkpoint_rating", "key_interaction_video"],
            "done_when": "A docs-trained persona can discover the slash menu, filter it, Tab-complete the primary suggestion, and execute the completed command in the TUI.",
            "block_if": [
                "The TUI frame does not show a slash command menu after typing / at the beginning of the prompt.",
                "Filtering with letters after / does not narrow the visible command list.",
                "No primary suggestion is visibly marked as the Tab target.",
                "Tab completion or Enter execution cannot be captured without live LLM authorization.",
            ],
        },
        "bugfix": {
            "minimum_evidence": ["session_trace", "candidate_diff", "oracle_result", "full_suite_result", "key_interaction_video"],
            "done_when": "A concrete bug candidate has a reviewable diff plus deterministic oracle/test output or a classified suite failure.",
            "block_if": [
                "No concrete bug/repro can be selected without live authorization.",
                "The bugfix story cannot produce a candidate diff.",
                "No deterministic oracle, targeted test, or classified full-suite result is available.",
            ],
        },
        "dogfood-marathon-tui": {
            "minimum_evidence": ["rendered_tui_frame", "key_interaction_video", "png-sequence"],
            "done_when": "One continuous kitsoki-dev TUI recording shows the real arrow-key menus, the 15-case autonomous marathon, the serious exception decision, and the final report/deck evidence.",
            "block_if": [
                "The recorder owns a private case list instead of consuming the scenario run bundle.",
                "The proof skips or fakes the TUI arrow-key choice widgets.",
                "The video is too fast to understand without a fast-forward/cassette affordance or frame/chapter evidence.",
                "The serious exception lacks an Issue reference, trace reference, warning affordance, or real decision question.",
                "The capture is not attached back to the universal scenario run as video and PNG sequence evidence.",
            ],
        },
        "docs-to-mcp-first-run": {
            "minimum_evidence": ["browser_screenshot", "rendered_tui_frame", "session_trace", "navigation_trace", "checkpoint_rating", "key_interaction_video"],
            "done_when": "A cold persona can move from docs/product-site material to a verified Studio MCP-backed scenario QA run with run, report, and deck paths.",
            "block_if": [
                "The docs path does not lead to a reusable story-owned scenario QA surface.",
                "Studio MCP identity or story loadability cannot be verified before dispatch.",
                "The run bundle, driver handoff, report, or deck path is missing from the evidence.",
            ],
        },
        "agent-launch-experience": {
            "minimum_evidence": ["session_trace", "rendered_tui_frame", "browser_screenshot", "checkpoint_rating", "review_notes", "key_interaction_video"],
            "done_when": "The persona can launch a Kitsoki-backed agent run with visible profile/state parity and supported operator-question behavior.",
            "block_if": [
                "The selected story/profile/model boundary is not visible before live dispatch.",
                "Web and TUI surfaces disagree about current state or next action.",
                "A needed operator question silently defaults instead of using operator-ask or recording a replay blocker.",
            ],
        },
        "remote-worker-campaign": {
            "minimum_evidence": ["session_trace", "command_output", "review_notes", "bug_report_markdown", "trace-replay"],
            "done_when": "A bounded remote worker or arena batch has a readiness receipt, attached evidence, issue-pipeline routing, and refreshed campaign artifacts.",
            "block_if": [
                "Remote readiness, gh-agent readiness, watchdog state, or ticket repo cannot be verified before dispatch.",
                "Worker placement does not leave a durable receipt naming worker, budget, scenarios, and run directory.",
                "Credible findings bypass the evidence-backed issue/fix pipeline or local stabilization sink rules.",
            ],
        },
        "campaign-rollup-review": {
            "minimum_evidence": ["session_trace", "review_summary", "checkpoint_rating", "rrweb"],
            "done_when": "The stakeholder rollup is regenerated from artifacts and conservatively reports coverage, evidence gaps, issue/fix state, cost, deck link, and next campaign slice.",
            "block_if": [
                "The summary relies on conversation memory instead of retained artifacts.",
                "A failed evidence, validation, issue, or gh-agent gate is summarized as passed.",
                "The Slidey deck claims coverage without playback media or an explicit blocker.",
            ],
        },
        "prd-design": {
            "minimum_evidence": ["session_trace", "prd_artifact", "design_artifact", "review_notes", "key_interaction_video"],
            "done_when": "The PRD/design artifact cites real repo files or commands, is reviewably scoped, and exposes open questions.",
            "block_if": [
                "The planning/design path requires live LLM authorization and no cassette exists.",
                "The artifact cannot be grounded in repository files or commands.",
                "The design output cannot be captured as a durable artifact.",
            ],
        },
        "feature-implementation": {
            "minimum_evidence": ["session_trace", "implementation_diff", "targeted_test_result", "review_summary", "key_interaction_video"],
            "done_when": "The implementation follows an accepted design slice and has a targeted deterministic test result or explicit blocker.",
            "block_if": [
                "No accepted design slice is available.",
                "The implementation would require live LLM authorization without a cassette.",
                "No diff or deterministic validation output can be captured.",
            ],
        },
        "evidence-backed-product-bug": {
            "minimum_evidence": ["bug_report_markdown", "screenshot_or_tui_png", "trace_reference", "reproduction_steps", "key_interaction_video"],
            "done_when": "A product bug report includes expected vs actual behavior, reproduction context, visual/TUI evidence, and trace reference.",
            "block_if": [
                "No product issue, weakness, or confusing behavior was observed.",
                "The evidence needed to reproduce the issue cannot be captured or safely redacted.",
                "The report would rely on memory rather than trace or visual evidence.",
            ],
        },
    }
    return gates.get(scenario_id, {
        "minimum_evidence": [],
        "done_when": "The scenario has captured evidence or an explicit blocker.",
        "block_if": ["The scenario cannot capture evidence under the current harness."],
    })


def leg_quality_gate(scenario_id: str, evidence_kinds: list[str], transport: str = "") -> dict:
    gate = dict(scenario_quality_gate(scenario_id))
    gate["block_if"] = list(gate.get("block_if", []))
    if not evidence_kinds:
        return gate
    evidence_set = set(evidence_kinds)
    minimum = [
        kind for kind in gate.get("minimum_evidence", [])
        if kind in evidence_set
    ]
    for kind in ["command_output", "trace-replay", "flow-fixture", "png-sequence", "rrweb"]:
        if kind in evidence_set and kind not in minimum:
            minimum.append(kind)
    if not minimum:
        minimum = list(evidence_kinds[: min(3, len(evidence_kinds))])
    gate["minimum_evidence"] = minimum
    if transport == "cli":
        gate["done_when"] = (
            gate.get("done_when", "")
            + " For CLI legs, command_output must include the command line, cwd, exit code, stdout/stderr, and any trace reference needed to replay the result."
        ).strip()
    return gate


def build_assignment_scenario_task(target: dict, persona: dict, scenario: dict) -> dict:
    repo = target["label"]
    stack = target.get("stack", "unknown stack")
    bug_query = target.get("bug_query", "")
    persona_label = persona["label"]
    risk_focus = ", ".join(persona.get("risk_focus", []))
    base = {
        "scenario": scenario["id"],
        "label": scenario["label"],
        "target": target["id"],
        "persona": persona["id"],
        "primary_story": scenario["primary_story"],
        "required_mcp": scenario["required_mcp"],
        "evidence": scenario["evidence"],
        "success_criteria": scenario["success_criteria"],
        "case_variants": scenario.get("case_variants", []),
    }
    prompts = {
        "product-discovery": (
            f"As a {persona_label}, start from the local Kitsoki product site and decide whether it credibly explains how to use Kitsoki on {repo} ({stack}). "
            f"Focus on {risk_focus}. Capture the first confusing claim, missing prerequisite, or clear next action."
        ),
        "project-onboarding": (
            f"Onboard {repo} using Kitsoki's documented project setup path. Confirm the generated project profile names plausible {stack} commands, repo files, and the next story to launch."
        ),
        "tui-slash-commands": (
            "Act as a user who just read the external Codex and Claude Code slash-command docs, not Kitsoki docs. "
            "In the TUI, type `/` at the beginning of the prompt, confirm a command menu appears, type letters to filter it, press Tab to accept the primary suggestion, then press Enter and verify the completed slash command runs."
        ),
        "bugfix": (
            f"Use the target bug queue for {repo}: {bug_query}. Pick or simulate one concrete bug candidate from that queue, drive the bugfix story, and require deterministic oracle/test evidence before calling the fix credible."
        ),
        "dogfood-marathon-tui": (
            "Start in the real kitsoki-dev TUI, ask `I want to do a dogfood marathon`, then type `start the marathon`. "
            "Capture one continuous interactive session that uses real arrow-key choice widgets, visibly processes all 15 cataloged bugs, surfaces the serious exception as a real decision, and ends with the aggregate report and per-bug deck evidence."
        ),
        "prd-design": (
            f"Turn one small improvement idea for {repo} into a PRD/design artifact. The idea should be grounded in {repo}'s stack ({stack}), existing project conventions, and the {persona_label} risk focus: {risk_focus}."
        ),
        "feature-implementation": (
            f"Implement or dry-run a small accepted design slice for {repo}. Keep the change reviewable for a {persona_label}, and validate with targeted deterministic tests or an explicit blocker."
        ),
        "evidence-backed-product-bug": (
            f"File a Kitsoki product bug discovered while working on {repo}. Include expected vs actual behavior, reproduction context, visual/TUI evidence, and a trace reference."
        ),
    }
    prompt_parts = [prompts.get(scenario["id"], scenario["task"])]
    variants = format_case_variants(scenario.get("case_variants", []))
    if variants:
        prompt_parts.append(variants)
    base["task_prompt"] = "\n\n".join(prompt_parts)
    base["evidence_dir"] = f"evidence/{target['id']}--{persona['id']}/{scenario['id']}"
    base["bug_query"] = bug_query if scenario["id"] == "bugfix" else ""
    return base


def driver_harness(primary_story: str) -> str:
    if primary_story == "product-site":
        return "browser"
    if "bugfix" in primary_story:
        return "record-or-live-with-deterministic-oracle"
    return "replay-or-record"


def driver_visual_surface(primary_story: str, required_mcp: list[str]) -> str:
    if "visual.open" in required_mcp and primary_story == "product-site":
        return "web"
    if "render.tui" in required_mcp or "session.open" in required_mcp:
        return "tui"
    if "visual.observe" in required_mcp:
        return "web-or-tui"
    return "artifact"


def transport_for_visual_surface(visual_surface: str, required_mcp: list[str]) -> str:
    if visual_surface in TRANSPORT_PROFILES:
        return visual_surface
    if visual_surface == "web-or-tui":
        return "web" if "visual.observe" in required_mcp and "render.tui" not in required_mcp else "tui"
    if visual_surface == "artifact":
        return "cli" if any(tool in required_mcp for tool in ["session.trace", "session.inspect", "session.status"]) else "tui"
    return "tui"


def scenario_tier(scenario: dict) -> str:
    """Return this scenario's corpus tier (see schema.json `tier`).

    An explicit `tier` wins. Absent that, a scenario that declares
    `transports` is treated as curated (it was reviewed enough to author a
    real contract); everything else is treated as mined, matching the
    inference `resolve_scenario_transports()` already used before `tier`
    existed.
    """
    declared = scenario.get("tier")
    if declared in CATALOG_TIERS:
        return declared
    return "curated" if scenario.get("transports") else "mined"


def default_scenario_transports(scenario: dict) -> dict:
    """Derive an implicit transports contract from a scenario's required_mcp.

    Scenarios authored before the `transports` field existed, and every mined
    scenario (generated from session transcripts rather than hand-authored),
    don't declare it. This mirrors driver_visual_surface()'s existing
    single-surface inference so a scenario missing the field keeps behaving
    exactly as it did before --transport existed, and only gains additional
    transports when it explicitly opts in via scenarios.json. Synthesizing
    this contract for a tier=mined scenario prints and records a visible
    notice -- see note_tier_synthesis().
    """
    note_tier_synthesis("scenario", scenario.get("id", "?"), "transports", scenario_tier(scenario))
    surface = driver_visual_surface(scenario.get("primary_story", ""), scenario.get("required_mcp", []))
    if surface == "web":
        allowed = ["web"]
    elif surface == "tui":
        allowed = ["tui"]
    elif surface == "web-or-tui":
        allowed = ["tui", "web"]
    elif surface == "artifact":
        allowed = ["cli"] if "session.trace" in scenario.get("required_mcp", []) else []
    else:
        allowed = []
    return {"allowed": allowed, "required": list(allowed), "overrides": {}}


def resolve_scenario_transports(scenario: dict) -> dict:
    """Normalize a scenario's transports contract, declared or derived."""
    declared = scenario.get("transports")
    if not declared:
        return default_scenario_transports(scenario)
    allowed = list(declared.get("allowed", []))
    required = list(declared.get("required", allowed))
    overrides = declared.get("overrides", {}) or {}
    return {"allowed": allowed, "required": required, "overrides": overrides}


def scenario_transport_leg(scenario: dict, transport: str) -> dict:
    """Build a scenario view scoped to one transport leg.

    Applies the scenario's per-transport `overrides` (required_mcp/evidence),
    falling back to the scenario's base lists, and attaches the transport's
    evidence contract (capture tool, evidence kind, proof level).
    """
    profile = transport_profile(transport)
    contract = resolve_scenario_transports(scenario)
    override = contract.get("overrides", {}).get(transport, {})
    leg = dict(scenario)
    leg["required_mcp"] = list(override.get("required_mcp", scenario.get("required_mcp", [])))
    leg["evidence"] = list(override.get("evidence", scenario.get("evidence", [])))
    leg["transport"] = transport
    leg["visual_surface"] = profile["visual_surface"]
    leg["transport_profile"] = compact_transport_profile(profile)
    leg["transport_evidence_contract"] = profile["evidence_contract"]
    # editor_evidence_contract is the opportunistic, stronger tier vscode legs
    # can reach on top of the mandatory bridge-level floor above (see
    # tools/persona_qa/transports.py). Only vscode carries one today; other
    # transports simply omit the key.
    editor_contract = profile.get("editor_evidence_contract")
    if editor_contract:
        leg["editor_evidence_contract"] = editor_contract
    leg["leg_id"] = f"{scenario['id']}::{transport}"
    return leg


def scenario_transport_legs(scenario: dict, transports: list[str]) -> list[dict]:
    """Expand one scenario into its scenario x transport legs.

    Requested transports outside the scenario's allowed set are skipped
    rather than erroring -- `--transport all` runs everything applicable to
    each scenario, it does not force every scenario onto every transport.
    """
    allowed = resolve_scenario_transports(scenario).get("allowed", [])
    applicable = [transport for transport in transports if transport in allowed]
    return [scenario_transport_leg(scenario, transport) for transport in applicable]


def leg_evidence_view(kinds: list[str], tracked_items: list[dict]) -> list[dict]:
    """Build the evidence-contract view (kind/status/path/hint) for one leg.

    `tracked_items` is the scenario-level evidence.json bookkeeping (still
    scenario-scoped, not per-transport); kinds not yet tracked show as
    missing rather than being dropped, so a per-transport evidence override
    that names a kind the base scenario doesn't track still renders.
    """
    tracked = {item["kind"]: item for item in tracked_items}
    return [
        {
            "kind": kind,
            "status": tracked.get(kind, {}).get("status", "missing"),
            "path": tracked.get(kind, {}).get("path", ""),
            "capture_hint": evidence_capture_hint(kind),
        }
        for kind in kinds
    ]


def driver_action_sequence(required_mcp: list[str]) -> list[str]:
    sequence = []
    if "session.open" in required_mcp:
        sequence.append("session.new or session.attach using the scenario primary_story")
    if "render.tui" in required_mcp:
        sequence.append("render.tui or render.tui_png before and after meaningful turns")
    if "visual.open" in required_mcp:
        sequence.append("visual.open for the scenario visual surface")
    if "visual.observe" in required_mcp:
        sequence.append("visual.observe before acting and when capturing evidence")
    if "visual.act" in required_mcp:
        sequence.append("visual.act using advertised action handles or natural persona actions")
    if "session.inspect" in required_mcp:
        sequence.append("session.status/session.world first; session.inspect only when targeted reads are insufficient")
    if not sequence:
        sequence.append("capture the named evidence artifacts and record findings")
    return sequence


def scenario_live_budget(run_json: dict, scenario_id: str) -> dict:
    minutes = int(run_json.get("live_budget_minutes", 20))
    remaining_action = "record_blocker"
    if minutes == 0:
        summary = (
            "Live/model dispatch is disabled for this run. Use replay/cassette paths, "
            "or record a blocker before any live call."
        )
    else:
        summary = (
            f"Spend at most {minutes} live minutes on this scenario. When the budget is "
            "reached, stop live exploration, record the blocker or partial evidence, "
            "and journal the attempt before moving to the next scenario."
        )
    return {
        "scenario": scenario_id,
        "max_live_minutes": minutes,
        "remaining_action": remaining_action,
        "summary": summary,
        "blocker_title": "Live budget exhausted",
        "blocker_summary": (
            "The scenario reached its per-scenario live budget before enough proof evidence "
            "could be captured. Preserve trace/frame evidence and continue with the next scenario."
        ),
    }


def resolve_mcp_tools(capability: str, driver_manifest: Optional[dict] = None) -> list[str]:
    manifest = driver_manifest or load_driver_manifest()
    return list(manifest.get("_resolved_capabilities", {}).get(capability, []))


def resolved_mcp_tools(capabilities: list[str], driver_manifest: Optional[dict] = None) -> list[str]:
    tools: list[str] = []
    for capability in capabilities:
        for tool in resolve_mcp_tools(capability, driver_manifest):
            if tool not in tools:
                tools.append(tool)
    return tools


def driver_actions(scenario: dict, run_json: dict, evidence_items: list[dict], driver_manifest: Optional[dict] = None) -> list[dict]:
    scenario_id = scenario["id"]
    evidence_dir = scenario.get(
        "evidence_dir",
        f"evidence/{run_json['project']['id']}--{run_json['persona']['id']}/{scenario_id}",
    )
    required_mcp = scenario.get("required_mcp", [])
    open_tools = [
        tool for tool in ["session.open", "visual.open"]
        if tool in required_mcp
    ] or ["session.status"]
    read_tools = [
        tool for tool in ["session.status", "render.tui", "visual.observe"]
        if tool == "session.status" or tool in required_mcp
    ]
    act_tools = [
        tool for tool in ["session.submit", "session.drive", "visual.act", "session.trace"]
        if tool in {"session.submit", "session.trace"} or tool in required_mcp
    ]
    capture_tools = ["visual.observe", "render.tui", "session.trace"]
    actions = [
        {
            "id": "open_surface",
            "goal": "Open or attach the Kitsoki/product surface named by the scenario.",
            "tools": open_tools,
            "resolved_tools": resolved_mcp_tools(open_tools, driver_manifest),
            "evidence": [],
            "record": "Record the handle, URL, or reason this surface could not be opened.",
        },
        {
            "id": "read_current_frame",
            "goal": "Observe the exact operator-visible state before acting.",
            "tools": read_tools,
            "resolved_tools": resolved_mcp_tools(read_tools, driver_manifest),
            "evidence": [
                item["kind"]
                for item in evidence_items
                if item["kind"] in {"browser_screenshot", "rendered_tui_frame", "screenshot_or_tui_png"}
            ],
            "record": f"Save frame evidence under {evidence_dir}/ before evaluating usability.",
        },
        {
            "id": "act_as_persona",
            "goal": "Take the next natural persona action and preserve route/interaction evidence.",
            "tools": act_tools,
            "resolved_tools": resolved_mcp_tools(act_tools, driver_manifest),
            "evidence": [
                item["kind"]
                for item in evidence_items
                if item["kind"] in {"navigation_trace", "session_trace", "key_interaction_video", "trace_reference"}
            ],
            "record": "Prefer natural phrasing when route quality is under test; otherwise use deterministic action handles.",
        },
        {
            "id": "capture_required_evidence",
            "goal": "Attach every minimum-evidence slot or record the matching quality-gate blocker.",
            "tools": capture_tools,
            "resolved_tools": resolved_mcp_tools(capture_tools, driver_manifest),
            "evidence": [item["kind"] for item in evidence_items],
            "record": "Use attach commands for captured evidence; use blocker command for honest gaps.",
        },
        {
            "id": "journal_attempt",
            "goal": "Append the driver's actual attempt, tools used, evidence references, and blockers.",
            "tools": ["story.driver_event", "tools/product-journey/run.py --record-driver-event"],
            "resolved_tools": [],
            "evidence": ["driver-journal.md"],
            "record": "Journal the attempt even when the scenario only produced a blocker.",
        },
    ]
    natural_utterances = scenario.get("natural_utterances", [])
    if natural_utterances:
        for action in actions:
            if action["id"] == "act_as_persona":
                action["natural_utterances"] = natural_utterances
                action["record"] = (
                    "Prefer the transcript-derived utterances when route quality is under test; "
                    "otherwise use deterministic action handles."
                )
                break
    return actions


def build_media_manifest(run_json: dict, evidence: dict) -> dict:
    items = []
    for item in evidence.get("items", []):
        artifact_path = item.get("path", "")
        if item.get("status") not in {"captured", "validated"} or not artifact_path:
            continue
        kind = media_kind(item.get("kind", ""), artifact_path)
        items.append({
            "scenario": item.get("scenario", ""),
            "evidence_kind": item.get("kind", ""),
            "media_kind": kind,
            "path": artifact_path,
            "status": item.get("status", ""),
            "source": normalize_evidence_source(item.get("source", ""), artifact_path, item.get("notes", "")),
            "notes": item.get("notes", ""),
            "playback": kind in {"video", "image"},
        })
    counts: dict[str, int] = {}
    for item in items:
        counts[item["media_kind"]] = counts.get(item["media_kind"], 0) + 1
    return {
        "run_id": run_json["run_id"],
        "items": items,
        "summary": {
            "total": len(items),
            "playback_items": sum(1 for item in items if item["playback"]),
            "video": counts.get("video", 0),
            "image": counts.get("image", 0),
            "trace": counts.get("trace", 0),
            "document": counts.get("document", 0),
            "artifact": counts.get("artifact", 0),
        },
    }


def render_scenario_outcomes(outcomes: dict) -> str:
    lines = [
        "# Product journey scenario outcomes",
        "",
        f"- Run: `{outcomes['run_id']}`",
        f"- Scenarios: {outcomes['summary']['scenarios']}",
        f"- Started: {outcomes['summary']['started']}",
        f"- With findings: {outcomes['summary']['with_findings']}",
        f"- With issues or weaknesses: {outcomes['summary']['with_issues']}",
        f"- With fixes: {outcomes['summary']['with_fixes']}",
        f"- Blocked: {outcomes['summary'].get('blocked', 0)}",
        "",
    ]
    for item in outcomes["items"]:
        lines.extend([
            f"## {item['label']}",
            "",
            f"- Scenario: `{item['scenario']}`",
            f"- Stage: `{item['stage']}`",
            f"- Story: `{item['primary_story']}`",
            f"- Evidence: {item['present_evidence_count']} / {item['required_evidence_count']} ({item['evidence_status']}; proof {item.get('proof_evidence_count', 0)}, demo {item.get('demo_evidence_count', 0)})",
            f"- Outcome: `{item['outcome']}`",
            f"- Findings: strength={item['finding_counts']['strength']}, weakness={item['finding_counts']['weakness']}, issue={item['finding_counts']['issue']}, fix={item['finding_counts']['fix']}, blocked={item['finding_counts'].get('blocked', 0)}",
            "",
        ])
        for finding in item["findings"]:
            lines.append(f"- {finding['kind']}: {finding['title']} ({finding['status']})")
        if item["findings"]:
            lines.append("")
    return "\n".join(lines) + "\n"


def autonomous_fix_story_command() -> str:
    return "autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<public-gh-agent-url>"


def autonomous_watchdog_story_command() -> str:
    return "autonomous_watchdog"


def final_story_gate_commands() -> list[str]:
    return [
        autonomous_watchdog_story_command(),
        autonomous_fix_story_command(),
        "review",
        "validate",
    ]


def attach_evidence_command(run_dir_arg: str, scenario_id: str, evidence_kind: str) -> str:
    return (
        "python3 tools/product-journey/run.py --attach-evidence "
        f"--run-dir {run_dir_arg} "
        f"--scenario {scenario_id} "
        f"--evidence-kind {evidence_kind} "
        "--evidence-path <path-or-retained-id> "
        "--evidence-source <retained|external|local|cassette> "
        f"--notes \"{evidence_capture_hint(evidence_kind)}\""
    )


def record_blocker_command(run_dir_arg: str, scenario_id: str) -> str:
    return (
        "python3 tools/product-journey/run.py --record-blocker "
        f"--run-dir {run_dir_arg} "
        f"--scenario {scenario_id} "
        "--title <blocker-title> --summary <why-this-scenario-could-not-be-captured> "
        "--evidence-path <trace-or-frame-path>"
    )


def journal_attempt_command(run_dir_arg: str, scenario_id: str) -> str:
    return (
        "python3 tools/product-journey/run.py --record-driver-event "
        f"--run-dir {run_dir_arg} "
        f"--scenario {scenario_id} "
        "--dispatch-mode <replay|record|live> "
        "--driver-status <attempted|captured|blocked|validated> "
        "--mcp-tools <comma-separated-tools-used> "
        "--evidence-refs <comma-separated-paths-or-retained-ids> "
        "--blockers <comma-separated-blockers-if-any> "
        "--summary <what-the-driver-actually-tried>"
    )


def evidence_artifact_path_template(evidence_dir: str, scenario_id: str, evidence_kind: str) -> str:
    ext = EVIDENCE_FILE_EXTENSIONS.get(evidence_kind, "txt")
    return f"{evidence_dir}/{scenario_id}-{evidence_kind}.{ext}"


def capture_phase_capabilities(profile: dict, phase: str, required_mcp: list[str]) -> list[str]:
    capabilities: list[str] = []
    for capability in profile.get(f"{phase}_capabilities", []):
        if capability not in capabilities:
            capabilities.append(capability)
    phase_extras = {
        "open": ["session.open", "visual.open"],
        "observe": ["render.tui", "visual.observe", "session.trace", "session.inspect", "session.status"],
        "act": ["visual.act", "session.submit", "session.drive", "session.trace"],
    }
    for capability in phase_extras.get(phase, []):
        if capability in required_mcp and capability not in capabilities:
            capabilities.append(capability)
    return capabilities


def capture_observe_capabilities(required_mcp: list[str], visual_surface: str, evidence_kind: str, profile: Optional[dict] = None) -> list[str]:
    capabilities: list[str] = []
    if profile is not None:
        capabilities.extend(capture_phase_capabilities(profile, "observe", required_mcp))
    if evidence_kind in {"rendered_tui_frame", "png-sequence"} or visual_surface == "tui":
        if "render.tui" not in capabilities:
            capabilities.append("render.tui")
    if evidence_kind in {"browser_screenshot", "screenshot_or_tui_png", "key_interaction_video", "rrweb"} or visual_surface in {"web", "vscode", "web-or-tui"}:
        if "visual.observe" not in capabilities:
            capabilities.append("visual.observe")
    if evidence_kind == "command_output" or visual_surface == "cli":
        for capability in ["session.status", "session.trace"]:
            if capability not in capabilities:
                capabilities.append(capability)
    if evidence_kind in {"session_trace", "trace_reference", "trace-replay", "flow-fixture", "navigation_trace", "ide_context_capture"}:
        if "session.trace" not in capabilities:
            capabilities.append("session.trace")
    for capability in ["render.tui", "visual.observe", "session.trace", "session.inspect"]:
        if capability in required_mcp and capability not in capabilities:
            capabilities.append(capability)
    return capabilities or ["session.status"]


def capture_route_for_slot(
    scenario: dict,
    run_json: dict,
    run_dir_arg: str,
    evidence_kind: str,
    required_mcp: list[str],
    visual_surface: str,
    driver_manifest: Optional[dict] = None,
    leg: Optional[dict] = None,
) -> dict:
    scenario_id = scenario["id"]
    leg_transport = (leg or {}).get("transport", "")
    inferred_surface = visual_surface or driver_visual_surface(scenario.get("primary_story", ""), required_mcp)
    route_transport = leg_transport or transport_for_visual_surface(inferred_surface, required_mcp)
    profile = transport_profile(route_transport)
    route_surface = profile.get("visual_surface", route_transport)
    evidence_dir = scenario.get(
        "evidence_dir",
        f"evidence/{run_json['project']['id']}--{run_json['persona']['id']}/{scenario_id}",
    )
    open_capabilities = capture_phase_capabilities(profile, "open", required_mcp) or ["session.status"]
    observe_capabilities = capture_observe_capabilities(required_mcp, route_surface, evidence_kind, profile)
    act_capabilities = capture_phase_capabilities(profile, "act", required_mcp) or ["session.trace"]
    artifact_template = evidence_artifact_path_template(evidence_dir, scenario_id, evidence_kind)
    route = {
        "route_id": f"{scenario_id}::{route_surface or 'default'}::{evidence_kind}",
        "scenario": scenario_id,
        "evidence_kind": evidence_kind,
        "primary_story": scenario["primary_story"],
        "transport": route_transport,
        "transport_profile": compact_transport_profile(profile),
        "visual_surface": route_surface,
        "harness": driver_harness(scenario["primary_story"]),
        "dispatch_mode_arg": "<replay|record|live>",
        "live_profile_arg": "<explicit-live-profile-if-record-or-live>",
        "evidence_dir": evidence_dir,
        "artifact_path_template": artifact_template,
        "setup_entrypoint": {
            "story_load_intent": f"load run_dir={run_dir_arg}",
            "primary_session": (
                f"session.new app={scenario['primary_story']} "
                "harness=<replay|record|live> profile=<explicit-live-profile-if-record-or-live>"
            ),
            "preflight": profile.get(
                "preflight",
                "capture_preflight must pass before record/live dispatch; replay uses cassette/local fixtures only.",
            ),
        },
        "open": {
            "capabilities": open_capabilities,
            "resolved_tools": resolved_mcp_tools(open_capabilities, driver_manifest),
        },
        "observe": {
            "capabilities": observe_capabilities,
            "resolved_tools": resolved_mcp_tools(observe_capabilities, driver_manifest),
        },
        "act": {
            "capabilities": act_capabilities,
            "resolved_tools": resolved_mcp_tools(act_capabilities, driver_manifest),
        },
        "recording": {
            "start": "Start recording before the first persona action from this route.",
            "stop": "Stop recording immediately after the target evidence slot and final frame are captured.",
            "path_template": artifact_template,
            "transport_rule": profile.get("recording_rule", ""),
            "proof_source_required": "retained|external|local|cassette",
            "no_substitution": "Do not attach demo, placeholder, synthetic, or unrelated media for this route.",
        },
        "commands": {
            "attach": attach_evidence_command(run_dir_arg, scenario_id, evidence_kind),
            "blocker": record_blocker_command(run_dir_arg, scenario_id),
            "journal": journal_attempt_command(run_dir_arg, scenario_id),
        },
    }
    if leg is not None:
        route["leg_id"] = leg.get("leg_id", "")
        route["transport_evidence_contract"] = leg.get("transport_evidence_contract", {})
    else:
        route["transport_evidence_contract"] = profile.get("evidence_contract", {})
    return route


def capture_routes_for_evidence(
    scenario: dict,
    run_json: dict,
    run_dir_arg: str,
    evidence_view: list[dict],
    required_mcp: list[str],
    visual_surface: str,
    driver_manifest: Optional[dict] = None,
    leg: Optional[dict] = None,
) -> list[dict]:
    return [
        capture_route_for_slot(
            scenario,
            run_json,
            run_dir_arg,
            item["kind"],
            required_mcp,
            visual_surface,
            driver_manifest,
            leg,
        )
        for item in evidence_view
        if item.get("kind")
    ]


def _execution_plan_step(
    order: int,
    scenario: dict,
    run_json: dict,
    run_dir_arg: str,
    evidence_view: list[dict],
    required_mcp: list[str],
    driver_manifest: Optional[dict] = None,
    leg: Optional[dict] = None,
) -> dict:
    attach_commands = [
        attach_evidence_command(run_dir_arg, scenario["id"], item["kind"])
        for item in evidence_view
    ]
    blocker_command = record_blocker_command(run_dir_arg, scenario["id"])
    visual_surface = leg.get("visual_surface", leg["transport"]) if leg is not None else driver_visual_surface(scenario["primary_story"], required_mcp)
    evidence_kinds = [item["kind"] for item in evidence_view if item.get("kind")]
    quality_gate = (
        leg_quality_gate(scenario["id"], evidence_kinds, leg.get("transport", ""))
        if leg is not None else scenario_quality_gate(scenario["id"])
    )
    step = {
        "order": order,
        "scenario": scenario["id"],
        "label": scenario["label"],
        "stage": scenario["stage"],
        "persona": run_json["persona"]["id"],
        "project": run_json["project"]["id"],
        "task": scenario["task"],
        "task_prompt": scenario.get("task_prompt", scenario["task"]),
        "natural_utterances": scenario.get("natural_utterances", []),
        "case_variants": scenario.get("case_variants", []),
        "primary_story": scenario["primary_story"],
        "live_budget": scenario_live_budget(run_json, scenario["id"]),
        "mcp_steps": [
            {"tool": tool, "instruction": mcp_step(tool)}
            for tool in required_mcp
        ],
        "evidence": evidence_view,
        "success_criteria": scenario["success_criteria"],
        "quality_gate": quality_gate,
        "attach_commands": attach_commands,
        "record_blocker_command": blocker_command,
        "capture_routes": capture_routes_for_evidence(
            scenario,
            run_json,
            run_dir_arg,
            evidence_view,
            required_mcp,
            visual_surface,
            driver_manifest,
            leg,
        ),
    }
    if leg is not None:
        step["transport"] = leg["transport"]
        step["leg_id"] = leg["leg_id"]
        step["transport_profile"] = leg["transport_profile"]
        step["transport_evidence_contract"] = leg["transport_evidence_contract"]
    return step


def build_agent_brief(run_json: dict, evidence: dict, execution_plan: dict, driver_manifest: Optional[dict] = None) -> dict:
    persona = run_json["persona"]
    lens = persona_lens(persona)
    driver_manifest = driver_manifest or load_driver_manifest()
    missing_evidence = [
        {"scenario": item["scenario"], "kind": item["kind"], "hint": evidence_capture_hint(item["kind"])}
        for item in evidence.get("items", [])
        if item.get("status") == "missing"
    ]
    return {
        "run_id": run_json["run_id"],
        "driver": driver_summary(driver_manifest),
        "project": run_json["project"],
        "persona": persona,
        "mission": (
            "Drive the product journey as this persona using the concrete tools named by the driver manifest. "
            "Capture evidence, record concrete findings, and avoid treating planned steps as validated."
        ),
        "recommended_agent": ".agents/agents/product-journey-qa-driver.md",
        "driver_plan": "driver-plan.json",
        "driver_plan_markdown": "driver-plan.md",
        "persona_contract": {
            "id": persona["id"],
            "label": persona["label"],
            "description": persona["description"],
            "surface_preference": persona.get("surface_preference", ""),
            "risk_focus": persona.get("risk_focus", []),
            "lens": lens,
        },
        "operating_rules": [
            "Read the current visual or Kitsoki frame before choosing the next action.",
            "Use natural persona phrasing; do not optimize only for the scripted happy path.",
            "Prefer MCP evidence over prose claims: screenshots, session traces, TUI frames, diffs, oracle output, and videos.",
            "Record strengths as well as weaknesses, issues, and fixes.",
            "If a live LLM or paid service would be required, stop and record the blocker instead of calling it from an automated test.",
            "Attach every useful artifact, then submit autonomous_fix when credible issue findings exist; the native gate runs the autonomous watchdog before filing or fixing. Review and validate through the story session. Use the CLI fallback commands only when the story session is unavailable.",
            "Use scenario affordance names from the driver manifest; scenarios and findings should not depend on raw selectors.",
        ],
        "scenario_order": [
            {
                "id": step["scenario"],
                "label": step["label"],
                "task": step["task"],
                "task_prompt": step.get("task_prompt", step["task"]),
                "primary_story": step["primary_story"],
                "mcp_tools": [mcp["tool"] for mcp in step["mcp_steps"]],
                "resolved_mcp_tools": resolved_mcp_tools([mcp["tool"] for mcp in step["mcp_steps"]], driver_manifest),
                "success_criteria": step["success_criteria"],
                "evidence": [item["kind"] for item in step["evidence"]],
                "natural_utterances": step.get("natural_utterances", []),
                "case_variants": step.get("case_variants", []),
                "quality_gate": step.get("quality_gate", scenario_quality_gate(step["scenario"])),
                "live_budget": step.get("live_budget", scenario_live_budget(run_json, step["scenario"])),
                "capture_routes": step.get("capture_routes", []),
                **({
                    "transport": step["transport"],
                    "leg_id": step["leg_id"],
                    "transport_profile": step.get("transport_profile", {}),
                } if "transport" in step else {}),
            }
            for step in execution_plan.get("steps", [])
        ],
        "missing_evidence": missing_evidence,
        "finalize_commands": execution_plan.get("finalize_commands", []),
    }


def _driver_plan_evidence_view(kinds: list[str], tracked_items: list[dict]) -> list[dict]:
    view = leg_evidence_view(kinds, tracked_items)
    for item in view:
        item["playback_candidate"] = (
            media_kind(item["kind"], item["path"]) in {"video", "image"}
            or item["kind"] in {"browser_screenshot", "key_interaction_video", "screenshot_or_tui_png"}
        )
    return view


def _driver_plan_entry(
    scenario: dict,
    run_json: dict,
    run_dir_arg: str,
    lens: dict,
    step: dict,
    required_mcp: list[str],
    evidence_view: list[dict],
    driver_action_scenario: dict,
    driver_action_evidence: list[dict],
    visual_surface: str,
    driver_manifest: Optional[dict] = None,
    leg: Optional[dict] = None,
) -> dict:
    scenario_id = scenario["id"]
    capture_routes = capture_routes_for_evidence(
        scenario,
        run_json,
        run_dir_arg,
        evidence_view,
        required_mcp,
        visual_surface,
        driver_manifest,
        leg,
    )
    # vscode legs get ONE extra capture route for the opportunistic
    # editor-level tier (tools/persona_qa/transports.py's
    # editor_evidence_contract), on top of the mandatory bridge-level route
    # capture_routes_for_evidence already emitted above. It is additive, not
    # a replacement -- see docs/persona-qa.md and record_leg_result.star for
    # how a missing editor-level route keeps a vscode leg at bridge-level
    # (degraded-evidence) rather than failing outright.
    editor_contract = (leg or {}).get("editor_evidence_contract")
    if editor_contract:
        editor_leg = dict(leg)
        editor_leg["transport_evidence_contract"] = editor_contract
        capture_routes = list(capture_routes) + [
            capture_route_for_slot(
                scenario,
                run_json,
                run_dir_arg,
                editor_contract.get("evidence_kind", "ide_context_capture"),
                required_mcp,
                visual_surface,
                driver_manifest,
                editor_leg,
            )
        ]
    entry = {
        "scenario": scenario_id,
        "label": scenario["label"],
        "stage": scenario["stage"],
        "primary_story": scenario["primary_story"],
        "task_prompt": scenario.get("task_prompt", scenario["task"]),
        "evidence_dir": scenario.get("evidence_dir", f"evidence/{run_json['project']['id']}--{run_json['persona']['id']}/{scenario_id}"),
        "harness": driver_harness(scenario["primary_story"]),
        "visual_surface": visual_surface,
        "live_budget": scenario_live_budget(run_json, scenario_id),
        "required_mcp": required_mcp,
        "resolved_mcp_tools": resolved_mcp_tools(required_mcp, driver_manifest),
        "action_sequence": driver_action_sequence(required_mcp),
        "driver_actions": driver_actions(driver_action_scenario, run_json, driver_action_evidence, driver_manifest),
        "capture_routes": capture_routes,
        "persona_prompts": [
            f"Act as {run_json['persona']['label']}: {run_json['persona']['description']}",
            f"Risk focus: {', '.join(run_json['persona'].get('risk_focus', []))}",
            f"Start from: {lens['starting_surface']}",
            f"First skepticism check: {lens['first_question']}",
            f"Escalate when: {lens['escalation_trigger']}",
            f"Evidence emphasis: {lens['evidence_emphasis']}",
            "Use natural operator phrasing where route quality or prompt quality is under test.",
        ],
        "natural_utterances": scenario.get("natural_utterances", []),
        "case_variants": scenario.get("case_variants", []),
        "persona_lens": lens,
        "evidence": evidence_view,
        "success_criteria": scenario["success_criteria"],
        "quality_gate": (
            leg_quality_gate(
                scenario_id,
                [item["kind"] for item in evidence_view if item.get("kind")],
                leg.get("transport", ""),
            )
            if leg is not None else scenario_quality_gate(scenario_id)
        ),
        "attach_commands": step.get("attach_commands", []),
        "record_finding_command": (
            "python3 tools/product-journey/run.py --record-finding "
            f"--run-dir {run_dir_arg} "
            "--finding-kind <strength|weakness|issue|fix> "
            f"--scenario {scenario_id} "
            "--title <title> --summary <summary> --evidence-path <path-or-retained-id>"
        ),
        "record_blocker_command": step.get("record_blocker_command", (
            record_blocker_command(run_dir_arg, scenario_id)
        )),
        "journal_command": journal_attempt_command(run_dir_arg, scenario_id),
    }
    if leg is not None:
        entry["transport"] = leg["transport"]
        entry["leg_id"] = leg["leg_id"]
        entry["transport_profile"] = leg["transport_profile"]
        entry["transport_evidence_contract"] = leg["transport_evidence_contract"]
        if leg.get("editor_evidence_contract"):
            entry["editor_evidence_contract"] = leg["editor_evidence_contract"]
    return entry


def render_driver_plan(plan: dict) -> str:
    driver = plan.get("driver", {})
    lines = [
        "# Product journey driver plan",
        "",
        f"- Run: `{plan['run_id']}`",
        f"- Driver: `{plan['driver_agent']}`",
        f"- Driving surface: `{driver.get('id', DEFAULT_DRIVER_ID)}` ({driver.get('label', '')})",
        f"- Project: `{plan['project']['label']}`",
        f"- Persona: `{plan['persona']['label']}`",
        "",
    ]
    if driver.get("affordances"):
        lines.extend(["## Driver Affordances", ""])
        for name, selector in sorted(driver.get("affordances", {}).items()):
            lines.append(f"- `{name}`: `{selector}`")
        lines.append("")
    for index, scenario in enumerate(plan["scenarios"], start=1):
        heading = f"{index}. {scenario['label']}"
        if scenario.get("transport"):
            heading = f"{heading} ({scenario['transport']})"
        lines.extend([
            f"## {heading}",
            "",
            f"- Scenario: `{scenario['scenario']}`",
            f"- Story: `{scenario['primary_story']}`",
            f"- Harness: `{scenario['harness']}`",
            f"- Visual surface: `{scenario['visual_surface']}`",
            f"- Live budget: {scenario.get('live_budget', {}).get('summary', '')}",
            f"- MCP: {', '.join(scenario['required_mcp'])}",
            f"- MCP tools: {', '.join(scenario.get('resolved_mcp_tools', [])) or '(none)'}",
            f"- Evidence dir: `{scenario['evidence_dir']}`",
        ])
        contract = scenario.get("transport_evidence_contract")
        profile = scenario.get("transport_profile", {})
        if profile:
            lines.append(
                f"- Transport profile: `{profile.get('id', '')}` ({profile.get('label', '')}; "
                f"{profile.get('level', '')})"
            )
        if contract:
            lines.append(
                f"- Transport evidence contract: `{scenario['transport']}` via {contract['primary_tool']} "
                f"-> `{contract['evidence_kind']}` ({contract['level']})"
            )
        if profile.get("preflight"):
            lines.append(f"- Preflight: {profile.get('preflight')}")
        lines.extend([
            "",
            scenario["task_prompt"],
            "",
        ])
        capture_routes = scenario.get("capture_routes", [])
        if capture_routes:
            lines.extend(["### Deterministic Capture Routes", ""])
            for route in capture_routes[:8]:
                lines.append(
                    f"- `{route.get('route_id', '')}`: `{route.get('primary_story', '')}` "
                    f"via `{route.get('transport', '')}`/`{route.get('visual_surface', '')}` -> "
                    f"`{route.get('artifact_path_template', '')}`"
                )
            if len(capture_routes) > 8:
                lines.append(f"- +{len(capture_routes) - 8} more route(s)")
            lines.append("")
        utterances = scenario.get("natural_utterances", [])
        if utterances:
            lines.extend(["### Transcript-Derived Utterances", ""])
            for utterance in utterances:
                lines.append(
                    f"- \"{utterance.get('text', '')}\" "
                    f"({utterance.get('source', '')}: {utterance.get('source_ref', '')})"
                )
            lines.append("")
        variants = scenario.get("case_variants", [])
        if variants:
            if format_case_variants(variants) not in scenario.get("task_prompt", ""):
                lines.extend(["### Case Variants", ""])
                for variant in variants:
                    lines.extend([
                        f"- `{variant.get('id', 'case')}`: \"{variant.get('utterance', '')}\"",
                        f"  - Setup: {variant.get('setup', '')}",
                        f"  - Success focus: {variant.get('success_focus', '')}",
                    ])
                lines.append("")
        lines.extend([
            "### Action Sequence",
            "",
        ])
        for action in scenario["action_sequence"]:
            lines.append(f"- {action}")
        lines.extend(["", "### Driver Actions", ""])
        for action in scenario.get("driver_actions", []):
            tools = ", ".join(action.get("tools", [])) or "(none)"
            resolved_tools = ", ".join(action.get("resolved_tools", [])) or "(none)"
            evidence_refs = ", ".join(action.get("evidence", [])) or "(none)"
            lines.extend([
                f"#### {action['id']}",
                "",
                f"- Goal: {action['goal']}",
                f"- Tools: {tools}",
                f"- MCP tools: {resolved_tools}",
                f"- Evidence: {evidence_refs}",
                f"- Record: {action['record']}",
                "",
            ])
            action_utterances = action.get("natural_utterances", [])
            if action_utterances:
                lines.extend(["Transcript-derived utterances:", ""])
                for utterance in action_utterances:
                    lines.append(
                        f"- \"{utterance.get('text', '')}\" "
                        f"({utterance.get('source', '')}: {utterance.get('source_ref', '')})"
                    )
                lines.append("")
        lines.extend(["", "### Persona Prompts", ""])
        for prompt in scenario["persona_prompts"]:
            lines.append(f"- {prompt}")
        lens = scenario.get("persona_lens", {})
        if lens:
            lines.extend(["", "### Persona Lens", ""])
            lines.append(f"- Starting surface: {lens.get('starting_surface', '')}")
            lines.append(f"- First question: {lens.get('first_question', '')}")
            lines.append(f"- Evidence emphasis: {lens.get('evidence_emphasis', '')}")
            lines.append(f"- Escalation trigger: {lens.get('escalation_trigger', '')}")
            lines.append(f"- Finding bias: {lens.get('finding_bias', '')}")
        lines.extend(["", "### Evidence", ""])
        for item in scenario["evidence"]:
            playback = " playback" if item["playback_candidate"] else ""
            path = item["path"] or "<path-or-retained-id>"
            lines.append(f"- `{item['kind']}`{playback}: {path} - {item['capture_hint']}")
        gate = scenario.get("quality_gate", {})
        lines.extend(["", "### Minimum Proof", ""])
        lines.append(f"- Done when: {gate.get('done_when', 'The scenario has captured evidence or an explicit blocker.')}")
        minimum = gate.get("minimum_evidence", [])
        if minimum:
            lines.append(f"- Minimum evidence: {', '.join(f'`{item}`' for item in minimum)}")
        block_if = gate.get("block_if", [])
        if block_if:
            lines.append("- Block if:")
            for condition in block_if:
                lines.append(f"  - {condition}")
        lines.extend(["", "### Attach Commands", ""])
        for command in scenario["attach_commands"]:
            lines.append(f"```sh\n{command}\n```")
        lines.extend(["", "### Finding Command", ""])
        lines.append(f"```sh\n{scenario['record_finding_command']}\n```")
        lines.extend(["", "### Blocker Command", ""])
        lines.append(f"```sh\n{scenario['record_blocker_command']}\n```")
        lines.extend(["", "### Journal Command", ""])
        lines.append(f"```sh\n{scenario['journal_command']}\n```")
        lines.extend(["", "### Success Criteria", ""])
        for criterion in scenario["success_criteria"]:
            lines.append(f"- {criterion}")
        lines.append("")
    lines.extend(["## Final Gates", ""])
    for command in plan["final_gates"]:
        lines.append(f"```sh\n{command}\n```")
    return "\n".join(lines) + "\n"


def render_agent_brief(brief: dict) -> str:
    driver = brief.get("driver", {})
    lines = [
        "# Product journey QA agent brief",
        "",
        f"- Run: `{brief['run_id']}`",
        f"- Driving surface: `{driver.get('id', DEFAULT_DRIVER_ID)}` ({driver.get('label', '')})",
        f"- Project: `{brief['project']['label']}`",
        f"- Persona: `{brief['persona_contract']['label']}`",
        f"- Surface preference: `{brief['persona_contract']['surface_preference']}`",
        f"- Risk focus: {', '.join(brief['persona_contract']['risk_focus'])}",
        f"- Recommended driver: `{brief.get('recommended_agent', '.agents/agents/product-journey-qa-driver.md')}`",
        f"- Driver plan: `{brief.get('driver_plan_markdown', 'driver-plan.md')}`",
        "",
    ]
    lens = brief["persona_contract"].get("lens", {})
    lines.extend(["## Persona Lens", ""])
    if lens:
        lines.extend([
            f"- Starting surface: {lens.get('starting_surface', '')}",
            f"- First question: {lens.get('first_question', '')}",
            f"- Evidence emphasis: {lens.get('evidence_emphasis', '')}",
            f"- Escalation trigger: {lens.get('escalation_trigger', '')}",
            f"- Finding bias: {lens.get('finding_bias', '')}",
        ])
    else:
        lines.append("- (not specified)")
    lines.extend([
        "",
        "## Mission",
        "",
        brief["mission"],
        "",
        "## Operating Rules",
        "",
    ])
    for rule in brief["operating_rules"]:
        lines.append(f"- {rule}")
    lines.extend(["", "## Driver Manifest", ""])
    lines.append(f"- App kind: `{driver.get('app_kind', '')}`")
    lines.append(f"- Manifest: `{driver.get('manifest_path', '')}`")
    lines.append("")
    lines.append("### Capability Tools")
    lines.append("")
    for capability, tools in sorted(driver.get("capabilities", {}).items()):
        lines.append(f"- `{capability}`: {', '.join(f'`{tool}`' for tool in tools)}")
    affordances = driver.get("affordances", {})
    lines.extend(["", "### Affordances", ""])
    if affordances:
        for name, selector in sorted(affordances.items()):
            lines.append(f"- `{name}`: `{selector}`")
    else:
        lines.append("- (none declared)")
    notes = driver.get("notes", [])
    if notes:
        lines.extend(["", "### Driver Notes", ""])
        for note in notes:
            lines.append(f"- {note}")
    lines.extend(["", "## Scenario Order", ""])
    for index, scenario in enumerate(brief["scenario_order"], start=1):
        lines.extend([
            f"### {index}. {scenario['label']}",
            "",
            f"- Scenario: `{scenario['id']}`",
            f"- Story: `{scenario['primary_story']}`",
            f"- MCP tools: {', '.join(scenario['mcp_tools'])}",
            f"- Resolved tools: {', '.join(scenario.get('resolved_mcp_tools', [])) or '(none)'}",
            f"- Evidence: {', '.join(scenario['evidence'])}",
            f"- Live budget: {scenario.get('live_budget', {}).get('summary', '')}",
            f"- Capture routes: {len(scenario.get('capture_routes', []))}",
            "",
            scenario.get("task_prompt", scenario["task"]),
            "",
            "Success criteria:",
        ])
        for criterion in scenario["success_criteria"]:
            lines.append(f"- {criterion}")
        variants = scenario.get("case_variants", [])
        if variants:
            if format_case_variants(variants) not in scenario.get("task_prompt", ""):
                lines.extend(["", "Case variants:"])
                for variant in variants:
                    lines.append(f"- `{variant.get('id', 'case')}`: \"{variant.get('utterance', '')}\"")
                    lines.append(f"  - Setup: {variant.get('setup', '')}")
                    lines.append(f"  - Success focus: {variant.get('success_focus', '')}")
        gate = scenario.get("quality_gate", {})
        if gate:
            lines.extend(["", "Minimum proof:"])
            lines.append(f"- Done when: {gate.get('done_when', 'The scenario has captured evidence or an explicit blocker.')}")
            minimum = gate.get("minimum_evidence", [])
            if minimum:
                lines.append(f"- Minimum evidence: {', '.join(f'`{item}`' for item in minimum)}")
            block_if = gate.get("block_if", [])
            if block_if:
                lines.append("- Block if:")
                for condition in block_if:
                    lines.append(f"  - {condition}")
        lines.append("")
    lines.extend(["## Missing Evidence", ""])
    if brief["missing_evidence"]:
        for item in brief["missing_evidence"]:
            lines.append(f"- `{item['scenario']}` / `{item['kind']}`: {item['hint']}")
    else:
        lines.append("- (none)")
    lines.extend(["", "## Finalize", ""])
    for command in brief["finalize_commands"]:
        lines.append(f"```sh\n{command}\n```")
    return "\n".join(lines) + "\n"


def render_driver_handoff(handoff: dict) -> str:
    lines = [
        "# Product journey driver handoff",
        "",
        f"- Run: `{handoff['run_id']}`",
        f"- Driver agent: `{handoff['driver_agent']}`",
        f"- Run dir: `{handoff['run_dir']}`",
        f"- Project: `{handoff['project']['label']}`",
        f"- Persona: `{handoff['persona']['label']}`",
        f"- Review: `{handoff['status']['review_status']}`",
        f"- Evidence: {handoff['status']['present_evidence_count']} / {handoff['status']['required_evidence_count']}",
        f"- Proof evidence: {handoff['status'].get('proof_evidence_count', 0)} attached; minimum proof {handoff['status'].get('proof_minimum_evidence_count', 0)} / {handoff['status'].get('minimum_evidence_count', 0)}",
        f"- Findings: {handoff['status']['findings_count']}",
        "",
        "## Operator Warning",
        "",
        handoff["operator_warning"],
        "",
        "## Suggested Driver Prompt",
        "",
        handoff["suggested_prompt"],
        "",
        "## Inputs",
        "",
    ]
    for label, path in handoff["inputs"].items():
        lines.append(f"- `{label}`: `{path}`")
    lines.extend(["", "## Dispatch Modes", ""])
    for mode in handoff["dispatch_modes"]:
        lines.append(f"- `{mode['mode']}`: {mode['description']}")
    lines.extend(["", "## Missing Evidence", ""])
    if handoff["missing_evidence"]:
        for item in handoff["missing_evidence"][:25]:
            lines.append(f"- `{item['scenario']}` / `{item['kind']}`: {item['hint']}")
    else:
        lines.append("- (none)")
    lines.extend(["", "## Missing Proof Evidence", ""])
    if handoff.get("missing_proof_evidence"):
        for row in handoff["missing_proof_evidence"][:25]:
            missing = ", ".join(f"`{kind}`" for kind in row.get("missing_proof_evidence", []))
            lines.append(
                f"- `{row['scenario']}`: proof {row.get('proof_minimum_evidence_count', 0)} / "
                f"{row.get('minimum_evidence_count', 0)} (captured {row.get('captured_minimum_evidence_count', 0)}); missing {missing}"
            )
            for slot in row.get("slots", []):
                lines.append(f"  - `{slot.get('kind', '')}`: {slot.get('capture_hint', '')}")
                route = slot.get("capture_route", {}) if isinstance(slot.get("capture_route", {}), dict) else {}
                if route:
                    lines.append(
                        f"    - Route `{route.get('route_id', '')}` opens `{route.get('primary_story', '')}` "
                        f"via `{route.get('visual_surface', '')}` and records `{route.get('artifact_path_template', '')}`"
                    )
                lines.append(f"    ```sh\n    {slot.get('attach_command', '')}\n    ```")
    else:
        lines.append("- (none)")
    lines.extend(["", "## Finalize", ""])
    for command in handoff["finalize_commands"]:
        lines.append(f"```sh\n{command}\n```")
    return "\n".join(lines) + "\n"


def render_execution_plan(plan: dict) -> str:
    lines = [
        "# Product journey execution plan",
        "",
        f"- Run: `{plan['run_id']}`",
        f"- Project: `{plan['project']['label']}`",
        f"- Persona: `{plan['persona']['label']}`",
        f"- Scenarios: {plan['summary']['scenario_count']}",
        f"- Evidence slots: {plan['summary']['evidence_count']}",
    ]
    if plan["summary"].get("transports"):
        lines.append(f"- Transports: {', '.join(plan['summary']['transports'])}")
        lines.append(f"- Legs: {plan['summary'].get('leg_count', len(plan['steps']))}")
    lines.append("")
    for step in plan["steps"]:
        heading = f"{step['order']}. {step['label']}"
        if step.get("transport"):
            heading = f"{heading} ({step['transport']})"
        lines.extend([
            f"## {heading}",
            "",
            f"- Scenario: `{step['scenario']}`",
            f"- Story: `{step['primary_story']}`",
            f"- Stage: `{step['stage']}`",
        ])
        contract = step.get("transport_evidence_contract")
        if contract:
            lines.append(
                f"- Transport evidence contract: `{step['transport']}` via {contract['primary_tool']} "
                f"-> `{contract['evidence_kind']}` ({contract['level']})"
            )
        lines.extend([
            "",
            step["task"],
            "",
            "Driver prompt:",
            "",
            step.get("task_prompt", step["task"]),
            "",
            "### MCP Steps",
            "",
        ])
        for mcp in step["mcp_steps"]:
            lines.append(f"- `{mcp['tool']}`: {mcp['instruction']}")
        lines.extend(["", "### Evidence", ""])
        for evidence in step["evidence"]:
            status = evidence["status"]
            path = evidence["path"] or "<path-or-retained-id>"
            lines.append(f"- `{evidence['kind']}` ({status}): {path} - {evidence['capture_hint']}")
        lines.extend(["", "### Attach Commands", ""])
        for command in step["attach_commands"]:
            lines.append(f"```sh\n{command}\n```")
        lines.extend(["", "### Blocker Command", ""])
        lines.append(f"```sh\n{step['record_blocker_command']}\n```")
        lines.extend(["", "### Success Criteria", ""])
        for criterion in step["success_criteria"]:
            lines.append(f"- {criterion}")
        lines.append("")
    lines.extend(["## Finalize", ""])
    for command in plan["finalize_commands"]:
        lines.append(f"```sh\n{command}\n```")
    return "\n".join(lines) + "\n"


def render_weakness_routes(routes: dict) -> str:
    lines = [
        "# Product Journey PRD/design routes",
        "",
        f"- Run: `{routes.get('run_id', '')}`",
        f"- Target pipeline: `{routes.get('target_pipeline', 'prd-design')}`",
        f"- Target story: `{routes.get('target_story', 'stories/prd')}`",
        f"- Open weaknesses routed: {routes.get('summary', {}).get('routed', 0)}",
        "",
    ]
    items = routes.get("items", [])
    if not items:
        lines.append("No open observed weakness findings need PRD/design routing.")
        return "\n".join(lines) + "\n"
    lines.extend(["## Routes", ""])
    for item in items:
        lines.extend([
            f"### {item.get('finding_id', '')}: {item.get('title', '')}",
            "",
            f"- Scenario: `{item.get('scenario', '')}` ({item.get('scenario_label', '')})",
            f"- Persona: `{item.get('persona', '')}`",
            f"- Severity: `{item.get('severity', '')}`",
            f"- Evidence: `{item.get('evidence_path', '')}`",
            f"- Target: `{item.get('target_story', 'stories/prd')}` / `{item.get('target_pipeline', 'prd-design')}`",
            "",
            item.get("summary", ""),
            "",
            "Suggested PRD idea:",
            "",
            f"> {item.get('suggested_idea', '')}",
            "",
        ])
    return "\n".join(lines) + "\n"


def render_prd_design_intake(intake: dict) -> str:
    lines = [
        "# Product Journey PRD/design intake",
        "",
        f"- Run: `{intake.get('run_id', '')}`",
        f"- Target pipeline: `{intake.get('target_pipeline', 'prd-design')}`",
        f"- Target story: `{intake.get('target_story', 'stories/prd')}`",
        f"- Intake items: {intake.get('summary', {}).get('intake_count', 0)}",
        "",
    ]
    items = intake.get("items", [])
    if not items:
        lines.append("No open observed weakness findings need PRD/design intake.")
        return "\n".join(lines) + "\n"
    lines.extend(["## Intake Items", ""])
    for item in items:
        lens = item.get("persona_lens", {})
        slots = item.get("story_slots", {})
        lines.extend([
            f"### {item.get('intake_id', '')}: {item.get('title', '')}",
            "",
            f"- Finding: `{item.get('finding_id', '')}`",
            f"- Scenario: `{item.get('scenario', '')}` ({item.get('scenario_label', '')})",
            f"- Persona: `{item.get('persona', '')}`",
            f"- Severity: `{item.get('severity', '')}`",
            f"- Evidence: `{item.get('evidence_path', '')}`",
            f"- Story: `{item.get('target_story', 'stories/prd')}` intent `{item.get('story_intent', 'start')}`",
            f"- Upstream paths: `{slots.get('upstream_paths', '')}`",
            "",
            "Persona lens:",
            "",
            f"- Starting surface: {lens.get('starting_surface', '')}",
            f"- First question: {lens.get('first_question', '')}",
            f"- Evidence emphasis: {lens.get('evidence_emphasis', '')}",
            f"- Escalation trigger: {lens.get('escalation_trigger', '')}",
            f"- Finding bias: {lens.get('finding_bias', '')}",
            "",
            "PRD idea:",
            "",
            f"> {slots.get('idea', '')}",
            "",
        ])
    return "\n".join(lines) + "\n"


def render_journey(run_json: dict) -> str:
    lines = [
        "# Product journey dry run",
        "",
        f"- Run: `{run_json['run_id']}`",
        f"- Mode: `{run_json['mode']}`",
        f"- Project: `{run_json['project']['label']}`",
        f"- Persona: `{run_json['persona']['label']}`",
        "",
        "## Stage Plan",
        "",
    ]
    for stage in run_json["stages"]:
        lines.append(f"- `{stage['id']}`: {stage['status']}")
        if stage["scenarios"]:
            lines.append(f"  - scenarios: {', '.join(stage['scenarios'])}")
        for evidence in stage["evidence"]:
            lines.append(f"  - evidence: {evidence}")
    lines.extend([
        "",
        "## Scenarios",
        "",
    ])
    for scenario in run_json["scenarios"]:
        lines.append(f"### {scenario['label']}")
        lines.append("")
        lines.append(f"- Stage: `{scenario['stage']}`")
        lines.append(f"- Story: `{scenario['primary_story']}`")
        lines.append(f"- MCP: {', '.join(scenario['required_mcp'])}")
        lines.append(f"- Evidence: {', '.join(scenario['evidence'])}")
        lines.append("")
        lines.append(scenario["task"])
        lines.append("")
    lines.extend([
        "",
        "## Next Evidence Needed",
        "",
        "- Visual MCP frames or browser screenshots for product discovery and docs/tutorial stages.",
        "- Kitsoki session traces for onboarding, PRD/design, feature implementation, and bugfix paths.",
        "- Oracle result JSON for every attempted project bug.",
        "- Video clips or retained screenshot IDs for Slidey playback scenes.",
    ])
    return "\n".join(lines) + "\n"

#!/usr/bin/env python3
"""Product-journey evaluation runner.

This is the first execution entrypoint for the product-journey harness. It is
intentionally deterministic: checks use existing local metadata and manifest
contracts so the runner itself stays cost-free by default.
"""

import argparse
import json
import os
import re
import shlex
import subprocess
import datetime
import tempfile
import shutil
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Optional


ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from tools.persona_qa.config import load_collection as load_persona_qa_collection  # noqa: E402
from tools.persona_qa.config import load_config as load_persona_qa_config  # noqa: E402
from tools.persona_qa.transports import (  # noqa: E402
    TRANSPORT_EVIDENCE_CONTRACTS,
    TRANSPORT_IDS,
    TRANSPORT_PROFILES,
    compact_transport_profile as persona_compact_transport_profile,
    normalize_transport_filter,
    transport_profile as persona_transport_profile,
)

_HERE = Path(__file__).resolve().parent
if str(_HERE) not in sys.path:
    sys.path.insert(0, str(_HERE))

from common import (
    CATALOG_TIERS,
    DEFAULT_DRIVER_ID,
    DRIVERS_DIR,
    EVIDENCE_FILE_EXTENSIONS,
    EVIDENCE_SOURCES,
    PLAYBACK_EVIDENCE_KINDS,
    PROJECT_ROOT,
    SCENARIO_ALIASES,
    STAGES,
    _TIER_NOTICES,
    compact_transport_profile,
    driver_manifest_path,
    driver_summary,
    evidence_source,
    format_case_variants,
    load_driver_manifest,
    media_kind,
    normalize_capability_tools,
    normalize_evidence_source,
    persona_lens,
    persona_tier,
    read_json,
    run_dir_from_arg,
    select_persona,
    transport_profile,
    write_json,
)

from emit import (
    _driver_plan_entry,
    _driver_plan_evidence_view,
    _execution_plan_step,
    _meta_value,
    attach_evidence_command,
    autonomous_fix_story_command,
    autonomous_watchdog_story_command,
    build_agent_brief,
    build_assignment_scenario_task,
    build_driver_journal,
    build_media_manifest,
    capture_observe_capabilities,
    capture_phase_capabilities,
    capture_route_for_slot,
    capture_routes_for_evidence,
    command_output_text,
    default_scenario_transports,
    driver_action_sequence,
    driver_actions,
    driver_harness,
    driver_visual_surface,
    evidence_artifact_path_template,
    evidence_capture_hint,
    evidence_plan,
    final_story_gate_commands,
    journal_attempt_command,
    leg_evidence_view,
    leg_quality_gate,
    mcp_step,
    note_tier_synthesis,
    parse_preflight_time,
    quota_preflight_check,
    record_blocker_command,
    render_agent_brief,
    render_capture_preflight_markdown,
    render_driver_handoff,
    render_driver_journal,
    render_driver_plan,
    render_execution_plan,
    render_journey,
    render_prd_design_intake,
    render_scenario_outcomes,
    render_weakness_routes,
    resolve_mcp_tools,
    resolve_project,
    resolve_scenario_transports,
    resolved_mcp_tools,
    scenario_live_budget,
    scenario_plan,
    scenario_quality_gate,
    scenario_tier,
    scenario_transport_leg,
    scenario_transport_legs,
    stage_plan,
    target_status,
    transport_for_visual_surface,
)

from marathon import (
    add_validation_issue,
    autonomous_driver_dispatch_markdown_path,
    autonomous_driver_dispatch_path,
    autonomous_fix_report_path,
    autonomous_marathon_control_markdown_path,
    autonomous_marathon_control_path,
    autonomous_marathon_control_summary,
    autonomous_marathon_cycle_seed,
    autonomous_marathon_due_command,
    autonomous_marathon_due_item,
    autonomous_marathon_due_params,
    autonomous_marathon_due_story_intent,
    autonomous_marathon_report_path,
    autonomous_marathon_watchdog_markdown_path,
    autonomous_marathon_watchdog_path,
    build_driver_contract_summary,
    build_next_driver_capture,
    campaign_worker_receipt_markdown_path,
    campaign_worker_receipt_path,
    campaign_worker_summary,
    check_gh_agent_health,
    check_gh_agent_readiness,
    credible_issue_findings,
    demo_evidence_path,
    derive_stats,
    driver_manifest_for_run_json,
    gh_agent_asset_name,
    gh_agent_fix_evidence_links,
    gh_agent_health_url,
    gh_agent_independent_verify_links,
    gh_agent_job_commit_sha,
    gh_agent_job_commit_url,
    gh_agent_job_evidence_links,
    gh_agent_job_independent_verify_links,
    gh_agent_job_integration_branch,
    gh_agent_job_triage_evidence_links,
    gh_agent_missing_fix_evidence,
    gh_agent_missing_independent_verify,
    gh_agent_missing_run_urls,
    gh_agent_missing_triage_evidence,
    gh_agent_ready_url,
    gh_agent_triage_evidence_links,
    github_issue_evidence_assets,
    github_issue_ref,
    independent_verify_gate_from_summary,
    invalid_autonomous_marathon_creation,
    issue_has_fixed_marker,
    issue_is_closed,
    issue_is_open,
    issue_marker_text,
    kitsoki_cli_command,
    latest_driver_heartbeat,
    load_issue_state,
    local_finding_body,
    local_finding_ref,
    marathon_smoke_ledger_path,
    next_driver_blocker_command,
    next_driver_capture_route,
    next_driver_capture_slot,
    normalize_issue_title,
    parse_final_json_object,
    parse_iso_datetime,
    render_autonomous_fix_report,
    render_autonomous_marathon_control,
    render_autonomous_marathon_report,
    render_autonomous_marathon_watchdog,
    render_campaign_worker_receipt,
    run_story_summary,
    scenario_minimum_evidence,
    scenario_playback_kind,
    scenario_qa_workspace_id,
    select_scenarios,
    shell,
    shell_command,
    split_csv,
    strip_scenario_qa_workspace_args,
    summarize_gh_agent_fix_evidence,
    unfiled_credible_findings,
    write_autonomous_fix_report,
    write_autonomous_marathon_report,
)

from matrix import (
    aggregate_driver_journal,
    aggregate_missing_proof_evidence,
    aggregate_persona_outcomes,
    aggregate_quality_gates,
    aggregate_scenario_outcomes,
    render_matrix_deck,
    render_matrix_summary,
    render_rollup_deck,
    render_rollup_summary,
    rollup_handoff_backlog_summary,
)

from review import (
    credible_findings_requiring_github,
    deck_scene_eyebrows,
    filed_issue_evidence_links,
    gh_agent_integration_landing_lines,
    gh_agent_missing_integration_landing,
    issue_closeout_gate,
    load_json_for_validation,
    missing_autonomous_fix_report_tokens,
    open_weakness_findings,
    playback_scene_for_item,
    route_profile_validation_errors,
    summarize_driver_action_contract,
    summarize_run_bundle,
    unattached_driver_evidence_refs,
    validate_final_commands,
    validate_required_keys,
    validate_slidey_deck_shape,
    validation_issue_summary,
)

CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
PERSONAS = ROOT / "tools" / "product-journey" / "personas.json"
SCENARIOS = ROOT / "tools" / "product-journey" / "scenarios.json"
GITHUB_TARGETS = ROOT / "tools" / "product-journey" / "github-targets.json"
SCHEMA = ROOT / "tools" / "product-journey" / "schema.json"
DRIVER_AGENT = ROOT / ".agents" / "agents" / "product-journey-qa-driver.md"
AUTONOMOUS_DRIVER_PROMPT = ROOT / "stories" / "product-journey-qa" / "prompts" / "autonomous_driver.md"
PRODUCT_JOURNEY_SKILL = ROOT / ".agents" / "skills" / "product-journey-qa" / "SKILL.md"
PRODUCT_JOURNEY_README = ROOT / "tools" / "product-journey" / "README.md"
LOG = ROOT / ".context" / "product-journey-runlog.md"
ARTIFACT_ROOT = ROOT / ".artifacts" / "product-journey"
MATRIX_ROOT = ARTIFACT_ROOT / "matrices"
TARGET_PROOF_ROOT = ARTIFACT_ROOT / "target-proofs"
DOGFOOD_ROOT = ARTIFACT_ROOT / "dogfood"
PREFLIGHT_ROOT = ARTIFACT_ROOT / "preflights"
DEFAULT_DECK = ROOT / "docs" / "decks" / "product-journey-eval.slidey.json"
NATIVE_GHAGENT_SMOKE = ROOT / "tools" / "product-journey" / "native_ghagent_test.py"
AUTONOMOUS_FIX_SMOKE = ROOT / "tools" / "product-journey" / "file_findings_test.py"
PERSONA_AUTOFIX_SMOKE = ROOT / "tools" / "product-journey" / "persona_autofix_smoke_test.py"
AUTONOMOUS_MARATHON_SMOKE = ROOT / "tools" / "product-journey" / "autonomous_marathon_smoke_test.py"
PROOF_EVIDENCE_SOURCES = {"retained", "external", "local", "cassette"}
CANONICAL_DRIVER_CAPABILITIES = [
    "visual.open",
    "visual.observe",
    "visual.act",
    "session.open",
    "session.status",
    "session.submit",
    "session.drive",
    "session.inspect",
    "session.trace",
    "render.tui",
]


def truthy(value) -> bool:
    return str(value or "").strip().lower() in {"1", "true", "yes", "y", "on"}


def load_catalog(path: Path):
    return json.loads(path.read_text())


def load_personas(path: Path):
    return load_persona_qa_collection(path, "personas")


def load_scenarios(path: Path):
    return load_persona_qa_collection(path, "scenarios")


def is_active_persona(persona: dict) -> bool:
    if persona.get("status") == "draft":
        return False
    return isinstance(persona.get("risk_focus"), list) and bool(persona.get("risk_focus"))


def active_personas(personas: list[dict]) -> list[dict]:
    return [persona for persona in personas if is_active_persona(persona)]


def is_active_scenario(scenario: dict) -> bool:
    if scenario.get("status") == "draft":
        return False
    return scenario.get("stage") in STAGES


def active_scenarios(scenarios: list[dict]) -> list[dict]:
    return [scenario for scenario in scenarios if is_active_scenario(scenario)]


def select_transports(transport_filter: str) -> list[str]:
    """Validate and normalize a --transport CLI value into an ordered id list.

    Mirrors select_scenarios()'s dup/unknown-id validation shape. An empty
    filter returns [] (today's byte-compatible, transport-unaware behavior);
    "all" (or any request that includes it) expands to every known transport.
    """
    try:
        return normalize_transport_filter(transport_filter)
    except ValueError as exc:
        raise SystemExit(f"--transport contains {exc}") from exc


def load_github_targets(path: Path):
    return json.loads(path.read_text())


def apply_persona_qa_config(config_path: str):
    """Apply an optional productized persona-QA project config.

    The runner remains the mature implementation point, but these globals are
    now data-owned by persona-qa.yaml instead of assumed from the Kitsoki repo
    layout. Paths not relevant to the public kit surface intentionally stay
    rooted at ROOT so existing smoke tests and story contract checks are not
    reinterpreted as external-project requirements.
    """

    global PROJECT_ROOT
    global CATALOG, PERSONAS, SCENARIOS, GITHUB_TARGETS, DRIVERS_DIR
    global DEFAULT_DRIVER_ID, LOG, ARTIFACT_ROOT, MATRIX_ROOT, TARGET_PROOF_ROOT
    global DOGFOOD_ROOT, PREFLIGHT_ROOT, DEFAULT_DECK

    config = load_persona_qa_config(config_path or None, repo_root=ROOT)
    PROJECT_ROOT = config.project_root
    CATALOG = config.path("catalogs", "catalog")
    PERSONAS = config.path("catalogs", "personas")
    SCENARIOS = config.path("catalogs", "scenarios")
    GITHUB_TARGETS = config.path("catalogs", "github_targets")
    DRIVERS_DIR = config.path("drivers", "dir")
    DEFAULT_DRIVER_ID = config.default_driver
    LOG = config.path("artifacts", "run_log")
    ARTIFACT_ROOT = config.path("artifacts", "root")
    MATRIX_ROOT = ARTIFACT_ROOT / "matrices"
    TARGET_PROOF_ROOT = ARTIFACT_ROOT / "target-proofs"
    DOGFOOD_ROOT = ARTIFACT_ROOT / "dogfood"
    PREFLIGHT_ROOT = ARTIFACT_ROOT / "preflights"
    DEFAULT_DECK = config.path("deck", "publish")
    return config


def append_log(message: str):
    LOG.parent.mkdir(parents=True, exist_ok=True)
    now = datetime.datetime.now().isoformat(timespec="seconds")
    entry = f"- [{now}] {message}\n"
    if not LOG.exists():
        LOG.write_text("# Product journey run log\n\n")
    with LOG.open("a", encoding="utf-8") as fp:
        fp.write(entry)


def now_utc() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def slug_timestamp() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def in_managed_dev_workspace(root: Path = ROOT) -> bool:
    return (root / ".kitsoki-dev-workspace.json").is_file() and (
        (root / ".kitsoki-capsule").is_file() or (root / ".kitsoki-clone").is_file()
    )


def scenario_qa_workspace_branch(workspace_id: str) -> str:
    return "agent/" + re.sub(r"[^A-Za-z0-9._/-]+", "-", workspace_id)


def ensure_scenario_qa_workspace(workspace_id: str) -> dict:
    script = ROOT / "scripts" / "dev-workspace.sh"
    if not script.is_file():
        raise SystemExit(f"scenario-qa workspace helper missing: {script}")
    common = ["--repo", str(ROOT), "--json"]
    status = subprocess.run(
        [str(script), "status", workspace_id, *common],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if status.returncode == 0:
        return parse_final_json_object(status.stdout)

    create = subprocess.run(
        [
            str(script),
            "create",
            "--id",
            workspace_id,
            "--branch",
            scenario_qa_workspace_branch(workspace_id),
            "--bootstrap",
            *common,
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if create.returncode != 0:
        detail = (create.stderr or create.stdout or "").strip()
        raise SystemExit(f"scenario-qa workspace create failed: {detail}")
    return parse_final_json_object(create.stdout)


def rerun_scenario_qa_in_workspace(args: argparse.Namespace) -> bool:
    if not getattr(args, "scenario_qa_workspace", False):
        return False
    if os.environ.get("KITSOKI_SCENARIO_QA_WORKSPACE_ACTIVE") == "1" or in_managed_dev_workspace():
        return False

    workspace_id = scenario_qa_workspace_id(getattr(args, "scenario_qa_workspace_id", ""))
    workspace = ensure_scenario_qa_workspace(workspace_id)
    workspace_root = Path(workspace["path"])
    child_argv = strip_scenario_qa_workspace_args(sys.argv[1:])
    cmd = [sys.executable, str(workspace_root / "tools" / "product-journey" / "run.py"), *child_argv]
    env = os.environ.copy()
    env["KITSOKI_SCENARIO_QA_WORKSPACE_ACTIVE"] = "1"
    env["KITSOKI_SCENARIO_QA_WORKSPACE_ID"] = workspace_id
    proc = subprocess.run(
        cmd,
        cwd=workspace_root,
        text=True,
        capture_output=True,
        env=env,
        check=False,
    )
    if proc.returncode != 0:
        if proc.stdout:
            print(proc.stdout, end="")
        if proc.stderr:
            print(proc.stderr, end="", file=sys.stderr)
        raise SystemExit(proc.returncode)

    if args.json_output:
        try:
            payload = parse_final_json_object(proc.stdout)
        except json.JSONDecodeError:
            if proc.stdout:
                print(proc.stdout, end="")
            raise
        payload["scenario_qa_workspace"] = {
            "id": workspace_id,
            "root": str(workspace_root),
            "branch": workspace.get("branch", ""),
            "reused": bool(workspace.get("reused", False)),
        }
        print(json.dumps(payload, sort_keys=True))
    else:
        if proc.stdout:
            print(proc.stdout, end="")
        print(f"Scenario QA workspace: {workspace_root}")
    if proc.stderr:
        print(proc.stderr, end="", file=sys.stderr)
    return True


def native_ghagent_smoke() -> dict:
    result = shell([sys.executable, str(NATIVE_GHAGENT_SMOKE)], ROOT)
    output = (result.stdout + result.stderr).strip()
    if result.returncode != 0:
        return {
            "status": "failed",
            "summary": "native gh-agent queue/drain smoke failed",
            "exit_code": result.returncode,
            "output": output,
        }
    return {
        "status": "passed",
        "summary": "native gh-agent queue/drain smoke passed",
        "exit_code": result.returncode,
        "output": output,
    }


def autonomous_fix_smoke() -> dict:
    result = shell([sys.executable, str(AUTONOMOUS_FIX_SMOKE)], ROOT)
    output = (result.stdout + result.stderr).strip()
    if result.returncode != 0:
        return {
            "status": "failed",
            "summary": "autonomous issue-to-fix smoke failed",
            "exit_code": result.returncode,
            "output": output,
        }
    return {
        "status": "passed",
        "summary": "autonomous issue-to-fix smoke passed",
        "exit_code": result.returncode,
        "output": output,
    }


def persona_autofix_smoke() -> dict:
    result = shell([sys.executable, str(PERSONA_AUTOFIX_SMOKE)], ROOT)
    output = (result.stdout + result.stderr).strip()
    if result.returncode != 0:
        return {
            "status": "failed",
            "summary": "persona replay autonomous issue-to-fix smoke failed",
            "exit_code": result.returncode,
            "output": output,
        }
    return {
        "status": "passed",
        "summary": "persona replay autonomous issue-to-fix smoke passed",
        "exit_code": result.returncode,
        "output": output,
    }


def autonomous_marathon_smoke(repeats: int = 1) -> dict:
    repeats = max(1, int(repeats or 1))
    report_dir = ARTIFACT_ROOT / "marathon-smokes" / slug_timestamp()
    result = shell([
        sys.executable,
        str(AUTONOMOUS_MARATHON_SMOKE),
        "--report-dir",
        str(report_dir),
        "--repeats",
        str(repeats),
    ], ROOT)
    output = (result.stdout + result.stderr).strip()
    summary = {}
    for line in output.splitlines():
        if line.startswith("SUMMARY_JSON: "):
            try:
                summary = json.loads(line.removeprefix("SUMMARY_JSON: "))
            except json.JSONDecodeError:
                summary = {}
    if result.returncode != 0:
        return {
            "status": "failed",
            "summary": "core use-case autonomous product-QA marathon persona sweep failed",
            "exit_code": result.returncode,
            "output": output,
            "cycle_count": repeats,
            "report_path": str(report_dir / "autonomous-marathon-smoke.json"),
            "report_markdown_path": str(report_dir / "autonomous-marathon-smoke.md"),
            **summary,
        }
    return {
        "status": "passed",
        "summary": "core use-case autonomous product-QA marathon persona sweep passed",
        "exit_code": result.returncode,
        "output": output,
        "cycle_count": repeats,
        "report_path": str(report_dir / "autonomous-marathon-smoke.json"),
        "report_markdown_path": str(report_dir / "autonomous-marathon-smoke.md"),
        **summary,
    }


def validate_marathon_smoke_ledger(ledger_arg: str, min_cycles: int = 1) -> dict:
    min_cycles = max(1, int(min_cycles or 1))
    if not str(ledger_arg).strip():
        issues: list[dict] = []
        add_validation_issue(issues, "error", "ledger-path", "Autonomous marathon smoke ledger path was not provided")
        return {
            "status": "invalid",
            "ledger_path": "",
            "ledger_markdown_path": "",
            "min_cycle_count": min_cycles,
            "errors": 1,
            "warnings": 0,
            "issues": issues,
            "summary": "ledger path missing",
        }
    ledger_path = marathon_smoke_ledger_path(ledger_arg)
    issues = []
    if not ledger_path.exists():
        add_validation_issue(issues, "error", "ledger-exists", "Autonomous marathon smoke ledger JSON does not exist", str(ledger_path))
        return {
            "status": "invalid",
            "ledger_path": str(ledger_path),
            "ledger_markdown_path": "",
            "min_cycle_count": min_cycles,
            "errors": 1,
            "warnings": 0,
            "issues": issues,
            "summary": "ledger missing",
        }
    try:
        ledger = read_json(ledger_path)
    except Exception as exc:
        add_validation_issue(issues, "error", "ledger-json", "Autonomous marathon smoke ledger JSON could not be parsed", str(exc))
        return {
            "status": "invalid",
            "ledger_path": str(ledger_path),
            "ledger_markdown_path": "",
            "min_cycle_count": min_cycles,
            "errors": 1,
            "warnings": 0,
            "issues": issues,
            "summary": "ledger JSON invalid",
        }

    markdown_path = marathon_smoke_ledger_path(str(ledger.get("report_markdown_path") or ledger_path.with_suffix(".md")))
    if not markdown_path.exists():
        add_validation_issue(issues, "error", "ledger-markdown", "Autonomous marathon smoke Markdown ledger does not exist", str(markdown_path))

    runs = [item for item in ledger.get("runs", []) if isinstance(item, dict)]
    cycle_count = int(ledger.get("cycle_count", 1) or 1)
    persona_count = int(ledger.get("persona_count", 0) or 0)
    scenario_count = int(ledger.get("scenario_count", 0) or 0)
    run_count = int(ledger.get("run_count", 0) or 0)
    expected_issue_count = int(ledger.get("expected_issue_count", 0) or 0)
    filed_issue_count = int(ledger.get("filed_issue_count", 0) or 0)
    done_count = int(ledger.get("gh_agent_done_count", 0) or 0)
    landing_count = int(ledger.get("gh_agent_integration_landing_count", 0) or 0)
    flawless_count = int(ledger.get("flawless_run_count", 0) or 0)
    expected_scenarios = ["project-onboarding", "prd-design", "bugfix"]

    if ledger.get("status") != "passed":
        add_validation_issue(issues, "error", "ledger-status", "Autonomous marathon smoke ledger did not pass", str(ledger.get("status", "")))
    if ledger.get("project") != "gears-rust":
        add_validation_issue(issues, "error", "ledger-project", "Autonomous marathon smoke ledger is not for gears-rust", str(ledger.get("project", "")))
    if ledger.get("scenario_ids") != expected_scenarios:
        add_validation_issue(issues, "error", "ledger-scenarios", "Autonomous marathon smoke ledger does not cover the core gears-rust scenarios", ",".join(str(item) for item in ledger.get("scenario_ids", [])))
    if cycle_count < 1:
        add_validation_issue(issues, "error", "ledger-cycle-count", "Autonomous marathon smoke ledger cycle count must be at least one", str(cycle_count))
    if cycle_count < min_cycles:
        add_validation_issue(issues, "error", "ledger-min-cycles", "Autonomous marathon smoke ledger does not meet the requested minimum cycle count", f"cycles={cycle_count}, min={min_cycles}")
    expected_run_count = cycle_count * persona_count
    if persona_count < 5 or run_count != expected_run_count or len(runs) != run_count:
        add_validation_issue(issues, "error", "ledger-personas", "Autonomous marathon smoke ledger does not contain one run for each active persona", f"personas={persona_count}, runs={run_count}, entries={len(runs)}")
    if scenario_count != len(expected_scenarios):
        add_validation_issue(issues, "error", "ledger-scenario-count", "Autonomous marathon smoke ledger scenario count is not the core-use-case count", str(scenario_count))
    if expected_issue_count != cycle_count * persona_count * scenario_count:
        add_validation_issue(issues, "error", "ledger-expected-issues", "Autonomous marathon smoke ledger expected issue count does not match cycles x personas x scenarios", str(expected_issue_count))
    if filed_issue_count != expected_issue_count or done_count != expected_issue_count or landing_count != expected_issue_count:
        add_validation_issue(issues, "error", "ledger-fix-counts", "Autonomous marathon smoke ledger did not file, fix, and land every expected issue", f"expected={expected_issue_count}, filed={filed_issue_count}, fixed={done_count}, landed={landing_count}")
    if flawless_count != run_count or ledger.get("success_rate") != f"{run_count}/{run_count}":
        add_validation_issue(issues, "error", "ledger-flawless", "Autonomous marathon smoke ledger does not show every persona run as flawless", f"flawless={flawless_count}, runs={run_count}, success_rate={ledger.get('success_rate', '')}")

    cycles_seen: dict[int, set[str]] = {}
    for item in runs:
        persona = str(item.get("persona") or "(unknown)")
        cycle = int(item.get("cycle", 1) or 1)
        cycles_seen.setdefault(cycle, set()).add(persona)
        run_dir_value = str(item.get("run_dir") or "")
        run_dir = marathon_smoke_ledger_path(run_dir_value) if run_dir_value else Path("")
        if item.get("status") != "passed":
            add_validation_issue(issues, "error", "run-status", "Persona run did not pass", persona)
        if item.get("scenario_ids") != expected_scenarios:
            add_validation_issue(issues, "error", "run-scenarios", "Persona run does not cover core gears-rust scenarios", persona)
        if int(item.get("filed_issue_count", 0) or 0) != scenario_count or int(item.get("gh_agent_done_count", 0) or 0) != scenario_count or int(item.get("gh_agent_integration_landing_count", 0) or 0) != scenario_count:
            add_validation_issue(issues, "error", "run-counts", "Persona run did not file, fix, and land one issue per scenario", persona)
        if item.get("autonomous_gate_summary") != "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass":
            add_validation_issue(issues, "error", "run-gates", "Persona run did not pass every autonomous gate", f"{persona}: {item.get('autonomous_gate_summary', '')}")
        if not run_dir_value or not run_dir.exists():
            add_validation_issue(issues, "error", "run-dir", "Persona run directory is missing from retained ledger", f"{persona}: {run_dir_value}")
            continue
        for field, check_id in (("autonomous_fix_report_path", "run-report"), ("deck_path", "run-deck")):
            artifact = str(item.get(field) or "")
            artifact_path = marathon_smoke_ledger_path(artifact) if artifact else Path("")
            if not artifact or not artifact_path.exists():
                add_validation_issue(issues, "error", check_id, f"Persona run retained artifact is missing: {field}", f"{persona}: {artifact}")
        validation = validate_run_bundle(run_dir)
        if validation.get("status") != "valid":
            add_validation_issue(issues, "error", "run-validation", "Persona run bundle no longer validates", f"{persona}: {validation.get('validation_issue_summary', '')}")
        review = review_run_bundle(run_dir, None)
        if review.get("review_status") != "ready" or int(review.get("failed", 0) or 0) != 0:
            add_validation_issue(issues, "error", "run-review", "Persona run bundle review is not ready", f"{persona}: {review.get('summary', '')}")

    expected_personas = {str(item) for item in ledger.get("persona_ids", []) if str(item)}
    if expected_personas:
        for cycle in range(1, cycle_count + 1):
            seen = cycles_seen.get(cycle, set())
            if seen != expected_personas:
                add_validation_issue(issues, "error", "ledger-cycle-coverage", "Autonomous marathon smoke ledger does not contain every persona in every cycle", f"cycle={cycle}, missing={','.join(sorted(expected_personas - seen))}")

    errors = sum(1 for issue in issues if issue.get("severity") == "error")
    warnings = sum(1 for issue in issues if issue.get("severity") == "warning")
    status = "valid" if errors == 0 else "invalid"
    return {
        "status": status,
        "ledger_path": str(ledger_path),
        "ledger_markdown_path": str(markdown_path),
        "project": ledger.get("project", ""),
        "cycle_count": cycle_count,
        "min_cycle_count": min_cycles,
        "persona_count": persona_count,
        "scenario_count": scenario_count,
        "run_count": run_count,
        "expected_issue_count": expected_issue_count,
        "filed_issue_count": filed_issue_count,
        "gh_agent_done_count": done_count,
        "gh_agent_integration_landing_count": landing_count,
        "flawless_run_count": flawless_count,
        "success_rate": ledger.get("success_rate", ""),
        "errors": errors,
        "warnings": warnings,
        "issues": issues,
        "summary": f"{status}: {flawless_count}/{run_count} flawless, filed={filed_issue_count}, fixed={done_count}, landed={landing_count}",
    }


def clone_local_repo(src: str, prefix: str) -> Path:
    clone_root = Path(tempfile.mkdtemp(prefix=prefix))
    clone = clone_root / Path(src).name
    result = shell([
        "git",
        "clone",
        "--no-local",
        "--no-checkout",
        src,
        str(clone),
    ], ROOT)
    if result.returncode != 0:
        raise RuntimeError(result.stdout + result.stderr)
    return clone


def verify_external_project(project: dict, repo_path: str) -> dict:
    bench = ROOT / "tools" / "bugfix-bakeoff" / "external" / "bench.py"
    try:
        clone = clone_local_repo(repo_path, f"{project['id']}-verify-")
    except RuntimeError as exc:
        return {
            "status": "error",
            "notes": f"{project['id']}: temp clone failed",
            "output": str(exc),
            "meta": _meta_value(project),
        }

    try:
        result = shell(
            ["python3", str(bench), "verify", "--project", project["id"], "--repo-dir", str(clone)],
            ROOT,
        )
        if result.returncode != 0:
            return {
                "status": "error",
                "notes": f"{project['id']}: benchmark verify failed",
                "output": result.stdout + result.stderr,
                "meta": _meta_value(project),
            }
        return {
            "status": "validated",
            "notes": f"{project['id']}: deterministic fixture verification passed from a no-local temp clone",
            "output": result.stdout + result.stderr,
            "meta": _meta_value(project),
        }
    finally:
        shutil.rmtree(clone.parent, ignore_errors=True)


def github_issue_search_query(target: dict) -> str:
    parsed = urllib.parse.urlparse(target.get("bug_query", ""))
    params = urllib.parse.parse_qs(parsed.query)
    query = params.get("q", [""])[0].strip()
    if "repo:" not in query:
        label = target.get("label", "")
        if label:
            query = f"repo:{label} {query}".strip()
    return query


def github_repo_slug(target: dict) -> str:
    label = target.get("label", "")
    if label.count("/") == 1:
        return label
    parsed = urllib.parse.urlparse(target.get("repo", ""))
    path = parsed.path.strip("/")
    if path.endswith(".git"):
        path = path[:-4]
    parts = path.split("/")
    if len(parts) >= 2:
        return "/".join(parts[:2])
    return ""


def github_request_json(url: str) -> tuple[Optional[dict], str]:
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "kitsoki-product-journey-target-proof",
        },
    )
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8")), ""
    except urllib.error.HTTPError as exc:
        return None, f"GitHub HTTP {exc.code}: {exc.reason}"
    except (urllib.error.URLError, TimeoutError) as exc:
        return None, str(exc)


def fetch_github_target_proof(target: dict, selection_contract: dict) -> dict:
    query = github_issue_search_query(target)
    floor = int(target.get("open_bug_floor", selection_contract.get("open_bug_floor", 0)))
    stargazer_floor = int(target.get("stargazer_floor", selection_contract.get("stargazer_floor", 0)))
    repo_slug = github_repo_slug(target)
    if not query:
        return {
            "target": target["id"],
            "label": target["label"],
            "status": "error",
            "error": "missing bug_query search string",
        }
    issue_url = "https://api.github.com/search/issues?" + urllib.parse.urlencode({
        "q": query,
        "per_page": "1",
    })
    issue_payload, issue_error = github_request_json(issue_url)
    if issue_error:
        return {
            "target": target["id"],
            "label": target["label"],
            "status": "error",
            "query": query,
            "api_url": issue_url,
            "error": issue_error,
        }
    if not repo_slug:
        return {
            "target": target["id"],
            "label": target["label"],
            "status": "error",
            "query": query,
            "api_url": issue_url,
            "error": "missing GitHub owner/repo slug",
        }
    repo_url = f"https://api.github.com/repos/{repo_slug}"
    repo_payload, repo_error = github_request_json(repo_url)
    if repo_error:
        return {
            "target": target["id"],
            "label": target["label"],
            "status": "error",
            "query": query,
            "api_url": issue_url,
            "repo_api_url": repo_url,
            "error": repo_error,
        }
    count = int(issue_payload.get("total_count", 0))
    stargazers = int(repo_payload.get("stargazers_count", 0))
    forks = int(repo_payload.get("forks_count", 0))
    watchers = int(repo_payload.get("subscribers_count", repo_payload.get("watchers_count", 0)))
    license_info = repo_payload.get("license") or {}
    reported_license = license_info.get("spdx_id") or license_info.get("key") or ""
    expected_license = target.get("license_spdx", "")
    effective_license = reported_license
    license_source = "github"
    if reported_license in {"", "NOASSERTION"} and expected_license:
        effective_license = expected_license
        license_source = "catalog"
    bug_floor_ok = count >= floor
    popularity_ok = stargazers >= stargazer_floor
    license_ok = bool(effective_license and effective_license != "NOASSERTION")
    return {
        "target": target["id"],
        "label": target["label"],
        "status": "pass" if bug_floor_ok and popularity_ok and license_ok else "fail",
        "query": query,
        "api_url": issue_url,
        "repo_api_url": repo_url,
        "bug_query": target.get("bug_query", ""),
        "open_bug_count": count,
        "open_bug_floor": floor,
        "stargazers_count": stargazers,
        "stargazer_floor": stargazer_floor,
        "forks_count": forks,
        "watchers_count": watchers,
        "license": effective_license,
        "reported_license": reported_license,
        "expected_license": expected_license,
        "license_source": license_source,
        "license_ok": license_ok,
        "popularity_ok": popularity_ok,
        "bug_floor_ok": bug_floor_ok,
        "checked_at": now_utc(),
    }


def refresh_github_target_proofs(github_targets: dict, seed: str) -> dict:
    proof_id = f"{slug_timestamp()}-github-target-proof-{seed}"
    proof_dir = TARGET_PROOF_ROOT / proof_id
    proof_dir.mkdir(parents=True, exist_ok=False)
    checks = [fetch_github_target_proof(target, github_targets["selection_contract"]) for target in github_targets["targets"]]
    passed = sum(1 for check in checks if check.get("status") == "pass")
    failed = sum(1 for check in checks if check.get("status") == "fail")
    errors = sum(1 for check in checks if check.get("status") == "error")
    proof = {
        "proof_id": proof_id,
        "created_at": now_utc(),
        "selection_contract": github_targets["selection_contract"],
        "summary": {
            "targets": len(checks),
            "passed": passed,
            "failed": failed,
            "errors": errors,
            "open_bug_floor": github_targets["selection_contract"].get("open_bug_floor", 100),
            "stargazer_floor": github_targets["selection_contract"].get("stargazer_floor", 0),
            "license": github_targets["selection_contract"].get("license", "open-source"),
        },
        "checks": checks,
        "artifacts": {
            "proof": "target-proof.json",
            "markdown": "target-proof.md",
        },
    }
    write_json(proof_dir / "target-proof.json", proof)
    (proof_dir / "target-proof.md").write_text(render_target_proof(proof), encoding="utf-8")
    return {
        "status": "target_proof_created",
        "proof_id": proof_id,
        "proof_dir": str(proof_dir),
        "proof_path": str(proof_dir / "target-proof.json"),
        "markdown_path": str(proof_dir / "target-proof.md"),
        "passed": passed,
        "failed": failed,
        "errors": errors,
        "target_count": len(checks),
        "open_bug_floor": proof["summary"]["open_bug_floor"],
        "stargazer_floor": proof["summary"]["stargazer_floor"],
    }


def render_target_proof(proof: dict) -> str:
    lines = [
        "# Product journey GitHub target proof",
        "",
        f"- Proof: `{proof['proof_id']}`",
        f"- Created: {proof['created_at']}",
        f"- Open bug floor: {proof['summary']['open_bug_floor']}",
        f"- Stargazer floor: {proof['summary'].get('stargazer_floor', 'unknown')}",
        f"- Passed: {proof['summary']['passed']} / {proof['summary']['targets']}",
        f"- Failed: {proof['summary']['failed']}",
        f"- Errors: {proof['summary']['errors']}",
        "",
        "## Targets",
        "",
    ]
    for check in proof["checks"]:
        lines.extend([
            f"### {check['label']}",
            "",
            f"- Status: {check['status']}",
            f"- Open bugs: {check.get('open_bug_count', 'unknown')} / floor {check.get('open_bug_floor', 'unknown')}",
            f"- Stars: {check.get('stargazers_count', 'unknown')} / floor {check.get('stargazer_floor', 'unknown')}",
            f"- Forks: {check.get('forks_count', 'unknown')}",
            f"- Watchers: {check.get('watchers_count', 'unknown')}",
            f"- License: {check.get('license', '')}",
            f"- License source: {check.get('license_source', '')}",
            f"- License OK: {check.get('license_ok', '')}",
            f"- Query: `{check.get('query', '')}`",
            f"- Checked: {check.get('checked_at', '')}",
            f"- Error: {check.get('error', '')}",
            "",
        ])
    return "\n".join(lines) + "\n"


def load_target_proof(path: str) -> dict:
    if not path:
        return {}
    proof_path = Path(path)
    if not proof_path.is_absolute():
        proof_path = PROJECT_ROOT / proof_path
    if proof_path.is_dir():
        proof_path = proof_path / "target-proof.json"
    return read_json(proof_path)


def merge_target_proofs(github_targets: dict, target_proof: dict) -> dict:
    if not target_proof:
        return github_targets
    proof_by_target = {
        check.get("target"): check
        for check in target_proof.get("checks", [])
    }
    merged = dict(github_targets)
    merged["targets"] = []
    for target in github_targets["targets"]:
        copied = dict(target)
        check = proof_by_target.get(target["id"])
        if check:
            copied["selection_proof"] = {
                "status": check.get("status", "error"),
                "open_bug_count": check.get("open_bug_count"),
                "open_bug_floor": check.get("open_bug_floor", target.get("open_bug_floor")),
                "stargazers_count": check.get("stargazers_count"),
                "stargazer_floor": check.get("stargazer_floor", target.get("stargazer_floor")),
                "forks_count": check.get("forks_count"),
                "watchers_count": check.get("watchers_count"),
                "license": check.get("license", ""),
                "reported_license": check.get("reported_license", ""),
                "expected_license": check.get("expected_license", ""),
                "license_source": check.get("license_source", ""),
                "license_ok": check.get("license_ok"),
                "popularity_ok": check.get("popularity_ok"),
                "bug_floor_ok": check.get("bug_floor_ok"),
                "checked_at": check.get("checked_at", target_proof.get("created_at", "")),
                "query": check.get("query", ""),
                "source": target_proof.get("proof_id", ""),
                "error": check.get("error", ""),
            }
        merged["targets"].append(copied)
    merged["target_proof"] = {
        "proof_id": target_proof.get("proof_id", ""),
        "created_at": target_proof.get("created_at", ""),
        "summary": target_proof.get("summary", {}),
    }
    return merged


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def build_run_bundle(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    project_id: str,
    persona_id: str,
    seed: str,
    mode: str,
    publish_deck: Optional[Path],
    live_budget_minutes: int = 20,
    transports: Optional[list[str]] = None,
    driver_manifest: Optional[dict] = None,
) -> tuple[Path, dict]:
    if live_budget_minutes < 0:
        raise SystemExit("--live-budget-minutes must be >= 0")
    _TIER_NOTICES.clear()
    target = resolve_project(catalog, github_targets, project_id)
    persona = select_persona(personas, persona_id, f"{project_id}:{seed}")
    driver_manifest = driver_manifest or load_driver_manifest()
    created_at = now_utc()
    run_id = f"{slug_timestamp()}-{project_id}-{persona['id']}-{seed}"
    run_dir = ARTIFACT_ROOT / run_id
    run_dir.mkdir(parents=True, exist_ok=False)

    stages = stage_plan(target, scenarios)
    scenario_items = scenario_plan(scenarios)
    scenario_task_by_id = {
        task["scenario"]: task
        for task in (
            build_assignment_scenario_task(target, persona, scenario)
            for scenario in scenarios
        )
    }
    for scenario in scenario_items:
        task = scenario_task_by_id.get(scenario["id"], {})
        scenario["task_prompt"] = task.get("task_prompt", scenario["task"])
        scenario["evidence_dir"] = task.get("evidence_dir", f"evidence/{target['id']}--{persona['id']}/{scenario['id']}")
        if task.get("bug_query"):
            scenario["bug_query"] = task["bug_query"]
    run_json = {
        "run_id": run_id,
        "created_at": created_at,
        "mode": mode,
        "seed": seed,
        "live_budget_minutes": live_budget_minutes,
        "project": _meta_value(target),
        "persona": persona,
        "driver": driver_summary(driver_manifest),
        "stages": stages,
        "scenarios": scenario_items,
        "artifacts": {
            "run": "run.json",
            "journey": "journey.md",
            "metrics": "metrics.json",
            "bugs": "bugs.json",
            "findings": "findings.json",
            "scenario_outcomes": "scenario-outcomes.json",
            "scenario_outcomes_markdown": "scenario-outcomes.md",
            "evidence": "evidence.json",
            "media_manifest": "media-manifest.json",
            "scenarios": "scenarios.json",
            "execution_plan": "execution-plan.json",
            "execution_plan_markdown": "execution-plan.md",
            "driver_plan": "driver-plan.json",
            "driver_plan_markdown": "driver-plan.md",
            "driver_journal": "driver-journal.json",
            "driver_journal_markdown": "driver-journal.md",
            "agent_brief": "agent-brief.json",
            "agent_brief_markdown": "agent-brief.md",
            "driver_handoff": "driver-handoff.json",
            "driver_handoff_markdown": "driver-handoff.md",
            "weakness_routes": "weakness-routes.json",
            "weakness_routes_markdown": "weakness-routes.md",
            "prd_design_intake": "prd-design-intake.json",
            "prd_design_intake_markdown": "prd-design-intake.md",
            "review": "review.json",
            "deck": "deck.slidey.json",
        },
        "notes": [
            "This dry run is deterministic and does not call a live LLM.",
            "Visual MCP, Kitsoki session driving, and video evidence are represented as planned stages until a live or cassette run supplies artifacts.",
        ],
    }
    if target.get("source") == "github-targets":
        run_json["notes"].append(
            "This project came from the GitHub matrix; refresh open bug counts before a live scored sweep."
        )
    if transports:
        run_json["transports"] = list(transports)
    evidence = evidence_plan(run_json)
    media_manifest = build_media_manifest(run_json, evidence)
    execution_plan = build_execution_plan(run_json, evidence, transports, driver_manifest)
    driver_plan = build_driver_plan(run_json, evidence, execution_plan, transports, driver_manifest)
    agent_brief = build_agent_brief(run_json, evidence, execution_plan, driver_manifest)
    findings = {"run_id": run_id, "items": [], "summary": {"strength": 0, "weakness": 0, "issue": 0, "fix": 0}}
    weakness_routes = build_weakness_routes(run_json, findings)
    prd_design_intake = build_prd_design_intake(run_json, weakness_routes)
    driver_journal = build_driver_journal(run_id, [])
    scenario_outcomes = build_scenario_outcomes(run_json, evidence, findings)
    present_evidence = [
        item for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"}
    ]
    demo_evidence = [
        item for item in present_evidence
        if (item.get("source") or evidence_source(item.get("path", ""), item.get("notes", ""))) == "demo"
    ]
    proof_evidence = [item for item in present_evidence if is_proof_evidence(item)]
    metrics = {
        "run_id": run_id,
        "stage_count": len(stages),
        "scenario_count": len(scenario_items),
        "validated_stage_count": sum(1 for stage in stages if stage["status"] in {"validated", "cached_validated"}),
        "captured_stage_count": sum(1 for stage in stages if stage["status"] == "captured"),
        "planned_stage_count": sum(1 for stage in stages if stage["status"] == "planned"),
        "required_evidence_count": evidence["summary"]["required"],
        "present_evidence_count": evidence["summary"]["present"],
        "missing_evidence_count": evidence["summary"]["missing"],
        "demo_evidence_count": len(demo_evidence),
        "proof_evidence_count": len(proof_evidence),
        "product_bugs_found": 0,
        "findings_count": 0,
        "strength_count": 0,
        "weakness_count": 0,
        "fix_count": 0,
        "blocked_count": 0,
        "review_status": "not_reviewed",
        "review_passed_checks": 0,
        "review_total_checks": 0,
        "oracle_results": [],
        "checkpoint_ratings": [],
    }
    bugs = {"run_id": run_id, "items": []}
    review = {
        "run_id": run_id,
        "status": "not_reviewed",
        "summary": "Run has not been reviewed for readiness yet.",
        "checks": [],
    }
    driver_handoff = build_driver_handoff(run_json, metrics, evidence, review)
    journey = render_journey(run_json)
    deck = render_deck(run_json, metrics, evidence=evidence, findings=findings, execution_plan=execution_plan, media_manifest=media_manifest, scenario_outcomes=scenario_outcomes, driver_plan=driver_plan)

    run_json["tier_notices"] = list(_TIER_NOTICES)

    write_json(run_dir / "run.json", run_json)
    (run_dir / "journey.md").write_text(journey, encoding="utf-8")
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "bugs.json", bugs)
    write_json(run_dir / "findings.json", findings)
    write_json(run_dir / "weakness-routes.json", weakness_routes)
    (run_dir / "weakness-routes.md").write_text(render_weakness_routes(weakness_routes), encoding="utf-8")
    write_json(run_dir / "prd-design-intake.json", prd_design_intake)
    (run_dir / "prd-design-intake.md").write_text(render_prd_design_intake(prd_design_intake), encoding="utf-8")
    write_json(run_dir / "scenario-outcomes.json", scenario_outcomes)
    (run_dir / "scenario-outcomes.md").write_text(render_scenario_outcomes(scenario_outcomes), encoding="utf-8")
    write_json(run_dir / "evidence.json", evidence)
    write_json(run_dir / "media-manifest.json", media_manifest)
    write_json(run_dir / "scenarios.json", {"run_id": run_id, "items": scenario_items})
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    write_json(run_dir / "driver-plan.json", driver_plan)
    (run_dir / "driver-plan.md").write_text(render_driver_plan(driver_plan), encoding="utf-8")
    write_json(run_dir / "driver-journal.json", driver_journal)
    (run_dir / "driver-journal.md").write_text(render_driver_journal(driver_journal), encoding="utf-8")
    write_json(run_dir / "agent-brief.json", agent_brief)
    (run_dir / "agent-brief.md").write_text(render_agent_brief(agent_brief), encoding="utf-8")
    write_json(run_dir / "driver-handoff.json", driver_handoff)
    (run_dir / "driver-handoff.md").write_text(render_driver_handoff(driver_handoff), encoding="utf-8")
    write_json(run_dir / "review.json", review)
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)
    return run_dir, run_json


def run_preflight_command(check_id: str, command: str, timeout: int, env: dict[str, str]) -> tuple[bool, str, str, str]:
    if not command:
        return True, "skipped", "", ""
    cmd = shlex.split(command)
    try:
        proc = subprocess.run(
            cmd,
            cwd=ROOT,
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        summary = f"exit_code={proc.returncode}; command={shlex.join(cmd)}"
        return proc.returncode == 0, summary, proc.stdout or "", proc.stderr or ""
    except subprocess.TimeoutExpired as exc:
        stdout = command_output_text(exc.stdout)
        stderr = command_output_text(exc.stderr)
        return False, f"{check_id} timed out after {timeout}s; command={shlex.join(cmd)}", stdout, stderr


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def capture_preflight(
    seed: str,
    command: str = "",
    timeout: int = 90,
    studio_command: str = "",
    quota_state: str = "",
) -> dict:
    preflight_id = f"{slug_timestamp()}-capture-{seed}"
    preflight_dir = PREFLIGHT_ROOT / preflight_id
    preflight_dir.mkdir(parents=True, exist_ok=False)
    output = preflight_dir / "webshot-smoke.png"
    helper = ROOT / "tools" / "runstatus" / "web-shot.ts"
    flow = ROOT / "testdata" / "apps" / "choice_smoke" / "flows" / "intro_begin.yaml"
    story = ROOT / "testdata" / "apps" / "choice_smoke"
    checks = []

    def add_check(check_id: str, passed: bool, summary: str) -> None:
        checks.append({
            "id": check_id,
            "status": "passed" if passed else "failed",
            "summary": summary,
        })

    add_check("webshot-helper", helper.exists(), str(helper))
    add_check("webshot-flow", flow.exists(), str(flow))

    if command:
        cmd = shlex.split(command)
    else:
        cmd = [
            "go",
            "run",
            "./cmd/kitsoki",
            "web-shot",
            str(story.relative_to(ROOT)),
            "--flow",
            str(flow.relative_to(ROOT)),
            "--state",
            "single_basic",
            "--viewport",
            "800x600",
            "--kitsoki-repo",
            str(ROOT),
            "-o",
            str(output),
        ]

    env = os.environ.copy()
    env.setdefault("GOCACHE", "/private/tmp/kitsoki-gocache")
    env["KITSOKI_CAPTURE_PREFLIGHT_OUT"] = str(output)
    env["KITSOKI_CAPTURE_PREFLIGHT_DIR"] = str(preflight_dir)
    started_at = now_utc()
    try:
        proc = subprocess.run(
            cmd,
            cwd=ROOT,
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        stdout = proc.stdout
        stderr = proc.stderr
        returncode = proc.returncode
        add_check("webshot-command", returncode == 0, f"exit_code={returncode}; command={shlex.join(cmd)}")
    except subprocess.TimeoutExpired as exc:
        stdout = command_output_text(exc.stdout)
        stderr = command_output_text(exc.stderr)
        returncode = 124
        add_check("webshot-command", False, f"timed out after {timeout}s; command={shlex.join(cmd)}")

    output_ok = output.exists() and output.stat().st_size > 0
    add_check("webshot-output", output_ok, str(output))
    if not studio_command:
        studio_command = "go test ./internal/mcp/studio -run TestStudioPing -count=1"
    studio_ok, studio_summary, studio_stdout, studio_stderr = run_preflight_command(
        "studio-ping",
        studio_command,
        timeout,
        env,
    )
    add_check("studio-ping", studio_ok, studio_summary)
    stdout = "\n".join(part for part in [stdout, studio_stdout] if part)
    stderr = "\n".join(part for part in [stderr, studio_stderr] if part)
    quota_path = Path(quota_state) if quota_state else PROJECT_ROOT / ".artifacts" / "quota" / "provider-state.json"
    if not quota_path.is_absolute():
        quota_path = PROJECT_ROOT / quota_path
    quota_ok, quota_summary = quota_preflight_check(quota_path)
    add_check("quota-window", quota_ok, quota_summary)
    status = "passed" if all(check["status"] == "passed" for check in checks) else "failed"
    stdout_tail = stdout[-4000:] if isinstance(stdout, str) else str(stdout)[-4000:]
    stderr_tail = stderr[-4000:] if isinstance(stderr, str) else str(stderr)[-4000:]
    result = {
        "status": status,
        "preflight_id": preflight_id,
        "preflight_dir": str(preflight_dir),
        "preflight_path": str(preflight_dir / "preflight.json"),
        "markdown_path": str(preflight_dir / "preflight.md"),
        "created_at": started_at,
        "webshot_output": str(output),
        "command": cmd,
        "studio_command": shlex.split(studio_command) if studio_command else [],
        "quota_state": str(quota_path),
        "exit_code": returncode,
        "stdout": stdout_tail,
        "stderr": stderr_tail,
        "checks": checks,
        "passed": sum(1 for check in checks if check["status"] == "passed"),
        "failed": sum(1 for check in checks if check["status"] == "failed"),
    }
    write_json(preflight_dir / "preflight.json", result)
    (preflight_dir / "preflight.md").write_text(render_capture_preflight_markdown(result), encoding="utf-8")
    return result


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def build_matrix_bundle(
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
    persona_mode: str,
    driver_manifest: Optional[dict] = None,
) -> tuple[Path, dict]:
    created_at = now_utc()
    driver_manifest = driver_manifest or load_driver_manifest()
    matrix_id = f"{slug_timestamp()}-github-10-{seed}"
    matrix_dir = MATRIX_ROOT / matrix_id
    matrix_dir.mkdir(parents=True, exist_ok=False)
    targets = github_targets["targets"]
    if len(targets) != 10:
        raise SystemExit(f"GitHub matrix requires exactly 10 targets, found {len(targets)}")

    scenario_ids = [scenario["id"] for scenario in scenarios]
    assignments = []
    for index, target in enumerate(targets):
        if persona_mode == "all":
            assigned_personas = personas
        else:
            assigned_personas = [select_persona(personas, "", f"{seed}:{target['id']}")]
        for persona in assigned_personas:
            assignment_id = f"{target['id']}--{persona['id']}"
            assignment_seed = f"{seed}-{index + 1:02d}-{persona['id']}"
            scenario_tasks = [
                build_assignment_scenario_task(target, persona, scenario)
                for scenario in scenarios
            ]
            driver_arg = "" if driver_manifest.get("id") == DEFAULT_DRIVER_ID else f" --driver {driver_manifest.get('id', DEFAULT_DRIVER_ID)}"
            assignments.append({
                "id": assignment_id,
                "target": target,
                "persona": persona,
                "scenarios": scenario_ids,
                "scenario_tasks": scenario_tasks,
                "seed": assignment_seed,
                "status": "planned",
                "evidence_dir": f"evidence/{assignment_id}",
                "emit_run_command": (
                    "python3 tools/product-journey/run.py --emit-run "
                    f"--project {target['id']} "
                    f"--persona {persona['id']} "
                    f"--seed {assignment_seed}"
                    f"{driver_arg}"
                ),
                "run_hint": (
                    "Create a product-journey run with this target/persona, drive the listed scenarios "
                    "through Kitsoki and visual MCP, attach evidence, record findings, then review the bundle."
                ),
            })

    matrix = {
        "matrix_id": matrix_id,
        "created_at": created_at,
        "seed": seed,
        "persona_mode": persona_mode,
        "driver": driver_summary(driver_manifest),
        "selection_contract": github_targets["selection_contract"],
        "target_proof": github_targets.get("target_proof", {}),
        "target_count": len(targets),
        "persona_count": len(personas) if persona_mode == "all" else 1,
        "assignment_count": len(assignments),
        "scenario_count": len(scenario_ids),
        "targets": targets,
        "personas": personas,
        "scenarios": [
            {
                "id": scenario["id"],
                "label": scenario["label"],
                "stage": scenario["stage"],
                "required_mcp": scenario["required_mcp"],
                "evidence": scenario["evidence"],
                "success_criteria": scenario["success_criteria"],
            }
            for scenario in scenarios
        ],
        "assignments": assignments,
        "artifacts": {
            "matrix": "matrix.json",
            "summary": "matrix.md",
            "deck": "deck.slidey.json",
        },
    }
    write_json(matrix_dir / "matrix.json", matrix)
    (matrix_dir / "matrix.md").write_text(render_matrix_summary(matrix), encoding="utf-8")
    write_json(matrix_dir / "deck.slidey.json", render_matrix_deck(matrix))
    return matrix_dir, matrix


def add_corpus_issue(issues: list[dict], severity: str, check_id: str, message: str, detail: str = "") -> None:
    issues.append({
        "severity": severity,
        "id": check_id,
        "message": message,
        "detail": detail,
    })


def line_number_for_offset(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def normalized_context(text: str, start: int, end: int, radius: int = 120) -> str:
    return " ".join(text[max(0, start - radius):min(len(text), end + radius)].split())


def display_path(path: Path) -> str:
    for base in [PROJECT_ROOT, ROOT]:
        try:
            return path.relative_to(base).as_posix()
        except ValueError:
            continue
    return path.as_posix()


def raw_github_reference_is_prohibition(context: str) -> bool:
    lower = context.lower()
    return any(token in lower for token in [
        "never file",
        "never run",
        "do not file",
        "do not run",
        "do not use",
        "must not file",
        "must not run",
        "must not use",
        "not standalone issue tools",
        "without raw gh",
    ])


def validate_native_gitops_boundaries(issues: list[dict]) -> None:
    """Keep product-journey issue filing/fixing behind story-owned native gitops."""
    prose_paths = [
        DRIVER_AGENT,
        AUTONOMOUS_DRIVER_PROMPT,
        PRODUCT_JOURNEY_SKILL,
        PRODUCT_JOURNEY_README,
        ROOT / "stories" / "product-journey-qa" / "README.md",
    ]
    forbidden_prose_tokens = [
        "gh issue create",
        "gh issue comment",
        "gh issue close",
        "gh issue edit",
        "gh issue reopen",
        "issue-comment",
        "issue-transition",
        "issue_create",
        "issue_comment",
        "issue_transition",
        "mcp__kitsoki__issue_create",
        "mcp__kitsoki__issue_comment",
        "mcp__kitsoki__issue_transition",
    ]
    for path in prose_paths:
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8")
        collapsed = " ".join(text.split())
        for token in forbidden_prose_tokens:
            start = 0
            while True:
                index = collapsed.find(token, start)
                if index == -1:
                    break
                context = normalized_context(collapsed, index, index + len(token))
                if not raw_github_reference_is_prohibition(context):
                    add_corpus_issue(
                        issues,
                        "error",
                        "native-gitops-boundary",
                        "Product journey guidance must not steer around story-owned native gitops filing/fixing",
                        f"{display_path(path)}: {context}",
                    )
                start = index + len(token)

    execution_patterns = [
        ("raw gh subprocess", re.compile(r"subprocess\.(?:run|check_call|check_output|Popen)\(\s*\[\s*['\"]gh['\"]")),
        ("raw gh argv", re.compile(r"\[\s*['\"]gh['\"]\s*,")),
        ("story host.run gh", re.compile(r"cmd:\s*gh\b")),
    ]
    scan_roots = [
        ROOT / "tools" / "product-journey",
        ROOT / "stories" / "product-journey-qa",
    ]
    for scan_root in scan_roots:
        if not scan_root.exists():
            continue
        for path in sorted(scan_root.rglob("*")):
            if path.is_dir() or path.suffix not in {".py", ".yaml", ".yml", ".md"}:
                continue
            text = path.read_text(encoding="utf-8")
            for label, pattern in execution_patterns:
                for match in pattern.finditer(text):
                    context = normalized_context(text, match.start(), match.end())
                    if "Do not run" in context or "Never file" in context:
                        continue
                    add_corpus_issue(
                        issues,
                        "error",
                        "native-gitops-boundary",
                        "Product journey automation must use kitsoki gitops/host.gh.ticket, not raw GitHub CLI commands",
                        f"{display_path(path)}:{line_number_for_offset(text, match.start())}: {label}",
                    )

    if AUTONOMOUS_DRIVER_PROMPT.exists():
        text = AUTONOMOUS_DRIVER_PROMPT.read_text(encoding="utf-8")
        collapsed = " ".join(text.split())
        lowered = collapsed.lower()
        finalizer_markers = [
            "outer product-journey story has already queued the autonomous finalizer",
            "finalizer owns `autonomous_watchdog`, `autonomous_fix`, review, validation, stats",
        ]
        if not all(marker in lowered for marker in finalizer_markers):
            add_corpus_issue(
                issues,
                "error",
                "autonomous-driver-finalizer-boundary",
                "Autonomous driver prompt must keep final gates owned by the story finalizer",
                display_path(AUTONOMOUS_DRIVER_PROMPT),
            )
        forbidden_driver_gate_patterns = [
            r"submit `autonomous_watchdog`",
            r"submit `autonomous_fix",
            r"submitting `review`",
            r"submit `review`",
            r"submitting `validate`",
            r"submit `validate`",
            r"submitting `stats`",
            r"submit `stats`",
        ]
        for pattern in forbidden_driver_gate_patterns:
            match = re.search(pattern, lowered)
            if match:
                context = normalized_context(collapsed, match.start(), match.end())
                add_corpus_issue(
                    issues,
                    "error",
                    "autonomous-driver-finalizer-boundary",
                    "Autonomous dispatched driver must not instruct the agent to run final gates",
                    f"{display_path(AUTONOMOUS_DRIVER_PROMPT)}: {context}",
                )


def duplicate_values(values: list[str]) -> list[str]:
    seen: set[str] = set()
    duplicates: set[str] = set()
    for value in values:
        if value in seen:
            duplicates.add(value)
        seen.add(value)
    return sorted(duplicates)


def validate_story_driver_contract_bindings(issues: list[dict]) -> None:
    rooms_dir = ROOT / "stories" / "product-journey-qa" / "rooms"
    if not rooms_dir.exists():
        add_corpus_issue(issues, "error", "story-bindings", "Product journey story rooms directory is missing", str(rooms_dir))
        return
    missing_contract_binds = []
    missing_next_capture_binds = []
    missing_next_attach_binds = []
    missing_next_blocker_binds = []
    for path in sorted(rooms_dir.glob("*.yaml")):
        lines = path.read_text(encoding="utf-8").splitlines()
        in_bind = False
        bind_start = 0
        bind_lines: list[str] = []
        bind_indent = 0
        for index, line in enumerate(lines, start=1):
            stripped = line.strip()
            indent = len(line) - len(line.lstrip(" "))
            if stripped == "bind:":
                in_bind = True
                bind_start = index
                bind_lines = []
                bind_indent = indent
                continue
            if in_bind and stripped and indent <= bind_indent:
                block = "\n".join(bind_lines)
                if (
                    'missing_proof_summary: "stdout_json.missing_proof_summary"' in block
                    and 'driver_contract_summary: "stdout_json.driver_contract_summary"' not in block
                ):
                    missing_contract_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
                if (
                    'driver_contract_summary: "stdout_json.driver_contract_summary"' in block
                    and 'next_driver_capture: "stdout_json.next_driver_capture"' not in block
                ):
                    missing_next_capture_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
                if (
                    'next_driver_capture: "stdout_json.next_driver_capture"' in block
                    and 'next_driver_attach_command: "stdout_json.next_driver_attach_command"' not in block
                ):
                    missing_next_attach_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
                if (
                    'next_driver_attach_command: "stdout_json.next_driver_attach_command"' in block
                    and 'next_driver_blocker_command: "stdout_json.next_driver_blocker_command"' not in block
                ):
                    missing_next_blocker_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
                in_bind = False
            if in_bind:
                bind_lines.append(line)
        if in_bind:
            block = "\n".join(bind_lines)
            if (
                'missing_proof_summary: "stdout_json.missing_proof_summary"' in block
                and 'driver_contract_summary: "stdout_json.driver_contract_summary"' not in block
            ):
                missing_contract_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
            if (
                'driver_contract_summary: "stdout_json.driver_contract_summary"' in block
                and 'next_driver_capture: "stdout_json.next_driver_capture"' not in block
            ):
                missing_next_capture_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
            if (
                'next_driver_capture: "stdout_json.next_driver_capture"' in block
                and 'next_driver_attach_command: "stdout_json.next_driver_attach_command"' not in block
            ):
                missing_next_attach_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
            if (
                'next_driver_attach_command: "stdout_json.next_driver_attach_command"' in block
                and 'next_driver_blocker_command: "stdout_json.next_driver_blocker_command"' not in block
            ):
                missing_next_blocker_binds.append(f"{path.relative_to(ROOT)}:{bind_start}")
    if missing_contract_binds:
        add_corpus_issue(
            issues,
            "error",
            "story-driver-contract-bindings",
            "Run-result story binds must preserve driver_contract_summary with missing_proof_summary",
            ", ".join(missing_contract_binds),
        )
    if missing_next_capture_binds:
        add_corpus_issue(
            issues,
            "error",
            "story-next-driver-capture-bindings",
            "Run-result story binds must preserve next_driver_capture with driver_contract_summary",
            ", ".join(missing_next_capture_binds),
        )
    if missing_next_attach_binds:
        add_corpus_issue(
            issues,
            "error",
            "story-next-driver-attach-bindings",
            "Run-result story binds must preserve next_driver_attach_command with next_driver_capture",
            ", ".join(missing_next_attach_binds),
        )
    if missing_next_blocker_binds:
        add_corpus_issue(
            issues,
            "error",
            "story-next-driver-blocker-bindings",
            "Run-result story binds must preserve next_driver_blocker_command with next_driver_attach_command",
            ", ".join(missing_next_blocker_binds),
        )


def validate_driver_agent_contract(issues: list[dict]) -> None:
    if not DRIVER_AGENT.exists():
        add_corpus_issue(issues, "error", "driver-agent-contract", "Product journey QA driver agent is missing", str(DRIVER_AGENT))
        return
    text = DRIVER_AGENT.read_text(encoding="utf-8")
    required_tokens = [
        "last_result.driver_scenarios",
        "last_result.missing_proof_evidence",
        "last_result.driver_final_gates",
        "last_result.next_driver_capture",
        "last_result.next_driver_capture_route",
        "last_result.next_driver_attach_command",
        "last_result.next_driver_blocker_command",
        "record the honest blocker",
    ]
    missing = [token for token in required_tokens if token not in text]
    if missing:
        add_corpus_issue(
            issues,
            "error",
            "driver-agent-contract",
            "Product journey QA driver agent does not describe the MCP-visible driver contract",
            ", ".join(missing),
        )
    frontmatter = ""
    if text.startswith("---"):
        parts = text.split("---", 2)
        if len(parts) >= 3:
            frontmatter = parts[1]
    forbidden_tools = [
        "mcp__kitsoki__issue_create",
    ]
    present_forbidden_tools = [tool for tool in forbidden_tools if tool in frontmatter]
    if present_forbidden_tools:
        add_corpus_issue(
            issues,
            "error",
            "driver-agent-forbidden-tools",
            "Product journey QA driver must file/fix through story-owned gates, not standalone issue tools",
            ", ".join(present_forbidden_tools),
        )
    forbidden_guidance = [
        "gh issue create",
        "issue_create",
    ]
    present_forbidden_guidance = [
        token for token in forbidden_guidance
        if token in text and f"Do not run `{token.split()[0]}`" not in text
    ]
    if present_forbidden_guidance:
        add_corpus_issue(
            issues,
            "error",
            "driver-agent-forbidden-filing-guidance",
            "Product journey QA driver guidance must not steer around story-owned filing/fixing",
            ", ".join(present_forbidden_guidance),
        )


def validate_autonomous_workflow_docs(issues: list[dict]) -> None:
    required_docs = [
        (PRODUCT_JOURNEY_SKILL, "skill"),
        (PRODUCT_JOURNEY_README, "README"),
    ]
    for path, label in required_docs:
        if not path.exists():
            add_corpus_issue(
                issues,
                "error",
                "autonomous-workflow-docs",
                f"Product journey {label} is missing",
                str(path),
            )
            continue
        text = path.read_text(encoding="utf-8")
        required_tokens = [
            "autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<url>",
            "story-owned",
            "kitsoki gitops autonomous-fix",
            "gh-agent",
            "independent-verify.md",
            # Matches both the deprecated "--persona-autofix-smoke" flag and
            # the canonical "--gate persona-autofix" it forwards to, so this
            # check does not force docs to keep citing a deprecated flag.
            "persona-autofix",
            "persona_autofix_smoke",
        ]
        missing = [token for token in required_tokens if token not in text]
        if missing:
            add_corpus_issue(
                issues,
                "error",
                "autonomous-workflow-docs",
                f"Product journey {label} does not present the story-owned autonomous fix path",
                f"{path.relative_to(ROOT)}: {', '.join(missing)}",
            )
        stale_guidance = [
            "file_findings ticket_repo=<owner/repo>` intent (preferred",
            "File the credible `issue` findings as GitHub issues through the story\n   `file_findings",
        ]
        present_stale = [token for token in stale_guidance if token in text]
        if present_stale:
            add_corpus_issue(
                issues,
                "error",
                "autonomous-workflow-docs",
                f"Product journey {label} still presents split filing as the preferred full-loop path",
                path.relative_to(ROOT).as_posix(),
            )
    run_created_room = ROOT / "stories" / "product-journey-qa" / "rooms" / "run_created.yaml"
    if not run_created_room.exists():
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey run_created room is missing",
            str(run_created_room),
        )
        return
    room_text = run_created_room.read_text(encoding="utf-8")
    autonomous_label = "autonomous_fix ticket_repo=owner/repo gh_agent_public_base_url=<url>"
    gitops_facade = "gitops"
    gitops_command = "autonomous-fix"
    report_bind = 'autonomous_fix_report_path: "stdout_json.autonomous_fix_report_path"'
    file_label = "file_findings ticket_repo=owner/repo"
    if autonomous_label not in room_text:
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey run view does not expose autonomous_fix as the full-loop action",
            run_created_room.relative_to(ROOT).as_posix(),
        )
    if gitops_facade not in room_text or gitops_command not in room_text:
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey autonomous fix must invoke the native gitops facade, not runner plumbing",
            run_created_room.relative_to(ROOT).as_posix(),
        )
    if report_bind not in room_text or "Autonomous report" not in room_text:
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey run view must bind and surface the autonomous-fix report artifact",
            run_created_room.relative_to(ROOT).as_posix(),
        )
    if file_label in room_text and autonomous_label in room_text and room_text.index(file_label) < room_text.index(autonomous_label):
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey run view lists file_findings before the story-owned autonomous_fix gate",
            run_created_room.relative_to(ROOT).as_posix(),
        )
    stale_room_guidance = "Use file_findings ticket_repo=owner/repo to file recorded issue findings"
    if stale_room_guidance in room_text:
        add_corpus_issue(
            issues,
            "error",
            "autonomous-workflow-docs",
            "Product journey run view still presents file_findings as the default issue-to-fix guidance",
            run_created_room.relative_to(ROOT).as_posix(),
        )
    file_findings_call = "id: file_product_journey_findings"
    if file_findings_call in room_text:
        call_start = room_text.index(file_findings_call)
        bind_start = room_text.find("\n              bind:", call_start)
        call_block = room_text[call_start:bind_start if bind_start != -1 else len(room_text)]
        forbidden_file_findings_tokens = [
            "--gh-agent-db",
            "--gh-agent-story",
            "--gh-agent-drain",
            "--gh-agent-public-base-url",
            "--gh-agent-project-root",
            "--gh-agent-incident-repo",
            "--gh-agent-asset-dir",
            "--gh-agent-comment-mode",
        ]
        present_forbidden_file_findings = [
            token for token in forbidden_file_findings_tokens if token in call_block
        ]
        if present_forbidden_file_findings:
            add_corpus_issue(
                issues,
                "error",
                "file-findings-gh-agent-boundary",
                "Product journey file_findings must stay filing-only; gh-agent queue/drain/fix belongs to autonomous_fix",
                ", ".join(present_forbidden_file_findings),
            )
    app_path = ROOT / "stories" / "product-journey-qa" / "app.yaml"
    if app_path.exists():
        app_text = app_path.read_text(encoding="utf-8")
        file_intent = "  file_findings:"
        autonomous_intent = "  autonomous_fix:"
        if file_intent in app_text and autonomous_intent in app_text:
            intent_start = app_text.index(file_intent)
            intent_end = app_text.index(autonomous_intent, intent_start)
            intent_block = app_text[intent_start:intent_end]
            if "gh_agent_" in intent_block:
                add_corpus_issue(
                    issues,
                    "error",
                    "file-findings-gh-agent-boundary",
                    "Product journey file_findings must not expose gh-agent slots; use autonomous_fix for the full issue-to-fix loop",
                    app_path.relative_to(ROOT).as_posix(),
                )


def validate_journey_corpus(personas: list[dict], scenarios: list[dict], github_targets: dict) -> dict:
    schema = read_json(SCHEMA)
    issues: list[dict] = []
    targets = github_targets.get("targets", [])
    active_persona_list = active_personas(personas)
    draft_persona_list = [persona for persona in personas if not is_active_persona(persona)]
    active_scenario_list = active_scenarios(scenarios)
    draft_scenario_list = [scenario for scenario in scenarios if not is_active_scenario(scenario)]
    persona_required = ["id", "label", "description", "surface_preference", "risk_focus"]
    scenario_required = ["id", "label", "stage", "task", "primary_story", "required_mcp", "evidence", "success_criteria"]
    target_required = ["id", "label", "repo", "stack", "license_spdx", "bug_query", "open_bug_floor", "status", "notes"]
    allowed_mcp = set(CANONICAL_DRIVER_CAPABILITIES)
    required_scenarios = {
        "product-discovery",
        "project-onboarding",
        "bugfix",
        "prd-design",
        "feature-implementation",
        "evidence-backed-product-bug",
    }
    core_natural_utterance_scenarios = set(schema.get("scenario", {}).get(
        "core_scenarios_require_natural_utterances",
        ["project-onboarding", "prd-design", "bugfix"],
    ))
    natural_utterance_required = schema.get("scenario", {}).get(
        "natural_utterance_required",
        ["text", "source", "source_ref"],
    )
    case_variant_required = schema.get("scenario", {}).get(
        "case_variant_required",
        ["id", "utterance", "setup", "success_focus"],
    )
    schema_transport_ids = set(schema.get("transports", {}).get("allowed_values", []))
    if schema_transport_ids != set(TRANSPORT_IDS):
        add_corpus_issue(
            issues,
            "error",
            "transport-schema-drift",
            "schema.json transport allowed_values must match run.py TRANSPORT_PROFILES",
            f"schema={', '.join(sorted(schema_transport_ids))}; runner={', '.join(TRANSPORT_IDS)}",
        )
    schema_contracts = schema.get("transports", {}).get("evidence_contract_by_transport", {})
    for transport, profile in TRANSPORT_PROFILES.items():
        expected_contract = profile.get("evidence_contract", {})
        declared_contract = schema_contracts.get(transport, {})
        for key in ["primary_tool", "evidence_kind", "level"]:
            if declared_contract.get(key) != expected_contract.get(key):
                add_corpus_issue(
                    issues,
                    "error",
                    "transport-schema-contract-drift",
                    "schema.json transport evidence contract must match run.py TRANSPORT_PROFILES",
                    f"{transport}/{key}: schema={declared_contract.get(key, '')!r}; runner={expected_contract.get(key, '')!r}",
                )
    # The four workflow personas WS-F wires across every natural-use scenario
    # (surface_preference names each persona's primary surface: TUI, web,
    # docs, VS Code). Product-journey's generic run/agent-brief/driver-plan
    # machinery already crosses every active persona with every active
    # scenario, so this check just guards the corpus does not silently drop
    # one.
    required_personas = {
        "core-maintainer",
        "dependency-debugger",
        "docs-minded-contributor",
        "ide-first-engineer",
    }

    persona_ids = [persona.get("id", "") for persona in personas]
    scenario_ids = [scenario.get("id", "") for scenario in scenarios]
    target_ids = [target.get("id", "") for target in targets]
    for label, values in [("persona", persona_ids), ("scenario", scenario_ids), ("target", target_ids)]:
        duplicates = duplicate_values(values)
        if duplicates:
            add_corpus_issue(issues, "error", f"duplicate-{label}-ids", f"Duplicate {label} ids", ", ".join(duplicates))
        blanks = [f"{label}-{index}" for index, value in enumerate(values, start=1) if not value]
        if blanks:
            add_corpus_issue(issues, "error", f"blank-{label}-ids", f"Blank {label} ids", ", ".join(blanks))

    for scenario in scenarios:
        declared_tier = scenario.get("tier")
        if declared_tier is not None and declared_tier not in CATALOG_TIERS:
            add_corpus_issue(
                issues, "error", "scenario-tier",
                "Scenario tier must be curated or mined when declared",
                f"{scenario.get('id', 'unknown')}: tier={declared_tier!r}",
            )
        elif declared_tier is None:
            add_corpus_issue(
                issues, "warn", "scenario-tier-undeclared",
                "Scenario does not declare an explicit tier; inferred from whether it has transports",
                f"{scenario.get('id', 'unknown')}: inferred={scenario_tier(scenario)}",
            )
    for persona in personas:
        declared_tier = persona.get("tier")
        if declared_tier is not None and declared_tier not in CATALOG_TIERS:
            add_corpus_issue(
                issues, "error", "persona-tier",
                "Persona tier must be curated or mined when declared",
                f"{persona.get('id', 'unknown')}: tier={declared_tier!r}",
            )
        elif declared_tier is None:
            add_corpus_issue(
                issues, "warn", "persona-tier-undeclared",
                "Persona does not declare an explicit tier; inferred from whether it has persona_lens",
                f"{persona.get('id', 'unknown')}: inferred={persona_tier(persona)}",
            )

    if draft_persona_list:
        draft_ids = ", ".join(sorted(persona.get("id", "unknown") for persona in draft_persona_list))
        add_corpus_issue(issues, "warn", "draft-personas", "Draft personas are skipped by active product-journey gates", draft_ids)
    if draft_scenario_list:
        draft_ids = sorted(scenario.get("id", "unknown") for scenario in draft_scenario_list)
        detail = ", ".join(draft_ids[:8])
        if len(draft_ids) > 8:
            detail = f"{detail}, ... ({len(draft_ids)} total)"
        add_corpus_issue(issues, "warn", "draft-scenarios", "Draft scenarios are skipped by active product-journey gates", detail)

    if len(active_persona_list) < 4:
        add_corpus_issue(issues, "warn", "persona-count", "Active persona corpus is narrow for natural-use sweeps", f"personas={len(active_persona_list)}")
    active_persona_ids = {persona.get("id", "") for persona in active_persona_list}
    missing_required_personas = sorted(required_personas - active_persona_ids)
    if missing_required_personas:
        add_corpus_issue(issues, "error", "required-personas", "Required workflow personas are missing or inactive", ", ".join(missing_required_personas))
    for persona in active_persona_list:
        missing = [key for key in persona_required if key not in persona]
        if missing:
            add_corpus_issue(issues, "error", "persona-required-keys", "Persona is missing required keys", f"{persona.get('id', 'unknown')}: {', '.join(missing)}")
        if not isinstance(persona.get("risk_focus", []), list) or not persona.get("risk_focus"):
            add_corpus_issue(issues, "error", "persona-risk-focus", "Persona must name at least one risk focus", persona.get("id", "unknown"))

    active_scenario_ids = [scenario.get("id", "") for scenario in active_scenario_list]
    missing_required_scenarios = sorted(required_scenarios - set(active_scenario_ids))
    if missing_required_scenarios:
        add_corpus_issue(issues, "error", "required-scenarios", "Required natural-use scenarios are missing", ", ".join(missing_required_scenarios))
    for scenario in active_scenario_list:
        scenario_id = scenario.get("id", "unknown")
        missing = [key for key in scenario_required if key not in scenario]
        if missing:
            add_corpus_issue(issues, "error", "scenario-required-keys", "Scenario is missing required keys", f"{scenario_id}: {', '.join(missing)}")
        if scenario.get("stage") not in STAGES:
            add_corpus_issue(issues, "error", "scenario-stage", "Scenario uses an unknown stage", f"{scenario_id}: {scenario.get('stage', '')}")
        unknown_mcp = sorted(set(scenario.get("required_mcp", [])) - allowed_mcp)
        if unknown_mcp:
            add_corpus_issue(issues, "error", "scenario-mcp", "Scenario requires unknown MCP tools", f"{scenario_id}: {', '.join(unknown_mcp)}")
        declared_transports = scenario.get("transports")
        if declared_transports:
            unknown_transports = sorted(set(declared_transports.get("allowed", [])) - set(TRANSPORT_IDS))
            if unknown_transports:
                add_corpus_issue(issues, "error", "scenario-transports-allowed", "Scenario declares unknown transport id(s)", f"{scenario_id}: {', '.join(unknown_transports)}")
            required_not_allowed = sorted(set(declared_transports.get("required", [])) - set(declared_transports.get("allowed", [])))
            if required_not_allowed:
                add_corpus_issue(issues, "error", "scenario-transports-required", "Scenario requires transport(s) it does not allow", f"{scenario_id}: {', '.join(required_not_allowed)}")
            overrides = declared_transports.get("overrides", {}) or {}
            unknown_override_transports = sorted(set(overrides.keys()) - set(declared_transports.get("allowed", [])))
            if unknown_override_transports:
                add_corpus_issue(issues, "error", "scenario-transports-overrides", "Scenario overrides a transport it does not allow", f"{scenario_id}: {', '.join(unknown_override_transports)}")
            for transport, override in overrides.items():
                unknown_override_mcp = sorted(set(override.get("required_mcp", [])) - allowed_mcp)
                if unknown_override_mcp:
                    add_corpus_issue(issues, "error", "scenario-transports-override-mcp", "Scenario transport override requires unknown MCP tools", f"{scenario_id}/{transport}: {', '.join(unknown_override_mcp)}")
        if not scenario.get("success_criteria"):
            add_corpus_issue(issues, "error", "scenario-success-criteria", "Scenario must have success criteria", scenario_id)
        if not scenario.get("evidence"):
            add_corpus_issue(issues, "error", "scenario-evidence", "Scenario must declare evidence slots", scenario_id)
        if scenario_id in core_natural_utterance_scenarios:
            utterances = scenario.get("natural_utterances", [])
            if not isinstance(utterances, list) or not utterances:
                add_corpus_issue(
                    issues,
                    "error",
                    "scenario-natural-utterances",
                    "Core scenario must carry transcript-derived natural utterances",
                    scenario_id,
                )
            else:
                missing_items = []
                for index, utterance in enumerate(utterances, start=1):
                    if not isinstance(utterance, dict):
                        missing_items.append(f"{scenario_id}[{index}]: not an object")
                        continue
                    missing_utterance_keys = [
                        key for key in natural_utterance_required
                        if not str(utterance.get(key, "")).strip()
                    ]
                    if missing_utterance_keys:
                        missing_items.append(f"{scenario_id}[{index}]: {', '.join(missing_utterance_keys)}")
                    if utterance.get("source") != "session-transcript":
                        missing_items.append(f"{scenario_id}[{index}]: source={utterance.get('source', '')}")
                if missing_items:
                    add_corpus_issue(
                        issues,
                        "error",
                        "scenario-natural-utterances",
                        "Core scenario natural utterances must name transcript source metadata",
                        "; ".join(missing_items),
                    )
        case_variants = scenario.get("case_variants", [])
        if case_variants:
            if not isinstance(case_variants, list):
                add_corpus_issue(
                    issues,
                    "error",
                    "scenario-case-variants",
                    "Scenario case_variants must be a list",
                    scenario_id,
                )
            else:
                variant_issues = []
                variant_ids = []
                for index, variant in enumerate(case_variants, start=1):
                    if not isinstance(variant, dict):
                        variant_issues.append(f"{scenario_id}[{index}]: not an object")
                        continue
                    variant_ids.append(variant.get("id", ""))
                    missing_variant_keys = [
                        key for key in case_variant_required
                        if not str(variant.get(key, "")).strip()
                    ]
                    if missing_variant_keys:
                        variant_issues.append(f"{scenario_id}[{index}]: {', '.join(missing_variant_keys)}")
                duplicate_variant_ids = duplicate_values([value for value in variant_ids if value])
                if duplicate_variant_ids:
                    variant_issues.append(f"{scenario_id}: duplicate ids {', '.join(duplicate_variant_ids)}")
                if variant_issues:
                    add_corpus_issue(
                        issues,
                        "error",
                        "scenario-case-variants",
                        "Scenario case variants must be complete and uniquely identified",
                        "; ".join(variant_issues),
                    )
        unknown_evidence = [
            kind for kind in scenario.get("evidence", [])
            if evidence_capture_hint(kind) == "Save this evidence artifact and attach it to the run."
        ]
        if unknown_evidence:
            add_corpus_issue(issues, "error", "scenario-evidence-kind", "Scenario uses evidence kinds without capture hints", f"{scenario_id}: {', '.join(unknown_evidence)}")
        if not scenario_playback_kind(scenario):
            declared_playback = sorted(PLAYBACK_EVIDENCE_KINDS & set(scenario.get("evidence", [])))
            add_corpus_issue(
                issues,
                "error",
                "scenario-playback-evidence",
                "Scenario must declare exactly one playback-capable evidence kind (rrweb|trace-replay|flow-fixture|png-sequence)",
                f"{scenario_id}: {', '.join(declared_playback) or 'none'}",
            )
        gate = scenario_quality_gate(scenario_id)
        missing_gate_keys = [key for key in schema["driver_plan"]["quality_gate_required"] if key not in gate]
        if missing_gate_keys:
            add_corpus_issue(issues, "error", "scenario-quality-gate", "Scenario quality gate is missing required keys", f"{scenario_id}: {', '.join(missing_gate_keys)}")
        minimum = set(gate.get("minimum_evidence", []))
        declared = set(scenario.get("evidence", []))
        extra = sorted(minimum - declared)
        if extra:
            add_corpus_issue(issues, "error", "scenario-quality-gate-evidence", "Quality gate evidence is not declared by scenario", f"{scenario_id}: {', '.join(extra)}")

    expected_targets = schema["matrix_result"]["target_count"]
    selection_contract = github_targets.get("selection_contract", {})
    if selection_contract.get("host") != "github.com":
        add_corpus_issue(issues, "error", "target-selection-host", "GitHub target selection contract must use github.com", selection_contract.get("host", ""))
    if selection_contract.get("license") != "open-source":
        add_corpus_issue(issues, "error", "target-selection-license", "GitHub target selection contract must require open-source licensing", selection_contract.get("license", ""))
    if selection_contract.get("open_bug_floor", 0) < 100:
        add_corpus_issue(issues, "error", "target-selection-bug-floor", "Selection open_bug_floor is below the natural-use floor", str(selection_contract.get("open_bug_floor", "")))
    if len(targets) != expected_targets:
        add_corpus_issue(issues, "error", "target-count", "GitHub target corpus must contain exactly 10 repositories", f"expected={expected_targets}, actual={len(targets)}")
    for target in targets:
        target_id = target.get("id", "unknown")
        missing = [key for key in target_required if key not in target]
        if missing:
            add_corpus_issue(issues, "error", "target-required-keys", "GitHub target is missing required keys", f"{target_id}: {', '.join(missing)}")
        repo = target.get("repo", "")
        parsed_repo = urllib.parse.urlparse(repo)
        if parsed_repo.netloc != "github.com" or len(parsed_repo.path.strip("/").split("/")) != 2:
            add_corpus_issue(issues, "error", "target-repo", "GitHub target repo must be a github.com owner/name URL", f"{target_id}: {repo}")
        if target.get("open_bug_floor", 0) < 100:
            add_corpus_issue(issues, "error", "target-open-bug-floor", "GitHub target open_bug_floor is below the natural-use floor", f"{target_id}: {target.get('open_bug_floor')}")
        if target.get("license_spdx", "") in {"", "NOASSERTION"}:
            add_corpus_issue(issues, "error", "target-license", "GitHub target must declare an open-source SPDX license", f"{target_id}: {target.get('license_spdx', '')}")
        issue_query = github_issue_search_query(target).lower()
        if "bug" not in issue_query:
            add_corpus_issue(issues, "warn", "target-bug-query", "GitHub target bug query does not include an explicit bug term or bug label", f"{target_id}: {target.get('bug_query', '')}")

    validate_story_driver_contract_bindings(issues)
    validate_driver_agent_contract(issues)
    validate_autonomous_workflow_docs(issues)
    validate_native_gitops_boundaries(issues)

    errors = sum(1 for issue in issues if issue["severity"] == "error")
    warnings = sum(1 for issue in issues if issue["severity"] == "warn")
    return {
        "status": "valid" if errors == 0 else "invalid",
        "personas": len(active_persona_list),
        "scenarios": len(active_scenario_list),
        "all_personas": len(personas),
        "all_scenarios": len(scenarios),
        "draft_personas": len(draft_persona_list),
        "draft_scenarios": len(draft_scenario_list),
        "targets": len(targets),
        "errors": errors,
        "warnings": warnings,
        "issues": issues,
    }


def is_proof_evidence(item: dict, run_dir: Optional[Path] = None) -> bool:
    if item.get("status") not in {"captured", "validated"}:
        return False
    source = item.get("source") or evidence_source(item.get("path", ""), item.get("notes", ""))
    if source not in PROOF_EVIDENCE_SOURCES:
        return False
    # When a run_dir is supplied (the gating call sites), file-backed proof
    # sources (local, cassette) must actually RESOLVE to a backing artifact —
    # an unbacked cassette://…/nothing.diff or a dangling local path is not
    # proof. Remote/opaque sources (retained, external) can't be stat'd, so
    # they stay proof on source alone. Reporting call sites pass no run_dir and
    # keep the legacy source-only classification.
    if run_dir is not None and source in {"local", "cassette"}:
        return artifact_ref_exists(run_dir, item.get("path", ""))
    return True


def is_playback_evidence(item: dict, run_dir: Optional[Path] = None) -> bool:
    """True when `item` is one of the four playback-capable evidence kinds
    (rrweb / trace-replay / flow-fixture / png-sequence) AND is backed by a
    real LOCAL file. Unlike is_proof_evidence, a cassette://, http(s)://, or
    other opaque/indirect URI is NEVER accepted here — this slot exists so the
    scenario can actually be replayed, and an indirection defeats that purpose
    even when it happens to resolve to a real file elsewhere."""
    if item.get("kind") not in PLAYBACK_EVIDENCE_KINDS:
        return False
    if item.get("status") not in {"captured", "validated"}:
        return False
    path = (item.get("path") or "").strip()
    if not path or "://" in path or path.startswith(("cassette:", "retained:", "image:", "trace:", "mcp:")):
        return False
    candidate = Path(path)
    if candidate.is_absolute():
        return candidate.is_file()
    if run_dir is None:
        return False
    return (
        (run_dir / candidate).is_file()
        or (PROJECT_ROOT / candidate).is_file()
        or (ROOT / candidate).is_file()
    )


def missing_playback_evidence(
    run_json: dict,
    evidence_items: list[dict],
    run_dir: Path,
    blocked_scenarios: Optional[set] = None,
) -> list[str]:
    """Non-mined, non-blocked scenarios whose declared playback-capable
    evidence kind lacks a captured/validated item backed by a real local file.
    A blocked scenario (recorded via record_blocker) is exempt, matching the
    other per-scenario coverage checks."""
    blocked_scenarios = blocked_scenarios or set()
    by_scenario: dict[str, list[dict]] = {}
    for item in evidence_items:
        by_scenario.setdefault(item.get("scenario", ""), []).append(item)
    missing = []
    for scenario in run_json.get("scenarios", []):
        if scenario.get("source") == "mined":
            continue
        scenario_id = scenario.get("id", "")
        if scenario_id in blocked_scenarios:
            continue
        kind = scenario_playback_kind(scenario)
        if not kind:
            missing.append(f"{scenario_id}: no playback-capable evidence kind declared")
            continue
        items = [item for item in by_scenario.get(scenario_id, []) if item.get("kind") == kind]
        if any(is_playback_evidence(item, run_dir) for item in items):
            continue
        attempted = [item.get("path", "") for item in items if item.get("status") in {"captured", "validated"}]
        if attempted:
            missing.append(f"{scenario_id}/{kind}: unbacked reference ({', '.join(attempted)})")
        else:
            missing.append(f"{scenario_id}/{kind}: not captured")
    return missing


def playback_deck_scenes(media_manifest: Optional[dict], limit: Optional[int] = None) -> list[dict]:
    # validate_run_bundle's deck-playback-coverage check requires the deck to
    # reference every media-manifest item marked playback=True, so this must
    # emit a scene for each one (no truncation) unless a caller explicitly
    # asks for a bounded preview via `limit`.
    if media_manifest is None:
        return []
    scenes = []
    for item in media_manifest.get("items", []):
        if not item.get("playback"):
            continue
        scene = playback_scene_for_item(item)
        if scene is not None:
            scenes.append(scene)
        if limit is not None and len(scenes) >= limit:
            break
    return scenes


def build_scenario_outcomes(run_json: dict, evidence: dict, findings: dict) -> dict:
    evidence_by_scenario: dict[str, list[dict]] = {}
    for item in evidence.get("items", []):
        evidence_by_scenario.setdefault(item.get("scenario", ""), []).append(item)
    findings_by_scenario: dict[str, list[dict]] = {}
    for item in findings.get("items", []):
        findings_by_scenario.setdefault(item.get("scenario", ""), []).append(item)

    outcomes = []
    for scenario in run_json["scenarios"]:
        scenario_evidence = evidence_by_scenario.get(scenario["id"], [])
        scenario_findings = findings_by_scenario.get(scenario["id"], [])
        present = [item for item in scenario_evidence if item.get("status") in {"captured", "validated"}]
        demo = [item for item in present if (item.get("source") or evidence_source(item.get("path", ""), item.get("notes", ""))) == "demo"]
        proof = [item for item in present if is_proof_evidence(item)]
        validated = [item for item in scenario_evidence if item.get("status") == "validated"]
        rejected = [item for item in scenario_evidence if item.get("status") == "rejected"]
        counts = {
            "strength": sum(1 for item in scenario_findings if item.get("kind") == "strength"),
            "weakness": sum(1 for item in scenario_findings if item.get("kind") == "weakness"),
            "issue": sum(1 for item in scenario_findings if item.get("kind") == "issue"),
            "fix": sum(1 for item in scenario_findings if item.get("kind") == "fix"),
            "blocked": sum(1 for item in scenario_findings if item.get("status") == "blocked"),
        }
        if scenario_evidence and len(validated) == len(scenario_evidence):
            evidence_status = "validated"
        elif present:
            evidence_status = "captured"
        elif rejected:
            evidence_status = "rejected"
        else:
            evidence_status = "missing"

        if counts["fix"]:
            outcome = "fix_recorded"
        elif counts["blocked"]:
            outcome = "blocked"
        elif counts["issue"]:
            outcome = "issue_found"
        elif counts["weakness"]:
            outcome = "weakness_found"
        elif counts["strength"]:
            outcome = "strength_observed"
        elif present:
            outcome = "evidence_captured"
        else:
            outcome = "not_started"

        outcomes.append({
            "scenario": scenario["id"],
            "label": scenario["label"],
            "stage": scenario["stage"],
            "primary_story": scenario["primary_story"],
            "evidence_status": evidence_status,
            "required_evidence_count": len(scenario_evidence),
            "present_evidence_count": len(present),
            "demo_evidence_count": len(demo),
            "proof_evidence_count": len(proof),
            "validated_evidence_count": len(validated),
            "rejected_evidence_count": len(rejected),
            "finding_counts": counts,
            "findings": [
                {
                    "id": item.get("id", ""),
                    "kind": item.get("kind", ""),
                    "title": item.get("title", ""),
                    "status": item.get("status", ""),
                    "severity": item.get("severity", ""),
                    "evidence_path": item.get("evidence_path", ""),
                }
                for item in scenario_findings
            ],
            "outcome": outcome,
        })

    return {
        "run_id": run_json["run_id"],
        "items": outcomes,
        "summary": {
            "scenarios": len(outcomes),
            "started": sum(1 for item in outcomes if item["outcome"] != "not_started"),
            "with_findings": sum(1 for item in outcomes if sum(item["finding_counts"][kind] for kind in ["strength", "weakness", "issue", "fix"]) > 0),
            "with_issues": sum(1 for item in outcomes if item["finding_counts"]["issue"] or item["finding_counts"]["weakness"]),
            "with_fixes": sum(1 for item in outcomes if item["finding_counts"]["fix"]),
            "blocked": sum(1 for item in outcomes if item["finding_counts"]["blocked"]),
            "fully_validated": sum(1 for item in outcomes if item["evidence_status"] == "validated"),
        },
    }


def autonomous_fix_cli_command(run_dir_arg: str) -> str:
    return (
        "go run ./cmd/kitsoki gitops autonomous-fix --json "
        "--report-invalid-autonomous-fix "
        f"--run-dir {run_dir_arg} "
        "--ticket-repo <owner/repo> "
        f"--agent-db {run_dir_arg}/gh-agent-jobs.sqlite "
        "--public-base-url <public-gh-agent-url>"
    )


def autonomous_watchdog_cli_command(run_dir_arg: str) -> str:
    return f"python3 tools/product-journey/run.py --autonomous-marathon-watchdog --run-dir {run_dir_arg}"


def leg_needs_live(leg: dict) -> bool:
    """Deterministic proxy for "this leg's flow needs interpretive behavior a
    cassette can't replay" — mirrors
    stories/scenario-qa/scripts/plan_legs.star's `_needs_live` so the preview
    path (`--transport-suite`) and the `check` legs it previews agree on when
    a leg needs a live authorization note. `natural_utterances` (free-text
    phrasing a cassette can't cover) is the strongest signal; a `harness`
    hint containing "live" is the other. Preview legs always come from a
    catalog scenario (there is no ad-hoc/free-text mode here), so unlike the
    Starlark version there is no `mode == "adhoc"` fallback to mirror.
    """
    utterances = leg.get("natural_utterances", [])
    if isinstance(utterances, list) and len(utterances) > 0:
        return True
    harness = leg.get("harness", "")
    if isinstance(harness, str) and "live" in harness:
        return True
    return False


def leg_live_authorization_note(leg: dict, live_profile: str) -> str:
    """Mirrors plan_legs.star's `_live_authorization_note`: empty when the
    leg doesn't need live drive, an authorized note when `live_profile` is
    set, otherwise a warning that the leg will run replay-only and missing
    cassettes will surface as degraded-evidence with a stated cause."""
    transport = leg.get("transport", "")
    if not leg_needs_live(leg):
        return ""
    if live_profile:
        return f"leg {transport}: live drive authorized (profile={live_profile})."
    return (
        f"leg {transport}: needs `profile=<name>` for live drive — will run "
        "replay-only; missing cassettes will be reported as degraded-evidence with cause."
    )


def build_transport_suite(
    scenarios: list[dict],
    transports: list[str],
    driver_manifest: Optional[dict] = None,
    live_profile: str = "",
) -> dict:
    """Describe the deterministic scenario x transport plan without artifacts.

    This is the story-facing preview used by `stories/scenario-qa` and by the
    internal compatibility adapter: it answers which legs a scenario can drive,
    what each leg must capture, and which stable entrypoints the driver must
    use.
    """

    driver_manifest = driver_manifest or load_driver_manifest()
    requested = list(transports or TRANSPORT_IDS)
    scenario_rows = []
    all_legs = []
    live_authorization_summary: list[str] = []
    skipped_total: dict[str, int] = {transport: 0 for transport in requested}
    for scenario in scenarios:
        contract = resolve_scenario_transports(scenario)
        allowed = list(contract.get("allowed", []))
        required = list(contract.get("required", allowed))
        legs = []
        for leg in scenario_transport_legs(scenario, requested):
            profile = transport_profile(leg["transport"])
            contract_details = profile.get("evidence_contract", {})
            primary_evidence_kind = contract_details.get("evidence_kind", "")
            open_capabilities = capture_phase_capabilities(profile, "open", leg["required_mcp"]) or ["session.status"]
            observe_capabilities = capture_observe_capabilities(
                leg["required_mcp"],
                leg.get("visual_surface", profile.get("visual_surface", leg["transport"])),
                primary_evidence_kind,
                profile,
            )
            act_capabilities = capture_phase_capabilities(profile, "act", leg["required_mcp"]) or ["session.trace"]
            live_authorization_note = leg_live_authorization_note(leg, live_profile)
            if live_authorization_note and not live_profile:
                live_authorization_summary.append(live_authorization_note)
            row = {
                "leg_id": leg["leg_id"],
                "scenario": scenario["id"],
                "scenario_label": scenario.get("label", scenario["id"]),
                "transport": leg["transport"],
                "transport_label": profile.get("label", leg["transport"]),
                "visual_surface": leg.get("visual_surface", profile.get("visual_surface", leg["transport"])),
                "primary_story": scenario["primary_story"],
                "required": leg["transport"] in required,
                "required_mcp": leg["required_mcp"],
                "resolved_mcp_tools": resolved_mcp_tools(leg["required_mcp"], driver_manifest),
                "evidence": leg["evidence"],
                "evidence_contract": contract_details,
                "quality_gate": leg_quality_gate(scenario["id"], leg["evidence"], leg["transport"]),
                "needs_live_hint": leg_needs_live(leg),
                "live_authorization_note": live_authorization_note,
                "entrypoints": {
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
                },
                "preflight": profile.get("preflight", ""),
                "recording_rule": profile.get("recording_rule", ""),
                "capture_policy": {
                    "proof_source_required": "retained|external|local|cassette",
                    "no_substitution": "Do not attach demo, placeholder, synthetic, or unrelated media as proof.",
                },
            }
            if profile.get("editor_evidence_contract"):
                row["editor_evidence_contract"] = profile["editor_evidence_contract"]
            legs.append(row)
            all_legs.append(row)
        skipped = [transport for transport in requested if transport not in allowed]
        for transport in skipped:
            skipped_total[transport] = skipped_total.get(transport, 0) + 1
        scenario_rows.append({
            "scenario": scenario["id"],
            "label": scenario.get("label", scenario["id"]),
            "stage": scenario.get("stage", ""),
            "task": scenario.get("task", ""),
            "success_criteria": scenario.get("success_criteria", []),
            "primary_story": scenario.get("primary_story", ""),
            "allowed_transports": allowed,
            "required_transports": required,
            "requested_transports": requested,
            "applicable_transports": [leg["transport"] for leg in legs],
            "skipped_transports": skipped,
            "leg_count": len(legs),
            "legs": legs,
        })

    return {
        "schema": "kitsoki/persona-qa-transport-suite/v1",
        "status": "ready",
        "driver": driver_summary(driver_manifest),
        "transport_profiles": [
            compact_transport_profile(transport_profile(transport))
            for transport in TRANSPORT_IDS
        ],
        "summary": {
            "scenario_count": len(scenario_rows),
            "requested_transports": requested,
            "transport_count": len(requested),
            "leg_count": len(all_legs),
            "skipped_count": sum(skipped_total.values()),
            "skipped_by_transport": {
                transport: count
                for transport, count in skipped_total.items()
                if count
            },
        },
        "live_profile": live_profile,
        "live_authorization_summary": live_authorization_summary,
        "scenarios": scenario_rows,
        "legs": all_legs,
    }


def render_transport_suite(suite: dict) -> str:
    summary = suite.get("summary", {})
    lines = [
        "# Persona QA Transport Suite",
        "",
        f"- Scenarios: {summary.get('scenario_count', 0)}",
        f"- Requested transports: {', '.join(summary.get('requested_transports', []))}",
        f"- Planned transport checks: {summary.get('leg_count', 0)}",
        f"- Driver: {suite.get('driver', {}).get('id', '')}",
        f"- Live profile: {suite.get('live_profile', '') or '(not set)'}",
        "",
    ]
    live_authorization_summary = suite.get("live_authorization_summary", [])
    if live_authorization_summary:
        lines.extend(["## Live Authorization", ""])
        for note in live_authorization_summary:
            lines.append(f"- {note}")
        lines.append("")
    lines.extend([
        "## Transport Profiles",
        "",
    ])
    for profile in suite.get("transport_profiles", []):
        lines.append(
            f"- `{profile.get('id', '')}` ({profile.get('label', '')}): "
            f"{profile.get('level', '')} via {profile.get('primary_tool', '')} "
            f"-> `{profile.get('evidence_kind', '')}`"
        )
    lines.extend(["", "## Scenario Transport Checks", ""])
    if not suite.get("legs"):
        lines.append("No legs resolved for this scenario/transport selection.")
        return "\n".join(lines) + "\n"
    for scenario in suite.get("scenarios", []):
        lines.extend([
            f"### {scenario.get('scenario', '')}: {scenario.get('label', '')}",
            "",
            f"- Primary story: `{scenario.get('primary_story', '')}`",
            f"- Goal: {scenario.get('task', '') or '(not described)'}",
            f"- Allowed: {', '.join(scenario.get('allowed_transports', [])) or '(none)'}",
            f"- Applicable: {', '.join(scenario.get('applicable_transports', [])) or '(none)'}",
        ])
        if scenario.get("skipped_transports"):
            lines.append(f"- Skipped: {', '.join(scenario.get('skipped_transports', []))}")
        for leg in scenario.get("legs", []):
            contract = leg.get("evidence_contract", {})
            entrypoints = leg.get("entrypoints", {})
            lines.extend([
                "",
                f"#### {leg.get('leg_id', '')}",
                "",
                f"- Transport: `{leg.get('transport', '')}` / `{leg.get('visual_surface', '')}`",
                f"- Evidence: `{contract.get('evidence_kind', '')}` ({contract.get('level', '')}) via {contract.get('primary_tool', '')}",
                f"- Open: {', '.join(entrypoints.get('open', {}).get('capabilities', []))}",
                f"- Observe: {', '.join(entrypoints.get('observe', {}).get('capabilities', []))}",
                f"- Act: {', '.join(entrypoints.get('act', {}).get('capabilities', []))}",
                f"- Minimum evidence: {', '.join(leg.get('quality_gate', {}).get('minimum_evidence', []))}",
            ])
            if leg.get("live_authorization_note"):
                lines.append(f"- Live authorization: {leg['live_authorization_note']}")
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def build_execution_plan(run_json: dict, evidence: dict, transports: Optional[list[str]] = None, driver_manifest: Optional[dict] = None) -> dict:
    """Build the execution plan's ordered steps.

    Omitting `transports` (the default) preserves today's byte-compatible
    output: one step per scenario, no `transport`/`leg_id` keys. Passing a
    transport list expands each scenario into its scenario x transport legs
    (scenarios not allowed on a requested transport are skipped for it, not
    errored), each leg carrying its own required_mcp/evidence contract and a
    `transport_evidence_contract` describing how that transport's evidence is
    captured.
    """
    driver_manifest = driver_manifest or load_driver_manifest()
    evidence_by_scenario: dict[str, list[dict]] = {}
    for item in evidence.get("items", []):
        evidence_by_scenario.setdefault(item["scenario"], []).append(item)

    run_dir_arg = run_dir_cli_arg(run_json["run_id"])
    steps = []
    order = 0
    scenarios_with_legs = 0
    for scenario in run_json["scenarios"]:
        scenario_evidence_items = evidence_by_scenario.get(scenario["id"], [])
        if not transports:
            order += 1
            steps.append(_execution_plan_step(
                order, scenario, run_json, run_dir_arg,
                leg_evidence_view(scenario["evidence"], scenario_evidence_items),
                scenario["required_mcp"],
                driver_manifest,
            ))
            continue
        legs = scenario_transport_legs(scenario, transports)
        if legs:
            scenarios_with_legs += 1
        for leg in legs:
            order += 1
            steps.append(_execution_plan_step(
                order, scenario, run_json, run_dir_arg,
                leg_evidence_view(leg["evidence"], scenario_evidence_items),
                leg["required_mcp"],
                driver_manifest,
                leg=leg,
            ))

    summary = {
        "scenario_count": len(steps),
        "evidence_count": sum(len(step["evidence"]) for step in steps),
    }
    if transports:
        summary["transports"] = list(transports)
        summary["scenario_count"] = scenarios_with_legs
        summary["leg_count"] = len(steps)

    return {
        "run_id": run_json["run_id"],
        "driver": driver_summary(driver_manifest),
        "project": run_json["project"],
        "persona": run_json["persona"],
        "created_at": now_utc(),
        "summary": summary,
        "steps": steps,
        "finalize_commands": [
            f"python3 tools/product-journey/run.py --record-finding --run-dir {run_dir_arg} --finding-kind <strength|weakness|issue|fix> --title <title> --summary <summary>",
            f"python3 tools/product-journey/run.py --record-blocker --run-dir {run_dir_arg} --scenario <scenario> --title <title> --summary <summary>",
            *final_story_gate_commands(),
        ],
    }


def build_driver_plan(run_json: dict, evidence: dict, execution_plan: dict, transports: Optional[list[str]] = None, driver_manifest: Optional[dict] = None) -> dict:
    """Build the driver plan's per-scenario (or per-leg) dispatch contracts.

    Omitting `transports` preserves today's byte-compatible output: one entry
    per scenario, `visual_surface` inferred by driver_visual_surface(). Passing
    a transport list expands each scenario into its scenario x transport legs,
    with `visual_surface` pinned to the leg's transport instead of inferred,
    and required_mcp/evidence taken from that leg's contract.
    """
    lens = persona_lens(run_json["persona"])
    driver_manifest = driver_manifest or load_driver_manifest()
    evidence_by_scenario: dict[str, list[dict]] = {}
    for item in evidence.get("items", []):
        evidence_by_scenario.setdefault(item["scenario"], []).append(item)
    steps_by_leg = {
        (step["scenario"], step.get("transport")): step
        for step in execution_plan.get("steps", [])
    }
    run_dir_arg = run_dir_cli_arg(run_json["run_id"])
    scenarios = []
    for scenario in run_json["scenarios"]:
        scenario_id = scenario["id"]
        scenario_evidence_items = evidence_by_scenario.get(scenario_id, [])
        if not transports:
            required_mcp = scenario.get("required_mcp", [])
            step = steps_by_leg.get((scenario_id, None), {})
            scenarios.append(_driver_plan_entry(
                scenario, run_json, run_dir_arg, lens, step,
                required_mcp,
                _driver_plan_evidence_view(scenario.get("evidence", []), scenario_evidence_items),
                driver_action_scenario=scenario,
                driver_action_evidence=scenario_evidence_items,
                visual_surface=driver_visual_surface(scenario["primary_story"], required_mcp),
                driver_manifest=driver_manifest,
            ))
            continue
        for leg in scenario_transport_legs(scenario, transports):
            step = steps_by_leg.get((scenario_id, leg["transport"]), {})
            evidence_view = _driver_plan_evidence_view(leg["evidence"], scenario_evidence_items)
            scenarios.append(_driver_plan_entry(
                scenario, run_json, run_dir_arg, lens, step,
                leg["required_mcp"], evidence_view,
                driver_action_scenario=leg,
                driver_action_evidence=evidence_view,
                visual_surface=leg.get("visual_surface", leg["transport"]),
                driver_manifest=driver_manifest,
                leg=leg,
            ))
    return {
        "run_id": run_json["run_id"],
        "driver_agent": ".agents/agents/product-journey-qa-driver.md",
        "driver": driver_summary(driver_manifest),
        "project": run_json["project"],
        "persona": run_json["persona"],
        "scenarios": scenarios,
        "final_gates": [
            *final_story_gate_commands(),
        ],
    }


def proof_gap_rows(run_json: dict, evidence: dict) -> list[dict]:
    captured = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"}
    }
    proof = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if is_proof_evidence(item)
    }
    rows = []
    run_dir_arg = run_dir_cli_arg(run_json.get("run_id", "<run-id>"))
    driver_manifest = driver_manifest_for_run_json(run_json)
    for scenario in run_json.get("scenarios", []):
        scenario_id = scenario.get("id", "")
        required_mcp = scenario.get("required_mcp", [])
        visual_surface = driver_visual_surface(scenario.get("primary_story", ""), required_mcp)
        minimum = scenario_quality_gate(scenario.get("id", "")).get("minimum_evidence", [])
        captured_minimum = [
            kind for kind in minimum
            if (scenario_id, kind) in captured
        ]
        proof_minimum = [
            kind for kind in minimum
            if (scenario_id, kind) in proof
        ]
        missing = sorted(set(minimum) - set(proof_minimum))
        if missing:
            rows.append({
                "scenario": scenario_id,
                "label": scenario.get("label", scenario.get("id", "")),
                "proof_minimum_evidence_count": len(proof_minimum),
                "captured_minimum_evidence_count": len(captured_minimum),
                "minimum_evidence_count": len(minimum),
                "missing_proof_evidence": missing,
                "record_blocker_command": (
                    record_blocker_command(run_dir_arg, scenario_id)
                ),
                "slots": [
                    {
                        "kind": kind,
                        "capture_hint": evidence_capture_hint(kind),
                        "attach_command": attach_evidence_command(run_dir_arg, scenario_id, kind),
                        "capture_route": capture_route_for_slot(
                            scenario,
                            run_json,
                            run_dir_arg,
                            kind,
                            required_mcp,
                            visual_surface,
                            driver_manifest,
                        ),
                    }
                    for kind in missing
                ],
            })
    return rows


def build_driver_handoff(run_json: dict, metrics: dict, evidence: dict, review: dict) -> dict:
    run_dir_arg = run_dir_cli_arg(run_json["run_id"])
    missing_evidence = [
        {"scenario": item.get("scenario", ""), "kind": item.get("kind", ""), "hint": evidence_capture_hint(item.get("kind", ""))}
        for item in evidence.get("items", [])
        if item.get("status") == "missing"
    ]
    missing_proof_evidence = proof_gap_rows(run_json, evidence)
    minimum_evidence_count = sum(
        len(scenario_quality_gate(scenario.get("id", "")).get("minimum_evidence", []))
        for scenario in run_json.get("scenarios", [])
    )
    missing_proof_evidence_count = sum(len(row["missing_proof_evidence"]) for row in missing_proof_evidence)
    proof_minimum_evidence_count = minimum_evidence_count - missing_proof_evidence_count
    return {
        "run_id": run_json["run_id"],
        "created_at": now_utc(),
        "driver_agent": ".agents/agents/product-journey-qa-driver.md",
        "run_dir": run_dir_arg,
        "project": run_json["project"],
        "persona": run_json["persona"],
        "status": {
            "review_status": review.get("status", "not_reviewed"),
            "present_evidence_count": metrics.get("present_evidence_count", 0),
            "required_evidence_count": metrics.get("required_evidence_count", 0),
            "missing_evidence_count": len(missing_evidence),
            "proof_evidence_count": metrics.get("proof_evidence_count", 0),
            "proof_minimum_evidence_count": proof_minimum_evidence_count,
            "minimum_evidence_count": minimum_evidence_count,
            "missing_proof_evidence_count": missing_proof_evidence_count,
            "findings_count": metrics.get("findings_count", 0),
        },
        "inputs": {
            "agent_brief": "agent-brief.md",
            "driver_plan": "driver-plan.md",
            "driver_journal": "driver-journal.md",
            "execution_plan": "execution-plan.md",
            "evidence": "evidence.json",
            "scenario_outcomes": "scenario-outcomes.md",
            "media_manifest": "media-manifest.json",
        },
        "dispatch_modes": [
            {
                "mode": "replay",
                "description": "Use existing cassettes or deterministic fixtures. Safe for no-LLM regression runs.",
            },
            {
                "mode": "record",
                "description": "Capture a new reusable cassette or visual evidence path with explicit operator approval.",
            },
            {
                "mode": "live",
                "description": "Use live model behavior only when the operator explicitly authorizes cost-bearing exploration.",
            },
        ],
        "operator_warning": (
            "This handoff does not automatically launch an LLM. Use it as the reviewable contract for a "
            "live or cassette-backed driver pass, then attach evidence and run review + validation."
        ),
        "suggested_prompt": (
            f"Drive product journey QA for run_dir={run_dir_arg}. Open or attach "
            "stories/product-journey-qa/app.yaml, submit "
            f"`load run_dir={run_dir_arg}`, then inspect story world `last_result.driver_scenarios`, "
            "`last_result.next_driver_capture`, `last_result.next_driver_capture_route`, "
            "`last_result.next_driver_attach_command`, "
            "`last_result.next_driver_blocker_command`, `last_result.missing_proof_evidence`, "
            "and `last_result.driver_final_gates`. "
            "Respect each scenario's `live_budget` from driver-plan.json; when the live budget is exhausted, "
            "record the blocker, journal the attempt, and move to the next scenario. "
            "Use `last_result.next_driver_capture_route` as the deterministic setup/recording route for the first proof slot, "
            "`last_result.next_driver_attach_command` for the proof attach when present, "
            "or `last_result.next_driver_blocker_command` when the slot is attempted but blocked, "
            "then use only the route's stable entrypoints and resolved tools to capture proof-source evidence or blockers, "
            "record findings, run autonomous_watchdog, then run the autonomous issue-to-fix gate when credible issue findings exist. "
            "If the autonomous fix gate is not armed with ticket_repo and gh_agent_public_base_url, leave those "
            "parameters explicit for the operator instead of silently skipping the gate. Finish with review and validation."
        ),
        "finalize_commands": [
            *final_story_gate_commands(),
        ],
        "missing_evidence": missing_evidence,
        "missing_proof_evidence": missing_proof_evidence,
        "cli_fallback_finalize_commands": [
            autonomous_watchdog_cli_command(run_dir_arg),
            autonomous_fix_cli_command(run_dir_arg),
            f"python3 tools/product-journey/run.py --review-run --run-dir {run_dir_arg}",
            f"python3 tools/product-journey/run.py --validate-run --run-dir {run_dir_arg}",
        ],
    }


def validate_driver_manifest(manifest: dict) -> dict:
    issues: list[dict] = []
    path = manifest.get("_path", "")
    for key in ["id", "label", "app_kind", "capabilities"]:
        if not manifest.get(key):
            issues.append({"severity": "error", "id": "driver-required-key", "detail": f"{path}: missing {key}"})
    capabilities = manifest.get("_resolved_capabilities", {})
    missing = [capability for capability in CANONICAL_DRIVER_CAPABILITIES if not capabilities.get(capability)]
    if missing:
        issues.append({"severity": "error", "id": "driver-capabilities", "detail": ", ".join(missing)})
    unknown = sorted(set(capabilities) - set(CANONICAL_DRIVER_CAPABILITIES))
    if unknown:
        issues.append({"severity": "error", "id": "driver-capability-keys", "detail": ", ".join(unknown)})
    notes = manifest.get("notes")
    if notes is not None and (
        not isinstance(notes, list) or any(not isinstance(note, str) or not note.strip() for note in notes)
    ):
        issues.append({"severity": "error", "id": "driver-notes-shape", "detail": "notes must be an array of non-empty strings"})
    launch = manifest.get("launch")
    if launch is not None:
        if not isinstance(launch, dict):
            issues.append({"severity": "error", "id": "driver-launch-shape", "detail": "launch must be an object"})
        else:
            if "command" in launch and not isinstance(launch.get("command"), str):
                issues.append({"severity": "error", "id": "driver-launch-command", "detail": "launch.command must be a string"})
            if "cwd" in launch and not isinstance(launch.get("cwd"), str):
                issues.append({"severity": "error", "id": "driver-launch-cwd", "detail": "launch.cwd must be a string"})
            ready = launch.get("ready")
            if ready is not None:
                if not isinstance(ready, dict):
                    issues.append({"severity": "error", "id": "driver-launch-ready", "detail": "launch.ready must be an object"})
                elif ready.get("http") and not isinstance(ready.get("http"), str):
                    issues.append({"severity": "error", "id": "driver-launch-ready-http", "detail": "launch.ready.http must be a string"})
                elif "timeout_s" in ready and not isinstance(ready.get("timeout_s"), int):
                    issues.append({"severity": "error", "id": "driver-launch-ready-timeout", "detail": "launch.ready.timeout_s must be an integer"})
    for oracle in manifest.get("oracles", []):
        if not isinstance(oracle, dict):
            issues.append({"severity": "error", "id": "driver-oracle-shape", "detail": "oracle entries must be objects"})
            continue
        command = str(oracle.get("command", "")).strip()
        if not oracle.get("id") or not command:
            issues.append({"severity": "error", "id": "driver-oracle-required", "detail": "oracle entries require id and command"})
            continue
        parts = shlex.split(command)
        if not parts:
            issues.append({"severity": "error", "id": "driver-oracle-command", "detail": f"{oracle.get('id')}: empty command"})
            continue
        executable = Path(parts[0])
        if executable.parent != Path("."):
            candidate = executable if executable.is_absolute() else PROJECT_ROOT / executable
            if not candidate.exists():
                issues.append({"severity": "error", "id": "driver-oracle-exists", "detail": f"{oracle.get('id')}: {parts[0]} not found"})
            elif not os.access(candidate, os.X_OK):
                issues.append({"severity": "error", "id": "driver-oracle-executable", "detail": f"{oracle.get('id')}: {parts[0]} is not executable"})
    return {
        "status": "ok" if not any(issue["severity"] == "error" for issue in issues) else "error",
        "driver": driver_summary(manifest),
        "canonical_capabilities": CANONICAL_DRIVER_CAPABILITIES,
        "issues": issues,
    }


def run_dir_cli_arg(run_id: str) -> str:
    path = ARTIFACT_ROOT / run_id
    if os.environ.get("KITSOKI_SCENARIO_QA_WORKSPACE_ACTIVE") == "1":
        return str(path)
    for base in [PROJECT_ROOT, ROOT]:
        try:
            return path.relative_to(base).as_posix()
        except ValueError:
            continue
    return str(path)


def is_external_artifact_ref(path: str) -> bool:
    value = path.strip()
    if not value:
        return False
    prefixes = (
        "http://",
        "https://",
        "retained:",
        "retained://",
        "image:",
        "image://",
        "trace:",
        "trace://",
        "mcp:",
        "mcp://",
        "cassette:",
        "cassette://",
    )
    return value.startswith(prefixes)


def cassette_ref_relpath(value: str) -> Optional[str]:
    """Map a cassette:// evidence URI to the run-relative artifact path it
    claims to back. The minted form is
    `cassette://product-journey/<run_id>/<rel>` (see cassette_replay_path); the
    `<rel>` is a normal run-dir-relative path (e.g. `traces/foo.jsonl`). Returns
    None for a non-cassette value."""
    if value.startswith("cassette://"):
        rest = value[len("cassette://"):]
    elif value.startswith("cassette:"):
        rest = value[len("cassette:"):]
    else:
        return None
    parts = rest.split("/", 2)
    if len(parts) == 3 and parts[0] == "product-journey":
        return parts[2]
    return rest


def artifact_ref_exists(run_dir: Path, path: str) -> bool:
    value = path.strip()
    if not value:
        return True
    # A cassette:// ref is a LOCAL recorded artifact, not a remote URL — it must
    # resolve to a real backing file. Without this check an unbacked
    # `cassette://…/nothing.diff` was treated as existing proof, letting the
    # review gate read `ready` with nothing on disk.
    if value.startswith(("cassette://", "cassette:")):
        rel = cassette_ref_relpath(value)
        if not rel:
            return False
        candidate = Path(rel)
        if candidate.is_absolute():
            return candidate.exists()
        return (
            (run_dir / candidate).exists()
            or (run_dir / "cassettes" / candidate).exists()
            or (PROJECT_ROOT / candidate).exists()
            or (ROOT / candidate).exists()
        )
    if is_external_artifact_ref(value):
        # Genuinely remote/opaque schemes (http(s), retained, image, trace, mcp)
        # cannot be stat'd here; treat as existing.
        return True
    candidate = Path(value)
    if candidate.is_absolute():
        return candidate.exists()
    return (run_dir / candidate).exists() or (PROJECT_ROOT / candidate).exists() or (ROOT / candidate).exists()


def missing_local_artifact_refs(run_dir: Path, items: list[dict]) -> list[str]:
    missing = []
    for item in items:
        path = item.get("path", "")
        if item.get("status") not in {"captured", "validated"} or not path:
            continue
        if not artifact_ref_exists(run_dir, path):
            scenario = item.get("scenario", "")
            kind = item.get("kind", item.get("evidence_kind", "artifact"))
            missing.append(f"{scenario}/{kind}:{path}")
    return sorted(missing)


def build_weakness_routes(run_json: dict, findings: dict) -> dict:
    scenarios = {
        scenario.get("id", ""): scenario
        for scenario in run_json.get("scenarios", [])
    }
    rows = []
    for index, item in enumerate(open_weakness_findings(findings), start=1):
        scenario_id = item.get("scenario", "")
        scenario = scenarios.get(scenario_id, {})
        finding_id = item.get("id") or f"weakness-{index}"
        idea = " | ".join(part for part in [
            f"Persona QA weakness: {item.get('title', '').strip()}",
            item.get("summary", "").strip(),
            f"persona={run_json.get('persona', {}).get('id', '')}",
            f"scenario={scenario_id}",
            f"evidence={item.get('evidence_path', '')}",
        ] if part)
        rows.append({
            "finding_id": finding_id,
            "title": item.get("title", ""),
            "summary": item.get("summary", ""),
            "severity": item.get("severity", ""),
            "status": item.get("status", "open"),
            "scenario": scenario_id,
            "scenario_label": scenario.get("label", scenario_id),
            "project": run_json.get("project", {}).get("id", ""),
            "persona": run_json.get("persona", {}).get("id", ""),
            "evidence_path": item.get("evidence_path", ""),
            "target_pipeline": "prd-design",
            "target_story": "stories/prd",
            "route_reason": "Weakness findings are usability/product-shape input, not bugfix queue items.",
            "suggested_intent": "start",
            "suggested_idea": idea,
        })
    return {
        "run_id": run_json.get("run_id", ""),
        "created_at": now_utc(),
        "target_pipeline": "prd-design",
        "target_story": "stories/prd",
        "summary": {
            "open_weaknesses": len(rows),
            "routed": len(rows),
        },
        "items": rows,
    }


def build_prd_design_intake(run_json: dict, routes: dict) -> dict:
    lens = persona_lens(run_json.get("persona", {}))
    upstream_paths = [
        "weakness-routes.md",
        "scenario-outcomes.md",
        "driver-journal.md",
        "driver-plan.md",
        "deck.slidey.json",
    ]
    items = []
    for route in routes.get("items", []):
        evidence_path = str(route.get("evidence_path", "")).strip()
        item_upstream_paths = list(upstream_paths)
        if evidence_path:
            item_upstream_paths.append(evidence_path)
        intake_id = f"prd-intake-{route.get('finding_id', len(items) + 1)}"
        idea = str(route.get("suggested_idea", "")).strip()
        items.append({
            "intake_id": intake_id,
            "finding_id": route.get("finding_id", ""),
            "title": route.get("title", ""),
            "summary": route.get("summary", ""),
            "severity": route.get("severity", ""),
            "project": route.get("project", run_json.get("project", {}).get("id", "")),
            "scenario": route.get("scenario", ""),
            "scenario_label": route.get("scenario_label", route.get("scenario", "")),
            "persona": route.get("persona", run_json.get("persona", {}).get("id", "")),
            "persona_lens": lens,
            "target_pipeline": "prd-design",
            "target_story": "stories/prd",
            "story_intent": "start",
            "story_slots": {
                "idea": idea,
                "upstream_paths": " ".join(item_upstream_paths),
                "workdir": ".",
            },
            "handoff": (
                "Open stories/prd and submit start with the provided idea and upstream_paths. "
                "Keep the persona lens and evidence paths attached through the PRD brief."
            ),
            "evidence_path": evidence_path,
            "upstream_paths": item_upstream_paths,
        })
    return {
        "schema": "kitsoki/product-journey-prd-design-intake/v1",
        "run_id": run_json.get("run_id", ""),
        "created_at": now_utc(),
        "target_pipeline": "prd-design",
        "target_story": "stories/prd",
        "summary": {
            "intake_count": len(items),
            "open_weaknesses": routes.get("summary", {}).get("open_weaknesses", len(items)),
        },
        "items": items,
    }


def update_derived_artifacts(run_dir: Path, publish_deck: Optional[Path] = None) -> None:
    run_json = read_json(run_dir / "run.json")
    driver_manifest = driver_manifest_for_run_json(run_json)
    evidence = read_json(run_dir / "evidence.json")
    bugs = read_json(run_dir / "bugs.json")
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"run_id": run_json["run_id"], "items": []}
    driver_journal = read_json(run_dir / "driver-journal.json") if (run_dir / "driver-journal.json").exists() else build_driver_journal(run_json["run_id"], [])
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {
        "run_id": run_json["run_id"],
        "status": "not_reviewed",
        "summary": "Run has not been reviewed for readiness yet.",
        "checks": [],
    }

    evidence_items = evidence.get("items", [])
    for item in evidence_items:
        item["source"] = normalize_evidence_source(item.get("source", ""), item.get("path", ""), item.get("notes", ""))
    present_items = [item for item in evidence_items if item.get("status") in {"captured", "validated"}]
    demo_items = [item for item in present_items if item.get("source") == "demo"]
    proof_items = [item for item in present_items if is_proof_evidence(item, run_dir)]
    scenario_status: dict[str, str] = {}
    for scenario in run_json["scenarios"]:
        items = [item for item in evidence_items if item.get("scenario") == scenario["id"]]
        present = [item for item in items if item.get("status") in {"captured", "validated"}]
        validated = [item for item in items if item.get("status") == "validated"]
        if items and len(validated) == len(items):
            status = "validated"
        elif present:
            status = "captured"
        else:
            status = "planned"
        scenario["evidence_status"] = status
        scenario["status"] = status
        scenario_status[scenario["id"]] = status

    for stage in run_json["stages"]:
        statuses = [scenario_status.get(scenario_id, "planned") for scenario_id in stage.get("scenarios", [])]
        if statuses and all(status == "validated" for status in statuses):
            stage["status"] = "validated"
        elif any(status in {"captured", "validated"} for status in statuses):
            stage["status"] = "captured"

    evidence["summary"] = {
        "required": len(evidence_items),
        "present": len(present_items),
        "missing": len(evidence_items) - len(present_items),
    }
    finding_items = findings.get("items", [])
    finding_summary = {
        "strength": sum(1 for item in finding_items if item.get("kind") == "strength"),
        "weakness": sum(1 for item in finding_items if item.get("kind") == "weakness"),
        "issue": sum(1 for item in finding_items if item.get("kind") == "issue"),
        "fix": sum(1 for item in finding_items if item.get("kind") == "fix"),
        "blocked": sum(1 for item in finding_items if item.get("status") == "blocked"),
    }
    findings["summary"] = finding_summary
    weakness_routes = build_weakness_routes(run_json, findings)
    prd_design_intake = build_prd_design_intake(run_json, weakness_routes)
    metrics = {
        "run_id": run_json["run_id"],
        "stage_count": len(run_json["stages"]),
        "scenario_count": len(run_json["scenarios"]),
        "validated_stage_count": sum(1 for stage in run_json["stages"] if stage["status"] == "validated"),
        "captured_stage_count": sum(1 for stage in run_json["stages"] if stage["status"] == "captured"),
        "planned_stage_count": sum(1 for stage in run_json["stages"] if stage["status"] == "planned"),
        "required_evidence_count": evidence["summary"]["required"],
        "present_evidence_count": evidence["summary"]["present"],
        "missing_evidence_count": evidence["summary"]["missing"],
        "demo_evidence_count": len(demo_items),
        "proof_evidence_count": len(proof_items),
        "product_bugs_found": len(bugs.get("items", [])),
        "findings_count": len(finding_items),
        "strength_count": finding_summary["strength"],
        "weakness_count": finding_summary["weakness"],
        "issue_count": finding_summary["issue"],
        "fix_count": finding_summary["fix"],
        "blocked_count": finding_summary["blocked"],
        "driver_event_count": len(driver_journal.get("items", [])),
        "review_status": review.get("status", "not_reviewed"),
        "review_passed_checks": review.get("summary_counts", {}).get("passed", 0),
        "review_total_checks": review.get("summary_counts", {}).get("total", 0),
        "oracle_results": [],
        "checkpoint_ratings": [],
    }

    write_json(run_dir / "run.json", run_json)
    write_json(run_dir / "evidence.json", evidence)
    media_manifest = build_media_manifest(run_json, evidence)
    write_json(run_dir / "media-manifest.json", media_manifest)
    write_json(run_dir / "findings.json", findings)
    write_json(run_dir / "weakness-routes.json", weakness_routes)
    (run_dir / "weakness-routes.md").write_text(render_weakness_routes(weakness_routes), encoding="utf-8")
    write_json(run_dir / "prd-design-intake.json", prd_design_intake)
    (run_dir / "prd-design-intake.md").write_text(render_prd_design_intake(prd_design_intake), encoding="utf-8")
    scenario_outcomes = build_scenario_outcomes(run_json, evidence, findings)
    write_json(run_dir / "scenario-outcomes.json", scenario_outcomes)
    (run_dir / "scenario-outcomes.md").write_text(render_scenario_outcomes(scenario_outcomes), encoding="utf-8")
    write_json(run_dir / "review.json", review)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "scenarios.json", {"run_id": run_json["run_id"], "items": run_json["scenarios"]})
    transports = run_json.get("transports") or []
    execution_plan = build_execution_plan(run_json, evidence, transports, driver_manifest)
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    driver_plan = build_driver_plan(run_json, evidence, execution_plan, transports, driver_manifest)
    write_json(run_dir / "driver-plan.json", driver_plan)
    (run_dir / "driver-plan.md").write_text(render_driver_plan(driver_plan), encoding="utf-8")
    driver_journal = build_driver_journal(run_json["run_id"], driver_journal.get("items", []))
    write_json(run_dir / "driver-journal.json", driver_journal)
    (run_dir / "driver-journal.md").write_text(render_driver_journal(driver_journal), encoding="utf-8")
    agent_brief = build_agent_brief(run_json, evidence, execution_plan, driver_manifest)
    write_json(run_dir / "agent-brief.json", agent_brief)
    (run_dir / "agent-brief.md").write_text(render_agent_brief(agent_brief), encoding="utf-8")
    driver_handoff = build_driver_handoff(run_json, metrics, evidence, review)
    write_json(run_dir / "driver-handoff.json", driver_handoff)
    (run_dir / "driver-handoff.md").write_text(render_driver_handoff(driver_handoff), encoding="utf-8")
    (run_dir / "journey.md").write_text(render_journey(run_json), encoding="utf-8")
    deck = render_deck(run_json, metrics, evidence, findings, review, execution_plan, media_manifest, scenario_outcomes, driver_plan)
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)


def prepare_driver_handoff(run_dir: Path, publish_deck: Optional[Path] = None) -> dict:
    update_derived_artifacts(run_dir, publish_deck)
    handoff = read_json(run_dir / "driver-handoff.json")
    result = {
        "status": "driver_handoff_ready",
        "run_id": handoff["run_id"],
        "run_dir": str(run_dir),
        "driver_agent": handoff["driver_agent"],
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "driver_handoff_json_path": str(run_dir / "driver-handoff.json"),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "suggested_prompt": handoff["suggested_prompt"],
        "missing_evidence_count": handoff["status"]["missing_evidence_count"],
        "published_deck_path": str(publish_deck) if publish_deck is not None else "",
    }
    result.update(run_story_summary(run_dir))
    return result


def attach_evidence(
    run_dir: Path,
    scenario_id: str,
    evidence_kind: str,
    artifact_path: str,
    status: str,
    source: str,
    notes: str,
    publish_deck: Optional[Path],
) -> None:
    run_json = read_json(run_dir / "run.json")
    evidence = read_json(run_dir / "evidence.json")
    known_scenarios = {scenario["id"] for scenario in run_json["scenarios"]}
    if scenario_id not in known_scenarios:
        known = ", ".join(sorted(known_scenarios))
        raise SystemExit(f"Unknown scenario '{scenario_id}'. Known: {known}")
    if status not in {"captured", "validated", "rejected"}:
        raise SystemExit("Evidence status must be captured, validated, or rejected")

    target = None
    for item in evidence["items"]:
        if item.get("scenario") == scenario_id and item.get("kind") == evidence_kind:
            target = item
            break
    if target is None:
        known = sorted(item["kind"] for item in evidence["items"] if item.get("scenario") == scenario_id)
        raise SystemExit(f"Unknown evidence kind '{evidence_kind}' for {scenario_id}. Known: {', '.join(known)}")

    target["status"] = status
    target["path"] = artifact_path
    target["notes"] = notes
    target["source"] = normalize_evidence_source(source, artifact_path, notes)
    target["updated_at"] = now_utc()

    for scenario in run_json["scenarios"]:
        if scenario["id"] == scenario_id:
            scenario.setdefault("artifacts", {})[evidence_kind] = artifact_path
            break

    write_json(run_dir / "run.json", run_json)
    write_json(run_dir / "evidence.json", evidence)
    update_derived_artifacts(run_dir, publish_deck=publish_deck)


def record_finding(
    run_dir: Path,
    kind: str,
    title: str,
    summary: str,
    scenario_id: str,
    severity: str,
    evidence_path: str,
    status: str,
    publish_deck: Optional[Path],
    origin: str = "observed",
) -> None:
    if kind not in {"strength", "weakness", "issue", "fix"}:
        raise SystemExit("Finding kind must be strength, weakness, issue, or fix")
    if status not in {"open", "fixed", "observed", "validated", "blocked"}:
        raise SystemExit("Finding status must be open, fixed, observed, validated, or blocked")
    if origin not in {"observed", "seeded"}:
        raise SystemExit("Finding origin must be observed or seeded")
    run_json = read_json(run_dir / "run.json")
    known_scenarios = {scenario["id"] for scenario in run_json["scenarios"]}
    if scenario_id and scenario_id not in known_scenarios:
        known = ", ".join(sorted(known_scenarios))
        raise SystemExit(f"Unknown scenario '{scenario_id}'. Known: {known}")
    findings_path = run_dir / "findings.json"
    findings = read_json(findings_path) if findings_path.exists() else {"run_id": run_json["run_id"], "items": []}
    items = findings.setdefault("items", [])
    item = {
        "id": f"finding-{len(items) + 1}",
        "kind": kind,
        "title": title,
        "summary": summary,
        "scenario": scenario_id,
        "severity": severity,
        "evidence_path": evidence_path,
        "status": status,
        "origin": origin,
        "created_at": now_utc(),
    }
    items.append(item)
    write_json(findings_path, findings)
    update_derived_artifacts(run_dir, publish_deck=publish_deck)


def record_blocker(
    run_dir: Path,
    scenario_id: str,
    title: str,
    summary: str,
    evidence_path: str,
    publish_deck: Optional[Path],
) -> None:
    if not scenario_id:
        raise SystemExit("--record-blocker requires --scenario")
    record_finding(
        run_dir,
        "issue",
        title,
        summary,
        scenario_id,
        "high",
        evidence_path,
        "blocked",
        publish_deck,
    )


def credible_issue_driver_receipt_gaps(
    findings: dict,
    driver_journal: dict,
    evidence: dict,
    run_dir: Path,
) -> list[str]:
    proof_refs = {
        item.get("path", "")
        for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"}
        and item.get("path", "")
        and is_proof_evidence(item, run_dir)
    }
    valid_events_by_scenario: dict[str, list[dict]] = {}
    for event in driver_journal.get("items", []):
        scenario_id = event.get("scenario", "")
        if event.get("status") not in {"captured", "validated"}:
            continue
        if not scenario_id:
            continue
        valid_events_by_scenario.setdefault(scenario_id, []).append(event)

    gaps = []
    for item in credible_issue_findings(findings):
        finding_id = item.get("id", "finding")
        scenario_id = item.get("scenario", "")
        if not scenario_id:
            gaps.append(f"{finding_id}: missing scenario")
            continue
        events = valid_events_by_scenario.get(scenario_id, [])
        if not events:
            gaps.append(f"{finding_id}/{scenario_id}: missing captured-or-validated driver event")
            continue
        if not any(set(event.get("evidence_refs", [])) & proof_refs for event in events):
            gaps.append(f"{finding_id}/{scenario_id}: missing proof evidence refs")
    return gaps


def filed_issue_evidence_lines(findings: dict) -> list[str]:
    lines = []
    items = findings.get("items", []) if isinstance(findings, dict) else []
    for item in items:
        if not isinstance(item, dict):
            continue
        issue = item.get("github_issue", {}) if isinstance(item.get("github_issue", {}), dict) else {}
        issue_url = str(issue.get("url", "")).strip()
        if not issue_url:
            continue
        for asset in github_issue_evidence_assets(issue):
            lines.append(
                f"issue_evidence={item.get('id', item.get('title', 'finding'))}, "
                f"{asset['name']}={asset['url']}"
            )
    return lines


def enqueue_gh_agent_fixes(run_dir: Path, ticket_repo: str, db_path: str, story: str) -> dict:
    if not db_path:
        return {
            "gh_agent_enqueue_status": "disabled",
            "gh_agent_enqueued_count": 0,
            "gh_agent_skipped_count": 0,
            "gh_agent_job_summary": "",
            "gh_agent_jobs": [],
        }
    findings = read_json(run_dir / "findings.json")
    jobs = []
    claims = []
    skipped = 0
    for item in credible_issue_findings(findings):
        repo, number, kind, url = github_issue_ref(item, ticket_repo)
        if not repo or not number:
            skipped += 1
            continue
        cmd = kitsoki_cli_command() + [
            "gh-agent", "enqueue",
            "--db", db_path,
            "--repo", repo,
            "--issue", number,
            "--kind", kind,
            "--story", story or "stories/bugfix",
            "--json",
        ]
        proc = shell(cmd, ROOT)
        if proc.returncode != 0:
            detail = proc.stderr.strip() or proc.stdout.strip()
            raise SystemExit(f"kitsoki gh-agent enqueue failed (exit {proc.returncode}): {detail}")
        try:
            queued = json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            raise SystemExit(f"kitsoki gh-agent enqueue printed invalid JSON ({exc}): {proc.stdout[:400]}")
        queued["finding_id"] = item.get("id", "")
        queued["issue_url"] = url
        issue = item.setdefault("github_issue", {})
        claim_url = issue.get("claim_comment_url") or f"{url}#issuecomment-kitsoki-autofix-claim"
        issue["claim_comment_url"] = claim_url
        issue["claim_job_id"] = queued.get("job_id", "")
        issue["claimed_by"] = "kitsoki gitops autonomous-fix"
        issue["claimed_at"] = now_utc()
        comments = issue.setdefault("comments", [])
        if not any(isinstance(comment, dict) and comment.get("url") == claim_url for comment in comments):
            comments.append({
                "body": "<!-- kitsoki-autofix-claim -->",
                "url": claim_url,
            })
        queued["claim_url"] = claim_url
        jobs.append(queued)
        claims.append({
            "finding_id": item.get("id", ""),
            "issue_url": url,
            "repo": repo,
            "number": number,
            "job_id": queued.get("job_id", ""),
            "comment_url": claim_url,
        })
    if jobs:
        write_json(run_dir / "findings.json", findings)
    summary = "; ".join(job.get("origin_ref", "") for job in jobs[:5])
    if len(jobs) > 5:
        summary += f"; +{len(jobs) - 5} more"
    return {
        "gh_agent_enqueue_status": "queued",
        "gh_agent_enqueued_count": len(jobs),
        "gh_agent_skipped_count": skipped,
        "gh_agent_job_summary": summary,
        "gh_agent_jobs": jobs,
        "gh_agent_claim_status": "claimed",
        "gh_agent_claim_count": len(claims),
        "gh_agent_claims": claims,
    }


def drain_gh_agent_fixes(
    db_path: str,
    repo: str,
    public_base_url: str,
    project_root: str,
    incident_repo: str,
    asset_dir: str = "",
    comment_mode: str = "none",
) -> dict:
    if not db_path:
        return {
            "gh_agent_drain_status": "disabled",
            "gh_agent_drained_count": 0,
            "gh_agent_done_count": 0,
            "gh_agent_failed_count": 0,
            "gh_agent_active_count": 0,
            "gh_agent_run_summary": "",
            "gh_agent_drained_jobs": [],
        }
    cmd = kitsoki_cli_command() + [
        "gh-agent", "drain",
        "--db", db_path,
        "--repo", repo,
        "--json",
    ]
    if public_base_url:
        cmd += ["--public-base-url", public_base_url]
    if project_root:
        cmd += ["--project-root", project_root]
    if incident_repo:
        cmd += ["--incident-repo", incident_repo]
    if asset_dir:
        cmd += ["--asset-dir", asset_dir]
    if comment_mode:
        cmd += ["--comment-mode", comment_mode]
    proc = shell(cmd, ROOT)
    if proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip()
        raise SystemExit(f"kitsoki gh-agent drain failed (exit {proc.returncode}): {detail}")
    try:
        drained = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"kitsoki gh-agent drain printed invalid JSON ({exc}): {proc.stdout[:400]}")
    jobs = drained.get("jobs", [])
    summary_parts = []
    for job in jobs[:5]:
        origin = job.get("origin_ref", "")
        state = job.get("state", "")
        run_url = job.get("run_url", "")
        summary_parts.append(f"{origin}={state}{' ' + run_url if run_url else ''}".strip())
    if len(jobs) > 5:
        summary_parts.append(f"+{len(jobs) - 5} more")
    return {
        "gh_agent_drain_status": drained.get("status", "unknown"),
        "gh_agent_drained_count": int(drained.get("drained_count", 0)),
        "gh_agent_done_count": int(drained.get("done_count", 0)),
        "gh_agent_failed_count": int(drained.get("failed_count", 0)),
        "gh_agent_active_count": int(drained.get("active_count", 0)),
        "gh_agent_run_summary": "; ".join(summary_parts),
        "gh_agent_drained_jobs": jobs,
    }


def record_gh_agent_findings_status(run_dir: Path, status: dict) -> None:
    findings_path = run_dir / "findings.json"
    findings = read_json(findings_path)
    findings["gh_agent"] = {
        "updated_at": now_utc(),
        "public_base_url": status.get("gh_agent_public_base_url", ""),
        "enqueue_status": status.get("gh_agent_enqueue_status", "disabled"),
        "enqueued_count": status.get("gh_agent_enqueued_count", 0),
        "skipped_count": status.get("gh_agent_skipped_count", 0),
        "job_summary": status.get("gh_agent_job_summary", ""),
        "jobs": status.get("gh_agent_jobs", []),
        "claim_status": status.get("gh_agent_claim_status", ""),
        "claim_count": status.get("gh_agent_claim_count", 0),
        "claims": status.get("gh_agent_claims", []),
        "drain_status": status.get("gh_agent_drain_status", "disabled"),
        "drained_count": status.get("gh_agent_drained_count", 0),
        "done_count": status.get("gh_agent_done_count", 0),
        "failed_count": status.get("gh_agent_failed_count", 0),
        "active_count": status.get("gh_agent_active_count", 0),
        "run_summary": status.get("gh_agent_run_summary", ""),
        "drained_jobs": status.get("gh_agent_drained_jobs", []),
    }
    write_json(findings_path, findings)


def file_findings(
    run_dir: Path,
    ticket_repo: str,
    dry_run: bool,
    publish_deck: Optional[Path],
    gh_agent_db: str = "",
    gh_agent_story: str = "stories/bugfix",
    gh_agent_drain: bool = False,
    gh_agent_public_base_url: str = "",
    gh_agent_project_root: str = "",
    gh_agent_incident_repo: str = "",
    gh_agent_asset_dir: str = "",
    gh_agent_comment_mode: str = "none",
) -> dict:
    """File the bundle's credible issue findings as GitHub issues.

    Body assembly, evidence upload (release assets + `## Artifacts` section),
    dedupe, and the findings.json write-back all live in the Go orchestration
    (`kitsoki bug file-findings` -> host.GitHubFileFindings -> host.GitHubFileBug,
    the same path the web Report-bug and TUI /bug surfaces use). This runner
    entrypoint drives that CLI and refreshes the derived bundle artifacts so
    review/validate gates see the filing results.
    """
    if not ticket_repo:
        raise SystemExit("--file-findings requires --ticket-repo (owner/repo)")
    if not (run_dir / "findings.json").exists():
        raise SystemExit(f"No findings.json in {run_dir}; record findings before filing")
    cmd = kitsoki_cli_command() + [
        "bug", "file-findings",
        "--run-dir", str(run_dir),
        "--repo", ticket_repo,
    ]
    if dry_run:
        cmd.append("--dry-run")
    proc = shell(cmd, ROOT)
    if proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip()
        raise SystemExit(f"kitsoki bug file-findings failed (exit {proc.returncode}): {detail}")
    try:
        filing = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"kitsoki bug file-findings printed invalid JSON ({exc}): {proc.stdout[:400]}")

    findings = read_json(run_dir / "findings.json")
    credible = credible_issue_findings(findings)
    filed_urls = [
        item.get("github_issue", {}).get("url", "")
        for item in credible
        if item.get("github_issue", {}).get("url")
    ]
    unfiled = unfiled_credible_findings(findings)
    outcomes = filing.get("outcomes") or []
    filed = filing.get("filed", 0)
    skipped = filing.get("skipped", 0)
    failed = filing.get("failed", 0)
    if dry_run:
        summary = f"Dry-run rendered {len(outcomes)} candidate issue(s) for {ticket_repo}; nothing was filed."
    else:
        summary = (
            f"Filed findings to {ticket_repo}: {filed} filed, {skipped} already filed, "
            f"{failed} failed; {len(unfiled)} credible finding(s) remain unfiled."
        )
    issue_summary = "; ".join(filed_urls[:5])
    if len(filed_urls) > 5:
        issue_summary += f"; +{len(filed_urls) - 5} more"
    result = {
        "status": filing.get("status", "findings_dry_run" if dry_run else "findings_filed"),
        "run_dir": str(run_dir),
        "ticket_repo": ticket_repo,
        "gh_agent_public_base_url": gh_agent_public_base_url,
        "dry_run": "yes" if dry_run else "no",
        "findings_filed_count": filed,
        "findings_skipped_count": skipped,
        "findings_failed_count": failed,
        "findings_unfiled_count": len(unfiled),
        "filed_issue_urls": filed_urls,
        "filed_issue_summary": issue_summary,
        "filing_summary": summary,
        "outcomes": outcomes,
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
    }
    if not dry_run and gh_agent_db:
        result.update(enqueue_gh_agent_fixes(run_dir, ticket_repo, gh_agent_db, gh_agent_story))
        if gh_agent_drain:
            asset_dir = gh_agent_asset_dir or str(run_dir / "gh-agent-assets")
            result.update(drain_gh_agent_fixes(
                gh_agent_db,
                ticket_repo,
                gh_agent_public_base_url,
                gh_agent_project_root,
                gh_agent_incident_repo,
                asset_dir,
                gh_agent_comment_mode,
            ))
        else:
            result.update({
                "gh_agent_drain_status": "not_requested",
                "gh_agent_drained_count": 0,
                "gh_agent_done_count": 0,
                "gh_agent_failed_count": 0,
                "gh_agent_active_count": 0,
                "gh_agent_run_summary": "",
                "gh_agent_drained_jobs": [],
            })
        record_gh_agent_findings_status(run_dir, result)
    else:
        result.update({
            "gh_agent_enqueue_status": "disabled" if not gh_agent_db else "dry-run",
            "gh_agent_enqueued_count": 0,
            "gh_agent_skipped_count": 0,
            "gh_agent_job_summary": "",
            "gh_agent_jobs": [],
            "gh_agent_drain_status": "disabled" if not gh_agent_db else "dry-run",
            "gh_agent_drained_count": 0,
            "gh_agent_done_count": 0,
            "gh_agent_failed_count": 0,
            "gh_agent_active_count": 0,
            "gh_agent_run_summary": "",
            "gh_agent_drained_jobs": [],
        })
    if not dry_run:
        # The Go side rewrote findings.json (issue URLs + the filing block), and
        # the gh-agent handoff may have added fix-run lifecycle evidence. Refresh
        # derived artifacts after both writes so decks/metrics/handoff stay honest.
        if result.get("gh_agent_enqueue_status", "") not in {"", "disabled", "dry-run"}:
            result["autonomous_fix_report_path"] = str(write_autonomous_fix_report(run_dir, result))
        update_derived_artifacts(run_dir, publish_deck=publish_deck)
    result.update(run_story_summary(run_dir))
    return result


def file_local_findings(
    run_dir: Path,
    dry_run: bool,
    publish_deck: Optional[Path],
    target: str = "kitsoki",
    target_dir: str = "",
) -> dict:
    """File the bundle's credible issue findings as local `.artifacts/issues/bugs`
    tickets via `kitsoki bug create --sink local-artifact` (the repo-wide local
    stabilization sink documented in AGENTS.md). This is the default campaign
    finding sink: local developer/dogfood findings stay local unless a caller
    explicitly opts into the GitHub sink (`file_findings` / `autonomous_fix`).

    Findings already resolved via either sink (an existing `github_issue.url`
    or `local_ticket.path`) are skipped so re-runs are idempotent. Filing a
    finding locally does not require or touch gh-agent; there is no
    autonomous fix loop for the local sink by design (a human reviews the
    local ticket pile directly, e.g. `kitsoki bug list --sink local-artifact
    --target kitsoki`).
    """
    if not (run_dir / "findings.json").exists():
        raise SystemExit(f"No findings.json in {run_dir}; record findings before filing")
    findings = read_json(run_dir / "findings.json")
    credible = credible_issue_findings(findings)
    root_dir = target_dir or str(ROOT)
    outcomes: list[dict] = []
    filed = 0
    skipped = 0
    failed = 0
    for item in credible:
        existing_gh = item.get("github_issue", {}).get("url", "")
        existing_local = local_finding_ref(item).get("path", "")
        if existing_gh or existing_local:
            skipped += 1
            outcomes.append({
                "finding_id": item.get("id", ""),
                "status": "skipped",
                "local_ticket_path": existing_local,
                "github_issue_url": existing_gh,
            })
            continue
        title = str(item.get("title") or item.get("id") or "campaign finding").strip()
        body = local_finding_body(item, run_dir)
        if dry_run:
            outcomes.append({"finding_id": item.get("id", ""), "status": "dry-run", "title": title, "body": body})
            continue
        cmd = kitsoki_cli_command() + [
            "bug", "create",
            "--target", target,
            "--target-dir", root_dir,
            "--sink", "local-artifact",
            "--title", title,
            "--body", body,
        ]
        if item.get("severity"):
            cmd += ["--severity", str(item["severity"])]
        if item.get("evidence_path"):
            cmd += ["--trace-ref", str(item["evidence_path"])]
        proc = shell(cmd, ROOT)
        if proc.returncode != 0:
            failed += 1
            detail = (proc.stderr or proc.stdout).strip()[:300]
            outcomes.append({"finding_id": item.get("id", ""), "status": "failed", "error": detail})
            continue
        ticket_path = proc.stdout.strip().splitlines()[-1].strip() if proc.stdout.strip() else ""
        item["local_ticket"] = {
            "path": ticket_path,
            "sink": "local-artifact",
            "target": target,
            "target_dir": root_dir,
            "filed_at": now_utc(),
        }
        filed += 1
        outcomes.append({"finding_id": item.get("id", ""), "status": "filed", "local_ticket_path": ticket_path})

    if not dry_run:
        write_json(run_dir / "findings.json", findings)
        update_derived_artifacts(run_dir, publish_deck=publish_deck)

    unfiled = unfiled_credible_findings(findings)
    filed_paths = [o["local_ticket_path"] for o in outcomes if o.get("status") == "filed"]
    ticket_summary = "; ".join(filed_paths[:5])
    if len(filed_paths) > 5:
        ticket_summary += f"; +{len(filed_paths) - 5} more"
    if dry_run:
        summary = f"Dry-run rendered {len(outcomes)} candidate local ticket(s) for the .artifacts/issues/bugs sink; nothing was filed."
    else:
        summary = (
            f"Filed findings to the local-artifact sink: {filed} filed, {skipped} already filed, "
            f"{failed} failed; {len(unfiled)} credible finding(s) remain unfiled. "
            "Use finding_sink=github ticket_repo=owner/repo for an explicit GitHub handoff."
        )
    result = {
        "status": "findings_dry_run_local" if dry_run else "findings_filed_local",
        "run_dir": str(run_dir),
        "filing_sink": "local-artifact",
        "local_sink_target": target,
        "local_sink_target_dir": root_dir,
        "dry_run": "yes" if dry_run else "no",
        "findings_filed_count": filed,
        "findings_skipped_count": skipped,
        "findings_failed_count": failed,
        "findings_unfiled_count": len(unfiled),
        "filed_issue_summary": ticket_summary,
        "filing_summary": summary,
        "outcomes": outcomes,
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
    }
    result.update(run_story_summary(run_dir))
    return result


def autonomous_fix_loop(
    run_dir: Path,
    ticket_repo: str,
    gh_agent_db: str,
    gh_agent_story: str,
    gh_agent_public_base_url: str,
    gh_agent_project_root: str,
    gh_agent_incident_repo: str,
    gh_agent_asset_dir: str,
    gh_agent_comment_mode: str,
    publish_deck: Optional[Path],
) -> dict:
    """Run the autonomous product-journey issue-to-fix gate.

    This is the story-owned reliability envelope for the goal state: file
    credible findings with artifact-preserving GitHub issue creation, enqueue
    and drain gh-agent fixes, refresh the review artifact, then validate the
    bundle contract.
    """
    filed = file_findings(
        run_dir,
        ticket_repo,
        False,
        publish_deck,
        gh_agent_db,
        gh_agent_story,
        True,
        gh_agent_public_base_url,
        gh_agent_project_root,
        gh_agent_incident_repo,
        gh_agent_asset_dir,
        gh_agent_comment_mode,
    )
    reviewed = review_run_bundle(run_dir, publish_deck)
    validation = validate_run_bundle(run_dir)
    story_summary = run_story_summary(run_dir)
    review_failed = int(reviewed.get("review_failed_count", reviewed.get("failed", 0)) or 0)
    review_ok = reviewed.get("review_status", reviewed.get("status", "")) == "ready" and review_failed == 0
    validation_ok = validation.get("status") == "valid" and int(validation.get("errors", 0) or 0) == 0
    filed_issue_count = len(filed.get("filed_issue_urls", []) or [])
    filing_ok = (
        filed.get("status") == "findings_filed"
        and filed_issue_count > 0
        and int(filed.get("findings_failed_count", filed.get("failed", 0)) or 0) == 0
        and int(filed.get("findings_unfiled_count", 0) or 0) == 0
    )
    gh_agent_requested = filed.get("gh_agent_enqueue_status", "") not in {"", "disabled", "dry-run"}
    gh_agent_enqueued = int(filed.get("gh_agent_enqueued_count", 0) or 0)
    gh_agent_done = int(filed.get("gh_agent_done_count", 0) or 0)
    gh_agent_ok = (
        gh_agent_requested
        and gh_agent_enqueued > 0
        and filed.get("gh_agent_drain_status") == "drained"
        and int(filed.get("gh_agent_failed_count", 0) or 0) == 0
        and int(filed.get("gh_agent_active_count", 0) or 0) == 0
        and gh_agent_done >= gh_agent_enqueued
        and int(story_summary.get("gh_agent_missing_evidence_count", 0) or 0) == 0
        and int(story_summary.get("gh_agent_missing_triage_count", 0) or 0) == 0
        and int(story_summary.get("gh_agent_missing_run_url_count", 0) or 0) == 0
    )
    independent_verify_ok, independent_verify_summary = independent_verify_gate_from_summary(
        story_summary,
        gh_agent_requested,
        gh_agent_enqueued,
    )
    status = "autonomous_fix_valid" if review_ok and validation_ok and filing_ok and gh_agent_ok and independent_verify_ok else "autonomous_fix_invalid"
    result = {
        **filed,
        "status": status,
        "autonomous_fix_status": status,
        "independent_verify_status": "pass" if independent_verify_ok else "fail",
        "independent_verify_summary": independent_verify_summary,
        "autonomous_gate_summary": (
            f"filing={'pass' if filing_ok else 'fail'}, "
            f"gh_agent={'pass' if gh_agent_ok else 'fail'}, "
            f"independent_verify={'pass' if independent_verify_ok else 'fail'}, "
            f"review={'pass' if review_ok else 'fail'}, "
            f"validation={'pass' if validation_ok else 'fail'}"
        ),
        "filing_status": filed.get("status", ""),
        "review_status": reviewed.get("review_status", reviewed.get("status", "")),
        "review_summary": reviewed.get("summary", ""),
        "review_passed_count": reviewed.get("review_passed_count", reviewed.get("passed", 0)),
        "review_failed_count": reviewed.get("review_failed_count", reviewed.get("failed", 0)),
        "review_warning_count": reviewed.get("review_warning_count", reviewed.get("warnings", 0)),
        "review_total_count": reviewed.get("review_total_count", reviewed.get("total", 0)),
        "review_backlog_summary": reviewed.get("review_backlog_summary", ""),
        "validation_status": validation.get("status", ""),
        "validation_errors": validation.get("errors", 0),
        "validation_warnings": validation.get("warnings", 0),
        "validation_issue_summary": validation.get("validation_issue_summary", ""),
        "validation_issues": validation.get("issues", []),
    }
    result.update(story_summary)
    result["autonomous_fix_report_path"] = str(write_autonomous_fix_report(run_dir, result, reviewed, validation))
    return result


def gitops_autonomous_fix(
    run_dir: Path,
    ticket_repo: str,
    gh_agent_db: str,
    gh_agent_story: str,
    gh_agent_public_base_url: str,
    gh_agent_project_root: str,
    gh_agent_incident_repo: str,
    gh_agent_asset_dir: str,
    gh_agent_comment_mode: str,
) -> dict:
    if os.environ.get("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE") == "1":
        cmd = ["go", "run", "./cmd/kitsoki"]
    else:
        cmd = kitsoki_cli_command()
    cmd.extend([
        "gitops",
        "autonomous-fix",
        "--json",
        "--report-invalid-autonomous-fix",
        "--run-dir",
        str(run_dir),
        "--ticket-repo",
        ticket_repo,
        "--agent-db",
        gh_agent_db,
        "--agent-story",
        gh_agent_story or "stories/bugfix",
        "--public-base-url",
        gh_agent_public_base_url,
        "--project-root",
        gh_agent_project_root,
        "--incident-repo",
        gh_agent_incident_repo,
        "--asset-dir",
        gh_agent_asset_dir,
        "--comment-mode",
        gh_agent_comment_mode or "none",
    ])
    if os.environ.get("KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND") == "1":
        cmd.append("--allow-test-backend")
    proc = shell(cmd, ROOT)
    if proc.returncode != 0:
        raise SystemExit(
            "kitsoki gitops autonomous-fix failed\n"
            + proc.stdout.strip()
            + ("\n" if proc.stdout.strip() and proc.stderr.strip() else "")
            + proc.stderr.strip()
        )
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"kitsoki gitops autonomous-fix printed invalid JSON: {exc}\n{proc.stdout}") from exc


def legacy_autonomous_fix_loop_cli_allowed() -> bool:
    return (
        os.environ.get("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE") == "1"
        or os.environ.get("KITSOKI_PRODUCT_JOURNEY_ALLOW_LEGACY_AUTOFIX_LOOP") == "1"
    )


def write_autonomous_marathon_control(
    run_dir: Path,
    run_json: dict,
    driver_mode: str,
    cadence_hours: int,
    heartbeat_minutes: int,
    watchdog_minutes: int,
    gh_agent_public_base_url: str = "",
    ticket_repo: str = "",
    driver_live_profile: str = "",
) -> dict:
    if cadence_hours <= 0:
        raise SystemExit("--autonomous-cadence-hours must be > 0")
    if heartbeat_minutes <= 0:
        raise SystemExit("--autonomous-heartbeat-minutes must be > 0")
    if watchdog_minutes <= 0:
        raise SystemExit("--autonomous-watchdog-minutes must be > 0")
    if watchdog_minutes < heartbeat_minutes:
        raise SystemExit("--autonomous-watchdog-minutes must be >= --autonomous-heartbeat-minutes")
    created = datetime.datetime.fromisoformat(str(run_json.get("created_at", now_utc())))
    next_due = created + datetime.timedelta(hours=cadence_hours)
    scenarios = [scenario.get("id", "") for scenario in run_json.get("scenarios", []) if scenario.get("id")]
    control = {
        "schema": "kitsoki/product-journey-autonomous-marathon-control/v1",
        "run_id": run_json.get("run_id", ""),
        "status": "armed" if driver_mode == "replay" else "ready_for_driver",
        "driver_mode": driver_mode,
        "driver": {
            "live_profile": driver_live_profile.strip(),
        },
        "scenario_scope": scenarios,
        "cadence": {
            "hours": cadence_hours,
            "created_at": run_json.get("created_at", ""),
            "next_due_at": next_due.isoformat(timespec="seconds"),
        },
        "budget": {
            "per_scenario_live_minutes": int(run_json.get("live_budget_minutes", 0) or 0),
            "budget_setter": "operator",
            "manual_glue_steps_target": 0,
        },
        "watchdog": {
            "heartbeat_minutes": heartbeat_minutes,
            "watchdog_minutes": watchdog_minutes,
            "on_missed_heartbeat": "record_blocker_and_stop_before_spend",
        },
        "gh_agent": {
            "public_base_url": gh_agent_public_base_url.strip().rstrip("/"),
        },
        "gitops": {
            "ticket_repo": ticket_repo.strip(),
        },
        "human_role": "review outcomes and set budget; no issue filing, fix dispatch, close-out, or stats glue",
        "final_gates": [
            "autonomous_watchdog",
            "autonomous_fix",
            "review",
            "validate",
            "stats",
        ],
    }
    write_json(autonomous_marathon_control_path(run_dir), control)
    autonomous_marathon_control_markdown_path(run_dir).write_text(render_autonomous_marathon_control(control), encoding="utf-8")
    return control


def autonomous_marathon_due(root: Path, checked_at: str = "", limit: int = 10) -> dict:
    checked = parse_iso_datetime(checked_at) if checked_at else parse_iso_datetime(now_utc())
    controls = sorted(root.rglob("autonomous-marathon-control.json")) if root.exists() else []
    due: list[dict] = []
    upcoming: list[dict] = []
    blocked: list[dict] = []
    ignored: list[dict] = []
    for control_path in controls:
        run_dir = control_path.parent
        try:
            control = read_json(control_path)
        except (OSError, json.JSONDecodeError) as exc:
            blocked.append({
                "run_id": run_dir.name,
                "run_dir": str(run_dir),
                "control_path": str(control_path),
                "blocked_reason": f"cannot read control: {exc}",
            })
            continue
        item = autonomous_marathon_due_item(run_dir, control, checked)
        if item.get("blocked_reason"):
            blocked.append(item)
        elif item.get("ignored_reason"):
            ignored.append(item)
        elif item.get("next_command"):
            due.append(item)
        elif item.get("next_due_at"):
            upcoming.append(item)
        else:
            ignored.append(item)
    due.sort(key=lambda item: (item.get("minutes_overdue", 0) * -1, item.get("next_due_at", ""), item.get("run_id", "")))
    upcoming.sort(key=lambda item: (item.get("minutes_until_due", 0), item.get("next_due_at", ""), item.get("run_id", "")))
    blocked.sort(key=lambda item: (item.get("run_id", ""), item.get("blocked_reason", "")))
    limited_due = due[:max(0, limit)]
    limited_upcoming = upcoming[:max(0, limit)]
    limited_blocked = blocked[:max(0, limit)]
    next_due = limited_due[0] if limited_due else {}
    summary = (
        f"{len(due)} due, {len(upcoming)} upcoming, {len(blocked)} blocked, {len(ignored)} ignored"
    )
    if next_due:
        summary += f"; next {next_due.get('run_id', '')} overdue {next_due.get('minutes_overdue', 0)}m"
    return {
        "schema": "kitsoki/product-journey-autonomous-marathon-due/v1",
        "status": "autonomous_marathon_due_checked",
        "checked_at": checked.isoformat(timespec="seconds"),
        "root": str(root),
        "summary": summary,
        "due_count": len(due),
        "upcoming_count": len(upcoming),
        "blocked_count": len(blocked),
        "ignored_count": len(ignored),
        "total_count": len(controls),
        "due_runs": limited_due,
        "upcoming_runs": limited_upcoming,
        "blocked_runs": limited_blocked,
        "ignored_runs": ignored[:max(0, limit)],
        "next_due_run_id": next_due.get("run_id", ""),
        "next_due_run_dir": next_due.get("run_dir", ""),
        "next_due_command": next_due.get("next_command", ""),
        "next_due_story_intent": next_due.get("next_story_intent", ""),
        "next_due_minutes_overdue": next_due.get("minutes_overdue", 0),
    }


def autonomous_marathon_advance_due(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    root: Path,
    checked_at: str,
    limit: int,
    gh_agent_db: str,
    gh_agent_story: str,
    gh_agent_project_root: str,
    gh_agent_incident_repo: str,
    gh_agent_asset_dir: str,
    gh_agent_comment_mode: str,
    issue_state_file: str,
    stats_root: str,
    stats_output: str,
    similarity_threshold: float,
    similar_pair_limit: int,
    publish_deck: Optional[Path],
) -> dict:
    checked = parse_iso_datetime(checked_at) if checked_at else parse_iso_datetime(now_utc())
    due_scan = autonomous_marathon_due(root, checked.isoformat(timespec="seconds"), limit)
    base = {
        "autonomous_due_status": due_scan["status"],
        "autonomous_due_summary": due_scan["summary"],
        "autonomous_due_root": due_scan["root"],
        "autonomous_due_count": due_scan["due_count"],
        "autonomous_due_upcoming_count": due_scan["upcoming_count"],
        "autonomous_due_blocked_count": due_scan["blocked_count"],
        "autonomous_due_ignored_count": due_scan["ignored_count"],
        "autonomous_due_total_count": due_scan["total_count"],
        "autonomous_due_next_run_id": due_scan["next_due_run_id"],
        "autonomous_due_next_run_dir": due_scan["next_due_run_dir"],
        "autonomous_due_next_command": due_scan["next_due_command"],
        "autonomous_due_next_story_intent": due_scan["next_due_story_intent"],
        "autonomous_due_next_minutes_overdue": due_scan["next_due_minutes_overdue"],
    }
    if not due_scan.get("due_runs"):
        blocked = int(due_scan.get("blocked_count", 0) or 0)
        status = "autonomous_marathon_advance_blocked" if blocked else "autonomous_marathon_not_due"
        summary = (
            f"No due marathon advanced; blocked controls present: {due_scan['summary']}"
            if blocked
            else f"No due marathon advanced: {due_scan['summary']}"
        )
        return {
            **base,
            "status": status,
            "autonomous_due_advance_status": status,
            "autonomous_due_advance_summary": summary,
            "autonomous_marathon_status": status,
            "autonomous_marathon_summary": summary,
        }

    source_run_dir = run_dir_from_arg(due_scan["next_due_run_dir"])
    control = read_json(autonomous_marathon_control_path(source_run_dir))
    run_json = read_json(source_run_dir / "run.json")
    params = autonomous_marathon_due_params(run_json, control, checked)
    result = autonomous_marathon(
        catalog,
        github_targets,
        personas,
        scenarios,
        None,
        params["project"],
        params["persona"],
        params["seed"],
        params["scenarios"],
        params["live_budget_minutes"],
        params["ticket_repo"],
        gh_agent_db,
        gh_agent_story,
        params["gh_agent_public_base_url"],
        gh_agent_project_root,
        gh_agent_incident_repo,
        gh_agent_asset_dir,
        gh_agent_comment_mode,
        issue_state_file,
        stats_root,
        stats_output,
        similarity_threshold,
        similar_pair_limit,
        params["autonomous_driver_mode"],
        params["autonomous_cadence_hours"],
        params["autonomous_heartbeat_minutes"],
        params["autonomous_watchdog_minutes"],
        publish_deck,
        autonomous_driver_live_profile=params["autonomous_driver_live_profile"],
    )
    result.update(base)
    result["source_run_id"] = due_scan["next_due_run_id"]
    result["source_run_dir"] = due_scan["next_due_run_dir"]
    result["autonomous_due_advance_status"] = "autonomous_marathon_advanced"
    result["autonomous_due_advance_summary"] = (
        f"Advanced due marathon {due_scan['next_due_run_id']} into {result.get('run_id', '')}: "
        f"{result.get('autonomous_marathon_status', result.get('status', ''))}"
    )
    return result


def autonomous_marathon_watchdog(run_dir: Path, checked_at: str = "") -> dict:
    control_path = autonomous_marathon_control_path(run_dir)
    markdown_path = autonomous_marathon_watchdog_markdown_path(run_dir)
    checked = parse_iso_datetime(checked_at) if checked_at else parse_iso_datetime(now_utc())
    run_json = read_json(run_dir / "run.json") if (run_dir / "run.json").exists() else {}
    result = {
        "schema": "kitsoki/product-journey-autonomous-marathon-watchdog/v1",
        "status": "autonomous_watchdog_blocked",
        "autonomous_watchdog_status": "autonomous_watchdog_blocked",
        "run_id": run_json.get("run_id", run_dir.name),
        "run_dir": str(run_dir),
        "checked_at": checked.isoformat(timespec="seconds"),
        "autonomous_control_path": str(control_path) if control_path.exists() else "",
        "autonomous_watchdog_path": str(autonomous_marathon_watchdog_path(run_dir)),
        "autonomous_watchdog_markdown_path": str(markdown_path),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "latest_heartbeat_at": "",
        "heartbeat_age_minutes": 0,
        "heartbeat_minutes": 0,
        "watchdog_minutes": 0,
        "on_missed_heartbeat": "",
        "blocker_summary": "",
    }
    if not control_path.exists():
        result["autonomous_watchdog_summary"] = "missing autonomous-marathon-control.json; stop before spend"
        result["blocker_summary"] = "Missing autonomous-marathon-control.json, so the standing loop cannot prove its cadence, budget, or watchdog contract."
        write_json(autonomous_marathon_watchdog_path(run_dir), result)
        markdown_path.write_text(render_autonomous_marathon_watchdog(result), encoding="utf-8")
        return result

    control = read_json(control_path)
    watchdog = control.get("watchdog", {})
    heartbeat_minutes = int(watchdog.get("heartbeat_minutes", 0) or 0)
    watchdog_minutes = int(watchdog.get("watchdog_minutes", 0) or 0)
    result["heartbeat_minutes"] = heartbeat_minutes
    result["watchdog_minutes"] = watchdog_minutes
    result["on_missed_heartbeat"] = watchdog.get("on_missed_heartbeat", "")

    if watchdog_minutes <= 0:
        result["autonomous_watchdog_summary"] = "invalid watchdog interval; stop before spend"
        result["blocker_summary"] = "autonomous-marathon-control.json has no positive watchdog interval."
        write_json(autonomous_marathon_watchdog_path(run_dir), result)
        markdown_path.write_text(render_autonomous_marathon_watchdog(result), encoding="utf-8")
        return result

    latest = latest_driver_heartbeat(run_dir)
    if latest:
        baseline_at = parse_iso_datetime(str(latest.get("created_at", "")))
        result["latest_heartbeat_at"] = baseline_at.isoformat(timespec="seconds")
        baseline_label = f"latest driver event {latest.get('id', '')}".strip()
    else:
        created_at = (
            control.get("cadence", {}).get("created_at")
            or run_json.get("created_at")
            or checked.isoformat(timespec="seconds")
        )
        baseline_at = parse_iso_datetime(str(created_at))
        result["latest_heartbeat_at"] = ""
        baseline_label = "run creation"
    age_minutes = max(0, int((checked - baseline_at).total_seconds() // 60))
    result["heartbeat_age_minutes"] = age_minutes

    if age_minutes > watchdog_minutes:
        result["autonomous_watchdog_summary"] = (
            f"stale heartbeat: {age_minutes}m since {baseline_label}; "
            f"watchdog={watchdog_minutes}m; stop before spend"
        )
        result["blocker_summary"] = (
            f"Missed heartbeat after {age_minutes} minute(s). "
            f"Policy is {result['on_missed_heartbeat'] or 'record_blocker_and_stop_before_spend'}."
        )
    else:
        result["status"] = "autonomous_watchdog_ok"
        result["autonomous_watchdog_status"] = "autonomous_watchdog_ok"
        result["autonomous_watchdog_summary"] = (
            f"heartbeat age {age_minutes}m within watchdog={watchdog_minutes}m"
        )

    write_json(autonomous_marathon_watchdog_path(run_dir), result)
    markdown_path.write_text(render_autonomous_marathon_watchdog(result), encoding="utf-8")
    return result


def render_autonomous_driver_dispatch_receipt(receipt: dict) -> str:
    blockers = receipt.get("blockers", [])
    lines = [
        "# Autonomous Driver Dispatch",
        "",
        f"- Run: `{receipt.get('run_id', '')}`",
        f"- Mode: `{receipt.get('mode', '')}`",
        f"- Status: `{receipt.get('status', '')}`",
        f"- Summary: {receipt.get('summary', '')}",
        f"- Evidence count: {receipt.get('evidence_count', 0)}",
        f"- Issue count: {receipt.get('issue_count', 0)}",
        f"- Trace: `{receipt.get('trace', '') or '(not reported)'}`",
        f"- Recorded at: `{receipt.get('recorded_at', '')}`",
        "",
        "## Blockers",
        "",
    ]
    if blockers:
        lines.extend(f"- {blocker}" for blocker in blockers)
    else:
        lines.append("No blockers were reported by the autonomous driver task.")
    return "\n".join(lines) + "\n"


def record_autonomous_driver_dispatch(
    run_dir: Path,
    mode: str,
    status: str,
    summary: str,
    evidence_count: int,
    issue_count: int,
    trace: str,
    blockers: str,
) -> dict:
    run_json = read_json(run_dir / "run.json")
    if mode not in {"record", "live"}:
        raise SystemExit("--record-autonomous-driver-dispatch requires --dispatch-mode record or live")
    if status not in {"captured", "blocked", "degraded-evidence", "failed"}:
        raise SystemExit("--record-autonomous-driver-dispatch requires --dispatch-status captured, blocked, degraded-evidence, or failed")
    receipt = {
        "schema": "kitsoki/product-journey-autonomous-driver-dispatch/v1",
        "run_id": run_json.get("run_id", run_dir.name),
        "run_dir": str(run_dir),
        "mode": mode,
        "status": status,
        "summary": summary,
        "evidence_count": max(0, int(evidence_count or 0)),
        "issue_count": max(0, int(issue_count or 0)),
        "trace": trace,
        "blockers": split_csv(blockers),
        "recorded_at": now_utc(),
    }
    write_json(autonomous_driver_dispatch_path(run_dir), receipt)
    autonomous_driver_dispatch_markdown_path(run_dir).write_text(
        render_autonomous_driver_dispatch_receipt(receipt),
        encoding="utf-8",
    )
    return receipt


def record_campaign_worker_receipt(
    run_dir: Path,
    backend: str,
    worker_id: str,
    status: str,
    ready_status: str,
    ready_summary: str,
    budget_minutes: int,
    receipt_source: str,
    imported_artifacts: str,
    summary: str,
) -> dict:
    run_json = read_json(run_dir / "run.json")
    backend = (backend or "local").strip()
    if backend not in {"local", "arena", "vm"}:
        raise SystemExit("--worker-backend must be local, arena, or vm")
    status = (status or "ready").strip()
    if status not in {"ready", "blocked", "running", "completed", "failed"}:
        raise SystemExit("--worker-status must be ready, blocked, running, completed, or failed")
    ready_status = (ready_status or ("pass" if status in {"ready", "running", "completed"} else "fail")).strip()
    if ready_status not in {"pass", "warn", "fail"}:
        raise SystemExit("--worker-ready-status must be pass, warn, or fail")
    worker_id = (worker_id or f"{backend}-worker").strip()
    artifacts = split_csv(imported_artifacts)
    existing = [item for item in artifacts if item and Path(item).exists()]
    import_status = "imported" if artifacts and len(existing) == len(artifacts) else ("partial" if existing else "none")
    missing = [item for item in artifacts if item and item not in existing]
    receipt = {
        "schema": "kitsoki/product-journey-campaign-worker-receipt/v1",
        "run_id": run_json.get("run_id", run_dir.name),
        "run_dir": str(run_dir),
        "backend": backend,
        "worker_id": worker_id,
        "status": status,
        "ready_status": ready_status,
        "ready_summary": ready_summary or f"{backend} worker {worker_id} reported {ready_status}",
        "scenario_scope": [
            scenario.get("id", "")
            for scenario in run_json.get("scenarios", [])
            if isinstance(scenario, dict) and scenario.get("id")
        ],
        "budget_minutes": max(0, int(budget_minutes or run_json.get("live_budget_minutes", 0) or 0)),
        "receipt_source": receipt_source,
        "imported_artifacts": artifacts,
        "artifact_import_status": import_status,
        "artifact_import_summary": (
            f"imported {len(existing)}/{len(artifacts)} artifact(s)"
            if artifacts else
            "no artifacts supplied by worker receipt"
        ),
        "missing_artifacts": missing,
        "summary": summary or f"Campaign worker {worker_id} is {status} for {run_json.get('run_id', run_dir.name)}.",
        "recorded_at": now_utc(),
    }
    write_json(campaign_worker_receipt_path(run_dir), receipt)
    campaign_worker_receipt_markdown_path(run_dir).write_text(render_campaign_worker_receipt(receipt), encoding="utf-8")
    update_derived_artifacts(run_dir, publish_deck=None)
    return receipt


def blocked_autonomous_driver_dispatch(run_dir: Path) -> dict:
    control_path = autonomous_marathon_control_path(run_dir)
    if not control_path.exists():
        return {}
    control = read_json(control_path)
    mode = control.get("driver_mode", "")
    if mode not in {"record", "live"}:
        return {}
    receipt_path = autonomous_driver_dispatch_path(run_dir)
    if receipt_path.exists():
        receipt = read_json(receipt_path)
        status = receipt.get("status", "")
        if status == "captured":
            blockers = receipt.get("blockers", [])
            if blockers:
                summary = "autonomous driver dispatch reported captured status with blocker(s); stop before final gates"
                trace = receipt.get("trace", "")
                receipt_markdown = str(autonomous_driver_dispatch_markdown_path(run_dir))
                status = "captured-with-blockers"
                return {
                    "autonomous_driver_mode": mode,
                    "autonomous_driver_status": status,
                    "autonomous_driver_summary": summary,
                    "autonomous_driver_dispatch_status": receipt.get("status", status),
                    "autonomous_driver_dispatch_summary": receipt.get("summary", summary),
                    "autonomous_driver_dispatch_trace": trace,
                    "autonomous_driver_dispatch_path": str(receipt_path),
                    "autonomous_driver_dispatch_markdown_path": receipt_markdown,
                }
            heartbeat = latest_driver_heartbeat(run_dir)
            if heartbeat:
                findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"items": []}
                evidence = read_json(run_dir / "evidence.json") if (run_dir / "evidence.json").exists() else {"items": []}
                claimed_evidence = max(0, int(receipt.get("evidence_count", 0) or 0))
                claimed_issues = max(0, int(receipt.get("issue_count", 0) or 0))
                actual_evidence = sum(
                    1 for item in evidence.get("items", [])
                    if is_proof_evidence(item, run_dir)
                )
                actual_issues = len(credible_issue_findings(findings))
                count_gaps = []
                if claimed_evidence > actual_evidence:
                    count_gaps.append(f"evidence={actual_evidence}/{claimed_evidence}")
                if claimed_issues > actual_issues:
                    count_gaps.append(f"issues={actual_issues}/{claimed_issues}")
                trace = receipt.get("trace", "")
                receipt_markdown = str(autonomous_driver_dispatch_markdown_path(run_dir))
                if not count_gaps:
                    if trace and artifact_ref_exists(run_dir, trace):
                        return {}
                    status = "missing-trace"
                    summary = "autonomous driver dispatch captured no reviewable driver trace; stop before final gates"
                    return {
                        "autonomous_driver_mode": mode,
                        "autonomous_driver_status": status,
                        "autonomous_driver_summary": summary,
                        "autonomous_driver_dispatch_status": receipt.get("status", status),
                        "autonomous_driver_dispatch_summary": receipt.get("summary", summary),
                        "autonomous_driver_dispatch_trace": trace,
                        "autonomous_driver_dispatch_path": str(receipt_path),
                        "autonomous_driver_dispatch_markdown_path": receipt_markdown,
                    }
                summary = "autonomous driver dispatch receipt claims artifacts that are not persisted: " + ", ".join(count_gaps)
                status = "inconsistent-counts"
                return {
                    "autonomous_driver_mode": mode,
                    "autonomous_driver_status": status,
                    "autonomous_driver_summary": summary,
                    "autonomous_driver_dispatch_status": receipt.get("status", status),
                    "autonomous_driver_dispatch_summary": receipt.get("summary", summary),
                    "autonomous_driver_dispatch_trace": trace,
                    "autonomous_driver_dispatch_path": str(receipt_path),
                    "autonomous_driver_dispatch_markdown_path": receipt_markdown,
                }
            summary = "autonomous driver dispatch captured no driver-journal heartbeat; stop before final gates"
            trace = receipt.get("trace", "")
            receipt_markdown = str(autonomous_driver_dispatch_markdown_path(run_dir))
            status = "missing-heartbeat"
            return {
                "autonomous_driver_mode": mode,
                "autonomous_driver_status": status,
                "autonomous_driver_summary": summary,
                "autonomous_driver_dispatch_status": receipt.get("status", status),
                "autonomous_driver_dispatch_summary": receipt.get("summary", summary),
                "autonomous_driver_dispatch_trace": trace,
                "autonomous_driver_dispatch_path": str(receipt_path),
                "autonomous_driver_dispatch_markdown_path": receipt_markdown,
            }
        summary = receipt.get("summary", "") or f"autonomous driver dispatch ended with status={status or 'unknown'}"
        trace = receipt.get("trace", "")
        receipt_markdown = str(autonomous_driver_dispatch_markdown_path(run_dir))
        if status in {"blocked", "degraded-evidence", "failed"} and not receipt.get("blockers", []):
            summary = (
                f"autonomous driver dispatch ended with status={status} "
                "without a structured blocker; stop before final gates"
            )
            status = "missing-blocker"
    else:
        status = "missing"
        summary = "autonomous driver dispatch receipt is missing; stop before final gates"
        trace = ""
        receipt_markdown = ""
    return {
        "autonomous_driver_mode": mode,
        "autonomous_driver_status": status,
        "autonomous_driver_summary": summary,
        "autonomous_driver_dispatch_status": status,
        "autonomous_driver_dispatch_summary": summary,
        "autonomous_driver_dispatch_trace": trace,
        "autonomous_driver_dispatch_path": str(receipt_path) if receipt_path.exists() else "",
        "autonomous_driver_dispatch_markdown_path": receipt_markdown,
    }


def attach_autonomous_marathon_replay_driver(run_dir: Path, run_json: dict, publish_deck: Optional[Path]) -> dict:
    """Attach deterministic replay proof to a marathon run without live LLM work."""
    attached: list[dict] = []
    issue_count = 0
    evidence_dir = run_dir / "autonomous-replay-evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    for scenario in run_json.get("scenarios", []):
        scenario_id = scenario.get("id", "")
        if not scenario_id:
            continue
        kinds = list(scenario_minimum_evidence(scenario_id))
        playback_kind = scenario_playback_kind(scenario)
        if playback_kind and playback_kind not in kinds:
            kinds.append(playback_kind)
        if not kinds:
            raise SystemExit(f"Autonomous replay driver cannot capture '{scenario_id}': no evidence contract")
        refs: list[str] = []
        for kind in kinds:
            suffix = ".mp4" if kind in {"key_interaction_video", "rrweb", "trace-replay", "flow-fixture", "png-sequence"} else ".md"
            artifact = evidence_dir / f"{scenario_id}-{kind}{suffix}"
            artifact.write_text(
                f"autonomous marathon replay proof\nscenario: {scenario_id}\nkind: {kind}\nrun: {run_json.get('run_id', '')}\n",
                encoding="utf-8",
            )
            attach_evidence(
                run_dir,
                scenario_id,
                kind,
                str(artifact),
                "validated",
                "cassette",
                f"Autonomous marathon replay proof for {scenario_id}/{kind}",
                publish_deck=publish_deck,
            )
            refs.append(str(artifact))
            attached.append({"scenario": scenario_id, "kind": kind, "path": str(artifact), "source": "cassette"})
        record_driver_event(
            run_dir,
            scenario_id,
            "replay",
            "captured",
            f"Story-owned autonomous marathon replay captured every required proof artifact for {scenario_id}.",
            "session.open,session.trace,render.tui,visual.observe",
            ",".join(refs),
            "",
            publish_deck=publish_deck,
        )
        record_finding(
            run_dir,
            "strength",
            f"Autonomous replay captured {scenario_id} proof",
            f"The story-owned replay driver attached every minimum-evidence artifact for {scenario_id} without operator glue.",
            scenario_id,
            "low",
            refs[-1],
            "observed",
            publish_deck=publish_deck,
        )
        record_finding(
            run_dir,
            "issue",
            f"Autonomous marathon should fix {scenario_id} persona QA issues",
            "A credible persona-QA issue should be filed with evidence, fixed by the gh-agent pipeline, independently verified, closed, and counted mechanically.",
            scenario_id,
            "high",
            refs[0],
            "open",
            publish_deck=publish_deck,
        )
        issue_count += 1
    first_ref = attached[0]["path"] if attached else str(run_dir / "driver-handoff.md")
    first_scenario = attached[0]["scenario"] if attached else ""
    if first_scenario:
        record_finding(
            run_dir,
            "weakness",
            "Autonomous marathon replay should graduate to live cadence",
            "Replay mode proves the no-operator loop deterministically; a budgeted live cadence should use the same story-owned final gates.",
            first_scenario,
            "medium",
            first_ref,
            "open",
            publish_deck=publish_deck,
        )
    update_derived_artifacts(run_dir, publish_deck=publish_deck)
    return {
        "autonomous_driver_mode": "replay",
        "autonomous_driver_status": "captured",
        "autonomous_driver_summary": f"Replay captured {len(attached)} artifact(s) across {len(run_json.get('scenarios', []))} scenario(s); recorded {issue_count} issue finding(s).",
        "autonomous_driver_evidence_count": len(attached),
        "autonomous_driver_issue_count": issue_count,
    }


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def autonomous_marathon(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    run_dir: Optional[Path],
    project_id: str,
    persona_id: str,
    seed: str,
    scenario_filter: str,
    live_budget_minutes: int,
    ticket_repo: str,
    gh_agent_db: str,
    gh_agent_story: str,
    gh_agent_public_base_url: str,
    gh_agent_project_root: str,
    gh_agent_incident_repo: str,
    gh_agent_asset_dir: str,
    gh_agent_comment_mode: str,
    issue_state_file: str,
    stats_root: str,
    stats_output: str,
    similarity_threshold: float,
    similar_pair_limit: int,
    autonomous_driver_mode: str,
    autonomous_cadence_hours: int,
    autonomous_heartbeat_minutes: int,
    autonomous_watchdog_minutes: int,
    publish_deck: Optional[Path],
    watchdog_checked_at: str = "",
    autonomous_driver_live_profile: str = "",
) -> dict:
    """Create or finalize a standing persona-QA marathon bundle.

    Creation is deterministic. Replay attaches cassette proof immediately;
    live/record return a ready bundle for the product-journey story to dispatch
    through host.agent.task before re-entering this finalizer. Finalization is
    story-owned: credible issue findings go through the native autonomous fix
    gate, review/validate are refreshed, weaknesses route to PRD/design, and
    stats derive from cached issue state.
    """
    if run_dir is None:
        autonomous_driver_live_profile = (autonomous_driver_live_profile or "").strip()
        run_scenarios = select_scenarios(scenarios, scenario_filter)
        created_dir, run_json = build_run_bundle(
            catalog,
            github_targets,
            personas,
            run_scenarios,
            project_id,
            persona_id,
            seed,
            "autonomous-marathon",
            publish_deck,
            live_budget_minutes,
            [],
        )
        if autonomous_driver_mode not in {"pending", "replay", "record", "live"}:
            return invalid_autonomous_marathon_creation(
                created_dir,
                run_json,
                f"unsupported autonomous_driver_mode={autonomous_driver_mode}",
                "unsupported-driver-mode",
                "driver=fail, filing=not_run, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=fail",
                ticket_repo,
                gh_agent_public_base_url,
                autonomous_driver_mode=autonomous_driver_mode,
                autonomous_driver_live_profile=autonomous_driver_live_profile,
            )
        if autonomous_driver_mode in {"record", "live"} and live_budget_minutes <= 0:
            return invalid_autonomous_marathon_creation(
                created_dir,
                run_json,
                f"{autonomous_driver_mode} autonomous marathon requires live_budget_minutes > 0 before driver dispatch.",
                "live-budget-required",
                "driver=fail, live_auth=not_run, filing=not_run, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=fail",
                ticket_repo,
                gh_agent_public_base_url,
                autonomous_driver_mode=autonomous_driver_mode,
                autonomous_driver_live_profile=autonomous_driver_live_profile,
            )
        if autonomous_driver_mode in {"record", "live"} and not autonomous_driver_live_profile:
            return invalid_autonomous_marathon_creation(
                created_dir,
                run_json,
                f"{autonomous_driver_mode} autonomous marathon requires autonomous_driver_live_profile so replay misses cannot silently fall through to an ambient live backend.",
                "live-profile-required",
                "driver=fail, live_auth=fail, filing=not_run, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=fail",
                ticket_repo,
                gh_agent_public_base_url,
                autonomous_driver_mode=autonomous_driver_mode,
                autonomous_driver_live_profile=autonomous_driver_live_profile,
            )
        health: Optional[dict] = None
        ready: Optional[dict] = None
        if autonomous_driver_mode != "replay" and live_budget_minutes > 0:
            if not ticket_repo:
                return invalid_autonomous_marathon_creation(
                    created_dir,
                    run_json,
                    "Live-budgeted autonomous marathon requires ticket_repo before driver handoff.",
                    "ticket-repo-required",
                    "preflight=pass, filing=fail, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=fail",
                    ticket_repo,
                    gh_agent_public_base_url,
                    autonomous_driver_mode=autonomous_driver_mode,
                    autonomous_driver_live_profile=autonomous_driver_live_profile,
                )
            if not gh_agent_public_base_url:
                return invalid_autonomous_marathon_creation(
                    created_dir,
                    run_json,
                    "Live-budgeted autonomous marathon requires gh_agent_public_base_url before driver handoff.",
                    "gh-agent-public-base-url",
                    "preflight=pass, filing=not_run, gh_agent=fail, independent_verify=not_run, review=not_run, validation=fail",
                    ticket_repo,
                    gh_agent_public_base_url,
                    autonomous_driver_mode=autonomous_driver_mode,
                    autonomous_driver_live_profile=autonomous_driver_live_profile,
                )
            health = check_gh_agent_health(gh_agent_public_base_url)
            if health.get("status") != "pass":
                return invalid_autonomous_marathon_creation(
                    created_dir,
                    run_json,
                    f"Hosted gh-agent health check failed before driver handoff: {health.get('summary', '')}",
                    "gh-agent-health",
                    "preflight=pass, filing=not_run, gh_agent=fail, independent_verify=not_run, review=not_run, validation=fail",
                    ticket_repo,
                    gh_agent_public_base_url,
                    gh_agent_health=health,
                    autonomous_driver_mode=autonomous_driver_mode,
                    autonomous_driver_live_profile=autonomous_driver_live_profile,
                )
            ready = check_gh_agent_readiness(gh_agent_public_base_url, ticket_repo)
            if ready.get("status") != "pass":
                return invalid_autonomous_marathon_creation(
                    created_dir,
                    run_json,
                    f"Hosted gh-agent readiness check failed before driver handoff: {ready.get('summary', '')}",
                    "gh-agent-readiness",
                    "preflight=pass, filing=not_run, gh_agent=fail, independent_verify=not_run, review=not_run, validation=fail",
                    ticket_repo,
                    gh_agent_public_base_url,
                    gh_agent_health=health,
                    gh_agent_readiness=ready,
                    autonomous_driver_mode=autonomous_driver_mode,
                    autonomous_driver_live_profile=autonomous_driver_live_profile,
                )
        control = write_autonomous_marathon_control(
            created_dir,
            run_json,
            autonomous_driver_mode,
            autonomous_cadence_hours,
            autonomous_heartbeat_minutes,
            autonomous_watchdog_minutes,
            gh_agent_public_base_url,
            ticket_repo,
            autonomous_driver_live_profile,
        )
        if autonomous_driver_mode == "replay":
            if not ticket_repo:
                raise SystemExit("--autonomous-marathon --autonomous-driver-mode replay requires --ticket-repo")
            if not gh_agent_public_base_url:
                raise SystemExit("--autonomous-marathon --autonomous-driver-mode replay requires --gh-agent-public-base-url")
            driver_result = attach_autonomous_marathon_replay_driver(created_dir, run_json, publish_deck)
            finalized = autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                created_dir,
                project_id,
                persona_id,
                seed,
                scenario_filter,
                live_budget_minutes,
                ticket_repo,
                gh_agent_db or str(created_dir / "gh-agent-jobs.sqlite"),
                gh_agent_story,
                gh_agent_public_base_url,
                gh_agent_project_root,
                gh_agent_incident_repo,
                gh_agent_asset_dir,
                gh_agent_comment_mode,
                issue_state_file,
                stats_root,
                stats_output,
                similarity_threshold,
                similar_pair_limit,
                "pending",
                autonomous_cadence_hours,
                autonomous_heartbeat_minutes,
                autonomous_watchdog_minutes,
                publish_deck,
                watchdog_checked_at,
            )
            finalized.update(driver_result)
            finalized["autonomous_control_path"] = str(autonomous_marathon_control_path(created_dir))
            finalized["autonomous_control_markdown_path"] = str(autonomous_marathon_control_markdown_path(created_dir))
            finalized["autonomous_control_status"] = control.get("status", "")
            finalized["autonomous_control_summary"] = autonomous_marathon_control_summary(control)
            finalized["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(created_dir, finalized))
            return finalized
        result = {
            "status": "autonomous_marathon_ready_for_driver",
            "autonomous_marathon_status": "autonomous_marathon_ready_for_driver",
            "autonomous_marathon_summary": (
                f"Created {len(run_json.get('scenarios', []))} scenario(s); driver evidence capture is "
                + ("ready for story-owned dispatch." if autonomous_driver_mode in {"record", "live"} else "pending.")
            ),
            "autonomous_driver_mode": autonomous_driver_mode,
            "autonomous_driver_live_profile": autonomous_driver_live_profile,
            "autonomous_driver_status": "ready_for_dispatch" if autonomous_driver_mode in {"record", "live"} else "pending",
            "autonomous_driver_summary": (
                f"Story-owned {autonomous_driver_mode} driver dispatch is ready."
                if autonomous_driver_mode in {"record", "live"}
                else "Driver evidence capture is pending."
            ),
            "autonomous_driver_evidence_count": 0,
            "autonomous_driver_issue_count": 0,
            "run_id": run_json["run_id"],
            "run_dir": str(created_dir),
            "project": run_json["project"]["id"],
            "persona": run_json["persona"]["id"],
            "seed": run_json.get("seed", ""),
            "deck_path": str(created_dir / "deck.slidey.json"),
            "execution_plan_path": str(created_dir / "execution-plan.md"),
            "driver_plan_path": str(created_dir / "driver-plan.md"),
            "driver_journal_path": str(created_dir / "driver-journal.md"),
            "agent_brief_path": str(created_dir / "agent-brief.md"),
            "driver_handoff_path": str(created_dir / "driver-handoff.md"),
            "media_manifest_path": str(created_dir / "media-manifest.json"),
            "scenario_outcomes_path": str(created_dir / "scenario-outcomes.md"),
            "autonomous_fix_status": "not_run",
            "independent_verify_status": "not_run",
            "independent_verify_summary": "independent verification not run",
            "autonomous_gate_summary": (
                "driver=ready, filing=not_run, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=not_run"
                if autonomous_driver_mode in {"record", "live"}
                else "filing=not_run, gh_agent=not_run, independent_verify=not_run, review=not_run, validation=not_run"
            ),
            "autonomous_control_path": str(autonomous_marathon_control_path(created_dir)),
            "autonomous_control_markdown_path": str(autonomous_marathon_control_markdown_path(created_dir)),
            "autonomous_control_status": control.get("status", ""),
            "autonomous_control_summary": autonomous_marathon_control_summary(control),
            "gh_agent_health_status": (health or {}).get("status", ""),
            "gh_agent_health_summary": (health or {}).get("summary", ""),
            "gh_agent_readiness_status": (ready or {}).get("status", ""),
            "gh_agent_readiness_summary": (ready or {}).get("summary", ""),
        }
        result.update(run_story_summary(created_dir))
        result["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(created_dir, result))
        return result

    update_derived_artifacts(run_dir, publish_deck=None)
    driver_dispatch_block = blocked_autonomous_driver_dispatch(run_dir)
    if driver_dispatch_block:
        summary = driver_dispatch_block.get("autonomous_driver_summary", "")
        result = {
            **run_story_summary(run_dir),
            **driver_dispatch_block,
            "status": "autonomous_marathon_invalid",
            "autonomous_marathon_status": "autonomous_marathon_invalid",
            "autonomous_marathon_summary": f"driver=fail: {summary}",
            "run_dir": str(run_dir),
            "deck_path": str(run_dir / "deck.slidey.json"),
            "execution_plan_path": str(run_dir / "execution-plan.md"),
            "driver_plan_path": str(run_dir / "driver-plan.md"),
            "driver_journal_path": str(run_dir / "driver-journal.md"),
            "agent_brief_path": str(run_dir / "agent-brief.md"),
            "driver_handoff_path": str(run_dir / "driver-handoff.md"),
            "media_manifest_path": str(run_dir / "media-manifest.json"),
            "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
            "autonomous_fix_status": "not_run",
            "autonomous_gate_summary": "driver=fail, watchdog=not_run, filing=not_run, gh_agent=not_run, independent_verify=fail, review=not_run, validation=fail",
            "independent_verify_status": "fail",
            "independent_verify_summary": summary,
            "issue_closeout_status": "not_run",
            "issue_closeout_count": 0,
            "issue_closeout_summary": "Issue close-out skipped because autonomous driver dispatch did not capture proof.",
            "autonomous_watchdog_status": "not_run",
            "autonomous_watchdog_summary": "watchdog skipped because autonomous driver dispatch did not capture proof",
            "autonomous_watchdog_path": "",
            "autonomous_watchdog_markdown_path": "",
            "autonomous_watchdog_age_minutes": 0,
            "filing_status": "not_run",
            "filing_summary": "Issue filing skipped because autonomous driver dispatch did not capture proof.",
            "findings_filed_count": 0,
            "findings_skipped_count": 0,
            "findings_failed_count": 0,
            "findings_unfiled_count": 0,
            "gh_agent_health_status": "not_run",
            "gh_agent_readiness_status": "not_run",
            "gh_agent_enqueue_status": "not_run",
            "gh_agent_drain_status": "not_run",
            "gh_agent_drained_count": 0,
            "gh_agent_done_count": 0,
            "gh_agent_failed_count": 0,
            "gh_agent_active_count": 0,
            "review_status": "not_run",
            "review_summary": "",
            "review_passed_count": 0,
            "review_failed_count": 0,
            "review_warning_count": 0,
            "review_total_count": 0,
            "review_backlog_summary": summary,
            "validation_status": "invalid",
            "validation_errors": 1,
            "validation_warnings": 0,
            "validation_issue_summary": "autonomous-driver-dispatch",
            "stats_status": "not_run",
            "stats_root": "",
            "stats_output": "",
            "stats_summary": "",
            "stats_gate_status": "fail",
            "stats_gate_summary": "driver dispatch failed before stats",
            "stats_current_run_scanned": "no",
            "stats_runs_scanned": 0,
            "stats_found_count": 0,
            "stats_filed_count": 0,
            "stats_fixed_count": 0,
            "stats_reopened_count": 0,
            "stats_unknown_state_count": 0,
            "stats_similar_pair_count": 0,
        }
        if autonomous_marathon_control_path(run_dir).exists():
            control = read_json(autonomous_marathon_control_path(run_dir))
            result["autonomous_control_path"] = str(autonomous_marathon_control_path(run_dir))
            result["autonomous_control_markdown_path"] = str(autonomous_marathon_control_markdown_path(run_dir))
            result["autonomous_control_status"] = control.get("status", "")
            result["autonomous_control_summary"] = autonomous_marathon_control_summary(control)
        result["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(run_dir, result))
        return result
    watchdog = autonomous_marathon_watchdog(run_dir, watchdog_checked_at)
    if watchdog.get("status") != "autonomous_watchdog_ok":
        base = {
            "autonomous_fix_status": "not_run",
            "independent_verify_status": "fail",
            "independent_verify_summary": watchdog.get("autonomous_watchdog_summary", ""),
            "autonomous_gate_summary": "watchdog=fail, filing=not_run, gh_agent=not_run, independent_verify=fail, review=not_run, validation=fail",
            "review_status": "not_run",
            "review_summary": "",
            "review_passed_count": 0,
            "review_failed_count": 0,
            "review_warning_count": 0,
            "review_total_count": 0,
            "review_backlog_summary": watchdog.get("blocker_summary", ""),
            "validation_status": "invalid",
            "validation_errors": 1,
            "validation_warnings": 0,
            "validation_issue_summary": "autonomous-watchdog",
        }
        result = {
            **base,
            **run_story_summary(run_dir),
            "status": "autonomous_marathon_invalid",
            "autonomous_marathon_status": "autonomous_marathon_invalid",
            "autonomous_marathon_summary": f"watchdog=fail: {watchdog.get('autonomous_watchdog_summary', '')}",
            "run_dir": str(run_dir),
            "deck_path": str(run_dir / "deck.slidey.json"),
            "execution_plan_path": str(run_dir / "execution-plan.md"),
            "driver_plan_path": str(run_dir / "driver-plan.md"),
            "driver_journal_path": str(run_dir / "driver-journal.md"),
            "agent_brief_path": str(run_dir / "agent-brief.md"),
            "driver_handoff_path": str(run_dir / "driver-handoff.md"),
            "media_manifest_path": str(run_dir / "media-manifest.json"),
            "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
            "autonomous_control_path": str(autonomous_marathon_control_path(run_dir)) if autonomous_marathon_control_path(run_dir).exists() else "",
            "autonomous_control_markdown_path": str(autonomous_marathon_control_markdown_path(run_dir)) if autonomous_marathon_control_markdown_path(run_dir).exists() else "",
            "stats_status": "not_run",
            "stats_root": "",
            "stats_output": "",
            "stats_summary": "",
            "stats_gate_status": "fail",
            "stats_gate_summary": "watchdog failed before stats",
            "stats_current_run_scanned": "no",
            "stats_runs_scanned": 0,
            "stats_found_count": 0,
            "stats_filed_count": 0,
            "stats_fixed_count": 0,
            "stats_reopened_count": 0,
            "stats_unknown_state_count": 0,
            "stats_similar_pair_count": 0,
        }
        result.update(run_story_summary(run_dir))
        result["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(run_dir, result))
        return result
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"items": []}
    credible_issues = credible_issue_findings(findings)
    if credible_issues:
        if not ticket_repo:
            raise SystemExit("--autonomous-marathon requires --ticket-repo when credible issue findings exist")
        if not gh_agent_db:
            gh_agent_db = str(run_dir / "gh-agent-jobs.sqlite")
        fix_result = gitops_autonomous_fix(
            run_dir,
            ticket_repo,
            gh_agent_db,
            gh_agent_story,
            gh_agent_public_base_url,
            gh_agent_project_root,
            gh_agent_incident_repo,
            gh_agent_asset_dir,
            gh_agent_comment_mode,
        )
        base = dict(fix_result)
        fix_valid = fix_result.get("autonomous_fix_status") == "autonomous_fix_valid"
    else:
        reviewed = review_run_bundle(run_dir, publish_deck)
        validation = validate_run_bundle(run_dir)
        review_status = reviewed.get("review_status", reviewed.get("status", ""))
        review_failed = int(reviewed.get("review_failed_count", reviewed.get("failed", 0)) or 0)
        validation_status = validation.get("status", "")
        validation_errors = int(validation.get("errors", 0) or 0)
        review_gate = "pass" if review_status == "ready" and review_failed == 0 else "fail"
        validation_gate = "pass" if validation_status == "valid" and validation_errors == 0 else "fail"
        base = {
            "autonomous_fix_status": "not_required",
            "independent_verify_status": "not_required",
            "independent_verify_summary": "independent verification not required",
            "autonomous_gate_summary": (
                "filing=not_required, gh_agent=not_required, independent_verify=not_required, "
                f"review={review_gate}, validation={validation_gate}"
            ),
            "review_status": review_status,
            "review_summary": reviewed.get("summary", ""),
            "review_passed_count": reviewed.get("review_passed_count", reviewed.get("passed", 0)),
            "review_failed_count": review_failed,
            "review_warning_count": reviewed.get("review_warning_count", reviewed.get("warnings", 0)),
            "review_total_count": reviewed.get("review_total_count", reviewed.get("total", 0)),
            "review_backlog_summary": reviewed.get("review_backlog_summary", ""),
            "validation_status": validation_status,
            "validation_errors": validation_errors,
            "validation_warnings": validation.get("warnings", 0),
            "validation_issue_summary": validation.get("validation_issue_summary", ""),
        }
        base.update(run_story_summary(run_dir))
        fix_valid = True

    stats_path = stats_output or str(run_dir / "autonomous-marathon-stats.json")
    stats = derive_stats(
        run_dir_from_arg(stats_root) if stats_root else ARTIFACT_ROOT,
        issue_state_file,
        similarity_threshold,
        similar_pair_limit,
        stats_path,
    )
    review_ok = base.get("review_status") == "ready" and int(base.get("review_failed_count", 0) or 0) == 0
    validation_ok = base.get("validation_status") == "valid" and int(base.get("validation_errors", 0) or 0) == 0
    stats_output_path = str(stats.get("stats_output", "")).strip()
    stats_output_exists = bool(stats_output_path) and run_dir_from_arg(stats_output_path).exists()
    credible_issue_count = len(credible_issues)
    scanned_run_dirs = {
        run_dir_from_arg(path).resolve()
        for path in stats.get("run_dirs", []) or []
        if str(path).strip()
    }
    current_run_scanned = run_dir.resolve() in scanned_run_dirs
    stats_ok = (
        stats.get("status") == "stats_derived"
        and stats_output_exists
        and current_run_scanned
        and int(stats.get("findings_found_count", 0) or 0) >= credible_issue_count
        and int(stats.get("findings_filed_count", 0) or 0) >= credible_issue_count
        and int(stats.get("issues_fixed_count", 0) or 0) >= credible_issue_count
    )
    stats_gate = (
        "pass"
        if stats_ok
        else (
            "fail: "
            f"status={stats.get('status', '')}, output={stats_output_path or '(missing)'}, "
            f"current_run_scanned={'yes' if current_run_scanned else 'no'}, "
            f"found={stats.get('findings_found_count', 0)}/{credible_issue_count}, "
            f"filed={stats.get('findings_filed_count', 0)}/{credible_issue_count}, "
            f"fixed={stats.get('issues_fixed_count', 0)}/{credible_issue_count}"
        )
    )
    stats_gate_status = "pass" if stats_ok else "fail"
    status = "autonomous_marathon_valid" if fix_valid and review_ok and validation_ok and stats_ok else "autonomous_marathon_invalid"
    result = {
        **base,
        "status": status,
        "autonomous_marathon_status": status,
        "autonomous_marathon_summary": (
            f"credible_issues={len(credible_issues)}, "
            f"fix={base.get('autonomous_fix_status', 'not_run')}, "
            f"review={base.get('review_status', '')}, "
            f"validation={base.get('validation_status', '')}, "
            f"stats={stats_gate}"
        ),
        "run_dir": str(run_dir),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "autonomous_control_path": str(autonomous_marathon_control_path(run_dir)) if autonomous_marathon_control_path(run_dir).exists() else "",
        "autonomous_control_markdown_path": str(autonomous_marathon_control_markdown_path(run_dir)) if autonomous_marathon_control_markdown_path(run_dir).exists() else "",
        "stats_status": stats.get("status", ""),
        "stats_root": stats.get("stats_root", ""),
        "stats_output": stats.get("stats_output", ""),
        "stats_summary": stats.get("stats_summary", ""),
        "stats_gate_status": stats_gate_status,
        "stats_gate_summary": stats_gate,
        "stats_current_run_scanned": "yes" if current_run_scanned else "no",
        "stats_runs_scanned": stats.get("runs_scanned", 0),
        "stats_found_count": stats.get("findings_found_count", 0),
        "stats_filed_count": stats.get("findings_filed_count", 0),
        "stats_fixed_count": stats.get("issues_fixed_count", 0),
        "stats_reopened_count": stats.get("issues_reopened_count", 0),
        "stats_unknown_state_count": stats.get("issues_unknown_state_count", 0),
        "stats_similar_pair_count": stats.get("similar_pair_count", 0),
    }
    if result["autonomous_control_path"]:
        control = read_json(autonomous_marathon_control_path(run_dir))
        result["autonomous_control_status"] = control.get("status", "")
        result["autonomous_control_summary"] = autonomous_marathon_control_summary(control)
    result.update(run_story_summary(run_dir))
    result["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(run_dir, result))
    return result


def refresh_issue_state_cache(root: Path, issue_state_file: str, ticket_repo: str) -> dict:
    output = run_dir_from_arg(issue_state_file) if issue_state_file else root / "stats" / "issue-state.json"
    cmd = [
        "go", "run", "./cmd/kitsoki",
        "gitops", "issue-state-cache",
        "--findings-root", str(root),
        "--output", str(output),
        "--json",
    ]
    if ticket_repo:
        cmd.extend(["--repo", ticket_repo])
    proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True)
    if proc.returncode != 0:
        raise SystemExit(
            "kitsoki gitops issue-state-cache failed\n"
            f"cmd: {' '.join(shlex.quote(part) for part in cmd)}\n"
            f"stdout:\n{proc.stdout}\n"
            f"stderr:\n{proc.stderr}"
        )
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"kitsoki gitops issue-state-cache printed invalid JSON: {exc}\n{proc.stdout}") from exc


def record_driver_event(
    run_dir: Path,
    scenario_id: str,
    dispatch_mode: str,
    status: str,
    summary: str,
    mcp_tools: str,
    evidence_refs: str,
    blockers: str,
    publish_deck: Optional[Path],
) -> dict:
    run_json = read_json(run_dir / "run.json")
    schema = read_json(SCHEMA)
    known_scenarios = {scenario["id"] for scenario in run_json["scenarios"]}
    if scenario_id and scenario_id not in known_scenarios:
        known = ", ".join(sorted(known_scenarios))
        raise SystemExit(f"Unknown scenario '{scenario_id}'. Known: {known}")
    if dispatch_mode not in schema["driver_journal"]["dispatch_modes"]:
        raise SystemExit("Driver dispatch mode must be replay, record, or live")
    if status not in schema["driver_journal"]["statuses"]:
        raise SystemExit("Driver event status must be attempted, captured, blocked, or validated")
    live_budget_minutes = int(run_json.get("live_budget_minutes", 0))
    if dispatch_mode == "live" and live_budget_minutes == 0 and status != "blocked":
        raise SystemExit(
            "Live driver dispatch is disabled for this run; record a blocked driver event "
            "or create the run with a positive live_budget_minutes value"
        )
    journal_path = run_dir / "driver-journal.json"
    journal = read_json(journal_path) if journal_path.exists() else build_driver_journal(run_json["run_id"], [])
    items = journal.setdefault("items", [])
    event = {
        "id": f"driver-event-{len(items) + 1}",
        "created_at": now_utc(),
        "scenario": scenario_id,
        "dispatch_mode": dispatch_mode,
        "status": status,
        "summary": summary,
        "mcp_tools": split_csv(mcp_tools),
        "evidence_refs": split_csv(evidence_refs),
        "blockers": split_csv(blockers),
    }
    items.append(event)
    journal = build_driver_journal(run_json["run_id"], items)
    write_json(journal_path, journal)
    (run_dir / "driver-journal.md").write_text(render_driver_journal(journal), encoding="utf-8")
    update_derived_artifacts(run_dir, publish_deck=publish_deck)
    return event


def seed_demo_driver_journal(run_dir: Path, run_json: dict, evidence: dict) -> int:
    journal_path = run_dir / "driver-journal.json"
    journal = read_json(journal_path) if journal_path.exists() else build_driver_journal(run_json["run_id"], [])
    items = journal.get("items", [])
    demo_scenarios = {
        item.get("scenario", "")
        for item in items
        if item.get("summary", "").startswith("Deterministic demo driver")
    }
    evidence_refs_by_scenario: dict[str, list[str]] = {}
    for item in evidence.get("items", []):
        if item.get("status") in {"captured", "validated"} and item.get("path"):
            evidence_refs_by_scenario.setdefault(item["scenario"], []).append(item["path"])

    added = 0
    for scenario in run_json.get("scenarios", []):
        scenario_id = scenario["id"]
        if scenario_id in demo_scenarios:
            continue
        items.append({
            "id": f"driver-event-{len(items) + 1}",
            "created_at": now_utc(),
            "scenario": scenario_id,
            "dispatch_mode": "replay",
            "status": "captured",
            "summary": (
                "Deterministic demo driver exercised the scenario contract with "
                "placeholder evidence. This proves the journal path, not live product usage."
            ),
            "mcp_tools": scenario.get("required_mcp", []),
            "evidence_refs": evidence_refs_by_scenario.get(scenario_id, []),
            "blockers": [],
        })
        added += 1

    journal = build_driver_journal(run_json["run_id"], items)
    write_json(journal_path, journal)
    (run_dir / "driver-journal.md").write_text(render_driver_journal(journal), encoding="utf-8")
    return added


def seed_demo_evidence(run_dir: Path, publish_deck: Optional[Path]) -> dict:
    run_json = read_json(run_dir / "run.json")
    evidence = read_json(run_dir / "evidence.json")
    demo_evidence = [
        (
            item["scenario"],
            item["kind"],
            demo_evidence_path(item["scenario"], item["kind"]),
            "captured",
            f"demo placeholder: {evidence_capture_hint(item['kind'])}",
        )
        for item in evidence.get("items", [])
    ]
    for scenario, kind, path, status, notes in demo_evidence:
        attach_evidence(run_dir, scenario, kind, path, status, "demo", notes, publish_deck=None)

    demo_findings = [
        ("strength", "Scenario contract is explicit", "The bundle names persona, scenario, expected MCP tools, evidence slots, and success criteria before live execution.", "product-discovery", "low", "screens/product-discovery.png", "observed"),
        ("weakness", "Onboarding still needs live visual proof", "The demo bundle shows the evidence contract, but a real visual MCP capture is still required to validate onboarding clarity.", "project-onboarding", "medium", "traces/onboarding.jsonl", "open"),
        ("issue", "Operator handoff can lose context", "A persona should not need private repo knowledge to pick the next Kitsoki story after onboarding.", "project-onboarding", "medium", "bug-reports/product-issue.md", "open"),
        ("fix", "Review deck now summarizes evidence and findings", "The product-journey runner regenerates metrics and Slidey scenes when evidence or findings are attached.", "evidence-backed-product-bug", "low", "deck.slidey.json", "fixed"),
    ]
    findings_path = run_dir / "findings.json"
    existing_titles = set()
    if findings_path.exists():
        existing_titles = {item.get("title", "") for item in read_json(findings_path).get("items", [])}
    findings_added = 0
    for kind, title, summary, scenario, severity, evidence_path, status in demo_findings:
        if title in existing_titles:
            continue
        record_finding(run_dir, kind, title, summary, scenario, severity, evidence_path, status, publish_deck=None, origin="seeded")
        findings_added += 1

    evidence = read_json(run_dir / "evidence.json")
    driver_events_added = seed_demo_driver_journal(run_dir, run_json, evidence)
    update_derived_artifacts(run_dir, publish_deck=publish_deck)
    metrics = read_json(run_dir / "metrics.json")
    result = {
        "status": "seeded",
        "run_dir": str(run_dir),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "evidence_added": len(demo_evidence),
        "findings_added": findings_added,
        "driver_events_added": driver_events_added,
        "driver_event_count": metrics.get("driver_event_count", 0),
        "present_evidence_count": metrics.get("present_evidence_count", 0),
        "findings_count": metrics.get("findings_count", 0),
        "published_deck_path": str(publish_deck) if publish_deck is not None else "",
    }
    result.update(run_story_summary(run_dir))
    return result


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def review_run_bundle(run_dir: Path, publish_deck: Optional[Path]) -> dict:
    schema = read_json(SCHEMA)
    update_derived_artifacts(run_dir, publish_deck=None)
    run_json = read_json(run_dir / "run.json")
    evidence = read_json(run_dir / "evidence.json")
    findings = read_json(run_dir / "findings.json")
    driver_journal = read_json(run_dir / "driver-journal.json") if (run_dir / "driver-journal.json").exists() else build_driver_journal(run_json["run_id"], [])
    metrics = read_json(run_dir / "metrics.json")
    execution_plan = build_execution_plan(run_json, evidence)

    required_files = [
        "run.json",
        "journey.md",
        "metrics.json",
        "bugs.json",
        "findings.json",
        "scenario-outcomes.json",
        "scenario-outcomes.md",
        "evidence.json",
        "media-manifest.json",
        "scenarios.json",
        "execution-plan.json",
        "execution-plan.md",
        "driver-plan.json",
        "driver-plan.md",
        "driver-journal.json",
        "driver-journal.md",
        "agent-brief.json",
        "agent-brief.md",
        "driver-handoff.json",
        "driver-handoff.md",
        "weakness-routes.json",
        "weakness-routes.md",
        "prd-design-intake.json",
        "prd-design-intake.md",
        "review.json",
        "deck.slidey.json",
    ]
    evidence_items = evidence.get("items", [])
    for item in evidence_items:
        item["source"] = normalize_evidence_source(item.get("source", ""), item.get("path", ""), item.get("notes", ""))
    media_manifest = build_media_manifest(run_json, evidence)
    present_items = [item for item in evidence_items if item.get("status") in {"captured", "validated"}]
    demo_items = [item for item in present_items if item.get("source") == "demo"]
    proof_items = [item for item in present_items if is_proof_evidence(item, run_dir)]
    rejected_items = [item for item in evidence_items if item.get("status") == "rejected"]
    video_items = [item for item in media_manifest["items"] if item["media_kind"] == "video"]
    playback_items = [item for item in media_manifest["items"] if item["playback"]]
    missing_playback_refs = missing_local_artifact_refs(run_dir, playback_items)
    finding_items = findings.get("items", [])
    finding_kinds = {item.get("kind") for item in finding_items}
    weakness_routes = build_weakness_routes(run_json, findings)
    prd_design_intake = build_prd_design_intake(run_json, weakness_routes)
    open_weakness_ids = {
        item.get("id") or f"weakness-{index}"
        for index, item in enumerate(open_weakness_findings(findings), start=1)
    }
    routed_weakness_ids = {
        item.get("finding_id", "")
        for item in weakness_routes.get("items", [])
        if item.get("target_pipeline") == "prd-design" and item.get("target_story") == "stories/prd"
    }
    unrouted_weaknesses = sorted(open_weakness_ids - routed_weakness_ids)
    intake_weakness_ids = {
        item.get("finding_id", "")
        for item in prd_design_intake.get("items", [])
        if item.get("target_pipeline") == "prd-design"
        and item.get("target_story") == "stories/prd"
        and item.get("story_intent") == "start"
        and item.get("story_slots", {}).get("idea")
        and item.get("story_slots", {}).get("upstream_paths")
        and item.get("persona_lens", {}).get("starting_surface")
        and item.get("persona_lens", {}).get("evidence_emphasis")
    }
    missing_prd_intake = sorted(open_weakness_ids - intake_weakness_ids)
    # Legacy findings predate the origin field; treat them as observed so old
    # bundles do not retroactively fail. New runs stamp origin explicitly.
    observed_findings = [item for item in finding_items if item.get("origin", "observed") != "seeded"]
    seeded_findings = [item for item in finding_items if item.get("origin", "observed") == "seeded"]
    blocked_scenarios = {
        item.get("scenario", "")
        for item in finding_items
        if item.get("status") == "blocked" and item.get("scenario")
    }
    attempted_scenarios = {
        item.get("scenario", "")
        for item in present_items
        if item.get("scenario")
    } | blocked_scenarios
    missing_attempts = [
        scenario.get("id", "")
        for scenario in run_json.get("scenarios", [])
        if scenario.get("id", "") not in attempted_scenarios
    ]
    journaled_scenarios = {
        item.get("scenario", "")
        for item in driver_journal.get("items", [])
        if item.get("scenario")
    }
    live_budget_minutes = int(run_json.get("live_budget_minutes", 0) or 0)
    forbidden_live_events = [
        item.get("id", f"driver-event-{index}")
        for index, item in enumerate(driver_journal.get("items", []), start=1)
        if item.get("dispatch_mode") == "live"
        and item.get("status") != "blocked"
        and live_budget_minutes == 0
    ]
    missing_driver_journal = [
        scenario.get("id", "")
        for scenario in run_json.get("scenarios", [])
        if scenario.get("id", "") not in journaled_scenarios
        and scenario.get("id", "") not in blocked_scenarios
    ]
    missing_driver_evidence_refs = unattached_driver_evidence_refs(evidence, driver_journal)
    missing_playback_evidence_slots = missing_playback_evidence(run_json, evidence_items, run_dir, blocked_scenarios)
    scenario_outcomes = build_scenario_outcomes(run_json, evidence, findings)
    driver_plan = build_driver_plan(run_json, evidence, execution_plan)
    quality_gates = summarize_quality_gates(evidence, scenario_outcomes, driver_plan, run_dir)
    driver_action_contract = summarize_driver_action_contract(driver_plan, schema)
    unsatisfied_quality_gates = [
        f"{gate['scenario']} ({gate['present_minimum_evidence_count']}/{gate['minimum_evidence_count']})"
        for gate in quality_gates
        if not gate.get("satisfied") and not gate.get("blocked")
    ]
    invalid_driver_actions = [
        f"{row['scenario']}: actions={','.join(row['action_ids']) or 'none'}"
        for row in driver_action_contract["rows"]
        if not row["valid"]
    ]
    filing_requested = bool(findings.get("filing", {}).get("requested"))
    credible_findings = credible_issue_findings(findings)
    credible_findings_needing_github = credible_findings_requiring_github(findings)
    unfiled_credible = unfiled_credible_findings(findings)
    driver_receipt_gaps = credible_issue_driver_receipt_gaps(findings, driver_journal, evidence, run_dir)
    gh_agent = findings.get("gh_agent", {}) if isinstance(findings.get("gh_agent", {}), dict) else {}
    gh_agent_requested = gh_agent.get("enqueue_status", "") not in {"", "disabled", "dry-run"}
    gh_agent_enqueued = int(gh_agent.get("enqueued_count", 0) or 0)
    gh_agent_done = int(gh_agent.get("done_count", 0) or 0)
    gh_agent_failed = int(gh_agent.get("failed_count", 0) or 0)
    gh_agent_active = int(gh_agent.get("active_count", 0) or 0)
    gh_agent_drain_status = gh_agent.get("drain_status", "")
    gh_agent_missing_evidence = gh_agent_missing_fix_evidence(gh_agent)
    gh_agent_missing_triage = gh_agent_missing_triage_evidence(gh_agent)
    gh_agent_missing_verify = gh_agent_missing_independent_verify(gh_agent)
    gh_agent_missing_run_url = gh_agent_missing_run_urls(gh_agent)
    gh_agent_missing_landing = gh_agent_missing_integration_landing(gh_agent)
    autonomous_fix_report_missing = missing_autonomous_fix_report_tokens(run_dir, findings)
    gh_agent_jobs_terminal = (
        (not credible_findings_needing_github and not gh_agent_requested)
        or (
            gh_agent_enqueued > 0
            and gh_agent_drain_status == "drained"
            and gh_agent_failed == 0
            and gh_agent_active == 0
            and gh_agent_done >= gh_agent_enqueued
        )
    )
    gh_agent_fix_evidence_complete = (not gh_agent_requested) or not gh_agent_missing_evidence
    gh_agent_triage_evidence_complete = (not gh_agent_requested) or not gh_agent_missing_triage
    gh_agent_independent_verify_complete = (not gh_agent_requested) or not gh_agent_missing_verify
    gh_agent_run_urls_complete = (not gh_agent_requested) or not gh_agent_missing_run_url
    gh_agent_integration_landing_complete = (not gh_agent_requested) or not gh_agent_missing_landing
    issue_closeout_ok, issue_closeout_detail = issue_closeout_gate(findings, gh_agent_requested, credible_findings_needing_github)

    checks = [
        {
            "id": "required-files",
            "status": "pass" if all((run_dir / name).exists() for name in required_files) else "fail",
            "summary": "All required bundle files exist.",
            "detail": ", ".join(name for name in required_files if not (run_dir / name).exists()),
        },
        {
            "id": "scenario-contract",
            "status": "pass" if len(run_json.get("scenarios", [])) >= 1 and len(evidence_items) >= len(run_json.get("scenarios", [])) else "fail",
            "summary": "Scenario and evidence contracts are present.",
            "detail": f"scenarios={len(run_json.get('scenarios', []))}, evidence_slots={len(evidence_items)}",
        },
        {
            "id": "scenario-attempts",
            "status": "pass" if not missing_attempts else "fail",
            "summary": "Every scenario has captured evidence or an explicit blocker.",
            "detail": ", ".join(missing_attempts),
        },
        {
            "id": "driver-journal-coverage",
            "status": "pass" if not missing_driver_journal else "fail",
            "summary": "Every non-blocked scenario has a driver journal event.",
            "detail": ", ".join(missing_driver_journal),
        },
        {
            "id": "driver-evidence-linked",
            "status": "pass" if not missing_driver_evidence_refs else "fail",
            "summary": "Captured driver journal evidence refs are attached as structured evidence.",
            "detail": ", ".join(missing_driver_evidence_refs),
        },
        {
            "id": "driver-live-budget",
            "status": "pass" if not forbidden_live_events else "fail",
            "summary": "Live driver dispatch is used only when the run has live budget, otherwise it is recorded as blocked.",
            "detail": ", ".join(forbidden_live_events),
        },
        {
            "id": "driver-action-contract",
            "status": "pass" if not invalid_driver_actions else "fail",
            "summary": "Every scenario keeps the reusable driver action sequence and journal recording path.",
            "detail": "; ".join(invalid_driver_actions),
        },
        {
            "id": "captured-evidence",
            "status": "pass" if present_items else "fail",
            "summary": "At least one captured or validated evidence artifact is attached.",
            "detail": f"present={len(present_items)}, required={len(evidence_items)}",
        },
        {
            "id": "non-demo-evidence",
            "status": "pass" if proof_items else "warn",
            "summary": "At least one captured evidence artifact is real, retained, external, or cassette-backed rather than seeded demo evidence.",
            "detail": f"proof={len(proof_items)}, demo={len(demo_items)}",
        },
        {
            "id": "key-video",
            "status": "pass" if video_items else "warn",
            "summary": "At least one key interaction video is attached for Slidey playback.",
            "detail": f"video_items={len(video_items)}",
        },
        {
            "id": "media-manifest",
            "status": "pass" if playback_items else "warn",
            "summary": "Captured visual media is listed in the playback manifest.",
            "detail": f"playback_items={len(playback_items)}",
        },
        {
            "id": "playback-artifacts-resolve",
            "status": "pass" if not missing_playback_refs else "warn",
            "summary": "Playback media references resolve locally or use retained/external IDs.",
            "detail": ", ".join(missing_playback_refs),
        },
        {
            "id": "playback-or-blocker",
            "status": "pass" if playback_items or blocked_scenarios else "fail",
            "summary": "The review deck has playback media or an explicit blocked-scenario reason for missing playback.",
            "detail": f"playback_items={len(playback_items)}, blocked_scenarios={len(blocked_scenarios)}",
        },
        {
            "id": "playback-evidence-backed",
            "status": "pass" if not missing_playback_evidence_slots else "fail",
            "summary": "Each scenario's playback-capable evidence slot (rrweb|trace-replay|flow-fixture|png-sequence) is backed by a real local file, not a cassette:// or other unbacked reference.",
            "detail": "; ".join(missing_playback_evidence_slots),
        },
        {
            "id": "findings-summary",
            "status": "pass" if finding_items else "fail",
            "summary": "Strengths, weaknesses, issues, or fixes are recorded.",
            "detail": f"findings={len(finding_items)}",
        },
        {
            "id": "balanced-findings",
            "status": "pass" if {"strength", "weakness"} <= finding_kinds and ("issue" in finding_kinds or "fix" in finding_kinds) else "warn",
            "summary": "Findings include positive evidence and at least one gap or fix.",
            "detail": ", ".join(sorted(kind for kind in finding_kinds if kind)) or "none",
        },
        {
            # Accuracy / novelty gate: a run backed by real proof evidence must
            # carry at least one *observed* finding. Seeded demo findings alone
            # can satisfy every structural check yet describe only the harness,
            # not the product. Pure seeded smokes (no proof evidence) stay a
            # warning so the deterministic dogfood loop keeps passing.
            "id": "observed-findings",
            "status": "pass" if observed_findings else ("fail" if proof_items else "warn"),
            "summary": "Runs with real proof evidence record at least one observed (non-seeded) finding.",
            "detail": f"observed={len(observed_findings)}, seeded={len(seeded_findings)}, proof={len(proof_items)}",
        },
        {
            "id": "scenario-outcomes",
            "status": "pass" if scenario_outcomes["summary"]["scenarios"] == len(run_json.get("scenarios", [])) else "fail",
            "summary": "Each scenario has an outcome row for review and matrix rollups.",
            "detail": f"outcomes={scenario_outcomes['summary']['scenarios']}, with_findings={scenario_outcomes['summary']['with_findings']}",
        },
        {
            "id": "quality-gates",
            "status": "pass" if not unsatisfied_quality_gates else "fail",
            "summary": "Every scenario satisfies its minimum proof gate or records an explicit blocker.",
            "detail": ", ".join(unsatisfied_quality_gates),
        },
        {
            "id": "no-rejected-evidence",
            "status": "pass" if not rejected_items else "warn",
            "summary": "No attached evidence is marked rejected.",
            "detail": f"rejected={len(rejected_items)}",
        },
        {
            "id": "weakness-routing",
            "status": "pass" if not unrouted_weaknesses else "fail",
            "summary": "Open observed weakness findings are routed into the PRD/design pipeline rather than the bugfix queue.",
            "detail": (
                f"unrouted: {', '.join(unrouted_weaknesses)}"
                if unrouted_weaknesses
                else f"routed={len(routed_weakness_ids)}/{len(open_weakness_ids)} target=stories/prd"
            ),
        },
        {
            "id": "prd-design-intake",
            "status": "pass" if not missing_prd_intake else "fail",
            "summary": "Open observed weakness routes have PRD/design start-intent intake with persona lens and upstream evidence.",
            "detail": (
                f"missing: {', '.join(missing_prd_intake)}"
                if missing_prd_intake
                else f"intake={len(intake_weakness_ids)}/{len(open_weakness_ids)} target=stories/prd intent=start"
            ),
        },
        {
            # GitHub filing gate: credible issue findings are not discussion
            # ready until the story-owned autonomous path has filed them.
            "id": "findings-filed",
            "status": "pass" if not unfiled_credible else "fail",
            "summary": "Every credible issue finding has a filed issue URL before review.",
            "detail": (
                f"unfiled: {', '.join(unfiled_credible)}"
                if unfiled_credible
                else (
                    f"ticket_repo={findings.get('filing', {}).get('ticket_repo', '')}, filed={len(credible_findings) - len(unfiled_credible)}/{len(credible_findings)}"
                    if filing_requested
                    else f"credible={len(credible_findings)}"
                )
            ),
        },
        {
            "id": "credible-issue-driver-receipts",
            "status": "pass" if not driver_receipt_gaps else "fail",
            "summary": "Every credible issue finding is backed by a captured or validated driver receipt with proof evidence.",
            "detail": (
                ", ".join(driver_receipt_gaps)
                if driver_receipt_gaps
                else f"credible={len(credible_findings)}"
            ),
        },
        {
            "id": "gh-agent-fixes",
            "status": "pass" if gh_agent_jobs_terminal else "fail",
            "summary": "When gh-agent fixing was requested, queued fix jobs drain to reviewable terminal runs.",
            "detail": (
                f"credible issue findings require autonomous_fix or a local-artifact ticket; credible={len(credible_findings_needing_github)}"
                if credible_findings_needing_github and not gh_agent_requested
                else "gh-agent fixing not requested"
                if not gh_agent_requested
                else f"enqueue={gh_agent.get('enqueue_status', '')}, drain={gh_agent_drain_status}, enqueued={gh_agent_enqueued}, done={gh_agent_done}, failed={gh_agent_failed}, active={gh_agent_active}; {gh_agent.get('run_summary', '')}"
            ),
        },
        {
            "id": "gh-agent-fix-evidence",
            "status": "pass" if gh_agent_fix_evidence_complete else "fail",
            "summary": "Completed gh-agent fix jobs expose reviewable evidence assets.",
            "detail": (
                "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(gh_agent_missing_evidence)}"
                    if gh_agent_missing_evidence
                    else f"evidence_links={len(gh_agent_fix_evidence_links(gh_agent))}"
                )
            ),
        },
        {
            "id": "gh-agent-triage-evidence",
            "status": "pass" if gh_agent_triage_evidence_complete else "fail",
            "summary": "Completed gh-agent fix jobs expose the triage preflight verdict artifact.",
            "detail": (
                "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(gh_agent_missing_triage)}"
                    if gh_agent_missing_triage
                    else f"triage_links={len(gh_agent_triage_evidence_links(gh_agent))}"
                )
            ),
        },
        {
            "id": "gh-agent-independent-verify",
            "status": "pass" if gh_agent_independent_verify_complete else "fail",
            "summary": "Completed gh-agent fix jobs expose story-owned independent verification artifacts.",
            "detail": (
                "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(gh_agent_missing_verify)}"
                    if gh_agent_missing_verify
                    else f"independent_verify_links={len(gh_agent_independent_verify_links(gh_agent))}"
                )
            ),
        },
        {
            "id": "gh-agent-run-url",
            "status": "pass" if gh_agent_run_urls_complete else "fail",
            "summary": "Completed gh-agent fix jobs expose public run URLs for human review.",
            "detail": (
                "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(gh_agent_missing_run_url)}"
                    if gh_agent_missing_run_url
                    else f"run_urls={len([job for job in gh_agent.get('drained_jobs', []) or [] if isinstance(job, dict) and str(job.get('run_url', '')).strip()])}"
                )
            ),
        },
        {
            "id": "gh-agent-integration-landing",
            "status": "pass" if gh_agent_integration_landing_complete else "fail",
            "summary": "Completed gh-agent fix jobs record the integration branch and commit that landed the change.",
            "detail": (
                "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(gh_agent_missing_landing)}"
                    if gh_agent_missing_landing
                    else "; ".join(gh_agent_integration_landing_lines(gh_agent))
                )
            ),
        },
        {
            "id": "issue-closeout",
            "status": "pass" if issue_closeout_ok else "fail",
            "summary": "After autonomous fixes pass, filed GitHub issues receive kitsoki-fixed-in close-out comments and are closed.",
            "detail": issue_closeout_detail,
        },
        {
            "id": "autonomous-fix-report",
            "status": "pass" if (not credible_findings_needing_github or (gh_agent_requested and not autonomous_fix_report_missing)) else "fail",
            "summary": "Autonomous fixes produce a complete human-review report with watchdog proof, hosted-agent readiness proof, filed issue links, run links, and evidence links.",
            "detail": (
                f"credible issue findings require autonomous_fix or a local-artifact ticket; credible={len(credible_findings_needing_github)}"
                if credible_findings_needing_github and not gh_agent_requested
                else "gh-agent fixing not requested"
                if not gh_agent_requested
                else (
                    f"missing: {', '.join(autonomous_fix_report_missing[:5])}"
                    if autonomous_fix_report_missing
                    else "autonomous-fix-report.md"
                )
            ),
        },
        {
            "id": "deck-generated",
            "status": "pass" if (run_dir / "deck.slidey.json").exists() else "fail",
            "summary": "Slidey deck exists for review.",
            "detail": "deck.slidey.json",
        },
    ]
    passed = sum(1 for check in checks if check["status"] == "pass")
    failed = sum(1 for check in checks if check["status"] == "fail")
    warned = sum(1 for check in checks if check["status"] == "warn")
    status = "ready" if failed == 0 else "needs_evidence"
    summary = f"{status}: {passed}/{len(checks)} checks passed, {warned} warnings, {failed} failures"
    review = {
        "run_id": run_json["run_id"],
        "status": status,
        "summary": summary,
        "reviewed_at": now_utc(),
        "summary_counts": {
            "passed": passed,
            "warned": warned,
            "failed": failed,
            "total": len(checks),
        },
        "checks": checks,
    }
    write_json(run_dir / "review.json", review)
    write_json(run_dir / "media-manifest.json", media_manifest)
    write_json(run_dir / "scenario-outcomes.json", scenario_outcomes)
    (run_dir / "scenario-outcomes.md").write_text(render_scenario_outcomes(scenario_outcomes), encoding="utf-8")
    write_json(run_dir / "weakness-routes.json", weakness_routes)
    (run_dir / "weakness-routes.md").write_text(render_weakness_routes(weakness_routes), encoding="utf-8")
    write_json(run_dir / "prd-design-intake.json", prd_design_intake)
    (run_dir / "prd-design-intake.md").write_text(render_prd_design_intake(prd_design_intake), encoding="utf-8")
    metrics["review_status"] = status
    metrics["review_passed_checks"] = passed
    metrics["review_total_checks"] = len(checks)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    write_json(run_dir / "driver-plan.json", driver_plan)
    (run_dir / "driver-plan.md").write_text(render_driver_plan(driver_plan), encoding="utf-8")
    agent_brief = build_agent_brief(run_json, evidence, execution_plan)
    write_json(run_dir / "agent-brief.json", agent_brief)
    (run_dir / "agent-brief.md").write_text(render_agent_brief(agent_brief), encoding="utf-8")
    driver_handoff = build_driver_handoff(run_json, metrics, evidence, review)
    write_json(run_dir / "driver-handoff.json", driver_handoff)
    (run_dir / "driver-handoff.md").write_text(render_driver_handoff(driver_handoff), encoding="utf-8")
    deck = render_deck(run_json, metrics, evidence, findings, review, execution_plan, media_manifest, scenario_outcomes, driver_plan)
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)
    result = {
        "status": "reviewed",
        "review_status": status,
        "summary": summary,
        "run_dir": str(run_dir),
        "review_path": str(run_dir / "review.json"),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "passed": passed,
        "warnings": warned,
        "failed": failed,
        "total": len(checks),
        "checks": checks,
        "published_deck_path": str(publish_deck) if publish_deck is not None else "",
    }
    result.update(run_story_summary(run_dir))
    return result


# TODO(carve): stays in run.py -- reads a monkeypatch-sensitive module
# global (see tools/product-journey/README.md#module-layout); moving it would
# silently stop observing test monkeypatches on the loaded run.py instance.
def validate_run_bundle(run_dir: Path) -> dict:
    schema = read_json(SCHEMA)
    issues: list[dict] = []
    required_files = schema["run_result"]["artifacts"]

    for name in required_files:
        if not (run_dir / name).exists():
            add_validation_issue(issues, "error", "required-file", "Required run artifact is missing", name)

    run_json = load_json_for_validation(run_dir / "run.json", issues)
    metrics = load_json_for_validation(run_dir / "metrics.json", issues)
    evidence = load_json_for_validation(run_dir / "evidence.json", issues)
    media_manifest = load_json_for_validation(run_dir / "media-manifest.json", issues)
    scenarios_json = load_json_for_validation(run_dir / "scenarios.json", issues)
    execution_plan = load_json_for_validation(run_dir / "execution-plan.json", issues)
    driver_plan = load_json_for_validation(run_dir / "driver-plan.json", issues)
    driver_journal = load_json_for_validation(run_dir / "driver-journal.json", issues)
    agent_brief = load_json_for_validation(run_dir / "agent-brief.json", issues)
    driver_handoff = load_json_for_validation(run_dir / "driver-handoff.json", issues)
    scenario_outcomes = load_json_for_validation(run_dir / "scenario-outcomes.json", issues)
    prd_design_intake = load_json_for_validation(run_dir / "prd-design-intake.json", issues)
    review = load_json_for_validation(run_dir / "review.json", issues)
    deck = load_json_for_validation(run_dir / "deck.slidey.json", issues)

    if run_json:
        validate_required_keys(run_json, schema["run_result"]["required"], issues, "run-required-keys", "run.json")
        artifact_values = set(run_json.get("artifacts", {}).values())
        missing_artifact_refs = [name for name in required_files if name not in artifact_values]
        if missing_artifact_refs:
            add_validation_issue(
                issues,
                "error",
                "run-artifact-map",
                "run.json artifacts map does not reference every required artifact",
                ", ".join(missing_artifact_refs),
            )
        run_driver = run_json.get("driver", {})
        driver_ids = {
            "run.json": run_driver.get("id", ""),
            "execution-plan.json": execution_plan.get("driver", {}).get("id", "") if execution_plan else "",
            "driver-plan.json": driver_plan.get("driver", {}).get("id", "") if driver_plan else "",
            "agent-brief.json": agent_brief.get("driver", {}).get("id", "") if agent_brief else "",
        }
        if run_driver and any(value and value != run_driver.get("id", "") for value in driver_ids.values()):
            add_validation_issue(
                issues,
                "error",
                "driver-id-mismatch",
                "Run artifacts disagree on the driver manifest id",
                ", ".join(f"{name}={value or '(missing)'}" for name, value in driver_ids.items()),
            )
        if run_driver and not all(driver_ids.values()):
            add_validation_issue(
                issues,
                "error",
                "driver-id-missing",
                "Run artifacts must record the driver manifest id",
                ", ".join(name for name, value in driver_ids.items() if not value),
            )

    for payload, schema_key, label in [
        (media_manifest, "media_manifest", "media-manifest.json"),
        (agent_brief, "agent_brief", "agent-brief.json"),
        (execution_plan, "execution_plan", "execution-plan.json"),
        (driver_plan, "driver_plan", "driver-plan.json"),
        (driver_journal, "driver_journal", "driver-journal.json"),
        (driver_handoff, "driver_handoff", "driver-handoff.json"),
        (scenario_outcomes, "scenario_outcomes", "scenario-outcomes.json"),
    ]:
        if payload:
            validate_required_keys(payload, schema[schema_key]["required"], issues, f"{schema_key}-required-keys", label)

    scenarios = run_json.get("scenarios", []) if run_json else []
    scenario_ids = {scenario.get("id", "") for scenario in scenarios}
    scenario_rows = scenarios_json.get("items", []) if scenarios_json else []
    evidence_items = evidence.get("items", []) if evidence else []
    media_items = media_manifest.get("items", []) if media_manifest else []
    outcome_items = scenario_outcomes.get("items", []) if scenario_outcomes else []
    execution_steps = execution_plan.get("steps", []) if execution_plan else []
    driver_scenarios = driver_plan.get("scenarios", []) if driver_plan else []
    driver_events = driver_journal.get("items", []) if driver_journal else []
    brief_scenarios = agent_brief.get("scenario_order", []) if agent_brief else []
    handoff_missing_evidence = driver_handoff.get("missing_evidence", []) if driver_handoff else []
    handoff_missing_proof_evidence = driver_handoff.get("missing_proof_evidence", []) if driver_handoff else []

    if scenarios_json and len(scenario_rows) != len(scenarios):
        add_validation_issue(
            issues,
            "error",
            "scenario-count",
            "scenarios.json item count does not match run.json scenarios",
            f"scenarios.json={len(scenario_rows)}, run.json={len(scenarios)}",
        )
    if run_json:
        missing_scenario_keys = [
            f"{scenario.get('id', 'unknown')}/{key}"
            for scenario in scenarios
            for key in schema["scenario"]["required"]
            if key not in scenario
        ]
        if missing_scenario_keys:
            add_validation_issue(issues, "error", "scenario-required-keys", "run.json scenarios are missing required keys", ", ".join(missing_scenario_keys))
    if scenario_outcomes and len(outcome_items) != len(scenarios):
        add_validation_issue(
            issues,
            "error",
            "scenario-outcome-count",
            "scenario-outcomes.json item count does not match run.json scenarios",
            f"outcomes={len(outcome_items)}, scenarios={len(scenarios)}",
        )
    transport_axis_enabled = bool((run_json or {}).get("transports"))
    expected_plan_entries = len(scenarios)
    if transport_axis_enabled and execution_plan:
        expected_plan_entries = int(execution_plan.get("summary", {}).get("leg_count", len(execution_steps)) or len(execution_steps))
    if execution_plan and len(execution_steps) != expected_plan_entries:
        add_validation_issue(
            issues,
            "error",
            "execution-plan-count",
            "execution-plan.json step count does not match the run's scenario/transport contract",
            f"steps={len(execution_steps)}, expected={expected_plan_entries}",
        )
    if execution_plan:
        validate_final_commands(
            execution_plan.get("finalize_commands", []),
            issues,
            "execution-plan-finalize-commands",
            "execution-plan.json",
        )
        missing_step_keys = [
            f"{step.get('scenario', f'step-{index}')}/{key}"
            for index, step in enumerate(execution_steps, start=1)
            for key in schema["execution_plan"]["step_required"]
            if key not in step
        ]
        if missing_step_keys:
            add_validation_issue(issues, "error", "execution-plan-step-required-keys", "execution-plan.json steps are missing required keys", ", ".join(missing_step_keys))
        stale_attach_commands = []
        invalid_step_routes = []
        for index, step in enumerate(execution_steps, start=1):
            scenario_id = step.get("scenario", f"step-{index}")
            evidence_kinds = [item.get("kind", "") for item in step.get("evidence", []) if item.get("kind", "")]
            commands = step.get("attach_commands", [])
            routes = step.get("capture_routes", [])
            if len(commands) != len(evidence_kinds):
                stale_attach_commands.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(commands)}"
                )
            if len(routes) != len(evidence_kinds):
                invalid_step_routes.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(routes)}"
                )
            for evidence_kind in evidence_kinds:
                matching = [
                    command for command in commands
                    if f"--scenario {scenario_id}" in command and f"--evidence-kind {evidence_kind}" in command
                ]
                if not matching:
                    stale_attach_commands.append(f"{scenario_id}/{evidence_kind}: missing command")
                    continue
                for token in schema["execution_plan"]["attach_command_tokens"]:
                    if token not in matching[0]:
                        stale_attach_commands.append(f"{scenario_id}/{evidence_kind}: command missing {token}")
                route = next((item for item in routes if item.get("evidence_kind") == evidence_kind), {})
                if not route:
                    invalid_step_routes.append(f"{scenario_id}/{evidence_kind}: missing route")
                    continue
                if route.get("scenario") != scenario_id:
                    invalid_step_routes.append(f"{scenario_id}/{evidence_kind}: route scenario={route.get('scenario', '')}")
                if route.get("primary_story") != step.get("primary_story"):
                    invalid_step_routes.append(f"{scenario_id}/{evidence_kind}: route primary_story mismatch")
                if not route.get("setup_entrypoint", {}).get("story_load_intent", "").startswith("load run_dir="):
                    invalid_step_routes.append(f"{scenario_id}/{evidence_kind}: missing story_load_intent")
                route_attach = route.get("commands", {}).get("attach", "")
                if route_attach != matching[0]:
                    invalid_step_routes.append(f"{scenario_id}/{evidence_kind}: route attach command mismatch")
                invalid_step_routes.extend(
                    route_profile_validation_errors(
                        route,
                        f"{scenario_id}/{evidence_kind}",
                        step.get("transport", ""),
                    )
                )
        if stale_attach_commands:
            add_validation_issue(issues, "error", "execution-plan-attach-commands", "execution-plan.json attach commands do not cover the evidence contract", "; ".join(stale_attach_commands))
        if invalid_step_routes:
            add_validation_issue(issues, "error", "execution-plan-capture-routes", "execution-plan.json capture routes do not cover deterministic evidence entrypoints", "; ".join(invalid_step_routes))
    if driver_plan and len(driver_scenarios) != expected_plan_entries:
        add_validation_issue(
            issues,
            "error",
            "driver-plan-count",
            "driver-plan.json scenario/leg count does not match the run's scenario/transport contract",
            f"scenarios={len(driver_scenarios)}, expected={expected_plan_entries}",
        )
    if driver_plan:
        validate_final_commands(
            driver_plan.get("final_gates", []),
            issues,
            "driver-plan-final-gates",
            "driver-plan.json",
        )
        missing_driver_scenario_keys = [
            f"{scenario.get('scenario', f'driver-scenario-{index}')}/{key}"
            for index, scenario in enumerate(driver_scenarios, start=1)
            for key in schema["driver_plan"]["scenario_required"]
            if key not in scenario
        ]
        if missing_driver_scenario_keys:
            add_validation_issue(issues, "error", "driver-plan-scenario-required-keys", "driver-plan.json scenarios are missing required keys", ", ".join(missing_driver_scenario_keys))
        invalid_live_budgets = []
        for index, scenario in enumerate(driver_scenarios, start=1):
            scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
            budget = scenario.get("live_budget", {})
            minutes = budget.get("max_live_minutes")
            if not isinstance(budget, dict):
                invalid_live_budgets.append(f"{scenario_id}: not an object")
                continue
            if not isinstance(minutes, int) or minutes < 0:
                invalid_live_budgets.append(f"{scenario_id}: max_live_minutes={minutes!r}")
            if not str(budget.get("summary", "")).strip():
                invalid_live_budgets.append(f"{scenario_id}: missing summary")
            if budget.get("remaining_action") != "record_blocker":
                invalid_live_budgets.append(f"{scenario_id}: remaining_action={budget.get('remaining_action', '')!r}")
        if invalid_live_budgets:
            add_validation_issue(issues, "error", "driver-plan-live-budget", "driver-plan.json scenarios have invalid live budget metadata", "; ".join(invalid_live_budgets))
        missing_driver_lens_keys = [
            f"{scenario.get('scenario', f'driver-scenario-{index}')}/{key}"
            for index, scenario in enumerate(driver_scenarios, start=1)
            for key in schema["driver_plan"]["persona_lens_required"]
            if key not in scenario.get("persona_lens", {})
        ]
        if missing_driver_lens_keys:
            add_validation_issue(issues, "error", "driver-plan-persona-lens", "driver-plan.json scenarios are missing persona lens keys", ", ".join(missing_driver_lens_keys))
        missing_gate_keys = [
            f"{scenario.get('scenario', f'driver-scenario-{index}')}/{key}"
            for index, scenario in enumerate(driver_scenarios, start=1)
            for key in schema["driver_plan"]["quality_gate_required"]
            if key not in scenario.get("quality_gate", {})
        ]
        if missing_gate_keys:
            add_validation_issue(issues, "error", "driver-plan-quality-gate", "driver-plan.json quality gates are missing required keys", ", ".join(missing_gate_keys))
        invalid_gate_evidence = []
        for scenario in driver_scenarios:
            scenario_id = scenario.get("scenario", "")
            declared = {
                item.get("kind", "")
                for item in scenario.get("evidence", [])
                if item.get("kind", "")
            }
            minimum = set(scenario.get("quality_gate", {}).get("minimum_evidence", []))
            extra = sorted(minimum - declared)
            if extra:
                invalid_gate_evidence.append(f"{scenario_id}: {', '.join(extra)}")
        if invalid_gate_evidence:
            add_validation_issue(issues, "error", "driver-plan-quality-gate-evidence", "Quality gate minimum evidence is not declared by the scenario", "; ".join(invalid_gate_evidence))
        missing_driver_actions = sorted({
            scenario.get("scenario", f"driver-scenario-{index}")
            for index, scenario in enumerate(driver_scenarios, start=1)
            if not scenario.get("driver_actions")
        })
        if missing_driver_actions:
            add_validation_issue(issues, "error", "driver-plan-actions", "driver-plan.json scenarios are missing driver_actions", ", ".join(missing_driver_actions))
        required_action_keys = schema["driver_plan"].get("driver_action_required", [])
        required_action_ids = schema["driver_plan"].get("driver_action_ids", [])
        invalid_action_keys = []
        invalid_action_order = []
        invalid_journal_actions = []
        for index, scenario in enumerate(driver_scenarios, start=1):
            scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
            actions = scenario.get("driver_actions", [])
            action_ids = [action.get("id", "") for action in actions]
            if required_action_ids and action_ids != required_action_ids:
                invalid_action_order.append(
                    f"{scenario_id}: expected={','.join(required_action_ids)} actual={','.join(action_ids)}"
                )
            for action in actions:
                action_id = action.get("id", "action")
                for key in required_action_keys:
                    if key not in action:
                        invalid_action_keys.append(f"{scenario_id}/{action_id}/{key}")
                if action_id == "journal_attempt":
                    journal_tools = " ".join(action.get("tools", []))
                    journal_record = action.get("record", "")
                    if "story.driver_event" not in journal_tools and "--record-driver-event" not in journal_tools:
                        invalid_journal_actions.append(f"{scenario_id}/{action_id}: missing recording tool")
                    if not journal_record.strip():
                        invalid_journal_actions.append(f"{scenario_id}/{action_id}: missing record instruction")
        if invalid_action_keys:
            add_validation_issue(issues, "error", "driver-plan-action-contract", "driver-plan.json driver_actions are missing required keys", ", ".join(invalid_action_keys))
        if invalid_action_order:
            add_validation_issue(issues, "error", "driver-plan-action-order", "driver-plan.json driver_actions do not match the required driver sequence", "; ".join(invalid_action_order))
        if invalid_journal_actions:
            add_validation_issue(issues, "error", "driver-plan-journal-action", "driver-plan.json journal_attempt actions cannot record driver events", "; ".join(invalid_journal_actions))
        missing_resolved_tools = sorted({
            scenario.get("scenario", f"driver-scenario-{index}")
            for index, scenario in enumerate(driver_scenarios, start=1)
            if scenario.get("required_mcp") and not scenario.get("resolved_mcp_tools")
        })
        if missing_resolved_tools:
            add_validation_issue(issues, "error", "driver-plan-resolved-mcp-tools", "driver-plan.json scenarios are missing resolved MCP tool names", ", ".join(missing_resolved_tools))
        unresolved_action_tools = []
        for index, scenario in enumerate(driver_scenarios, start=1):
            scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
            for action in scenario.get("driver_actions", []):
                canonical = [
                    tool for tool in action.get("tools", [])
                    if tool.startswith(("session.", "render.", "visual."))
                ]
                if canonical and not action.get("resolved_tools"):
                    unresolved_action_tools.append(f"{scenario_id}/{action.get('id', 'action')}")
        if unresolved_action_tools:
            add_validation_issue(issues, "error", "driver-plan-action-resolved-tools", "driver-plan.json actions are missing resolved MCP tool names", ", ".join(unresolved_action_tools))
        missing_journal_commands = sorted({
            scenario.get("scenario", f"driver-scenario-{index}")
            for index, scenario in enumerate(driver_scenarios, start=1)
            if "--record-driver-event" not in scenario.get("journal_command", "")
        })
        if missing_journal_commands:
            add_validation_issue(issues, "error", "driver-plan-journal-command", "driver-plan.json scenarios are missing record-driver-event journal commands", ", ".join(missing_journal_commands))
        stale_driver_attach_commands = []
        invalid_driver_routes = []
        for index, scenario in enumerate(driver_scenarios, start=1):
            scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
            scenario_transport = scenario.get("transport", "")
            if scenario_transport:
                expected_profile = compact_transport_profile(transport_profile(scenario_transport)) if scenario_transport in TRANSPORT_PROFILES else {}
                if scenario_transport not in TRANSPORT_PROFILES:
                    invalid_driver_routes.append(f"{scenario_id}: unknown transport={scenario_transport}")
                elif scenario.get("transport_profile", {}) != expected_profile:
                    invalid_driver_routes.append(f"{scenario_id}: transport_profile mismatch for {scenario_transport}")
                elif scenario.get("visual_surface", "") != expected_profile.get("visual_surface", ""):
                    invalid_driver_routes.append(
                        f"{scenario_id}: visual_surface={scenario.get('visual_surface', '')}, expected={expected_profile.get('visual_surface', '')}"
                    )
            evidence_kinds = [item.get("kind", "") for item in scenario.get("evidence", []) if item.get("kind", "")]
            commands = scenario.get("attach_commands", [])
            routes = scenario.get("capture_routes", [])
            if len(commands) != len(evidence_kinds):
                stale_driver_attach_commands.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(commands)}"
                )
            if len(routes) != len(evidence_kinds):
                invalid_driver_routes.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(routes)}"
                )
            for evidence_kind in evidence_kinds:
                matching = [
                    command for command in commands
                    if f"--scenario {scenario_id}" in command and f"--evidence-kind {evidence_kind}" in command
                ]
                if not matching:
                    stale_driver_attach_commands.append(f"{scenario_id}/{evidence_kind}: missing command")
                    continue
                route = next((item for item in routes if item.get("evidence_kind") == evidence_kind), {})
                if not route:
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: missing route")
                    continue
                missing_route_keys = [
                    key for key in schema["driver_plan"].get("capture_route_required", [])
                    if key not in route
                ]
                if missing_route_keys:
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: missing {', '.join(missing_route_keys)}")
                if route.get("scenario") != scenario_id:
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route scenario={route.get('scenario', '')}")
                if route.get("primary_story") != scenario.get("primary_story"):
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route primary_story mismatch")
                if route.get("harness") != scenario.get("harness"):
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route harness mismatch")
                if route.get("commands", {}).get("attach", "") != matching[0]:
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route attach command mismatch")
                if route.get("commands", {}).get("blocker", "") != scenario.get("record_blocker_command", ""):
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route blocker command mismatch")
                if route.get("commands", {}).get("journal", "") != scenario.get("journal_command", ""):
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: route journal command mismatch")
                if not route.get("recording", {}).get("path_template", ""):
                    invalid_driver_routes.append(f"{scenario_id}/{evidence_kind}: missing recording path_template")
                invalid_driver_routes.extend(
                    route_profile_validation_errors(
                        route,
                        f"{scenario_id}/{evidence_kind}",
                        scenario_transport,
                    )
                )
        if stale_driver_attach_commands:
            add_validation_issue(issues, "error", "driver-plan-attach-commands", "driver-plan.json attach commands do not cover the scenario evidence slots", "; ".join(stale_driver_attach_commands))
        if invalid_driver_routes:
            add_validation_issue(issues, "error", "driver-plan-capture-routes", "driver-plan.json capture routes do not define stable setup/recording entrypoints", "; ".join(invalid_driver_routes))
    if driver_journal:
        missing_event_keys = [
            f"{event.get('id', f'event-{index}')}/{key}"
            for index, event in enumerate(driver_events, start=1)
            for key in schema["driver_journal"]["item_required"]
            if key not in event
        ]
        if missing_event_keys:
            add_validation_issue(issues, "error", "driver-journal-event-required-keys", "driver-journal.json events are missing required keys", ", ".join(missing_event_keys))
        invalid_event_modes = sorted({
            event.get("dispatch_mode", "")
            for event in driver_events
            if event.get("dispatch_mode", "") not in schema["driver_journal"]["dispatch_modes"]
        })
        if invalid_event_modes:
            add_validation_issue(issues, "error", "driver-journal-dispatch-mode", "driver-journal.json events use unknown dispatch modes", ", ".join(invalid_event_modes))
        invalid_event_statuses = sorted({
            event.get("status", "")
            for event in driver_events
            if event.get("status", "") not in schema["driver_journal"]["statuses"]
        })
        if invalid_event_statuses:
            add_validation_issue(issues, "error", "driver-journal-status", "driver-journal.json events use unknown statuses", ", ".join(invalid_event_statuses))
        live_budget_minutes = int((run_json or {}).get("live_budget_minutes", 0) or 0)
        forbidden_live_events = sorted({
            event.get("id", f"event-{index}")
            for index, event in enumerate(driver_events, start=1)
            if event.get("dispatch_mode") == "live"
            and event.get("status") != "blocked"
            and live_budget_minutes == 0
        })
        if forbidden_live_events:
            add_validation_issue(
                issues,
                "error",
                "driver-journal-live-budget",
                "driver-journal.json records non-blocked live dispatch while live_budget_minutes is zero",
                ", ".join(forbidden_live_events),
            )
        unknown_driver_scenarios = sorted({
            event.get("scenario", "")
            for event in driver_events
            if event.get("scenario") and event.get("scenario") not in scenario_ids
        })
        if unknown_driver_scenarios:
            add_validation_issue(issues, "error", "driver-journal-scenario", "driver-journal.json events reference unknown scenarios", ", ".join(unknown_driver_scenarios))
        if driver_journal.get("summary", {}).get("events") != len(driver_events):
            add_validation_issue(issues, "error", "driver-journal-summary", "driver-journal.json summary events is stale", f"expected={len(driver_events)}, actual={driver_journal.get('summary', {}).get('events')}")
        unattached_refs = unattached_driver_evidence_refs(evidence or {"items": []}, driver_journal)
        if unattached_refs:
            add_validation_issue(
                issues,
                "error",
                "driver-journal-evidence-refs",
                "driver-journal.json captured evidence refs are not attached in evidence.json",
                ", ".join(unattached_refs),
            )
    if agent_brief and len(brief_scenarios) != expected_plan_entries:
        add_validation_issue(
            issues,
            "error",
            "agent-brief-count",
            "agent-brief.json scenario order count does not match the run's scenario/transport contract",
            f"scenario_order={len(brief_scenarios)}, expected={expected_plan_entries}",
        )
    if agent_brief:
        validate_final_commands(
            agent_brief.get("finalize_commands", []),
            issues,
            "agent-brief-finalize-commands",
            "agent-brief.json",
        )
        missing_brief_lens_keys = [
            key for key in schema["agent_brief"]["persona_lens_required"]
            if key not in agent_brief.get("persona_contract", {}).get("lens", {})
        ]
        if missing_brief_lens_keys:
            add_validation_issue(issues, "error", "agent-brief-persona-lens", "agent-brief.json persona_contract is missing lens keys", ", ".join(missing_brief_lens_keys))
        missing_brief_scenario_keys = [
            f"{scenario.get('id', f'brief-scenario-{index}')}/{key}"
            for index, scenario in enumerate(brief_scenarios, start=1)
            for key in schema["agent_brief"]["scenario_required"]
            if key not in scenario
        ]
        if missing_brief_scenario_keys:
            add_validation_issue(issues, "error", "agent-brief-scenario-required-keys", "agent-brief.json scenarios are missing required keys", ", ".join(missing_brief_scenario_keys))
    if driver_handoff:
        validate_final_commands(
            driver_handoff.get("finalize_commands", []),
            issues,
            "driver-handoff-finalize-commands",
            "driver-handoff.json",
        )
        missing_status_keys = [
            key for key in schema["driver_handoff"]["status_required"]
            if key not in driver_handoff.get("status", {})
        ]
        if missing_status_keys:
            add_validation_issue(issues, "error", "driver-handoff-status", "driver-handoff.json status is missing required keys", ", ".join(missing_status_keys))
        missing_input_keys = [
            key for key in schema["driver_handoff"]["inputs_required"]
            if key not in driver_handoff.get("inputs", {})
        ]
        if missing_input_keys:
            add_validation_issue(issues, "error", "driver-handoff-inputs", "driver-handoff.json inputs are missing required keys", ", ".join(missing_input_keys))
        missing_input_files = [
            f"{key}:{path}"
            for key, path in driver_handoff.get("inputs", {}).items()
            if path and not (run_dir / path).exists()
        ]
        if missing_input_files:
            add_validation_issue(issues, "error", "driver-handoff-input-files", "driver-handoff.json inputs point at missing run files", ", ".join(missing_input_files))
        dispatch_modes = [
            item.get("mode", "")
            for item in driver_handoff.get("dispatch_modes", [])
            if isinstance(item, dict)
        ]
        missing_dispatch_modes = sorted(set(schema["driver_handoff"]["dispatch_modes"]) - set(dispatch_modes))
        if missing_dispatch_modes:
            add_validation_issue(issues, "error", "driver-handoff-dispatch-modes", "driver-handoff.json is missing required dispatch modes", ", ".join(missing_dispatch_modes))
        if driver_plan and driver_handoff.get("driver_agent") != driver_plan.get("driver_agent"):
            add_validation_issue(issues, "error", "driver-handoff-driver-agent", "driver-handoff.json driver does not match driver-plan.json", f"handoff={driver_handoff.get('driver_agent')}, driver_plan={driver_plan.get('driver_agent')}")
        if run_json and driver_handoff.get("run_id") != run_json.get("run_id"):
            add_validation_issue(issues, "error", "driver-handoff-run-id", "driver-handoff.json run_id does not match run.json", f"handoff={driver_handoff.get('run_id')}, run={run_json.get('run_id')}")
        if review and driver_handoff.get("status", {}).get("review_status") != review.get("status"):
            add_validation_issue(issues, "error", "driver-handoff-review-status", "driver-handoff.json review status is stale", f"handoff={driver_handoff.get('status', {}).get('review_status')}, review={review.get('status')}")
        if metrics:
            for key in ["present_evidence_count", "required_evidence_count", "proof_evidence_count", "findings_count"]:
                if driver_handoff.get("status", {}).get(key) != metrics.get(key):
                    add_validation_issue(issues, "error", "driver-handoff-metrics", f"driver-handoff.json {key} is stale or inconsistent", f"expected={metrics.get(key)}, actual={driver_handoff.get('status', {}).get(key)}")
        actual_missing_count = len([
            item for item in evidence_items
            if item.get("status") == "missing"
        ])
        if driver_handoff.get("status", {}).get("missing_evidence_count") != actual_missing_count:
            add_validation_issue(issues, "error", "driver-handoff-missing-count", "driver-handoff.json missing evidence count is stale", f"expected={actual_missing_count}, actual={driver_handoff.get('status', {}).get('missing_evidence_count')}")
        if len(handoff_missing_evidence) != actual_missing_count:
            add_validation_issue(issues, "error", "driver-handoff-missing-list", "driver-handoff.json missing evidence list is stale", f"expected={actual_missing_count}, actual={len(handoff_missing_evidence)}")
        expected_proof_gaps = proof_gap_rows(run_json or {"scenarios": []}, evidence or {"items": []})
        expected_missing_proof_count = sum(len(row["missing_proof_evidence"]) for row in expected_proof_gaps)
        expected_minimum_count = sum(
            len(scenario_quality_gate(scenario.get("id", "")).get("minimum_evidence", []))
            for scenario in scenarios
        )
        expected_proof_minimum_count = expected_minimum_count - expected_missing_proof_count
        if len(handoff_missing_proof_evidence) != len(expected_proof_gaps):
            add_validation_issue(issues, "error", "driver-handoff-proof-gap-list", "driver-handoff.json missing proof evidence list is stale", f"expected={len(expected_proof_gaps)}, actual={len(handoff_missing_proof_evidence)}")
        expected_proof_by_scenario = {
            row.get("scenario", ""): row
            for row in expected_proof_gaps
        }
        actual_proof_by_scenario = {
            row.get("scenario", ""): row
            for row in handoff_missing_proof_evidence
        }
        stale_proof_rows = []
        missing_slot_details = []
        for scenario_id, expected_row in expected_proof_by_scenario.items():
            actual_row = actual_proof_by_scenario.get(scenario_id, {})
            expected_missing = expected_row.get("missing_proof_evidence", [])
            actual_missing = actual_row.get("missing_proof_evidence", [])
            if actual_missing != expected_missing:
                stale_proof_rows.append(
                    f"{scenario_id}: expected={', '.join(expected_missing)}, actual={', '.join(actual_missing)}"
                )
            slots = actual_row.get("slots", [])
            slot_by_kind = {slot.get("kind", ""): slot for slot in slots if isinstance(slot, dict)}
            for kind in expected_missing:
                slot = slot_by_kind.get(kind, {})
                missing_keys = [
                    key for key in schema["driver_handoff"]["missing_proof_slot_required"]
                    if not slot.get(key)
                ]
                if missing_keys:
                    missing_slot_details.append(f"{scenario_id}/{kind}: {', '.join(missing_keys)}")
                command = slot.get("attach_command", "")
                for token in ["--attach-evidence", f"--scenario {scenario_id}", f"--evidence-kind {kind}", "--evidence-source <retained|external|local|cassette>"]:
                    if command and token not in command:
                        missing_slot_details.append(f"{scenario_id}/{kind}: attach_command missing {token}")
                route = slot.get("capture_route", {})
                if not isinstance(route, dict) or not route:
                    missing_slot_details.append(f"{scenario_id}/{kind}: missing capture_route")
                else:
                    if route.get("scenario") != scenario_id:
                        missing_slot_details.append(f"{scenario_id}/{kind}: route scenario={route.get('scenario', '')}")
                    if route.get("evidence_kind") != kind:
                        missing_slot_details.append(f"{scenario_id}/{kind}: route evidence_kind={route.get('evidence_kind', '')}")
                    if route.get("commands", {}).get("attach", "") != command:
                        missing_slot_details.append(f"{scenario_id}/{kind}: route attach command mismatch")
                    if not route.get("setup_entrypoint", {}).get("primary_session", ""):
                        missing_slot_details.append(f"{scenario_id}/{kind}: route missing primary_session")
                    if not route.get("recording", {}).get("path_template", ""):
                        missing_slot_details.append(f"{scenario_id}/{kind}: route missing recording path_template")
                    missing_slot_details.extend(
                        route_profile_validation_errors(route, f"{scenario_id}/{kind}")
                    )
        if stale_proof_rows:
            add_validation_issue(issues, "error", "driver-handoff-proof-gap-detail", "driver-handoff.json missing proof evidence details are stale", "; ".join(stale_proof_rows))
        if missing_slot_details:
            add_validation_issue(issues, "error", "driver-handoff-proof-slot-detail", "driver-handoff.json missing proof evidence slots are not actionable", "; ".join(missing_slot_details))
        for key, expected in [
            ("missing_proof_evidence_count", expected_missing_proof_count),
            ("minimum_evidence_count", expected_minimum_count),
            ("proof_minimum_evidence_count", expected_proof_minimum_count),
        ]:
            if driver_handoff.get("status", {}).get(key) != expected:
                add_validation_issue(issues, "error", "driver-handoff-proof-metrics", f"driver-handoff.json {key} is stale or inconsistent", f"expected={expected}, actual={driver_handoff.get('status', {}).get(key)}")
        if driver_plan and driver_handoff.get("finalize_commands") != driver_plan.get("final_gates"):
            add_validation_issue(issues, "error", "driver-handoff-final-gates", "driver-handoff.json finalize commands do not match driver-plan final gates")
        if not driver_handoff.get("suggested_prompt", "").strip():
            add_validation_issue(issues, "error", "driver-handoff-prompt", "driver-handoff.json suggested_prompt is empty")
        else:
            handoff_prompt = driver_handoff.get("suggested_prompt", "")
            if "last_result.next_driver_capture" not in handoff_prompt:
                add_validation_issue(issues, "error", "driver-handoff-prompt", "driver-handoff.json suggested_prompt does not mention next_driver_capture")
            if "last_result.next_driver_capture_route" not in handoff_prompt:
                add_validation_issue(issues, "error", "driver-handoff-prompt", "driver-handoff.json suggested_prompt does not mention next_driver_capture_route")
            if "last_result.next_driver_attach_command" not in handoff_prompt:
                add_validation_issue(issues, "error", "driver-handoff-prompt", "driver-handoff.json suggested_prompt does not mention next_driver_attach_command")
            if "last_result.next_driver_blocker_command" not in handoff_prompt:
                add_validation_issue(issues, "error", "driver-handoff-prompt", "driver-handoff.json suggested_prompt does not mention next_driver_blocker_command")

    if run_json and driver_plan and driver_handoff:
        summary = summarize_run_bundle(run_dir)
        summary_scenarios = summary.get("driver_scenarios", [])
        summary_missing_proof = summary.get("missing_proof_evidence", [])
        summary_final_gates = summary.get("driver_final_gates", [])
        summary_contract = summary.get("driver_contract_summary", "")
        summary_next_capture = summary.get("next_driver_capture", "")
        summary_next_capture_route = summary.get("next_driver_capture_route", {})
        summary_next_attach_command = summary.get("next_driver_attach_command", "")
        summary_next_blocker_command = summary.get("next_driver_blocker_command", "")
        if len(summary_scenarios) != len(driver_scenarios):
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-contract",
                "summarize-run driver_scenarios count does not match driver-plan.json",
                f"expected={len(driver_scenarios)}, actual={len(summary_scenarios)}",
            )
        if len(summary_missing_proof) != len(handoff_missing_proof_evidence):
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-contract",
                "summarize-run missing_proof_evidence count does not match driver-handoff.json",
                f"expected={len(handoff_missing_proof_evidence)}, actual={len(summary_missing_proof)}",
            )
        if summary_final_gates != driver_plan.get("final_gates", []):
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-contract",
                "summarize-run driver_final_gates do not match driver-plan.json",
            )
        if review and summary.get("review_status") != review.get("status"):
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-contract-review",
                "summarize-run review_status does not match review.json",
                f"expected={review.get('status')}, actual={summary.get('review_status')}",
            )
        expected_next_capture = build_next_driver_capture(driver_handoff)
        if summary_next_capture != expected_next_capture:
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-next-capture",
                "summarize-run next_driver_capture does not match driver-handoff.json",
                f"expected={expected_next_capture}, actual={summary_next_capture}",
            )
        expected_next_attach_command = next_driver_capture_slot(driver_handoff).get("attach_command", "")
        if summary_next_attach_command != expected_next_attach_command:
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-next-attach-command",
                "summarize-run next_driver_attach_command does not match driver-handoff.json",
                f"expected={expected_next_attach_command}, actual={summary_next_attach_command}",
            )
        expected_next_capture_route = next_driver_capture_route(driver_handoff)
        if summary_next_capture_route != expected_next_capture_route:
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-next-capture-route",
                "summarize-run next_driver_capture_route does not match driver-handoff.json",
                f"expected={expected_next_capture_route.get('route_id', '')}, actual={summary_next_capture_route.get('route_id', '') if isinstance(summary_next_capture_route, dict) else type(summary_next_capture_route).__name__}",
            )
        expected_next_blocker_command = next_driver_blocker_command(driver_handoff)
        if summary_next_blocker_command != expected_next_blocker_command:
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-next-blocker-command",
                "summarize-run next_driver_blocker_command does not match driver-handoff.json",
                f"expected={expected_next_blocker_command}, actual={summary_next_blocker_command}",
            )
        missing_summary_tokens = [
            token for token in [
                "Driver contract:",
                "last_result.driver_scenarios",
                "last_result.next_driver_capture_route",
                "last_result.missing_proof_evidence",
                "last_result.driver_final_gates",
            ]
            if token not in summary_contract
        ]
        if missing_summary_tokens:
            add_validation_issue(
                issues,
                "error",
                "loaded-driver-contract-summary",
                "summarize-run driver_contract_summary does not point drivers at the MCP-visible contract",
                ", ".join(missing_summary_tokens),
            )

    required_evidence = {
        (item.get("scenario", ""), item.get("kind", ""))
        for item in evidence_items
    }
    declared_evidence = {
        (scenario.get("id", ""), evidence_kind)
        for scenario in scenarios
        for evidence_kind in scenario.get("evidence", [])
    }
    if declared_evidence - required_evidence:
        missing = sorted(f"{scenario}/{kind}" for scenario, kind in declared_evidence - required_evidence)
        add_validation_issue(issues, "error", "evidence-contract", "evidence.json is missing declared scenario evidence slots", ", ".join(missing))
    if required_evidence - declared_evidence:
        extra = sorted(f"{scenario}/{kind}" for scenario, kind in required_evidence - declared_evidence)
        add_validation_issue(issues, "warn", "evidence-contract-extra", "evidence.json has slots not declared by run.json scenarios", ", ".join(extra))

    if run_json:
        missing_playback_kind = sorted(
            scenario.get("id", "unknown")
            for scenario in scenarios
            if scenario.get("source") != "mined" and not scenario_playback_kind(scenario)
        )
        if missing_playback_kind:
            add_validation_issue(
                issues,
                "error",
                "scenario-playback-evidence-kind",
                "run.json scenarios must declare exactly one playback-capable evidence kind (rrweb|trace-replay|flow-fixture|png-sequence)",
                ", ".join(missing_playback_kind),
            )
        # Demo-seeded placeholder evidence (source == "demo") never claims to
        # be real proof anywhere else in this validator (see
        # non-demo-evidence / artifact-ref-exists), so it is exempt here too —
        # this check targets a captured/validated item that DOES claim a real
        # local backing (local/cassette/etc.) but is not actually backed.
        unbacked_playback_evidence = sorted(
            f"{item.get('scenario', '')}/{item.get('kind', '')}:{item.get('path', '')}"
            for item in evidence_items
            if item.get("kind", "") in PLAYBACK_EVIDENCE_KINDS
            and item.get("status") in {"captured", "validated"}
            and item.get("source", "") != "demo"
            and not is_playback_evidence(item, run_dir)
        )
        if unbacked_playback_evidence:
            add_validation_issue(
                issues,
                "error",
                "playback-evidence-unbacked",
                "Playback-capable evidence must be a real LOCAL file, not a cassette:// or other unbacked/opaque reference",
                ", ".join(unbacked_playback_evidence),
            )

    schema_evidence_sources = set(schema["evidence_sources"])
    invalid_evidence_sources = sorted({
        item.get("source", "")
        for item in evidence_items
        if item.get("source", "") not in schema_evidence_sources
    })
    if invalid_evidence_sources:
        add_validation_issue(issues, "error", "evidence-source", "evidence.json uses unknown evidence sources", ", ".join(invalid_evidence_sources))
    unknown_present_evidence = sorted({
        f"{item.get('scenario', '')}/{item.get('kind', '')}"
        for item in evidence_items
        if item.get("status") in {"captured", "validated"} and item.get("source", "") == "unknown"
    })
    if unknown_present_evidence:
        add_validation_issue(issues, "warn", "evidence-source-unknown", "Captured evidence has unknown source and does not count as proof evidence", ", ".join(unknown_present_evidence))

    driver_ids = {item.get("scenario", "") for item in driver_scenarios}
    if driver_plan and driver_ids != scenario_ids:
        missing = sorted(scenario_ids - driver_ids)
        extra = sorted(driver_ids - scenario_ids)
        detail = f"missing={', '.join(missing) or 'none'}; extra={', '.join(extra) or 'none'}"
        add_validation_issue(issues, "error", "driver-plan-scenarios", "driver-plan.json scenarios do not match run.json scenarios", detail)

    unknown_scenario_refs = sorted({
        item.get("scenario", "")
        for item in [*evidence_items, *media_items, *outcome_items]
        if item.get("scenario", "") and item.get("scenario", "") not in scenario_ids
    })
    if unknown_scenario_refs:
        add_validation_issue(issues, "error", "unknown-scenario-ref", "Artifacts reference unknown scenarios", ", ".join(unknown_scenario_refs))

    present_evidence = {
        (item.get("scenario", ""), item.get("kind", ""), item.get("path", ""))
        for item in evidence_items
        if item.get("status") in {"captured", "validated"} and item.get("path")
    }
    media_refs = {
        (item.get("scenario", ""), item.get("evidence_kind", ""), item.get("path", ""))
        for item in media_items
    }
    if present_evidence - media_refs:
        missing = sorted(f"{scenario}/{kind}:{path}" for scenario, kind, path in present_evidence - media_refs)
        add_validation_issue(issues, "error", "media-manifest-coverage", "media-manifest.json is missing captured evidence items", ", ".join(missing))
    missing_artifact_refs = missing_local_artifact_refs(run_dir, evidence_items)
    if missing_artifact_refs:
        add_validation_issue(
            issues,
            "warn",
            "artifact-ref-exists",
            "Captured evidence paths do not resolve locally and are not retained/external references",
            ", ".join(missing_artifact_refs),
        )

    schema_media_kinds = set(schema["media_manifest"]["media_kinds"])
    invalid_media_kinds = sorted({
        item.get("media_kind", "")
        for item in media_items
        if item.get("media_kind", "") not in schema_media_kinds
    })
    if invalid_media_kinds:
        add_validation_issue(issues, "error", "media-kind", "media-manifest.json uses unknown media kinds", ", ".join(invalid_media_kinds))

    schema_outcomes = set(schema["scenario_outcomes"]["outcomes"])
    invalid_outcomes = sorted({
        item.get("outcome", "")
        for item in outcome_items
        if item.get("outcome", "") not in schema_outcomes
    })
    if invalid_outcomes:
        add_validation_issue(issues, "error", "scenario-outcome-kind", "scenario-outcomes.json uses unknown outcome values", ", ".join(invalid_outcomes))

    expected_metrics = {
        "scenario_count": len(scenarios),
        "required_evidence_count": len(evidence_items),
        "present_evidence_count": len([
            item for item in evidence_items if item.get("status") in {"captured", "validated"}
        ]),
    }
    for key, expected in expected_metrics.items():
        if metrics and metrics.get(key) != expected:
            add_validation_issue(issues, "error", "metrics-consistency", f"metrics.json {key} is stale or inconsistent", f"expected={expected}, actual={metrics.get(key)}")

    if review and review.get("status") not in schema["review_statuses"]:
        add_validation_issue(issues, "error", "review-status", "review.json has an unknown status", review.get("status", ""))
    if review:
        review_checks = review.get("checks", [])
        invalid_check_statuses = sorted({
            check.get("status", "")
            for check in review_checks
            if check.get("status", "") not in schema["review_check_statuses"]
        })
        if invalid_check_statuses:
            add_validation_issue(issues, "error", "review-check-status", "review.json has unknown check statuses", ", ".join(invalid_check_statuses))
        expected_review_checks = set(schema.get("review_check_ids", []))
        actual_review_checks = {
            check.get("id", "")
            for check in review_checks
            if check.get("id", "")
        }
        missing_review_checks = sorted(expected_review_checks - actual_review_checks)
        extra_review_checks = sorted(actual_review_checks - expected_review_checks)
        if missing_review_checks:
            add_validation_issue(issues, "error", "review-check-contract", "review.json is missing required review checks", ", ".join(missing_review_checks))
        if extra_review_checks:
            add_validation_issue(issues, "warn", "review-check-extra", "review.json has checks outside the schema contract", ", ".join(extra_review_checks))
        expected_review_counts = {
            "passed": sum(1 for check in review_checks if check.get("status") == "pass"),
            "warned": sum(1 for check in review_checks if check.get("status") == "warn"),
            "failed": sum(1 for check in review_checks if check.get("status") == "fail"),
            "total": len(review_checks),
        }
        for key, expected in expected_review_counts.items():
            actual = review.get("summary_counts", {}).get(key)
            if actual != expected:
                add_validation_issue(issues, "error", "review-summary-counts", f"review.json summary_counts.{key} is stale or inconsistent", f"expected={expected}, actual={actual}")
        expected_review_status = "ready" if expected_review_counts["failed"] == 0 else "needs_evidence"
        if review.get("status") != expected_review_status:
            add_validation_issue(issues, "error", "review-status-consistency", "review.json status does not match failed review checks", f"expected={expected_review_status}, actual={review.get('status')}")
        if metrics:
            for key, expected in [
                ("review_passed_checks", expected_review_counts["passed"]),
                ("review_total_checks", expected_review_counts["total"]),
                ("review_status", review.get("status")),
            ]:
                if metrics.get(key) != expected:
                    add_validation_issue(issues, "error", "metrics-review-consistency", f"metrics.json {key} is stale or inconsistent with review.json", f"expected={expected}, actual={metrics.get(key)}")

    # GitHub/autonomous-fix contract: credible issue findings are not final
    # review artifacts until the story-owned native path files them and drains
    # fix jobs with human-review evidence.
    findings_json = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {}
    filing = findings_json.get("filing", {}) if findings_json else {}
    credible_findings = credible_issue_findings(findings_json)
    credible_findings_needing_github = credible_findings_requiring_github(findings_json)
    unfiled = unfiled_credible_findings(findings_json)
    driver_receipt_gaps = credible_issue_driver_receipt_gaps(
        findings_json,
        driver_journal or {"items": []},
        evidence or {"items": []},
        run_dir,
    )
    weakness_routes = read_json(run_dir / "weakness-routes.json") if (run_dir / "weakness-routes.json").exists() else {}
    prd_design_intake = prd_design_intake if isinstance(prd_design_intake, dict) else {}
    open_weakness_ids = {
        item.get("id") or f"weakness-{index}"
        for index, item in enumerate(open_weakness_findings(findings_json), start=1)
    }
    routed_weakness_ids = {
        item.get("finding_id", "")
        for item in weakness_routes.get("items", [])
        if isinstance(item, dict)
        and item.get("target_pipeline") == "prd-design"
        and item.get("target_story") == "stories/prd"
    }
    missing_weakness_routes = sorted(open_weakness_ids - routed_weakness_ids)
    intake_weakness_ids = {
        item.get("finding_id", "")
        for item in prd_design_intake.get("items", [])
        if isinstance(item, dict)
        and item.get("target_pipeline") == "prd-design"
        and item.get("target_story") == "stories/prd"
        and item.get("story_intent") == "start"
        and item.get("story_slots", {}).get("idea")
        and item.get("story_slots", {}).get("upstream_paths")
        and item.get("persona_lens", {}).get("starting_surface")
        and item.get("persona_lens", {}).get("evidence_emphasis")
    }
    missing_prd_intake = sorted(open_weakness_ids - intake_weakness_ids)
    if missing_weakness_routes:
        add_validation_issue(
            issues,
            "error",
            "weakness-routing",
            "Open observed weakness findings require PRD/design route artifacts",
            ", ".join(missing_weakness_routes),
        )
    if weakness_routes and weakness_routes.get("summary", {}).get("routed") != len(routed_weakness_ids):
        add_validation_issue(
            issues,
            "error",
            "weakness-routing",
            "weakness-routes.json summary is stale or inconsistent",
            f"summary.routed={weakness_routes.get('summary', {}).get('routed')}, actual={len(routed_weakness_ids)}",
        )
    if missing_prd_intake:
        add_validation_issue(
            issues,
            "error",
            "prd-design-intake",
            "Open observed weakness findings require PRD/design intake artifacts with persona lens and story start slots",
            ", ".join(missing_prd_intake),
        )
    if prd_design_intake and prd_design_intake.get("summary", {}).get("intake_count") != len(intake_weakness_ids):
        add_validation_issue(
            issues,
            "error",
            "prd-design-intake",
            "prd-design-intake.json summary is stale or inconsistent",
            f"summary.intake_count={prd_design_intake.get('summary', {}).get('intake_count')}, actual={len(intake_weakness_ids)}",
        )
    if unfiled:
        add_validation_issue(
            issues,
            "error",
            "findings-filed",
            "Credible issue findings require story-owned autonomous GitHub filing before review",
            ", ".join(unfiled),
        )
    if driver_receipt_gaps:
        add_validation_issue(
            issues,
            "error",
            "credible-issue-driver-receipts",
            "Credible issue findings require captured or validated driver receipts with proof evidence before autonomous fixing",
            ", ".join(driver_receipt_gaps),
        )
    if filing.get("requested"):
        if filing.get("failed"):
            add_validation_issue(
                issues,
                "warn",
                "findings-filing-failures",
                "The last findings filing run reported failures; re-run --file-findings",
                f"failed={filing.get('failed')}",
            )

    validate_slidey_deck_shape(deck, media_manifest, issues)
    scene_eyebrows = deck_scene_eyebrows(deck)
    for expected in ["Persona lens", "Driver plan", "Driver contract", "Video playback", "Scenario outcomes", "Finding matrix", "PRD/design routes", "GH-agent fixes", "Proof gates"]:
        if deck and expected not in scene_eyebrows:
            add_validation_issue(issues, "error", "deck-scene", "deck.slidey.json is missing a required review scene", expected)
    prd_route_bodies = [
        str(scene.get("body", ""))
        for scene in deck.get("scenes", [])
        if isinstance(scene, dict) and scene.get("eyebrow") == "PRD/design routes"
    ] if deck else []
    missing_route_deck_tokens = [
        item.get("finding_id", "")
        for item in weakness_routes.get("items", [])
        if isinstance(item, dict)
        and item.get("finding_id")
        and not any(item.get("finding_id", "") in body for body in prd_route_bodies)
    ]
    if missing_route_deck_tokens:
        add_validation_issue(
            issues,
            "error",
            "weakness-routing-deck",
            "PRD/design route findings are missing from the review deck",
            ", ".join(missing_route_deck_tokens[:5]),
        )
    gh_agent = findings_json.get("gh_agent", {}) if isinstance(findings_json.get("gh_agent", {}), dict) else {}
    gh_agent_requested = gh_agent.get("enqueue_status", "") not in {"", "disabled", "dry-run"}
    if credible_findings_needing_github and not gh_agent_requested:
        add_validation_issue(
            issues,
            "error",
            "gh-agent-fixes",
            "Credible issue findings require the native autonomous_fix gate or a local-artifact ticket before final validation",
            f"credible={len(credible_findings_needing_github)}",
        )
        add_validation_issue(
            issues,
            "error",
            "autonomous-fix-report",
            "Credible issue findings require an autonomous-fix report with issue, run, and evidence links, or a local-artifact ticket",
            f"credible={len(credible_findings_needing_github)}",
        )
    if gh_agent_requested:
        enqueued = int(gh_agent.get("enqueued_count", 0) or 0)
        done = int(gh_agent.get("done_count", 0) or 0)
        failed = int(gh_agent.get("failed_count", 0) or 0)
        active = int(gh_agent.get("active_count", 0) or 0)
        if enqueued == 0 or gh_agent.get("drain_status") != "drained" or failed or active or done < enqueued:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-fixes",
                "gh-agent fixing was requested but queued fixes did not drain to successful reviewable runs",
                f"enqueue={gh_agent.get('enqueue_status', '')}, drain={gh_agent.get('drain_status', '')}, enqueued={enqueued}, done={done}, failed={failed}, active={active}",
            )
        missing_evidence = gh_agent_missing_fix_evidence(gh_agent)
        if missing_evidence:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-fix-evidence",
                "Completed gh-agent fix jobs are missing reviewable fix evidence assets",
                ", ".join(missing_evidence[:5]),
            )
        missing_triage = gh_agent_missing_triage_evidence(gh_agent)
        if missing_triage:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-triage-evidence",
                "Completed gh-agent fix jobs are missing triage preflight verdict artifacts",
                ", ".join(missing_triage[:5]),
            )
        missing_verify = gh_agent_missing_independent_verify(gh_agent)
        if missing_verify:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-independent-verify",
                "Completed gh-agent fix jobs are missing story-owned independent verification artifacts",
                ", ".join(missing_verify[:5]),
            )
        missing_run_urls = gh_agent_missing_run_urls(gh_agent)
        if missing_run_urls:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-run-url",
                "Completed gh-agent fix jobs are missing reviewable run URLs",
                ", ".join(missing_run_urls[:5]),
            )
        missing_landing = gh_agent_missing_integration_landing(gh_agent)
        if missing_landing:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-integration-landing",
                "Completed gh-agent fix jobs are missing integration branch or commit landing proof",
                ", ".join(missing_landing[:5]),
            )
        issue_closeout_ok, issue_closeout_detail = issue_closeout_gate(findings_json, gh_agent_requested, credible_findings_needing_github)
        if not issue_closeout_ok:
            add_validation_issue(
                issues,
                "error",
                "issue-closeout",
                "Autonomous fixes completed but filed GitHub issues were not closed with kitsoki-fixed-in close-out evidence",
                issue_closeout_detail,
            )
        missing_report_tokens = missing_autonomous_fix_report_tokens(run_dir, findings_json)
        if missing_report_tokens:
            add_validation_issue(
                issues,
                "error",
                "autonomous-fix-report",
                "Autonomous fix report is missing watchdog proof, hosted-agent readiness proof, filed issue links, run links, or evidence links required for human review",
                ", ".join(missing_report_tokens[:5]),
            )
        scene_bodies = [
            str(scene.get("body", ""))
            for scene in deck.get("scenes", [])
            if isinstance(scene, dict) and scene.get("eyebrow") == "GH-agent fixes"
        ] if deck else []
        expected_tokens = [
            job.get("run_url", "")
            for job in gh_agent.get("drained_jobs", [])
            if isinstance(job, dict) and job.get("run_url")
        ]
        for job in gh_agent.get("drained_jobs", []):
            if not isinstance(job, dict):
                continue
            expected_tokens.extend([
                gh_agent_job_integration_branch(job),
                gh_agent_job_commit_sha(job),
                gh_agent_job_commit_url(job),
            ])
        original_issue_evidence = filed_issue_evidence_links(findings_json)
        if original_issue_evidence:
            expected_tokens.append("issue_evidence=")
            expected_tokens.extend(original_issue_evidence)
        expected_tokens.append("autonomous-fix-report.md")
        expected_tokens.extend(
            item.get("comment_url", "")
            for item in gh_agent.get("claims", []) or []
            if isinstance(item, dict) and item.get("comment_url")
        )
        if gh_agent.get("claim_status"):
            expected_tokens.append(f"Claims: {gh_agent.get('claim_status')}")
        expected_tokens.extend(
            link
            for job in gh_agent.get("drained_jobs", [])
            if isinstance(job, dict)
            for link in gh_agent_job_evidence_links(job)
        )
        if gh_agent_triage_evidence_links(gh_agent):
            expected_tokens.append("triage=")
        if gh_agent_independent_verify_links(gh_agent):
            expected_tokens.append("independent_verify=")
        issue_closeout = findings_json.get("issue_closeout", {}) if isinstance(findings_json.get("issue_closeout", {}), dict) else {}
        if issue_closeout:
            expected_tokens.append(f"Issue close-out: {issue_closeout.get('status', '')}")
            expected_tokens.extend(
                item.get("comment_url", "")
                for item in issue_closeout.get("items", []) or []
                if isinstance(item, dict) and item.get("comment_url")
            )
        missing_tokens = [
            token for token in expected_tokens
            if token and not any(token in body for body in scene_bodies)
        ]
        if missing_tokens:
            add_validation_issue(
                issues,
                "error",
                "gh-agent-fix-deck",
                "GH-agent fix, evidence, or issue close-out links are missing from the review deck",
                ", ".join(missing_tokens[:5]),
            )
    playback_count = media_manifest.get("summary", {}).get("playback_items", 0) if media_manifest else 0
    video_scenes = [
        scene for scene in deck.get("scenes", [])
        if isinstance(scene, dict) and scene.get("eyebrow") == "Video playback"
    ] if deck else []
    if playback_count and not any(scene.get("media") for scene in video_scenes):
        add_validation_issue(issues, "error", "deck-media", "Video playback scene has no media entries despite manifest playback items", f"playback_items={playback_count}")
    embeddable_playback_count = len([
        item for item in media_items
        if item.get("playback") and playback_scene_for_item(item) is not None
    ])
    playback_evidence_scenes = [
        scene for scene in deck.get("scenes", [])
        if isinstance(scene, dict) and scene.get("eyebrow") == "Playback evidence"
    ] if deck else []
    if embeddable_playback_count and len(playback_evidence_scenes) < min(embeddable_playback_count, 6):
        add_validation_issue(
            issues,
            "error",
            "deck-playback-scenes",
            "deck.slidey.json is missing standalone playback evidence scenes",
            f"expected={min(embeddable_playback_count, 6)}, actual={len(playback_evidence_scenes)}",
        )

    errors = sum(1 for issue in issues if issue["severity"] == "error")
    warnings = sum(1 for issue in issues if issue["severity"] == "warn")
    return {
        "status": "valid" if errors == 0 else "invalid",
        "run_dir": str(run_dir),
        "checked_artifacts": len(required_files),
        "errors": errors,
        "warnings": warnings,
        "validation_issue_summary": validation_issue_summary(issues),
        "issues": issues,
    }


def validate_matrix_bundle(matrix_dir: Path, strict_target_proof: bool = False) -> dict:
    schema = read_json(SCHEMA)
    issues: list[dict] = []
    required_files = schema["matrix_result"]["artifacts"]
    for name in required_files:
        if not (matrix_dir / name).exists():
            add_validation_issue(issues, "error", "required-file", "Required matrix artifact is missing", name)

    matrix = load_json_for_validation(matrix_dir / "matrix.json", issues)
    deck = load_json_for_validation(matrix_dir / "deck.slidey.json", issues)
    if matrix:
        validate_required_keys(matrix, schema["matrix_result"]["required"], issues, "matrix-required-keys", "matrix.json")
        if matrix.get("target_count") != schema["matrix_result"]["target_count"]:
            add_validation_issue(
                issues,
                "error",
                "matrix-target-count",
                "matrix target count does not match the 10-repo contract",
                f"expected={schema['matrix_result']['target_count']}, actual={matrix.get('target_count')}",
            )
        if matrix.get("target_count") != len(matrix.get("targets", [])):
            add_validation_issue(issues, "error", "matrix-target-list", "matrix target_count does not match targets length", f"target_count={matrix.get('target_count')}, targets={len(matrix.get('targets', []))}")
        targets_without_proof = [
            target.get("id", f"target-{index}")
            for index, target in enumerate(matrix.get("targets", []), start=1)
            if not target.get("selection_proof")
        ]
        if targets_without_proof:
            add_validation_issue(
                issues,
                "error" if strict_target_proof else "warn",
                "matrix-target-proof",
                "Matrix targets do not include refreshed GitHub open-bug proof",
                ", ".join(targets_without_proof),
            )
        targets_below_floor = [
            (
                f"{target.get('id', f'target-{index}')}: "
                f"{target.get('selection_proof', {}).get('open_bug_count', 'unknown')} < "
                f"{target.get('selection_proof', {}).get('open_bug_floor', target.get('open_bug_floor', 'unknown'))}"
            )
            for index, target in enumerate(matrix.get("targets", []), start=1)
            if (
                target.get("selection_proof", {}).get("bug_floor_ok") is False
                or (
                    "bug_floor_ok" not in target.get("selection_proof", {})
                    and target.get("selection_proof", {}).get("status") == "fail"
                )
            )
        ]
        if targets_below_floor:
            add_validation_issue(issues, "error", "matrix-target-bug-floor", "GitHub proof shows targets below the open-bug floor", "; ".join(targets_below_floor))
        targets_below_popularity_floor = [
            (
                f"{target.get('id', f'target-{index}')}: "
                f"{target.get('selection_proof', {}).get('stargazers_count', 'unknown')} < "
                f"{target.get('selection_proof', {}).get('stargazer_floor', matrix.get('selection_contract', {}).get('stargazer_floor', 'unknown'))}"
            )
            for index, target in enumerate(matrix.get("targets", []), start=1)
            if (
                target.get("selection_proof")
                and target.get("selection_proof", {}).get("popularity_ok") is False
            )
        ]
        if targets_below_popularity_floor:
            add_validation_issue(issues, "error", "matrix-target-popularity-floor", "GitHub proof shows targets below the popularity floor", "; ".join(targets_below_popularity_floor))
        targets_without_license_proof = [
            (
                f"{target.get('id', f'target-{index}')}: "
                f"{target.get('selection_proof', {}).get('license', 'unknown')}"
            )
            for index, target in enumerate(matrix.get("targets", []), start=1)
            if (
                target.get("selection_proof")
                and target.get("selection_proof", {}).get("license_ok") is False
            )
        ]
        if targets_without_license_proof:
            add_validation_issue(issues, "error", "matrix-target-license", "GitHub proof does not show open-source license coverage", "; ".join(targets_without_license_proof))
        targets_with_proof_errors = [
            f"{target.get('id', f'target-{index}')}: {target.get('selection_proof', {}).get('error', '')}"
            for index, target in enumerate(matrix.get("targets", []), start=1)
            if target.get("selection_proof", {}).get("status") == "error"
        ]
        if targets_with_proof_errors:
            add_validation_issue(issues, "error", "matrix-target-proof-error", "GitHub proof has target refresh errors", "; ".join(targets_with_proof_errors))
        if matrix.get("assignment_count") != len(matrix.get("assignments", [])):
            add_validation_issue(issues, "error", "matrix-assignment-list", "matrix assignment_count does not match assignments length", f"assignment_count={matrix.get('assignment_count')}, assignments={len(matrix.get('assignments', []))}")
        scenario_count = len(matrix.get("scenarios", []))
        if matrix.get("scenario_count") != scenario_count:
            add_validation_issue(issues, "error", "matrix-scenario-list", "matrix scenario_count does not match scenarios length", f"scenario_count={matrix.get('scenario_count')}, scenarios={scenario_count}")
        missing_assignment_keys = [
            f"{assignment.get('id', f'assignment-{index}')}/{key}"
            for index, assignment in enumerate(matrix.get("assignments", []), start=1)
            for key in schema["matrix_result"]["assignment_required"]
            if key not in assignment
        ]
        if missing_assignment_keys:
            add_validation_issue(issues, "error", "matrix-assignment-required-keys", "Matrix assignments are missing required keys", ", ".join(missing_assignment_keys))
        missing_commands = [
            assignment.get("id", f"assignment-{index}")
            for index, assignment in enumerate(matrix.get("assignments", []), start=1)
            if not assignment.get("emit_run_command")
        ]
        if missing_commands:
            add_validation_issue(issues, "error", "matrix-emit-command", "Matrix assignments are missing emit_run_command", ", ".join(missing_commands))
        missing_tasks = [
            assignment.get("id", f"assignment-{index}")
            for index, assignment in enumerate(matrix.get("assignments", []), start=1)
            if len(assignment.get("scenario_tasks", [])) != scenario_count
        ]
        if missing_tasks:
            add_validation_issue(issues, "error", "matrix-scenario-tasks", "Matrix assignments are missing per-scenario task prompts", ", ".join(missing_tasks))
        missing_task_keys = [
            f"{assignment.get('id', f'assignment-{index}')}/{task.get('scenario', f'task-{task_index}')}/{key}"
            for index, assignment in enumerate(matrix.get("assignments", []), start=1)
            for task_index, task in enumerate(assignment.get("scenario_tasks", []), start=1)
            for key in schema["matrix_result"]["scenario_task_required"]
            if key not in task
        ]
        if missing_task_keys:
            add_validation_issue(issues, "error", "matrix-scenario-task-required-keys", "Matrix scenario tasks are missing required keys", ", ".join(missing_task_keys))
        empty_prompts = [
            f"{assignment.get('id', f'assignment-{index}')}/{task.get('scenario', 'unknown')}"
            for index, assignment in enumerate(matrix.get("assignments", []), start=1)
            for task in assignment.get("scenario_tasks", [])
            if not task.get("task_prompt", "")
        ]
        if empty_prompts:
            add_validation_issue(issues, "error", "matrix-empty-task-prompt", "Matrix scenario tasks include empty prompts", ", ".join(empty_prompts))

    validate_slidey_deck_shape(deck, {"items": []}, issues)
    if deck and len(deck.get("scenes", [])) < 3:
        add_validation_issue(issues, "warn", "matrix-deck-scenes", "Matrix deck has very few scenes", f"scenes={len(deck.get('scenes', []))}")
    matrix_scene_eyebrows = deck_scene_eyebrows(deck)
    for expected in ["Selection", "Target proof", "Personas", "Scenarios", "Task prompts", "Execution"]:
        if deck and expected not in matrix_scene_eyebrows:
            add_validation_issue(issues, "error", "matrix-deck-scene", "deck.slidey.json is missing a required matrix review scene", expected)
    proof_scenes = [
        scene for scene in deck.get("scenes", [])
        if isinstance(scene, dict) and scene.get("eyebrow") == "Target proof"
    ] if deck else []
    if deck and proof_scenes and "Strict sweep ready:" not in proof_scenes[0].get("body", ""):
        add_validation_issue(issues, "error", "matrix-deck-target-proof-readiness", "Target proof deck scene does not show strict sweep readiness")

    rollup_files = schema["matrix_rollup"]["artifacts"]
    present_rollup_files = [name for name in rollup_files if (matrix_dir / name).exists()]
    if present_rollup_files:
        missing_rollup_files = [name for name in rollup_files if not (matrix_dir / name).exists()]
        if missing_rollup_files:
            add_validation_issue(issues, "error", "rollup-required-file", "Partial matrix rollup artifacts are present", ", ".join(missing_rollup_files))
        rollup = load_json_for_validation(matrix_dir / "rollup.json", issues)
        if rollup:
            validate_required_keys(rollup, schema["matrix_rollup"]["required"], issues, "rollup-required-keys", "rollup.json")
            summary = rollup.get("summary", {})
            if summary.get("scenario_outcomes", 0) != len(rollup.get("scenario_outcomes", [])):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-scenario-outcomes",
                    "rollup summary scenario_outcomes does not match scenario_outcomes length",
                    f"summary={summary.get('scenario_outcomes')}, rows={len(rollup.get('scenario_outcomes', []))}",
                )
            quality_gates = rollup.get("quality_gates", [])
            persona_outcomes = rollup.get("persona_outcomes", [])
            if summary.get("persona_outcomes", 0) != len(persona_outcomes):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-persona-outcomes",
                    "rollup summary persona_outcomes does not match persona_outcomes length",
                    f"summary={summary.get('persona_outcomes')}, rows={len(persona_outcomes)}",
                )
            driver_journal = rollup.get("driver_journal", [])
            if summary.get("driver_journal_rows", 0) != len(driver_journal):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-driver-journal",
                    "rollup summary driver_journal_rows does not match driver_journal length",
                    f"summary={summary.get('driver_journal_rows')}, rows={len(driver_journal)}",
                )
            expected_driver_events = sum(row.get("events", 0) for row in driver_journal)
            if summary.get("driver_journal_events", 0) != expected_driver_events:
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-driver-journal-events",
                    "rollup summary driver_journal_events does not match driver journal rows",
                    f"summary={summary.get('driver_journal_events')}, rows={expected_driver_events}",
                )
            expected_persona_gate_total = sum(row.get("quality_gate_total_runs", 0) for row in persona_outcomes)
            if summary.get("quality_gate_total_runs", 0) != expected_persona_gate_total:
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-persona-quality-gates",
                    "persona outcome quality gate totals do not match rollup summary",
                    f"summary={summary.get('quality_gate_total_runs')}, personas={expected_persona_gate_total}",
                )
            if summary.get("quality_gate_rows", 0) != len(quality_gates):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-quality-gates",
                    "rollup summary quality_gate_rows does not match quality_gates length",
                    f"summary={summary.get('quality_gate_rows')}, rows={len(quality_gates)}",
                )
            expected_gate_total = sum(row.get("runs", 0) for row in quality_gates)
            if summary.get("quality_gate_total_runs", 0) != expected_gate_total:
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-quality-gate-total",
                    "rollup summary quality_gate_total_runs does not match quality gate rows",
                    f"summary={summary.get('quality_gate_total_runs')}, rows={expected_gate_total}",
                )
            expected_gate_proof = sum(row.get("proof_minimum_evidence_count", 0) for row in quality_gates)
            if summary.get("quality_gate_proof_minimum_evidence_count", 0) != expected_gate_proof:
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-quality-gate-proof-evidence",
                    "rollup summary quality_gate_proof_minimum_evidence_count does not match quality gate rows",
                    f"summary={summary.get('quality_gate_proof_minimum_evidence_count')}, rows={expected_gate_proof}",
                )
            missing_proof_evidence = rollup.get("missing_proof_evidence", [])
            if summary.get("missing_proof_evidence_rows", 0) != len(missing_proof_evidence):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-missing-proof-evidence-rows",
                    "rollup summary missing_proof_evidence_rows does not match missing_proof_evidence length",
                    f"summary={summary.get('missing_proof_evidence_rows')}, rows={len(missing_proof_evidence)}",
                )
            expected_missing_proof = sum(row.get("missing_runs", 0) for row in missing_proof_evidence)
            if summary.get("quality_gate_missing_proof_evidence_count", 0) != expected_missing_proof:
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-missing-proof-evidence-total",
                    "rollup summary quality_gate_missing_proof_evidence_count does not match missing proof rows",
                    f"summary={summary.get('quality_gate_missing_proof_evidence_count')}, rows={expected_missing_proof}",
                )
            missing_affected_runs = []
            stale_affected_counts = []
            for row in missing_proof_evidence:
                affected_runs = row.get("affected_runs", [])
                if len(affected_runs) != row.get("missing_runs", 0):
                    stale_affected_counts.append(
                        f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: expected={row.get('missing_runs', 0)}, actual={len(affected_runs)}"
                    )
                for run in affected_runs:
                    missing_keys = [
                        key for key in ["run_id", "project", "persona", "run_dir", "driver_handoff_path"]
                        if not run.get(key)
                    ]
                    if missing_keys:
                        missing_affected_runs.append(
                            f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: {', '.join(missing_keys)}"
                        )
                    handoff_path = run.get("driver_handoff_path", "")
                    if handoff_path and not Path(handoff_path).exists():
                        missing_affected_runs.append(
                            f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: missing handoff {handoff_path}"
                        )
            if stale_affected_counts:
                add_validation_issue(issues, "error", "rollup-missing-proof-affected-count", "rollup missing proof affected_runs counts are stale", "; ".join(stale_affected_counts))
            if missing_affected_runs:
                add_validation_issue(issues, "error", "rollup-missing-proof-affected-runs", "rollup missing proof affected_runs are not actionable", "; ".join(missing_affected_runs))
            rollup_deck = load_json_for_validation(matrix_dir / "rollup.slidey.json", issues)
            validate_slidey_deck_shape(rollup_deck, {"items": []}, issues)
            if rollup_deck and quality_gates and "Quality gates" not in deck_scene_eyebrows(rollup_deck):
                add_validation_issue(
                    issues,
                    "error",
                    "rollup-deck-quality-gates",
                    "rollup.slidey.json is missing the quality gate scene",
                )
            rollup_scene_eyebrows = deck_scene_eyebrows(rollup_deck)
            for expected in ["Coverage", "Runs", "Findings", "Persona outcomes", "Scenario outcomes", "Driver journal", "Quality gates", "Missing proof"]:
                if rollup_deck and expected not in rollup_scene_eyebrows:
                    add_validation_issue(issues, "error", "rollup-deck-scene", "rollup.slidey.json is missing a required rollup review scene", expected)

    errors = sum(1 for issue in issues if issue["severity"] == "error")
    warnings = sum(1 for issue in issues if issue["severity"] == "warn")
    return {
        "status": "valid" if errors == 0 else "invalid",
        "matrix_dir": str(matrix_dir),
        "checked_artifacts": len(required_files) + len(present_rollup_files),
        "errors": errors,
        "warnings": warnings,
        "validation_issue_summary": validation_issue_summary(issues),
        "issues": issues,
    }


def collect_rollup_runs(matrix: dict, explicit_run_dirs: list[str]) -> list[Path]:
    explicit_run_dirs = [value for value in explicit_run_dirs if value]
    if explicit_run_dirs:
        return [run_dir_from_arg(value) for value in explicit_run_dirs]

    assignment_keys = {
        (assignment["target"]["id"], assignment["persona"]["id"], assignment["seed"])
        for assignment in matrix.get("assignments", [])
    }
    runs = []
    if not ARTIFACT_ROOT.exists():
        return runs
    for path in sorted(ARTIFACT_ROOT.iterdir()):
        if not path.is_dir() or path.name == "matrices":
            continue
        run_path = path / "run.json"
        if not run_path.exists():
            continue
        try:
            run_json = read_json(run_path)
        except json.JSONDecodeError:
            continue
        key = (
            run_json.get("project", {}).get("id", ""),
            run_json.get("persona", {}).get("id", ""),
            run_json.get("seed", ""),
        )
        if key in assignment_keys:
            runs.append(path)
    return runs


def summarize_run_for_rollup(run_dir: Path) -> dict:
    run_json = read_json(run_dir / "run.json")
    metrics = read_json(run_dir / "metrics.json") if (run_dir / "metrics.json").exists() else {}
    evidence = read_json(run_dir / "evidence.json") if (run_dir / "evidence.json").exists() else {"items": [], "summary": {}}
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"items": [], "summary": {}}
    outcomes = read_json(run_dir / "scenario-outcomes.json") if (run_dir / "scenario-outcomes.json").exists() else {"items": [], "summary": {}}
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {"status": "not_reviewed", "summary": ""}
    driver_plan = read_json(run_dir / "driver-plan.json") if (run_dir / "driver-plan.json").exists() else {"scenarios": []}
    driver_journal = read_json(run_dir / "driver-journal.json") if (run_dir / "driver-journal.json").exists() else build_driver_journal(run_json["run_id"], [])
    finding_summary = findings.get("summary", {})
    quality_gates = summarize_quality_gates(evidence, outcomes, driver_plan, run_dir)
    return {
        "run_id": run_json["run_id"],
        "run_dir": str(run_dir),
        "project": run_json.get("project", {}),
        "persona": run_json.get("persona", {}),
        "seed": run_json.get("seed", ""),
        "review_status": review.get("status", "not_reviewed"),
        "review_summary": review.get("summary", ""),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "present_evidence_count": metrics.get("present_evidence_count", evidence.get("summary", {}).get("present", 0)),
        "required_evidence_count": metrics.get("required_evidence_count", evidence.get("summary", {}).get("required", 0)),
        "findings_count": metrics.get("findings_count", len(findings.get("items", []))),
        "strength_count": finding_summary.get("strength", metrics.get("strength_count", 0)),
        "weakness_count": finding_summary.get("weakness", metrics.get("weakness_count", 0)),
        "issue_count": finding_summary.get("issue", metrics.get("issue_count", 0)),
        "fix_count": finding_summary.get("fix", metrics.get("fix_count", 0)),
        "blocked_count": finding_summary.get("blocked", metrics.get("blocked_count", 0)),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "scenario_outcomes": outcomes.get("items", []),
        "scenario_outcomes_summary": outcomes.get("summary", {}),
        "driver_journal_summary": driver_journal.get("summary", {}),
        "driver_journal_events": driver_journal.get("items", []),
        "quality_gates": quality_gates,
    }


def summarize_quality_gates(evidence: dict, outcomes: dict, driver_plan: dict, run_dir: Optional[Path] = None) -> list[dict]:
    captured_evidence = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"}
    }
    proof_evidence = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if is_proof_evidence(item, run_dir)
    }
    outcomes_by_scenario = {
        item.get("scenario", ""): item
        for item in outcomes.get("items", [])
    }
    rows = []
    for scenario in driver_plan.get("scenarios", []):
        gate = scenario.get("quality_gate", {})
        minimum = gate.get("minimum_evidence", [])
        present = [
            item
            for item in minimum
            if (scenario.get("scenario", ""), item) in captured_evidence
        ]
        proof = [
            item
            for item in minimum
            if (scenario.get("scenario", ""), item) in proof_evidence
        ]
        outcome = outcomes_by_scenario.get(scenario.get("scenario", ""), {})
        blocked = outcome.get("outcome") == "blocked" or outcome.get("finding_counts", {}).get("blocked", 0) > 0
        satisfied = bool(minimum) and len(proof) >= len(minimum)
        rows.append({
            "scenario": scenario.get("scenario", ""),
            "label": scenario.get("label", scenario.get("scenario", "")),
            "minimum_evidence_count": len(minimum),
            "present_minimum_evidence_count": len(present),
            "proof_minimum_evidence_count": len(proof),
            "missing_minimum_evidence": sorted(set(minimum) - set(present)),
            "missing_proof_minimum_evidence": sorted(set(minimum) - set(proof)),
            "outcome": outcome.get("outcome", "not_started"),
            "blocked": blocked,
            "satisfied": satisfied,
            "done_when": gate.get("done_when", ""),
        })
    return rows


def build_matrix_rollup(matrix_dir: Path, explicit_run_dirs: list[str]) -> dict:
    matrix = read_json(matrix_dir / "matrix.json")
    run_dirs = collect_rollup_runs(matrix, explicit_run_dirs)
    runs = [summarize_run_for_rollup(path) for path in run_dirs]
    assignment_count = matrix.get("assignment_count", 0)
    reviewed = [run for run in runs if run["review_status"] != "not_reviewed"]
    ready = [run for run in runs if run["review_status"] == "ready"]
    scenario_outcomes = aggregate_scenario_outcomes(runs)
    persona_outcomes = aggregate_persona_outcomes(runs)
    quality_gates = aggregate_quality_gates(runs)
    driver_journal = aggregate_driver_journal(runs)
    missing_proof_evidence = aggregate_missing_proof_evidence(quality_gates, runs)
    totals = {
        "runs_found": len(runs),
        "assignments": assignment_count,
        "reviewed_runs": len(reviewed),
        "ready_runs": len(ready),
        "present_evidence_count": sum(run["present_evidence_count"] for run in runs),
        "required_evidence_count": sum(run["required_evidence_count"] for run in runs),
        "findings_count": sum(run["findings_count"] for run in runs),
        "strength_count": sum(run["strength_count"] for run in runs),
        "weakness_count": sum(run["weakness_count"] for run in runs),
        "issue_count": sum(run["issue_count"] for run in runs),
        "fix_count": sum(run["fix_count"] for run in runs),
        "blocked_count": sum(run.get("blocked_count", 0) for run in runs),
        "scenario_outcomes": len(scenario_outcomes),
        "scenario_outcomes_with_findings": sum(1 for row in scenario_outcomes if row["findings_count"] > 0),
        "persona_outcomes": len(persona_outcomes),
        "driver_journal_rows": len(driver_journal),
        "driver_journal_events": sum(row["events"] for row in driver_journal),
        "driver_journal_evidence_refs": sum(row["evidence_refs"] for row in driver_journal),
        "driver_journal_blocked_events": sum(row["blocked_events"] for row in driver_journal),
        "quality_gate_rows": len(quality_gates),
        "quality_gate_satisfied_runs": sum(row["satisfied_runs"] for row in quality_gates),
        "quality_gate_total_runs": sum(row["runs"] for row in quality_gates),
        "quality_gate_blocked_runs": sum(row["blocked_runs"] for row in quality_gates),
        "quality_gate_present_minimum_evidence_count": sum(row["present_minimum_evidence_count"] for row in quality_gates),
        "quality_gate_proof_minimum_evidence_count": sum(row["proof_minimum_evidence_count"] for row in quality_gates),
        "quality_gate_minimum_evidence_count": sum(row["minimum_evidence_count"] for row in quality_gates),
        "quality_gate_missing_proof_evidence_count": sum(row["missing_runs"] for row in missing_proof_evidence),
        "missing_proof_evidence_rows": len(missing_proof_evidence),
    }
    return {
        "matrix_id": matrix["matrix_id"],
        "created_at": now_utc(),
        "matrix_dir": str(matrix_dir),
        "matrix_deck_path": str(matrix_dir / "deck.slidey.json"),
        "summary": totals,
        "runs": runs,
        "scenario_outcomes": scenario_outcomes,
        "persona_outcomes": persona_outcomes,
        "driver_journal": driver_journal,
        "quality_gates": quality_gates,
        "missing_proof_evidence": missing_proof_evidence,
        "missing_assignment_count": max(assignment_count - len(runs), 0),
        "artifacts": {
            "rollup": "rollup.json",
            "summary": "rollup.md",
            "deck": "rollup.slidey.json",
        },
    }


def write_matrix_rollup(matrix_dir: Path, explicit_run_dirs: list[str]) -> dict:
    rollup = build_matrix_rollup(matrix_dir, explicit_run_dirs)
    write_json(matrix_dir / "rollup.json", rollup)
    (matrix_dir / "rollup.md").write_text(render_rollup_summary(rollup), encoding="utf-8")
    write_json(matrix_dir / "rollup.slidey.json", render_rollup_deck(rollup))
    return {
        "status": "rollup_created",
        "matrix_id": rollup["matrix_id"],
        "matrix_dir": str(matrix_dir),
        "rollup_path": str(matrix_dir / "rollup.json"),
        "markdown_path": str(matrix_dir / "rollup.md"),
        "deck_path": str(matrix_dir / "rollup.slidey.json"),
        **rollup["summary"],
        "missing_assignment_count": rollup["missing_assignment_count"],
        "missing_proof_handoff_summary": rollup_handoff_backlog_summary(rollup),
    }


def render_dogfood_smoke_summary(report: dict) -> str:
    validation = report["validation"]
    review = report["review"]
    rollup = report["rollup"]
    corpus = report["corpus_validation"]
    lines = [
        "# Product journey dogfood smoke",
        "",
        f"- Smoke: `{report['dogfood_id']}`",
        f"- Created: {report['created_at']}",
        f"- Seed: `{report['seed']}`",
        f"- Status: `{report['status']}`",
        f"- Matrix: `{report['matrix']['matrix_id']}`",
        f"- Run: `{report['run']['run_id']}`",
        f"- Assignment: `{report['assignment']['id']}`",
        "",
        "## Artifacts",
        "",
        f"- Matrix dir: `{report['matrix']['matrix_dir']}`",
        f"- Matrix deck: `{report['matrix']['deck_path']}`",
        f"- Run dir: `{report['run']['run_dir']}`",
        f"- Run deck: `{report['run']['deck_path']}`",
        f"- Run agent brief: `{report['run']['agent_brief_path']}`",
        f"- Rollup deck: `{rollup['deck_path']}`",
        f"- Smoke deck: `{report['artifacts']['deck']}`",
        "",
        "## Gates",
        "",
        f"- Corpus validation: {corpus['status']} ({corpus['errors']} errors, {corpus['warnings']} warnings)",
        f"- Review: {review['review_status']} - {review['summary']}",
        f"- Driver journal events: {report['run'].get('driver_event_count', 0)}",
        f"- Run validation: {validation['run']['status']} ({validation['run']['errors']} errors, {validation['run']['warnings']} warnings)",
        f"- Run validation issues: {validation['run'].get('validation_issue_summary') or '(none)'}",
        f"- Matrix validation: {validation['matrix']['status']} ({validation['matrix']['errors']} errors, {validation['matrix']['warnings']} warnings)",
        f"- Matrix validation issues: {validation['matrix'].get('validation_issue_summary') or '(none)'}",
        f"- Rollup runs: {rollup['runs_found']} / {rollup['assignments']}",
        f"- Rollup evidence: {rollup['present_evidence_count']} / {rollup['required_evidence_count']}",
        "",
        "## Notes",
        "",
    ]
    for note in report["notes"]:
        lines.append(f"- {note}")
    return "\n".join(lines) + "\n"


def render_dogfood_smoke_deck(report: dict) -> dict:
    validation = report["validation"]
    review = report["review"]
    rollup = report["rollup"]
    corpus = report["corpus_validation"]
    artifact_body = "\n".join([
        f"Matrix: {report['matrix']['matrix_dir']}",
        f"Matrix deck: {report['matrix']['deck_path']}",
        f"Run: {report['run']['run_dir']}",
        f"Run deck: {report['run']['deck_path']}",
        f"Agent brief: {report['run']['agent_brief_path']}",
        f"Rollup deck: {rollup['deck_path']}",
    ])
    gate_body = "\n".join([
        f"Corpus validation: {corpus['status']} ({corpus['errors']} errors, {corpus['warnings']} warnings)",
        f"Review: {review['review_status']}",
        f"Review checks: {review['passed']}/{review['total']} passed, {review['warnings']} warnings, {review['failed']} failures",
        f"Review backlog: {review.get('review_backlog_summary', '(none)') or '(none)'}",
        f"Run validation: {validation['run']['status']} ({validation['run']['errors']} errors, {validation['run']['warnings']} warnings)",
        f"Run validation issues: {validation['run'].get('validation_issue_summary') or '(none)'}",
        f"Matrix validation: {validation['matrix']['status']} ({validation['matrix']['errors']} errors, {validation['matrix']['warnings']} warnings)",
        f"Matrix validation issues: {validation['matrix'].get('validation_issue_summary') or '(none)'}",
        f"Rollup runs: {rollup['runs_found']} / {rollup['assignments']}",
    ])
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey Dogfood Smoke",
            "phase": "dogfood-smoke",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Product Journey Dogfood Smoke",
                "subtitle": f"{report['assignment']['target']['label']} · {report['assignment']['persona']['label']}",
                "narration": "A deterministic no-LLM proof that the product journey matrix, run, review, validation, rollup, and deck artifacts compose end to end.",
            },
            {
                "type": "narrative",
                "eyebrow": "Assignment",
                "title": report["assignment"]["id"],
                "body": "\n".join([
                    f"Target: {report['assignment']['target']['label']}",
                    f"Persona: {report['assignment']['persona']['label']}",
                    f"Seed: {report['assignment']['seed']}",
                    f"Scenarios: {report['matrix']['scenario_count']}",
                ]),
                "narration": "The smoke uses the first deterministic matrix assignment as a representative end-to-end bundle.",
            },
            {
                "type": "narrative",
                "eyebrow": "Gates",
                "title": report["status"],
                "body": gate_body,
                "narration": "The smoke is successful when generated artifacts validate and any seeded-run review failure is limited to missing proof evidence.",
            },
            {
                "type": "narrative",
                "eyebrow": "Artifacts",
                "title": "Reviewable outputs",
                "body": artifact_body,
                "narration": "The smoke emits the same review surfaces a live or cassette-backed product journey run will use.",
            },
        ],
    }


def dogfood_review_is_expected_demo_only(reviewed: dict) -> bool:
    if reviewed.get("review_status") == "ready":
        return True
    failed_checks = {
        check.get("id", "")
        for check in reviewed.get("checks", [])
        if check.get("status") == "fail"
    }
    # Demo-seeded evidence is placeholder-only (attach_evidence records a path
    # without writing a backing file), so the playback-capable slot can never
    # resolve to a real local file here — same category as the unattempted
    # quality-gate coverage this smoke already tolerates.
    return failed_checks <= {"quality-gates", "playback-evidence-backed"}


def driver_replay_review_is_expected_one_scenario(reviewed: dict) -> bool:
    failed_checks = {
        check.get("id", "")
        for check in reviewed.get("checks", [])
        if check.get("status") == "fail"
    }
    # This smoke replays exactly one scenario's minimum-evidence contract via
    # cassette-backed proof; it never attaches the playback-capable slot (a
    # cassette:// ref is deliberately NOT accepted there), and every other
    # scenario in the bundle stays untouched.
    return failed_checks <= {"scenario-attempts", "driver-journal-coverage", "quality-gates", "playback-or-blocker", "playback-evidence-backed"}


def driver_replay_readiness_status(review_status: str) -> str:
    return "ready" if review_status == "ready" else "needs_evidence"


def cassette_replay_path(run_id: str, scenario_id: str, evidence_kind: str) -> str:
    return f"cassette://product-journey/{run_id}/{demo_evidence_path(scenario_id, evidence_kind)}"


def render_driver_replay_smoke_summary(report: dict) -> str:
    review = report["review"]
    validation = report["validation"]
    lines = [
        "# Product Journey Driver Replay Smoke",
        "",
        f"- Status: `{report['status']}`",
        f"- Smoke: `{report['smoke_id']}`",
        f"- Run: `{report['run']['run_id']}`",
        f"- Scenario: `{report['scenario']['id']}`",
        f"- Persona: `{report['persona']['id']}`",
        f"- Run dir: `{report['run']['run_dir']}`",
        f"- Deck: `{report['run']['deck_path']}`",
        f"- Driver journal: `{report['run']['driver_journal_path']}`",
        f"- Media manifest: `{report['run']['media_manifest_path']}`",
        f"- Readiness: `{report.get('readiness_status', 'needs_evidence')}`",
        f"- Review: `{review.get('review_status')}` - {review.get('summary')}",
        f"- Validation: `{validation.get('status')}` - {validation.get('validation_issue_summary', '') or 'no issues'}",
        "",
        "## Attached Evidence",
        "",
    ]
    for item in report["attached_evidence"]:
        lines.append(f"- `{item['scenario']}/{item['kind']}`: `{item['path']}` ({item['source']})")
    lines.extend([
        "",
        "## Expected Scope",
        "",
        "This smoke proves one cassette-backed driver scenario loop end to end. It is expected to leave other scenarios incomplete, so review failures must be limited to scenario coverage and quality-gate coverage.",
    ])
    return "\n".join(lines) + "\n"


def render_driver_replay_smoke_deck(report: dict) -> dict:
    review = report["review"]
    validation = report["validation"]
    attached = [
        f"{item['kind']}: {item['path']}"
        for item in report["attached_evidence"]
    ]
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey Driver Replay Smoke",
            "phase": "driver-replay-smoke",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Driver replay smoke",
                "subtitle": f"{report['scenario']['id']} · {report['persona']['label']}",
                "narration": "This deck summarizes a deterministic cassette-backed product journey driver replay.",
            },
            {
                "type": "narrative",
                "eyebrow": "Evidence",
                "title": "Structured proof attached",
                "body": "\n".join(f"- {line}" for line in attached),
                "narration": "Each driver journal evidence reference is also attached as structured evidence.",
            },
            {
                "type": "narrative",
                "eyebrow": "Review",
                "title": f"{report.get('readiness_status', 'needs_evidence')} readiness",
                "body": "\n".join([
                    f"Review status: {review.get('review_status', 'unknown')}",
                    review.get("summary", ""),
                    report.get("review_backlog_summary", ""),
                ]),
                "narration": "The run is not globally ready until the remaining scenarios are captured or blocked.",
            },
            {
                "type": "narrative",
                "eyebrow": "Validation",
                "title": validation.get("status", "unknown"),
                "body": validation.get("validation_issue_summary", "") or "No validation issues.",
                "narration": "Validation must pass even when review honestly reports incomplete scenario coverage.",
            },
        ],
    }


def render_driver_replay_sweep_summary(report: dict) -> str:
    lines = [
        "# Product Journey Driver Replay Sweep",
        "",
        f"- Status: `{report['status']}`",
        f"- Sweep: `{report['sweep_id']}`",
        f"- Persona: `{report['persona']['id']}`",
        f"- Scenarios: {report['summary']['passed']} / {report['summary']['scenarios']} passed",
        f"- Readiness: `{report['readiness_status']}` ({report['summary']['review_ready']} / {report['summary']['scenarios']} reviews ready)",
        f"- Playback scenarios: {report['summary']['playback_scenarios']} / {report['summary']['scenarios']}",
        f"- Validation errors: {report['summary']['validation_errors']}",
        f"- Sweep dir: `{report['sweep_dir']}`",
        "",
        "## Scenarios",
        "",
    ]
    for row in report["scenarios"]:
        lines.extend([
            f"### {row['scenario']}",
            "",
            f"- Status: `{row['status']}`",
            f"- Readiness: `{row['readiness_status']}`",
            f"- Review: `{row['review_status']}` - {row['review_summary']}",
            f"- Validation: `{row['validation_status']}`",
            f"- Evidence: {row['attached_evidence_count']}",
            f"- Playback items: {row['playback_items']}",
            f"- Run: `{row['run_dir']}`",
            f"- Deck: `{row['run_deck_path']}`",
            "",
        ])
    return "\n".join(lines)


def render_driver_replay_sweep_deck(report: dict) -> dict:
    rows = [
        f"{row['scenario']}: {row['status']}, readiness {row['readiness_status']}, playback {row['playback_items']}, validation {row['validation_status']}"
        for row in report["scenarios"]
    ]
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey Driver Replay Sweep",
            "phase": "driver-replay-sweep",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Driver replay sweep",
                "subtitle": f"{report['summary']['passed']} / {report['summary']['scenarios']} contract-passed · readiness {report['readiness_status']} · {report['persona']['label']}",
                "narration": "This deck summarizes the deterministic cassette-backed replay sweep across every product journey scenario.",
            },
            {
                "type": "narrative",
                "eyebrow": "Coverage",
                "title": "Scenario replay coverage",
                "body": "\n".join(f"- {row}" for row in rows),
                "narration": "Each scenario should have proof evidence, an attached driver journal, playback media, review output, and clean validation.",
            },
            {
                "type": "narrative",
                "eyebrow": "Artifacts",
                "title": report["sweep_id"],
                "body": f"Report: {report['artifacts']['report']}\nSummary: {report['artifacts']['summary']}",
                "narration": "The sweep report links to each scenario run bundle and its normal Slidey review deck.",
            },
        ],
    }


def build_driver_replay_sweep(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
    persona_id: str,
) -> dict:
    sweep_id = f"{slug_timestamp()}-driver-replay-sweep-{seed}"
    sweep_dir = DOGFOOD_ROOT / sweep_id
    sweep_dir.mkdir(parents=True, exist_ok=False)
    rows = []
    scenario_reports = []
    for scenario in scenarios:
        scenario_id = scenario["id"]
        report = build_driver_replay_smoke(
            catalog,
            github_targets,
            personas,
            scenarios,
            f"{seed}-{scenario_id}",
            scenario_id,
            persona_id,
        )
        scenario_reports.append(report)
        media_manifest = read_json(Path(report["run"]["media_manifest_path"]))
        row = {
            "scenario": scenario_id,
            "status": report["status"],
            "smoke_id": report["smoke_id"],
            "smoke_dir": report["smoke_dir"],
            "run_dir": report["run"]["run_dir"],
            "run_deck_path": report["run"]["deck_path"],
            "driver_journal_path": report["run"]["driver_journal_path"],
            "media_manifest_path": report["run"]["media_manifest_path"],
            "attached_evidence_count": len(report["attached_evidence"]),
            "playback_items": media_manifest.get("summary", {}).get("playback_items", 0),
            "review_status": report["review"].get("review_status", ""),
            "readiness_status": driver_replay_readiness_status(report["review"].get("review_status", "")),
            "review_summary": report["review"].get("summary", ""),
            "validation_status": report["validation"].get("status", ""),
            "validation_errors": report["validation"].get("errors", 0),
            "validation_warnings": report["validation"].get("warnings", 0),
        }
        rows.append(row)

    failed = [
        row for row in rows
        if row["status"] != "passed"
        or row["validation_status"] != "valid"
        or row["validation_errors"]
        or row["playback_items"] < 1
    ]
    review_ready = sum(1 for row in rows if row["readiness_status"] == "ready")
    review_needs_evidence = len(rows) - review_ready
    summary = {
        "scenarios": len(rows),
        "passed": len(rows) - len(failed),
        "failed": len(failed),
        "review_ready": review_ready,
        "review_needs_evidence": review_needs_evidence,
        "playback_scenarios": sum(1 for row in rows if row["playback_items"] >= 1),
        "validation_errors": sum(row["validation_errors"] for row in rows),
        "validation_warnings": sum(row["validation_warnings"] for row in rows),
        "attached_evidence_count": sum(row["attached_evidence_count"] for row in rows),
    }
    report = {
        "status": "passed" if not failed else "failed",
        "readiness_status": "ready" if review_needs_evidence == 0 else "needs_evidence",
        "sweep_id": sweep_id,
        "created_at": now_utc(),
        "seed": seed,
        "persona": select_persona(personas, persona_id, seed),
        "sweep_dir": str(sweep_dir),
        "summary": summary,
        "scenarios": rows,
        "failed_scenarios": [row["scenario"] for row in failed],
        "scenario_reports": [
            {
                "scenario": row["scenario"],
                "report_path": scenario_report["artifacts"]["report"],
                "summary_path": scenario_report["artifacts"]["summary"],
                "deck_path": scenario_report["artifacts"]["deck"],
            }
            for row, scenario_report in zip(rows, scenario_reports)
        ],
        "artifacts": {
            "report": str(sweep_dir / "driver-replay-sweep.json"),
            "summary": str(sweep_dir / "driver-replay-sweep.md"),
            "deck": str(sweep_dir / "driver-replay-sweep.slidey.json"),
        },
    }
    write_json(sweep_dir / "driver-replay-sweep.json", report)
    (sweep_dir / "driver-replay-sweep.md").write_text(render_driver_replay_sweep_summary(report), encoding="utf-8")
    deck = render_driver_replay_sweep_deck(report)
    deck_issues: list[dict] = []
    validate_slidey_deck_shape(deck, {"items": []}, deck_issues)
    if deck_issues:
        raise SystemExit(f"driver replay sweep deck validation failed: {validation_issue_summary(deck_issues)}")
    write_json(sweep_dir / "driver-replay-sweep.slidey.json", deck)
    return report


# ---------------------------------------------------------------------------
# scenario-qa fold: stories/scenario-qa owns report.md (folded in Starlark,
# scripts/build_report.star) and dispatches the driver/judge agents per
# transport leg; this run.py subcommand owns the ONE derived artifact that
# most benefits from this module's existing Slidey-deck machinery --
# deck.slidey.json -- rather than reimplementing scene-shape validation in
# Starlark. `--scenario-qa-report` is additive: it does not touch the
# scenario-keyed run.json/evidence.json/deck.slidey.json pipeline that
# `--emit-run`/`update_derived_artifacts` own for the matrix/rollup path
# (those are per-scenario, not per-transport-leg, and reworking them is out of
# scope here) -- it just overwrites <run-dir>/deck.slidey.json with a
# transport-leg-shaped deck once the scenario-qa story has recorded every
# leg's driver+judge outcome.
def scenario_qa_leg_items(leg_results: Optional[dict]) -> list[dict]:
    if not isinstance(leg_results, dict):
        return []
    items = leg_results.get("items", [])
    return [item for item in items if isinstance(item, dict)] if isinstance(items, list) else []


def scenario_qa_leg_level(item: dict) -> str:
    """The evidence level for one recorded leg -- 'bridge-level' for vscode
    (the IDE bridge stub/recording path, never a genuine editor -- see
    TRANSPORT_EVIDENCE_CONTRACTS and docs/persona-qa.md), 'terminal-level'
    for cli, and 'frame-level' for tui/web. Prefers the leg's own recorded
    `evidence_level`/`transport_evidence_contract` (present once
    scripts/record_leg_result.star has run for real); falls back to the
    transport-id lookup so a leg_results row that only carries a bare
    transport id (e.g. an older recording, or a flow fixture's stubbed data)
    still gets labeled correctly.
    """
    level = item.get("evidence_level", "")
    if level:
        return level
    contract = item.get("transport_evidence_contract")
    if isinstance(contract, dict) and contract.get("level"):
        return contract["level"]
    return TRANSPORT_EVIDENCE_CONTRACTS.get(item.get("transport", ""), {}).get("level", "")


def scenario_qa_leg_counts(items: list[dict]) -> dict:
    passed = sum(1 for item in items if item.get("verdict") == "pass")
    degraded = sum(1 for item in items if item.get("verdict") == "degraded-evidence")
    judged = sum(1 for item in items if item.get("verdict", "") not in ("", "unjudged"))
    return {
        "total": len(items),
        "pass": passed,
        "degraded": degraded,
        "fail": max(judged - passed - degraded, 0),
    }


def scenario_qa_leg_task(item: dict, fallback: str) -> str:
    for key in ("scenario_task", "task", "description"):
        value = str(item.get(key, "") or "").strip()
        if value:
            return value
    return fallback


def scenario_qa_leg_persona(item: dict) -> str:
    personas = item.get("personas")
    if isinstance(personas, list):
        text = ", ".join(str(persona).strip() for persona in personas if str(persona).strip())
        if text:
            return text
    for key in ("persona", "persona_label"):
        value = str(item.get(key, "") or "").strip()
        if value:
            return value
    return "QA engineer / developer"


def scenario_qa_leg_checked_lines(item: dict) -> list[str]:
    raw = item.get("checked")
    if raw is None:
        raw = item.get("checklist")
    if isinstance(raw, list):
        lines = [str(line).strip().rstrip(".") for line in raw if str(line).strip()]
        if lines:
            return lines[:4]
    if isinstance(raw, str) and raw.strip():
        return [line.strip(" -").rstrip(".") for line in raw.splitlines() if line.strip(" -")][:4]

    transport = str(item.get("transport", "") or "").strip()
    level = scenario_qa_leg_level(item)
    return [
        f"Drive the requested behavior in the {transport or 'selected'} transport",
        f"Collect {level or 'transport'} evidence that an independent judge can inspect",
        "Show the final verdict and replay/report artifacts to the operator",
    ]


def scenario_qa_leg_label(item: dict, fallback: str) -> str:
    transport = str(item.get("transport", "") or "").strip()
    label = str(item.get("transport_label", "") or "").strip()
    if not label:
        label = {
            "web": "Web UI",
            "tui": "TUI",
            "vscode": "VS Code bridge",
            "cli": "CLI",
        }.get(transport, transport or "Transport")
    return label


def scenario_qa_check_tags(lines: list[str]) -> list[str]:
    tags: list[str] = []

    def add(tag: str) -> None:
        if tag and tag not in tags:
            tags.append(tag)

    for raw in lines:
        line = str(raw or "").strip()
        lower = line.lower()
        if any(word in lower for word in ["input", "enter", "request", "prompt", "question"]):
            add("input path")
        if any(word in lower for word in ["preview", "plan", "before capture", "dry-run"]):
            add("plan preview")
        if any(word in lower for word in ["progress", "status", "per-transport", "verdict", "result"]):
            add("progress/results")
        if any(word in lower for word in ["summary", "report", "conclusion"]):
            add("summary report")
        if any(word in lower for word in ["artifact", "replay", "rrweb", "link", "playback"]):
            add("replay/artifact links")
        if any(word in lower for word in ["return", "main-room", "main room", "back", "exit"]):
            add("return path")
        if any(word in lower for word in ["evidence", "capture", "judge", "inspect"]):
            add("evidence capture")
        if any(word in lower for word in ["transport", "web", "tui", "vscode", "cli"]):
            add("transport behavior")
        if len(tags) >= 5:
            break

    if tags:
        return tags[:5]

    fallback_tags = []
    for line in lines[:3]:
        text = " ".join(str(line).strip().split())
        if len(text) > 36:
            text = text[:33].rstrip() + "..."
        if text and text not in fallback_tags:
            fallback_tags.append(text)
    return fallback_tags


def scenario_qa_leg_detail(item: dict, playback_refs: list[dict]) -> str:
    transport = str(item.get("transport", "") or "").strip() or "transport"
    level = scenario_qa_leg_level(item) or "evidence"
    tool = ""
    contract = item.get("transport_evidence_contract")
    if isinstance(contract, dict):
        tool = str(contract.get("primary_tool", "") or "").strip()
    if not tool:
        tool = str(item.get("primary_tool", "") or item.get("evidence_tool", "") or "").strip()

    tags = scenario_qa_check_tags(scenario_qa_leg_checked_lines(item))
    checked = ", ".join(tags) if tags else "transport behavior"
    evidence = f"{level} {transport}"
    if tool:
        evidence += f" via {tool}"
    if playback_refs:
        evidence += "; rrweb replay"
    verdict = str(item.get("verdict", "") or "unjudged")
    detail = f"Checked: {checked}. Evidence: {evidence}. Judge: {verdict}."
    cause = str(item.get("cause", "") or "").strip()
    if verdict != "pass" and cause:
        detail += f" Cause: {cause}."
    return detail


def scenario_qa_report_title(counts: dict) -> str:
    title = f"Transport checks: {counts['pass']} / {counts['total']} passed"
    extras = []
    if counts["fail"] > 0:
        extras.append(f"{counts['fail']} failed")
    if counts["degraded"] > 0:
        extras.append(f"{counts['degraded']} degraded")
    if extras:
        title += " · " + " · ".join(extras)
    return title


def scenario_qa_report_summary(name: str, counts: dict) -> str:
    summary = f"{counts['pass']} / {counts['total']} transport checks passed"
    if counts["fail"] > 0:
        summary += f", {counts['fail']} failed"
    if counts["degraded"] > 0:
        summary += f", {counts['degraded']} degraded-evidence"
    return summary + "."


def scenario_qa_natural_prompt_lines(items: list[dict]) -> list[str]:
    lines = []
    for item in items:
        count = int(item.get("natural_utterance_count", 0) or 0)
        if count <= 0:
            continue
        sources = item.get("natural_utterance_sources", [])
        if not isinstance(sources, list):
            sources = []
        source_text = ", ".join(str(source) for source in sources if source)
        example = str(item.get("natural_utterance_example", "") or "")
        line = (
            f"{item.get('transport', '')} / {item.get('scenario', '')}: "
            f"{count} transcript-derived prompt(s)"
        )
        if example:
            line += f"; example: \"{example}\""
        if source_text:
            line += f"; sources: {source_text}"
        lines.append(line)
    return lines


def scenario_qa_playback_refs(item: dict) -> list[dict]:
    """Return playback references carried by a recorded scenario-qa leg.

    The story records whatever the driver/judge actually produced. Newer driver
    results can carry an explicit playback_path/playback_ref; older rows may
    only have evidence_refs. Treat only obvious playback media as deck scenes.
    """
    refs: list[dict] = []

    def add_ref(raw, label: str = "") -> None:
        if isinstance(raw, dict):
            path = str(raw.get("path") or raw.get("ref") or raw.get("href") or "")
            notes = str(raw.get("notes") or raw.get("summary") or raw.get("caption") or label)
        else:
            path = str(raw or "")
            notes = label
        path = path.strip()
        if not path:
            return
        lower = path.lower()
        if not (lower.endswith(".rrweb.json") or lower.endswith(".mp4") or lower.endswith(".webm") or lower.endswith(".mov")):
            return
        if any(existing.get("path") == path for existing in refs):
            return
        refs.append({"path": path, "notes": notes})

    for key in ["playback_path", "playback_ref", "rrweb_path", "video_path"]:
        add_ref(item.get(key), str(item.get("playback_caption") or item.get("verdict_summary") or ""))

    evidence_refs = item.get("evidence_refs", [])
    if isinstance(evidence_refs, list):
        for ref in evidence_refs:
            add_ref(ref, str(item.get("verdict_summary") or ""))

    return refs


def scenario_qa_playback_items(items: list[dict]) -> list[dict]:
    playback = []
    for item in items:
        refs = scenario_qa_playback_refs(item)
        for ref in refs:
            path = ref["path"]
            playback.append({
                "scenario": item.get("scenario", ""),
                "transport": item.get("transport", ""),
                "evidence_kind": item.get("playback_kind") or "session_replay",
                "media_kind": "video",
                "path": path,
                "status": "captured",
                "source": item.get("playback_source") or "local",
                "notes": ref.get("notes") or item.get("verdict_summary", ""),
                "playback": True,
            })
    return playback


def scenario_qa_deck_status(verdict: str) -> str:
    normalized = (verdict or "").strip().lower()
    if normalized == "pass":
        return "validated"
    if normalized in {"fail", "failed"}:
        return "issue"
    if "degraded" in normalized:
        return "skipped"
    return "pending"


def parse_scenario_qa_leg_results(raw: str) -> dict:
    """Accepts an inline JSON object (the common case -- a story's `host.run`
    invoke templates world.leg_results, a map, directly into this flag and the
    engine auto-serializes it to compact JSON) or `@<path>` to a JSON file, so
    the subcommand is also easy to drive by hand while authoring/debugging.
    """
    if not raw:
        return {"items": []}
    text = raw
    if raw.startswith("@"):
        path = Path(raw[1:])
        if not path.is_absolute():
            path = PROJECT_ROOT / path
        text = path.read_text(encoding="utf-8")
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"--leg-results-json is not valid JSON: {exc}")
    if not isinstance(parsed, dict):
        raise SystemExit("--leg-results-json must decode to a JSON object with an 'items' list")
    return parsed


def render_scenario_qa_deck(name: str, run_id: str, items: list[dict], counts: dict) -> dict:
    natural_prompt_lines = scenario_qa_natural_prompt_lines(items)
    playback_items = scenario_qa_playback_items(items)
    verdict_items = []
    for item in items[:6]:
        playback_refs = scenario_qa_playback_refs(item)
        ref = playback_refs[0]["path"] if playback_refs else item.get("leg_id", "")
        ref_type = "log"
        if playback_refs:
            ref_type = "rrweb" if str(ref).lower().endswith(".rrweb.json") else "artifact"
        verdict_items.append({
            "label": scenario_qa_leg_label(item, name),
            "status": scenario_qa_deck_status(str(item.get("verdict", ""))),
            "detail": scenario_qa_leg_detail(item, playback_refs),
            "refType": ref_type,
            "ref": ref,
            "note": " · ".join(
                part
                for part in [
                    scenario_qa_leg_level(item),
                    str(item.get("driver_status", "") or "recorded"),
                ]
                if part
            ),
        })
    scenes = [
        {
            "type": "title",
            "title": "Scenario QA",
            "subtitle": name,
            "narration": "This deck folds every transport check's independently-judged verdict into one per-scenario view.",
        },
        {
            "type": "evidence",
            "title": scenario_qa_report_title(counts),
            "items": verdict_items or [{
                "label": "No transport checks recorded",
                "status": "pending",
                "detail": "Run the scenario across one or more transports before reviewing this report.",
            }],
            "caption": f"Scenario: {name}",
            "narration": "Each row summarizes the transport, checked behavior, evidence level, replay source, and independent judge verdict.",
        },
    ]
    if playback_items:
        scenes.append({
            "type": "evidence",
            "title": f"Session evidence: {len(playback_items)} user session replay(s) embedded",
            "items": [
                {
                    "label": f"{item.get('transport', '')} user session",
                    "status": "done",
                    "detail": item.get("notes", "") or "Recorded user-session playback.",
                    "refType": "rrweb" if str(item.get("path", "")).lower().endswith(".rrweb.json") else "artifact",
                    "ref": item.get("path", ""),
                    "note": item.get("scenario", ""),
                }
                for item in playback_items
            ][:6],
            "narration": "The report embeds the recorded user sessions for the transports that produced playback evidence.",
        })
        for playback in playback_items:
            scene = playback_scene_for_item(playback)
            if scene is not None:
                scene["eyebrow"] = "User session replay"
                scenes.append(scene)
    if natural_prompt_lines:
        scenes.append({
            "type": "narrative",
            "eyebrow": "Natural prompts",
            "lede": "Transcript-derived scenario wording",
            "body": "\n".join(f"- {line}" for line in natural_prompt_lines),
            "narration": "Scenario QA preserves the mined human wording that the driver used for natural persona actions.",
        })
    scenes.append({
        "type": "narrative",
        "eyebrow": "Run",
        "lede": run_id,
        "body": f"{counts['total']} transport check(s); {counts['pass']} pass, {counts['fail']} fail, {counts['degraded']} degraded-evidence.",
        "narration": "vscode legs are always bridge-level proof (the IDE bridge stub/recording path) -- never mistake them for editor-level coverage.",
    })
    return {
        "meta": {
            "mode": "pitch",
            "title": "Scenario QA",
            "phase": "scenario-qa-report",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": scenes,
    }


def render_scenario_qa_review(name: str, run_id: str, items: list[dict], counts: dict) -> dict:
    checks = []
    if counts["total"] <= 0:
        checks.append({
            "id": "scenario-qa-legs",
            "status": "warn",
            "summary": "No transport checks have been recorded for this scenario-qa report.",
        })
        status = "not_reviewed"
        summary = "Run has no recorded transport checks yet."
    else:
        checks.append({
            "id": "scenario-qa-legs",
            "status": "pass",
            "summary": f"{counts['total']} transport check(s) recorded.",
        })
        if counts["fail"] > 0:
            checks.append({
                "id": "scenario-qa-verdicts",
                "status": "fail",
                "summary": f"{counts['fail']} transport check(s) failed.",
            })
        elif counts["degraded"] > 0:
            checks.append({
                "id": "scenario-qa-verdicts",
                "status": "warn",
                "summary": f"{counts['degraded']} transport check(s) reported degraded evidence.",
            })
        else:
            checks.append({
                "id": "scenario-qa-verdicts",
                "status": "pass",
                "summary": "Every recorded transport check passed.",
            })
        status = "ready" if counts["fail"] == 0 and counts["degraded"] == 0 else "needs_evidence"
        summary = scenario_qa_report_summary(name, counts)

    natural_prompt_count = sum(int(item.get("natural_utterance_count", 0) or 0) for item in items)
    if natural_prompt_count > 0:
        checks.append({
            "id": "scenario-qa-natural-prompts",
            "status": "pass",
            "summary": f"{natural_prompt_count} transcript-derived prompt(s) preserved across recorded legs.",
        })
    elif counts["total"] > 0:
        checks.append({
            "id": "scenario-qa-natural-prompts",
            "status": "warn",
            "summary": "Recorded legs did not carry transcript-derived natural prompt metadata.",
        })

    return {
        "run_id": run_id,
        "scenario": name,
        "status": status,
        "summary": summary,
        "summary_counts": {
            "total": counts["total"],
            "pass": counts["pass"],
            "fail": counts["fail"],
            "degraded": counts["degraded"],
            "natural_prompts": natural_prompt_count,
        },
        "checks": checks,
    }


def scenario_qa_markdown_cell(value) -> str:
    return str(value if value is not None else "").replace("|", "\\|").replace("\n", "<br>")


def render_scenario_qa_markdown(name: str, run_id: str, items: list[dict], counts: dict) -> str:
    lines = [
        "# Scenario QA report",
        "",
        f"- Scenario: `{name}`",
        f"- Run: `{run_id}`",
        "",
        "| Transport | Scenario | Level | Natural prompts | Driver | Verdict | Cause | Playback | Notes |",
        "|---|---|---|---:|---|---|---|---|---|",
    ]
    for item in items:
        # `cause` is computed deterministically by
        # stories/scenario-qa/scripts/record_leg_result.star (never left to
        # an LLM judge/summary) for every non-"pass" verdict -- issue group B
        # (silent live-authorization fallback) requires every degraded/
        # unsupported/blocked/failed leg to carry a machine-readable AND
        # human-readable reason instead of a bare verdict with no stated why.
        lines.append(
            "| "
            + " | ".join(
                scenario_qa_markdown_cell(value)
                for value in [
                    item.get("transport", ""),
                    item.get("scenario", ""),
                    item.get("evidence_level", ""),
                    item.get("natural_utterance_count", 0),
                    item.get("driver_status", ""),
                    item.get("verdict", ""),
                    item.get("cause", ""),
                    item.get("playback_path", ""),
                    item.get("verdict_summary", ""),
                ]
            )
            + " |"
        )
    natural_lines = scenario_qa_natural_prompt_lines(items)
    if natural_lines:
        lines.extend(["", "## Natural Prompt Coverage", ""])
        lines.extend(f"- {line}" for line in natural_lines)
    lines.extend(["", scenario_qa_report_summary(name, counts), ""])
    return "\n".join(lines)


def build_driver_replay_smoke(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
    smoke_scenario: str,
    persona_id: str,
) -> dict:
    scenario_slug = smoke_scenario or "bugfix"
    smoke_id = f"{slug_timestamp()}-driver-replay-{scenario_slug}-{seed}"
    smoke_dir = DOGFOOD_ROOT / smoke_id
    smoke_dir.mkdir(parents=True, exist_ok=False)
    scenario_ids = {scenario.get("id", "") for scenario in scenarios}
    scenario_id = scenario_slug
    if scenario_id not in scenario_ids:
        known = ", ".join(sorted(scenario_ids))
        raise SystemExit(f"Unknown replay smoke scenario '{scenario_id}'. Known: {known}")

    persona = select_persona(personas, persona_id, f"{seed}:{scenario_id}:driver-replay")
    run_dir, run_json = build_run_bundle(
        catalog,
        github_targets,
        personas,
        scenarios,
        "vscode",
        persona["id"],
        f"{seed}-{scenario_id}-driver-replay",
        "driver-replay-smoke",
        publish_deck=None,
    )
    replay_evidence = [
        (kind, cassette_replay_path(run_json["run_id"], scenario_id, kind))
        for kind in scenario_minimum_evidence(scenario_id)
    ]
    if not replay_evidence:
        raise SystemExit(f"Replay smoke scenario '{scenario_id}' has no minimum evidence contract")
    attached_evidence = []
    for kind, path in replay_evidence:
        # Write the real backing artifact the cassette:// ref points at. A
        # cassette proof ref must resolve to a file on disk (see
        # artifact_ref_exists / is_proof_evidence); minting the ref without
        # backing it was the unbacked-cassette-proof bug this smoke used to
        # exhibit. The content is a deterministic recorded-replay stub.
        rel = cassette_ref_relpath(path)
        if rel:
            backing = run_dir / rel
            backing.parent.mkdir(parents=True, exist_ok=True)
            backing.write_text(
                f"driver-replay cassette artifact\nscenario: {scenario_id}\nkind: {kind}\n"
                f"run: {run_json['run_id']}\n",
                encoding="utf-8",
            )
        attach_evidence(
            run_dir,
            scenario_id,
            kind,
            path,
            "captured",
            "cassette",
            f"driver replay cassette proof for {scenario_id}/{kind}",
            publish_deck=None,
        )
        attached_evidence.append({
            "scenario": scenario_id,
            "kind": kind,
            "path": path,
            "source": "cassette",
        })

    record_driver_event(
        run_dir,
        scenario_id,
        "replay",
        "captured",
        f"Deterministic driver replay followed the {scenario_id} scenario contract and attached every cassette-backed proof ref.",
        "session.open,session.trace,render.tui,visual.observe",
        ",".join(path for _, path in replay_evidence),
        "",
        publish_deck=None,
    )
    record_finding(
        run_dir,
        "strength",
        "Replay driver can close one scenario loop",
        f"The driver replay attached every {scenario_id} minimum-evidence slot and journaled the exact refs it produced.",
        scenario_id,
        "low",
        replay_evidence[-1][1],
        "observed",
        publish_deck=None,
    )
    record_finding(
        run_dir,
        "weakness",
        "Remaining scenarios still need live or cassette passes",
        "The smoke proves one driver loop only; every other scenario still needs evidence or blockers before the run is representative.",
        scenario_id,
        "medium",
        str(run_dir / "driver-handoff.md"),
        "open",
        publish_deck=None,
    )
    record_finding(
        run_dir,
        "issue",
        "Global readiness should stay blocked until all scenarios are captured",
        "Review must remain needs_evidence when only one scenario has proof, even though validation proves the artifact contract is internally consistent.",
        scenario_id,
        "medium",
        str(run_dir / "review.json"),
        "open",
        publish_deck=None,
        origin="seeded",
    )

    reviewed = review_run_bundle(run_dir, publish_deck=None)
    validation = validate_run_bundle(run_dir)
    review_is_expected = driver_replay_review_is_expected_one_scenario(reviewed)
    readiness_status = driver_replay_readiness_status(reviewed.get("review_status", ""))
    status = "passed" if review_is_expected and validation.get("status") == "valid" else "failed"
    report = {
        "status": status,
        "readiness_status": readiness_status,
        "smoke_id": smoke_id,
        "created_at": now_utc(),
        "seed": seed,
        "persona": run_json["persona"],
        "smoke_dir": str(smoke_dir),
        "scenario": {
            "id": scenario_id,
            "expected_incomplete_review": review_is_expected,
        },
        "run": {
            "run_id": run_json["run_id"],
            "run_dir": str(run_dir),
            "deck_path": str(run_dir / "deck.slidey.json"),
            "driver_journal_path": str(run_dir / "driver-journal.md"),
            "driver_handoff_path": str(run_dir / "driver-handoff.md"),
            "media_manifest_path": str(run_dir / "media-manifest.json"),
            "review_path": str(run_dir / "review.json"),
        },
        "attached_evidence": attached_evidence,
        "review": reviewed,
        "review_backlog_summary": reviewed.get("review_backlog_summary", ""),
        "validation": validation,
        "artifacts": {
            "report": str(smoke_dir / "driver-replay-smoke.json"),
            "summary": str(smoke_dir / "driver-replay-smoke.md"),
            "deck": str(smoke_dir / "driver-replay-smoke.slidey.json"),
        },
    }
    write_json(smoke_dir / "driver-replay-smoke.json", report)
    (smoke_dir / "driver-replay-smoke.md").write_text(render_driver_replay_smoke_summary(report), encoding="utf-8")
    deck = render_driver_replay_smoke_deck(report)
    deck_issues: list[dict] = []
    validate_slidey_deck_shape(deck, {"items": []}, deck_issues)
    if deck_issues:
        raise SystemExit(f"driver replay smoke deck validation failed: {validation_issue_summary(deck_issues)}")
    write_json(smoke_dir / "driver-replay-smoke.slidey.json", deck)
    return report


def build_dogfood_smoke(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
) -> dict:
    dogfood_id = f"{slug_timestamp()}-dogfood-{seed}"
    dogfood_dir = DOGFOOD_ROOT / dogfood_id
    dogfood_dir.mkdir(parents=True, exist_ok=False)

    corpus_validation = validate_journey_corpus(personas, scenarios, github_targets)
    matrix_dir, matrix = build_matrix_bundle(github_targets, personas, scenarios, f"{seed}-matrix", "primary")
    assignment = matrix["assignments"][0]
    run_dir, run_json = build_run_bundle(
        catalog,
        github_targets,
        personas,
        scenarios,
        assignment["target"]["id"],
        assignment["persona"]["id"],
        assignment["seed"],
        "dogfood-smoke",
        publish_deck=None,
    )
    seeded = seed_demo_evidence(run_dir, publish_deck=None)
    reviewed = review_run_bundle(run_dir, publish_deck=None)
    run_validation = validate_run_bundle(run_dir)
    rollup = write_matrix_rollup(matrix_dir, [str(run_dir)])
    matrix_validation = validate_matrix_bundle(matrix_dir)
    review_is_usable_for_smoke = dogfood_review_is_expected_demo_only(reviewed)
    status = "passed" if (
        corpus_validation["status"] == "valid"
        and
        review_is_usable_for_smoke
        and run_validation["status"] == "valid"
        and matrix_validation["status"] == "valid"
    ) else "failed"
    report = {
        "status": status,
        "dogfood_id": dogfood_id,
        "created_at": now_utc(),
        "seed": seed,
        "dogfood_dir": str(dogfood_dir),
        "corpus_validation": corpus_validation,
        "matrix": {
            "matrix_id": matrix["matrix_id"],
            "matrix_dir": str(matrix_dir),
            "deck_path": str(matrix_dir / "deck.slidey.json"),
            "scenario_count": matrix["scenario_count"],
            "assignment_count": matrix["assignment_count"],
            "target_count": matrix["target_count"],
        },
        "assignment": {
            "id": assignment["id"],
            "seed": assignment["seed"],
            "target": assignment["target"],
            "persona": assignment["persona"],
        },
        "run": {
            "run_id": run_json["run_id"],
            "run_dir": str(run_dir),
            "deck_path": str(run_dir / "deck.slidey.json"),
            "execution_plan_path": str(run_dir / "execution-plan.md"),
            "driver_plan_path": str(run_dir / "driver-plan.md"),
            "driver_journal_path": str(run_dir / "driver-journal.md"),
            "driver_event_count": seeded.get("driver_event_count", 0),
            "agent_brief_path": str(run_dir / "agent-brief.md"),
            "driver_handoff_path": str(run_dir / "driver-handoff.md"),
            "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
            "media_manifest_path": str(run_dir / "media-manifest.json"),
        },
        "seeded": seeded,
        "review": reviewed,
        "review_is_artifact_loop_usable": review_is_usable_for_smoke,
        "validation": {
            "run": run_validation,
            "matrix": matrix_validation,
        },
        "rollup": rollup,
        "notes": [
            "This smoke is deterministic and does not call a live LLM.",
            "Demo evidence and driver journal placeholders prove aggregation, audit-trail wiring, and deck shape only; live visual MCP or cassette evidence is still required for product claims.",
            "Matrix validation may warn when current GitHub target proof has not been refreshed with --refresh-github-targets.",
        ],
        "artifacts": {
            "report": str(dogfood_dir / "dogfood.json"),
            "summary": str(dogfood_dir / "dogfood.md"),
            "deck": str(dogfood_dir / "deck.slidey.json"),
        },
    }
    write_json(dogfood_dir / "dogfood.json", report)
    (dogfood_dir / "dogfood.md").write_text(render_dogfood_smoke_summary(report), encoding="utf-8")
    write_json(dogfood_dir / "deck.slidey.json", render_dogfood_smoke_deck(report))
    return report


def render_deck(
    run_json: dict,
    metrics: dict,
    evidence: Optional[dict] = None,
    findings: Optional[dict] = None,
    review: Optional[dict] = None,
    execution_plan: Optional[dict] = None,
    media_manifest: Optional[dict] = None,
    scenario_outcomes: Optional[dict] = None,
    driver_plan: Optional[dict] = None,
) -> dict:
    stage_lines = [f"{stage['id']}: {stage['status']}" for stage in run_json["stages"]]
    scenario_lines = [
        f"{scenario['label']}: {scenario['stage']} ({', '.join(scenario['required_mcp'])})"
        for scenario in run_json["scenarios"]
    ]
    captured = []
    if evidence is not None:
        captured = [
            f"{item['scenario']} / {item['kind']} [{item.get('source', evidence_source(item.get('path', ''), item.get('notes', '')))}]: {item.get('path', '')}"
            for item in evidence.get("items", [])
            if item.get("status") in {"captured", "validated"} and item.get("path")
        ]
    playback_items = []
    if media_manifest is not None:
        playback_items = [item for item in media_manifest.get("items", []) if item.get("playback")]
    playback_lines = [
        f"{item['scenario']} / {item['evidence_kind']} ({item['media_kind']}): {item['path']}"
        for item in playback_items
    ]
    if not playback_lines:
        playback_body = "No playback media attached yet. Expected media: product discovery screenshots, onboarding frames, bugfix video, PRD/design captures, feature implementation captures, and product bug filing evidence."
    else:
        playback_body = "\n".join(playback_lines[:12])
    captured_body = "\n".join(captured[:12]) if captured else "No evidence attached yet."
    finding_items = findings.get("items", []) if findings is not None else []
    finding_lines = [
        f"{item['kind']}: {item['title']} ({item.get('severity', 'n/a')})"
        for item in finding_items[:12]
    ]
    findings_body = "\n".join(finding_lines) if finding_lines else "No strengths, weaknesses, issues, or fixes recorded yet."
    filed_issue_lines = [
        f"{item.get('id', item.get('title', 'finding'))}: {item.get('github_issue', {}).get('url', '')}"
        for item in finding_items
        if item.get("github_issue", {}).get("url")
    ]
    issue_evidence_lines = filed_issue_evidence_lines(findings or {})
    weakness_routes = build_weakness_routes(run_json, findings or {"items": []})
    prd_design_intake = build_prd_design_intake(run_json, weakness_routes)
    weakness_route_lines = [
        (
            f"{item.get('finding_id', '')}: {item.get('title', '')} -> "
            f"{item.get('target_story', 'stories/prd')} start ({item.get('scenario', '')}, {item.get('persona', '')})"
        )
        for item in weakness_routes.get("items", [])
    ]
    if weakness_route_lines:
        weakness_routes_body = "\n".join([
            "Intake artifact: prd-design-intake.md",
            f"Intake items: {prd_design_intake.get('summary', {}).get('intake_count', 0)}",
            *weakness_route_lines[:12],
        ])
    else:
        weakness_routes_body = "No open observed weakness findings need PRD/design routing."
    gh_agent = findings.get("gh_agent", {}) if isinstance(findings, dict) and isinstance(findings.get("gh_agent", {}), dict) else {}
    issue_closeout = findings.get("issue_closeout", {}) if isinstance(findings, dict) and isinstance(findings.get("issue_closeout", {}), dict) else {}
    gh_agent_claim_lines = []
    for claim in gh_agent.get("claims", []) or []:
        if not isinstance(claim, dict):
            continue
        details = [
            str(claim.get("issue_url", "")).strip(),
            str(claim.get("comment_url", "")).strip(),
            str(claim.get("job_id", "")).strip(),
        ]
        gh_agent_claim_lines.append("claim=" + ", ".join(part for part in details if part))
    gh_agent_job_lines = []
    for job in gh_agent.get("drained_jobs", []) or gh_agent.get("jobs", []):
        if not isinstance(job, dict):
            continue
        details = [
            f"{job.get('origin_ref', '')}",
            f"state={job.get('state', '')}",
        ]
        if job.get("run_url"):
            details.append(f"run={job.get('run_url')}")
        branch = gh_agent_job_integration_branch(job)
        commit = gh_agent_job_commit_sha(job)
        commit_url = gh_agent_job_commit_url(job)
        if branch and commit:
            landing = f"{branch}@{commit}"
            if commit_url:
                landing += f" ({commit_url})"
            details.append(f"integration={landing}")
        elif job.get("state") == "done":
            details.append("integration=missing")
        if job.get("incident_url"):
            details.append(f"incident={job.get('incident_url')}")
        if job.get("err_msg"):
            details.append(f"error={job.get('err_msg')}")
        evidence_links = gh_agent_job_evidence_links(job)
        if evidence_links:
            details.append("evidence=" + ", ".join(evidence_links))
        elif job.get("state") == "done":
            details.append("evidence=missing")
        triage_links = gh_agent_job_triage_evidence_links(job)
        if triage_links:
            details.append("triage=" + ", ".join(triage_links[:3]))
        elif job.get("state") == "done":
            details.append("triage=missing")
        independent_verify_links = gh_agent_job_independent_verify_links(job)
        if independent_verify_links:
            details.append("independent_verify=" + ", ".join(independent_verify_links[:3]))
        elif job.get("state") == "done":
            details.append("independent_verify=missing")
        gh_agent_job_lines.append(" · ".join(part for part in details if part))
    gh_agent_requested = gh_agent.get("enqueue_status", "") not in {"", "disabled", "dry-run"}
    gh_agent_lines = [
        f"Filing: {len(filed_issue_lines)} issue URL(s)",
        *filed_issue_lines[:8],
        *issue_evidence_lines[:8],
        (
            "Queue: "
            f"{gh_agent.get('enqueue_status', 'not requested')} · "
            f"enqueued {gh_agent.get('enqueued_count', 0)}, skipped {gh_agent.get('skipped_count', 0)}"
        ),
        (
            "Claims: "
            f"{gh_agent.get('claim_status', 'not claimed')} · "
            f"claimed {gh_agent.get('claim_count', 0)}"
        ),
        (
            "Drain: "
            f"{gh_agent.get('drain_status', 'not requested')} · "
            f"drained {gh_agent.get('drained_count', 0)}, done {gh_agent.get('done_count', 0)}, "
            f"failed {gh_agent.get('failed_count', 0)}, active {gh_agent.get('active_count', 0)}"
        ),
    ]
    if gh_agent_requested:
        gh_agent_lines.append("Autonomous report: autonomous-fix-report.md")
    if gh_agent_claim_lines:
        gh_agent_lines.extend(gh_agent_claim_lines[:8])
    closeout_lines = []
    if issue_closeout:
        closeout_lines.append(
            f"Issue close-out: {issue_closeout.get('status', '')} · closed {issue_closeout.get('count', 0)}"
        )
        if issue_closeout.get("summary"):
            closeout_lines.append(str(issue_closeout.get("summary", "")))
        for item in issue_closeout.get("items", []) or []:
            if not isinstance(item, dict):
                continue
            closeout_lines.append(
                "closeout="
                + ", ".join(
                    part
                    for part in [
                        str(item.get("issue_url", "")).strip(),
                        str(item.get("comment_url", "")).strip(),
                        str(item.get("run_url", "")).strip(),
                    ]
                    if part
                )
            )
    if closeout_lines:
        gh_agent_lines.extend(closeout_lines[:8])
    if gh_agent_job_lines:
        gh_agent_lines.extend(gh_agent_job_lines[:8])
    else:
        gh_agent_lines.append("No gh-agent fix runs have been recorded yet.")
    gh_agent_body = "\n".join(gh_agent_lines)
    lens = persona_lens(run_json["persona"])
    persona_body = (
        f"Starting surface: {lens['starting_surface']}\n"
        f"First question: {lens['first_question']}\n"
        f"Evidence emphasis: {lens['evidence_emphasis']}\n"
        f"Escalation trigger: {lens['escalation_trigger']}\n"
        f"Finding bias: {lens['finding_bias']}"
    )
    outcome_lines = []
    finding_matrix_lines = []
    if scenario_outcomes is not None:
        outcome_lines = [
            f"{item['scenario']}: {item['outcome']} - evidence {item['present_evidence_count']}/{item['required_evidence_count']} - findings {sum(item['finding_counts'].get(kind, 0) for kind in ['strength', 'weakness', 'issue', 'fix'])}"
            for item in scenario_outcomes.get("items", [])
        ]
        finding_matrix_lines = [
            (
                f"{item['scenario']}: "
                f"strength {item['finding_counts'].get('strength', 0)}, "
                f"weakness {item['finding_counts'].get('weakness', 0)}, "
                f"issue {item['finding_counts'].get('issue', 0)}, "
                f"fix {item['finding_counts'].get('fix', 0)}, "
                f"blocked {item['finding_counts'].get('blocked', 0)}"
            )
            for item in scenario_outcomes.get("items", [])
        ]
    outcomes_body = "\n".join(outcome_lines) if outcome_lines else "No scenario outcomes generated yet."
    finding_matrix_body = "\n".join(finding_matrix_lines) if finding_matrix_lines else "No scenario finding counts generated yet."
    review_body = "Not reviewed yet."
    if review is not None:
        review_lines = [review.get("summary", "No review summary.")]
        sorted_checks = sorted(
            review.get("checks", []),
            key=lambda check: {"fail": 0, "warn": 1, "pass": 2}.get(check.get("status", ""), 3),
        )
        for check in sorted_checks[:10]:
            review_lines.append(f"{check.get('status', 'unknown')}: {check.get('id', 'check')} - {check.get('summary', '')}")
        review_body = "\n".join(review_lines)
    execution_lines = []
    if execution_plan is not None:
        execution_lines = [
            f"{step['scenario']}: {', '.join(mcp['tool'] for mcp in step['mcp_steps'])}"
            for step in execution_plan.get("steps", [])
        ]
    execution_body = "\n".join(execution_lines) if execution_lines else "Execution plan not generated yet."
    driver_lines = []
    driver_contract_lines = []
    proof_gate_lines = []
    if driver_plan is not None:
        driver_lines = [
            f"{scenario['scenario']}: {scenario['harness']} / {scenario['visual_surface']}"
            for scenario in driver_plan.get("scenarios", [])
        ]
        driver_contract = summarize_driver_action_contract(driver_plan, read_json(SCHEMA))
        driver_contract_lines = [
            f"{row['scenario']}: {'ok' if row['valid'] else 'needs attention'} - "
            f"{row['action_count']}/{row['expected_action_count']} actions, "
            f"journal {'yes' if row['journal_recordable'] else 'no'}"
            for row in driver_contract.get("rows", [])
        ]
        evidence_items_for_gates = evidence.get("items", []) if evidence is not None else []
        captured_evidence = {
            (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
            for item in evidence_items_for_gates
            if item.get("status") in {"captured", "validated"}
        }
        proof_evidence = {
            (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
            for item in evidence_items_for_gates
            if is_proof_evidence(item)
        }
        outcomes_by_scenario = {
            item.get("scenario", ""): item
            for item in scenario_outcomes.get("items", [])
        } if scenario_outcomes is not None else {}
        for scenario in driver_plan.get("scenarios", []):
            gate = scenario.get("quality_gate", {})
            minimum = gate.get("minimum_evidence", [])
            present = [
                item
                for item in minimum
                if (scenario.get("scenario", ""), item) in captured_evidence
            ]
            proof = [
                item
                for item in minimum
                if (scenario.get("scenario", ""), item) in proof_evidence
            ]
            outcome = outcomes_by_scenario.get(scenario.get("scenario", ""), {})
            proof_gate_lines.append(
                f"{scenario.get('scenario', '')}: proof {len(proof)}/{len(minimum)} minimum evidence "
                f"(captured {len(present)}), "
                f"outcome {outcome.get('outcome', 'not_started')} - {gate.get('done_when', '')}"
            )
    driver_body = "\n".join(driver_lines) if driver_lines else "Driver plan not generated yet."
    driver_contract_body = "\n".join(driver_contract_lines) if driver_contract_lines else "Driver action contract not generated yet."
    proof_gates_body = "\n".join(proof_gate_lines) if proof_gate_lines else "Quality gates not generated yet."
    playback_scenes = playback_deck_scenes(media_manifest)
    scenes = [
        {
            "type": "title",
            "title": "Product Journey QA",
            "subtitle": f"{run_json['project']['label']} · {run_json['persona']['label']}",
            "narration": "A deterministic dry run of the product journey QA pipeline.",
        },
        {
            "type": "narrative",
            "eyebrow": "Run shape",
            "title": run_json["run_id"],
            "body": "\n".join(stage_lines),
            "narration": "The run records every expected stage before live or cassette evidence is attached.",
        },
        {
            "type": "narrative",
            "eyebrow": "Persona lens",
            "title": run_json["persona"]["label"],
            "body": persona_body,
            "narration": "The persona lens explains how this reviewer should start, what they should question first, and which evidence matters most.",
        },
        {
            "type": "narrative",
            "eyebrow": "Scenarios",
            "title": "Repeatable tasks",
            "body": "\n".join(scenario_lines),
            "narration": "Each scenario names the story, MCP tools, evidence, and success criteria expected from a real run.",
        },
        {
            "type": "narrative",
            "eyebrow": "Execution plan",
            "title": "MCP capture steps",
            "body": execution_body,
            "narration": "The execution plan turns each scenario into concrete MCP capture steps and attach commands.",
        },
        {
            "type": "narrative",
            "eyebrow": "Driver plan",
            "title": "Harness and visual surfaces",
            "body": driver_body,
            "narration": "The driver plan gives the product-journey QA agent machine-readable harness, visual surface, and evidence instructions.",
        },
        {
            "type": "narrative",
            "eyebrow": "Driver contract",
            "title": "Reusable action loop",
            "body": driver_contract_body,
            "narration": "The driver contract shows whether each scenario still follows the standard open, observe, act, capture, and journal loop.",
        },
        {
            "type": "narrative",
            "eyebrow": "Metrics",
            "title": "Current evidence",
            "body": f"Validated stages: {metrics['validated_stage_count']} / {metrics['stage_count']}\nCaptured stages: {metrics.get('captured_stage_count', 0)}\nScenarios: {metrics['scenario_count']}\nEvidence present: {metrics['present_evidence_count']} / {metrics['required_evidence_count']}\nProof evidence: {metrics.get('proof_evidence_count', 0)} · Demo evidence: {metrics.get('demo_evidence_count', 0)}\nDriver events: {metrics.get('driver_event_count', 0)}\nFindings: {metrics.get('findings_count', 0)}\nStrengths: {metrics.get('strength_count', 0)} · Weaknesses: {metrics.get('weakness_count', 0)} · Fixes: {metrics.get('fix_count', 0)} · Blocked: {metrics.get('blocked_count', 0)}\nProduct bugs found: {metrics['product_bugs_found']}",
            "narration": "This report distinguishes validated evidence from planned stages.",
        },
        {
            "type": "narrative",
            "eyebrow": "Findings",
            "title": "Strengths, weaknesses, issues, fixes",
            "body": findings_body,
            "narration": "The journey report records what worked, what failed, what was found, and what was fixed.",
        },
        {
            "type": "narrative",
            "eyebrow": "GH-agent fixes",
            "title": "Filed issues and autonomous fix runs",
            "body": gh_agent_body,
            "narration": "Filed product-journey issues and gh-agent fix runs are kept together so reviewers can inspect both the original evidence and the autonomous repair evidence.",
        },
        {
            "type": "narrative",
            "eyebrow": "Finding matrix",
            "title": "Findings by scenario",
            "body": finding_matrix_body,
            "narration": "This matrix shows which scenarios produced strengths, weaknesses, issues, fixes, or blockers.",
        },
        {
            "type": "narrative",
            "eyebrow": "PRD/design routes",
            "title": "Weaknesses routed to design",
            "body": weakness_routes_body,
            "narration": "Open weakness findings are routed to the PRD/design pipeline instead of being filed as bugfix work.",
        },
        {
            "type": "narrative",
            "eyebrow": "Scenario outcomes",
            "title": "Per-scenario status",
            "body": outcomes_body,
            "narration": "Each scenario is summarized separately so natural-use gaps remain visible after the bundle-level review passes.",
        },
        {
            "type": "narrative",
            "eyebrow": "Proof gates",
            "title": "Minimum scenario proof",
            "body": proof_gates_body,
            "narration": "Quality gates show whether each scenario has the minimum evidence needed before a live or cassette-backed journey is considered complete.",
        },
        {
            "type": "narrative",
            "eyebrow": "Review readiness",
            "title": metrics.get("review_status", "not_reviewed"),
            "body": review_body,
            "narration": "The review gate checks whether the bundle has enough evidence and findings to discuss.",
        },
        {
            "type": "narrative",
            "eyebrow": "Video playback",
            "title": "Key interactions",
            "body": playback_body,
            "media": playback_items[:12],
            "playback_scene_count": len(playback_scenes),
            "narration": "Slidey scenes carry structured playback media for key visual interactions.",
        },
    ]
    scenes.extend(playback_scenes)
    scenes.extend([
        {
            "type": "narrative",
            "eyebrow": "Captured evidence",
            "title": "Attached artifacts",
            "body": captured_body,
            "narration": "Captured artifacts are linked back to the scenarios that produced them.",
        },
        {
            "type": "narrative",
            "eyebrow": "Next",
            "title": "Evidence to attach",
            "body": "Visual MCP frames, Kitsoki traces, oracle results, and video clips will turn this dry run into a reviewable journey deck.",
            "narration": "The next iteration attaches real visual and trace evidence to these scenes.",
        },
    ])
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey QA",
            "phase": "dry-run",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": scenes,
    }


def resolve_local_repo_path(project: dict) -> tuple[str, list[str]]:
    """Resolve a catalog project's local checkout path portably.

    Priority: the project's own env var (`local_repo_env`, e.g.
    `POSTGRESQL_REPO`) -> `$KITSOKI_PJ_REPOS_DIR/<project id>` -> the
    `~/code/<project id>` convention every other Kitsoki dev doc assumes ->
    a legacy `local_repo_path` catalog field for callers that still set one.
    No machine-specific absolute path is baked into this repo; every path
    here is either an operator-provided env var or derived from a portable
    convention. Returns (resolved_path, candidates_tried) so callers can
    build an actionable message naming every env var that was checked.
    """
    local_repo_env = project.get("local_repo_env", "")
    project_id = project.get("id", "")
    candidates: list[str] = []
    if local_repo_env:
        env_value = os.environ.get(local_repo_env, "")
        if env_value:
            return env_value, candidates
        candidates.append(local_repo_env)
    if project_id:
        repos_dir = os.environ.get("KITSOKI_PJ_REPOS_DIR", "")
        if repos_dir:
            candidate = Path(repos_dir) / project_id
            candidates.append(str(candidate))
            if candidate.exists():
                return str(candidate), candidates
        home_candidate = Path.home() / "code" / project_id
        candidates.append(str(home_candidate))
        if home_candidate.exists():
            return str(home_candidate), candidates
    legacy_path = project.get("local_repo_path", "")
    if legacy_path:
        return legacy_path, candidates
    return "", candidates


def local_repo_path_gate_message(project: dict, candidates: list[str]) -> str:
    """Actionable preflight message for an unresolved local checkout path."""
    local_repo_env = project.get("local_repo_env", "")
    project_id = project.get("id", "unknown")
    env_hint = f"set {local_repo_env}=/path/to/{project_id}, or " if local_repo_env else ""
    return (
        f"Gate: could not resolve a local checkout for '{project_id}'. "
        f"{env_hint}set KITSOKI_PJ_REPOS_DIR=/parent/dir containing a {project_id}/ checkout, "
        f"or place the checkout at ~/code/{project_id} (checked: {', '.join(candidates) or 'none'})."
    )


def run_project_check(project):
    validation_command = project.get("validation_command", "")
    if validation_command:
        result = shell(["bash", "-lc", validation_command], ROOT)
        if result.returncode != 0:
            return {
                "status": "error",
                "notes": f"{project['id']}: local oracle validation failed",
                "output": result.stdout + result.stderr,
                "meta": _meta_value(project),
                "next": [
                    validation_command,
                ],
            }
        return {
            "status": "validated",
            "notes": f"{project['id']}: local oracle validation passed",
            "output": result.stdout + result.stderr,
            "meta": _meta_value(project),
            "next": [
                validation_command,
            ],
        }

    if (
        project.get("run_mode") == "external-benchmark"
        and project.get("status") == "validated"
        and not os.environ.get("BUGFIX_BAKEOFF_RECHECK")
    ):
        return {
            "status": "validated",
            "notes": f"{project['id']}: cached validation; set BUGFIX_BAKEOFF_RECHECK=1 to rerun the heavy external benchmark",
            "meta": _meta_value(project),
            "next": [
                "Set BUGFIX_BAKEOFF_RECHECK=1 to rerun the heavy external-benchmark verifier.",
            ],
        }

    if project["run_mode"] != "external-benchmark":
        return {
            "status": "planned",
            "notes": f"{project['id']} is currently {project['status']}: {project['notes']}",
            "meta": _meta_value(project),
            "next": [
                "Capture manifests and deterministic scoring contract before check command is enabled.",
            ],
        }

    bench = ROOT / "tools" / "bugfix-bakeoff" / "external" / "bench.py"
    result = shell(["python3", str(bench), "meta", "--project", project["id"]], ROOT)
    if result.returncode != 0:
        return {
            "status": "error",
            "meta": _meta_value(project),
            "notes": "bench.py metadata check failed",
            "output": result.stdout + result.stderr,
        }

    try:
        meta = json.loads(result.stdout)
    except json.JSONDecodeError:
        return {
            "status": "error",
            "meta": _meta_value(project),
            "notes": "bench.py returned non-JSON metadata",
            "output": result.stdout + result.stderr,
        }

    default_check_command = f"python3 {bench.as_posix()} verify --project {project['id']}"

    checks = [
        f"Project: {meta['id']}",
        f"Repo:   {meta['repo']}",
        f"Oracles baseline count: {len(meta.get('bugs', []))}",
    ]

    local_repo_env = project.get("local_repo_env", "")
    local_repo_path, checked_candidates = resolve_local_repo_path(project)
    if project.get("local_repo_env"):
        checks.append(f"Local repo env: {local_repo_env}")
    if local_repo_path:
        checks.append(f"Local checkout: {local_repo_path}")

    run_command = project.get("run_command", default_check_command)
    if "<path>" in run_command:
        if local_repo_path:
            run_command = run_command.replace("<path>", local_repo_path)
            checks.append(f"{local_repo_env}={local_repo_path}")
            if not Path(local_repo_path).exists():
                checks.append(f"Gate: resolved path does not exist: {local_repo_path}")
        else:
            checks.append(local_repo_path_gate_message(project, checked_candidates))
    checks.extend([
        "Run command:",
        f"  {run_command}",
    ])

    if project.get("run_mode") == "external-benchmark" and local_repo_path and Path(local_repo_path).exists():
        checks.append("Verifying fixture arming through a no-local temp clone.")
        verify_report = verify_external_project(project, local_repo_path)
        checks.append(f"Verify status: {verify_report['status']}")
        checks.append(f"Verify notes: {verify_report['notes']}")
        if "output" in verify_report and verify_report["output"]:
            checks.append("Verify output:")
            for line in verify_report["output"].splitlines():
                checks.append(f"  {line}")
        return {
            **verify_report,
            "next": checks,
        }

    if project.get("status") == "planned" and local_repo_path and Path(local_repo_path).exists():
        checks.append("Local checkout present; corpus/manifests still pending.")

    return {
        "status": "ready",
        "notes": "External benchmark contract found; deterministic checks are wired.",
        "meta": _meta_value(project),
        "next": checks,
    }


def print_status(catalog):
    print("Product Journey Registry")
    for p in catalog["targets"]:
        print(f"- {p['id']} ({p['status']}): {p['notes']}")
    print("\nPerspectives")
    for p in catalog["perspectives"]:
        print(f"- {p['id']} ({p['status']}) [{p['owner']}]: {p['description']}")


def print_check(catalog, project_id):
    target = next((t for t in catalog["targets"] if t["id"] == project_id), None)
    if target is None:
        known = ", ".join(t["id"] for t in catalog["targets"])
        raise SystemExit(f"Unknown project '{project_id}'. Known: {known}")

    report = run_project_check(target)
    print(f"Project check: {project_id}")
    print(f"Status: {report['status']}")
    print(f"Notes: {report['notes']}")
    print("Next:")
    for step in report["next"]:
        print(f"  {step}")
    if "output" in report:
        print(report["output"])

    print(f"Meta: project={report['meta']['id']} label={report['meta']['label']} status={report['meta']['status']}")
    append_log(f"Checked {project_id}: {report['status']}")


def build_report_payload(catalog: dict, generated_at: str, run_checks: bool) -> dict:
    checks = {}
    if run_checks:
        for target in catalog["targets"]:
            checks[target["id"]] = run_project_check(target)
    return {
        "program": catalog.get("program", "Product journey evaluator"),
        "title": "Product Journey Eval",
        "summary": "Local harness, project lanes, and next product-site work from structured catalog/check artifacts.",
        "generated_at": generated_at,
        "catalog": "tools/product-journey/catalog.json",
        "run_log": ".context/product-journey-runlog.md",
        "reference_deck": "docs/decks/product-journey-eval.slidey.json",
        "next_site_journey": "Stage the local production web build and use it for skeptical-operator walkthroughs.",
        "targets": catalog["targets"],
        "perspectives": catalog["perspectives"],
        "checks": checks,
        "next_steps": [
            {
                "label": "Site journey",
                "status": "next",
                "detail": "Run make web, serve 127.0.0.1:7777, and capture deterministic product-site review evidence.",
            },
            {
                "label": "Fresh evidence",
                "status": "next",
                "detail": "Use --run-checks when refreshing local oracle evidence; keep heavy gears-rust recheck explicit.",
            },
            {
                "label": "Reference deck",
                "status": "done",
                "detail": "Preserve the hand-refined docs/decks/product-journey-eval.slidey.json as the narrative reference.",
            },
        ],
    }


def report_paths(generated_at: str, report_arg: str, deck_arg: str, markdown_arg: str) -> tuple[Path, Path, Path]:
    """Paths for --mode report output.

    Nested under .artifacts/product-journey/eval/<generated-at>/, a sibling of
    the run-bundle root (.artifacts/product-journey/<run-id>/) rather than a
    separate .artifacts/product-journey-eval/ tree, so eval reports and run
    bundles share one artifact root and --prune-runs can tell them apart by
    directory name instead of by timestamp-vs-run-id naming convention alone.
    Explicit --report/--deck/--markdown always win; only the defaults move.
    """
    run_id = generated_at.lower().replace(":", "-")
    for ch in ("/", "\\", " "):
        run_id = run_id.replace(ch, "-")
    base = ARTIFACT_ROOT / "eval" / run_id
    return (
        Path(report_arg) if report_arg else base / "report.json",
        Path(deck_arg) if deck_arg else base / "deck.slidey.json",
        Path(markdown_arg) if markdown_arg else base / "report.md",
    )


def render_report_markdown(payload: dict) -> str:
    lines = [
        f"# {payload['title']}",
        "",
        f"- Program: {payload['program']}",
        f"- Generated: {payload['generated_at']}",
        f"- Catalog: `{payload['catalog']}`",
        f"- Reference deck: `{payload['reference_deck']}`",
        "",
        payload["summary"],
        "",
        "## Targets",
        "",
    ]
    for target in payload["targets"]:
        lines.append(f"- `{target['id']}` ({target.get('stack', '')}): {target.get('status', '')} - {target.get('notes', '')}")
    lines.extend([
        "",
        "## Perspectives",
        "",
    ])
    for perspective in payload["perspectives"]:
        lines.append(f"- `{perspective['id']}` ({perspective.get('owner', '')}): {perspective.get('status', '')} - {perspective.get('description', '')}")
    lines.extend([
        "",
        "## Checks",
        "",
    ])
    if payload["checks"]:
        for target_id, check in payload["checks"].items():
            lines.append(f"- `{target_id}`: {check.get('status', check)}")
    else:
        lines.append("- (not refreshed for this report; pass --run-checks to refresh local oracle evidence)")
    lines.extend([
        "",
        "## Next steps",
        "",
    ])
    for step in payload["next_steps"]:
        lines.append(f"- [{step['status']}] {step['label']}: {step['detail']}")
    return "\n".join(lines) + "\n"


def render_report_deck(payload: dict) -> dict:
    target_lines = [
        f"{target['id']}: {target.get('status', '')} ({target.get('stack', '')})"
        for target in payload["targets"]
    ]
    perspective_lines = [
        f"{perspective['id']}: {perspective.get('status', '')} - owner {perspective.get('owner', '')}"
        for perspective in payload["perspectives"]
    ]
    if payload["checks"]:
        check_lines = [
            f"{target_id}: {check.get('status', check)}"
            for target_id, check in payload["checks"].items()
        ]
    else:
        check_lines = ["Not refreshed for this report; pass --run-checks to refresh local oracle evidence."]
    next_step_lines = [
        f"[{step['status']}] {step['label']}: {step['detail']}"
        for step in payload["next_steps"]
    ]
    return {
        "meta": {
            "mode": "pitch",
            "title": payload["title"],
            "phase": "product-journey-eval",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": payload["title"],
                "subtitle": payload["program"],
                "narration": payload["summary"],
            },
            {
                "type": "narrative",
                "eyebrow": "Targets",
                "title": f"{len(payload['targets'])} project lanes",
                "body": "\n".join(target_lines) or "No targets in catalog.",
                "narration": "Every project lane the catalog currently tracks, with its validation status.",
            },
            {
                "type": "narrative",
                "eyebrow": "Perspectives",
                "title": f"{len(payload['perspectives'])} evaluator perspectives",
                "body": "\n".join(perspective_lines) or "No perspectives in catalog.",
                "narration": "The owning perspectives the catalog is evaluated from.",
            },
            {
                "type": "narrative",
                "eyebrow": "Checks",
                "title": "Local oracle evidence",
                "body": "\n".join(check_lines),
                "narration": "Structured check results captured for this report, if --run-checks was requested.",
            },
            {
                "type": "narrative",
                "eyebrow": "Next steps",
                "title": "Open follow-ups",
                "body": "\n".join(next_step_lines) or "No open follow-ups.",
                "narration": "Remaining and completed follow-up items tracked for this report.",
            },
        ],
    }


def write_report(catalog: dict, generated_at: str, report_path: Path, deck_path: Path, markdown_path: Path, run_checks: bool) -> None:
    payload = build_report_payload(catalog, generated_at, run_checks)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")

    deck_path.parent.mkdir(parents=True, exist_ok=True)
    write_json(deck_path, render_report_deck(payload))

    markdown_path.parent.mkdir(parents=True, exist_ok=True)
    markdown_path.write_text(render_report_markdown(payload), encoding="utf-8")

    print(f"Report: {report_path}")
    print(f"Deck: {deck_path}")
    print(f"Markdown: {markdown_path}")


def prune_runs(keep: int, dry_run: bool) -> dict:
    """Retention policy for the transient run-bundle sprawl.

    Smoke iterations pile up hundreds of timestamped run dirs directly under
    .artifacts/product-journey/. This keeps anything whose name contains
    ``-final`` (curated keepers) plus the newest ``keep`` run dirs, and removes
    the rest. The matrices/, dogfood/, target-proofs/, eval/, marathon-smokes/,
    and preflights/ subtrees are never touched — every non-run-bundle sibling
    directory under ARTIFACT_ROOT (see MATRIX_ROOT/TARGET_PROOF_ROOT/
    DOGFOOD_ROOT/PREFLIGHT_ROOT and the eval/ report root) must be listed here
    or it silently gets swept up with the timestamped run dirs. Dry-run by
    default so callers see what would go before it goes.
    """
    import shutil

    protected_names = {
        "matrices",
        "dogfood",
        "target-proofs",
        "eval",
        "marathon-smokes",
        "preflights",
    }
    run_dirs = [
        path
        for path in ARTIFACT_ROOT.iterdir()
        if path.is_dir() and path.name not in protected_names
    ]
    # Newest first by directory name (names are sortable UTC timestamps).
    run_dirs.sort(key=lambda p: p.name, reverse=True)

    kept: list[str] = []
    removed: list[str] = []
    survivors = 0
    for path in run_dirs:
        is_final = "-final" in path.name
        within_keep = survivors < keep
        if is_final or within_keep:
            kept.append(path.name)
            if not is_final:
                survivors += 1
            continue
        removed.append(path.name)
        if not dry_run:
            shutil.rmtree(path)

    return {
        "status": "pruned" if not dry_run else "dry-run",
        "root": str(ARTIFACT_ROOT),
        "kept": kept,
        "removed": removed,
        "kept_count": len(kept),
        "removed_count": len(removed),
        "keep": keep,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="", help="persona-qa.yaml project config for portable kit paths")
    parser.add_argument("--project", default="gears-rust", help="Project id from catalog or github-targets")
    parser.add_argument(
        "--mode",
        default="status",
        choices=["status", "check", "report"],
        help="status: print catalog, check: validate a single project",
    )
    parser.add_argument("--persona", default="", help="Persona id from tools/product-journey/personas.json")
    parser.add_argument("--seed", default="default", help="Deterministic run seed")
    parser.add_argument("--smoke-scenario", default="bugfix", help="Scenario id for --driver-replay-smoke")
    parser.add_argument("--smoke-persona", default="core-maintainer", help="Persona id for driver replay smoke/sweep")
    parser.add_argument("--live-budget-minutes", type=int, default=20, help="Per-scenario live/model budget written into emitted run contracts")
    parser.add_argument("--run-log", action="store_true", help="Force a timestamped run log entry")
    parser.add_argument("--emit-run", action="store_true", help="Write a no-LLM run artifact bundle and Slidey deck")
    parser.add_argument("--driver", default="", help="Driver manifest id or path for --emit-run/--emit-matrix")
    parser.add_argument("--driver-smoke", default="", help="Deprecated; use --gate driver-manifest --driver <id-or-path>. Validate a driver manifest id/path without launching the target")
    parser.add_argument(
        "--gate",
        default="",
        help=(
            "Comma-separated gate name(s), or 'all', to run one or more deterministic no-LLM gates "
            "with uniform gate_status (pass/fail) + readiness_status JSON/exit-code semantics. Names: "
            "driver-manifest (uses --driver), dogfood, driver-replay (uses --smoke-scenario/--smoke-persona), "
            "ghagent, autonomous-fix, persona-autofix, autonomous-marathon (uses --autonomous-marathon-smoke-repeats), "
            "marathon-ledger (uses --marathon-smoke-ledger; not included in 'all', needs an existing ledger). "
            "Replaces the deprecated --*-smoke flags; see tools/product-journey/README.md#gates."
        ),
    )
    parser.add_argument("--scenarios", default="", help="Comma-separated scenario ids to include with --emit-run")
    parser.add_argument(
        "--transport",
        default="",
        help=(
            "Comma-separated transport ids (tui,web,vscode,cli) or 'all' to expand --emit-run's "
            "execution-plan/driver-plan into scenario x transport checks. Omit to keep today's "
            "single-surface-per-scenario output."
        ),
    )
    parser.add_argument("--transport-suite", action="store_true", help="Preview scenario x transport checks without creating a run bundle")
    parser.add_argument("--live-profile", default="", help="With --transport-suite, the explicit live backend profile authorizing live drive for legs that need it (natural_utterances/live harness); omit to preview the same replay-only warning `check` legs surface")
    parser.add_argument("--emit-matrix", action="store_true", help="Write a no-LLM 10-repo GitHub journey matrix")
    parser.add_argument("--dogfood-smoke", action="store_true", help="Deprecated; use --gate dogfood. Run a deterministic no-LLM matrix-to-rollup smoke and write review artifacts")
    parser.add_argument("--driver-replay-smoke", action="store_true", help="Deprecated; use --gate driver-replay. Run a deterministic no-LLM one-scenario driver replay smoke with cassette evidence")
    parser.add_argument("--driver-replay-sweep", action="store_true", help="Run deterministic no-LLM driver replay smokes for every scenario")
    parser.add_argument("--capture-preflight", action="store_true", help="Run no-LLM capture-toolchain preflight checks")
    parser.add_argument("--native-ghagent-smoke", action="store_true", help="Deprecated; use --gate ghagent. Run no-LLM native gh-agent enqueue/drain smoke through kitsoki commands")
    parser.add_argument("--autonomous-fix-smoke", action="store_true", help="Deprecated; use --gate autonomous-fix. Run no-LLM full autonomous issue filing and gh-agent fix smoke")
    parser.add_argument("--persona-autofix-smoke", action="store_true", help="Deprecated; use --gate persona-autofix. Run no-LLM persona replay issue-to-fix smoke through the gitops autonomous gate")
    parser.add_argument("--autonomous-marathon-smoke", action="store_true", help="Deprecated; use --gate autonomous-marathon. Run no-LLM scoped persona-QA marathon smoke through native autonomous fix and stats")
    parser.add_argument("--autonomous-marathon-smoke-repeats", type=int, default=1, help="Number of full active-persona cycles for --gate autonomous-marathon")
    parser.add_argument("--validate-marathon-smoke-ledger", action="store_true", help="Deprecated; use --gate marathon-ledger. Validate a retained autonomous marathon smoke JSON ledger")
    parser.add_argument("--marathon-smoke-ledger", default="", help="Path to autonomous-marathon-smoke.json for --validate-marathon-smoke-ledger")
    parser.add_argument("--min-marathon-smoke-cycles", type=int, default=1, help="Minimum cycle_count required when validating a retained autonomous marathon smoke ledger")
    parser.add_argument("--report-invalid-marathon-smoke-ledger", action="store_true", help="Print invalid retained-ledger validation JSON instead of exiting early")
    parser.add_argument("--preflight-command", default="", help="Override the webshot smoke command for --capture-preflight tests")
    parser.add_argument("--preflight-studio-command", default="", help="Override the studio.ping smoke command for --capture-preflight tests")
    parser.add_argument("--preflight-quota-state", default="", help="Override provider quota state file for --capture-preflight tests")
    parser.add_argument("--preflight-timeout", type=int, default=90, help="Timeout in seconds for --capture-preflight webshot smoke")
    parser.add_argument("--validate-corpus", action="store_true", help="Validate personas, scenarios, and GitHub target catalog without writing artifacts")
    parser.add_argument("--refresh-github-targets", action="store_true", help="Query GitHub for current open bug counts and write a target-proof artifact")
    parser.add_argument("--target-proof-file", default="", help="target-proof.json or target-proof directory to merge into --emit-matrix")
    parser.add_argument("--rollup-matrix", action="store_true", help="Aggregate reviewed run bundles into a matrix rollup deck")
    parser.add_argument("--validate-run", action="store_true", help="Validate an existing run bundle without rewriting artifacts")
    parser.add_argument("--validate-matrix", action="store_true", help="Validate an existing matrix bundle without rewriting artifacts")
    parser.add_argument("--strict-target-proof", action="store_true", help="With --validate-matrix, require refreshed GitHub proof for every target")
    parser.add_argument("--matrix-dir", default="", help="Existing .artifacts/product-journey/matrices/<matrix-id> directory")
    parser.add_argument("--rollup-run-dir", action="append", default=[], help="Run bundle directory to include in --rollup-matrix; repeatable")
    parser.add_argument(
        "--matrix-personas",
        default="primary",
        choices=["primary", "all"],
        help="primary: one deterministic persona per target; all: every persona for every target",
    )
    parser.add_argument("--attach-evidence", action="store_true", help="Attach one evidence artifact to an existing run bundle")
    parser.add_argument("--record-finding", action="store_true", help="Record one strength, weakness, issue, or fix in an existing run bundle")
    parser.add_argument("--record-blocker", action="store_true", help="Record an explicit blocked scenario as an issue finding")
    parser.add_argument("--record-driver-event", action="store_true", help="Append one driver execution event to driver-journal.json")
    parser.add_argument("--record-autonomous-driver-dispatch", action="store_true", help="Write the story-owned autonomous driver dispatch receipt into a run bundle")
    parser.add_argument("--campaign-worker", action="store_true", help="Record/import a local, arena, or VM campaign worker readiness receipt into a run bundle")
    parser.add_argument("--seed-demo-evidence", action="store_true", help="Attach deterministic demo evidence and findings to an existing run bundle")
    parser.add_argument("--file-findings", action="store_true", help="File the bundle's credible issue findings as GitHub issues via the kitsoki bug orchestration")
    parser.add_argument("--file-local-findings", action="store_true", help="File the bundle's credible issue findings as local .artifacts/issues/bugs tickets via kitsoki bug create --sink local-artifact (the default campaign finding sink; see AGENTS.md)")
    parser.add_argument("--local-sink-target", default="kitsoki", choices=["kitsoki", "story"], help="With --file-local-findings, the kitsoki bug create --target to file into (default: kitsoki)")
    parser.add_argument("--local-sink-target-dir", default="", help="With --file-local-findings, override the local sink target-root; defaults to the kitsoki repo root")
    parser.add_argument("--autonomous-fix-loop", action="store_true", help="Internal legacy test backend for kitsoki gitops autonomous-fix")
    parser.add_argument("--autonomous-marathon", action="store_true", help="Create or finalize a standing persona-QA marathon through native autonomous fix, review, validation, and stats")
    parser.add_argument("--autonomous-marathon-due", action="store_true", help="Scan standing persona-QA marathon control artifacts and report due cadence cycles")
    parser.add_argument("--autonomous-marathon-advance-due", action="store_true", help="Advance the next due standing persona-QA marathon cycle through the native autonomous marathon path")
    parser.add_argument("--autonomous-marathon-watchdog", action="store_true", help="Check a standing persona-QA marathon heartbeat against its watchdog control artifact")
    parser.add_argument(
        "--autonomous-driver-mode",
        default="pending",
        choices=["pending", "replay", "record", "live"],
        help="With --autonomous-marathon creation, pending emits a driver handoff; replay attaches cassette-backed proof and runs the native final gates; record/live are story-dispatched through host.agent.task",
    )
    parser.add_argument("--autonomous-driver-live-profile", default="", help="Required explicit live backend profile for record/live autonomous driver dispatch")
    parser.add_argument("--autonomous-cadence-hours", type=int, default=24, help="Cadence recorded in autonomous marathon control artifacts")
    parser.add_argument("--autonomous-heartbeat-minutes", type=int, default=15, help="Heartbeat interval recorded in autonomous marathon control artifacts")
    parser.add_argument("--autonomous-watchdog-minutes", type=int, default=45, help="Watchdog escalation interval recorded in autonomous marathon control artifacts")
    parser.add_argument("--watchdog-now", default="", help="Deterministic ISO timestamp for --autonomous-marathon-watchdog tests")
    parser.add_argument("--due-now", default="", help="Deterministic ISO timestamp for autonomous marathon due/advance tests")
    parser.add_argument("--due-limit", type=int, default=10, help="Maximum due/upcoming/blocked marathon rows returned by autonomous marathon due/advance")
    parser.add_argument("--report-invalid-autonomous-fix", action="store_true", help="With internal autonomous-fix backends, print invalid gate JSON and exit 0 so story callers can bind failure evidence")
    parser.add_argument("--report-invalid-autonomous-marathon", action="store_true", help="With --autonomous-marathon, print invalid marathon JSON and exit 0 so story callers can bind failure evidence")
    parser.add_argument("--report-blocked-autonomous-watchdog", action="store_true", help="With --autonomous-marathon-watchdog, print blocked watchdog JSON and exit 0 so story callers can bind failure evidence")
    parser.add_argument("--stats", action="store_true", help="Derive product-journey issue stats from run bundles and cached issue state")
    parser.add_argument("--stats-root", default="", help="Root containing product-journey run bundles for --stats; defaults to .artifacts/product-journey")
    parser.add_argument("--issue-state-file", default="", help="Optional JSON fixture/cache with GitHub issue state for --stats")
    parser.add_argument("--refresh-issue-state", nargs="?", const="true", default="", help="Before --stats, refresh the issue-state cache through kitsoki gitops issue-state-cache (true/false)")
    parser.add_argument("--stats-output", default="", help="Optional path to write the derived --stats JSON")
    parser.add_argument("--similarity-threshold", type=float, default=0.82, help="Title similarity threshold for --stats duplicate/similar issue detection")
    parser.add_argument("--similar-pair-limit", type=int, default=25, help="Maximum similar issue pairs to include in --stats JSON; use -1 for all pairs")
    parser.add_argument("--ticket-repo", default="", help="owner/repo GitHub target for --file-findings")
    parser.add_argument("--gh-agent-db", default="", help="With --file-findings, enqueue filed issues into this gh-agent SQLite job DB")
    parser.add_argument("--gh-agent-story", default="stories/bugfix", help="Story path to queue for filed issue fixes when --gh-agent-db is set")
    parser.add_argument("--gh-agent-drain", action="store_true", help="With --file-findings and --gh-agent-db, immediately drain queued gh-agent fixes")
    parser.add_argument("--gh-agent-public-base-url", default="", help="Public gh-agent base URL used when draining queued fixes")
    parser.add_argument("--gh-agent-project-root", default="", help="Local checkout root used by gh-agent drain for onboarded target repos")
    parser.add_argument("--gh-agent-incident-repo", default="", help="Repo for gh-agent incident tickets during drain; defaults to --ticket-repo")
    parser.add_argument("--gh-agent-asset-dir", default="", help="Root directory for gh-agent drain artifacts; defaults to <run-dir>/gh-agent-assets")
    parser.add_argument("--gh-agent-comment-mode", default="none", choices=["none", "github"], help="Comment mode for gh-agent drain; none keeps deterministic gates offline")
    parser.add_argument("--dry-run", action="store_true", help="With --file-findings, render what would be filed without calling GitHub")
    parser.add_argument("--debug-file", action="store_true", help="Allow real --file-findings filing as an isolated diagnostic; the full issue-to-fix path is autonomous-fix")
    parser.add_argument(
        "--filing-mode",
        default="file",
        choices=["file", "dry-run"],
        help="Value-style alias for --dry-run so story slots can select the mode (--filing-mode dry-run)",
    )
    parser.add_argument("--scenario-qa-report", action="store_true", help="Fold stories/scenario-qa's recorded per-transport check results into <run-dir>/deck.slidey.json")
    parser.add_argument(
        "--scenario-qa-workspace",
        action="store_true",
        help="For scenario-qa --emit-run calls, create/reuse a managed dev workspace and emit the run bundle there",
    )
    parser.add_argument(
        "--scenario-qa-workspace-id",
        default="",
        help="Managed dev workspace id for --scenario-qa-workspace (default: $KITSOKI_SCENARIO_QA_WORKSPACE_ID or scenario-qa)",
    )
    parser.add_argument("--scenario-description", default="", help="Free-text ad-hoc scenario name for --scenario-qa-report when no --scenario id was used")
    parser.add_argument("--leg-results-json", default="", help="JSON object (or @path to a JSON file) with {items:[...]} per-transport check driver+judge outcomes, for --scenario-qa-report")
    parser.add_argument("--review-run", action="store_true", help="Review an existing run bundle for readiness")
    parser.add_argument("--driver-handoff", action="store_true", help="Refresh and print the product-journey QA driver handoff artifact")
    parser.add_argument("--summarize-run", action="store_true", help="Print the story-load summary for an existing run bundle")
    parser.add_argument("--run-dir", default="", help="Existing .artifacts/product-journey/<run-id> directory")
    parser.add_argument("--scenario", default="", help="Scenario id for --attach-evidence")
    parser.add_argument("--evidence-kind", default="", help="Evidence kind for --attach-evidence")
    parser.add_argument("--evidence-path", default="", help="Path, retained media id, URL, or trace reference for --attach-evidence")
    parser.add_argument(
        "--evidence-status",
        default="captured",
        choices=["captured", "validated", "rejected"],
        help="Status for --attach-evidence",
    )
    parser.add_argument(
        "--evidence-source",
        default="",
        choices=["", *sorted(EVIDENCE_SOURCES)],
        help="Evidence source for --attach-evidence; inferred from path when omitted",
    )
    parser.add_argument("--notes", default="", help="Notes for --attach-evidence")
    parser.add_argument(
        "--dispatch-mode",
        default="replay",
        choices=["replay", "record", "live"],
        help="Driver dispatch mode for --record-driver-event or --record-autonomous-driver-dispatch",
    )
    parser.add_argument(
        "--driver-status",
        default="attempted",
        choices=["attempted", "captured", "blocked", "validated"],
        help="Driver event status for --record-driver-event",
    )
    parser.add_argument("--mcp-tools", default="", help="Comma-separated MCP tools used for --record-driver-event")
    parser.add_argument("--evidence-refs", default="", help="Comma-separated evidence refs produced for --record-driver-event")
    parser.add_argument("--blockers", default="", help="Comma-separated blockers observed for --record-driver-event or --record-autonomous-driver-dispatch")
    parser.add_argument(
        "--dispatch-status",
        default="captured",
        choices=["captured", "blocked", "degraded-evidence", "failed"],
        help="Autonomous driver task status for --record-autonomous-driver-dispatch",
    )
    parser.add_argument("--driver-trace", default="", help="Trace or journal path for --record-autonomous-driver-dispatch")
    parser.add_argument("--autonomous-driver-evidence-count", type=int, default=0, help="Evidence count reported by the autonomous driver task")
    parser.add_argument("--autonomous-driver-issue-count", type=int, default=0, help="Issue count reported by the autonomous driver task")
    parser.add_argument("--worker-backend", default="local", choices=["local", "arena", "vm"], help="Worker backend for --campaign-worker")
    parser.add_argument("--worker-id", default="", help="Worker identity for --campaign-worker receipts")
    parser.add_argument("--worker-status", default="ready", choices=["ready", "blocked", "running", "completed", "failed"], help="Worker status for --campaign-worker")
    parser.add_argument("--worker-ready-status", default="", choices=["", "pass", "warn", "fail"], help="Readiness gate status for --campaign-worker")
    parser.add_argument("--worker-ready-summary", default="", help="Human-readable readiness summary for --campaign-worker")
    parser.add_argument("--worker-budget-minutes", type=int, default=0, help="Per-scenario budget asserted by the worker receipt")
    parser.add_argument("--worker-receipt-source", default="", help="External receipt, arena run id, or operator note that produced --campaign-worker")
    parser.add_argument("--worker-import-artifact", action="append", default=[], help="Artifact path imported from the worker; repeatable")
    parser.add_argument(
        "--finding-kind",
        default="issue",
        choices=["strength", "weakness", "issue", "fix"],
        help="Finding kind for --record-finding",
    )
    parser.add_argument("--title", default="", help="Finding title for --record-finding")
    parser.add_argument("--summary", default="", help="Finding summary for --record-finding")
    parser.add_argument("--severity", default="medium", help="Finding severity for --record-finding")
    parser.add_argument(
        "--finding-status",
        default="observed",
        choices=["open", "fixed", "observed", "validated", "blocked"],
        help="Finding status for --record-finding",
    )
    parser.add_argument("--json-output", action="store_true", help="Print machine-readable JSON for story/host.run callers")
    parser.add_argument(
        "--publish-deck",
        action="store_true",
        help="Also update docs/decks/product-journey-eval.slidey.json with the generated deck",
    )
    parser.add_argument("--generated-at", default="", help="required for --mode report; deterministic timestamp")
    parser.add_argument("--report", default="", help="structured report JSON for --mode report; default is .artifacts/product-journey/<generated-at>/report.json")
    parser.add_argument("--deck", default="", help="generated Slidey spec for --mode report; default is .artifacts/product-journey/<generated-at>/deck.slidey.json")
    parser.add_argument("--markdown", default="", help="generated Markdown index for --mode report; default is .artifacts/product-journey/<generated-at>/report.md")
    parser.add_argument("--run-checks", action="store_true", help="refresh target checks while building report")
    parser.add_argument("--prune-runs", action="store_true", help="Remove transient run-bundle dirs, keeping *-final keepers and the newest --keep runs")
    parser.add_argument("--keep", type=int, default=10, help="Number of newest non-final run dirs to keep with --prune-runs")
    parser.add_argument("--apply", action="store_true", help="With --prune-runs, actually delete (default is dry-run)")
    args = parser.parse_args()

    if rerun_scenario_qa_in_workspace(args):
        return

    apply_persona_qa_config(args.config)
    catalog = load_catalog(CATALOG)
    all_personas = load_personas(PERSONAS)
    all_scenarios = load_scenarios(SCENARIOS)
    personas = active_personas(all_personas)
    scenarios = active_scenarios(all_scenarios)
    github_targets = load_github_targets(GITHUB_TARGETS)

    # --driver-smoke (deprecated) and --gate driver-manifest are handled by the
    # unified gate dispatcher below, alongside the other --*-smoke gates.

    if args.transport_suite:
        scenario_filter = args.scenarios or args.scenario or ""
        selected_scenarios = select_scenarios(scenarios, scenario_filter)
        selected_transports = select_transports(args.transport or "all")
        suite = build_transport_suite(
            selected_scenarios,
            selected_transports,
            load_driver_manifest(args.driver or ""),
            live_profile=args.live_profile,
        )
        if args.json_output:
            print(json.dumps(suite, sort_keys=True))
        else:
            print(render_transport_suite(suite))
        return

    if args.prune_runs:
        result = prune_runs(keep=args.keep, dry_run=not args.apply)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
        else:
            print(f"Prune {result['status']}: kept {result['kept_count']}, removed {result['removed_count']} (keep={result['keep']})")
            for name in result["removed"]:
                print(f"- remove {name}")
            if not args.apply:
                print("(dry-run; re-run with --apply to delete)")
        append_log(f"Pruned product journey runs: {result['status']} kept={result['kept_count']} removed={result['removed_count']}")
        return

    if args.validate_corpus:
        result = validate_journey_corpus(all_personas, all_scenarios, github_targets)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Validated product journey corpus: {result['status']}")
            if result["status"] != "valid":
                raise SystemExit(1)
            return
        print(f"Corpus validation status: {result['status']}")
        print(f"Personas: {result['personas']}")
        print(f"Scenarios: {result['scenarios']}")
        print(f"GitHub targets: {result['targets']}")
        print(f"Errors: {result['errors']}")
        print(f"Warnings: {result['warnings']}")
        for issue in result["issues"]:
            detail = f" ({issue['detail']})" if issue.get("detail") else ""
            print(f"- {issue['severity']}: {issue['id']}: {issue['message']}{detail}")
        append_log(f"Validated product journey corpus: {result['status']}")
        if result["status"] != "valid":
            raise SystemExit(1)
        return

    if args.capture_preflight:
        result = capture_preflight(
            args.seed,
            args.preflight_command,
            args.preflight_timeout,
            args.preflight_studio_command,
            args.preflight_quota_state,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Ran capture preflight {result['preflight_id']}: {result['status']}")
            if result["status"] != "passed":
                raise SystemExit(1)
            return
        print(f"Product journey capture preflight: {result['preflight_id']}")
        print(f"Status: {result['status']}")
        print(f"Artifacts: {result['preflight_dir']}")
        print(f"Webshot: {result['webshot_output']}")
        print(f"Checks: {result['passed']} passed, {result['failed']} failed")
        for check in result["checks"]:
            print(f"- {check['status']}: {check['id']} ({check['summary']})")
        append_log(f"Ran capture preflight {result['preflight_id']}: {result['status']}")
        if result["status"] != "passed":
            raise SystemExit(1)
        return

    # --- Unified gate dispatcher -------------------------------------------------
    # Replaces the 8 overlapping --*-smoke flags (--driver-smoke, --dogfood-smoke,
    # --driver-replay-smoke, --native-ghagent-smoke, --autonomous-fix-smoke,
    # --persona-autofix-smoke, --autonomous-marathon-smoke,
    # --validate-marathon-smoke-ledger) with one --gate <name>[,<name>...]|all
    # verb. Every gate's JSON payload additively gains "gate" (the gate name),
    # "gate_status" ("pass"/"fail"), and "readiness_status" (the gate's own
    # value, or "not_applicable" when the gate has no readiness concept) so
    # callers get one uniform pass/fail + readiness shape without losing any
    # existing field a story/tool already binds on. The deprecated flags below
    # keep working: each sets --driver (for --driver-smoke's value) and
    # forwards into the same gate names, printing a one-line deprecation
    # notice to stderr (never stdout, so JSON callers keep parsing clean).
    _GATE_OLD_FLAGS = {
        "driver-manifest": "--driver-smoke",
        "dogfood": "--dogfood-smoke",
        "driver-replay": "--driver-replay-smoke",
        "ghagent": "--native-ghagent-smoke",
        "autonomous-fix": "--autonomous-fix-smoke",
        "persona-autofix": "--persona-autofix-smoke",
        "autonomous-marathon": "--autonomous-marathon-smoke",
        "marathon-ledger": "--validate-marathon-smoke-ledger",
    }
    # marathon-ledger is excluded from 'all': it validates a previously
    # written --marathon-smoke-ledger artifact rather than running a
    # self-contained check, so it has nothing to do without that input.
    _GATE_ALL_ORDER = [
        "driver-manifest",
        "dogfood",
        "driver-replay",
        "ghagent",
        "autonomous-fix",
        "persona-autofix",
        "autonomous-marathon",
    ]

    def _gate_envelope(name: str, payload: dict, ok: bool) -> dict:
        envelope = dict(payload)
        envelope["gate"] = name
        envelope["gate_status"] = "pass" if ok else "fail"
        envelope.setdefault("readiness_status", "not_applicable")
        return envelope

    def _run_gate(name: str):
        """Run one deterministic gate. Returns (json_payload, ok, human_lines, log_line)."""
        if name == "driver-manifest":
            result = validate_driver_manifest(load_driver_manifest(args.driver))
            ok = result["status"] == "ok"
            human_lines = [
                f"Driver manifest: {result['status']}",
                f"Driver: {result['driver']['id']} ({result['driver']['label']})",
                f"Manifest: {result['driver']['manifest_path']}",
                *[f"- {issue['severity']}: {issue['id']}: {issue['detail']}" for issue in result["issues"]],
            ]
            return result, ok, human_lines, f"Validated driver manifest {result['driver']['id']}"

        if name == "ghagent":
            result = native_ghagent_smoke()
            ok = result["status"] == "passed"
            human_lines = [f"Native gh-agent smoke: {result['status']}", result["summary"]]
            if result["output"]:
                human_lines.append(result["output"])
            return result, ok, human_lines, f"Ran native gh-agent smoke: {result['status']}"

        if name == "autonomous-fix":
            result = autonomous_fix_smoke()
            ok = result["status"] == "passed"
            human_lines = [f"Autonomous fix smoke: {result['status']}", result["summary"]]
            if result["output"]:
                human_lines.append(result["output"])
            return result, ok, human_lines, f"Ran autonomous fix smoke: {result['status']}"

        if name == "persona-autofix":
            result = persona_autofix_smoke()
            ok = result["status"] == "passed"
            human_lines = [f"Persona autofix smoke: {result['status']}", result["summary"]]
            if result["output"]:
                human_lines.append(result["output"])
            return result, ok, human_lines, f"Ran persona autofix smoke: {result['status']}"

        if name == "autonomous-marathon":
            result = autonomous_marathon_smoke(args.autonomous_marathon_smoke_repeats)
            ok = result["status"] == "passed"
            human_lines = [f"Autonomous marathon smoke: {result['status']}", result["summary"]]
            if result.get("report_path"):
                human_lines.append(f"Ledger JSON: {result['report_path']}")
            if result.get("report_markdown_path"):
                human_lines.append(f"Ledger Markdown: {result['report_markdown_path']}")
            if result["output"]:
                human_lines.append(result["output"])
            return result, ok, human_lines, f"Ran autonomous marathon smoke: {result['status']}"

        if name == "marathon-ledger":
            if not args.marathon_smoke_ledger and not args.report_invalid_marathon_smoke_ledger:
                raise SystemExit("--gate marathon-ledger requires --marathon-smoke-ledger")
            result = validate_marathon_smoke_ledger(args.marathon_smoke_ledger, args.min_marathon_smoke_cycles)
            # --report-invalid-marathon-smoke-ledger asks for the invalid JSON
            # back with a clean (non-raising) exit so callers can bind the
            # failure evidence themselves; preserve that as this gate's ok.
            ok = result["status"] == "valid" or args.report_invalid_marathon_smoke_ledger
            human_lines = [
                f"Autonomous marathon smoke ledger: {result['status']}",
                result["summary"],
                f"Ledger JSON: {result['ledger_path']}",
                f"Ledger Markdown: {result['ledger_markdown_path']}",
            ]
            for issue in result["issues"]:
                detail = f" ({issue['detail']})" if issue.get("detail") else ""
                human_lines.append(f"- {issue['severity']}: {issue['id']}: {issue['message']}{detail}")
            return result, ok, human_lines, f"Validated autonomous marathon smoke ledger {Path(args.marathon_smoke_ledger).name}: {result['status']}"

        if name == "dogfood":
            report = build_dogfood_smoke(catalog, github_targets, personas, scenarios, args.seed)
            payload = {
                "status": report["status"],
                "dogfood_id": report["dogfood_id"],
                "dogfood_dir": report["dogfood_dir"],
                "report_path": report["artifacts"]["report"],
                "summary_path": report["artifacts"]["summary"],
                "deck_path": report["artifacts"]["deck"],
                "matrix_dir": report["matrix"]["matrix_dir"],
                "matrix_deck_path": report["matrix"]["deck_path"],
                "run_dir": report["run"]["run_dir"],
                "run_deck_path": report["run"]["deck_path"],
                "rollup_deck_path": report["rollup"]["deck_path"],
                "corpus_validation_status": report["corpus_validation"]["status"],
                "corpus_validation_errors": report["corpus_validation"]["errors"],
                "corpus_validation_warnings": report["corpus_validation"]["warnings"],
                "review_status": report["review"]["review_status"],
                "review_summary": report["review"]["summary"],
                "review_passed": report["review"].get("review_passed_count", report["review"].get("passed", 0)),
                "review_warnings": report["review"]["warnings"],
                "review_failed": report["review"].get("review_failed_count", report["review"].get("failed", 0)),
                "review_total": report["review"].get("review_total_count", report["review"].get("total", 0)),
                "review_backlog_summary": report["review"].get("review_backlog_summary", ""),
                "run_validation_status": report["validation"]["run"]["status"],
                "run_validation_warnings": report["validation"]["run"]["warnings"],
                "run_validation_issue_summary": report["validation"]["run"].get("validation_issue_summary", ""),
                "validation_issue_summary": report["validation"]["run"].get("validation_issue_summary", ""),
                "matrix_validation_status": report["validation"]["matrix"]["status"],
                "matrix_validation_warnings": report["validation"]["matrix"]["warnings"],
                "matrix_validation_issue_summary": report["validation"]["matrix"].get("validation_issue_summary", ""),
            }
            ok = report["status"] == "passed"
            human_lines = [
                f"Product journey dogfood smoke: {report['dogfood_id']}",
                f"Status: {report['status']}",
                f"Artifacts: {report['dogfood_dir']}",
                f"Summary: {report['artifacts']['summary']}",
                f"Smoke deck: {report['artifacts']['deck']}",
                f"Matrix: {report['matrix']['matrix_dir']}",
                f"Run: {report['run']['run_dir']}",
                f"Run deck: {report['run']['deck_path']}",
                f"Rollup deck: {report['rollup']['deck_path']}",
                f"Corpus validation: {report['corpus_validation']['status']} ({report['corpus_validation']['warnings']} warnings)",
                f"Review: {report['review']['summary']}",
                f"Run validation: {report['validation']['run']['status']} ({report['validation']['run']['warnings']} warnings)",
                f"Matrix validation: {report['validation']['matrix']['status']} ({report['validation']['matrix']['warnings']} warnings)",
            ]
            return payload, ok, human_lines, f"Ran product journey dogfood smoke {report['dogfood_id']}: {report['status']}"

        if name == "driver-replay":
            report = build_driver_replay_smoke(catalog, github_targets, personas, scenarios, args.seed, args.smoke_scenario, args.smoke_persona)
            reviewed = report["review"]
            validation = report["validation"]
            payload = {
                "status": report["status"],
                "readiness_status": report["readiness_status"],
                "smoke_id": report["smoke_id"],
                "smoke_dir": report["smoke_dir"],
                "report_path": report["artifacts"]["report"],
                "summary_path": report["artifacts"]["summary"],
                "deck_path": report["artifacts"]["deck"],
                "run_dir": report["run"]["run_dir"],
                "run_deck_path": report["run"]["deck_path"],
                "driver_journal_path": report["run"]["driver_journal_path"],
                "driver_handoff_path": report["run"]["driver_handoff_path"],
                "media_manifest_path": report["run"]["media_manifest_path"],
                "scenario": report["scenario"]["id"],
                "persona": report["persona"]["id"],
                "persona_label": report["persona"]["label"],
                "attached_evidence_count": len(report["attached_evidence"]),
                "review_status": reviewed.get("review_status"),
                "review_summary": reviewed.get("summary"),
                "review_passed": reviewed.get("review_passed_count", reviewed.get("passed", 0)),
                "review_warnings": reviewed.get("warnings", 0),
                "review_failed": reviewed.get("review_failed_count", reviewed.get("failed", 0)),
                "review_total": reviewed.get("review_total_count", reviewed.get("total", 0)),
                "review_ready": 1 if report["readiness_status"] == "ready" else 0,
                "review_needs_evidence": 0 if report["readiness_status"] == "ready" else 1,
                "review_backlog_summary": report.get("review_backlog_summary", ""),
                "validation_status": validation.get("status"),
                "validation_warnings": validation.get("warnings"),
                "validation_issue_summary": validation.get("validation_issue_summary", ""),
            }
            ok = report["status"] == "passed"
            human_lines = [
                f"Product journey driver replay smoke: {report['smoke_id']}",
                f"Status: {report['status']}",
                f"Readiness: {report['readiness_status']}",
                f"Persona: {report['persona']['label']}",
                f"Artifacts: {report['smoke_dir']}",
                f"Summary: {report['artifacts']['summary']}",
                f"Smoke deck: {report['artifacts']['deck']}",
                f"Run: {report['run']['run_dir']}",
                f"Run deck: {report['run']['deck_path']}",
                f"Review: {report['review']['summary']}",
                f"Validation: {report['validation']['status']} ({report['validation']['warnings']} warnings)",
            ]
            return payload, ok, human_lines, f"Ran product journey driver replay smoke {report['smoke_id']}: {report['status']}"

        raise SystemExit(f"Unknown --gate name: {name} (choices: {', '.join(sorted(_GATE_OLD_FLAGS))}, all)")

    _requested_gates: list[str] = []
    _legacy_gate_flags = [
        ("driver_smoke", "driver-manifest", True),
        ("dogfood_smoke", "dogfood", False),
        ("driver_replay_smoke", "driver-replay", False),
        ("native_ghagent_smoke", "ghagent", False),
        ("autonomous_fix_smoke", "autonomous-fix", False),
        ("persona_autofix_smoke", "persona-autofix", False),
        ("autonomous_marathon_smoke", "autonomous-marathon", False),
        ("validate_marathon_smoke_ledger", "marathon-ledger", False),
    ]
    for attr, gate_name, is_value_style in _legacy_gate_flags:
        value = getattr(args, attr)
        if not value:
            continue
        if is_value_style and not args.driver:
            args.driver = value
        print(f"[deprecated] {_GATE_OLD_FLAGS[gate_name]} is deprecated; use --gate {gate_name} instead.", file=sys.stderr)
        if gate_name not in _requested_gates:
            _requested_gates.append(gate_name)
    if args.gate:
        for token in args.gate.split(","):
            token = token.strip()
            if not token:
                continue
            if token == "all":
                for gate_name in _GATE_ALL_ORDER:
                    if gate_name not in _requested_gates:
                        _requested_gates.append(gate_name)
                continue
            if token not in _GATE_OLD_FLAGS:
                raise SystemExit(f"Unknown --gate name: {token} (choices: {', '.join(sorted(_GATE_OLD_FLAGS))}, all)")
            if token not in _requested_gates:
                _requested_gates.append(token)

    if _requested_gates:
        gate_results = []
        overall_ok = True
        for gate_name in _requested_gates:
            payload, ok, human_lines, log_line = _run_gate(gate_name)
            envelope = _gate_envelope(gate_name, payload, ok)
            overall_ok = overall_ok and ok
            if args.json_output:
                gate_results.append(envelope)
            else:
                print(f"Gate: {gate_name} ({envelope['gate_status']})")
                for line in human_lines:
                    print(line)
            append_log(log_line)
        if args.json_output:
            if len(gate_results) == 1:
                print(json.dumps(gate_results[0], sort_keys=True))
            else:
                print(json.dumps({
                    "status": "pass" if overall_ok else "fail",
                    "gates": gate_results,
                }, sort_keys=True))
        if not overall_ok:
            raise SystemExit(1)
        return

    if args.driver_replay_sweep:
        report = build_driver_replay_sweep(catalog, github_targets, personas, scenarios, args.seed, args.smoke_persona)
        if args.json_output:
            print(json.dumps({
                "status": report["status"],
                "readiness_status": report["readiness_status"],
                "sweep_id": report["sweep_id"],
                "persona": report["persona"]["id"],
                "persona_label": report["persona"]["label"],
                "sweep_dir": report["sweep_dir"],
                "report_path": report["artifacts"]["report"],
                "summary_path": report["artifacts"]["summary"],
                "deck_path": report["artifacts"]["deck"],
                "scenario_count": report["summary"]["scenarios"],
                "passed": report["summary"]["passed"],
                "failed": report["summary"]["failed"],
                "review_ready": report["summary"]["review_ready"],
                "review_needs_evidence": report["summary"]["review_needs_evidence"],
                "playback_scenarios": report["summary"]["playback_scenarios"],
                "validation_errors": report["summary"]["validation_errors"],
                "validation_warnings": report["summary"]["validation_warnings"],
                "attached_evidence_count": report["summary"]["attached_evidence_count"],
                "failed_scenarios": report["failed_scenarios"],
                "failed_scenario_summary": ", ".join(report["failed_scenarios"]),
            }, sort_keys=True))
            append_log(f"Ran product journey driver replay sweep {report['sweep_id']}: {report['status']}")
            if report["status"] != "passed":
                raise SystemExit(1)
            return
        print(f"Product journey driver replay sweep: {report['sweep_id']}")
        print(f"Status: {report['status']}")
        print(f"Readiness: {report['readiness_status']}")
        print(f"Scenarios: {report['summary']['passed']} / {report['summary']['scenarios']} passed")
        print(f"Reviews ready: {report['summary']['review_ready']} / {report['summary']['scenarios']}")
        print(f"Playback: {report['summary']['playback_scenarios']} / {report['summary']['scenarios']}")
        print(f"Artifacts: {report['sweep_dir']}")
        print(f"Summary: {report['artifacts']['summary']}")
        print(f"Deck: {report['artifacts']['deck']}")
        append_log(f"Ran product journey driver replay sweep {report['sweep_id']}: {report['status']}")
        if report["status"] != "passed":
            raise SystemExit(1)
        return

    if args.refresh_github_targets:
        result = refresh_github_target_proofs(github_targets, args.seed)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Refreshed GitHub target proof {result['proof_id']}: passed={result['passed']} failed={result['failed']} errors={result['errors']}")
            if result["failed"] or result["errors"]:
                raise SystemExit(1)
            return
        print(f"Product journey GitHub target proof: {result['proof_id']}")
        print(f"Artifacts: {result['proof_dir']}")
        print(f"Proof: {result['proof_path']}")
        print(f"Passed: {result['passed']} / {result['target_count']}")
        print(f"Failed: {result['failed']}")
        print(f"Errors: {result['errors']}")
        append_log(f"Refreshed GitHub target proof {result['proof_id']}: passed={result['passed']} failed={result['failed']} errors={result['errors']}")
        if result["failed"] or result["errors"]:
            raise SystemExit(1)
        return

    if args.validate_run:
        if not args.run_dir:
            raise SystemExit("--validate-run requires --run-dir")
        run_dir = run_dir_from_arg(args.run_dir)
        result = validate_run_bundle(run_dir)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Validated run bundle {run_dir.name}: {result['status']}")
            if result["status"] != "valid":
                raise SystemExit(1)
            return
        print(f"Validation status: {result['status']}")
        print(f"Artifacts: {run_dir}")
        print(f"Errors: {result['errors']}")
        print(f"Warnings: {result['warnings']}")
        for issue in result["issues"]:
            detail = f" ({issue['detail']})" if issue.get("detail") else ""
            print(f"- {issue['severity']}: {issue['id']}: {issue['message']}{detail}")
        append_log(f"Validated run bundle {run_dir.name}: {result['status']}")
        if result["status"] != "valid":
            raise SystemExit(1)
        return

    if args.validate_matrix:
        if not args.matrix_dir:
            raise SystemExit("--validate-matrix requires --matrix-dir")
        matrix_dir = run_dir_from_arg(args.matrix_dir)
        result = validate_matrix_bundle(matrix_dir, args.strict_target_proof)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Validated matrix bundle {matrix_dir.name}: {result['status']}")
            if result["status"] != "valid":
                raise SystemExit(1)
            return
        print(f"Validation status: {result['status']}")
        print(f"Artifacts: {matrix_dir}")
        print(f"Errors: {result['errors']}")
        print(f"Warnings: {result['warnings']}")
        for issue in result["issues"]:
            detail = f" ({issue['detail']})" if issue.get("detail") else ""
            print(f"- {issue['severity']}: {issue['id']}: {issue['message']}{detail}")
        append_log(f"Validated matrix bundle {matrix_dir.name}: {result['status']}")
        if result["status"] != "valid":
            raise SystemExit(1)
        return

    if args.rollup_matrix:
        if not args.matrix_dir:
            raise SystemExit("--rollup-matrix requires --matrix-dir")
        matrix_dir = run_dir_from_arg(args.matrix_dir)
        rollup = write_matrix_rollup(matrix_dir, args.rollup_run_dir)
        if args.json_output:
            print(json.dumps(rollup, sort_keys=True))
            append_log(f"Emitted matrix rollup {rollup['matrix_id']}")
            return
        print(f"Product journey matrix rollup: {rollup['matrix_id']}")
        print(f"Artifacts: {matrix_dir}")
        print(f"Rollup: {rollup['rollup_path']}")
        print(f"Deck: {rollup['deck_path']}")
        print(f"Runs: {rollup['runs_found']} / {rollup['assignments']}")
        print(f"Evidence: {rollup['present_evidence_count']} / {rollup['required_evidence_count']}")
        append_log(f"Emitted matrix rollup {rollup['matrix_id']}")
        return

    if args.summarize_run:
        if not args.run_dir:
            raise SystemExit("--summarize-run requires --run-dir")
        run_dir = run_dir_from_arg(args.run_dir)
        result = summarize_run_bundle(run_dir)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Loaded run bundle {run_dir.name}")
            return
        print(f"Product journey run: {result['run_id']}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {result['deck_path']}")
        print(f"Driver handoff: {result['driver_handoff_path']}")
        print(f"Missing proof: {result['missing_proof_evidence_count']}")
        append_log(f"Loaded run bundle {run_dir.name}")
        return

    if args.emit_matrix:
        github_targets_for_matrix = merge_target_proofs(github_targets, load_target_proof(args.target_proof_file))
        driver_manifest = load_driver_manifest(args.driver)
        matrix_dir, matrix = build_matrix_bundle(github_targets_for_matrix, personas, scenarios, args.seed, args.matrix_personas, driver_manifest)
        target_proof = matrix.get("target_proof", {})
        target_proof_summary = target_proof.get("summary", {})
        target_proof_ready = bool(target_proof) and target_proof_summary.get("failed", 0) == 0 and target_proof_summary.get("errors", 0) == 0
        if args.json_output:
            print(json.dumps({
                "status": "matrix_created",
                "matrix_id": matrix["matrix_id"],
                "matrix_dir": str(matrix_dir),
                "deck_path": str(matrix_dir / "deck.slidey.json"),
                "target_proof": target_proof,
                "target_proof_id": target_proof.get("proof_id", ""),
                "target_proof_checked_at": target_proof.get("created_at", ""),
                "target_proof_passed": target_proof_summary.get("passed", 0),
                "target_proof_failed": target_proof_summary.get("failed", 0),
                "target_proof_errors": target_proof_summary.get("errors", 0),
                "target_proof_ready": "yes" if target_proof_ready else "no",
                "target_count": matrix["target_count"],
                "assignment_count": matrix["assignment_count"],
                "scenario_count": matrix["scenario_count"],
                "persona_mode": matrix["persona_mode"],
            }, sort_keys=True))
            append_log(f"Emitted GitHub matrix {matrix['matrix_id']}")
            return
        print(f"Product journey GitHub matrix: {matrix['matrix_id']}")
        print(f"Artifacts: {matrix_dir}")
        print(f"Deck: {matrix_dir / 'deck.slidey.json'}")
        print(f"Targets: {matrix['target_count']}")
        print(f"Assignments: {matrix['assignment_count']}")
        append_log(f"Emitted GitHub matrix {matrix['matrix_id']}")
        return

    if args.scenario_qa_report:
        if not args.run_dir:
            raise SystemExit("--scenario-qa-report requires --run-dir")
        run_dir = run_dir_from_arg(args.run_dir)
        name = args.scenario or args.scenario_description or "(unnamed scenario)"
        leg_results = parse_scenario_qa_leg_results(args.leg_results_json)
        items = scenario_qa_leg_items(leg_results)
        counts = scenario_qa_leg_counts(items)
        summary = scenario_qa_report_summary(name, counts)

        # Deck generation (deck.slidey.json + review.json) is best-effort and
        # must never take report.md down with it. Previously a deck-shape
        # validation failure raised SystemExit BEFORE report.md was ever
        # written (the write sat below the validation check), so a bad deck
        # silently left report.md missing/stale with no cause recorded
        # anywhere -- the "quiet partial failure" persona-qa productization
        # brief issue group F / P1.6. deck_error carries the cause into both
        # the JSON envelope (deck_error key, read by stories/scenario-qa's
        # report room) and report.md itself, and report.md is now written
        # UNCONDITIONALLY, after this best-effort block, regardless of
        # whether it succeeded.
        deck_error = ""
        deck_path: Optional[Path] = None
        review_path: Optional[Path] = None
        review_status = ""
        try:
            deck = render_scenario_qa_deck(name, run_dir.name, items, counts)
            deck_issues: list[dict] = []
            validate_slidey_deck_shape(deck, {"items": []}, deck_issues)
            if deck_issues:
                raise ValueError(f"scenario-qa deck validation failed: {validation_issue_summary(deck_issues)}")
            deck_path = run_dir / "deck.slidey.json"
            write_json(deck_path, deck)
            review = render_scenario_qa_review(name, run_dir.name, items, counts)
            review_path = run_dir / "review.json"
            write_json(review_path, review)
            review_status = review["status"]
        except Exception as exc:  # noqa: BLE001 -- best-effort; see comment above
            deck_error = str(exc)
            deck_path = None
            review_path = None

        report_markdown = render_scenario_qa_markdown(name, run_dir.name, items, counts)
        if deck_error:
            report_markdown += f"\nDeck generation failed: {deck_error}\n"
        report_path = run_dir / "report.md"
        report_path.write_text(report_markdown, encoding="utf-8")

        status = "scenario_qa_deck_built" if not deck_error else "scenario_qa_report_built_deck_failed"
        log_summary = summary if not deck_error else f"{summary} (deck generation failed: {deck_error})"
        if args.json_output:
            print(json.dumps({
                "status": status,
                "run_dir": str(run_dir),
                "report_path": str(report_path),
                "deck_path": str(deck_path) if deck_path else "",
                "review_path": str(review_path) if review_path else "",
                "review_status": review_status,
                "deck_error": deck_error,
                "leg_count": counts["total"],
                "pass_count": counts["pass"],
                "fail_count": counts["fail"],
                "degraded_count": counts["degraded"],
                "report_summary": summary,
                "summary": summary,
            }, sort_keys=True))
            append_log(f"Built scenario-qa deck for {run_dir.name}: {log_summary}")
            return
        print(f"Deck: {deck_path}" if deck_path else "Deck: (failed — see report.md)")
        print(summary)
        if deck_error:
            print(f"Deck generation failed: {deck_error}")
        append_log(f"Built scenario-qa deck for {run_dir.name}: {log_summary}")
        return

    if args.autonomous_marathon_watchdog:
        if not args.run_dir:
            raise SystemExit("--autonomous-marathon-watchdog requires --run-dir")
        run_dir = run_dir_from_arg(args.run_dir)
        result = autonomous_marathon_watchdog(run_dir, args.watchdog_now)
        result.update(run_story_summary(run_dir))
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Checked autonomous marathon watchdog for {run_dir.name}: {result['status']}")
            if result["status"] != "autonomous_watchdog_ok" and not args.report_blocked_autonomous_watchdog:
                raise SystemExit(1)
            return
        print(f"Status: {result['status']}")
        print(result["autonomous_watchdog_summary"])
        print(f"Report: {result['autonomous_watchdog_markdown_path']}")
        append_log(f"Checked autonomous marathon watchdog for {run_dir.name}: {result['status']}")
        if result["status"] != "autonomous_watchdog_ok" and not args.report_blocked_autonomous_watchdog:
            raise SystemExit(1)
        return

    if args.autonomous_marathon_due:
        root = run_dir_from_arg(args.stats_root) if args.stats_root else ARTIFACT_ROOT
        result = autonomous_marathon_due(root, args.due_now, args.due_limit)
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Checked autonomous marathon due runs under {root}: {result['summary']}")
            return
        print(f"Autonomous marathon due: {result['summary']}")
        print(f"Root: {result['root']}")
        if result["next_due_command"]:
            print(f"Next command: {result['next_due_command']}")
            print(f"Next story intent: {result['next_due_story_intent']}")
        for item in result["blocked_runs"]:
            print(f"- blocked {item.get('run_id', '')}: {item.get('blocked_reason', '')}")
        append_log(f"Checked autonomous marathon due runs under {root}: {result['summary']}")
        return

    if args.autonomous_marathon_advance_due:
        root = run_dir_from_arg(args.stats_root) if args.stats_root else ARTIFACT_ROOT
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        result = autonomous_marathon_advance_due(
            catalog,
            github_targets,
            personas,
            scenarios,
            root,
            args.due_now,
            args.due_limit,
            args.gh_agent_db,
            args.gh_agent_story,
            args.gh_agent_project_root,
            args.gh_agent_incident_repo,
            args.gh_agent_asset_dir,
            args.gh_agent_comment_mode,
            args.issue_state_file,
            args.stats_root,
            args.stats_output,
            args.similarity_threshold,
            args.similar_pair_limit,
            publish_deck,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Advanced autonomous marathon due cycle under {root}: {result.get('autonomous_due_advance_status', result['status'])}")
            if result["status"] in {"autonomous_marathon_invalid", "autonomous_marathon_advance_blocked"} and not args.report_invalid_autonomous_marathon:
                raise SystemExit(1)
            return
        print(f"Status: {result['status']}")
        print(result.get("autonomous_due_advance_summary", result.get("autonomous_marathon_summary", "")))
        if result.get("run_dir"):
            print(f"Artifacts: {result['run_dir']}")
        if result.get("autonomous_marathon_report_path"):
            print(f"Report: {result['autonomous_marathon_report_path']}")
        append_log(f"Advanced autonomous marathon due cycle under {root}: {result.get('autonomous_due_advance_status', result['status'])}")
        if result["status"] in {"autonomous_marathon_invalid", "autonomous_marathon_advance_blocked"} and not args.report_invalid_autonomous_marathon:
            raise SystemExit(1)
        return

    if args.autonomous_marathon:
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir) if args.run_dir else None
        result = autonomous_marathon(
            catalog,
            github_targets,
            personas,
            scenarios,
            run_dir,
            args.project,
            args.persona,
            args.seed,
            args.scenarios,
            args.live_budget_minutes,
            args.ticket_repo,
            args.gh_agent_db,
            args.gh_agent_story,
            args.gh_agent_public_base_url,
            args.gh_agent_project_root,
            args.gh_agent_incident_repo,
            args.gh_agent_asset_dir,
            args.gh_agent_comment_mode,
            args.issue_state_file,
            args.stats_root,
            args.stats_output,
            args.similarity_threshold,
            args.similar_pair_limit,
            args.autonomous_driver_mode,
            args.autonomous_cadence_hours,
            args.autonomous_heartbeat_minutes,
            args.autonomous_watchdog_minutes,
            publish_deck,
            autonomous_driver_live_profile=args.autonomous_driver_live_profile,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Ran autonomous marathon for {Path(result['run_dir']).name}: {result['status']}")
            if result["status"] == "autonomous_marathon_invalid" and not args.report_invalid_autonomous_marathon:
                raise SystemExit(1)
            return
        print(f"Status: {result['status']}")
        print(result["autonomous_marathon_summary"])
        print(f"Artifacts: {result['run_dir']}")
        print(f"Report: {result['autonomous_marathon_report_path']}")
        if result.get("autonomous_fix_report_path"):
            print(f"Autonomous fix: {result['autonomous_fix_report_path']}")
        if result.get("stats_output"):
            print(f"Stats: {result['stats_output']}")
        append_log(f"Ran autonomous marathon for {Path(result['run_dir']).name}: {result['status']}")
        if result["status"] == "autonomous_marathon_invalid" and not args.report_invalid_autonomous_marathon:
            raise SystemExit(1)
        return

    if args.file_findings:
        if not args.run_dir:
            raise SystemExit("--file-findings requires --run-dir")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        dry_run = args.dry_run or args.filing_mode == "dry-run"
        if not dry_run and not args.debug_file:
            raise SystemExit(
                "Real product-journey filing is routed through autonomous-fix; "
                "--file-findings --filing-mode file requires --debug-file for isolated diagnostics"
            )
        result = file_findings(
            run_dir,
            args.ticket_repo,
            dry_run,
            publish_deck,
            args.gh_agent_db,
            args.gh_agent_story,
            args.gh_agent_drain,
            args.gh_agent_public_base_url,
            args.gh_agent_project_root,
            args.gh_agent_incident_repo,
            args.gh_agent_asset_dir,
            args.gh_agent_comment_mode,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Filed findings for {run_dir.name}: {result['filing_summary']}")
            return
        print(f"Status: {result['status']}")
        print(result["filing_summary"])
        for outcome in result["outcomes"]:
            line = f"- {outcome.get('finding_id', '?')} [{outcome.get('status', '?')}]"
            if outcome.get("issue_url"):
                line += f" {outcome['issue_url']}"
            if outcome.get("error"):
                line += f" ({outcome['error']})"
            print(line)
        print(f"Unfiled credible findings: {result['findings_unfiled_count']}")
        if result.get("gh_agent_enqueue_status") != "disabled":
            print(
                "GH-agent fixes: "
                f"{result['gh_agent_enqueue_status']} "
                f"({result['gh_agent_enqueued_count']} queued, "
                f"{result['gh_agent_skipped_count']} skipped)"
            )
            print(
                "GH-agent drain: "
                f"{result['gh_agent_drain_status']} "
                f"({result['gh_agent_done_count']} done, "
                f"{result['gh_agent_failed_count']} failed, "
                f"{result['gh_agent_active_count']} active)"
            )
        append_log(f"Filed findings for {run_dir.name}: {result['filing_summary']}")
        return

    if args.file_local_findings:
        if not args.run_dir:
            raise SystemExit("--file-local-findings requires --run-dir")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        result = file_local_findings(
            run_dir,
            args.dry_run,
            publish_deck,
            args.local_sink_target,
            args.local_sink_target_dir,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Filed local findings for {run_dir.name}: {result['filing_summary']}")
            return
        print(f"Status: {result['status']}")
        print(result["filing_summary"])
        for outcome in result["outcomes"]:
            line = f"- {outcome.get('finding_id', '?')} [{outcome.get('status', '?')}]"
            if outcome.get("local_ticket_path"):
                line += f" {outcome['local_ticket_path']}"
            if outcome.get("error"):
                line += f" ({outcome['error']})"
            print(line)
        print(f"Unfiled credible findings: {result['findings_unfiled_count']}")
        append_log(f"Filed local findings for {run_dir.name}: {result['filing_summary']}")
        return

    if args.autonomous_fix_loop:
        if not legacy_autonomous_fix_loop_cli_allowed():
            raise SystemExit(
                "--autonomous-fix-loop is an internal test backend; use "
                "kitsoki gitops autonomous-fix or the product-journey story autonomous_fix intent"
            )
        if not args.run_dir:
            raise SystemExit("--autonomous-fix-loop requires --run-dir")
        if not args.gh_agent_db:
            raise SystemExit("--autonomous-fix-loop requires --gh-agent-db")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        result = autonomous_fix_loop(
            run_dir,
            args.ticket_repo,
            args.gh_agent_db,
            args.gh_agent_story,
            args.gh_agent_public_base_url,
            args.gh_agent_project_root,
            args.gh_agent_incident_repo,
            args.gh_agent_asset_dir,
            args.gh_agent_comment_mode,
            publish_deck,
        )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Ran autonomous fix loop for {run_dir.name}: {result['status']}")
            if result["status"] != "autonomous_fix_valid" and not args.report_invalid_autonomous_fix:
                raise SystemExit(1)
            return
        print(f"Status: {result['status']}")
        print(result["filing_summary"])
        print(
            "GH-agent drain: "
            f"{result['gh_agent_drain_status']} "
            f"({result['gh_agent_done_count']} done, "
            f"{result['gh_agent_failed_count']} failed, "
            f"{result['gh_agent_active_count']} active)"
        )
        print(f"Review: {result['review_summary']}")
        print(f"Validation: {result['validation_status']} ({result['validation_errors']} errors, {result['validation_warnings']} warnings)")
        append_log(f"Ran autonomous fix loop for {run_dir.name}: {result['status']}")
        if result["status"] != "autonomous_fix_valid" and not args.report_invalid_autonomous_fix:
            raise SystemExit(1)
        return

    if args.stats:
        stats_root = run_dir_from_arg(args.stats_root) if args.stats_root else ARTIFACT_ROOT
        issue_state_file = args.issue_state_file
        issue_state_refresh = None
        if truthy(args.refresh_issue_state):
            issue_state_refresh = refresh_issue_state_cache(stats_root, issue_state_file, args.ticket_repo)
            issue_state_file = str(issue_state_refresh.get("output", issue_state_file))
        result = derive_stats(stats_root, issue_state_file, args.similarity_threshold, args.similar_pair_limit, args.stats_output)
        if issue_state_refresh:
            result["issue_state_status"] = issue_state_refresh.get("status", "")
            result["issue_state_output"] = issue_state_refresh.get("output", "")
            result["issue_state_count"] = issue_state_refresh.get("issues_count", 0)
            result["issue_state_summary"] = (
                f"Refreshed {issue_state_refresh.get('issues_count', 0)} issue state record(s) "
                "through kitsoki gitops issue-state-cache."
            )
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Derived product journey stats: {result['stats_summary']}")
            return
        print(result["stats_summary"])
        print(f"Runs scanned: {result['runs_scanned']}")
        print(f"Unknown issue states: {result['issues_unknown_state_count']}")
        if result["stats_output"]:
            print(f"Stats: {result['stats_output']}")
        append_log(f"Derived product journey stats: {result['stats_summary']}")
        return

    if args.review_run:
        if not args.run_dir:
            raise SystemExit("--review-run requires --run-dir")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        reviewed = review_run_bundle(run_dir, publish_deck)
        if args.json_output:
            print(json.dumps(reviewed, sort_keys=True))
            append_log(f"Reviewed run bundle {run_dir.name}: {reviewed['review_status']}")
            return
        print(f"Review status: {reviewed['review_status']}")
        print(reviewed["summary"])
        print(f"Review: {reviewed['review_path']}")
        print(f"Deck: {reviewed['deck_path']}")
        print(f"Execution plan: {reviewed['execution_plan_path']}")
        print(f"Driver plan: {reviewed['driver_plan_path']}")
        print(f"Agent brief: {reviewed['agent_brief_path']}")
        print(f"Driver handoff: {reviewed['driver_handoff_path']}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Reviewed run bundle {run_dir.name}: {reviewed['review_status']}")
        return

    if args.seed_demo_evidence:
        if not args.run_dir:
            raise SystemExit("--seed-demo-evidence requires --run-dir")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        seeded = seed_demo_evidence(run_dir, publish_deck)
        if args.json_output:
            print(json.dumps(seeded, sort_keys=True))
            append_log(f"Seeded demo evidence for {run_dir.name}")
            return
        print("Seeded demo evidence")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
        print(f"Driver plan: {run_dir / 'driver-plan.md'}")
        print(f"Agent brief: {run_dir / 'agent-brief.md'}")
        print(f"Evidence present: {seeded['present_evidence_count']}")
        print(f"Findings: {seeded['findings_count']}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Seeded demo evidence for {run_dir.name}")
        return

    if args.driver_handoff:
        if not args.run_dir:
            raise SystemExit("--driver-handoff requires --run-dir")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        handoff = prepare_driver_handoff(run_dir, publish_deck)
        if args.json_output:
            print(json.dumps(handoff, sort_keys=True))
            append_log(f"Prepared driver handoff for {run_dir.name}")
            return
        print("Product journey driver handoff ready")
        print(f"Run: {run_dir}")
        print(f"Driver agent: {handoff['driver_agent']}")
        print(f"Handoff: {handoff['driver_handoff_path']}")
        print(f"Driver plan: {handoff['driver_plan_path']}")
        print(f"Agent brief: {handoff['agent_brief_path']}")
        print(f"Missing evidence: {handoff['missing_evidence_count']}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Prepared driver handoff for {run_dir.name}")
        return

    if args.record_driver_event:
        missing = []
        for flag, value in {
            "--run-dir": args.run_dir,
            "--scenario": args.scenario,
            "--summary": args.summary,
        }.items():
            if not value:
                missing.append(flag)
        if missing:
            raise SystemExit(f"--record-driver-event requires {', '.join(missing)}")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        event = record_driver_event(
            run_dir,
            args.scenario,
            args.dispatch_mode,
            args.driver_status,
            args.summary,
            args.mcp_tools,
            args.evidence_refs,
            args.blockers,
            publish_deck,
        )
        if args.json_output:
            result = {
                "status": "driver_event_recorded",
                "run_dir": str(run_dir),
                "event_id": event["id"],
                "scenario": event["scenario"],
                "dispatch_mode": event["dispatch_mode"],
                "driver_status": event["status"],
                "driver_journal_path": str(run_dir / "driver-journal.md"),
                "driver_journal_json_path": str(run_dir / "driver-journal.json"),
                "deck_path": str(run_dir / "deck.slidey.json"),
                "driver_handoff_path": str(run_dir / "driver-handoff.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }
            result.update(run_story_summary(run_dir))
            print(json.dumps(result, sort_keys=True))
            append_log(f"Recorded driver event for {run_dir.name}: {event['scenario']} / {event['status']}")
            return
        print(f"Recorded driver event: {event['id']}")
        print(f"Scenario: {event['scenario']}")
        print(f"Driver journal: {run_dir / 'driver-journal.md'}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Recorded driver event for {run_dir.name}: {event['scenario']} / {event['status']}")
        return

    if args.record_autonomous_driver_dispatch:
        missing = []
        for flag, value in {
            "--run-dir": args.run_dir,
            "--summary": args.summary,
        }.items():
            if not value:
                missing.append(flag)
        if missing:
            raise SystemExit(f"--record-autonomous-driver-dispatch requires {', '.join(missing)}")
        run_dir = run_dir_from_arg(args.run_dir)
        receipt = record_autonomous_driver_dispatch(
            run_dir,
            args.dispatch_mode,
            args.dispatch_status,
            args.summary,
            args.autonomous_driver_evidence_count,
            args.autonomous_driver_issue_count,
            args.driver_trace,
            args.blockers,
        )
        result = {
            "status": "autonomous_driver_dispatch_recorded",
            "run_dir": str(run_dir),
            "autonomous_driver_dispatch_path": str(autonomous_driver_dispatch_path(run_dir)),
            "autonomous_driver_dispatch_markdown_path": str(autonomous_driver_dispatch_markdown_path(run_dir)),
            "autonomous_driver_dispatch_status": receipt["status"],
            "autonomous_driver_dispatch_summary": receipt["summary"],
            "autonomous_driver_dispatch_trace": receipt["trace"],
        }
        result.update(run_story_summary(run_dir))
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Recorded autonomous driver dispatch for {run_dir.name}: {receipt['status']}")
            return
        print(f"Autonomous driver dispatch: {receipt['status']}")
        print(f"Receipt: {result['autonomous_driver_dispatch_markdown_path']}")
        append_log(f"Recorded autonomous driver dispatch for {run_dir.name}: {receipt['status']}")
        return

    if args.campaign_worker:
        if not args.run_dir:
            raise SystemExit("--campaign-worker requires --run-dir")
        run_dir = run_dir_from_arg(args.run_dir)
        receipt = record_campaign_worker_receipt(
            run_dir,
            args.worker_backend,
            args.worker_id,
            args.worker_status,
            args.worker_ready_status,
            args.worker_ready_summary,
            args.worker_budget_minutes,
            args.worker_receipt_source,
            ",".join(args.worker_import_artifact),
            args.summary,
        )
        result = {
            "status": "campaign_worker_recorded",
            "run_dir": str(run_dir),
            "campaign_worker_backend": receipt["backend"],
            "campaign_worker_id": receipt["worker_id"],
            "campaign_worker_status": receipt["status"],
            "campaign_worker_ready_status": receipt["ready_status"],
            "campaign_worker_summary": campaign_worker_summary(receipt),
            "campaign_worker_receipt_path": str(campaign_worker_receipt_path(run_dir)),
            "campaign_worker_receipt_markdown_path": str(campaign_worker_receipt_markdown_path(run_dir)),
            "campaign_worker_imported_artifact_count": len(receipt["imported_artifacts"]),
            "campaign_worker_artifact_import_status": receipt["artifact_import_status"],
            "deck_path": str(run_dir / "deck.slidey.json"),
            "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        }
        result.update(run_story_summary(run_dir))
        if args.json_output:
            print(json.dumps(result, sort_keys=True))
            append_log(f"Recorded campaign worker receipt for {run_dir.name}: {receipt['status']}")
            return
        print(f"Campaign worker: {receipt['status']}")
        print(f"Receipt: {result['campaign_worker_receipt_markdown_path']}")
        append_log(f"Recorded campaign worker receipt for {run_dir.name}: {receipt['status']}")
        return

    if args.record_blocker:
        missing = []
        for flag, value in {
            "--run-dir": args.run_dir,
            "--scenario": args.scenario,
            "--title": args.title,
            "--summary": args.summary,
        }.items():
            if not value:
                missing.append(flag)
        if missing:
            raise SystemExit(f"--record-blocker requires {', '.join(missing)}")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        record_blocker(run_dir, args.scenario, args.title, args.summary, args.evidence_path, publish_deck)
        if args.json_output:
            result = {
                "status": "blocker_recorded",
                "run_dir": str(run_dir),
                "scenario": args.scenario,
                "title": args.title,
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "driver_plan_path": str(run_dir / "driver-plan.md"),
                "driver_journal_path": str(run_dir / "driver-journal.md"),
                "agent_brief_path": str(run_dir / "agent-brief.md"),
                "driver_handoff_path": str(run_dir / "driver-handoff.md"),
                "media_manifest_path": str(run_dir / "media-manifest.json"),
                "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }
            result.update(run_story_summary(run_dir))
            print(json.dumps(result, sort_keys=True))
            append_log(f"Recorded blocker for {run_dir.name}: {args.scenario} / {args.title}")
            return
        print(f"Recorded blocker: {args.scenario} / {args.title}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
        print(f"Driver plan: {run_dir / 'driver-plan.md'}")
        print(f"Agent brief: {run_dir / 'agent-brief.md'}")
        print(f"Driver handoff: {run_dir / 'driver-handoff.md'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Recorded blocker for {run_dir.name}: {args.scenario} / {args.title}")
        return

    if args.record_finding:
        missing = []
        for flag, value in {
            "--run-dir": args.run_dir,
            "--title": args.title,
            "--summary": args.summary,
        }.items():
            if not value:
                missing.append(flag)
        if missing:
            raise SystemExit(f"--record-finding requires {', '.join(missing)}")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        record_finding(
            run_dir,
            args.finding_kind,
            args.title,
            args.summary,
            args.scenario,
            args.severity,
            args.evidence_path,
            args.finding_status,
            publish_deck,
        )
        if args.json_output:
            result = {
                "status": "recorded",
                "run_dir": str(run_dir),
                "finding_kind": args.finding_kind,
                "title": args.title,
                "scenario": args.scenario,
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "driver_plan_path": str(run_dir / "driver-plan.md"),
                "driver_journal_path": str(run_dir / "driver-journal.md"),
                "agent_brief_path": str(run_dir / "agent-brief.md"),
                "driver_handoff_path": str(run_dir / "driver-handoff.md"),
                "media_manifest_path": str(run_dir / "media-manifest.json"),
                "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }
            result.update(run_story_summary(run_dir))
            print(json.dumps(result, sort_keys=True))
            append_log(f"Recorded {args.finding_kind} finding for {run_dir.name}: {args.title}")
            return
        print(f"Recorded finding: {args.finding_kind} / {args.title}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
        print(f"Driver plan: {run_dir / 'driver-plan.md'}")
        print(f"Agent brief: {run_dir / 'agent-brief.md'}")
        print(f"Driver handoff: {run_dir / 'driver-handoff.md'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Recorded {args.finding_kind} finding for {run_dir.name}: {args.title}")
        return

    if args.attach_evidence:
        missing = []
        for flag, value in {
            "--run-dir": args.run_dir,
            "--scenario": args.scenario,
            "--evidence-kind": args.evidence_kind,
            "--evidence-path": args.evidence_path,
        }.items():
            if not value:
                missing.append(flag)
        if missing:
            raise SystemExit(f"--attach-evidence requires {', '.join(missing)}")
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir = run_dir_from_arg(args.run_dir)
        attach_evidence(
            run_dir,
            args.scenario,
            args.evidence_kind,
            args.evidence_path,
            args.evidence_status,
            args.evidence_source,
            args.notes,
            publish_deck,
        )
        source = normalize_evidence_source(args.evidence_source, args.evidence_path, args.notes)
        if args.json_output:
            result = {
                "status": "attached",
                "run_dir": str(run_dir),
                "scenario": args.scenario,
                "evidence_kind": args.evidence_kind,
                "evidence_path": args.evidence_path,
                "evidence_source": source,
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "driver_plan_path": str(run_dir / "driver-plan.md"),
                "driver_journal_path": str(run_dir / "driver-journal.md"),
                "agent_brief_path": str(run_dir / "agent-brief.md"),
                "driver_handoff_path": str(run_dir / "driver-handoff.md"),
                "media_manifest_path": str(run_dir / "media-manifest.json"),
                "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }
            result.update(run_story_summary(run_dir))
            print(json.dumps(result, sort_keys=True))
            append_log(f"Attached evidence {args.scenario}/{args.evidence_kind} to {run_dir.name}")
            return
        print(f"Attached evidence: {args.scenario}/{args.evidence_kind}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
        print(f"Driver plan: {run_dir / 'driver-plan.md'}")
        print(f"Agent brief: {run_dir / 'agent-brief.md'}")
        print(f"Driver handoff: {run_dir / 'driver-handoff.md'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Attached evidence {args.scenario}/{args.evidence_kind} to {run_dir.name}")
        return

    if args.emit_run:
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_scenarios = select_scenarios(scenarios, args.scenarios)
        run_transports = select_transports(args.transport)
        driver_manifest = load_driver_manifest(args.driver)
        run_dir, run_json = build_run_bundle(
            catalog,
            github_targets,
            personas,
            run_scenarios,
            args.project,
            args.persona,
            args.seed,
            "dry-run",
            publish_deck,
            args.live_budget_minutes,
            run_transports,
            driver_manifest,
        )
        if args.json_output:
            driver_plan = read_json(run_dir / "driver-plan.json")
            result = {
                "status": "created",
                "run_id": run_json["run_id"],
                "run_dir": str(run_dir),
                "run_dir_rel": run_dir_cli_arg(run_json["run_id"]),
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "driver_plan_path": str(run_dir / "driver-plan.md"),
                "driver_plan": driver_plan,
                "driver_journal_path": str(run_dir / "driver-journal.md"),
                "agent_brief_path": str(run_dir / "agent-brief.md"),
                "driver_handoff_path": str(run_dir / "driver-handoff.md"),
                "media_manifest_path": str(run_dir / "media-manifest.json"),
                "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }
            if os.environ.get("KITSOKI_SCENARIO_QA_WORKSPACE_ACTIVE") == "1":
                result["scenario_qa_workspace"] = {
                    "id": scenario_qa_workspace_id(""),
                    "root": str(ROOT),
                    "branch": "",
                    "reused": True,
                }
            result.update(run_story_summary(run_dir))
            print(json.dumps(result, sort_keys=True))
            append_log(f"Emitted dry-run bundle {run_json['run_id']}")
            return
        print(f"Product journey run: {run_json['run_id']}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
        print(f"Driver plan: {run_dir / 'driver-plan.md'}")
        print(f"Agent brief: {run_dir / 'agent-brief.md'}")
        print(f"Driver handoff: {run_dir / 'driver-handoff.md'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Emitted dry-run bundle {run_json['run_id']}")
        return

    if args.mode == "status":
        print_status(catalog)
        append_log("Printed journey catalog and perspective status")
        return

    if args.mode == "report":
        if not args.generated_at:
            raise SystemExit("--generated-at is required for deterministic report generation")
        report_path, deck_path, markdown_path = report_paths(
            args.generated_at,
            args.report,
            args.deck,
            args.markdown,
        )
        write_report(
            catalog,
            args.generated_at,
            report_path,
            deck_path,
            markdown_path,
            args.run_checks,
        )
        return

    print_check(catalog, args.project)

    if args.run_log:
        append_log(f"Manual run flag set for project {args.project}")


if __name__ == "__main__":
    main()

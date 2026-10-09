#!/usr/bin/env python3
"""Extracted from run.py: marathon module (see tools/product-journey/README.md)."""

import difflib
import json
import os
import re
import shlex
import subprocess
import datetime
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Optional


from common import (
    DEFAULT_DRIVER_ID,
    PLAYBACK_EVIDENCE_KINDS,
    PROJECT_ROOT,
    SCENARIO_ALIASES,
    load_driver_manifest,
    read_json,
    run_dir_from_arg,
    write_json,
)
from emit import (
    scenario_quality_gate,
)


def select_scenarios(scenarios: list[dict], scenario_filter: str) -> list[dict]:
    requested = []
    for item in [item.strip() for item in scenario_filter.split(",") if item.strip()]:
        requested.extend(SCENARIO_ALIASES.get(item, [item]))
    if not requested:
        return scenarios

    by_id = {scenario["id"]: scenario for scenario in scenarios}
    duplicates = sorted({scenario_id for scenario_id in requested if requested.count(scenario_id) > 1})
    if duplicates:
        raise SystemExit(f"--scenarios contains duplicate scenario id(s): {', '.join(duplicates)}")

    unknown = [scenario_id for scenario_id in requested if scenario_id not in by_id]
    if unknown:
        known = ", ".join(sorted(by_id))
        raise SystemExit(f"--scenarios contains unknown active scenario id(s): {', '.join(unknown)}. Known active scenarios: {known}")

    return [by_id[scenario_id] for scenario_id in requested]


def parse_iso_datetime(value: str) -> datetime.datetime:
    try:
        parsed = datetime.datetime.fromisoformat(value)
    except ValueError as exc:
        raise SystemExit(f"Invalid ISO datetime: {value}") from exc
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=datetime.timezone.utc)
    return parsed.astimezone(datetime.timezone.utc)


def shell(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=False,
        env=os.environ.copy(),
        text=True,
        capture_output=True,
    )


def scenario_qa_workspace_id(value: str) -> str:
    raw = (value or os.environ.get("KITSOKI_SCENARIO_QA_WORKSPACE_ID") or "scenario-qa").strip()
    safe = re.sub(r"[^A-Za-z0-9._-]+", "-", raw).strip(".-")
    return safe or "scenario-qa"


def parse_final_json_object(text: str) -> dict:
    stripped = text.strip()
    if not stripped:
        raise json.JSONDecodeError("empty output", text, 0)
    try:
        parsed = json.loads(stripped)
    except json.JSONDecodeError:
        start = stripped.rfind("\n{")
        if start >= 0:
            parsed = json.loads(stripped[start + 1 :])
        else:
            start = stripped.rfind("{")
            if start < 0:
                raise
            parsed = json.loads(stripped[start:])
    if not isinstance(parsed, dict):
        raise json.JSONDecodeError("final JSON value is not an object", text, 0)
    return parsed


def strip_scenario_qa_workspace_args(argv: list[str]) -> list[str]:
    stripped: list[str] = []
    skip_next = False
    for arg in argv:
        if skip_next:
            skip_next = False
            continue
        if arg == "--scenario-qa-workspace":
            continue
        if arg == "--scenario-qa-workspace-id":
            skip_next = True
            continue
        if arg.startswith("--scenario-qa-workspace-id="):
            continue
        stripped.append(arg)
    return stripped


def marathon_smoke_ledger_path(value: str) -> Path:
    path = Path(value)
    if not path.is_absolute():
        path = PROJECT_ROOT / path
    return path


def scenario_playback_kind(scenario: dict) -> Optional[str]:
    """The single playback-capable evidence kind a scenario declares in its
    `evidence` list, or None if it declares zero or more than one."""
    declared = sorted(PLAYBACK_EVIDENCE_KINDS & set(scenario.get("evidence", [])))
    return declared[0] if len(declared) == 1 else None


def driver_manifest_for_run_json(run_json: dict) -> dict:
    driver = run_json.get("driver", {}) if isinstance(run_json, dict) else {}
    manifest_ref = driver.get("manifest_path") or driver.get("id") or DEFAULT_DRIVER_ID
    return load_driver_manifest(manifest_ref)


def build_driver_contract_summary(driver_plan: dict, handoff: dict) -> str:
    driver_scenarios = driver_plan.get("scenarios", [])
    final_gates = driver_plan.get("final_gates", [])
    missing_proof_evidence = handoff.get("missing_proof_evidence", [])
    has_transport_axis = any(scenario.get("transport") for scenario in driver_scenarios)
    unit_label = "transport checks" if has_transport_axis else "scenarios"
    seen = set()
    ordered_ids = []
    for scenario in driver_scenarios:
        scenario_id = scenario.get("scenario", "")
        if scenario_id and scenario_id not in seen:
            seen.add(scenario_id)
            ordered_ids.append(scenario_id)
    scenario_ids = ", ".join(
        ordered_ids[:5]
    )
    if len(ordered_ids) > 5:
        scenario_ids = f"{scenario_ids}, +{len(ordered_ids) - 5} more"
    return (
        f"Driver contract: {len(driver_scenarios)} {unit_label}"
        f"{f' ({scenario_ids})' if scenario_ids else ''}; "
        f"{len(missing_proof_evidence)} missing-proof rows; "
        f"{len(final_gates)} final gates. Inspect last_result.driver_scenarios, "
        "last_result.next_driver_capture_route, last_result.missing_proof_evidence, "
        "and last_result.driver_final_gates."
    )


def next_driver_capture_slot(handoff: dict) -> dict:
    for row in handoff.get("missing_proof_evidence", []):
        scenario = row.get("scenario", "")
        slots = row.get("slots", [])
        if not scenario or not slots:
            continue
        slot = slots[0]
        kind = slot.get("kind", "")
        if kind:
            return {"scenario": scenario, **slot}
    return {}


def next_driver_capture_route(handoff: dict) -> dict:
    slot = next_driver_capture_slot(handoff)
    route = slot.get("capture_route", {})
    return route if isinstance(route, dict) else {}


def next_driver_blocker_command(handoff: dict) -> str:
    slot = next_driver_capture_slot(handoff)
    scenario = slot.get("scenario", "")
    if not scenario:
        return ""
    for row in handoff.get("missing_proof_evidence", []):
        if row.get("scenario") == scenario:
            return row.get("record_blocker_command", "")
    return ""


def build_next_driver_capture(handoff: dict) -> str:
    slot = next_driver_capture_slot(handoff)
    if slot:
        scenario = slot.get("scenario", "")
        kind = slot.get("kind", "")
        hint = slot.get("capture_hint", "")
        route = slot.get("capture_route", {}) if isinstance(slot.get("capture_route", {}), dict) else {}
        route_id = route.get("route_id", "")
        route_suffix = f" Route: {route_id}." if route_id else ""
        if kind:
            return f"Next capture: {scenario}/{kind}.{route_suffix} {hint}".strip()
    return ""


def run_story_summary(run_dir: Path) -> dict:
    run_json = read_json(run_dir / "run.json") if (run_dir / "run.json").exists() else {}
    metrics = read_json(run_dir / "metrics.json") if (run_dir / "metrics.json").exists() else {}
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"summary": {}}
    handoff = read_json(run_dir / "driver-handoff.json") if (run_dir / "driver-handoff.json").exists() else {}
    driver_plan = read_json(run_dir / "driver-plan.json") if (run_dir / "driver-plan.json").exists() else {}
    agent_brief = read_json(run_dir / "agent-brief.json") if (run_dir / "agent-brief.json").exists() else {}
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {}
    weakness_routes = read_json(run_dir / "weakness-routes.json") if (run_dir / "weakness-routes.json").exists() else {"summary": {}, "items": []}
    prd_design_intake = read_json(run_dir / "prd-design-intake.json") if (run_dir / "prd-design-intake.json").exists() else {"summary": {}, "items": []}
    control = read_json(autonomous_marathon_control_path(run_dir)) if autonomous_marathon_control_path(run_dir).exists() else {}
    watchdog = read_json(autonomous_marathon_watchdog_path(run_dir)) if autonomous_marathon_watchdog_path(run_dir).exists() else {}
    driver_dispatch = read_json(autonomous_driver_dispatch_path(run_dir)) if autonomous_driver_dispatch_path(run_dir).exists() else {}
    campaign_worker = read_json(campaign_worker_receipt_path(run_dir)) if campaign_worker_receipt_path(run_dir).exists() else {}
    finding_summary = findings.get("summary", {})
    gh_agent = findings.get("gh_agent", {}) if isinstance(findings.get("gh_agent", {}), dict) else {}
    issue_closeout = findings.get("issue_closeout", {}) if isinstance(findings.get("issue_closeout", {}), dict) else {}
    lens = agent_brief.get("persona_contract", {}).get("lens", {})
    missing_proof_rows = handoff.get("missing_proof_evidence", [])
    missing_proof_summary = []
    for row in missing_proof_rows[:3]:
        missing = ", ".join(row.get("missing_proof_evidence", []))
        missing_proof_summary.append(f"{row.get('scenario', '')}: {missing}")
    if len(missing_proof_rows) > 3:
        missing_proof_summary.append(f"+{len(missing_proof_rows) - 3} more scenarios")
    review_checks = review.get("checks", [])
    actionable_review = [
        check for check in review_checks
        if check.get("status") in {"fail", "warn"}
    ]
    actionable_review.sort(key=lambda check: {"fail": 0, "warn": 1}.get(check.get("status"), 2))
    review_backlog = []
    for check in actionable_review[:4]:
        detail = check.get("detail", "")
        suffix = f" ({detail})" if detail else ""
        review_backlog.append(f"{check.get('status', 'unknown')}: {check.get('id', 'check')}{suffix}")
    if len(actionable_review) > 4:
        review_backlog.append(f"+{len(actionable_review) - 4} more review checks")
    gh_agent_fix_evidence = gh_agent_fix_evidence_links(gh_agent)
    gh_agent_missing_evidence = gh_agent_missing_fix_evidence(gh_agent)
    gh_agent_triage_evidence = gh_agent_triage_evidence_links(gh_agent)
    gh_agent_missing_triage = gh_agent_missing_triage_evidence(gh_agent)
    gh_agent_independent_verify = gh_agent_independent_verify_links(gh_agent)
    gh_agent_missing_verify = gh_agent_missing_independent_verify(gh_agent)
    missing_run_urls = gh_agent_missing_run_urls(gh_agent)
    control_gh_agent = control.get("gh_agent", {}) if isinstance(control.get("gh_agent", {}), dict) else {}
    control_driver = control.get("driver", {}) if isinstance(control.get("driver", {}), dict) else {}
    return {
        "gh_agent_public_base_url": control_gh_agent.get("public_base_url", gh_agent.get("public_base_url", "")),
        "autonomous_driver_live_profile": control_driver.get("live_profile", ""),
        "persona_starting_surface": lens.get("starting_surface", ""),
        "persona_first_question": lens.get("first_question", ""),
        "persona_evidence_emphasis": lens.get("evidence_emphasis", ""),
        "persona_escalation_trigger": lens.get("escalation_trigger", ""),
        "persona_finding_bias": lens.get("finding_bias", ""),
        "live_budget_minutes": run_json.get("live_budget_minutes", 0),
        "scenario_count": metrics.get("scenario_count", 0),
        "proof_evidence_count": metrics.get("proof_evidence_count", 0),
        "demo_evidence_count": metrics.get("demo_evidence_count", 0),
        "finding_total_count": sum(finding_summary.get(kind, 0) for kind in ["strength", "weakness", "issue", "fix"]),
        "strength_count": finding_summary.get("strength", metrics.get("strength_count", 0)),
        "weakness_count": finding_summary.get("weakness", metrics.get("weakness_count", 0)),
        "issue_count": finding_summary.get("issue", metrics.get("issue_count", 0)),
        "fix_count": finding_summary.get("fix", metrics.get("fix_count", 0)),
        "blocked_count": finding_summary.get("blocked", metrics.get("blocked_count", 0)),
        "weakness_route_count": weakness_routes.get("summary", {}).get("routed", len(weakness_routes.get("items", []))),
        "weakness_route_summary": "; ".join(
            f"{item.get('finding_id', '')}->{item.get('target_story', '')}"
            for item in weakness_routes.get("items", [])[:4]
            if isinstance(item, dict)
        ),
        "prd_design_intake_path": str(run_dir / "prd-design-intake.md") if (run_dir / "prd-design-intake.md").exists() else "",
        "prd_design_intake_count": prd_design_intake.get("summary", {}).get("intake_count", len(prd_design_intake.get("items", []))),
        "prd_design_intake_summary": "; ".join(
            f"{item.get('finding_id', '')}->{item.get('target_story', '')} {item.get('story_intent', '')}"
            for item in prd_design_intake.get("items", [])[:4]
            if isinstance(item, dict)
        ),
        "missing_evidence_count": metrics.get("missing_evidence_count", handoff.get("status", {}).get("missing_evidence_count", 0)),
        "missing_proof_evidence_count": handoff.get("status", {}).get("missing_proof_evidence_count", 0),
        "proof_minimum_evidence_count": handoff.get("status", {}).get("proof_minimum_evidence_count", 0),
        "minimum_evidence_count": handoff.get("status", {}).get("minimum_evidence_count", 0),
        "missing_proof_summary": "; ".join(missing_proof_summary),
        "driver_contract_summary": build_driver_contract_summary(driver_plan, handoff) if driver_plan else "",
        "next_driver_capture": build_next_driver_capture(handoff),
        "next_driver_capture_route": next_driver_capture_route(handoff),
        "next_driver_attach_command": next_driver_capture_slot(handoff).get("attach_command", ""),
        "next_driver_blocker_command": next_driver_blocker_command(handoff),
        "review_passed_count": review.get("summary_counts", {}).get("passed", 0),
        "review_failed_count": review.get("summary_counts", {}).get("failed", 0),
        "review_warning_count": review.get("summary_counts", {}).get("warned", 0),
        "review_total_count": review.get("summary_counts", {}).get("total", 0),
        "review_backlog_summary": "; ".join(review_backlog),
        "gh_agent_enqueue_status": gh_agent.get("enqueue_status", ""),
        "gh_agent_enqueued_count": gh_agent.get("enqueued_count", 0),
        "gh_agent_skipped_count": gh_agent.get("skipped_count", 0),
        "gh_agent_job_summary": gh_agent.get("job_summary", ""),
        "gh_agent_claim_status": gh_agent.get("claim_status", ""),
        "gh_agent_claim_count": gh_agent.get("claim_count", 0),
        "gh_agent_claim_summary": "; ".join(
            str(item.get("comment_url", ""))
            for item in gh_agent.get("claims", [])[:4]
            if isinstance(item, dict) and item.get("comment_url")
        ),
        "gh_agent_drain_status": gh_agent.get("drain_status", ""),
        "gh_agent_drained_count": gh_agent.get("drained_count", 0),
        "gh_agent_done_count": gh_agent.get("done_count", 0),
        "gh_agent_failed_count": gh_agent.get("failed_count", 0),
        "gh_agent_active_count": gh_agent.get("active_count", 0),
        "gh_agent_run_summary": gh_agent.get("run_summary", ""),
        "gh_agent_fix_evidence_count": len(gh_agent_fix_evidence),
        "gh_agent_missing_evidence_count": len(gh_agent_missing_evidence),
        "gh_agent_fix_evidence_summary": summarize_gh_agent_fix_evidence(gh_agent),
        "gh_agent_missing_evidence_summary": "; ".join(gh_agent_missing_evidence[:4]) + (f"; +{len(gh_agent_missing_evidence) - 4} more" if len(gh_agent_missing_evidence) > 4 else ""),
        "gh_agent_triage_evidence_count": len(gh_agent_triage_evidence),
        "gh_agent_missing_triage_count": len(gh_agent_missing_triage),
        "gh_agent_triage_evidence_summary": "; ".join(gh_agent_triage_evidence[:4]) + (f"; +{len(gh_agent_triage_evidence) - 4} more" if len(gh_agent_triage_evidence) > 4 else ""),
        "gh_agent_missing_triage_summary": "; ".join(gh_agent_missing_triage[:4]) + (f"; +{len(gh_agent_missing_triage) - 4} more" if len(gh_agent_missing_triage) > 4 else ""),
        "gh_agent_independent_verify_count": len(gh_agent_independent_verify),
        "gh_agent_missing_verify_count": len(gh_agent_missing_verify),
        "gh_agent_independent_verify_summary": "; ".join(gh_agent_independent_verify[:4]) + (f"; +{len(gh_agent_independent_verify) - 4} more" if len(gh_agent_independent_verify) > 4 else ""),
        "gh_agent_missing_verify_summary": "; ".join(gh_agent_missing_verify[:4]) + (f"; +{len(gh_agent_missing_verify) - 4} more" if len(gh_agent_missing_verify) > 4 else ""),
        "gh_agent_missing_run_url_count": len(missing_run_urls),
        "issue_closeout_status": issue_closeout.get("status", ""),
        "issue_closeout_count": issue_closeout.get("count", 0),
        "issue_closeout_summary": issue_closeout.get("summary", ""),
        "autonomous_control_path": str(autonomous_marathon_control_path(run_dir)) if control else "",
        "autonomous_control_markdown_path": str(autonomous_marathon_control_markdown_path(run_dir)) if control else "",
        "autonomous_control_status": control.get("status", ""),
        "autonomous_control_summary": autonomous_marathon_control_summary(control) if control else "",
        "autonomous_watchdog_status": watchdog.get("autonomous_watchdog_status", ""),
        "autonomous_watchdog_summary": watchdog.get("autonomous_watchdog_summary", ""),
        "autonomous_watchdog_path": watchdog.get("autonomous_watchdog_path", str(autonomous_marathon_watchdog_path(run_dir)) if watchdog else ""),
        "autonomous_watchdog_markdown_path": watchdog.get("autonomous_watchdog_markdown_path", str(autonomous_marathon_watchdog_markdown_path(run_dir)) if watchdog else ""),
        "autonomous_watchdog_age_minutes": watchdog.get("heartbeat_age_minutes", 0),
        "autonomous_driver_dispatch_path": str(autonomous_driver_dispatch_path(run_dir)) if driver_dispatch else "",
        "autonomous_driver_dispatch_markdown_path": str(autonomous_driver_dispatch_markdown_path(run_dir)) if driver_dispatch else "",
        "autonomous_driver_dispatch_status": driver_dispatch.get("status", ""),
        "autonomous_driver_dispatch_summary": driver_dispatch.get("summary", ""),
        "autonomous_driver_dispatch_trace": driver_dispatch.get("trace", ""),
        "campaign_worker_backend": campaign_worker.get("backend", ""),
        "campaign_worker_id": campaign_worker.get("worker_id", ""),
        "campaign_worker_status": campaign_worker.get("status", ""),
        "campaign_worker_ready_status": campaign_worker.get("ready_status", ""),
        "campaign_worker_summary": campaign_worker_summary(campaign_worker) if campaign_worker else "",
        "campaign_worker_receipt_path": str(campaign_worker_receipt_path(run_dir)) if campaign_worker else "",
        "campaign_worker_receipt_markdown_path": str(campaign_worker_receipt_markdown_path(run_dir)) if campaign_worker else "",
        "campaign_worker_imported_artifact_count": len(campaign_worker.get("imported_artifacts", [])),
        "campaign_worker_artifact_import_status": campaign_worker.get("artifact_import_status", ""),
    }


def kitsoki_cli_command() -> list[str]:
    """Resolve the kitsoki CLI invocation for headless orchestration.

    Defaults to `go run ./cmd/kitsoki` from the repo root; tests and installed
    environments override with KITSOKI_BIN (a shell-split command prefix).
    """
    override = os.environ.get("KITSOKI_BIN", "").strip()
    if override:
        return shlex.split(override)
    return ["go", "run", "./cmd/kitsoki"]


def credible_issue_findings(findings: dict) -> list[dict]:
    """Findings that must be filed when GitHub filing is requested: observed
    (non-seeded) `issue` findings, including blocked scenarios."""
    return [
        item for item in findings.get("items", [])
        if item.get("kind") == "issue" and item.get("origin", "observed") != "seeded"
    ]


def local_finding_ref(item: dict) -> dict:
    """The `local_ticket` block a credible finding carries once it has been
    filed to the default local-artifact sink (see `file_local_findings`).
    Returns {} when the finding has no local ticket."""
    ref = item.get("local_ticket")
    return ref if isinstance(ref, dict) else {}


def unfiled_credible_findings(findings: dict) -> list[str]:
    """Credible issue findings resolved by NEITHER sink: no filed GitHub
    issue and no local-artifact ticket. Filing to either sink satisfies the
    `findings-filed` review/validate gate; only the GitHub-only autonomous
    fix chain (gh-agent drain, close-out, autonomous-fix report) additionally
    requires the GitHub sink specifically (see `credible_findings_requiring_github`)."""
    return [
        item.get("id", "")
        for item in credible_issue_findings(findings)
        if not item.get("github_issue", {}).get("url")
        and not local_finding_ref(item).get("path")
    ]


def github_issue_ref(item: dict, fallback_repo: str) -> tuple[str, str, str, str]:
    github_issue = item.get("github_issue", {}) or {}
    url = str(github_issue.get("url", "")).strip()
    repo = str(github_issue.get("repo", "") or fallback_repo).strip()
    number = str(github_issue.get("number", "")).strip()
    kind = "issue"
    if (not repo or not number) and url:
        parsed = urllib.parse.urlparse(url)
        parts = [part for part in parsed.path.split("/") if part]
        if parsed.netloc.lower() == "github.com" and len(parts) >= 4 and parts[2] in {"issues", "pull"}:
            repo = repo or f"{parts[0]}/{parts[1]}"
            number = number or parts[3]
            if parts[2] == "pull":
                kind = "pr"
    return repo, number, kind, url


def github_issue_evidence_assets(issue: dict) -> list[dict]:
    raw = issue.get("evidence_assets", []) if isinstance(issue, dict) else []
    if isinstance(raw, dict):
        raw = [{"name": name, "url": url} for name, url in sorted(raw.items())]
    out = []
    for asset in raw or []:
        if not isinstance(asset, dict):
            continue
        url = str(asset.get("url", "")).strip()
        if not url:
            continue
        out.append({
            "name": str(asset.get("name", "") or "evidence").strip(),
            "url": url,
        })
    return out


def gh_agent_job_evidence_links(job: dict) -> list[str]:
    links: list[str] = []
    for asset in job.get("assets", []) or []:
        if not isinstance(asset, dict):
            continue
        for key in ("url", "href", "path"):
            value = str(asset.get(key, "")).strip()
            if value:
                links.append(value)
                break
    return links


def gh_agent_asset_name(asset: dict) -> str:
    return Path(str(asset.get("name") or asset.get("path") or asset.get("url") or "")).name


def gh_agent_job_independent_verify_links(job: dict) -> list[str]:
    links: list[str] = []
    for asset in job.get("assets", []) or []:
        if not isinstance(asset, dict):
            continue
        if gh_agent_asset_name(asset) != "independent-verify.md":
            continue
        for key in ("url", "href", "path"):
            value = str(asset.get(key, "")).strip()
            if value:
                links.append(value)
                break
    return links


def gh_agent_job_triage_evidence_links(job: dict) -> list[str]:
    links: list[str] = []
    for asset in job.get("assets", []) or []:
        if not isinstance(asset, dict):
            continue
        if gh_agent_asset_name(asset) != "triage-verdict.md":
            continue
        for key in ("url", "href", "path"):
            value = str(asset.get(key, "")).strip()
            if value:
                links.append(value)
                break
    return links


def gh_agent_missing_fix_evidence(gh_agent: dict) -> list[str]:
    missing: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        if not gh_agent_job_evidence_links(job):
            missing.append(str(job.get("origin_ref") or job.get("job_id") or "unknown"))
    return missing


def gh_agent_missing_triage_evidence(gh_agent: dict) -> list[str]:
    missing: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        if not gh_agent_job_triage_evidence_links(job):
            missing.append(str(job.get("origin_ref") or job.get("job_id") or "unknown"))
    return missing


def gh_agent_triage_evidence_links(gh_agent: dict) -> list[str]:
    links: list[str] = []
    seen: set[str] = set()
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        for link in gh_agent_job_triage_evidence_links(job):
            if link not in seen:
                seen.add(link)
                links.append(link)
    return links


def gh_agent_missing_run_urls(gh_agent: dict) -> list[str]:
    missing: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        if not str(job.get("run_url", "")).strip():
            missing.append(str(job.get("origin_ref") or job.get("job_id") or "unknown"))
    return missing


def gh_agent_job_integration_branch(job: dict) -> str:
    return str(job.get("integration_branch") or job.get("branch") or "").strip()


def gh_agent_job_commit_sha(job: dict) -> str:
    return str(job.get("commit_sha") or job.get("commit") or job.get("fixed_commit") or "").strip()


def gh_agent_job_commit_url(job: dict) -> str:
    return str(job.get("commit_url") or "").strip()


def gh_agent_fix_evidence_links(gh_agent: dict) -> list[str]:
    links: list[str] = []
    seen: set[str] = set()
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        for link in gh_agent_job_evidence_links(job):
            if link not in seen:
                seen.add(link)
                links.append(link)
    return links


def gh_agent_missing_independent_verify(gh_agent: dict) -> list[str]:
    missing: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        if not gh_agent_job_independent_verify_links(job):
            missing.append(str(job.get("origin_ref") or job.get("job_id") or "unknown"))
    return missing


def gh_agent_independent_verify_links(gh_agent: dict) -> list[str]:
    links: list[str] = []
    seen: set[str] = set()
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        for link in gh_agent_job_independent_verify_links(job):
            if link not in seen:
                seen.add(link)
                links.append(link)
    return links


def summarize_gh_agent_fix_evidence(gh_agent: dict) -> str:
    links = gh_agent_fix_evidence_links(gh_agent)
    if not links:
        return ""
    parts = links[:4]
    if len(links) > 4:
        parts.append(f"+{len(links) - 4} more")
    return "; ".join(parts)


def autonomous_fix_report_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-fix-report.md"


def render_autonomous_fix_report(run_dir: Path, status: dict, review: Optional[dict] = None, validation: Optional[dict] = None) -> str:
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"items": []}
    gh_agent = findings.get("gh_agent", {}) if isinstance(findings.get("gh_agent", {}), dict) else {}
    filing = findings.get("filing", {}) if isinstance(findings.get("filing", {}), dict) else {}
    issue_closeout = findings.get("issue_closeout", {}) if isinstance(findings.get("issue_closeout", {}), dict) else {}
    issue_items = [
        item for item in findings.get("items", [])
        if isinstance(item, dict) and item.get("github_issue", {}).get("url")
    ]
    lines = [
        "# Autonomous Fix Report",
        "",
        f"- Run: `{run_dir.name}`",
        f"- Ticket repo: `{status.get('ticket_repo', filing.get('ticket_repo', ''))}`",
        f"- Status: `{status.get('autonomous_fix_status', status.get('status', ''))}`",
        f"- Gates: {status.get('autonomous_gate_summary', '(not evaluated)')}",
        f"- Independent verification: `{status.get('independent_verify_status', '')}` - {status.get('independent_verify_summary', '')}",
        f"- Issue close-out: `{status.get('issue_closeout_status', '')}` ({int(status.get('issue_closeout_count', 0) or 0)} closed)",
        "",
        "## Autonomous Watchdog",
        "",
        f"- Status: `{status.get('autonomous_watchdog_status', '') or '(not checked)'}` - {status.get('autonomous_watchdog_summary', '')}",
        f"- Age: {int(status.get('autonomous_watchdog_age_minutes', 0) or 0)} minute(s)",
        f"- Report: {status.get('autonomous_watchdog_markdown_path', '') or '(not generated)'}",
        "",
        "## Hosted GH-agent",
        "",
        f"- Health: `{status.get('gh_agent_health_status', '') or '(not checked)'}` - {status.get('gh_agent_health_summary', '')}",
        f"- Readiness: `{status.get('gh_agent_readiness_status', '') or '(not checked)'}` - {status.get('gh_agent_readiness_summary', '')}",
        "",
        "## Filed Issues",
        "",
    ]
    if issue_items:
        for item in issue_items:
            issue = item.get("github_issue", {})
            lines.append(f"- `{item.get('id', item.get('title', 'finding'))}`: {issue.get('url', '')}")
            for asset in github_issue_evidence_assets(issue)[:4]:
                lines.append(f"  - evidence `{asset['name']}`: {asset['url']}")
    else:
        lines.append("- (none)")
    lines.extend([
        "",
        "## GH-agent Claims",
        "",
        f"- Claims: `{gh_agent.get('claim_status', status.get('gh_agent_claim_status', 'not requested'))}`",
        f"- Claimed: {gh_agent.get('claim_count', status.get('gh_agent_claim_count', 0))}",
    ])
    claim_items = [item for item in gh_agent.get("claims", []) or [] if isinstance(item, dict)]
    if claim_items:
        for item in claim_items:
            parts = [
                item.get("issue_url", ""),
                f"comment={item.get('comment_url', '')}" if item.get("comment_url") else "",
                f"job={item.get('job_id', '')}" if item.get("job_id") else "",
            ]
            lines.append("- " + " · ".join(part for part in parts if part))
    else:
        lines.append("- (none)")
    lines.extend([
        "",
        "## GH-agent Runs",
        "",
        (
            f"- Queue: `{gh_agent.get('enqueue_status', 'not requested')}` "
            f"(enqueued {gh_agent.get('enqueued_count', 0)}, skipped {gh_agent.get('skipped_count', 0)})"
        ),
        (
            f"- Drain: `{gh_agent.get('drain_status', 'not requested')}` "
            f"(drained {gh_agent.get('drained_count', 0)}, done {gh_agent.get('done_count', 0)}, "
            f"failed {gh_agent.get('failed_count', 0)}, active {gh_agent.get('active_count', 0)})"
        ),
        "",
    ])
    jobs = [job for job in gh_agent.get("drained_jobs", []) or gh_agent.get("jobs", []) if isinstance(job, dict)]
    if jobs:
        for job in jobs:
            label = job.get("origin_ref") or job.get("job_id") or "unknown"
            lines.extend([
                f"### {label}",
                "",
                f"- State: `{job.get('state', '')}`",
                f"- Run URL: {job.get('run_url', '') or '(missing)'}",
                f"- Integration branch: `{gh_agent_job_integration_branch(job) or '(missing)'}`",
                f"- Commit: `{gh_agent_job_commit_sha(job) or '(missing)'}`",
            ])
            if gh_agent_job_commit_url(job):
                lines.append(f"- Commit URL: {gh_agent_job_commit_url(job)}")
            if job.get("incident_url"):
                lines.append(f"- Incident: {job.get('incident_url')}")
            if job.get("err_msg"):
                lines.append(f"- Error: {job.get('err_msg')}")
            evidence_links = gh_agent_job_evidence_links(job)
            lines.append("- Evidence:")
            if evidence_links:
                lines.extend([f"  - {link}" for link in evidence_links])
            else:
                lines.append("  - (missing)")
            lines.append("")
    else:
        lines.append("- (none)")
    lines.extend([
        "",
        "## Issue Close-out",
        "",
        f"- Issue close-out: `{issue_closeout.get('status', status.get('issue_closeout_status', 'not run'))}`",
        f"- Closed: {issue_closeout.get('count', status.get('issue_closeout_count', 0))}",
    ])
    if issue_closeout.get("summary"):
        lines.append(f"- Summary: {issue_closeout.get('summary')}")
    closeout_items = [item for item in issue_closeout.get("items", []) or [] if isinstance(item, dict)]
    if closeout_items:
        for item in closeout_items:
            parts = [
                item.get("issue_url", ""),
                f"comment={item.get('comment_url', '')}" if item.get("comment_url") else "",
                f"run={item.get('run_url', '')}" if item.get("run_url") else "",
            ]
            lines.append("- " + " · ".join(part for part in parts if part))
    else:
        lines.append("- (none)")
    review_status = (review or {}).get("review_status", (review or {}).get("status", status.get("review_status", "")))
    lines.extend([
        "## Review",
        "",
        f"- Status: `{review_status}`",
        f"- Summary: {(review or {}).get('summary', status.get('review_summary', ''))}",
        "",
        "## Validation",
        "",
        f"- Status: `{(validation or {}).get('status', status.get('validation_status', ''))}`",
        f"- Issues: {(validation or {}).get('validation_issue_summary', status.get('validation_issue_summary', '')) or '(none)'}",
        "",
    ])
    return "\n".join(lines)


def write_autonomous_fix_report(run_dir: Path, status: dict, review: Optional[dict] = None, validation: Optional[dict] = None) -> Path:
    path = autonomous_fix_report_path(run_dir)
    path.write_text(render_autonomous_fix_report(run_dir, status, review, validation) + "\n", encoding="utf-8")
    return path


def independent_verify_gate_from_summary(summary: dict, gh_agent_requested: bool, enqueued_count: int) -> tuple[bool, str]:
    if not gh_agent_requested:
        return True, "independent verification not required"
    verified = int(summary.get("gh_agent_independent_verify_count", 0) or 0)
    missing = int(summary.get("gh_agent_missing_verify_count", 0) or 0)
    if enqueued_count <= 0:
        return False, "no queued fix jobs"
    if missing > 0:
        return False, f"missing={missing}, verified={verified}/{enqueued_count}"
    if verified < enqueued_count:
        return False, f"verified={verified}/{enqueued_count}"
    return True, f"verified={verified}/{enqueued_count}"


def local_finding_body(item: dict, run_dir: Path) -> str:
    lines = [str(item.get("summary", "")).strip()]
    lines.append("")
    if item.get("scenario"):
        lines.append(f"Scenario: {item['scenario']}")
    if item.get("severity"):
        lines.append(f"Severity: {item['severity']}")
    if item.get("evidence_path"):
        lines.append(f"Evidence: {item['evidence_path']}")
    lines.append(f"Run bundle: {run_dir}")
    return "\n".join(line for line in lines if line is not None).strip() + "\n"


def autonomous_marathon_control_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-marathon-control.json"


def autonomous_marathon_control_markdown_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-marathon-control.md"


def autonomous_marathon_watchdog_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-marathon-watchdog.json"


def autonomous_marathon_watchdog_markdown_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-marathon-watchdog.md"


def render_autonomous_marathon_control(control: dict) -> str:
    cadence = control.get("cadence", {})
    watchdog = control.get("watchdog", {})
    budget = control.get("budget", {})
    driver = control.get("driver", {}) if isinstance(control.get("driver", {}), dict) else {}
    gh_agent = control.get("gh_agent", {})
    gitops = control.get("gitops", {}) if isinstance(control.get("gitops", {}), dict) else {}
    lines = [
        "# Autonomous Marathon Control",
        "",
        f"- Run: `{control.get('run_id', '')}`",
        f"- Status: `{control.get('status', '')}`",
        f"- Driver mode: `{control.get('driver_mode', '')}`",
        f"- Driver live profile: `{driver.get('live_profile', '') or '(not set)'}`",
        f"- Cadence: every {cadence.get('hours', 0)} hour(s)",
        f"- Next due: `{cadence.get('next_due_at', '')}`",
        f"- Per-scenario live budget: {budget.get('per_scenario_live_minutes', 0)} minute(s)",
        f"- Human role: {control.get('human_role', '')}",
        f"- Heartbeat: every {watchdog.get('heartbeat_minutes', 0)} minute(s)",
        f"- Watchdog: escalate after {watchdog.get('watchdog_minutes', 0)} minute(s)",
        f"- Ticket repo: `{gitops.get('ticket_repo', '') or '(not set)'}`",
        f"- Hosted gh-agent: `{gh_agent.get('public_base_url', '') or '(not set)'}`",
        "",
        "## Final Gates",
        "",
    ]
    for gate in control.get("final_gates", []):
        lines.append(f"- `{gate}`")
    return "\n".join(lines) + "\n"


def autonomous_marathon_control_summary(control: dict) -> str:
    cadence = control.get("cadence", {})
    budget = control.get("budget", {})
    watchdog = control.get("watchdog", {})
    return (
        f"cadence={cadence.get('hours', 0)}h, "
        f"next_due={cadence.get('next_due_at', '')}, "
        f"budget={budget.get('per_scenario_live_minutes', 0)}m/scenario, "
        f"heartbeat={watchdog.get('heartbeat_minutes', 0)}m, "
        f"watchdog={watchdog.get('watchdog_minutes', 0)}m"
    )


def shell_command(parts: list[str]) -> str:
    return " ".join(shlex.quote(str(part)) for part in parts)


def gh_agent_health_url(public_base_url: str) -> str:
    base = public_base_url.strip().rstrip("/")
    if not base:
        return ""
    return f"{base}/healthz"


def gh_agent_ready_url(public_base_url: str) -> str:
    base = public_base_url.strip().rstrip("/")
    if not base:
        return ""
    return f"{base}/api/ready"


def check_gh_agent_health(public_base_url: str, timeout: float = 5.0) -> dict:
    url = gh_agent_health_url(public_base_url)
    if not url:
        return {
            "status": "fail",
            "summary": "gh_agent_public_base_url is required",
            "url": "",
        }
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            body = resp.read(256).decode("utf-8", errors="replace").strip()
            code = getattr(resp, "status", resp.getcode())
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        return {
            "status": "fail",
            "summary": f"{url}: {exc}",
            "url": url,
        }
    if code == 200 and body == "ok":
        return {
            "status": "pass",
            "summary": f"{url}: ok",
            "url": url,
            "http_status": code,
        }
    return {
        "status": "fail",
        "summary": f"{url}: expected HTTP 200 body ok, got HTTP {code} body {body!r}",
        "url": url,
        "http_status": code,
    }


def check_gh_agent_readiness(public_base_url: str, ticket_repo: str, timeout: float = 5.0) -> dict:
    url = gh_agent_ready_url(public_base_url)
    if not url:
        return {
            "status": "fail",
            "summary": "gh_agent_public_base_url is required",
            "url": "",
        }
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            body = resp.read(4096).decode("utf-8", errors="replace")
            code = getattr(resp, "status", resp.getcode())
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        return {
            "status": "fail",
            "summary": f"{url}: {exc}",
            "url": url,
        }
    if code != 200:
        return {
            "status": "fail",
            "summary": f"{url}: expected HTTP 200, got HTTP {code}",
            "url": url,
            "http_status": code,
        }
    try:
        payload = json.loads(body)
    except json.JSONDecodeError as exc:
        return {
            "status": "fail",
            "summary": f"{url}: invalid JSON readiness payload ({exc})",
            "url": url,
            "http_status": code,
        }
    expected_repo = ticket_repo.strip()
    actual_repo = str(payload.get("repo", "")).strip()
    if payload.get("status") != "ready":
        return {
            "status": "fail",
            "summary": f"{url}: status={payload.get('status', '')!r}",
            "url": url,
            "http_status": code,
        }
    if expected_repo and actual_repo != expected_repo:
        return {
            "status": "fail",
            "summary": f"{url}: repo mismatch, expected {expected_repo}, got {actual_repo or '(empty)'}",
            "url": url,
            "http_status": code,
        }
    if payload.get("drain_enabled") is not True:
        return {
            "status": "fail",
            "summary": f"{url}: drain loop is not enabled",
            "url": url,
            "http_status": code,
        }
    public_base = str(payload.get("public_base_url", "")).strip().rstrip("/")
    expected_base = public_base_url.strip().rstrip("/")
    if public_base and expected_base and public_base != expected_base:
        return {
            "status": "fail",
            "summary": f"{url}: public_base_url mismatch, expected {expected_base}, got {public_base}",
            "url": url,
            "http_status": code,
        }
    worker = str(payload.get("worker", "")).strip()
    return {
        "status": "pass",
        "summary": f"{url}: ready for {actual_repo or expected_repo} as {worker or '(unknown worker)'}",
        "url": url,
        "http_status": code,
        "payload": payload,
    }


def invalid_autonomous_marathon_creation(
    created_dir: Path,
    run_json: dict,
    summary: str,
    validation_issue: str,
    gate_summary: str,
    ticket_repo: str,
    gh_agent_public_base_url: str,
    gh_agent_health: Optional[dict] = None,
    gh_agent_readiness: Optional[dict] = None,
    autonomous_driver_mode: str = "pending",
    autonomous_driver_live_profile: str = "",
) -> dict:
    result = run_story_summary(created_dir)
    result.update({
        "status": "autonomous_marathon_invalid",
        "autonomous_marathon_status": "autonomous_marathon_invalid",
        "autonomous_marathon_summary": summary,
        "autonomous_driver_mode": autonomous_driver_mode or "pending",
        "autonomous_driver_live_profile": autonomous_driver_live_profile,
        "autonomous_driver_status": "invalid",
        "autonomous_driver_summary": summary,
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
        "ticket_repo": ticket_repo,
        "gh_agent_public_base_url": gh_agent_public_base_url,
        "gh_agent_health_status": (gh_agent_health or {}).get("status", ""),
        "gh_agent_health_summary": (gh_agent_health or {}).get("summary", ""),
        "gh_agent_readiness_status": (gh_agent_readiness or {}).get("status", ""),
        "gh_agent_readiness_summary": (gh_agent_readiness or {}).get("summary", ""),
        "autonomous_fix_status": "not_run",
        "independent_verify_status": "not_run",
        "independent_verify_summary": summary,
        "autonomous_gate_summary": gate_summary,
        "autonomous_control_path": "",
        "autonomous_control_markdown_path": "",
        "autonomous_control_status": "not_run",
        "autonomous_control_summary": "",
        "review_status": "not_run",
        "review_summary": "",
        "review_failed_count": 0,
        "validation_status": "invalid",
        "validation_errors": 1,
        "validation_warnings": 0,
        "validation_issue_summary": validation_issue,
    })
    result["autonomous_marathon_report_path"] = str(write_autonomous_marathon_report(created_dir, result))
    return result


def autonomous_marathon_cycle_seed(seed: str, checked: datetime.datetime) -> str:
    base = (seed or "default").strip()
    suffix = checked.strftime("%Y%m%dT%H%M%SZ")
    if base.endswith(suffix):
        return base
    return f"{base}-cycle-{suffix}"


def autonomous_marathon_due_params(run_json: dict, control: dict, checked: datetime.datetime) -> dict:
    cadence = control.get("cadence", {}) if isinstance(control.get("cadence", {}), dict) else {}
    budget = control.get("budget", {}) if isinstance(control.get("budget", {}), dict) else {}
    watchdog = control.get("watchdog", {}) if isinstance(control.get("watchdog", {}), dict) else {}
    driver = control.get("driver", {}) if isinstance(control.get("driver", {}), dict) else {}
    gitops = control.get("gitops", {}) if isinstance(control.get("gitops", {}), dict) else {}
    gh_agent = control.get("gh_agent", {}) if isinstance(control.get("gh_agent", {}), dict) else {}
    scenarios = control.get("scenario_scope") or [
        scenario.get("id", "")
        for scenario in run_json.get("scenarios", [])
        if isinstance(scenario, dict) and scenario.get("id")
    ]
    project = run_json.get("project", {}).get("id", "") if isinstance(run_json.get("project"), dict) else ""
    persona = run_json.get("persona", {}).get("id", "") if isinstance(run_json.get("persona"), dict) else ""
    return {
        "project": project,
        "persona": persona,
        "seed": autonomous_marathon_cycle_seed(str(run_json.get("seed", "")), checked),
        "scenarios": ",".join(str(item) for item in scenarios if str(item).strip()),
        "live_budget_minutes": int(budget.get("per_scenario_live_minutes", run_json.get("live_budget_minutes", 0)) or 0),
        "autonomous_driver_mode": str(control.get("driver_mode", "") or "pending"),
        "autonomous_driver_live_profile": str(driver.get("live_profile", "") or control.get("driver_live_profile", "")),
        "autonomous_cadence_hours": int(cadence.get("hours", 24) or 24),
        "autonomous_heartbeat_minutes": int(watchdog.get("heartbeat_minutes", 15) or 15),
        "autonomous_watchdog_minutes": int(watchdog.get("watchdog_minutes", 45) or 45),
        "ticket_repo": str(gitops.get("ticket_repo", "")),
        "gh_agent_public_base_url": str(gh_agent.get("public_base_url", "")),
    }


def autonomous_marathon_due_command(run_json: dict, control: dict, checked: datetime.datetime) -> list[str]:
    params = autonomous_marathon_due_params(run_json, control, checked)
    parts = [
        "python3",
        "tools/product-journey/run.py",
        "--autonomous-marathon",
        "--json-output",
        "--report-invalid-autonomous-marathon",
        "--project",
        params["project"],
        "--persona",
        params["persona"],
        "--seed",
        params["seed"],
        "--scenarios",
        params["scenarios"],
        "--live-budget-minutes",
        params["live_budget_minutes"],
        "--autonomous-driver-mode",
        params["autonomous_driver_mode"],
        "--autonomous-cadence-hours",
        params["autonomous_cadence_hours"],
        "--autonomous-heartbeat-minutes",
        params["autonomous_heartbeat_minutes"],
        "--autonomous-watchdog-minutes",
        params["autonomous_watchdog_minutes"],
        "--ticket-repo",
        params["ticket_repo"],
        "--gh-agent-public-base-url",
        params["gh_agent_public_base_url"],
    ]
    if params.get("autonomous_driver_live_profile"):
        parts.extend([
            "--autonomous-driver-live-profile",
            params["autonomous_driver_live_profile"],
        ])
    return [str(part) for part in parts]


def autonomous_marathon_due_story_intent(run_json: dict, control: dict, checked: datetime.datetime) -> str:
    parts = autonomous_marathon_due_command(run_json, control, checked)
    values = {}
    index = 0
    while index < len(parts):
        part = parts[index]
        if part.startswith("--") and index + 1 < len(parts) and not parts[index + 1].startswith("--"):
            values[part[2:].replace("-", "_")] = parts[index + 1]
            index += 2
            continue
        index += 1
    story_parts = [
        "autonomous_marathon",
        f"project={values.get('project', '')}",
        f"persona={values.get('persona', '')}",
        f"seed={values.get('seed', '')}",
        f"scenarios={values.get('scenarios', '')}",
        f"live_budget_minutes={values.get('live_budget_minutes', '')}",
        f"autonomous_driver_mode={values.get('autonomous_driver_mode', '')}",
    ]
    if values.get("autonomous_driver_live_profile"):
        story_parts.append(f"autonomous_driver_live_profile={values.get('autonomous_driver_live_profile', '')}")
    story_parts.extend([
        f"ticket_repo={values.get('ticket_repo', '')}",
        f"gh_agent_public_base_url={values.get('gh_agent_public_base_url', '')}",
    ])
    return " ".join(story_parts).strip()


def autonomous_marathon_due_item(run_dir: Path, control: dict, checked: datetime.datetime) -> dict:
    run_json = read_json(run_dir / "run.json") if (run_dir / "run.json").exists() else {}
    cadence = control.get("cadence", {}) if isinstance(control.get("cadence", {}), dict) else {}
    budget = control.get("budget", {}) if isinstance(control.get("budget", {}), dict) else {}
    driver = control.get("driver", {}) if isinstance(control.get("driver", {}), dict) else {}
    gitops = control.get("gitops", {}) if isinstance(control.get("gitops", {}), dict) else {}
    gh_agent = control.get("gh_agent", {}) if isinstance(control.get("gh_agent", {}), dict) else {}
    status = str(control.get("status", ""))
    driver_mode = str(control.get("driver_mode", ""))
    next_due_value = str(cadence.get("next_due_at", ""))
    item = {
        "run_id": control.get("run_id", run_json.get("run_id", run_dir.name)),
        "run_dir": str(run_dir),
        "control_path": str(autonomous_marathon_control_path(run_dir)),
        "status": status,
        "driver_mode": driver_mode,
        "autonomous_driver_live_profile": str(driver.get("live_profile", "") or control.get("driver_live_profile", "")),
        "project": run_json.get("project", {}).get("id", "") if isinstance(run_json.get("project"), dict) else "",
        "persona": run_json.get("persona", {}).get("id", "") if isinstance(run_json.get("persona"), dict) else "",
        "seed": run_json.get("seed", ""),
        "scenario_scope": control.get("scenario_scope", []),
        "live_budget_minutes": int(budget.get("per_scenario_live_minutes", run_json.get("live_budget_minutes", 0)) or 0),
        "ticket_repo": str(gitops.get("ticket_repo", "")),
        "gh_agent_public_base_url": str(gh_agent.get("public_base_url", "")),
        "next_due_at": next_due_value,
        "minutes_until_due": 0,
        "minutes_overdue": 0,
        "blocked_reason": "",
        "ignored_reason": "",
        "next_command": "",
        "next_story_intent": "",
    }
    if not run_json:
        item["blocked_reason"] = "missing run.json"
        return item
    if status not in {"armed", "ready_for_driver"}:
        item["ignored_reason"] = f"control status {status or '(empty)'} is not an active standing marathon"
        return item
    if not next_due_value:
        item["blocked_reason"] = "missing cadence.next_due_at"
        return item
    try:
        next_due = parse_iso_datetime(next_due_value)
    except SystemExit:
        item["blocked_reason"] = f"invalid cadence.next_due_at: {next_due_value}"
        return item
    delta_minutes = int((next_due - checked).total_seconds() // 60)
    item["minutes_until_due"] = max(0, delta_minutes)
    item["minutes_overdue"] = max(0, -delta_minutes)
    if driver_mode == "pending":
        item["blocked_reason"] = "pending driver mode still requires operator handoff; use replay, record, or live for unattended cadence"
        return item
    if driver_mode in {"record", "live"} and not item["autonomous_driver_live_profile"]:
        item["blocked_reason"] = "record/live driver mode requires persisted autonomous_driver_live_profile for unattended cadence"
        return item
    if driver_mode in {"record", "live"}:
        dispatch = read_json(autonomous_driver_dispatch_path(run_dir)) if autonomous_driver_dispatch_path(run_dir).exists() else {}
        if dispatch.get("status") != "captured":
            item["blocked_reason"] = "record/live driver has no captured autonomous-driver-dispatch receipt"
            return item
    if item["live_budget_minutes"] > 0 or driver_mode == "replay":
        missing = []
        if not item["ticket_repo"]:
            missing.append("ticket_repo")
        if not item["gh_agent_public_base_url"]:
            missing.append("gh_agent_public_base_url")
        if missing:
            item["blocked_reason"] = "missing gitops config: " + ", ".join(missing)
            return item
    if checked < next_due:
        return item
    command_parts = autonomous_marathon_due_command(run_json, control, checked)
    item["next_command"] = shell_command(command_parts)
    item["next_story_intent"] = autonomous_marathon_due_story_intent(run_json, control, checked)
    return item


def latest_driver_heartbeat(run_dir: Path) -> dict:
    journal_path = run_dir / "driver-journal.json"
    if not journal_path.exists():
        return {}
    journal = read_json(journal_path)
    events = [
        event for event in journal.get("items", [])
        if isinstance(event, dict) and event.get("created_at")
    ]
    if not events:
        return {}
    return max(events, key=lambda event: parse_iso_datetime(str(event.get("created_at", ""))))


def render_autonomous_marathon_watchdog(result: dict) -> str:
    lines = [
        "# Autonomous Marathon Watchdog",
        "",
        f"- Run: `{result.get('run_id', '')}`",
        f"- Status: `{result.get('autonomous_watchdog_status', result.get('status', ''))}`",
        f"- Summary: {result.get('autonomous_watchdog_summary', '')}",
        f"- Checked at: `{result.get('checked_at', '')}`",
        f"- Control: `{result.get('autonomous_control_path', '') or '(missing)'}`",
        f"- Driver journal: `{result.get('driver_journal_path', '') or '(missing)'}`",
        f"- Heartbeat: every {result.get('heartbeat_minutes', 0)} minute(s)",
        f"- Watchdog: escalate after {result.get('watchdog_minutes', 0)} minute(s)",
        f"- Latest heartbeat: `{result.get('latest_heartbeat_at', '') or '(none)'}`",
        f"- Heartbeat age: {result.get('heartbeat_age_minutes', 0)} minute(s)",
        f"- On missed heartbeat: `{result.get('on_missed_heartbeat', '')}`",
        "",
        "## Blocker",
        "",
    ]
    if result.get("blocker_summary"):
        lines.append(result["blocker_summary"])
    else:
        lines.append("No watchdog blocker is active.")
    return "\n".join(lines) + "\n"


def autonomous_marathon_report_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-marathon-report.md"


def autonomous_driver_dispatch_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-driver-dispatch.json"


def autonomous_driver_dispatch_markdown_path(run_dir: Path) -> Path:
    return run_dir / "autonomous-driver-dispatch.md"


def campaign_worker_receipt_path(run_dir: Path) -> Path:
    return run_dir / "campaign-worker-receipt.json"


def campaign_worker_receipt_markdown_path(run_dir: Path) -> Path:
    return run_dir / "campaign-worker-receipt.md"


def render_campaign_worker_receipt(receipt: dict) -> str:
    lines = [
        "# Campaign Worker Receipt",
        "",
        f"- Run: `{receipt.get('run_id', '')}`",
        f"- Backend: `{receipt.get('backend', '')}`",
        f"- Worker: `{receipt.get('worker_id', '')}`",
        f"- Status: `{receipt.get('status', '')}`",
        f"- Ready: `{receipt.get('ready_status', '')}` - {receipt.get('ready_summary', '')}",
        f"- Scenario scope: `{', '.join(receipt.get('scenario_scope', [])) or '(none)'}`",
        f"- Budget: `{receipt.get('budget_minutes', 0)}` min/scenario",
        f"- Receipt source: `{receipt.get('receipt_source', '') or '(operator)'}`",
        f"- Artifact import: `{receipt.get('artifact_import_status', '')}` - {receipt.get('artifact_import_summary', '')}",
        f"- Recorded at: `{receipt.get('recorded_at', '')}`",
        "",
        "## Artifacts",
        "",
    ]
    imported = receipt.get("imported_artifacts", [])
    if imported:
        lines.extend(f"- `{item}`" for item in imported)
    else:
        lines.append("- No imported artifacts were reported.")
    lines.extend(["", "## Summary", "", receipt.get("summary", "") or "(none)"])
    return "\n".join(lines) + "\n"


def campaign_worker_summary(receipt: dict) -> str:
    return (
        f"{receipt.get('backend', '')}:{receipt.get('worker_id', '')} "
        f"{receipt.get('status', '')}; ready={receipt.get('ready_status', '')}; "
        f"artifacts={len(receipt.get('imported_artifacts', []))}"
    )


def render_autonomous_marathon_report(run_dir: Path, result: dict) -> str:
    lines = [
        "# Autonomous Marathon Report",
        "",
        f"- Run: `{run_dir.name}`",
        f"- Status: `{result.get('autonomous_marathon_status', result.get('status', ''))}`",
        f"- Summary: {result.get('autonomous_marathon_summary', '')}",
        f"- Autonomous driver: `{result.get('autonomous_driver_mode', 'pending')}` / `{result.get('autonomous_driver_status', 'pending')}`",
        f"- Driver live profile: `{result.get('autonomous_driver_live_profile', '') or '(not set)'}`",
        f"- Driver proof: {result.get('autonomous_driver_summary', '')}",
        f"- Driver dispatch receipt: `{result.get('autonomous_driver_dispatch_markdown_path', '') or '(not generated)'}`",
        f"- Driver dispatch trace: `{result.get('autonomous_driver_dispatch_trace', '') or '(not reported)'}`",
        f"- Control: `{result.get('autonomous_control_path', '') or '(not generated)'}`",
        f"- Control status: `{result.get('autonomous_control_status', '') or '(unknown)'}` - {result.get('autonomous_control_summary', '')}",
        f"- Watchdog: `{result.get('autonomous_watchdog_status', '') or '(not checked)'}` - {result.get('autonomous_watchdog_summary', '')}",
        f"- Watchdog report: `{result.get('autonomous_watchdog_markdown_path', '') or '(not generated)'}`",
        f"- Driver handoff: `{result.get('driver_handoff_path', run_dir / 'driver-handoff.md')}`",
        f"- Autonomous fix report: `{result.get('autonomous_fix_report_path', '') or '(not generated)'}`",
        f"- Stats output: `{result.get('stats_output', '') or '(not generated)'}`",
        "",
        "## Gates",
        "",
        f"- Autonomous fix: `{result.get('autonomous_fix_status', 'not_run')}`",
        f"- Autonomous gates: {result.get('autonomous_gate_summary', '(not run)')}",
        f"- Review: `{result.get('review_status', '')}` ({result.get('review_failed_count', 0)} failed)",
        f"- Validation: `{result.get('validation_status', '')}` ({result.get('validation_errors', 0)} errors)",
        f"- Weakness routes: {result.get('weakness_route_count', 0)}",
        f"- Stats gate: `{result.get('stats_gate_status', '')}` - {result.get('stats_gate_summary', '')}",
        f"- Stats current run scanned: `{result.get('stats_current_run_scanned', '')}`",
        f"- Stats: {result.get('stats_summary', '(not derived)')}",
        "",
        "## Next Driver Action",
        "",
    ]
    next_attach = result.get("next_driver_attach_command", "")
    next_blocker = result.get("next_driver_blocker_command", "")
    if next_attach or next_blocker:
        if next_attach:
            lines.append(f"- Attach proof: `{next_attach}`")
        if next_blocker:
            lines.append(f"- Record blocker: `{next_blocker}`")
    else:
        lines.append("- No pending driver action is advertised by the handoff artifact.")
    return "\n".join(lines)


def write_autonomous_marathon_report(run_dir: Path, result: dict) -> Path:
    path = autonomous_marathon_report_path(run_dir)
    path.write_text(render_autonomous_marathon_report(run_dir, result) + "\n", encoding="utf-8")
    return path


def normalize_issue_title(value: str) -> str:
    return " ".join(
        "".join(ch.lower() if ch.isalnum() else " " for ch in value).split()
    )


def load_issue_state(path: str) -> dict[str, dict]:
    if not path:
        return {}
    source = run_dir_from_arg(path)
    if not source.exists():
        raise SystemExit(f"--issue-state-file does not exist: {source}")
    payload = read_json(source)
    if isinstance(payload, dict) and isinstance(payload.get("issues"), list):
        items = payload["issues"]
    elif isinstance(payload, list):
        items = payload
    elif isinstance(payload, dict):
        items = []
        for key, value in payload.items():
            if isinstance(value, dict):
                item = dict(value)
                item.setdefault("url", key)
                items.append(item)
    else:
        items = []
    state: dict[str, dict] = {}
    for item in items:
        if not isinstance(item, dict):
            continue
        url = item.get("url") or item.get("html_url") or item.get("issue_url")
        if url:
            state[str(url)] = item
        repo = item.get("repo", "")
        number = item.get("number", "")
        if repo and number:
            state[f"https://github.com/{repo}/issues/{number}"] = item
    return state


def issue_marker_text(issue: dict) -> str:
    chunks = [
        str(issue.get("body", "")),
        str(issue.get("resolution", "")),
        str(issue.get("fixed_by", "")),
    ]
    for comment in issue.get("comments", []) or []:
        if isinstance(comment, dict):
            chunks.append(str(comment.get("body", "")))
        else:
            chunks.append(str(comment))
    return "\n".join(chunks).lower()


def issue_is_closed(issue: dict) -> bool:
    return str(issue.get("state", issue.get("status", ""))).lower() in {"closed", "fixed", "resolved"}


def issue_is_open(issue: dict) -> bool:
    return str(issue.get("state", issue.get("status", ""))).lower() in {"open", "reopened"}


def issue_has_fixed_marker(issue: dict) -> bool:
    return "kitsoki-fixed-in" in issue_marker_text(issue)


def derive_stats(root: Path, issue_state_file: str, similarity_threshold: float, similar_pair_limit: int, stats_output: str) -> dict:
    issue_state = load_issue_state(issue_state_file)
    run_findings: list[tuple[Path, dict]] = []
    if root.exists():
        for path in sorted(root.rglob("findings.json")):
            if "stats" in path.parts:
                continue
            try:
                findings = read_json(path)
            except json.JSONDecodeError as exc:
                raise SystemExit(f"Invalid findings JSON in {path}: {exc}")
            run_findings.append((path.parent, findings))

    credible: list[dict] = []
    filed: list[dict] = []
    fixed: list[dict] = []
    reopened: list[dict] = []
    unknown_state = 0
    for run_dir, findings in run_findings:
        for item in credible_issue_findings(findings):
            entry = dict(item)
            entry["run_dir"] = str(run_dir)
            credible.append(entry)
            github_issue = item.get("github_issue", {}) or {}
            url = github_issue.get("url", "")
            local_ticket_path = local_finding_ref(item).get("path", "")
            if not url and not local_ticket_path:
                continue
            filed.append(entry)
            if not url:
                # Local-artifact-only ticket: filed, but there is no GitHub
                # issue-state to reconcile fixed/reopened against.
                continue
            merged_issue = dict(github_issue)
            if url in issue_state:
                merged_issue.update(issue_state[url])
            if not merged_issue.get("state") and not merged_issue.get("status"):
                unknown_state += 1
            has_marker = issue_has_fixed_marker(merged_issue)
            if has_marker and issue_is_closed(merged_issue):
                fixed.append(entry)
            if has_marker and issue_is_open(merged_issue):
                reopened.append(entry)

    similar_pairs = []
    titled = [
        item for item in credible
        if normalize_issue_title(str(item.get("title", "")))
    ]
    for index, left in enumerate(titled):
        left_title = str(left.get("title", ""))
        left_norm = normalize_issue_title(left_title)
        for right in titled[index + 1:]:
            right_title = str(right.get("title", ""))
            right_norm = normalize_issue_title(right_title)
            score = difflib.SequenceMatcher(None, left_norm, right_norm).ratio()
            if score >= similarity_threshold:
                similar_pairs.append({
                    "score": round(score, 3),
                    "left_title": left_title,
                    "right_title": right_title,
                    "left_issue_url": left.get("github_issue", {}).get("url", ""),
                    "right_issue_url": right.get("github_issue", {}).get("url", ""),
                    "left_run_dir": left.get("run_dir", ""),
                    "right_run_dir": right.get("run_dir", ""),
                })
    similar_pairs.sort(key=lambda item: (-item["score"], item["left_title"], item["right_title"]))
    visible_pairs = similar_pairs if similar_pair_limit < 0 else similar_pairs[:similar_pair_limit]
    stats_summary = (
        f"Derived product-journey stats: {len(credible)} found, {len(filed)} filed, "
        f"{len(fixed)} fixed, {len(reopened)} reopened, {len(similar_pairs)} similar pair(s)."
    )
    result = {
        "status": "stats_derived",
        "stats_root": str(root),
        "stats_output": "",
        "runs_scanned": len(run_findings),
        "run_dirs": [str(run_dir) for run_dir, _findings in run_findings],
        "findings_found_count": len(credible),
        "findings_filed_count": len(filed),
        "issues_fixed_count": len(fixed),
        "issues_reopened_count": len(reopened),
        "issues_unknown_state_count": unknown_state,
        "similar_pair_count": len(similar_pairs),
        "similar_pairs_shown": len(visible_pairs),
        "similar_pairs": visible_pairs,
        "manual_stats_replaced": "yes",
        "stats_summary": stats_summary,
    }
    if stats_output:
        output_path = run_dir_from_arg(stats_output)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        result["stats_output"] = str(output_path)
        write_json(output_path, result)
    return result


def split_csv(value: str) -> list[str]:
    return [item.strip() for item in value.split(",") if item.strip()]


def demo_evidence_path(scenario: str, kind: str) -> str:
    paths = {
        "browser_screenshot": f"screens/{scenario}.png",
        "screenshot_or_tui_png": f"screens/{scenario}.png",
        "rendered_tui_frame": f"screens/{scenario}-tui.png",
        "key_interaction_video": f"media/{scenario}-key-interaction.mp4",
        "session_trace": f"traces/{scenario}.jsonl",
        "trace_reference": f"traces/{scenario}.jsonl",
        "navigation_trace": f"traces/{scenario}-navigation.json",
        "page_url": f"artifacts/{scenario}-page-url.txt",
        "checkpoint_rating": f"artifacts/{scenario}-checkpoint-rating.json",
        "generated_config_diff": f"diffs/{scenario}-config.diff",
        "candidate_diff": f"diffs/{scenario}-candidate.diff",
        "implementation_diff": f"diffs/{scenario}-implementation.diff",
        "onboarding_smoke_result": f"oracle-results/{scenario}-smoke.json",
        "oracle_result": f"oracle-results/{scenario}-oracle.json",
        "full_suite_result": f"oracle-results/{scenario}-full-suite.json",
        "targeted_test_result": f"oracle-results/{scenario}-targeted-tests.json",
        "prd_artifact": f"artifacts/{scenario}-prd.md",
        "design_artifact": f"artifacts/{scenario}-design.md",
        "review_notes": f"artifacts/{scenario}-review-notes.md",
        "review_summary": f"artifacts/{scenario}-review-summary.md",
        "bug_report_markdown": f"bug-reports/{scenario}.md",
        "reproduction_steps": f"bug-reports/{scenario}-repro.md",
    }
    return paths.get(kind, f"artifacts/{scenario}-{kind}.txt")


def add_validation_issue(issues: list[dict], severity: str, check_id: str, message: str, detail: str = "") -> None:
    issues.append({
        "severity": severity,
        "id": check_id,
        "message": message,
        "detail": detail,
    })


def scenario_minimum_evidence(scenario_id: str) -> list[str]:
    return scenario_quality_gate(scenario_id).get("minimum_evidence", [])

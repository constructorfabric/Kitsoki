#!/usr/bin/env python3
"""Extracted from run.py: review module (see tools/product-journey/README.md)."""

import json
import sys
from pathlib import Path
from typing import Optional

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from tools.persona_qa.transports import (
    TRANSPORT_EVIDENCE_CONTRACTS,
    TRANSPORT_PROFILES,
)


from common import (
    compact_transport_profile,
    read_json,
    transport_profile,
)
from emit import (
    final_story_gate_commands,
)
from marathon import (
    add_validation_issue,
    autonomous_fix_report_path,
    autonomous_marathon_watchdog_path,
    build_driver_contract_summary,
    build_next_driver_capture,
    credible_issue_findings,
    gh_agent_job_commit_sha,
    gh_agent_job_commit_url,
    gh_agent_job_evidence_links,
    gh_agent_job_integration_branch,
    github_issue_evidence_assets,
    local_finding_ref,
    next_driver_blocker_command,
    next_driver_capture_route,
    next_driver_capture_slot,
    run_story_summary,
)


def playback_scene_for_item(item: dict) -> Optional[dict]:
    path = item.get("path", "")
    if not path:
        return None
    scenario = item.get("scenario", "")
    evidence_kind = item.get("evidence_kind", "")
    title = f"{scenario} / {evidence_kind}".strip(" /")
    caption = item.get("notes", "") or path
    suffix = Path(path).suffix.lower()
    if item.get("media_kind") == "video":
        scene = {
            "type": "video",
            "mode": "embedded",
            "eyebrow": "Playback evidence",
            "title": title,
            "caption": caption,
            "chapters": "auto",
            "narration": f"Playback evidence for {title}.",
        }
        if suffix == ".json" or path.endswith(".rrweb.json"):
            scene["rrweb"] = path
        else:
            scene["src"] = path
        return scene
    if item.get("media_kind") == "image":
        return {
            "type": "image",
            "eyebrow": "Playback evidence",
            "title": title,
            "src": path,
            "caption": caption,
            "narration": f"Screenshot evidence for {title}.",
        }
    return None


def unattached_driver_evidence_refs(evidence: dict, driver_journal: dict) -> list[str]:
    attached = {
        (item.get("scenario", ""), item.get("path", ""))
        for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"} and item.get("path")
    }
    missing = []
    for event in driver_journal.get("items", []):
        if event.get("status") not in {"captured", "validated"}:
            continue
        scenario = event.get("scenario", "")
        for ref in event.get("evidence_refs", []):
            if ref and (scenario, ref) not in attached:
                missing.append(f"{event.get('id', 'driver-event')}/{scenario}:{ref}")
    return sorted(missing)


def open_weakness_findings(findings: dict) -> list[dict]:
    return [
        item for item in findings.get("items", [])
        if item.get("kind") == "weakness"
        and item.get("origin", "observed") != "seeded"
        and item.get("status", "open") not in {"blocked", "fixed"}
    ]


def summarize_run_bundle(run_dir: Path) -> dict:
    run_json = read_json(run_dir / "run.json")
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {}
    driver_plan = read_json(run_dir / "driver-plan.json") if (run_dir / "driver-plan.json").exists() else {}
    handoff = read_json(run_dir / "driver-handoff.json") if (run_dir / "driver-handoff.json").exists() else {}
    driver_scenarios = []
    for scenario in driver_plan.get("scenarios", []):
        driver_scenarios.append({
            "scenario": scenario.get("scenario", ""),
            "label": scenario.get("label", ""),
            "primary_story": scenario.get("primary_story", ""),
            "task_prompt": scenario.get("task_prompt", ""),
            "harness": scenario.get("harness", ""),
            "visual_surface": scenario.get("visual_surface", ""),
            "resolved_mcp_tools": scenario.get("resolved_mcp_tools", []),
            "driver_actions": scenario.get("driver_actions", []),
            "capture_routes": scenario.get("capture_routes", []),
            "persona_lens": scenario.get("persona_lens", {}),
            "evidence": scenario.get("evidence", []),
            "quality_gate": scenario.get("quality_gate", {}),
            "attach_commands": scenario.get("attach_commands", []),
            "record_finding_command": scenario.get("record_finding_command", ""),
            "record_blocker_command": scenario.get("record_blocker_command", ""),
            "journal_command": scenario.get("journal_command", ""),
            "success_criteria": scenario.get("success_criteria", []),
        })
    final_gates = driver_plan.get("final_gates", [])
    missing_proof_evidence = handoff.get("missing_proof_evidence", [])
    driver_contract_summary = build_driver_contract_summary(driver_plan, handoff)
    return {
        "status": "run_loaded",
        "run_id": run_json["run_id"],
        "run_dir": str(run_dir),
        "project": run_json["project"]["id"],
        "persona": run_json["persona"]["id"],
        "seed": run_json.get("seed", ""),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "driver_plan_path": str(run_dir / "driver-plan.md"),
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "agent_brief_path": str(run_dir / "agent-brief.md"),
        "driver_handoff_path": str(run_dir / "driver-handoff.md"),
        "media_manifest_path": str(run_dir / "media-manifest.json"),
        "scenario_outcomes_path": str(run_dir / "scenario-outcomes.md"),
        "review_status": review.get("status", ""),
        "review_summary": review.get("summary", ""),
        "driver_scenarios": driver_scenarios,
        "driver_final_gates": final_gates,
        "missing_proof_evidence": missing_proof_evidence,
        "driver_contract_summary": driver_contract_summary,
        "next_driver_capture": build_next_driver_capture(handoff),
        "next_driver_capture_route": next_driver_capture_route(handoff),
        "next_driver_attach_command": next_driver_capture_slot(handoff).get("attach_command", ""),
        "next_driver_blocker_command": next_driver_blocker_command(handoff),
        "suggested_prompt": handoff.get("suggested_prompt", ""),
    } | run_story_summary(run_dir)


def credible_findings_requiring_github(findings: dict) -> list[dict]:
    """Credible issue findings not resolved via the local-artifact ticket
    sink. Scopes the GitHub-only autonomous-fix gate chain (gh-agent drain,
    fix/triage/verify evidence, run URLs, integration landing, issue
    close-out, autonomous-fix report) so a campaign run that only used the
    default local finding sink is never forced through GitHub filing/fixing
    it never requested. Findings already filed to GitHub stay in this set so
    their fix chain is still enforced."""
    return [
        item for item in credible_issue_findings(findings)
        if not local_finding_ref(item).get("path")
    ]


def filed_issue_evidence_links(findings: dict) -> list[str]:
    links: list[str] = []
    seen: set[str] = set()
    items = findings.get("items", []) if isinstance(findings, dict) else []
    for item in items:
        if not isinstance(item, dict):
            continue
        issue = item.get("github_issue", {}) if isinstance(item.get("github_issue", {}), dict) else {}
        if not str(issue.get("url", "")).strip():
            continue
        for asset in github_issue_evidence_assets(issue):
            url = asset["url"]
            if url not in seen:
                seen.add(url)
                links.append(url)
    return links


def gh_agent_missing_integration_landing(gh_agent: dict) -> list[str]:
    missing: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        label = str(job.get("origin_ref") or job.get("job_id") or "unknown")
        branch = gh_agent_job_integration_branch(job)
        commit = gh_agent_job_commit_sha(job)
        gaps = []
        if not branch:
            gaps.append("branch")
        elif not branch.startswith("integration/"):
            gaps.append(f"branch={branch}")
        if not commit:
            gaps.append("commit")
        if gaps:
            missing.append(f"{label} ({', '.join(gaps)})")
    return missing


def gh_agent_integration_landing_lines(gh_agent: dict) -> list[str]:
    lines: list[str] = []
    for job in gh_agent.get("drained_jobs", []) or []:
        if not isinstance(job, dict) or job.get("state") != "done":
            continue
        branch = gh_agent_job_integration_branch(job)
        commit = gh_agent_job_commit_sha(job)
        if not branch or not commit:
            continue
        line = f"{branch}@{commit}"
        commit_url = gh_agent_job_commit_url(job)
        if commit_url:
            line += f" ({commit_url})"
        lines.append(line)
    return lines


def missing_autonomous_fix_report_tokens(run_dir: Path, findings: dict) -> list[str]:
    path = autonomous_fix_report_path(run_dir)
    if not path.exists():
        return ["autonomous-fix-report.md"]
    text = path.read_text(encoding="utf-8")
    gh_agent = findings.get("gh_agent", {}) if isinstance(findings.get("gh_agent", {}), dict) else {}
    issue_closeout = findings.get("issue_closeout", {}) if isinstance(findings.get("issue_closeout", {}), dict) else {}
    expected = [
        item.get("github_issue", {}).get("url", "")
        for item in findings.get("items", [])
        if isinstance(item, dict) and item.get("github_issue", {}).get("url")
    ]
    expected.extend(filed_issue_evidence_links(findings))
    expected.extend(
        job.get("run_url", "")
        for job in gh_agent.get("drained_jobs", [])
        if isinstance(job, dict) and job.get("run_url")
    )
    for job in gh_agent.get("drained_jobs", []):
        if not isinstance(job, dict):
            continue
        expected.extend([
            gh_agent_job_integration_branch(job),
            gh_agent_job_commit_sha(job),
            gh_agent_job_commit_url(job),
        ])
    expected.extend(
        link
        for job in gh_agent.get("drained_jobs", [])
        if isinstance(job, dict)
        for link in gh_agent_job_evidence_links(job)
    )
    expected.extend(
        item.get("comment_url", "")
        for item in gh_agent.get("claims", []) or []
        if isinstance(item, dict) and item.get("comment_url")
    )
    if gh_agent.get("claim_status"):
        expected.append(f"Claims: `{gh_agent.get('claim_status')}`")
    expected.extend(
        item.get("comment_url", "")
        for item in issue_closeout.get("items", []) or []
        if isinstance(item, dict) and item.get("comment_url")
    )
    if issue_closeout.get("status"):
        expected.append(f"Issue close-out: `{issue_closeout.get('status')}`")
    expected.extend([
        "## Autonomous Watchdog",
        "## Hosted GH-agent",
        "Health: `",
        "Readiness: `",
        "/healthz",
        "/api/ready",
    ])
    watchdog_path = autonomous_marathon_watchdog_path(run_dir)
    if watchdog_path.exists():
        watchdog = read_json(watchdog_path)
        if watchdog.get("autonomous_watchdog_status") or watchdog.get("status"):
            expected.append(f"Status: `{watchdog.get('autonomous_watchdog_status', watchdog.get('status'))}`")
        if watchdog.get("autonomous_watchdog_summary"):
            expected.append(str(watchdog.get("autonomous_watchdog_summary")))
        if watchdog.get("autonomous_watchdog_markdown_path"):
            expected.append(str(watchdog.get("autonomous_watchdog_markdown_path")))
    missing = [token for token in expected if token and token not in text]
    if "Status: `(not checked)`" in text:
        missing.append("autonomous_watchdog_status")
    if "Report: (not generated)" in text:
        missing.append("autonomous_watchdog_markdown_path")
    if "Health: `(not checked)`" in text:
        missing.append("gh_agent_health_status")
    if "Readiness: `(not checked)`" in text:
        missing.append("gh_agent_readiness_status")
    if "Health: `pass`" not in text:
        missing.append("gh_agent_health_status=pass")
    if "Readiness: `pass`" not in text:
        missing.append("gh_agent_readiness_status=pass")
    return list(dict.fromkeys(missing))


def issue_closeout_gate(findings: dict, gh_agent_requested: bool, credible_findings: list[dict]) -> tuple[bool, str]:
    if not credible_findings or not gh_agent_requested:
        return True, "issue close-out not required"
    issue_closeout = findings.get("issue_closeout", {}) if isinstance(findings.get("issue_closeout", {}), dict) else {}
    status = str(issue_closeout.get("status", "")).strip()
    count = int(issue_closeout.get("count", 0) or 0)
    errors = issue_closeout.get("errors", []) if isinstance(issue_closeout.get("errors", []), list) else []
    if status != "closed":
        return False, f"status={status or '(missing)'}, count={count}"
    if count < len(credible_findings):
        return False, f"closed={count}/{len(credible_findings)}"
    if errors:
        return False, "; ".join(str(item) for item in errors[:3])
    return True, f"closed={count}/{len(credible_findings)}"


def route_profile_validation_errors(route: dict, context: str, expected_transport: str = "") -> list[str]:
    errors = []
    route_transport = route.get("transport", "")
    if not route_transport:
        errors.append(f"{context}: missing transport")
        return errors
    if route_transport not in TRANSPORT_PROFILES:
        errors.append(f"{context}: unknown transport={route_transport}")
        return errors
    if expected_transport and route_transport != expected_transport:
        errors.append(f"{context}: route transport={route_transport}, expected={expected_transport}")
    expected_profile = compact_transport_profile(transport_profile(route_transport))
    actual_profile = route.get("transport_profile", {})
    if actual_profile != expected_profile:
        errors.append(f"{context}: transport_profile mismatch for {route_transport}")
    expected_surface = expected_profile.get("visual_surface", route_transport)
    if route.get("visual_surface") != expected_surface:
        errors.append(f"{context}: visual_surface={route.get('visual_surface', '')}, expected={expected_surface}")
    expected_contract = TRANSPORT_EVIDENCE_CONTRACTS[route_transport]
    actual_contract = route.get("transport_evidence_contract", {})
    if not actual_contract:
        errors.append(f"{context}: missing transport_evidence_contract")
    elif actual_contract != expected_contract:
        errors.append(f"{context}: transport_evidence_contract mismatch for {route_transport}")
    if route.get("setup_entrypoint", {}).get("preflight") != expected_profile.get("preflight", ""):
        errors.append(f"{context}: preflight does not match transport profile")
    if route.get("recording", {}).get("transport_rule") != expected_profile.get("recording_rule", ""):
        errors.append(f"{context}: recording rule does not match transport profile")
    return errors


def validation_issue_summary(issues: list[dict], limit: int = 4) -> str:
    if not issues:
        return ""
    severity_rank = {"error": 0, "warn": 1}
    ordered = sorted(
        issues,
        key=lambda issue: (
            severity_rank.get(issue.get("severity", ""), 2),
            issue.get("id", ""),
            issue.get("detail", ""),
        ),
    )
    parts = []
    for issue in ordered[:limit]:
        severity = issue.get("severity", "issue")
        check_id = issue.get("id", "unknown")
        detail = issue.get("detail", "")
        if len(detail) > 160:
            detail = f"{detail[:157]}..."
        parts.append(f"{severity}: {check_id} ({detail})" if detail else f"{severity}: {check_id}")
    if len(ordered) > limit:
        parts.append(f"+{len(ordered) - limit} more validation issues")
    return "; ".join(parts)


def validate_required_keys(data: dict, required: list[str], issues: list[dict], check_id: str, label: str) -> None:
    missing = [key for key in required if key not in data]
    if missing:
        add_validation_issue(issues, "error", check_id, f"{label} is missing required keys", ", ".join(missing))


def load_json_for_validation(path: Path, issues: list[dict]) -> dict:
    if not path.exists():
        add_validation_issue(issues, "error", "missing-json", "Required JSON file is missing", path.name)
        return {}
    try:
        return read_json(path)
    except json.JSONDecodeError as exc:
        add_validation_issue(issues, "error", "invalid-json", "JSON file cannot be parsed", f"{path.name}: {exc}")
        return {}


def validate_final_commands(commands: list[str], issues: list[dict], check_id: str, label: str) -> None:
    if not commands:
        add_validation_issue(issues, "error", check_id, f"{label} has no final story-owned autonomous-fix/review/validation commands")
        return
    required = final_story_gate_commands()
    missing = [
        command for command in required
        if command not in commands
    ]
    if missing:
        add_validation_issue(issues, "error", check_id, f"{label} is missing final story-owned autonomous-fix/review/validation commands", ", ".join(missing))
    fallback_tokens = ["--autonomous-fix-loop", "--review-run", "--validate-run"]
    fallback_commands = [
        command for command in commands
        if any(token in command for token in fallback_tokens)
    ]
    if fallback_commands:
        add_validation_issue(
            issues,
            "error",
            check_id,
            f"{label} exposes CLI fallback commands as primary final gates",
            "; ".join(fallback_commands),
        )


def deck_scene_eyebrows(deck: dict) -> set[str]:
    return {
        scene.get("eyebrow", "")
        for scene in deck.get("scenes", [])
        if isinstance(scene, dict)
    }


def validate_slidey_deck_shape(deck: dict, media_manifest: dict, issues: list[dict]) -> None:
    if not deck:
        return
    meta = deck.get("meta", {})
    if not isinstance(meta, dict):
        add_validation_issue(issues, "error", "deck-meta", "deck.slidey.json meta must be an object")
        meta = {}
    for key in ["title", "mode", "phase", "resolution"]:
        if key not in meta:
            add_validation_issue(issues, "error", "deck-meta", "deck.slidey.json meta is missing required keys", key)
    if meta.get("mode") not in {"api", "pitch"}:
        add_validation_issue(issues, "error", "deck-meta-mode", "deck.slidey.json meta.mode must be api or pitch", str(meta.get("mode", "")))
    resolution = meta.get("resolution", {})
    if not isinstance(resolution, dict) or not resolution.get("width") or not resolution.get("height"):
        add_validation_issue(issues, "error", "deck-resolution", "deck.slidey.json meta.resolution must include width and height")

    scenes = deck.get("scenes", [])
    if not isinstance(scenes, list) or not scenes:
        add_validation_issue(issues, "error", "deck-scenes", "deck.slidey.json scenes must be a non-empty list")
        return
    allowed_scene_types = {"title", "narrative", "video", "cards", "quote", "table", "image", "evidence"}
    missing_scene_keys = []
    invalid_scene_types = []
    malformed_media = []
    for index, scene in enumerate(scenes, start=1):
        if not isinstance(scene, dict):
            add_validation_issue(issues, "error", "deck-scene-shape", "deck.slidey.json scenes must be objects", f"scene={index}")
            continue
        if scene.get("type", "") not in allowed_scene_types:
            invalid_scene_types.append(f"{index}:{scene.get('type', '')}")
        if scene.get("type") == "title" and not scene.get("title"):
            missing_scene_keys.append(f"{index}/title")
        if scene.get("type") != "title" and "body" not in scene and "media" not in scene and "video" not in scene and "image" not in scene and "cards" not in scene and "items" not in scene and "rrweb" not in scene and "src" not in scene and "lede" not in scene:
            missing_scene_keys.append(f"{index}/content")
        if "media" in scene:
            if not isinstance(scene.get("media"), list):
                malformed_media.append(f"{index}:media-not-list")
            else:
                for media_index, media in enumerate(scene.get("media", []), start=1):
                    if not isinstance(media, dict):
                        malformed_media.append(f"{index}.{media_index}:media-not-object")
                    elif not media.get("path") or not media.get("media_kind"):
                        malformed_media.append(f"{index}.{media_index}:missing-path-or-kind")
    if invalid_scene_types:
        add_validation_issue(issues, "error", "deck-scene-type", "deck.slidey.json has unsupported scene types", ", ".join(invalid_scene_types))
    if missing_scene_keys:
        add_validation_issue(issues, "error", "deck-scene-required", "deck.slidey.json scenes are missing title or content", ", ".join(missing_scene_keys))
    if malformed_media:
        add_validation_issue(issues, "error", "deck-media-shape", "deck.slidey.json media entries are malformed", ", ".join(malformed_media))

    manifest_playback_paths = {
        item.get("path", "")
        for item in media_manifest.get("items", [])
        if item.get("playback") and item.get("path")
    } if media_manifest else set()
    deck_media_paths = {
        media.get("path", "")
        for scene in scenes
        if isinstance(scene, dict)
        for media in scene.get("media", [])
        if isinstance(media, dict) and media.get("path")
    }
    standalone_playback_paths = {
        scene.get("src") or scene.get("video") or scene.get("image") or scene.get("rrweb") or ""
        for scene in scenes
        if isinstance(scene, dict) and scene.get("eyebrow") == "Playback evidence"
    }
    missing_playback_paths = sorted(manifest_playback_paths - deck_media_paths - standalone_playback_paths)
    if missing_playback_paths:
        add_validation_issue(issues, "error", "deck-playback-coverage", "deck.slidey.json does not reference all playback manifest paths", ", ".join(missing_playback_paths))


def summarize_driver_action_contract(driver_plan: dict, schema: dict) -> dict:
    required_ids = schema["driver_plan"].get("driver_action_ids", [])
    required_keys = schema["driver_plan"].get("driver_action_required", [])
    rows = []
    invalid_rows = []
    for index, scenario in enumerate(driver_plan.get("scenarios", []), start=1):
        scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
        actions = scenario.get("driver_actions", [])
        action_ids = [action.get("id", "") for action in actions]
        missing_keys = []
        journal_recordable = False
        for action in actions:
            action_id = action.get("id", "action")
            for key in required_keys:
                if key not in action:
                    missing_keys.append(f"{action_id}/{key}")
            if action_id == "journal_attempt":
                journal_tools = " ".join(action.get("tools", []))
                journal_recordable = (
                    "story.driver_event" in journal_tools
                    or "--record-driver-event" in journal_tools
                ) and bool(action.get("record", "").strip())
        order_matches = action_ids == required_ids
        valid = order_matches and not missing_keys and journal_recordable
        row = {
            "scenario": scenario_id,
            "action_count": len(actions),
            "expected_action_count": len(required_ids),
            "action_ids": action_ids,
            "expected_action_ids": required_ids,
            "order_matches": order_matches,
            "missing_keys": missing_keys,
            "journal_recordable": journal_recordable,
            "valid": valid,
        }
        rows.append(row)
        if not valid:
            invalid_rows.append(row)
    return {
        "scenario_count": len(rows),
        "valid_scenarios": len(rows) - len(invalid_rows),
        "invalid_scenarios": len(invalid_rows),
        "required_action_ids": required_ids,
        "required_action_keys": required_keys,
        "rows": rows,
    }

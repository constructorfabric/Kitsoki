#!/usr/bin/env python3
"""Product-journey evaluation runner.

This is the first execution entrypoint for the product-journey harness. It is
intentionally deterministic: checks use existing local metadata and manifest
contracts so the runner itself stays cost-free by default.
"""

import argparse
import hashlib
import json
import os
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
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
PERSONAS = ROOT / "tools" / "product-journey" / "personas.json"
SCENARIOS = ROOT / "tools" / "product-journey" / "scenarios.json"
GITHUB_TARGETS = ROOT / "tools" / "product-journey" / "github-targets.json"
SCHEMA = ROOT / "tools" / "product-journey" / "schema.json"
DRIVER_AGENT = ROOT / ".agents" / "agents" / "product-journey-qa-driver.md"
LOG = ROOT / ".context" / "product-journey-runlog.md"
ARTIFACT_ROOT = ROOT / ".artifacts" / "product-journey"
MATRIX_ROOT = ARTIFACT_ROOT / "matrices"
TARGET_PROOF_ROOT = ARTIFACT_ROOT / "target-proofs"
DOGFOOD_ROOT = ARTIFACT_ROOT / "dogfood"
DEFAULT_DECK = ROOT / "docs" / "decks" / "product-journey-eval.slidey.json"
EVIDENCE_SOURCES = {"demo", "retained", "external", "local", "cassette", "unknown"}
PROOF_EVIDENCE_SOURCES = {"retained", "external", "local", "cassette"}
STAGES = [
    "discover_product",
    "follow_tutorial",
    "onboard_project",
    "plan_project_work",
    "fix_bug",
    "file_product_issue",
    "score_and_report",
]


def load_catalog(path: Path):
    return json.loads(path.read_text())


def load_personas(path: Path):
    return json.loads(path.read_text())["personas"]


def load_scenarios(path: Path):
    return json.loads(path.read_text())["scenarios"]


def load_github_targets(path: Path):
    return json.loads(path.read_text())


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


def shell(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=False,
        env=os.environ.copy(),
        text=True,
        capture_output=True,
    )


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
        proof_path = ROOT / proof_path
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


def target_status(project: dict) -> str:
    if project.get("validation_command"):
        return "ready-heavy-check"
    if project.get("run_mode") == "external-benchmark" and project.get("status") == "validated":
        return "cached_validated"
    if project.get("source") == "github-targets" or project.get("run_mode") == "github-matrix":
        return "planned"
    return project.get("status", "planned")


def select_persona(personas: list[dict], persona_id: str, seed: str) -> dict:
    if persona_id:
        for persona in personas:
            if persona["id"] == persona_id:
                return persona
        known = ", ".join(persona["id"] for persona in personas)
        raise SystemExit(f"Unknown persona '{persona_id}'. Known: {known}")
    digest = hashlib.sha256(seed.encode("utf-8")).digest()
    return personas[digest[0] % len(personas)]


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
        planned.append({
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
        })
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
) -> tuple[Path, dict]:
    target = resolve_project(catalog, github_targets, project_id)
    persona = select_persona(personas, persona_id, f"{project_id}:{seed}")
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
        "project": _meta_value(target),
        "persona": persona,
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
    evidence = evidence_plan(run_json)
    media_manifest = build_media_manifest(run_json, evidence)
    execution_plan = build_execution_plan(run_json, evidence)
    driver_plan = build_driver_plan(run_json, evidence, execution_plan)
    agent_brief = build_agent_brief(run_json, evidence, execution_plan)
    findings = {"run_id": run_id, "items": [], "summary": {"strength": 0, "weakness": 0, "issue": 0, "fix": 0}}
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

    write_json(run_dir / "run.json", run_json)
    (run_dir / "journey.md").write_text(journey, encoding="utf-8")
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "bugs.json", bugs)
    write_json(run_dir / "findings.json", findings)
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


def build_matrix_bundle(
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
    persona_mode: str,
) -> tuple[Path, dict]:
    created_at = now_utc()
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
    }
    return hints.get(kind, "Save this evidence artifact and attach it to the run.")


def scenario_quality_gate(scenario_id: str) -> dict:
    gates = {
        "product-discovery": {
            "minimum_evidence": ["browser_screenshot", "page_url", "navigation_trace", "checkpoint_rating", "key_interaction_video"],
            "done_when": "The persona can state what Kitsoki is, who it is for, and one credible next action from visible product-site evidence.",
            "block_if": [
                "The local product site cannot be opened or observed.",
                "Navigation depends on private repo knowledge instead of visible page content.",
                "A key page, demo, or link needed for the next action is unavailable.",
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
        "bugfix": {
            "minimum_evidence": ["session_trace", "candidate_diff", "oracle_result", "full_suite_result", "key_interaction_video"],
            "done_when": "A concrete bug candidate has a reviewable diff plus deterministic oracle/test output or a classified suite failure.",
            "block_if": [
                "No concrete bug/repro can be selected without live authorization.",
                "The bugfix story cannot produce a candidate diff.",
                "No deterministic oracle, targeted test, or classified full-suite result is available.",
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


def add_corpus_issue(issues: list[dict], severity: str, check_id: str, message: str, detail: str = "") -> None:
    issues.append({
        "severity": severity,
        "id": check_id,
        "message": message,
        "detail": detail,
    })


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


def validate_journey_corpus(personas: list[dict], scenarios: list[dict], github_targets: dict) -> dict:
    schema = read_json(SCHEMA)
    issues: list[dict] = []
    targets = github_targets.get("targets", [])
    persona_required = ["id", "label", "description", "surface_preference", "risk_focus"]
    scenario_required = ["id", "label", "stage", "task", "primary_story", "required_mcp", "evidence", "success_criteria"]
    target_required = ["id", "label", "repo", "stack", "license_spdx", "bug_query", "open_bug_floor", "status", "notes"]
    allowed_mcp = {"visual.open", "visual.observe", "visual.act", "session.open", "session.inspect", "render.tui"}
    required_scenarios = {
        "product-discovery",
        "project-onboarding",
        "bugfix",
        "prd-design",
        "feature-implementation",
        "evidence-backed-product-bug",
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

    if len(personas) < 4:
        add_corpus_issue(issues, "warn", "persona-count", "Persona corpus is narrow for natural-use sweeps", f"personas={len(personas)}")
    for persona in personas:
        missing = [key for key in persona_required if key not in persona]
        if missing:
            add_corpus_issue(issues, "error", "persona-required-keys", "Persona is missing required keys", f"{persona.get('id', 'unknown')}: {', '.join(missing)}")
        if not isinstance(persona.get("risk_focus", []), list) or not persona.get("risk_focus"):
            add_corpus_issue(issues, "error", "persona-risk-focus", "Persona must name at least one risk focus", persona.get("id", "unknown"))

    missing_required_scenarios = sorted(required_scenarios - set(scenario_ids))
    if missing_required_scenarios:
        add_corpus_issue(issues, "error", "required-scenarios", "Required natural-use scenarios are missing", ", ".join(missing_required_scenarios))
    for scenario in scenarios:
        scenario_id = scenario.get("id", "unknown")
        missing = [key for key in scenario_required if key not in scenario]
        if missing:
            add_corpus_issue(issues, "error", "scenario-required-keys", "Scenario is missing required keys", f"{scenario_id}: {', '.join(missing)}")
        if scenario.get("stage") not in STAGES:
            add_corpus_issue(issues, "error", "scenario-stage", "Scenario uses an unknown stage", f"{scenario_id}: {scenario.get('stage', '')}")
        unknown_mcp = sorted(set(scenario.get("required_mcp", [])) - allowed_mcp)
        if unknown_mcp:
            add_corpus_issue(issues, "error", "scenario-mcp", "Scenario requires unknown MCP tools", f"{scenario_id}: {', '.join(unknown_mcp)}")
        if not scenario.get("success_criteria"):
            add_corpus_issue(issues, "error", "scenario-success-criteria", "Scenario must have success criteria", scenario_id)
        if not scenario.get("evidence"):
            add_corpus_issue(issues, "error", "scenario-evidence", "Scenario must declare evidence slots", scenario_id)
        unknown_evidence = [
            kind for kind in scenario.get("evidence", [])
            if evidence_capture_hint(kind) == "Save this evidence artifact and attach it to the run."
        ]
        if unknown_evidence:
            add_corpus_issue(issues, "error", "scenario-evidence-kind", "Scenario uses evidence kinds without capture hints", f"{scenario_id}: {', '.join(unknown_evidence)}")
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

    errors = sum(1 for issue in issues if issue["severity"] == "error")
    warnings = sum(1 for issue in issues if issue["severity"] == "warn")
    return {
        "status": "valid" if errors == 0 else "invalid",
        "personas": len(personas),
        "scenarios": len(scenarios),
        "targets": len(targets),
        "errors": errors,
        "warnings": warnings,
        "issues": issues,
    }


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
    }
    prompts = {
        "product-discovery": (
            f"As a {persona_label}, start from the local Kitsoki product site and decide whether it credibly explains how to use Kitsoki on {repo} ({stack}). "
            f"Focus on {risk_focus}. Capture the first confusing claim, missing prerequisite, or clear next action."
        ),
        "project-onboarding": (
            f"Onboard {repo} using Kitsoki's documented project setup path. Confirm the generated project profile names plausible {stack} commands, repo files, and the next story to launch."
        ),
        "bugfix": (
            f"Use the target bug queue for {repo}: {bug_query}. Pick or simulate one concrete bug candidate from that queue, drive the bugfix story, and require deterministic oracle/test evidence before calling the fix credible."
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
    base["task_prompt"] = prompts.get(scenario["id"], scenario["task"])
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


def resolve_mcp_tools(capability: str) -> list[str]:
    mapping = {
        "visual.open": ["mcp__kitsoki__visual_open"],
        "visual.observe": ["mcp__kitsoki__visual_observe"],
        "visual.act": ["mcp__kitsoki__visual_act"],
        "session.open": ["mcp__kitsoki__session_new", "mcp__kitsoki__session_attach"],
        "session.status": ["mcp__kitsoki__session_status"],
        "session.submit": ["mcp__kitsoki__session_submit", "mcp__kitsoki__session_drive"],
        "session.drive": ["mcp__kitsoki__session_drive"],
        "session.inspect": ["mcp__kitsoki__session_inspect", "mcp__kitsoki__session_world", "mcp__kitsoki__session_trace"],
        "session.trace": ["mcp__kitsoki__session_trace"],
        "render.tui": ["mcp__kitsoki__render_tui", "mcp__kitsoki__render_tui_png"],
    }
    return mapping.get(capability, [])


def resolved_mcp_tools(capabilities: list[str]) -> list[str]:
    tools: list[str] = []
    for capability in capabilities:
        for tool in resolve_mcp_tools(capability):
            if tool not in tools:
                tools.append(tool)
    return tools


def driver_actions(scenario: dict, run_json: dict, evidence_items: list[dict]) -> list[dict]:
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
    return [
        {
            "id": "open_surface",
            "goal": "Open or attach the Kitsoki/product surface named by the scenario.",
            "tools": open_tools,
            "resolved_tools": resolved_mcp_tools(open_tools),
            "evidence": [],
            "record": "Record the handle, URL, or reason this surface could not be opened.",
        },
        {
            "id": "read_current_frame",
            "goal": "Observe the exact operator-visible state before acting.",
            "tools": read_tools,
            "resolved_tools": resolved_mcp_tools(read_tools),
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
            "resolved_tools": resolved_mcp_tools(act_tools),
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
            "resolved_tools": resolved_mcp_tools(capture_tools),
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


def media_kind(evidence_kind: str, artifact_path: str) -> str:
    value = f"{evidence_kind} {artifact_path}".lower()
    suffix = Path(artifact_path).suffix.lower()
    if "video" in value or suffix in {".mp4", ".mov", ".webm", ".gif"}:
        return "video"
    if "screenshot" in value or "png" in value or suffix in {".png", ".jpg", ".jpeg", ".webp"}:
        return "image"
    if "trace" in value or suffix in {".jsonl", ".trace"}:
        return "trace"
    if suffix in {".md", ".txt", ".json", ".yaml", ".yml"}:
        return "document"
    return "artifact"


def evidence_source(artifact_path: str, notes: str = "") -> str:
    combined = f"{artifact_path} {notes}".lower()
    if "demo placeholder" in combined or "deterministic placeholder" in combined:
        return "demo"
    if "cassette" in combined or artifact_path.startswith("cassette://") or "/cassettes/" in artifact_path:
        return "cassette"
    if artifact_path.startswith(("retained://", "image://")):
        return "retained"
    if artifact_path.startswith(("http://", "https://")):
        return "external"
    if artifact_path:
        return "local"
    return "unknown"


def normalize_evidence_source(source: str, artifact_path: str, notes: str = "") -> str:
    normalized = source.strip().lower() if source else evidence_source(artifact_path, notes)
    if normalized not in EVIDENCE_SOURCES:
        known = ", ".join(sorted(EVIDENCE_SOURCES))
        raise SystemExit(f"Evidence source must be one of: {known}")
    return normalized


def is_proof_evidence(item: dict) -> bool:
    if item.get("status") not in {"captured", "validated"}:
        return False
    source = item.get("source") or evidence_source(item.get("path", ""), item.get("notes", ""))
    return source in PROOF_EVIDENCE_SOURCES


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
            "product_journey_media": item,
        }
        if suffix == ".json" or path.endswith(".rrweb.json"):
            scene["rrweb"] = path
        else:
            scene["video"] = path
        return scene
    if item.get("media_kind") == "image":
        return {
            "type": "narrative",
            "eyebrow": "Playback evidence",
            "title": title,
            "body": caption,
            "image": path,
            "product_journey_media": item,
            "narration": f"Screenshot evidence for {title}.",
        }
    return None


def playback_deck_scenes(media_manifest: Optional[dict], limit: int = 6) -> list[dict]:
    if media_manifest is None:
        return []
    scenes = []
    for item in media_manifest.get("items", []):
        if not item.get("playback"):
            continue
        scene = playback_scene_for_item(item)
        if scene is not None:
            scenes.append(scene)
        if len(scenes) >= limit:
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


def build_execution_plan(run_json: dict, evidence: dict) -> dict:
    evidence_by_scenario: dict[str, list[dict]] = {}
    for item in evidence.get("items", []):
        evidence_by_scenario.setdefault(item["scenario"], []).append(item)

    run_dir_arg = f".artifacts/product-journey/{run_json['run_id']}"
    steps = []
    for index, scenario in enumerate(run_json["scenarios"], start=1):
        evidence_items = evidence_by_scenario.get(scenario["id"], [])
        attach_commands = [
            "python3 tools/product-journey/run.py --attach-evidence "
            f"--run-dir {run_dir_arg} "
            f"--scenario {scenario['id']} "
            f"--evidence-kind {item['kind']} "
            f"--evidence-path <path-or-retained-id> "
            "--evidence-source <retained|external|local|cassette> "
            f"--notes \"{evidence_capture_hint(item['kind'])}\""
            for item in evidence_items
        ]
        record_blocker_command = (
            "python3 tools/product-journey/run.py --record-blocker "
            f"--run-dir {run_dir_arg} "
            f"--scenario {scenario['id']} "
            "--title <blocker-title> --summary <why-this-scenario-could-not-be-captured> "
            "--evidence-path <trace-or-frame-path>"
        )
        steps.append({
            "order": index,
            "scenario": scenario["id"],
            "label": scenario["label"],
            "stage": scenario["stage"],
            "persona": run_json["persona"]["id"],
            "project": run_json["project"]["id"],
            "task": scenario["task"],
            "task_prompt": scenario.get("task_prompt", scenario["task"]),
            "primary_story": scenario["primary_story"],
            "mcp_steps": [
                {"tool": tool, "instruction": mcp_step(tool)}
                for tool in scenario["required_mcp"]
            ],
            "evidence": [
                {
                    "kind": item["kind"],
                    "status": item.get("status", "missing"),
                    "path": item.get("path", ""),
                    "capture_hint": evidence_capture_hint(item["kind"]),
                }
                for item in evidence_items
            ],
            "success_criteria": scenario["success_criteria"],
            "quality_gate": scenario_quality_gate(scenario["id"]),
            "attach_commands": attach_commands,
            "record_blocker_command": record_blocker_command,
        })

    return {
        "run_id": run_json["run_id"],
        "project": run_json["project"],
        "persona": run_json["persona"],
        "created_at": now_utc(),
        "summary": {
            "scenario_count": len(steps),
            "evidence_count": sum(len(step["evidence"]) for step in steps),
        },
        "steps": steps,
        "finalize_commands": [
            f"python3 tools/product-journey/run.py --record-finding --run-dir {run_dir_arg} --finding-kind <strength|weakness|issue|fix> --title <title> --summary <summary>",
            f"python3 tools/product-journey/run.py --record-blocker --run-dir {run_dir_arg} --scenario <scenario> --title <title> --summary <summary>",
            f"python3 tools/product-journey/run.py --review-run --run-dir {run_dir_arg}",
            f"python3 tools/product-journey/run.py --validate-run --run-dir {run_dir_arg}",
        ],
    }


def build_agent_brief(run_json: dict, evidence: dict, execution_plan: dict) -> dict:
    persona = run_json["persona"]
    lens = persona_lens(persona)
    missing_evidence = [
        {"scenario": item["scenario"], "kind": item["kind"], "hint": evidence_capture_hint(item["kind"])}
        for item in evidence.get("items", [])
        if item.get("status") == "missing"
    ]
    return {
        "run_id": run_json["run_id"],
        "project": run_json["project"],
        "persona": persona,
        "mission": (
            "Drive the product journey as this persona using Kitsoki MCP and visual MCP. "
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
            "Attach every useful artifact with tools/product-journey/run.py --attach-evidence, then run --review-run and --validate-run.",
        ],
        "scenario_order": [
            {
                "id": step["scenario"],
                "label": step["label"],
                "task": step["task"],
                "task_prompt": step.get("task_prompt", step["task"]),
                "primary_story": step["primary_story"],
                "mcp_tools": [mcp["tool"] for mcp in step["mcp_steps"]],
                "success_criteria": step["success_criteria"],
                "evidence": [item["kind"] for item in step["evidence"]],
                "quality_gate": step.get("quality_gate", scenario_quality_gate(step["scenario"])),
            }
            for step in execution_plan.get("steps", [])
        ],
        "missing_evidence": missing_evidence,
        "finalize_commands": execution_plan.get("finalize_commands", []),
    }


def build_driver_plan(run_json: dict, evidence: dict, execution_plan: dict) -> dict:
    lens = persona_lens(run_json["persona"])
    evidence_by_scenario: dict[str, list[dict]] = {}
    for item in evidence.get("items", []):
        evidence_by_scenario.setdefault(item["scenario"], []).append(item)
    steps_by_scenario = {
        step["scenario"]: step
        for step in execution_plan.get("steps", [])
    }
    run_dir_arg = f".artifacts/product-journey/{run_json['run_id']}"
    scenarios = []
    for scenario in run_json["scenarios"]:
        scenario_id = scenario["id"]
        required_mcp = scenario.get("required_mcp", [])
        evidence_items = evidence_by_scenario.get(scenario_id, [])
        step = steps_by_scenario.get(scenario_id, {})
        scenarios.append({
            "scenario": scenario_id,
            "label": scenario["label"],
            "stage": scenario["stage"],
            "primary_story": scenario["primary_story"],
            "task_prompt": scenario.get("task_prompt", scenario["task"]),
            "evidence_dir": scenario.get("evidence_dir", f"evidence/{run_json['project']['id']}--{run_json['persona']['id']}/{scenario_id}"),
            "harness": driver_harness(scenario["primary_story"]),
            "visual_surface": driver_visual_surface(scenario["primary_story"], required_mcp),
            "required_mcp": required_mcp,
            "resolved_mcp_tools": resolved_mcp_tools(required_mcp),
            "action_sequence": driver_action_sequence(required_mcp),
            "driver_actions": driver_actions(scenario, run_json, evidence_items),
            "persona_prompts": [
                f"Act as {run_json['persona']['label']}: {run_json['persona']['description']}",
                f"Risk focus: {', '.join(run_json['persona'].get('risk_focus', []))}",
                f"Start from: {lens['starting_surface']}",
                f"First skepticism check: {lens['first_question']}",
                f"Escalate when: {lens['escalation_trigger']}",
                f"Evidence emphasis: {lens['evidence_emphasis']}",
                "Use natural operator phrasing where route quality or prompt quality is under test.",
            ],
            "persona_lens": lens,
            "evidence": [
                {
                    "kind": item["kind"],
                    "status": item.get("status", "missing"),
                    "path": item.get("path", ""),
                    "capture_hint": evidence_capture_hint(item["kind"]),
                    "playback_candidate": media_kind(item["kind"], item.get("path", "")) in {"video", "image"} or item["kind"] in {"browser_screenshot", "key_interaction_video", "screenshot_or_tui_png"},
                }
                for item in evidence_items
            ],
            "success_criteria": scenario["success_criteria"],
            "quality_gate": scenario_quality_gate(scenario_id),
            "attach_commands": step.get("attach_commands", []),
            "record_finding_command": (
                "python3 tools/product-journey/run.py --record-finding "
                f"--run-dir {run_dir_arg} "
                "--finding-kind <strength|weakness|issue|fix> "
                f"--scenario {scenario_id} "
                "--title <title> --summary <summary> --evidence-path <path-or-retained-id>"
            ),
            "record_blocker_command": step.get("record_blocker_command", (
                "python3 tools/product-journey/run.py --record-blocker "
                f"--run-dir {run_dir_arg} "
                f"--scenario {scenario_id} "
                "--title <blocker-title> --summary <why-this-scenario-could-not-be-captured> "
                "--evidence-path <trace-or-frame-path>"
            )),
            "journal_command": (
                "python3 tools/product-journey/run.py --record-driver-event "
                f"--run-dir {run_dir_arg} "
                f"--scenario {scenario_id} "
                "--dispatch-mode <replay|record|live> "
                "--driver-status <attempted|captured|blocked|validated> "
                "--mcp-tools <comma-separated-tools-used> "
                "--evidence-refs <comma-separated-paths-or-retained-ids> "
                "--blockers <comma-separated-blockers-if-any> "
                "--summary <what-the-driver-actually-tried>"
            ),
        })
    return {
        "run_id": run_json["run_id"],
        "driver_agent": ".agents/agents/product-journey-qa-driver.md",
        "project": run_json["project"],
        "persona": run_json["persona"],
        "scenarios": scenarios,
        "final_gates": [
            f"python3 tools/product-journey/run.py --review-run --run-dir {run_dir_arg}",
            f"python3 tools/product-journey/run.py --validate-run --run-dir {run_dir_arg}",
        ],
    }


def persona_lens(persona: dict) -> dict:
    lenses = {
        "core-maintainer": {
            "starting_surface": "terminal-first; prefer TUI/session state before browser surfaces",
            "first_question": "Will this produce a minimal, reviewable diff that follows the repository's style?",
            "evidence_emphasis": "candidate diff, targeted tests, full-suite classification, and trace events for unexpected routing",
            "escalation_trigger": "generated churn, hidden broad scope, missing deterministic test proof, or unclear ownership boundary",
            "finding_bias": "Prefer reviewability, bisectability, and least-surprise findings over cosmetic notes.",
        },
        "dependency-debugger": {
            "starting_surface": "web-first; begin from public issue/repro context and then enter the story surface",
            "first_question": "How quickly can I reproduce the dependency bug and decide whether Kitsoki's fix is trustworthy?",
            "evidence_emphasis": "reproduction steps, oracle output, key interaction video, and handoff artifacts",
            "escalation_trigger": "unclear repro setup, missing oracle, ambiguous pass/fail state, or no handoff artifact for my app",
            "finding_bias": "Favor time-to-repro, confidence, and downstream handoff clarity.",
        },
        "docs-minded-contributor": {
            "starting_surface": "docs-first; follow the documented path before trying hidden commands",
            "first_question": "Can I follow the docs without private repo context or tribal knowledge?",
            "evidence_emphasis": "page URLs, screenshots, prerequisite commands, stale-link proof, and onboarding smoke results",
            "escalation_trigger": "stale docs, missing prerequisites, broken media, or commands that require unexplained setup",
            "finding_bias": "Prefer onboarding clarity, documented next actions, and confusing-copy findings.",
        },
        "ide-first-engineer": {
            "starting_surface": "visual-first; use web, TUI PNG, or editor-like surfaces before terminal archaeology",
            "first_question": "Can I understand current state and next action from the visible UI?",
            "evidence_emphasis": "visual frames, retained image IDs, operator-question state, navigation traces, and key interaction video",
            "escalation_trigger": "state that is only visible in logs, silent operator defaults, confusing navigation, or unreadable layout",
            "finding_bias": "Favor visible-state, affordance, and navigation findings.",
        },
        "hobbyist-contributor": {
            "starting_surface": "docs-and-web-first; avoid repo archaeology until the product gives a concrete next step",
            "first_question": "Can I make meaningful progress in a short spare-time session without knowing the repository's internal conventions?",
            "evidence_emphasis": "setup commands, first-success proof, small issue selection, key interaction video, and explicit stop points",
            "escalation_trigger": "unclear prerequisites, long-running setup, ambiguous next action, or work that expands beyond a small contribution",
            "finding_bias": "Favor time-budget, setup-friction, and beginner-safe next-step findings.",
        },
    }
    default = {
        "starting_surface": persona.get("surface_preference", "surface chosen by scenario"),
        "first_question": f"What would a {persona.get('label', 'reviewer')} naturally try first, and what evidence proves the result?",
        "evidence_emphasis": ", ".join(persona.get("risk_focus", [])) or "scenario minimum evidence",
        "escalation_trigger": "the scenario cannot produce proof evidence or a clear blocker",
        "finding_bias": "Tie findings to the persona risk focus and scenario success criteria.",
    }
    return lenses.get(persona.get("id", ""), default)


def render_driver_plan(plan: dict) -> str:
    lines = [
        "# Product journey driver plan",
        "",
        f"- Run: `{plan['run_id']}`",
        f"- Driver: `{plan['driver_agent']}`",
        f"- Project: `{plan['project']['label']}`",
        f"- Persona: `{plan['persona']['label']}`",
        "",
    ]
    for index, scenario in enumerate(plan["scenarios"], start=1):
        lines.extend([
            f"## {index}. {scenario['label']}",
            "",
            f"- Scenario: `{scenario['scenario']}`",
            f"- Story: `{scenario['primary_story']}`",
            f"- Harness: `{scenario['harness']}`",
            f"- Visual surface: `{scenario['visual_surface']}`",
            f"- MCP: {', '.join(scenario['required_mcp'])}",
            f"- MCP tools: {', '.join(scenario.get('resolved_mcp_tools', [])) or '(none)'}",
            f"- Evidence dir: `{scenario['evidence_dir']}`",
            "",
            scenario["task_prompt"],
            "",
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
    lines = [
        "# Product journey QA agent brief",
        "",
        f"- Run: `{brief['run_id']}`",
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
    lines.extend(["", "## Scenario Order", ""])
    for index, scenario in enumerate(brief["scenario_order"], start=1):
        lines.extend([
            f"### {index}. {scenario['label']}",
            "",
            f"- Scenario: `{scenario['id']}`",
            f"- Story: `{scenario['primary_story']}`",
            f"- MCP tools: {', '.join(scenario['mcp_tools'])}",
            f"- Evidence: {', '.join(scenario['evidence'])}",
            "",
            scenario.get("task_prompt", scenario["task"]),
            "",
            "Success criteria:",
        ])
        for criterion in scenario["success_criteria"]:
            lines.append(f"- {criterion}")
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
    run_dir_arg = f".artifacts/product-journey/{run_json.get('run_id', '<run-id>')}"
    for scenario in run_json.get("scenarios", []):
        scenario_id = scenario.get("id", "")
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
                    "python3 tools/product-journey/run.py --record-blocker "
                    f"--run-dir {run_dir_arg} "
                    f"--scenario {scenario_id} "
                    "--title <blocker-title> "
                    "--summary <why-this-scenario-could-not-be-captured> "
                    "--evidence-path <trace-or-frame-path>"
                ),
                "slots": [
                    {
                        "kind": kind,
                        "capture_hint": evidence_capture_hint(kind),
                        "attach_command": (
                            "python3 tools/product-journey/run.py --attach-evidence "
                            f"--run-dir {run_dir_arg} "
                            f"--scenario {scenario_id} "
                            f"--evidence-kind {kind} "
                            "--evidence-path <path-or-retained-id> "
                            "--evidence-source <retained|external|local|cassette> "
                            f"--notes \"{evidence_capture_hint(kind)}\""
                        ),
                    }
                    for kind in missing
                ],
            })
    return rows


def build_driver_handoff(run_json: dict, metrics: dict, evidence: dict, review: dict) -> dict:
    run_dir_arg = f".artifacts/product-journey/{run_json['run_id']}"
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
            "`last_result.next_driver_capture`, `last_result.next_driver_attach_command`, "
            "`last_result.next_driver_blocker_command`, `last_result.missing_proof_evidence`, "
            "and `last_result.driver_final_gates`. "
            "Use `last_result.next_driver_attach_command` for the first proof attach when present, "
            "or `last_result.next_driver_blocker_command` when the slot is attempted but blocked, "
            "then use Kitsoki Studio MCP and visual MCP to capture proof-source evidence or blockers, "
            "record findings, then run review and validation."
        ),
        "finalize_commands": [
            f"python3 tools/product-journey/run.py --review-run --run-dir {run_dir_arg}",
            f"python3 tools/product-journey/run.py --validate-run --run-dir {run_dir_arg}",
        ],
        "missing_evidence": missing_evidence,
        "missing_proof_evidence": missing_proof_evidence,
    }


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
        "",
    ]
    for step in plan["steps"]:
        lines.extend([
            f"## {step['order']}. {step['label']}",
            "",
            f"- Scenario: `{step['scenario']}`",
            f"- Story: `{step['primary_story']}`",
            f"- Stage: `{step['stage']}`",
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


def write_json(path: Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def run_dir_from_arg(value: str) -> Path:
    path = Path(value)
    if not path.is_absolute():
        path = ROOT / path
    return path


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


def artifact_ref_exists(run_dir: Path, path: str) -> bool:
    value = path.strip()
    if not value or is_external_artifact_ref(value):
        return True
    candidate = Path(value)
    if candidate.is_absolute():
        return candidate.exists()
    return (run_dir / candidate).exists() or (ROOT / candidate).exists()


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


def update_derived_artifacts(run_dir: Path, publish_deck: Optional[Path] = None) -> None:
    run_json = read_json(run_dir / "run.json")
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
    proof_items = [item for item in present_items if is_proof_evidence(item)]
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
    scenario_outcomes = build_scenario_outcomes(run_json, evidence, findings)
    write_json(run_dir / "scenario-outcomes.json", scenario_outcomes)
    (run_dir / "scenario-outcomes.md").write_text(render_scenario_outcomes(scenario_outcomes), encoding="utf-8")
    write_json(run_dir / "review.json", review)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "scenarios.json", {"run_id": run_json["run_id"], "items": run_json["scenarios"]})
    execution_plan = build_execution_plan(run_json, evidence)
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    driver_plan = build_driver_plan(run_json, evidence, execution_plan)
    write_json(run_dir / "driver-plan.json", driver_plan)
    (run_dir / "driver-plan.md").write_text(render_driver_plan(driver_plan), encoding="utf-8")
    driver_journal = build_driver_journal(run_json["run_id"], driver_journal.get("items", []))
    write_json(run_dir / "driver-journal.json", driver_journal)
    (run_dir / "driver-journal.md").write_text(render_driver_journal(driver_journal), encoding="utf-8")
    agent_brief = build_agent_brief(run_json, evidence, execution_plan)
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


def build_driver_contract_summary(driver_plan: dict, handoff: dict) -> str:
    driver_scenarios = driver_plan.get("scenarios", [])
    final_gates = driver_plan.get("final_gates", [])
    missing_proof_evidence = handoff.get("missing_proof_evidence", [])
    scenario_ids = ", ".join(
        scenario.get("scenario", "")
        for scenario in driver_scenarios[:5]
        if scenario.get("scenario", "")
    )
    if len(driver_scenarios) > 5:
        scenario_ids = f"{scenario_ids}, +{len(driver_scenarios) - 5} more"
    return (
        f"Driver contract: {len(driver_scenarios)} scenarios"
        f"{f' ({scenario_ids})' if scenario_ids else ''}; "
        f"{len(missing_proof_evidence)} missing-proof rows; "
        f"{len(final_gates)} final gates. Inspect last_result.driver_scenarios, "
        "last_result.missing_proof_evidence, and last_result.driver_final_gates."
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
        if kind:
            return f"Next capture: {scenario}/{kind}. {hint}".strip()
    return ""


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
        "next_driver_attach_command": next_driver_capture_slot(handoff).get("attach_command", ""),
        "next_driver_blocker_command": next_driver_blocker_command(handoff),
        "suggested_prompt": handoff.get("suggested_prompt", ""),
    } | run_story_summary(run_dir)


def run_story_summary(run_dir: Path) -> dict:
    metrics = read_json(run_dir / "metrics.json") if (run_dir / "metrics.json").exists() else {}
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"summary": {}}
    handoff = read_json(run_dir / "driver-handoff.json") if (run_dir / "driver-handoff.json").exists() else {}
    driver_plan = read_json(run_dir / "driver-plan.json") if (run_dir / "driver-plan.json").exists() else {}
    agent_brief = read_json(run_dir / "agent-brief.json") if (run_dir / "agent-brief.json").exists() else {}
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {}
    finding_summary = findings.get("summary", {})
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
    return {
        "persona_starting_surface": lens.get("starting_surface", ""),
        "persona_first_question": lens.get("first_question", ""),
        "persona_evidence_emphasis": lens.get("evidence_emphasis", ""),
        "persona_escalation_trigger": lens.get("escalation_trigger", ""),
        "persona_finding_bias": lens.get("finding_bias", ""),
        "proof_evidence_count": metrics.get("proof_evidence_count", 0),
        "demo_evidence_count": metrics.get("demo_evidence_count", 0),
        "finding_total_count": sum(finding_summary.get(kind, 0) for kind in ["strength", "weakness", "issue", "fix"]),
        "strength_count": finding_summary.get("strength", metrics.get("strength_count", 0)),
        "weakness_count": finding_summary.get("weakness", metrics.get("weakness_count", 0)),
        "issue_count": finding_summary.get("issue", metrics.get("issue_count", 0)),
        "fix_count": finding_summary.get("fix", metrics.get("fix_count", 0)),
        "blocked_count": finding_summary.get("blocked", metrics.get("blocked_count", 0)),
        "missing_evidence_count": metrics.get("missing_evidence_count", handoff.get("status", {}).get("missing_evidence_count", 0)),
        "missing_proof_evidence_count": handoff.get("status", {}).get("missing_proof_evidence_count", 0),
        "proof_minimum_evidence_count": handoff.get("status", {}).get("proof_minimum_evidence_count", 0),
        "minimum_evidence_count": handoff.get("status", {}).get("minimum_evidence_count", 0),
        "missing_proof_summary": "; ".join(missing_proof_summary),
        "driver_contract_summary": build_driver_contract_summary(driver_plan, handoff) if driver_plan else "",
        "next_driver_capture": build_next_driver_capture(handoff),
        "next_driver_attach_command": next_driver_capture_slot(handoff).get("attach_command", ""),
        "next_driver_blocker_command": next_driver_blocker_command(handoff),
        "review_passed_count": review.get("summary_counts", {}).get("passed", 0),
        "review_failed_count": review.get("summary_counts", {}).get("failed", 0),
        "review_warning_count": review.get("summary_counts", {}).get("warned", 0),
        "review_total_count": review.get("summary_counts", {}).get("total", 0),
        "review_backlog_summary": "; ".join(review_backlog),
    }


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
) -> None:
    if kind not in {"strength", "weakness", "issue", "fix"}:
        raise SystemExit("Finding kind must be strength, weakness, issue, or fix")
    if status not in {"open", "fixed", "observed", "validated", "blocked"}:
        raise SystemExit("Finding status must be open, fixed, observed, validated, or blocked")
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


def split_csv(value: str) -> list[str]:
    return [item.strip() for item in value.split(",") if item.strip()]


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
        record_finding(run_dir, kind, title, summary, scenario, severity, evidence_path, status, publish_deck=None)
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
        "review.json",
        "deck.slidey.json",
    ]
    evidence_items = evidence.get("items", [])
    for item in evidence_items:
        item["source"] = normalize_evidence_source(item.get("source", ""), item.get("path", ""), item.get("notes", ""))
    media_manifest = build_media_manifest(run_json, evidence)
    present_items = [item for item in evidence_items if item.get("status") in {"captured", "validated"}]
    demo_items = [item for item in present_items if item.get("source") == "demo"]
    proof_items = [item for item in present_items if is_proof_evidence(item)]
    rejected_items = [item for item in evidence_items if item.get("status") == "rejected"]
    video_items = [item for item in media_manifest["items"] if item["media_kind"] == "video"]
    playback_items = [item for item in media_manifest["items"] if item["playback"]]
    missing_playback_refs = missing_local_artifact_refs(run_dir, playback_items)
    finding_items = findings.get("items", [])
    finding_kinds = {item.get("kind") for item in finding_items}
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
    missing_driver_journal = [
        scenario.get("id", "")
        for scenario in run_json.get("scenarios", [])
        if scenario.get("id", "") not in journaled_scenarios
        and scenario.get("id", "") not in blocked_scenarios
    ]
    missing_driver_evidence_refs = unattached_driver_evidence_refs(evidence, driver_journal)
    scenario_outcomes = build_scenario_outcomes(run_json, evidence, findings)
    driver_plan = build_driver_plan(run_json, evidence, execution_plan)
    quality_gates = summarize_quality_gates(evidence, scenario_outcomes, driver_plan)
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


def add_validation_issue(issues: list[dict], severity: str, check_id: str, message: str, detail: str = "") -> None:
    issues.append({
        "severity": severity,
        "id": check_id,
        "message": message,
        "detail": detail,
    })


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
        add_validation_issue(issues, "error", check_id, f"{label} has no final review/validation commands")
        return
    required = ["--review-run", "--validate-run"]
    missing = [
        token for token in required
        if not any(token in command for command in commands)
    ]
    if missing:
        add_validation_issue(issues, "error", check_id, f"{label} is missing final review/validation commands", ", ".join(missing))


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
    resolution = meta.get("resolution", {})
    if not isinstance(resolution, dict) or not resolution.get("width") or not resolution.get("height"):
        add_validation_issue(issues, "error", "deck-resolution", "deck.slidey.json meta.resolution must include width and height")

    scenes = deck.get("scenes", [])
    if not isinstance(scenes, list) or not scenes:
        add_validation_issue(issues, "error", "deck-scenes", "deck.slidey.json scenes must be a non-empty list")
        return
    allowed_scene_types = {"title", "narrative", "video", "cards", "quote", "table"}
    missing_scene_keys = []
    invalid_scene_types = []
    malformed_media = []
    for index, scene in enumerate(scenes, start=1):
        if not isinstance(scene, dict):
            add_validation_issue(issues, "error", "deck-scene-shape", "deck.slidey.json scenes must be objects", f"scene={index}")
            continue
        if scene.get("type", "") not in allowed_scene_types:
            invalid_scene_types.append(f"{index}:{scene.get('type', '')}")
        if not scene.get("title"):
            missing_scene_keys.append(f"{index}/title")
        if scene.get("type") != "title" and "body" not in scene and "media" not in scene and "video" not in scene and "image" not in scene and "cards" not in scene:
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
        scene.get("video") or scene.get("image") or scene.get("rrweb") or ""
        for scene in scenes
        if isinstance(scene, dict) and scene.get("eyebrow") == "Playback evidence"
    }
    missing_playback_paths = sorted(manifest_playback_paths - deck_media_paths - standalone_playback_paths)
    if missing_playback_paths:
        add_validation_issue(issues, "error", "deck-playback-coverage", "deck.slidey.json does not reference all playback manifest paths", ", ".join(missing_playback_paths))


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
    if execution_plan and len(execution_steps) != len(scenarios):
        add_validation_issue(
            issues,
            "error",
            "execution-plan-count",
            "execution-plan.json step count does not match run.json scenarios",
            f"steps={len(execution_steps)}, scenarios={len(scenarios)}",
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
        for index, step in enumerate(execution_steps, start=1):
            scenario_id = step.get("scenario", f"step-{index}")
            evidence_kinds = [item.get("kind", "") for item in step.get("evidence", []) if item.get("kind", "")]
            commands = step.get("attach_commands", [])
            if len(commands) != len(evidence_kinds):
                stale_attach_commands.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(commands)}"
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
        if stale_attach_commands:
            add_validation_issue(issues, "error", "execution-plan-attach-commands", "execution-plan.json attach commands do not cover the evidence contract", "; ".join(stale_attach_commands))
    if driver_plan and len(driver_scenarios) != len(scenarios):
        add_validation_issue(
            issues,
            "error",
            "driver-plan-count",
            "driver-plan.json scenario count does not match run.json scenarios",
            f"scenarios={len(driver_scenarios)}, run.json={len(scenarios)}",
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
        declared_by_scenario = {
            scenario.get("id", ""): set(scenario.get("evidence", []))
            for scenario in scenarios
        }
        for scenario in driver_scenarios:
            scenario_id = scenario.get("scenario", "")
            declared = declared_by_scenario.get(scenario_id, set())
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
        for index, scenario in enumerate(driver_scenarios, start=1):
            scenario_id = scenario.get("scenario", f"driver-scenario-{index}")
            evidence_kinds = [item.get("kind", "") for item in scenario.get("evidence", []) if item.get("kind", "")]
            commands = scenario.get("attach_commands", [])
            if len(commands) != len(evidence_kinds):
                stale_driver_attach_commands.append(
                    f"{scenario_id}: expected={len(evidence_kinds)}, actual={len(commands)}"
                )
            for evidence_kind in evidence_kinds:
                if not any(f"--scenario {scenario_id}" in command and f"--evidence-kind {evidence_kind}" in command for command in commands):
                    stale_driver_attach_commands.append(f"{scenario_id}/{evidence_kind}: missing command")
        if stale_driver_attach_commands:
            add_validation_issue(issues, "error", "driver-plan-attach-commands", "driver-plan.json attach commands do not cover the scenario evidence slots", "; ".join(stale_driver_attach_commands))
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
    if agent_brief and len(brief_scenarios) != len(scenarios):
        add_validation_issue(
            issues,
            "error",
            "agent-brief-count",
            "agent-brief.json scenario order count does not match run.json scenarios",
            f"scenario_order={len(brief_scenarios)}, scenarios={len(scenarios)}",
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

    validate_slidey_deck_shape(deck, media_manifest, issues)
    scene_eyebrows = deck_scene_eyebrows(deck)
    for expected in ["Persona lens", "Driver plan", "Driver contract", "Video playback", "Scenario outcomes", "Finding matrix", "Proof gates"]:
        if deck and expected not in scene_eyebrows:
            add_validation_issue(issues, "error", "deck-scene", "deck.slidey.json is missing a required review scene", expected)
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


def render_matrix_summary(matrix: dict) -> str:
    proof = matrix.get("target_proof", {})
    proof_summary = proof.get("summary", {})
    strict_ready = bool(proof) and proof_summary.get("failed", 0) == 0 and proof_summary.get("errors", 0) == 0
    lines = [
        "# Product journey GitHub matrix",
        "",
        f"- Matrix: `{matrix['matrix_id']}`",
        f"- Seed: `{matrix['seed']}`",
        f"- Targets: {matrix['target_count']}",
        f"- Assignments: {matrix['assignment_count']}",
        f"- Scenarios per assignment: {matrix['scenario_count']}",
        "",
        "## Selection Contract",
        "",
        f"- Host: {matrix['selection_contract']['host']}",
        f"- License: {matrix['selection_contract'].get('license', 'not set')}",
        f"- Open bug floor: {matrix['selection_contract']['open_bug_floor']}",
        f"- Stargazer floor: {matrix['selection_contract'].get('stargazer_floor', 'not set')}",
        f"- Refresh: {matrix['selection_contract']['refresh_note']}",
        f"- Target proof: {proof.get('proof_id', 'not refreshed')}",
        f"- Target proof checked: {proof.get('created_at', '')}",
        f"- Strict sweep ready: {'yes' if strict_ready else 'no - run refresh-github-targets and validate with --strict-target-proof'}",
        "",
        "## Targets",
        "",
    ]
    for target in matrix["targets"]:
        selection_proof = target.get("selection_proof", {})
        if selection_proof:
            proof_line = (
                f"{selection_proof.get('status')} - "
                f"{selection_proof.get('open_bug_count')} open bugs "
                f"(floor {selection_proof.get('open_bug_floor')}), "
                f"{selection_proof.get('stargazers_count', 'unknown')} stars "
                f"(floor {selection_proof.get('stargazer_floor', matrix['selection_contract'].get('stargazer_floor', 'unknown'))}, "
                f"license {selection_proof.get('license', 'unknown')} via {selection_proof.get('license_source', 'unknown')}, "
                f"checked {selection_proof.get('checked_at')})"
            )
        else:
            proof_line = "not refreshed"
        lines.extend([
            f"### {target['label']}",
            "",
            f"- Repo: {target['repo']}",
            f"- Stack: {target['stack']}",
            f"- Bug query: {target['bug_query']}",
            f"- Selection proof: {proof_line}",
            f"- Status: {target['status']}",
            f"- Notes: {target['notes']}",
            "",
        ])
    lines.extend([
        "## Assignments",
        "",
    ])
    for assignment in matrix["assignments"]:
        lines.append(
            f"- `{assignment['id']}`: {assignment['target']['label']} as "
            f"{assignment['persona']['label']} ({len(assignment['scenarios'])} scenarios) - "
            f"`{assignment['emit_run_command']}`"
        )
        for task in assignment.get("scenario_tasks", [])[:2]:
            lines.append(f"  - `{task['scenario']}`: {task['task_prompt']}")
    lines.extend([
        "",
        "## Execution Loop",
        "",
        "1. Refresh each target's open bug count from its `bug_query` before a live scored sweep.",
        "2. Create one product-journey run per assignment.",
        "3. Drive scenarios through Kitsoki and visual MCP using the assigned persona.",
        "4. Attach evidence, record findings, and run the review gate.",
        "5. Review the per-run Slidey deck plus this matrix deck.",
    ])
    return "\n".join(lines) + "\n"


def render_matrix_deck(matrix: dict) -> dict:
    target_lines = [
        (
            f"{target['label']} - {target['stack']} - "
            f"{target.get('selection_proof', {}).get('open_bug_count', 'unrefreshed')} bugs / floor {target['open_bug_floor']} - "
            f"{target.get('selection_proof', {}).get('stargazers_count', 'unrefreshed')} stars / floor {matrix['selection_contract'].get('stargazer_floor', 'n/a')}"
        )
        for target in matrix["targets"]
    ]
    proof = matrix.get("target_proof", {})
    proof_summary = proof.get("summary", {})
    strict_ready = bool(proof) and proof_summary.get("failed", 0) == 0 and proof_summary.get("errors", 0) == 0
    proof_lines = [
        f"Proof: {proof.get('proof_id', 'not refreshed')}",
        f"Checked: {proof.get('created_at', '')}",
        f"Passed: {proof_summary.get('passed', 0)} / {proof_summary.get('targets', 0)}",
        f"Failed: {proof_summary.get('failed', 0)}",
        f"Errors: {proof_summary.get('errors', 0)}",
        f"Bug floor: {proof_summary.get('open_bug_floor', matrix['selection_contract'].get('open_bug_floor', 'n/a'))}",
        f"Star floor: {proof_summary.get('stargazer_floor', matrix['selection_contract'].get('stargazer_floor', 'n/a'))}",
        f"Strict sweep ready: {'yes' if strict_ready else 'no - validate with --strict-target-proof before live scoring'}",
    ]
    assignment_lines = [
        f"{assignment['target']['label']} / {assignment['persona']['label']}"
        for assignment in matrix["assignments"][:16]
    ]
    scenario_lines = [
        f"{scenario['label']}: {', '.join(scenario['required_mcp'])}"
        for scenario in matrix["scenarios"]
    ]
    task_lines = []
    for assignment in matrix["assignments"][:5]:
        first_task = assignment.get("scenario_tasks", [{}])[0]
        if first_task:
            task_lines.append(f"{assignment['target']['label']} / {assignment['persona']['label']}: {first_task.get('task_prompt', '')}")
    return {
        "meta": {
            "mode": "report",
            "title": "Product Journey GitHub Matrix",
            "phase": "planning",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "GitHub Product Journey Matrix",
                "subtitle": f"{matrix['target_count']} repos · {matrix['assignment_count']} assignments",
                "narration": "A repeatable no-LLM plan for natural product journey QA across popular GitHub projects.",
            },
            {
                "type": "narrative",
                "eyebrow": "Selection",
                "title": "Popular GitHub repos with large bug queues",
                "body": "\n".join(target_lines),
                "narration": "Each target is selected for public GitHub usage, popularity, and a large bug-labeled issue corpus.",
            },
            {
                "type": "narrative",
                "eyebrow": "Target proof",
                "title": "Bug corpus and popularity evidence",
                "body": "\n".join(proof_lines),
                "narration": "Current GitHub proof is optional for no-LLM planning, but required before claiming the live matrix satisfies the bug-count and popularity floors.",
            },
            {
                "type": "narrative",
                "eyebrow": "Personas",
                "title": matrix["persona_mode"],
                "body": "\n".join(assignment_lines),
                "narration": "The matrix assigns personas deterministically so results are repeatable across reruns.",
            },
            {
                "type": "narrative",
                "eyebrow": "Scenarios",
                "title": "MCP evidence contract",
                "body": "\n".join(scenario_lines),
                "narration": "Every assignment uses the same scenario set and evidence contract.",
            },
            {
                "type": "narrative",
                "eyebrow": "Task prompts",
                "title": "Natural-use seeds",
                "body": "\n".join(task_lines) if task_lines else "No assignment task prompts generated.",
                "narration": "Each matrix assignment includes deterministic task prompts so natural-use runs are repeatable.",
            },
            {
                "type": "narrative",
                "eyebrow": "Execution",
                "title": "From matrix to reviewable deck",
                "body": "Create runs\nDrive Kitsoki and visual MCP\nAttach evidence\nRecord findings\nRun review gate\nReview per-run and matrix Slidey decks",
                "narration": "The matrix is a planning artifact; each assignment still produces its own evidence-backed bundle.",
            },
        ],
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
    quality_gates = summarize_quality_gates(evidence, outcomes, driver_plan)
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


def summarize_quality_gates(evidence: dict, outcomes: dict, driver_plan: dict) -> list[dict]:
    captured_evidence = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if item.get("status") in {"captured", "validated"}
    }
    proof_evidence = {
        (item.get("scenario", ""), item.get("kind", item.get("evidence_kind", "")))
        for item in evidence.get("items", [])
        if is_proof_evidence(item)
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


def aggregate_scenario_outcomes(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for outcome in run.get("scenario_outcomes", []):
            scenario_id = outcome.get("scenario", "")
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "label": outcome.get("label", scenario_id),
                "runs": 0,
                "present_evidence_count": 0,
                "required_evidence_count": 0,
                "findings_count": 0,
                "strength_count": 0,
                "weakness_count": 0,
                "issue_count": 0,
                "fix_count": 0,
                "blocked_count": 0,
                "outcomes": {},
            })
            finding_counts = outcome.get("finding_counts", {})
            row["runs"] += 1
            row["present_evidence_count"] += outcome.get("present_evidence_count", 0)
            row["required_evidence_count"] += outcome.get("required_evidence_count", 0)
            row["strength_count"] += finding_counts.get("strength", 0)
            row["weakness_count"] += finding_counts.get("weakness", 0)
            row["issue_count"] += finding_counts.get("issue", 0)
            row["fix_count"] += finding_counts.get("fix", 0)
            row["blocked_count"] += finding_counts.get("blocked", 0)
            row["findings_count"] += sum(finding_counts.get(kind, 0) for kind in ["strength", "weakness", "issue", "fix"])
            outcome_name = outcome.get("outcome", "unknown")
            row["outcomes"][outcome_name] = row["outcomes"].get(outcome_name, 0) + 1
    return [by_scenario[key] for key in sorted(by_scenario)]


def aggregate_persona_outcomes(runs: list[dict]) -> list[dict]:
    by_persona: dict[str, dict] = {}
    for run in runs:
        persona = run.get("persona", {})
        persona_id = persona.get("id", "unknown")
        row = by_persona.setdefault(persona_id, {
            "persona": persona_id,
            "label": persona.get("label", persona_id),
            "runs": 0,
            "reviewed_runs": 0,
            "ready_runs": 0,
            "present_evidence_count": 0,
            "required_evidence_count": 0,
            "findings_count": 0,
            "strength_count": 0,
            "weakness_count": 0,
            "issue_count": 0,
            "fix_count": 0,
            "blocked_count": 0,
            "quality_gate_satisfied_runs": 0,
            "quality_gate_total_runs": 0,
            "quality_gate_blocked_runs": 0,
            "proof_minimum_evidence_count": 0,
            "minimum_evidence_count": 0,
            "review_statuses": {},
        })
        row["runs"] += 1
        row["reviewed_runs"] += 1 if run.get("review_status") != "not_reviewed" else 0
        row["ready_runs"] += 1 if run.get("review_status") == "ready" else 0
        row["present_evidence_count"] += run.get("present_evidence_count", 0)
        row["required_evidence_count"] += run.get("required_evidence_count", 0)
        row["findings_count"] += run.get("findings_count", 0)
        row["strength_count"] += run.get("strength_count", 0)
        row["weakness_count"] += run.get("weakness_count", 0)
        row["issue_count"] += run.get("issue_count", 0)
        row["fix_count"] += run.get("fix_count", 0)
        row["blocked_count"] += run.get("blocked_count", 0)
        status = run.get("review_status", "not_reviewed")
        row["review_statuses"][status] = row["review_statuses"].get(status, 0) + 1
        for gate in run.get("quality_gates", []):
            row["quality_gate_total_runs"] += 1
            row["quality_gate_satisfied_runs"] += 1 if gate.get("satisfied") else 0
            row["quality_gate_blocked_runs"] += 1 if gate.get("blocked") else 0
            row["proof_minimum_evidence_count"] += gate.get("proof_minimum_evidence_count", 0)
            row["minimum_evidence_count"] += gate.get("minimum_evidence_count", 0)
    return [by_persona[key] for key in sorted(by_persona)]


def aggregate_quality_gates(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for gate in run.get("quality_gates", []):
            scenario_id = gate.get("scenario", "")
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "label": gate.get("label", scenario_id),
                "runs": 0,
                "satisfied_runs": 0,
                "blocked_runs": 0,
                "present_minimum_evidence_count": 0,
                "proof_minimum_evidence_count": 0,
                "minimum_evidence_count": 0,
                "missing_minimum_evidence": {},
                "missing_proof_minimum_evidence": {},
                "outcomes": {},
            })
            row["runs"] += 1
            row["satisfied_runs"] += 1 if gate.get("satisfied") else 0
            row["blocked_runs"] += 1 if gate.get("blocked") else 0
            row["present_minimum_evidence_count"] += gate.get("present_minimum_evidence_count", 0)
            row["proof_minimum_evidence_count"] += gate.get("proof_minimum_evidence_count", 0)
            row["minimum_evidence_count"] += gate.get("minimum_evidence_count", 0)
            outcome = gate.get("outcome", "not_started")
            row["outcomes"][outcome] = row["outcomes"].get(outcome, 0) + 1
            for evidence_kind in gate.get("missing_minimum_evidence", []):
                row["missing_minimum_evidence"][evidence_kind] = row["missing_minimum_evidence"].get(evidence_kind, 0) + 1
            for evidence_kind in gate.get("missing_proof_minimum_evidence", []):
                row["missing_proof_minimum_evidence"][evidence_kind] = row["missing_proof_minimum_evidence"].get(evidence_kind, 0) + 1
    return [by_scenario[key] for key in sorted(by_scenario)]


def aggregate_driver_journal(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for event in run.get("driver_journal_events", []):
            scenario_id = event.get("scenario", "")
            if not scenario_id:
                continue
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "events": 0,
                "runs": set(),
                "statuses": {},
                "dispatch_modes": {},
                "mcp_tools": {},
                "evidence_refs": 0,
                "blocked_events": 0,
            })
            row["events"] += 1
            row["runs"].add(run.get("run_id", ""))
            status = event.get("status", "attempted")
            row["statuses"][status] = row["statuses"].get(status, 0) + 1
            mode = event.get("dispatch_mode", "")
            if mode:
                row["dispatch_modes"][mode] = row["dispatch_modes"].get(mode, 0) + 1
            for tool in event.get("mcp_tools", []):
                row["mcp_tools"][tool] = row["mcp_tools"].get(tool, 0) + 1
            row["evidence_refs"] += len(event.get("evidence_refs", []))
            if status == "blocked" or event.get("blockers"):
                row["blocked_events"] += 1
    return [
        {**row, "runs": len(row["runs"])}
        for _, row in sorted(by_scenario.items())
    ]


def aggregate_missing_proof_evidence(quality_gates: list[dict], runs: list[dict]) -> list[dict]:
    rows_by_key: dict[tuple[str, str], dict] = {}
    for gate in quality_gates:
        for evidence_kind, count in gate.get("missing_proof_minimum_evidence", {}).items():
            rows_by_key[(gate.get("scenario", ""), evidence_kind)] = {
                "scenario": gate.get("scenario", ""),
                "label": gate.get("label", gate.get("scenario", "")),
                "evidence_kind": evidence_kind,
                "missing_runs": count,
                "runs": gate.get("runs", 0),
                "affected_runs": [],
            }

    for run in runs:
        for gate in run.get("quality_gates", []):
            scenario_id = gate.get("scenario", "")
            for evidence_kind in gate.get("missing_proof_minimum_evidence", []):
                row = rows_by_key.get((scenario_id, evidence_kind))
                if row is None:
                    continue
                row["affected_runs"].append({
                    "run_id": run.get("run_id", ""),
                    "project": run.get("project", {}).get("id", ""),
                    "persona": run.get("persona", {}).get("id", ""),
                    "run_dir": run.get("run_dir", ""),
                    "driver_handoff_path": run.get("driver_handoff_path", ""),
                })

    return sorted(rows_by_key.values(), key=lambda row: (-row["missing_runs"], row["scenario"], row["evidence_kind"]))


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


def render_rollup_summary(rollup: dict) -> str:
    summary = rollup["summary"]
    lines = [
        "# Product journey matrix rollup",
        "",
        f"- Matrix: `{rollup['matrix_id']}`",
        f"- Runs found: {summary['runs_found']} / {summary['assignments']}",
        f"- Reviewed runs: {summary['reviewed_runs']}",
        f"- Ready runs: {summary['ready_runs']}",
        f"- Evidence present: {summary['present_evidence_count']} / {summary['required_evidence_count']}",
        f"- Findings: {summary['findings_count']} (strengths {summary['strength_count']}, weaknesses {summary['weakness_count']}, issues {summary['issue_count']}, fixes {summary['fix_count']}, blocked {summary.get('blocked_count', 0)})",
        f"- Persona outcome rows: {summary.get('persona_outcomes', 0)}",
        f"- Scenario outcome rows: {summary['scenario_outcomes']} ({summary['scenario_outcomes_with_findings']} with findings)",
        f"- Driver journal: {summary.get('driver_journal_events', 0)} events across {summary.get('driver_journal_rows', 0)} scenarios ({summary.get('driver_journal_blocked_events', 0)} blocked, {summary.get('driver_journal_evidence_refs', 0)} evidence refs)",
        f"- Quality gates: {summary.get('quality_gate_satisfied_runs', 0)} / {summary.get('quality_gate_total_runs', 0)} satisfied, {summary.get('quality_gate_blocked_runs', 0)} blocked, proof evidence {summary.get('quality_gate_proof_minimum_evidence_count', 0)} / {summary.get('quality_gate_minimum_evidence_count', 0)} (captured {summary.get('quality_gate_present_minimum_evidence_count', 0)})",
        f"- Missing proof evidence rows: {summary.get('missing_proof_evidence_rows', 0)} ({summary.get('quality_gate_missing_proof_evidence_count', 0)} missing run-slots)",
        "",
        "## Runs",
        "",
    ]
    for run in rollup["runs"]:
        lines.extend([
            f"### {run['project'].get('label', run['project'].get('id', 'unknown'))} / {run['persona'].get('label', run['persona'].get('id', 'unknown'))}",
            "",
            f"- Run: `{run['run_id']}`",
            f"- Review: {run['review_status']} - {run['review_summary']}",
            f"- Evidence: {run['present_evidence_count']} / {run['required_evidence_count']}",
            f"- Quality gates: {sum(1 for gate in run.get('quality_gates', []) if gate.get('satisfied'))} / {len(run.get('quality_gates', []))} satisfied",
            f"- Findings: {run['findings_count']}",
            f"- Deck: `{run['deck_path']}`",
            f"- Execution plan: `{run['execution_plan_path']}`",
            "",
        ])
    if not rollup["runs"]:
        lines.append("- (no run bundles matched this matrix)")
    lines.extend(["", "## Persona Outcomes", ""])
    if rollup.get("persona_outcomes"):
        for row in rollup["persona_outcomes"]:
            status_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["review_statuses"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Persona: `{row['persona']}`",
                f"- Runs: {row['runs']} (reviewed {row['reviewed_runs']}, ready {row['ready_runs']})",
                f"- Evidence: {row['present_evidence_count']} / {row['required_evidence_count']}",
                f"- Findings: {row['findings_count']} (strengths {row['strength_count']}, weaknesses {row['weakness_count']}, issues {row['issue_count']}, fixes {row['fix_count']}, blocked {row.get('blocked_count', 0)})",
                f"- Quality gates: {row['quality_gate_satisfied_runs']} / {row['quality_gate_total_runs']} satisfied, {row['quality_gate_blocked_runs']} blocked",
                f"- Proof evidence: {row['proof_minimum_evidence_count']} / {row['minimum_evidence_count']}",
                f"- Review statuses: {status_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no persona outcomes found in matched runs)")
    lines.extend(["", "## Scenario Outcomes", ""])
    if rollup["scenario_outcomes"]:
        for row in rollup["scenario_outcomes"]:
            outcome_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["outcomes"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Scenario: `{row['scenario']}`",
                f"- Runs: {row['runs']}",
                f"- Evidence: {row['present_evidence_count']} / {row['required_evidence_count']}",
                f"- Findings: {row['findings_count']} (strengths {row['strength_count']}, weaknesses {row['weakness_count']}, issues {row['issue_count']}, fixes {row['fix_count']}, blocked {row.get('blocked_count', 0)})",
                f"- Outcomes: {outcome_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no scenario outcomes found in matched runs)")
    lines.extend(["", "## Driver Journal", ""])
    if rollup.get("driver_journal"):
        for row in rollup["driver_journal"]:
            status_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["statuses"].items()))
            mode_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["dispatch_modes"].items()))
            tool_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["mcp_tools"].items()))
            lines.extend([
                f"### {row['scenario']}",
                "",
                f"- Runs: {row['runs']}",
                f"- Events: {row['events']}",
                f"- Statuses: {status_counts or '(none)'}",
                f"- Dispatch modes: {mode_counts or '(none)'}",
                f"- Evidence refs: {row['evidence_refs']}",
                f"- Blocked events: {row['blocked_events']}",
                f"- MCP tools: {tool_counts or '(none recorded)'}",
                "",
            ])
    else:
        lines.append("- (no driver journal events found in matched runs)")
    lines.extend(["", "## Quality Gates", ""])
    if rollup.get("quality_gates"):
        for row in rollup["quality_gates"]:
            missing = ", ".join(f"{name}={count}" for name, count in sorted(row["missing_proof_minimum_evidence"].items()))
            outcome_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["outcomes"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Scenario: `{row['scenario']}`",
                f"- Runs: {row['runs']}",
                f"- Satisfied: {row['satisfied_runs']} / {row['runs']}",
                f"- Blocked: {row['blocked_runs']}",
                f"- Minimum proof evidence: {row['proof_minimum_evidence_count']} / {row['minimum_evidence_count']} (captured {row['present_minimum_evidence_count']})",
                f"- Missing proof evidence: {missing or '(none)'}",
                f"- Outcomes: {outcome_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no quality gate rows found in matched runs)")
    lines.extend(["", "## Missing Proof Evidence", ""])
    if rollup.get("missing_proof_evidence"):
        for row in rollup["missing_proof_evidence"]:
            lines.append(
                f"- `{row['scenario']}` / `{row['evidence_kind']}`: missing in {row['missing_runs']} / {row['runs']} runs"
            )
            for run in row.get("affected_runs", [])[:5]:
                lines.append(
                    f"  - `{run.get('project', '')}` / `{run.get('persona', '')}`: "
                    f"`{run.get('run_id', '')}`; handoff `{run.get('driver_handoff_path', '')}`"
                )
            if len(row.get("affected_runs", [])) > 5:
                lines.append(f"  - +{len(row.get('affected_runs', [])) - 5} more runs")
    else:
        lines.append("- (none)")
    return "\n".join(lines) + "\n"


def render_rollup_deck(rollup: dict) -> dict:
    summary = rollup["summary"]
    run_lines = [
        f"{run['project'].get('label', run['project'].get('id', 'unknown'))} / {run['persona'].get('label', run['persona'].get('id', 'unknown'))}: {run['review_status']} ({run['present_evidence_count']}/{run['required_evidence_count']} evidence)"
        for run in rollup["runs"][:16]
    ]
    findings_body = (
        f"Strengths: {summary['strength_count']}\n"
        f"Weaknesses: {summary['weakness_count']}\n"
        f"Issues: {summary['issue_count']}\n"
        f"Fixes: {summary['fix_count']}\n"
        f"Blocked: {summary.get('blocked_count', 0)}"
    )
    scenario_lines = [
        f"{row['scenario']}: evidence {row['present_evidence_count']}/{row['required_evidence_count']}, findings {row['findings_count']}, outcomes {', '.join(f'{name}={count}' for name, count in sorted(row['outcomes'].items()))}"
        for row in rollup["scenario_outcomes"][:12]
    ]
    persona_lines = [
        f"{row['persona']}: runs {row['runs']}, ready {row['ready_runs']}, evidence {row['present_evidence_count']}/{row['required_evidence_count']}, proof {row['proof_minimum_evidence_count']}/{row['minimum_evidence_count']}, findings {row['findings_count']}"
        for row in rollup.get("persona_outcomes", [])[:12]
    ]
    driver_lines = [
        f"{row['scenario']}: events {row['events']}, runs {row['runs']}, statuses {', '.join(f'{name}={count}' for name, count in sorted(row['statuses'].items()))}, refs {row['evidence_refs']}, blocked {row['blocked_events']}"
        for row in rollup.get("driver_journal", [])[:12]
    ]
    quality_gate_lines = [
        f"{row['scenario']}: satisfied {row['satisfied_runs']}/{row['runs']}, proof evidence {row['proof_minimum_evidence_count']}/{row['minimum_evidence_count']}, blocked {row['blocked_runs']}"
        for row in rollup.get("quality_gates", [])[:12]
    ]
    missing_proof_lines = [
        (
            f"{row['scenario']} / {row['evidence_kind']}: missing {row['missing_runs']}/{row['runs']} runs"
            + (
                f" - start {row.get('affected_runs', [{}])[0].get('project', '')}/"
                f"{row.get('affected_runs', [{}])[0].get('persona', '')}"
                if row.get("affected_runs") else ""
            )
        )
        for row in rollup.get("missing_proof_evidence", [])[:16]
    ]
    return {
        "meta": {
            "mode": "report",
            "title": "Product Journey Matrix Rollup",
            "phase": "rollup",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Product Journey Matrix Rollup",
                "subtitle": f"{summary['runs_found']} / {summary['assignments']} runs",
                "narration": "Aggregated product-journey evidence and findings across matrix assignments.",
            },
            {
                "type": "narrative",
                "eyebrow": "Coverage",
                "title": "Evidence and readiness",
                "body": f"Reviewed runs: {summary['reviewed_runs']}\nReady runs: {summary['ready_runs']}\nEvidence present: {summary['present_evidence_count']} / {summary['required_evidence_count']}\nProof evidence: {summary.get('quality_gate_proof_minimum_evidence_count', 0)} / {summary.get('quality_gate_minimum_evidence_count', 0)}\nQuality gates satisfied: {summary.get('quality_gate_satisfied_runs', 0)} / {summary.get('quality_gate_total_runs', 0)}\nMissing proof rows: {summary.get('missing_proof_evidence_rows', 0)}\nMissing assignments: {rollup['missing_assignment_count']}",
                "narration": "This rollup shows whether the matrix has enough completed runs to review.",
            },
            {
                "type": "narrative",
                "eyebrow": "Runs",
                "title": "Assignment status",
                "body": "\n".join(run_lines) if run_lines else "No run bundles matched this matrix yet.",
                "narration": "Each run links back to its own deck and execution plan in the rollup markdown.",
            },
            {
                "type": "narrative",
                "eyebrow": "Findings",
                "title": "Strengths, weaknesses, issues, fixes",
                "body": findings_body,
                "narration": "Finding counts are aggregated from the per-run findings files.",
            },
            {
                "type": "narrative",
                "eyebrow": "Persona outcomes",
                "title": "Cross-persona signals",
                "body": "\n".join(persona_lines) if persona_lines else "No persona outcomes found in matched runs.",
                "narration": "Persona outcome rollups show whether different natural-use lenses are producing different evidence, findings, and proof coverage.",
            },
            {
                "type": "narrative",
                "eyebrow": "Scenario outcomes",
                "title": "Cross-run scenario signals",
                "body": "\n".join(scenario_lines) if scenario_lines else "No scenario outcomes found in matched runs.",
                "narration": "Scenario-level rollups show which journeys are repeatedly weak across natural-use assignments.",
            },
            {
                "type": "narrative",
                "eyebrow": "Driver journal",
                "title": "Reusable driver attempts",
                "body": "\n".join(driver_lines) if driver_lines else "No driver journal events found in matched runs.",
                "narration": "Driver journal rollups show which scenarios the reusable driver actually attempted, captured, blocked, or validated.",
            },
            {
                "type": "narrative",
                "eyebrow": "Quality gates",
                "title": "Cross-run proof coverage",
                "body": "\n".join(quality_gate_lines) if quality_gate_lines else "No quality gate rows found in matched runs.",
                "narration": "Quality gate rollups show which scenarios have enough proof-source minimum evidence to count as completed across the matrix.",
            },
            {
                "type": "narrative",
                "eyebrow": "Missing proof",
                "title": "Evidence backlog",
                "body": "\n".join(missing_proof_lines) if missing_proof_lines else "No missing proof evidence across reviewed runs.",
                "narration": "The missing proof scene shows which evidence kinds still need live visual MCP or cassette-backed capture.",
            },
        ],
    }


def rollup_handoff_backlog_summary(rollup: dict, limit: int = 3) -> str:
    lines = []
    for row in rollup.get("missing_proof_evidence", [])[:limit]:
        affected = row.get("affected_runs", [])
        if affected:
            first = affected[0]
            lines.append(
                f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: "
                f"{first.get('project', '')}/{first.get('persona', '')} -> {first.get('driver_handoff_path', '')}"
            )
        else:
            lines.append(f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: no affected run link")
    remaining = len(rollup.get("missing_proof_evidence", [])) - limit
    if remaining > 0:
        lines.append(f"+{remaining} more proof rows in rollup.md")
    return "; ".join(lines)


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
            "mode": "report",
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
    return failed_checks <= {"quality-gates"}


def driver_replay_review_is_expected_one_scenario(reviewed: dict) -> bool:
    failed_checks = {
        check.get("id", "")
        for check in reviewed.get("checks", [])
        if check.get("status") == "fail"
    }
    return failed_checks <= {"scenario-attempts", "driver-journal-coverage", "quality-gates", "playback-or-blocker"}


def cassette_replay_path(run_id: str, scenario_id: str, evidence_kind: str) -> str:
    return f"cassette://product-journey/{run_id}/{demo_evidence_path(scenario_id, evidence_kind)}"


def scenario_minimum_evidence(scenario_id: str) -> list[str]:
    return scenario_quality_gate(scenario_id).get("minimum_evidence", [])


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
            "mode": "report",
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
                "title": review.get("review_status", "unknown"),
                "body": "\n".join([
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
        f"{row['scenario']}: {row['status']}, playback {row['playback_items']}, validation {row['validation_status']}"
        for row in report["scenarios"]
    ]
    return {
        "meta": {
            "mode": "report",
            "title": "Product Journey Driver Replay Sweep",
            "phase": "driver-replay-sweep",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Driver replay sweep",
                "subtitle": f"{report['summary']['passed']} / {report['summary']['scenarios']} scenarios passed · {report['persona']['label']}",
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
    summary = {
        "scenarios": len(rows),
        "passed": len(rows) - len(failed),
        "failed": len(failed),
        "playback_scenarios": sum(1 for row in rows if row["playback_items"] >= 1),
        "validation_errors": sum(row["validation_errors"] for row in rows),
        "validation_warnings": sum(row["validation_warnings"] for row in rows),
        "attached_evidence_count": sum(row["attached_evidence_count"] for row in rows),
    }
    report = {
        "status": "passed" if not failed else "failed",
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
    )

    reviewed = review_run_bundle(run_dir, publish_deck=None)
    validation = validate_run_bundle(run_dir)
    review_is_expected = driver_replay_review_is_expected_one_scenario(reviewed)
    status = "passed" if review_is_expected and validation.get("status") == "valid" else "failed"
    report = {
        "status": status,
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
            "eyebrow": "Finding matrix",
            "title": "Findings by scenario",
            "body": finding_matrix_body,
            "narration": "This matrix shows which scenarios produced strengths, weaknesses, issues, fixes, or blockers.",
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
            "mode": "report",
            "title": "Product Journey QA",
            "phase": "dry-run",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": scenes,
    }


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
        and not os.environ.get("GEARS_RUST_RECHECK")
    ):
        return {
            "status": "validated",
            "notes": f"{project['id']}: cached validation; set GEARS_RUST_RECHECK=1 to rerun the heavy external benchmark",
            "meta": _meta_value(project),
            "next": [
                "Set GEARS_RUST_RECHECK=1 to rerun the heavy external-benchmark verifier.",
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
    local_repo_path = os.environ.get(local_repo_env, "") if local_repo_env else ""
    if not local_repo_path:
        local_repo_path = project.get("local_repo_path", "")
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
                checks.append(f"Gate: {local_repo_env} path does not exist: {local_repo_path}")
        else:
            checks.append(f"Gate: set {local_repo_env} before running this command.")
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
    run_id = generated_at.lower().replace(":", "-")
    for ch in ("/", "\\", " "):
        run_id = run_id.replace(ch, "-")
    base = ARTIFACT_ROOT / run_id
    return (
        Path(report_arg) if report_arg else base / "report.json",
        Path(deck_arg) if deck_arg else base / "deck.slidey.json",
        Path(markdown_arg) if markdown_arg else base / "report.md",
    )


def write_report(catalog: dict, generated_at: str, report_path: Path, deck_path: Path, markdown_path: Path, run_checks: bool) -> None:
    payload = build_report_payload(catalog, generated_at, run_checks)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")

    builder = ROOT / "tools" / "report-deck" / "deterministic_deck.py"
    result = shell([
        sys.executable,
        str(builder),
        "--kind",
        "product-journey",
        "--input",
        str(report_path),
        "--out",
        str(deck_path),
        "--markdown",
        str(markdown_path),
    ], ROOT)
    if result.returncode != 0:
        raise SystemExit(result.stdout + result.stderr)
    print(result.stdout.strip())


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
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
    parser.add_argument("--run-log", action="store_true", help="Force a timestamped run log entry")
    parser.add_argument("--emit-run", action="store_true", help="Write a no-LLM run artifact bundle and Slidey deck")
    parser.add_argument("--emit-matrix", action="store_true", help="Write a no-LLM 10-repo GitHub journey matrix")
    parser.add_argument("--dogfood-smoke", action="store_true", help="Run a deterministic no-LLM matrix-to-rollup smoke and write review artifacts")
    parser.add_argument("--driver-replay-smoke", action="store_true", help="Run a deterministic no-LLM one-scenario driver replay smoke with cassette evidence")
    parser.add_argument("--driver-replay-sweep", action="store_true", help="Run deterministic no-LLM driver replay smokes for every scenario")
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
    parser.add_argument("--seed-demo-evidence", action="store_true", help="Attach deterministic demo evidence and findings to an existing run bundle")
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
        help="Driver dispatch mode for --record-driver-event",
    )
    parser.add_argument(
        "--driver-status",
        default="attempted",
        choices=["attempted", "captured", "blocked", "validated"],
        help="Driver event status for --record-driver-event",
    )
    parser.add_argument("--mcp-tools", default="", help="Comma-separated MCP tools used for --record-driver-event")
    parser.add_argument("--evidence-refs", default="", help="Comma-separated evidence refs produced for --record-driver-event")
    parser.add_argument("--blockers", default="", help="Comma-separated blockers observed for --record-driver-event")
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
    args = parser.parse_args()

    catalog = load_catalog(CATALOG)
    personas = load_personas(PERSONAS)
    scenarios = load_scenarios(SCENARIOS)
    github_targets = load_github_targets(GITHUB_TARGETS)

    if args.validate_corpus:
        result = validate_journey_corpus(personas, scenarios, github_targets)
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

    if args.dogfood_smoke:
        report = build_dogfood_smoke(catalog, github_targets, personas, scenarios, args.seed)
        if args.json_output:
            print(json.dumps({
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
            }, sort_keys=True))
            append_log(f"Ran product journey dogfood smoke {report['dogfood_id']}: {report['status']}")
            if report["status"] != "passed":
                raise SystemExit(1)
            return
        print(f"Product journey dogfood smoke: {report['dogfood_id']}")
        print(f"Status: {report['status']}")
        print(f"Artifacts: {report['dogfood_dir']}")
        print(f"Summary: {report['artifacts']['summary']}")
        print(f"Smoke deck: {report['artifacts']['deck']}")
        print(f"Matrix: {report['matrix']['matrix_dir']}")
        print(f"Run: {report['run']['run_dir']}")
        print(f"Run deck: {report['run']['deck_path']}")
        print(f"Rollup deck: {report['rollup']['deck_path']}")
        print(f"Corpus validation: {report['corpus_validation']['status']} ({report['corpus_validation']['warnings']} warnings)")
        print(f"Review: {report['review']['summary']}")
        print(f"Run validation: {report['validation']['run']['status']} ({report['validation']['run']['warnings']} warnings)")
        print(f"Matrix validation: {report['validation']['matrix']['status']} ({report['validation']['matrix']['warnings']} warnings)")
        append_log(f"Ran product journey dogfood smoke {report['dogfood_id']}: {report['status']}")
        if report["status"] != "passed":
            raise SystemExit(1)
        return

    if args.driver_replay_smoke:
        report = build_driver_replay_smoke(catalog, github_targets, personas, scenarios, args.seed, args.smoke_scenario, args.smoke_persona)
        if args.json_output:
            reviewed = report["review"]
            validation = report["validation"]
            print(json.dumps({
                "status": report["status"],
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
                "review_backlog_summary": report.get("review_backlog_summary", ""),
                "validation_status": validation.get("status"),
                "validation_warnings": validation.get("warnings"),
                "validation_issue_summary": validation.get("validation_issue_summary", ""),
            }, sort_keys=True))
            append_log(f"Ran product journey driver replay smoke {report['smoke_id']}: {report['status']}")
            if report["status"] != "passed":
                raise SystemExit(1)
            return
        print(f"Product journey driver replay smoke: {report['smoke_id']}")
        print(f"Status: {report['status']}")
        print(f"Persona: {report['persona']['label']}")
        print(f"Artifacts: {report['smoke_dir']}")
        print(f"Summary: {report['artifacts']['summary']}")
        print(f"Smoke deck: {report['artifacts']['deck']}")
        print(f"Run: {report['run']['run_dir']}")
        print(f"Run deck: {report['run']['deck_path']}")
        print(f"Review: {report['review']['summary']}")
        print(f"Validation: {report['validation']['status']} ({report['validation']['warnings']} warnings)")
        append_log(f"Ran product journey driver replay smoke {report['smoke_id']}: {report['status']}")
        if report["status"] != "passed":
            raise SystemExit(1)
        return

    if args.driver_replay_sweep:
        report = build_driver_replay_sweep(catalog, github_targets, personas, scenarios, args.seed, args.smoke_persona)
        if args.json_output:
            print(json.dumps({
                "status": report["status"],
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
        print(f"Scenarios: {report['summary']['passed']} / {report['summary']['scenarios']} passed")
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
        matrix_dir, matrix = build_matrix_bundle(github_targets_for_matrix, personas, scenarios, args.seed, args.matrix_personas)
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
        run_dir, run_json = build_run_bundle(
            catalog,
            github_targets,
            personas,
            scenarios,
            args.project,
            args.persona,
            args.seed,
            "dry-run",
            publish_deck,
        )
        if args.json_output:
            result = {
                "status": "created",
                "run_id": run_json["run_id"],
                "run_dir": str(run_dir),
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

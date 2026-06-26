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
from pathlib import Path
from typing import Optional


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
PERSONAS = ROOT / "tools" / "product-journey" / "personas.json"
SCENARIOS = ROOT / "tools" / "product-journey" / "scenarios.json"
GITHUB_TARGETS = ROOT / "tools" / "product-journey" / "github-targets.json"
LOG = ROOT / ".context" / "product-journey-runlog.md"
ARTIFACT_ROOT = ROOT / ".artifacts" / "product-journey"
MATRIX_ROOT = ARTIFACT_ROOT / "matrices"
DEFAULT_DECK = ROOT / "docs" / "decks" / "product-journey-eval.slidey.json"
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
    for key in ["repo", "stack", "bug_query", "open_bug_floor", "source"]:
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
            "evidence": "evidence.json",
            "scenarios": "scenarios.json",
            "execution_plan": "execution-plan.json",
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
    execution_plan = build_execution_plan(run_json, evidence)
    findings = {"run_id": run_id, "items": [], "summary": {"strength": 0, "weakness": 0, "issue": 0, "fix": 0}}
    metrics = {
        "run_id": run_id,
        "stage_count": len(stages),
        "scenario_count": len(scenario_items),
        "validated_stage_count": sum(1 for stage in stages if stage["status"] in {"validated", "cached_validated"}),
        "planned_stage_count": sum(1 for stage in stages if stage["status"] == "planned"),
        "required_evidence_count": evidence["summary"]["required"],
        "present_evidence_count": evidence["summary"]["present"],
        "missing_evidence_count": evidence["summary"]["missing"],
        "product_bugs_found": 0,
        "findings_count": 0,
        "strength_count": 0,
        "weakness_count": 0,
        "fix_count": 0,
        "review_status": "not_reviewed",
        "review_passed_checks": 0,
        "review_total_checks": 0,
        "oracle_results": [],
        "checkpoint_ratings": [],
    }
    bugs = {"run_id": run_id, "items": []}
    journey = render_journey(run_json)
    deck = render_deck(run_json, metrics, evidence=evidence, findings=findings, execution_plan=execution_plan)

    write_json(run_dir / "run.json", run_json)
    (run_dir / "journey.md").write_text(journey, encoding="utf-8")
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "bugs.json", bugs)
    write_json(run_dir / "findings.json", findings)
    write_json(run_dir / "evidence.json", evidence)
    write_json(run_dir / "scenarios.json", {"run_id": run_id, "items": scenario_items})
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    write_json(run_dir / "review.json", {
        "run_id": run_id,
        "status": "not_reviewed",
        "summary": "Run has not been reviewed for readiness yet.",
        "checks": [],
    })
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
            assignments.append({
                "id": assignment_id,
                "target": target,
                "persona": persona,
                "scenarios": scenario_ids,
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
            f"--notes \"{evidence_capture_hint(item['kind'])}\""
            for item in evidence_items
        ]
        steps.append({
            "order": index,
            "scenario": scenario["id"],
            "label": scenario["label"],
            "stage": scenario["stage"],
            "persona": run_json["persona"]["id"],
            "project": run_json["project"]["id"],
            "task": scenario["task"],
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
            "attach_commands": attach_commands,
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
            f"python3 tools/product-journey/run.py --review-run --run-dir {run_dir_arg}",
        ],
    }


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


def update_derived_artifacts(run_dir: Path, publish_deck: Optional[Path] = None) -> None:
    run_json = read_json(run_dir / "run.json")
    evidence = read_json(run_dir / "evidence.json")
    bugs = read_json(run_dir / "bugs.json")
    findings = read_json(run_dir / "findings.json") if (run_dir / "findings.json").exists() else {"run_id": run_json["run_id"], "items": []}
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {
        "run_id": run_json["run_id"],
        "status": "not_reviewed",
        "summary": "Run has not been reviewed for readiness yet.",
        "checks": [],
    }

    evidence_items = evidence.get("items", [])
    present_items = [item for item in evidence_items if item.get("status") in {"captured", "validated"}]
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
        "product_bugs_found": len(bugs.get("items", [])),
        "findings_count": len(finding_items),
        "strength_count": finding_summary["strength"],
        "weakness_count": finding_summary["weakness"],
        "issue_count": finding_summary["issue"],
        "fix_count": finding_summary["fix"],
        "review_status": review.get("status", "not_reviewed"),
        "review_passed_checks": review.get("summary_counts", {}).get("passed", 0),
        "review_total_checks": review.get("summary_counts", {}).get("total", 0),
        "oracle_results": [],
        "checkpoint_ratings": [],
    }

    write_json(run_dir / "run.json", run_json)
    write_json(run_dir / "evidence.json", evidence)
    write_json(run_dir / "findings.json", findings)
    write_json(run_dir / "review.json", review)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "scenarios.json", {"run_id": run_json["run_id"], "items": run_json["scenarios"]})
    execution_plan = build_execution_plan(run_json, evidence)
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    (run_dir / "journey.md").write_text(render_journey(run_json), encoding="utf-8")
    deck = render_deck(run_json, metrics, evidence, findings, review, execution_plan)
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)


def attach_evidence(
    run_dir: Path,
    scenario_id: str,
    evidence_kind: str,
    artifact_path: str,
    status: str,
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
    if status not in {"open", "fixed", "observed", "validated"}:
        raise SystemExit("Finding status must be open, fixed, observed, or validated")
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


def seed_demo_evidence(run_dir: Path, publish_deck: Optional[Path]) -> dict:
    demo_evidence = [
        ("product-discovery", "browser_screenshot", "screens/product-discovery.png", "captured", "demo visual MCP screenshot placeholder"),
        ("project-onboarding", "session_trace", "traces/onboarding.jsonl", "captured", "demo Studio MCP session trace placeholder"),
        ("bugfix", "key_interaction_video", "media/bugfix-key-interaction.mp4", "captured", "demo key interaction video placeholder"),
        ("prd-design", "design_artifact", "artifacts/design.md", "captured", "demo design artifact placeholder"),
        ("feature-implementation", "targeted_test_result", "oracle-results/feature-tests.json", "captured", "demo targeted test result placeholder"),
        ("evidence-backed-product-bug", "bug_report_markdown", "bug-reports/product-issue.md", "captured", "demo product bug report placeholder"),
    ]
    for scenario, kind, path, status, notes in demo_evidence:
        attach_evidence(run_dir, scenario, kind, path, status, notes, publish_deck=None)

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

    update_derived_artifacts(run_dir, publish_deck=publish_deck)
    metrics = read_json(run_dir / "metrics.json")
    return {
        "status": "seeded",
        "run_dir": str(run_dir),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "evidence_added": len(demo_evidence),
        "findings_added": findings_added,
        "present_evidence_count": metrics.get("present_evidence_count", 0),
        "findings_count": metrics.get("findings_count", 0),
        "published_deck_path": str(publish_deck) if publish_deck is not None else "",
    }


def review_run_bundle(run_dir: Path, publish_deck: Optional[Path]) -> dict:
    update_derived_artifacts(run_dir, publish_deck=None)
    run_json = read_json(run_dir / "run.json")
    evidence = read_json(run_dir / "evidence.json")
    findings = read_json(run_dir / "findings.json")
    metrics = read_json(run_dir / "metrics.json")
    execution_plan = build_execution_plan(run_json, evidence)

    required_files = [
        "run.json",
        "journey.md",
        "metrics.json",
        "bugs.json",
        "findings.json",
        "evidence.json",
        "scenarios.json",
        "execution-plan.json",
        "execution-plan.md",
        "review.json",
        "deck.slidey.json",
    ]
    evidence_items = evidence.get("items", [])
    present_items = [item for item in evidence_items if item.get("status") in {"captured", "validated"}]
    rejected_items = [item for item in evidence_items if item.get("status") == "rejected"]
    video_items = [
        item for item in present_items
        if "video" in item.get("kind", "") or "video" in item.get("path", "")
    ]
    finding_items = findings.get("items", [])
    finding_kinds = {item.get("kind") for item in finding_items}

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
            "id": "captured-evidence",
            "status": "pass" if present_items else "fail",
            "summary": "At least one captured or validated evidence artifact is attached.",
            "detail": f"present={len(present_items)}, required={len(evidence_items)}",
        },
        {
            "id": "key-video",
            "status": "pass" if video_items else "warn",
            "summary": "At least one key interaction video is attached for Slidey playback.",
            "detail": f"video_items={len(video_items)}",
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
    metrics["review_status"] = status
    metrics["review_passed_checks"] = passed
    metrics["review_total_checks"] = len(checks)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "execution-plan.json", execution_plan)
    (run_dir / "execution-plan.md").write_text(render_execution_plan(execution_plan), encoding="utf-8")
    deck = render_deck(run_json, metrics, evidence, findings, review, execution_plan)
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)
    return {
        "status": "reviewed",
        "review_status": status,
        "summary": summary,
        "run_dir": str(run_dir),
        "review_path": str(run_dir / "review.json"),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "execution_plan_path": str(run_dir / "execution-plan.md"),
        "passed": passed,
        "warnings": warned,
        "failed": failed,
        "total": len(checks),
        "published_deck_path": str(publish_deck) if publish_deck is not None else "",
    }


def render_matrix_summary(matrix: dict) -> str:
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
        f"- Open bug floor: {matrix['selection_contract']['open_bug_floor']}",
        f"- Refresh: {matrix['selection_contract']['refresh_note']}",
        "",
        "## Targets",
        "",
    ]
    for target in matrix["targets"]:
        lines.extend([
            f"### {target['label']}",
            "",
            f"- Repo: {target['repo']}",
            f"- Stack: {target['stack']}",
            f"- Bug query: {target['bug_query']}",
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
        f"{target['label']} - {target['stack']} - bug floor {target['open_bug_floor']}+"
        for target in matrix["targets"]
    ]
    assignment_lines = [
        f"{assignment['target']['label']} / {assignment['persona']['label']}"
        for assignment in matrix["assignments"][:16]
    ]
    scenario_lines = [
        f"{scenario['label']}: {', '.join(scenario['required_mcp'])}"
        for scenario in matrix["scenarios"]
    ]
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
                "eyebrow": "Execution",
                "title": "From matrix to reviewable deck",
                "body": "Create runs\nDrive Kitsoki and visual MCP\nAttach evidence\nRecord findings\nRun review gate\nReview per-run and matrix Slidey decks",
                "narration": "The matrix is a planning artifact; each assignment still produces its own evidence-backed bundle.",
            },
        ],
    }


def collect_rollup_runs(matrix: dict, explicit_run_dirs: list[str]) -> list[Path]:
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
    review = read_json(run_dir / "review.json") if (run_dir / "review.json").exists() else {"status": "not_reviewed", "summary": ""}
    finding_summary = findings.get("summary", {})
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
        "present_evidence_count": metrics.get("present_evidence_count", evidence.get("summary", {}).get("present", 0)),
        "required_evidence_count": metrics.get("required_evidence_count", evidence.get("summary", {}).get("required", 0)),
        "findings_count": metrics.get("findings_count", len(findings.get("items", []))),
        "strength_count": finding_summary.get("strength", metrics.get("strength_count", 0)),
        "weakness_count": finding_summary.get("weakness", metrics.get("weakness_count", 0)),
        "issue_count": finding_summary.get("issue", metrics.get("issue_count", 0)),
        "fix_count": finding_summary.get("fix", metrics.get("fix_count", 0)),
    }


def build_matrix_rollup(matrix_dir: Path, explicit_run_dirs: list[str]) -> dict:
    matrix = read_json(matrix_dir / "matrix.json")
    run_dirs = collect_rollup_runs(matrix, explicit_run_dirs)
    runs = [summarize_run_for_rollup(path) for path in run_dirs]
    assignment_count = matrix.get("assignment_count", 0)
    reviewed = [run for run in runs if run["review_status"] != "not_reviewed"]
    ready = [run for run in runs if run["review_status"] == "ready"]
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
    }
    return {
        "matrix_id": matrix["matrix_id"],
        "created_at": now_utc(),
        "matrix_dir": str(matrix_dir),
        "matrix_deck_path": str(matrix_dir / "deck.slidey.json"),
        "summary": totals,
        "runs": runs,
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
        f"- Findings: {summary['findings_count']} (strengths {summary['strength_count']}, weaknesses {summary['weakness_count']}, issues {summary['issue_count']}, fixes {summary['fix_count']})",
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
            f"- Findings: {run['findings_count']}",
            f"- Deck: `{run['deck_path']}`",
            f"- Execution plan: `{run['execution_plan_path']}`",
            "",
        ])
    if not rollup["runs"]:
        lines.append("- (no run bundles matched this matrix)")
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
        f"Fixes: {summary['fix_count']}"
    )
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
                "body": f"Reviewed runs: {summary['reviewed_runs']}\nReady runs: {summary['ready_runs']}\nEvidence present: {summary['present_evidence_count']} / {summary['required_evidence_count']}\nMissing assignments: {rollup['missing_assignment_count']}",
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
        ],
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
    }


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
) -> dict:
    stage_lines = [f"{stage['id']}: {stage['status']}" for stage in run_json["stages"]]
    scenario_lines = [
        f"{scenario['label']}: {scenario['stage']} ({', '.join(scenario['required_mcp'])})"
        for scenario in run_json["scenarios"]
    ]
    captured = []
    if evidence is not None:
        captured = [
            f"{item['scenario']} / {item['kind']}: {item.get('path', '')}"
            for item in evidence.get("items", [])
            if item.get("status") in {"captured", "validated"} and item.get("path")
        ]
    video_lines = [line for line in captured if "video" in line]
    if not video_lines:
        video_body = "No clips attached yet. Expected clips: product discovery, onboarding, bugfix, PRD/design, feature implementation, and product bug filing."
    else:
        video_body = "\n".join(video_lines)
    captured_body = "\n".join(captured[:12]) if captured else "No evidence attached yet."
    finding_items = findings.get("items", []) if findings is not None else []
    finding_lines = [
        f"{item['kind']}: {item['title']} ({item.get('severity', 'n/a')})"
        for item in finding_items[:12]
    ]
    findings_body = "\n".join(finding_lines) if finding_lines else "No strengths, weaknesses, issues, or fixes recorded yet."
    review_body = "Not reviewed yet."
    if review is not None:
        review_lines = [review.get("summary", "No review summary.")]
        for check in review.get("checks", [])[:8]:
            review_lines.append(f"{check.get('status', 'unknown')}: {check.get('id', 'check')} - {check.get('summary', '')}")
        review_body = "\n".join(review_lines)
    execution_lines = []
    if execution_plan is not None:
        execution_lines = [
            f"{step['scenario']}: {', '.join(mcp['tool'] for mcp in step['mcp_steps'])}"
            for step in execution_plan.get("steps", [])
        ]
    execution_body = "\n".join(execution_lines) if execution_lines else "Execution plan not generated yet."
    return {
        "meta": {
            "mode": "report",
            "title": "Product Journey QA",
            "phase": "dry-run",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
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
                "eyebrow": "Metrics",
                "title": "Current evidence",
                "body": f"Validated stages: {metrics['validated_stage_count']} / {metrics['stage_count']}\nCaptured stages: {metrics.get('captured_stage_count', 0)}\nScenarios: {metrics['scenario_count']}\nEvidence present: {metrics['present_evidence_count']} / {metrics['required_evidence_count']}\nFindings: {metrics.get('findings_count', 0)}\nStrengths: {metrics.get('strength_count', 0)} · Weaknesses: {metrics.get('weakness_count', 0)} · Fixes: {metrics.get('fix_count', 0)}\nProduct bugs found: {metrics['product_bugs_found']}",
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
                "eyebrow": "Review readiness",
                "title": metrics.get("review_status", "not_reviewed"),
                "body": review_body,
                "narration": "The review gate checks whether the bundle has enough evidence and findings to discuss.",
            },
            {
                "type": "narrative",
                "eyebrow": "Video playback",
                "title": "Key interactions",
                "body": video_body,
                "narration": "Slidey scenes reserve space for key interaction playback once visual evidence is captured.",
            },
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
        ],
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
    parser.add_argument("--run-log", action="store_true", help="Force a timestamped run log entry")
    parser.add_argument("--emit-run", action="store_true", help="Write a no-LLM run artifact bundle and Slidey deck")
    parser.add_argument("--emit-matrix", action="store_true", help="Write a no-LLM 10-repo GitHub journey matrix")
    parser.add_argument("--rollup-matrix", action="store_true", help="Aggregate reviewed run bundles into a matrix rollup deck")
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
    parser.add_argument("--seed-demo-evidence", action="store_true", help="Attach deterministic demo evidence and findings to an existing run bundle")
    parser.add_argument("--review-run", action="store_true", help="Review an existing run bundle for readiness")
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
    parser.add_argument("--notes", default="", help="Notes for --attach-evidence")
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
        choices=["open", "fixed", "observed", "validated"],
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

    if args.emit_matrix:
        matrix_dir, matrix = build_matrix_bundle(github_targets, personas, scenarios, args.seed, args.matrix_personas)
        if args.json_output:
            print(json.dumps({
                "status": "matrix_created",
                "matrix_id": matrix["matrix_id"],
                "matrix_dir": str(matrix_dir),
                "deck_path": str(matrix_dir / "deck.slidey.json"),
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
        print(f"Evidence present: {seeded['present_evidence_count']}")
        print(f"Findings: {seeded['findings_count']}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Seeded demo evidence for {run_dir.name}")
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
            print(json.dumps({
                "status": "recorded",
                "run_dir": str(run_dir),
                "finding_kind": args.finding_kind,
                "title": args.title,
                "scenario": args.scenario,
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }, sort_keys=True))
            append_log(f"Recorded {args.finding_kind} finding for {run_dir.name}: {args.title}")
            return
        print(f"Recorded finding: {args.finding_kind} / {args.title}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
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
            args.notes,
            publish_deck,
        )
        if args.json_output:
            print(json.dumps({
                "status": "attached",
                "run_dir": str(run_dir),
                "scenario": args.scenario,
                "evidence_kind": args.evidence_kind,
                "evidence_path": args.evidence_path,
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }, sort_keys=True))
            append_log(f"Attached evidence {args.scenario}/{args.evidence_kind} to {run_dir.name}")
            return
        print(f"Attached evidence: {args.scenario}/{args.evidence_kind}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
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
            print(json.dumps({
                "status": "created",
                "run_id": run_json["run_id"],
                "run_dir": str(run_dir),
                "deck_path": str(run_dir / "deck.slidey.json"),
                "execution_plan_path": str(run_dir / "execution-plan.md"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }, sort_keys=True))
            append_log(f"Emitted dry-run bundle {run_json['run_id']}")
            return
        print(f"Product journey run: {run_json['run_id']}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        print(f"Execution plan: {run_dir / 'execution-plan.md'}")
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

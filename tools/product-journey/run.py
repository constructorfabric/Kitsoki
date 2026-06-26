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
from pathlib import Path
from typing import Optional


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
PERSONAS = ROOT / "tools" / "product-journey" / "personas.json"
SCENARIOS = ROOT / "tools" / "product-journey" / "scenarios.json"
LOG = ROOT / ".context" / "product-journey-runlog.md"
ARTIFACT_ROOT = ROOT / ".artifacts" / "product-journey"
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
    return {
        "id": project["id"],
        "label": project.get("label", project["id"]),
        "status": project["status"],
        "notes": project["notes"],
        "manifest": project.get("manifest"),
    }


def target_status(project: dict) -> str:
    if project.get("validation_command"):
        return "ready-heavy-check"
    if project.get("run_mode") == "external-benchmark" and project.get("status") == "validated":
        return "cached_validated"
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
            evidence.append(project.get("manifest") or project.get("validation_command") or "catalog target")
        elif stage in {"discover_product", "follow_tutorial", "file_product_issue"}:
            status = "planned"
            evidence.append("requires visual MCP/browser evidence in live or cassette run")
        elif stage == "onboard_project":
            status = "planned"
            evidence.append(project.get("manifest") or "project onboarding fixture pending")
        elif stage in {"plan_project_work", "fix_bug"}:
            status = readiness if project.get("manifest") else "planned"
            evidence.append(project.get("manifest") or "bug/design fixture pending")
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
    personas: list[dict],
    scenarios: list[dict],
    project_id: str,
    persona_id: str,
    seed: str,
    mode: str,
    publish_deck: Optional[Path],
) -> tuple[Path, dict]:
    target = next((t for t in catalog["targets"] if t["id"] == project_id), None)
    if target is None:
        known = ", ".join(t["id"] for t in catalog["targets"])
        raise SystemExit(f"Unknown project '{project_id}'. Known: {known}")
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
            "evidence": "evidence.json",
            "scenarios": "scenarios.json",
            "deck": "deck.slidey.json",
        },
        "notes": [
            "This dry run is deterministic and does not call a live LLM.",
            "Visual MCP, Kitsoki session driving, and video evidence are represented as planned stages until a live or cassette run supplies artifacts.",
        ],
    }
    evidence = evidence_plan(run_json)
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
        "oracle_results": [],
        "checkpoint_ratings": [],
    }
    bugs = {"run_id": run_id, "items": []}
    journey = render_journey(run_json)
    deck = render_deck(run_json, metrics)

    write_json(run_dir / "run.json", run_json)
    (run_dir / "journey.md").write_text(journey, encoding="utf-8")
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "bugs.json", bugs)
    write_json(run_dir / "evidence.json", evidence)
    write_json(run_dir / "scenarios.json", {"run_id": run_id, "items": scenario_items})
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)
    return run_dir, run_json


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
        "oracle_results": [],
        "checkpoint_ratings": [],
    }

    write_json(run_dir / "run.json", run_json)
    write_json(run_dir / "evidence.json", evidence)
    write_json(run_dir / "metrics.json", metrics)
    write_json(run_dir / "scenarios.json", {"run_id": run_json["run_id"], "items": run_json["scenarios"]})
    (run_dir / "journey.md").write_text(render_journey(run_json), encoding="utf-8")
    deck = render_deck(run_json, metrics, evidence)
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


def render_deck(run_json: dict, metrics: dict, evidence: Optional[dict] = None) -> dict:
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
                "eyebrow": "Metrics",
                "title": "Current evidence",
                "body": f"Validated stages: {metrics['validated_stage_count']} / {metrics['stage_count']}\nCaptured stages: {metrics.get('captured_stage_count', 0)}\nScenarios: {metrics['scenario_count']}\nEvidence present: {metrics['present_evidence_count']} / {metrics['required_evidence_count']}\nProduct bugs found: {metrics['product_bugs_found']}",
                "narration": "This report distinguishes validated evidence from planned stages.",
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


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", default="gears-rust", help="Project id from catalog")
    parser.add_argument(
        "--mode",
        default="status",
        choices=["status", "check"],
        help="status: print catalog, check: validate a single project",
    )
    parser.add_argument("--persona", default="", help="Persona id from tools/product-journey/personas.json")
    parser.add_argument("--seed", default="default", help="Deterministic run seed")
    parser.add_argument("--run-log", action="store_true", help="Force a timestamped run log entry")
    parser.add_argument("--emit-run", action="store_true", help="Write a no-LLM run artifact bundle and Slidey deck")
    parser.add_argument("--attach-evidence", action="store_true", help="Attach one evidence artifact to an existing run bundle")
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
    parser.add_argument("--json-output", action="store_true", help="Print machine-readable JSON for story/host.run callers")
    parser.add_argument(
        "--publish-deck",
        action="store_true",
        help="Also update docs/decks/product-journey-eval.slidey.json with the generated deck",
    )
    args = parser.parse_args()

    catalog = load_catalog(CATALOG)
    personas = load_personas(PERSONAS)
    scenarios = load_scenarios(SCENARIOS)

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
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }, sort_keys=True))
            append_log(f"Attached evidence {args.scenario}/{args.evidence_kind} to {run_dir.name}")
            return
        print(f"Attached evidence: {args.scenario}/{args.evidence_kind}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Attached evidence {args.scenario}/{args.evidence_kind} to {run_dir.name}")
        return

    if args.emit_run:
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir, run_json = build_run_bundle(catalog, personas, scenarios, args.project, args.persona, args.seed, "dry-run", publish_deck)
        if args.json_output:
            print(json.dumps({
                "status": "created",
                "run_id": run_json["run_id"],
                "run_dir": str(run_dir),
                "deck_path": str(run_dir / "deck.slidey.json"),
                "published_deck_path": str(publish_deck) if publish_deck is not None else "",
            }, sort_keys=True))
            append_log(f"Emitted dry-run bundle {run_json['run_id']}")
            return
        print(f"Product journey run: {run_json['run_id']}")
        print(f"Artifacts: {run_dir}")
        print(f"Deck: {run_dir / 'deck.slidey.json'}")
        if publish_deck is not None:
            print(f"Published deck: {publish_deck}")
        append_log(f"Emitted dry-run bundle {run_json['run_id']}")
        return

    if args.mode == "status":
        print_status(catalog)
        append_log("Printed journey catalog and perspective status")
        return

    print_check(catalog, args.project)

    if args.run_log:
        append_log(f"Manual run flag set for project {args.project}")


if __name__ == "__main__":
    main()

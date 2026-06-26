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


def stage_plan(project: dict) -> list[dict]:
    readiness = target_status(project)
    stages: list[dict] = []
    for stage in STAGES:
        status = "planned"
        evidence: list[str] = []
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
        stages.append({"id": stage, "status": status, "evidence": evidence})
    return stages


def build_run_bundle(
    catalog: dict,
    personas: list[dict],
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

    stages = stage_plan(target)
    run_json = {
        "run_id": run_id,
        "created_at": created_at,
        "mode": mode,
        "seed": seed,
        "project": _meta_value(target),
        "persona": persona,
        "stages": stages,
        "artifacts": {
            "run": "run.json",
            "journey": "journey.md",
            "metrics": "metrics.json",
            "bugs": "bugs.json",
            "deck": "deck.slidey.json",
        },
        "notes": [
            "This dry run is deterministic and does not call a live LLM.",
            "Visual MCP, Kitsoki session driving, and video evidence are represented as planned stages until a live or cassette run supplies artifacts.",
        ],
    }
    metrics = {
        "run_id": run_id,
        "stage_count": len(stages),
        "validated_stage_count": sum(1 for stage in stages if stage["status"] in {"validated", "cached_validated"}),
        "planned_stage_count": sum(1 for stage in stages if stage["status"] == "planned"),
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
    write_json(run_dir / "deck.slidey.json", deck)
    if publish_deck is not None:
        publish_deck.parent.mkdir(parents=True, exist_ok=True)
        write_json(publish_deck, deck)
    return run_dir, run_json


def write_json(path: Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


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
        for evidence in stage["evidence"]:
            lines.append(f"  - evidence: {evidence}")
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


def render_deck(run_json: dict, metrics: dict) -> dict:
    stage_lines = [f"{stage['id']}: {stage['status']}" for stage in run_json["stages"]]
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
                "eyebrow": "Metrics",
                "title": "Current evidence",
                "body": f"Validated stages: {metrics['validated_stage_count']} / {metrics['stage_count']}\nProduct bugs found: {metrics['product_bugs_found']}",
                "narration": "This report distinguishes validated evidence from planned stages.",
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
    parser.add_argument(
        "--publish-deck",
        action="store_true",
        help="Also update docs/decks/product-journey-eval.slidey.json with the generated deck",
    )
    args = parser.parse_args()

    catalog = load_catalog(CATALOG)
    personas = load_personas(PERSONAS)

    if args.emit_run:
        publish_deck = DEFAULT_DECK if args.publish_deck else None
        run_dir, run_json = build_run_bundle(catalog, personas, args.project, args.persona, args.seed, "dry-run", publish_deck)
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

#!/usr/bin/env python3
"""Product-journey evaluation runner.

This is the first execution entrypoint for the product-journey harness. It is
intentionally deterministic: checks use existing local metadata and manifest
contracts so the runner itself stays cost-free by default.
"""

import argparse
import json
import os
import subprocess
import datetime
import tempfile
import shutil
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
LOG = ROOT / ".context" / "product-journey-runlog.md"


def load_catalog(path: Path):
    return json.loads(path.read_text())


def append_log(message: str):
    LOG.parent.mkdir(parents=True, exist_ok=True)
    now = datetime.datetime.now().isoformat(timespec="seconds")
    entry = f"- [{now}] {message}\n"
    if not LOG.exists():
        LOG.write_text("# Product journey run log\n\n")
    with LOG.open("a", encoding="utf-8") as fp:
        fp.write(entry)


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
    parser.add_argument("--run-log", action="store_true", help="Force a timestamped run log entry")
    args = parser.parse_args()

    catalog = load_catalog(CATALOG)

    if args.mode == "status":
        print_status(catalog)
        append_log("Printed journey catalog and perspective status")
        return

    print_check(catalog, args.project)

    if args.run_log:
        append_log(f"Manual run flag set for project {args.project}")


if __name__ == "__main__":
    main()

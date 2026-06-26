#!/usr/bin/env python3
"""Local exploratory QA runner.

This is a thin wrapper around the product-journey catalog plus the deterministic
external benchmark verifier for gears-rust. It intentionally keeps the local
story-qa surface honest: one validated project, two explicitly planned lanes.
"""

import argparse
import datetime
import json
import os
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
REPORT = ROOT / ".context" / "story-qa-run.md"
ARTIFACT_ROOT = ROOT / ".artifacts" / "story-qa"


def shell(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=False,
        env=os.environ.copy(),
        text=True,
        capture_output=True,
    )


def load_catalog() -> dict:
    return json.loads(CATALOG.read_text())


def write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def write_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def render_report(summary: dict) -> str:
    body = ["# Story QA run", "", f"- [{summary['generated_at']}] story-qa summary", ""]
    for target in summary["targets"]:
        verify = target["verify"]
        body.append(f"- {target['id']}: {target['status']} - {target['notes']}")
        detail = f"  verify: {verify['status']}"
        if verify.get("detail"):
            detail += f" ({verify['detail']})"
        body.append(f"- {detail}")
        for line in verify.get("output", [])[:20]:
            body.append(f"  - {line}")
    body.append("")
    body.append(f"Artifacts: `{summary['artifact_dir']}`")
    return "\n".join(body) + "\n"


def write_slidey_spec(path: Path, summary: dict) -> None:
    builder = ROOT / "tools" / "report-deck" / "deterministic_deck.py"
    subprocess.run(
        [
            "python3",
            str(builder),
            "--kind",
            "story-qa",
            "--input-json",
            json.dumps(summary, sort_keys=True),
            "--out",
            str(path),
        ],
        cwd=ROOT,
        check=True,
        stdout=subprocess.DEVNULL,
    )


def local_temp_clone(src: str) -> Path:
    tmp = Path(tempfile.mkdtemp(prefix="story-qa-"))
    clone = tmp / Path(src).name
    result = shell(["git", "clone", "--no-local", "--no-checkout", src, str(clone)], ROOT)
    if result.returncode != 0:
        raise RuntimeError(result.stdout + result.stderr)
    return clone


def verify_gears_rust(repo: str) -> tuple[str, str]:
    bench = ROOT / "tools" / "bugfix-bakeoff" / "external" / "bench.py"
    clone = local_temp_clone(repo)
    try:
        result = shell(
            ["python3", str(bench), "verify", "--project", "gears-rust", "--repo-dir", str(clone)],
            ROOT,
        )
        output = result.stdout + result.stderr
        if result.returncode == 0:
            return "validated", output
        return "error", output
    finally:
        import shutil

        shutil.rmtree(clone.parent, ignore_errors=True)


def output_lines(output: str) -> list[str]:
    return [line for line in output.strip().splitlines() if line.strip()]


def run(project: str, generated_at: str = "") -> dict:
    if not generated_at:
        generated_at = datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds").replace("+00:00", "Z")
    run_id = generated_at.replace(":", "").replace("-", "").replace("T", "-").replace("Z", "z")
    artifact_dir = ARTIFACT_ROOT / run_id
    markdown_path = artifact_dir / "report.md"
    summary_path = artifact_dir / "summary.json"
    deck_path = artifact_dir / "deck.slidey.json"
    catalog = load_catalog()
    targets = catalog["targets"]
    if project != "all":
        targets = [t for t in targets if t["id"] == project]
        if not targets:
            known = ", ".join(t["id"] for t in catalog["targets"])
            raise SystemExit(f"Unknown project {project!r}. Known: {known}")

    rows: list[dict] = []
    for target in targets:
        pid = target["id"]
        row = {
            "id": pid,
            "label": target.get("label", pid),
            "stack": target.get("stack", ""),
            "run_mode": target.get("run_mode", ""),
            "status": target.get("status", ""),
            "notes": target.get("notes", ""),
            "verify": {"status": "planned", "detail": "", "output": []},
        }
        validation_command = target.get("validation_command", "")
        if validation_command:
            result = shell(["bash", "-lc", validation_command], ROOT)
            if result.returncode == 0:
                row["verify"] = {"status": "validated", "detail": validation_command, "output": output_lines(result.stdout)}
                rows.append(row)
                continue
            row["verify"] = {
                "status": "error",
                "detail": validation_command,
                "output": output_lines(result.stdout + result.stderr),
            }
            rows.append(row)
            continue
        if pid == "gears-rust":
            if target.get("status") == "validated" and not os.environ.get("GEARS_RUST_RECHECK"):
                row["verify"] = {
                    "status": "validated",
                    "detail": "cached; set GEARS_RUST_RECHECK=1 to rerun",
                    "output": [],
                }
                rows.append(row)
                continue
            repo = os.environ.get(target.get("local_repo_env", "GEARS_RUST_REPO"), "")
            if not repo:
                row["verify"] = {"status": "blocked", "detail": "set GEARS_RUST_REPO to a local checkout", "output": []}
                rows.append(row)
                continue
            status, output = verify_gears_rust(repo)
            row["verify"] = {"status": status, "detail": repo, "output": output_lines(output)}
            rows.append(row)
            continue
        local_path = target.get("local_repo_path", "")
        if local_path and Path(local_path).exists():
            row["verify"] = {
                "status": "available",
                "detail": f"local checkout present; corpus wiring pending: {local_path}",
                "output": [],
            }
        else:
            row["verify"] = {"status": "planned", "detail": f"needs a local {pid} checkout/corpus", "output": []}
        rows.append(row)

    summary = {
        "project": project,
        "generated_at": generated_at,
        "artifact_dir": str(artifact_dir.relative_to(ROOT)),
        "markdown_path": str(markdown_path.relative_to(ROOT)),
        "summary_path": str(summary_path.relative_to(ROOT)),
        "deck_path": str(deck_path.relative_to(ROOT)),
        "catalog": str(CATALOG.relative_to(ROOT)),
        "targets": rows,
    }
    markdown = render_report(summary)
    write_text(REPORT, markdown)
    write_text(markdown_path, markdown)
    write_json(summary_path, summary)
    deck_summary = dict(summary)
    deck_summary["_source"] = str(summary_path.relative_to(ROOT))
    write_slidey_spec(deck_path, deck_summary)
    return summary


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", default="all", help="gears-rust, postgresql, kubernetes, or all")
    parser.add_argument("--generated-at", default="", help="fixed ISO timestamp for deterministic artifact paths")
    args = parser.parse_args()

    summary = run(args.project, generated_at=args.generated_at)
    print("Story QA")
    for target in summary["targets"]:
        print(f"{target['id']}: {target['status']} - {target['notes']}")
        verify = target["verify"]
        detail = f"  verify: {verify['status']}"
        if verify.get("detail"):
            detail += f" ({verify['detail']})"
        print(detail)
        for line in verify.get("output", [])[:20]:
            print(f"    {line}")
    print(f"\nreport: {REPORT}")
    print(f"artifact report: {summary['markdown_path']}")
    print(f"summary: {summary['summary_path']}")
    print(f"deck: {summary['deck_path']}")


if __name__ == "__main__":
    main()

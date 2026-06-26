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


def write_report(lines: list[str]) -> None:
    REPORT.parent.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now().isoformat(timespec="seconds")
    body = ["# Story QA run", "", f"- [{stamp}] story-qa summary", ""]
    body.extend(f"- {line}" for line in lines)
    REPORT.write_text("\n".join(body) + "\n")


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


def run(project: str) -> list[str]:
    catalog = load_catalog()
    targets = catalog["targets"]
    if project != "all":
        targets = [t for t in targets if t["id"] == project]
        if not targets:
            known = ", ".join(t["id"] for t in catalog["targets"])
            raise SystemExit(f"Unknown project {project!r}. Known: {known}")

    lines: list[str] = []
    for target in targets:
        pid = target["id"]
        lines.append(f"{pid}: {target['status']} - {target['notes']}")
        validation_command = target.get("validation_command", "")
        if validation_command:
            result = shell(["bash", "-lc", validation_command], ROOT)
            if result.returncode == 0:
                lines.append("  verify: validated")
                if result.stdout.strip():
                    lines.extend(f"    {line}" for line in result.stdout.strip().splitlines())
                continue
            lines.append("  verify: error")
            if result.stdout.strip():
                lines.extend(f"    {line}" for line in result.stdout.strip().splitlines())
            if result.stderr.strip():
                lines.extend(f"    {line}" for line in result.stderr.strip().splitlines())
            continue
        if pid == "gears-rust":
            if target.get("status") == "validated" and not os.environ.get("GEARS_RUST_RECHECK"):
                lines.append("  verify: validated (cached; set GEARS_RUST_RECHECK=1 to rerun)")
                continue
            repo = os.environ.get(target.get("local_repo_env", "GEARS_RUST_REPO"), "")
            if not repo:
                lines.append("  verify: blocked (set GEARS_RUST_REPO to a local checkout)")
                continue
            status, output = verify_gears_rust(repo)
            lines.append(f"  verify: {status}")
            if output.strip():
                lines.extend(f"    {line}" for line in output.strip().splitlines())
            continue
        local_path = target.get("local_repo_path", "")
        if local_path and Path(local_path).exists():
            lines.append(f"  local checkout: {local_path}")
            lines.append("  verify: available (local checkout present; corpus wiring pending)")
        else:
            lines.append(f"  verify: planned (needs a local {pid} checkout/corpus)")

    write_report(lines)
    return lines


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", default="all", help="gears-rust, postgresql, kubernetes, or all")
    args = parser.parse_args()

    lines = run(args.project)
    print("Story QA")
    for line in lines:
        print(line)
    print(f"\nreport: {REPORT}")


if __name__ == "__main__":
    main()

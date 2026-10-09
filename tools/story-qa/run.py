#!/usr/bin/env python3
"""Local exploratory QA runner.

The default path is summary-only and cost-free. Heavy deterministic checks are
available through an explicit flag so a quick QA status command does not launch
large local project builds by surprise.
"""

import argparse
import datetime
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "tools" / "product-journey" / "catalog.json"
REPORT = ROOT / ".context" / "story-qa-run.md"
DEFAULT_TIMEOUT_SECONDS = 600


def shell(cmd: list[str], cwd: Path, timeout: int = DEFAULT_TIMEOUT_SECONDS) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=False,
        env=os.environ.copy(),
        text=True,
        capture_output=True,
        timeout=timeout,
    )


def load_catalog() -> dict:
    return json.loads(CATALOG.read_text())


def write_report(lines: list[str]) -> None:
    REPORT.parent.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now().isoformat(timespec="seconds")
    body = ["# Story QA run", "", f"- [{stamp}] story-qa summary", ""]
    body.extend(f"- {line}" for line in lines)
    REPORT.write_text("\n".join(body) + "\n")


def target_status(target: dict) -> str:
    if target.get("validation_command"):
        return "ready-heavy-check"
    if target.get("run_mode") == "external-benchmark" and target.get("status") == "validated":
        return "cached_validated"
    return target.get("status", "planned")


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
        shutil.rmtree(clone.parent, ignore_errors=True)


def run(project: str, check: bool, timeout: int) -> list[str]:
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
        status = target_status(target)
        lines.append(f"{pid}: {status} - {target['notes']}")
        if not check:
            if target.get("validation_command"):
                lines.append("  verify: skipped (pass --check to run heavyweight local oracle)")
            elif status == "cached_validated":
                lines.append("  verify: cached (pass --check with required local repo env to re-run)")
            else:
                lines.append("  verify: skipped (summary mode)")
            continue

        validation_command = target.get("validation_command", "")
        if validation_command:
            try:
                result = shell(["bash", "-lc", validation_command], ROOT, timeout=timeout)
            except subprocess.TimeoutExpired as exc:
                lines.append(f"  verify: timeout after {timeout}s")
                if exc.stdout:
                    lines.extend(f"    {line}" for line in str(exc.stdout).splitlines())
                if exc.stderr:
                    lines.extend(f"    {line}" for line in str(exc.stderr).splitlines())
                continue
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
            repo = os.environ.get(target.get("local_repo_env", "BUGFIX_BAKEOFF_REPO"), "")
            if not repo:
                lines.append("  verify: blocked (set BUGFIX_BAKEOFF_REPO to a local checkout)")
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
    parser.add_argument("--check", action="store_true", help="Run gated deterministic verification commands")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="Per-check timeout in seconds")
    args = parser.parse_args()

    lines = run(args.project, check=args.check, timeout=args.timeout)
    print("Story QA")
    for line in lines:
        print(line)
    print(f"\nreport: {REPORT}")


if __name__ == "__main__":
    main()

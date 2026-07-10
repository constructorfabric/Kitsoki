#!/usr/bin/env python3
"""run_checks.py — the WS-F gate's check suite: real no-LLM proofs → verdict files.

Maps each matrix cell with a real, runnable, no-LLM proof today onto a
`replay`-check_type invocation, runs it, and writes one
`schemas/completion-state.schema.json`-conformant verdict JSON per cell into a
verdicts directory that `generate.py --verdicts-dir` (and `--gate`) reads back.

This intentionally does NOT go through tools/arena's container executor —
every check here is a plain `go run ./cmd/kitsoki test flows <app.yaml>` (or a
product-journey smoke script) that already runs fine on the bare host, no
docker needed. It reuses arena's check-type VOCABULARY (`check_type: replay` /
`journey-verdict`) and completion-state SHAPE so the same verdict files would
also make sense if a future spec wired these same suites through arena
(`tools/arena/README.md`'s check-type contract), but the runner itself is
deliberately the simplest thing that works.

Scope (WS-F F1 exit: "start with the cells that have real runnable proofs
today"): the four story flow suites the plan names directly — `prd`,
`bugfix`, `dev-story` (covers onboarding), and `deliver` (the
decompose→implement chain per WS-B) — plus one product-journey
`--gate driver-replay` pass as a first `journey-verdict`
(experience-class) pilot, plus a `routing` check running the dev-story
no-LLM routing-tier suite (`kitsoki test routing`). The legacy
landing_freeform/landing_proposal fixtures this check exercises were
triaged in dwf3 (.context/dwf3-routing-triage.md) — every fixture in
`stories/dev-story/intents/*.yaml` now passes `kitsoki test routing
stories/dev-story/app.yaml` cleanly, so this check has no pending-triage
caveat left. Flow counts move fast; run the suite for the current number
rather than trusting any figure written here.

Usage:
  python3 tools/dev-workflow-matrix/run_checks.py                      # run all, write verdicts
  python3 tools/dev-workflow-matrix/run_checks.py --verdicts-dir DIR
  python3 tools/dev-workflow-matrix/run_checks.py --only onboard,fix-bug
  python3 tools/dev-workflow-matrix/run_checks.py --dry-run            # print commands, run nothing
"""
from __future__ import annotations

import argparse
import json
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Callable

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_VERDICTS_DIR = REPO_ROOT / ".artifacts" / "dev-workflow-matrix" / "verdicts"

SCHEMA_VERSION = "1.1.0"


@dataclass(frozen=True)
class CheckDef:
    """One declared check: which matrix cell it evidences, and how to run it."""

    workflow: str
    surface: str
    repo: str
    check_type: str
    command: list[str]
    summary: str


# The check suite. Every entry names the story flow suite (or product-journey
# smoke) that is today's real proof for one matrix cell — transcribed from the
# manifest's own `reason` text so the mapping stays honest and traceable.
CHECKS: list[CheckDef] = [
    CheckDef(
        workflow="onboard",
        surface="tui",
        repo="kitsoki-dev",
        check_type="replay",
        command=["go", "run", "./cmd/kitsoki", "test", "flows", "stories/dev-story/app.yaml"],
        summary="dev-story flow suite (init rooms; onboarding's TUI proof)",
    ),
    CheckDef(
        workflow="prd-proposal",
        surface="tui",
        repo="kitsoki-dev",
        check_type="replay",
        command=["go", "run", "./cmd/kitsoki", "test", "flows", "stories/prd/app.yaml"],
        summary="prd flow suite (33 flows)",
    ),
    CheckDef(
        workflow="fix-bug",
        surface="tui",
        repo="kitsoki-dev",
        check_type="replay",
        command=["go", "run", "./cmd/kitsoki", "test", "flows", "stories/bugfix/app.yaml"],
        summary="bugfix flow suite (full glob, triage mode included)",
    ),
    CheckDef(
        workflow="decompose-implement",
        surface="tui",
        repo="kitsoki-dev",
        check_type="replay",
        command=["go", "run", "./cmd/kitsoki", "test", "flows", "stories/deliver/app.yaml"],
        summary="deliver flow suite (11 flows; WS-B B1 decomposition chain candidate)",
    ),
    CheckDef(
        workflow="routing",
        surface="tui",
        repo="kitsoki-dev",
        check_type="replay",
        command=["go", "run", "./cmd/kitsoki", "test", "routing", "stories/dev-story/app.yaml"],
        summary="dev-story no-LLM routing-tier suite (landing_freeform + landing_proposal_routing + workflow_core5_routing; triaged in dwf3)",
    ),
    CheckDef(
        workflow="fix-bug",
        surface="vscode",
        repo="kitsoki-dev",
        check_type="journey-verdict",
        command=[
            "python3",
            "tools/product-journey/run.py",
            "--gate",
            "driver-replay",
            "--smoke-scenario",
            "bugfix",
        ],
        summary="product-journey driver-replay gate (bugfix scenario, vscode-surfaced driver)",
    ),
]


RunFn = Callable[[list[str], Path], "subprocess.CompletedProcess[str]"]


def default_run(command: list[str], cwd: Path) -> "subprocess.CompletedProcess[str]":
    return subprocess.run(
        command, cwd=cwd, capture_output=True, text=True, check=False
    )


def _flow_suite_verdict(proc: "subprocess.CompletedProcess[str]") -> tuple[str, str, str]:
    """(verdict, health, summary) for a `test flows` invocation."""
    if proc.returncode == 0:
        return "solved", "model:result", "flow suite passed"
    tail = (proc.stdout or proc.stderr or "").strip().splitlines()
    last_line = tail[-1] if tail else f"exit {proc.returncode}"
    return "failed", "model:result", f"flow suite failed: {last_line}"


def _driver_replay_smoke_verdict(
    proc: "subprocess.CompletedProcess[str]", repo_root: Path
) -> tuple[str, str, str, list[str]]:
    """(verdict, health, summary, evidence_refs) for a --gate driver-replay run.

    `--gate driver-replay` (deprecated alias: `--driver-replay-smoke`) prints
    an `Artifacts: <smoke_dir>` line (see `tools/product-journey/run.py`'s
    gate dispatcher); `build_driver_replay_smoke` always writes its report to
    `<smoke_dir>/driver-replay-smoke.json` (`report["artifacts"]["report"]`),
    so this derives the report path from that one stdout line rather than
    re-implementing the freshest-dir lookup. If nothing legible is found, this
    is an infra signal, not a model result.
    """
    if proc.returncode != 0:
        tail = (proc.stdout or proc.stderr or "").strip().splitlines()
        last_line = tail[-1] if tail else f"exit {proc.returncode}"
        return "blocked", "infra:harness", f"smoke crashed: {last_line}", []

    report_path = None
    for line in (proc.stdout or "").splitlines():
        line = line.strip()
        if line.startswith("Artifacts:"):
            candidate = Path(line.split("Artifacts:", 1)[1].strip()) / "driver-replay-smoke.json"
            if candidate.exists():
                report_path = candidate
            break
    if report_path is None:
        return "blocked", "infra:missing-completion-state", "smoke ran but report path not found in stdout", []

    try:
        report = json.loads(report_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as err:
        return "blocked", "infra:completion-state-malformed", f"unreadable report: {err}", []

    status = report.get("status")
    verdict = "solved" if status == "passed" else "failed"
    evidence = [str(report_path)]
    return verdict, "model:result", f"driver-replay smoke {status}", evidence


def run_check(check: CheckDef, repo_root: Path, run_fn: RunFn) -> dict:
    proc = run_fn(check.command, repo_root)
    evidence_refs: list[str] = []
    if check.check_type == "journey-verdict":
        verdict, health, summary, evidence_refs = _driver_replay_smoke_verdict(proc, repo_root)
    else:
        verdict, health, summary = _flow_suite_verdict(proc)

    return {
        "schema_version": SCHEMA_VERSION,
        "verdict": verdict,
        "health": health,
        "check_type": check.check_type,
        "target_id": check.repo,
        "axis": {"workflow": check.workflow, "surface": check.surface},
        "metrics": {},
        "evidence_refs": evidence_refs,
        "summary": f"{check.summary}: {summary}",
    }


def verdict_filename(check: CheckDef) -> str:
    return f"{check.workflow}__{check.surface}__{check.repo}__{check.check_type}.json"


def run_all(
    checks: list[CheckDef],
    repo_root: Path,
    verdicts_dir: Path,
    run_fn: RunFn = default_run,
    dry_run: bool = False,
) -> list[dict]:
    verdicts_dir.mkdir(parents=True, exist_ok=True)
    results = []
    for check in checks:
        if dry_run:
            print(f"[dry-run] {' '.join(check.command)}  (cwd={repo_root})")
            continue
        payload = run_check(check, repo_root, run_fn)
        out_path = verdicts_dir / verdict_filename(check)
        out_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(
            f"[dev-workflow-matrix] {check.workflow} x {check.surface} x {check.repo} "
            f"({check.check_type}): {payload['verdict']} -> {out_path}"
        )
        results.append(payload)
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--verdicts-dir", type=Path, default=DEFAULT_VERDICTS_DIR)
    parser.add_argument(
        "--only",
        default="",
        help="comma-separated workflow ids to restrict to (default: all declared checks)",
    )
    parser.add_argument("--dry-run", action="store_true", help="print commands, run nothing")
    args = parser.parse_args(argv)

    checks = CHECKS
    if args.only:
        wanted = {w.strip() for w in args.only.split(",") if w.strip()}
        checks = [c for c in CHECKS if c.workflow in wanted]

    run_all(checks, args.repo_root, args.verdicts_dir, dry_run=args.dry_run)
    # This runner always exits 0 for a completed pass (including model
    # `failed` verdicts, which are real signal, not an infra break); the gate
    # decision belongs to `generate.py --gate`, which compares these verdicts
    # against the manifest's claimed statuses.
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""docs_fidelity.py — WS-G G1 `docs-fidelity` experience-check runner.

Plan: `.context/dev-workflows-surface-matrix-plan.md` §WS-G, G1/G3. A
`docs-fidelity` check dispatches a persona agent whose ONLY map is one
published doc (no repo spelunking, no tribal knowledge) at a
`(workflow, surface, repo)` matrix cell, collects a per-claim
truthful/stale/missing score plus an overall pass, and writes one
`schemas/completion-state.schema.json`-conformant verdict file with
`check_type: "docs-fidelity"` — the same writer contract `run_checks.py` uses
for `replay`/`journey-verdict`, so `generate.py`'s `--verdicts-dir` ingestion
picks it straight into the matrix's experience column.

Home: this is a plain sibling of `run_checks.py` in
`tools/dev-workflow-matrix/`, NOT a `tools/arena` plugin. Arena's
`docs-fidelity` (`tools/arena/arena/checks.py`) is a declared-but-unimplemented
placeholder, and its `ux-heuristic`/`journey-verdict` are pure FILE-ADAPTERS
that read an externally-produced `verdict.json` off disk — arena has no
runner of its own for either type. This module (and `ux_heuristic.py`) IS
that runner: it does the persona dispatch and writes the completion-state
verdict directly, matching how `run_checks.py` already writes `replay`/
`journey-verdict` verdicts for this same matrix without going through arena's
container executor.

Agent dispatch is injected (`agent_dispatch.DispatchFn`, AGENTS.md DI
discipline): production runs use `agent_dispatch.claude_cli_dispatch`; tests
inject a scripted fake that returns canned claim-scores, so this module's
tests never touch a real LLM, network, or subprocess (this file has NO test
runs of its own beyond `docs_fidelity_test.py`, which is 100% offline).

Usage:
  python3 tools/dev-workflow-matrix/docs_fidelity.py --list
  python3 tools/dev-workflow-matrix/docs_fidelity.py --dry-run
  python3 tools/dev-workflow-matrix/docs_fidelity.py                      # dispatch for real (LIVE, costs an agent turn)
  python3 tools/dev-workflow-matrix/docs_fidelity.py --only onboard
  python3 tools/dev-workflow-matrix/docs_fidelity.py --verdicts-dir DIR

See `tools/dev-workflow-matrix/README.md` for how a real (cassette-recorded,
plan G5) live run is expected to work — this file's own CLI never records a
cassette itself; that recording discipline lives at the dispatch layer/harness
level the way other live-gated tools in this repo do.
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

# Sibling-module imports (`agent_dispatch`, shared with `ux_heuristic.py`): make
# sure this file's own directory is importable BEFORE the import runs, since
# tests load this module directly via importlib.util.spec_from_file_location
# (same pattern as run_checks_test.py) rather than via package machinery.
_THIS_DIR = Path(__file__).resolve().parent
if str(_THIS_DIR) not in sys.path:
    sys.path.insert(0, str(_THIS_DIR))

from agent_dispatch import AgentDispatchError, DispatchFn, claude_cli_dispatch, extract_json_object  # noqa: E402

# read-only inspection (Read/Grep/Glob): enough to check the doc's claims
# against what exists, without the unbounded execute-the-doc behavior that
# hung the first live run for the full 900s (and no project MCP startup)
def _production_dispatch(prompt: str, cwd: Path) -> str:
    return claude_cli_dispatch(prompt, cwd, tools=("Read", "Grep", "Glob"))


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_VERDICTS_DIR = REPO_ROOT / ".artifacts" / "dev-workflow-matrix" / "verdicts"
SCHEMA_VERSION = "1.1.0"
CHECK_TYPE = "docs-fidelity"


@dataclass(frozen=True)
class DocsFidelityCheck:
    """One declared docs-fidelity cell: which doc a persona's map is limited to."""

    workflow: str
    surface: str
    repo: str
    doc_path: str  # repo-relative path to the canonical doc (plan G3)
    persona: str = "docs-minded-contributor"
    summary: str = ""


# WS-G G3: canonical docs per workflow. Scoped to the cells the plan names as
# the natural first docs-fidelity targets (onboarding + fix-a-bug on
# gears-rust, per plan §3 Phase 2/G2) — grows as more `docs/` narratives land.
#
# G2 grid growth: a canonical doc now exists for all 5 workflows
# (docs/workflows/*.md + docs/project-onboarding.md), so every workflow gets
# at least one declared docs-fidelity cell on its persona-assigned surface
# (plan §WS-G G2: core-maintainer -> gh-agent+tui, dependency-debugger ->
# tui+web, docs-minded-contributor -> web (docs-fidelity itself applies to
# every cell, not just this persona's home surface), IDE-first -> vscode+web).
# A doc's claims are read-only text/repo-structure checks that don't depend
# on which repo instance is driving it, so the gears-rust acid-test repo gets
# the same doc wherever gears-rust is already exercising that workflow
# (manifest.yaml) rather than every workflow at once — declaring is free
# (dispatch only happens under --only), so these are safe to grow ahead of a
# live run.
CHECKS: list[DocsFidelityCheck] = [
    DocsFidelityCheck(
        workflow="onboard",
        surface="tui",
        repo="gears-rust",
        doc_path="docs/project-onboarding.md",
        persona="docs-minded-contributor",
        summary="onboarding narrative doc, followed with no repo spelunking",
    ),
    DocsFidelityCheck(
        workflow="fix-bug",
        surface="tui",
        repo="kitsoki-dev",
        doc_path="docs/workflows/fix-a-bug.md",
        persona="dependency-debugger",
        summary="canonical fix-a-bug workflow doc, followed with no repo spelunking",
    ),
    DocsFidelityCheck(
        workflow="fix-bug",
        surface="tui",
        repo="gears-rust",
        doc_path="docs/workflows/fix-a-bug.md",
        persona="dependency-debugger",
        summary="same fix-a-bug doc, judged against the gears-rust acid-test repo (already onboard-proven on tui)",
    ),
    DocsFidelityCheck(
        workflow="prd-proposal",
        surface="web",
        repo="kitsoki-dev",
        doc_path="docs/workflows/prd-and-design.md",
        persona="docs-minded-contributor",
        summary="canonical prd-and-design workflow doc, followed with no repo spelunking",
    ),
    DocsFidelityCheck(
        workflow="decompose-implement",
        surface="tui",
        repo="kitsoki-dev",
        doc_path="docs/workflows/decompose-and-implement.md",
        persona="core-maintainer",
        summary="canonical decompose-and-implement workflow doc, followed with no repo spelunking",
    ),
    DocsFidelityCheck(
        workflow="file-bug",
        surface="web",
        repo="kitsoki-dev",
        doc_path="docs/workflows/file-a-bug.md",
        persona="docs-minded-contributor",
        summary="canonical file-a-bug workflow doc, followed with no repo spelunking",
    ),
]


def build_prompt(check: DocsFidelityCheck, doc_text: str) -> str:
    return f"""You are the persona "{check.persona}" attempting the "{check.workflow}" \
workflow on the "{check.surface}" surface of the "{check.repo}" repo. The \
document quoted below is your ONLY map for HOW to do the workflow — no tribal \
knowledge, no guessing at intent. You may inspect the repository READ-ONLY \
(Read/Grep/Glob) to check whether the doc's claims match what actually exists \
(files, commands, flags, story/room names), but you must NOT execute any \
command, build anything, or modify any file. Read the doc end to end and list \
EVERY factual or actionable claim it makes about how to accomplish this \
workflow (a command to run, a UI element to click, a file to create, a \
sequence of steps). Score each claim exactly one of:
  - "truthful": what it references exists and matches the doc as written.
  - "stale": it describes something that no longer exists or contradicts reality.
  - "missing": a step you needed was not covered by the doc at all.

Your ENTIRE final response must be a single JSON object — no preamble, no \
prose before or after — of this exact shape:
{{"claims": [{{"claim": "<quoted or paraphrased claim>", "score": "truthful|stale|missing", "note": "<why>"}}, ...], "overall_pass": true|false, "summary": "<one line>"}}

DOCUMENT ({check.doc_path}):
---
{doc_text}
---
"""


def _verdict_payload(
    check: DocsFidelityCheck,
    *,
    verdict: str,
    health: str,
    summary: str,
    claims: list[dict] | None = None,
    overall_pass: bool | None = None,
) -> dict:
    payload: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "check_type": CHECK_TYPE,
        "verdict": verdict,
        "health": health,
        "job_type": "dev-workflow-matrix",
        "target_id": check.repo,
        "axis": {"workflow": check.workflow, "surface": check.surface},
        "metrics": {},
        "evidence_refs": [check.doc_path],
        "summary": summary,
    }
    if claims is not None:
        payload["claims"] = claims
    if overall_pass is not None:
        payload["overall_pass"] = overall_pass
    return payload


def run_check(check: DocsFidelityCheck, repo_root: Path, dispatch: DispatchFn = _production_dispatch) -> dict:
    """Run one docs-fidelity check, returning a completion-state verdict dict.

    Never raises: a missing doc, a dispatch failure, or an unparsable agent
    response all fold into an honest `blocked`/`failed` verdict rather than
    crashing the runner — the same "never a fake green, never a crash"
    discipline `run_checks.py` and arena's file-adapter checks follow.
    """
    doc_full = repo_root / check.doc_path
    if not doc_full.exists():
        return _verdict_payload(
            check,
            verdict="failed",
            health="model:result",
            summary=f"doc {check.doc_path} does not exist — a missing canonical doc IS a docs-fidelity failure (plan G3)",
            claims=[],
            overall_pass=False,
        )

    doc_text = doc_full.read_text(encoding="utf-8")
    prompt = build_prompt(check, doc_text)
    try:
        raw = dispatch(prompt, repo_root)
        parsed = extract_json_object(raw)
    except AgentDispatchError as err:
        return _verdict_payload(
            check,
            verdict="blocked",
            health="infra:harness",
            summary=f"docs-fidelity agent dispatch failed: {err}",
        )

    claims = parsed.get("claims") or []
    overall_pass = bool(parsed.get("overall_pass", False))
    stale_or_missing = [c for c in claims if isinstance(c, dict) and c.get("score") in ("stale", "missing")]
    verdict = "solved" if overall_pass and not stale_or_missing else "failed"
    summary = parsed.get("summary") or (
        f"{len(stale_or_missing)}/{len(claims)} doc claims stale/missing"
    )
    return _verdict_payload(
        check,
        verdict=verdict,
        health="model:result",
        summary=summary,
        claims=claims,
        overall_pass=overall_pass,
    )


def verdict_filename(check: DocsFidelityCheck) -> str:
    return f"{check.workflow}__{check.surface}__{check.repo}__{CHECK_TYPE}.json"


def run_all(
    checks: list[DocsFidelityCheck],
    repo_root: Path,
    verdicts_dir: Path,
    dispatch: DispatchFn = _production_dispatch,
    dry_run: bool = False,
) -> list[dict]:
    verdicts_dir.mkdir(parents=True, exist_ok=True)
    results = []
    for check in checks:
        if dry_run:
            print(f"[dry-run] docs-fidelity: {check.workflow} x {check.surface} x {check.repo} <- {check.doc_path}")
            continue
        payload = run_check(check, repo_root, dispatch)
        out_path = verdicts_dir / verdict_filename(check)
        out_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(
            f"[dev-workflow-matrix] docs-fidelity {check.workflow} x {check.surface} x {check.repo}: "
            f"{payload['verdict']} -> {out_path}"
        )
        results.append(payload)
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--verdicts-dir", type=Path, default=DEFAULT_VERDICTS_DIR)
    parser.add_argument("--only", default="", help="comma-separated workflow ids to restrict to")
    parser.add_argument("--dry-run", action="store_true", help="print what would be dispatched; dispatch nothing")
    parser.add_argument(
        "--list",
        action="store_true",
        help="enumerate declared docs-fidelity checks (workflow/surface/repo/doc) and exit; never dispatches",
    )
    args = parser.parse_args(argv)

    checks = CHECKS
    if args.only:
        wanted = {w.strip() for w in args.only.split(",") if w.strip()}
        checks = [c for c in CHECKS if c.workflow in wanted]

    if args.list:
        for check in checks:
            print(f"docs-fidelity  {check.workflow:<24} {check.surface:<8} {check.repo:<12} {check.doc_path}")
        return 0

    run_all(checks, args.repo_root, args.verdicts_dir, dry_run=args.dry_run)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

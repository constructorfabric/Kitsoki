#!/usr/bin/env python3
"""ux_heuristic.py — WS-G G1 `ux-heuristic` experience-check runner.

Plan: `.context/dev-workflows-surface-matrix-plan.md` §WS-G, G1. A
`ux-heuristic` check takes already-captured evidence for one matrix cell —
web screenshots, `render_tui_png` output, or a VS Code webview capture — plus
a heuristic catalog, dispatches a vision-critique agent against them, and
writes one `schemas/completion-state.schema.json`-conformant verdict with
`check_type: "ux-heuristic"`, exactly the writer contract `run_checks.py` and
`docs_fidelity.py` use so `generate.py --verdicts-dir` folds it into the
matrix's experience column.

Catalog reuse (plan G1: "the kitsoki-ui-review pattern"): this module reuses
`.agents/skills/kitsoki-ui-review/heuristics.yaml` as-is rather than
inventing a second catalog — same `checks:` list (id/title/severity/nielsen/
look_for/not_this), same "error always blocks, warn only under --strict, info
never blocks" severity discipline, and the same "ground every finding in a
literal, cited frame" instruction baked into the prompt. This runner widens
that skill's web-only tour capture to any surface's captured frames (TUI
`render_tui_png` output, VS Code webview screenshots), per plan G1's
"extended beyond web to TUI transcripts/`render_tui_png` and the VS Code
webview" language — it does not re-run the skill's own capture pipeline.

Home + DI: a plain sibling of `run_checks.py`/`docs_fidelity.py` in
`tools/dev-workflow-matrix/`, not a `tools/arena` plugin (arena's
`ux-heuristic` is a FILE-ADAPTER that reads an externally-produced
`verdict.json`; this module is that external producer for the dev-workflow
matrix's own cells). Agent dispatch is injected via
`agent_dispatch.DispatchFn` — production runs shell out to a real vision
agent; tests inject a scripted fake, so this module's own tests never touch a
real LLM, network, or subprocess.

Usage:
  python3 tools/dev-workflow-matrix/ux_heuristic.py --list
  python3 tools/dev-workflow-matrix/ux_heuristic.py --dry-run
  python3 tools/dev-workflow-matrix/ux_heuristic.py                       # dispatch for real (LIVE, costs an agent turn)
  python3 tools/dev-workflow-matrix/ux_heuristic.py --only fix-bug
  python3 tools/dev-workflow-matrix/ux_heuristic.py --verdicts-dir DIR
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_THIS_DIR = Path(__file__).resolve().parent
if str(_THIS_DIR) not in sys.path:
    sys.path.insert(0, str(_THIS_DIR))

from agent_dispatch import AgentDispatchError, DispatchFn, claude_cli_dispatch, extract_json_object  # noqa: E402

# Read only: the vision critique must open the frame files, nothing else
def _production_dispatch(prompt: str, cwd: Path) -> str:
    return claude_cli_dispatch(prompt, cwd, tools=("Read",))


try:
    import yaml
except ImportError:  # pragma: no cover - yaml is a repo-wide dependency already
    yaml = None

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_VERDICTS_DIR = REPO_ROOT / ".artifacts" / "dev-workflow-matrix" / "verdicts"
DEFAULT_CATALOG_PATH = REPO_ROOT / ".agents" / "skills" / "kitsoki-ui-review" / "heuristics.yaml"
SCHEMA_VERSION = "1.1.0"
CHECK_TYPE = "ux-heuristic"


@dataclass(frozen=True)
class UxHeuristicCheck:
    """One declared ux-heuristic cell: which captured frames a vision agent judges."""

    workflow: str
    surface: str
    repo: str
    frame_paths: tuple[str, ...]  # repo-relative capture paths (screenshots/PNGs/transcripts)
    persona: str = "IDE-first"
    summary: str = ""


# WS-G G1/G2: the surfaces the plan calls out by name for ux-heuristic beyond
# the web tour review (`render_tui_png` for TUI, a VS Code webview capture).
# Frame paths point at where each surface's capture pipeline is documented to
# land its output; a missing frame is an honest `blocked`, never a fake green.
CHECKS: list[UxHeuristicCheck] = [
    UxHeuristicCheck(
        workflow="fix-bug",
        surface="tui",
        repo="kitsoki-dev",
        frame_paths=(".artifacts/dev-workflow-matrix/frames/fix-bug-tui.png",),
        persona="dependency-debugger",
        summary="bugfix TUI render_tui_png capture, judged against the shared heuristic catalog",
    ),
    UxHeuristicCheck(
        workflow="fix-bug",
        surface="vscode",
        repo="kitsoki-dev",
        frame_paths=(".artifacts/dev-workflow-matrix/frames/fix-bug-vscode.png",),
        persona="IDE-first",
        summary="bugfix VS Code webview capture, judged against the shared heuristic catalog",
    ),
]


def load_catalog(catalog_path: Path = DEFAULT_CATALOG_PATH) -> list[dict]:
    """Load the shared kitsoki-ui-review heuristic catalog's `checks:` list."""
    if yaml is None:
        raise RuntimeError("pyyaml is required to load the heuristic catalog")
    data = yaml.safe_load(catalog_path.read_text(encoding="utf-8"))
    return (data or {}).get("checks") or []


def build_prompt(check: UxHeuristicCheck, catalog: list[dict], frame_paths: list[Path]) -> str:
    catalog_lines = []
    for entry in catalog:
        catalog_lines.append(
            f"- [{entry.get('id')}] ({entry.get('severity')}, Nielsen #{entry.get('nielsen')}) "
            f"{entry.get('title')}: look for {entry.get('look_for', '').strip()} — NOT: "
            f"{entry.get('not_this', '').strip()}"
        )
    catalog_text = "\n".join(catalog_lines) if catalog_lines else "(no catalog entries loaded)"
    frames_text = "\n".join(f"- {p}" for p in frame_paths)
    return f"""You are a UX critic playing the persona "{check.persona}" who just walked the \
"{check.workflow}" workflow on the "{check.surface}" surface of "{check.repo}". Open EACH \
captured frame listed below and critique it against the heuristic catalog. Every \
finding MUST cite the frame filename and quote what is literally visible — no \
speculation about frames you did not open.

CAPTURED FRAMES:
{frames_text}

HEURISTIC CATALOG:
{catalog_text}

Respond with ONLY a JSON object, no prose before or after, of this exact shape:
{{"findings": [{{"id": "<catalog id>", "frame": "<frame path>", "severity": "error|warn|info", "summary": "<literal, cited observation>"}}, ...], "overall_pass": true|false, "summary": "<one line>"}}
"""


def _verdict_payload(
    check: UxHeuristicCheck,
    *,
    verdict: str,
    health: str,
    summary: str,
    findings: list[dict] | None = None,
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
        "evidence_refs": list(check.frame_paths),
        "summary": summary,
    }
    if findings is not None:
        payload["findings"] = findings
    if overall_pass is not None:
        payload["overall_pass"] = overall_pass
    return payload


def run_check(
    check: UxHeuristicCheck,
    repo_root: Path,
    dispatch: DispatchFn = _production_dispatch,
    catalog_path: Path = DEFAULT_CATALOG_PATH,
) -> dict:
    """Run one ux-heuristic check, returning a completion-state verdict dict.

    Never raises: missing frames, a missing/unreadable catalog, a dispatch
    failure, or an unparsable agent response all fold into an honest
    `blocked`/`failed` verdict — captured evidence is a precondition this
    runner checks itself rather than trusting the agent to notice.
    """
    missing = [p for p in check.frame_paths if not (repo_root / p).exists()]
    if missing:
        return _verdict_payload(
            check,
            verdict="blocked",
            health="infra:missing-evidence",
            summary=f"captured frame(s) not found: {', '.join(missing)} — run the surface's capture pipeline first",
        )

    try:
        catalog = load_catalog(catalog_path)
    except (OSError, RuntimeError) as err:
        return _verdict_payload(
            check,
            verdict="blocked",
            health="infra:harness",
            summary=f"could not load heuristic catalog at {catalog_path}: {err}",
        )

    frame_paths = [repo_root / p for p in check.frame_paths]
    prompt = build_prompt(check, catalog, frame_paths)
    try:
        raw = dispatch(prompt, repo_root)
        parsed = extract_json_object(raw)
    except AgentDispatchError as err:
        return _verdict_payload(
            check,
            verdict="blocked",
            health="infra:harness",
            summary=f"ux-heuristic agent dispatch failed: {err}",
        )

    findings = parsed.get("findings") or []
    overall_pass = bool(parsed.get("overall_pass", False))
    error_findings = [f for f in findings if isinstance(f, dict) and f.get("severity") == "error"]
    # Authoritative gate (mirrors kitsoki-ui-review stage 3): an `error`
    # finding always fails the check, regardless of what the agent claims
    # `overall_pass` is — this runner recomputes pass/fail from severities,
    # it does not trust any model's self-reported verdict.
    verdict = "solved" if overall_pass and not error_findings else "failed"
    summary = parsed.get("summary") or (
        f"{len(error_findings)} error-severity finding(s) of {len(findings)} total"
    )
    return _verdict_payload(
        check,
        verdict=verdict,
        health="model:result",
        summary=summary,
        findings=findings,
        overall_pass=overall_pass,
    )


def verdict_filename(check: UxHeuristicCheck) -> str:
    return f"{check.workflow}__{check.surface}__{check.repo}__{CHECK_TYPE}.json"


def run_all(
    checks: list[UxHeuristicCheck],
    repo_root: Path,
    verdicts_dir: Path,
    dispatch: DispatchFn = _production_dispatch,
    catalog_path: Path = DEFAULT_CATALOG_PATH,
    dry_run: bool = False,
) -> list[dict]:
    verdicts_dir.mkdir(parents=True, exist_ok=True)
    results = []
    for check in checks:
        if dry_run:
            print(
                f"[dry-run] ux-heuristic: {check.workflow} x {check.surface} x {check.repo} "
                f"<- {', '.join(check.frame_paths)}"
            )
            continue
        payload = run_check(check, repo_root, dispatch, catalog_path)
        out_path = verdicts_dir / verdict_filename(check)
        out_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(
            f"[dev-workflow-matrix] ux-heuristic {check.workflow} x {check.surface} x {check.repo}: "
            f"{payload['verdict']} -> {out_path}"
        )
        results.append(payload)
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--verdicts-dir", type=Path, default=DEFAULT_VERDICTS_DIR)
    parser.add_argument("--catalog", type=Path, default=DEFAULT_CATALOG_PATH)
    parser.add_argument("--only", default="", help="comma-separated workflow ids to restrict to")
    parser.add_argument("--dry-run", action="store_true", help="print what would be dispatched; dispatch nothing")
    parser.add_argument(
        "--list",
        action="store_true",
        help="enumerate declared ux-heuristic checks (workflow/surface/repo/frames) and exit; never dispatches",
    )
    args = parser.parse_args(argv)

    checks = CHECKS
    if args.only:
        wanted = {w.strip() for w in args.only.split(",") if w.strip()}
        checks = [c for c in CHECKS if c.workflow in wanted]

    if args.list:
        for check in checks:
            frames = ", ".join(check.frame_paths)
            print(f"ux-heuristic   {check.workflow:<24} {check.surface:<8} {check.repo:<12} {frames}")
        return 0

    run_all(checks, args.repo_root, args.verdicts_dir, catalog_path=args.catalog, dry_run=args.dry_run)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

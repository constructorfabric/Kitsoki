#!/usr/bin/env python3
"""Write deterministic Cherny-loop report artifacts."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path


def _slug(value: str) -> str:
    out = []
    for ch in (value or "").lower():
        if ch.isalnum():
            out.append(ch)
        elif ch in {"-", "_", ".", " ", ":"}:
            out.append("-")
    text = "".join(out).strip("-")
    return re.sub(r"-+", "-", text) or "run"


def _status(reason: str) -> str:
    if reason in {"achieved", "already_met"}:
        return "done"
    if reason in {"iteration_budget", "cost_budget", "aborted"}:
        return "blocked"
    return "pending"


def _build_summary(args: argparse.Namespace) -> dict:
    reason = args.terminal_reason or "pending"
    gate_detail = args.gate_stdout or args.gate_reason or args.last_gate_failure or "No gate output recorded."
    artifacts = [
        {"label": "Goal artifact", "status": "done" if args.goal_artifact else "pending", "detail": "Primary artifact under test.", "ref": args.goal_artifact},
        {"label": "Worktree", "status": "done" if args.worktree_path else "pending", "detail": args.workspace_branch, "ref": args.worktree_path},
        {"label": "Commit", "status": "done" if args.commit_sha else "pending", "detail": "Committed maker work.", "ref": args.commit_sha},
    ]
    return {
        "title": "Cherny Loop Report",
        "summary": f"{reason}; {args.iteration} of {args.iteration_budget} iteration(s)",
        "terminal_reason": reason,
        "goal_text": args.goal_text,
        "goal_artifact": args.goal_artifact,
        "objectives": [
            {"label": "Loop outcome", "status": _status(reason), "detail": f"Terminal reason: {reason}."},
            {"label": "Red-before-green", "status": "done" if args.baseline_checked == "true" else "pending", "detail": "Baseline gate was checked before maker spend."},
            {"label": "Gate", "status": "done" if args.gate_ok == "true" else "blocked", "detail": gate_detail},
            {"label": "Budget", "status": "blocked" if reason in {"iteration_budget", "cost_budget"} else "done", "detail": f"iteration={args.iteration}/{args.iteration_budget}; cost={args.session_cost_usd}/{args.cost_budget_usd}"},
        ],
        "items": [
            {"id": "goal", "status": reason, "owner": "operator", "artifact": args.goal_artifact or args.goal_text},
            {"id": "maker", "status": "done" if args.maker_summary else "pending", "owner": "maker", "artifact": args.maker_summary},
            {"id": "gate", "status": "passed" if args.gate_ok == "true" else "failed", "owner": args.gate_mode, "artifact": gate_detail},
        ],
        "artifacts": artifacts,
        "next_steps": [
            {"label": "Review loop evidence", "detail": "Inspect the linked worktree, commit, and gate output."},
            {"label": "Resume or integrate", "detail": "Achieved runs can be integrated; exhausted or aborted runs need operator review."},
        ],
    }


def _write_markdown(path: Path, summary: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        "# Cherny Loop Report",
        "",
        summary["summary"],
        "",
        "## Goal",
        "",
        summary.get("goal_text") or "(not recorded)",
        "",
        "## Artifacts",
    ]
    for artifact in summary.get("artifacts", []):
        lines.append(f"- {artifact.get('label')}: `{artifact.get('ref', '')}` ({artifact.get('status', '')})")
    lines.extend(["", "## Next steps"])
    for step in summary.get("next_steps", []):
        lines.append(f"- {step.get('label')}: {step.get('detail')}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--goal-text", default="")
    ap.add_argument("--goal-artifact", default="")
    ap.add_argument("--gate-mode", default="")
    ap.add_argument("--gate-command", default="")
    ap.add_argument("--gate-ok", default="")
    ap.add_argument("--gate-stdout", default="")
    ap.add_argument("--gate-reason", default="")
    ap.add_argument("--last-gate-failure", default="")
    ap.add_argument("--baseline-checked", default="")
    ap.add_argument("--iteration", default="0")
    ap.add_argument("--iteration-budget", default="0")
    ap.add_argument("--session-cost-usd", default="0")
    ap.add_argument("--cost-budget-usd", default="0")
    ap.add_argument("--maker-summary", default="")
    ap.add_argument("--terminal-reason", default="")
    ap.add_argument("--worktree-path", default="")
    ap.add_argument("--workspace-branch", default="")
    ap.add_argument("--commit-sha", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or f"{args.terminal_reason or 'run'}-{dt.datetime.now(dt.timezone.utc).strftime('%Y%m%d-%H%M%SZ')}")
    out_dir = Path(args.artifact_root) / "cherny-loop" / run_id
    summary_path = out_dir / "summary.json"
    report_path = out_dir / "report.md"
    deck_path = out_dir / "deck.slidey.json"

    summary = _build_summary(args)
    summary["markdown_path"] = str(report_path)
    summary["summary_path"] = str(summary_path)
    summary["deck_path"] = str(deck_path)

    out_dir.mkdir(parents=True, exist_ok=True)
    summary_path.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    _write_markdown(report_path, summary)

    deck_tool = Path(__file__).resolve().parents[3] / "tools" / "report-deck" / "deterministic_deck.py"
    subprocess.run([
        sys.executable,
        str(deck_tool),
        "--kind", "workflow",
        "--input", str(summary_path),
        "--out", str(deck_path),
    ], check=True, capture_output=True, text=True)

    print(json.dumps({
        "report_path": str(report_path),
        "summary_path": str(summary_path),
        "deck_path": str(deck_path),
    }))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

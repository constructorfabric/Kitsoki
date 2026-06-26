#!/usr/bin/env python3
"""Write deterministic dev-story human-review handoff artifacts.

The script consumes structured world fields and feeds tools/report-deck's
standard workflow builder. It never asks an LLM to write deck content.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


def _slug(value: str) -> str:
    out = []
    for ch in (value or "").lower():
        if ch.isalnum():
            out.append(ch)
        elif ch in {"-", "_", ".", " ", ":"}:
            out.append("-")
    text = "".join(out).strip("-")
    return re.sub(r"-+", "-", text) or "run"


def _status(value: str) -> str:
    value = (value or "").lower()
    if value in {"merged", "shipped", "done", "reviewed"}:
        return "done"
    if value in {"needs-human", "abandoned", "validation_failed", "not-reproducible"}:
        return "blocked"
    if value in {"triaged", "code_ready", "open"}:
        return "pending"
    return "pending"


def _build_summary(args: argparse.Namespace) -> dict[str, Any]:
    reason = args.reason or args.last_error or "Autonomous run stopped before PR-ready completion."
    pr_ref = args.pr_id or args.pr_url
    artifacts = [
        {
            "label": "Ticket",
            "status": "done" if args.ticket_id else "pending",
            "detail": args.ticket_title,
            "ref": args.ticket_id,
        },
        {
            "label": "Pull request",
            "status": "done" if pr_ref else "pending",
            "detail": "PR associated with this handoff.",
            "ref": pr_ref,
        },
        {
            "label": "Issue comment",
            "status": "done" if args.issue_comment_id else "pending",
            "detail": "External handoff comment.",
            "ref": args.issue_comment_id,
        },
    ]
    return {
        "title": "dev-story Human Review Handoff",
        "summary": f"{args.status or 'unknown'}: {reason}",
        "ticket_id": args.ticket_id,
        "ticket_title": args.ticket_title,
        "status": args.status,
        "reason": reason,
        "pr_id": args.pr_id,
        "pr_url": args.pr_url,
        "objectives": [
            {"label": "Autonomous run", "status": _status(args.status), "detail": f"Stopped with status {args.status or 'unknown'}."},
            {"label": "Reason captured", "status": "done" if reason else "blocked", "detail": reason},
            {"label": "External issue handoff", "status": "done" if args.issue_comment_id or args.judge_mode == "human" else "pending", "detail": "Human mode is local-only; autonomous modes post issue comments."},
            {"label": "PR handoff", "status": "done" if args.pr_comment_ok or not args.pr_id else "pending", "detail": "PR comment posted when an autonomous PR exists."},
        ],
        "items": [
            {"id": "ticket", "status": args.status or "pending", "owner": "dev-story", "artifact": args.ticket_id},
            {"id": "pr", "status": "linked" if pr_ref else "pending", "owner": "vcs", "artifact": pr_ref},
            {"id": "reason", "status": "captured", "owner": "dev-story", "artifact": reason},
        ],
        "artifacts": artifacts,
        "next_steps": [
            {"label": "Review reason", "detail": reason},
            {"label": "Resume or close", "detail": "Use the linked ticket/PR and this deck to decide whether to resume automation or close manually."},
        ],
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        "# dev-story Human Review Handoff",
        "",
        summary.get("summary", ""),
        "",
        f"- Ticket: `{summary.get('ticket_id', '')}` {summary.get('ticket_title', '')}",
        f"- Status: `{summary.get('status', '')}`",
        f"- PR: `{summary.get('pr_id') or summary.get('pr_url') or ''}`",
        "",
        "## Reason",
        "",
        summary.get("reason", ""),
        "",
        "## Next steps",
    ]
    for step in summary.get("next_steps", []):
        lines.append(f"- {step.get('label')}: {step.get('detail')}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ticket-id", default="")
    ap.add_argument("--ticket-title", default="")
    ap.add_argument("--status", default="")
    ap.add_argument("--reason", default="")
    ap.add_argument("--last-error", default="")
    ap.add_argument("--pr-id", default="")
    ap.add_argument("--pr-url", default="")
    ap.add_argument("--judge-mode", default="")
    ap.add_argument("--issue-comment-id", default="")
    ap.add_argument("--pr-comment-ok", default="false")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id_seed = args.run_id or args.ticket_id or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ")
    out_dir = Path(args.artifact_root) / "dev-story-human-review" / _slug(run_id_seed)
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

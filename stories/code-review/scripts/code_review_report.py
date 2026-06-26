#!/usr/bin/env python3
"""Write deterministic code-review report artifacts."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
from pathlib import Path


def _slug(value: str) -> str:
    chars = []
    for ch in (value or "").lower():
        if ch.isalnum():
            chars.append(ch)
        elif ch in {"-", "_", ".", " ", ":"}:
            chars.append("-")
    return re.sub(r"-+", "-", "".join(chars).strip("-")) or "run"


def _build_summary(args: argparse.Namespace) -> dict:
    title = f"Code Review: {args.pr_id or args.pr_title or 'PR'}"
    decision = args.decision or "reviewed"
    objectives = [
        {"label": "Load PR", "status": "done" if args.pr_id else "pending", "detail": args.pr_title},
        {"label": "Review diff", "status": "done" if args.review_summary else "pending", "detail": args.review_title},
        {"label": "Draft comment", "status": "done" if args.draft_comment else "pending", "detail": args.draft_comment[:240]},
        {"label": "Post final decision", "status": "done", "detail": decision},
        {"label": "Report deck", "status": "done", "detail": "Generated from structured review artifacts."},
    ]
    artifacts = [
        {"label": "PR", "status": "done" if args.pr_id else "pending", "ref": args.pr_url or args.pr_id},
        {"label": "Review summary", "status": "done" if args.review_summary else "pending", "ref": args.review_summary},
        {"label": "Decision artifact", "status": "done" if args.decision_summary else "pending", "ref": args.decision_summary},
    ]
    items = [
        {"id": "review", "status": "done" if args.review_summary else "pending", "owner": "reviewer", "artifact": args.review_title},
        {"id": "decision", "status": decision, "owner": "reviewer", "artifact": args.decision_title},
    ]
    next_steps = [{"label": "Follow PR response", "detail": "Watch for author replies if changes were requested."}]
    if decision == "approve":
        next_steps = [{"label": "Let merge policy proceed", "detail": "The PR has reviewer approval."}]
    return {
        "title": title,
        "summary": f"{args.pr_id or 'PR'} reviewed: {decision}.",
        "objectives": objectives,
        "artifacts": artifacts,
        "items": items,
        "next_steps": next_steps,
    }


def _write_markdown(path: Path, summary: dict) -> None:
    lines = ["# Code Review Report", "", summary["summary"], "", "## Objectives"]
    for item in summary.get("objectives", []):
        lines.append(f"- {item.get('label')}: {item.get('status')} - {item.get('detail')}")
    lines.extend(["", "## Artifacts"])
    for item in summary.get("artifacts", []):
        lines.append(f"- {item.get('label')}: `{item.get('ref', '')}`")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--pr-id", default="")
    ap.add_argument("--pr-title", default="")
    ap.add_argument("--pr-url", default="")
    ap.add_argument("--decision", default="")
    ap.add_argument("--review-title", default="")
    ap.add_argument("--review-summary", default="")
    ap.add_argument("--decision-title", default="")
    ap.add_argument("--decision-summary", default="")
    ap.add_argument("--draft-comment", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or args.pr_id or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ"))
    out_dir = Path(args.artifact_root) / "code-review" / run_id
    report_path = out_dir / "report.md"
    summary_path = out_dir / "summary.json"
    deck_path = out_dir / "deck.slidey.json"

    summary = _build_summary(args)
    summary["report_path"] = str(report_path)
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
        "summary": summary["summary"],
    }))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

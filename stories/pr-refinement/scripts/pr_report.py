#!/usr/bin/env python3
"""Write deterministic PR-refinement close-out report artifacts."""

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
    chars: list[str] = []
    for ch in (value or "").lower():
        if ch.isalnum():
            chars.append(ch)
        elif ch in {"-", "_", ".", " ", ":", "/"}:
            chars.append("-")
    return re.sub(r"-+", "-", "".join(chars).strip("-")) or "run"


def _jsonish(value: str) -> Any:
    text = (value or "").strip()
    if not text:
        return ""
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return text


def _artifact_title(artifact: dict[str, Any]) -> str:
    return str(artifact.get("summary_title") or artifact.get("root_cause") or "No CI diagnosis recorded")


def _build_summary(args: argparse.Namespace) -> dict[str, Any]:
    diagnose = _jsonish(args.diagnose_artifact)
    diagnose_obj = diagnose if isinstance(diagnose, dict) else {}
    comments = _jsonish(args.pr_comments)
    pr_label = args.pr_id or args.pr_url or args.ticket_id or "PR"
    summary = f"{pr_label} merged at {args.merge_sha or 'unknown SHA'}."
    if args.ci_state:
        summary += f" CI ended {args.ci_state}."
    objectives = [
        {"label": "Open pull request", "status": "done" if args.pr_url else "pending", "detail": args.pr_url},
        {"label": "Stabilize CI", "status": "done" if args.ci_state == "success" else "pending", "detail": args.ci_failed_checks or "green"},
        {"label": "Resolve review comments", "status": "done" if args.pending_comments == "0" else "pending", "detail": f"{args.pending_comments or 0} pending"},
        {"label": "Merge PR", "status": "done" if args.merge_sha else "pending", "detail": args.merge_sha},
        {"label": "Report deck", "status": "done", "detail": "Generated from PR-refinement world state."},
    ]
    artifacts = [
        {"label": "Pull request", "status": "done" if args.pr_url else "pending", "ref": args.pr_url},
        {"label": "Merge SHA", "status": "done" if args.merge_sha else "pending", "ref": args.merge_sha},
        {"label": "CI failed checks", "status": "done" if args.ci_failed_checks else "pending", "ref": args.ci_failed_checks},
        {"label": "Diagnosis artifact", "status": "done" if diagnose_obj else "pending", "ref": _artifact_title(diagnose_obj)},
        {"label": "Review comments", "status": "done" if comments else "pending", "ref": comments if isinstance(comments, str) else json.dumps(comments, sort_keys=True)},
    ]
    items = [
        {"id": "pr", "status": "merged" if args.merge_sha else "open", "owner": args.feature_branch, "artifact": args.pr_url},
        {"id": "ci", "status": args.ci_state or "unknown", "owner": "pr-refinement", "artifact": args.ci_failed_checks},
        {"id": "comments", "status": "resolved" if args.pending_comments == "0" else "pending", "owner": "pr-refinement", "artifact": args.pr_comments},
        {"id": "diagnosis", "status": "recorded" if diagnose_obj else "not-needed", "owner": "diagnoser", "artifact": _artifact_title(diagnose_obj)},
    ]
    return {
        "title": "PR Refinement Report",
        "summary": summary,
        "ticket_id": args.ticket_id,
        "ticket_title": args.ticket_title,
        "pr_id": args.pr_id,
        "pr_url": args.pr_url,
        "merge_sha": args.merge_sha,
        "objectives": objectives,
        "artifacts": artifacts,
        "items": items,
        "next_steps": [
            {"label": "Review merged PR", "detail": "Confirm the parent story records pr_url, merge_sha, and status."},
            {"label": "Continue parent workflow", "detail": "Let the importing story close tickets, publish summaries, or run delivery-tail as needed."},
        ],
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    lines = [
        "# PR Refinement Report",
        "",
        summary["summary"],
        "",
        f"- Ticket: {summary.get('ticket_id', '')} - {summary.get('ticket_title', '')}",
        f"- PR: {summary.get('pr_url', '')}",
        f"- Merge SHA: `{summary.get('merge_sha', '')}`",
        "",
        "## Objectives",
    ]
    for item in summary.get("objectives", []):
        lines.append(f"- {item.get('label')}: {item.get('status')} - {item.get('detail')}")
    lines.extend(["", "## Artifacts"])
    for item in summary.get("artifacts", []):
        ref = str(item.get("ref", ""))
        lines.append(f"- {item.get('label')}: {ref[:240]}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ticket-id", default="")
    ap.add_argument("--ticket-title", default="")
    ap.add_argument("--feature-branch", default="")
    ap.add_argument("--pr-id", default="")
    ap.add_argument("--pr-url", default="")
    ap.add_argument("--ci-state", default="")
    ap.add_argument("--ci-failed-checks", default="")
    ap.add_argument("--pr-comments", default="")
    ap.add_argument("--pending-comments", default="0")
    ap.add_argument("--diagnose-artifact", default="")
    ap.add_argument("--merge-sha", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or args.pr_id or args.merge_sha or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ"))
    out_dir = Path(args.artifact_root) / "pr-refinement" / run_id
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

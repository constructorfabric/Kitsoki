#!/usr/bin/env python3
"""Write deterministic bugfix close-out report artifacts."""

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


def _json_obj(value: str) -> dict[str, Any]:
    text = (value or "").strip()
    if not text:
        return {}
    try:
        loaded = json.loads(text)
    except json.JSONDecodeError:
        return {}
    return loaded if isinstance(loaded, dict) else {}


def _title(artifact: dict[str, Any], fallback: str) -> str:
    return str(artifact.get("summary_title") or artifact.get("status") or fallback)


def _summary(args: argparse.Namespace) -> dict[str, Any]:
    reproduction = _json_obj(args.reproduction_artifact)
    proposal = _json_obj(args.propose_fix_artifact)
    implementation = _json_obj(args.implement_artifact)
    testing = _json_obj(args.implement_review_artifact)
    validation = _json_obj(args.validate_artifact)
    done = _json_obj(args.done_artifact)
    summary = done.get("summary_title") or f"{args.ticket_id or 'Bug'} fixed at {args.fixed_in_commit or 'unknown commit'}."
    objectives = [
        {"label": "Reproduce bug", "status": "done" if reproduction else "pending", "detail": _title(reproduction, "No reproduction artifact")},
        {"label": "Propose fix", "status": "done" if proposal else "pending", "detail": _title(proposal, "No proposal artifact")},
        {"label": "Apply implementation", "status": "done" if implementation else "pending", "detail": _title(implementation, "No implementation artifact")},
        {"label": "Verify tests", "status": "done" if testing else "pending", "detail": _title(testing, "No testing artifact")},
        {"label": "Validate fix", "status": "done" if validation else "pending", "detail": _title(validation, "No validation artifact")},
        {"label": "Close out", "status": "done" if done else "pending", "detail": _title(done, "No close-out artifact")},
    ]
    artifacts = [
        {"label": "Ticket", "status": "done" if args.ticket_id else "pending", "ref": args.ticket_url or args.ticket_id},
        {"label": "Fixed commit", "status": "done" if args.fixed_in_commit else "pending", "ref": args.fixed_in_commit},
        {"label": "Worktree", "status": "done" if args.workdir else "pending", "ref": args.workdir},
        {"label": "Reproduction", "status": "done" if reproduction else "pending", "ref": _title(reproduction, "")},
        {"label": "Validation", "status": "done" if validation else "pending", "ref": _title(validation, "")},
    ]
    items = [
        {"id": "reproduction", "status": "done" if reproduction else "missing", "owner": "reproducer", "artifact": _title(reproduction, "")},
        {"id": "implementation", "status": "done" if implementation else "missing", "owner": "implementer", "artifact": _title(implementation, "")},
        {"id": "testing", "status": str(testing.get("status") or testing.get("outcome") or ("done" if testing else "missing")), "owner": "test_author", "artifact": _title(testing, "")},
        {"id": "validation", "status": str(validation.get("outcome") or ("done" if validation else "missing")), "owner": "validator", "artifact": _title(validation, "")},
    ]
    return {
        "title": "Bugfix Report",
        "summary": str(summary),
        "ticket_id": args.ticket_id,
        "ticket_title": args.ticket_title,
        "fixed_in_commit": args.fixed_in_commit,
        "objectives": objectives,
        "artifacts": artifacts,
        "items": items,
        "next_steps": [
            {"label": "Review fixed-in ledger", "detail": "Confirm ticket comment records the commit and validation summary."},
            {"label": "Continue parent workflow", "detail": "If imported, let pr-refinement or delivery-tail consume the close-out artifact."},
        ],
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    lines = [
        "# Bugfix Report",
        "",
        summary["summary"],
        "",
        f"- Ticket: {summary.get('ticket_id', '')} - {summary.get('ticket_title', '')}",
        f"- Fixed in: `{summary.get('fixed_in_commit', '')}`",
        "",
        "## Objectives",
    ]
    for item in summary.get("objectives", []):
        lines.append(f"- {item.get('label')}: {item.get('status')} - {item.get('detail')}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ticket-id", default="")
    ap.add_argument("--ticket-title", default="")
    ap.add_argument("--ticket-url", default="")
    ap.add_argument("--workdir", default="")
    ap.add_argument("--fixed-in-commit", default="")
    ap.add_argument("--reproduction-artifact", default="")
    ap.add_argument("--propose-fix-artifact", default="")
    ap.add_argument("--implement-artifact", default="")
    ap.add_argument("--implement-review-artifact", default="")
    ap.add_argument("--validate-artifact", default="")
    ap.add_argument("--done-artifact", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or args.ticket_id or args.fixed_in_commit or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ"))
    out_dir = Path(args.artifact_root) / "bugfix" / run_id
    report_path = out_dir / "report.md"
    summary_path = out_dir / "summary.json"
    deck_path = out_dir / "deck.slidey.json"

    summary = _summary(args)
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

#!/usr/bin/env python3
"""Write deterministic docs-review report artifacts."""

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


def _load_json(value: str, fallback: Any) -> Any:
    if not value:
        return fallback
    try:
        return json.loads(value)
    except json.JSONDecodeError:
        return fallback


def _build_summary(args: argparse.Namespace) -> dict[str, Any]:
    verdict = _load_json(args.verdict_json, {})
    fix = _load_json(args.fix_json, {})
    decision = verdict.get("decision") or "missing"
    fixed = bool(fix.get("applied"))
    stale_docs = verdict.get("stale_docs") or []
    files_changed = fix.get("files_changed") or []
    unresolved = fix.get("unresolved") or []
    blockers = fix.get("blockers") or []

    status = "done" if decision == "up_to_date" or fixed else "blocked" if decision == "needs_update" else "pending"
    artifacts = [
        {"label": "Verdict artifact", "status": "done" if args.artifact_path else "pending", "detail": "Structured docs-review verdict.", "ref": args.artifact_path},
        {"label": "Commit", "status": "done" if args.commit_sha else "pending", "detail": f"Mode: {args.review_mode}", "ref": args.commit_sha},
    ]
    for row in stale_docs[:8]:
        artifacts.append({
            "label": row.get("path", "stale doc"),
            "status": "blocked" if not fixed else "done",
            "detail": row.get("lines", ""),
            "ref": row.get("path", ""),
        })
    for row in files_changed[:8]:
        artifacts.append({
            "label": row.get("path", "changed doc"),
            "status": "done",
            "detail": row.get("change", ""),
            "ref": row.get("path", ""),
        })

    return {
        "title": "Docs Review Report",
        "summary": verdict.get("summary") or fix.get("summary") or f"decision={decision}",
        "commit_sha": args.commit_sha,
        "review_mode": args.review_mode,
        "decision": decision,
        "objectives": [
            {"label": "Review verdict", "status": "done" if decision != "missing" else "blocked", "detail": f"Decision: {decision}."},
            {"label": "Stale docs", "status": "blocked" if stale_docs and not fixed else "done", "detail": f"{len(stale_docs)} stale doc citation(s)."},
            {"label": "Fix applied", "status": "done" if fixed or decision == "up_to_date" else "pending", "detail": fix.get("summary") or "No fix artifact."},
            {"label": "Blockers", "status": "blocked" if blockers or unresolved else "done", "detail": f"{len(blockers)} blocker(s), {len(unresolved)} unresolved item(s)."},
        ],
        "items": [
            {"id": "verdict", "status": decision, "owner": "docs-review", "artifact": args.artifact_path},
            {"id": "stale-docs", "status": "blocked" if stale_docs and not fixed else "done", "owner": "docs-review", "artifact": str(len(stale_docs))},
            {"id": "fix", "status": "done" if fixed else "pending", "owner": "docs-writer", "artifact": fix.get("summary", "")},
        ],
        "artifacts": artifacts,
        "next_steps": [
            {"label": "Review artifact", "detail": "Open the verdict and generated deck together."},
            {"label": "Review diff", "detail": "For patched docs, inspect the uncommitted diff before staging."},
        ],
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        "# Docs Review Report",
        "",
        summary.get("summary", ""),
        "",
        f"- Commit: `{summary.get('commit_sha', '')}`",
        f"- Mode: `{summary.get('review_mode', '')}`",
        f"- Decision: `{summary.get('decision', '')}`",
        "",
        "## Artifacts",
    ]
    for artifact in summary.get("artifacts", []):
        lines.append(f"- {artifact.get('label')}: `{artifact.get('ref', '')}` ({artifact.get('status', '')})")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--commit-sha", default="")
    ap.add_argument("--review-mode", default="recent")
    ap.add_argument("--artifact-path", default="")
    ap.add_argument("--verdict-json", default="")
    ap.add_argument("--fix-json", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or args.commit_sha or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ"))
    out_dir = Path(args.artifact_root) / "docs-review" / run_id
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

#!/usr/bin/env python3
"""Write deterministic demo-video-loop report artifacts."""

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


def _status(reason: str) -> str:
    if reason == "achieved":
        return "done"
    if reason in {"iteration_budget", "cost_budget"}:
        return "blocked"
    if reason == "aborted":
        return "blocked"
    return "pending"


def _build_summary(args: argparse.Namespace) -> dict[str, Any]:
    reason = args.terminal_reason or "pending"
    media = []
    if args.video_path:
        item: dict[str, Any] = {
            "title": f"{args.feature_slug} demo video",
            "src": args.video_path,
            "caption": args.maker_summary,
        }
        if args.chapters_path:
            item["chapters"] = args.chapters_path
        media.append(item)

    artifacts = [
        {"label": "Video", "status": "done" if args.video_path else "pending", "ref": args.video_path, "detail": "Canonical watch-speed demo video."},
        {"label": "Frames", "status": "done" if args.frames_dir else "pending", "ref": args.frames_dir, "detail": "Captured frames for visual QA."},
        {"label": "Chapters", "status": "done" if args.chapters_path else "pending", "ref": args.chapters_path, "detail": "Demo-video chapter sidecar."},
        {"label": "Feature brief", "status": "done" if args.feature_md_path else "pending", "ref": args.feature_md_path, "detail": "Feature description supplied to QA."},
        {"label": "Scenarios", "status": "done" if args.scenarios_path else "pending", "ref": args.scenarios_path, "detail": "Observable QA scenarios."},
    ]

    phases = [
        {"who": "Maker", "action": "Record demo", "detail": args.maker_summary or "No maker summary recorded."},
        {"who": "Video gate", "action": "Validate media", "detail": args.video_validate_reason or "Video gate passed."},
        {"who": "QA gate", "action": "Review scenarios", "detail": f"exit={args.qa_exit_code}; ok={args.qa_ok}"},
    ]
    if args.qa_report:
        phases.append({"who": "QA report", "action": "Feedback", "detail": args.qa_report[:280]})

    return {
        "title": f"{args.feature_slug} Demo Video Report",
        "summary": f"{reason}; iteration {args.iteration} of {args.iteration_budget}",
        "feature_slug": args.feature_slug,
        "terminal_reason": reason,
        "objectives": [
            {"label": "Demo loop", "status": _status(reason), "detail": f"Terminal reason: {reason}."},
            {"label": "Video validation", "status": "done" if args.video_valid == "true" else "blocked", "detail": args.video_validate_reason or "Video gate passed."},
            {"label": "Visual QA", "status": "done" if args.qa_ok == "true" else "blocked", "detail": f"QA exit code {args.qa_exit_code}."},
            {"label": "Iteration budget", "status": "done" if reason != "iteration_budget" else "blocked", "detail": f"{args.iteration} of {args.iteration_budget} iteration(s)."},
        ],
        "personas": [
            {"id": "maker", "name": "Demo maker"},
            {"id": "reviewer", "name": "QA reviewer"},
        ],
        "phases": phases,
        "media": media,
        "artifacts": artifacts,
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        f"# {summary['title']}",
        "",
        summary["summary"],
        "",
        "## Artifacts",
    ]
    for artifact in summary.get("artifacts", []):
        lines.append(f"- {artifact.get('label')}: `{artifact.get('ref', '')}` ({artifact.get('status', '')})")
    lines.extend(["", "## Phases"])
    for phase in summary.get("phases", []):
        lines.append(f"- {phase.get('who')}: {phase.get('action')} — {phase.get('detail')}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--feature-slug", default="demo-video")
    ap.add_argument("--terminal-reason", default="")
    ap.add_argument("--iteration", default="0")
    ap.add_argument("--iteration-budget", default="0")
    ap.add_argument("--maker-summary", default="")
    ap.add_argument("--video-path", default="")
    ap.add_argument("--frames-dir", default="")
    ap.add_argument("--chapters-path", default="")
    ap.add_argument("--feature-md-path", default="")
    ap.add_argument("--scenarios-path", default="")
    ap.add_argument("--video-valid", default="")
    ap.add_argument("--video-validate-reason", default="")
    ap.add_argument("--qa-ok", default="")
    ap.add_argument("--qa-exit-code", default="0")
    ap.add_argument("--qa-report", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or f"{args.feature_slug}-{dt.datetime.now(dt.timezone.utc).strftime('%Y%m%d-%H%M%SZ')}")
    out_dir = Path(args.artifact_root) / "demo-video-loop" / run_id
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
        "--kind", "feature-demo",
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

#!/usr/bin/env python3
"""Write deterministic ui-fix report artifacts.

The story state is authoritative; this script only normalizes fixed/skipped/
still-failing groups into review artifacts and a standardized Slidey deck.
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
    chars = []
    for ch in (value or "").lower():
        if ch.isalnum():
            chars.append(ch)
        elif ch in {"-", "_", ".", " ", ":"}:
            chars.append("-")
    return re.sub(r"-+", "-", "".join(chars).strip("-")) or "run"


def _parse_lines(raw: str, status: str) -> list[dict[str, str]]:
    items = []
    for line in (raw or "").splitlines():
        line = line.strip()
        if not line:
            continue
        parts = (line.split("|") + ["", "", ""])[:3]
        item_id, title, artifact = [part.strip() for part in parts]
        items.append({
            "id": item_id or title or "group",
            "title": title or item_id or "group",
            "status": status,
            "artifact": artifact,
        })
    return items


def _build_summary(args: argparse.Namespace) -> dict[str, Any]:
    fixed = _parse_lines(args.fixed_lines, "fixed")
    skipped = _parse_lines(args.skipped_lines, "skipped")
    still_failing = _parse_lines(args.still_failing_lines, "still failing")

    work_items: list[dict[str, str]] = []
    media: list[dict[str, str]] = []
    for item in fixed:
        handle = item["artifact"]
        label = item["title"]
        work_items.append({
            "id": item["id"],
            "status": "fixed",
            "owner": "ui-fix",
            "artifact": handle,
        })
        if handle:
            media.append({"label": label, "path": handle, "status": "done"})
    for item in skipped:
        work_items.append({
            "id": item["id"],
            "status": "skipped",
            "owner": "operator",
            "artifact": item["artifact"],
        })
    for item in still_failing:
        work_items.append({
            "id": item["id"],
            "status": "still failing",
            "owner": "operator",
            "artifact": item["artifact"],
        })

    total = args.group_count if args.group_count >= 0 else len(work_items)
    summary_text = (
        f"{len(fixed)} fixed, {len(skipped)} skipped, "
        f"{len(still_failing)} still failing across {total} group(s)."
    )
    objectives = [
        {"label": "Load UI audit", "status": "done" if args.verdict_path else "pending", "detail": args.verdict_path},
        {"label": "Group findings", "status": "done" if total or not args.verdict_path else "pending", "detail": f"{total} group(s) in scope."},
        {"label": "Apply fixes", "status": "done" if fixed else ("skipped" if skipped and not still_failing else "pending"), "detail": f"{len(fixed)} fixed."},
        {"label": "Verify results", "status": "failed" if still_failing else "done", "detail": f"{len(still_failing)} group(s) still failing."},
        {"label": "Report deck", "status": "done", "detail": "Generated from structured story state."},
    ]
    artifacts = [
        {"label": "Verdict JSON", "status": "done" if args.verdict_path else "pending", "ref": args.verdict_path},
        {"label": "Frames directory", "status": "done" if args.frames_dir else "pending", "ref": args.frames_dir},
    ]
    for item in media[:8]:
        artifacts.append({"label": item["label"], "status": "done", "ref": item["path"]})
    next_steps = []
    if still_failing:
        next_steps.append({"label": "Review remaining failures", "detail": f"{len(still_failing)} group(s) need another fix or scope decision."})
    if skipped:
        next_steps.append({"label": "Triage skipped groups", "detail": f"{len(skipped)} group(s) were deliberately skipped."})
    next_steps.append({"label": "Re-run full UI review", "detail": "Use kitsoki-ui-review to regenerate verdict.json with the full gate."})

    return {
        "title": "UI Fix Report",
        "summary": summary_text,
        "verdict_path": args.verdict_path,
        "frames_dir": args.frames_dir,
        "counts": {"fixed": len(fixed), "skipped": len(skipped), "still_failing": len(still_failing), "total": total},
        "objectives": objectives,
        "artifacts": artifacts,
        "items": work_items,
        "next_steps": next_steps,
    }


def _write_markdown(path: Path, summary: dict[str, Any]) -> None:
    lines = [
        "# UI Fix Report",
        "",
        summary["summary"],
        "",
        f"- Verdict: `{summary.get('verdict_path', '')}`",
        f"- Frames: `{summary.get('frames_dir', '')}`",
        "",
        "## Groups",
    ]
    for item in summary.get("items", []):
        artifact = item.get("artifact") or ""
        lines.append(f"- {item.get('id')}: {item.get('status')}" + (f" -> `{artifact}`" if artifact else ""))
    lines.extend(["", "## Next steps"])
    for step in summary.get("next_steps", []):
        lines.append(f"- {step.get('label')}: {step.get('detail')}")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--fixed-lines", default="")
    ap.add_argument("--skipped-lines", default="")
    ap.add_argument("--still-failing-lines", default="")
    ap.add_argument("--group-count", type=int, default=-1)
    ap.add_argument("--verdict-path", default="")
    ap.add_argument("--frames-dir", default="")
    ap.add_argument("--artifact-root", default=".artifacts")
    ap.add_argument("--run-id", default="")
    args = ap.parse_args()

    run_id = _slug(args.run_id or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%SZ"))
    out_dir = Path(args.artifact_root) / "ui-fix" / run_id
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

#!/usr/bin/env python3
"""Write a markdown report for a punch-list run."""

from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys

from punch_lib import ROOT, ensure_parent, read_state


def fanout_status(status: str) -> str:
    return {
        "passed": "succeeded",
        "partial": "retried",
        "failed": "failed",
        "skipped": "skipped",
        "pending": "pending",
    }.get(status or "pending", "pending")


def main() -> None:
    state_path = sys.argv[1] if len(sys.argv) > 1 else ""
    state = read_state(state_path)
    items = state.get("items") or []
    results = (state.get("results") or {}).get("items", [])
    defaults = state.get("defaults") or {}
    run_id = str(defaults.get("trace_run_id") or Path(state_path).stem or "latest")
    counts = {name: len([i for i in items if i.get("status") == name]) for name in ["passed", "partial", "failed", "skipped", "pending"]}
    summary = (
        f"{counts['passed']} passed, {counts['partial']} partial, "
        f"{counts['failed']} failed, {counts['skipped']} skipped, {counts['pending']} pending"
    )
    out_dir = Path(".artifacts/punch-list") / run_id
    out = out_dir / "report.md"
    summary_json = out_dir / "report.json"
    deck_path = out_dir / "deck.slidey.json"
    ensure_parent(str(out))
    lines = [
        "# Punch-list Report",
        "",
        f"Manifest: `{state.get('manifest_path', '')}`",
        f"Summary: {summary}",
        "",
        "| Item | Status | Story | Trace | Summary |",
        "|---|---:|---|---|---|",
    ]
    by_id = {r.get("id"): r for r in results}
    for item in items:
        result = by_id.get(item.get("id"), {})
        lines.append(
            "| {id} | {status} | `{story}` | `{trace}` | {summary} |".format(
                id=item.get("id", ""),
                status=item.get("status", ""),
                story=item.get("story", ""),
                trace=result.get("trace_path") or item.get("trace_path", ""),
                summary=(result.get("summary") or item.get("last_error") or "").replace("|", "\\|"),
            )
        )
    out.write_text("\n".join(lines) + "\n")

    fanout_items = []
    for item in items:
        result = by_id.get(item.get("id"), {})
        fanout_items.append({
            "id": item.get("id", ""),
            "label": item.get("title", item.get("id", "")),
            "status": fanout_status(item.get("status", "")),
            "attempts": item.get("attempts", 1),
            "owner": item.get("model") or item.get("profile") or "",
            "artifact": result.get("trace_path") or item.get("trace_path", ""),
        })

    payload = {
        "title": "Punch-list Report",
        "summary": summary,
        "items": fanout_items,
        "artifacts": [
            {"label": "Manifest", "status": "done", "ref": state.get("manifest_path", "")},
            {"label": "Markdown report", "status": "done", "ref": str(out)},
        ],
    }
    summary_json.write_text(json.dumps(payload, indent=2) + "\n")

    builder = ROOT / "tools" / "report-deck" / "deterministic_deck.py"
    result = subprocess.run(
        [
            sys.executable,
            str(builder),
            "--kind",
            "fanout",
            "--input",
            str(summary_json),
            "--out",
            str(deck_path),
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        raise SystemExit(result.stdout + result.stderr)

    print(json.dumps({
        "report_path": str(out),
        "summary_path": str(summary_json),
        "deck_path": str(deck_path),
        "summary": summary,
    }))


if __name__ == "__main__":
    main()

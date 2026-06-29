#!/usr/bin/env python3
"""Write a markdown report for a punch-list run."""

from __future__ import annotations

import json
from pathlib import Path
import sys

from punch_lib import ensure_parent, read_state


def main() -> None:
    state_path = sys.argv[1] if len(sys.argv) > 1 else ""
    state = read_state(state_path)
    items = state.get("items") or []
    results = (state.get("results") or {}).get("items", [])
    counts = {name: len([i for i in items if i.get("status") == name]) for name in ["passed", "partial", "failed", "skipped", "pending"]}
    summary = (
        f"{counts['passed']} passed, {counts['partial']} partial, "
        f"{counts['failed']} failed, {counts['skipped']} skipped, {counts['pending']} pending"
    )
    out = Path(".artifacts/punch-list/report.md")
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
    print(json.dumps({"report_path": str(out), "summary": summary}))


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Update the punch-list board and pick the next pending item."""

from __future__ import annotations

import json
import sys

from punch_lib import read_state, write_state


def main() -> None:
    state_path = sys.argv[1] if len(sys.argv) > 1 else ""
    mark_id = sys.argv[2] if len(sys.argv) > 2 else ""
    mark_status = sys.argv[3] if len(sys.argv) > 3 else ""
    mark_error = sys.argv[4] if len(sys.argv) > 4 else ""
    state = read_state(state_path)
    items = state.get("items") or []
    results = state.get("results") or {"items": []}
    result_items = results.setdefault("items", [])

    if mark_id and mark_status:
        for item in items:
            if item.get("id") == mark_id:
                item["status"] = mark_status
                item["last_error"] = mark_error
                record = {
                    "id": mark_id,
                    "title": item.get("title", ""),
                    "status": mark_status,
                    "story": item.get("story", ""),
                    "implementation_story": item.get("implementation_story", ""),
                    "profile": item.get("profile", ""),
                    "model": item.get("model", ""),
                    "trace_path": item.get("trace_path", ""),
                    "summary": mark_error,
                }
                result_items = [r for r in result_items if r.get("id") != mark_id]
                result_items.append(record)
                results["items"] = result_items
                break

    pending = [item for item in items if item.get("status") == "pending"]
    counts = {name: len([i for i in items if i.get("status") == name]) for name in ["passed", "partial", "failed", "skipped", "pending"]}
    processed = len(items) - counts["pending"]
    next_item = pending[0] if pending else {}
    state["items"] = items
    state["results"] = results
    write_state(state_path, state)
    print(json.dumps({
        "items": items,
        "results": results,
        "next_item": next_item,
        "next_item_id": next_item.get("id", ""),
        "route": "dispatch" if pending else "done",
        "has_pending": bool(pending),
        "processed_count": processed,
        "passed_count": counts["passed"],
        "partial_count": counts["partial"],
        "failed_count": counts["failed"],
        "skipped_count": counts["skipped"],
        "pending_count": counts["pending"],
        "count_summary": (
            f"Processed {processed} | Passed {counts['passed']} | Partial {counts['partial']} | "
            f"Failed {counts['failed']} | Skipped {counts['skipped']} | Pending {counts['pending']}"
        ),
    }))


if __name__ == "__main__":
    main()

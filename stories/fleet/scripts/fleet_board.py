#!/usr/bin/env python3
"""fleet_board.py — recompute the fleet board (read/rewrite the state file).

set:/increment can't mutate list elements (the work-decomposition discipline),
so the board recompute — mark a brief shipped/parked, recount, pick the next
pending — reads the sidecar state file, applies the mark, rewrites it, and
reports the updated list + the next pending brief + counts. Rewriting the file
keeps fleet restartable (a re-run resumes from shipped/parked status).

Usage:
  fleet_board.py <state_path> [mark_id] [mark_status] [mark_error]

Emits (stdout JSON):
  { "fleet_briefs": [...], "next_brief": {...}|{}, "has_pending": bool,
    "shipped_count": int, "parked_count": int, "pending_count": int }
"""
import json
import sys


def main():
    if len(sys.argv) < 2 or not sys.argv[1]:
        print(json.dumps({"fleet_briefs": [], "next_brief": {}, "has_pending": False,
                          "shipped_count": 0, "parked_count": 0, "pending_count": 0}))
        return

    state_path = sys.argv[1]
    try:
        with open(state_path) as f:
            briefs = (json.load(f) or {}).get("fleet_briefs", []) or []
    except Exception:  # noqa: BLE001
        briefs = []

    mark_id = sys.argv[2] if len(sys.argv) > 2 else ""
    mark_status = sys.argv[3] if len(sys.argv) > 3 else ""
    mark_error = sys.argv[4] if len(sys.argv) > 4 else ""

    if mark_id and mark_status:
        for b in briefs:
            if b.get("id") == mark_id:
                b["status"] = mark_status
                if mark_error:
                    b["last_error"] = mark_error
                break

    # Persist the updated board (restartable trail).
    try:
        with open(state_path, "w") as f:
            json.dump({"fleet_briefs": briefs}, f)
    except Exception:  # noqa: BLE001
        pass

    pending = [b for b in briefs if b.get("status") == "pending"]
    print(json.dumps({
        "fleet_briefs":  briefs,
        "next_brief":    pending[0] if pending else {},
        "has_pending":   len(pending) > 0,
        # A single, mutually-exclusive route string the board emits on — avoids
        # two bool guards both/neither firing across the import settle.
        "route":         "dispatch" if pending else "done",
        "shipped_count": len([b for b in briefs if b.get("status") == "shipped"]),
        "parked_count":  len([b for b in briefs if b.get("status") == "parked"]),
        "pending_count": len(pending),
    }))


if __name__ == "__main__":
    main()

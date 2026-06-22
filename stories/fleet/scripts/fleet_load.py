#!/usr/bin/env python3
"""fleet_load.py — parse a decomposition.yaml brief list into a fleet state file.

Reads the decomposition manifest produced by work-decomposition.md and writes the
fleet coordination board to a sidecar JSON state file (so the brief list — which
set:/increment cannot mutate element-wise — lives in a file the board script
rewrites deterministically, and fleet is restartable across runs: epic OQ #3).
Each board entry is { id, brief, gate_command, status: "pending", last_error }.

The `brief` text handed to ship-it is the brief's agent_brief (the self-contained
implementer prompt) when present, else its goal/title. The `gate_command` is the
manifest's explicit gate_command (fleet's deterministic contract — an exact
command the re-verify can re-run), else a test_plan that names a command.

Usage:  fleet_load.py <decomposition.(yaml|json)> [state_path]
Emits (stdout JSON):
  { "fleet_briefs": [...], "fleet_state_path": "<path>", "error": "" }
"""
import json
import os
import sys


def _load(path):
    with open(path) as f:
        text = f.read()
    try:
        import yaml
        return yaml.safe_load(text)
    except ImportError:
        return json.loads(text)


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"fleet_briefs": [], "fleet_state_path": "", "error": "no decomposition path"}))
        return
    path = sys.argv[1]
    state_path = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else (path + ".fleet-state.json")
    try:
        doc = _load(path)
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"fleet_briefs": [], "fleet_state_path": "", "error": f"parse failed: {e}"}))
        return

    out = []
    for b in (doc or {}).get("briefs", []) or []:
        out.append({
            "id": b.get("id", ""),
            "brief": b.get("agent_brief") or b.get("goal") or b.get("title") or "",
            "gate_command": b.get("gate_command") or (b.get("test_plan", "") or "").strip(),
            "status": "pending",
            "last_error": "",
        })

    try:
        os.makedirs(os.path.dirname(state_path) or ".", exist_ok=True)
        with open(state_path, "w") as f:
            json.dump({"fleet_briefs": out}, f)
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"fleet_briefs": out, "fleet_state_path": "", "error": f"state write failed: {e}"}))
        return

    print(json.dumps({"fleet_briefs": out, "fleet_state_path": state_path, "error": ""}))


if __name__ == "__main__":
    main()

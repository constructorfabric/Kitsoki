#!/usr/bin/env python3
"""Enforce per-item punch-list run policy."""

from __future__ import annotations

import json
import sys

from punch_lib import is_llm_spending_command, read_state, story_path_exists


def main() -> None:
    state_path = sys.argv[1] if len(sys.argv) > 1 else ""
    item_id = sys.argv[2] if len(sys.argv) > 2 else ""
    state = read_state(state_path)
    item = next((i for i in state.get("items", []) if i.get("id") == item_id), None)
    if not item:
        msg = f"item not found: {item_id}"
        print(json.dumps({"policy_result": {"status": "fail", "message": msg}, "error": msg}))
        return

    errors: list[str] = []
    if not story_path_exists(item.get("story", "")):
        errors.append(f"story path missing: {item.get('story', '')}")
    if item.get("implementation_story") and not story_path_exists(item.get("implementation_story", "")):
        errors.append(f"implementation_story path missing: {item.get('implementation_story', '')}")

    live = item.get("harness") == "live"
    if live and item.get("profile") != "codex-native":
        errors.append("live work must use profile codex-native")
    if live and item.get("model") != "gpt-5.5":
        errors.append("live work must use model gpt-5.5")
    if item.get("implementation_story") and not item.get("verify") and not item.get("gate_command"):
        errors.append("implementation item has no deterministic verifier")
    for check in item.get("verify") or []:
        if check.get("kind") == "command" and is_llm_spending_command(check.get("cmd", "")):
            errors.append(f"verifier invokes LLM/live command: {check.get('cmd')}")

    if errors:
        msg = "; ".join(errors)
        print(json.dumps({"policy_result": {"status": "fail", "message": msg}, "error": msg}))
    else:
        print(json.dumps({"policy_result": {"status": "ok", "message": "profile/model/verifier policy passed"}, "error": ""}))


if __name__ == "__main__":
    main()

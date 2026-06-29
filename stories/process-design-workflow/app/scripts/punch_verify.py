#!/usr/bin/env python3
"""Run deterministic verification checks for one punch-list item."""

from __future__ import annotations

import json
import subprocess
import sys

from punch_lib import is_llm_spending_command, read_state


def run_cmd(cmd: list[str]) -> dict:
    proc = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120)
    return {"cmd": " ".join(cmd), "exit_code": proc.returncode, "output": proc.stdout[-4000:], "ok": proc.returncode == 0}


def main() -> None:
    state_path = sys.argv[1] if len(sys.argv) > 1 else ""
    item_id = sys.argv[2] if len(sys.argv) > 2 else ""
    state = read_state(state_path)
    item = next((i for i in state.get("items", []) if i.get("id") == item_id), None)
    if not item:
        print(json.dumps({"verify_result": {"status": "failed", "summary": f"item not found: {item_id}", "checks": []}}))
        return

    checks = []
    verify = item.get("verify") or []
    if item.get("gate_command"):
        verify = list(verify) + [{"kind": "command", "cmd": item.get("gate_command")}]
    if not verify:
        print(json.dumps({"verify_result": {"status": "partial", "summary": "no deterministic verifier declared", "checks": []}}))
        return

    for check in verify:
        kind = check.get("kind")
        try:
            if kind == "story_validate":
                story = check.get("story", "")
                checks.append(run_cmd(["go", "run", "./cmd/kitsoki", "render", story, "-o", "-"]))
            elif kind == "story_test":
                story = check.get("story", "")
                cmd = ["go", "run", "./cmd/kitsoki", "test", "flows", story]
                if check.get("flows"):
                    cmd.extend(["--flows", check.get("flows")])
                checks.append(run_cmd(cmd))
            elif kind == "command":
                cmd = check.get("cmd", "")
                if is_llm_spending_command(cmd):
                    checks.append({"cmd": cmd, "exit_code": 2, "output": "blocked: verifier appears to invoke LLM/live command", "ok": False})
                else:
                    proc = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120, shell=True)
                    checks.append({"cmd": cmd, "exit_code": proc.returncode, "output": proc.stdout[-4000:], "ok": proc.returncode == 0})
            else:
                checks.append({"cmd": kind or "", "exit_code": 2, "output": f"unsupported verify kind: {kind}", "ok": False})
        except Exception as exc:  # noqa: BLE001
            checks.append({"cmd": str(check), "exit_code": 2, "output": str(exc), "ok": False})

    failed = [c for c in checks if not c.get("ok")]
    status = "passed" if not failed else "failed"
    summary = f"{len(checks) - len(failed)}/{len(checks)} deterministic checks passed"
    print(json.dumps({"verify_result": {"status": status, "summary": summary, "checks": checks}}))


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""judge_verdict_after.py — AFTER agent-split.

Invokes the judge via `kitsoki agent decide` (or, when
KITSOKI_AGENT_SOCK is set, via the unix-socket JSON-RPC daemon — zero
extra process overhead).

Improvements over the "before" version:
  - Verdict schema is the canonical stories/bugfix/schemas/judge_verdict.json;
    no client-side reimplementation that can drift.
  - Every call is recorded as a labeled decision in the kitsoki trace.
    The KITSOKI_SESSION_ID env var (set by the state machine or by the
    operator's shell) threads the subprocess decision into the parent
    session so the full execution tree is queryable in one place.
  - JSON output is guaranteed by the agent's submit-schema enforcement;
    no regex scraping.
  - When KITSOKI_AGENT_SOCK is set the call goes over the unix socket
    rather than forking a new process — useful when running many judges
    in parallel (e.g. bulk CI triage).

Usage:
    # CLI path (no daemon needed):
    python judge_verdict_after.py <ticket_id> <artifact_title> <artifact_body>

    # Socket path (daemon already running):
    KITSOKI_AGENT_SOCK=/tmp/kitsoki-agent.sock \\
        python judge_verdict_after.py <ticket_id> <artifact_title> <artifact_body>
"""

import json
import os
import socket
import subprocess
import sys
import pathlib


# Path to the judge prompt template and schema, relative to this script.
_HERE = pathlib.Path(__file__).parent
_STORIES_ROOT = _HERE.parent
_SCHEMA = str(_STORIES_ROOT / "schemas" / "judge_verdict.json")
_PROMPT = str(_STORIES_ROOT / "prompts" / "judge_validating.md")


# ── socket client ─────────────────────────────────────────────────────────────

def _rpc_call(sock_path: str, method: str, params: dict) -> dict:
    """Send one JSON-RPC 2.0 request over a unix socket and return result."""
    req = {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.connect(sock_path)
        s.sendall((json.dumps(req) + "\n").encode())
        buf = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            buf += chunk
            if b"\n" in buf:
                break
    resp = json.loads(buf.split(b"\n")[0])
    if "error" in resp and resp["error"]:
        raise RuntimeError(f"RPC error {resp['error']['code']}: {resp['error']['message']}")
    return resp.get("result", {})


# ── agent decide helpers ──────────────────────────────────────────────────────

def _call_via_socket(sock_path: str, ticket_id: str, artifact_title: str, artifact_body: str) -> dict:
    params = {
        "prompt": _PROMPT,
        "schema": _SCHEMA,
        "args_json": json.dumps({
            "ticket_id":      ticket_id,
            "artifact_title": artifact_title,
            "artifact_body":  artifact_body,
        }),
    }
    result = _rpc_call(sock_path, "agent.decide", params)
    # result["data"]["submitted"] holds the typed verdict
    submitted = result.get("data", {}).get("submitted", {})
    if not submitted:
        raise ValueError(f"agent.decide returned no submitted data: {result}")
    return submitted


def _call_via_cli(ticket_id: str, artifact_title: str, artifact_body: str) -> dict:
    args_json = json.dumps({
        "ticket_id":      ticket_id,
        "artifact_title": artifact_title,
        "artifact_body":  artifact_body,
    })
    env = os.environ.copy()
    # Pass the parent session ID so this decision is linked in the trace.
    # (The state machine sets KITSOKI_SESSION_ID before launching scripts.)
    result = subprocess.run(
        [
            "kitsoki", "agent", "decide",
            "--prompt", _PROMPT,
            "--schema", _SCHEMA,
            "--args-json", args_json,
        ],
        capture_output=True,
        text=True,
        env=env,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(f"kitsoki agent decide failed ({result.returncode}):\n{result.stderr.strip()}")
    # kitsoki agent decide prints JSON to stdout when stdout is not a TTY.
    data = json.loads(result.stdout)
    submitted = data.get("data", {}).get("submitted", {})
    if not submitted:
        raise ValueError(f"agent decide returned no submitted data: {data}")
    return submitted


# ── main ──────────────────────────────────────────────────────────────────────

def main() -> None:
    if len(sys.argv) < 4:
        print(
            f"usage: {sys.argv[0]} <ticket_id> <artifact_title> <artifact_body>",
            file=sys.stderr,
        )
        sys.exit(1)

    ticket_id      = sys.argv[1]
    artifact_title = sys.argv[2]
    artifact_body  = sys.argv[3]

    sock_path = os.environ.get("KITSOKI_AGENT_SOCK", "")
    if sock_path:
        verdict = _call_via_socket(sock_path, ticket_id, artifact_title, artifact_body)
    else:
        verdict = _call_via_cli(ticket_id, artifact_title, artifact_body)

    print(json.dumps(verdict, indent=2))


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
Generate oracle-with-separate-prompts.snapshot.json — similar to oracle-rich
but with large prompts stored in separate files (using prompt_file field).

This tests the new usePromptLoader composable in the UI.
"""

from __future__ import annotations
import json
import os
from datetime import datetime, timedelta, timezone

SESSION = "sess-oracle-separate-prompts"
APP_ID = "bugfix"
MODEL = "claude-3-sonnet"

# Load the existing bugfix snapshot to reuse its app + mermaid blocks.
_here = os.path.dirname(os.path.abspath(__file__))
_fixture_dir = os.path.join(_here, "..")
_bugfix_snapshot_path = os.path.join(_fixture_dir, "bugfix.snapshot.json")

with open(_bugfix_snapshot_path) as fh:
    _bugfix = json.load(fh)

MERMAID = _bugfix["mermaid"]

_STRIP_STATE_KEYS = {"View", "On", "OnEnter"}

def _trim_app(app: dict) -> dict:
    import copy
    trimmed = copy.deepcopy(app)
    states = trimmed.get("States", {})
    for state_name, state_def in states.items():
        if isinstance(state_def, dict):
            for key in _STRIP_STATE_KEYS:
                state_def.pop(key, None)
            sub = state_def.get("States", {})
            if isinstance(sub, dict):
                for sub_name, sub_def in sub.items():
                    if isinstance(sub_def, dict):
                        for key in _STRIP_STATE_KEYS:
                            sub_def.pop(key, None)
    return trimmed

APP_DEF = _trim_app(_bugfix["app"])

T0 = datetime(2026, 5, 26, 10, 0, 0, tzinfo=timezone.utc)
_ts = T0

def advance(seconds: float) -> None:
    global _ts
    _ts += timedelta(seconds=seconds)

def now_str() -> str:
    ms = int(_ts.microsecond / 1000)
    return _ts.strftime("%Y-%m-%dT%H:%M:%S.") + f"{ms:03d}Z"

def tick(step: float = 0.05) -> str:
    advance(step)
    return now_str()

_events: list[dict] = []

def emit(level: str, msg: str, turn: int, state_path: str, step: float = 0.05, **attrs) -> dict:
    ev = {
        "time": tick(step),
        "level": level,
        "msg": msg,
        "session_id": SESSION,
        "turn": turn,
        "state_path": state_path,
        "attrs": {k: v for k, v in attrs.items() if v is not None},
    }
    _events.append(ev)
    return ev

def turn1_decide() -> None:
    turn = 1
    sp = "idle"

    advance(2)
    emit("INFO", "turn.start", turn, sp, input="start BUG-4711")
    emit("DEBUG", "machine.state_entered", turn, sp)

    emit("DEBUG", "oracle.decide.start", turn, sp,
         verb="decide",
         call_id="call-decide-001",
         agent="strategy_router",
         model=MODEL)

    advance(0.8)

    # Use prompt_file instead of inline prompt. The start/complete pair shares a
    # call_id so the timeline merges them into one row (as production traces do).
    emit("DEBUG", "oracle.decide.complete", turn, sp, step=0.01,
         verb="decide",
         call_id="call-decide-001",
         agent="strategy_router",
         model=MODEL,
         duration_ms=840,
         prompt_tokens=512,
         response_tokens=18,
         cost_usd=0.0009,
         system_prompt_file="oracle-prompts/decide-001-system.txt",
         prompt_file="oracle-prompts/decide-001.txt",
         input={
             "choices": [
                 {"id": "reproduce_locally", "description": "Attempt to reproduce the bug."},
                 {"id": "ask_user", "description": "Ask the user for more information."},
             ]
         },
         response={
             "json": {"choice": "reproduce_locally", "confidence": 0.92},
             "decision": "reproduce_locally",
         })

    emit("DEBUG", "machine.transition", turn, sp,
         **{"from": sp, "to": "reproducing._executing"},
         intent="start")
    emit("DEBUG", "world.update", turn, sp, set={"ticket_id": "BUG-4711"})
    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("DEBUG", "machine.state_entered", turn, "reproducing._executing")
    emit("INFO", "turn.end", turn, "reproducing._executing")

def turn2_extract() -> None:
    turn = 2
    sp = "reproducing._executing"

    advance(3)
    emit("INFO", "turn.start", turn, sp, input="(auto:executing)")

    emit("DEBUG", "oracle.extract.start", turn, sp,
         verb="extract",
         call_id="call-extract-001",
         agent="intake_extractor",
         model=MODEL)

    advance(0.6)

    emit("DEBUG", "oracle.extract.complete", turn, sp, step=0.01,
         verb="extract",
         call_id="call-extract-001",
         agent="intake_extractor",
         model=MODEL,
         duration_ms=620,
         prompt_tokens=384,
         response_tokens=42,
         cost_usd=0.00071,
         system_prompt_file="oracle-prompts/extract-001-system.txt",
         prompt_file="oracle-prompts/extract-001.txt",
         input={"schema": {"type": "object", "properties": {"title": {"type": "string"}}}},
         response={"extracted": {"title": "Race condition in worker pool"}})

    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("INFO", "turn.end", turn, sp)

# Generate events
turn1_decide()
turn2_extract()

# Output fixture
output = {
    "session": {"id": SESSION, "app_id": APP_ID},
    "app": APP_DEF,
    "mermaid": MERMAID,
    "events": _events,
}

print(json.dumps(output, indent=2))

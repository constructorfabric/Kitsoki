#!/usr/bin/env python3
"""
Generate oracle-rich.snapshot.json — a synthetic but realistic-looking
snapshot that exercises all five oracle verbs (decide, extract, ask, task,
converse) with the full rich attrs shape described in ORACLE_ATTRS.md.

The snapshot reuses the bugfix app def and mermaid snapshot from
bugfix.snapshot.json but has its own session id and event sequence.

Usage:
    python3 oracle-rich.gen.py > oracle-rich.snapshot.json
or via Makefile:
    make -C tools/runstatus/fixtures oracle-rich
"""

from __future__ import annotations
import json
import os
import sys
from datetime import datetime, timedelta, timezone

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

SESSION = "sess-oracle-rich-001"
APP_ID = "bugfix"
MODEL = "claude-3-sonnet"

# Load the existing bugfix snapshot to reuse its app + mermaid blocks.
_here = os.path.dirname(os.path.abspath(__file__))
_fixture_dir = os.path.join(_here, "..")
_bugfix_snapshot_path = os.path.join(_fixture_dir, "bugfix.snapshot.json")


with open(_bugfix_snapshot_path) as fh:
    _bugfix = json.load(fh)

MERMAID = _bugfix["mermaid"]

# Trim the States blocks to reduce fixture size: remove View and On sub-keys
# (prompt templates / transition tables) that are not needed for oracle-attrs
# rendering. All structural keys (Type, Mode, Description, Terminal, Initial,
# States, Intents, RelevantWorld, etc.) are preserved for the diagram and
# detail drawer.
_STRIP_STATE_KEYS = {"View", "On", "OnEnter"}

def _trim_app(app: dict) -> dict:
    import copy
    trimmed = copy.deepcopy(app)
    states = trimmed.get("States", {})
    for state_name, state_def in states.items():
        if isinstance(state_def, dict):
            for key in _STRIP_STATE_KEYS:
                state_def.pop(key, None)
            # Also trim sub-states if present
            sub = state_def.get("States", {})
            if isinstance(sub, dict):
                for sub_name, sub_def in sub.items():
                    if isinstance(sub_def, dict):
                        for key in _STRIP_STATE_KEYS:
                            sub_def.pop(key, None)
    return trimmed

APP_DEF = _trim_app(_bugfix["app"])

# ---------------------------------------------------------------------------
# Timestamp helpers
# ---------------------------------------------------------------------------

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


# ---------------------------------------------------------------------------
# Event builder
# ---------------------------------------------------------------------------

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


# ---------------------------------------------------------------------------
# Turn 1: oracle.decide
# Scenario: idle state — decide whether to reproduce locally or ask the user
# for more info about ticket BUG-4711.
# ---------------------------------------------------------------------------

def turn1_decide() -> None:
    turn = 1
    sp = "idle"

    advance(2)
    emit("INFO", "turn.start", turn, sp, input="start BUG-4711")
    emit("DEBUG", "machine.state_entered", turn, sp)

    # oracle.decide.start — lightweight early-visibility event
    emit("DEBUG", "oracle.decide.start", turn, sp,
         verb="decide",
         agent="strategy_router",
         model=MODEL)

    advance(0.8)

    # oracle.decide.complete — full rich event
    emit("DEBUG", "oracle.decide.complete", turn, sp, step=0.01,
         verb="decide",
         agent="strategy_router",
         model=MODEL,
         duration_ms=840,
         prompt_tokens=512,
         response_tokens=18,
         cost_usd=0.0009,
         system_prompt=(
             "You are a strategy router for the bugfix pipeline. "
             "Given the user's message and the current world state, choose the best next step "
             "from the provided choices. Output valid JSON with a 'choice' field set to one of "
             "the choice ids, and a 'confidence' field between 0 and 1."
         ),
         prompt=(
             "The user typed: 'start BUG-4711'.\n\n"
             "World state: ticket_id is empty, no reproduction artifact exists.\n\n"
             "Choose the best next step."
         ),
         input={
             "choices": [
                 {
                     "id": "reproduce_locally",
                     "description": "Attempt to reproduce the bug in a local workspace using the ticket id.",
                 },
                 {
                     "id": "ask_user",
                     "description": "Ask the user for more information before proceeding (ticket details are missing).",
                 },
             ]
         },
         response={
             "json": {"choice": "reproduce_locally", "confidence": 0.92},
             "decision": "reproduce_locally",
         })

    emit("DEBUG", "machine.transition", turn, sp,
         **{"from": sp, "to": "reproducing._executing"},
         intent="start")
    emit("DEBUG", "machine.world.write", turn, sp, key="ticket_id", value="BUG-4711")
    emit("DEBUG", "machine.world.write", turn, sp, key="ticket_title",
         value="race condition in worker pool on shutdown")
    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("DEBUG", "machine.state_entered", turn, "reproducing._executing")
    emit("INFO", "turn.end", turn, "reproducing._executing")


# ---------------------------------------------------------------------------
# Turn 2: oracle.extract
# Scenario: intake extractor pulls structured fields from a user message
# that arrived on the thread (BUG-4711 triage note).
# ---------------------------------------------------------------------------

def turn2_extract() -> None:
    turn = 2
    sp = "reproducing._executing"

    advance(3)
    emit("INFO", "turn.start", turn, sp, input="(auto:executing)")

    # oracle.extract.start
    emit("DEBUG", "oracle.extract.start", turn, sp,
         verb="extract",
         agent="intake_extractor",
         model=MODEL)

    advance(0.6)

    # oracle.extract.complete
    emit("DEBUG", "oracle.extract.complete", turn, sp, step=0.01,
         verb="extract",
         agent="intake_extractor",
         model=MODEL,
         duration_ms=620,
         prompt_tokens=380,
         response_tokens=45,
         cost_usd=0.0006,
         system_prompt=(
             "You are the intake extractor for the bugfix pipeline. "
             "Extract structured fields from the user's triage message according to the provided JSON Schema. "
             "Return only valid JSON that satisfies the schema."
         ),
         prompt=(
             "Triage message from thread BUG-4711:\n\n"
             "'BUG-4711: Race condition in worker pool on shutdown. "
             "The dispatcher closes the done channel without holding the mu lock while worker "
             "goroutines read it under mu. Severity: high. Reported by: ops-team.'"
         ),
         input={
             "schema": {
                 "type": "object",
                 "properties": {
                     "ticket_id": {"type": "string", "description": "Jira/issue tracker id"},
                     "summary": {"type": "string", "description": "One-sentence bug summary"},
                     "severity": {
                         "type": "string",
                         "enum": ["low", "medium", "high", "critical"],
                         "description": "Bug severity level",
                     },
                 },
                 "required": ["ticket_id", "summary", "severity"],
             }
         },
         response={
             "json": {
                 "ticket_id": "BUG-4711",
                 "summary": "Race condition in worker pool on shutdown",
                 "severity": "high",
             },
             "extracted": {
                 "ticket_id": "BUG-4711",
                 "summary": "Race condition in worker pool on shutdown",
                 "severity": "high",
             },
         })

    emit("DEBUG", "machine.world.write", turn, sp, key="ticket_id", value="BUG-4711")
    emit("DEBUG", "machine.world.write", turn, sp, key="ticket_title",
         value="Race condition in worker pool on shutdown")
    emit("INFO", "turn.end", turn, sp)


# ---------------------------------------------------------------------------
# Turn 3: oracle.ask
# Scenario: idle router classifying a user message into an intent + slots.
# ---------------------------------------------------------------------------

def turn3_ask() -> None:
    turn = 3
    sp = "idle"

    advance(8)
    emit("INFO", "turn.start", turn, sp, input="restart from proposing please")
    emit("DEBUG", "machine.state_exited", turn, "reproducing._executing")
    emit("DEBUG", "machine.state_entered", turn, sp)

    # oracle.ask.start
    emit("DEBUG", "oracle.ask.start", turn, sp,
         verb="ask",
         agent="idle_router",
         model=MODEL)

    advance(0.5)

    # oracle.ask.complete
    emit("DEBUG", "oracle.ask.complete", turn, sp, step=0.01,
         verb="ask",
         agent="idle_router",
         model=MODEL,
         duration_ms=510,
         prompt_tokens=420,
         response_tokens=28,
         cost_usd=0.0005,
         system_prompt=(
             "You are the intent router for the bugfix pipeline idle state. "
             "Classify the user message into one of the registered intents and extract any slots. "
             "Registered intents: start, quit, restart_from, quick. "
             "Output JSON with 'intent' and 'slots' fields."
         ),
         prompt="User: restart from proposing please",
         input={
             "instructions": "Classify the user message into one of: start, quit, restart_from, quick.",
         },
         response={
             "text": "Intent: restart_from",
             "intent": "restart_from",
             "slots": {"stage": "proposing"},
         })

    emit("DEBUG", "machine.transition", turn, sp,
         **{"from": sp, "to": "proposing._executing"},
         intent="restart_from")
    emit("DEBUG", "machine.world.write", turn, sp, key="restart_from_stage", value="proposing")
    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("DEBUG", "machine.state_entered", turn, "proposing._executing")
    emit("INFO", "turn.end", turn, "proposing._executing")


# ---------------------------------------------------------------------------
# Turn 4: oracle.task
# Scenario: reproducing_specialist runs a full task — reads source files,
# greps for the racing function, edits a test file, runs the test suite.
# This is the meaty verb with tool_calls and files_changed.
# ---------------------------------------------------------------------------

def turn4_task() -> None:
    turn = 4
    sp = "reproducing._executing"

    advance(5)
    emit("INFO", "turn.start", turn, sp, input="(auto:executing)")
    emit("DEBUG", "machine.state_exited", turn, "proposing._executing")
    emit("DEBUG", "machine.state_entered", turn, sp)

    # oracle.task.start
    emit("DEBUG", "oracle.task.start", turn, sp,
         verb="task",
         agent="reproducing_specialist",
         model=MODEL)

    advance(22)

    # oracle.task.complete — rich event with tool_calls + files_changed
    emit("DEBUG", "oracle.task.complete", turn, sp, step=0.01,
         verb="task",
         agent="reproducing_specialist",
         model=MODEL,
         duration_ms=22400,
         prompt_tokens=2400,
         response_tokens=1820,
         cost_usd=0.018,
         system_prompt=(
             "You are the reproducing specialist for the kitsoki bugfix pipeline. "
             "Your goal is to reproduce the reported bug by reading the relevant source files, "
             "understanding the race condition, writing a failing test, and confirming the test "
             "fails under the Go race detector. "
             "Use the available tools: Read, Grep, Edit, Bash. "
             "Return a concise summary of your findings and the reproduction steps."
         ),
         prompt=(
             "Reproduce BUG-4711: race condition in worker pool on shutdown.\n\n"
             "Repository path: /workspace/BUG-4711\n"
             "Package under test: workerpool\n\n"
             "Steps expected:\n"
             "1. Read the dispatcher implementation to locate the race.\n"
             "2. Grep for the shutdown function across the package.\n"
             "3. Read the existing test file to find the right insertion point.\n"
             "4. Add a failing test that exercises the race.\n"
             "5. Run `go test -race ./workerpool/...` and confirm the test fails.\n"
             "6. Summarise the root cause."
         ),
         input={
             "instructions": (
                 "Reproduce the reported race condition. Write a failing test named "
                 "TestDispatcherShutdownRace that demonstrates the race under `go test -race`. "
                 "Do not fix the bug — only prove it is reproducible."
             ),
             "files_in": [
                 "workerpool/dispatcher.go",
                 "workerpool/worker.go",
             ],
         },
         response={
             "text": (
                 "Root cause identified: Dispatcher.shutdown() closes the done channel without "
                 "holding d.mu, while worker goroutines in (*Worker).loop() check done under d.mu. "
                 "This creates a concurrent read/write on the channel head pointer.\n\n"
                 "Added TestDispatcherShutdownRace in dispatcher_test.go. "
                 "The test spawns 8 concurrent workers and calls Shutdown() while tasks are "
                 "still dispatching. Under `go test -race` it fails reliably:\n\n"
                 "    DATA RACE\n"
                 "    Write at 0x00c0001a4080 by goroutine 12:\n"
                 "      workerpool.(*Dispatcher).shutdown()\n"
                 "    Previous read at 0x00c0001a4080 by goroutine 17:\n"
                 "      workerpool.(*Worker).loop()\n\n"
                 "Reproduction confirmed. Test committed to dispatcher_test.go."
             )
         },
         tool_calls=[
             {
                 "seq": 1,
                 "tool": "Read",
                 "args": {"file_path": "workerpool/dispatcher.go"},
                 "result": (
                     "package workerpool\n\nimport \"sync\"\n\n"
                     "type Dispatcher struct {\n\tmu      sync.Mutex\n\tworkers []*Worker\n"
                     "\tdone    chan struct{}\n}\n\n"
                     "func NewDispatcher(n int) *Dispatcher {\n"
                     "\treturn &Dispatcher{done: make(chan struct{})}\n}\n\n"
                     "func (d *Dispatcher) Start() {\n"
                     "\tfor i := 0; i < cap(d.workers); i++ {\n"
                     "\t\tw := &Worker{d: d}\n\t\td.workers = append(d.workers, w)\n"
                     "\t\tgo w.loop()\n\t}\n}\n\n"
                     "// shutdown closes the done channel without holding mu — RACE\n"
                     "func (d *Dispatcher) shutdown() {\n\tclose(d.done)\n}"
                 ),
                 "duration_ms": 12,
             },
             {
                 "seq": 2,
                 "tool": "Grep",
                 "args": {
                     "pattern": "func.*[Ss]hutdown",
                     "path": "workerpool/",
                     "recursive": True,
                 },
                 "result": (
                     "workerpool/dispatcher.go:28:func (d *Dispatcher) shutdown() {\n"
                     "workerpool/dispatcher.go:31:func (d *Dispatcher) Shutdown() {\n"
                 ),
                 "duration_ms": 8,
             },
             {
                 "seq": 3,
                 "tool": "Read",
                 "args": {"file_path": "workerpool/dispatcher.go", "offset": 31, "limit": 10},
                 "result": (
                     "func (d *Dispatcher) Shutdown() {\n"
                     "\td.mu.Lock()\n"
                     "\tdefer d.mu.Unlock()\n"
                     "\td.shutdown()\n"
                     "}\n"
                 ),
                 "duration_ms": 10,
             },
             {
                 "seq": 4,
                 "tool": "Read",
                 "args": {"file_path": "workerpool/dispatcher_test.go"},
                 "result": (
                     "package workerpool\n\nimport (\n\t\"testing\"\n\t\"sync\"\n)\n\n"
                     "func TestDispatcherBasic(t *testing.T) {\n"
                     "\td := NewDispatcher(2)\n\td.Start()\n\td.Shutdown()\n}\n\n"
                     "func TestDispatcherDispatch(t *testing.T) {\n"
                     "\td := NewDispatcher(4)\n\td.Start()\n"
                     "\tvar count int32\n"
                     "\tfor i := 0; i < 100; i++ {\n"
                     "\t\td.Dispatch(func() { atomic.AddInt32(&count, 1) })\n\t}\n"
                     "\td.Shutdown()\n}\n"
                 ),
                 "duration_ms": 11,
             },
             {
                 "seq": 5,
                 "tool": "Edit",
                 "args": {
                     "file_path": "workerpool/dispatcher_test.go",
                     "old_string": "func TestDispatcherDispatch(t *testing.T) {",
                     "new_string": (
                         "func TestDispatcherShutdownRace(t *testing.T) {\n"
                         "\tconst workers = 8\n"
                         "\td := NewDispatcher(workers)\n"
                         "\td.Start()\n"
                         "\tvar wg sync.WaitGroup\n"
                         "\tfor i := 0; i < workers*4; i++ {\n"
                         "\t\twg.Add(1)\n"
                         "\t\tgo func() {\n"
                         "\t\t\tdefer wg.Done()\n"
                         "\t\t\td.Dispatch(noopTask)\n"
                         "\t\t}()\n"
                         "\t}\n"
                         "\twg.Wait()\n"
                         "\td.Shutdown()\n"
                         "}\n\n"
                         "func TestDispatcherDispatch(t *testing.T) {"
                     ),
                 },
                 "result": "Edit applied successfully.",
                 "duration_ms": 14,
             },
             {
                 "seq": 6,
                 "tool": "Bash",
                 "args": {
                     "command": "cd /workspace/BUG-4711 && go test -race -run TestDispatcherShutdownRace ./workerpool/... 2>&1 | tail -25",
                     "timeout": 30000,
                 },
                 "result": (
                     "--- FAIL: TestDispatcherShutdownRace (0.12s)\n"
                     "    DATA RACE\n"
                     "    Write at 0x00c0001a4080 by goroutine 12:\n"
                     "      workerpool.(*Dispatcher).shutdown()\n"
                     "          /workspace/BUG-4711/workerpool/dispatcher.go:29\n"
                     "    Previous read at 0x00c0001a4080 by goroutine 17:\n"
                     "      workerpool.(*Worker).loop()\n"
                     "          /workspace/BUG-4711/workerpool/worker.go:44\n"
                     "FAIL\tworkerpool\t0.128s\n"
                 ),
                 "duration_ms": 3100,
             },
             {
                 "seq": 7,
                 "tool": "Bash",
                 "args": {
                     "command": "cd /workspace/BUG-4711 && go test -race ./workerpool/... 2>&1 | tail -5",
                     "timeout": 30000,
                 },
                 "result": (
                     "ok  \tworkerpool\t3.241s\n"
                     "# Only TestDispatcherShutdownRace fails; other tests pass."
                 ),
                 "duration_ms": 3241,
             },
         ],
         files_changed=[
             {
                 "path": "workerpool/dispatcher_test.go",
                 "status": "modified",
                 "additions": 16,
                 "deletions": 0,
                 "diff": (
                     "--- workerpool/dispatcher_test.go\n"
                     "+++ workerpool/dispatcher_test.go\n"
                     "@@ -14,0 +14,16 @@\n"
                     "+func TestDispatcherShutdownRace(t *testing.T) {\n"
                     "+\tconst workers = 8\n"
                     "+\td := NewDispatcher(workers)\n"
                     "+\td.Start()\n"
                     "+\tvar wg sync.WaitGroup\n"
                     "+\tfor i := 0; i < workers*4; i++ {\n"
                     "+\t\twg.Add(1)\n"
                     "+\t\tgo func() {\n"
                     "+\t\t\tdefer wg.Done()\n"
                     "+\t\t\td.Dispatch(noopTask)\n"
                     "+\t\t}()\n"
                     "+\t}\n"
                     "+\twg.Wait()\n"
                     "+\td.Shutdown()\n"
                     "+}\n"
                     "+\n"
                 ),
             },
         ])

    # Host calls that run alongside the task
    emit("DEBUG", "host.call.start", turn, sp,
         handler="iface.workspace.sync", input={"workspace_id": "default"})
    emit("DEBUG", "host.call.complete", turn, sp, step=0.2,
         handler="iface.workspace.sync", duration_ms=240, **{"return": "ok"})
    emit("DEBUG", "host.call.start", turn, sp,
         handler="iface.vcs.checkout", input={"branch": "bug/BUG-4711"})
    emit("DEBUG", "host.call.complete", turn, sp, step=0.08,
         handler="iface.vcs.checkout", duration_ms=80, **{"return": "ok"})
    emit("DEBUG", "host.call.start", turn, sp,
         handler="iface.transport.post", input={"thread": "BUG-4711", "kind": "reproduction"})
    emit("DEBUG", "host.call.complete", turn, sp, step=0.13,
         handler="iface.transport.post", duration_ms=130, post_id="p_001")

    emit("DEBUG", "machine.world.write", turn, sp,
         key="reproduction_artifact",
         value={"confirmed": True, "test": "TestDispatcherShutdownRace", "tokens": 1820})
    emit("DEBUG", "machine.transition", turn, sp,
         **{"from": sp, "to": "reproducing._awaiting_reply"},
         intent="@auto")
    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("DEBUG", "machine.state_entered", turn, "reproducing._awaiting_reply")
    emit("INFO", "turn.end", turn, "reproducing._awaiting_reply")


# ---------------------------------------------------------------------------
# Turn 5: oracle.converse
# Scenario: a clarification agent holds a brief back-and-forth with the user
# about the expected shutdown behaviour before the fix is proposed.
# ---------------------------------------------------------------------------

def turn5_converse() -> None:
    turn = 5
    sp = "reproducing._awaiting_reply"

    advance(10)
    emit("INFO", "turn.start", turn, sp, input="before we fix this, question about shutdown semantics")

    # oracle.converse.start
    emit("DEBUG", "oracle.converse.start", turn, sp,
         verb="converse",
         agent="clarification_agent",
         model=MODEL)

    advance(3)

    # oracle.converse.complete
    emit("DEBUG", "oracle.converse.complete", turn, sp, step=0.01,
         verb="converse",
         agent="clarification_agent",
         model=MODEL,
         duration_ms=3200,
         prompt_tokens=680,
         response_tokens=142,
         cost_usd=0.0014,
         system_prompt=(
             "You are a clarification agent embedded in the bugfix pipeline. "
             "Your role is to conduct a short, focused dialogue with the operator to resolve "
             "ambiguity about requirements before the fix is proposed. "
             "Ask targeted questions, acknowledge answers, and summarise the agreed behaviour "
             "as the final message."
         ),
         prompt=(
             "We need to clarify the expected shutdown behaviour for Dispatcher before "
             "the proposing specialist authors the fix.\n\n"
             "Known facts:\n"
             "- Current code: shutdown() closes done channel without holding mu.\n"
             "- Workers: read done inside mu.\n"
             "- Race: confirmed under -race detector.\n\n"
             "Conduct a clarification dialogue with the operator."
         ),
         input={
             "messages": [
                 {
                     "role": "assistant",
                     "content": (
                         "Before I write the fix, I need to clarify the expected shutdown semantics. "
                         "Should Dispatcher.Shutdown() wait for in-flight tasks to drain before "
                         "returning, or should it signal cancellation and return immediately?"
                     ),
                 },
                 {
                     "role": "user",
                     "content": (
                         "It should drain: wait for in-flight tasks to finish, but with a timeout. "
                         "After the timeout, force-cancel any remaining tasks."
                     ),
                 },
                 {
                     "role": "assistant",
                     "content": (
                         "Understood — drain with timeout, then force-cancel. "
                         "Should the timeout be a fixed constant (e.g. 5s) "
                         "or configurable per Dispatcher instance?"
                     ),
                 },
             ]
         },
         response={
             "text": (
                 "Configurable per instance via a ShutdownTimeout field, defaulting to 5 seconds. "
                 "Zero value means block indefinitely (no force-cancel). "
                 "Summary of agreed behaviour:\n"
                 "1. Shutdown() acquires mu, signals done.\n"
                 "2. Waits up to ShutdownTimeout for all workers to exit their loops.\n"
                 "3. If timeout elapses, cancels remaining tasks via context.\n"
                 "4. Returns after all goroutines have exited."
             )
         })

    emit("DEBUG", "machine.transition", turn, sp,
         **{"from": sp, "to": "proposing._executing"},
         intent="accept")
    emit("DEBUG", "machine.world.write", turn, sp,
         key="refine_feedback",
         value="Shutdown must drain with configurable timeout (default 5s), then force-cancel.")
    emit("DEBUG", "machine.state_exited", turn, sp)
    emit("DEBUG", "machine.state_entered", turn, "proposing._executing")
    emit("INFO", "turn.end", turn, "proposing._executing")


# ---------------------------------------------------------------------------
# Build and emit the snapshot
# ---------------------------------------------------------------------------

def build_snapshot() -> dict:
    # Generate all events
    turn1_decide()
    turn2_extract()
    turn3_ask()
    turn4_task()
    turn5_converse()

    # Session header reflects the final state after turn 5
    session = {
        "session_id": SESSION,
        "app_id": APP_ID,
        "current_state": "proposing._executing",
        "turn": 5,
        "started_at": T0.strftime("%Y-%m-%dT%H:%M:%S.000Z"),
        "terminal": False,
    }

    return {
        "session": session,
        "app": APP_DEF,
        "mermaid": MERMAID,
        "events": _events,
    }


if __name__ == "__main__":
    snapshot = build_snapshot()
    json.dump(snapshot, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")

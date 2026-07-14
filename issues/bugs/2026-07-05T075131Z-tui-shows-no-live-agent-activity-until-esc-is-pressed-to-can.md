---
# --- identity ------------------------------------------------
id: "2026-07-05T075131Z-tui-shows-no-live-agent-activity-until-esc-is-pressed-to-can"
title: "TUI shows no live agent activity until Esc is pressed to cancel"
target: "kitsoki"
filed_at: 2026-07-05T07:51:31Z
filed_by: "brad"

# --- target context ------------------------------------------
component: "tui"
kitsoki_rev: "09a03b37"

# --- classification ------------------------------------------
status: "open"
labels: []

# --- evidence ------------------------------------------------
trace_ref: "/Users/brad/.kitsoki/sessions/Kitsoki/6fcb7d4b-tui-fbb1dbdc-bd10-4d96-abcd-a98bfe8823b3.jsonl"
related: []
---

# TUI shows no live agent activity until Esc is pressed to cancel

Expected: while a free-text dispatched agent task (root.landing) is running, the TUI should stream live activity (assistant messages, tool calls) as they occur.

Actual: during a ~3.5 minute agent task, the TUI showed no activity at all. Pressing Esc (which cancels) caused all the agent's prior activity to suddenly render at once, even though nothing was actually wrong with the task.

Trace evidence: the trace file records continuous agent.stream events (assistant.message, shell tool calls) from 2026-07-05T07:44:20Z through 2026-07-05T07:47:42Z under state_path root.landing, call_id cf03ea58-a051-4758-823c-062b22f65cdc — e.g.:
  {"turn":1,"seq":10,"ts":"2026-07-05T07:44:40.635183Z","kind":"agent.stream",...,"text":"The broad search hit generated artifacts..."}
  ...
  {"turn":1,"seq":83,"ts":"2026-07-05T07:47:42.606231Z","kind":"agent.stream",...}
No panics or errors appear in the trace — the underlying agent dispatch and story execution were healthy. This points to the TUI's live rendering/streaming path failing to display buffered agent.stream events until a cancel (Esc) forces a redraw/flush.

## Steps to reproduce

1. From the workbench, type a free-text request that dispatches a long-running agent task (e.g. "remove the resume session dialog from the entry to the TUI").
2. Watch the TUI while the agent works — no activity/streaming output appears.
3. Press Esc to cancel.
4. Observe all the agent's prior activity (messages, tool calls) render at once, despite no actual error.

---
# triage-marathon: ALREADY-FIXED in main — 131dc049 — first-agent-message scroll fix + regression test
# --- identity ------------------------------------------------
id: "2026-06-21T040821Z-top-of-chat-message-cut-off"
title: "Top of chat message cut off"
target: "story"
filed_at: 2026-06-21T04:08:21Z

# --- classification ------------------------------------------
severity: "med"
status: fixed
labels: []

# --- evidence ------------------------------------------------
trace_ref: "8df58c71-da1c-404c-afaf-344e3cae3b7b"
related: []
---

# Top of chat message cut off

It looks like the top of the first chat message is cut off by the header toolbar.

## Console (recent)

- [log] 🍍 "run" store installed 🆕
- [log] 🍍 "proposals" store installed 🆕

## Artifacts

- HAR capture (scrubbed): ./2026-06-21T040821Z-top-of-chat-message-cut-off.artifacts/har.json
- Session replay (rrweb): ./2026-06-21T040821Z-top-of-chat-message-cut-off.artifacts/rrweb.json
- Console log: ./2026-06-21T040821Z-top-of-chat-message-cut-off.artifacts/console.json

The HAR retains the 48 most-recent /rpc exchange(s) (ring-buffer capacity 256).

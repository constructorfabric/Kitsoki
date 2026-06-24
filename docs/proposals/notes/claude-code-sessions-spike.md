# Spike — claude-code-sessions assumption validation

Gate for phase A of
[`claude-code-sessions-proposal.md`](../claude-code-sessions-proposal.md).
The proposal's §0 lists five empirical claims (A1–A5) about the
`claude` CLI's behaviour around `--resume`, `--session-id`,
stream-json output, and `--permission-mode`. This page records
the spike that validated them.

**Environment.**

- `claude --version` → `2.1.140 (Claude Code)`.
- `tmux -V` → `tmux 3.2a`.
- Workspace: a fresh `mktemp -d`. JSONL session files land in
  `~/.claude/projects/-tmp-kitsoki-spike-<rand>/<session-id>.jsonl`.

**Headline.** All five assumptions hold as stated by the
proposal. Two small caveats are recorded under A4 and A5.

---

## A1 — alternating headless and interactive `--resume`

| | |
|---|---|
| **Claim** | A chat's JSONL survives being driven by `claude -p --resume <id>` (headless) and `claude --resume <id>` (interactive, inside tmux) in any interleaving. |
| **Result** | **PASS.** |

**Procedure.**

1. Seed a session with `claude -p --session-id <uuid> "Reply with the digit 7."` (headless).
2. Two more headless `--resume` turns to confirm continuity ("What number did you just say?" → `7`; "What is 12 plus 5?" → `17`).
3. `tmux new-session -d -s kitsoki-spike-a1 "claude --resume <id>"`; accept the workspace-trust prompt; `tmux send-keys` "Reply with the digit 42." then Enter; wait for the response.
4. `tmux kill-session -t kitsoki-spike-a1` while claude was idle.
5. `claude -p --resume <id> "What number did you say most recently?"` → reply `42`.

The final headless turn replied with the value produced by the
interactive turn. The JSONL grew from 9 lines (seed) to 29
(post-interactive) to 42 (post-final-headless), with
well-formed `user`/`assistant`/`attachment`/`queue-operation`/
`last-prompt`/`ai-title` entries throughout. The interactive
mode adds a `file-history-snapshot` event type that headless
does not emit, but it sits alongside the normal entries and is
purely additive.

**Conclusion.** Mode-switching design in proposal §5 is viable.

---

## A2 — recognisable end-of-turn marker in stream-json

| | |
|---|---|
| **Claim** | The `--output-format stream-json` NDJSON contains a recognisable terminal event. |
| **Result** | **PASS — two clean signals.** |

**Captured tail (last four lines of a representative headless
turn; content fields trimmed for readability):**

```json
{"type":"stream_event","event":{"type":"content_block_stop","index":0},…}
{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn",…}},…}
{"type":"stream_event","event":{"type":"message_stop"},…}
{"type":"result","subtype":"success","is_error":false,"duration_ms":1677,"num_turns":1,"result":"17","stop_reason":"end_turn","total_cost_usd":0.013739,"terminal_reason":"completed",…}
```

Two viable signals:

- **`{"type": "stream_event", "event": {"type": "message_stop"}}`** — emitted as the model finishes the final content block.
- **`{"type": "result", "subtype": "success", ...}`** — emitted as the `--print` invocation completes. Carries the final `result` text, `stop_reason`, `terminal_reason`, `is_error`, total cost, and full token usage. This is the cleanest, highest-level signal — also the one to read when you want the assistant's reply text without reassembling it from `content_block_delta` events.

§9.1 idle-detection should key on `type: "result"`.
`is_error: true` distinguishes failed runs without inventing a
heuristic. `terminal_reason` (`"completed"` / others) is the
secondary disambiguator.

---

## A3 — `--session-id` only on the first invocation

| | |
|---|---|
| **Claim** | `--session-id <uuid>` is required only when minting a brand-new session id. Subsequent invocations use `--resume <id>` alone. |
| **Result** | **PASS.** |

Already the pattern in
[`internal/host/agent_ask_with_mcp.go`](../../../internal/host/agent_ask_with_mcp.go:546)
(headless retry loop). Behavioural confirmation: after the first
`-p --session-id <uuid>` call created the JSONL, four subsequent
`-p --resume <uuid>` calls succeeded across multiple turns, with
conversation continuity intact. No `--session-id` was re-passed
on any resume.

Inversion check: an attempt to re-use `--session-id` against an
existing id yields `"Session ID … is already in use"` (noted in
the existing code comment at `agent_ask_with_mcp.go:549`).

**Conclusion.** Schema is already correct: `chats.claude_session_id`
is the persistent identifier; spawn paths choose the flag based
on `claudeSessionMinted` as today.

---

## A4 — clean recovery when tmux kills mid-turn

| | |
|---|---|
| **Claim** | Killing the tmux session while claude is generating leaves the JSONL in a state that `claude --resume` opens cleanly. |
| **Result** | **PASS, with a small wrinkle.** |

**Procedure.**

1. Seed a session (`claude -p --session-id <uuid>` → `"ready"`).
2. `tmux new-session -d …  "claude --resume <id> …"`; accept trust prompt.
3. Send a long-generation prompt (`"Count from 1 to 50, one number per line, with brief commentary…"`); poll until the JSONL grows (assistant turn started); wait ~1 second; `tmux kill-session`.
4. `claude -p --resume <id> "Please confirm you are responsive. Reply with OK."` → exit 0, replied `OK`.
5. Follow-up `-p --resume` with `--output-format stream-json` → terminated with a normal `type: "result"` event.

**Wrinkle.** The killed user prompt is persisted to the JSONL,
but its assistant reply is absent. On the *next* `--resume`,
claude injects a synthetic user-side recovery turn:

```json
{"type":"user","text":"Continue from where you left off."}
```

…which the model often answers with a brief filler reply
(observed: `"No response requested."`). The next real user turn
then proceeds normally. **Implication for §11.2 /
`kitsoki chat detach --mode stop`:** the chat is not corrupted,
but the first user turn after a mid-generation kill will see one
extra synthetic exchange in the transcript. Authors should
either expect this or have the dispatcher silently discard the
synthetic pair before showing transcript output. Minor — does
not block the proposal.

---

## A5 — `--permission-mode` is per-invocation

| | |
|---|---|
| **Claim** | The permission flag chosen for a `--resume` invocation governs that invocation only; it is not persisted into the session file. |
| **Result** | **PASS — strongly confirmed.** |

Four-way control to distinguish "flag persistence" from "implicit
permissive default in `-p` mode without a flag." Each control
asks claude to invoke the `Write` tool and observes whether the
file was actually created:

| # | Setup | Flag passed | `Write` succeeded? |
|---|---|---|---|
| 1 | Fresh session id | *(none)* | **YES** (file written) |
| 2 | Fresh session id | `--permission-mode default` | **NO** (`permission_denials: [Write]`) |
| 3 | Resume control-2's id | `--permission-mode bypassPermissions` | **YES** (file written) |
| 4 | Resume control-2's id again | `--permission-mode default` | **NO** (`permission_denials: [Write]`) |

Notes:

- The `result` event reports denials via a structured
  `permission_denials: [{tool_name, tool_use_id, tool_input}, …]`
  array, not just `is_error: true`. Useful surface for telling
  the user *what* was refused.
- Controls 3 and 4 are the load-bearing pair. Bypass in 3 lets
  the write through; reverting to `default` in 4 refuses it
  again on the *same* session id — meaning bypass did **not**
  linger. Permission state is per-invocation.
- Control 1 vs. 2 explains a confusing initial observation: when
  the flag is omitted in `-p` mode, the headless path is
  permissive (a separate, documented default). When `default` is
  passed explicitly, tool use is gated and refused because there
  is no human to prompt. Today's
  [`agent_ask_with_mcp.go:569`](../../../internal/host/agent_ask_with_mcp.go:569)
  passes `bypassPermissions` explicitly — clear and correct.
- The JSONL contains a `{"type":"permission-mode","permissionMode":"<value>"}`
  log entry per invocation. This is a record, not a state file:
  changing the flag on the next `--resume` overrides it.

**Implication for §11.2.** The proposed per-chat permission
level (`interactive-only` / `bypass-when-headless` /
`bypass-always`) is straightforward to enforce: the spawn path
reads the chat-level setting and passes the corresponding
`--permission-mode` flag on every invocation. There is no
sticky CLI-level grant to revoke and no JSONL state to migrate
when the chat's level changes.

---

## Summary

| # | Assumption | Verdict | Phase-A impact |
|---|---|---|---|
| A1 | Alternating headless/interactive `--resume` against one JSONL is safe | **PASS** | §5 mode-switching design viable as written |
| A2 | Recognisable end-of-turn marker in stream-json | **PASS** | §9.1 idle-detection keys on `type: "result"` |
| A3 | `--session-id` only on first invocation; `--resume` thereafter | **PASS** | No schema change; spawn paths already correct |
| A4 | Tmux kill mid-turn leaves the JSONL resumable | **PASS** (synthetic "Continue from where you left off." user turn injected on next resume) | §11.2 `--mode stop`: expect one synthetic exchange after kill; transcript code may want to filter it |
| A5 | `--permission-mode` is per-invocation, not persisted | **PASS** | §11.2 per-chat permission level is a spawn-time flag choice; no migration concerns |

**Phase A is unblocked.**

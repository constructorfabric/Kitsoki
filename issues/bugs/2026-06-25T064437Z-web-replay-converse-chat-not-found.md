---
assignee: ""
component: host
external: {}
filed_at: "2026-06-25T06:44:37Z"
id: 2026-06-25T064437Z-web-replay-converse-chat-not-found
kitsoki_rev: 1162ac96
severity: P2
status: fixed
target: kitsoki
title: host.agent.converse fails 'chat not found' under `kitsoki web --harness replay` — prior-art room shows a bogus 'scan failed' banner
trace_ref: ""
url: issues/bugs/2026-06-25T064437Z-web-replay-converse-chat-not-found.md
---

## Body

Under `kitsoki web --harness replay` (with a host cassette), `host.agent.converse`
fails with `get chat <id>: chats: chat not found`. In the slidey-dev / dev-story
PRD walk this sets `world.last_error` during PRD discovery, and the downstream
prior-art **search** room then renders a bogus banner:

    ⚠ Prior-art scan failed — host.agent.converse: get chat chat-1: chats: chat not found

The overlap scan itself succeeded ("No overlapping work found"); the banner is
pure collateral from the failed converse call. Any conversation-driven demo or
flow that runs `host.agent.converse` through the web replay harness hits this.

### Root cause

`internal/host/agent_converse.go` runs a **real** chat-store lookup
`cs.Get(ctx, chatID)` — only the agent *dispatch* (the model call) is mocked
under replay. `chatID` (e.g. `chat-1`) is the id returned by `host.chat.resolve`.
But under the **web replay harness**, `host.chat.resolve` is satisfied from the
host cassette episode (returns `chat_id: chat-1`) **without running the real
resolve handler**, so the chat is never created in the store. converse's real
`cs.Get(chat-1)` then fails (`internal/chats/store.go` → `ErrChatNotFound`).

### Why it passes under `kitsoki test flows` but fails under `kitsoki web`

The flow `stories/slidey-dev/flows/pm_idea.yaml` host_handlers and the web cassette
`stories/slidey-dev/assets/pm_idea-host.cassette.yaml` are **identical** for
`chat.resolve` / `converse` (both return `chat-1`), yet `kitsoki test flows …
pm_idea.yaml` passes 3/3. So the two replay harnesses intercept host calls
**differently**:

- `test flows` — the stub fully REPLACES `host.agent.converse` → no real
  `cs.Get`, no chat needed.
- `kitsoki web --harness replay` — runs the REAL converse handler with only the
  agent dispatch mocked → real `cs.Get` → needs `chat-1` to exist → fails.

That asymmetry is the bug.

### Steps to reproduce

1. `make build-bin`
2. `./bin/kitsoki web --stories-dir stories/slidey-dev --harness replay \
      --recording stories/slidey-dev/assets/pm_idea-recording.yaml \
      --host-cassette stories/slidey-dev/assets/pm_idea-host.cassette.yaml \
      --addr 127.0.0.1:7799 --db /tmp/x.db`
3. Drive the PRD discovery turn (type the idea), then advance toward the
   prior-art search room.
4. The search room shows "⚠ Prior-art scan failed — host.agent.converse: get
   chat chat-1: chats: chat not found".

### Expected vs actual

**Expected:** under the web replay harness, a cassette-stubbed `host.chat.resolve`
followed by `host.agent.converse` succeeds — converse returns the stubbed answer
without a "chat not found" error, and `world.last_error` stays empty.

**Actual:** converse's real `cs.Get` fails because the stubbed resolve never
created the chat; `world.last_error` is set and surfaced as a "scan failed"
banner.

### Proposed fix sketch

1. **Preferred** — unify the two replay-harness host-call interception paths so
   `test flows` and `kitsoki web --harness replay` can't diverge (removes the
   whole class of "passes in flows, fails in web").
2. Or — when a cassette episode matches a side-effecting handler
   (`host.chat.resolve`), still run the real handler's side effect (create the
   chat) and only override the returned data — i.e. cassette = response override,
   not handler bypass, for chat-store-mutating handlers.
3. Or (narrowest) — under replay, have `host.agent.converse` create-on-miss for
   the chat, mirroring what resolve would have done.

Add a regression test that drives `host.agent.converse` through the web replay
harness with a cassette-stubbed `host.chat.resolve` and asserts no "chat not
found" error (RED before the fix, GREEN after).

### Severity rationale

P2: a real error surfaced on a user-visible surface in every web-replay
conversation demo, but with a working alternate path (`test flows`) and no
state-machine correctness loss. The story is correctly EXPOSING the runtime issue
(per `stories/AGENTS.md`) — fix the runtime, not the story.

### Files involved

- `internal/host/agent_converse.go` — the real `cs.Get` on the converse path.
- `internal/host/chat_handlers.go` — `host.chat.resolve` create vs stub.
- the web replay-harness host-call interception (vs the `test flows` path) —
  where the two diverge.
- `internal/chats/store.go` — `ErrChatNotFound`.
</content>

## Comment 2026-06-25T07:01:58Z by kitsoki


### Reproduction artifact: 2026-06-25T064437Z-web-replay-converse-chat-not-found

## Bug: host.agent.converse fails with 'chat not found' under `kitsoki web --harness replay`

### What is broken

Under `kitsoki web --harness replay` (with a host cassette), `host.agent.converse` fails with:

```
host.agent.converse: get chat chat-1: chat not found: chat-1
```

This sets `world.last_error` and causes any downstream room (e.g. a prior-art search room) to display a bogus error banner even though the underlying operation succeeded.

### Root cause

The bug is an asymmetry between the two replay paths:

- **`kitsoki test flows`** — the host cassette fully *replaces* `host.agent.converse` with a stub. No real `cs.Get` call happens; no chat needs to exist in the store. ✅ Passes.
- **`kitsoki web --harness replay`** — the host cassette stubs only the *agent dispatch* (the model call). The **real** `AgentConverseHandler` runs and calls `cs.Get(ctx, chatID)` inside `doConverseChatTurn`. Meanwhile, the immediately prior `host.chat.resolve` call was intercepted by the cassette and returned `chat_id: "chat-1"` **without** running the real `ChatResolveHandler`, so the chat was never created in the SQLite store. The real `cs.Get` then fails with "chat not found". ❌ Fails.

### Reproduction evidence

A regression test is on disk at `internal/host/agent_converse_replay_repro_test.go`. It simulates the web replay harness: an empty `ChatStore` (no pre-created chats), a `FakeConverse` runner, and a call to `AgentConverseHandler` with `chat_id: "chat-1"` that was "returned by a cassette-stubbed resolve" (i.e. the chat was never created). The test asserts correct behaviour (no "chat not found" error) and is **RED** on the current unfixed tree.

**Run command:**
```
go test ./internal/host/ -run TestAgentConverse_ReplayHarness_CassetteChatID -v -count=1
```

**Failing output (actual):**
```
--- FAIL: TestAgentConverse_ReplayHarness_CassetteChatID (0.00s)
    agent_converse_replay_repro_test.go:74: BUG 2026-06-25T064437Z-web-replay-converse-chat-not-found reproduced:
        host.agent.converse returned 'chat not found' for a cassette-supplied chat_id.
        Under kitsoki web --harness replay, host.chat.resolve is cassette-stubbed and
        never creates the chat in the live store, so the real cs.Get fails.
        Result.Error: "host.agent.converse: get chat chat-1: chat not found: chat-1"
FAIL    kitsoki/internal/host   0.692s
```

### Services / components implicated

- `internal/host/agent_converse.go` — `doConverseChatTurn` calls `cs.Get(ctx, chatID)` unconditionally; does not handle the cassette-supplied case where the chat was never created in the live store.
- `internal/host/chat_handlers.go` — `ChatResolveHandler` (real) creates the chat via `cs.Resolve`; bypassed by the cassette without side-effecting the store.
- `internal/testrunner/cassette.go` — the host cassette that intercepts `host.chat.resolve` responses without running the real handler's create side-effect.
- `internal/chats/store.go` — `ErrChatNotFound` propagates upward from the store layer.

_phase: reproducing_2026-06-25T064437Z-web-replay-converse-chat-not-found_0_

## Comment 2026-06-25T07:07:52Z by kitsoki

### Fix proposal: 2026-06-25T064437Z-web-replay-converse-chat-not-found

## Bug

`host.agent.converse` fails with **"chat not found"** under `kitsoki web --harness replay` when a preceding `host.chat.resolve` call was intercepted by the host cassette. The cassette returns a synthetic `chat_id` without running the real `ChatResolveHandler`, so no row is ever inserted into the SQLite store. The real `AgentConverseHandler` then calls `cs.Get(ctx, chatID)` inside `doConverseChatTurn`, finds nothing, and propagates `ErrChatNotFound` as `Result.Error`, which sets `world.last_error` and causes downstream rooms (e.g. a prior-art search room) to show a bogus error banner.

## Root Cause

**`internal/host/agent_converse.go`, line 361** — `doConverseChatTurn` calls `cs.Get(ctx, chatID)` unconditionally. This is correct in the normal path (`host.chat.resolve` ran the real handler and created the row first), but wrong in the replay-harness path where the cassette intercepted `host.chat.resolve` and returned a canned `chat_id` **without** performing the store side-effect.

The asymmetry is:
- `kitsoki test flows` — `host.agent.converse` is also fully cassette-stubbed, so `cs.Get` is never reached. ✅
- `kitsoki web --harness replay` — only the LLM dispatch is mocked; the real handler runs, reaches `cs.Get`, and fails. ❌

## Fix

Add **`GetOrEnsure(ctx, chatID) (*ChatRecord, error)`** to the `host.ChatStore` interface. Semantics:
- If the chat row exists → return it (identical to `Get`).
- If not → `INSERT OR IGNORE` a minimal row (`id=chatID`, `app_id=''`, `room=''`, `scope_key=''`, `title='untitled chat'`, `status='active'`), then return the newly created row.

In `doConverseChatTurn`, replace the single call `cs.Get(ctx, chatID)` (line 361) with `cs.GetOrEnsure(ctx, chatID)`.

In `chats.Store`, implement via `INSERT OR IGNORE` (idempotent — no-op if row already exists). The adapter, `fakeChatStore`, and `chatStoreProbe` each get a trivial implementation. The `INSERT OR IGNORE` means normal production behaviour is completely unchanged; the method only diverges from `Get` in the replay-harness case.

## Files

| File | Change |
|------|--------|
| `internal/host/chats.go` | Add `GetOrEnsure` to `ChatStore` interface |
| `internal/chats/store.go` | Implement `GetOrEnsure` on `*Store` using `INSERT OR IGNORE` |
| `internal/chathost/adapter.go` | Thin passthrough: `a.s.GetOrEnsure(ctx, chatID)` |
| `internal/host/agent_converse.go` | Replace `cs.Get` → `cs.GetOrEnsure` at line 361 |
| `internal/host/chat_handlers_test.go` | Add `GetOrEnsure` to `fakeChatStore` (get-or-create in map) |
| `internal/orchestrator/hostdispatch_test.go` | Add `GetOrEnsure` to `chatStoreProbe` (nil, nil no-op) |

## Confidence: 0.92

The reproduction test is authoritative and the call path is fully traced. The `INSERT OR IGNORE` idiom is idempotent and safe under the existing `WithLock` envelope. Risk: `runAgentAskWithMCPWithChat` (`agent_ask_with_mcp.go` ~line 506) has the same latent `cs.Get` pattern and the same exposure — out of scope here, but the implementer should note it.

_phase: proposing_2026-06-25T064437Z-web-replay-converse-chat-not-found_0_

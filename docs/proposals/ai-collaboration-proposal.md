# Proposal — AI Collaboration & Parallel Observability

**Status:** Draft. Authored by an AI collaborator (Claude) working with
the human author on cyber-repo's `devstory` story.

**Problem:** an AI agent reading/editing this codebase can build rooms,
write flows, and modify kitsoki itself without much friction.  But it
cannot *experience* the running app the way a human does:

- `kitsoki run` requires a TTY.
- The `--trace` file captures internal events (turns, harness calls,
  machine transitions) but not the rendered view — so the AI has no
  way to see what the human actually saw on screen.
- There's no stateless "apply one turn" endpoint, so the AI can't
  quickly answer "what happens if I type X in state Y with world Z?"
  without spinning up a real session.
- There's no way to peek at a running session without disturbing it.
- Natural-language input goes through the harness (claude CLI); the AI
  can't test those paths without invoking the real LLM.

Bugs fall through the gap.  The most recent example: the Terminal Room
hung forever at "Please wait…" after `accept`, because kitsoki's `Effect`
has no `on_success` hook.  The AI had written and tested the room but
couldn't see this — it only surfaced when the human actually drove it.

This proposal adds five small kitsoki surfaces that turn the blind spots
into shared context.  Each is independently useful.  The first two are
the highest-leverage; the rest are progressively bigger.

---

## 1. View-rendered bytes in the trace  *(~30 LoC; highest priority)*

### Change

Add a `view_rendered` field to `turn.done` (and to a new `view.rendered`
event emitted on every state entry).  Field is the rendered view as
plain text.

```json
{"msg":"turn.done","state":"terminal_result","mode":"transitioned",
 "view_rendered":"Terminal › Result\n\n$ standctl pods mc-clean-24794\n\nNAME   READY\n..."}
```

### Why it's huge

The human runs:

```
kitsoki run app.yaml --trace /tmp/t.jsonl --trace-pretty /tmp/t.log
```

today.  With this change the trace file becomes a complete after-the-
fact transcript.  When the human says "after I accepted, the view was
wrong" the AI can read the trace and see exactly what the view was.
No re-running, no mystery.  Byte-for-byte reproducibility of the human
experience.

Bonus: `kitsoki record` and `kitsoki test flows` get strictly richer data
for free — flows could assert against the rendered text, not just the
state name.

### Implementation sketch

In `internal/tui/render.go` (or wherever `renderView` lives), after
producing the view string, emit it via the existing tracer:

```go
tr.Debug("view.rendered", "state", st, "bytes", len(view), "view", view)
```

Add to `turn.done` event emission in `internal/orchestrator/`:

```go
tr.Debug("turn.done", ..., "view_rendered", view)
```

Cost: ~30 LoC across two files.  No schema migration — JSONL is
dict-typed.

---

## 2. `kitsoki turn` — stateless one-shot turn execution  *(~200 LoC)*

### Command

```
kitsoki turn <app.yaml> --state <S> [--world @file.json] [--slots @file.json] \
           (--intent <name> | --input "<text>") \
           [--harness replay --oracle <path>]
```

Output: JSON on stdout.

```json
{
  "prev_state": "terminal_idle",
  "next_state": "terminal_reviewing",
  "transitions": [{"from":"terminal_idle","to":"terminal_reviewing","intent":"propose"}],
  "effects_applied": [
    {"type":"set","key":"proposal_raw","before":"","after":"check pods on mc-clean-24794"},
    {"type":"invoke","namespace":"host.run"}
  ],
  "host_calls": [
    {"host":"host.run","cmd":"...","stdout":"...","exit_code":0}
  ],
  "world_before": {...},
  "world_after": {...},
  "view_rendered": "Terminal › Review\n\nYou asked for:\n  check pods on mc-clean-24794\n..."
}
```

### Why

- AI can answer "what happens if I type X in state Y with world Z?"
  deterministically, without spinning up a session.
- AI can probe new rooms in isolation.  "If proposal_cmd is empty and
  the user hits accept, does it blow up?"  A shell loop over every
  state × intent pair surfaces INTENT_NOT_ALLOWED_IN_STATE mismatches
  faster than flow tests.
- A fast compliance tool: "for every state, run a noop turn and
  confirm the view is non-empty and < N rows".

### Implementation sketch

Reuse existing machinery:
- `internal/app.Load` to parse app.yaml
- `internal/machine.New` to build the machine
- `internal/orchestrator` to fire one turn
- Skip TUI entirely — dump the orchestrator's turn result as JSON.

Cost: one new `cmd/kitsoki/turn.go` (~150 LoC) plus a small reshape of
orchestrator to expose a `SingleTurn(state, world, input)` entry point
(~50 LoC).  Both already exist in slightly-different shapes for the
test runner.

---

## 3. `kitsoki inspect --session-id <id>`  *(~150 LoC)*

### Command

```
kitsoki inspect --session-id brad-devstory-2026-04-23
```

Output: JSON snapshot of the live session.

```json
{
  "session_id": "...",
  "app": "devstory",
  "current_state": "terminal_result",
  "world": {...},
  "last_turns": [
    {"turn":17,"input":"accept","next_state":"terminal_result","view_bytes":1842},
    ...
  ],
  "last_view": "...",
  "pending_jobs": [],
  "pending_notifications": []
}
```

Read-only.  Does not take a lock.  Doesn't disturb the live session.

### Why

When the human says "something just broke", the AI can run this
against the live session-id and see exactly what kitsoki thinks is
going on — current state, last few inputs, world-slot values, last
rendered view.  Like `tmux attach` but read-only and JSON-structured.

Works alongside `--trace` — the trace file captures *history*,
inspect captures the *now*.

### Implementation sketch

`cmd/kitsoki/inspect.go` opens the SQLite session store read-only,
joins the relevant events, formats as JSON.  Everything it needs is
already persisted — nothing new to capture.

---

## 4. `kitsoki drive` — headless scripted driver  *(~300 LoC)*

### Command

```
kitsoki drive <app.yaml> --script inputs.txt --transcript out.md \
            [--harness claude|replay --oracle <path>]
```

`inputs.txt` is human-typed text, one line per turn:

```
consult the oracle
how does the ZTA proxy work
go back
open terminal
check pods on mc-clean-24794
accept
```

Runs each line through the real harness (same code path as the TUI),
writes a rich markdown transcript:

```md
## Turn 1 — main
> consult the oracle
routed → `go_oracle` (confidence 0.95, 3.2s)
view:
    Oracle — interactive Claude session
    ...
```

### Why

Until now the AI has no way to test the claude-harness path end-to-end.
Flow tests route structured intents directly; they never exercise "does
Claude-haiku route 'go to debug room' to go_debug?".  `kitsoki drive`
gives the AI a headless but end-to-end driver.  When the AI changes a
room's intent examples, it can re-drive a known input script and
confirm the routing didn't regress — at real cost (claude billable
turns), but deterministic and scriptable.

Also useful as a "smoke test the full app" harness in CI.

### Implementation sketch

Similar to `kitsoki record` (which already does this up to a point, but
consumes a pre-routed flow YAML).  Refactor: move the turn-loop core
out of `cmd/kitsoki/run.go` so both `run` (interactive) and `drive`
(scripted) share it.

---

## 5. Transient loading surface for on_enter  *(~50 LoC; TUI UX)*

### Problem

`on_enter` can invoke a slow `host.run` (our claude-draft takes 10-60s).
While it runs, the TUI shows the *previous* state's view with no
indication anything's happening.

### Change

Per-state YAML field:

```yaml
terminal_reviewing:
  loading_view: |
    Drafting your command…
    (Claude is investigating — this may take up to a minute)
  on_enter:
    - invoke: host.run
      with: {...}
```

When transitioning into a state that has `loading_view`, render that
first, wait for `on_enter` host calls to finish, then render `view`.

Alternative: automatic spinner in the input line while any `on_enter`
host.run is outstanding, with no per-state opt-in required.

### Why

The AI author can't see this problem from outside the TUI.  It only
surfaced because the human reported "it hangs for a minute".  Closing
the gap makes the pattern safe for room authors to use slow `on_enter`
handlers.

---

## Suggested rollout

1. **Ship #1 alone first.**  30 LoC, unblocks everything.  Any
   `--trace` consumer (AI, future devstory analytics, flow-test
   assertions) gets the rendered view for free.

2. **Then #2 + #3 together.**  They share the orchestrator refactor
   and together give the AI two complementary lenses: exploration
   (stateless) and observation (live).

3. **#4 later** — bigger but orthogonal.  Wait until we've found real
   uses for it.

4. **#5 whenever UX polish bubbles up.**

---

## Non-goals

- A web UI for kitsoki.
- A plugin system.  The AI works fine with Go code; it doesn't need
  dynamic loading.
- Read-write session inspection.  `kitsoki inspect` stays read-only;
  mutating a live session from another process is a recipe for sadness.
- Replacing `kitsoki test flows` — they stay the primary correctness
  harness.  `kitsoki turn` / `kitsoki drive` are complementary tools.

---

## Why this is worth doing

The cyber-repo `devstory` story is built by an AI agent and driven by
a human.  Every bug that only the human sees is a bug the AI wrote
blind.  These five surfaces turn "blind writing" into "shared
observation" at low total cost (~700 LoC across 5 commands, most of
which is JSON formatting of things that already exist).

The asymmetric win is #1 — 30 lines to make `--trace` a complete
transcript.  Start there.

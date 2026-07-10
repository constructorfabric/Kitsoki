# Proposal — Pending AI-collaboration surfaces

**Status:** v1 trimmed. The original draft proposed five surfaces;
three have shipped and are now documented in normal docs:

- **View-rendered bytes in the trace** — `view_rendered` field on
  `turn.done` events. See
  [`docs/architecture/overview.md` §11.5](../architecture/overview.md) and the
  emit sites in `internal/orchestrator/orchestrator.go`.
- **`kitsoki turn`** — stateless one-shot turn execution. See
  [`docs/guide/development/developer-guide.md` §6](../guide/development/developer-guide.md) and
  `cmd/kitsoki/turn.go`.
- **`kitsoki inspect --session-id <id>`** — read-only session
  snapshot. See [`docs/guide/development/developer-guide.md` §6](../guide/development/developer-guide.md)
  and `cmd/kitsoki/inspect.go`.

One surface below remains in design: `loading_view` per-state YAML
(§2). The TUI's automatic spinner (the alternative noted in §2) is
already implemented.

§1 (`kitsoki drive`) has been **superseded** by the interactive drive loop
that shipped under [`mcp-studio`](../architecture/mcp-studio.md), now consumed
by the [`story-qa`](../stories/story-qa.md) skill (formerly the
`story-qa-agent` epic, since deleted per the proposal lifecycle). The
scripted-input driver sketched below is the wrong shape for an AI *agent*,
which decides its next input from what it just saw; the shipped
**interactive** `kitsoki drive` (free-text input, live or replay harness, VCR
cassette modes, human-fidelity frame per turn) is that reframing. §1 is
retained below only as the original sketch it built on.

**Context.** The motivating `devstory` story is built by an AI
agent and driven by a human. Every bug that only the human sees
is one the AI wrote blind. The shipped trace + turn + inspect
surfaces narrowed that gap; these two would close it further.

---

## 1. `kitsoki drive` — headless scripted driver *(~300 LoC)*

### Command

```
kitsoki drive <app.yaml> --script inputs.txt --transcript out.md \
            [--harness claude|replay --recording <path>]
```

`inputs.txt` is human-typed text, one line per turn:

```
consult the agent
how does the ZTA proxy work
go back
open terminal
check pods on mc-clean-24794
accept
```

Runs each line through the real harness (same code path as the
TUI), writes a rich markdown transcript:

```md
## Turn 1 — main
> consult the agent
routed → `go_agent` (confidence 0.95, 3.2s)
view:
    Agent — interactive Claude session
    ...
```

### Why

Today the AI has no way to test the claude-harness path
end-to-end. Flow tests route structured intents directly; they
never exercise "does Claude-haiku route 'go to debug room' to
`go_debug`?" `kitsoki drive` gives the AI a headless but
end-to-end driver. When the AI changes a room's intent examples,
it can re-drive a known input script and confirm the routing
didn't regress — at real cost (claude billable turns), but
deterministic and scriptable.

Also useful as a "smoke test the full app" harness in CI.

### Implementation sketch

Similar to `kitsoki record` (which already does this up to a
point, but consumes a pre-routed flow YAML). Refactor: move the
turn-loop core out of `cmd/kitsoki/run.go` so both `run`
(interactive) and `drive` (scripted) share it.

---

## 2. Transient loading surface for `on_enter` *(~50 LoC; TUI UX)*

### Problem

`on_enter` can invoke a slow `host.run` (our claude-draft takes
10-60s). While it runs, the TUI shows the *previous* state's view
with no indication anything's happening.

The automatic-spinner alternative noted below has shipped (see
`internal/tui/tui.go`'s `pendingKind` / `pendingDeterministic`
branch), which addresses the worst case. The per-state authored
loading copy below is still useful for rooms that want a more
descriptive "Drafting your command…" message rather than a
generic spinner.

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

When transitioning into a state that has `loading_view`, render
that first, wait for `on_enter` host calls to finish, then render
`view`.

### Why

The AI author can't see this problem from outside the TUI. The
generic spinner closes the worst case; an authored
`loading_view:` lets rooms with predictable slow effects tell the
user what's happening, not just that *something* is.

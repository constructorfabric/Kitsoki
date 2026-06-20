# Recipe: a deterministic flow test (with a host cassette)

**Goal:** lock a story's behaviour so a change can't silently regress
it — without spending LLM tokens.

A **flow test** (Mode 2) drives the state machine with explicit
`intent:` turns and asserts on the resulting state, world, view, and
inbox. This is the test you write for almost every bug.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: river
initial_world:
  water_depth: 2

turns:
  - intent: { name: ford, slots: {} }
    expect_state: riverbank
    expect_world: { water_depth: 2 }

  - intent: { name: look, slots: {} }
    expect_view_matches: "You made it across"

expect_no_errors: true
```

When a turn drives a host or agent call that you want to be
deterministic, attach a **host cassette** instead of real handlers.
`host_cassette:` and `host_handlers:` are mutually exclusive.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: proposing
host_cassette: cassettes/bugfix-happy.yaml

turns:
  - intent: { name: accept, slots: {} }
    advance_clock: "200ms"          # virtual clock for async work
    expect_state: implementing
    expect_inbox: { unread: 1 }
```

```yaml
# cassettes/bugfix-happy.yaml — the cassette file
kind: host_cassette
app_id: bugfix
episodes:
  - id: phase_2_agent
    match:
      handler: host.agent.decide
      phase: proposing
    response:
      data:
        submitted: !include artifacts/proposing.json
```

> **Before you call it fixed:** confirm the test FAILS without your
> change. A test that passes either way isn't testing the fix. For bugs
> spanning concurrent I/O or rendering, assert on the *combined* output
> the user actually sees — see the tracing overview and `CLAUDE.md`.

**Reference**
- [`../tracing/testing.md`](../tracing/testing.md) — Mode 1 vs Mode 2, fixture shape, assertions
- [`../tracing/cassettes.md`](../tracing/cassettes.md) — episode matching, `!include`, record mode, CI safety
- [`../tracing/README.md`](../tracing/README.md) — why the trace is the authoritative state

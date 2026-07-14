# Proposal — Auto-advancing states (default-on)

**Status:** Not implemented. Motivated by the focused-engineering bugfix
pipeline's e2e validation (May 2026), where most state transitions are
deterministic compute-then-route and the external driver (loop.py) has
to fire `done` repeatedly just to walk the FSM forward.

**Tldr:** Make kitsoki auto-fire `done` after a state's on_enter chain
completes successfully. States that genuinely wait for an external
event opt out with `wait: true`. Kitsoki is pre-release; this becomes
the default behaviour, no backward-compat layer.

---

## 1. Problem

Today every `_executing` state follows the same protocol whether it
genuinely waits or not:

1. State entered.
2. Run on_enter chain — host calls fire, bindings apply.
3. Sit at the state.
4. **Wait for an external `--intent done` (or `on_error`) to fire the
   `on.done` arc.**

Step 4 makes sense for genuine waits — operator endorsement, reviewer
PR-comment polling, time-based queues. It's a tax on compute phases
that just run their host calls and route.

In a typical bug-fix pipeline the wait/compute ratio is ~1:4 — only the
4 stage checkpoints (`phase_1_7`, `phase_6`, `phase_9`, `phase_9_7`)
and the PR-event idle state (`phase_12_6_awaiting_reply`) actually
wait. The other 15+ are compute. Today the driver (loop.py) wakes up
every 60s, polls `hally session show`, sees state `_executing`, and
fires `--intent done` to nudge the FSM forward. This:

- **Wastes the operator's intuition.** Every `_executing` reads
  identical in YAML; the structural difference between "compute" and
  "wait" isn't visible.
- **Couples state-machine semantics to driver poll cadence.** A
  20-phase compute run takes 20 × 60s = 20 min of pure poll latency
  before any real work happens.
- **Pollutes flow tests.** Half of `stories/bugfix/flows/happy.yaml`
  is `intent: done` boilerplate between phase assertions.
- **Forces every driver to reimplement the same pump.** loop.py has
  it. `kitsoki turn` has it. Manual e2e tests fire `done` by hand
  five times to walk through a single dispatch.

The driver should fire intents that represent **real external events**
(`pr_comment`, `pr_merged`, operator `continue`/`quit`/`refine`). It
shouldn't manually pump the FSM through deterministic transitions.

---

## 2. Proposed default

**Auto-advance is the default for any state with an on_enter chain.**

When a state's on_enter chain completes without raising or routing to
an `_error` state, kitsoki internally fires `done` against the state's
`on.done` arc. The arc's guards run normally; whichever branch matches
transitions. No external driver call required.

```yaml
states:
  phase_minus_1_executing:
    on_enter:
      - invoke: host.run
        with: { cmd: "python3 -m bugfix context --phase phase_minus_1 ..." }
        bind: { phase_minus_1_context: stdout_json }
      - invoke: host.agent.ask_with_mcp
        with: { ... }
        bind: { phase_minus_1_artifact: submitted.summary_markdown }
    on:
      done:
        - target: phase_0_executing
    # No wait: true — auto-advance fires `done` after on_enter completes.
```

States that genuinely wait declare it:

```yaml
states:
  phase_1_7_awaiting_reply:
    wait: true                    # ← explicit opt-out
    description: "Operator endorses the reproduction stage on Jira"
    on:
      continue:
        - target: phase_3_executing
        - when: "slots.last_reply_author in world.allowed_authors"
      quit:
        - target: terminated
      refine:
        - target: phase_1_7_executing
        - effects: [{ set: { refine_feedback: "{{ slots.feedback }}" } }]
```

When `wait: true`:

- kitsoki does NOT auto-fire `done` after on_enter (the chain may not
  even exist for pure wait states).
- The state sits until an external intent fires one of its declared
  arcs.
- This is the only path that gets "external dispatch required"
  semantics.

### Auto-fire rules

After every on_enter chain completes, kitsoki evaluates:

1. **Any per-step `on_error:` redirected the FSM** → no auto-fire.
   The error route already transitioned us elsewhere.
2. **State has `wait: true`** → no auto-fire. Wait states are
   declared.
3. **State has no `done` arc declared in `on:`** → load-time
   validation error (states without `done` arcs and without
   `wait: true` are authoring bugs).
4. **Otherwise** → kitsoki internally fires `done` against the state's
   `on.done` arc, using the same code path an external
   `--intent done` would.

### Interaction with `on_error`

Per-step `on_error:` on a host call routes immediately when that step
raises. That's a transition BEFORE auto-fire would have fired, so
auto-fire is a no-op on the failure branch. No change to how authors
write `on_error:`.

### Interaction with cycle budgets

Existing template arcs use `when: world.cycle__X__on_failure < 3` —
guards evaluate against the world at fire time. Auto-fire fires `done`
at the same point an external driver would have, so the guards see the
same world snapshot. No behaviour change.

### Interaction with `host.agent.ask_with_mcp` retries

The agent's validator retries up to N times. Auto-fire doesn't run
until the call returns (success after retries, or final failure that
routed via `on_error`). No race.

### Interaction with background host calls

A host call marked `background: true` returns from the turn before
finishing. Auto-fire fires immediately on the foreground chain's
completion — the background call's result lands in a later turn via
whatever intent the background completion declares. (No change from
today's foreground/background separation.)

---

## 3. Implementation sketch

~30–50 lines of Go in the orchestrator.

**Files touched:**

- `internal/app/types.go` — add `Wait bool` to the `State` struct.
- `internal/app/loader.go` — load + validate. Error at load time if
  a state has an on_enter chain AND no `wait: true` AND no `done` arc.
- `internal/orchestrator/orchestrator.go` — at the end of
  `runOnEnter`, when (no `on_error:` step redirected) AND
  (state has on_enter chain) AND (state is not `wait: true`),
  dispatch `done` internally via `SubmitDirect("done", nil)`. The
  internal fire shares the existing intent-dispatch code path.
- `internal/store/event.go` — emit a new `AutoAdvanced` event
  (replay-safe, no-op on replay) so the trace shows who fired `done`
  and why. Distinguishes auto-fired from driver-fired `done` in
  traces.

**Tests:**

- `TestAutoAdvance_FiresDoneAfterOnEnter` — single state with one
  on_enter step + `done` arc → target. Assert the session lands at
  `target` in one turn (no external `--intent done` required).
- `TestAutoAdvance_RespectsOnError` — on_enter step with `on_error:
  fail_state` that raises. Assert the FSM goes to `fail_state`, not
  the done arc's target.
- `TestAutoAdvance_GuardedArcsRunNormally` — `done` arc with multiple
  `when:` branches. Assert the matching branch is taken, with the
  same precedence as external-driver `done`.
- `TestAutoAdvance_WaitTrueDoesNotFire` — state declared `wait: true`
  with an on_enter chain. Assert the FSM stays at the state after
  on_enter completes.
- `TestAutoAdvance_LoadFailsWithoutDoneArc` — state with on_enter
  chain, no `wait: true`, no `done` arc. Expect a load-time
  validation error.
- `TestAutoAdvance_NoOnEnterNoFire` — pure wait state (no on_enter)
  without `wait: true` declared. Should also fail load validation —
  authors must mark wait states explicitly.

---

## 4. Bugfix Migration

After kitsoki ships, `stories/bugfix/app.yaml` updates:

| State | `wait:` |
|---|---|
| `phase_X_executing` for X in {minus_1, 0, 0_5, 1, 1_5, 3, 4, 5, 6_5, 7, 7_5, 8, 9_5, 9_6, 12, 12_5, 12_6_executing, 13_executing} | unset (auto-advance) |
| `phase_1_7_awaiting_reply`, `phase_6_awaiting_reply`, `phase_9_awaiting_reply`, `phase_9_7_awaiting_reply` | `true` (operator-endorsement checkpoints) |
| `phase_12_6_awaiting_reply` | `true` (PR-event idle state) |
| All `_error` states | `true` (wait for operator `continue`/`quit`) |
| `bootstrap` | `true` (wait for `set_session_context`) |
| `terminated` | n/a (terminal state) |

Concrete consequences:

- Drop ~15 `intent: done` turns from `happy.yaml` and
  `script_phase_dispatch.yaml`. Flow tests get shorter and clearer.
- `loop.py`'s "see `_executing` → fire `done`" branch goes away. The
  loop only fires intents for real external events (`pr_comment`,
  `pr_merged`, Jira-comment replies). Driver complexity drops.
- A clean pipeline run executes back-to-back compute phases without
  poll-interval latency between them. The only intentional pauses
  are at `_awaiting_reply` / `wait: true` states.

---

## 5. Alternatives considered

### a. `auto_advance: true` opt-in flag (the original draft)

Same field semantics but inverted — auto-advance is opt-in, default is
the current "wait for external `done`." Useful only if backward compat
matters. It doesn't.

### b. New state kind: `compute` vs `gated`

YAML vocabulary change: `kind: compute` (auto-advance) vs
`kind: gated` (wait). Same semantics, more typing. **Rejected** — a
single boolean (`wait: true`) is enough, and most states are compute
so the default should be compute.

### c. Host-call-driven arcs

Let `done`-arc guards read `last_host.exit_code == 0` directly;
kitsoki fires the arc internally when the chain finishes. More
expressive (any condition can drive routing) but requires extending
the expr whitelist with a new root (`last_host`). **Deferred** —
bigger lift than the boolean, and the boolean covers the actual pain.
The two are not mutually exclusive; host-call-driven guards could
land later for finer-grained routing.

### d. Signal model

First-class signals (timeout, host-success, intent, slot-bound) with a
declared expected signal per state. Kitsoki transitions when an
expected signal lands. Closest to actor / Erlang semantics; cleanest
end state. **Deferred** — multi-week refactor. The default-auto-fire
flag is the minimum step that closes the immediate ergonomic gap; a
signal-model proposal can subsume it later.

---

## 6. Open questions

- **Auto-fire and dispatch ordering.** If on_enter's last step is a
  `host.transport.post` and the transport fails (transport.post is an
  effect, not a host call with `on_error:`), where does the FSM go?
  Recommend: transport.post failure surfaces as a turn-level error
  that routes via the existing orchestrator error path (to the state's
  `_error` if declared, otherwise turn-fails). Auto-fire is a no-op
  in that case.
- **What about `_error` states themselves?** They're declared
  `wait: true` — operator decides `continue` (retry) or `quit`. No
  change.
- **Driver double-dispatch.** If a driver (loop.py) still fires
  `done` externally on a state that just auto-fired, the FSM has
  already advanced — kitsoki should reject the stale intent cleanly.
  The existing turn-id mechanism handles this — drivers include the
  turn id they observed, and the orchestrator rejects stale turns.
  Document the expectation; no code change.
- **Migration in `phase_template.reviewed_phase`.** The template
  body's `_executing` and `_error` states need the `wait:` field set
  appropriately at template-instantiation time. Add a `wait:`
  parameter to the template, default unset on `_executing` (compute,
  auto-advance) and `wait: true` on `_error` and `_awaiting_reply`.

# Runtime: Post-host-bind effect hook

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone

## Why

`bind:` is a flat field copy — `host.Result.Data[result_key]` → `world[world_key]`
(`internal/machine/machine.go:70-72`, applied per-leaf in
`internal/orchestrator/host_dispatch_bind.go:165`). There is no transform,
no expression, no derivation. The moment an author wants to map a host
result *through* logic — derive a world flag from an `agent.decide`
verdict's `{intent, confidence, reason}`, normalise a path, branch a
`say:` on a returned status — they hit a wall, because **the bound value
is not visible to any deterministic effect in the same chain.**

The cause is the two-phase execution split. `machine.Turn` does *not* run
host calls; it collects them into a `[]HostInvocation` queue and returns
(`internal/machine/machine.go:1493-1534` queues each `invoke:`;
`hostCallsWillBind` at `:2514-2521` even *skips the machine-time render*
because the orchestrator owns the post-bind view, `:906`). The
orchestrator dispatches the queue *later* in `dispatchHostCalls` and
applies the binds there. So within one `on_enter:` / transition chain,
every machine-time `set:`/`increment:`/`say:`/`when:` sees the **pre-bind**
world. A `set:` placed after an `invoke:` in the same chain does **not**
see the just-bound value.

Today only three things re-evaluate against the post-bind world, and each
is a narrow special case:

1. **`emit_intent:`** — deferred. `applyEffectsTraced` silently swallows an
   `emit_intent` whose `when:` errors against an unbound key
   (`internal/machine/machine.go:1407-1426`); `settlePostBindEmits`
   re-runs it after the bind (`internal/orchestrator/orchestrator.go:1209-1310`).
   A *non-emit* effect (`set`/`say`/`invoke`) whose `when:` errors against
   the unbound key is propagated as an authoring error
   (`machine.go:1427`) — so you cannot even guard a `set:` on a key the
   preceding `invoke:` will bind.
2. **A subsequent `invoke:`'s `with:`** — `rerenderHostArgs` re-renders the
   *next* host call's templated args against the post-bind world
   (`internal/orchestrator/host_dispatch_bind.go:31-67`), with per-leaf
   fallback. This is why bugfix's two-step `on_enter:` composes.
3. **The next room's `on_enter:`** — once `emit_intent` lands on the next
   state, that state's `on_enter` runs against the post-bind world.

Plus a fourth, asymmetric path: **`on_complete:`**. A `background: true`
invoke gets a post-completion effect list that runs against the
post-bind world via `RunEffectsAndState`
(`internal/orchestrator/oncomplete.go:167-268`) — `set:`, `say:`,
`invoke:`, `emit_intent:`, and a `Target:` transition all see
`last_job_result`. **Synchronous invokes get no equivalent.** A foreground
`agent.decide` whose `{intent, confidence}` you want to fold into a world
flag has no natural home for the transform: authors push it into the next
room's `on_enter`, bury it in the decide schema/prompt, or route-only via
`emit_intent`. The capability exists for background jobs and is missing for
the synchronous case that motivates it.

## What changes

Give a synchronous `invoke:` a **`then:` effect list that the orchestrator
runs after the bind, against the post-bind world** — the deterministic
mirror of `on_complete:` for background jobs. Effects in `then:`
(`set`/`increment`/`say`/`emit_intent`) see the just-bound result, so a
result can be mapped through expression logic at its call site instead of
leaking the transform into the next room.

One sentence: *every binding `invoke:` may carry a `then:` list that the
orchestrator runs once, post-bind, in the originating state's context —
symmetric with `on_complete:`.*

## Impact

- **Code seams:** `internal/orchestrator/host_dispatch_bind.go`
  (apply `then:` immediately after the per-leaf bind, before the next
  queued call's `rerenderHostArgs`); `internal/machine/machine.go:1522-1534`
  (carry `Then` on `HostInvocation`); `internal/app/types.go:728` (new
  `Effect.Then` field) + `internal/app/loader.go` (validation).
- **Vocabulary:** one new effect field `then:` (table below). No new host
  call, no new world key, no new gate/decider.
- **Stories affected:** none by default — `then:` is opt-in and absent
  everywhere today. Candidate first adopter: any room mapping an
  `agent.decide`/`ask_with_mcp` result through a flag (bugfix judge
  rooms, `stories/bugfix/rooms/*`).
- **Backward compat:** fully additive. Existing stories and cassettes are
  byte-for-byte unaffected; a story with no `then:` runs exactly as today.
- **Docs on ship:** `docs/stories/state-machine.md` (effect vocabulary +
  the post-bind ordering contract), `docs/stories/authoring.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| effect | `then:` | `[]Effect` on an `invoke:` effect | Runs once, **after** this invoke's `bind:` applies, against the post-bind world. Sees the bound keys + `$host_result`. Sibling of `on_complete:` (which is background-only). |

`then:` reuses the existing `Effect` shape — `set`/`increment`/`say`/
`when`/`emit_intent`/`slots`. It is rejected (load-time) when:
- the parent effect has no `invoke:` (nothing binds);
- the parent is `background: true` (use `on_complete:` — they are the same
  concept on the two dispatch paths, and allowing both invites confusion);
- it contains a nested `invoke:` with `background: true`, or a nested
  `then:`/`on_complete:` (one level deep, like `on_complete:`).

## The model

Today (synchronous invoke, the gap):

```
on_enter ──▶ machine.Turn queues [invoke A (bind X)] ──▶ returns (pre-bind world)
          │
orchestrator.dispatchHostCalls ──▶ run A ──▶ bind X into world  ──▶ (no post-bind effect runs)
```

Proposed:

```
orchestrator.dispatchHostCalls ──▶ run A ──▶ bind X ──▶ run A.then (post-bind world) ──▶ next queued call
```

`then:` is **deterministic execution**, not an interpretive decision: it
is `set`/`increment`/`say` (pure world mutation + narration) plus an
optional `emit_intent` (which routes through the *existing* recorded
decision machinery — `settlePostBindEmits`, the `decider:`, `GateDecided`).
It adds no new interpretive operator and does not blur the moat: the only
*decision* a `then:` can express is an `emit_intent`, already gated and
recorded. The transform itself (`set: { ok: "{{ $host_result.confidence >= 0.8 }}" }`)
is replayable arithmetic over the recorded bind, not a judgment.

```yaml
states:
  judging_executing:
    on_enter:
      - invoke: host.agent.decide
        with: { agent: judge, schema: judge_verdict }
        bind: { verdict: submitted }          # flat copy, as today
        then:
          # runs post-bind: world.verdict is populated here
          - set: { approved: "{{ world.verdict.confidence >= world.threshold }}" }
          - say: "Judge: {{ world.verdict.intent }} ({{ world.verdict.confidence }})"
          - emit_intent: "{{ world.verdict.intent }}"
            when: "world.verdict.confidence >= world.threshold"
    on:
      done: [{ target: applied }]
```

## Decision recording

`then:` produces the **same events** as the effects it contains, recorded
identically to a machine-time chain so replay is bit-stable:

- `set:`/`increment:` → `EffectApplied` (`store.EffectApplied`, the same
  payload shape `applyEffectsTraced` emits at `machine.go:1453`/`:1469`).
- `say:` → `MachineSay` (`machine.go:1489`).
- `emit_intent:` → routed through `settlePostBindEmits`/
  `DispatchPostBindEmits`, which already emits `GateDecided` for any gate
  it resolves (per `execution-modes-and-gate-deciders.md` §4).

No new event type. The trace must make clear *where* in the turn a `then:`
effect ran (post-bind, attributed to the originating `invoke:`), so an
`origin: "invoke_then"` field on the effect events (mirroring
`oncomplete.go`'s `kind: "background_completion"` turn marker) is the only
recording change. No interpretive decision is added that isn't already a
labeled datapoint.

## Engine seams & invariants

- **Where it runs.** `then:` fires inside `dispatchHostCalls`, immediately
  after this invocation's bind is applied and **before** the next queued
  call's `rerenderHostArgs` (`host_dispatch_bind.go:31`). This gives the
  natural composition: A binds, A.then can `set:` a derived key, B's
  `with:` re-renders against a world that already reflects both.
- **Ordering contract (the load-bearing invariant):** for a chain
  `[invoke A, invoke B]`, the post-bind world progression is
  `bind(A) → then(A) → rerender+run B → bind(B) → then(B)`. `then(A)`
  must run before B so B can read A's derived keys. This is the same
  left-to-right, fully-ordered contract `applyEffectsTraced` already
  guarantees for machine-time effects.
- **`on_error:` interaction.** If the invoke itself errors, `on_error:`
  redirects *before* `then:` would run — `then:` does **not** run on the
  failure branch (symmetric with `on_complete:` not running until the job
  reaches a terminal *done* state, and with auto-advance's
  no-fire-on-error rule, `auto-advance-states-proposal.md` §2). An error
  *inside* a `then:` effect (bad `set:` expr) is a turn-level failure,
  fail-fast — no partial advance — exactly as `RunEffectsAndState` already
  treats `on_complete:` (`oncomplete.go:174-179`).
- **emit_intent depth caps.** A `then:`-emitted intent enters the existing
  `settlePostBindEmits` loop and is bounded by `EmitIntentMaxDepth` (8,
  per-chain) and `OrchestratorPostBindMaxDepth` (4, orchestrator outer
  loop, `orchestrator.go:1192-1207`). `then:` does not add a new recursion
  axis — it feeds the same pass — so the existing budget covers it.
- **Cycle budgets.** `then:` runs once per invoke per turn; it cannot loop
  on its own. Any cycle is an `emit_intent` cycle, already capped above.
- **Load-time invariant.** `loader.go` rejects `then:` on a non-invoke
  effect, on a `background: true` invoke, and on nested
  `then:`/`on_complete:`/`background:` — fail-fast at load with a clear
  message, never at runtime.

## Alternatives

### A. Extend the post-bind re-evaluation pass (no new keyword)

Generalise `settlePostBindEmits` to also re-run any `set:`/`increment:`
that *followed* a binding invoke in the same chain — re-walk the original
`on_enter` list post-bind and apply the trailing mutations. No new
vocabulary; the existing `set:`-after-`invoke:` "just works."

**Rejected as primary.** It is implicit and magical: a `set:` would
sometimes run at machine-time (pre-bind) and sometimes be silently re-run
post-bind, with no syntactic signal which. It also collides with the
current contract that a non-emit `when:` error is an authoring bug
(`machine.go:1427`) — we'd have to soften that, weakening a real
fail-fast. `then:` keeps the post-bind region *explicit and lexically
scoped to its call site*, which is easier to read, validate, and trace.

### B. Generalise `on_complete:` to fire for synchronous invokes too

Rather than a new `then:` keyword, let `on_complete:` run on synchronous
invokes (post-bind) as well as background ones. One keyword, two dispatch
paths.

**Lean: viable, and the recommended *naming* fallback** if review prefers
not to add a word — but the semantics differ enough to warrant separation.
`on_complete:` carries `last_job_id`/`last_job_status`/`last_job_result`
and a `Target:` transition (`oncomplete.go:128-134`, `:238-267`); a
synchronous `then:` has no job and should bind `$host_result` instead. If
review wants one keyword, we name it `on_complete:` and document the two
contexts; the design here is otherwise identical.

### C. Push the transform into the decide schema / next room (status quo)

Do nothing; authors keep deriving flags in the next room's `on_enter` or
in the prompt. **Rejected** — that is exactly the asymmetry this proposal
removes, and it scatters one call site's logic across two rooms.

## Sibling concern: `on_first_enter` / on_enter idempotency

The same two-phase split that makes `then:` necessary also under-specifies
**entry lifecycle**. `on_enter` is documented as *not* a once-per-lifetime
hook — it re-fires on `/reload` (`RerunOnEnter`), on self-re-entry
(`target: <thisRoom>`), and on `on_error:` sibling redirects
(`docs/stories/state-machine.md:533-553`). The canonical footgun: a chat
room whose `on_enter` calls `host.chat.create` (unconditional INSERT)
spawns a *fresh empty chat* on `/reload`, orphaning the thread
(`state-machine.md:549-552`). Today the only fix is author discipline:
get-or-create verbs (`host.chat.resolve`), or a guard flag — the
`bf_autostart_attempted` pattern in `stories/bugfix/rooms/idle.yaml:97-130`
(`when: "... && !world.bf_autostart_attempted"`, then
`set: { bf_autostart_attempted: true }`).

This is the same disease as the `then:` gap: **the effect-chain model
under-specifies ordering relative to host dispatch and to entry
lifecycle.** A distinct run-once entry hook, `on_first_enter:`, would
dissolve the idempotency footgun — `host.chat.create` lives in
`on_first_enter` (runs exactly once per room per session), `on_enter`
stays "every (re)entry" for view/prompt refresh.

The hard invariant: **`/reload` (and self-re-entry, and `on_error:`
redirect) must count as re-entry, not first-entry.** First-entry fires
once, keyed on whether the room has ever been entered in this session's
event log — not on whether it is the *current* state. This is a separate
slice (it touches the entry path, not host dispatch) and is recorded here
as related work; it can ship independently. Listed under Open questions
whether to split it into its own `runtime` proposal.

## Backward compatibility / migration

Additive and opt-in. No story carries `then:` today, so every existing
story and cassette is unaffected. No one-shot migration needed. First
adopters convert a "derive-flag-in-next-room" pattern into a call-site
`then:` voluntarily.

## Tasks

```
## 1. Engine — then:
- [ ] 1.1 Add Effect.Then ([]Effect) in internal/app/types.go; carry Then on machine.HostInvocation (machine.go:1522)
- [ ] 1.2 Apply then: in dispatchHostCalls after this call's bind, before next call's rerenderHostArgs; bind $host_result
- [ ] 1.3 Load-time validation + clear errors (then: only on synchronous invoke; no nested background/then/on_complete)
- [ ] 1.4 Record then: effects as EffectApplied/MachineSay with origin:"invoke_then"; emit_intent routes through settlePostBindEmits

## 2. Verification (no LLM)
- [ ] 2.1 kitsoki turn: invoke with stubbed bind + then:{set} → derived world key present post-turn
- [ ] 2.2 Flow fixture: agent.decide stubbed by id → then: maps verdict to flag + emit_intent; legacy (no then:) path still green
- [ ] 2.3 then: on_error path — invoke errors → on_error redirects, then: does NOT run
- [ ] 2.4 then: emit_intent respects EmitIntentMaxDepth / OrchestratorPostBindMaxDepth

## 3. Sibling — on_first_enter (decide first; may split to its own proposal)
- [ ] 3.1 Decide: fold here or split. If fold: on_first_enter run-once keyed on event-log first-entry; reload/self-re-entry = re-entry
- [ ] 3.2 Migrate one host.chat.create room off the bf_autostart_attempted guard onto on_first_enter

## 4. Adopt + document
- [ ] 4.1 Migrate one real room (bugfix judge) onto then:
- [ ] 4.2 Update state-machine.md (effect vocab + post-bind ordering contract) / authoring.md; trim/delete this proposal
```

## Verification

A reviewer confirms `then:` without an LLM via stateless `kitsoki turn`
with a stubbed host result: a chain `invoke (bind X) → then: set Y from X`
lands `Y` derived from the bound `X` in the post-turn world; without the
change, `Y` is computed against the pre-bind (nil) `X` and the turn either
errors or sets the wrong value. The intent-only flow fixture stubs
`host.agent.decide` **by per-invoke `id:`** (per memory:
agent-stub-by-id) and asserts the `then:`-derived flag + the emitted
intent. No real-LLM test is required; the bind value is fixture-supplied,
so the transform is exercised deterministically.

## Open questions

1. **`then:` vs. generalised `on_complete:`** — separate keyword
   (explicit, distinct `$host_result` vs job semantics) or one keyword with
   two contexts (alternative B)? *Lean: separate `then:`; fall back to
   `on_complete:` only if review rejects a new word.*
2. **`$host_result` exposure** — does `then:` see the *full* host result
   (`$host_result.<key>`) in addition to the bound world keys, or only the
   binds? *Lean: expose `$host_result` so a `then:` can map fields the
   author chose not to `bind:`, avoiding throwaway binds.*
3. **Split `on_first_enter` out?** — it touches the entry path, not host
   dispatch; it has one coherent Why of its own. *Lean: keep it documented
   here as the sibling diagnosis, but ship it as its own `runtime` slice if
   `then:` lands first.*
4. **Does a `then:` emit_intent that transitions away strand a later queued
   invoke** in the same `on_enter`? *Lean: a transition ends the chain
   (same as machine-time `emit_intent`); document that an `invoke:` after a
   `then:`-emitting invoke is unreachable, and validate against it if cheap.*

## Non-goals

- Expression-valued `bind:` (transforming *during* the copy). `then:` after
  a flat `bind:` covers the cases; a computed-bind grammar is a larger,
  separate change.
- Re-running arbitrary machine-time effects post-bind (alternative A) —
  explicitly rejected as too implicit.
- Changing `on_complete:` background semantics.
- Any new interpretive operator or decider — `then:` is deterministic
  execution; its only decision (`emit_intent`) reuses existing gates.

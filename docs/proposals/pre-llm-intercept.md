# Epic: pre-LLM prompt interception — deterministic commands before the agent

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 2 (0/2 shipped)

## Why

In a long coding-agent session, a large fraction of what the user types is not a
novel reasoning request — it is a **known command** phrased in natural language:
"rebase this onto main", "run the tests", "open the PR", "what changed since the
last tag". Today every one of those costs a full agentic turn: tokens, latency,
and a non-deterministic result that varies run to run. The agent re-derives, from
scratch, how to do a thing kitsoki could resolve **deterministically** and
**identically every time**.

kitsoki already resolves free text → intent with *zero* LLM. The
[semantic-routing](../architecture/semantic-routing.md) stack matches an
utterance against a room's declared synonyms/examples in microseconds
(`internal/semroute/`), and the state machine then executes the resolved intent
through recorded, replayable host calls. The only consumers of that capability
are kitsoki's own TUI and `Orchestrator.Turn` — it is not exposed as a **front
door** that a general-purpose agent can consult *before* its LLM runs.

This epic exposes it as exactly that: a **pre-LLM gate**. The agent's input is
intercepted, checked against a bound kitsoki room with no LLM, and — when it is a
recognized command — handled by the kitsoki story while the agent's main LLM is
**never invoked for that turn**. It is kitsoki's whole thesis (deterministic +
recorded + pluggable) applied as a cheap, auditable command layer in front of an
expensive non-deterministic one.

## What changes

Once both slices ship:

- A new `kitsoki intercept` command answers, with **no LLM and no side effects**,
  "does this input deterministically resolve to a command in the bound room, and
  to what?" — then, *only* when a conservative gate says yes, executes that
  command and returns its rendered result as structured JSON + a distinct exit
  code (slice #1).
- A repo opts in with an `intercept:` block in its
  [`.kitsoki.yaml`](../../internal/webconfig/webconfig.go) naming the app + room
  to gate against. `kitsoki hook install` wires the matching per-agent hook
  (slice #2).
- In **Claude Code**, a recognized command never reaches the model: the
  `UserPromptSubmit` hook calls `kitsoki intercept` and, on a match, blocks the
  prompt and surfaces kitsoki's result. Everything unrecognized passes through to
  the agent **untouched**.
- In **Codex CLI** and **GitHub Copilot**, which have *no* pre-model interception
  hook today, the same engine is reachable through an honest degraded path
  (explicit user-invoked command; optionally an MCP tool the model can be steered
  to — flagged as model-in-the-loop, **not** a bypass). We track their upstream
  feature requests so the full path lands when they ship it.

## Impact

- **Spans:** runtime (slice #1 — the engine gate), tooling/integration (slice #2 —
  the per-agent hooks + installer + binding).
- **Net surface:** one new `kitsoki intercept` command + an `Orchestrator.Classify`
  seam that splits *classify* from *execute* (`internal/orchestrator/`); an
  `intercept:` block on `webconfig.WebConfig`; a `kitsoki hook install` subcommand
  + one Claude Code `UserPromptSubmit` shim; new `intercept.*` trace events; one
  minimal flow-test example app. No new third-party deps.
- **Docs on ship:** a new `docs/architecture/prompt-intercept.md` (the gate + the
  agent-integration matrix), cross-linked from
  [`semantic-routing.md`](../architecture/semantic-routing.md) (it is a fifth
  consumer of the same tiers) and `docs/getting-started.md`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Intercept engine | runtime | `kitsoki intercept`: a no-LLM, no-side-effect `Classify` pass + a conservative gate + a gated `execute` pass, emitting structured JSON + exit codes | — | Draft | [`intercept-engine.md`](intercept-engine.md) |
| 2 | Agent intercept hooks | tooling | Per-agent shims + `kitsoki hook install` + the `.kitsoki.yaml intercept:` binding + the honest Claude / Codex / Copilot capability matrix | 1 | Draft | [`agent-intercept-hooks.md`](agent-intercept-hooks.md) |

## Sequencing

```
#1 (intercept engine) ──▶ #2 (agent hooks)
```

Slice #1 is independently shippable and testable with no agent at all — pipe text
to `kitsoki intercept` and assert the verdict + exit code. Slice #2 is thin once
#1 exists: it is glue (a stdin→`kitsoki intercept`→block-or-passthrough shim), an
installer, the binding schema, and the honesty matrix.

## Shared decisions

1. **Pass-through is the default; the gate is conservative.** A turn the user
   meant for the agent must never be silently hijacked (principle of least
   surprise). Only a **deterministic** hit (confidence 1.00) or a high-confidence
   **synonym** hit (≥ `confidence_bar`, default 0.90) intercepts. A tie (0.50),
   a slot-incomplete template (0.65), and any no-match all **fall through to the
   agent untouched** (see the bands in
   [semantic-routing.md §1](../architecture/semantic-routing.md)).
2. **No agent can substitute a synthetic assistant answer pre-model.** The
   *only* pre-model lever is **block + a reason string** (Claude Code) or nothing
   (Codex/Copilot). So the contract is: kitsoki does the real work in the room,
   and the result is surfaced **as the block reason**. This mirrors the inverse of
   the [operator-ask](../architecture/operator-ask.md) finding ("a `PreToolUse`
   hook can only allow/deny, it cannot supply a tool_result") — here allow/deny
   is exactly enough, and we accept the ceiling honestly.
3. **No LLM on the intercept path — ever.** Interception uses only the no-LLM
   tiers (deterministic / synonym / optional embedding). If a verdict can't be
   reached without the LLM, that *is* a pass-through. Defeating this would defeat
   the entire value (cheaper + deterministic + auditable).
4. **The binding lives in `.kitsoki.yaml`.** The repo declares `intercept: {app,
   room, …}`, extending the config that is already "the stable extension point
   for machine-global keys" (`internal/webconfig/webconfig.go:55`);
   `.kitsoki.local.yaml` can disable it per developer.
5. **Every intercept is recorded.** Matched *and* passed-through turns land in the
   bound session's trace, so the interceptor is auditable and feeds the existing
   synonym-growth loop — a phrasing that *passed through* is exactly the candidate
   to add as a synonym so it intercepts next time
   (`kitsoki inspect --synonym-suggestions`,
   [semantic-routing.md §3.2](../architecture/semantic-routing.md)).
6. **Session identity is the host agent's `session_id`.** Claude Code delivers it
   on the hook stdin; keying the intercept session to it lets a multi-turn command
   flow (clarify → confirm) persist across intercepts via the existing trace
   journal — with no LLM in the loop.

## Cross-cutting open questions

1. **How does an intercepted exchange show up in the *agent's* transcript?** A
   blocked Claude prompt is ephemeral (the user sees the block reason; no assistant
   turn is recorded) while the durable record lives in the kitsoki trace (decision
   #5). Is that confusing in a long convo, or is the clear `⌁ kitsoki` marker
   enough? *Lean: ephemeral-in-agent + durable-in-trace for v1; revisit if it
   reads as "my turn vanished."*
2. **Stateless vs persistent execution for v1.** `OneShot` (stateless, in-memory)
   is simpler; the trace-backed `--trace`-keyed session enables multi-turn command
   flows. *Lean: stateless OneShot for v1, persistent (`session_id`-keyed) as the
   immediate follow-on — decided in slice #1.*
3. **Latency budget.** The classify pass runs synchronously before every prompt;
   the common "this is NOT a command" path must stay near-free. *Lean: lexical
   tiers only by default (microseconds per semantic-routing.md); the embedding
   tier (a round-trip) is opt-in per repo.*

## Non-goals

- **Replacing the coding agent.** This is a fast-path for *recognized* commands;
  everything else flows to the agent untouched. It never suggests or autocompletes
  — it acts or it passes through.
- **Real-LLM anything on the intercept path.** Excluded by decision #3.
- **Faking an assistant turn.** No agent supports a synthetic pre-model answer; we
  surface results as a clearly-marked block reason, not as the agent's voice.
- **Building pre-model hooks into Codex/Copilot.** Out of our control; slice #2
  degrades honestly and tracks their feature requests.

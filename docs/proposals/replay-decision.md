# Runtime: replay a decision — re-run one recorded call against a different operator

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../trace-introspection.md

## Why

Langfuse's "Open in Playground" lets a user re-run a recorded generation with
edits (`.context/langfuse-trace-viewer-comparison.md`, idea #6). We can do
strictly more, because of two things Langfuse lacks: our trace is
**deterministic** and it **carries the effective story**
(`session.story` embeds the story base64 + hash, replayable byte-identical —
`internal/store/event.go:193`). That means a recorded agent call is fully
reconstructable: its resolved prompt, agent, schema, and world context are
all in the trace. So we can take a single `agent.call` and **re-dispatch it
against a different operator** — Claude vs. the local-model sidecar
(`local-model-agent.md`) vs. a human — or against an edited prompt, and
**diff the verdict**. That is the natural, demonstrable UI for the
pluggable-operator half of the moat (`feedback_kitsoki_moat_is_architecture`):
"this decider, swapped, would have chosen differently."

There is no way to do this today: re-running a decision means re-running the
whole story.

## What changes

One sentence: **add a `kitsoki replay-call` capability (CLI + a
`runstatus.call.replay` RPC) that, given a recorded `call_id` from a trace,
reconstructs that single agent call from the embedded story + recorded
inputs and re-dispatches it against a chosen operator (and optionally an
edited prompt), returning the new verdict for side-by-side diff — with zero
effect on the original run, world, or machine.**

It is a *read-and-recompute* operation: it reads the recorded call, runs one
isolated dispatch, and hands back a result. It never advances the original
machine or writes the story trace.

## Impact

- **Code seams:**
  - A new isolated single-call dispatch entry point reusing the existing
    agent dispatch (`internal/host/agent_dispatch.go:Dispatch` ~`:314`)
    without the surrounding machine loop — feed it the recorded resolved
    prompt + agent/schema, get back a `Verdict`/response.
  - Prompt/agent/schema reconstruction from the trace: the
    `agent.call.start` prompt ref (inline or `prompt_file`) + `attrs.verb` +
    `attrs.agent`/`model` give the inputs; the embedded `session.story`
    supplies the schema/agent definitions.
  - `cmd/kitsoki/replay_call.go` (CLI) + a `runstatus.call.replay` JSON-RPC
    method on the runstatus server.
- **Vocabulary:** no new story-author vocabulary. A new *operator-facing*
  command + RPC; the `--operator claude|local|human` choice routes to an
  existing agent backend.
- **Stories affected:** none — replay never touches a running story.
- **Backward compat:** any trace that carries `session.story` +
  `agent.call.*` with a prompt ref (the canonical shape since
  `runstatus-trace-fidelity.md`) is replayable; older traces missing the
  embedded story or prompt ref are not (graceful "not replayable" message).
- **Docs on ship:** `docs/architecture/` (pluggable-operator replay),
  `docs/tracing/run-status-ui.md` (the replay-and-diff affordance).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| CLI | `kitsoki replay-call` | `--trace <jsonl> --call-id <id> --operator claude\|local\|human [--prompt-edit <file>]` → verdict JSON | isolated single dispatch |
| RPC | `runstatus.call.replay` | `{call_id, operator, prompt_override?} → {verdict, diff_vs_original}` | read-only over the run; side-effect-free dispatch |

## The model

```
recorded trace ──▶ reconstruct ONE call (prompt + agent + schema + world ctx)
                        │   from agent.call.start ref + embedded session.story
                        ▼
                   isolated dispatch  ──operator=claude|local|human──▶ new Verdict
                        │   (reuses agent_dispatch.Dispatch; NO machine loop,
                        │    NO world write, NO trace append to the original run)
                        ▼
                   diff(original Verdict, new Verdict) ──▶ side-by-side
```

The moat boundary is the whole point: the **operator is the pluggable
interpretive component**, and replay makes that pluggability *visible and
testable* — same recorded input, different operator, diffed output. The
deterministic execution around it (prompt resolution, schema validation) is
reused unchanged from the live path, so a replay against the *same* operator
with no edits must reproduce the original verdict (the determinism check
below).

## Decision recording

By design, replay records **nothing into the original run** — it must not,
or it would corrupt a deterministic trace. Two options for capturing replay
results (Open question 1): (a) ephemeral, returned to the caller and shown in
the viewer only; (b) recorded as a `trace.annotation` (slice #5) on the
target call — "operator `local` would have chosen `refine` (0.55)". Option
(b) is attractive because it reuses the annotation sidecar and makes a replay
a *labeled comparison datapoint*, but it depends on slice #5. *Lean:
ephemeral in v1; optionally persist as an annotation once #5 lands.*

## Engine seams & invariants

- **Where it hooks:** a new thin caller around the existing
  `agent_dispatch.Dispatch` path, bypassing the machine. The hard invariant:
  the original session's trace sink is **never** passed to the replay
  dispatch — it writes to a throwaway sink (or none).
- **Reconstruction completeness invariant:** a call is replayable only if the
  trace carries (a) `session.story` and (b) the call's resolved prompt ref.
  If either is missing, replay refuses with a clear message rather than
  guessing — never fabricate a prompt.
- **Determinism check (load-time of the feature, not the story):** replaying
  a recorded call against the *same* operator with no prompt edit, using the
  recorded model, must reproduce the recorded verdict for a cassette-backed
  call (the operator backend is itself replayed from its cassette). This is
  the test that proves reconstruction is faithful.
- **Runtime boundary for `task` verbs.** A `task` agent can run tools with side
  effects; replaying one must honor the same secure runtime policy the live path
  used (`task-fs-sandbox.md`). *Lean: v1 restricts replay to side-effect-free
  verbs (`decide`/`ask`/`extract`); `task`/`converse` replay is gated behind the
  secure-runtime slice.*

## Backward compatibility / migration

- **Default off / explicit invocation.** Replay is an operator action, never
  automatic. No story changes, no behavior change to any run.
- **Trace requirements.** Canonical traces (post-`runstatus-trace-fidelity.md`)
  carry `session.story` + `agent.call.*` with a guaranteed prompt ref, so
  they're replayable. Older/partial traces degrade gracefully to "not
  replayable."
- **No-cost guarantee for tests.** Replay against the `claude` operator execs
  the local `claude` CLI (`project_agent_uses_claude_cli`), and tests must
  not (`feedback_no_llm_tests`): the determinism test replays against a
  **cassette-backed** operator, never a live LLM.

## Tasks

```
## 1. Engine
- [ ] 1.1 Reconstruct a single call from a trace (session.story + agent.call.start ref → prompt+agent+schema+world)
- [ ] 1.2 Isolated single-call dispatch reusing agent_dispatch.Dispatch; throwaway sink; NO machine/world/trace write to the original
- [ ] 1.3 Operator routing (claude | local | human); refuse non-replayable traces with a clear message
- [ ] 1.4 Restrict v1 to side-effect-free verbs (decide/ask/extract); gate task/converse behind the secure agent runtime in task-fs-sandbox

## 2. Verification
- [ ] 2.1 Determinism: replay a cassette-backed decide against the same operator, no edit → reproduces the recorded verdict
- [ ] 2.2 Isolation: replay does not append to / mutate the original trace, world, or machine (assert sink untouched)
- [ ] 2.3 Operator swap: replay the same call against a second cassette-backed operator → different verdict, diffed

## 3. Adopt + document
- [ ] 3.1 kitsoki replay-call CLI + runstatus.call.replay RPC + SPA "replay this decision" affordance with side-by-side diff
- [ ] 3.2 Update docs/architecture/ (pluggable-operator replay) + run-status-ui.md; trim/delete this slice
```

## Verification

No live LLM (`feedback_no_llm_tests`). The determinism and operator-swap
tests both use **cassette-backed operators**: the original run is a recorded
cassette, and each replay operator is itself a cassette, so the whole test is
deterministic and free. The key assertions are (a) same-operator/no-edit
replay reproduces the recorded verdict bit-for-bit, proving faithful
reconstruction, and (b) the original session's sink receives **zero** new
events during a replay, proving isolation. A `kitsoki replay-call
--operator claude` against a real model stays a manual, opt-in check, never
in CI.

## Open questions

1. **Persist replay results or keep them ephemeral?** *Lean: ephemeral in
   v1; once slice #5 ships, optionally record a replay as a
   `trace.annotation` so an operator comparison becomes a labeled datapoint.*
2. **Human operator UX.** "Replay against a human" means prompting the
   operator to choose the intent themselves in the viewer. *Lean: defer the
   human operator to a follow-on; v1 covers `claude` and `local` (the
   pluggable-backend demo); human replay overlaps the staged-decider human
   path in `execution-modes-and-gate-deciders.md`.*
3. **Prompt-edit scope.** Editing the prompt invalidates the determinism
   guarantee by definition. *Lean: allow `--prompt-edit` but clearly label
   the result as "edited — not a faithful replay" in the diff.*

## Non-goals

- **Replaying a whole turn or run.** This is single-call replay; replaying a
  sequence is a much larger change (and the existing deterministic replay
  already re-runs whole runs).
- **`task`/`converse` replay in v1** — gated behind the secure agent runtime in
  `task-fs-sandbox.md`
  because those verbs can have side effects.
- **Auto-comparing operators across a dataset.** Systematically replaying
  many calls against two backends to score them is the
  `agent-contract-eval.md` Layer-2 territory; this slice provides the
  single-call primitive it could build on, not the batch eval.

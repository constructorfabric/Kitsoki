# Runtime: intercept engine — classify without executing, gate, then execute

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../pre-llm-intercept.md

## Why

kitsoki resolves free text → intent with no LLM today, but every consumer of that
capability **executes on hit**. The deterministic tier matches an utterance and
*immediately* submits the resolved intent — `TryDeterministic` ends in
`o.SubmitDirectRouted(...)` on both the display and example branches
(`internal/orchestrator/deterministic.go:214` and `:237`); `TrySemantic`
(`internal/orchestrator/semantic.go:242`) is the same shape. There is no way to
**ask** "would this input deterministically resolve, and to what?" *without also
running the effects*.

That is exactly what a pre-LLM gate needs: a cheap, **side-effect-free** check it
can run before every agent prompt to decide "is this a known command?", and only
*then* — once a conservative gate has decided to intercept — execute it. The
existing `kitsoki turn` command can execute a turn, but it takes an explicit
`--intent`, or routes `--input` through the **harness** (the LLM) — it does not
expose the no-LLM classify boundary as a standalone gate
(`cmd/kitsoki/turn.go`).

The moat reminder from the runtime template applies almost literally here:
**separate the interpretive/matching decision from deterministic execution.**
Today they are fused; this slice splits them.

## What changes

A new `kitsoki intercept` command and an `Orchestrator.Classify` method that runs
**only** the no-LLM tiers (deterministic → synonym → optional embedding) and
returns a `semroute.Verdict` (`internal/semroute/verdict.go:32`) with **zero
effects applied and zero events written** beyond the trace classification record.
A conservative gate (epic decision #1) maps the verdict to *intercept* or
*pass-through*. On *intercept*, an explicit second pass executes the resolved
intent against the bound room and captures the rendered result.

> One sentence: **split "classify" from "execute" so an external caller can
> cheaply ask "is this a known command?" with no side effects, then act only when
> the gate decides to intercept.**

## Impact

- **Code seams:** `internal/orchestrator/` — extract a pure `classifyOnly` out of
  the `TryDeterministic`/`TrySemantic` hit paths
  (`deterministic.go:179`, `semantic.go:242`); add `Orchestrator.Classify`; new
  `cmd/kitsoki/intercept.go`. Execute reuses `OneShot`
  (`orchestrator.go:1893`, returning `OneShotResult` at `outcome.go:147`) for v1.
- **Vocabulary:** one command, one orchestrator method, one config block (table
  below; the `intercept:` *schema* is owned by slice #2).
- **Stories affected:** none. `Turn`'s live behavior is byte-identical — the
  classify/execute split is an internal refactor that produces the same external
  result (classify → immediately execute) for the existing caller.
- **Backward compat:** purely additive + opt-in. No `intercept:` binding ⇒ nothing
  intercepts.
- **Docs on ship:** a new `docs/architecture/prompt-intercept.md` (gate half),
  cross-linked from
  [`semantic-routing.md`](../architecture/semantic-routing.md) as a fifth consumer
  of the same tiers.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| command | `kitsoki intercept` | stdin host-hook JSON **or** `--input`/`--session`/`--app`/`--room`/`--bar` → JSON verdict + exit code | no-LLM classify + gated execute |
| method | `Orchestrator.Classify` | `(ctx, state, input) → (semroute.Verdict, matched bool)` | zero effects; lexical tiers only; no harness, no extract-LLM |
| exit code | `0 / 1 / 2 / 3 / 10` | handled / rejected / terminal / infra / **no-match (pass-through)** | the hook branches on this — reuses `kitsoki turn`'s 0–3 (`turn.go:46`), adds a distinct pass-through code |
| config | `.kitsoki.yaml intercept:` | `{enabled, app, room, confidence_bar, tiers}` | the per-repo binding — **schema owned by slice #2** |

## The model

```
host prompt ──▶ kitsoki intercept ──▶ Classify(state, input)
                                          │   (deterministic | synonym | [embedding] — NO LLM)
                                          ▼
                                    semroute.Verdict
                                          │
                  conf ≥ bar AND not a tie (0.50) AND no missing slots? 
                       │no ──────────────────────────────▶ exit 10  (hook passes prompt to the agent's LLM)
                       │yes
                       ▼
                 execute intent on the bound room  (OneShot v1 │ trace-backed persistent later)
                       ▼
                 {matched:true, intent, confidence, match_reason, result_text, terminal, needs_followup}
                       ▼
                 exit 0  (hook blocks the prompt, surfaces result_text)
```

- **Interpretive vs deterministic:** classify is *lexical matching*, not LLM
  interpretation — it is already deterministic and replayable
  ([semantic-routing.md §1.1–1.3](../architecture/semantic-routing.md)). Execute
  is the ordinary state-machine path (deterministic effects + recorded host
  calls). Nothing on this path is an LLM decision; that is the point.
- **The gate** is pure data: `Verdict.Confidence`, `Verdict.Candidates`
  (non-empty ⇒ tie ⇒ pass-through), and `Verdict.MissingSlots`
  (`verdict.go:32`) against the configured bar. The `RequiresUnfilledSlot` guard
  that production routing already applies (semantic-routing.md §3.1) means a
  verb that names a command but can't fill a required slot is a **pass-through**,
  not a half-executed command.

## Decision recording

Interception must be auditable (epic decision #5). The classify pass already emits
`turn.deterministic_hit` / `turn.deterministic_miss`
(`deterministic.go:202`/`:248`) and `turn.semantic_hit`; this slice adds one
interception-level event so the *gate* outcome is reconstructable:

| Event | Fields |
|---|---|
| `intercept.matched` | `input`, `intent`, `confidence`, `match_reason`, `gate_bar`, `executed` |
| `intercept.passed` | `input`, `top_confidence`, `reason` (`below_bar` \| `tie` \| `missing_slot` \| `no_match`) |

A passed-through phrasing is precisely a synonym-growth candidate, so this event
feeds the existing read-only loop unchanged
(`kitsoki inspect --synonym-suggestions`, semantic-routing.md §3.2). *(If the new
events warrant first-class trace-format treatment, that is a small `tracing.md`
follow-on — link it then.)*

## Engine seams & invariants

- **Classify/execute split.** Extract `classifyOnly(ctx, sid, input) (semroute.Verdict,
  bool, error)` from the body of `TryDeterministic`/`TrySemantic` — everything up
  to but **not** including the `SubmitDirectRouted` call. `Turn` keeps today's
  behavior by calling `classifyOnly` then submitting (identical external result);
  `intercept` calls `classifyOnly`, gates, and submits only on intercept.
- **Zero-effect invariant.** A `Classify` call applies **no** effects and writes
  **no** events except its trace classification record. A flow fixture must prove
  a classify-only call leaves world + state byte-identical (the moat: matching is
  not mutating).
- **No-LLM invariant.** `intercept` must refuse the main-turn harness and the
  `extract_llm_on_no_match` tier (semantic-routing.md §2.1) — only deterministic +
  synonym + (opt-in) embedding. A verdict unreachable without the LLM is a
  pass-through by definition.
- **Load-time fail-fast.** The room named by `.kitsoki.yaml intercept.room` must
  exist and be loadable when the command starts; a missing/invalid binding is an
  infra error (exit 3), never a silent pass-through-everything.

## Backward compatibility / migration

Purely additive and opt-in. Existing stories and cassettes are unchanged. `Turn`'s
observable behavior is preserved exactly (the split is a refactor with the same
result for its one caller). No story migration; the only new author-facing surface
is the optional `.kitsoki.yaml intercept:` block (slice #2).

## Tasks

```
## 1. Engine
- [ ] 1.1 Extract `classifyOnly` from TryDeterministic/TrySemantic; rewire `Turn` onto it (no behavior change)
- [ ] 1.2 Add `Orchestrator.Classify` (no-LLM tiers only; refuses harness + extract-LLM)
- [ ] 1.3 `kitsoki intercept` command: stdin hook-JSON + flags; gate; structured JSON; exit codes (0/1/2/3/10)
- [ ] 1.4 `intercept.matched` / `intercept.passed` trace events
- [ ] 1.5 Load-time fail-fast on a missing/invalid bound room

## 2. Verification (all no-LLM)
- [ ] 2.1 Stateless: `kitsoki intercept --input "rebase onto main"` over a fixture app → {matched:true, intent, exit 0}
- [ ] 2.2 Stateless: an ambiguous/unknown input → {matched:false, exit 10} (pass-through)
- [ ] 2.3 Flow fixture: classify-only call leaves world + state byte-identical (zero-effect invariant)
- [ ] 2.4 Flow fixture: the execute path runs the matched intent's host calls via cassette (no LLM)

## 3. Adopt + document
- [ ] 3.1 A minimal `stories/intercept-demo/` (or bind an existing command room) as the flow-test fixture
- [ ] 3.2 `docs/architecture/prompt-intercept.md` (gate half) + cross-link from semantic-routing.md; trim this slice from the epic
```

## Verification

No LLM anywhere. `kitsoki intercept --app stories/intercept-demo/app.yaml --room
commands --input "rebase onto main"` → JSON `{matched:true,intent:"rebase",…}`,
exit 0; `--input "what does this function do?"` → `{matched:false}`, exit 10.
Zero-effect and execute paths are covered by `kitsoki test flows` fixtures with
host cassettes (CLAUDE.md: no real LLM, no cost). The classify/execute refactor is
guarded by the existing `Turn` flow fixtures continuing to pass unchanged.

## Open questions

1. **Pass-through exit code** — a distinct code (10) vs reusing `kitsoki turn`'s
   `1` (rejected). *Lean: distinct — the hook must never confuse "kitsoki declined
   to handle this" (pass to the agent) with "kitsoki handled it and the intent was
   rejected" (a real, surfaced result).*
2. **Execute mode for v1** — stateless `OneShot` vs trace-backed persistent
   (`session_id`-keyed). *Lean: `OneShot` for v1; persistent immediately after, to
   unlock multi-turn command flows (clarify → confirm) with no LLM.*
3. **Embedding tier in `intercept`** — recall vs the synchronous round-trip on
   every prompt. *Lean: off by default; opt-in via `tiers:` per repo (epic OQ #3).*

## Non-goals

- The per-agent hook shims, installer, and capability matrix — slice #2.
- Any real-LLM routing on the intercept path — excluded by epic decision #3.
- Changing `Turn`'s live behavior.

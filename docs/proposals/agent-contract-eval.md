# Tracing: Agent call-site contract tests + correctness eval

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   — standalone (consumed by `docs/proposals/local-model-agent.md`;
            hosts the conformance check for `docs/proposals/agent-capability-model.md`)

## Why

A kitsoki story's interpretive decisions are agent call sites — an
`agent.decide` with a `prompt_path` + `schema` (e.g. the `proposal_brief_judge`
in `stories/dev-story/rooms/proposal.yaml`). We test the *deterministic graph*
around them exhaustively (flow fixtures), but the **call site itself — does this
prompt, against this schema, on this model, actually produce the right answer? —
is untested except by a full live run-through.** Two concrete holes:

1. **Mocks are unverified inventions.** A flow's `host_handlers` stub or a
   cassette `response.data` is never checked against the call site's JSON
   schema (`internal/testrunner/hoststubs.go:27`, `cassette.go` resolve both
   skip it). The proposal-flow stubs I just wrote (`submitted: {verdict:
   "continue", …}`) are *invented shapes* — nothing guarantees the real
   `proposal_brief_judge` emits that structure. A green flow suite proves the
   graph, not the boundary. (This is the "stubs must replay real wire shapes,
   not invented" fidelity rule.)

2. **No measured confidence per backend.** We have a free local backend
   (`internal/agent/local_llm.go`, llama.cpp) alongside Claude, but no way to
   ask *"for this specific call site, what fraction of known cases does
   backend X get right?"* without hand-driving the whole story against each
   model. `docs/proposals/local-model-agent.md` wants to route small decides
   to the free local model — but routing there is a leap of faith until we can
   measure that, say, `proposal_brief_judge` scores 95% on llama.cpp and only
   then promote it.

The unifying need: **measure agent call-site correctness, per backend,
cheaply — turning each call site into a scored, labeled dataset** rather than a
trusted-by-vibes prompt. That measurement is both the fidelity gate for mocks
(Layer 1) and the confidence signal for backend selection (Layer 2).

## What changes

Two layers, both hung off the existing `kitsoki cassette` command
(`cmd/kitsoki/cassette.go`):

- **Layer 1 — offline schema-conformance (free, CI-default).** `kitsoki
  cassette lint <cassette> --app <app>` resolves each episode's call site →
  its schema and validates `response.data.submitted` against it via the
  existing `agent.ValidateSubmission` (`internal/agent/validate.go:48`). Same
  check is added to the flow-stub resolver so `host_handlers` mocks are
  schema-checked too. Invented shapes fail immediately, offline, at zero cost.

- **Layer 2 — live correctness eval (opt-in, gated).** A per-call-site
  **dataset** of `{input, expected}` examples; `kitsoki cassette verify --app
  <app> --live --model <backend>` re-fires each example's prompt at the chosen
  backend, validates the response against the schema, **scores it against
  `expected` with a comparator**, and reports a **correctness %** per call site
  per backend. Free on llama.cpp; gated behind `--live` so normal CI never pays
  for Claude.

A call site that scores high on the free backend is a candidate for
`local-model-agent` to route there with evidence.

- **Layer 1b — effect/toolbox conformance (free, CI-default).** The
  [capability-model epic](agent-capability-model.md) gives every agent a
  declared `effect` class + toolbox (recorded on the host event by its slice
  1). This layer is the *audit* half of that contract: `kitsoki cassette lint
  --app` also checks that an episode's **recorded tool uses never exceeded its
  toolbox / declared effect** — a `read`-class call that recorded a `Write`, or
  a tool outside its box, fails the lint. It's the offline, trace-level proof
  that the slice-2 tool allowlist and slice-3 sandbox actually held (or that a
  cassette predates them and needs re-recording). Pure schema check has a
  natural sibling here: schema conformance says the *output* was well-formed;
  effect conformance says the *behavior* stayed in the box.

## Impact

- **Producers:** no new trace events — this *consumes* existing call-site
  metadata (prompt_path / schema / agent on the `Effect.Invoke`) and agent
  outputs.
- **Consumers:** `kitsoki cassette lint`/`verify` (new), and
  `docs/proposals/local-model-agent.md`'s routing decision (the eval report is
  its confidence input; maps onto the `semroute.Verdict` bands
  0.90/0.80/0.65/0.50 it already references).
- **Format:** a new eval-dataset file kind (see below); cassettes unchanged.
- **Backward compat:** Layer 1 is additive — a malformed mock starts failing
  `lint` (intended); no cassette/trace format change. Existing cassettes that
  already carry real recorded responses pass Layer 1 by construction.
- **Docs on ship:** `docs/tracing/cassettes.md` (Layer 1) + a new
  `docs/testing/agent-evals.md` (Layer 2).

## Dataset & scoring model

A dataset is one file per call site (proposed: `stories/<app>/evals/<call-id>.yaml`):

```yaml
kind: agent_eval
app: ../app.yaml
call: brief_check            # the invoke id: in the room
agent: proposal_brief_judge  # resolves prompt_path + schema + model defaults
comparator: field_subset     # exact | field_subset | enum | judge
examples:
  - name: crisp-brief-passes
    args: { brief_path: 001-brief.md, idea: "…" }
    fixtures: { "001-brief.md": "!include cases/crisp.md" }   # files the prompt reads
    expect: { verdict: continue }
  - name: vague-brief-clarifies
    args: { brief_path: 001-brief.md, idea: "…" }
    fixtures: { "001-brief.md": "!include cases/vague.md" }
    expect: { verdict: clarify }
```

**Comparators** (pluggable; the eval declares which):

| Comparator | Pass when | Use for |
|---|---|---|
| `exact` | response == expected (deep) | extract with a fixed answer |
| `field_subset` | every key in `expect` matches in the response | decide where only `verdict` matters, prose varies |
| `enum` | a named field's value == expected | classifier-style decides |
| `judge` | a separate `agent.decide` rules response ≈ expected | open-ended outputs; **note: the judge is itself a call site that should have its own eval** |

**Correctness %** = passing examples / total, per (call site, backend). Because
LLM output is non-deterministic, an example may be run N times (`--repeat N`)
and scored as pass-rate; the report carries the band, not a single bit.

## Backends

The eval runs against any agent transport already in the tree
(`internal/agent`: in-process Claude, subprocess, MCP-HTTP, and the local
llama.cpp at `local_llm.go`). `--model`/`--backend` selects one; omitting it
runs the **comparison matrix** (Claude vs llama.cpp side by side) so the report
shows the accuracy *gap* directly. Local-backend schema conformance leans on the
grammar enforcement already in `internal/agent/grammar_subset.go`, so Layer 1
should already be ~100% on llama.cpp; Layer 2 measures the *semantic* gap.

## Determinism

- **Layer 1 is fully deterministic** (pure schema validation) and is the
  CI-default — it must stay green with no network/LLM.
- **Layer 2 is explicitly non-deterministic** and never runs in default CI
  (`--live` gate, mirroring the `//go:build ide_live` opt-in convention). Its
  output is a *score with a sample size*, not a pass/fail assertion. The eval
  fixtures (the `cases/*.md`, the `expect` blocks) ARE deterministic and
  committed; only the model call varies.

## Producers & consumers

- **Producer of the signal:** `kitsoki cassette verify --live` writes a report
  (JSON + a terminal table) of `{call_site, backend, correctness, n, failures[]}`.
- **Consumer:** `local-model-agent`'s router consults the report (or a
  committed threshold per call site) to decide whether a decision is safe to
  route to the free backend. This proposal delivers the *measurement*; the
  *routing* is local-model-agent's slice.

## Backward compatibility

Old cassettes and traces load unchanged. Layer 1 only adds a failing condition
to `lint` for mocks whose `submitted` violates the schema — which is the bug we
want surfaced. No `--app` passed to `lint` ⇒ Layer 1 is skipped (today's
behavior preserved); the schema check is opt-in via `--app`.

## Fixtures / golden datasets

- `stories/dev-story/evals/brief_check.yaml` is the worked example (the call
  site this whole thread started from), with a crisp-brief and a vague-brief
  case. Its Layer 1 lint runs in CI; its Layer 2 report is regenerated by hand
  with `--live` and the latest number recorded in the eval file's header.
- A deliberately-wrong mock fixture proves Layer 1 has teeth (mutation test:
  break a stub's shape → `lint` must fail).

## Tasks

```
## 1. Layer 1 — offline schema-conformance (free)
- [ ] 1.1 cassette lint --app: resolve episode → call-site schema, validate
          response.data.submitted via agent.ValidateSubmission
- [ ] 1.2 Same check in the flow-stub resolver (hoststubs.go) so host_handlers
          mocks are schema-checked under `kitsoki test flows`
- [ ] 1.3 Mutation test: a broken stub shape fails lint
- [ ] 1.4 Backfill: lint the proposal-flow stubs I just wrote; fix any drift

## 1b. Effect/toolbox conformance (free) — deps: capability-model slice 1
- [ ] 1b.1 cassette lint --app also checks recorded tool uses ⊆ toolbox and ≤ declared effect
- [ ] 1b.2 Mutation test: a recorded out-of-box tool use (read-class call that wrote) fails lint

## 2. Eval dataset format + comparators
- [ ] 2.1 `kind: agent_eval` loader (call, agent, examples, fixtures, comparator)
- [ ] 2.2 exact / field_subset / enum comparators (deterministic)
- [ ] 2.3 judge comparator (records the judge as its own call site)

## 3. Layer 2 — live correctness eval (gated)
- [ ] 3.1 cassette verify --app --live --model <backend>: fire each example,
          validate schema, score vs expect, --repeat N for pass-rate
- [ ] 3.2 Comparison matrix (Claude vs llama.cpp) + JSON/table report
- [ ] 3.3 Worked example stories/dev-story/evals/brief_check.yaml

## 4. Document + hand off
- [ ] 4.1 docs/tracing/cassettes.md (Layer 1); docs/testing/agent-evals.md (Layer 2)
- [ ] 4.2 Note in local-model-agent.md how it consumes the report
- [ ] 4.3 Migrate shipped sections out of this proposal; trim/delete it
```

## Open questions

1. **Where do datasets live** — `stories/<app>/evals/<call>.yaml` (next to the
   story, version-controlled with it) vs a top-level corpus. *Lean: next to the
   story* — the prompt and its eval should move together.
2. **Sourcing `expected`** — hand-authored vs harvested from accepted real
   sessions (a `kitsoki trace to-eval` that pulls a recorded call's input +
   the response the operator accepted). *Lean: support both; start hand-authored.*
3. **The `judge` comparator's regress** — scoring open-ended output needs an
   LLM judge, which is itself a call site needing an eval. *Lean: keep judge
   comparators rare; prefer field_subset/enum; require the judge to have its own
   committed eval before it can score another.*
4. **What % bar promotes a call site to the free backend** — a single threshold
   vs per-call-site bands. *Lean: per-call-site, declared in the eval header,
   defaulting to the semroute 0.90 band.*

## Non-goals

- The routing decision itself (which backend serves a call in production) —
  that's `docs/proposals/local-model-agent.md`; this proposal only produces the
  evidence.
- New trace event types — this consumes existing call-site metadata and agent
  outputs; it does not add producers.
- Prompt *authoring* / optimization tooling — the eval measures a prompt; it
  doesn't rewrite it.

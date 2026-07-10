# Tracing: Agent task adherence benchmark

**Status:** Partially shipped. The offline eval substrate, `kitsoki eval`
list/show/run validation commands, `selection:` call-site metadata, the
`pr-refinement` merge-judge pilot dataset, compact fixture reports, and
`docs/testing/agent-evals.md` are implemented. Remaining work: strict cassette
and flow-stub conformance, live benchmark matrix execution, runtime pin
selection, and full TUI/web benchmark dashboards.
**Kind:**   tracing
**Epic:**   — standalone (consumed by `docs/proposals/local-model-agent.md`;
            hosts the conformance check for `docs/proposals/agent-capability-model.md`)

## Why

Kitsoki's product bet is that a story can turn messy agentic work into a set of
small, well-bounded tasks: the context is prepared before the model runs, the
allowed tools are narrow, the expected output is structured, and the surrounding
workflow is deterministic. That should let story authors run many tasks in
parallel, try cheaper/faster models, and still get acceptable outcomes.

Today we cannot prove that bet in a reusable way. We test the deterministic graph
with flow fixtures, and we can record cassettes, but we do not have a standard
answer to:

1. **Did this specific task obey its contract?** A flow's `host_handlers` stub
   or cassette response can invent a shape that the real agent would never
   produce. A green flow proves the graph, not the model boundary.
2. **Which backend/model is good enough for this task?** Kitsoki can run Claude,
   Codex, local llama.cpp, and operator-defined synthetic profiles, but story
   authors have no side-by-side evidence for `claude:sonnet` vs
   `codex:gpt-5-codex` vs `synthetic.new:syn:small:text`.
3. **Can the author pin a cheaper choice without hiding risk?** A task should
   carry its selected harness/backend/model/effort only after a benchmark shows
   that configuration meets the task's adherence bar.
4. **Can operators see the proof?** CLI-only reports are not enough. The TUI and
   web UI need to show why a task is pinned to a model, when the evidence is
   stale, and which failures block promotion.

The feature is deliberately product-facing: make Kitsoki the place where a story
author defines a bounded task, runs a model matrix against it, reviews adherence
evidence, and pins the cheapest acceptable configuration.

### Why Kitsoki and not an external evals framework

The story graph is the bounding structure that makes this tractable. A Kitsoki
call site already declares the task scope, prepared context, output schema,
toolbox, and effect class. Kitsoki can enforce that contract offline, run a
reproducible model matrix against it, and record the pinning decision beside the
story as traceable evidence.

External eval frameworks can still consume exported reports, but they do not own
the story-level contract. They cannot reliably know which tools were allowed,
which deterministic context was prepared, or which pin should be considered
fresh for a specific call site. The benchmark belongs where the contract is
defined.

## What changes

Add a **task adherence benchmark** for agent call sites. Each benchmark is a
story-local dataset plus acceptance policy. Kitsoki can run that dataset against
one or more harness profiles, score the outputs, record the results, and surface
an explicit promotion decision.

The benchmark has four layers:

- **Layer 1 — offline contract conformance (free, CI-default).** `kitsoki
  cassette lint <cassette> --app <app>` resolves each recorded agent call to its
  prompt/schema/toolbox contract and validates that mock/cassette output and
  recorded tool use stayed inside the declared box. This never calls a model.

- **Layer 2 — live adherence benchmark (opt-in, gated).** A story-local eval
  dataset runs the task against selected harness profiles and model variants:
  Claude, Codex, local llama.cpp, `synthetic-claude`, `synthetic-codex`, or any
  configured profile. Each run validates structure, scores semantic adherence,
  records latency/cost/tool-use, and reports pass rate over repeat runs.

- **Layer 3 — evidence-based pinning.** A task may declare a `selection:` policy
  and a pinned `profile/model/effort` only if the latest benchmark result meets
  its thresholds. Pinning is a recorded decision, not a silent config tweak.

- **Layer 4 — operator surfaces.** TUI and web show the task's current contract,
  candidate profiles, adherence matrix, costs, stale-evidence warnings, and the
  pin/unpin decision record.

This proposal owns the benchmark format, trace/report fidelity, CLI, and
operator-facing evidence surfaces. `local-model-agent.md` consumes the results
for routing cheap local decisions; this proposal generalizes the evidence model
to every harness profile and backend.

## Impact

- **New command surface:** `kitsoki eval run [--live]`, `kitsoki eval list`,
  and `kitsoki eval show`. The offline validation commands have shipped; the
  `--live` runner remains gated and intentionally makes no provider calls.
  Offline conformance still needs to extend `kitsoki cassette lint` rather than
  overloading cassette verification with live benchmark behavior.
- **Code seams:** call-site resolution near the cassette/flow stub resolver;
  report persistence in the trace/cassette area; TUI/web read-only views over
  reports; runtime selection reads task-level pins conservatively.
- **Vocabulary:** story-local `evals/*.yaml`; per-call-site `selection:` policy
  for `profile`, `model`, `effort`, evidence, and optional fallback; benchmark
  reports; pin/unpin decisions.
- **Stories affected:** none by default. A story opts in by adding eval
  datasets or a selection policy.
- **Backward compat:** existing stories, cassettes, and flow fixtures load
  unchanged. Offline lint only becomes stricter when the caller passes `--app`
  or enables the new check in a flow suite.
- **Docs on ship:** `docs/testing/agent-evals.md` has shipped for the offline
  dataset/report workflow. Remaining docs belong in `docs/tracing/cassettes.md`,
  `docs/architecture/agent-plugin.md`, and TUI/web docs as those slices land.

## First Pilot

Use the `pr-refinement` story's merge-judge `decide` gate as the first pilot:
a single-turn `agent.decide` task with a small stable schema and an existing
cassette. It demonstrates the central claim directly: when the context is
prepared and the task is bounded, cheaper models can be tested in parallel and
pinned when they meet the adherence bar.

If implementation discovers an equally representative single-turn decide task
that ships faster, it may substitute that task, but the first pilot must retain
these properties:

- single-turn agent call;
- prepared context, no exploratory tool use;
- small structured output schema;
- deterministic comparator;
- existing cassette or fixture suitable for offline conformance tests;
- visible TUI/web report state from committed fixture reports.

## Product Workflow

The intended author workflow is:

1. **Define a bounded task.** The story already has an agent call site with
   prompt, schema, prepared inputs, and declared toolbox/effect class.
2. **Add examples.** The author commits a small eval file with representative
   inputs, fixtures, expected output, and scoring comparator.
3. **Run the matrix.** The author runs a gated benchmark across configured
   profiles and models with `kitsoki eval run --live`, usually starting with
   cheap candidates.
4. **Review failures.** The UI groups failures by contract violation, wrong
   answer, tool overreach, timeout, or cost/latency regression.
5. **Pin the winner.** If a candidate meets the threshold, the author records a
   pin for that task. The pin includes the benchmark report id and thresholds it
   satisfied.
6. **Keep it honest.** Schema/prompt/example changes mark prior results stale;
   offline lint still runs in CI without live LLM calls. Strict lint fails stale
   pins only when explicitly enabled.

## Dataset & Scoring Model

A dataset lives beside the story, one file per task:

```yaml
kind: agent_eval
app: ../app.yaml
call: merge_judge
agent: merge_judge_agent

task:
  goal: "Classify whether a PR refinement diff is safe to merge."
  boundedness:
    max_turns: 1
    tool_policy: none
    prepared_context: true
  adherence_bar:
    min_pass_rate: 0.95
    max_p95_latency_ms: 8000
    max_avg_cost_usd: 0.002

matrix:
  profiles: [claude, codex-native, synthetic-claude, synthetic-codex, local]
  models:
    synthetic-codex: [syn:large:text, syn:small:text]
    synthetic-claude: [syn:large:text, syn:small:text]
  effort: [low, medium]
  repeat: 5

comparator: enum
examples:
  - name: clean-diff-merges
    args: { diff_path: cases/clean.diff, context_path: cases/clean-ctx.md }
    expect: { verdict: merge }
  - name: failing-ci-blocks
    args: { diff_path: cases/ci-fail.diff, context_path: cases/ci-fail-ctx.md }
    expect: { verdict: block }

selection:
  strategy: cheapest_passing
  pinned:
    profile: synthetic-codex
    model: syn:small:text
    effort: low
    evidence: reports/merge_judge/2026-06-22T10-41-00Z.json
    fallback_profile: claude
```

**Comparators** are deterministic where possible:

| Comparator | Pass when | Use for |
|---|---|---|
| `exact` | response equals expected | fixed extraction |
| `field_subset` | every expected key matches | decide outputs with variable prose |
| `enum` | one named field matches | classifier-style routing |
| `artifact_diff` | generated artifact matches a golden or accepted patch | bounded task outputs |
| `judge` | a separate evaluated judge accepts the answer | rare open-ended outputs; allowed only when the judge call site itself has passing evidence |

The benchmark reports an **adherence score** per candidate:

- schema valid rate
- comparator pass rate
- toolbox/effect conformance
- p50/p95 latency
- average and p95 cost
- retry/fallback rate
- failure samples with prompt/report ids

The headline pass/fail is policy-driven: a model passes only if it meets the
task's adherence bar and its contract conformance is 100%.

## Backends, Profiles, and Models

Benchmarks run against harness profiles, not hard-coded providers. A profile is
the same concept already used by local config: backend, default model, supported
models, endpoint/env, and any model-specific options. The matrix references
profile names from `.kitsoki.yaml` / `.kitsoki.local.yaml`:

```yaml
harness_profiles:
  synthetic-claude:
    backend: claude
    model: syn:large:text
    models: [syn:large:text, syn:small:text]
    models_endpoint: https://api.synthetic.new/openai/v1/models
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"

  synthetic-codex:
    backend: codex
    model: syn:large:text
    models: [syn:large:text, syn:small:text]
    models_endpoint: https://api.synthetic.new/openai/v1/models
    env:
      OPENAI_BASE_URL: https://api.synthetic.new/openai/v1
      OPENAI_API_KEY: "${SYNTHETIC_API_KEY}"
```

The report records both the human-facing profile and the resolved execution
details:

```json
{
  "profile": "synthetic-codex",
  "backend": "codex",
  "provider": "synthetic.new",
  "model": "syn:small:text",
  "effort": "low",
  "schema_hash": "sha256:...",
  "prompt_hash": "sha256:...",
  "dataset_hash": "sha256:..."
}
```

This keeps the feature open to Claude, Codex, local llama.cpp, and future
providers without adding provider-specific benchmark vocabulary. It also lets
synthetic.new's larger model catalog participate in the same matrix as native
Claude/Codex models.

## Evidence-Based Pinning

A pin is allowed only when the report is fresh for the current prompt/schema/
dataset/toolbox hashes and meets the declared adherence bar. The pin lives with
the task so normal story review can see it:

```yaml
invoke: host.agent.decide
id: merge_judge
agent: merge_judge_agent
selection:
  profile: synthetic-codex
  model: syn:small:text
  effort: low
  evidence: stories/pr-refinement/evals/reports/merge_judge/latest.json
  fallback_profile: claude
```

Runtime behavior is conservative:

- Missing evidence does not break existing stories, but the UI marks the pin
  unverified.
- Stale evidence warns in TUI/web and fails a strict CI lint if enabled.
- A runtime validation failure falls back only if the call site explicitly
  declares a fallback; otherwise the pinned run fails so stale or invalid
  evidence surfaces instead of silently regressing.
- Pin changes are traceable: who pinned, when, against which report, and what
  cheaper/faster candidate was rejected.

## TUI and Web Surfaces

The operator surface should answer "why is this task using this model?" without
opening raw JSON.

**TUI**

- `kitsoki eval list` or a story command shows tasks with status: unmeasured,
  passing, failing, stale, pinned.
- A task detail view shows the candidate matrix, adherence bar, selected pin,
  cost/latency columns, and failure counts.
- During a live session, a compact task badge shows `profile/model`, whether the
  pin is verified, and whether a fallback happened.

**Web UI**

- Story/task benchmark dashboard: sortable matrix by pass rate, cost, latency,
  and freshness.
- Failure drill-down: example input, expected subset, actual submitted output,
  contract/tool violations, and trace links.
- Pin action: records the selected candidate and evidence id, with a warning if
  the candidate passes quality but exceeds cost/latency policy.

These are evidence surfaces, not model marketing. The primary call to action is
to improve the task contract or choose a passing cheaper candidate.

## Determinism and Cost Guardrails

- Offline lint and report freshness checks are deterministic and CI-safe.
- Live benchmark runs require an explicit `--live` or equivalent UI confirmation.
- Synthetic/Claude/Codex runs may incur cost and must never run in default tests.
- Reports are committed only when the author chooses to pin or document a
  decision; raw provider logs stay in `.artifacts/` unless needed for review.
- Flow tests use cassettes or stubs, and stubs must pass Layer 1 schema/toolbox
  conformance.

## Decision Recording

The trace/report must be enough to reconstruct the decision:

- task id, story app, prompt/schema/dataset/toolbox hashes
- profile/backend/provider/model/effort
- examples run, repeat count, comparator, thresholds
- submitted output, validation status, comparator result
- cost/latency/tool-use/fallback metadata
- pin/unpin decision and evidence id

If this adds new trace events, the implementation should split the event
producer detail into a focused tracing child proposal. The minimum viable
implementation can start with report JSON plus links from existing trace events.

## Producers & Consumers

- **Producers:** `kitsoki eval run`, flow stub lint, cassette lint, and optional
  UI-triggered benchmark runs.
- **Consumers:** TUI/web benchmark views, selection lint, `local-model-agent`'s
  cheap-routing decision, story review, and cost reports.
- **Related proposals:** `local-model-agent.md` consumes passing local-model
  evidence; `reward-function.md` scores whole episodes;
  `agent-capability-model.md` defines the toolbox/effect-class model that
  conformance reads.

## Backward Compatibility

Old stories and cassettes load unchanged. A story gets benchmark behavior only
after adding `evals/*.yaml` or a selection policy. Existing `--claude-model` and
harness-profile settings continue to work; task-level pins are narrower and win
only for the named call site.

## Fixtures / Golden Datasets

- `stories/pr-refinement/evals/merge_judge.yaml` is the worked pilot example
  for a single-turn `agent.decide` task with `comparator: enum`.
- A bounded `host.agent.task` example demonstrates minimal tool use: prepared
  context, one expected artifact, read-only or write-scoped toolbox, and an
  `artifact_diff` comparator.
- A deliberately broken mock proves Layer 1 has teeth: schema violation and
  out-of-toolbox use must fail without a live model.

## Tasks

```
## 0. Pilot setup
- [x] 0.1 Audit `pr-refinement` merge-judge decide gate: confirm schema,
          toolbox/effect class, and cassette exist and are suitable for the
          pilot
- [x] 0.2 Author `stories/pr-refinement/evals/merge_judge.yaml` with enum
          comparator, 2+ examples, and full matrix declaration
- [x] 0.3 Commit fixture reports that let TUI/web render unmeasured, passing,
          failing, stale, and pinned states without live LLM calls

## 1. Offline contract conformance (free)
- [ ] 1.1 `cassette lint --app`: resolve episode -> call-site schema/toolbox,
          validate submitted output via agent.ValidateSubmission
- [ ] 1.2 Check flow `host_handlers` stubs under `kitsoki test flows`
- [x] 1.3 Check recorded tool uses against declared toolbox/effect when
          agent-capability-model metadata is present
- [ ] 1.4 Mutation tests for broken schema and out-of-box tool use
          (`internal/agenteval/conformance` covers out-of-box tool-use traces;
          schema mutation tests remain with cassette/flow stub lint wiring)

## 2. Eval dataset + report format
- [x] 2.1 Load `kind: agent_eval` files with task boundedness, matrix,
          adherence bar, examples, comparator, and selection policy
- [x] 2.2 Implement deterministic comparators: exact, field_subset, enum,
          artifact_diff
- [x] 2.3 Define report JSON with prompt/schema/dataset/toolbox hashes and
          cost/latency/fallback fields
- [x] 2.4 Add `kitsoki eval list`, `kitsoki eval show`, and offline
          `kitsoki eval run` validation over datasets and reports

## 3. Live benchmark runner (gated)
- [ ] 3.1 Run selected profile/model/effort matrix behind `--live` (currently
          gated and not implemented; no provider calls are made)
- [ ] 3.2 Support repeat runs and pass-rate bands
- [ ] 3.3 Resolve harness profiles from `harness_profiles` in `.kitsoki.yaml`
          / `.kitsoki.local.yaml`, including native Claude/Codex, local, and
          synthetic.new Claude/Codex profiles and model lists
- [ ] 3.4 Run the merge-judge matrix behind an explicit live gate and record
          the cheapest passing candidate
- [ ] 3.5 Add a bounded `agent.task` worked example after the decide pilot lands

## 4. Evidence-based pinning
- [x] 4.0 Add call-site `selection:` metadata to `host.agent.*` invokes
- [ ] 4.1 Lint selection policy: pin must reference fresh passing evidence when
          strict mode is enabled
- [ ] 4.2 Runtime call-site selection reads the pin conservatively and records
          fallback/substitution
- [ ] 4.3 Pin/unpin decision record includes rejected candidates and reason

## 5. TUI and web surfaces
- [ ] 5.1 TUI task list/detail view for adherence matrix and stale/pinned status
- [ ] 5.2 Web benchmark dashboard with failure drill-down and trace links
- [ ] 5.3 Live-session task badge showing verified profile/model and fallback

## 6. Document + hand off
- [x] 6.1 docs/testing/agent-evals.md: author workflow and benchmark format
- [ ] 6.2 docs/tracing/cassettes.md: offline conformance and report fidelity
- [ ] 6.3 docs/architecture/agent-plugin.md: profile/model/effort selection
- [ ] 6.4 Note in local-model-agent.md how it consumes passing benchmark reports
- [x] 6.5 Migrate shipped sections out of this proposal; trim it to remaining
          work
```

## Verification

Default verification must not call a live LLM:

- Unit tests for dataset loading, hashing, comparators, report freshness, and
  selection lint.
- Flow/cassette tests proving invented response shapes and out-of-toolbox calls
  fail offline.
- UI tests render the benchmark states from committed fixture reports.
- Live provider tests are gated and run only when explicitly requested.

## Resolved Decisions

The reconciliation turns the earlier leans into implementation decisions:

1. **Command shape** — add `kitsoki eval run [--live]`, `kitsoki eval list`, and
   `kitsoki eval show`; keep `kitsoki cassette lint --app` for offline
   conformance.
2. **Where pins live** — directly on the call site as `selection:`; eval
   datasets and report evidence live beside the story under `evals/`.
3. **Effort vocabulary** — normalize to `low/medium/high`; reports record the
   resolved provider-native values.
4. **Judge comparator regress** — allow `judge` only when the judge call site
   itself has passing evidence; standard tasks should use deterministic
   comparators.
5. **Report retention** — commit selected evidence plus compact decision
   history; keep raw run artifacts in `.artifacts/` unless they are needed for
   review.

## Non-goals

- Fully autonomous prompt optimization. The benchmark measures and pinpoints
  failures; it does not rewrite the prompt.
- Replacing story flow tests. Flow tests still prove deterministic graph
  behavior; benchmarks prove model-boundary adherence.
- Whole-episode reward. That belongs to `reward-function.md`; this proposal
  focuses on bounded agent tasks/call sites.
- Default live CI. Paid or external model calls remain explicitly gated.

# Epic: repo history as training material

**Status:** Draft v3. Feasibility-reviewed against the current mining,
bakeoff, agent-eval, and trainable-story substrates. The gears-rust bugfix
reference path is now implemented as a product smoke; the broader generic
corpus/task-case/precedent slices remain draft.
**Kind:**   epic
**Slices:** 5 generic slices remain draft; 1 shipped bugfix reference path

## Why

Kitsoki already has the core idea it needs: a story can be treated as a
trainable model, with traces and transcripts acting as labeled examples rather
than disposable logs. The existing
[`stories-as-trainable-models.md`](stories-as-trainable-models.md) epic names
the loop at the level of reward, credit assignment, and optimizer step. The
current `training-loop` slice names the per-story update cycle:
*forward pass -> reward -> attribution -> candidate edit -> flow-fixture
validation -> accept/discard*.

What is still missing is the **productized corpus and scaling workflow around
that loop**: how Kitsoki should learn from its own repo history, from sister
repos, from external open-source repos, and from repeated operator work so the
next run is better than the last one.

This epic is feasible only if it **reuses and generalizes existing substrate**
instead of building a parallel "history trainer":

- `internal/mining` already defines the backend-neutral
  `CanonicalSession` / `SessionSource` substrate and the DI-shaped
  mine -> propose -> apply loop.
- `docs/proposals/session-mining-backend-generalization.md` already defines the
  corpus adapters, evidence index, drivers, and kitsoki trace-pattern mining
  shape this epic should consume.
- `tools/bugfix-bakeoff/external/` already proves the most concrete version of a
  history-backed training case: find a real historical bug, pin the baseline and
  real fix, hide the real PR's regression test as an oracle, drive a candidate
  workflow, and grade deterministically.
- `internal/agenteval` already defines story-local eval datasets for bounded
  agent call sites, with deterministic validation and offline reports.
- `stories/task-bakeoff`, `stories/model-harness-eval`, and
  `tools/session-mining/eval_pilot_report.py` already provide report/deck/cost
  rollups from committed evidence without re-spending model calls.

The user-facing problem is broader than a single feature. The same historical
material should improve:

- onboarding: which prior cases show a new user how Kitsoki behaves;
- bug fixing: which past failures and fixes are the nearest training examples;
- feature spec and design: which earlier proposals, reviews, and decisions are
  the right reference set;
- feature implementation: which code paths, stories, and traces should be
  surfaced as the implementation precedent;
- dev lifecycle and SDLC: which canonical workflows, gates, and acceptance
  patterns should become the default operating model.

Today that knowledge exists, but it is scattered across proposals, docs,
transcripts, traces, bakeoff manifests, eval reports, and repo history. The
result is repeated explanation, weak precedent selection, and expensive manual
case discovery. This epic makes that history into a structured training corpus
for Kitsoki stories, so the system can learn the way the user described: as an
LLM-like model that improves from examples, but with the "weights" living in
story files, prompts, `.star` scripts, flow fixtures, eval datasets, and
selection policies rather than tensors.

## Feasibility verdict

Feasible, with three constraints:

1. **Use the mining corpus as the source of truth.** Do not introduce a second
   repo-history database. Add repo-history sources, labels, and drivers to the
   existing canonical corpus model (`internal/mining/corpus.go`) and to the
   session-mining backend-generalization proposal.
2. **Generalize the bugfix-bakeoff contract, not its scripts only.** The reusable
   concept is a task case with: historical baseline, expected outcome, hidden or
   deterministic oracle, contender/candidate matrix, cost telemetry, and offline
   rollup. Bugfix is one lane, not the whole system.
3. **Autonomous scaling must be gated by deterministic arming.** The system may
   autonomously mine cases, prepare manifests, verify RED/GREEN or equivalent
   historical outcome splits, run no-cost scoring/reporting, and queue
   cost-bearing cells. It must not run live LLM training/eval cells in automated
   tests or CI; live drives remain operator-approved and resumable.

## What changes

Once this epic and its slices ship, Kitsoki can do five things consistently:

1. **Curate training material from history.** Past proposals, session traces,
   bugfixes, eval reports, design reviews, onboarding runs, and shipped docs are
   normalized into the existing mining corpus with purpose, provenance, and
   outcome labels.
2. **Convert history into task cases.** A historical item can graduate into a
   generic task manifest: inputs, baseline/historical context, expected behavior,
   deterministic oracle or comparator, acceptance bar, cost policy, and
   artifacts. The bugfix-bakeoff manifest is the reference implementation, but
   the schema must also cover design, onboarding, implementation, docs review,
   and story-routing tasks.
3. **Route work to the right precedent.** A new task can ask the corpus for the
   nearest successful and failed cases for onboarding, bug fixing, design,
   implementation, or lifecycle planning instead of starting from a blank prompt.
4. **Run bounded training/eval loops at scale.** A cheap-to-expensive ladder can
   drive prepared task cases, stop at the first acceptable result, record cost,
   and resume without re-spend. The deterministic side runs freely; live model
   cells are explicit operator-only actions.
5. **Close the loop with outcomes.** Selected examples, candidate edits,
   validations, accept/reject decisions, costs, and rollups are recorded in
   traces and durable artifacts. Successful patterns can become future defaults;
   failed patterns become explicit anti-patterns and regression fixtures.

The end state is a Kitsoki that treats repository history as training data for
its own stories: the corpus is curated, training examples are named, task cases
are armed, selection is traceable, and improvements are validated by flow
fixtures, agent evals, or deterministic project oracles.

## Shipped reference path: gears-rust bugfix training

The bugfix lane now has a durable, no-cost reference path for a heavy/private
repo:

- `tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml` captures
  four armable historical fixes plus reference-only marathon and hard-case
  examples.
- `make gears-bakeoff` proves the hidden oracles are RED at the historical
  baseline and GREEN after the real fix against a local checkout.
- `make history-smoke` is the reusable product-path smoke: harness unit tests,
  candidate/profile preflight, scoped oracle arming, exact `drive_cell.sh`
  command rendering, free `drive_cell.sh --no-drive` preparation, and
  `repo-bakeoff` story flow validation. `make gears-history-smoke` is the
  preconfigured gears-rust wrapper, and `make gears-history-full-smoke` covers
  and prepares all four armable gears-rust fixtures. The smoke also writes a
  readiness Markdown audit under `.artifacts/external-bakeoff/readiness/`.
- `stories/repo-bakeoff` wraps the deterministic prepare, run-command handoff,
  scoring, reporting, and Slidey deck generation path without running live LLM
  cells in tests.
- `docs/recipes/repo-history-training-gears-rust.md` is the repo-owner recipe
  for repeating the path on gears-rust or another private repo.

This shipped path is intentionally still a bugfix-lane specialization. It proves
the process discipline this epic wants to generalize, but it does not replace
the remaining corpus, generic task-case, precedent-selection, and workflow
integration slices below.

## Reuse and extension targets

This epic must build on these current surfaces:

| Existing surface | Reuse | Required extension |
|---|---|---|
| [`internal/mining`](../../internal/mining) | Canonical session envelope, source adapters, evidence refs, DI seams, staged proposal/apply gate | Add repo-history case labels, task-case output driver, source registry integration, and training-example indexes |
| [`session-mining-backend-generalization.md`](session-mining-backend-generalization.md) | Backend-neutral corpus, evidence index, kitsoki trace-pattern mining drivers | Add `history-task` and `precedent-search` drivers rather than a new corpus |
| [`tools/bugfix-bakeoff/external`](../../tools/bugfix-bakeoff/external) | Manifest shape, RED/GREEN oracle arming, baseline/fix pins, cost-bearing `drive_cell`, free `verify`/`score`/`summarize`, escalation ladder | Extract a generic task/oracle schema so bugfix becomes one specialization |
| [`internal/agenteval`](../../internal/agenteval) | Story-local eval datasets, call-site resolution, comparator/adherence-bar contract | Allow mined history examples to draft eval datasets and link reports back to corpus cases |
| [`stories/task-bakeoff`](../../stories/task-bakeoff) | Matrix comparison workflow, deterministic report/deck handoff | Point at the generic task-case manifest instead of only bugfix-shaped cells |
| [`stories/model-harness-eval`](../../stories/model-harness-eval) | Operator-approved live policy, no-cost default, local/project/author apply modes | Use as the model/candidate selection lane for task-case sweeps |
| [`tools/session-mining/eval_pilot_report.py`](../../tools/session-mining/eval_pilot_report.py) | Offline report aggregation over committed evidence | Consume generic task-case reports alongside agent eval and coverage evidence |
| [`docs/testing/open-source-repo-catalog.md`](../testing/open-source-repo-catalog.md) | Public/private repo fixture catalog and graduation checklist | Add non-bugfix task lanes and status tracking for mined training cases |

## General architecture

The architecture is one pipeline with two separable halves: a free, deterministic
preparation half and an explicitly gated live-drive half.

```text
repo history / traces / transcripts / docs / eval reports
        |
        v
existing mining corpus
  CanonicalSession + evidence refs + source adapters
        |
        v
history-task miner
  labels purpose, outcome, trainable surface, story, repo, cost, evidence
        |
        v
task-case manifests
  bugfix | design | onboarding | implementation | docs | routing | eval
        |
        +--> deterministic arming
        |      RED/GREEN oracle, comparator, fixture, or acceptance-bar proof
        |
        +--> precedent index
        |      nearest successes, failures, anti-patterns, examples
        |
        v
gated runner
  candidate ladder + resumable cells + cost extraction + traces
        |
        v
offline rollup
  reports, decks, promoted fixtures/evals/docs, rejected anti-patterns
```

### Task-case contract

A task case is the generalization of a bugfix-bakeoff bug row and an
`internal/agenteval` example. It should be stored as a manifest row, not as prose
inside a proposal.

```yaml
kind: history_task.v1
id: <stable-case-id>
lane: bugfix|design|onboarding|implementation|docs|routing|agent_eval
source:
  corpus_ref: <canonical-session-or-repo-history-ref>
  repo: <path-or-url>
  baseline_ref: <sha-or-trace-ref>
  success_ref: <sha-or-trace-ref>
story:
  app: stories/<story>/app.yaml
  entrypoint: <intent-or-room>
trainable_surface:
  weight_kind: prompt|slot|star|graph|decider|fixture|eval|selection
  target_refs: []
input:
  prompt_or_ticket: |
    ...
oracle:
  kind: red_green|flow_fixture|agent_eval|artifact_comparator|static_check|human_review_required
  command: <optional deterministic command>
  comparator: <optional comparator name>
  acceptance_bar: {}
cost_policy:
  live_policy: no_cost|operator_approved
  ladder: <candidate-ladder-name>
artifacts:
  root: .artifacts/history-training/<case-id>/
```

Rules:

- `oracle.kind=red_green` is the current bugfix-bakeoff shape: prove failure at
  baseline and success at the real fix before spending.
- `oracle.kind=flow_fixture` uses `kitsoki test flows` as the no-LLM validation
  set for story behavior.
- `oracle.kind=agent_eval` links to `stories/<story>/evals/*.yaml` and
  `internal/agenteval` comparators.
- `oracle.kind=human_review_required` is allowed only as a queue state; it is
  not enough for autonomous training or CI.
- Every case records source refs back to the canonical corpus and writes
  generated review artifacts under `.artifacts/`, not the repo root.

### Precedent selection contract

Precedent selection is retrieval plus policy, not ambient memory. A story that
uses history must record:

- the candidate set searched;
- the selected examples and anti-patterns;
- why each was selected;
- which prompt/room/decision received the examples;
- whether the final outcome improved, regressed, or remained unknown.

This should be a trace event or artifact emitted by the story, then normalized
back into the mining corpus. The selected precedent becomes part of the run's
training data and can be audited later.

### Autonomous scaling contract

Autonomous scaling means:

- mine cases from history without live LLM where deterministic extraction is
  enough;
- use at most one schema-checked, cassette-backed agent pass where summarization
  or case drafting needs a model;
- verify every case is armed before it can enter a live runner;
- run free `verify`, `score`, `aggregate`, and deck/report generation on demand
  and in tests;
- maintain resumable cell state so re-running a ladder does not re-spend solved
  cells;
- default to cheapest viable candidates and escalate only when the current rung
  fails;
- keep live LLM drives operator-approved, never automatic in CI;
- promote accepted outcomes into docs, fixtures, eval datasets, or story
  defaults, then delete or trim proposals per the normal proposal lifecycle.

This is the bugfix-bakeoff discipline generalized across lanes.

## Impact

- **Spans:** tracing/mining, runtime/tooling, story integrations, docs/process.
- **Net surface:** corpus labels and drivers; generic task-case manifests;
  oracle/comparator arming; precedent-selection artifacts; a gated/resumable
  runner over candidate ladders; offline report/deck rollups; docs that explain
  how story authors and operators use the corpus.
- **Runtime risk:** low if v1 stays outside the engine hot path and reuses
  `internal/mining`, `internal/agenteval`, and existing story/test runners via
  dependency injection.
- **Cost risk:** controlled by the same rule as bakeoff and model-harness eval:
  automated paths are no-cost; live cells require explicit operator approval.
- **Docs on ship:** a new architecture/process writeup for the history-training
  loop, plus updates to story-authoring, testing, onboarding, model-harness eval,
  open-source repo catalog, and proposal docs.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 0 | gears-rust bugfix reference path | tooling + story + docs | Product-smoke the repo-history loop on a heavy/private Rust repo using the existing external bakeoff contract | — | Shipped | [`../recipes/repo-history-training-gears-rust.md`](../recipes/repo-history-training-gears-rust.md) |
| 1 | Corpus and labels | tracing + runtime | Extend the existing mining corpus with repo-history sources, case labels, source refs, and precedent indexes | — | Draft | `repo-history-corpus.md` |
| 2 | Generic task/oracle manifests | runtime + tooling | Extract the bugfix-bakeoff case/oracle/cell/result contract into a lane-neutral manifest and scorer interface | 1 | Draft; informed by shipped gears-rust reference | `repo-history-task-cases.md` |
| 3 | Precedent selection | story + tracing | Let stories request, inject, and trace selected examples/anti-patterns from the corpus | 1 | Draft | `repo-history-precedent-selection.md` |
| 4 | Gated autonomous runner | runtime + story | Run armed task cases through cheap-to-expensive ladders, resumably, with no-cost verification/reporting and operator-approved live cells | 2, 3 | Draft | `repo-history-runner.md` |
| 5 | Workflow integrations | story + docs | Wire onboarding, bugfix, spec/design, implementation, docs review, and SDLC stories to the shared precedent/task-case loop | 3, 4 | Draft | `repo-history-workflows.md` |

## Sequencing

```text
#1 (corpus + labels)
       |
       +--> #2 (task/oracle manifests) --> #4 (gated runner)
       |
       +--> #3 (precedent selection) ----> #5 (workflow integrations)
                                      \---> #4 (runner uses selected context)
```

Slice 1 is the substrate. Slice 2 generalizes the bugfix-bakeoff harness into a
task-case contract. Slice 3 makes history usable inside stories. Slice 4 scales
armed cases through candidate ladders. Slice 5 keeps the domain-specific logic in
the stories, not in the runner.

## Shared decisions

1. **No second corpus.** Repo-history training data is an extension of
   `internal/mining` and session-mining, not a new archive format.
2. **Bugfix bakeoff is the reference, not the boundary.** Its RED/GREEN oracle,
   manifest, cell result, candidate ladder, cost extraction, and offline rollup
   are the model for all task lanes. Bugfix-specific fields stay behind a lane
   adapter.
3. **Task cases must be armed before live drive.** A case without a deterministic
   oracle, flow fixture, agent-eval comparator, static check, or explicitly
   reviewed acceptance bar cannot enter the autonomous runner.
4. **Training is observable.** Stories that consume history record selected
   examples, selected anti-patterns, selection policy, candidate edits, validation
   results, and accept/reject decisions.
5. **Autonomy is bounded.** The system may mine, prepare, verify, score, roll up,
   and queue cases autonomously. Live model cells and story-weight edits remain
   gated by explicit operator or deterministic acceptance policy.
6. **Promotion is the real weight update.** A historical lesson is not "trained"
   until it becomes a prompt/fixture/flow/eval/story/default/doc change that
   passes the validation gate and is recorded as such.

## Cross-cutting open questions

1. **Where does the generic task-case schema live?** *Lean:* start under
   `tools/history-training/` only if a standalone tool is necessary; otherwise
   put the manifest loader/scorer in `internal/agenteval` or a sibling
   `internal/taskcase` package and keep bugfix-bakeoff as the first adapter.
2. **How much of bugfix-bakeoff should be refactored immediately?** *Lean:* do
   not rewrite the working harness first. Add the lane-neutral schema beside it,
   build an adapter for the current bugfix manifests, then migrate only once the
   generic path can reproduce current summaries.
3. **What is the minimum oracle for non-code tasks?** *Lean:* require one of:
   flow fixture, agent-eval comparator, artifact schema/static check, or human
   review promoted into a deterministic fixture/eval before the case is counted
   as autonomous.
4. **Should sibling repos be first-class sources in v1?** *Lean:* yes, but only
   as named corpus sources with explicit roots and local-only flags, following
   the open-source repo catalog's separation of public OSS and local/private
   references.
5. **How does precedent selection avoid overfitting?** *Lean:* every selected
   precedent must be paired with at least one anti-pattern or holdout case where
   possible, and acceptance must key on the oracle/fixture/eval, not on lexical
   similarity to the example.

## Non-goals

- Replacing existing proposal, story authoring, session-mining, agent-eval, or
  bugfix-bakeoff workflows wholesale.
- Training on arbitrary repo noise without curation, source refs, and outcome
  labels.
- Live LLM evaluation in automated tests or CI.
- Building a generic external search engine before the local corpus loop exists.
- Tuning provider model weights. The trained surface is Kitsoki's story and
  workflow artifacts.
- Counting a human-only anecdote as autonomous training evidence before it is
  promoted into a deterministic oracle, flow fixture, eval comparator, or static
  check.

## Tasks

- [ ] Cut the five slices into child proposals, keeping this file as the epic
      index only.
- [x] Ship the gears-rust bugfix reference path with a local-only manifest,
      RED/GREEN arming, `repo-bakeoff` deterministic flow fixtures, exact
      live-cell command rendering, generated report/deck output, and a no-cost
      `make history-smoke` product-path gate with gears-rust one-fixture and
      full-corpus wrappers.
- [ ] Add a corpus-label design that extends `internal/mining` and the
      session-mining backend-generalization proposal instead of creating a new
      corpus.
- [ ] Define the generic task-case manifest and adapter plan from the current
      bugfix-bakeoff manifest/result contracts.
- [ ] Prove the generic manifest can represent the existing `query-string`,
      `gears-rust`, and `kitsoki` bugfix-bakeoff projects without changing their
      outcome semantics.
- [ ] Add one non-bugfix pilot lane, preferably a story-local `agent_eval` or
      flow-fixture case, to prove the schema is not bugfix-shaped.
- [ ] Add precedent-selection trace/artifact events to one target story under a
      no-LLM fixture.
- [ ] Add offline aggregation/report output under `.artifacts/history-training/`
      using existing eval/bakeoff reporting where possible.
- [ ] Write shipped narrative docs and delete this epic when the slices land.

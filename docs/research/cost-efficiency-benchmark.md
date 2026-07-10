# Kitsoki vs Agentic-Only: Cost-Efficiency Investigation Design

**Status:** FROZEN (M0 complete) — this is the committed protocol. Hypotheses,
metric definitions, and success criteria below are pre-registered and MUST NOT
be altered to fit results. Any change after this freeze requires a new,
explicitly versioned amendment section, not a silent edit. No live spend may
occur under this protocol until the corpus is also frozen (M1, see
`docs/goals/generalized-usage/decomposition.yaml` change `WB.1`).

**Claims under test:** the economics scenes of `.context/profit.slidey.json` — the
"Economics: where cost actually moves" scorecard, "Why this can be efficient", and
"Kitsoki trains the process like a model".
**Frozen:** 2026-07-03.
**Origin:** migrated verbatim (structure and content) from
`.context/cost-efficiency-benchmark-plan.md`, which is superseded by this
document. See `docs/goals/generalized-usage/decomposition.yaml` change `WB.0`
for the freeze gate.

---

## 1. Objective

Produce **research-grade, reproducible, deterministically orchestrated** evidence
for the profit-thesis economic claim:

> The economic lever is not cheaper tokens alone. It is narrower tasks, fewer
> misses, and reusable process memory.

Concretely: on the **same real tasks**, under **identical conditions**, a kitsoki
story pipeline achieves **matched-or-better outcomes at materially lower cost**
than agentic-only execution — and, unlike the agentic baseline, the kitsoki
process is **trainable**: failures become deterministic workflow patches whose
improvement is measured round-over-round and validated on held-out tasks.

The investigation harness IS the training loop. That is the point of the design,
not a side effect: `profit.slidey.json`'s "story → agent → trace → failure →
improve" cycle is executed literally, with every round versioned and disclosed.

### What "conclusively demonstrate" means here

Pre-registered hypotheses, a frozen task corpus with hidden deterministic
oracles, a strong (not straw-man) baseline, full disclosure of every round
including failures, and headline numbers taken **only from a confirmation run on
held-out tasks with the frozen trained process** — never from the training
rounds themselves. Iterating until we hit targets is legitimate *because* the
trainability of the process is itself a registered hypothesis (H3) — but the
iteration must be visible, and the final claim must generalize.

---

## 2. Hypotheses (pre-registered, frozen)

| ID | Hypothesis | Primary metric | Pre-registered success criterion |
|----|-----------|----------------|----------------------------------|
| **H1** | Cost efficiency at outcome parity | cost-per-solve (tokens primary, USD secondary) | On held-out tasks: kitsoki solve-rate ≥ baseline solve-rate AND kitsoki cost-per-solve ≤ **0.5×** the best agentic-only cost-per-solve (≥2× efficiency; we expect larger, the bar is deliberately conservative) |
| **H2** | Model right-sizing ("narrow steps need weaker models") | solve-rate by (treatment × model tier) | kitsoki + **mid-tier** model achieves solve-rate ≥ agentic-only + **frontier** model on held-out tasks |
| **H3** | Process trainability ("failures are training data") | round-over-round Δ(solve-rate, cost-per-solve) on training split, confirmed on held-out split | ≥1 training round produces a statistically visible improvement on the training split that **persists on held-out tasks** (no-overfit gate), while the frozen baseline arm does not improve |
| **H4** | Reprocessing-tax immunity (flat cost in session length) | cost of op #k as a function of k within a session | kitsoki per-op cost flat (±20%) across a 5-op session; agentic-only per-op cost grows monotonically (extends `docs/case-studies/git-ops-cost.md` with *measured*, recorded numerators) |

Failure to meet a criterion is reported as such. H3's loop ("we must continue")
runs until criteria are met, max rounds are exhausted, or improvement plateaus —
§7 defines the stop rule; whichever fires is what gets published.

---

## 3. What exists vs what must be built

All measured facts below verified against the tree on 2026-07-03 (at freeze time).

### Exists (reuse, don't reinvent)

| Asset | Location | Role in this design |
|---|---|---|
| Intent mining (instance-first, grounded, no-fabrication gate) | `tools/session-mining/` steps A–F (`prep.py`, `intents.workflow.js`, `ground.py`, `tag_score.py`, `outcomes.py`, `emit.py`) | Phase A: find the real task archetypes from this repo's transcripts |
| Exact raw-agentic cost extraction | `tools/session-mining/cost_extract.py` + `pricing.py` (reads `message.usage`; reproduces native cost to ~0.4%) | Baseline-arm cost accounting; archetype ranking by real dollars |
| Story-vs-raw cost pairing (observational) | `tools/session-mining/cost_report.py`, `make cost-report`, `docs/case-studies/git-ops-cost.md` | H4 precedent + reporting idiom; its honesty gap (authored `record_mode: none` cassettes) is closed by this study's recorded runs |
| Cell/oracle/scoring machinery | `tools/bugfix-bakeoff/external/` (`bench.py` score/verify/preflight, `drive_cell.sh`, `escalate.sh`, `candidates.yaml`, `results/SCHEMA.md`) | Cell contract, hidden-oracle overlay, RED/GREEN arming, deterministic grading, cost fields |
| Container/VM comparison runner | `tools/arena` — **worktree-only** (`.worktrees/arena`, PRs #57/#61 merged to their branch, NOT in main HEAD) | The deterministic orchestrator: JobSpec → Cell[] → containers → rollup |
| 10-OSS-repo corpus + selection contract | `tools/product-journey/github-targets.json` (vscode, kubernetes, nextjs, tensorflow, pytorch, rust, cpython, ansible, grafana, typescript) | The demonstration corpus |
| Fixture discovery method for external repos | `.agents/skills/external-repo-bakeoff/SKILL.md` | How to find + verify real filed-issue bugs with regression-test PRs per target repo |
| Matrix method + offline zero-re-spend reporting | `.agents/skills/matrix-task-comparison/SKILL.md`, `eval_pilot_report.py --markdown --deck` | Report/deck regeneration without re-running |
| Drivable stories | `stories/bench-bugfix`, `stories/task-bakeoff` | The kitsoki-arm executors |

### Must be built (the composition gap)

1. **Land arena on main** — cherry-pick `tools/arena` from the worktree onto
   current main (memory: main moves fast; re-cut, don't whole-branch merge).
2. **`paired-task` arena plugin** — runs one task as N treatments, pairs the
   measured costs, applies the shared hidden oracle to every arm. Wraps
   `bench.py` for bugfix; adds oracle runners for the non-bugfix archetypes (§5.3).
3. **Agentic-only treatment executor** — a first-class `single` treatment beyond
   the bug9 pilot: headless `claude -p` / codex run in the same container, same
   ticket text, same repo, no kitsoki. Two baseline tiers (§6.2).
4. **Archetype → task instantiation recipes** — turn a mined archetype into a
   runnable, oracle-bearing case against a target repo (§5).
5. **Round ledger + training-packet tooling** — deterministic scripts that turn a
   round's failed cells into a failure taxonomy and versioned process patches (§7).

---

## 4. Definitions (the measurement contract)

- **Cell** = (task × treatment × candidate × repeat). One hermetic git worktree
  per cell at the task's pinned `baseline_sha`, one container, never shared.
- **Treatments** (structure axis):
  - `kitsoki` — the story pipeline driven headless via the studio MCP
    (`tools/mcp-drive/drive.sh`), per external-repo-bakeoff.
  - `single-naive` — one-shot agentic run: ticket text + repo, nothing else.
  - `single-briefed` — the **strong baseline**: same agentic loop given the same
    ticket, the same onboarding/context material kitsoki receives, and a
    competent generic instruction file. Headline comparisons use
    `single-briefed`; `single-naive` is reported as context. A reviewer must not
    be able to say "you beat a straw man."
- **Candidates** (model axis, from `external/candidates.yaml`): frontier
  (Opus-class / GPT-5.5), mid (Sonnet-class / GLM-5.2), small (Haiku-class).
  Pinned model IDs + effort per candidate; identical grid on every arm.
- **Outcome:** `quality ∈ {solved, partial, failed, pending}` per
  `results/SCHEMA.md`. `pending` = infrastructure/quota block (verified by
  direct probe), **never** counted as a capability result. Adjudication of
  impl-coupled oracles follows the bakeoff protocol: behaviour, not prose;
  raw `oracle_status` always preserved alongside.
- **Cost:** tokens are the provider-neutral **primary** axis; USD secondary.
  kitsoki arm: sum of authoritative `payload.meta.cost_usd` / `meta.usage` from
  the trace (`bench.py read_trace_metrics`). Agentic arm: `message.usage` ×
  `pricing.py`. Subscription-auth runs report tokens + `cost_usd: null`, priced
  via the table and flagged estimated. Price-table version stamped into every
  cell result.
- **Headline metric:** **cost-per-solve** = Σ cost of all attempted cells in the
  stratum ÷ number solved. This charges each arm for its misses — exactly the
  deck's "fewer misses" lever — instead of comparing only successful runs.
- **Secondary metrics:** wall time, guidance turns, compliance (5-heuristic mean),
  solve-rate, and the (solve-rate × cost) efficiency frontier per stratum.
- **Repeats:** headline cells run **n=3** (report median + range; LLM runs are
  nondeterministic and single runs are not evidence); exploratory grid cells n=1,
  flagged exploratory.

---

## 5. Phase A — Corpus construction via session mining

**Goal:** a frozen manifest of real tasks, grounded in what developers actually
do (mined from this repo's own history — our first OSS corpus), instantiated
against the 10-repo OSS corpus, each with a hidden deterministic oracle,
RED/GREEN-verified, split into training and held-out sets. **No LLM cost except
the one schema-validated mining pass (step B).**

### 5.1 Mine the archetypes (this repo = the seed corpus)

```
cd tools/session-mining
python3 prep.py ~/.claude/projects/-Users-brad-code-Kitsoki --job cost-bench-$(date +%Y%m%d)
# → Workflow(intents.workflow.js)  (the ONE LLM step, schema-constrained)
# → ground.py → tag_score.py → outcomes.py → emit.py → verify_link.py → validate_reports.py
```

Then rank mined intent clusters by **worth-benchmarking score**:
`frequency × median raw cost of the cluster's turns (cost_extract.py --grep)
× mechanical_fraction`. Select the top **4–6 archetypes**. Expected (to be
confirmed by the data, not assumed): bug-fix, failing-test repair, git-ops,
docs/README change, small feature-with-test, refactor-with-suite-green.

**Deliverable:** `tools/arena/corpus/archetypes.yaml` — per archetype: mined
evidence (instance ids, frequency, cost distribution), oracle template, and an
instantiation recipe. This file is what makes the task selection *evidence-based
rather than curated to flatter kitsoki* — the selection rule is committed before
tasks are chosen.

### 5.2 Instantiate against the 10-OSS corpus

For each of the 10 targets in `tools/product-journey/github-targets.json`:

- **bug-fix / failing-test archetypes:** external-repo-bakeoff fixture discovery —
  merged fix-PRs with isolated regression tests; pin `baseline_sha = fix_sha^`;
  hidden oracle = the PR's own regression test; verify RED@baseline,
  GREEN@fix before freeze (`bench.py verify`).
- **git-ops / docs / refactor archetypes:** instantiate from real repo history
  (a real merged docs PR, a real conflicted rebase reconstructed at pinned SHAs)
  with deterministic repo-state oracles (§5.3).
- **Feasibility triage (pre-registered, not silent):** the corpus stays fixed at
  10 repos, but archetype assignment respects hermetic-container reality — a
  repo whose oracle cannot run in a container in < 20 min (likely tensorflow,
  pytorch, rust full builds) gets the non-build archetypes (git-ops, docs)
  instead of bug-fix. The assignment matrix is committed in the manifest with
  reasons. No repo is dropped.

Target volume: **2 tasks per repo × 10 repos = 20 tasks** plus 4–6 kitsoki-repo
tasks from the mine itself, ≈ 24–26 total.

### 5.3 Oracles (the verdict function per archetype)

Research-grade rule: **headline claims ride deterministic oracles only.**

| Archetype | Oracle | Determinism |
|---|---|---|
| bug-fix | hidden regression test overlay, exit 0 = GREEN (`bench.py score`) | full |
| failing-test repair | pinned suite green at baseline, no test deletions (diff-checked) | full |
| git-ops | repo-state assertions: branch topology, merge parents, file content, clean tree | full |
| docs | deterministic checks (target file exists, required anchors/claims present, links resolve, builds) | full |
| refactor / small feature | suite green + hidden characterization test + behavioural diff checks | full |
| anything needing judgment | LLM-judge with frozen rubric + cassette | **excluded from headline**; reported separately as secondary |

Oracles follow the bakeoff brittleness rules (`bench.py lint_oracles`): assert
behaviour, never one implementation's error prose; never leak into any arm's
prompt or `gate_command`.

### 5.4 Freeze and split

Commit `tools/arena/corpus/cost-bench.manifest.yaml`: every task with
`{id, repo, archetype, baseline_sha, oracle, ticket, verified_red, verified_green}`.
Then split **stratified by archetype and repo**: ~65% training / ~35% held-out
(held-out ≈ 8–9 tasks, ≥1 per archetype). The held-out set is
**never run during training rounds** and its oracles are never read by anyone
patching the process. Split recorded in the manifest; frozen at M1.

---

## 6. Phase B — The harness (deterministic orchestration)

### 6.1 Orchestrator

`tools/arena` (landed on main) is the single front door. One JobSpec per round:

```yaml
# tools/arena/specs/cost-efficiency-round-N.yaml (sketch)
job_type: paired-task
corpus: tools/arena/corpus/cost-bench.manifest.yaml
split: training            # or: holdout (confirmation runs only)
process_version: v<N>      # the kitsoki story/prompt/routing bundle under test
treatments: [kitsoki, single-briefed, single-naive]
candidates: [frontier, mid, small]     # keys into external/candidates.yaml
repeats: {headline: 3, exploratory: 1}
placement: {hosts: [local, vm-1], concurrency: 4, retry: infra-only}
```

`arena plan` enumerates cells deterministically (no exec, no spend). `arena run`
defaults to the **no-LLM arming path** (oracle verify inside the container);
`--live` is the only way to spend and is always explicit.

### 6.2 The `paired-task` plugin (new)

- `image(cell)` — per-repo container images with pinned toolchains.
- `drive_command(cell, live)`:
  - `kitsoki` → `tools/mcp-drive/drive.sh` driving `stories/bench-bugfix` /
    `stories/task-bakeoff` with the process-version bundle; explicit `trace:`
    path (a missing trace is a known cost-loss failure mode).
  - `single-*` → headless `claude -p` / codex with the tier-appropriate context
    package; transcript retained for `cost_extract.py`.
  - Container gotchas already learned the hard way (from memory, encode in the
    plugin, don't rediscover): `IS_SANDBOX=1` for claude-as-root; codex env
    forwarding via `~/.codex/config.toml mcp_servers.*.env`;
    `--strict-mcp-config`; score CLI exits 0 on any completed grade.
- `score(cell)` — oracle overlay onto a copy of the tree; emits the
  `results/SCHEMA.md` cell JSON extended with `{treatment_pair_id,
  process_version, price_table_version, repeat_index}`.

### 6.3 Determinism & repeatability requirements (checklist)

- Pinned: `baseline_sha`, model IDs, effort, container image digests, price
  table version, prompt/story bundle version — all stamped into every cell JSON.
- Hermetic: one worktree + container per cell; a shared checkout is a protocol
  violation (bakeoff concurrency bug #9).
- Pre-flight before any spend: every task RED@baseline (a GREEN baseline is
  degenerate and proves nothing); provider probes so quota blocks become
  `pending`, not `failed`.
- Zero-re-spend reporting: `aggregate.py`-style rollup + report + deck
  regenerate offline from cell JSONs; re-analysis never re-runs models.
- No-LLM CI: the whole pipeline (enumerate → arm → score → aggregate → report)
  runs green on cassettes/fixtures in `make test` before the first live cell.
  Per AGENTS.md, automated tests never touch a real LLM.

---

## 7. Phase C — The round protocol (the training loop)

This is the "harness guides our model training" core. In kitsoki's thesis the
thing being trained is **the process** — stories, prompts, routing, gates, model
rungs — exactly the deck's `failure → improve → story` edge.

### Round N lifecycle

1. **Arm** (no-LLM): `arena run` arming pass; all training cells verified.
2. **Run** (live, gated): the full training-split matrix at `process_version vN`.
3. **Score & aggregate** (deterministic, no-LLM): rollup by treatment ×
   candidate × archetype; distributions from repeats.
4. **Analysis gate** against §2 criteria evaluated on the training split.
   - **Met** → proceed to the confirmation run (§7.3).
   - **Not met** → training pass (step 5). *This is the "we must continue" path.*
5. **Training pass** (versioned, disclosed):
   - **Failure mining:** every failed/partial kitsoki cell's trace is triaged
     into a failure taxonomy (routing miss, gate too loose/tight, prompt gap,
     context starvation, model under-rung, host bug). Deterministic script +
     human review; output is the round's **training packet**
     (`.artifacts/cost-efficiency/round-N/training-packet.md`).
   - **Patch rules (what MAY change):** story YAML, prompts, routing fixtures,
     gate predicates, escalation-ladder rungs, host-call ergonomics.
   - **Anti-overfit rules (what may NOT change):** tasks, oracles, the split,
     the metric definitions, anything referencing a specific task's content.
     Patches must be phrased generically (the dogfood-marathon discipline:
     harden for GENERAL use, never to the cases in the run). Every patch lands
     with a no-LLM regression flow so it can never silently rot.
   - **Ratchet:** before the next live round, re-run the no-LLM flow suite +
     arming pass; a patch that breaks a previously-passing flow is rejected.
   - **Baseline fairness control:** the agentic arm's brief gets an equal,
     time-boxed tuning budget each round (same person-hours, documented). The
     registered *expectation* (this is H3's real content) is that generic prompt
     tuning plateaus while process patches compound — but the baseline must be
     given the chance, or the trainability comparison is unfair.
   - Bump to `process_version v(N+1)`; commit; tag.
6. **Repeat** from step 1.

### 7.2 Stop rule (pre-registered)

Stop training when the **first** of these fires:
- criteria met on the training split → go to confirmation;
- **R_max = 5** rounds exhausted;
- improvement plateau: Δ(cost-per-solve) < 10% for 2 consecutive rounds.

Whichever fires is reported. All rounds appear in the final report — the
round-over-round curve is itself the H3 evidence, and hiding a bad round would
invalidate the study.

### 7.3 Confirmation run (the headline)

Freeze the final `process_version`. Run the **held-out split**, all treatments,
full candidate grid, n=3 — the first and only time held-out tasks are executed.
H1/H2 verdicts come from this run alone. H3's verdict = training-curve
improvement **and** held-out performance consistent with the training-split
gains (a big train/hold-out gap = overfitting, reported as an H3 failure).
H4 runs as a dedicated 5-op-session experiment on git-ops-archetype tasks, both
arms, measuring per-op cost as a function of op index — with **recorded** kitsoki
cassettes, closing the `record_mode: none` honesty gap flagged in
`docs/proposals/per-story-cost-tracking.md`.

---

## 8. Phase D — Reporting

- `docs/case-studies/cost-efficiency.md` — the narrative successor to
  `git-ops-cost.md`: method, all rounds, confirmation results, threats to
  validity, raw-data pointers.
- `docs/decks/cost-efficiency.slidey.json` — regenerated offline from cell
  JSONs (`eval_pilot_report.py --deck` idiom); feeds measured numbers back into
  the profit deck's scorecard scene (replacing its current qualitative cells).
- Every figure traceable: deck cell → rollup → cell JSONs → traces/transcripts.
- Mandatory sections: round ledger (every round, every number), pending/quota
  log, adjudication log (every oracle override + rationale), baseline-tuning
  effort log, price-table + model-pin versions.

### Threats to validity (register up front, answer in the report)

1. **Task selection bias** — tasks mined from *our* usage of *our* repo could
   favor kitsoki. Mitigations: committed selection rule before task choice;
   instantiation on 10 third-party repos; archetype/repo-stratified held-out set.
2. **Weak baseline** — mitigated by `single-briefed` (same ticket, same
   onboarding context, tuned each round under the fairness control).
3. **Oracle coupling** — behaviour-not-prose linting + adjudication protocol
   with preserved raw status.
4. **Nondeterminism of LLM runs** — n=3 repeats on headline cells; medians +
   ranges, never single points.
5. **Quota/infra conflation** — `pending` never counts as `failed`; probes
   distinguish; pacing per known provider ceilings (spark daily cap, GLM 5xx).
6. **Goodhart in training rounds** — hidden oracles never readable by patch
   authors; generic-patch rule; held-out confirmation.
7. **Price-table drift** — tokens primary; USD stamped with table version;
   subscription runs flagged estimated.
8. **Iterating-to-significance** — full round disclosure + held-out-only
   headline is the defense; the protocol makes "we continued until it worked"
   an *audited training claim*, not a hidden garden of forking paths.

---

## 9. Budget & spend envelope (gated, order-of-magnitude)

Live spend only via `arena run --live`, per-round sign-off, in this shape:

| Stage | Cells (≈) | Notes |
|---|---|---|
| Pilot shakeout | 12 | 3 tasks (query-string, already armed) × 2 treatments × 2 candidates × n=1 — proves the paired plugin end-to-end before the corpus exists |
| Training round (each) | ~100–160 live runs | ~16 training tasks × 3 treatments × headline candidates, n=3 on headline strata, n=1 exploratory |
| Confirmation | ~140–220 | 8–9 held-out tasks × 3 treatments × 3 candidates × n=3 |
| H4 session experiment | ~30 | 5-op sessions × 2 arms × 3 repeats |

Costs are dominated by the frontier-candidate agentic arm (that asymmetry is
itself the result). Use the escalation-ladder idiom to keep kitsoki-arm spend
at the cheapest viable rung. Every round's spend is reported next to its
results — the meta-number ("what did this study cost per arm") is publishable
evidence in its own right.

---

## 10. Milestones

| M | Deliverable | Gate |
|---|---|---|
| **M0** | This protocol committed; hypotheses/criteria frozen | review sign-off — **DONE, this document** |
| **M1** | Mining run complete; `archetypes.yaml` + frozen `cost-bench.manifest.yaml` (tasks verified RED/GREEN, split committed) | `bench.py verify` green on every task; no-LLM |
| **M2** | arena landed on main; `paired-task` plugin; no-LLM arming + scoring + rollup green in `make test` | zero live spend so far |
| **M3** | Pilot shakeout (12 cells) | paired cost + oracle verdicts end-to-end on real containers |
| **M4** | Training rounds 1..N (each: run → packet → patches → version bump) | stop rule §7.2 |
| **M5** | Confirmation run on held-out; case study + deck; profit-deck scorecard updated with measured numbers | all figures traceable |

Recommended first actions after M0: land arena from `.worktrees/arena` onto
current main (cherry-pick, not merge — main velocity), and start the M1 mining
job (cheap, one schema-validated LLM pass) in parallel. Tracked as
`WB.1` / `WB.2` in `docs/goals/generalized-usage/decomposition.yaml`.

---

## Appendix: expected-results anchor (what "does not produce expected results" means)

The registered expectations, so round-gate decisions are mechanical:
- E1 = H1 criterion (≥2× cost-per-solve efficiency at solve-rate parity).
- E2 = H2 criterion (kitsoki+mid ≥ single-briefed+frontier solve-rate).
- E3 = per-archetype: no archetype where kitsoki is *worse* on cost-per-solve
  (a single bad archetype triggers a targeted training pass, not corpus edits).
The observational ~175× from the git-ops case study is **not** the bar for the
controlled comparison — that number includes the in-session reprocessing tax,
which is measured separately as H4. The controlled same-task multiplier will be
smaller; 2× is the registered floor, and the honest number is whatever the
confirmation run measures.

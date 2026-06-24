# Case study: the bugfix bake-off — is structure worth more than a bigger model?

> **Status: methodology validated + first results; full grid pending.**
> The full 2×4×5 grid is **partial** — only `bug9`'s `single`(opus/sonnet)
> and `opus-4.8|kitsoki` cells have completed and been scored. GLM-5.2
> cells are blocked on a synthetic.new quota wall (HTTP 429); `bug12`/`bug14`
> and the sonnet/glm/gpt `kitsoki` cells are pending. Read this as a
> validated *method* plus a first worked example — **not** a finished sweep.
> The empty cells below are marked `pending`, not invented.

The two companion studies establish that the [`bugfix`](bug-fix.md)
pipeline *can* be deterministic and [what determinism costs](git-ops-cost.md).
This one asks the question those leave open: **does the structure actually
make the fixes better, and how does that trade against the model you point
at it?** We hold the bug set fixed and vary two axes — structure (the
kitsoki pipeline vs. a single multi-stage prompt) and model (GLM-5.2, Opus
4.8, Sonnet 4.6, GPT-5.5) — then grade every cell against a hidden oracle.

The honest early headline: **structure is not automatically cheaper**
(`opus-4.8|kitsoki` cost **$7.73** vs **$4.00** for the same model under a
single prompt) **but it is markedly more thorough** — it wrote its own
regression test, ran a refine loop, and parked safely at a red gate instead
of shipping. And in a parallel dogfood run the pipeline *caught* a
65-test-breaking fix that a naive single prompt shipped unverified. The
model axis matters as much as the structure axis.

---

## 1. Method

### The 2×4 factorial

For each of 5 bugs we run a **2 (treatment) × 4 (candidate)** grid — up to
**40 cells** — from the manifest
[`tools/bugfix-bakeoff/bakeoff.yaml`](../../tools/bugfix-bakeoff/bakeoff.yaml).

- **Structure axis (treatment).** `kitsoki` drives the
  [`bugfix`](../../stories/bugfix/) story (the seven-room
  reproduce→propose→implement→test→review→validate→done pipeline, with the
  candidate as the maker model); `single` gives the *same* candidate one
  multi-stage prompt plus up to **5 oracle-gated guidance turns**.
- **Model axis (candidate).** The same four candidates run under both
  treatments. The maker model is set by the session **profile**, not the
  story's agent-def:

  | key | profile | model | provider | single-prompt invoker |
  |---|---|---|---|---|
  | `glm-5.2` | synthetic-claude | hf:zai-org/GLM-5.2 | synthetic.new | `session` |
  | `opus-4.8` | claude-native | opus | anthropic | `claude -p` |
  | `sonnet-4.6` | claude-sonnet | sonnet | anthropic | `claude -p` |
  | `gpt-5.5` | codex-native | gpt-5.5 | openai-codex | `session` |

### Hermetic baselines, hidden oracle, adjudication

Each cell runs from a **hermetic baseline = the real fix's parent commit**
(`<fix_sha>^`), in its **own** disposable worktree (cells must never share
one — sharing a checkout *is* `bug9`). The baseline is pre-flighted to
confirm it is genuinely **RED** before any spend: several candidate bugs
were dropped because their `<fix>^` was already green (a test added on top
of an already-merged behavioural fix → a degenerate cell).

The grader's **hidden oracle is the fix's own regression test** — kept
*out* of the candidate's tree, copied in only at scoring time, run, removed.
The candidate must reproduce and fix the bug without ever seeing it.

Oracles are frequently **wording- or implementation-coupled**: they assert
a literal substring or symbol from the canonical fix, and so *false-fail* a
behaviourally-correct fix done a different way. The grade therefore has an
**adjudication step**: when the oracle fails, a judge decides
solved/partial/failed on **behaviour**, sets `adjudicated=true` with a
rationale, but the raw `oracle_status` is *always* preserved so the JSON
never lies about what the automated oracle did. All three completed cells
below are adjudicated solves over a false-failing oracle.

### One-basis cost

Cost is reported on **one consistent basis** (see
[git-ops-cost.md §4](git-ops-cost.md#4-method-and-the-synthetic-fallback)).
Kitsoki traces carry an authoritative per-call `payload.meta.cost_usd` —
summed directly. `claude -p` subscription transcripts carry **no** cost, so
they are priced from recorded `message.usage` through a corrected rate table
(Opus 4.8 = $5/$25 in/out, cache 0.5/6.25/10; Sonnet = $3/$15 — the old
15/75 Opus row was stale). The corrected table reproduces kitsoki's native
cost to ~0.4%. **Tokens are the provider-neutral primary axis; USD second.**
Cost is reported, never gated.

The full shapes are in
[`results/SCHEMA.md`](../../tools/bugfix-bakeoff/results/SCHEMA.md);
`quality ∈ {solved, partial, failed}` and a five-boolean compliance rate
(reproduced-red, own-regression-test, suite-green, in-scope, stage-order).

---

## 2. Results so far

All three completed cells are on **`bug9`** — the P1 "concurrent dogfood
sessions share one checkout, destroying WIP" bug
([ticket](../../issues/bugs/2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git.md),
fix `67ac5fb1`, baseline `ea2ca55a`). All three are **adjudicated solves**:
the oracle (`TestRepro_ConcurrentSessionsShareCheckout_DestroysWIP`) asserts
the *canonical* sentinel-refusal wording and false-fails every fix that took
a different valid route.

| cell | quality | oracle | guidance | cost (USD) | tokens | wall | what happened |
|---|---|---|---|---|---|---|---|
| `opus-4.8 \| single` | solved (adj.) | fail | 0 substantive¹ | **$4.00** | 3.5M | 299s | Behaviourally correct: refuses session B naming the owner, threads `session_id` through `host_dispatch.go` like the canonical fix. Oracle false-fails on wording. |
| `sonnet-4.6 \| single` | solved (adj.) | fail | 1 | **$3.02** | 3.2M | 288s | Turn-1 in-memory map didn't fence; **one behavioural-feedback turn** fixed it to a correct refusal. |
| `opus-4.8 \| kitsoki` | solved (adj.) | fail | 1 | **$7.73** | 4.0M | 1056s | Chose a **different valid fix** — a per-session worktree path (the ticket's *own* proposed fix), **wrote a regression test**, and **parked safely at `needs-human`** behind the RED-gate. |

¹ The opus/single cell's `guidance_turns` records 1 resume, but the fix was
behaviourally complete on the first pass; sonnet needed one genuine
behavioural-correction turn.

**Pending cells** (not run — do not read as zero): all of `bug12`/`bug14`;
all `glm-5.2|*` (blocked on synthetic.new 429); the `sonnet-4.6|kitsoki`,
`glm-5.2|kitsoki`, `gpt-5.5|kitsoki` cells; and `gpt-5.5|single`.

### Reading the three cells

- **Structure is not automatically cheaper.** `opus-4.8|kitsoki` ($7.73)
  cost ~1.9× the *same model* under a single prompt ($4.00), and took ~3.5×
  the wall time. The pipeline's overhead is real.
- **Structure is more thorough.** Only the `kitsoki` cell wrote its *own*
  regression test, ran a refine loop, and **parked at a red gate** rather
  than declaring victory — it never shipped an unverified fix. The two
  single-prompt cells solved the behaviour but added no regression test and
  left the suite red.
- **A cheaper model + one feedback turn matched the frontier model.**
  `sonnet-4.6|single` reached the same behavioural solve as `opus-4.8|single`
  for **$3.02** with one guidance turn — the model axis is at least as
  consequential as the structure axis on this bug.
- **The pipeline picked the ticket's own proposed fix.** Left to a single
  prompt, opus threaded `session_id` through dispatch; the pipeline
  independently arrived at per-session worktree paths — the resolution the
  ticket itself proposed.

A full per-treatment / per-candidate / per-cell rollup (and the markdown
grid) regenerates mechanically from the cells — see
[How to regenerate](#how-to-regenerate) — but with only one bug's three
cells scored, those averages are not yet meaningful and are deliberately
omitted here rather than presented as a sweep.

---

## 3. What the dogfood run surfaced

Building the `kitsoki` treatment was itself a [dogfood
marathon](../../.agents/skills/dogfood-marathon/SKILL.md): drive the real
bugfix story live over real cases and treat every friction as a
pipeline-improvement opportunity (hardened generally — **never** overfit to
the cases in the run). The findings:

- **F1 — blind implementer, now hardened (`d210ea67`).** The implementer
  was told *not* to run tests and submitted blind → a parallel run **shipped
  a 65-test-breaking fix**. The pipeline now makes the implementer
  self-verify (build + targeted + neighbour tests) before submit, the
  reproducer writes a RED-now test, the test-author marks `failed` on any CI
  failure (firing the refine loop), and the proposer prefers the smallest
  local fix. This is the headline: **the pipeline caught a bad fix a naive
  single prompt shipped.**
- **F2 — under-reported parking.** A `needs-human` park surfaces the
  regression-gate technicality ("never RED on the pre-fix snapshot") instead
  of the louder "your fix breaks N tests." The parking reason under-reports
  severity.
- **F3 — uncleaned worktree.** A `needs-human` park leaves the worktree +
  branch uncleaned, with no surfaced path / resume / cleanup hint.
- **P1 — missing trace (filed).** Live MCP sessions don't always leave a
  discoverable trace (`session_new` used `os.CreateTemp` instead of
  `store.DefaultTracePath`), so the scorer can't always find the transcript
  to extract cost from. The `trace_found` flag records this per cell; tracked
  at
  [`issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md`](../../issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md).

### What worked / what didn't

**Worked:** hermetic per-cell baselines + a hidden oracle gave honest,
self-report-proof grading; adjudication rescued behaviourally-correct fixes
the wording-coupled oracle would have failed; the one-basis cost method
reproduced native cost to ~0.4%; the refine loop and RED-gate parking did
their jobs (no unverified fix shipped from the pipeline).

**Didn't:** the GLM-5.2 quota wall blocked a whole column; wording-coupled
oracles meant *every* completed cell needed adjudication (author behavioural
oracles next time); kitsoki's structure cost more, not less, on the one bug
where a single prompt happened to land a correct fix unaided.

---

## 4. Reproducibility appendix

- **Framework.** Everything lives at
  [`tools/bugfix-bakeoff/`](../../tools/bugfix-bakeoff/) — manifest
  ([`bakeoff.yaml`](../../tools/bugfix-bakeoff/bakeoff.yaml)), per-cell
  prepare/run, scoring (`score.py`, with adjudication +
  committed-work-aware compliance + format-agnostic cost), aggregation
  (`aggregate.py`), the result contract
  ([`results/SCHEMA.md`](../../tools/bugfix-bakeoff/results/SCHEMA.md)), the
  narrative slidey deck
  ([`docs/decks/bugfix-bakeoff.slidey.json`](../decks/bugfix-bakeoff.slidey.json)
  — bake to a self-contained `.slidey.html` with `slidey bundle`), and
  a README runbook. Cost machinery is reused from
  [`tools/session-mining/`](../../tools/session-mining/).
- **The two reusable skills.** This run distilled into two general skills:
  [`matrix-task-comparison`](../../.agents/skills/matrix-task-comparison/SKILL.md)
  (run any harness×model / contender matrix over a task set, score, adjudicate,
  deck) and
  [`dogfood-marathon`](../../.agents/skills/dogfood-marathon/SKILL.md)
  (process a backlog live, harden the pipeline generally without overfitting).
- **Forthcoming stories.** `stories/task-bakeoff/` and
  `stories/dogfood-marathon/` will orchestrate these as kitsoki stories that
  emit a slidey report — planned, not yet built.
- **How to regenerate.**

  ```bash
  python3 tools/bugfix-bakeoff/aggregate.py    # cells/*.json -> results/summary.json (+ rollup)
  # offline data deck, zero re-spend:
  python3 tools/bugfix-bakeoff/aggregate.py --emit-agenteval
  python3 tools/session-mining/eval_pilot_report.py --markdown --deck
  ```

## See also

- [bug-fix.md](bug-fix.md) — the pipeline under test (the seven-room story,
  typed artifacts, oracle-as-test). This study does not re-explain it.
- [git-ops-cost.md](git-ops-cost.md) — the cost extractor and reprocessing-tax
  framing reused here for the token/cost columns.
- [Progressive determinism](../architecture/concept.md#4-progressive-determinism)
  — the principle the bake-off puts to an outcome test.

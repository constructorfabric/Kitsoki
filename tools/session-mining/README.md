# session-mining

Mine your Claude Code session transcripts for the developer workflows you repeat,
rank them by how worth-automating they are, and emit a **redacted, shareable
report** that aggregates with other people's. The point: discover which recurring
procedures are mostly-mechanical-with-a-few-judgment-calls — the sweet spot for
turning into deterministic scripts with named decision gates.

Tool-agnostic and dependency-light: `jq` + `python3` (stdlib only; the optional
intent-mining schema validator additionally wants `jsonschema`). No data leaves
your machine except a report you've explicitly scrubbed and gated.

> This kit is standalone. Its kitsoki-specific consumer (turning top patterns into
> kitsoki stories) is a thin downstream layer documented in
> [`../../docs/proposals/session-pattern-mining/`](../../docs/proposals/session-pattern-mining/).

---

## Three modes

The kit serves three distinct jobs over the same distilled traces:

| | **Pattern mining** (this README) | **Focused idea mining** | **Intent mining** |
|---|---|---|---|
| Question | "Which recurring workflows are worth scripting?" | "What have I said about **topic X**?" | "What did the user ask for, and what concrete actions would reproduce it?" |
| Output | A redacted, shareable, aggregatable `report.json` + `BRIEF.md` | A local, ranked, themed Markdown brief of ideas/pain/design notes | Two linked JSON reports: `intents.json` (catalog) + `analysis.json` (per-instance recipes) |
| Unit | a *workflow type* (aggregated) | a *theme* | an **intent instance** (preserved, never aggregated away) |
| Redaction | **Mandatory** — a model scores the traces, the report is shared | **None** — stays in `/tmp`, never shared; redaction would strip the content you want | **None by default** — local `.artifacts/`, opt-in `--redact` |
| Extractor | `prompts/extractor.md` (vocabulary-scored) | a fan-out workflow (one reader per batch) keyed to your topic | `intents.workflow.js` — one **strictly-validated agent** pass, re-checked deterministically |
| Driver | the Quickstart below | the **`session-idea-mining`** skill | the **["Intent mining" section](#intent-mining-the-third-mode)** below |

Both modes share `distill.jq` and the new **`prep.py`** (distill + bin-pack into
byte-balanced batches in one command — replaces the hand-rolled `for f in $(ls -S
…)` loop). Pattern mining runs `prep.py … --redact`; idea mining omits `--redact`.

A third, lighter consumer rides the same distillation: **`recap.sh`** — a
recency-first wrapper over `prep.py` that answers *"what have we been working on
lately?"* for the current repo. It resolves the repo's `~/.claude/projects/<slug>`
dir, distills the most recent N sessions (no redaction — local), and prints the
trace paths newest-first. It's driven by the **`session-recap`** agent
(`.claude/agents/session-recap.md`, pinned to Haiku) so the transcript volume
never lands in the calling session's context — the caller gets back only a short
recap. Use it for "catch me up" / "remind me where we left off", optionally
`--grep`-focused on a topic.

---

## Quickstart (pattern mining)

Fast path with `prep.py` (distill **and** redact **and** batch in one step):

```sh
cd tools/session-mining
python3 prep.py ~/.claude/projects/<your-project-dir> --out /tmp/sm --redact
# -> /tmp/sm/traces/*.txt (redacted), /tmp/sm/batches/batch-NN.txt, manifest.json
# then run the extractor (step 3 below) over the batches, and continue at step 4.
```

The longhand pipeline (equivalent, step by step):

```sh
cd tools/session-mining
PROJ=~/.claude/projects/<your-project-dir>          # one dir per repo you've used Claude Code in
mkdir -p /tmp/sm

# 1+2. distill each transcript to an action trace, then REDACT (mandatory)
for f in $(ls -S "$PROJ"/*.jsonl | head -12); do
  jq -r -f distill.jq "$f" | python3 redact.py > /tmp/sm/$(basename "$f" .jsonl).txt
done

# 3. extract: build the extractor prompt and run cheap subagents over the redacted
#    traces (1-2 traces per agent, fan out). See prompts/extractor.md for the prompt;
#    the agents emit one report.json per agent/run.

# 4+5. aggregate the per-agent reports into one, scored & gated
python3 aggregate.py /tmp/sm/report-*.json > combined.json

# 6. SHARE-BOUNDARY SCRUB — genericize file/path/name leakage in free-text fields.
#    Put any bare product/codenames (an app name used as a CLI subcommand, etc.)
#    one-per-line in names.txt; the path/file collapse handles the rest.
python3 redact.py --report --names names.txt < combined.json > report.json

# 7. share-gate — MUST pass before the report leaves your machine
python3 redact.py --scan < report.json && echo "safe to share"

# 8. turn the report into an actionable brief (what to build, which gates, first step)
python3 report.py report.json --top 5 > BRIEF.md
```

The raw transcripts and the distilled traces are the **private tier** — keep them
in `/tmp`, never commit, never share. Only the scrubbed, gated `report.json` (and
the `BRIEF.md` you derive from it) is shareable.

---

## Intent mining (the third mode)

Where pattern mining asks *which workflow types recur*, intent mining is
**instance-first**: it identifies each individual user request (an **intent**) and
recovers the concrete **actions + parameters** that would *deterministically*
reproduce the requested output. Where pure determinism is impossible it reduces
the LLM to a **strictly-validated agent** rather than letting it narrate freely.
It is local by design — outputs land in a gitignored `.artifacts/` job folder.

### The two linked reports

A run emits two reports into `.artifacts/session-mining/<job>/`:

- **`intents.json`** (REPORT 1, the catalog) — one row per intent: a stable
  `instance_id` (`<session-id>#<span-index>`), the **verbatim `user_text`**, the
  multi-dimensional `tags`, the `session`, the `span` (line range into the trace),
  and `analysis_ref` (`analysis.json#<instance_id>`). Plus deterministic
  `tags` rollups and `total_intents`.
- **`analysis.json`** (REPORT 2, the recipes) — one row per instance: `tags`, the
  `determinism` verdict, the grounded `actions` (each with `tool`, genericized
  `signature`, `parameters`, a `cite` into the trace, and a `grounded` flag),
  `agent_gates` (only when not fully deterministic), `measured` trace signals,
  and `grounding` stats. Plus `clusters` grouping like intents together.

**Cross-link contract.** Every `intents.json` row's `analysis_ref` is
`analysis.json#<instance_id>` and that `instance_id` exists in `analysis.json`
(and no analysis instance is an orphan). `verify_link.py <job-dir>` checks it and
exits non-zero on any violation.

**Schema validation.** `intents.json` and `analysis.json` have JSON Schemas under
`schema/{intents,analysis}.schema.json`. `validate_reports.py <job-dir>` validates
both against their schemas *and* re-checks the cross-link contract (which JSON
Schema alone can't express). This is the one optional dependency: it needs
`jsonschema` (`pip3 install --user jsonschema`) — the rest of the spine is
stdlib-only so it runs anywhere. The no-LLM test runs this validation on every run.

### LLM-minimality & the strict-agent guarantee

The pipeline is six steps; **only step B touches an LLM**, and its output is
schema-constrained *and* re-validated deterministically in step C:

| # | Step | Engine | Tool |
|---|---|---|---|
| A | Distill the corpus into action traces + a manifest | deterministic | `prep.py --job <name>` |
| B | **Agent pass** — segment each trace into intent spans; per span draft a recipe (ordered actions + genericized signature + parameters) with a **citation on every action**; assign tags; flag judgment gates | **LLM, strict schema** | `intents.workflow.js` |
| C | **Ground & validate** every action against the cited trace line; drop spans that ground nothing | deterministic | `ground.py` |
| D | **Tag & group** — validate tags against the vocab, roll up counts, cluster by tag-set + normalized signature | deterministic | `tag_score.py` |
| E | **Score determinism** per instance from measured trace signals + grounding completeness + gates | deterministic | `tag_score.py` |
| E′ | *(optional)* **Recover per-tool-call outcomes** from the raw `.jsonl` (`is_error`, stdout/stderr heads, interrupted) into a session-ordered intermediate | deterministic | `outcomes.py` |
| F | **Emit** the two linked reports; recover **verbatim user text from the raw `.jsonl`**; with `--outcomes`, attach each grounded action's real `outcome` + a per-instance `satisfaction` review flag | deterministic | `emit.py` |

The agent *proposes* a structured hypothesis; deterministic code *disposes* of it.
`ground.py` confirms (1) the cited line actually contains that tool call, and (2)
every emitted parameter value is a **substring** of the cited tool input — so an
action or parameter the trace doesn't support is rejected, not reported. The
substring check is strong for distinctive values (paths, flags, messages) and
weaker for short common ones (a one-word value can match incidentally); it is a
grounding gate against fabrication, not a proof of exact equality. This is what
makes the "deterministic recipe" claim trustworthy (review §3). The verbatim
`user_text` is recovered deterministically from the raw transcript, **never** taken
from the LLM.

The `determinism` verdict (step E) is computed, not guessed:
- **deterministic** — every action grounded, no judgment gate.
- **agent-gated** — reproducible except at N named gates, each carrying a strict
  `validator`, or where grounding is only partial (the LLM is allowed but boxed).
- **irreducible-llm** — nothing grounded / no concrete actions; flagged honestly
  rather than dressed up as a recipe.

### Taxonomy — tag-like, multi-attribute

An intent carries **one or more tags across a few controlled dimensions**, not a
single category — granularity comes from *combinations*, not a ballooning flat
list. The dimensions live in `vocab/tags.yaml`:

- `action` — *what* was asked. Reuses `vocab/core.yaml` ids verbatim (so intent
  mining and pattern mining share the same intent-shaped keys).
- `surface` — *what it touches*: code, test, docs, proposal, config, ui, story,
  schema, ci, infra.
- `scope` — blast radius (single-file / cross-module / repo-wide), optional.

Unknown tags are dropped with a stderr warning in step D — every tag must be in
the vocab and reusable across many intents.

### Running it

```sh
cd tools/session-mining
JOB=intents-$(date +%Y%m%d)
PROJ=~/.claude/projects/<your-project-dir>

# A. distill into the local .artifacts job folder (no --redact: this mode is local)
python3 prep.py "$PROJ" --job "$JOB"
#   -> .artifacts/session-mining/$JOB/{traces/,batches/,manifest.json}
#   stdout tail gives BATCHES= and BATCHDIR= for the workflow

# B. the one LLM step — run intents.workflow.js (schema-validated agent), e.g.
#    Workflow({ scriptPath: "tools/session-mining/intents.workflow.js", args: {
#      batchDir:   ".artifacts/session-mining/<JOB>/batches",
#      batchCount: <BATCHES from prep.py>,
#      outDir:     ".artifacts/session-mining/<JOB>/agent" } })
#   -> .artifacts/session-mining/$JOB/agent/agent-batch-NN.json

JOBDIR=../../.artifacts/session-mining/$JOB

# C. ground the agent hypothesis against the traces (rejects fabrications)
python3 ground.py --agent "$JOBDIR/agent" --traces "$JOBDIR/traces" --out "$JOBDIR/grounded.json"

# D+E. validate tags, cluster, score determinism (deterministic)
python3 tag_score.py --grounded "$JOBDIR/grounded.json" --traces "$JOBDIR/traces" --out "$JOBDIR/scored.json"

# E'. (optional) recover per-tool-call outcomes from the raw jsonl. Enables the
#     outcome-conformance + intent-satisfaction lenses (real result of each action,
#     and whether a follow-up turn corrected it). Omit to keep the legacy reports.
python3 outcomes.py --raw "$PROJ" --out "$JOBDIR/outcomes.json"

# F. emit the two linked reports; verbatim text comes from the RAW jsonl.
#    Pass --outcomes to attach each action's outcome + a per-instance satisfaction flag.
python3 emit.py --scored "$JOBDIR/scored.json" --traces "$JOBDIR/traces" \
    --raw "$PROJ" --outcomes "$JOBDIR/outcomes.json" --out-dir "$JOBDIR" --job "$JOB"

# verify the cross-link contract
python3 verify_link.py "$JOBDIR"

# validate both reports against their JSON Schemas + the cross-link contract.
# Needs jsonschema (`pip3 install --user jsonschema`); the spine above is stdlib-only.
python3 validate_reports.py "$JOBDIR"
```

### Testing (no LLM, ever)

Steps C–F are standalone python that take the agent's JSON as a file input, so the
whole C→F pipeline is unit-testable with **no LLM and no cost** (per AGENTS.md). The
fixture under `tests/fixtures/intent/` ships a sample distilled trace, a raw jsonl
snippet, and a sample agent output (including a deliberately-fabricated action that
the grounding gate must reject). Run:

```sh
python3 tools/session-mining/tests/test_intent_pipeline.py
```

It asserts the grounding gate (the fabricated span is quarantined and dropped), the
determinism verdicts, the measured trace signals, the verbatim recovery from raw
jsonl, and the cross-link contract. Step B (`intents.workflow.js`) is exercised only
at real runtime.

The optional outcome + satisfaction slice (E′ + `emit.py --outcomes`) has its own
no-LLM test over `tests/fixtures/intent_outcomes/`:

```sh
python3 tools/session-mining/tests/test_outcomes.py
```

It asserts outcome recovery (including an id-regime missing-result → `null`, no
positional cascade), the ordinal-alignment invariant against the emitted report, the
`satisfaction` review flag, back-compat (no new keys without `--outcomes`), and
schema conformance.

**Downstream consumer — story coverage mining.** The outcome + satisfaction lenses
exist to drive a kitsoki **story**'s tests and features from real transcripts:
`coverage_prep.py` + a per-story `mining.profile.yaml` turn a mined run into a
coverage worksheet. The loop is documented at
[`docs/stories/story-coverage-mining.md`](../../docs/stories/story-coverage-mining.md);
the worked flagship (committed corpus + `run.sh` demo + filled worksheet) is
[`examples/git-ops/`](examples/git-ops/), exercised by the no-LLM
`tests/test_git_ops_coverage.py`. Driven by the **`story-coverage-mining`** skill
(`.agents/skills/story-coverage-mining/`).

**Downstream consumer — cost tracking.** The same per-story
`mining.profile.yaml` also drives **cost savings**. `cost_report.py`
(`make cost-report`) pairs each story's deterministic cost (agent `cost_usd`
summed from its host cassette; routed/host steps are $0) against the real
raw-agentic cost of the same operations in mined sessions — a per-intent
median/p90 distribution, the reprocessing tax, and cold-resume re-warm — read
from recorded `message.usage` via `cost_extract.py` + `pricing.py` (exact, no
LLM). The narrative is the
[git-ops cost case study](../../docs/case-studies/git-ops-cost.md). `make
mining-test` runs every no-LLM invariant in this directory (the cost stack plus
the intent-pipeline, outcomes, and git-ops coverage suites); `make test` and CI
run it as a fourth suite, so these guards never rot.

---

## Concepts

**Pattern** — a recurring procedure: a mostly-mechanical skeleton plus a few real
decision points. `vocab/core.yaml` holds the controlled vocabulary of pattern ids
(the cross-user merge keys).

**Progressive determinism (the ladder)** — every pattern sits on a maturity ladder.
Mining tells you which rung a pattern is on and whether climbing is worth it.

| Stage | What runs the steps | What runs the decisions |
|---|---|---|
| **L0** ad hoc | human + model, freehand | every time, from scratch |
| **L1** documented | a checklist / skill | model, guided by prose |
| **L2** scripted skeleton | a deterministic script | model/human at *named* gates |
| **L3** defaulted | a deterministic script | a default rule on the common case; model/human only on low confidence |
| **L4** fully deterministic | a deterministic script | rules; no model needed |

You climb a rung by *recording* the decisions made at a gate: enough labelled
decisions let you fit a default and push L2→L3, then L3→L4. Mining picks the ladder
worth climbing; running the work produces the labels.

---

## The pipeline

```
*.jsonl  ──distill.jq──▶  trace.txt  ──redact.py──▶  trace.redacted.txt
   (private tier — never shared)                          │
                                          extractor (prompts/extractor.md, cheap model)
                                                           │  one report.json per agent
                                          aggregate.py  (merge + score + PROMOTION GATE)
                                                           │  combined.json
                                          redact.py --report  (share-boundary scrub)
                                                           │
                                          redact.py --scan    (share-gate; must pass)
                                                           │
                                  report.py ──▶ BRIEF.md  ·  share / re-aggregate
```

| Stage | Artifact | What it does |
|---|---|---|
| 1 Distill | `distill.jq` | 10+ MB JSONL → ~100 KB action trace (`USER:` / `AI:` / `> Tool: arg`). Deterministic, free. |
| 2 Redact | `redact.py` | Scrub home dirs, repo names, secrets, emails, URLs, tickets, IPs. Runs *before* any model sees the trace. Keeps path tails (private tier). |
| 3 Extract | `prompts/extractor.md` + `vocab/` | Cheap subagents score traces against the vocabulary and propose novel patterns; emit `report.json`. |
| 4–5 Aggregate & score | `aggregate.py` | Merge per-agent (and cross-user) reports; recompute `determinism_priority`; gate novel patterns. Associative (re-aggregatable). |
| 6 Scrub | `redact.py --report` | Genericize free-text (`example_signatures`, `decision_points`): path tails → `<path>`, filenames → `<file>`, `--names` words → `<name>`. The share boundary; does not trust the model to have genericized. |
| 7 Share-gate | `redact.py --scan` | Refuse to ship a report with any secret-shaped content. |
| 8 Brief | `report.py` | Render the aggregate into an actionable markdown brief: verdict + ladder move + gates + skeleton + first step per top candidate. |

---

## Safety model

Two tiers, one boundary:

| Tier | Contents | Leaves the machine? |
|---|---|---|
| Private | raw `*.jsonl`, distilled traces (path tails intact) | **Never** (`/tmp`, gitignored) |
| Shareable | `report.json` + `BRIEF.md` | Only after `redact.py --report` then `--scan` |

Four deliberate choices keep reports safe:
- **No verbatim quotes.** Prose can carry secrets, customer data, or proprietary
  code, and regex redaction of prose is never fully reliable. Examples are
  **genericized tool-call signatures** (`go test ./<pkg>/... -run <Test> → Edit
  <file> → rerun`) — real shape, zero content.
- **Don't trust the model to genericize.** The extractor is *told* to emit generic
  signatures, but doesn't do it reliably (it copies real paths/filenames). So the
  share-boundary scrub (`redact.py --report`, stage 6) deterministically collapses
  path tails → `<path>`, filenames → `<file>`, and `--names` words → `<name>` in
  every `example_signatures` / `decision_points` string. This is the guarantee, not
  the model's good behavior.
- **No identity.** Name, email, OS username, repo, host, ticket, URL, IP are all
  scrubbed; `contributor` is `null` or an opaque self-chosen salt.
- **Defense in depth.** Redact the trace (stage 2), scrub the report (stage 6),
  *and* gate it (stage 7).

`redact.py` is verified end-to-end on real transcripts: 0 identifying tokens
survive (home paths, repo names, emails, tickets, URLs scrubbed; `--report`
removes `pellicule`/`renderer.js`/`/tmp/<proj>-*`-style leaks from signatures),
`/dev/null` and other shell idioms are preserved, and `--scan` fails on planted
`ghp_…`/`AKIA…` tokens.

Residual risk: secrets with no recognizable shape, and **bare product/codenames in
prose** (e.g. an app name used as a CLI subcommand, `./cmd/myapp`) — the path/file
collapse won't catch those. The `--names` denylist is the last-mile control for
them; put your project/codenames in it. Glance at a report before your first share.

---

## Vocabulary & overlays

- `vocab/core.yaml` — language-/tool-agnostic pattern ids. These are the merge
  keys; their *meaning* is stable. Adding an id or changing a meaning bumps
  `vocab_version`.
- `vocab/overlay-<lang>.yaml` — ecosystem-specific *example signatures* attached to
  core ids. Overlays never add ids. Ships with `overlay-go.yaml`; copy it for your
  stack (`overlay-js.yaml`, `overlay-python.yaml`, …). Using the right overlay keeps
  irrelevant ecosystem patterns out of your `novel` bucket.

---

## Aggregation & the promotion gate (the noise control)

`aggregate.py` merges reports by pattern `id`:
- `occurrences`, `sessions_seen` → **sum**
- `mechanical_fraction` → **mean weighted by occurrences**
- `pain` → **max** (conservative)
- `decision_points`, `example_signatures` → **union** (deduped)
- `pain` synonyms (`"medium"`, `"severe"`, …) are normalized; an unrecognized
  value is treated as `low` **and warned about** on stderr, never silently bucketed
- `contributors` → **count of distinct reports** reporting the id — the real
  cross-user commonality signal
- `determinism_priority` = `commonality × mechanical × pain_weight`, recomputed

Novel (free-form) patterns are **not** trusted as merge keys. They're clustered by
a normalized key and held behind a gate: a cluster is surfaced as a
`novel_promotion_candidate` only once **≥ `PROMOTE_MIN_CONTRIBUTORS` (default 2)**
distinct contributors independently report it; otherwise it sits in
`novel_quarantine` and contributes nothing to the counts. So an open-coding pass
can fill the quarantine but **cannot inflate the shared numbers**.

Worked example — `examples/merge/a.json` (Go) + `b.json` (Python):
```sh
python3 aggregate.py examples/merge/a.json examples/merge/b.json
```
`fix-failing-tests` merges across both contributors (weighted-mean mechanical, max
pain); the novel `ci-log-triage` reported by *both* is promoted; the singletons
`warp-debug-flag` and `notebook-cell-iteration` are quarantined. Pre-computed at
`examples/merge/merged.json`.

**Re-aggregation is safe (the merge is associative).** An aggregated report is
itself a valid input to `aggregate.py`, so you can merge in waves —
`aggregate(aggregate(a, b), c)` gives the same counts, priorities, and gate
decisions as `aggregate(a, b, c)`. Each report carries its own weight
(`reports_merged`) and per-pattern `contributors` count, and re-aggregation pulls
an input's quarantine forward — so a novel pattern that was one contributor short
of promotion can still promote in a later round instead of being dropped. This is
what makes "everyone shares a report, someone merges them later, then merges the
next batch into that" actually sound. The merged shape has its own schema,
`schema/aggregate.schema.json` (the single-contributor input shape is
`schema/report.schema.json`).

---

## From report to action

The aggregate JSON is a ranked *diagnostic*. `report.py` turns it into a
*prescriptive* brief so the ranking drives decisions:

```sh
python3 report.py report.json --top 5 > BRIEF.md
```

For each top candidate the brief states:

- **Verdict** — `BUILD NOW` (high priority, corroborated by ≥2 contributors, a
  crisp gate set), `BUILD (judgment-heavy)` (worth it but keep a human/model at the
  gates), `PROMISING` (high priority, only one contributor — needs corroboration),
  `ALREADY MOSTLY SOLVED`, or `LATER`. Thresholds are explicit in `report.py` so
  two runs are comparable.
- **The move** — the ladder step (e.g. `L1 → L3`), pulled from the pattern's
  `default_ladder_target` in `vocab/core.yaml`.
- **Gates to install** — the `decision_points`. These *are* the actionable kernel:
  each becomes a named decision point with a default/LLM/human decider. (The
  extractor caps these at ~3 abstract forks; the union across contributors can
  still exceed that, so the brief flags "consolidate into ≤3 real gates".)
- **Skeleton to script** — the genericized `example_signatures` (the mechanical
  part to automate).
- **First step** — script the skeleton, wrap each gate, record every decision so
  the gate can climb the ladder.

It also lists newly-corroborated novel patterns ("promote into the vocabulary")
and the watch list ("needs N more contributors"). The full ranking table is always
included, so nothing below the cut line is hidden.

---

## Does it find novel patterns, or only expected ones?

Both — but the *gate* is what makes that safe, not the extractor. The default
extractor is seeded (scores the vocabulary) plus a "propose novel" rider; in the
reference run that rider independently surfaced `fan-out-agents-and-reconcile` from
4 of 6 agents, which is now promoted into `core.yaml`. A full open-coding pass (no
seed) discovers more but adds noise — and noise is exactly what the promotion gate
absorbs without polluting counts. **Treat full open-coding as an experiment:** run
it alongside the seeded pass on the same traces and compare only the *promoted*
sets. If it promotes patterns the seeded pass missed, keep it; if it only grows the
quarantine, drop it.

---

## Reproducibility & versioning

A report is comparable to another iff they share `prompt_version` **and**
`vocab_version`. Both are stamped into every report. `aggregate.py` warns when it
merges mixed `vocab_version`s. The extractor prompt is a versioned artifact
(`prompts/extractor.md`), not an ad-hoc message — that's what makes runs repeatable.

---

## Files

```
distill.jq              raw JSONL transcript -> compact action trace
prep.py                 distill + (optional --redact) + bin-pack into byte-balanced batches; one command, all modes (--job targets .artifacts/ for intent mining). Drops dispatched headless agent/agent transcripts (entrypoint!=cli) by default; --keep-agent-sessions to include them
redact.py               deterministic scrubber; `--report` scrubs a report's free-text, `--scan` is the share-gate
report.py               render a pattern-mining aggregate into an actionable BRIEF.md (verdict + gates + skeleton + first step)
focus_brief.py          render a focused idea-mining synthesis JSON into a ranked themed Markdown brief (idea-mining mode)
prompts/extractor.md    the versioned extractor prompt (the reproducible core)
vocab/core.yaml         controlled vocabulary (cross-user merge keys)
vocab/overlay-go.yaml   Go/backend example signatures (copy per ecosystem)
vocab/tags.yaml         INTENT MINING tag taxonomy (action / surface / scope dimensions)
schema/report.schema.json     JSON Schema for one contributor's report (aggregate.py input)
schema/aggregate.schema.json  JSON Schema for a merged report (aggregate.py output; re-aggregatable)
schema/intents.schema.json    JSON Schema for REPORT 1 (the intents catalog)
schema/analysis.schema.json   JSON Schema for REPORT 2 (the per-instance recipes)
aggregate.py            merge + score + promotion gate (stdlib only; associative)
intents.workflow.js     INTENT MINING step B — the one strictly-validated agent pass (schema-constrained)
intent_common.py        shared helpers for the intent-mining spine (trace/vocab/io primitives)
ground.py               INTENT MINING step C — ground & validate agent output against the traces
tag_score.py            INTENT MINING steps D+E — tag/group + determinism scoring (deterministic)
outcomes.py             INTENT MINING step E′ (optional) — recover per-tool-call outcomes (is_error/stdout/stderr/interrupted) from raw jsonl into a session-ordered intermediate
emit.py                 INTENT MINING step F — emit the two linked reports; verbatim text from raw jsonl; --outcomes attaches per-action outcome + per-instance satisfaction
verify_link.py          check the intents.json <-> analysis.json cross-link contract
validate_reports.py     validate both reports against their JSON Schemas (needs `jsonschema`)
coverage_prep.py        STORY COVERAGE MINING data-prep — scope-filter + arg-aware dedup + candidate-room join + outcome/satisfaction inlining over a story's mining.profile.yaml; emits intents.git.json + a coverage.md worksheet skeleton (NO verdicts). See docs/stories/story-coverage-mining.md
tests/                  fixture + no-LLM end-to-end tests (intent C->F pipeline; outcome+satisfaction; the git-ops coverage flagship)
examples/report.example.json   a real redacted report (reference run)
examples/merge/         two reports + their merged output (worked aggregation)
examples/git-ops/       STORY COVERAGE MINING flagship — committed corpus + run.sh demo + worked coverage.worked.md (the worked answer to "how does coverage mining work?")
```

## Limitations

- Reference numbers come from 12 biggest-by-size sessions — biased toward long
  authoring epics. Sample by recency and activity type too before trusting counts.
- `mechanical_fraction` / `pain` are model estimates, not measurements.
- Redaction is best-effort on shape; the signatures-not-quotes rule is the real
  guarantee.

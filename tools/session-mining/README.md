# session-mining

Mine your Claude Code session transcripts for the developer workflows you repeat,
rank them by how worth-automating they are, and emit a **redacted, shareable
report** that aggregates with other people's. The point: discover which recurring
procedures are mostly-mechanical-with-a-few-judgment-calls — the sweet spot for
turning into deterministic scripts with named decision gates.

Tool-agnostic and dependency-light: `jq` + `python3` (stdlib only). No data leaves
your machine except a report you've explicitly scrubbed and gated.

> This kit is standalone. Its kitsoki-specific consumer (turning top patterns into
> kitsoki stories) is a thin downstream layer documented in
> [`../../docs/proposals/session-pattern-mining/`](../../docs/proposals/session-pattern-mining/).

---

## Quickstart

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
redact.py               deterministic scrubber; `--report` scrubs a report's free-text, `--scan` is the share-gate
report.py               render an aggregate into an actionable BRIEF.md (verdict + gates + skeleton + first step)
prompts/extractor.md    the versioned extractor prompt (the reproducible core)
vocab/core.yaml         controlled vocabulary (cross-user merge keys)
vocab/overlay-go.yaml   Go/backend example signatures (copy per ecosystem)
schema/report.schema.json     JSON Schema for one contributor's report (aggregate.py input)
schema/aggregate.schema.json  JSON Schema for a merged report (aggregate.py output; re-aggregatable)
aggregate.py            merge + score + promotion gate (stdlib only; associative)
examples/report.example.json   a real redacted report (reference run)
examples/merge/         two reports + their merged output (worked aggregation)
```

## Limitations

- Reference numbers come from 12 biggest-by-size sessions — biased toward long
  authoring epics. Sample by recency and activity type too before trusting counts.
- `mechanical_fraction` / `pain` are model estimates, not measurements.
- Redaction is best-effort on shape; the signatures-not-quotes rule is the real
  guarantee.

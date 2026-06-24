# Case study: what does the git-ops demo *cost*?

This is the companion to [bug-fix.md](bug-fix.md). Where bug-fix shows
*how* a prompt-driven loop becomes a deterministic pipeline, this one
puts a price tag on the difference — using **real telemetry from real
Claude Code sessions**, not a model.

The git-ops story runs four operations — commit, rebase-with-conflict,
merge, worktree setup — for a committed **$0.0955** (two agent calls;
everything else deterministic and free). The question is what the same
work costs in a raw agentic loop. The answer turns on one mechanism, and
it is the whole case study:

> **In an agentic loop, the cost of an action isn't the action — it's
> reprocessing the entire conversation to reach it.** To take the next
> step, the model re-reads everything before it. So the *same* `git
> commit` is cheap as the 2nd action and expensive as the 30th, and total
> cost grows super-linearly with session length. Kitsoki's deterministic
> engine never feeds a conversation back through a model to decide the
> next step, so that tax is simply absent.

Everything here is reproducible from telemetry already on disk, no LLM
spend: [`tools/session-mining/cost_extract.py`](../../tools/session-mining/cost_extract.py)
reads the `message.usage` recorded on every assistant message and prices
it via the shared table [`pricing.py`](../../tools/session-mining/pricing.py).

---

## 1. The reprocessing tax, measured

Real Claude Code transcripts record, per assistant message, exactly how
many tokens were *fresh input* versus *cache-read of prior context*. That
cache-read figure **is** the reprocessing tax: tokens the model re-read,
and you paid for, purely to carry the existing conversation forward one
more step.

Across real interactive sessions in this repo, that tax is overwhelming:

| real session (interactive) | turns | API calls | total cost | reprocessing tax |
|---|---|---|---|---|
| building the git-ops demo¹ | 17 | 924 | **$546.13** | **98%** (227M tok re-read) |
| a PRD/commit/push session² | 10 | 322 | **$99.26** | **99%** (43.6M tok re-read) |

¹ `3404d3de…` — the *actual session this very demo was built in*.
² `5085ba39…`. Both Opus-4.8, priced exactly from recorded usage.

98–99% of all input tokens were reprocessing. The model spent almost none
of its input budget on new information — it spent it re-reading the
conversation. That is the cost an agentic loop pays to stay coherent
across turns, and it is the cost Kitsoki does not pay.

### The climb

Because each turn re-reads everything before it, per-action cost rises as
the session grows — independent of how hard the action is. From the
demo-building session (`cost_extract.py … --by-turn`):

```
     cost  cumulative  reproc-tok  calls | action
    $8.60       $8.60     654,620     16 | (early) set up the session-mining job
   $81.10     $237.90  28,183,994    198 | demonstrate + validate with the git-ops story
  $147.22     $385.12  69,221,622    208 | make a tour-driven demo video
    $1.71     $546.13   1,117,036      2 | what branch?
```

The last action is *"what branch?"* — a one-word question. It still cost
**$1.71** and re-read **1.1M tokens**, because answering it meant
reprocessing the entire 17-turn conversation. In the git-ops story,
"what branch am I on" is a deterministic host call: **$0**, the same
whether it's the 1st action or the 100th.

There is a clean floor, too: a genuinely *isolated* `commit this` in a
fresh session (`fa1c688c…`, 2 calls) costs **$0.12** — already more than
the git-ops story's entire four-operation run. But the floor understates
reality. Developers don't commit in a vacuum; they commit on turn 20 of a
working session, and pay the turn-20 reprocessing price to do it.

### And it's worse cold: coming back after a break (measured)

The climb above assumes the cache is **warm**. Claude Code's prompt cache
has a **1-hour TTL**. Step away longer — lunch, a meeting, the next
morning — and it's gone: the first turn back must re-*write* the whole
conversation prefix cold (at the cache-write rate, ~20× the cache-read
rate) before any work happens.

This is **directly measurable**, not modelled. Resuming a session appends
to the same transcript file (cross-file continuation links are vanishingly
rare — 1 in 207 sessions here), so a break is a large time gap between
consecutive records, and the cold re-write is a `cache_creation` spike on
the next turn. `cost_extract.py` detects gaps past the TTL and prices that
re-write. Real examples from this repo's sessions:

These are genuinely *discrete* actions — the kind where dragging the whole
prior conversation along is pure overhead, not the point (a resume whose
intent *is* to reprocess, like "continue", isn't a fair example):

| break | resumed with | cold re-warm (measured) | vs warm |
|---|---|---|---|
| **20.2 h** (`a650171f`) | *"just commit on main"* | **$4.22** | 20× ($0.21) |
| 19.0 h (`54b43d51`) | *"fix the PDFs on webview"* | $11.26 | 20× |
| 19.9 h (`64672ba6`) | *"is it well documented?"* | $1.71 | 20× |

You came back the next morning, typed *"just commit on main"*, and paid
**$4.22 to re-warm the conversation before git even ran** — re-writing
140,623 tokens cold. In the git-ops story, "commit on main" is a
deterministic git operation: **$0** to resume, warm or cold, because there
is no conversation cache to expire — nothing is fed back through a model.
The cold-resume penalty isn't a tail risk; it's what you pay every time
you return to a long-running session to do one discrete thing.

---

## 2. Why the deterministic story has no tax

The git-ops story's spend is committed ground truth, read straight from
the demo's host cassette
[`demo_agent.cassette.yaml`](../../stories/git-ops/flows/cassettes/demo_agent.cassette.yaml):

| paid surface | cost |
|---|---|
| `host.agent.decide` — draft the commit message | $0.0121 |
| `host.agent.task` — resolve the two-file conflict | $0.0834 |
| everything else — routing, branch detection, every git command, the whole worktree lifecycle | **$0.0000** |
| **total (4 operations)** | **$0.0955** |

Two structural reasons this carries no reprocessing tax:

1. **Deterministic steps never call a model.** Routing each typed
   utterance to an intent, branch detection, staging, every merge guard,
   the worktree lifecycle — these are state-machine transitions and real
   `git`. There is no conversation to re-read because there is no model in
   the loop for them.
2. **The two agent calls get *focused* context, not the transcript.**
   When a model *is* needed — authoring a commit message, resolving a
   conflict — it receives the specific artifact (the staged diff; the two
   conflicted files), not the accumulated session. So even the paid
   surfaces don't pay the tax.

This is why the story's cost is **flat in session length**: the 100th
operation costs the same as the 1st, because nothing accumulates a
conversation to reprocess. The raw loop's cost is a function of how long
you've been talking; the story's isn't. That is the differentiator the
price tag measures — [progressive determinism](../architecture/concept.md#4-progressive-determinism)
turns the dominant cost term to zero.

---

## 3. The comparison

| | the work | cost |
|---|---|---|
| git-ops **story** | 4 operations, any session length, warm or cold | **$0.0955**, flat |
| Claude Code, **isolated floor** | one `commit this`, fresh session | ~$0.12 |
| Claude Code, **realistic** | git ops entangled in a working session | $1–$20+ **per turn**, climbing |
| Claude Code, **cold resume** | one discrete action after a >1h break | the re-warm alone, measured: $1.71–$11.26 before any work |
| Claude Code, **a full session** | the demo-building session itself | **$546.13** |

The story did the four operations once for less than a single isolated
commit costs in a raw loop — and the gap only widens the longer the
session, because the tax compounds. The deterministic engine scales for
free; you pay for judgement (the two agent calls), not for replaying the
conversation.

---

## 4. Method, and the synthetic fallback

**Real path (this case study).** `cost_extract.py` reads recorded
`message.usage` — `input_tokens`, `output_tokens`,
`cache_read_input_tokens`, `cache_creation_input_tokens` (with the 5m/1h
split) and `model` — and computes exact cost via `pricing.py`. Nothing is
modelled: the API already split input into fresh / cache-write /
cache-read buckets, so cost is a dot product of recorded counts with
published rates. (`costUSD` is null under a subscription, which is why we
compute from token counts; it is authoritative when present.) Mining runs
now capture this automatically — [`prep.py`](../../tools/session-mining/prep.py)
writes a `costs.json` sidecar per run from each source transcript before
distillation drops the telemetry, so *every* mined corpus carries the
real dollars it cost to produce.

**Synthetic fallback.** The committed git-ops demo corpus
([`examples/git-ops/raw/`](../../tools/session-mining/examples/git-ops/raw/))
is hand-authored stubs with no telemetry, so it can't be read this way.
For that case only, [`cost_estimate.py`](../../tools/session-mining/cost_estimate.py)
*models* the same re-send-everything mechanism (with explicit knobs for
base prompt size, prior-context, and a warm/cold cache band) and produces
a range. It is the fallback, documented as such; the real extractor is
the authority wherever telemetry exists.

```bash
# the reprocessing tax + the climb, from real telemetry (no LLM)
python3 tools/session-mining/cost_extract.py ~/.claude/projects/<proj>/SESSION.jsonl --by-turn

# find + cost every turn that ran a given command across many sessions
python3 tools/session-mining/cost_extract.py ~/.claude/projects/<proj>/*.jsonl --grep 'git rebase'

# invariants tests, no LLM, no network
python3 tools/session-mining/tests/test_cost_extract.py
python3 tools/session-mining/tests/test_cost_estimate.py
```

### Threats to validity

* **The tax is real, the dollars depend on a price table.** Token counts
  and the cache split are recorded (exact); only the per-token *rate* is a
  constant we supply (`pricing.py`, Sonnet/Opus/Haiku list price, 2026-06).
  Unknown models are flagged, not silently priced.
* **Attribution is per user-turn.** A turn that does more than one thing
  attributes all its cost to that turn — but that's faithful: you *did*
  pay to reprocess the whole conversation for that turn regardless of how
  many sub-tasks it contained.
* **Cold resumes are measured where a break exists, counterfactual
  otherwise.** When a transcript contains a real >1h gap, the cold re-write
  is read straight from the resume turn's `cache_creation` (exact, e.g. the
  $4.22 above). Only when a transcript has no such break does the tool fall
  back to a clearly-labelled rate counterfactual (cache-write-1h ÷
  cache-read ≈ 20×) on a small action's measured prefix. Either way it is a
  per-resume figure, not a session-wide multiplier — consecutive turns
  within the hour stay warm.
* **Everything uncertain pushes the raw number up.** Real sessions have
  retries, larger diffs, and longer system prompts; the deterministic
  story's $0.0955 is the committed measured value. The honest claim is the
  conservative one: the gap is *at least* this large.

---

## Per-story cost tracking — `make cost-report`

This case study is the hand-written *narrative*; the **numbers it argues
are now generated per story, automatically**, by
[`cost_report.py`](../../tools/session-mining/cost_report.py)
(`make cost-report`). For every story that ships a `mining.profile.yaml` it
pairs two figures and reports the gap:

- **numerator — the deterministic story cost.** Read straight from the
  story's host cassette(s): every routed/host step is $0 by construction,
  so the agent `cost_usd` *is* the story's cost. Programmatic, not
  transcribed by hand (this section used to copy git-ops's $0.0121/$0.0834
  into prose; the tool now sums them from the cassette).
- **denominator — the raw agentic cost.** The same operations in real
  Claude Code sessions, scoped by the story's `mining.profile.yaml` grep
  (the same prefilter mining uses; dispatched agent/agent sessions
  dropped), found per user-turn by `cost_extract.py`, and reported as a
  **distribution** (median / p90 per intent), with the reprocessing tax and
  cold-resume re-warm those turns paid — not one curated example.

```bash
make cost-report                       # all stories -> .artifacts/cost-report/cost-report.md
python3 tools/session-mining/cost_report.py --story git-ops   # one story, to stdout
make mining-test                       # the no-LLM invariants for the whole stack (also run by `make test`)
```

For git-ops today that yields the story's **$0.0955** against a real
median of **~$17 per equivalent operation** across dozens of mined
sessions (≈175×), and surfaces the **model-mix lever** the deterministic
boundary unlocks: the raw operations ran on Opus, while the story's two
agent calls need only Sonnet (~5× cheaper per token). Stories without a
recorded agent cassette yet show a real raw baseline and a *"not yet
measured"* numerator — which is the honest state until they record one.

The throughline: **session mining is the cost denominator for the whole
project.** The same corpus that gives coverage and intents gives "what
would this have cost as an agent loop?" — so every new story gets a
cost-savings number for free.

**What's still hand-authored (the remaining honesty gap).** git-ops's two
agent costs live in a `record_mode: none` cassette — *authored*, not
recorded. `cost_report.py` flags them (⚠ authored), but closing the gap
means recording real agent cassettes (LLM spend, gated) so the numerator
is measured end-to-end. That, plus a couple of methodology refinements, is
tracked in
[`docs/proposals/per-story-cost-tracking.md`](../proposals/per-story-cost-tracking.md).

## See also

- [Token usage comparison](../competitive-analysis/token-usage/) — the
  broader framing this case study instantiates for one workflow.
- [bug-fix case study](bug-fix.md) — the *how* behind the *how much*.
- [git-ops story](../stories/git-ops.md) and
  [story-coverage-mining](../stories/story-coverage-mining.md) — where the
  four operations come from.
- [Progressive determinism](../architecture/concept.md#4-progressive-determinism)
  — the principle the price tag measures.

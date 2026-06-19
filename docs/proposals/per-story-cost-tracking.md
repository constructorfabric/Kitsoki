# Per-story cost tracking — roadmap

**Goal.** Every Kitsoki story should publish the cost savings it delivers
versus doing the same work as a raw Claude Code agentic loop. The git-ops
[case study](../case-studies/git-ops-cost.md) is the hand-built proof; this
turns it into an automatic, per-story capability. **Session mining is the cost
denominator for the whole project** — the same mined corpus that gives us
coverage and intents gives us "what would this have cost as an agent loop?"

## Where we are

Shipped (commits on `feat/story-conformance-mining`):

- `tools/session-mining/pricing.py` — authoritative per-model price table; exact
  `message_cost()` incl. 5m/1h cache split; flags unpriced models.
- `tools/session-mining/cost_extract.py` — REAL cost from recorded `message.usage`,
  attributed per user-turn; reprocessing-tax %, per-action climb, and **measured
  cold resumes** (within-file time-gap detection → cold prefix re-write cost).
- `tools/session-mining/prep.py` — every mining run now writes a `costs.json`
  sidecar + manifest `real_cost` block from each source transcript.
- `tools/session-mining/cost_estimate.py` — synthetic-corpus fallback (modeled),
  reconciled onto the shared price table.
- Tests: `test_cost_extract.py`, `test_cost_estimate.py` (no LLM).

## The gaps to close (priority order)

### 1. Measure the deterministic (numerator) side live — BIGGEST HONESTY GAP
The git-ops demo's oracle costs ($0.0121 decide, $0.0834 task) were **authored by
hand** in `demo_oracle.cassette.yaml`. To claim savings honestly the story side
must be *measured*:
- Kitsoki already emits `oracle.call.complete` with real `cost_usd`/usage
  (`internal/host/oracle_event_sink.go`, `oracle_runner.go`). Capture these into a
  per-story cost ledger from real (non-cassette) runs.
- Record real oracle cassettes (record mode) so the committed demo numbers are
  genuine recorded costs, not estimates. Until then, the case study should keep
  flagging these two numbers as hand-authored (it does).

### 2. Per-story baseline from the mined corpus
Each story has `stories/<name>/mining.profile.yaml` + a mined session corpus.
- A driver that, per story, runs `prep.py` over its corpus → `costs.json`, then
  aggregates the real raw-agentic cost (total, reprocessing tax, cold-resume
  re-warm) as the denominator.
- Decide corpus scope: the story's own mined examples vs a broader real-session
  pull grepped by the story's domain vocabulary (the latter is more honest/real
  but noisier — entanglement is the point, per the case study).

### 3. Per-intent matching (apples-to-apples)
Session mining already classifies operations into an intent taxonomy
(`tools/session-mining/` intents pipeline). Align mined operations to the story's
intents so savings are computed per intent (commit vs rebase vs merge), not just
per session. This makes the savings table mirror the story's actual surface.

### 4. Distribution, not anecdote
Across the corpus, report median / p90 raw cost per operation (and per cold
resume) so each story's savings carry error bars. `cost_extract.py --grep <op>`
already finds all instances; add aggregation.

### 5. One report across all stories
`make cost-report` → per story: {measured story cost, mined raw baseline,
savings, reprocessing-tax saved, cold-resume saved}. Emit a markdown table — the
reusable form of the case study. Also surface the **model-mix lever**: real
sessions run on Opus, while the narrow oracle tasks need only Sonnet; the
deterministic boundary is what lets a cheaper model suffice. Quantify it.

### 6. Smaller methodology improvements
- Tokenizer accuracy for the synthetic estimator fallback (currently chars/token
  heuristic; only matters for telemetry-free corpora).
- Cross-file continuation: rare (1/207) but real; if `--resume` usage rises, chain
  transcripts via the first-message parent/leaf links for end-to-end session cost.
- Price-table freshness: dated constants in `pricing.py`; consider a check.

## Definition of done
A new story that ships a `mining.profile.yaml` automatically gets: a real
raw-agentic baseline, a measured deterministic cost, and a per-intent savings
table with a distribution — no hand assembly. The git-ops case study becomes the
narrative companion to a number every story carries.

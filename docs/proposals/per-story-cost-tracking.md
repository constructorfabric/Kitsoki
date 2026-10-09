# Per-story cost tracking — remaining work

**Status:** Partially shipped. `cost_report.py`/`pricing.py`/`cost_extract.py`
are live (`make cost-report`); recording a real (non-authored) agent-cost
numerator remains the biggest honesty gap (§Remaining item 1), plus the
methodology refinements in item 2.

**Goal.** Every Kitsoki story publishes the cost savings it delivers versus doing
the same work as a raw Claude Code agentic loop. **Session mining is the cost
denominator for the whole project** — the same mined corpus that gives coverage
and intents gives "what would this have cost as an agent loop?"

## Shipped

The driver and its report are in place (commits on `feat/story-conformance-mining`):

- `tools/session-mining/pricing.py` — authoritative per-model price table; exact
  `message_cost()` incl. 5m/1h cache split; flags unpriced models.
- `tools/session-mining/cost_extract.py` — REAL cost from recorded `message.usage`,
  per user-turn; reprocessing-tax %, per-action climb, measured cold resumes.
- `tools/session-mining/prep.py` — every mining run writes a `costs.json` sidecar.
- **`tools/session-mining/cost_report.py` (`make cost-report`)** — per story, pairs
  the **deterministic numerator** (agent `cost_usd` summed straight from the
  story's host cassette; every routed/host step is $0) with the **raw-agentic
  denominator** (real telemetry scoped by the story's `mining.profile.yaml`,
  per-intent median/p90 distribution, reprocessing tax + cold-resume re-warm), and
  emits a savings table — the reusable form of the
  [case study](../case-studies/git-ops-cost.md). Dispatched agent/agent and
  synthetic-fixture telemetry are dropped. Model-mix lever (raw Opus vs agent
  Sonnet) is surfaced. Tests: `test_cost_report.py` (no LLM).

This covers the original roadmap items 2–5 (real baseline, per-intent matching,
distribution, one report) and the *programmatic* half of item 1 (the numerator is
read from the cassette, not transcribed by hand).

## Remaining

### 1. Record the agent numerator live — BIGGEST HONESTY GAP
`cost_report.py` reads each story's agent `cost_usd` from its host cassette, but
git-ops's cassette is `record_mode: none` — those two numbers ($0.0121, $0.0834)
are **authored**, not recorded. The report flags them (⚠ authored); closing it:
- Record real agent cassettes (record mode) so the committed numbers are genuine
  recorded costs. **This costs LLM spend and is gated** — do it only when asked.
- Optionally capture `agent.call.complete` `cost_usd`/usage
  (`internal/host/agent_event_sink.go`, `agent_runner.go`) from non-cassette runs
  into a per-story ledger that `cost_report.py` reads in preference to the cassette,
  so live runs feed the numerator automatically.
- Until then every other story shows a real raw baseline and a *"not yet measured"*
  numerator — the honest state until it records an agent cassette.

### 2. Methodology refinements
- **Corpus scope.** The baseline greps the whole repo's real-session family by the
  profile's vocabulary. That over-matches prose (e.g. "worktree" is constant in
  this repo) — recall-only, a denominator-up (conservative) bias the case study
  documents. If it proves too noisy, add a command-only match mode (require the hit
  in a tool `command`, not just user text) as an opt-in tightening.
- **Tokenizer accuracy** for the synthetic estimator fallback (`cost_estimate.py`,
  chars/token heuristic) — only matters for telemetry-free corpora.
- **Cross-file continuation:** rare (1/207) but real; if `--resume` usage rises,
  chain transcripts via parent/leaf links for end-to-end session cost.
- **Price-table freshness:** dated constants in `pricing.py`; consider a check, and
  add `claude-fable-5` once its published rate is known (currently fallback-priced
  and flagged).

## Definition of done
A new story that ships a `mining.profile.yaml` automatically gets a real
raw-agentic baseline, a per-intent savings distribution, and — once it records an
agent cassette — a measured deterministic cost, with no hand assembly. The
baseline + distribution are **done**; the measured numerator lands per story as
each records its agent cassette.

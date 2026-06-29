#!/usr/bin/env python3
"""Single authoritative Claude model price table for the mining tools.

USD per 1,000,000 tokens, list price as of 2026-06. Both the real-cost
extractor (cost_extract.py, reads recorded message.usage) and the modelled
estimator (cost_estimate.py, the synthetic-corpus fallback) import from here so
there is exactly one place to update when prices move.

Each tier carries the four (five with the 1-hour cache) rates the Anthropic API
bills independently. Recorded `message.usage` already splits input into the
uncached/cache-write/cache-read buckets, so real cost is an exact dot product of
the recorded counts with these rates — no cold/warm modelling.

`costUSD` in a transcript is authoritative when present (API-key mode populates
it); it is null under a subscription, which is why we keep this table to compute
from the recorded token counts.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Price:
    """USD per 1M tokens for one model tier."""

    input: float          # fresh, uncached input
    output: float
    cache_write_5m: float  # ephemeral 5-minute cache write (1.25x input)
    cache_write_1h: float  # ephemeral 1-hour cache write (2x input)
    cache_read: float      # cache read (0.1x input)
    # True when one or more rates are a best-known ESTIMATE rather than a
    # confirmed published list price. The row still resolves for its model id,
    # but price_for()/message_cost() flag is_exact=False so the caller discloses
    # the estimate instead of treating the dollar figure as authoritative.
    is_estimate: bool = False


# Keyed by a model-id prefix; longest matching prefix wins (see price_for).
PRICING: dict[str, Price] = {
    # Opus 4.x — the Claude Code default for heavy coding sessions.
    # Updated 2026-06 to the current Opus 4.8 list price ($5 in / $25 out per
    # Mtok; cache_read 0.1x = 0.5, write_5m 1.25x = 6.25, write_1h 2x = 10).
    # Verified against kitsoki's native per-call meta.cost_usd on a real bugfix
    # trace: this table reproduces the recorded session cost to within ~0.4%.
    # (The prior 15/75 row was stale Opus-4 pricing and overstated cost ~3x.)
    "claude-opus-4": Price(5.0, 25.0, 6.25, 10.0, 0.50),
    # Sonnet 4.x — the demo's agent model and a common Claude Code tier.
    "claude-sonnet-4": Price(3.0, 15.0, 3.75, 6.0, 0.30),
    # Haiku 4.5 — cheap tier.
    "claude-haiku-4": Price(1.0, 5.0, 1.25, 2.0, 0.10),

    # --- Non-Anthropic bake-off candidates (see tools/bugfix-bakeoff) --------
    # These models are billed by their own providers, NOT the Anthropic API, so
    # the cache-tier split below is a pragmatic mapping onto the same Price shape
    # rather than five independently published rates. All numbers are best-known
    # public USD/1M-token rates as of 2026-06 and are flagged is_estimate=True so
    # downstream cost_exact resolves to false (SCHEMA: "false => priced from an
    # added/est rate row"). Update + clear the flag once rates are confirmed.
    #
    # GLM-5.2 served via synthetic.new (model id `hf:zai-org/GLM-5.2`).
    # ESTIMATE (2026-06): synthetic.new does not publish a per-token GLM-5.2 list
    # price (flat-subscription gateway); these mirror published GLM-4.x-class
    # open-weight serving rates. input/output set from that band; cache tiers
    # derived with the Anthropic multipliers (write_5m=1.25x, write_1h=2x,
    # read=0.1x) since synthetic.new doesn't bill a separate cache tier.
    "hf:zai-org/GLM-5.2": Price(0.60, 2.20, 0.75, 1.20, 0.06, is_estimate=True),
    # GPT-5.5 served via codex (model id `gpt-5.5`).
    # ESTIMATE (2026-06): public OpenAI GPT-5.5 list price not yet confirmed in
    # this table; input/output set from the GPT-5-class band and cache_read from
    # OpenAI's cached-input discount. cache_write tiers derived with the same
    # Anthropic multipliers for shape parity (OpenAI bills no cache-write fee).
    "gpt-5.5": Price(1.25, 10.0, 1.5625, 2.50, 0.125, is_estimate=True),
}

# Models with no published price we model: fall back to this tier and FLAG it so
# the caller can disclose the assumption rather than silently pricing at it.
FALLBACK_PRICE = PRICING["claude-sonnet-4"]
PRICED_AT = "claude-sonnet-4 (fallback — no published rate)"


# Claude Code caches the prompt prefix with a 1-HOUR TTL (transcripts record
# every write as ephemeral_1h). Within the hour, reprocessing the prefix to take
# the next action is billed at cache_read (~0.1x input). Step away past the TTL
# and the cache is gone: the same prefix re-bills WITHOUT the discount — at full
# input rate on re-read, and the first cold turn re-WRITES at up to 2x input.
# So "come back after a break and do one small thing" pays a cold premium on the
# whole conversation. cold_premium() returns that multiple over the warm rate.
def cold_premium(model: str) -> tuple[float, float]:
    """(conservative, first-turn) cold-resume cost multiple over a warm cache-read.
    conservative = full-input/cache-read (you simply lose the cache discount);
    first-turn = cache-write-1h/cache-read (the cold turn must re-write the prefix)."""
    p, _ = price_for(model)
    return p.input / p.cache_read, p.cache_write_1h / p.cache_read


def price_for(model: str) -> tuple[Price, bool]:
    """Return (price, is_exact). is_exact=False means either we used the fallback
    tier for an unrecognised model OR the resolved row is a best-known ESTIMATE
    (Price.is_estimate) rather than a confirmed published rate — in both cases the
    caller should disclose it."""
    if not model:
        return FALLBACK_PRICE, False
    best, blen = None, -1
    for prefix, price in PRICING.items():
        if model.startswith(prefix) and len(prefix) > blen:
            best, blen = price, len(prefix)
    if best is None:
        return FALLBACK_PRICE, False
    return best, not best.is_estimate


def message_cost(usage: dict, model: str) -> tuple[float, bool]:
    """Exact USD cost of one assistant message from its recorded `usage` block.

    usage carries the API's independent buckets:
      input_tokens                  — fresh uncached input
      cache_read_input_tokens       — read from prompt cache
      cache_creation_input_tokens   — written to cache (total)
      cache_creation.ephemeral_{5m,1h}_input_tokens — the write split
      output_tokens
    Returns (usd, is_exact); is_exact mirrors price_for (model recognised)."""
    p, exact = price_for(model)
    cc = usage.get("cache_creation") or {}
    cw5 = cc.get("ephemeral_5m_input_tokens")
    cw1 = cc.get("ephemeral_1h_input_tokens")
    if cw5 is None and cw1 is None:
        # no split recorded — treat the whole cache write as 5-minute.
        cw5 = usage.get("cache_creation_input_tokens", 0)
        cw1 = 0
    usd = (
        usage.get("input_tokens", 0) * p.input
        + usage.get("output_tokens", 0) * p.output
        + usage.get("cache_read_input_tokens", 0) * p.cache_read
        + (cw5 or 0) * p.cache_write_5m
        + (cw1 or 0) * p.cache_write_1h
    ) / 1e6
    return usd, exact

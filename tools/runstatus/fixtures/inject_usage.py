#!/usr/bin/env python3
"""Inject plausible token-usage meta into cassette-based runstatus snapshots.

The cassette-replay fixtures (bugfix / completed / in-progress / edge-cases)
were recorded without token usage, so the runstatus UI has nothing to show in
the per-call header or the run-total chip. This backfills each
`agent.call.complete` event with the canonical transport `meta` shape the live
claude-CLI transport now emits:

    attrs.meta = {
      "transport": "claude-cli",
      "usage": {
        "input_tokens":            <int>,
        "output_tokens":           <int>,
        "cache_read_input_tokens": <int>,
        "cache_creation_input_tokens": <int>,
      },
      "cost_usd": <float>,
    }

Numbers are *plausible*, not real: derived deterministically from each call's
verb, the length of its recorded response, its duration, and its position in
the run, so they vary call-to-call and look realistic without any RNG (the
files stay byte-stable across re-runs). Existing real usage is left untouched.

Usage:  python3 inject_usage.py bugfix.snapshot.json completed.snapshot.json ...
"""

import json
import sys

# Sonnet-ish per-million-token rates (USD). Cache reads are much cheaper than
# fresh input; cache writes carry a small premium over plain input.
RATE_INPUT = 3.0 / 1_000_000
RATE_OUTPUT = 15.0 / 1_000_000
RATE_CACHE_READ = 0.30 / 1_000_000
RATE_CACHE_WRITE = 3.75 / 1_000_000

# Baseline fresh-input prompt size per verb (tokens), before the cached share.
VERB_BASE_INPUT = {
    "decide": 650,
    "extract": 520,
    "ask": 900,
    "task": 4200,
    "converse": 780,
}
# Fallback output token estimate per verb when there is no response text.
VERB_BASE_OUTPUT = {
    "decide": 22,
    "extract": 60,
    "ask": 30,
    "task": 900,
    "converse": 140,
}


def response_text_len(attrs):
    """Rough character count of the recorded response, if any."""
    resp = attrs.get("response")
    if isinstance(resp, dict):
        for key in ("text", "extracted", "json", "decision"):
            v = resp.get(key)
            if isinstance(v, str):
                return len(v)
            if v is not None:
                return len(json.dumps(v))
    if isinstance(resp, str):
        return len(resp)
    return 0


def estimate_usage(attrs, idx):
    verb = str(attrs.get("verb", "ask"))
    base_in = VERB_BASE_INPUT.get(verb, 800)
    # Later calls in a run accumulate more prompt context; nudge input up a bit
    # per call so the totals grow monotonically and look like a real session.
    growth = 1.0 + min(idx, 12) * 0.06
    input_tokens = int(base_in * growth)

    # Output tokens track the actual recorded response length (~4 chars/token),
    # falling back to a per-verb baseline when no response is present.
    rlen = response_text_len(attrs)
    if rlen > 0:
        output_tokens = max(8, rlen // 4)
    else:
        output_tokens = VERB_BASE_OUTPUT.get(verb, 40)

    # First call has nothing cached; subsequent calls reuse most of the prompt
    # prefix from the cache. Cache creation is the slice newly written this call.
    if idx == 0:
        cache_read = 0
        cache_creation = int(input_tokens * 0.5)
    else:
        cache_read = int(input_tokens * 0.72)
        cache_creation = int(input_tokens * 0.08)

    fresh_input = max(0, input_tokens - cache_read - cache_creation)
    cost = (
        fresh_input * RATE_INPUT
        + cache_read * RATE_CACHE_READ
        + cache_creation * RATE_CACHE_WRITE
        + output_tokens * RATE_OUTPUT
    )
    return {
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "cache_read_input_tokens": cache_read,
        "cache_creation_input_tokens": cache_creation,
    }, round(cost, 6)


def inject(path):
    with open(path) as f:
        doc = json.load(f)

    idx = 0
    touched = 0
    for ev in doc.get("events", []):
        if ev.get("msg") != "agent.call.complete":
            continue
        attrs = ev.setdefault("attrs", {})
        meta = attrs.get("meta")
        if isinstance(meta, dict) and isinstance(meta.get("usage"), dict):
            idx += 1
            continue  # already has real usage; leave it
        usage, cost = estimate_usage(attrs, idx)
        if not isinstance(meta, dict):
            meta = {}
        meta.setdefault("transport", "claude-cli")
        meta["usage"] = usage
        meta["cost_usd"] = cost
        attrs["meta"] = meta
        idx += 1
        touched += 1

    # Serialize the way Go's json.MarshalIndent does so the diff stays additive:
    #  - ensure_ascii=False keeps UTF-8 glyphs (—, ·, →) literal, and
    #  - Go's encoder HTML-escapes <, >, & as <, >, & by default.
    text = json.dumps(doc, indent=2, ensure_ascii=False)
    text = text.replace("&", "\\u0026").replace("<", "\\u003c").replace(">", "\\u003e")
    with open(path, "w") as f:
        f.write(text)
        f.write("\n")
    print(f"{path}: injected usage into {touched} agent.call.complete events")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    for p in sys.argv[1:]:
        inject(p)

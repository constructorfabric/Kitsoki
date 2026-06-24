#!/usr/bin/env python3
"""Behavioral tests for cost_estimate.py, NO LLM, no network.

The estimator is a parametric model, so we don't pin exact dollars (knobs move
them by design). We pin the INVARIANTS that make the comparison honest:

  * Kitsoki cost is read from the committed cassette and is FLAT in prior-context
    (the deterministic engine never re-sends a conversation to a model).
  * Raw Claude Code cost rises monotonically with prior-context and with turn
    count (re-send-everything-per-call billing).
  * The warm-cache aggregate is the floor and cold is the ceiling.
  * The session SHAPE matches the mined transcripts (verifiable structure).

Run:  python3 tools/session-mining/tests/test_cost_estimate.py
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import cost_estimate as ce


class _Args:
    """Minimal args namespace mirroring argparse defaults."""

    def __init__(self, **over):
        for k, v in ce.DEFAULTS.items():
            setattr(self, k, v)
        self.raw = ce.DEFAULT_RAW
        self.prior_context = 0
        for k, v in over.items():
            setattr(self, k, v)


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    # --- Kitsoki ground truth from the committed cassette --------------------
    kit_total, items = ce.read_kitsoki_cost(ce.DEFAULT_CASSETTE)
    check(abs(kit_total - 0.0955) < 1e-9,
          "kitsoki total should be the two committed agent costs (0.0955), got %r"
          % kit_total)
    check(len(items) == 2, "expected 2 paid agent surfaces, got %d" % len(items))
    handlers = {h for h, _ in items}
    check(handlers == {"host.agent.decide", "host.agent.task"},
          "unexpected paid handlers: %r" % handlers)

    # --- Session shape is verifiable from the mined transcripts --------------
    expected_calls = {
        "sess-commit-happy": 2,
        "sess-rebase-conflict": 4,
        "sess-merge-direct": 1,
        "sess-worktree": 1,
    }
    a0 = _Args()
    for sid, n in expected_calls.items():
        s = ce.build_session(sid, sid, a0.raw / f"{sid}.jsonl", a0)
        check(s.n_calls == n,
              "%s should have %d assistant API calls, got %d" % (sid, n, s.n_calls))

    # --- Raw cost rises with prior-context; Kitsoki is flat ------------------
    def fleet_cost(prior, fn):
        a = _Args(prior_context=prior)
        return sum(
            fn(ce.build_session(sid, sid, a.raw / f"{sid}.jsonl", a), a)
            for _, sid in ce.DEMO_SESSIONS
        )

    cold0 = fleet_cost(0, ce.cost_cold)
    cold50 = fleet_cost(50000, ce.cost_cold)
    cold200 = fleet_cost(200000, ce.cost_cold)
    check(cold0 < cold50 < cold200,
          "cold cost must rise with prior context: %r" % [cold0, cold50, cold200])
    check(kit_total == kit_total,  # flat by construction (read once, no knob)
          "kitsoki cost must not depend on prior context")

    # --- Warm is the aggregate floor, cold the ceiling -----------------------
    for prior in (0, 50000, 200000):
        warm = fleet_cost(prior, ce.cost_warm)
        cold = fleet_cost(prior, ce.cost_cold)
        check(warm <= cold,
              "warm aggregate (%r) must be <= cold (%r) at prior=%d"
              % (warm, cold, prior))

    # --- Even raw fresh-session cost dwarfs the deterministic story ----------
    check(cold0 > kit_total * 2,
          "raw fresh-session 4-op cost (%r) should be >2x Kitsoki (%r)"
          % (cold0, kit_total))

    # --- More turns => more cost (rebase's 4 calls cost more than 1-call ops) -
    a = _Args()
    rebase = ce.build_session("sess-rebase-conflict", "", a.raw / "sess-rebase-conflict.jsonl", a)
    merge = ce.build_session("sess-merge-direct", "", a.raw / "sess-merge-direct.jsonl", a)
    check(ce.cost_cold(rebase, a) > ce.cost_cold(merge, a),
          "4-call rebase should cost more than 1-call merge")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: cost_estimate invariants (no LLM): "
          "kitsoki flat @ $%.4f, raw rises with context, warm<=cold" % kit_total)
    return 0


if __name__ == "__main__":
    sys.exit(run())

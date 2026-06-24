#!/usr/bin/env python3
"""Tests for cost_report.py — the per-story savings driver. NO LLM, no network.

Covers the two halves it joins:
  * read_story_cost — sums agent.cost_usd from a story's host cassette(s) and
    flags authored (record_mode none) numbers.
  * baseline_from_corpus — scopes synthetic transcripts by a profile, attributes
    per-operation real cost, drops fixture/synthetic + dispatched-agent sessions,
    and reports a distribution.
Plus the percentile/median helpers and an end-to-end build_report smoke.

Run:  python3 tools/session-mining/tests/test_cost_report.py
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import pricing
import cost_report as cr


def _write(path, records):
    with open(path, "w") as fh:
        for r in records:
            fh.write(json.dumps(r) + "\n")


def _amsg(model, out_tok, cmd=None, cache_read=0, ts=None, cache_write=0):
    content = [{"type": "text", "text": "ok"}]
    if cmd:
        content.append({"type": "tool_use", "name": "Bash", "input": {"command": cmd}})
    m = {"type": "assistant",
         "message": {"model": model, "content": content,
                     "usage": {"input_tokens": 50, "output_tokens": out_tok,
                               "cache_read_input_tokens": cache_read,
                               "cache_creation_input_tokens": cache_write,
                               "cache_creation": {"ephemeral_5m_input_tokens": 0,
                                                  "ephemeral_1h_input_tokens": cache_write}}}}
    if ts:
        m["timestamp"] = ts
    return m


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    def approx(a, b, msg, tol=1e-6):
        check(abs(a - b) < tol, "%s: %r vs %r" % (msg, a, b))

    # --- percentile/median helpers ------------------------------------------
    check(cr._median([]) == 0.0, "median of empty is 0")
    check(cr._median([5]) == 5, "median of one")
    approx(cr._median([1, 2, 3]), 2, "median odd")
    check(cr._pct([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 0.9) == 10
          or cr._pct([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 0.9) == 9,
          "p90 nearest-rank picks the top of a 10-sample")

    # --- read_story_cost: sum agent costs from a cassette, flag authored ----
    with tempfile.TemporaryDirectory() as d:
        cdir = os.path.join(d, "flows", "cassettes")
        os.makedirs(cdir)
        cass = "\n".join([
            "kind: host_cassette",
            "record_mode: none",
            "episodes:",
            "  - id: a",
            "    match:",
            "      handler: host.agent.decide",
            "    agent:",
            '      model: "claude-sonnet-4-6"',
            "      cost_usd: 0.0121",
            # the embedded transcript carries a DIFFERENT key in quoted JSON — must
            # NOT be summed (regression guard for the anchored regex).
            "      response: '{\"total_cost_usd\":0.0121}'",
            "  - id: b",
            "    match:",
            "      handler: host.agent.task",
            "    agent:",
            '      model: "claude-sonnet-4-6"',
            "      cost_usd: 0.0834",
            "",
        ])
        with open(os.path.join(cdir, "demo.cassette.yaml"), "w") as fh:
            fh.write(cass)
        sc = cr.read_story_cost(d)
    approx(sc.usd, 0.0955, "story cost = sum of the two agent cost_usd")
    check(sc.agent_calls == 2, "two agent calls, got %d" % sc.agent_calls)
    check(not sc.recorded, "record_mode none must flag authored (recorded=False)")
    check(sc.measured, "measured must be True when costs were found")
    check(sc.models == {"claude-sonnet-4-6"}, "model captured: %r" % sc.models)
    check([h for h, _ in sc.breakdown] == ["host.agent.decide", "host.agent.task"],
          "breakdown handlers in order: %r" % sc.breakdown)

    # a story with no cassette -> not measured (deterministic-only, unknown agent)
    with tempfile.TemporaryDirectory() as d:
        empty = cr.read_story_cost(d)
    check(not empty.measured, "no cassette -> not measured")

    # --- baseline_from_corpus: scope, attribute, drop synthetic + agents -----
    profile = {"scope": {"grep": ["git commit", "rebase"]}}
    with tempfile.TemporaryDirectory() as d:
        proj = os.path.join(d, "proj")
        os.makedirs(proj)
        # A: a real opus session that commits (cache_read = reprocessing tax).
        _write(os.path.join(proj, "real.jsonl"), [
            {"type": "user", "message": {"content": "look around"}},
            _amsg("claude-opus-4-8", 100, cache_read=200000),
            {"type": "user", "message": {"content": "commit this"}},
            _amsg("claude-opus-4-8", 80, cmd="git commit -m wip", cache_read=500000),
        ])
        # B: a synthetic-fixture session (model <synthetic>, zero real cost) — must
        # be dropped entirely from the baseline.
        _write(os.path.join(proj, "synth.jsonl"), [
            {"type": "user", "message": {"content": "git commit fixture"}},
            {"type": "assistant", "message": {"model": "<synthetic>",
                "content": [{"type": "text", "text": "ok"}],
                "usage": {"input_tokens": 0, "output_tokens": 0,
                          "cache_read_input_tokens": 0,
                          "cache_creation_input_tokens": 0}}},
        ])
        # C: a dispatched agent/agent session (entrypoint != cli) — dropped before
        # parsing, even though it git-commits.
        _write(os.path.join(proj, "agent.jsonl"), [
            {"type": "user", "entrypoint": "sdk-cli", "message": {"content": "git commit"}},
            _amsg("claude-opus-4-8", 50, cmd="git commit -m x", cache_read=10000),
        ])
        bl = cr.baseline_from_corpus(profile, [proj], min_bytes=0)

    # only A's commit turn survives. (A's "look around" turn has no git op.)
    check(bl.total_n() == 1, "exactly 1 real matching op, got %d" % bl.total_n())
    check(bl.transcripts >= 1, "real transcript scanned")
    check(bl.ops["git commit"].n() == 1, "commit bucket has the one real op")
    check(bl.ops["rebase"].n() == 0, "rebase bucket empty (no rebase op)")
    check(bl.ops["git commit"].models == {"claude-opus-4-8"},
          "synthetic model excluded from baseline models: %r" % bl.ops["git commit"].models)
    # cost matches the exact price of that one assistant message
    want, _ = pricing.message_cost(
        {"input_tokens": 50, "output_tokens": 80, "cache_read_input_tokens": 500000,
         "cache_creation_input_tokens": 0}, "claude-opus-4-8")
    approx(bl.all_op_costs[0], want, "baseline op cost == exact message cost")
    check(bl.ops["git commit"].reproc == [500000], "reprocessing tokens captured")

    # --- cold-resume re-warm aggregation ------------------------------------
    profile2 = {"scope": {"grep": ["git commit"]}}
    with tempfile.TemporaryDirectory() as d:
        proj = os.path.join(d, "proj")
        os.makedirs(proj)
        _write(os.path.join(proj, "gap.jsonl"), [
            {"type": "user", "timestamp": "2026-06-01T10:00:00Z", "message": {"content": "start"}},
            _amsg("claude-opus-4-8", 10, ts="2026-06-01T10:00:05Z", cache_read=1000),
            {"type": "user", "timestamp": "2026-06-02T06:00:00Z", "message": {"content": "git commit now"}},
            _amsg("claude-opus-4-8", 10, cmd="git commit -m x",
                  ts="2026-06-02T06:00:10Z", cache_read=1000, cache_write=120000),
        ])
        blg = cr.baseline_from_corpus(profile2, [proj], min_bytes=0)
    rewarms = [r for st in blg.ops.values() for r in st.rewarm_usd]
    check(len(rewarms) == 1, "one cold-resume re-warm captured, got %d" % len(rewarms))
    opus = pricing.PRICING["claude-opus-4"]
    approx(rewarms[0], 120000 * opus.cache_write_1h / 1e6,
           "re-warm cost = cold prefix re-write @ 1h write rate")

    # --- build_report smoke: produces a summary table + a per-story section --
    md = cr.build_report([], [])  # no stories -> still renders the header + table
    check("# Per-story cost report" in md, "report has a title")
    check("| story | story cost |" in md, "report has a summary table header")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: cost_report (no LLM): cassette numerator, corpus denominator, "
          "synthetic/agent drops, cold-resume re-warm, report render")
    return 0


if __name__ == "__main__":
    sys.exit(run())

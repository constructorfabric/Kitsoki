#!/usr/bin/env python3
"""Extract REAL cost from a real Claude Code transcript — no modelling.

Where cost_estimate.py *models* cost for the synthetic/redacted demo corpus
(which carries no telemetry), this reads the genuine `message.usage` recorded on
every assistant message of a real session and computes exact cost via the shared
price table (pricing.py). The recorded usage already splits input into
uncached / cache-write / cache-read buckets and records the 5m/1h cache-write
split, so there is nothing to model: real cost is a dot product of recorded
counts with published rates.

A real Claude Code session is one long transcript with many operations
interleaved, and that entanglement is the whole point — not noise to strip out.
To take the NEXT action, the model reprocesses the ENTIRE conversation before it
(those are the cache-read tokens). So the same `git commit` is cheap as the 2nd
action and expensive as the 30th — not because committing got harder, but
because there's now a long conversation to re-read to get there. That
reprocessing tax is exactly what Kitsoki's deterministic engine eliminates: it
never feeds a conversation back through a model to decide the next step.

So the unit is the USER TURN (a user message + the assistant API calls it
triggers), and the story --by-turn tells is the CLIMB: per-action cost rising as
the session grows, plus the cache-read (reprocess) tokens each turn re-reads just
to carry the prior conversation forward. The header also reports what share of
all input tokens were reprocessing. --grep finds the turn that ran a command.

COLD RESUMES (coming back after a break) are tracked too, and measured rather
than modelled. Resuming a session APPENDS to the same transcript file (cross-file
parentUuid links are vanishingly rare), so a break shows up as a large time gap
between consecutive records. Past the 1h cache TTL the cache is gone, so the
first turn back re-WRITES the prefix cold — a cache_creation spike billed at the
write rate. We detect gaps >= CACHE_TTL_MIN and capture that re-write as the REAL
$ paid just to re-warm the conversation before any work happens. (When a
transcript contains no such break, the summary falls back to a clearly-labelled
rate counterfactual instead.)

Usage:
  # whole-session real cost
  cost_extract.py SESSION.jsonl

  # per-operation (user-turn) breakdown with the user text
  cost_extract.py SESSION.jsonl --by-turn

  # find + cost every turn that ran a `git rebase` across many real sessions,
  # the foundation for "re-mine the demo for real"
  cost_extract.py ~/.claude/projects/<proj>/*.jsonl --grep 'git rebase' --by-turn

  # machine-readable (the sidecar prep.py writes per mined session)
  cost_extract.py SESSION.jsonl --json
"""

from __future__ import annotations

import argparse
import datetime
import glob
import json
import os
import sys
from dataclasses import dataclass, field

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pricing

# Claude Code's prompt cache TTL (minutes). A gap longer than this between
# consecutive transcript records means the cache expired: resuming re-WRITES the
# prefix cold (a cache_creation spike) instead of re-reading it. Resumes append to
# the same session file, so a within-file time gap is the continuation signal.
CACHE_TTL_MIN = 60


def _ts(s):
    if not isinstance(s, str):
        return None
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None


@dataclass
class Turn:
    """One user request and the assistant API calls it triggered."""

    user_text: str
    calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    cache_read: int = 0
    cache_write: int = 0
    cost_usd: float = 0.0
    models: set = field(default_factory=set)
    commands: list = field(default_factory=list)  # tool_use signatures seen
    exact: bool = True  # False if any message hit an unpriced model
    # Cold-resume: gap (minutes) before this turn if it followed a break past the
    # cache TTL, and the REAL cost of the cold prefix re-write on the first turn
    # back (directly measured from its cache_creation, not a counterfactual).
    gap_min: float = 0.0
    rewarm_tokens: int = 0
    rewarm_usd: float = 0.0

    @property
    def total_tokens(self) -> int:
        return self.input_tokens + self.output_tokens + self.cache_read + self.cache_write


@dataclass
class SessionCost:
    path: str
    turns: list = field(default_factory=list)

    def total(self) -> float:
        return sum(t.cost_usd for t in self.turns)

    def calls(self) -> int:
        return sum(t.calls for t in self.turns)

    def tokens(self) -> int:
        return sum(t.total_tokens for t in self.turns)

    def models(self) -> set:
        m = set()
        for t in self.turns:
            m |= t.models
        return m

    def exact(self) -> bool:
        return all(t.exact for t in self.turns)

    def resumes(self) -> list:
        """Turns that followed a break past the cache TTL (cold resumes)."""
        return [t for t in self.turns if t.gap_min >= CACHE_TTL_MIN]

    def rewarm_usd(self) -> float:
        """REAL total $ paid to re-warm the conversation after breaks."""
        return sum(t.rewarm_usd for t in self.turns)


def _user_text(content) -> str | None:
    """The genuine user request text, or None for harness-injected turns
    (slash-command caveats, system reminders, tool_results)."""
    if isinstance(content, str):
        s = content.strip()
        if not s or s.startswith("<"):
            return None
        return s
    if isinstance(content, list):
        if any(b.get("type") == "tool_result" for b in content):
            return None  # tool result, not a user request
        texts = [b.get("text", "") for b in content if b.get("type") == "text"]
        s = " ".join(texts).strip()
        if not s or s.startswith("<command") or "system-reminder" in s:
            return None
        return s
    return None


def _tool_sigs(content) -> list:
    sigs = []
    if isinstance(content, list):
        for b in content:
            if b.get("type") == "tool_use":
                inp = b.get("input", {})
                arg = (inp.get("command") or inp.get("file_path")
                       or inp.get("description") or "")
                sigs.append(f"{b.get('name')}: {str(arg)[:80]}")
    return sigs


def _rewarm_cost(usage: dict, model: str) -> tuple[int, float]:
    """The cold prefix re-write on the first turn back: just the cache_creation
    (write) component, priced at the write rate. This is REAL cost paid to
    re-warm the conversation, measured from the resume turn's usage."""
    p, _ = pricing.price_for(model)
    cc = usage.get("cache_creation") or {}
    cw5 = cc.get("ephemeral_5m_input_tokens")
    cw1 = cc.get("ephemeral_1h_input_tokens")
    if cw5 is None and cw1 is None:
        cw5, cw1 = usage.get("cache_creation_input_tokens", 0), 0
    toks = (cw5 or 0) + (cw1 or 0)
    usd = ((cw5 or 0) * p.cache_write_5m + (cw1 or 0) * p.cache_write_1h) / 1e6
    return toks, usd


def extract(path: str) -> SessionCost:
    sc = SessionCost(path=path)
    cur = Turn(user_text="(session start)")
    sc.turns.append(cur)
    prev_ts = None        # timestamp of the previous record (any type)
    pending_gap = 0.0     # gap before the current turn, until its first call lands
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            ts = _ts(rec.get("timestamp"))
            gap = ((ts - prev_ts).total_seconds() / 60) if (ts and prev_ts) else 0.0
            if ts:
                prev_ts = ts
            msg = rec.get("message") or {}
            if rec.get("type") == "user" and not rec.get("isMeta"):
                ut = _user_text(msg.get("content"))
                if ut is not None:
                    cur = Turn(user_text=ut)
                    sc.turns.append(cur)
                    # a break before this user request => cold resume; record the
                    # gap and re-warm the first assistant call below.
                    cur.gap_min = gap if gap >= CACHE_TTL_MIN else 0.0
                    pending_gap = cur.gap_min
            elif rec.get("type") == "assistant":
                usage = msg.get("usage")
                if not usage:
                    continue
                model = msg.get("model", "")
                usd, exact = pricing.message_cost(usage, model)
                if pending_gap and cur.calls == 0:
                    # first call after the break: its cache_creation IS the cold
                    # prefix re-write. Capture the real re-warm cost, once.
                    cur.rewarm_tokens, cur.rewarm_usd = _rewarm_cost(usage, model)
                    pending_gap = 0.0
                cur.calls += 1
                cur.input_tokens += usage.get("input_tokens", 0)
                cur.output_tokens += usage.get("output_tokens", 0)
                cur.cache_read += usage.get("cache_read_input_tokens", 0)
                cur.cache_write += usage.get("cache_creation_input_tokens", 0)
                cur.cost_usd += usd
                cur.models.add(model)
                cur.exact = cur.exact and exact
                cur.commands.extend(_tool_sigs(msg.get("content")))
    # drop empty leading/synthetic turns with no calls
    sc.turns = [t for t in sc.turns if t.calls > 0]
    return sc


def fmt_usd(x: float) -> str:
    return f"${x:,.4f}" if x < 1 else f"${x:,.2f}"


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("sessions", nargs="+", help="real .jsonl transcript(s) / globs")
    ap.add_argument("--by-turn", action="store_true",
                    help="per-operation (user-turn) breakdown")
    ap.add_argument("--grep", help="only show turns whose user text or a tool "
                                   "command contains this substring")
    ap.add_argument("--json", action="store_true", help="machine-readable output")
    ap.add_argument("--top", type=int, default=0,
                    help="with --grep, show only the N cheapest matching turns")
    args = ap.parse_args()

    paths = []
    for s in args.sessions:
        paths.extend(sorted(glob.glob(os.path.expanduser(s))) or [s])

    sessions = [extract(p) for p in paths if os.path.exists(p)]
    any_inexact = any(not s.exact() for s in sessions)

    if args.grep:
        # cross-session search: find + cost every matching operation.
        hits = []
        for sc in sessions:
            for t in sc.turns:
                hay = (t.user_text + " " + " ".join(t.commands)).lower()
                if args.grep.lower() in hay:
                    hits.append((sc, t))
        hits.sort(key=lambda ht: ht[1].cost_usd)
        if args.top:
            hits = hits[:args.top]
        if args.json:
            print(json.dumps([_turn_json(sc, t) for sc, t in hits], indent=2))
            return
        print(f"# real cost of operations matching '{args.grep}' "
              f"({len(hits)} found across {len(sessions)} sessions)\n")
        for sc, t in hits:
            print(f"{fmt_usd(t.cost_usd):>10}  {t.calls:>2} calls  "
                  f"{t.total_tokens:>9,} tok  {os.path.basename(sc.path)[:18]}  "
                  f"| {t.user_text[:70]}")
        if any_inexact:
            print(f"\n[!] some turns priced via {pricing.PRICED_AT}")
        return

    if args.json:
        print(json.dumps([_session_json(s) for s in sessions], indent=2))
        return

    for sc in sessions:
        reproc = sum(t.cache_read for t in sc.turns)
        total_in = sum(t.input_tokens + t.cache_read + t.cache_write for t in sc.turns)
        share = (reproc / total_in * 100) if total_in else 0.0
        print(f"\n# {os.path.basename(sc.path)}")
        print(f"  total {fmt_usd(sc.total())} · {sc.calls()} API calls · "
              f"{sc.tokens():,} tok · models {sorted(sc.models())}")
        print(f"  reprocessing tax: {share:.0f}% of input tokens were cache reads "
              f"of prior context ({reproc:,} tok re-read to take the next action)")
        # Cold resumes — REAL, measured. A break past the 1h cache TTL forces the
        # next turn to re-WRITE the prefix cold (a cache_creation spike), captured
        # as rewarm_usd. Kitsoki: $0 to resume — no conversation cache to expire.
        model = next(iter(sc.models()), "")
        resumes = sc.resumes()
        if resumes:
            tot = sc.rewarm_usd()
            print(f"  cold resumes: {len(resumes)} break(s) >1h cost {fmt_usd(tot)} "
                  f"REAL just to re-warm the conversation (measured cache re-writes):")
            for t in sorted(resumes, key=lambda x: -x.rewarm_usd)[:3]:
                warm = t.rewarm_tokens * pricing.price_for(model)[0].cache_read / 1e6
                mult = (t.rewarm_usd / warm) if warm else 0
                print(f"    +{t.gap_min/60:4.1f}h gap -> re-wrote {t.rewarm_tokens:,} tok "
                      f"cold = {fmt_usd(t.rewarm_usd)} ({mult:.0f}x the warm "
                      f"{fmt_usd(warm)})  | {t.user_text[:40]}")
        elif model:
            # No break observed in this transcript — show the rate-based premium so
            # the cold case is still visible (counterfactual on measured warm tokens).
            small = [t for t in sc.turns if t.calls <= 3 and t.cache_read]
            worst = max(small or sc.turns, key=lambda t: t.cache_read, default=None)
            if worst and worst.cache_read:
                cons, first = pricing.cold_premium(model)
                p, _ = pricing.price_for(model)
                warm = worst.cache_read * p.cache_read / 1e6
                print(f"  cold-resume premium (no break in this transcript; rate "
                      f"counterfactual): re-reading the {worst.cache_read:,}-tok "
                      f"prefix cost {fmt_usd(warm)} warm -> ~{cons:.0f}x "
                      f"({fmt_usd(warm * cons)}) cold | {worst.user_text[:32]}")
        if args.by_turn:
            # The story is the CLIMB: each turn re-reads the whole conversation so
            # far, so the cost of taking the next action rises as the session
            # grows. cumⁿ = what you've paid to reach this action; reproc = tokens
            # re-read this turn just to carry the prior conversation forward.
            print(f"    {'cost':>9} {'cumulative':>11} {'reproc-tok':>11} "
                  f"{'calls':>5}  | action")
            cum = 0.0
            for t in sc.turns:
                cum += t.cost_usd
                flag = "" if t.exact else " *"
                if t.gap_min >= CACHE_TTL_MIN:
                    print(f"    {'⏸ ':>9} {'':>11} {'':>11} {'':>5}   "
                          f"·· +{t.gap_min/60:.1f}h break -> cold re-warm "
                          f"{fmt_usd(t.rewarm_usd)} ({t.rewarm_tokens:,} tok re-written)")
                print(f"    {fmt_usd(t.cost_usd):>9} {fmt_usd(cum):>11} "
                      f"{t.cache_read:>11,} {t.calls:>5}{flag}  | {t.user_text[:56]}")
    if any_inexact:
        print(f"\n[!] some messages priced via {pricing.PRICED_AT} (marked *)")


def _turn_json(sc: SessionCost, t: Turn) -> dict:
    d = dict(session=os.path.basename(sc.path), user_text=t.user_text,
             calls=t.calls, cost_usd=round(t.cost_usd, 6),
             input_tokens=t.input_tokens, output_tokens=t.output_tokens,
             cache_read=t.cache_read, cache_write=t.cache_write,
             models=sorted(t.models), exact=t.exact)
    if t.gap_min >= CACHE_TTL_MIN:
        d["cold_resume"] = dict(gap_min=round(t.gap_min, 1),
                                rewarm_tokens=t.rewarm_tokens,
                                rewarm_usd=round(t.rewarm_usd, 6))
    return d


def _session_json(sc: SessionCost) -> dict:
    return dict(session=os.path.basename(sc.path), path=sc.path,
                cost_usd=round(sc.total(), 6), api_calls=sc.calls(),
                total_tokens=sc.tokens(), models=sorted(sc.models()),
                exact=sc.exact(),
                cold_resumes=len(sc.resumes()),
                rewarm_usd=round(sc.rewarm_usd(), 6),
                turns=[_turn_json(sc, t) for t in sc.turns])


if __name__ == "__main__":
    main()

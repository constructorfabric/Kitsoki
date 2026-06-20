#!/usr/bin/env python3
"""cost_report.py — per-story cost savings, automatic, from real telemetry.

The reusable form of docs/case-studies/git-ops-cost.md. For each story that ships
a stories/<name>/mining.profile.yaml, this pairs two numbers and reports the gap:

  numerator (the story)   — the DETERMINISTIC cost of running the work as a Kitsoki
                            story: $0 for every routed/host step, plus whatever the
                            story's agent calls actually cost. Read straight from
                            the story's host cassette(s) agent.cost_usd (see
                            read_story_cost). Programmatic, not hand-transcribed.

  denominator (the loop)  — the RAW AGENTIC cost of doing the same operations in a
                            Claude Code session, from real recorded telemetry. We
                            scope real transcripts by the story's mining.profile.yaml
                            (the same grep prefilter mining uses), find the user
                            turns that did the story's operations (cost_extract.py),
                            and report their cost DISTRIBUTION (median / p90) — not a
                            single curated example — plus the reprocessing tax and
                            cold-resume re-warm those turns paid.

The point the case study makes is structural: in a loop the per-operation cost is
dominated by REPROCESSING the prior conversation to reach the action, so it climbs
with session length and spikes cold; in the story it is flat and tax-free because
no conversation is fed back through a model. This tool measures that gap per story,
per intent, with error bars — instead of one hand-built example.

Honesty notes it carries forward (not papered over):
  * A story's agent costs are only as real as its cassette. record_mode `none`
    cassettes are AUTHORED numbers; the report flags them. Recording the cassette
    live (LLM spend, gated) is what makes the numerator measured end-to-end.
  * The raw baseline is real telemetry, exact-priced via pricing.py; unknown models
    are flagged. The grep scope over-matches prose (recall-only) — same caveat the
    mining profile documents; this is a denominator-up bias, the conservative side.

Stdlib only. NO LLM, NO network, NO cost — it reads telemetry already on disk.

Usage:
  # report for one story, to stdout
  cost_report.py --story git-ops

  # all stories with a mining.profile.yaml -> a markdown file (the make target)
  cost_report.py --all --out .artifacts/cost-report/cost-report.md

  # point at specific transcript dirs (default: this repo's ~/.claude/projects/*)
  cost_report.py --story git-ops --projects '~/.claude/projects/-Users-...-Kitsoki*'
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import re
import sys
from dataclasses import dataclass, field

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import cost_extract  # real per-turn cost from recorded usage
import coverage_prep  # mining.profile.yaml subset loader
import pricing
import prep  # grep prefilter + agent-session classifier (shared with mining)

REPO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
STORIES_DIR = os.path.join(REPO_ROOT, "stories")

# Default transcript pool: every Claude Code project dir for THIS repo (the main
# checkout + its worktrees each get their own ~/.claude/projects/<slug>). Claude
# Code slugifies the abs path by replacing every non-alphanumeric run with '-', so
# '/Users/.../Kitsoki/.worktrees/x' -> '-Users-...-Kitsoki--worktrees-x'.
_REPO_SLUG = re.sub(r"[^a-zA-Z0-9]+", "-", REPO_ROOT)
# trim the worktree suffix so the glob matches the WHOLE repo family (main checkout
# + all worktrees + per-story dirs), not just this one worktree.
_REPO_SLUG = re.sub(r"-+worktrees-.*$", "", _REPO_SLUG)
DEFAULT_PROJECTS = "~/.claude/projects/%s*" % _REPO_SLUG


# --- numerator: the deterministic story cost, read from its cassettes ---------

# A cassette's agent.cost_usd is a YAML key at line start (after indent). The
# embedded agent transcript carries `"total_cost_usd":N` inside a quoted JSON
# string — a DIFFERENT key, and quoted — so anchoring on the YAML key excludes it.
_COST_RE = re.compile(r'^\s+cost_usd:\s*([0-9.]+)\s*$')
_MODEL_RE = re.compile(r'^\s+model:\s*"?([\w.\-]+)"?\s*$')
_RECMODE_RE = re.compile(r'^\s*record_mode:\s*"?(\w+)"?\s*$')
_HANDLER_RE = re.compile(r'^\s+handler:\s*"?([\w.]+)"?\s*$')


@dataclass
class StoryCost:
    """Deterministic cost of running the story: $0 for every host/routing step,
    plus the real cost of its agent calls (read from the host cassette)."""

    usd: float = 0.0
    agent_calls: int = 0
    models: set = field(default_factory=set)
    breakdown: list = field(default_factory=list)  # (handler, cost_usd)
    recorded: bool = True   # False if any backing cassette is record_mode none (authored)
    cassettes: list = field(default_factory=list)
    measured: bool = False  # True once at least one agent cost was found


def read_story_cost(story_dir: str) -> StoryCost:
    """Sum agent.cost_usd across the story's host cassettes. Deterministic steps
    contribute $0 by construction (they never call a model), so the cassette agent
    spend IS the story's cost. Flags authored (record_mode none) cassettes."""
    sc = StoryCost()
    cass_glob = os.path.join(story_dir, "flows", "cassettes", "*.yaml")
    for path in sorted(glob.glob(cass_glob)):
        sc.cassettes.append(os.path.basename(path))
        cur_handler, cur_model, rec_none = None, None, False
        with open(path, errors="ignore") as fh:
            for line in fh:
                m = _RECMODE_RE.match(line)
                if m:
                    rec_none = rec_none or (m.group(1) == "none")
                    continue
                m = _HANDLER_RE.match(line)
                if m:
                    cur_handler = m.group(1)
                    continue
                m = _MODEL_RE.match(line)
                if m:
                    cur_model = m.group(1)
                    continue
                m = _COST_RE.match(line)
                if m:
                    cost = float(m.group(1))
                    sc.usd += cost
                    sc.agent_calls += 1
                    sc.measured = True
                    if cur_model:
                        sc.models.add(cur_model)
                    sc.breakdown.append((cur_handler or "host.agent.?", cost))
        if rec_none:
            sc.recorded = False
    return sc


# --- denominator: the raw agentic baseline, from real telemetry ---------------

@dataclass
class OpStat:
    """The real cost distribution of one operation across the mined corpus."""

    name: str
    costs: list = field(default_factory=list)        # per-turn USD
    reproc: list = field(default_factory=list)        # per-turn cache-read tokens
    rewarm_usd: list = field(default_factory=list)    # cold re-warm $ on resumed turns
    sessions: set = field(default_factory=set)
    models: set = field(default_factory=set)
    exact: bool = True

    def n(self) -> int:
        return len(self.costs)


def _pct(xs: list, q: float) -> float:
    """Nearest-rank percentile (q in [0,1]); stdlib-only, robust on tiny samples."""
    if not xs:
        return 0.0
    s = sorted(xs)
    if len(s) == 1:
        return s[0]
    idx = int(round(q * (len(s) - 1)))
    return s[idx]


def _median(xs: list) -> float:
    return _pct(xs, 0.5)


@dataclass
class Baseline:
    profile: dict
    transcripts: int = 0
    sessions_matched: int = 0
    ops: dict = field(default_factory=dict)          # op_name -> OpStat
    all_op_costs: list = field(default_factory=list)  # every matching turn's $
    exact: bool = True

    def total_n(self) -> int:
        return len(self.all_op_costs)


def _is_synthetic_model(m: str) -> bool:
    """A fixture/placeholder model id (recorded test cassettes, synthetic corpora)
    — NOT a real priced API call. Dropped from the raw baseline so committed test
    telemetry can't masquerade as real agentic cost. Real-but-unpriced models
    (e.g. a new tier) are kept and flagged inexact, not dropped."""
    m = (m or "").strip().lower()
    return not m or m.startswith("<") or "synthetic" in m or "mock" in m


def _op_probes(profile: dict) -> list:
    """The per-operation probes: the profile's grep words are the operation
    vocabulary (rebase, git commit, conflict, ...). Each matching turn is bucketed
    to the FIRST probe it matches, so the per-intent table mirrors the story's
    surface. Falls back to a single 'all' bucket if no grep is declared."""
    scope = profile.get("scope", {})
    probes = [p for p in scope.get("grep", []) if p and p.strip()]
    return probes or []


def baseline_from_corpus(profile: dict, project_globs: list,
                         min_bytes: int = 30000) -> Baseline:
    """Scope real transcripts by the story's profile (same grep prefilter mining
    uses, agent/agent sessions dropped), find the user turns that did the story's
    operations, and collect their real cost distribution. No LLM, exact pricing."""
    bl = Baseline(profile=profile)
    probes = _op_probes(profile)
    grep_words = probes[:]  # raw-jsonl prefilter words = the operation vocabulary
    for op in probes:
        bl.ops[op] = OpStat(name=op)

    paths = []
    for g in project_globs:
        for d in sorted(glob.glob(os.path.expanduser(g))):
            if os.path.isdir(d):
                paths.extend(sorted(glob.glob(os.path.join(d, "*.jsonl"))))
    # cheap raw-substring prefilter + drop dispatched agent/agent transcripts
    # (mining them back in is self-cannibalism — same rule as prep.py).
    for p in paths:
        try:
            if os.path.getsize(p) < min_bytes:
                continue
        except OSError:
            continue
        if grep_words and not prep.grep_match(p, grep_words):
            continue
        if prep.is_agent_session(p):
            continue
        bl.transcripts += 1
        try:
            sc = cost_extract.extract(p)
        except (OSError, ValueError):
            continue
        matched_here = False
        for t in sc.turns:
            hay = (t.user_text + " " + " ".join(t.commands)).lower()
            bucket = next((op for op in probes if op.lower() in hay), None)
            if bucket is None:
                continue
            # drop fixture/synthetic telemetry — only real priced calls are baseline.
            # synthetic harness messages carry zero usage (no cost), so a turn that
            # is ALL-synthetic represents no real agentic spend.
            real_models = {m for m in t.models if not _is_synthetic_model(m)}
            if t.models and not real_models:
                continue
            matched_here = True
            st = bl.ops[bucket]
            st.costs.append(t.cost_usd)
            st.reproc.append(t.cache_read)
            st.sessions.add(os.path.basename(sc.path))
            st.models |= real_models
            st.exact = st.exact and t.exact
            if t.rewarm_usd > 0:
                st.rewarm_usd.append(t.rewarm_usd)
            bl.all_op_costs.append(t.cost_usd)
            bl.exact = bl.exact and t.exact
        if matched_here:
            bl.sessions_matched += 1
    return bl


# --- report -------------------------------------------------------------------

def _usd(x: float) -> str:
    return cost_extract.fmt_usd(x)


def _reproc_share(bl: Baseline) -> float:
    reproc = sum(sum(st.reproc) for st in bl.ops.values())
    # approximate total input as reproc + (we don't keep fresh-input per op here);
    # the case study reports the share from cost_extract directly. Here we report
    # the absolute re-read tokens, which is the honest, available figure.
    return reproc


def render_story(name: str, sc: StoryCost, bl: Baseline) -> list:
    L = []
    L.append("## %s" % name)
    L.append("")

    # numerator
    if sc.measured:
        flag = "" if sc.recorded else \
            "  ⚠ authored (record_mode `none` cassette — record live to confirm)"
        parts = ", ".join("%s %s" % (h, _usd(c)) for h, c in sc.breakdown)
        L.append("- **Story cost (deterministic):** %s over %d agent call(s) "
                 "— %s; model(s) %s.%s"
                 % (_usd(sc.usd), sc.agent_calls, parts,
                    sorted(sc.models) or "?", flag))
    else:
        L.append("- **Story cost (deterministic):** _not yet measured_ — no host "
                 "cassette with agent `cost_usd` under "
                 "`stories/%s/flows/cassettes/`. Deterministic steps are $0; record "
                 "the story's agent cassette to capture the rest." % name)

    # denominator
    if bl.total_n() == 0:
        L.append("- **Raw agentic baseline:** no matching operations found in the "
                 "transcript pool (%d transcripts scanned). Widen `--projects` or the "
                 "profile `scope.grep`." % bl.transcripts)
        L.append("")
        return L
    med = _median(bl.all_op_costs)
    p90 = _pct(bl.all_op_costs, 0.9)
    raw_models = sorted({m for st in bl.ops.values() for m in st.models})
    L.append("- **Raw agentic baseline (real telemetry):** %d matching operation(s) "
             "across %d session(s) of %d scanned. Per-operation cost: median **%s**, "
             "p90 **%s**, max %s. Model(s) %s.%s"
             % (bl.total_n(), bl.sessions_matched, bl.transcripts,
                _usd(med), _usd(p90), _usd(max(bl.all_op_costs)),
                raw_models, "" if bl.exact else " [* some fallback-priced]"))
    reproc = _reproc_share(bl)
    L.append("  - reprocessing tax: %s tokens re-read across these operations just "
             "to carry the prior conversation forward (the cost the story avoids)."
             % ("{:,}".format(reproc)))
    rewarms = [r for st in bl.ops.values() for r in st.rewarm_usd]
    if rewarms:
        L.append("  - cold resumes: %d matching operation(s) followed a >1h break; "
                 "median re-warm **%s** paid before the operation ran." %
                 (len(rewarms), _usd(_median(rewarms))))

    # savings
    if sc.measured:
        if sc.usd > 0:
            ratio = med / sc.usd
            L.append("- **Savings (median operation):** %s → %s = **%s** per "
                     "operation (≈ **%.0f×** cheaper), flat in session length."
                     % (_usd(med), _usd(sc.usd), _usd(med - sc.usd), ratio))
        else:
            L.append("- **Savings (median operation):** %s → $0 (fully "
                     "deterministic story)." % _usd(med))
        # model-mix lever
        opus = [m for m in raw_models if "opus" in m]
        sonnet_agent = [m for m in sc.models if "sonnet" in m]
        if opus and sonnet_agent:
            op, _ = pricing.price_for(opus[0])
            so, _ = pricing.price_for(sonnet_agent[0])
            lever = op.input / so.input if so.input else 0
            L.append("- **Model-mix lever:** raw ops run on %s; the story's agent "
                     "needs only %s (~%.0f× cheaper/token). The deterministic "
                     "boundary is what lets the cheaper model suffice."
                     % (opus[0], sonnet_agent[0], lever))

    # per-intent distribution
    rows = [(op, st) for op, st in bl.ops.items() if st.n() > 0]
    if rows:
        L.append("")
        L.append("  | operation (intent probe) | n | median | p90 | sessions | model(s) |")
        L.append("  |---|--:|--:|--:|--:|---|")
        for op, st in sorted(rows, key=lambda r: -_median(r[1].costs)):
            L.append("  | `%s` | %d | %s | %s | %d | %s |"
                     % (op, st.n(), _usd(_median(st.costs)),
                        _usd(_pct(st.costs, 0.9)), len(st.sessions),
                        ", ".join(sorted(st.models)) or "?"))
    L.append("")
    return L


def discover_stories() -> list:
    out = []
    for d in sorted(glob.glob(os.path.join(STORIES_DIR, "*", "mining.profile.yaml"))):
        out.append(os.path.basename(os.path.dirname(d)))
    return out


def build_report(stories: list, project_globs: list) -> str:
    L = []
    L.append("<!-- GENERATED by tools/session-mining/cost_report.py — do not edit by hand. -->")
    L.append("# Per-story cost report")
    L.append("")
    L.append("Auto-generated savings per story: the **deterministic story cost** "
             "(agent spend from the story's host cassettes; every routed/host step "
             "is $0) versus the **raw agentic cost** of the same operations in real "
             "Claude Code sessions (real telemetry, exact-priced). The reusable form "
             "of [docs/case-studies/git-ops-cost.md](../../docs/case-studies/git-ops-cost.md); "
             "see it for the reprocessing-tax mechanism this measures.")
    L.append("")
    L.append("_No LLM, no cost: reads `message.usage` already on disk via "
             "`cost_extract.py` + `pricing.py`. Raw baseline scoped per story by "
             "`mining.profile.yaml`'s grep prefilter; dispatched agent/agent "
             "sessions dropped (self-cannibalism)._")
    L.append("")

    computed = []  # (name, StoryCost, Baseline)
    for name in stories:
        story_dir = os.path.join(STORIES_DIR, name)
        prof_path = os.path.join(story_dir, "mining.profile.yaml")
        if not os.path.exists(prof_path):
            continue
        profile = coverage_prep.load_profile(prof_path)
        sc = read_story_cost(story_dir)
        bl = baseline_from_corpus(profile, project_globs)
        computed.append((name, sc, bl))

    # summary table
    L.append("## Summary")
    L.append("")
    L.append("| story | story cost | raw median/op | raw p90/op | savings/op | ops sampled |")
    L.append("|---|--:|--:|--:|--:|--:|")
    for name, sc, bl in computed:
        story_c = _usd(sc.usd) if sc.measured else "—"
        if bl.total_n():
            med, p90 = _median(bl.all_op_costs), _pct(bl.all_op_costs, 0.9)
            med_s, p90_s = _usd(med), _usd(p90)
            sav = _usd(med - sc.usd) if sc.measured else "—"
        else:
            med_s = p90_s = sav = "—"
        L.append("| [%s](#%s) | %s | %s | %s | %s | %d |"
                 % (name, name, story_c, med_s, p90_s, sav, bl.total_n()))
    L.append("")

    for name, sc, bl in computed:
        L.extend(render_story(name, sc, bl))

    return "\n".join(L) + "\n"


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--story", help="one story name (dir under stories/)")
    g.add_argument("--all", action="store_true",
                   help="every story with a mining.profile.yaml")
    ap.add_argument("--projects", action="append", default=[],
                    help="transcript dir glob(s) (repeatable). Default: this repo's "
                         "~/.claude/projects/<slug>* family.")
    ap.add_argument("--out", help="write markdown here (default: stdout)")
    args = ap.parse_args(argv)

    project_globs = args.projects or [DEFAULT_PROJECTS]
    if args.all:
        stories = discover_stories()
    else:
        stories = [args.story]
        if not os.path.exists(os.path.join(STORIES_DIR, args.story,
                                            "mining.profile.yaml")):
            print("no stories/%s/mining.profile.yaml" % args.story, file=sys.stderr)
            return 2

    md = build_report(stories, project_globs)
    if args.out:
        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
        with open(args.out, "w") as fh:
            fh.write(md)
        print("wrote %s (%d stories)" % (args.out, len(stories)), file=sys.stderr)
    else:
        sys.stdout.write(md)
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Estimate what the git-ops demo operations would have cost as *raw Claude Code
agentic sessions*, and compare against Kitsoki's deterministic story cost.

Why this exists
---------------
The git-ops demo replays four operations a developer actually drove in recorded
Claude Code sessions (commit, rebase-with-conflict, merge, worktree). In Kitsoki
those run as a deterministic story: routing/branch-detection/git are free, and
the only spend is two genuine agent calls (~$0.10 total, read from the committed
host cassette). The natural question a viewer asks is: *what would the same work
have cost if I'd just done it in Claude Code?*

That number isn't in the mined transcripts — they're redacted, and they carry no
usage telemetry. But the transcripts DO pin the one thing that dominates agentic
cost: the **shape** of the session (how many assistant API calls, how many
tool round-trips). Claude Code re-sends the entire growing conversation — plus a
large system prompt and tool schemas — on *every* assistant call. So cost grows
super-linearly with turns, and it scales with how big your conversation already
was before you asked. This script models exactly that.

What it is honest about
-----------------------
* The transcript content is a FLOOR. Real tool_results (git diffs, full file
  reads during conflict resolution) are far larger than the redacted stubs, so
  every tool_result is inflated to a configurable realistic floor.
* The base system-prompt + tool-schema overhead (~18k tok) is re-sent every call
  and is the single biggest driver for short operations — it's a knob.
* "How early/big the conversation is before it" is the --prior-context sweep.
* "Whether we're cached or not" is the warm/cold spread: WARM is the cache-read
  floor (5-min prompt cache holds the stable prefix), COLD is the no-cache
  ceiling. The real bill for any given run lands inside that band.

So the output is a RANGE, not a false-precision point estimate — and the Kitsoki
side is committed ground truth, not a guess.

Pricing: Claude Sonnet 4.x list price (the Claude Code default and the demo's
agent model), USD per 1M tokens, as of 2026-06. Override with --price-* if it
drifts. Token counts are a chars-per-token heuristic (no network, no tokenizer
dep); --chars-per-token tunes it. None of these knobs change the qualitative
story — the deterministic engine is ~100x cheaper because it doesn't re-send a
conversation to a model to decide what `git` already knows.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import pricing  # noqa: E402  (shared authoritative price table)
DEFAULT_RAW = HERE / "examples" / "git-ops" / "raw"
DEFAULT_CASSETTE = (
    HERE.parent.parent
    / "stories"
    / "git-ops"
    / "flows"
    / "cassettes"
    / "demo_agent.cassette.yaml"
)

# The four operations the demo replays, in lifecycle order, mapped to their
# mined session transcripts. These are exactly the sessions the tour drives.
DEMO_SESSIONS = [
    ("commit the staged fix", "sess-commit-happy"),
    ("rebase onto main + resolve conflicts", "sess-rebase-conflict"),
    ("merge the feature branch into main", "sess-merge-direct"),
    ("set up a worktree for the new feature", "sess-worktree"),
]

# Price defaults come from the shared table (pricing.py) so there is exactly one
# place to update rates. This estimator models the SYNTHETIC/redacted demo corpus
# (no telemetry); for REAL transcripts use cost_extract.py, which reads recorded
# usage and needs no chars/token or cold/warm modelling at all. We default to the
# Sonnet tier here because the demo's agent model is Sonnet, but real Claude Code
# coding sessions usually run on the pricier Opus tier (see the case study).
_P = pricing.PRICING["claude-sonnet-4"]
DEFAULTS = dict(
    price_in=_P.input,            # fresh input
    price_out=_P.output,          # output
    price_cache_write=_P.cache_write_5m,  # cache write (5m) = 1.25x input
    price_cache_read=_P.cache_read,       # cache read = 0.10x input
    chars_per_token=3.8,     # English+code heuristic
    base_tokens=18000,       # system prompt + tool schemas, re-sent every call
    tool_result_floor=450,   # realistic min for a git/file tool result
    msg_overhead=8,          # structural tokens per message
)


@dataclass
class Call:
    """One assistant API call: the model saw `input` context, emitted `output`."""

    input: int
    output: int
    # delta = tokens added since the previous call (what a warm cache must write).
    delta: int


@dataclass
class Session:
    sid: str
    label: str
    calls: list[Call] = field(default_factory=list)

    @property
    def n_calls(self) -> int:
        return len(self.calls)


def toks(text: str, cpt: float) -> int:
    return max(1, round(len(text) / cpt))


def content_tokens(content, cpt: float, overhead: int) -> int:
    """Estimate tokens for a Claude Code message `content` (string or block list)."""
    if isinstance(content, str):
        return toks(content, cpt) + overhead
    total = overhead
    for block in content or []:
        btype = block.get("type")
        if btype == "text":
            total += toks(block.get("text", ""), cpt)
        elif btype == "tool_use":
            # name + serialized input args (the command / edit payload).
            total += toks(block.get("name", ""), cpt)
            total += toks(json.dumps(block.get("input", {})), cpt)
            total += 6  # tool_use envelope
        elif btype == "tool_result":
            c = block.get("content", "")
            if not isinstance(c, str):
                c = json.dumps(c)
            total += toks(c, cpt)
        else:
            total += toks(json.dumps(block), cpt)
    return total


def is_tool_result(content) -> bool:
    return (
        isinstance(content, list)
        and any(b.get("type") == "tool_result" for b in content)
    )


def build_session(sid: str, label: str, path: Path, args) -> Session:
    """Walk the transcript, accumulating the conversation the way Claude Code
    bills it: every assistant message is an API call that sees the full prefix."""
    cpt = args.chars_per_token
    running = args.base_tokens + args.prior_context  # re-sent on every call
    last_call_input = 0  # cumulative context at the previous assistant call
    sess = Session(sid=sid, label=label)

    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        rec = json.loads(line)
        content = rec.get("message", {}).get("content")
        if rec.get("type") == "assistant":
            out = content_tokens(content, cpt, args.msg_overhead)
            delta = running - last_call_input  # new since the prior call
            sess.calls.append(Call(input=running, output=out, delta=delta))
            last_call_input = running
            running += out  # the assistant's reply joins the transcript
        else:
            # user turn or tool_result: inflate redacted results to a real floor.
            t = content_tokens(content, cpt, args.msg_overhead)
            if is_tool_result(content):
                t = max(t, args.tool_result_floor)
            running += t
    return sess


def cost_cold(sess: Session, args) -> float:
    """No caching: every call pays full input price on the entire prefix."""
    c = 0.0
    for call in sess.calls:
        c += call.input * args.price_in / 1e6
        c += call.output * args.price_out / 1e6
    return c


def cost_warm(sess: Session, args) -> float:
    """5-min prompt cache: stable prefix is cache-read, only the per-call delta
    is cache-written. The first call writes its whole prefix."""
    c = 0.0
    for i, call in enumerate(sess.calls):
        if i == 0:
            c += call.input * args.price_cache_write / 1e6
        else:
            read = call.input - call.delta
            c += read * args.price_cache_read / 1e6
            c += call.delta * args.price_cache_write / 1e6
        c += call.output * args.price_out / 1e6
    return c


def read_kitsoki_cost(cassette: Path) -> tuple[float, list[tuple[str, float]]]:
    """Sum the committed agent cost_usd values — Kitsoki's real deterministic
    spend for these operations (the only paid surface in the story)."""
    if not cassette.exists():
        return 0.0, []
    text = cassette.read_text()
    items = []
    # pair each handler with the next cost_usd in file order.
    handlers = re.findall(r"handler:\s*(\S+)", text)
    costs = [float(m) for m in re.findall(r"cost_usd:\s*([\d.]+)", text)]
    for h, cost in zip(handlers, costs):
        items.append((h, cost))
    return sum(costs), items


def fmt_usd(x: float) -> str:
    return f"${x:,.4f}" if x < 1 else f"${x:,.2f}"


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--raw", type=Path, default=DEFAULT_RAW,
                   help="dir of mined session .jsonl transcripts")
    p.add_argument("--cassette", type=Path, default=DEFAULT_CASSETTE,
                   help="committed host cassette (Kitsoki ground-truth cost)")
    p.add_argument("--prior-context", type=int, default=0,
                   help="tokens of pre-existing conversation re-sent every call "
                        "(single run; --sweep overrides for the matrix)")
    p.add_argument("--sweep", type=str,
                   default="0,25000,50000,100000,200000",
                   help="comma list of --prior-context values for the matrix")
    p.add_argument("--markdown", type=Path,
                   help="also write the report as markdown to this path")
    for k, v in DEFAULTS.items():
        p.add_argument(f"--{k.replace('_', '-')}", type=type(v), default=v)
    args = p.parse_args()

    kit_total, kit_items = read_kitsoki_cost(args.cassette)
    sweep = [int(x) for x in args.sweep.split(",") if x.strip()]

    lines: list[str] = []

    def out(s: str = "") -> None:
        print(s)
        lines.append(s)

    out("# git-ops: raw Claude Code cost vs Kitsoki deterministic story")
    out()
    out("Model: Sonnet 4.x list price "
        f"(in ${args.price_in}/Mtok, out ${args.price_out}/Mtok, "
        f"cache write ${args.price_cache_write}, read ${args.price_cache_read}). "
        f"Base system+tools {args.base_tokens:,} tok/call; "
        f"tool-result floor {args.tool_result_floor} tok; "
        f"~{args.chars_per_token} chars/token.")
    out()

    # --- Kitsoki side: committed ground truth ---------------------------------
    out("## Kitsoki deterministic story (committed ground truth)")
    out()
    out("| paid surface | model | cost |")
    out("|---|---|---|")
    for h, cost in kit_items:
        out(f"| `{h}` | claude-sonnet-4-6 | {fmt_usd(cost)} |")
    out(f"| **everything else** (routing, branch-detect, all git, worktree) "
        f"| — deterministic, no LLM | **$0.0000** |")
    out(f"| **TOTAL (4 operations)** | | **{fmt_usd(kit_total)}** |")
    out()
    out("Routing every typed utterance, staging, each merge guard and the whole "
        "worktree lifecycle are deterministic transitions + real git — they cost "
        "nothing. Only the commit-message draft and the two-file conflict "
        "resolution are work a model must do.")
    out()

    # --- Per-session structure (verifiable from the transcript) ---------------
    sessions0 = {
        sid: build_session(sid, label,
                           args.raw / f"{sid}.jsonl",
                           _with_prior(args, 0))
        for label, sid in DEMO_SESSIONS
    }
    out("## Session shape (mined, verifiable)")
    out()
    out("| operation | assistant API calls | context at final call (tok)¹ |")
    out("|---|---|---|")
    for label, sid in DEMO_SESSIONS:
        s = sessions0[sid]
        final_ctx = s.calls[-1].input if s.calls else 0
        out(f"| {label} | {s.n_calls} | {final_ctx:,} |")
    out()
    out("¹ at --prior-context=0: base system+tools + the turns so far. Each call "
        "re-sends this whole prefix; that's why short agentic ops still cost real "
        "money, and why cost climbs as the session grows.")
    out()

    # --- The matrix: cost as conversation size grows, warm..cold band ---------
    out("## Estimated raw Claude Code cost — the comparison")
    out()
    out("Each cell is the **4-operation total**. Range = warm-cache floor → "
        "cold-cache ceiling; your real bill lands in the band depending on cache "
        "hits. Columns = how big your conversation already was before you asked.")
    out()
    header = "| scenario | " + " | ".join(
        f"+{c // 1000}k prior" if c else "fresh session" for c in sweep
    ) + " |"
    out(header)
    out("|" + "---|" * (len(sweep) + 1))

    warm_totals = {c: 0.0 for c in sweep}
    cold_totals = {c: 0.0 for c in sweep}
    for c in sweep:
        a = _with_prior(args, c)
        for label, sid in DEMO_SESSIONS:
            s = build_session(sid, label, a.raw / f"{sid}.jsonl", a)
            warm_totals[c] += cost_warm(s, a)
            cold_totals[c] += cost_cold(s, a)

    band = "| Claude Code (warm→cold) | " + " | ".join(
        f"{fmt_usd(warm_totals[c])} – {fmt_usd(cold_totals[c])}" for c in sweep
    ) + " |"
    out(band)
    kit_row = "| **Kitsoki story (actual)** | " + " | ".join(
        f"**{fmt_usd(kit_total)}**" for _ in sweep
    ) + " |"
    out(kit_row)
    mult = "| _multiple_ | " + " | ".join(
        (f"{warm_totals[c] / kit_total:,.0f}× – {cold_totals[c] / kit_total:,.0f}×"
         if kit_total else "—") for c in sweep
    ) + " |"
    out(mult)
    out()
    out("Kitsoki's cost is flat across columns because the deterministic engine "
        "never re-sends a conversation to a model — prior context doesn't inflate "
        "it. Raw Claude Code pays to re-read everything, every call, every "
        "operation. The deterministic engine scales for free; you pay for "
        "judgment, not plumbing.")
    out()

    if args.markdown:
        args.markdown.parent.mkdir(parents=True, exist_ok=True)
        args.markdown.write_text("\n".join(lines) + "\n")
        print(f"\n[wrote {args.markdown}]")


def _with_prior(args, prior: int):
    """Clone args with a specific prior-context (cheap namespace copy)."""
    import copy

    a = copy.copy(args)
    a.prior_context = prior
    return a


if __name__ == "__main__":
    main()

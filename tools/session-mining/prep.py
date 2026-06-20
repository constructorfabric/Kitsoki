#!/usr/bin/env python3
"""prep.py — distill a project's Claude Code transcripts and bin-pack them into
byte-balanced batches, in one command.

This is the seam shared by both mining modes:

  * focused idea-mining (local, not shared) — run WITHOUT --redact, then fan out
    reader agents over the batches (see the `session-idea-mining` skill).
  * pattern-mining (shareable) — run WITH --redact so the model never sees raw
    content, then run the extractor (see README "Quickstart").

It replaces the hand-rolled `for f in $(ls -S ...); do jq ... done` loop plus the
ad-hoc batching python: filter -> distill -> (optional redact) -> drop empties ->
first-fit-decreasing bin-pack -> manifest.

Output layout (under --out, default /tmp/sm-<tag>):
    traces/<session-id>.txt        one distilled (optionally redacted) trace each
    batches/batch-NN.txt           newline-delimited absolute trace paths, ~--budget bytes each
    manifest.json                  params + counts + per-batch sizes + real-cost summary
    costs.json                     REAL per-session cost from each source jsonl's
                                   recorded message.usage (cost_extract.py). The
                                   distilled trace drops telemetry; this captures it
                                   from the source before it's lost, so every mining
                                   run carries the genuine $ it cost to produce —
                                   "real costs for real comparisons" for everything
                                   mined. Empty/zero for synthetic corpora with no usage.

Stdout ends with `BATCHES=<n>` and `BATCHDIR=<path>` so a caller (skill/workflow)
can read how many reader agents to spawn.

stdlib only; python3.9+. Requires `jq` on PATH (for distill.jq).
"""
import argparse
import json
import os
import subprocess
import sys
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import cost_extract  # noqa: E402  (real-cost capture from source telemetry)

DISTILL = os.path.join(HERE, "distill.jq")
REDACT = os.path.join(HERE, "redact.py")


def eprint(*a):
    print(*a, file=sys.stderr)


def list_sessions(proj):
    fs = [os.path.join(proj, f) for f in os.listdir(proj) if f.endswith(".jsonl")]
    # sort by size desc by default; --sample recency re-sorts below
    fs.sort(key=lambda p: -os.path.getsize(p))
    return fs


def grep_match(path, words):
    """True if the raw jsonl contains any of `words` (cheap substring scan)."""
    if not words:
        return True
    try:
        with open(path, "r", errors="ignore") as fh:
            blob = fh.read()
    except OSError:
        return False
    return any(w in blob for w in words)


# Claude Code stamps every user record with `entrypoint`, recording HOW the session
# was launched. Interactive human sessions are "cli"; sessions dispatched headlessly
# (a `claude -p` agent/subagent — UI-QA reviewer, adversarial verifier, etc.) are
# "sdk-cli" / "sdk". Mining the latter is self-cannibalism (the model eating its own
# agent output), so we classify on this structural origin signal — NOT on prompt
# content, which would be brittle and overfit to today's agent wording.
HUMAN_ENTRYPOINT = "cli"


def session_entrypoint(path):
    """The session's launch origin from its first user record's `entrypoint`:
    'cli' = interactive human, 'sdk-cli'/'sdk' = dispatched headless agent. None if
    undeterminable. Falls back to promptSource=='sdk' when entrypoint is absent."""
    try:
        fh = open(path, "r", errors="ignore")
    except OSError:
        return None
    with fh:
        for raw in fh:
            try:
                obj = json.loads(raw)
            except (json.JSONDecodeError, ValueError):
                continue
            if obj.get("type") != "user":
                continue
            ep = obj.get("entrypoint")
            if ep:
                return ep
            if obj.get("promptSource") == "sdk":
                return "sdk"
    return None


def is_agent_session(path):
    """True if the session was dispatched headlessly (an agent/agent), not authored
    by a human at the interactive CLI. Structural, content-independent."""
    ep = session_entrypoint(path)
    return ep is not None and ep != HUMAN_ENTRYPOINT


def distill_one(src, dst, redact):
    """Distill src -> dst via distill.jq, optionally piping through redact.py.
    Returns dst size in bytes, or -1 on failure."""
    try:
        jq = subprocess.run(["jq", "-r", "-f", DISTILL, src],
                            capture_output=True, text=True)
        if jq.returncode != 0:
            return -1
        out = jq.stdout
        if redact:
            rd = subprocess.run([sys.executable, REDACT], input=out,
                                capture_output=True, text=True)
            if rd.returncode != 0:
                return -1
            out = rd.stdout
        with open(dst, "w") as fh:
            fh.write(out)
        return os.path.getsize(dst)
    except OSError:
        return -1


def binpack(traces, budget):
    """First-fit-decreasing bin-pack [(path,size)] into bins <= budget bytes.
    A single trace larger than budget gets its own bin."""
    traces = sorted(traces, key=lambda t: -t[1])
    bins = []  # [total, [paths]]
    for path, size in traces:
        placed = False
        for b in bins:
            if b[0] + size <= budget:
                b[0] += size
                b[1].append(path)
                placed = True
                break
        if not placed:
            bins.append([size, [path]])
    return bins


def main():
    ap = argparse.ArgumentParser(
        description="Distill + bin-pack Claude Code transcripts into batches.")
    ap.add_argument("project_dir",
                    help="~/.claude/projects/<slug> — one dir per repo")
    ap.add_argument("--out", default=None,
                    help="output dir (default /tmp/sm-<basename-of-project>)")
    ap.add_argument("--job", default=None,
                    help="intent-mining job name. When set (and --out is not), "
                         "output goes to .artifacts/session-mining/<job>/ at the "
                         "repo root — the local, gitignored tier the intent-mining "
                         "mode uses. Does NOT change the /tmp default the other "
                         "two modes rely on.")
    ap.add_argument("--min-bytes", type=int, default=30000,
                    help="skip raw sessions smaller than this (default 30000)")
    ap.add_argument("--grep", action="append", default=[], metavar="WORD",
                    help="keep only sessions whose raw jsonl contains WORD "
                         "(repeatable; OR semantics). Cheap topical prefilter.")
    ap.add_argument("--sample", choices=["size", "recency", "all"], default="all",
                    help="which sessions to take after filtering (default all). "
                         "size=largest first, recency=newest first.")
    ap.add_argument("--max", type=int, default=0,
                    help="cap number of sessions after sampling (0 = no cap)")
    ap.add_argument("--budget", type=int, default=200000,
                    help="target bytes per batch (default 200000)")
    ap.add_argument("--redact", action="store_true",
                    help="pipe each trace through redact.py (REQUIRED for the "
                         "shareable pattern-mining mode; omit for local idea-mining)")
    ap.add_argument("--min-trace", type=int, default=200,
                    help="drop distilled traces smaller than this (near-empty)")
    ap.add_argument("--keep-agent-sessions", action="store_true",
                    help="KEEP dispatched headless agent/agent transcripts "
                         "(entrypoint!=cli). They are dropped by default — mining "
                         "the model's own agent output back in is self-cannibalism.")
    args = ap.parse_args()

    proj = os.path.abspath(os.path.expanduser(args.project_dir))
    if not os.path.isdir(proj):
        eprint("error: project dir not found:", proj)
        return 2
    if not shutil.which("jq"):
        eprint("error: `jq` not on PATH")
        return 2

    if args.out:
        out = args.out
    elif args.job:
        # repo root = two levels up from tools/session-mining/
        repo_root = os.path.abspath(os.path.join(HERE, "..", ".."))
        out = os.path.join(repo_root, ".artifacts", "session-mining", args.job)
    else:
        out = os.path.join("/tmp", "sm-" + os.path.basename(proj.rstrip("/")))
    traces_dir = os.path.join(out, "traces")
    batches_dir = os.path.join(out, "batches")
    if os.path.isdir(out):
        shutil.rmtree(out)
    os.makedirs(traces_dir)
    os.makedirs(batches_dir)

    sessions = list_sessions(proj)
    sessions = [s for s in sessions if os.path.getsize(s) >= args.min_bytes]
    if args.grep:
        sessions = [s for s in sessions if grep_match(s, args.grep)]
    # Drop dispatched headless agent/agent transcripts (entrypoint != cli) by
    # default — they are the model's own subagent output, not human sessions, and
    # mining them is self-cannibalism. Structural signal, not prompt-content match.
    agent_dropped = []
    if not args.keep_agent_sessions:
        kept = []
        for s in sessions:
            if is_agent_session(s):
                agent_dropped.append(os.path.basename(s)[:-len(".jsonl")])
            else:
                kept.append(s)
        sessions = kept
    if args.sample == "recency":
        sessions.sort(key=lambda p: -os.path.getmtime(p))
    elif args.sample == "size":
        sessions.sort(key=lambda p: -os.path.getsize(p))
    if args.max > 0:
        sessions = sessions[:args.max]

    if agent_dropped:
        eprint("dropped %d dispatched agent/agent session(s) (entrypoint!=cli); "
               "use --keep-agent-sessions to include them" % len(agent_dropped))
    eprint("candidate sessions:", len(sessions),
           "(min-bytes=%d, grep=%s, sample=%s, max=%s)" %
           (args.min_bytes, args.grep or "-", args.sample, args.max or "-"))

    traces = []
    costs = []  # real per-session cost, captured from source telemetry
    failed = 0
    dropped = 0
    for src in sessions:
        sid = os.path.basename(src)[:-len(".jsonl")]
        dst = os.path.join(traces_dir, sid + ".txt")
        sz = distill_one(src, dst, args.redact)
        if sz < 0:
            failed += 1
            if os.path.exists(dst):
                os.remove(dst)
            continue
        if sz < args.min_trace:
            dropped += 1
            os.remove(dst)
            continue
        traces.append((dst, sz))
        # Capture the REAL cost from the source jsonl's recorded usage before the
        # distilled trace (telemetry-free) is all that's left. Never raises — a
        # synthetic/usage-free source simply yields $0 (exact=True, calls=0).
        try:
            sc = cost_extract.extract(src)
            costs.append(cost_extract._session_json(sc))
        except (OSError, ValueError):
            pass

    bins = binpack(traces, args.budget)
    for i, (tot, paths) in enumerate(bins, 1):
        with open(os.path.join(batches_dir, "batch-%02d.txt" % i), "w") as fh:
            fh.write("\n".join(paths) + "\n")

    total_bytes = sum(s for _, s in traces)

    # Real-cost sidecar: per-session genuine $ + an aggregate, from recorded usage.
    cost_total = round(sum(c["cost_usd"] for c in costs), 6)
    cost_calls = sum(c["api_calls"] for c in costs)
    cost_priced = sorted({m for c in costs for m in c["models"]})
    cost_exact = all(c["exact"] for c in costs)
    costs_doc = {
        "note": "REAL cost from each source transcript's recorded message.usage, "
                "via cost_extract.py + pricing.py. costUSD-null (subscription) "
                "sources are computed from token counts. $0 == no telemetry "
                "(synthetic corpus).",
        "total_cost_usd": cost_total,
        "total_api_calls": cost_calls,
        "models": cost_priced,
        "all_exact": cost_exact,
        "sessions": sorted(costs, key=lambda c: -c["cost_usd"]),
    }
    with open(os.path.join(out, "costs.json"), "w") as fh:
        json.dump(costs_doc, fh, indent=2)

    manifest = {
        "project_dir": proj,
        "out": out,
        "params": {
            "min_bytes": args.min_bytes, "grep": args.grep, "sample": args.sample,
            "max": args.max, "budget": args.budget, "redacted": args.redact,
            "min_trace": args.min_trace,
            "keep_agent_sessions": args.keep_agent_sessions,
        },
        "traces": len(traces),
        "distill_failed": failed,
        "dropped_empty": dropped,
        "agent_sessions_dropped": agent_dropped,
        "total_trace_bytes": total_bytes,
        "real_cost": {"total_usd": cost_total, "api_calls": cost_calls,
                      "models": cost_priced, "all_exact": cost_exact,
                      "sidecar": "costs.json"},
        "batches": [{"file": "batches/batch-%02d.txt" % i,
                     "traces": len(p), "bytes": t}
                    for i, (t, p) in enumerate(bins, 1)],
    }
    with open(os.path.join(out, "manifest.json"), "w") as fh:
        json.dump(manifest, fh, indent=2)

    eprint("distilled %d traces (%d KB), %d failed, %d near-empty dropped" %
           (len(traces), total_bytes // 1000, failed, dropped))
    if cost_total > 0:
        eprint("real cost of mined sessions: $%.2f across %d API calls (costs.json)%s"
               % (cost_total, cost_calls, "" if cost_exact else " [some fallback-priced]"))
    eprint("%d batches of ~%d KB:" % (len(bins), args.budget // 1000))
    for i, (t, p) in enumerate(bins, 1):
        eprint("  batch-%02d: %d traces, %d KB" % (i, len(p), t // 1000))
    if args.redact:
        eprint("traces REDACTED — safe for the model in shareable pattern-mining.")
    else:
        eprint("traces NOT redacted — keep local (idea-mining mode).")

    # machine-readable tail for callers (skill/workflow)
    print("BATCHES=%d" % len(bins))
    print("BATCHDIR=%s" % batches_dir)
    print("MANIFEST=%s" % os.path.join(out, "manifest.json"))
    return 0


if __name__ == "__main__":
    sys.exit(main())

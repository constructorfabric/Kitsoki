#!/usr/bin/env python3
"""codex_prep.py — distill + bin-pack codex CLI/TUI rollouts into the SAME
traces/<sid>.txt + batches/batch-NN.txt + manifest.json shape prep.py produces
for Claude Code, so ground.py/tag_score.py/emit.py/redact.py run UNCHANGED
against a codex-sourced job. See codex_adapter.py for the parsing seam this
wraps, and README "codex adapter" for the end-to-end recipe.

Differences from prep.py, each driven by the real corpus shape (confirmed by
reading rollouts before writing this, not guessed):

  * codex rollouts live nested by date under `~/.codex/sessions/YYYY/MM/DD/
    rollout-*.jsonl`, not flat-per-project like `~/.claude/projects/<slug>/` —
    this CLI recurses, and (since one root serves every project on the box)
    filters by `session_meta.cwd` (`--cwd` substring, repeatable).
  * codex classifies dispatched headless agent runs via `payload.originator`
    (`codex_exec`/`exec`), not Claude Code's `entrypoint` — dropped by default
    for the same self-cannibalism reason `prep.py` drops `entrypoint!=cli`
    (`--keep-agent-sessions` to include them).
  * this CLI ALSO stages a flat `raw/<sid>.jsonl` per kept session (copied, not
    symlinked, so a --redact run's traces/ dir and a later `emit.py --raw` both
    see plain files) — `emit.py`'s verbatim-recovery and `codex_outcomes.py`
    both expect a flat `<sid>.jsonl` directory the way Claude Code's raw
    project dir already is. `emit.py`'s raw-jsonl verbatim reader only
    recognizes Claude Code's `type=="user"` shape, so on a codex raw/ file it
    harmlessly finds zero matches and falls back to the (truncated) distilled
    trace line — never crashes, never fabricates.
  * no costs.json sidecar (yet): codex's per-call cost/telemetry shape is
    project cost_extract.py doesn't understand codex token_count events; noted
    as an unresolved follow-up in this task's report rather than guessed at.

Stdlib only. No LLM. Deterministic.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import codex_adapter as ca  # noqa: E402
from prep import binpack  # noqa: E402  (shared bin-packer, identical semantics)

REDACT = os.path.join(HERE, "redact.py")
DEFAULT_ROOT = os.path.expanduser("~/.codex/sessions")


def eprint(*a):
    print(*a, file=sys.stderr)


def redact_text(text):
    rd = subprocess.run([sys.executable, REDACT], input=text,
                         capture_output=True, text=True)
    if rd.returncode != 0:
        return None
    return rd.stdout


def cwd_match(path, needles):
    if not needles:
        return True
    cwd = None
    for i, obj in enumerate(ca.iter_records(path)):
        if obj.get("type") == "session_meta":
            cwd = (obj.get("payload") or {}).get("cwd") or ""
            break
        if i >= 5:
            break
    if cwd is None:
        return False
    return any(n in cwd for n in needles)


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Distill + bin-pack codex CLI/TUI rollouts (mirrors prep.py for Claude Code).")
    ap.add_argument("sessions_root", nargs="?", default=DEFAULT_ROOT,
                     help="root of nested rollout-*.jsonl files (default ~/.codex/sessions)")
    ap.add_argument("--out", default=None,
                     help="output dir (default /tmp/sm-codex-<basename-of-root>)")
    ap.add_argument("--job", default=None,
                     help="like prep.py --job: writes to .artifacts/session-mining/<job>/")
    ap.add_argument("--cwd", action="append", default=[], metavar="SUBSTR",
                     help="keep only sessions whose session_meta.cwd contains SUBSTR "
                          "(repeatable, OR semantics; default: keep all projects)")
    ap.add_argument("--min-bytes", type=int, default=5000,
                     help="skip raw rollouts smaller than this (default 5000; codex "
                          "rollouts run leaner than Claude Code's, so the default is "
                          "lower than prep.py's 30000)")
    ap.add_argument("--sample", choices=["size", "recency", "all"], default="all")
    ap.add_argument("--max", type=int, default=0)
    ap.add_argument("--budget", type=int, default=200000)
    ap.add_argument("--redact", action="store_true")
    ap.add_argument("--min-trace", type=int, default=200)
    ap.add_argument("--keep-agent-sessions", action="store_true",
                     help="keep codex_exec/exec (headless dispatched agent) rollouts")
    args = ap.parse_args(argv)

    root = os.path.abspath(os.path.expanduser(args.sessions_root))
    if not os.path.isdir(root):
        eprint("error: sessions root not found:", root)
        return 2

    if args.out:
        out = args.out
    elif args.job:
        repo_root = os.path.abspath(os.path.join(HERE, "..", ".."))
        out = os.path.join(repo_root, ".artifacts", "session-mining", args.job)
    else:
        out = os.path.join("/tmp", "sm-codex-" + os.path.basename(root.rstrip("/")))
    traces_dir = os.path.join(out, "traces")
    raw_dir = os.path.join(out, "raw")
    batches_dir = os.path.join(out, "batches")
    if os.path.isdir(out):
        shutil.rmtree(out)
    os.makedirs(traces_dir)
    os.makedirs(raw_dir)
    os.makedirs(batches_dir)

    sessions = ca.find_session_files(root)
    sessions = [s for s in sessions if os.path.getsize(s) >= args.min_bytes]
    sessions = [s for s in sessions if cwd_match(s, args.cwd)]

    agent_dropped = []
    if not args.keep_agent_sessions:
        kept = []
        for s in sessions:
            if ca.is_agent_session(s):
                agent_dropped.append(ca.session_id(s))
            else:
                kept.append(s)
        sessions = kept

    if args.sample == "recency":
        sessions.sort(key=lambda p: -os.path.getmtime(p))
    elif args.sample == "size":
        sessions.sort(key=lambda p: -os.path.getsize(p))
    if args.max > 0:
        sessions = sessions[: args.max]

    if agent_dropped:
        eprint("dropped %d dispatched agent/exec session(s) (originator in codex_exec/exec); "
               "use --keep-agent-sessions to include them" % len(agent_dropped))
    eprint("candidate sessions:", len(sessions),
           "(root=%s, cwd=%s, min-bytes=%d, sample=%s, max=%s)" %
           (root, args.cwd or "-", args.min_bytes, args.sample, args.max or "-"))

    traces = []
    failed = 0
    dropped = 0
    for src in sessions:
        sid = ca.session_id(src)
        dst = os.path.join(traces_dir, sid + ".txt")
        try:
            lines, _tool_call_ids = ca.distill_session(src)
        except OSError:
            failed += 1
            continue
        text = "\n".join(lines) + ("\n" if lines else "")
        if args.redact:
            redacted = redact_text(text)
            if redacted is None:
                failed += 1
                continue
            text = redacted
        with open(dst, "w") as fh:
            fh.write(text)
        sz = os.path.getsize(dst)
        if sz < args.min_trace:
            os.remove(dst)
            dropped += 1
            continue
        traces.append((dst, sz))
        shutil.copyfile(src, os.path.join(raw_dir, sid + ".jsonl"))

    bins = binpack(traces, args.budget)
    for i, (_tot, paths) in enumerate(bins, 1):
        with open(os.path.join(batches_dir, "batch-%02d.txt" % i), "w") as fh:
            fh.write("\n".join(paths) + "\n")

    total_bytes = sum(s for _, s in traces)
    manifest = {
        "source": "codex",
        "sessions_root": root,
        "out": out,
        "params": {
            "cwd": args.cwd, "min_bytes": args.min_bytes, "sample": args.sample,
            "max": args.max, "budget": args.budget, "redacted": args.redact,
            "min_trace": args.min_trace, "keep_agent_sessions": args.keep_agent_sessions,
        },
        "traces": len(traces),
        "distill_failed": failed,
        "dropped_empty": dropped,
        "agent_sessions_dropped": len(agent_dropped),
        "total_trace_bytes": total_bytes,
        "batches": [{"file": "batches/batch-%02d.txt" % i,
                     "traces": len(p), "bytes": t}
                    for i, (t, p) in enumerate(bins, 1)],
    }
    with open(os.path.join(out, "manifest.json"), "w") as fh:
        json.dump(manifest, fh, indent=2)

    eprint("distilled %d codex traces (%d KB), %d failed, %d near-empty dropped" %
           (len(traces), total_bytes // 1000, failed, dropped))
    eprint("%d batches of ~%d KB:" % (len(bins), args.budget // 1000))
    for i, (t, p) in enumerate(bins, 1):
        eprint("  batch-%02d: %d traces, %d KB" % (i, len(p), t // 1000))
    if args.redact:
        eprint("traces REDACTED — safe for the model in shareable pattern-mining.")
    else:
        eprint("traces NOT redacted — keep local (idea-mining mode).")

    eprint("BATCHES=%d" % len(bins))
    eprint("BATCHDIR=%s" % batches_dir)
    return 0


if __name__ == "__main__":
    sys.exit(main())

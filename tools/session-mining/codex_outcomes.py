#!/usr/bin/env python3
"""codex_outcomes.py — recover per-tool-call RESULTS from raw codex rollouts,
into the EXACT `outcomes.py` output schema (`{schema_version, sessions: {sid:
{join, tool_outcomes}}}`), so `emit.py --outcomes` reads either source's
outcomes.json identically. See codex_adapter.py for the extraction logic this
wraps; this file is just its CLI, mirroring outcomes.py's flag/output shape.

Why a separate CLI instead of teaching outcomes.py a second raw format:
outcomes.py's `collect_session` is keyed on Claude Code's tool_use/tool_result
id-or-positional join; codex's join is a genuinely different (simpler, always
id-keyed) shape. Same reasoning as codex_prep.py vs prep.py.

CLI:
    codex_outcomes.py --raw <dir of <sid>.jsonl codex rollouts> --out <outcomes.json>
                       [--stdout-head 600]

The --raw dir is expected FLAT (one <sid>.jsonl per session) — exactly what
`codex_prep.py`'s raw/ output stages, mirroring the flat raw project dir Claude
Code already has.

Stdlib only. No LLM. Deterministic.
"""
import argparse
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import codex_adapter as ca  # noqa: E402
import intent_common as ic  # noqa: E402


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Recover per-tool-call results from raw codex rollouts (deterministic).")
    ap.add_argument("--raw", required=True, help="dir of flat <sid>.jsonl codex rollouts")
    ap.add_argument("--out", required=True, help="write outcomes.json here")
    ap.add_argument("--stdout-head", type=int, default=600,
                     help="truncate stdout/stderr heads to this many chars (default 600)")
    args = ap.parse_args(argv)

    sessions = {}
    for f in sorted(os.listdir(args.raw)):
        if not f.endswith(".jsonl"):
            continue
        sid = f[: -len(".jsonl")]
        records = list(ca.iter_records(os.path.join(args.raw, f)))
        sess = ca.session_tool_outcomes(records, args.stdout_head)
        sessions[sid] = sess
        print("codex_outcomes: %s join=%s tool_outcomes=%d"
              % (sid, sess["join"], len(sess["tool_outcomes"])), file=sys.stderr)

    payload = {"schema_version": ic.SCHEMA_VERSION, "sessions": sessions}
    ic.dump_json(payload, args.out)
    print("recovered outcomes for %d codex session(s) -> %s" % (len(sessions), args.out),
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

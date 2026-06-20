#!/usr/bin/env python3
"""ground.py — step C of intent mining: ground & validate the agent hypothesis.

The agent (step B, the one LLM pass) PROPOSES a structured hypothesis: for each
intent span, a drafted recipe of actions, each carrying a citation (the trace line
it claims to come from). This step VERIFIES that hypothesis against the
deterministic trace — it is what turns the LLM into a *strictly-validated agent*
(review §3):

  1. confirm the cited trace line actually contains that tool call (reject
     fabricated actions),
  2. confirm each emitted parameter VALUE is a substring of the cited tool input
     (reject fabricated parameters),
  3. mark each action grounded/ungrounded; quarantine any span whose actions can't
     be grounded at all.

Input  (--agent):  one merged agent JSON, OR a dir of per-batch agent JSON
                    files. Shape per file:
   { "session": "<sid>", "spans": [
       { "span": [first, last], "tags": {...},
         "actions": [ { "tool": "Bash", "signature": "...",
                        "parameters": {...}, "cite": {"line": N} } ] } ] }
Traces (--traces):  the traces/ dir holding <session>.txt.
Output (--out):     grounded.json — the same spans annotated with per-action
                    `grounded` flags and per-span grounding stats. Consumed by
                    tag_score.py and emit.py.

Stdlib only. No LLM. Deterministic.
"""
import argparse
import os
import sys

import intent_common as ic


def _glob_agent(path):
    """Return a list of per-trace agent records from a file or a dir."""
    records = []
    if os.path.isdir(path):
        for f in sorted(os.listdir(path)):
            if f.endswith(".json"):
                records.extend(_records_from(ic.load_json(os.path.join(path, f))))
    else:
        records.extend(_records_from(ic.load_json(path)))
    return records


def _records_from(obj):
    """Accept either a single {session, spans} record or a list of them, or a
    wrapper {records: [...]}."""
    if isinstance(obj, list):
        return obj
    if isinstance(obj, dict) and "records" in obj:
        return obj["records"]
    return [obj]


def _param_values(parameters):
    """Flatten parameter values to a list of strings for substring checks."""
    vals = []
    for v in (parameters or {}).values():
        if isinstance(v, (list, tuple)):
            vals.extend(str(x) for x in v)
        else:
            vals.append(str(v))
    return vals


def ground_action(action, trace_lines):
    """Return (grounded: bool, reason: str) for one action against the trace.

    Grounded iff the cited line is a tool line whose tool matches, AND every
    non-empty emitted parameter value appears as a substring of the cited arg.
    """
    cite = action.get("cite") or {}
    line_no = cite.get("line")
    if not isinstance(line_no, int) or line_no < 1 or line_no >= len(trace_lines):
        return (False, "cite-line-out-of-range")
    tool, arg = ic.parse_tool_line(trace_lines[line_no])
    if tool is None:
        return (False, "cited-line-not-a-tool-call")
    if tool != action.get("tool"):
        return (False, "tool-mismatch:%s!=%s" % (tool, action.get("tool")))
    for val in _param_values(action.get("parameters")):
        if not val:
            continue
        if val not in arg:
            return (False, "param-not-in-arg:%s" % val[:40])
    return (True, "ok")


def ground_record(record, traces_dir):
    """Ground all spans of one trace record. Returns the annotated record."""
    sid = record.get("session")
    trace_path = os.path.join(traces_dir, sid + ".txt")
    if not os.path.exists(trace_path):
        # No trace -> nothing can be grounded; quarantine every span.
        for span in record.get("spans", []):
            for a in span.get("actions", []):
                a["grounded"] = False
            span["grounding"] = {
                "actions_cited": len(span.get("actions", [])),
                "actions_validated": 0,
                "quarantined": True,
            }
        record["_trace_missing"] = True
        return record

    trace_lines = ic.read_trace_lines(trace_path)
    for span in record.get("spans", []):
        actions = span.get("actions", [])
        validated = 0
        for a in actions:
            ok, reason = ground_action(a, trace_lines)
            a["grounded"] = ok
            if not ok:
                a["_ground_reason"] = reason
            if ok:
                validated += 1
        span["grounding"] = {
            "actions_cited": len(actions),
            "actions_validated": validated,
            # quarantined: an actionful span that grounded NOTHING is untrustworthy.
            "quarantined": bool(actions) and validated == 0,
        }
    return record


def main(argv=None):
    ap = argparse.ArgumentParser(description="Ground & validate agent output against traces.")
    ap.add_argument("--agent", required=True, help="agent JSON file or dir of per-batch JSON")
    ap.add_argument("--traces", required=True, help="traces/ dir (holds <session>.txt)")
    ap.add_argument("--out", required=True, help="write grounded.json here")
    ap.add_argument("--keep-quarantined", action="store_true",
                    help="keep quarantined spans (default: drop spans that grounded nothing)")
    args = ap.parse_args(argv)

    records = _glob_agent(args.agent)
    grounded = [ground_record(r, args.traces) for r in records]

    total_spans = 0
    kept_spans = 0
    dropped = 0
    out_records = []
    for r in grounded:
        kept = []
        for span in r.get("spans", []):
            total_spans += 1
            q = span.get("grounding", {}).get("quarantined")
            if q and not args.keep_quarantined:
                dropped += 1
                continue
            kept.append(span)
            kept_spans += 1
        r2 = dict(r)
        r2["spans"] = kept
        out_records.append(r2)

    payload = {
        "schema_version": ic.SCHEMA_VERSION,
        "records": out_records,
        "stats": {"spans_total": total_spans, "spans_kept": kept_spans,
                  "spans_dropped_quarantined": dropped},
    }
    ic.dump_json(payload, args.out)
    print("grounded %d spans (%d kept, %d quarantined-dropped) -> %s"
          % (total_spans, kept_spans, dropped, args.out), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

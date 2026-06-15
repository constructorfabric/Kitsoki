#!/usr/bin/env python3
"""emit.py — step F of intent mining: emit the two linked JSON reports.

Consumes the deterministic, scored records (tag_score.py output) and produces:

  intents.json   REPORT 1 — the intents catalog. Each record:
                 instance_id, user_text (VERBATIM), tags, session, span,
                 analysis_ref. Plus tag rollups + total_intents + version stamps.
  analysis.json  REPORT 2 — per-instance recipe. Each record: instance_id, tags,
                 determinism, actions (tool/signature/parameters/cite/grounded),
                 oracle_gates (only when not fully deterministic), measured,
                 grounding. Plus the clusters.

VERBATIM user text is recovered DETERMINISTICALLY from the raw .jsonl (NOT from
the truncated distilled trace and NOT from the LLM): for a span we find the user
turn that the span starts on and read that turn's message content from the raw
transcript. This mirrors distill.jq's USER-line selection so the Nth `USER:` line
in the trace maps to the Nth qualifying user turn in the jsonl.

The cross-link contract: every intents.json record's analysis_ref is
"analysis.json#<instance_id>" and that instance_id exists in analysis.json. Run
verify_link.py to check it.

Stdlib only. Deterministic.
"""
import argparse
import json
import os
import re
import sys

import intent_common as ic


# ---- verbatim recovery from raw jsonl (mirrors distill.jq USER selection) ----

def _user_text_from_content(content):
    """Return the USER text distill.jq would emit for a user turn, or None to skip.

    Mirrors distill.jq:
      - string content starting with '<' is skipped (tool-result/meta),
      - array content: first text block, skipping <command*> / system-reminder.
    Returns the VERBATIM text (untruncated) — distill truncates to 600 chars; we
    don't, since this is the local .artifacts tier.
    """
    if isinstance(content, str):
        if content.startswith("<"):
            return None
        return content
    if isinstance(content, list):
        for b in content:
            if isinstance(b, dict) and b.get("type") == "text":
                t = b.get("text", "")
                if t.startswith("<command") or "system-reminder" in t:
                    return None
                return t
        return None
    return None


def raw_user_turns(jsonl_path):
    """Ordered list of verbatim user-turn texts that distill.jq would have emitted
    as `USER:` lines — same order, same filtering. Index i == the (i+1)th USER line.
    """
    turns = []
    if not os.path.exists(jsonl_path):
        return turns
    with open(jsonl_path, "r", errors="ignore") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw:
                continue
            try:
                obj = json.loads(raw)
            except json.JSONDecodeError:
                continue
            if obj.get("type") != "user":
                continue
            # skip harness-injected user turns (command caveats, skill preambles);
            # they carry isMeta:true. MUST mirror distill.jq so the Nth USER: trace
            # line still maps to the Nth genuine raw user turn.
            if obj.get("isMeta") is True:
                continue
            txt = _user_text_from_content((obj.get("message") or {}).get("content"))
            if txt is not None:
                turns.append(txt)
    return turns


USER_LINE = re.compile(r"^USER:\s?")


def user_line_indices(trace_lines):
    """1-based trace line numbers that are USER: lines, in order. The k-th entry
    corresponds to raw_user_turns[k]."""
    return [i for i in range(1, len(trace_lines)) if trace_lines[i] and trace_lines[i].startswith("USER:")]


def verbatim_for_span(span, trace_lines, user_lines, raw_turns):
    """Recover the verbatim user text for the user turn the span STARTS on.

    Find the last USER: line at or before span[0]; its ordinal selects the raw
    turn. Falls back to the truncated trace line if raw recovery is unavailable.
    """
    first = span.get("span", [1])[0]
    chosen = None
    for ordinal, ln in enumerate(user_lines):
        if ln <= first:
            chosen = ordinal
        else:
            break
    if chosen is not None and chosen < len(raw_turns):
        return raw_turns[chosen]
    # fallback: the (truncated) trace line itself
    if chosen is not None and user_lines[chosen] < len(trace_lines):
        return USER_LINE.sub("", trace_lines[user_lines[chosen]])
    return ""


def main(argv=None):
    ap = argparse.ArgumentParser(description="Emit the two linked intent reports (deterministic).")
    ap.add_argument("--scored", required=True, help="tag_score.py output")
    ap.add_argument("--traces", required=True, help="traces/ dir")
    ap.add_argument("--raw", required=True,
                    help="dir of raw <session>.jsonl transcripts (for verbatim recovery)")
    ap.add_argument("--out-dir", required=True, help="job dir; writes intents.json + analysis.json")
    ap.add_argument("--job", default=None, help="job id stamp (default: basename of out-dir)")
    ap.add_argument("--prompt-version", default=None)
    ap.add_argument("--vocab-version", default=None)
    args = ap.parse_args(argv)

    scored = ic.load_json(args.scored)
    records = scored.get("records", [])
    job = args.job or os.path.basename(os.path.normpath(args.out_dir))

    intents = []
    instances = []

    for rec in records:
        sid = rec.get("session")
        trace_path = os.path.join(args.traces, sid + ".txt")
        trace_lines = ic.read_trace_lines(trace_path) if os.path.exists(trace_path) else [None]
        user_lines = user_line_indices(trace_lines)
        raw_turns = raw_user_turns(os.path.join(args.raw, sid + ".jsonl"))

        for span in rec.get("spans", []):
            instance_id = span.get("instance_id")
            tags = span.get("tags", {})
            user_text = verbatim_for_span(span, trace_lines, user_lines, raw_turns)

            intents.append({
                "instance_id": instance_id,
                "user_text": user_text,
                "session": sid,
                "span": span.get("span"),
                "tags": tags,
                "analysis_ref": "analysis.json#" + instance_id,
            })

            actions = []
            for a in span.get("actions", []):
                actions.append({
                    "tool": a.get("tool"),
                    "signature": a.get("signature", ""),
                    "parameters": a.get("parameters", {}),
                    "cite": a.get("cite", {}),
                    "grounded": bool(a.get("grounded")),
                })
            inst = {
                "instance_id": instance_id,
                "tags": tags,
                "determinism": span.get("determinism"),
                "actions": actions,
                "measured": span.get("measured", {}),
                "grounding": span.get("grounding", {}),
            }
            gates = span.get("oracle_gates")
            if span.get("determinism") != "deterministic" and gates:
                inst["oracle_gates"] = gates
            instances.append(inst)

    # optional version stamps: include only when set, so reports stay schema-clean
    # (the schema types these as strings; a null stamp is just an absent stamp).
    versions = {k: v for k, v in (
        ("tags_version", scored.get("tags_version")),
        ("vocab_version", args.vocab_version),
        ("prompt_version", args.prompt_version),
    ) if v is not None}

    intents_report = {
        "schema_version": ic.SCHEMA_VERSION,
        "job": job,
        "generated_from": "manifest.json",
        **versions,
        "total_intents": len(intents),
        "intents": intents,
        "tags": scored.get("tags", {}),
    }
    analysis_report = {
        "schema_version": ic.SCHEMA_VERSION,
        "job": job,
        **versions,
        "clusters": scored.get("clusters", []),
        "instances": instances,
    }

    ic.dump_json(intents_report, os.path.join(args.out_dir, "intents.json"))
    ic.dump_json(analysis_report, os.path.join(args.out_dir, "analysis.json"))
    print("emitted %d intents -> %s/{intents,analysis}.json"
          % (len(intents), args.out_dir), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

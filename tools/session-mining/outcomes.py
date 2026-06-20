#!/usr/bin/env python3
"""outcomes.py — recover per-tool-call RESULTS from the raw CC transcripts.

This is the one signal the deterministic intent-mining spine does not already
have: what each tool call actually produced (error? stdout/stderr? interrupted?).
ground.py / tag_score.py / emit.py only ever see the distilled trace + the agent
hypothesis; the raw `.jsonl` carries the tool results, so we read them HERE, once,
into a small session-ordered intermediate that emit.py joins by tool ordinal.

The join is purely ordinal and rests on the same load-bearing invariant emit.py
already relies on: distill.jq emits exactly one `> Tool: arg` trace line per
`tool_use` block in document order, so the k-th tool_use in <sid>.jsonl is the
k-th `>` line in traces/<sid>.txt. This file produces tool_outcomes[k] in that
same document order.

CLI:
    outcomes.py --raw <dir of <sid>.jsonl> --out <outcomes.json> [--stdout-head 600]

Output (internal intermediate, like grounded.json/scored.json — no schema file):
    { "schema_version": "1.0",
      "sessions": {
        "<sid>": { "join": "id"|"positional"|"mixed",
                   "tool_outcomes": [ {is_error, stdout_head, stderr_head, interrupted} | null, ... ] } } }

Reads BOTH result shapes seen across CC versions:
  - the content-block `tool_result` (type=="tool_result", carries tool_use_id,
    is_error, content),
  - the Claude-Code top-level `toolUseResult` (carries stdout/stderr/interrupted).
A tool_result block and its sibling toolUseResult belong to the same user record.

Stdlib only. No LLM. Deterministic.
"""
import argparse
import json
import os
import sys

import intent_common as ic


def _head(s, n):
    """Stringify and truncate to n chars (None -> "")."""
    if s is None:
        return ""
    if not isinstance(s, str):
        s = str(s)
    return s[:n]


def _content_text(content):
    """Extract display text from a tool_result `content` field.

    Content is usually a bare string, but newer CC shapes carry a LIST of
    content blocks (e.g. [{"type":"text","text":"..."}]). Join the text blocks
    so the head reads as the real output rather than a Python dict-repr.
    """
    if isinstance(content, list):
        parts = [b.get("text", "") for b in content
                 if isinstance(b, dict) and b.get("type") == "text"]
        return "\n".join(p for p in parts if p)
    return content


def collect_session(jsonl_path, stdout_head):
    """Return {"join": ..., "tool_outcomes": [...]} for one raw <sid>.jsonl.

    Mirrors distill.jq's tool_use selection exactly: one entry per content block
    with type=="tool_use" in a type=="assistant" record, in document order.
    """
    tool_uses = []          # ordered list of {"id": <id or None>}
    results_by_id = {}      # tool_use_id -> result dict
    results_in_order = []   # ordered list of result dicts (positional fallback)

    if not os.path.exists(jsonl_path):
        return {"join": "id", "tool_outcomes": []}

    with open(jsonl_path, "r", errors="ignore") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw:
                continue
            try:
                obj = json.loads(raw)
            except json.JSONDecodeError:
                continue
            typ = obj.get("type")
            if typ == "assistant":
                content = (obj.get("message") or {}).get("content")
                if isinstance(content, list):
                    for b in content:
                        if isinstance(b, dict) and b.get("type") == "tool_use":
                            tool_uses.append({"id": b.get("id")})
            elif typ == "user":
                # Top-level toolUseResult (sibling of message). A user record
                # normally carries exactly ONE tool_result block, so the record's
                # toolUseResult belongs to it. If a record ever carries MULTIPLE
                # tool_result blocks (some CC versions batch parallel tool calls),
                # the single record-level toolUseResult cannot be attributed to a
                # specific block — duplicating its stdout/stderr onto every block
                # would mis-report parallel calls. In that case drop it and fall
                # back to each block's own `content`. (is_error is per-block.)
                tur = obj.get("toolUseResult")
                content = (obj.get("message") or {}).get("content")
                blocks = [b for b in content
                          if isinstance(b, dict) and b.get("type") == "tool_result"] \
                    if isinstance(content, list) else []
                attr_tur = tur if len(blocks) == 1 else None
                if blocks:
                    for b in blocks:
                        result = {
                            "is_error": bool(b.get("is_error", False)),
                            "content": b.get("content"),
                            "tool_use_result": attr_tur,
                        }
                        rid = b.get("tool_use_id")
                        if rid is not None:
                            results_by_id[rid] = result
                        results_in_order.append(result)
                elif tur is not None:
                    # toolUseResult with no tool_result block — still a result.
                    result = {"is_error": False, "content": None, "tool_use_result": tur}
                    results_in_order.append(result)

    # Decide join mode (diagnostic). "id" requires every tool_use to match a
    # result by id. A transcript whose results arrive only as top-level
    # toolUseResult (no tool_result block) carries no result-side ids, so it
    # honestly reports "positional" even when the tool_uses themselves have ids —
    # the join actually used IS positional.
    have_use_ids = any(tu["id"] is not None for tu in tool_uses)
    matched_by_id = sum(1 for tu in tool_uses
                        if tu["id"] is not None and tu["id"] in results_by_id)
    if have_use_ids and matched_by_id == len(tool_uses) and tool_uses:
        join = "id"
    elif matched_by_id > 0:
        join = "mixed"
    else:
        join = "positional"

    # Positional fallback is ONLY safe when NO result carries an id (a truly
    # id-less or toolUseResult-only transcript), where the k-th result lines up
    # with the k-th tool_use by construction. The moment any result has an id we
    # are in the id-join regime: an unmatched id-bearing tool_use means its
    # result is genuinely absent (interrupted/abandoned), so it MUST become a
    # null entry. Per-element positional fallback in the id regime would cascade
    # a *later* call's result onto the missing one — masking the very
    # interrupted-outcome this tool exists to capture (spec §3: "missing result
    # ⇒ entry is null").
    can_positional = not results_by_id
    tool_outcomes = []
    for k, tu in enumerate(tool_uses):
        result = None
        if tu["id"] is not None and tu["id"] in results_by_id:
            result = results_by_id[tu["id"]]
        elif can_positional and k < len(results_in_order):
            result = results_in_order[k]
        # else: id-join regime, this call's result is absent -> null
        tool_outcomes.append(_outcome_from(result, stdout_head))
    return {"join": join, "tool_outcomes": tool_outcomes}


def _outcome_from(result, stdout_head):
    """Build the {is_error, stdout_head, stderr_head, interrupted} entry, or None."""
    if result is None:
        return None
    tur = result.get("tool_use_result")
    if isinstance(tur, dict) and tur:
        # the canonical Bash/tool shape: {stdout, stderr, interrupted}
        stdout = _head(tur.get("stdout"), stdout_head)
        stderr = _head(tur.get("stderr"), stdout_head)
        interrupted = bool(tur.get("interrupted", False))
    elif isinstance(tur, (str, list)) and tur:
        # real transcripts also carry toolUseResult as a bare string or a list of
        # content blocks (Read/Edit/structured tools). Treat it as stdout; never
        # call .get on it (that was a crash on real data).
        stdout = _head(_content_text(tur) if isinstance(tur, list) else tur, stdout_head)
        stderr = ""
        interrupted = False
    else:
        # no usable toolUseResult — fall back to the tool_result content for stdout
        # (content may be a bare string or a list of content blocks).
        stdout = _head(_content_text(result.get("content")), stdout_head)
        stderr = ""
        interrupted = False
    return {
        "is_error": bool(result.get("is_error", False)),
        "stdout_head": stdout,
        "stderr_head": stderr,
        "interrupted": interrupted,
    }


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Recover per-tool-call results from raw CC transcripts (deterministic).")
    ap.add_argument("--raw", required=True, help="dir of raw <session>.jsonl transcripts")
    ap.add_argument("--out", required=True, help="write outcomes.json here")
    ap.add_argument("--stdout-head", type=int, default=600,
                    help="truncate stdout/stderr heads to this many chars (default 600)")
    args = ap.parse_args(argv)

    sessions = {}
    for f in sorted(os.listdir(args.raw)):
        if not f.endswith(".jsonl"):
            continue
        sid = f[:-len(".jsonl")]
        sess = collect_session(os.path.join(args.raw, f), args.stdout_head)
        sessions[sid] = sess
        print("outcomes: %s join=%s tool_outcomes=%d"
              % (sid, sess["join"], len(sess["tool_outcomes"])), file=sys.stderr)

    payload = {"schema_version": ic.SCHEMA_VERSION, "sessions": sessions}
    ic.dump_json(payload, args.out)
    print("recovered outcomes for %d sessions -> %s" % (len(sessions), args.out),
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

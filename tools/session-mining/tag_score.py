#!/usr/bin/env python3
"""tag_score.py — steps D + E of intent mining (both deterministic, no LLM).

D. Tag & group:
   - validate every tag against vocab/tags.yaml (unknown tags warned + dropped),
   - roll up per-dimension tag counts,
   - cluster spans by (action tag-set + normalized action signature) so recurring
     intents surface. The agent already assigned tags in step B; this only
     aggregates/clusters.

E. Score determinism per instance from MEASURED trace signals + grounding
   completeness + presence of judgment gates (NOT unvalidated LLM estimates):
     - deterministic   : every action grounded, no agent_gates, no edit->rerun
                         churn beyond a clean run.
     - agent-gated    : reproducible except at N named gates (agent_gates present
                         and validated), OR grounding incomplete but mostly-ok.
     - irreducible-llm : nothing grounded (quarantined) — the recipe can't be
                         trusted as deterministic.

The `measured` signals are recomputed here from the trace itself over each span's
line range — tool-call count, edit->rerun cycles, retries — so the verdict rests
on the trace, not the model.

Input  (--grounded): ground.py output.
Traces (--traces):   the traces/ dir.
Output (--out):      scored.json — records with per-span tags (validated),
                     measured, and determinism, plus top-level clusters + tag
                     rollups. Consumed by emit.py.

Stdlib only.
"""
import argparse
import re
import sys

import intent_common as ic

EDIT_TOOLS = {"Edit", "Write", "NotebookEdit", "MultiEdit", "Update"}
RUN_RE = re.compile(r"\b(test|go test|pytest|npm|make|run|build|cargo|jest|vitest)\b", re.I)


def measure_span(trace_lines, span):
    """Compute deterministic trace signals over [first, last] (1-based inclusive).

    tool_calls        — number of tool-call lines in range
    edit_rerun_cycles — count of (edit tool ... then a run/test command) transitions
    retries           — repeated identical tool-call lines (same tool+arg) in range
    """
    first, last = span.get("span", [1, 1])[:2]
    first = max(1, first)
    last = min(len(trace_lines) - 1, last)
    seen = {}
    tool_calls = 0
    retries = 0
    edit_rerun = 0
    pending_edit = False
    for i in range(first, last + 1):
        tool, arg = ic.parse_tool_line(trace_lines[i])
        if tool is None:
            continue
        tool_calls += 1
        key = (tool, arg)
        if key in seen:
            retries += 1
        seen[key] = seen.get(key, 0) + 1
        if tool in EDIT_TOOLS:
            pending_edit = True
        elif pending_edit and (tool == "Bash" and RUN_RE.search(arg or "")):
            edit_rerun += 1
            pending_edit = False
    return {"tool_calls": tool_calls, "edit_rerun_cycles": edit_rerun, "retries": retries}


def validate_tags(tags, vocab):
    """Drop unknown tags per dimension; warn on stderr. Returns cleaned tags."""
    clean = {}
    dims = vocab.get("dimensions", {})
    for dim, members in (tags or {}).items():
        allowed = dims.get(dim)
        if allowed is None:
            print("warn: unknown tag dimension %r dropped" % dim, file=sys.stderr)
            continue
        kept = []
        for m in (members or []):
            if m in allowed:
                kept.append(m)
            else:
                print("warn: unknown %s tag %r dropped" % (dim, m), file=sys.stderr)
        clean[dim] = kept
    return clean


def score_determinism(span):
    """Verdict from grounding + gates. Pure function of the (already-grounded) span."""
    g = span.get("grounding", {})
    cited = g.get("actions_cited", 0)
    validated = g.get("actions_validated", 0)
    gates = span.get("agent_gates") or []

    if g.get("quarantined") or (cited > 0 and validated == 0):
        return "irreducible-llm"
    if gates:
        return "agent-gated"
    if cited == 0:
        # no concrete actions recovered -> open-ended
        return "irreducible-llm"
    if validated < cited:
        # partially grounded recipe still needs an agent to fill the gap
        return "agent-gated"
    return "deterministic"


def normalized_signature(actions):
    """Stable cluster key fragment from the action sequence (tools only)."""
    return ">".join(a.get("tool", "?") for a in actions)


def cluster_key(tags, actions):
    acts = sorted((tags or {}).get("action", []))
    return "[%s] %s" % (",".join(acts) or "-", normalized_signature(actions))


def main(argv=None):
    ap = argparse.ArgumentParser(description="Tag/group + determinism scoring (deterministic).")
    ap.add_argument("--grounded", required=True, help="ground.py output")
    ap.add_argument("--traces", required=True, help="traces/ dir")
    ap.add_argument("--tags-vocab", default=None, help="vocab/tags.yaml (default: bundled)")
    ap.add_argument("--out", required=True, help="write scored.json here")
    args = ap.parse_args(argv)

    vocab = ic.load_tag_vocab(args.tags_vocab or ic.default_tags_vocab_path())
    grounded = ic.load_json(args.grounded)
    records = grounded.get("records", [])

    trace_cache = {}

    def lines_for(sid):
        if sid not in trace_cache:
            import os
            p = os.path.join(args.traces, sid + ".txt")
            trace_cache[sid] = ic.read_trace_lines(p) if os.path.exists(p) else [None]
        return trace_cache[sid]

    tag_rollup = {}  # dim -> {tag: count}
    clusters = {}    # key -> [instance_id]

    for rec in records:
        sid = rec.get("session")
        lines = lines_for(sid)
        for idx, span in enumerate(rec.get("spans", [])):
            instance_id = "%s#%d" % (sid, idx)
            span["instance_id"] = instance_id
            span["tags"] = validate_tags(span.get("tags"), vocab)
            span["measured"] = measure_span(lines, span)
            span["determinism"] = score_determinism(span)

            for dim, members in span["tags"].items():
                d = tag_rollup.setdefault(dim, {})
                for m in members:
                    d[m] = d.get(m, 0) + 1

            key = cluster_key(span["tags"], span.get("actions", []))
            clusters.setdefault(key, []).append(instance_id)

    cluster_list = [{"key": k, "count": len(v), "instances": v}
                    for k, v in sorted(clusters.items(), key=lambda kv: -len(kv[1]))]

    payload = {
        "schema_version": ic.SCHEMA_VERSION,
        "tags_version": vocab.get("tags_version"),
        "records": records,
        "tags": tag_rollup,
        "clusters": cluster_list,
    }
    ic.dump_json(payload, args.out)
    n_inst = sum(len(r.get("spans", [])) for r in records)
    print("scored %d instances into %d clusters -> %s"
          % (n_inst, len(cluster_list), args.out), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

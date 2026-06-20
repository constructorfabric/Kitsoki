#!/usr/bin/env python3
"""intent_brief.py — render the two intent-mining reports into a readable brief.

    python3 intent_brief.py <job-dir> > BRIEF.md
    python3 intent_brief.py --intents intents.json --analysis analysis.json > BRIEF.md

The two JSON reports (intents.json + analysis.json) are the machine deliverable;
this is the human view. It summarizes the corpus, the determinism split, the
grounding rate, the tag distribution, and the recurring clusters, then lists every
intent with its verbatim ask, determinism verdict, recipe, and agent gates.

Deterministic, stdlib only. Reads only the emitted reports (not the traces).
"""
import argparse
import json
import os
import sys
from collections import Counter

DET_ICON = {"deterministic": "🟢", "agent-gated": "🟡", "irreducible-llm": "🔴"}


def _load(p):
    with open(p) as fh:
        return json.load(fh)


def _oneline(s, n=160):
    s = " ".join((s or "").split())
    return s[:n] + ("…" if len(s) > n else "")


def render(intents, analysis):
    out = []
    w = out.append
    inst = {i["instance_id"]: i for i in analysis.get("instances", [])}
    rows = intents.get("intents", [])
    det = Counter(i["determinism"] for i in analysis.get("instances", []))
    cited = sum(i.get("grounding", {}).get("actions_cited", 0) for i in inst.values())
    valid = sum(i.get("grounding", {}).get("actions_validated", 0) for i in inst.values())

    w("# Session-mining intent brief\n")
    w("_job `%s` · %d intents · %d clusters · grounding %d/%d actions validated (%d%%)_\n"
      % (intents.get("job", "?"), intents.get("total_intents", len(rows)),
         len(analysis.get("clusters", [])), valid, cited,
         round(100 * valid / cited) if cited else 100))

    w("\n## Reproducibility split\n")
    w("| verdict | count | meaning |")
    w("|---|--:|---|")
    w("| 🟢 deterministic | %d | pure tool sequence, all params grounded, no judgment fork |"
      % det.get("deterministic", 0))
    w("| 🟡 agent-gated | %d | reproducible except at named gates, each with a strict validator |"
      % det.get("agent-gated", 0))
    w("| 🔴 irreducible-llm | %d | output genuinely needs open-ended generation |"
      % det.get("irreducible-llm", 0))

    w("\n## Tag distribution\n")
    for dim in ("action", "surface", "scope"):
        tg = intents.get("tags", {}).get(dim, {})
        if not tg:
            continue
        top = sorted(tg.items(), key=lambda x: -x[1])
        w("- **%s** — %s" % (dim, ", ".join("`%s` %d" % (k, v) for k, v in top[:12])))

    clusters = sorted(analysis.get("clusters", []), key=lambda c: -c["count"])
    recurring = [c for c in clusters if c["count"] > 1]
    if recurring:
        w("\n## Recurring intent shapes (clusters seen >1×)\n")
        for c in recurring:
            w("- **%d×** `%s`" % (c["count"], _oneline(c["key"], 110)))

    w("\n## Intents\n")
    # group by determinism for readability: gated/irreducible first (the actionable ones)
    order = {"agent-gated": 0, "irreducible-llm": 1, "deterministic": 2}
    rows_sorted = sorted(
        rows, key=lambda r: (order.get(inst.get(r["instance_id"], {}).get("determinism"), 9),
                             r["instance_id"]))
    for r in rows_sorted:
        an = inst.get(r["instance_id"], {})
        d = an.get("determinism", "?")
        w("\n### %s %s" % (DET_ICON.get(d, ""), r["instance_id"]))
        w("> %s" % _oneline(r.get("user_text", ""), 220))
        acts = an.get("actions", [])
        tags = r.get("tags", {})
        w("- **tags:** action=%s%s" % (
            tags.get("action", []),
            (" · surface=%s" % tags.get("surface")) if tags.get("surface") else ""))
        m = an.get("measured", {})
        w("- **measured:** %d tool calls · %d edit→rerun · %d retries"
          % (m.get("tool_calls", 0), m.get("edit_rerun_cycles", 0), m.get("retries", 0)))
        if acts:
            sig = "  →  ".join(a.get("signature", a.get("tool", "?")) for a in acts[:8])
            if len(acts) > 8:
                sig += "  →  …(%d more)" % (len(acts) - 8)
            w("- **recipe:** %s" % sig)
        for g in an.get("agent_gates", []) or []:
            w("- **gate:** %s — _validator:_ %s" % (g.get("decision"), g.get("validator")))
    return "\n".join(out) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description="Render intent-mining reports into a readable brief.")
    ap.add_argument("job_dir", nargs="?", help="job dir holding intents.json + analysis.json")
    ap.add_argument("--intents", help="intents.json (overrides job_dir)")
    ap.add_argument("--analysis", help="analysis.json (overrides job_dir)")
    args = ap.parse_args(argv)

    if args.intents and args.analysis:
        ip, ap_ = args.intents, args.analysis
    elif args.job_dir:
        ip = os.path.join(args.job_dir, "intents.json")
        ap_ = os.path.join(args.job_dir, "analysis.json")
    else:
        ap.error("pass a job dir, or both --intents and --analysis")
    sys.stdout.write(render(_load(ip), _load(ap_)))
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Render an aggregated session-mining report into an actionable brief.

    python3 report.py combined.json [--vocab vocab/core.yaml] [--top N] > BRIEF.md

The aggregate (aggregate.py output) is a ranked *diagnostic*: it tells you which
recurring procedures are most worth automating. This turns that into a
*prescriptive* brief — for each top candidate it states the verdict, the ladder
move, the gates to install (the judgment to preserve), the mechanical skeleton to
script, the evidence, and a concrete first step. Everything below the cut line is
still listed, so nothing is hidden.

Stdlib only. The vocab file is read only to look up each pattern's name,
one-line definition, and default ladder target ("the move"); it is optional.

What the verdict means (thresholds are explicit so two runs are comparable):
  BUILD NOW            high priority, corroborated by >=PROMOTE_MIN contributors,
                       and a crisp gate set (<= CRISP_GATES decisions)
  BUILD (judgment)     high priority + corroborated, but many decision points —
                       worth a story, but keep a model/human at the gates (low ladder target)
  PROMISING            high priority but only one contributor — needs corroboration
  ALREADY MOSTLY SOLVED  very mechanical + painless — low payoff to formalize further
  LATER                everything else, by priority
"""
import argparse
import json
import re
import sys

PRIORITY_CUT = 0.50      # at/above this, a pattern is a "build" candidate
CRISP_GATES = 4          # <= this many (unioned across contributors) reads as a clean gate set
SOLVED_MECH = 0.90       # mechanical_fraction at/above which, with low pain, ROI to formalize is low
PAIN_MARK = {"high": "🔴", "med": "🟠", "low": "⚪"}


def load_vocab(path):
    """Minimal id -> {name, definition, ladder_target} map. No pyyaml dependency."""
    vocab = {}
    cur = None
    if not path:
        return vocab
    try:
        lines = open(path).read().splitlines()
    except OSError:
        return vocab
    for ln in lines:
        m = re.match(r"\s*-\s*id:\s*(\S+)", ln)
        if m:
            cur = {"id": m.group(1)}
            vocab[cur["id"]] = cur
            continue
        if cur is None:
            continue
        m = re.match(r"\s*name:\s*(.+)", ln)
        if m:
            cur["name"] = m.group(1).strip()
        m = re.match(r"\s*definition:\s*(.+)", ln)
        if m:
            cur["definition"] = m.group(1).strip()
        m = re.match(r"\s*default_ladder_target:\s*(L[0-4])", ln)
        if m:
            cur["ladder_target"] = m.group(1)
    return vocab


def verdict(p, total_contributors, promote_min):
    corroborated = p["contributors"] >= promote_min
    gates = len(p.get("decision_points", []))
    crisp = gates <= CRISP_GATES
    mech = p.get("mechanical_fraction", 0)
    pain = p.get("pain", "low")
    solved = mech >= SOLVED_MECH and pain == "low"
    prio = p.get("determinism_priority", 0)
    if solved:
        return "ALREADY MOSTLY SOLVED", "🔵"
    if prio >= PRIORITY_CUT and corroborated and crisp:
        return "BUILD NOW", "🟢"
    if prio >= PRIORITY_CUT and corroborated:
        return "BUILD (judgment-heavy)", "🟡"
    if prio >= PRIORITY_CUT:
        return "PROMISING (needs corroboration)", "🟣"
    return "LATER", "⚪"


def brief_block(p, vocab, total, promote_min):
    v = vocab.get(p["id"], {})
    name = v.get("name", p["id"])
    label, icon = verdict(p, total, promote_min)
    target = v.get("ladder_target", "L2")
    gates = p.get("decision_points", [])
    sigs = p.get("example_signatures", [])
    out = []
    out.append(f"### {icon} {name} — {label}")
    if v.get("definition"):
        out.append(f"_{v['definition']}_")
    out.append("")
    out.append(
        f"**Why:** priority **{p['determinism_priority']:.2f}** · "
        f"seen by **{p['contributors']}/{total}** contributors · "
        f"**{p['occurrences']}** occurrences · "
        f"pain {PAIN_MARK.get(p.get('pain','low'),'')} {p.get('pain','?')} · "
        f"{int(round(p.get('mechanical_fraction',0)*100))}% mechanical"
    )
    out.append(f"**The move:** L1 (recurring manual work today) → **{target}** target")
    out.append("")
    if gates:
        out.append(f"**Gates to install ({len(gates)} decision point"
                   f"{'s' if len(gates)!=1 else ''} — the judgment to keep):**")
        for g in gates[:5]:
            out.append(f"- {g}")
        if len(gates) > 5:
            out.append(f"- …and {len(gates)-5} more (consolidate into ≤3 real gates before building)")
        out.append("")
    if sigs:
        out.append("**Skeleton to script (observed tool-call shape):**")
        for s in sigs[:4]:
            out.append(f"- `{s}`")
        if len(sigs) > 4:
            out.append(f"- …and {len(sigs)-4} more variant(s)")
        out.append("")
    out.append("**First step:** script the skeleton above; wrap each gate as a named "
               "decision point (a default rule where one is obvious, else prompt a "
               f"model/human); record every decision so the gate can climb toward {target}.")
    out.append("")
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser(description="Render an aggregated report into an actionable brief.")
    ap.add_argument("report", help="aggregate.py output JSON")
    ap.add_argument("--vocab", default="vocab/core.yaml", help="controlled vocabulary (for names + ladder targets)")
    ap.add_argument("--top", type=int, default=0, help="limit the action shortlist to N (0 = all build candidates)")
    args = ap.parse_args()

    d = json.load(open(args.report))
    vocab = load_vocab(args.vocab)
    total = d.get("contributors", 1)
    promote_min = d.get("promote_min_contributors", 2)
    patterns = d.get("patterns", [])

    candidates = [p for p in patterns
                  if verdict(p, total, promote_min)[0].startswith(("BUILD", "PROMISING"))]
    if args.top:
        candidates = candidates[:args.top]

    w = sys.stdout.write
    w("# Session-mining action brief\n\n")
    vv = d.get("vocab_version", "?")
    w(f"_{d.get('reports_merged','?')} report(s) · {total} contributor(s) · "
      f"vocab {vv} · promotion threshold {promote_min} contributors._\n\n")

    w("## Build these (ranked)\n\n")
    if candidates:
        for p in candidates:
            w(brief_block(p, vocab, total, promote_min))
    else:
        w("_No pattern cleared the priority cut. Mine more sessions or contributors._\n\n")

    w("\n## Full ranking\n\n")
    w("| Pattern | Verdict | Prio | Contrib | Occ | Pain | Gates |\n")
    w("|---|---|--:|--:|--:|:--:|--:|\n")
    for p in patterns:
        label, icon = verdict(p, total, promote_min)
        w(f"| {p['id']} | {icon} {label} | {p['determinism_priority']:.2f} "
          f"| {p['contributors']}/{total} | {p['occurrences']} | {p.get('pain','?')} "
          f"| {len(p.get('decision_points',[]))} |\n")
    w("\n")

    quar = d.get("novel_quarantine", [])
    cand = d.get("novel_promotion_candidates", [])
    if cand:
        w("## Newly corroborated patterns (promote into the vocabulary)\n\n")
        for p in cand:
            w(f"- **{p['id']}** — {p['contributors']} contributors, "
              f"{p['occurrences']} occ. Add to `vocab/core.yaml` (bump `vocab_version`).\n")
        w("\n")
    if quar:
        w("## Watch list (novel, not yet corroborated)\n\n")
        w("Each needs more independent contributors before it counts. Not actionable yet.\n\n")
        for p in sorted(quar, key=lambda x: x["contributors"], reverse=True):
            need = max(0, promote_min - p["contributors"])
            w(f"- `{p['id']}` — {p['contributors']} contributor(s), "
              f"needs {need} more to promote\n")
        w("\n")


if __name__ == "__main__":
    main()

# gs_preamble.star — build the BOUNDED, fixed-shape evaluator preamble.
#
# Deterministic port of goal.py:build_preamble. A projection of current state
# (never an accumulation of history): goal criteria + a one-line-per-change ledger
# table + the open frontier + the scope-disjoint ready set + "your job". Reads the
# ledger.json that gs_ledger just wrote and GOAL.md from <goal_dir>. Writes
# <work_dir>/preamble.md and returns the string for the evaluate decide-gate.

STATES = ["blocked", "ready", "assigned", "in_flight", "reviewing", "verified", "integrated", "parked"]
FRONTIER = ["assigned", "in_flight", "reviewing", "verified", "parked"]


def _goal_criteria(ctx, goal_dir):
    p = goal_dir + "/GOAL.md"
    if not ctx.fs.exists(p):
        return []
    out = []
    for ln in ctx.fs.read(p).split("\n"):
        s = ln.strip()
        if s.startswith("- **G"):
            out.append(s)
    return out


def _scopes_overlap(a, b):
    aa = [x.rstrip("/") for x in a]
    bb = [y.rstrip("/") for y in b]
    for x in aa:
        for y in bb:
            if x == y:
                return True
            if x.endswith("/**") or y.endswith("/**"):
                xd = x[:-3].rstrip("/")
                yd = y[:-3].rstrip("/")
                if xd == yd or xd.startswith(yd + "/") or yd.startswith(xd + "/"):
                    return True
            if x.startswith(y + "/") or y.startswith(x + "/"):
                return True
    return False


def _ready_set(rows, k):
    picked = []
    for r in rows:
        if r["state"] != "ready":
            continue
        overlap = False
        for p in picked:
            if _scopes_overlap(r["scope_globs"], p["scope_globs"]):
                overlap = True
        if overlap:
            continue
        picked.append(r)
        if len(picked) >= k:
            break
    return picked


def main(ctx):
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    work_dir = ctx.inputs["work_dir"].rstrip("/")

    ledger_path = work_dir + "/ledger.json"
    if not ctx.fs.exists(ledger_path):
        fail("gs_preamble: no ledger.json at " + ledger_path + " (run gs_ledger first)")
    ledger = json.decode(ctx.fs.read(ledger_path))
    rows = ledger.get("changes") or []
    counts = ledger.get("counts") or {}
    crit = _goal_criteria(ctx, goal_dir)

    lines = []
    lines.append("# /goal preamble — " + str(ledger.get("goal_slug")) + " (BOUNDED projection; do not treat as history)")
    lines.append("")
    lines.append("## Goal criteria (the target)")
    if crit:
        for c in crit:
            lines.append(c)
    else:
        lines.append("(GOAL.md criteria not found)")
    lines.append("")
    lines.append("## Ledger (current state — one line per change)")
    lines.append("| id | wall | state | gate | deps |")
    lines.append("|----|------|-------|------|------|")
    for r in rows:
        deps = ",".join(r.get("depends_on") or [])
        if deps == "":
            deps = "—"
        lines.append("| " + str(r["change_id"]) + " | " + str(r.get("wall")) + " | " + str(r["state"]) +
                     " | " + str(r.get("gate_class")) + ":" + str(r.get("gate_status")) + " | " + deps + " |")
    lines.append("")
    parts = []
    for s in STATES:
        n = counts.get(s, 0)
        if n:
            parts.append(s + "=" + str(n))
    lines.append("Counts: " + ", ".join(parts))
    lines.append("")

    frontier = []
    for r in rows:
        if r["state"] in FRONTIER:
            frontier.append(r)
    lines.append("## Frontier (open changes only — the last summary each)")
    if not frontier:
        lines.append("(no open changes — dispatch from the ready set)")
    for r in frontier:
        lines.append("- **" + str(r["change_id"]) + "** (" + str(r["state"]) + "): " +
                     str(r.get("last_summary") or "(no summary yet)"))
    lines.append("")

    lines.append("## Ready to dispatch now (disjoint scopes, deps satisfied)")
    ready = _ready_set(rows, 99)
    if not ready:
        lines.append("(nothing ready — every open change is blocked, in flight, or integrated)")
    for r in ready:
        lines.append("- " + str(r["change_id"]) + " — " + str(r.get("title")) +
                     " (scope: " + ", ".join(r.get("scope_globs") or []) + ")")
    lines.append("")

    lines.append("## Your job")
    lines.append("Emit ONE JSON: {verdict, summary, next_instruction}. verdict=done only if every " +
                 "change is `integrated` AND every wall-gate is green. Otherwise pick the next change " +
                 "from the ready set. Do not restate this preamble back.")

    preamble = "\n".join(lines) + "\n"
    ctx.fs.write(work_dir + "/preamble.md", preamble)
    return {"preamble": preamble}

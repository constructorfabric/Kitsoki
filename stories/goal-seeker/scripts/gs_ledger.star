# gs_ledger.star — fold the append-only log into the ledger (source of truth = the log).
#
# Deterministic port of goal.py:fold_ledger. Reads the decomposition (the change
# set) from <goal_dir>/decomposition.yaml and the append-only <work_dir>/log.jsonl,
# folds the log over the change set in seq order, derives blocked<->ready from
# dependency satisfaction, writes <work_dir>/ledger.json, and returns the ledger +
# counts + the deterministic ready set for the bounded preamble. No LLM, no network.
#
# Determinism guarantees enforced here (NOT trusted to any LLM):
#   * reviewer-only-green — a change's gate flips green ONLY from a log entry whose
#     actor is `reviewer` or `integrator`. A worker's self-reported gate.status=green
#     is IGNORED. "green" means independently verified.
#   * remaining — count of changes not yet integrated; 0 ⇒ the goal is done. verdict=
#     done is this deterministic fact, never an LLM claim (the evaluate gate reads it).
#   * ready_id — the first scope-disjoint, deps-satisfied ready change (goal.py's
#     ready_set). The authoritative pick even when the evaluator's advice disagrees.

STATES = ["blocked", "ready", "assigned", "in_flight", "reviewing", "verified", "integrated", "parked"]
TERMINAL_OK = ["integrated"]
GREEN_ACTORS = ["reviewer", "integrator"]
# Parity with goal.py:PARK_REASONS. `gate_not_red` is the RED-first park (dispatch's
# redcheck found the change's gate already green — not a completion, needs triage).
# `drive_failed` is the punch-list park (the maker drive could not green a genuinely
# RED change's gate — punch-list exited needs-human/abandoned instead of done).
PARK_REASONS = ["quota", "conflict", "escalation", "gate_not_red", "drive_failed"]


def _seq(e):
    return e.get("seq", 0)


def _read_log(ctx, work_dir):
    path = work_dir + "/log.jsonl"
    if not ctx.fs.exists(path):
        return []
    out = []
    for line in ctx.fs.read(path).split("\n"):
        line = line.strip()
        if line == "":
            continue
        out.append(json.decode(line))
    return sorted(out, key=_seq)


def _fold(changes, log):
    rows = {}
    for c in changes:
        cid = str(c["id"])
        gate = c.get("gate") or {}
        rows[cid] = {
            "change_id": cid,
            "wall": c.get("wall"),
            "title": c.get("title"),
            "pipeline": c.get("pipeline"),
            "scope_globs": [str(g) for g in (c.get("scope") or [])],
            "depends_on": [str(d) for d in (c.get("depends_on") or [])],
            "state": "blocked",
            "branch": None,
            "sha": None,
            "gate_status": "red",   # RED-first: nothing is green until independently proven
            "gate_class": gate.get("class"),
            "last_summary": None,
            "park_reason": None,   # {quota, conflict, escalation, gate_not_red, drive_failed} when state==parked
        }

    for e in log:
        cid = str(e.get("change_id", ""))
        if cid not in rows:
            continue
        r = rows[cid]
        if e.get("state") in STATES:
            r["state"] = e["state"]
            if e["state"] != "parked":
                r["park_reason"] = None
        if e.get("branch"):
            r["branch"] = e["branch"]
        if e.get("sha"):
            r["sha"] = e["sha"]
        # reviewer-only-green: a gate flips green ONLY from a reviewer/integrator entry.
        # A worker's self-reported green is recorded but does NOT count as verified.
        g = e.get("gate") or {}
        gstatus = g.get("status")
        if gstatus:
            actor = str(e.get("actor") or "")
            if gstatus != "green" or actor in GREEN_ACTORS:
                r["gate_status"] = gstatus
        if e.get("summary"):
            r["last_summary"] = e["summary"]
        if e.get("park_reason") in PARK_REASONS:
            r["park_reason"] = e["park_reason"]

    # derive blocked<->ready for anything still in the intake states. A `parked` row
    # is left alone here (its state is neither "blocked" nor "ready") so it stays
    # parked — and therefore excluded from _ready_set below — until an explicit log
    # entry moves it (goal.py: fold_ledger's deps_integrated pass has the same shape).
    for cid in rows:
        r = rows[cid]
        if r["state"] in ("blocked", "ready"):
            ok = True
            for d in r["depends_on"]:
                if d in rows and rows[d]["state"] not in TERMINAL_OK:
                    ok = False
            r["state"] = "ready" if ok else "blocked"

    return rows


def _counts(rows):
    c = {}
    for s in STATES:
        c[s] = 0
    for cid in rows:
        st = rows[cid]["state"]
        c[st] = c.get(st, 0) + 1
    return c


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


def _ready_set(ordered):
    picked = []
    for r in ordered:
        if r["state"] != "ready":
            continue
        overlap = False
        for p in picked:
            if _scopes_overlap(r["scope_globs"], p["scope_globs"]):
                overlap = True
        if overlap:
            continue
        picked.append(r)
    return picked


def main(ctx):
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    work_dir = ctx.inputs["work_dir"].rstrip("/")

    decomp_path = goal_dir + "/decomposition.yaml"
    if not ctx.fs.exists(decomp_path):
        fail("gs_ledger: no decomposition.yaml at " + decomp_path)
    doc = yaml.decode(ctx.fs.read(decomp_path))
    if type(doc) != "dict":
        fail("gs_ledger: decomposition.yaml must be a mapping")
    changes = doc.get("changes") or []
    if not changes:
        fail("gs_ledger: decomposition.yaml has no `changes`")

    log = _read_log(ctx, work_dir)
    rows = _fold(changes, log)
    counts = _counts(rows)

    ordered = []
    remaining = 0
    for c in changes:
        r = rows[str(c["id"])]
        ordered.append(r)
        if r["state"] not in TERMINAL_OK:
            remaining += 1

    ready = _ready_set(ordered)
    ready_ids = [r["change_id"] for r in ready]
    ready_id = ready_ids[0] if ready_ids else ""

    ledger = {
        "goal_slug": doc.get("goal_slug"),
        "counts": counts,
        "remaining": remaining,
        "ready_ids": ready_ids,
        "changes": ordered,
    }
    ctx.fs.write(work_dir + "/ledger.json", json.encode(ledger) + "\n")
    # remaining = changes not yet integrated. verdict=done is a DETERMINISTIC fact
    # (remaining==0), never trusted to the LLM; ready_id is the authoritative next
    # pick even if the evaluator's advice disagrees (anti-false-done + anti-drift).
    return {
        "ledger": ledger,
        "counts": counts,
        "remaining": remaining,
        "ready_id": ready_id,
        "ready_count": len(ready_ids),
    }

# gs_init.star — initialize the goal work_dir (the deterministic `init` step).
#
# Port of goal.py:init's log seeding. Ensures <work_dir>/log.jsonl exists so the
# append-only log has a home. When `reset` is true it TRUNCATES the log first — a
# TEST-FIXTURE-ONLY affordance that discards all append-only history, including
# every integrated fact. To keep hermetic flow fixtures from reading stale derived
# state after that destructive wipe, reset also writes an empty ledger, blanks the
# preamble, and neutralizes any existing manifests/*.json files with "{}".
#
# In production reset defaults false. Real sessions must preserve log.jsonl as the
# source of truth; targeted unpark/reopen work should append a precise log entry,
# not use reset_log.


EMPTY_COUNTS = {
    "assigned": 0,
    "blocked": 0,
    "in_flight": 0,
    "integrated": 0,
    "parked": 0,
    "ready": 0,
    "reviewing": 0,
    "verified": 0,
}


def _is_reset(v):
    return v == True or v == "true"


def _reset_derived(ctx, work_dir):
    empty_ledger = {
        "changes": [],
        "counts": EMPTY_COUNTS,
        "goal_slug": "",
        "ready_ids": [],
        "remaining": 0,
    }
    ctx.fs.write(work_dir + "/ledger.json", json.encode(empty_ledger) + "\n")
    ctx.fs.write(work_dir + "/preamble.md", "")
    manifests = ctx.fs.glob(work_dir + "/manifests/*.json")
    for path in manifests:
        ctx.fs.write(path, "{}\n")


def _seed_if_present(ctx, src, dst, overwrite=False):
    if src == "" or dst == "":
        return
    if ctx.fs.exists(dst) and not overwrite:
        return
    if ctx.fs.exists(src):
        ctx.fs.write(dst, ctx.fs.read(src))


def main(ctx):
    work_dir = ctx.inputs["work_dir"].rstrip("/")
    goal_dir = str(ctx.inputs.get("goal_dir") or "").rstrip("/")
    path = work_dir + "/log.jsonl"
    reset = ctx.inputs.get("reset")
    if _is_reset(reset):
        ctx.fs.write(path, "")
        _reset_derived(ctx, work_dir)
        if goal_dir != "":
            _seed_if_present(ctx, goal_dir + "/log.jsonl", path, True)
            _seed_if_present(ctx, goal_dir + "/plan-evolution.jsonl", work_dir + "/plan-evolution.jsonl", True)
    elif not ctx.fs.exists(path):
        if goal_dir != "":
            _seed_if_present(ctx, goal_dir + "/log.jsonl", path)
            _seed_if_present(ctx, goal_dir + "/plan-evolution.jsonl", work_dir + "/plan-evolution.jsonl")
        if not ctx.fs.exists(path):
            ctx.fs.write(path, "")
    return {"log_path": path}

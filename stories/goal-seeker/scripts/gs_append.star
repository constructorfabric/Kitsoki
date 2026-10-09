# gs_append.star — append one structured entry to the append-only log.
#
# Deterministic port of goal.py:append_entry. Computes the next seq from the
# existing <work_dir>/log.jsonl, appends one JSON line, and writes the file back
# (ctx.fs has no append — read-concat-write is the idiom). The log is the SOURCE OF
# TRUTH gs_ledger folds; every state change (an integration, a dispatch, a park)
# is an entry here. Returns the assigned seq + the log path.


def main(ctx):
    work_dir = ctx.inputs["work_dir"].rstrip("/")
    change_id = ctx.inputs["change_id"].strip()
    if change_id == "":
        fail("gs_append: change_id is required")

    path = work_dir + "/log.jsonl"
    existing = ""
    seq = 1
    if ctx.fs.exists(path):
        existing = ctx.fs.read(path)
        maxseq = 0
        for line in existing.split("\n"):
            line = line.strip()
            if line == "":
                continue
            e = json.decode(line)
            s = e.get("seq", 0)
            if s > maxseq:
                maxseq = s
        seq = maxseq + 1

    state = str(ctx.inputs.get("state") or "integrated")
    actor = str(ctx.inputs.get("actor") or "worker")
    entry = {
        "seq": seq,
        "kind": str(ctx.inputs.get("kind") or "integration"),
        "change_id": change_id,
        "state": state,
        "actor": actor,
    }
    park_reason = str(ctx.inputs.get("park_reason") or "")
    if park_reason != "":
        entry["park_reason"] = park_reason
    sha = str(ctx.inputs.get("sha") or "")
    if sha != "":
        entry["sha"] = sha
    summary = str(ctx.inputs.get("summary") or "")
    if summary != "":
        entry["summary"] = summary
    gate_status = str(ctx.inputs.get("gate_status") or "")
    if gate_status == "" and state == "integrated":
        gate_status = "green"
    if gate_status != "":
        entry["gate"] = {"status": gate_status}
    # reviewer-only-green is enforced in gs_ledger.fold: a green gate from a `worker`
    # actor is recorded but NOT counted. Integration entries pass actor=integrator.

    if existing != "" and not existing.endswith("\n"):
        existing = existing + "\n"
    ctx.fs.write(path, existing + json.encode(entry) + "\n")
    return {"seq": str(seq), "log_path": path}

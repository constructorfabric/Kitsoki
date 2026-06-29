# pick_case.star — select the backlog case at case_index for the per-case
# dispatcher (processing). The template mini-language can't index a list by a
# variable, so the selection runs here. Also emits a SCALAR `current_case_id`
# the dispatcher's on_enter guard reads — a freshly-bound nested map field
# (current_case.id) is not reliably visible to a same-pass guard, but a freshly
# bound top-level scalar is (the slidey rendering deck_handle pattern). An empty
# current_case_id is the drain signal (backlog past the end OR budget hit).
#
# Interface (authoritative in pick_case.star.yaml):
#   inputs:  backlog (object), case_index (int), cases_processed (int), case_budget (int)
#   world:   current_case (object), current_case_id (string)
#   outputs: current_case (object), current_case_id (string)

def main(ctx):
    backlog = ctx.inputs.get("backlog", {})
    items = backlog.get("items", []) if type(backlog) == "dict" else []
    idx = int(ctx.inputs.get("case_index", 0))
    processed = int(ctx.inputs.get("cases_processed", 0))
    budget = int(ctx.inputs.get("case_budget", 25))

    if idx < 0 or idx >= len(items) or processed >= budget:
        return {"current_case": {}, "current_case_id": ""}

    case = items[idx]
    cid = case.get("id", "") if type(case) == "dict" else ""
    return {"current_case": case, "current_case_id": cid}

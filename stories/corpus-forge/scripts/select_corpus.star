def main(ctx):
    candidates = ctx.inputs["candidates"]
    task_limit = ctx.inputs["task_limit"]
    corpus_role = ctx.inputs["corpus_role"]
    if task_limit < 1:
        fail("task_limit must be at least one")
    if corpus_role != "calibration" and corpus_role != "heldout":
        fail("corpus_role must be calibration or heldout")
    ordered = sorted(candidates, key = lambda candidate: candidate["id"])
    selected = ordered[:task_limit]
    if len(selected) < task_limit:
        fail("not enough canonical candidates for requested task_limit")
    ids = []
    for candidate in selected:
        ids.append(candidate["id"])
    return {"candidates": selected, "selected_ids": json.encode(ids)}

# accumulate_iteration.star — append one refine iteration to world.iterations.
#
# expr-lang (the `set:` evaluator) has no list-append and forbids map literals,
# so the iteration record is built deterministically here (the same discipline
# as stories/ui-fix/scripts/accumulate.star).
#
# Interface (authoritative in accumulate_iteration.star.yaml):
#   inputs:  video_handle (string?), instruction (string?)
#   world:   iterations (object {items:[...]}), refine_result (object {edited:[...]})
#   outputs: iterations (object {items:[...]})

def main(ctx):
    bucket = ctx.world.get("iterations") or {"items": []}
    if type(bucket) != "dict":
        bucket = {"items": []}
    items = list(bucket.get("items", []))

    refine_result = ctx.world.get("refine_result") or {}
    edited = refine_result.get("edited", []) if type(refine_result) == "dict" else []

    entry = {
        "video_handle":       ctx.inputs.get("video_handle", ""),
        "feedback_addressed": edited,
        "instruction":        ctx.inputs.get("instruction", ""),
    }
    items.append(entry)
    return {"iterations": {"items": items}}

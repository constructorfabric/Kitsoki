# accumulate_addressed.star — append one addressed-annotation record to
# world.addressed. expr-lang (the `set:` evaluator) has no list-append, so the
# record is built deterministically here (the ui-fix discipline).
#
# Interface (authoritative in accumulate_addressed.star.yaml):
#   inputs:  deck_handle (string?), anchor_label (string?), instruction (string?)
#   world:   addressed (object {items:[...]}), refine_result (object {edited:[...]})
#   outputs: addressed (object {items:[...]})

def main(ctx):
    bucket = ctx.world.get("addressed") or {"items": []}
    if type(bucket) != "dict":
        bucket = {"items": []}
    items = list(bucket.get("items", []))

    refine_result = ctx.world.get("refine_result") or {}
    edited = refine_result.get("edited", []) if type(refine_result) == "dict" else []

    entry = {
        "deck_handle":  ctx.inputs.get("deck_handle", ""),
        "anchor_label": ctx.inputs.get("anchor_label", ""),
        "instruction":  ctx.inputs.get("instruction", ""),
        "edited":       edited,
    }
    items.append(entry)
    return {"addressed": {"items": items}}

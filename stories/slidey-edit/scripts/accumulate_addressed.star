# accumulate_addressed.star — append one addressed-annotation record to
# world.addressed. expr-lang (the `set:` evaluator) has no list-append, so the
# record is built deterministically here (the ui-fix discipline).
#
# Interface (authoritative in accumulate_addressed.star.yaml):
#   inputs:  deck_handle (string?), anchor_label (string?), instruction (string?),
#            edited (list?) — the refs the reviser touched this pass
#   world:   addressed (object {items:[...]})
#   outputs: addressed (object {items:[...]})
#
# `edited` is passed as a machine-resolved INPUT (`{{ world.refine_result.edited }}`),
# NOT read from ctx.world: this script runs as a TRANSITION effect (the rerender
# handler), where ctx.world reflects a later snapshot than the per-position
# machine-resolved inputs — reading refine_result off ctx.world here came back
# empty even though refine_result was still set when the effect fired. The input is
# resolved at this effect's position (before the same handler's reset clears it).

def main(ctx):
    bucket = ctx.world.get("addressed") or {"items": []}
    if type(bucket) != "dict":
        bucket = {"items": []}
    items = list(bucket.get("items", []))

    edited = ctx.inputs.get("edited")
    if type(edited) != "list":
        edited = []

    entry = {
        "deck_handle":  ctx.inputs.get("deck_handle", ""),
        "anchor_label": ctx.inputs.get("anchor_label", ""),
        "instruction":  ctx.inputs.get("instruction", ""),
        "edited":       edited,
    }
    items.append(entry)
    return {"addressed": {"items": items}}

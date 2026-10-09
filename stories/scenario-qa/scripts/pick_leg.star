# pick_leg.star — select the leg at leg_index for the execute room. The
# template mini-language can't index a list by a variable (same reason
# stories/dogfood-marathon's pick_case.star exists), so the selection runs
# here.
#
# Interface (authoritative in pick_leg.star.yaml):
#   inputs:  legs (object {items:[...]}), leg_index (int)
#   outputs: current_leg (object), current_leg_id (string),
#            current_leg_json (string — for agent prompt context.args, where
#            a raw object value renders as a Go map string, not JSON)

def main(ctx):
    legs = ctx.inputs.get("legs", {})
    items = legs.get("items", []) if type(legs) == "dict" else []
    idx = int(ctx.inputs.get("leg_index", 0))

    if idx < 0 or idx >= len(items):
        return {"current_leg": {}, "current_leg_id": "", "current_leg_json": "{}"}

    leg = items[idx]
    leg_id = leg.get("leg_id", "") if type(leg) == "dict" else ""
    return {
        "current_leg": leg,
        "current_leg_id": leg_id,
        "current_leg_json": json.encode(leg),
    }

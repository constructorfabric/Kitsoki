# serialize_plan.star — deterministic object→string bridge.
#
# Templates can render an object's fields but cannot JSON-encode a whole object,
# and the splitting room needs the agent's bucket plan as a single JSON string to
# hand to git. This reads the live `buckets` artifact off the world snapshot and
# returns it as a compact JSON string. No I/O, no clock, no randomness — pure.

def main(ctx):
    buckets = ctx.world.get("buckets") or {}
    return {"buckets_json": json.encode(buckets)}

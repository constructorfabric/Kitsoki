# accumulate.star — append current group to a named world bucket (fixed/skipped/still_failing).
# Reads the bucket and current group from world directly.
#
# Interface (authoritative in accumulate.star.yaml):
#   inputs:  bucket_key (string), media_handle (string?), reason (string?)
#   world:   <bucket_key>, current
#   outputs: bucket (object)

def main(ctx):
    bucket_key = ctx.inputs["bucket_key"]
    bucket = ctx.world.get(bucket_key) or {"items": []}
    if type(bucket) != "dict":
        bucket = {"items": []}
    items = list(bucket.get("items", []))

    current = ctx.world.get("current") or {}
    entry = {
        "group":        current,
        "media_handle": ctx.inputs.get("media_handle", ""),
        "reason":       ctx.inputs.get("reason", ""),
    }
    items.append(entry)
    return {"bucket": {"items": items}}

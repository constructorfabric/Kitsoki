# accumulate.star — append current group to a named world bucket (fixed/skipped/still_failing).
# Reads the bucket and current group from world directly.
#
# Interface (authoritative in accumulate.star.yaml):
#   inputs:  bucket_key (string), media_handle (string?), reason (string?)
#   world:   <bucket_key>, current
#   outputs: bucket (object), report_lines (string)

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

    line_key = bucket_key + "_report_lines"
    existing = ctx.world.get(line_key) or ""
    group_id = str(current.get("id") or "")
    title = str(current.get("title") or group_id or "group")
    artifact = str(ctx.inputs.get("media_handle") or ctx.inputs.get("reason") or "")
    line = group_id + "|" + title.replace("|", "/") + "|" + artifact.replace("\n", " ")
    if existing:
        line = existing.rstrip("\n") + "\n" + line
    return {"bucket": {"items": items}, "report_lines": line + "\n"}

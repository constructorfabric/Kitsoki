# get_current.star — extract groups.items[cursor] as the current group.
# Reads world.groups directly.
#
# Interface (authoritative in get_current.star.yaml):
#   inputs:  cursor (int)
#   world:   groups
#   outputs: item (object)

def main(ctx):
    groups = ctx.world.get("groups") or {"items": []}
    cursor = ctx.inputs["cursor"]
    items = groups.get("items", [])
    if type(items) != "list" or cursor < 0 or cursor >= len(items):
        return {"item": {}}
    return {"item": items[cursor]}

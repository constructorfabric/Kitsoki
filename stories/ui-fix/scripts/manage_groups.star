# manage_groups.star — mechanical group list operations for the review room.
#
# Reads world.groups directly. Takes op + id params as string inputs.
#
# Interface (authoritative in manage_groups.star.yaml):
#   inputs:  op (string), a (string?), b (string?), ids_str (string?)
#   world:   groups
#   outputs: groups (object)

def _find_by_id(items, gid):
    for i, g in enumerate(items):
        if g.get("id", "") == gid:
            return i, g
    return -1, None

def main(ctx):
    groups = ctx.world.get("groups") or {"items": []}
    op = ctx.inputs["op"]
    items = list(groups.get("items", []))

    if op == "drop":
        gid = ctx.inputs.get("a", "")
        items = [g for g in items if g.get("id", "") != gid]

    elif op == "reorder":
        ids_str = ctx.inputs.get("ids_str", "")
        new_order = [s.strip() for s in ids_str.split(",") if s.strip()]
        id_map = {g.get("id", ""): g for g in items}
        reordered = [id_map[gid] for gid in new_order if gid in id_map]
        mentioned = {gid: True for gid in new_order}
        for g in items:
            if g.get("id", "") not in mentioned:
                reordered.append(g)
        items = reordered

    elif op == "merge":
        gid_a = ctx.inputs.get("a", "")
        gid_b = ctx.inputs.get("b", "")
        idx_a, grp_a = _find_by_id(items, gid_a)
        idx_b, grp_b = _find_by_id(items, gid_b)
        if grp_a != None and grp_b != None:
            sev_a = SEVERITY_RANK.get(grp_a.get("severity", "info"), 1)
            sev_b = SEVERITY_RANK.get(grp_b.get("severity", "info"), 1)
            merged = {
                "id":             grp_a.get("id", ""),
                "title":          grp_a.get("title", "") + " + " + grp_b.get("title", ""),
                "pattern":        grp_a.get("pattern", ""),
                "root_cause":     grp_a.get("root_cause", ""),
                "severity":       grp_a.get("severity", "info") if sev_a >= sev_b else grp_b.get("severity", "info"),
                "member_ids":     grp_a.get("member_ids", []) + grp_b.get("member_ids", []),
                "surfaces":       list({s: True for s in grp_a.get("surfaces", []) + grp_b.get("surfaces", [])}.keys()),
                "viewports":      list({v: True for v in grp_a.get("viewports", []) + grp_b.get("viewports", [])}.keys()),
                "before_frames":  list({f: True for f in grp_a.get("before_frames", []) + grp_b.get("before_frames", [])}.keys()),
                "recommendation": grp_a.get("recommendation", ""),
            }
            new_items = []
            for g in items:
                gid = g.get("id", "")
                if gid == gid_a:
                    new_items.append(merged)
                elif gid == gid_b:
                    pass
                else:
                    new_items.append(g)
            items = new_items

    return {"groups": {"items": items}}

SEVERITY_RANK = {"error": 3, "warn": 2, "info": 1}

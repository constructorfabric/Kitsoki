# gate_edited_scene.star — enforce that the reviser edited ONLY the slide the
# operator was looking at. The refine task reports the element refs it touched
# (`edited`, each "<scene>/<el>"). Every one must share the resolved scene_index;
# a stray edit to another slide is the "edited the wrong slide" bug, so we flag
# it and the room re-renders unchanged + tells the operator instead of shipping a
# silent wrong-slide change.
#
# Pure validation — slidey-specific ref shape lives in the story, not core.
#
# Interface (authoritative in gate_edited_scene.star.yaml):
#   inputs:  scene_index (int, required), edited (list?), scene_label (string?),
#            summary (string?)  — the reviser's own one-line summary
#   outputs: ok (bool), stray (list of string), edited_count (int),
#            message (string) — ready-to-show summary (reviser's on success, an
#            honest wrong-slide / no-op note otherwise)

def main(ctx):
    scene_index = ctx.inputs.get("scene_index")
    if scene_index == None:
        scene_index = -1
    edited = ctx.inputs.get("edited") or []
    label = ctx.inputs.get("scene_label") or "the slide"
    summary = ctx.inputs.get("summary") or "Applied your edit."

    # No identified slide → the gate can't vouch for anything: not ok.
    if scene_index < 0:
        return {
            "ok": False,
            "stray": [str(r) for r in edited],
            "edited_count": len(edited),
            "message": "⚠️ I couldn't tell which slide you meant, so nothing was changed — point at a slide and try again.",
        }

    want = str(scene_index)
    stray = []
    for ref in edited:
        r = str(ref)
        head = r.split("/")[0] if "/" in r else r
        if head.strip() != want:
            stray.append(r)

    if len(edited) == 0:
        return {"ok": False, "stray": [], "edited_count": 0,
                "message": "Nothing changed on %s — try rewording what you want edited." % label}

    if len(stray) > 0:
        return {"ok": False, "stray": stray, "edited_count": len(edited),
                "message": "⚠️ That edit touched the wrong slide (%s), not %s — try again." % (", ".join(stray), label)}

    return {"ok": True, "stray": [], "edited_count": len(edited), "message": summary}

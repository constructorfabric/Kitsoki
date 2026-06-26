# resolve_scene.star — resolve WHICH slide the refine targets and hand the
# reviser that scene's CURRENT content, so the edit lands on the slide the
# operator is looking at (not a guessed default).
#
# A deck is many scenes. The operator points at ONE — via (in priority order):
#   1. current_scene — the scope the live embed plugin reports for the slide on
#      screen (the authoritative "slide we're looking at").
#   2. the anchor ref "<scene>/<el>" — a real element pick carries its scene.
# When NEITHER is present we DO NOT guess a scene; we return scene_index = -1 so
# the room can ask the operator which slide instead of silently editing slide 1.
#
# Outputs feed the reviser prompt (the resolved scene's JSON + a human label) and
# the gate (the scene_index every `edited` ref must share). All slidey-specific
# knowledge lives HERE in the story — kitsoki core stays producer-agnostic.
#
# Interface (authoritative in resolve_scene.star.yaml):
#   inputs:  spec_path (string, required), anchor_ref (string?), current_scene (string?)
#   outputs: scene_index (int), scene (object), scene_json (string),
#            scene_label (string), scene_count (int)

def _scene_from_ref(ref):
    # "<scene>/<el>" → scene int, or -1 when unparseable.
    if type(ref) != "string" or "/" not in ref:
        return -1
    head = ref.split("/")[0].strip()
    if head == "" or not head.isdigit():
        return -1
    return int(head)

def _label_for(idx, scene):
    title = scene.get("title") or scene.get("eyebrow") or scene.get("lede") or scene.get("type") or ""
    if title:
        return "Scene %d · %s" % (idx, title)
    return "Scene %d" % idx

def main(ctx):
    spec_path = (ctx.inputs.get("spec_path") or "").strip()
    if spec_path == "":
        return {"scene_index": -1, "scene": {}, "scene_json": "{}", "scene_label": "(no deck)", "scene_count": 0}

    if not ctx.fs.exists(spec_path):
        return {"scene_index": -1, "scene": {}, "scene_json": "{}", "scene_label": "(deck not found)", "scene_count": 0}

    deck = json.decode(ctx.fs.read(spec_path))
    scenes = deck.get("scenes") or deck.get("slides") or []
    count = len(scenes)

    # 1) the viewed scene the embed plugin reported, when present.
    idx = -1
    cur = ctx.inputs.get("current_scene")
    if cur != None and str(cur).strip() != "":
        s = str(cur).strip()
        if s.isdigit():
            idx = int(s)

    # 2) else the anchor ref's scene prefix ("<scene>/<el>").
    if idx < 0:
        idx = _scene_from_ref(ctx.inputs.get("anchor_ref") or "")

    # No usable signal → -1 (the room asks which slide rather than guessing).
    if idx < 0 or idx >= count:
        return {"scene_index": -1, "scene": {}, "scene_json": "{}", "scene_label": "(no slide identified)", "scene_count": count}

    scene = scenes[idx]
    return {
        "scene_index": idx,
        "scene": scene,
        "scene_json": json.encode(scene),
        "scene_label": _label_for(idx, scene),
        "scene_count": count,
    }

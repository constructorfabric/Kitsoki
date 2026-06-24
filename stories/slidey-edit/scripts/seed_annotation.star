# seed_annotation.star — seed a default LOCATION-TIED annotation when none is
# attached, so the refine loop is drivable from a baked anchor (kitsoki tour/web
# with no live point). A live surface that attaches args.visual.anchor lands its
# own bundle in world.annotation and this is skipped (the room's `once:` is keyed
# on annotation).
#
# The seeded anchor is the CANONICAL `semantic_element` target — the exact shape
# a real pick serializes from the deck's `.semantic.json` sidecar (see
# .context/unified-artifact-annotation.md and host.AnchorSemanticElementTarget):
#
#   anchor:
#     kind: semantic_element
#     semantic_element: { plugin: "slidey", ref: "<scene>/<el>", bbox: [x,y,w,h] }
#     label: "<display>"
#
# `ref` is an OPAQUE STRING ("<sceneIndex>/<el>") round-tripped verbatim back to
# slidey — NOT an object. The AnnotationAnchor union also admits {kind: region,
# region:{shape,path,bbox}} and {kind: dom_node, ...}; the seeded default uses
# semantic_element on a real rendered element so the refine pass demonstrates the
# element→scene resolution against genuine refs + bboxes from baked/deck.semantic.json.
#
# The seeded ref + bbox below are the REAL output of rendering baked/deck.json
# through slidey: scene 1 (the "One anchor union" cards row), card 0 — the
# "semantic_element" card. bbox is in 1920×1080 render pixels.
#
# Interface (authoritative in seed_annotation.star.yaml):
#   inputs:  spec_path (string?), frame_handle (string?)
#   world:   annotation (object)
#   outputs: annotation (object)

def main(ctx):
    existing = ctx.world.get("annotation") or {}
    if type(existing) == "dict" and existing.get("anchor"):
        # A live bundle is already attached — leave it.
        return {"annotation": existing}

    anchor = {
        "kind": "semantic_element",
        "semantic_element": {
            "plugin": "slidey",
            "ref": "1/card_0",
            "bbox": [140, 518, 535, 114],
        },
        "label": "Scene 1 · card 0",
    }
    return {
        "annotation": {
            "anchor": anchor,
            "instruction": "Make the semantic_element card stand out and add a one-line example beneath it.",
            "frame_handle": ctx.inputs.get("frame_handle", ""),
        },
    }

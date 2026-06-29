# build_deck.star — produce the slidey report deck SPEC reference from the run
# data. Returns {spec_path, summary, scenes_preview}.
#
# ctx.fs is READ-ONLY (read / exists / glob — no write), so this script does NOT
# write the deck JSON itself. The deck spec is materialized two ways:
#   - LIVE drive: the kitsoki-mcp-driver / operator writes the deck JSON from the
#     journaled results (the same way slidey decks are authored) to out_path.
#   - flow / deterministic: a BAKED report deck lives at out_path
#     (baked/report.deck.json) so host.slidey.render renders a real deck without
#     a live author — the slidey-edit baked-deck discipline.
# Either way this script returns the spec_path (out_path) the slideshow room
# renders, plus a data-derived summary built ONLY from the recorded rollup —
# never a fabricated outcome.
#
# Interface (authoritative in build_deck.star.yaml):
#   inputs:  results (object), rollup (object), findings (object), out_path (string)
#   world:   deck_spec (object)
#   outputs: deck_spec (object {spec_path, summary})

def main(ctx):
    out_path = ctx.inputs.get("out_path", "stories/dogfood-marathon/baked/report.deck.json")
    rollup = ctx.inputs.get("rollup", {})
    counts = rollup.get("counts", {}) if type(rollup) == "dict" else {}

    processed = counts.get("processed", 0)
    solved = counts.get("solved", 0)

    summary = "Dogfood marathon report — %d case(s), %d solved." % (processed, solved)

    # A short preview of the scenes the deck encodes (for views/audit); the real
    # rendered scenes live in the deck JSON at out_path.
    scenes_preview = [
        "title: Dogfood marathon report",
        "rollup: counts + cost/tokens/time totals",
        "outcomes: per-case triage / exit / independent-verify",
        "what worked / what didn't",
        "findings → no-overfit hardening",
    ]

    return {
        "deck_spec": {
            "spec_path": out_path,
            "summary": summary,
            "scenes_preview": scenes_preview,
        },
    }

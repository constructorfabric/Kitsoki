# Bugfix bake-off narrative deck

`deck.json` is the slidey spec for the **narrative** presentation of the
bake-off (the persuasion deck — distinct from the data deck that
`eval_pilot_report.py --deck` renders from results). It tells the
structure-vs-model story in ~10 slides; the companion written report is
[`docs/case-studies/bugfix-bakeoff.md`](../../../docs/case-studies/bugfix-bakeoff.md).

## Status: placeholders pending live data

Every results-dependent value in the deck is marked **`TBD from
summary.json`**. Fill those in from
[`results/summary.json`](../results/SCHEMA.md) (produced by `aggregate.py`
after the live grid runs) **before** baking — the deck should never ship
with `TBD` text.

## How it bakes

The spec follows the same slidey schema as
[`stories/slidey-edit/baked/deck.json`](../../../stories/slidey-edit/baked/deck.json)
(scene types `title` / `cards` / `narrative`; validated against
[`stories/slidey-edit/schemas/deck.json`](../../../stories/slidey-edit/schemas/deck.json)).
**Commit only the `.slidey.json` spec.** The HTML is a deterministic render of
the JSON — preview it with the **VS Code kitsoki extension** (renders the deck
from the spec, no bake step) or bundle it locally for an off-disk file. A
committed `.slidey.html` is just a stale multi-MB duplicate, so `*.slidey.html`
is gitignored.

```bash
# validate the spec
python3 -m json.tool docs/decks/bugfix-bakeoff.slidey.json > /dev/null

# OPTIONAL local preview — bundle to a single self-contained HTML under
# .artifacts/ (gitignored), never into docs/decks/:
slidey bundle docs/decks/bugfix-bakeoff.slidey.json .artifacts/bugfix-bakeoff/deck.html
```

To author/edit interactively (render → annotate → refine), drive it through the
[`slidey-edit`](../../../stories/slidey-edit/) story.

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
It bakes to a self-contained **static HTML deck** with `slidey bundle` —
the exact bake path documented in
[`stories/slidey-edit/README.md`](../../../stories/slidey-edit/README.md)
(see "Baked demo artifacts"):

```bash
# validate
python3 -m json.tool tools/bugfix-bakeoff/deck/deck.json > /dev/null

# bake to a single self-contained HTML file (no server, opens off disk)
slidey bundle tools/bugfix-bakeoff/deck/deck.json tools/bugfix-bakeoff/deck/deck.html
```

The baked `deck.html` inlines the whole slidey SPA (several MB) and should
**not** be committed — keep generated output under `.artifacts/` or
gitignore it, per the repo artifact conventions. To author/edit
interactively (render → annotate → refine), drive it through the
[`slidey-edit`](../../../stories/slidey-edit/) story.

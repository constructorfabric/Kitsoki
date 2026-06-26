# Slidey Report Decks

Kitsoki report decks should be deterministic review artifacts. The deck structure
comes from code, and the numbers/statuses/links come from workflow outputs such
as `summary.json`, scored cell JSON, onboarding apply results, gate verdicts, or
recorded media metadata. Do not ask an LLM to draft a free-form deck.

Use the standardized builder:

```bash
python3 tools/report-deck/deterministic_deck.py \
  --kind bakeoff-summary \
  --input tools/bugfix-bakeoff/results/summary.json \
  --job bugfix-bakeoff \
  --run-id 2026-06-26T00:00:00Z
```

Supported deck kinds:

- `bakeoff-summary`: matrix/task bakeoffs from `tools/bugfix-bakeoff/results/summary.json`.
- `external-summary`: external repo bakeoffs from `tools/bugfix-bakeoff/external/results/summary.json`.
- `onboarding`: project onboarding review decks from discovery/apply JSON.
- `product-journey`: product-journey catalog/check reports, preserving
  `docs/decks/product-journey-eval.slidey.json` as the hand-refined reference.
- `workflow`: generic bulk/fan-out/dynamic workflow decks with objectives,
  evidence, work-item tables, next steps, and optional media scenes.
- `feature-demo`: deterministic feature-development demo decks with personas and
  rrweb/video scenes.
- `bug-report`: deterministic bug report decks with reproducer, evidence, and
  rrweb/video playback.
- `fanout`: bulk/fan-out job decks with succeeded, failed, retried, pending, and
  skipped item status tables.
- `dynamic-workflow`: generated dynamic-workflow receipt/export decks from
  `.artifacts/dynamic-workflows/<workflow-id>/receipt.json` or export reports.

For hybrid demos, keep the same deterministic spine and reference existing media
artifacts. The `workflow` kind accepts media entries with `rrweb` plus
`chapters:"auto"`, matching the pattern in `docs/decks/dev-story-hybrid.slidey.json`.

`docs/decks/` is for curated, hand-refined reference decks such as the hybrid
demo and product-journey reference. Generated job decks should go under
`.artifacts/<job>/<run-id>/` (or another explicit `.artifacts` job folder) so a
new run never clobbers an older report. Put generated HTML, MP4s, temporary deck
drafts, and QA output under `.artifacts/` too.

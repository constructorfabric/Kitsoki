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
  --out tools/bugfix-bakeoff/results/deck.slidey.json \
  --markdown tools/bugfix-bakeoff/results/report.md
```

Supported deck kinds:

- `bakeoff-summary`: matrix/task bakeoffs from `tools/bugfix-bakeoff/results/summary.json`.
- `external-summary`: external repo bakeoffs from `tools/bugfix-bakeoff/external/results/summary.json`.
- `onboarding`: project onboarding review decks from discovery/apply JSON.
- `workflow`: generic bulk/fan-out/dynamic workflow decks with objectives,
  evidence, work-item tables, next steps, and optional media scenes.

For hybrid demos, keep the same deterministic spine and reference existing media
artifacts. The `workflow` kind accepts media entries with `rrweb` plus
`chapters:"auto"`, matching the pattern in `docs/decks/dev-story-hybrid.slidey.json`.

Commit `.slidey.json` specs that are intended as durable docs. Put generated HTML,
MP4s, temporary deck drafts, and QA output under `.artifacts/`.

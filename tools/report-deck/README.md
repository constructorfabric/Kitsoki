# Deterministic Report Decks

`deterministic_deck.py` builds standardized Slidey JSON decks from structured
job artifacts. It is an offline renderer: it never asks an LLM to draft a deck.
LLM-authored text may appear only when it is already present in a validated gate
artifact or summary JSON that the job provides as input.

## Contract

- Jobs write `summary.json`, `report.md`, and `deck.slidey.json` together under
  a run-specific `.artifacts/<job>/<run>/` folder.
- Jobs pass structured data to this tool with `--input` or `--input-json`.
- Use `--job <name> --run-id <id>` when the caller wants the tool to derive the
  `.artifacts/` output path. Use `--out` only when the job already owns a
  run-specific artifact folder.
- Runtime decks should link objective status, gate outputs, artifact paths, and
  media paths for review. Feature demos and bug reports should include rrweb or
  video media through the structured `media` list.
- Curated reference decks may live in `docs/decks/`, but generated job outputs
  must not write there.

Example:

```sh
python3 tools/report-deck/deterministic_deck.py \
  --kind workflow \
  --input .artifacts/my-job/run-001/summary.json \
  --out .artifacts/my-job/run-001/deck.slidey.json
```

The generated deck carries a `_comment` that points back to this tool; fix the
source summary or the job-specific report script rather than hand-editing the
generated JSON.

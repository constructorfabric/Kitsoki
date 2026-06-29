# PRD: Speaker-Notes Export

> Status: **Published** · Owner: Product · Source idea: "give presenters a printable per-scene script"

## Problem & Context

A slidey deck is a single JSON spec that renders to video, PDF, and an
interactive web player. But a presenter rehearsing a talk has **no printable,
per-scene handout** of what each scene actually says — the narration and on-screen
copy live inside the render pipeline, not in any reviewable document. Today the
only way to read the script is to scrub the rendered video or open the raw JSON.

## Target Users

- **Presenters** rehearsing a talk who want a script to read away from the screen.
- **Deck authors** reviewing flow and wording before a dry run.
- **Reviewers** giving notes on copy without opening the renderer.

## Goals

- A `slidey deck.json --notes out.md` flag that emits a per-scene handout
  (eyebrow + body + narration) from the *same* spec — one Markdown section per
  scene, in spec order.
- **Deterministic and LLM-free**: same spec in, same notes out. No model in the
  loop, byte-stable output suitable for diffing in review.
- Zero new authoring surface — it reads the existing deck spec, nothing else.

## Non-Goals

- Re-rendering the video or PDF (this is a text export only).
- Editing or round-tripping notes back into the spec.
- Translating or rewording narration — it is emitted verbatim.

## Requirements

### Functional

- One Markdown `##` section per scene, titled with the scene eyebrow (falling
  back to `Scene N` when absent), in spec order.
- Each section emits, when present: the **body** copy, the **narration** text,
  and the scene **type** as a small label.
- Scenes with no narration emit the section with an explicit *(no narration)*
  marker rather than being silently dropped — a reviewer must see every scene.
- Output is written to the `--notes` path; `-` writes to stdout.

### Non-functional

- **Headline metric**: decks-exported-to-notes-without-rendering-video.
- Deterministic: identical bytes for identical input across runs and machines.
- Runs in well under a second for a 50-scene deck (pure spec read, no render).

## Acceptance Criteria

- `slidey deck.json --notes out.md` produces a Markdown file with exactly one
  section per scene, in order.
- Running it twice on the same spec produces byte-identical output.
- A scene with no narration still appears, with the *(no narration)* marker.
- The command exits non-zero with a clear message when the spec is invalid.

## Edge Cases

- Empty deck (no scenes) → a valid file with a heading and a "no scenes" note.
- Very long narration → emitted verbatim, no truncation.
- Duplicate eyebrows → sections are still distinct (suffixed by index).

## Rollout

1. Land `--notes` behind the existing CLI with a unit test over a fixture deck.
2. Document the flag in the slidey CLI reference and the authoring guide.
3. Announce in the changelog; no migration required (purely additive).

## Open Questions

- Should scenes with no narration be skipped or emitted empty? *(Resolved:
  emitted empty with a marker, per the requirement above.)*
- Do we want a `--notes-format` switch for plain text vs Markdown later? Deferred.

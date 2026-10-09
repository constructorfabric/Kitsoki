# Goal-Seeker Tour Clips

This directory holds deterministic rrweb clips used by the goal-seeker PM deck
pipeline. They are intentionally no-LLM artifacts: the report generator embeds
the JSON directly from `docs/goals/generalized-usage/video-catalog.md`, and the
flow gate proves the generated decks reference non-empty rrweb clips.

Long-form, visually rich captures can replace these fixtures later without
changing the catalog contract: one row per `demo_video:` change, with
compendium-worthy clips marked in the `Compendium` column.

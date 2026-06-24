# `issues/features/` — kitsoki PRD-track features

Empty by design until Wave 3 / Phase 5 lands `stories/cypilot/`.

cypilot's artifact pipeline reads files from this directory (matching
the bug format in `docs/stories/bugs.md` with `target: kitsoki` and a
`feature: true` frontmatter flag, or one of the planned subtypes
`spec` / `prd` / `design`). The pipeline produces PRD + DESIGN +
DECOMPOSITION artifacts and either commits them back here or hands
them off to the bugfix story for implementation.

Until Phase 5 lands, the dogfood's `ticket_globs:` covers this
directory for completeness — a feature file dropped here will surface
in the `tickets` search and route through the (Wave 3-stub) cypilot
sub-story.

For the canonical bug-format frontmatter spec, see `../README.md`.

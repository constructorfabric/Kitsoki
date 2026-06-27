# Cross-repo onboarding and harness readiness

This case study summarizes onboarding coverage we used to validate that Kitsoki’s
readiness contract works beyond a single repository.

## Scope

- `gears-rust` — initial live onboarding pass with project-profile inference and command discovery.
- `slidey` — harness smoke pass across dev/design/bugfix stories in the deck-focused story family.
- `postgresql` — ready-heavy-check validation with deterministic local oracle context.
- `kubernetes` — ready-heavy-check validation with deterministic local oracle context.

## Evidence snapshots

- `stories/dev-story/app.yaml` on live onboarding surfaced the inferred project profile for `gears-rust` and produced reproducible trace artifacts.
- `.context/slidey-pitch-dogfood-pass-2026-06-26.md` captures the slidey story suite results and live MCP pass details.
- `.context/story-qa-run.md` captures the readiness checks for `postgresql` and `kubernetes`.

## Outcome

- Onboarding is now treated as a per-target gate, not a repo-specific one-off.
- The pipeline reuses the same MCP contract for discovery, profile drafting, status checks, and trace capture.
- The same onboarding output is used as readiness input before any costed live fix or proposal work starts.

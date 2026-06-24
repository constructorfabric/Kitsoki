# Slidey: speaker-notes export

An epic to add a spec-derived speaker-notes handout to slidey, alongside the
existing video / PDF / web outputs. Three independently-shippable slices.

## Slice 1 — notes model
Add a `SceneNotes` struct in `internal/notes/` carrying the per-scene narration
plus the scene title and ordinal, derived from the existing deck spec. Model and
unit tests only — no rendering yet.

Gate: `go test ./internal/notes/...`

## Slice 2 — notes renderer
Add a `RenderHandout` function in `internal/notes/render/` that turns a slice of
`SceneNotes` into a printable per-scene handout (one block per scene).
Depends on slice 1 (consumes the notes model).

Gate: `go test ./internal/notes/render/...`

## Slice 3 — wire `--notes` CLI flag
Register a `--notes <path>` flag on the slidey CLI that runs the renderer over
the parsed deck spec and writes the handout. Add a smoke-test asserting the file
is produced.
Depends on slice 2 (invokes the renderer).

Gate: `go test ./cmd/slidey/...`

# Bugfix task — staged, in-scope

You are fixing a single bug in the kitsoki repository (Go + TypeScript). You are
working in a hermetic worktree checked out at the commit BEFORE the fix, so the
bug is present. Follow the stages below in order. Do not edit unrelated code.

## Bug context

**Component:** tui · **Severity:** P3

**Symptom:** The TUI runs every state's `view:` through Glamour with
`glamour.WithPreservedNewLines()`. That setting is required for structured views
(bullet lists, indented examples) where each authored line must stay on its own
line — but it also CAPS pure-prose views at their authored hand-wrapped width.
A foyer view hand-wrapped at ~65 chars/line sits in a narrow column even on a
150-col terminal; the right half of the panel is blank. Shrinking the terminal
works (Glamour re-wraps longer-than-panel lines); growing past the authored wrap
is a no-op.

**Expected:** prose blocks reflow to the available panel width; structured
blocks (lists, code) preserve their layout.

**Actual:** every authored line is preserved verbatim regardless of panel width;
prose can't grow past its authored wrap.

**Investigation hints:**
- `internal/tui/transcript.go` — `renderMarkdown` and the Glamour config
  (`WithPreservedNewLines`).
- The fix may introduce a way to distinguish prose blocks (reflowable) from
  structured blocks (layout-preserved) so each renders correctly — e.g. a typed
  view-element path, or detecting prose vs structured content.

## Stages — do these IN ORDER

1. **REPRODUCE (RED first).** Write a focused failing test that reproduces the
   bug: render a pure-prose view, hand-wrapped narrow, at a wide panel width, and
   assert the rendered output reflows to (approximately) the panel width rather
   than staying capped at the authored wrap. Run it and CONFIRM IT IS RED. Do not
   proceed until you have a red test that fails for the right reason.
2. **IMPLEMENT (minimal fix).** Make the smallest change that fixes the root
   cause while keeping structured views (lists/code) intact. Stay in scope.
3. **VERIFY (GREEN + no regressions).** Re-run your test and confirm it is GREEN.
   Then run the surrounding suite and confirm nothing regressed.

## Build / test commands

```
go build ./...
go test ./internal/tui/... ./internal/render/...
go test ./internal/tui/ -run <YourTestName>   # your repro test
```

## Rules

- Write your OWN reproduction test; do not look for or rely on any pre-existing
  hidden regression test.
- Keep the change minimal and in-scope; do not refactor or touch unrelated code.
- Honor the stage order: reproduce (red) → implement → verify (green).

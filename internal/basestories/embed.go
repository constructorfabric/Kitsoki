package basestories

import "embed"

// stories embeds the kitsoki story library so an `import:
// { source: "@kitsoki/<name>" }` can resolve against the binary when no
// on-disk kitsoki checkout is present (see the package doc).
//
// The embedded tree is a STAGED COPY of the repo's top-level stories/,
// placed here by `make embed-stories` (also wired as `go:generate`) — Go's
// //go:embed cannot reference a parent directory, so the library is mirrored
// into this package's own stories/ subdir at build time. The staged copy is
// gitignored; a committed stories/.gitkeep keeps this pattern matching on a
// fresh checkout so `go build` / `go test` compile before staging has run.
// In that un-staged state the only embedded entry is the placeholder, and
// [Materialize] reports [ErrNotStaged] rather than the package failing to
// compile (mirrors internal/runstatus/web's ErrNotBuilt seam).
//
// The `all:` prefix is required so the .gitkeep dotfile placeholder — and any
// dot-prefixed story asset — is embedded.
//
//go:generate make -C ../.. embed-stories
//go:embed all:stories
var stories embed.FS

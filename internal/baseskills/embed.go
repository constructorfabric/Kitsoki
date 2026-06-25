package baseskills

import "embed"

// assets embeds the kitsoki agent toolkit (a STAGED COPY of the repo's
// .agents/skills and .agents/agents, mirrored under assets/skills and
// assets/agents by `make embed-skills`). Go's //go:embed cannot reference a
// parent directory, so the trees are staged into this package's assets/ subdir
// at build time. The staged copy is gitignored; a committed assets/.gitkeep
// keeps this pattern matching on a fresh checkout so `go build`/`go test`
// compile before staging has run. In that un-staged state the only embedded
// entry is the placeholder, and [Materialize] reports [ErrNotStaged].
//
// The `all:` prefix is required so the .gitkeep placeholder and any
// dot-prefixed asset (e.g. SKILL.md frontmatter siblings) are embedded.
//
//go:generate make -C ../.. embed-skills
//go:embed all:assets
var assets embed.FS

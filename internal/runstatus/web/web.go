// Package web embeds the built runstatus single-page app so the kitsoki
// binary can both inline it into self-contained HTML artifacts
// (export-status) and serve it for live mode (status serve) with no Node
// toolchain present at runtime.
//
// The asset (assets/index.html) is produced by `make build` / `make
// install`, which run `pnpm build` under tools/runstatus/ and stage the
// resulting single-file bundle here. That generated file is gitignored;
// only the assets/.gitkeep placeholder is committed, so the //go:embed
// pattern always matches and `go build` / `go test` compile on a fresh
// checkout. When the SPA has not been built, [IndexHTML] returns
// [ErrNotBuilt] at runtime rather than the package failing to compile.
//
// Build dependency: an embedded directory must contain at least one
// matching file for //go:embed to succeed; assets/.gitkeep guarantees
// that. The `all:` prefix is required so the dotfile placeholder is
// embedded.
package web

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:assets
var distFS embed.FS

// ErrNotBuilt is returned by [IndexHTML] when the runstatus SPA has not
// been built into this binary — i.e. only the assets/.gitkeep placeholder
// is present. Run `make build` (which runs `pnpm build` under
// tools/runstatus/) to bundle and stage the UI before building the binary.
var ErrNotBuilt = errors.New("runstatus SPA not built into this binary: run `make build` (needs pnpm) to bundle the UI")

// IndexHTML returns the bytes of the bundled single-file runstatus SPA
// (assets/index.html). All CSS and JS are inlined by vite-plugin-singlefile,
// so this one file is the entire app. It returns [ErrNotBuilt] when the SPA
// has not yet been built and only the placeholder is present.
func IndexHTML() ([]byte, error) {
	b, err := distFS.ReadFile("assets/index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotBuilt
		}
		return nil, err
	}
	return b, nil
}

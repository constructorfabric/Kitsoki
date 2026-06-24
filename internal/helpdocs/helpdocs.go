// Package helpdocs embeds the built help-docs site (the embedded variant of
// the VitePress promo/docs site under tools/site) so `kitsoki web` serves it
// offline at /help/ with no Node toolchain present at runtime.
//
// The assets are produced by `make site-embed`, which builds the site with
// base=/help/ in the "embedded" variant (posters only — demo MP4s are NEVER
// embedded; video pages link out to the hosted site) and stages the multi-file
// dist here. The staged tree is gitignored; only assets/.gitkeep is committed,
// so the //go:embed pattern always matches and `go build` compiles on a fresh
// checkout. When the docs have not been staged, [Handler] serves an actionable
// placeholder page instead of failing — mirroring the runstatus SPA's
// ErrNotBuilt philosophy (internal/runstatus/web).
package helpdocs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:assets
var distFS embed.FS

// Built reports whether the help-docs site is staged into this binary
// (i.e. assets/index.html exists, not just the .gitkeep placeholder).
func Built() bool {
	_, err := fs.Stat(distFS, "assets/index.html")
	return err == nil
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>kitsoki help</title></head>
<body style="font-family: system-ui; max-width: 40rem; margin: 4rem auto; line-height: 1.5">
<h1>Help docs not built into this binary</h1>
<p>Stage them with <code>make site-embed</code> and rebuild, or read the full
docs online at
<a href="https://bsacrobatix.github.io/Kitsoki/">bsacrobatix.github.io/Kitsoki</a>.</p>
</body></html>
`

// Handler serves the embedded help-docs site (mount under /help/ with
// http.StripPrefix). When the site is not staged it serves the actionable
// placeholder for every path rather than erroring — help being absent must
// never break the surface that hosts it.
func Handler() http.Handler {
	if !Built() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuiltPage))
		})
	}
	sub, err := fs.Sub(distFS, "assets")
	if err != nil {
		// Unreachable: "assets" is embedded above. Degrade like not-built.
		return http.NotFoundHandler()
	}
	return http.FileServerFS(sub)
}

// kit_ui.go — the S3c kit-UI vertical slice
// (.context/kits-implementation-plan.md design decision D3): a static asset
// route serving a kit's UI module bundle at /kit/<namespace>/<kit>/ui/*, plus
// index.html injection of the installed-kit registry and a minimal import
// map so a (future) SPA loader can dynamic-import each kit's entry module.
//
// # What this is (and is not)
//
// D3's full target is a much bigger frontend architecture change: the host
// SPA build stops inlining everything (vite-plugin-singlefile today), `vue` +
// a new `@kitsoki/ui-sdk` package become separate pinned ESM assets, kit UI
// modules are standard Vite lib-mode builds, and the SPA's router
// dynamic-imports each installed kit's entry and calls router.addRoute() at
// runtime. None of that build-tooling extraction happens here — it is a
// substantial, separately-scoped effort (see the PR description). What DOES
// land here, deliberately, is the server-side half that doesn't depend on
// any of that: the kit asset route those future kit modules will be served
// from, and the index.html injection point a future SPA loader will read.
// Both are independently useful and testable today (a kit can already ship a
// static HTML/JS bundle reachable at a stable URL), and neither has to be
// redone when the ui-sdk extraction lands later.
//
// Deferred/NOT attempted here (see PR description for the full list):
//   - The @kitsoki/ui-sdk extraction and externalizing vue in the SPA build.
//   - The SPA-side runtime loader (dynamic import + router.addRoute()).
//   - The VSCode-webview validation spike the plan doc flags as "S3c likely"
//     follow-up work.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/kit"
)

// kitUIPathPrefix is the mount point for the kit UI static asset route.
const kitUIPathPrefix = "/kit/"

// handleKitUI serves a kit's provides.ui asset tree at
// /kit/<namespace>/<kit>/ui/<rest...>, resolved against
// <kit-root>/ui/<rest...> on disk. 404s (rather than 500s) for every
// resolution failure — an unknown/mismatched kit, a missing dispatcher, or a
// path that would escape the kit's ui/ directory — since this is a public
// static-asset surface with no reason to distinguish those cases to a client.
func (s *Server) handleKitUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.kits == nil {
		http.NotFound(w, r)
		return
	}

	// Path shape: /kit/<namespace>/<kit>/ui/<rest...>
	rest := strings.TrimPrefix(r.URL.Path, kitUIPathPrefix)
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 4 || parts[2] != "ui" {
		http.NotFound(w, r)
		return
	}
	namespace, kitName, assetPath := parts[0], parts[1], parts[3]

	manifest, ok := s.kits.Kits().Get(kitName)
	if !ok || manifest.Namespace != namespace {
		http.NotFound(w, r)
		return
	}

	uiRoot := filepath.Join(manifest.Dir(), "ui")
	cleaned := path.Clean("/" + assetPath) // collapses ../ segments against a rooted path
	full := filepath.Join(uiRoot, filepath.FromSlash(cleaned))
	// Defense in depth beyond path.Clean: refuse anything that resolves
	// outside uiRoot (e.g. a cleaned-but-still-escaping absolute path on a
	// platform with different separator handling).
	if !strings.HasPrefix(full, filepath.Clean(uiRoot)+string(filepath.Separator)) && full != filepath.Clean(uiRoot) {
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, full)
}

// installedKitUIEntry is one row of the import map / registry blob injected
// into index.html — the minimal shape the SPA's runtime kit-module loader
// (S5) needs to dynamic-import a kit's entry module and register its nav,
// per D3's `provides.ui: [{id, title, entry, nav}]` shape (kit.UIEntry, S5 —
// widened from S3c's bare entry-path-string form).
type installedKitUIEntry struct {
	ModuleID string `json:"module_id"`
	URL      string `json:"url"`
}

// kitImportMapEntries renders one (module id -> served URL) pair per
// provides.ui entry across every installed kit, for the injected <script
// type="importmap">. Module ids are "@<namespace>/<kit>/ui/<entry>" so two
// kits' entries never collide; URLs point at this server's own
// /kit/<namespace>/<kit>/ui/<entry>.mjs route.
func kitImportMapEntries(reg *kit.Registry) []installedKitUIEntry {
	if reg == nil {
		return nil
	}
	var out []installedKitUIEntry
	for _, def := range reg.List() {
		for _, entry := range def.Provides.UI {
			moduleID := fmt.Sprintf("@%s/%s/ui/%s", def.Namespace, def.Kit, entry.Entry)
			url := fmt.Sprintf("%s%s/%s/ui/%s.mjs", kitUIPathPrefix, def.Namespace, def.Kit, entry.Entry)
			out = append(out, installedKitUIEntry{ModuleID: moduleID, URL: url})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModuleID < out[j].ModuleID })
	return out
}

// injectKitRegistry splices a kit-registry <script> and an import-map
// <script> into index.html's <head>, right before its closing tag. Returns
// index unchanged when no dispatcher is attached (no kits installed — the
// common case today) or when </head> isn't found (e.g. a non-standard or
// truncated bundle) — injection is a best-effort enhancement, never a hard
// requirement for serving the SPA.
func (s *Server) injectKitRegistry(index []byte) []byte {
	if s.kits == nil {
		return index
	}
	kits := s.listInstalledKits()
	if len(kits) == 0 {
		return index
	}

	registryJSON, err := json.Marshal(kits)
	if err != nil {
		return index
	}
	importMapEntries := kitImportMapEntries(s.kits.Kits())
	imports := make(map[string]string, len(importMapEntries))
	for _, e := range importMapEntries {
		imports[e.ModuleID] = e.URL
	}
	importMap := map[string]any{"imports": imports}
	importMapJSON, err := json.Marshal(importMap)
	if err != nil {
		return index
	}

	injected := fmt.Sprintf(
		"<script id=\"kitsoki-kits\" type=\"application/json\">%s</script>\n"+
			"<script type=\"importmap\">%s</script>\n</head>",
		registryJSON, importMapJSON,
	)

	const closeHead = "</head>"
	idx := strings.Index(strings.ToLower(string(index)), closeHead)
	if idx < 0 {
		return index
	}
	out := make([]byte, 0, len(index)+len(injected))
	out = append(out, index[:idx]...)
	out = append(out, injected...)
	out = append(out, index[idx+len(closeHead):]...)
	return out
}

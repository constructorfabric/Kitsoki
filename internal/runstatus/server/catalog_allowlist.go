// catalog_allowlist.go — F1 of the portal-product-materialization-plan's
// catalog-routing stage: a browser must never name a raw filesystem path in
// a graph.* RPC's `catalog_path`. This builds the alias -> absolute-path
// allowlist an RPC caller's optional `catalog` alias param resolves
// through, mirroring the SAME convention the POG portal already uses
// client-side for read-only federation (portal/vite.config.ts's
// memberCatalogs(), ~line 186-204, in the separate POG repo): the home
// catalog is always addressable as alias "pog", and each `pog/track/v0`
// node in it whose `repo:` field names a sibling checkout contributes one
// more alias — the target catalog's own `catalog.id` with any "-catalog"
// suffix stripped, falling back to basename(repo) when the target has no
// id or isn't loadable.
//
// This intentionally does NOT reuse internal/mcp/graphsrv.CatalogSet (a
// near-identical alias-resolution type built for graph-MCP on the unlanded
// graph-mcp-p2 branch/worktree) — that type is bound once from CLI flags at
// process start, has no notion of "derive aliases from track nodes in a
// home catalog", and living on an unlanded branch it can't be imported from
// here. The two are deliberately kept in the same alias-derivation SPIRIT
// (reject raw paths, list known aliases on a miss) without being the same
// package; see that file's CatalogSet.Resolve/catalogDefaultAlias for the
// sibling convention this was modeled on.
package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	objectgraph "kitsoki/internal/graph"

	"gopkg.in/yaml.v3"
)

// catalogTrackTypeID is the resolved type-registry id (Node.TypeID, the
// middle segment of a "<pack>/<type>/<version>" schema pin — see
// internal/graph/loader.go's ParseSchemaPin/buildNode) that marks a node as
// a federated-track declaration. Existing TypeID comparisons elsewhere in
// this codebase (internal/graph/propose.go's node.TypeID == "changeset",
// internal/host/graph_read_ops.go's node.TypeID == "changeset") use the
// bare type id the same way.
const catalogTrackTypeID = "track"

// catalogTrackSchemaPack is checked alongside catalogTrackTypeID (via the
// node's full Schema pin) so an unrelated "track" type defined by some
// other schema pack never accidentally federates into the allowlist.
const catalogTrackSchemaPack = "pog/track/"

// homeCatalogAlias is the alias reserved for the server's own home catalog,
// fixed at "pog" to match POG portal's memberCatalogs(), which seeds its
// alias map with the literal `["pog", catalogPath]` pair regardless of the
// home catalog's own `catalog.id`. Keeping this literal (rather than
// deriving it from the home catalog's id like track aliases are) is a
// deliberate F1 design choice: the whole point of this allowlist is that
// both sides of the wire agree on alias spelling without talking to each
// other, and the portal's convention already hard-codes "pog" for "the
// catalog I was launched against" — deriving something else here would
// silently break that parity.
const homeCatalogAlias = "pog"

// CatalogAllowlist maps a caller-supplied alias (never a filesystem path)
// to an absolute catalog path the server is willing to load on that
// caller's behalf. It is the routing surface graph.propose/authorize/
// withdraw/apply/rebase's optional `catalog` RPC param resolves through.
type CatalogAllowlist struct {
	aliases map[string]string // alias -> absolute catalog path
}

// Resolve maps alias to a bound absolute catalog path. ok is false when
// alias is not in the allowlist.
func (a *CatalogAllowlist) Resolve(alias string) (path string, ok bool) {
	if a == nil {
		return "", false
	}
	path, ok = a.aliases[alias]
	return path, ok
}

// Aliases returns the bound aliases in sorted order, for "unknown alias"
// error messages.
func (a *CatalogAllowlist) Aliases() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.aliases))
	for alias := range a.aliases {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

// buildCatalogAllowlist loads homeCatalogPath and derives its alias ->
// absolute-path allowlist: "pog" for the home catalog itself, plus one
// alias per pog/track/v0 node found in it (see package doc comment for the
// derivation). Loading is deliberately done fresh on every call (no
// caching): a home catalog is a small YAML file/bundle, track membership
// changes as track nodes are added/removed, and staleness here would be a
// silent authorization bug (a removed track staying resolvable) — so the
// safe default (reload every time) was chosen over a cache with unclear
// invalidation, per F1's stated scope.
//
// A track node whose target catalog file does not exist, or whose own
// catalog fails to load, is silently skipped rather than failing the whole
// build — an unavailable federation member is simply not addressable, the
// same behavior POG's portal-side memberCatalogs() already has (it swallows
// the load error for exactly this reason).
//
// homeCatalogPath itself failing to load still returns a valid (home-only)
// allowlist rather than an error: callers that never pass a `catalog` alias
// are unaffected by a broken home catalog, and a caller that does name an
// alias gets a clear "unknown alias" rather than every graph.* RPC 500ing.
func buildCatalogAllowlist(homeCatalogPath, repoRoot string) *CatalogAllowlist {
	out := &CatalogAllowlist{aliases: map[string]string{}}

	absHome, err := filepath.Abs(homeCatalogPath)
	if err != nil {
		return out
	}
	out.aliases[homeCatalogAlias] = absHome

	cat, err := objectgraph.LoadCatalog(absHome)
	if err != nil {
		return out
	}

	// homeDir is the base a track node's repo-relative `repo:`/`catalog:`
	// field resolves against. This MUST be repoRoot (the git checkout
	// root), not the home catalog file's own directory: the home catalog
	// conventionally lives at <repoRoot>/pog/catalog.yaml, one level below
	// repoRoot, and every real track's `repo:` field (e.g.
	// "../studio-sassfully") is authored relative to repoRoot to match
	// sibling checkouts on disk — the SAME convention POG portal's
	// vite.config.ts uses client-side (memberCatalogs()'s portfolioRoot,
	// the git common-dir parent, not dirname(catalogPath)). Resolving
	// against the catalog file's own directory instead (as an earlier
	// version of this function did) is off by one path segment for any
	// home catalog nested under a repoRoot subdirectory — the normal case,
	// not an edge case — and was only masked by test/gate fixtures that
	// (wrongly) nested their scratch member repos one level too shallow to
	// match real topology.
	homeDir, err := filepath.Abs(repoRoot)
	if err != nil {
		homeDir = filepath.Dir(absHome)
	}

	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if node.TypeID != catalogTrackTypeID || !strings.HasPrefix(string(node.Schema), catalogTrackSchemaPack) {
			continue
		}
		repo, _ := node.Fields["repo"].(string)
		if repo == "" {
			continue
		}
		override, _ := node.Fields["catalog"].(string)
		var trackCatalogPath string
		if override != "" {
			trackCatalogPath = filepath.Join(homeDir, override)
		} else {
			trackCatalogPath = filepath.Join(homeDir, repo, "pog", "catalog.yaml")
		}
		if _, statErr := os.Stat(trackCatalogPath); statErr != nil {
			continue
		}
		absTrack, err := filepath.Abs(trackCatalogPath)
		if err != nil {
			continue
		}

		alias := ""
		if trackID := peekCatalogID(absTrack); trackID != "" {
			alias = strings.TrimSuffix(trackID, "-catalog")
		}
		if alias == "" {
			alias = filepath.Base(repo)
		}
		out.aliases[alias] = absTrack
	}
	return out
}

// peekCatalogID best-effort reads a catalog file's (or bundle directory's)
// top-level `catalog: {id: ...}` block without going through the full
// internal/graph.LoadCatalog (whose *Catalog deliberately has no field for
// it once nodes are type-checked — internal/graph/loader.go's
// fileSingleCatalog.Catalog / loadBundle's inline catalog-schema peek are
// both read-then-discarded today). Mirrors graph-mcp's readCatalogID
// (internal/mcp/graphsrv/catalog.go on the graph-mcp-p2 branch) — same
// shape, duplicated here rather than imported since that package lives on
// an unlanded branch/worktree.
func peekCatalogID(path string) string {
	filePath := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		filePath = filepath.Join(path, "catalog.yaml")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	var doc struct {
		Catalog struct {
			ID string `yaml:"id"`
		} `yaml:"catalog"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Catalog.ID)
}

// resolveGraphCatalogParam implements the shared `catalog` (alias) /
// `catalog_path` (raw path, back-compat) resolution rule for the bare
// graph.* RPC carriers (graphProposeRPC and friends, graph_rpc.go):
//
//   - Neither given: caller error (each handler already requires one or the
//     other before calling this — see its own "missing" check).
//   - Only catalog_path given: pass it through unchanged (100% backward
//     compatible, zero behavior change for existing callers).
//   - Only catalog given: resolve it through allowlist; error listing known
//     aliases if unresolvable.
//   - Both given: resolve catalog through the allowlist and require it to
//     equal catalog_path exactly — anything else is an ambiguous caller
//     error rather than a silent override, since a portal caller should
//     never legitimately send both.
func resolveGraphCatalogParam(allowlist *CatalogAllowlist, catalogAlias, catalogPath, rpcMethod string) (string, *rpcError) {
	if catalogAlias == "" {
		return catalogPath, nil
	}
	resolved, ok := allowlist.Resolve(catalogAlias)
	if !ok {
		return "", &rpcError{
			Code: codeServerError,
			Message: fmt.Sprintf("%s: catalog %q is not a known alias (known aliases: %s)",
				rpcMethod, catalogAlias, strings.Join(allowlist.Aliases(), ", ")),
		}
	}
	if catalogPath != "" && catalogPath != resolved {
		return "", &rpcError{
			Code: codeServerError,
			Message: fmt.Sprintf("%s: both 'catalog' (%q -> %q) and 'catalog_path' (%q) were given and disagree",
				rpcMethod, catalogAlias, resolved, catalogPath),
		}
	}
	return resolved, nil
}

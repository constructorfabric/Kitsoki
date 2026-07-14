package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trackFixtureTypeRegistry = `type_registry:
  - id: track
    schema: graph-type/v0
`

// writeHomeFixture lays out a home catalog at <root>/pog/catalog.yaml with
// three pog/track/v0 nodes:
//   - track-sassfully: repo only, default <repo>/pog/catalog.yaml location,
//     target catalog.id "sassfully-catalog" -> alias "sassfully".
//   - track-kitsoki: repo + an explicit catalog: override pointing at a
//     non-default path, target catalog.id "kitsoki-toolchain-catalog" ->
//     alias "kitsoki-toolchain".
//   - track-broken: repo names a sibling directory that has no catalog at
//     all — must be silently skipped, not error the whole build.
//
// It also writes the two real target catalogs the resolvable tracks point
// at. The layout mirrors real POG topology, not a flattened one: the home
// checkout (repoRoot) sits under root with its home catalog nested one
// level further at <repoRoot>/pog/catalog.yaml, and sibling checkouts sit
// beside repoRoot (children of root, not of repoRoot) — e.g. root =
// ~/code, repoRoot = ~/code/POG, sassfully = ~/code/studio-sassfully — so
// `repo: ../studio-sassfully` (relative to repoRoot) correctly reaches it.
// Returns root, repoRoot, and the home catalog's absolute path.
func writeHomeFixture(t *testing.T) (root, repoRoot, homePath string) {
	t.Helper()
	root = t.TempDir()
	repoRoot = filepath.Join(root, "pog-home-repo")

	homeDir := filepath.Join(repoRoot, "pog")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	homePath = filepath.Join(homeDir, "catalog.yaml")
	homeYAML := `schema: project-object-graph/seed-catalog/v0
catalog:
  id: pog-program-catalog
` + trackFixtureTypeRegistry + `nodes:
  - schema: pog/track/v0
    id: track-sassfully
    title: sassfully
    status: active
    visibility: internal
    repo: ../studio-sassfully
  - schema: pog/track/v0
    id: track-kitsoki
    title: kitsoki toolchain
    status: active
    visibility: internal
    repo: ../kitsoki-repo
    catalog: ../kitsoki-repo/custom/catalog.yaml
  - schema: pog/track/v0
    id: track-broken
    title: no catalog here
    status: active
    visibility: internal
    repo: ../nowhere
`
	if err := os.WriteFile(homePath, []byte(homeYAML), 0o644); err != nil {
		t.Fatalf("write home catalog: %v", err)
	}

	sassfullyDir := filepath.Join(root, "studio-sassfully", "pog")
	if err := os.MkdirAll(sassfullyDir, 0o755); err != nil {
		t.Fatalf("mkdir sassfully: %v", err)
	}
	sassfullyPath := filepath.Join(sassfullyDir, "catalog.yaml")
	sassfullyYAML := `schema: project-object-graph/seed-catalog/v0
catalog:
  id: sassfully-catalog
nodes: []
`
	if err := os.WriteFile(sassfullyPath, []byte(sassfullyYAML), 0o644); err != nil {
		t.Fatalf("write sassfully catalog: %v", err)
	}

	kitsokiDir := filepath.Join(root, "kitsoki-repo", "custom")
	if err := os.MkdirAll(kitsokiDir, 0o755); err != nil {
		t.Fatalf("mkdir kitsoki custom: %v", err)
	}
	kitsokiPath := filepath.Join(kitsokiDir, "catalog.yaml")
	kitsokiYAML := `schema: project-object-graph/seed-catalog/v0
catalog:
  id: kitsoki-toolchain-catalog
nodes: []
`
	if err := os.WriteFile(kitsokiPath, []byte(kitsokiYAML), 0o644); err != nil {
		t.Fatalf("write kitsoki catalog: %v", err)
	}

	return root, repoRoot, homePath
}

// TestBuildCatalogAllowlist_DerivesAliasesFromTrackNodes is (a): the
// allowlist builder must derive one alias per resolvable pog/track/v0 node
// (default-location and catalog:-override cases), always include the home
// catalog under "pog", and silently skip a track whose target catalog does
// not exist.
func TestBuildCatalogAllowlist_DerivesAliasesFromTrackNodes(t *testing.T) {
	root, repoRoot, homePath := writeHomeFixture(t)

	allowlist := buildCatalogAllowlist(homePath, repoRoot)

	wantHome, err := filepath.Abs(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := allowlist.Resolve("pog"); !ok || got != wantHome {
		t.Errorf(`Resolve("pog") = (%q, %v), want (%q, true)`, got, ok, wantHome)
	}

	wantSassfully, _ := filepath.Abs(filepath.Join(root, "studio-sassfully", "pog", "catalog.yaml"))
	if got, ok := allowlist.Resolve("sassfully"); !ok || got != wantSassfully {
		t.Errorf(`Resolve("sassfully") = (%q, %v), want (%q, true)`, got, ok, wantSassfully)
	}

	wantKitsoki, _ := filepath.Abs(filepath.Join(root, "kitsoki-repo", "custom", "catalog.yaml"))
	if got, ok := allowlist.Resolve("kitsoki-toolchain"); !ok || got != wantKitsoki {
		t.Errorf(`Resolve("kitsoki-toolchain") = (%q, %v), want (%q, true)`, got, ok, wantKitsoki)
	}

	if _, ok := allowlist.Resolve("nowhere"); ok {
		t.Error(`Resolve("nowhere") should not resolve — track-broken's target catalog does not exist`)
	}
	if _, ok := allowlist.Resolve("broken"); ok {
		t.Error(`Resolve("broken") should not resolve — track-broken's target catalog does not exist`)
	}

	if got, want := len(allowlist.Aliases()), 3; got != want {
		t.Errorf("Aliases() = %v (len %d), want len %d (pog, sassfully, kitsoki-toolchain)", allowlist.Aliases(), got, want)
	}
}

// TestGraphProposeRPC_UnknownCatalogAliasRejected is (b): an RPC call
// naming an unbound `catalog` alias must be rejected with a clear error
// naming the known aliases, and must never fall through to touching a
// filesystem path — proven here by never creating any 'catalog_path'
// fixture at all: if the resolver fell through to objectgraph.Propose, the
// failure would be a "stat ...: no such file" style error instead of the
// allowlist's "not a known alias" message.
func TestGraphProposeRPC_UnknownCatalogAliasRejected(t *testing.T) {
	_, repoRoot, _ := writeHomeFixture(t)
	s := &Server{materializeRoot: repoRoot}

	_, rerr := s.graphProposeRPC(map[string]any{
		"catalog": "not-a-bound-alias",
		"title":   "should never get this far",
		"operations": []any{
			map[string]any{"kind": "modified", "node": "whatever", "changes": []any{}},
		},
	})
	if rerr == nil {
		t.Fatal("graphProposeRPC: expected an error for an unknown catalog alias, got nil")
	}
	const wantSubstr = "not a known alias"
	if !strings.Contains(rerr.Message, wantSubstr) {
		t.Errorf("graphProposeRPC error = %q, want it to contain %q", rerr.Message, wantSubstr)
	}
	for _, alias := range []string{"pog", "sassfully", "kitsoki-toolchain"} {
		if !strings.Contains(rerr.Message, alias) {
			t.Errorf("graphProposeRPC error = %q, want it to list known alias %q", rerr.Message, alias)
		}
	}
}

// TestGraphProposeRPC_KnownCatalogAliasResolvesAndNoParamStillWorks is (c):
// a `catalog` alias that IS bound resolves to the right path and behaves
// exactly like passing that path via `catalog_path` directly; and a call
// with neither `catalog` nor `catalog_path` still fails exactly the way it
// always has (backward compatibility — zero behavior change for existing
// callers that never pass `catalog`).
func TestGraphProposeRPC_KnownCatalogAliasResolvesAndNoParamStillWorks(t *testing.T) {
	_, repoRoot, homePath := writeHomeFixture(t)
	s := &Server{materializeRoot: repoRoot}

	// The bound "pog" alias's target is a real (if minimal) catalog, so this
	// operation reaches internal/graph.Propose itself, which rejects it for
	// a content reason ("changes" empty) — proving alias resolution
	// succeeded and the call proceeded past the allowlist gate (an unknown
	// alias would instead come back as an *rpcError with "not a known
	// alias", never reaching Propose at all).
	op := map[string]any{
		"operations": []any{
			map[string]any{"kind": "modified", "node": "feature-one", "changes": []any{}},
		},
	}
	res, rerr := s.graphProposeRPC(mergeParams(map[string]any{"catalog": "pog", "title": "alias resolution smoke test"}, op))
	if rerr != nil {
		t.Fatalf("graphProposeRPC(catalog=pog): unexpected *rpcError %+v", rerr)
	}
	result, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result shape: %#v", res)
	}
	if rejected, _ := result["rejected"].(bool); !rejected {
		t.Fatalf("result = %#v, want rejected=true (the fixture operation has empty 'changes')", result)
	}

	wantHome, _ := filepath.Abs(homePath)
	resDirect, rerrDirect := s.graphProposeRPC(mergeParams(map[string]any{"catalog_path": wantHome, "title": "same call via catalog_path directly"}, op))
	if rerrDirect != nil {
		t.Fatalf("graphProposeRPC(catalog_path=%q): unexpected *rpcError %+v", wantHome, rerrDirect)
	}
	resultDirect, ok := resDirect.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result shape: %#v", resDirect)
	}
	if resultDirect["reject_reasons"] == nil || result["reject_reasons"] == nil {
		t.Fatalf("expected both calls to carry reject_reasons; got %#v vs %#v", resultDirect, result)
	}

	// Backward compatibility: no `catalog` and no `catalog_path` at all
	// must still be rejected the same way it always was.
	_, rerrMissing := s.graphProposeRPC(map[string]any{
		"title":      "missing both",
		"operations": []any{},
	})
	if rerrMissing == nil {
		t.Fatal("expected an error when neither 'catalog' nor 'catalog_path' is given")
	}
	if !strings.Contains(rerrMissing.Message, "missing 'catalog_path'") {
		t.Errorf("graphProposeRPC error = %q, want it to still report the missing catalog_path", rerrMissing.Message)
	}
}

// mergeParams shallow-merges b into a copy of a (b wins on key overlap) —
// a small test-only convenience for reusing the same "operations" payload
// across a "catalog" call and a "catalog_path" call.
func mergeParams(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

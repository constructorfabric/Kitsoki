package server

// materialize_catalog_test.go — F3 (federated-materialize follow-up to F1's
// catalog-allowlist): proves graph.materialize.start/.checks resolve
// `catalog` as an alias through the SAME s.graphAllowlist() mechanism
// graph.propose/authorize/withdraw/apply/rebase use (catalog_allowlist_test.go),
// and that the derived RepoRoot lands on the resolved catalog's own repo
// root — s.materializeRoot for the home "pog" alias (zero behavior change),
// the federated member's own root for a track alias.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// materializeAllowlistFixture builds a minimal home catalog (alias "pog", at
// <root>/pog/catalog.yaml) with one pog/track/v0 node federating a member
// catalog (alias "member", at <root>/member-repo/pog/catalog.yaml) —
// mirroring catalog_allowlist_test.go's writeHomeFixture shape. Each catalog
// carries one node found ONLY in that catalog (home-only-node /
// member-only-node), so a test can tell which catalog an RPC actually loaded
// from which node id it can and cannot see.
func materializeAllowlistFixture(t *testing.T) (root string) {
	t.Helper()
	// outer is the portfolio root (mirrors real ~/code/): repoRoot (root,
	// returned below) is a checkout nested one level under it, and
	// member-repo sits BESIDE repoRoot — not inside it — matching the real
	// POG-vs-studio-sassfully sibling-checkout topology `repo:
	// ../member-repo` is authored against.
	outer := t.TempDir()
	root = filepath.Join(outer, "home-repo")

	homeDir := filepath.Join(root, "pog")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	homeYAML := `schema: project-object-graph/seed-catalog/v0
catalog:
  id: home-catalog
type_registry:
  - id: track
    schema: graph-type/v0
  - id: core-node
    schema: graph-type/v0
    required_fields: [id, schema, title, status, visibility]
nodes:
  - schema: pog/track/v0
    id: track-member
    title: member
    status: active
    visibility: internal
    repo: ../member-repo
  - schema: graph/core-node/v0
    id: home-only-node
    title: Home-only node
    status: active
    visibility: internal
`
	if err := os.WriteFile(filepath.Join(homeDir, "catalog.yaml"), []byte(homeYAML), 0o644); err != nil {
		t.Fatalf("write home catalog: %v", err)
	}

	memberDir := filepath.Join(outer, "member-repo", "pog")
	if err := os.MkdirAll(memberDir, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	memberYAML := `schema: project-object-graph/seed-catalog/v0
catalog:
  id: member-catalog
type_registry:
  - id: core-node
    schema: graph-type/v0
    required_fields: [id, schema, title, status, visibility]
nodes:
  - schema: graph/core-node/v0
    id: member-only-node
    title: Member-only node
    status: active
    visibility: internal
`
	if err := os.WriteFile(filepath.Join(memberDir, "catalog.yaml"), []byte(memberYAML), 0o644); err != nil {
		t.Fatalf("write member catalog: %v", err)
	}

	return root
}

// TestResolveMaterializeCatalog_HomeAliasMatchesOldBehavior is the "zero
// behavior change for the home-catalog case" guarantee: resolving `catalog:
// "pog"` must yield the home catalog's absolute path and a RepoRoot equal to
// s.materializeRoot — exactly what the pre-F3 hardcoded
// `RepoRoot: s.materializeRoot` produced.
func TestResolveMaterializeCatalog_HomeAliasMatchesOldBehavior(t *testing.T) {
	root := materializeAllowlistFixture(t)
	s := &Server{materializeRoot: root}

	catalogPath, repoRoot, rerr := s.resolveMaterializeCatalog(map[string]any{"catalog": "pog"}, "graph.materialize.start")
	if rerr != nil {
		t.Fatalf("resolveMaterializeCatalog(pog): unexpected error %+v", rerr)
	}

	wantCatalog, err := filepath.Abs(filepath.Join(root, "pog", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if catalogPath != wantCatalog {
		t.Errorf("catalogPath = %q, want %q", catalogPath, wantCatalog)
	}

	wantRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if repoRoot != wantRoot {
		t.Errorf("repoRoot = %q, want %q (== s.materializeRoot)", repoRoot, wantRoot)
	}
}

// TestResolveMaterializeCatalog_FederatedAliasUsesMemberOwnRoot is the
// federated-materialize case F3 exists for: resolving a track alias
// ("member") must load THAT catalog (not the home catalog) and derive
// RepoRoot as the member's own repo root — two filepath.Dir() calls up from
// its pog/catalog.yaml, per buildCatalogAllowlist's absTrack convention —
// not s.materializeRoot.
func TestResolveMaterializeCatalog_FederatedAliasUsesMemberOwnRoot(t *testing.T) {
	root := materializeAllowlistFixture(t)
	s := &Server{materializeRoot: root}

	catalogPath, repoRoot, rerr := s.resolveMaterializeCatalog(map[string]any{"catalog": "member"}, "graph.materialize.checks")
	if rerr != nil {
		t.Fatalf("resolveMaterializeCatalog(member): unexpected error %+v", rerr)
	}

	wantCatalog, err := filepath.Abs(filepath.Join(filepath.Dir(root), "member-repo", "pog", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if catalogPath != wantCatalog {
		t.Errorf("catalogPath = %q, want %q", catalogPath, wantCatalog)
	}

	wantRoot, err := filepath.Abs(filepath.Join(filepath.Dir(root), "member-repo"))
	if err != nil {
		t.Fatal(err)
	}
	if repoRoot != wantRoot {
		t.Errorf("repoRoot = %q, want %q (member's own root, not s.materializeRoot=%q)", repoRoot, wantRoot, s.materializeRoot)
	}
	if repoRoot == s.materializeRoot {
		t.Errorf("repoRoot must NOT equal the home s.materializeRoot for a federated alias")
	}
}

// TestResolveMaterializeCatalog_UnknownAliasRejected proves an unresolvable
// `catalog` value is rejected with the same "not a known alias" style error
// the other graph.* verbs produce, and — critically — NEVER falls through to
// treating the string as a raw filesystem path: passing an alias that
// happens to also be a real path on disk (this test's own root dir) must
// still be rejected as an unknown alias, not silently loaded.
func TestResolveMaterializeCatalog_UnknownAliasRejected(t *testing.T) {
	root := materializeAllowlistFixture(t)
	s := &Server{materializeRoot: root}

	// A real, existing path on disk — proving a hit here would only be
	// possible via a raw-path fallback, which must not exist.
	realPath := filepath.Join(root, "pog", "catalog.yaml")

	for _, alias := range []string{"not-a-bound-alias", realPath} {
		_, _, rerr := s.resolveMaterializeCatalog(map[string]any{"catalog": alias}, "graph.materialize.start")
		if rerr == nil {
			t.Fatalf("resolveMaterializeCatalog(%q): expected an error, got nil", alias)
		}
		if !strings.Contains(rerr.Message, "not a known alias") {
			t.Errorf("resolveMaterializeCatalog(%q) error = %q, want it to contain %q", alias, rerr.Message, "not a known alias")
		}
		if !strings.Contains(rerr.Message, "pog") || !strings.Contains(rerr.Message, "member") {
			t.Errorf("resolveMaterializeCatalog(%q) error = %q, want it to list known aliases (pog, member)", alias, rerr.Message)
		}
	}

	// Missing 'catalog' entirely is still a distinct, clear error.
	_, _, rerr := s.resolveMaterializeCatalog(map[string]any{}, "graph.materialize.start")
	if rerr == nil {
		t.Fatal("resolveMaterializeCatalog({}): expected an error for missing 'catalog', got nil")
	}
	if !strings.Contains(rerr.Message, "missing 'catalog'") {
		t.Errorf("resolveMaterializeCatalog({}) error = %q, want it to report the missing 'catalog' param", rerr.Message)
	}
}

// TestMaterializeChecks_FederatedAliasLoadsMemberCatalog is the end-to-end
// proof through the actual RPC handler (not just the resolver helper): a
// `catalog: "member"` call to graph.materialize.checks must see
// member-only-node (proving it loaded the member's own catalog file) and
// must NOT see home-only-node under that alias — and vice versa for
// `catalog: "pog"`. Both calls fail past node lookup (neither node declares
// a materialize: binding in this minimal fixture), but the failure message
// still tells the two cases apart: "not found in catalog" (wrong/home
// catalog loaded) vs "does not declare a materialize: binding" (right
// catalog loaded, node found).
func TestMaterializeChecks_FederatedAliasLoadsMemberCatalog(t *testing.T) {
	root := materializeAllowlistFixture(t)
	s := &Server{materializeRoot: root}
	ctx := context.Background()

	// "member" alias + member's own node: found, fails later at the
	// materialize-binding check — proving the member catalog was loaded.
	_, rerr := s.materializeChecks(ctx, map[string]any{"catalog": "member", "node_id": "member-only-node"})
	if rerr == nil {
		t.Fatal("materializeChecks(member, member-only-node): expected an error (no materialize: binding), got nil")
	}
	if !strings.Contains(rerr.Message, "does not declare a materialize") {
		t.Errorf("materializeChecks(member, member-only-node) error = %q, want a materialize-binding error (proves the member catalog loaded and found the node)", rerr.Message)
	}

	// "member" alias + a node that only exists in the HOME catalog: must not
	// be found — proving the member catalog, not the home catalog, is what
	// actually got loaded for this alias.
	_, rerr = s.materializeChecks(ctx, map[string]any{"catalog": "member", "node_id": "home-only-node"})
	if rerr == nil {
		t.Fatal("materializeChecks(member, home-only-node): expected a 'not found' error, got nil")
	}
	if !strings.Contains(rerr.Message, "not found in catalog") {
		t.Errorf("materializeChecks(member, home-only-node) error = %q, want a 'not found in catalog' error", rerr.Message)
	}

	// "pog" alias + the home-only node: found, fails at the same
	// materialize-binding check — proving the home catalog resolves and
	// behaves exactly as it did before F3.
	_, rerr = s.materializeChecks(ctx, map[string]any{"catalog": "pog", "node_id": "home-only-node"})
	if rerr == nil {
		t.Fatal("materializeChecks(pog, home-only-node): expected an error (no materialize: binding), got nil")
	}
	if !strings.Contains(rerr.Message, "does not declare a materialize") {
		t.Errorf("materializeChecks(pog, home-only-node) error = %q, want a materialize-binding error", rerr.Message)
	}
}

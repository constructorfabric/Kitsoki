package graphsrv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/mcp/graphsrv"
)

// writeScopeFile writes a scope-spec YAML into a temp dir and returns its
// path.
func writeScopeFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scope.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write scope file: %v", err)
	}
	return path
}

// reqAlphaScope is the canonical test scope: everything reachable out of
// req-alpha (req-alpha, uc-alpha, req-iso-gamma) plus the always-in-scope
// changeset nodes (cs-1, cs-2) = 5 of the fixture's 8 nodes. Out of scope:
// req-beta, change-root, change-leaf.
const reqAlphaScope = "roots: [req-alpha]\n"

func TestGraphServer_ScopedOpenReportsScope(t *testing.T) {
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"main=" + scopePath},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.open", map[string]any{})
	if isErr {
		t.Fatalf("graph.open returned an error: %+v", m)
	}
	if nc, _ := m["node_count"].(float64); int(nc) != 5 {
		t.Fatalf("scoped node_count = %v, want 5", m["node_count"])
	}
	scope, _ := m["scope"].(map[string]any)
	if scope == nil {
		t.Fatalf("expected a scope block on a scoped session's graph.open, got %+v", m)
	}
	if mc, _ := scope["member_count"].(float64); int(mc) != 5 {
		t.Fatalf("scope.member_count = %v, want 5", scope["member_count"])
	}
	if tc, _ := scope["total_node_count"].(float64); int(tc) != 8 {
		t.Fatalf("scope.total_node_count = %v, want 8", scope["total_node_count"])
	}
	if guide, _ := m["guide"].(string); !strings.Contains(guide, "SCOPED") {
		t.Fatalf("expected the guide to state the session is scoped, got %q", m["guide"])
	}
}

func TestGraphServer_ScopedFindSeesOnlyMembers(t *testing.T) {
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"main=" + scopePath},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.find", map[string]any{"count_only": true})
	if isErr {
		t.Fatalf("graph.find returned an error: %+v", m)
	}
	if total, _ := m["total"].(float64); int(total) != 5 {
		t.Fatalf("scoped find total = %v, want 5", m["total"])
	}

	// req-beta exists in the catalog but must be invisible to a scoped
	// find. Match on its statement text ("beta must hold" only appears on
	// req-beta itself; the bare id "req-beta" would also match the in-scope
	// changeset cs-1 that mentions it).
	m, isErr = callTool(t, cs, "graph.find", map[string]any{"text": "beta must hold"})
	if isErr {
		t.Fatalf("graph.find(text): %+v", m)
	}
	if total, _ := m["total"].(float64); int(total) != 0 {
		t.Fatalf("scoped find should not see req-beta's content, total = %v", m["total"])
	}
}

func TestGraphServer_ScopedGetMarksOutOfScope(t *testing.T) {
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"main=" + scopePath},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.get", map[string]any{"ids": []string{"req-alpha", "req-beta"}})
	if isErr {
		t.Fatalf("graph.get returned an error: %+v", m)
	}
	nodes, _ := m["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected exactly the in-scope node, got %d nodes: %+v", len(nodes), nodes)
	}
	missing, _ := m["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("expected req-beta in missing, got %+v", missing)
	}
	entry, _ := missing[0].(map[string]any)
	if oos, _ := entry["out_of_scope"].(bool); !oos {
		t.Fatalf("expected missing req-beta to be marked out_of_scope, got %+v", entry)
	}
}

func TestGraphServer_ScopedNeighborsOutOfScopeRoot(t *testing.T) {
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"main=" + scopePath},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.neighbors", map[string]any{"id": "req-beta"})
	if !isErr {
		t.Fatalf("expected an error walking from an out-of-scope root, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeOutOfScope {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeOutOfScope)
	}
}

func TestGraphServer_ScopedProposeRejectsOutOfScopeWrite(t *testing.T) {
	path := mutableFixture(t)
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + path},
		ScopeFlags:   []string{"main=" + scopePath},
		Mode:         graphsrv.ModePropose,
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose",
		proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if !isErr {
		t.Fatalf("expected an out-of-scope propose to be rejected, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeOutOfScope {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeOutOfScope)
	}

	// An in-scope write on the same server goes through.
	m, isErr = callTool(t, cs, "graph.propose",
		proposeArgs("flip req-alpha", []map[string]any{flipStatusOp("req-alpha", "active", "done")}))
	if isErr {
		t.Fatalf("in-scope propose should succeed, got %+v", m)
	}
	if id, _ := m["changeset_id"].(string); id == "" {
		t.Fatalf("expected a changeset_id, got %+v", m)
	}
}

func TestGraphServer_ScopedApplyRejectsOutOfScopeChangeset(t *testing.T) {
	path := mutableFixture(t)
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{"main=" + path},
		ScopeFlags:   []string{"main=" + scopePath},
		Mode:         graphsrv.ModePropose,
	})
	defer done()

	// cs-1 flips req-beta — out of scope, so even a dry-run apply is
	// rejected before the engine sees it.
	m, isErr := callTool(t, cs, "graph.apply", map[string]any{"id": "cs-1", "dry_run": true})
	if !isErr {
		t.Fatalf("expected an out-of-scope apply to be rejected, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeOutOfScope {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeOutOfScope)
	}
}

func TestGraphServer_ScopeStartupValidation(t *testing.T) {
	// A malformed scope file must fail server construction, never degrade
	// to an unscoped session.
	badScope := writeScopeFile(t, "rootz: [req-alpha]\n")
	if _, err := graphsrv.NewServer(graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"main=" + badScope},
	}); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected an unknown-key startup error, got %v", err)
	}

	// A scope bound to an unbound alias fails too.
	goodScope := writeScopeFile(t, reqAlphaScope)
	if _, err := graphsrv.NewServer(graphsrv.Config{
		CatalogFlags: []string{"main=" + fixturePath},
		ScopeFlags:   []string{"other=" + goodScope},
	}); err == nil || !strings.Contains(err.Error(), "not a bound catalog alias") {
		t.Fatalf("expected an unbound-alias startup error, got %v", err)
	}

	// A scope with no catalog at all fails.
	if _, err := graphsrv.ParseScopeFlags([]string{goodScope}, &graphsrv.CatalogSet{}); err == nil || !strings.Contains(err.Error(), "no catalog is bound") {
		t.Fatalf("expected a no-catalog scope error, got %v", err)
	}
}

func TestGraphServer_ScopeFlagDefaultsToDefaultCatalog(t *testing.T) {
	// --scope with no alias binds to the default catalog.
	scopePath := writeScopeFile(t, reqAlphaScope)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{fixturePath},
		ScopeFlags:   []string{scopePath},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.find", map[string]any{"count_only": true})
	if isErr {
		t.Fatalf("graph.find: %+v", m)
	}
	if total, _ := m["total"].(float64); int(total) != 5 {
		t.Fatalf("default-alias scoped find total = %v, want 5", m["total"])
	}
}

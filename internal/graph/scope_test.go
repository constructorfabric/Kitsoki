package graph

import (
	"strings"
	"testing"
)

func intPtr(n int) *int { return &n }

func resolve(t *testing.T, cat *Catalog, spec *ScopeSpec) map[NodeID]bool {
	t.Helper()
	members, err := ResolveScope(cat, spec)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	return members
}

func assertMembers(t *testing.T, members map[NodeID]bool, want ...NodeID) {
	t.Helper()
	if len(members) != len(want) {
		t.Fatalf("member count = %d, want %d (members: %v)", len(members), len(want), members)
	}
	for _, id := range want {
		if !members[id] {
			t.Fatalf("expected %q in scope, members: %v", id, members)
		}
	}
}

func TestResolveScope_RootsOutUnlimited(t *testing.T) {
	cat := loadReadsFixture(t)
	members := resolve(t, cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}})

	// req-alpha -acceptance-> uc-alpha -covers-> req-alpha, req-iso-gamma;
	// changeset nodes are always in scope.
	assertMembers(t, members, "req-alpha", "uc-alpha", "req-iso-gamma", "cs-1", "cs-2")
}

func TestResolveScope_DepthBounds(t *testing.T) {
	cat := loadReadsFixture(t)

	members := resolve(t, cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}, Depth: intPtr(0)})
	assertMembers(t, members, "req-alpha", "cs-1", "cs-2")

	members = resolve(t, cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}, Depth: intPtr(1)})
	assertMembers(t, members, "req-alpha", "uc-alpha", "cs-1", "cs-2")
}

func TestResolveScope_DirectionInFollowsTopLevelStorage(t *testing.T) {
	cat := loadReadsFixture(t)
	// change-leaf points at change-root via a storage:top_level edge; an
	// "in"-direction scope rooted at change-root must see it.
	members := resolve(t, cat, &ScopeSpec{Roots: []NodeID{"change-root"}, Direction: DirectionIn})
	assertMembers(t, members, "change-root", "change-leaf", "cs-1", "cs-2")
}

func TestResolveScope_EdgeAllowlistBlocksTraversal(t *testing.T) {
	cat := loadReadsFixture(t)
	// Traversal restricted to "acceptance": req-alpha -> uc-alpha, but the
	// covers hop back out to req-iso-gamma is blocked.
	members := resolve(t, cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}, Edges: []EdgeField{"acceptance"}})
	assertMembers(t, members, "req-alpha", "uc-alpha", "cs-1", "cs-2")
}

func TestResolveScope_TypesAndInclude(t *testing.T) {
	cat := loadReadsFixture(t)
	members := resolve(t, cat, &ScopeSpec{Types: []string{"change"}, Include: []NodeID{"req-beta"}})
	assertMembers(t, members, "change-root", "change-leaf", "req-beta", "cs-1", "cs-2")
}

func TestResolveScope_ExcludeBlocksMembershipAndTraversal(t *testing.T) {
	cat := loadReadsFixture(t)
	// Excluding uc-alpha removes it AND stops the walk from reaching
	// req-iso-gamma through it. Excluding a changeset overrides the
	// auto-include. Unknown exclude ids are ignored.
	members := resolve(t, cat, &ScopeSpec{
		Roots:   []NodeID{"req-alpha"},
		Exclude: []NodeID{"uc-alpha", "cs-2", "no-such-node"},
	})
	assertMembers(t, members, "req-alpha", "cs-1")
}

func TestResolveScope_UnknownRootAndTypeAndEdgeError(t *testing.T) {
	cat := loadReadsFixture(t)
	if _, err := ResolveScope(cat, &ScopeSpec{Roots: []NodeID{"req-alfa"}}); err == nil || !strings.Contains(err.Error(), "unknown root") {
		t.Fatalf("unknown root: got %v", err)
	}
	if _, err := ResolveScope(cat, &ScopeSpec{Types: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("unknown type: got %v", err)
	}
	if _, err := ResolveScope(cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}, Edges: []EdgeField{"nope"}}); err == nil || !strings.Contains(err.Error(), "unknown edge") {
		t.Fatalf("unknown edge: got %v", err)
	}
}

func TestApplyScope_PrunesNodesAndEdges(t *testing.T) {
	cat := loadReadsFixture(t)
	// Roots-only scope on uc-alpha: its covers edges (req-alpha,
	// req-iso-gamma) both point out of scope and must be pruned from the
	// view — without mutating the original catalog.
	scoped, err := ApplyScope(cat, &ScopeSpec{Roots: []NodeID{"uc-alpha"}, Depth: intPtr(0)})
	if err != nil {
		t.Fatalf("ApplyScope: %v", err)
	}
	if _, ok := scoped.Nodes["req-alpha"]; ok {
		t.Fatalf("req-alpha should be pruned from the scoped view")
	}
	if got := scoped.Nodes["uc-alpha"].Edges["covers"]; len(got) != 0 {
		t.Fatalf("scoped uc-alpha covers = %v, want pruned empty", got)
	}
	if got := cat.Nodes["uc-alpha"].Edges["covers"]; len(got) != 2 {
		t.Fatalf("original catalog mutated: uc-alpha covers = %v", got)
	}
	info := scoped.Scope
	if info == nil || info.TotalNodes != len(cat.Nodes) || info.MemberCount != len(scoped.Nodes) || info.PrunedEdges != 2 {
		t.Fatalf("ScopeInfo = %+v", info)
	}
	if !info.Excluded["req-beta"] || info.Excluded["uc-alpha"] {
		t.Fatalf("Excluded set wrong: %v", info.Excluded)
	}
}

func TestApplyScope_PrunesTopLevelStorageEdge(t *testing.T) {
	cat := loadReadsFixture(t)
	scoped, err := ApplyScope(cat, &ScopeSpec{Include: []NodeID{"change-leaf"}})
	if err != nil {
		t.Fatalf("ApplyScope: %v", err)
	}
	node := scoped.Nodes["change-leaf"]
	decl := EdgeFieldDecl{ID: "depends_on", Storage: StorageTopLevel}
	if got := node.EdgeTargets(decl); len(got) != 0 {
		t.Fatalf("scoped change-leaf depends_on = %v, want pruned empty", got)
	}
	if got := cat.Nodes["change-leaf"].EdgeTargets(decl); len(got) != 1 {
		t.Fatalf("original catalog mutated: change-leaf depends_on = %v", got)
	}
}

func TestParseScopeSpec_Validation(t *testing.T) {
	if _, err := ParseScopeSpec(map[string]any{"rootz": []any{"a"}}); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key: got %v", err)
	}
	if _, err := ParseScopeSpec(map[string]any{}); err == nil || !strings.Contains(err.Error(), "at least one of") {
		t.Fatalf("empty spec: got %v", err)
	}
	if _, err := ParseScopeSpec(map[string]any{"roots": []any{"a"}, "direction": "sideways"}); err == nil || !strings.Contains(err.Error(), "direction") {
		t.Fatalf("bad direction: got %v", err)
	}
	if _, err := ParseScopeSpec(map[string]any{"roots": []any{"a"}, "depth": -1}); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("negative depth: got %v", err)
	}
}

func TestParseScopeSpec_WireMapRoundTrip(t *testing.T) {
	spec, err := ParseScopeSpec(map[string]any{
		"roots":     []any{"req-alpha"},
		"direction": "both",
		"depth":     2,
		"edges":     []any{"acceptance"},
		"types":     []any{"change"},
		"include":   []any{"req-beta"},
		"exclude":   []any{"uc-alpha"},
	})
	if err != nil {
		t.Fatalf("ParseScopeSpec: %v", err)
	}
	back, err := ParseScopeSpec(spec.WireMap())
	if err != nil {
		t.Fatalf("round-trip ParseScopeSpec: %v", err)
	}
	if back.Direction != DirectionBoth || back.Depth == nil || *back.Depth != 2 ||
		len(back.Roots) != 1 || len(back.Edges) != 1 || len(back.Types) != 1 ||
		len(back.Include) != 1 || len(back.Exclude) != 1 {
		t.Fatalf("round-trip lost fields: %+v", back)
	}
}

func TestScopeWriteViolations(t *testing.T) {
	cat := loadReadsFixture(t)
	members := resolve(t, cat, &ScopeSpec{Roots: []NodeID{"req-alpha"}})

	ops := []Operation{
		{Kind: OpModified, Node: "req-alpha"},              // in scope: ok
		{Kind: OpModified, Node: "req-beta"},               // out of scope
		{Kind: OpAdded, Node: "req-new"},                   // additions always ok
		{Kind: OpModified, Node: "no-such-node"},           // unknown: engine's problem
		{Kind: OpRenamed, From: "change-root", To: "cr2"},  // out of scope
		{Kind: OpRegistryTypeAdded, Node: "some-new-type"}, // registry: always blocked
	}
	v := ScopeWriteViolations(cat, members, ops)
	if len(v) != 3 {
		t.Fatalf("violations = %v, want 3", v)
	}
	joined := strings.Join(v, "\n")
	for _, want := range []string{`"req-beta" is out of scope`, `"change-root" is out of scope`, "catalog-wide"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("violations missing %q:\n%s", want, joined)
		}
	}
}

package proposalsadapter

import (
	"os"
	"strings"
	"testing"

	"kitsoki/internal/graph"
)

const seedCatalogPath = "../../../docs/proposals/project-object-graph/seed-objects.yaml"

func TestGraphSourcedProposals_IncludesTheEpic(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	proposals := GraphSourcedProposals(cat)
	found := false
	for _, p := range proposals {
		if p.ID == "proposal-project-object-graph" {
			found = true
		}
	}
	if !found {
		t.Error("expected proposal-project-object-graph among graph-sourced proposals")
	}
}

// TestRenderIndexEntry_MatchesCommittedREADME is W6.0's drift-lint gate,
// exercised directly: the generated entry must byte-match the committed
// docs/proposals/README.md line for this proposal — the two artifacts are
// meant to be the same bytes, unlike W3.0's featuresadapter (which checks
// semantic equivalence against a re-parsed structured file). See the
// package doc comment for why the two adapters check differently.
func TestRenderIndexEntry_MatchesCommittedREADME(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	node, ok := cat.Nodes["proposal-project-object-graph"]
	if !ok {
		t.Fatal("proposal-project-object-graph not found in the seed catalog")
	}

	got, err := RenderIndexEntry(node)
	if err != nil {
		t.Fatalf("RenderIndexEntry: %v", err)
	}

	raw, err := os.ReadFile("../../../docs/proposals/README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if line == got {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no line in docs/proposals/README.md matches the generated entry.\ngenerated: %s", got)
	}
}

// TestGraphSourcedProposals_ExcludesChildren is the domain-model fix this
// session's stress test surfaced: converting the top-level README index
// (W6.1) left ~20 nested child bullets (e.g. under trace-introspection.md,
// view-rendering-readability.md) unconvertible, because the proposal type
// had no way to represent "this is a sub-slice of another proposal, render
// it indented under the parent." child_of + RenderChildEntry close that
// gap; GraphSourcedProposals must keep excluding children from the
// top-level index so a child never double-renders as its own bullet.
func TestGraphSourcedProposals_ExcludesChildren(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	for _, p := range GraphSourcedProposals(cat) {
		if p.ID == "proposal-effect-taxonomy" {
			t.Error("proposal-effect-taxonomy is a child_of proposal-agent-capability-model and must not appear in the top-level index")
		}
	}
}

// TestGraphSourcedChildProposals_IncludesTraceIntrospectionSlices verifies
// the child-proposal query surfaces nested slices keyed by child_of.
func TestGraphSourcedChildProposals_IncludesTraceIntrospectionSlices(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	children := GraphSourcedChildProposals(cat)
	found := false
	for _, c := range children {
		if c.ID == "proposal-trace-decision-detail" {
			found = true
		}
	}
	if !found {
		t.Error("expected proposal-trace-decision-detail among the graph-sourced child proposals")
	}
}

// TestRenderChildEntry_MatchesCommittedREADME is the child analogue of
// TestRenderIndexEntry_MatchesCommittedREADME: the generated nested bullet
// must byte-match a line in the committed README, including the optional
// child_note parenthetical (e.g. "partially implemented").
func TestRenderChildEntry_MatchesCommittedREADME(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	node, ok := cat.Nodes["proposal-kitsoki-trace-pattern-matching"]
	if !ok {
		t.Fatal("proposal-kitsoki-trace-pattern-matching not found in the seed catalog")
	}

	got, err := RenderChildEntry(node)
	if err != nil {
		t.Fatalf("RenderChildEntry: %v", err)
	}

	raw, err := os.ReadFile("../../../docs/proposals/README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if line == got {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no line in docs/proposals/README.md matches the generated child entry.\ngenerated: %s", got)
	}
}

func TestRenderIndexEntry_ErrorsOnMissingBlurb(t *testing.T) {
	cat, err := graph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	// change-object-graph-substrate is a real node but not a proposal, and
	// has no index_blurb — exercises the missing-field error path.
	node := cat.Nodes["change-object-graph-substrate"]
	if node == nil {
		t.Fatal("fixture assumption broken: change-object-graph-substrate missing")
	}
	if _, err := RenderIndexEntry(node); err == nil {
		t.Fatal("expected an error rendering a node with no index_blurb")
	}
}

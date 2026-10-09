package graph

import (
	"strings"
	"testing"
)

func TestLoadCatalog_GoodSingleFile(t *testing.T) {
	cat, err := LoadCatalog("testdata/good/minimal.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(cat.Nodes))
	}
	clause, ok := cat.Nodes["clause-one"]
	if !ok {
		t.Fatal("clause-one not loaded")
	}
	if clause.TypeID != "iso9001-clause" {
		t.Errorf("clause-one TypeID = %q, want iso9001-clause", clause.TypeID)
	}
	if got := clause.Fields["clause_ref"]; got != "8.2.1" {
		t.Errorf("clause-one Fields[clause_ref] = %v, want 8.2.1", got)
	}
	// A derived type's edges must validate against the ancestor's edge decl
	// (iso9001-clause has no edge_fields of its own; it inherits
	// required_by from requirement).
	if got := clause.Edges["required_by"]; len(got) != 1 || got[0] != "feature-one" {
		t.Errorf("clause-one required_by = %v, want [feature-one]", got)
	}
}

func TestLoadCatalog_GoodBundle(t *testing.T) {
	cat, err := LoadCatalog("testdata/good/bundle-min")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(cat.Nodes))
	}
	w10 := cat.Nodes["change-w1-0"]
	if w10 == nil {
		t.Fatal("change-w1-0 not loaded")
	}
	// depends_on is declared top_level storage: it must come through Fields
	// (the shipped change-node contract shape), not through Edges.
	dependsOn, _ := w10.Fields["depends_on"].([]any)
	if len(dependsOn) != 1 || dependsOn[0] != "change-wm-0" {
		t.Errorf("change-w1-0 Fields[depends_on] = %v, want [change-wm-0]", w10.Fields["depends_on"])
	}
	if len(w10.Edges["depends_on"]) != 0 {
		t.Errorf("depends_on must not also populate Edges (top_level storage): got %v", w10.Edges["depends_on"])
	}
}

func TestLoadCatalog_SeedCorpusRegression(t *testing.T) {
	cat, err := LoadCatalog("../../docs/proposals/project-object-graph/seed-objects.yaml")
	if err != nil {
		t.Fatalf("seed-objects.yaml must load clean: %v", err)
	}
	if len(cat.Nodes) == 0 {
		t.Fatal("seed-objects.yaml loaded zero nodes")
	}
	if _, ok := cat.Registry.Effective("feature"); !ok {
		t.Error("seed-objects.yaml type registry did not resolve the feature type")
	}
	// The seed fixture predates Shared decision 2 and uses derives_from +
	// v0 pins; the loader must accept it as a deprecated alias and warn,
	// not silently ignore the mismatch (WM.1 design-session follow-up).
	if len(cat.Warnings) == 0 {
		t.Error("expected a derives_from deprecation warning loading the pre-decision seed fixture")
	}
	foundDerivesFromWarning := false
	for _, w := range cat.Warnings {
		if strings.Contains(w, "derives_from") {
			foundDerivesFromWarning = true
		}
	}
	if !foundDerivesFromWarning {
		t.Errorf("warnings = %v, want at least one mentioning derives_from", cat.Warnings)
	}
}

// TestLoadCatalog_NestsUnderEdgeMarker is the W6.2 follow-up's regression
// check: proposal.child_of and persona.persona_of must both parse with
// NestsUnder set, so a generic UI list projection can derive "which edges
// mean nest me under my target" from the type registry instead of a
// hand-maintained kind->edge table that silently drifts (the bug the
// marker replaces).
func TestLoadCatalog_NestsUnderEdgeMarker(t *testing.T) {
	cat, err := LoadCatalog("../../docs/proposals/project-object-graph/seed-objects.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	for _, tc := range []struct {
		typeID, edgeID string
	}{
		{"proposal", "child_of"},
		{"persona", "persona_of"},
	} {
		eff, ok := cat.Registry.Effective(tc.typeID)
		if !ok {
			t.Fatalf("type %q not found", tc.typeID)
		}
		found := false
		for _, decl := range eff.EdgeFields {
			if decl.ID != EdgeField(tc.edgeID) {
				continue
			}
			found = true
			if !decl.NestsUnder {
				t.Errorf("%s.%s: NestsUnder = false, want true", tc.typeID, tc.edgeID)
			}
		}
		if !found {
			t.Errorf("%s: edge field %q not found", tc.typeID, tc.edgeID)
		}
	}
}

func TestLoadCatalog_BadFixtures(t *testing.T) {
	cases := []struct {
		name       string
		file       string
		wantErrSub string
	}{
		{"unknown type", "testdata/bad/unknown-type.yaml", "unknown type"},
		{"cardinality one exceeded", "testdata/bad/bad-cardinality.yaml", "cardinality \"one\""},
		{"missing required envelope field", "testdata/bad/missing-title.yaml", "missing title"},
		{"invalid visibility", "testdata/bad/invalid-visibility.yaml", "invalid visibility"},
		{"duplicate node id", "testdata/bad/duplicate-node.yaml", "duplicate node id"},
		{"cyclic extends", "testdata/bad/cyclic-extends.yaml", "cyclic extends chain"},
		{"non-kebab node id", "testdata/bad/non-kebab-id.yaml", "not a kebab-case id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadCatalog(tc.file)
			if err == nil {
				t.Fatalf("%s: expected LoadCatalog to fail, it didn't", tc.file)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("%s: error = %v, want substring %q", tc.file, err, tc.wantErrSub)
			}
		})
	}
}

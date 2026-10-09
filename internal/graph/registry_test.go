package graph

import (
	"strings"
	"testing"
)

func TestRegistry_ResolveDerivation(t *testing.T) {
	r := NewRegistry()
	must(t, r.Register(TypeDef{
		ID:             "core-node",
		Schema:         "graph-type/v0",
		RequiredFields: []string{"id", "schema", "title", "status", "visibility"},
	}))
	must(t, r.Register(TypeDef{
		ID:      "requirement",
		Schema:  "graph-type/v0",
		Extends: "core-node",
		EdgeFields: []EdgeFieldDecl{
			{ID: "required_by", TargetType: "feature", Cardinality: CardinalityMany},
		},
	}))
	must(t, r.Register(TypeDef{
		ID:             "iso9001-clause",
		Schema:         "graph-type/v0",
		Extends:        "requirement",
		RequiredFields: []string{"clause_ref"},
	}))

	if err := r.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	clause, ok := r.Effective("iso9001-clause")
	if !ok {
		t.Fatalf("iso9001-clause not resolved")
	}
	wantRequired := []string{"id", "schema", "title", "status", "visibility", "clause_ref"}
	if !equalStrings(clause.RequiredFields, wantRequired) {
		t.Errorf("required fields = %v, want %v", clause.RequiredFields, wantRequired)
	}
	if len(clause.EdgeFields) != 1 || clause.EdgeFields[0].ID != "required_by" {
		t.Errorf("expected iso9001-clause to inherit the required_by edge field, got %+v", clause.EdgeFields)
	}
	wantAncestry := []string{"core-node", "requirement", "iso9001-clause"}
	if !equalStrings(clause.Ancestry, wantAncestry) {
		t.Errorf("ancestry = %v, want %v", clause.Ancestry, wantAncestry)
	}

	// The generic lint this derivation exists for: an edge targeting
	// "requirement" must accept a derived type like "iso9001-clause".
	if !r.IsA("iso9001-clause", "requirement") {
		t.Errorf("expected iso9001-clause IsA requirement")
	}
	if r.IsA("iso9001-clause", "feature") {
		t.Errorf("iso9001-clause must not be assignable to unrelated type feature")
	}
}

func TestRegistry_CyclicExtends(t *testing.T) {
	r := NewRegistry()
	must(t, r.Register(TypeDef{ID: "a", Extends: "b"}))
	must(t, r.Register(TypeDef{ID: "b", Extends: "a"}))

	err := r.Resolve()
	if err == nil {
		t.Fatal("expected cyclic extends to fail Resolve")
	}
	if !strings.Contains(err.Error(), "cyclic extends chain") {
		t.Errorf("error = %v, want mention of cyclic extends chain", err)
	}
}

func TestRegistry_UnknownParent(t *testing.T) {
	r := NewRegistry()
	must(t, r.Register(TypeDef{ID: "orphan", Extends: "does-not-exist"}))

	err := r.Resolve()
	if err == nil {
		t.Fatal("expected unknown parent to fail Resolve")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %v, want mention of unknown type", err)
	}
}

func TestRegistry_IncompatibleEdgeRedeclaration(t *testing.T) {
	r := NewRegistry()
	must(t, r.Register(TypeDef{
		ID: "core-node",
		EdgeFields: []EdgeFieldDecl{
			{ID: "sources", TargetType: "source", Cardinality: CardinalityMany},
		},
	}))
	must(t, r.Register(TypeDef{
		ID:      "feature",
		Extends: "core-node",
		EdgeFields: []EdgeFieldDecl{
			// Redeclares "sources" with an incompatible cardinality.
			{ID: "sources", TargetType: "source", Cardinality: CardinalityOne},
		},
	}))

	err := r.Resolve()
	if err == nil {
		t.Fatal("expected incompatible edge field redeclaration to fail Resolve")
	}
	if !strings.Contains(err.Error(), "redeclares inherited edge field") {
		t.Errorf("error = %v, want mention of redeclared edge field", err)
	}
}

func TestRegistry_DuplicateTypeID(t *testing.T) {
	r := NewRegistry()
	must(t, r.Register(TypeDef{ID: "feature"}))
	if err := r.Register(TypeDef{ID: "feature"}); err == nil {
		t.Fatal("expected duplicate type id to fail Register")
	}
}

func TestRegistry_NonKebabTypeID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(TypeDef{ID: "Feature_Type"}); err == nil {
		t.Fatal("expected non-kebab type id to fail Register")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

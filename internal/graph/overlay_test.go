package graph

import "testing"

// TestLoadCatalogWithOverlay_UnionsAdditiveNodes exercises the additive-only
// union LoadCatalogWithOverlay does: base's nodes are kept as-is, overlay's
// new node is added, and the result is a valid catalog a caller can then
// hand to Diff/DiffNodes against the base alone.
func TestLoadCatalogWithOverlay_UnionsAdditiveNodes(t *testing.T) {
	base, err := LoadCatalog("testdata/good/minimal.yaml")
	if err != nil {
		t.Fatalf("load base: %v", err)
	}
	merged, err := LoadCatalogWithOverlay("testdata/good/minimal.yaml", "testdata/overlay/add-one.yaml")
	if err != nil {
		t.Fatalf("LoadCatalogWithOverlay: %v", err)
	}

	if len(merged.Nodes) != len(base.Nodes)+1 {
		t.Fatalf("merged has %d nodes, want %d (base) + 1 (overlay)", len(merged.Nodes), len(base.Nodes))
	}
	if _, ok := merged.Nodes["req-two"]; !ok {
		t.Fatalf("merged catalog missing overlay node req-two")
	}
	for id := range base.Nodes {
		if _, ok := merged.Nodes[id]; !ok {
			t.Errorf("merged catalog dropped base node %q", id)
		}
	}

	diffs := DiffNodes(base, merged)
	if len(diffs) != 1 || diffs[0].ID != "req-two" || diffs[0].Kind != GapAdded {
		t.Errorf("DiffNodes(base, merged) = %+v, want exactly one GapAdded req-two", diffs)
	}
}

// TestLoadCatalogWithOverlay_RejectsDuplicateID guards the additive-only
// contract: an overlay node id that already exists in base is an error, not
// a silent override — representing a modification needs real changeset
// apply/merge (epic slice 2), not this loader.
func TestLoadCatalogWithOverlay_RejectsDuplicateID(t *testing.T) {
	_, err := LoadCatalogWithOverlay("testdata/good/minimal.yaml", "testdata/overlay/duplicate.yaml")
	if err == nil {
		t.Fatal("LoadCatalogWithOverlay with a duplicate id: want error, got nil")
	}
}

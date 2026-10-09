package graph

import (
	"sort"
	"testing"
)

const readsFixturePath = "../host/testdata/graph-reads-fixture.yaml"

func loadReadsFixture(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadCatalog(readsFixturePath)
	if err != nil {
		t.Fatalf("LoadCatalog(%s): %v", readsFixturePath, err)
	}
	return cat
}

func TestBuildReverseIndex_TopLevelStorageEdgeVisible(t *testing.T) {
	cat := loadReadsFixture(t)
	idx := BuildReverseIndex(cat)

	// change-leaf --depends_on--> change-root is a storage:top_level edge
	// (read from Fields, not Edges) — this is the exact hazard the plan's
	// §3.3 spec rule calls out: a reverse index built off node.Edges alone
	// would silently miss it.
	refs := idx["change-root"]
	var found bool
	for _, r := range refs {
		if r.Node == "change-leaf" && r.EdgeField == "depends_on" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected change-root's reverse index to include change-leaf via top_level depends_on, got %+v", refs)
	}
}

func TestBuildReverseIndex_NormalEdgeStorage(t *testing.T) {
	cat := loadReadsFixture(t)
	idx := BuildReverseIndex(cat)

	refs := idx["req-alpha"]
	var found bool
	for _, r := range refs {
		if r.Node == "uc-alpha" && r.EdgeField == "covers" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected req-alpha's reverse index to include uc-alpha via covers, got %+v", refs)
	}
}

func TestNeighbors_OutgoingRespectsDepthAndEdgeFilter(t *testing.T) {
	cat := loadReadsFixture(t)

	// depth 1, out, only "acceptance": req-alpha -> uc-alpha.
	triples := Neighbors(cat, "req-alpha", DirectionOut, []EdgeField{"acceptance"}, 1, 0)
	if len(triples) != 1 || triples[0].To != "uc-alpha" || triples[0].EdgeField != "acceptance" {
		t.Fatalf("unexpected triples: %+v", triples)
	}

	// depth 2, out, unfiltered: req-alpha -acceptance-> uc-alpha -covers-> req-alpha (cycle, already visited so not re-walked further, but the hop itself is recorded once).
	triples2 := Neighbors(cat, "req-alpha", DirectionOut, nil, 2, 0)
	if len(triples2) == 0 {
		t.Fatal("expected at least one triple at depth 2")
	}
	var sawDepth2 bool
	for _, tr := range triples2 {
		if tr.Depth == 2 {
			sawDepth2 = true
		}
		if tr.Depth > 2 {
			t.Errorf("triple %+v exceeds requested depth 2", tr)
		}
	}
	if !sawDepth2 {
		t.Errorf("expected a depth-2 hop, got %+v", triples2)
	}
}

func TestNeighbors_InboundViaTopLevelEdge(t *testing.T) {
	cat := loadReadsFixture(t)

	// change-root has no outgoing depends_on, but change-leaf depends on it
	// (top_level storage) — direction "in" must surface that hop.
	triples := Neighbors(cat, "change-root", DirectionIn, []EdgeField{"depends_on"}, 1, 0)
	if len(triples) != 1 || triples[0].From != "change-leaf" || triples[0].To != "change-root" {
		t.Fatalf("expected one inbound depends_on triple from change-leaf, got %+v", triples)
	}
}

func TestNeighbors_BothDirectionsUnion(t *testing.T) {
	cat := loadReadsFixture(t)
	triples := Neighbors(cat, "req-alpha", DirectionBoth, nil, 1, 0)
	var directions []string
	for _, tr := range triples {
		directions = append(directions, tr.Direction)
	}
	sort.Strings(directions)
	var hasOut, hasIn bool
	for _, d := range directions {
		if d == "out" {
			hasOut = true
		}
		if d == "in" {
			hasIn = true
		}
	}
	if !hasOut || !hasIn {
		t.Fatalf("expected both out and in hops from req-alpha, got directions %v (triples=%+v)", directions, triples)
	}
}

func TestNeighbors_LimitCaps(t *testing.T) {
	cat := loadReadsFixture(t)
	triples := Neighbors(cat, "req-alpha", DirectionBoth, nil, 2, 1)
	if len(triples) != 1 {
		t.Fatalf("expected exactly 1 triple with limit=1, got %d: %+v", len(triples), triples)
	}
}

func TestNeighbors_DeterministicAcrossRuns(t *testing.T) {
	cat := loadReadsFixture(t)
	first := Neighbors(cat, "req-alpha", DirectionBoth, nil, 2, 0)
	for i := 0; i < 5; i++ {
		again := Neighbors(cat, "req-alpha", DirectionBoth, nil, 2, 0)
		if len(again) != len(first) {
			t.Fatalf("run %d: length differs: %d vs %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d: triple %d differs: %+v vs %+v", i, j, again[j], first[j])
			}
		}
	}
}

func TestNearestIDs_TypoSuggestsCloseMatch(t *testing.T) {
	cat := loadReadsFixture(t)
	sugg := NearestIDs(cat, "req-alph", 3)
	if len(sugg) == 0 || sugg[0] != "req-alpha" {
		t.Fatalf("expected req-alpha as nearest suggestion for req-alph, got %v", sugg)
	}
}

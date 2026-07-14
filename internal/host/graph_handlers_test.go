package host

import (
	"context"
	"testing"

	objectgraph "kitsoki/internal/graph"
)

const seedCatalogPath = "../../docs/proposals/project-object-graph/seed-objects.yaml"
const seedOverlayPath = "../../docs/proposals/project-object-graph/seed-objects.overlay-ui-declutter.yaml"

// TestGraphHandler_Project exercises the host.graph.project op end to end
// (the S5 replacement for the deleted internal/app/graph/objectcatalog.go —
// see objectCatalogGraph/objectCatalogDiffGraph below for the ported
// nests_under / diff-badging regression checks against those unexported
// helpers directly).
func TestGraphHandler_Project(t *testing.T) {
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "project",
		"catalog_path": seedCatalogPath,
		"graph_id":     "test-project",
	})
	if err != nil {
		t.Fatalf("GraphHandler(project): %v", err)
	}
	wire, ok := res.Data["graph"].(interface{})
	if !ok || wire == nil {
		t.Fatal("expected result.Data[\"graph\"]")
	}
}

// TestGraphHandler_Project_RegistryCarriesArtifactMaterialize is the C1
// regression test for the kit-mode gap this passthrough closes: before it,
// host.graph.project's response had no "registry" key at all, so a
// kit-served portal (unlike the Vite-dev-only /api/catalog splice route)
// never saw a type's artifact:/materialize: declaration — the Materialize
// button/gates checklist silently never rendered outside `npm run dev`.
func TestGraphHandler_Project_RegistryCarriesArtifactMaterialize(t *testing.T) {
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "project",
		"catalog_path": "../materialize/testdata/catalog.yaml",
		"graph_id":     "test-registry",
	})
	if err != nil {
		t.Fatalf("GraphHandler(project): %v", err)
	}
	registry, ok := res.Data["registry"].([]map[string]any)
	if !ok {
		t.Fatalf("expected result.Data[\"registry\"] as []map[string]any, got %T", res.Data["registry"])
	}
	var workItem map[string]any
	for _, entry := range registry {
		if entry["id"] == "work-item" {
			workItem = entry
			break
		}
	}
	if workItem == nil {
		t.Fatalf("no \"work-item\" entry in registry passthrough: %v", registry)
	}
	artifact, ok := workItem["artifact"].(map[string]any)
	if !ok {
		t.Fatalf("work-item registry entry missing artifact: %v", workItem)
	}
	materialize, ok := artifact["materialize"].(map[string]any)
	if !ok {
		t.Fatalf("work-item artifact missing materialize: %v", artifact)
	}
	if materialize["story"] != "story" {
		t.Errorf("materialize.story = %v, want %q", materialize["story"], "story")
	}
	gates, _ := materialize["gates"].([]string)
	if len(gates) != 2 || gates[0] != "gate" || gates[1] != "owner" {
		t.Errorf("materialize.gates = %v, want [gate owner]", materialize["gates"])
	}
	for _, entry := range registry {
		if entry["id"] == "core-node" {
			t.Error("core-node should be filtered out of the registry passthrough, matching vite.config.ts's dev route")
		}
	}
}

func TestGraphHandler_Load(t *testing.T) {
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "load",
		"catalog_path": seedCatalogPath,
	})
	if err != nil {
		t.Fatalf("GraphHandler(load): %v", err)
	}
	if res.Data["node_count"].(int) == 0 {
		t.Fatal("expected node_count > 0")
	}
}

func TestGraphHandler_Lint(t *testing.T) {
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "lint",
		"catalog_path": seedCatalogPath,
	})
	if err != nil {
		t.Fatalf("GraphHandler(lint): %v", err)
	}
	if _, ok := res.Data["issues"]; !ok {
		t.Fatal("expected issues key")
	}
}

func TestGraphHandler_UnknownOp(t *testing.T) {
	_, err := GraphHandler(context.Background(), map[string]any{"op": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown op")
	}
}

func TestGraphHandler_PresentationRequiresKitDir(t *testing.T) {
	_, err := GraphHandler(context.Background(), map[string]any{"op": "presentation"})
	if err == nil {
		t.Fatal("expected error when _kit_dir is missing")
	}
}

func TestObjectCatalogDiffGraph_BadgesTheOverlayAddition(t *testing.T) {
	current, err := objectgraph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog(base): %v", err)
	}
	desired, err := objectgraph.LoadCatalogWithOverlay(seedCatalogPath, seedOverlayPath)
	if err != nil {
		t.Fatalf("LoadCatalogWithOverlay: %v", err)
	}

	g := objectCatalogDiffGraph(current, desired, "test-diff")
	if g.Kind != "object-graph-diff" {
		t.Errorf("g.Kind = %q, want object-graph-diff", g.Kind)
	}
	if len(g.Nodes) != len(desired.Nodes) {
		t.Fatalf("diff graph has %d nodes, want %d (no removed nodes in this fixture pair)", len(g.Nodes), len(desired.Nodes))
	}

	var added, unchanged int
	for _, n := range g.Nodes {
		switch n.Attrs["diff_kind"] {
		case "added":
			added++
			if n.ID != "evidence-object-graph-ui-persona-review" {
				t.Errorf("unexpected added node %q", n.ID)
			}
		case "unchanged":
			unchanged++
		default:
			t.Errorf("node %q has unexpected diff_kind %v", n.ID, n.Attrs["diff_kind"])
		}
	}
	if added != 1 {
		t.Errorf("added = %d, want exactly 1", added)
	}
	if unchanged != len(current.Nodes) {
		t.Errorf("unchanged = %d, want %d (every base node)", unchanged, len(current.Nodes))
	}
}

func TestObjectCatalogGraph_NestsUnderThreadsToWireEdge(t *testing.T) {
	cat, err := objectgraph.LoadCatalog(seedCatalogPath)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	g := objectCatalogGraph(cat, "test")

	var found bool
	for _, e := range g.Edges {
		if e.Kind != "child_of" {
			continue
		}
		found = true
		if e.Attrs["nests_under"] != true {
			t.Errorf("child_of edge %s: attrs[nests_under] = %v, want true", e.ID, e.Attrs["nests_under"])
		}
	}
	if !found {
		t.Fatal("expected at least one child_of edge in the wire graph")
	}

	for _, e := range g.Edges {
		if e.Kind != "proposes" {
			continue
		}
		if e.Attrs["nests_under"] == true {
			t.Errorf("proposes edge %s unexpectedly carries attrs[nests_under]=true", e.ID)
		}
	}
}
func TestGraphHandler_Query(t *testing.T) {
	// refs-to mode
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "query",
		"catalog_path": seedCatalogPath,
		"mode":         "refs-to",
		"target":       "usecase-developer-traces-requirement-to-proof",
	})
	if err != nil {
		t.Fatalf("GraphHandler(query refs-to): %v", err)
	}
	refs, ok := res.Data["references"].([]any)
	if !ok {
		t.Fatal("expected references list")
	}
	if len(refs) == 0 {
		t.Fatal("expected references to not be empty")
	}

	// explain-type mode
	res, err = GraphHandler(context.Background(), map[string]any{
		"op":           "query",
		"catalog_path": seedCatalogPath,
		"mode":         "explain-type",
		"target":       "feature",
	})
	if err != nil {
		t.Fatalf("GraphHandler(query explain-type): %v", err)
	}
	if res.Data["type_id"] != "feature" {
		t.Errorf("expected type_id feature, got %v", res.Data["type_id"])
	}

	// impact mode
	res, err = GraphHandler(context.Background(), map[string]any{
		"op":           "query",
		"catalog_path": seedCatalogPath,
		"mode":         "impact",
		"target":       "usecase-developer-traces-requirement-to-proof",
		"to_type":      "feature",
	})
	if err != nil {
		t.Fatalf("GraphHandler(query impact): %v", err)
	}
	if res.Data["node_id"] != "usecase-developer-traces-requirement-to-proof" {
		t.Errorf("expected node_id usecase-developer-traces-requirement-to-proof, got %v", res.Data["node_id"])
	}
}

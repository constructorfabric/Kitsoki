package graphsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/mcp/graphsrv"
)

// callTool is a small helper: call a tool, fail the test on transport error,
// and return the structured content as a map plus IsError.
func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: StructuredContent is not a map: %T %+v", name, res.StructuredContent, res.StructuredContent)
	}
	return m, res.IsError
}

func TestGraphServer_OpenReturnsOverview(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.open", map[string]any{})
	if isErr {
		t.Fatalf("graph.open returned an error: %+v", m)
	}
	if nc, _ := m["node_count"].(float64); nc != 8 {
		t.Fatalf("node_count = %v, want 8", m["node_count"])
	}
	types, _ := m["types"].([]any)
	if len(types) == 0 {
		t.Fatalf("expected non-empty types census, got %+v", m)
	}
	lint, _ := m["lint"].(map[string]any)
	if lint == nil {
		t.Fatalf("expected lint summary, got %+v", m)
	}
	changesets, _ := m["changesets"].(map[string]any)
	if changesets == nil {
		t.Fatalf("expected changeset lifecycle counts, got %+v", m)
	}
	if guide, _ := m["guide"].(string); guide == "" {
		t.Fatalf("expected a non-empty guide string, got %+v", m)
	}
	if fb, _ := m["feedback"].(map[string]any); fb == nil {
		t.Fatalf("expected a feedback block, got %+v", m)
	}
}

func TestGraphServer_GetReturnsEnvelopeAndMissing(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.get", map[string]any{"ids": []string{"req-alpha", "req-alfa-typo"}})
	if isErr {
		t.Fatalf("graph.get returned an error: %+v", m)
	}
	nodes, _ := m["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 resolved node, got %+v", nodes)
	}
	node, _ := nodes[0].(map[string]any)
	if node["id"] != "req-alpha" {
		t.Fatalf("expected req-alpha, got %+v", node)
	}
	if _, ok := node["edges"]; !ok {
		t.Fatalf("expected edges to always be present, got %+v", node)
	}
	if _, ok := node["refs_in"]; !ok {
		t.Fatalf("expected refs_in to always be present, got %+v", node)
	}
	missing, _ := m["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing entry, got %+v", missing)
	}
	miss, _ := missing[0].(map[string]any)
	suggestions, _ := miss["suggestions"].([]any)
	if len(suggestions) == 0 {
		t.Fatalf("expected nearest-id suggestions for a near-miss id, got %+v", miss)
	}
}

func TestGraphServer_GetRejectsTooManyIDs(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	ids := make([]string, 21)
	for i := range ids {
		ids[i] = "x"
	}
	m, isErr := callTool(t, cs, "graph.get", map[string]any{"ids": ids})
	if !isErr {
		t.Fatalf("expected VALIDATION for >20 ids, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeValidation {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeValidation)
	}
}

func TestGraphServer_FindPaginatesWithCursor(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	seen := map[string]bool{}
	var cursor string
	pages := 0
	for {
		args := map[string]any{"limit": 3}
		if cursor != "" {
			args["cursor"] = cursor
		}
		m, isErr := callTool(t, cs, "graph.find", args)
		if isErr {
			t.Fatalf("graph.find returned an error: %+v", m)
		}
		if changed, _ := m["catalog_changed"].(bool); changed {
			t.Fatalf("unexpected catalog_changed on an unmodified catalog: %+v", m)
		}
		rows, _ := m["rows"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			seen[row["id"].(string)] = true
		}
		pages++
		nc, _ := m["next_cursor"].(string)
		if nc == "" {
			break
		}
		cursor = nc
		if pages > 10 {
			t.Fatalf("pagination did not terminate")
		}
	}
	if len(seen) != 8 {
		t.Fatalf("expected to see all 8 nodes across pages, saw %d: %+v", len(seen), seen)
	}
	if pages < 2 {
		t.Fatalf("expected pagination across multiple pages with limit=3, got %d page(s)", pages)
	}
}

func TestGraphServer_FindDetectsCatalogChanged(t *testing.T) {
	// Copy the fixture into a temp dir so this test can mutate it without
	// affecting other tests sharing testdata/graph-fixture.yaml.
	dir := t.TempDir()
	src, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mutablePath := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(mutablePath, src, 0o644); err != nil {
		t.Fatalf("write mutable fixture: %v", err)
	}

	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{mutablePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.find", map[string]any{"limit": 1})
	if isErr {
		t.Fatalf("graph.find returned an error: %+v", m)
	}
	cursor, _ := m["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("expected a next_cursor with limit=1 and 8 nodes, got %+v", m)
	}

	// Mutate the catalog: add a new node.
	mutated := string(src) + "\n  - schema: graph/change/v0\n    id: change-extra\n    title: Extra change\n    status: active\n    visibility: internal\n    goal: appended after cursor was minted\n    depends_on: []\n"
	if err := os.WriteFile(mutablePath, []byte(mutated), 0o644); err != nil {
		t.Fatalf("mutate fixture: %v", err)
	}

	m2, isErr := callTool(t, cs, "graph.find", map[string]any{"limit": 1, "cursor": cursor})
	if isErr {
		t.Fatalf("graph.find (post-mutation) returned an error: %+v", m2)
	}
	if changed, _ := m2["catalog_changed"].(bool); !changed {
		t.Fatalf("expected catalog_changed:true after the bound catalog changed, got %+v", m2)
	}
}

func TestGraphServer_NeighborsWrapsEngine(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.neighbors", map[string]any{"id": "uc-alpha", "direction": "both", "depth": 1})
	if isErr {
		t.Fatalf("graph.neighbors returned an error: %+v", m)
	}
	triples, _ := m["triples"].([]any)
	if len(triples) == 0 {
		t.Fatalf("expected non-empty triples for uc-alpha, got %+v", m)
	}
}

func TestGraphServer_NeighborsUnknownNode(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.neighbors", map[string]any{"id": "does-not-exist"})
	if !isErr {
		t.Fatalf("expected an error for an unknown node, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeUnknownNode {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeUnknownNode)
	}
}

func TestGraphServer_NeighborsUnknownEdge(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.neighbors", map[string]any{"id": "req-alpha", "edges": []string{"not-a-real-edge"}})
	if !isErr {
		t.Fatalf("expected an error for an unknown edge field, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeUnknownEdge {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeUnknownEdge)
	}
	errMsg, _ := m["error"].(string)
	if !strings.Contains(errMsg, "acceptance") {
		t.Fatalf("expected the error to list the known edge vocabulary, got %q", errMsg)
	}
}

func TestGraphServer_TypeUnknownTypeID(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.type", map[string]any{"type_id": "not-a-real-type"})
	if !isErr {
		t.Fatalf("expected an error for an unknown type_id, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeUnknownType {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeUnknownType)
	}
}

func TestGraphServer_TypeExplainAndList(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.type", map[string]any{"type_id": "requirement"})
	if isErr {
		t.Fatalf("graph.type returned an error: %+v", m)
	}
	if m["type_id"] != "requirement" {
		t.Fatalf("expected type_id requirement, got %+v", m)
	}
	if _, ok := m["instance_count"]; !ok {
		t.Fatalf("expected instance_count when type_id is given, got %+v", m)
	}

	m2, isErr := callTool(t, cs, "graph.type", map[string]any{})
	if isErr {
		t.Fatalf("graph.type (list) returned an error: %+v", m2)
	}
	types, _ := m2["types"].([]any)
	if len(types) == 0 {
		t.Fatalf("expected a non-empty type list when type_id is omitted, got %+v", m2)
	}
}

func TestGraphServer_ImpactWrapsQuery(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.impact", map[string]any{"id": "req-alpha"})
	if isErr {
		t.Fatalf("graph.impact returned an error: %+v", m)
	}
	if m["node_id"] != "req-alpha" {
		t.Fatalf("expected node_id req-alpha, got %+v", m)
	}
	if _, ok := m["references"]; !ok {
		t.Fatalf("expected references, got %+v", m)
	}
}

// TestGraphServer_ToolsListByteCeiling is the golden byte-ceiling test
// (graph-mcp-plan.md §3.7): serializes the tools/list response and asserts
// it stays comfortably under a concrete, measured ceiling. Default mode
// (propose) now registers 17 tools: 9 read (7 + graph.changeset +
// graph.history) + 6 write (propose/withdraw/apply/authorize + claim/release) + 2
// feedback. The plan's own ballpark is ~24KB for a similarly sized
// ~15-tool family; a 24KB ceiling (generous headroom for schema/
// description growth without being so loose it stops catching a real
// regression) is the documented number here.
const toolsListByteCeiling = 24 * 1024

func TestGraphServer_ToolsListByteCeiling(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 17 {
		t.Fatalf("expected 17 tools (9 read + 6 write + 2 feedback) in default (propose) mode, got %d: %+v", len(res.Tools), toolNames(res.Tools))
	}
	b, err := json.Marshal(res.Tools)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	t.Logf("measured tools/list size: %d bytes (ceiling %d)", len(b), toolsListByteCeiling)
	if len(b) > toolsListByteCeiling {
		t.Fatalf("tools/list serialized to %d bytes, want <= %d", len(b), toolsListByteCeiling)
	}
}

func TestGraphServer_ToolsListModeGating(t *testing.T) {
	cases := []struct {
		mode  string
		count int
	}{
		{graphsrv.ModeRead, 11},    // 9 read + 2 feedback, no write tools
		{graphsrv.ModePropose, 17}, // + propose/withdraw/apply/authorize/claim/release
		{graphsrv.ModeSteward, 17},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}, Mode: tc.mode})
			defer done()
			res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
			if err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			if len(res.Tools) != tc.count {
				t.Fatalf("mode %s: expected %d tools, got %d: %+v", tc.mode, tc.count, len(res.Tools), toolNames(res.Tools))
			}
			names := map[string]bool{}
			for _, tool := range res.Tools {
				names[tool.Name] = true
			}
			if tc.mode == graphsrv.ModeRead {
				for _, w := range []string{"graph.propose", "graph.withdraw", "graph.apply", "graph.authorize", "graph.claim", "graph.release"} {
					if names[w] {
						t.Errorf("mode read: %s should not be registered", w)
					}
				}
			}
			if !names["graph.changeset"] {
				t.Errorf("mode %s: graph.changeset should always be registered", tc.mode)
			}
		})
	}
}

func toolNames(tools []*mcpsdk.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

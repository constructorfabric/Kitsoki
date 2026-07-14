package server

import (
	"os"
	"path/filepath"
	"testing"
)

// nonCanonicalFixture mirrors internal/graph's blockScalarFixture: a
// single-file catalog whose hand-wrapped folded block scalar makes
// checkCanonical reject every lifecycle verb until canonicalized.
const nonCanonicalFixture = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: canon-rpc-fixture
type_registry:
  - id: core-node
    schema: graph-type/v0
    required_fields: [id, schema, title, status, visibility]
  - id: requirement
    schema: graph-type/v0
    extends: core-node
  - id: changeset
    schema: graph-type/v0
    extends: core-node
nodes:
  - schema: graph/requirement/v0
    id: req-block
    title: Requirement with a block scalar
    status: draft
    visibility: internal
    statement: >-
      This is a folded block scalar that has been hand-wrapped at a
      narrow width by a human editor, spanning several short lines
      instead of one long line, to keep diffs readable in review —
      yaml.v3's re-marshal collapses this onto one line, the exact
      reflow hazard the canonicality guard exists to catch.
`

func writeNonCanonicalFixture(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(dst, []byte(nonCanonicalFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst
}

// TestGraphRPC_RejectDetailsAndCanonicalize covers the two halves of the
// portal's graceful NEEDS_CANONICALIZATION handling: (1) a rejected
// graph.propose carries structured reject_details (code + file) alongside
// the raw reject_reasons, so a browser client never regexes message text;
// (2) graph.canonicalize repairs the catalog, after which the same propose
// goes through.
func TestGraphRPC_RejectDetailsAndCanonicalize(t *testing.T) {
	root := writeNonCanonicalFixture(t)
	s := &Server{}

	propose := func() map[string]any {
		result, rerr := s.graphProposeRPC(map[string]any{
			"catalog_path": root,
			"title":        "blocked until canonicalized",
			"operations": []any{
				map[string]any{
					"kind":  "added",
					"after": map[string]any{"schema": "graph/requirement/v0", "id": "req-new", "title": "Lands after canonicalize", "status": "draft", "visibility": "internal"},
				},
			},
		})
		if rerr != nil {
			t.Fatalf("graphProposeRPC: %+v", rerr)
		}
		return result.(map[string]any)
	}

	// (1) Rejected, with structured details.
	m := propose()
	if rejected, _ := m["rejected"].(bool); !rejected {
		t.Fatalf("expected propose rejected on non-canonical catalog, got: %#v", m)
	}
	details, _ := m["reject_details"].([]any)
	if len(details) == 0 {
		t.Fatalf("expected reject_details alongside reject_reasons, got: %#v", m)
	}
	d0 := details[0].(map[string]any)
	if code, _ := d0["code"].(string); code != "needs_canonicalization" {
		t.Errorf("reject_details[0].code = %q, want needs_canonicalization", code)
	}
	if file, _ := d0["file"].(string); file != root {
		t.Errorf("reject_details[0].file = %q, want %q", file, root)
	}

	// (2) graph.canonicalize repairs it.
	result, rerr := s.graphCanonicalizeRPC(map[string]any{"catalog_path": root})
	if rerr != nil {
		t.Fatalf("graphCanonicalizeRPC: %+v", rerr)
	}
	cm := result.(map[string]any)
	if changed, _ := cm["changed_files"].([]any); len(changed) != 1 {
		t.Fatalf("expected one changed file, got: %#v", cm)
	}

	// Second canonicalize is a no-op reporting already_canonical.
	result, rerr = s.graphCanonicalizeRPC(map[string]any{"catalog_path": root})
	if rerr != nil {
		t.Fatalf("second graphCanonicalizeRPC: %+v", rerr)
	}
	if already, _ := result.(map[string]any)["already_canonical"].(bool); !already {
		t.Errorf("second canonicalize should report already_canonical, got: %#v", result)
	}

	// And the propose that was blocked now lands.
	m = propose()
	if rejected, _ := m["rejected"].(bool); rejected {
		t.Fatalf("propose still rejected after canonicalize: %#v", m)
	}
}

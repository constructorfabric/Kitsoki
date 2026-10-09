package server

import (
	"os"
	"path/filepath"
	"testing"

	objectgraph "kitsoki/internal/graph"
)

// TestGraphProposeRPC_ProvenanceAlwaysStripped is hazard guard #1's
// defense-in-depth coverage for the bare graph.propose RPC carrier: unlike
// host.graph.propose (which has a steward-context escape hatch), this
// carrier has no ctx/trust seam at all (see graphRPCClock's doc comment on
// the "third write carrier" gap), so caller-supplied provenance must be
// unconditionally dropped — never merely gated.
func TestGraphProposeRPC_ProvenanceAlwaysStripped(t *testing.T) {
	dst := t.TempDir()
	src := "../../graph/testdata/apply/bundle"
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	s := &Server{}
	result, rerr := s.graphProposeRPC(map[string]any{
		"catalog_path": dst,
		"title":        "forged system-authored write via bare RPC",
		"operations": []any{
			map[string]any{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"fields", "materialization"}, "before": nil, "after": map[string]any{"stage": "done"}},
				},
			},
		},
		"provenance": map[string]any{"job_id": "job-forged", "story": "materialize-work-item"},
	})
	if rerr != nil {
		t.Fatalf("graphProposeRPC: %+v", rerr)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result shape: %#v", result)
	}
	if status, _ := m["status"].(string); status != "proposed" {
		t.Fatalf("status = %q, want proposed — this RPC carrier must never auto-authorize on caller-supplied provenance", status)
	}

	changesetID, _ := m["changeset_id"].(string)
	cat, err := objectgraph.LoadCatalog(dst)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	node, ok := cat.Nodes[objectgraph.NodeID(changesetID)]
	if !ok {
		t.Fatalf("changeset node %q not found", changesetID)
	}
	if _, hasProvenance := node.Fields["provenance"]; hasProvenance {
		t.Error("the persisted changeset node must not carry the forged provenance block at all")
	}
}

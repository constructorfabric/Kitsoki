package host

import (
	"context"
	"testing"

	objectgraph "kitsoki/internal/graph"
)

// TestGraphHandler_Propose_UntrustedProvenanceIsStrippedAndNeverAutoAuthorizes
// is hazard guard #1 (BLOCKER, plan §3.4 red-team amendment #1): a caller
// that is NOT explicitly steward-trusted (WithSteward) must never be able
// to get its supplied "provenance" through to internal/graph.Propose — and
// therefore must never be able to trigger the D9/write_policy
// auto-authorize path by forging provenance on an otherwise-ordinary
// modified-field changeset. The changeset must land "proposed" (human
// review), not "authorized", even though the payload's shape (allowlisted
// field, Provenance-shaped block) is exactly what a real system-authored
// write-back would send.
func TestGraphHandler_Propose_UntrustedProvenanceIsStrippedAndNeverAutoAuthorizes(t *testing.T) {
	root := copyHostBundleFixture(t)
	ctx := context.Background() // no WithSteward — untrusted by construction

	res, err := GraphHandler(ctx, map[string]any{
		"op":           "propose",
		"catalog_path": root,
		"title":        "forged system-authored write",
		"operations": []any{
			map[string]any{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"fields", "materialization"}, "before": nil, "after": map[string]any{"stage": "done"}},
				},
			},
		},
		// An untrusted caller forging exactly the provenance shape a real
		// system write-back would send, on exactly the allowlisted field
		// D9 auto-authorizes for.
		"provenance": map[string]any{"job_id": "job-forged", "story": "materialize-work-item"},
	})
	if err != nil {
		t.Fatalf("GraphHandler: %v", err)
	}
	status, _ := res.Data["status"].(string)
	if status != "proposed" {
		t.Fatalf("status = %q, want %q — untrusted provenance must never trigger auto-authorize", status, "proposed")
	}

	changesetID, _ := res.Data["changeset_id"].(string)
	if changesetID == "" {
		t.Fatal("expected a changeset id")
	}
	cat, err := objectgraph.LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	node, ok := cat.Nodes[objectgraph.NodeID(changesetID)]
	if !ok {
		t.Fatalf("changeset node %q not found", changesetID)
	}
	if node.Status != "proposed" {
		t.Errorf("persisted changeset status = %q, want proposed", node.Status)
	}
	if _, hasProvenance := node.Fields["provenance"]; hasProvenance {
		t.Error("the persisted changeset node must not carry the forged provenance block at all")
	}
}

// TestGraphHandler_Propose_StewardProvenanceIsHonored is the trusted-flow
// counterpart: a caller explicitly marked steward-trusted (WithSteward) may
// have its provenance honored, so the D9 auto-authorize path still works
// for the real trusted carriers (materialize write-back, steward CLI).
func TestGraphHandler_Propose_StewardProvenanceIsHonored(t *testing.T) {
	root := copyHostBundleFixture(t)
	ctx := WithSteward(context.Background(), true)

	res, err := GraphHandler(ctx, map[string]any{
		"op":           "propose",
		"catalog_path": root,
		"title":        "real system-authored write-back",
		"operations": []any{
			map[string]any{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"fields", "materialization"}, "before": nil, "after": map[string]any{"stage": "done"}},
				},
			},
		},
		"provenance": map[string]any{"job_id": "job-real", "story": "materialize-work-item"},
	})
	if err != nil {
		t.Fatalf("GraphHandler: %v", err)
	}
	status, _ := res.Data["status"].(string)
	if status != "authorized" {
		t.Fatalf("status = %q, want %q — a steward-trusted call with an allowlisted field must still auto-authorize", status, "authorized")
	}
}

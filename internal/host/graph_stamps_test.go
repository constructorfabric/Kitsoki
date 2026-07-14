package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	objectgraph "kitsoki/internal/graph"
)

// copyHostBundleFixture copies internal/graph/testdata/apply/bundle (the
// same fixture internal/graph's own Propose/Authorize/Apply tests use) into
// a fresh temp dir, so a host.graph.propose/authorize/apply round trip
// through GraphHandler can mutate its own scratch copy.
func copyHostBundleFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "../graph/testdata/apply/bundle"
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
	return dst
}

// TestGraphHandler_Propose_StampsWithActorAndFixedClock exercises the full
// ctx -> GraphHandler -> graphResolveClock/ActorFromContext -> objectgraph.
// Propose seam (plan §3.4 item 1/2): KITSOKI_GRAPH_CLOCK_FIXED pins the
// clock, host.WithActor injects the operator identity, and both land in the
// new changeset node's Fields (created_at/authored_by) with zero schema
// migration.
func TestGraphHandler_Propose_StampsWithActorAndFixedClock(t *testing.T) {
	prev := getenvGraphClockFixed
	getenvGraphClockFixed = func() string { return "2026-05-01T09:00:00Z" }
	t.Cleanup(func() { getenvGraphClockFixed = prev })

	root := copyHostBundleFixture(t)
	ctx := WithActor(context.Background(), "carol")

	res, err := GraphHandler(ctx, map[string]any{
		"op":           "propose",
		"catalog_path": root,
		"title":        "stamped via host.graph.propose",
		"operations": []any{
			map[string]any{
				"kind": "added",
				"after": map[string]any{
					"schema": "graph/requirement/v0", "id": "req-host-stamp",
					"title": "Stamped", "status": "draft", "visibility": "public",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("GraphHandler(propose): %v", err)
	}
	if rejected, _ := res.Data["rejected"].(bool); rejected {
		t.Fatalf("expected propose to succeed, got: %v", res.Data["reject_reasons"])
	}
	changesetID := res.Data["changeset_id"].(string)

	cat, err := objectgraph.LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := cat.Nodes[objectgraph.NodeID(changesetID)]
	if !ok {
		t.Fatalf("changeset node %q not found", changesetID)
	}
	if got, _ := node.Fields["created_at"].(string); got != "2026-05-01T09:00:00Z" {
		t.Errorf("created_at = %q, want the KITSOKI_GRAPH_CLOCK_FIXED stamp", got)
	}
	if got, _ := node.Fields["authored_by"].(string); got != "carol" {
		t.Errorf("authored_by = %q, want %q", got, "carol")
	}
}

// TestGraphHandler_Propose_InvalidClockFixedEnvErrors: an unparseable
// KITSOKI_GRAPH_CLOCK_FIXED must fail closed (an error, not a silent
// fallback to the real clock) — determinism under gates is the whole point.
func TestGraphHandler_Propose_InvalidClockFixedEnvErrors(t *testing.T) {
	prev := getenvGraphClockFixed
	getenvGraphClockFixed = func() string { return "not-a-timestamp" }
	t.Cleanup(func() { getenvGraphClockFixed = prev })

	root := copyHostBundleFixture(t)
	_, err := GraphHandler(context.Background(), map[string]any{
		"op":           "propose",
		"catalog_path": root,
		"title":        "should not commit",
		"operations": []any{
			map[string]any{
				"kind": "added",
				"after": map[string]any{
					"schema": "graph/requirement/v0", "id": "req-should-not-exist",
					"title": "x", "status": "draft", "visibility": "public",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid KITSOKI_GRAPH_CLOCK_FIXED value")
	}
}

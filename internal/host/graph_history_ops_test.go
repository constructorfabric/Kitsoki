package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	objectgraph "kitsoki/internal/graph"
)

const historyFixturePath = "testdata/graph-history-fixture.yaml"

// TestHistoryChanged_CatchesStatusOnlyFlip is the plan §3.5 red-team
// amendment #6 required test case: objectgraph.DiffNodes deliberately
// excludes Status from its structural comparison (see diff.go), so a
// commit/changeset that flips ONLY a node's status must still show up as a
// gap when historyChanged unions DiffNodes with an explicit Status
// compare — proving historyChanged catches what DiffNodes alone misses.
func TestHistoryChanged_CatchesStatusOnlyFlip(t *testing.T) {
	older := &objectgraph.Catalog{Nodes: map[objectgraph.NodeID]*objectgraph.Node{
		"req-alpha": {ID: "req-alpha", Title: "Requirement alpha", Status: "active", Visibility: "internal"},
	}}
	newer := &objectgraph.Catalog{Nodes: map[objectgraph.NodeID]*objectgraph.Node{
		"req-alpha": {ID: "req-alpha", Title: "Requirement alpha", Status: "done", Visibility: "internal"},
	}}

	// Sanity check the premise: DiffNodes alone must NOT see this change
	// (Status is deliberately excluded from structurallyDiffers).
	if diffs := objectgraph.DiffNodes(older, newer); len(diffs) != 0 {
		t.Fatalf("premise violated: DiffNodes alone found %d diffs for a status-only flip, want 0 (Status is excluded from structurallyDiffers): %+v", len(diffs), diffs)
	}

	diffs := historyChanged(older, newer)
	if len(diffs) != 1 {
		t.Fatalf("historyChanged: got %d diffs, want 1 (the status-only flip): %+v", len(diffs), diffs)
	}
	if diffs[0].ID != "req-alpha" || diffs[0].Kind != objectgraph.GapModified {
		t.Errorf("historyChanged: got %+v, want {ID: req-alpha, Kind: modified}", diffs[0])
	}
}

func TestGraphHistoryOp_ChangesetEra(t *testing.T) {
	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "history",
		"catalog_path": historyFixturePath,
		"id":           "req-beta",
	})
	if err != nil {
		t.Fatalf("GraphHandler(history): %v", err)
	}
	entries, ok := res.Data["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected at least 1 entry, got %v", res.Data["entries"])
	}
	e0 := entries[0].(map[string]any)
	if e0["source"] != "changeset" {
		t.Errorf("source = %v, want changeset", e0["source"])
	}
	if e0["changeset_id"] != "cs-1" {
		t.Errorf("changeset_id = %v, want cs-1", e0["changeset_id"])
	}
	if e0["kind"] != "modified" {
		t.Errorf("kind = %v, want modified", e0["kind"])
	}
	// applied_at should win over authorized_at/created_at.
	if e0["ts"] != "2026-01-03T00:00:00Z" {
		t.Errorf("ts = %v, want 2026-01-03T00:00:00Z (applied_at should win)", e0["ts"])
	}
}

func TestGraphHistoryOp_UnknownNodeErrors(t *testing.T) {
	_, err := GraphHandler(context.Background(), map[string]any{
		"op":           "history",
		"catalog_path": historyFixturePath,
		"id":           "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected an error for an unknown node id")
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Errorf("error = %q, want it to contain \"unknown node\"", err.Error())
	}
}

// ─── git-era fixture ───

func requireGitForHistoryTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
}

const historyGitFixtureHeader = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: graph-history-git-fixture
type_registry:
  - id: core-node
    schema: graph-type/v0
    extends: null
    required_fields: [id, schema, title, status, visibility]
  - id: requirement
    schema: graph-type/v0
    extends: core-node
nodes:
`

const historyGitFixtureReqAlphaV1 = `  - schema: graph/requirement/v0
    id: req-alpha
    title: Requirement alpha
    status: active
    visibility: internal
    statement: alpha must hold
`

const historyGitFixtureReqAlphaV2 = `  - schema: graph/requirement/v0
    id: req-alpha
    title: Requirement alpha v2
    status: active
    visibility: internal
    statement: alpha must hold
`

const historyGitFixtureReqAlphaV3StatusFlip = `  - schema: graph/requirement/v0
    id: req-alpha
    title: Requirement alpha v2
    status: done
    visibility: internal
    statement: alpha must hold
`

const historyGitFixtureReqBeta = `  - schema: graph/requirement/v0
    id: req-beta
    title: Requirement beta
    status: active
    visibility: internal
    statement: beta must hold
`

const historyGitFixtureReqGamma = `  - schema: graph/requirement/v0
    id: req-gamma
    title: Requirement gamma
    status: active
    visibility: internal
    statement: gamma must hold
`

// newHistoryGitFixture builds a 4-commit git history exercising every
// NodeDiff kind graph.history's git era must classify:
//
//	c1 (seed): req-alpha v1 + req-beta
//	c2: req-alpha's title changes (structural GapModified via DiffNodes)
//	c3: req-alpha's status ONLY flips active -> done (the mandatory
//	    status-only-flip case, plan §3.5 red-team amendment #6 —
//	    DiffNodes alone would miss this, historyChanged must not)
//	c4: req-gamma is added (GapAdded)
func newHistoryGitFixture(t *testing.T) (repoRoot, catalogPath string) {
	t.Helper()
	requireGitForHistoryTest(t)

	repoRoot = t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	catalogPath = filepath.Join(repoRoot, "catalog.yaml")
	writeAndCommit := func(body, message string) {
		content := historyGitFixtureHeader + body
		if err := os.WriteFile(catalogPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write catalog: %v", err)
		}
		run("add", "catalog.yaml")
		run("commit", "-q", "-m", message)
	}

	writeAndCommit(historyGitFixtureReqAlphaV1+historyGitFixtureReqBeta, "seed fixture catalog")
	writeAndCommit(historyGitFixtureReqAlphaV2+historyGitFixtureReqBeta, "modify req-alpha title")
	writeAndCommit(historyGitFixtureReqAlphaV3StatusFlip+historyGitFixtureReqBeta, "flip req-alpha status only")
	writeAndCommit(historyGitFixtureReqAlphaV3StatusFlip+historyGitFixtureReqBeta+historyGitFixtureReqGamma, "add req-gamma")

	return repoRoot, catalogPath
}

func TestGraphHistoryOp_GitEra_ScopedToID(t *testing.T) {
	_, catalogPath := newHistoryGitFixture(t)

	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "history",
		"catalog_path": catalogPath,
		"id":           "req-alpha",
	})
	if err != nil {
		t.Fatalf("GraphHandler(history): %v", err)
	}
	entries, ok := res.Data["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected git-era entries for req-alpha, got %v", res.Data["entries"])
	}

	var sawStatusFlip, sawTitleChange bool
	for _, raw := range entries {
		e := raw.(map[string]any)
		if e["source"] != "git" {
			t.Fatalf("entry source = %v, want git (no changesets in this fixture): %+v", e["source"], e)
		}
		if e["id"] != "req-alpha" {
			t.Errorf("entry id = %v, want req-alpha (id filter should have excluded req-beta/req-gamma)", e["id"])
		}
		if e["rev"] == nil || e["rev"] == "" {
			t.Errorf("entry missing rev: %+v", e)
		}
		if e["kind"] == "modified" {
			// Both the title-change commit and the status-only-flip
			// commit produce a "modified" entry for req-alpha; we can't
			// tell them apart by kind alone, so just confirm we got (at
			// least) two distinct "modified" entries — the mandatory
			// proof that the status-only commit wasn't silently dropped.
			if sawTitleChange {
				sawStatusFlip = true
			}
			sawTitleChange = true
		}
	}
	if !sawTitleChange || !sawStatusFlip {
		t.Errorf("expected 2 distinct git-era \"modified\" entries for req-alpha (title change + status-only flip), got entries: %+v", entries)
	}
}

func TestGraphHistoryOp_GitEra_CatalogWideTimeline(t *testing.T) {
	_, catalogPath := newHistoryGitFixture(t)

	res, err := GraphHandler(context.Background(), map[string]any{
		"op":           "history",
		"catalog_path": catalogPath,
	})
	if err != nil {
		t.Fatalf("GraphHandler(history): %v", err)
	}
	entries, ok := res.Data["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected a catalog-wide timeline, got %v", res.Data["entries"])
	}

	ids := map[string]bool{}
	var sawAdded bool
	for _, raw := range entries {
		e := raw.(map[string]any)
		ids[e["id"].(string)] = true
		if e["kind"] == "added" {
			sawAdded = true
		}
	}
	if !ids["req-alpha"] {
		t.Errorf("expected req-alpha to appear in the catalog-wide timeline, got ids: %v", ids)
	}
	if !sawAdded {
		t.Errorf("expected an \"added\" entry for req-gamma (commit 4), got entries: %+v", entries)
	}
}

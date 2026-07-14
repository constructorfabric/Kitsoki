package graphsrv_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/mcp/graphsrv"
)

const historyGitFixtureHeader = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: graph-history-mcp-fixture
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

const historyGitFixtureReqAlphaTitleChanged = `  - schema: graph/requirement/v0
    id: req-alpha
    title: Requirement alpha v2
    status: active
    visibility: internal
    statement: alpha must hold
`

// historyGitFixtureReqAlphaStatusFlip is commit 3's content: title stays
// exactly as commit 2 left it, ONLY status changes. This is the mandatory
// plan §3.5 red-team amendment #6 test case — a commit that
// objectgraph.DiffNodes alone (Status is deliberately excluded from its
// structural comparison, see internal/graph/diff.go) would not detect, but
// host.historyChanged must.
const historyGitFixtureReqAlphaStatusFlip = `  - schema: graph/requirement/v0
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

// newHistoryMCPFixture inits a fresh git repo with a 4-commit history
// (seed, structural title change, status-only flip, add a node) — the same
// shape as internal/host/graph_history_ops_test.go's fixture, rebuilt here
// so this package's round-trip test doesn't depend on internal/host's
// test-only helpers (unexported, different package).
func newHistoryMCPFixture(t *testing.T) (repoRoot, catalogPath string) {
	t.Helper()
	requireGit(t)

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
	writeAndCommit(historyGitFixtureReqAlphaTitleChanged+historyGitFixtureReqBeta, "modify req-alpha title")
	writeAndCommit(historyGitFixtureReqAlphaStatusFlip+historyGitFixtureReqBeta, "flip req-alpha status only")
	writeAndCommit(historyGitFixtureReqAlphaStatusFlip+historyGitFixtureReqBeta+historyGitFixtureReqGamma, "add req-gamma")

	return repoRoot, catalogPath
}

func TestGraphServer_HistoryScopedToID_IncludesStatusOnlyFlip(t *testing.T) {
	_, catalogPath := newHistoryMCPFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.history", map[string]any{"id": "req-alpha"})
	if isErr {
		t.Fatalf("graph.history returned an error: %+v", m)
	}
	entries, ok := m["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected entries for req-alpha, got %+v", m)
	}

	modifiedCount := 0
	for _, raw := range entries {
		e, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("entry is not a map: %T %+v", raw, raw)
		}
		if e["source"] != "git" {
			t.Fatalf("entry source = %v, want git (this fixture has no changesets): %+v", e["source"], e)
		}
		if e["id"] != "req-alpha" {
			t.Errorf("entry id = %v, want req-alpha", e["id"])
		}
		if _, ok := e["rev"].(string); !ok {
			t.Errorf("entry missing string rev: %+v", e)
		}
		if e["kind"] == "modified" {
			modifiedCount++
		}
	}
	// Two distinct "modified" commits touched req-alpha: the title change
	// and the status-only flip. Both must be present — this is the actual
	// proof that the status-only commit wasn't silently dropped.
	if modifiedCount != 2 {
		t.Errorf("expected 2 \"modified\" entries for req-alpha (title change + status-only flip), got %d: %+v", modifiedCount, entries)
	}
}

func TestGraphServer_HistoryCatalogWideTimeline(t *testing.T) {
	_, catalogPath := newHistoryMCPFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.history", map[string]any{})
	if isErr {
		t.Fatalf("graph.history returned an error: %+v", m)
	}
	entries, ok := m["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("expected a catalog-wide timeline, got %+v", m)
	}

	ids := map[string]bool{}
	for _, raw := range entries {
		e := raw.(map[string]any)
		ids[e["id"].(string)] = true
	}
	if !ids["req-alpha"] {
		t.Errorf("expected req-alpha in the catalog-wide timeline, got ids: %v", ids)
	}
	if !ids["req-gamma"] {
		t.Errorf("expected req-gamma (added in commit 4) in the catalog-wide timeline, got ids: %v", ids)
	}
}

func TestGraphServer_HistoryUnknownNodeErrors(t *testing.T) {
	_, catalogPath := newHistoryMCPFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	m, isErr := callTool(t, cs, "graph.history", map[string]any{"id": "does-not-exist"})
	if !isErr {
		t.Fatalf("expected an error for an unknown node, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeUnknownNode {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeUnknownNode)
	}
}

func TestGraphServer_HistoryToolListedInEveryMode(t *testing.T) {
	_, catalogPath := newHistoryMCPFixture(t)
	for _, mode := range []string{graphsrv.ModeRead, graphsrv.ModePropose, graphsrv.ModeSteward} {
		t.Run(mode, func(t *testing.T) {
			cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}, Mode: mode})
			defer done()
			res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
			if err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			var found bool
			for _, tool := range res.Tools {
				if tool.Name == "graph.history" {
					found = true
				}
			}
			if !found {
				t.Errorf("mode %s: graph.history not listed", mode)
			}
		})
	}
}

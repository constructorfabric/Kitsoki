package graphsrv_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/mcp/graphsrv"
)

// requireGit skips the test if git isn't on PATH (mirrors internal/app's
// requireGit precedent).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
}

// newGitFixtureCatalog inits a fresh git repo under a temp dir, copies the
// package's fixture catalog into it, and commits — giving a real git
// toplevel distinct from the test binary's own working tree, for the
// repo-root-anchoring test below.
func newGitFixtureCatalog(t *testing.T) (repoRoot, catalogPath string) {
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

	src, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	catalogPath = filepath.Join(repoRoot, "catalog.yaml")
	if err := os.WriteFile(catalogPath, src, 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	run("add", "catalog.yaml")
	run("commit", "-q", "-m", "seed fixture catalog")
	return repoRoot, catalogPath
}

func mustCallTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(%s): StructuredContent is not a map: %T %+v", name, res.StructuredContent, res.StructuredContent)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned an error result: %+v", name, m)
	}
	return m
}

func TestFeedbackReport_AnchorsUnderCatalogRepoRootNotCwd(t *testing.T) {
	repoRoot, catalogPath := newGitFixtureCatalog(t)

	// The critical assertion: launch the server (and run the call) with
	// cwd set to somewhere else entirely — an unrelated temp dir, not the
	// catalog's repo. If anchoring used process cwd (or the launch dir),
	// the bundle would land there instead of under repoRoot.
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	m := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title":       "graph.find is missing a foo filter",
		"goal":        "filter nodes by foo",
		"why_blocked": "no foo filter exists on graph.find",
		"expected":    "graph.find should accept a foo filter",
	})
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatalf("expected ok:true, got %+v", m)
	}
	localPath, _ := m["local_path"].(string)
	if localPath == "" {
		t.Fatalf("expected local_path to be set, got %+v", m)
	}

	// The bundle must exist under the catalog repo's toplevel...
	bundlePath := filepath.Join(repoRoot, ".artifacts", "graph-mcp", "feedback.jsonl")
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected feedback ledger at %s, got: %v", bundlePath, err)
	}
	reportsDir := filepath.Join(repoRoot, ".artifacts", "graph-mcp", "reports")
	entries, err := os.ReadDir(reportsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one markdown report under %s, got %v (err=%v)", reportsDir, entries, err)
	}

	// ...and must NOT exist anywhere under the unrelated cwd.
	if _, err := os.Stat(filepath.Join(elsewhere, ".artifacts")); !os.IsNotExist(err) {
		t.Fatalf("expected no .artifacts under the unrelated cwd %s, stat err = %v", elsewhere, err)
	}
}

func TestFeedbackReport_DedupesByKindAndNormalizedTitle(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)

	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	first := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "Missing   Foo Filter", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	firstID, _ := first["report_id"].(string)
	if firstID == "" {
		t.Fatalf("expected report_id, got %+v", first)
	}
	if dup, _ := first["duplicate_of"]; dup != nil {
		t.Fatalf("first submission should not be a duplicate, got %+v", first)
	}

	// Same (kind, normalized-title) modulo case/whitespace -> duplicate.
	second := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "missing foo   filter", "goal": "different goal text", "why_blocked": "w2", "expected": "e2",
	})
	dupOf, _ := second["duplicate_of"].(string)
	if dupOf != firstID {
		t.Fatalf("expected duplicate_of=%s, got %+v", firstID, second)
	}
}

func TestFeedbackReport_ULIDShapeAndMonotonic(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	m1 := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "one", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	m2 := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "two", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	id1, _ := m1["report_id"].(string)
	id2, _ := m2["report_id"].(string)
	if len(id1) != 26 || len(id2) != 26 {
		t.Fatalf("expected 26-char ULIDs, got %q, %q", id1, id2)
	}
	if id1 >= id2 {
		t.Fatalf("expected monotonically increasing ULIDs, got %s then %s", id1, id2)
	}
}

func TestFeedbackList_ReturnsSubmittedReports(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	mustCallTool(t, cs, "feedback.report", map[string]any{
		"kind": "bug", "title": "a bug", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	mustCallTool(t, cs, "feedback.report", map[string]any{
		"kind": "doc_gap", "title": "a doc gap", "goal": "g", "why_blocked": "w", "expected": "e",
	})

	all := mustCallTool(t, cs, "feedback.list", map[string]any{})
	if total, _ := all["total"].(float64); total != 2 {
		t.Fatalf("expected total=2, got %+v", all)
	}

	onlyBugs := mustCallTool(t, cs, "feedback.list", map[string]any{"kind": "bug"})
	if total, _ := onlyBugs["total"].(float64); total != 1 {
		t.Fatalf("expected total=1 for kind=bug, got %+v", onlyBugs)
	}
}

func TestFeedbackReport_NonBlockingOnBadCatalogAlias(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "feedback.report",
		Arguments: map[string]any{
			"title": "t", "goal": "g", "why_blocked": "w", "expected": "e",
			"anchor": map[string]any{"catalog": "nope"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("feedback.report must never return isError, got %+v", res.StructuredContent)
	}
	m, _ := res.StructuredContent.(map[string]any)
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatalf("expected ok:true even on an unresolvable anchor catalog, got %+v", m)
	}
	routingErrors, _ := m["routing_errors"].([]any)
	if len(routingErrors) == 0 {
		t.Fatalf("expected a routing_errors entry for the unresolvable anchor catalog, got %+v", m)
	}
}

func TestFeedbackReport_DegradedSinkRecordsRoutingError(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags: []string{catalogPath},
		FeedbackSink: graphsrv.FeedbackSinkGithub,
	})
	defer done()

	m := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "t", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	routingErrors, _ := m["routing_errors"].([]any)
	if len(routingErrors) == 0 {
		t.Fatalf("expected a routing_errors entry noting the github sink degraded to local, got %+v", m)
	}
	// It still wrote locally despite the degrade warning.
	routed, _ := m["routed"].([]any)
	if len(routed) != 1 {
		t.Fatalf("expected exactly one routed entry (local), got %+v", m)
	}
}

func TestGraphOpen_FeedbackPendingReflectsLocalSink(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	before := mustCallTool(t, cs, "graph.open", map[string]any{})
	feedbackBefore, _ := before["feedback"].(map[string]any)
	if pending, _ := feedbackBefore["pending"].(float64); pending != 0 {
		t.Fatalf("expected pending=0 before any reports, got %+v", before)
	}

	mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "t", "goal": "g", "why_blocked": "w", "expected": "e",
	})

	after := mustCallTool(t, cs, "graph.open", map[string]any{})
	feedbackAfter, _ := after["feedback"].(map[string]any)
	if pending, _ := feedbackAfter["pending"].(float64); pending != 1 {
		t.Fatalf("expected pending=1 after one report, got %+v", after)
	}
}

func TestFeedbackReport_EvidenceRingBufferIsHashedNotRaw(t *testing.T) {
	_, catalogPath := newGitFixtureCatalog(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{catalogPath}})
	defer done()

	// Call a read tool with a distinctive raw arg value first, so we can
	// assert it never appears verbatim in the written bundle.
	secretID := "req-super-secret-id-marker"
	_, _ = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "graph.get",
		Arguments: map[string]any{"ids": []string{secretID}},
	})

	m := mustCallTool(t, cs, "feedback.report", map[string]any{
		"title": "t", "goal": "g", "why_blocked": "w", "expected": "e",
	})
	localPath, _ := m["local_path"].(string)
	if localPath == "" {
		t.Fatalf("expected local_path, got %+v", m)
	}

	repoRoot, err := filepath.Abs(filepath.Dir(catalogPath))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// local_path is relative to sinkDir's parent (<repoRoot>/.artifacts).
	full := filepath.Join(repoRoot, ".artifacts", localPath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read report markdown %s: %v", full, err)
	}
	if strings.Contains(string(data), secretID) {
		t.Fatalf("evidence ring buffer leaked a raw arg value into the report bundle: %s", data)
	}

	// Also verify the JSONL ledger line doesn't leak it either.
	jsonlPath := filepath.Join(repoRoot, ".artifacts", "graph-mcp", "feedback.jsonl")
	jsonlData, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if strings.Contains(string(jsonlData), secretID) {
		t.Fatalf("evidence ring buffer leaked a raw arg value into the JSONL ledger: %s", jsonlData)
	}
}

package graphsrv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/mcp/graphsrv"
)

// capsuleFixture builds a git repo holding the fixture catalog plus the
// capsule-write-routing project profile and an executable (never actually
// executed — the fake runner intercepts it) scripts/dev-workspace.sh stub.
func capsuleFixture(t *testing.T, writeVia string) (repoRoot, catalogPath string) {
	t.Helper()
	repoRoot, catalogPath = newGitFixtureCatalog(t)
	if writeVia != "" {
		profile := fmt.Sprintf("schema: project-profile/v1\nid: fixture\ngraph:\n  write_via: %s\n", writeVia)
		mustWriteFile(t, filepath.Join(repoRoot, ".kitsoki", "project-profile.yaml"), profile, 0o644)
	}
	mustWriteFile(t, filepath.Join(repoRoot, "scripts", "dev-workspace.sh"), "#!/bin/sh\nexit 1\n", 0o755)
	return repoRoot, catalogPath
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeWorkspaceRunner is the deterministic dev-workspace.sh stand-in: create
// materializes the workspace by copying the repo's catalog file, commit and
// merge just record (configurably failing), so no real clone ever happens.
type fakeWorkspaceRunner struct {
	t        *testing.T
	repoRoot string
	wsRoot   string

	mu       sync.Mutex
	calls    [][]string
	mergeErr string // non-empty: merge exits 1 with this stderr
}

func (f *fakeWorkspaceRunner) Run(_ context.Context, dir, program string, args ...string) (graphsrv.WorkspaceRunResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{}, args...))
	f.mu.Unlock()
	if !strings.HasSuffix(program, "dev-workspace.sh") {
		f.t.Fatalf("unexpected program: %s", program)
	}
	switch args[0] {
	case "create":
		id := argValue(args, "--id")
		wsPath := filepath.Join(f.wsRoot, id)
		src, err := os.ReadFile(filepath.Join(f.repoRoot, "catalog.yaml"))
		if err != nil {
			return graphsrv.WorkspaceRunResult{}, err
		}
		if err := os.MkdirAll(wsPath, 0o755); err != nil {
			return graphsrv.WorkspaceRunResult{}, err
		}
		if err := os.WriteFile(filepath.Join(wsPath, "catalog.yaml"), src, 0o644); err != nil {
			return graphsrv.WorkspaceRunResult{}, err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "id": id, "path": wsPath})
		return graphsrv.WorkspaceRunResult{Stdout: string(out)}, nil
	case "commit":
		return graphsrv.WorkspaceRunResult{}, nil
	case "merge":
		if f.mergeErr != "" {
			return graphsrv.WorkspaceRunResult{ExitCode: 1, Stderr: f.mergeErr}, nil
		}
		return graphsrv.WorkspaceRunResult{}, nil
	default:
		f.t.Fatalf("unexpected dev-workspace verb: %v", args)
		return graphsrv.WorkspaceRunResult{}, nil
	}
}

func (f *fakeWorkspaceRunner) verbs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c[0]
	}
	return out
}

func (f *fakeWorkspaceRunner) call(verb string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == verb {
			return c
		}
	}
	return nil
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestReadProjectGraphConfig_ValuesAndDegrades(t *testing.T) {
	dir := t.TempDir()

	// No .kitsoki at all → zero config (direct).
	if got := graphsrv.ReadProjectGraphConfigForTest(dir); got.WriteVia != "" {
		t.Fatalf("no profile: write_via = %q, want empty", got.WriteVia)
	}

	mustWriteFile(t, filepath.Join(dir, ".kitsoki", "project-profile.yaml"),
		"graph:\n  write_via: capsule\n  gate: \"make graph-check\"\n", 0o644)
	got := graphsrv.ReadProjectGraphConfigForTest(dir)
	if got.WriteVia != graphsrv.WriteViaCapsule || got.Gate != "make graph-check" {
		t.Fatalf("capsule profile: got %+v", got)
	}

	// An unknown write_via value degrades to unset, never an error.
	mustWriteFile(t, filepath.Join(dir, ".kitsoki", "project-profile.yaml"),
		"graph:\n  write_via: worktree\n", 0o644)
	if got := graphsrv.ReadProjectGraphConfigForTest(dir); got.WriteVia != "" {
		t.Fatalf("bogus write_via: got %q, want empty", got.WriteVia)
	}
}

func TestGraphServer_CapsuleWriteRoutesThroughWorkspace(t *testing.T) {
	repoRoot, catalogPath := capsuleFixture(t, "capsule")
	primaryBefore, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read primary catalog: %v", err)
	}

	runner := &fakeWorkspaceRunner{t: t, repoRoot: repoRoot, wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		Actor:           "alice",
		WorkspaceRunner: runner,
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if isErr {
		t.Fatalf("graph.propose (capsule) returned an error: %+v", m)
	}
	changesetID, _ := m["changeset_id"].(string)
	if changesetID == "" {
		t.Fatalf("no changeset_id in %+v", m)
	}

	// The primary catalog must be byte-identical: the write landed in the
	// workspace only.
	primaryAfter, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("re-read primary catalog: %v", err)
	}
	if string(primaryBefore) != string(primaryAfter) {
		t.Fatalf("capsule-routed write mutated the primary catalog")
	}

	// The full lifecycle ran: create, then commit, then merge.
	if verbs := runner.verbs(); strings.Join(verbs, ",") != "create,commit,merge" {
		t.Fatalf("dev-workspace verbs = %v, want [create commit merge]", verbs)
	}
	if commit := runner.call("commit"); !strings.Contains(strings.Join(commit, " "), changesetID) {
		t.Fatalf("commit call does not name the changeset: %v", commit)
	}
	if merge := runner.call("merge"); argValue(merge, "--gate") != "git diff --check" {
		t.Fatalf("merge gate = %q, want the default hygiene gate", argValue(merge, "--gate"))
	}
	if merge := runner.call("merge"); strings.Contains(strings.Join(merge, " "), "--teardown") {
		t.Fatalf("merge must keep the workspace alive (no --teardown): %v", merge)
	}

	// The workspace copy carries the proposed changeset...
	wsCatalog := filepath.Join(runner.wsRoot, argValue(runner.call("create"), "--id"), "catalog.yaml")
	wsData, err := os.ReadFile(wsCatalog)
	if err != nil {
		t.Fatalf("read workspace catalog: %v", err)
	}
	if !strings.Contains(string(wsData), changesetID) {
		t.Fatalf("workspace catalog does not contain %s", changesetID)
	}

	// ...and reads now route to the workspace: the new changeset is visible
	// even though the primary catalog never changed.
	lm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "get", "id": changesetID})
	if isErr {
		t.Fatalf("graph.changeset get after capsule propose: %+v", lm)
	}

	// Receipts stay anchored at the PRIMARY repo root, never the workspace.
	if _, err := os.Stat(filepath.Join(repoRoot, ".artifacts", "graph-mcp", "receipts.jsonl")); err != nil {
		t.Fatalf("receipts journal not anchored at primary repo root: %v", err)
	}
}

func TestGraphServer_CapsuleValidateOnlySkipsIntegration(t *testing.T) {
	_, catalogPath := capsuleFixture(t, "capsule")
	runner := &fakeWorkspaceRunner{t: t, repoRoot: filepath.Dir(catalogPath), wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WorkspaceRunner: runner,
	})
	defer done()

	args := proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})
	args["validate_only"] = true
	if m, isErr := callTool(t, cs, "graph.propose", args); isErr {
		t.Fatalf("graph.propose validate_only (capsule): %+v", m)
	}
	// validate_only writes nothing, so nothing may be committed or merged;
	// the workspace itself is still materialized (validation runs there).
	if verbs := runner.verbs(); strings.Join(verbs, ",") != "create" {
		t.Fatalf("dev-workspace verbs = %v, want [create] only", verbs)
	}
}

func TestGraphServer_WriteViaFlagOverridesProjectConfig(t *testing.T) {
	_, catalogPath := capsuleFixture(t, "capsule")
	runner := &fakeWorkspaceRunner{t: t, repoRoot: filepath.Dir(catalogPath), wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WriteVia:        graphsrv.WriteViaDirect,
		WorkspaceRunner: runner,
	})
	defer done()

	if m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})); isErr {
		t.Fatalf("graph.propose (--write-via direct override): %+v", m)
	}
	if len(runner.verbs()) != 0 {
		t.Fatalf("direct override must never touch dev-workspace.sh, got %v", runner.verbs())
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if !strings.Contains(string(data), "flip req-beta") {
		t.Fatalf("direct override did not write to the primary catalog")
	}
}

func TestGraphServer_NoProfileDefaultsToDirect(t *testing.T) {
	_, catalogPath := capsuleFixture(t, "") // git repo, script present, NO .kitsoki
	runner := &fakeWorkspaceRunner{t: t, repoRoot: filepath.Dir(catalogPath), wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WorkspaceRunner: runner,
	})
	defer done()

	if m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})); isErr {
		t.Fatalf("graph.propose (no profile): %+v", m)
	}
	if len(runner.verbs()) != 0 {
		t.Fatalf("no .kitsoki profile must mean direct working-dir edits, got %v", runner.verbs())
	}
}

func TestGraphServer_ProtectedCatalogAutoRoutesThroughCapsule(t *testing.T) {
	repoRoot, catalogPath := capsuleFixture(t, "")
	if err := os.Chmod(catalogPath, 0o444); err != nil {
		t.Fatalf("protect catalog: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(catalogPath, 0o644) })
	runner := &fakeWorkspaceRunner{t: t, repoRoot: repoRoot, wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WorkspaceRunner: runner,
	})
	defer done()

	if m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})); isErr {
		t.Fatalf("graph.propose (protected auto route): %+v", m)
	}
	if verbs := runner.verbs(); strings.Join(verbs, ",") != "create,commit,merge" {
		t.Fatalf("dev-workspace verbs = %v, want [create commit merge]", verbs)
	}
}

func TestGraphServer_ProtectedCatalogDirectOverrideStaysDirect(t *testing.T) {
	_, catalogPath := capsuleFixture(t, "")
	if err := os.Chmod(catalogPath, 0o444); err != nil {
		t.Fatalf("protect catalog: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(catalogPath, 0o644) })
	runner := &fakeWorkspaceRunner{t: t, repoRoot: filepath.Dir(catalogPath), wsRoot: t.TempDir()}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WriteVia:        graphsrv.WriteViaDirect,
		WorkspaceRunner: runner,
	})
	defer done()

	if _, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})); !isErr {
		t.Fatal("graph.propose (--write-via direct) unexpectedly bypassed the protected catalog")
	}
	if verbs := runner.verbs(); len(verbs) != 0 {
		t.Fatalf("direct override must never touch dev-workspace.sh, got %v", verbs)
	}
}

func TestGraphServer_CapsuleWithoutScriptFails(t *testing.T) {
	repoRoot, catalogPath := capsuleFixture(t, "capsule")
	if err := os.Remove(filepath.Join(repoRoot, "scripts", "dev-workspace.sh")); err != nil {
		t.Fatalf("remove script: %v", err)
	}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WorkspaceRunner: &fakeWorkspaceRunner{t: t, repoRoot: repoRoot, wsRoot: t.TempDir()},
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if !isErr {
		t.Fatalf("expected CAPSULE_WORKFLOW error without the lifecycle script, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeCapsuleWorkflow {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeCapsuleWorkflow)
	}
}

func TestGraphServer_CapsuleMergeFailureNamesWorkspace(t *testing.T) {
	repoRoot, catalogPath := capsuleFixture(t, "capsule")
	runner := &fakeWorkspaceRunner{t: t, repoRoot: repoRoot, wsRoot: t.TempDir(), mergeErr: "merge: gate failed"}
	cs, done := connectGraphServer(t, graphsrv.Config{
		CatalogFlags:    []string{catalogPath},
		Mode:            graphsrv.ModePropose,
		WorkspaceRunner: runner,
	})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if !isErr {
		t.Fatalf("expected an error when the capsule merge fails, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeCapsuleWorkflow {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeCapsuleWorkflow)
	}
	hint, _ := m["hint"].(string)
	if !strings.Contains(hint, runner.wsRoot) {
		t.Fatalf("merge-failure hint must name the workspace so work isn't lost, got %q", hint)
	}
}

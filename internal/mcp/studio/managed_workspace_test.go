package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/capsuletest"
)

type workspaceFakeRunner struct {
	workspace string
	calls     [][]string
	failMerge bool
}

func (r *workspaceFakeRunner) Run(_ context.Context, command string, args ...string) (WorkspaceCommandResult, error) {
	r.calls = append(r.calls, append([]string{command}, args...))
	if len(args) > 0 && args[0] == "merge" && r.failMerge {
		r.failMerge = false
		return WorkspaceCommandResult{ExitCode: 1, Stderr: "target advanced; retry after rebase"}, nil
	}
	if len(args) > 0 && args[0] == "create" {
		return WorkspaceCommandResult{Stdout: `{"id":"` + filepath.Base(r.workspace) + `","path":"` + r.workspace + `","branch":"agent/test","base":"staging/local","target":"staging/local"}`}, nil
	}
	return WorkspaceCommandResult{Stdout: `{"ok":true}`}, nil
}

func workspaceObjective(t *testing.T, mutations int) *ObjectiveService {
	t.Helper()
	svc, err := NewObjectiveService(NewMemoryObjectiveStore(), func() time.Time { return time.Unix(1, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Open(context.Background(), OpenObjectiveInput{ID: "objective-1", Goal: "test managed workspace", Acceptance: []string{"all guarded mutations have receipts"}, Budget: Budget{MaxMutations: mutations}, Policy: PolicyProfile{Name: "strict", Strict: true}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestWorkspaceLifecycleUsesManagedScriptAndReturnsReceipts(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	runner := &workspaceFakeRunner{workspace: repo}
	svc, err := NewManagedWorkspaceService(filepath.Dir(repo), "/repo/scripts/dev-workspace.sh", runner, workspaceObjective(t, 8), func() time.Time { return time.Unix(2, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: "objective-1", ID: filepath.Base(repo), Branch: "agent/test", SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Receipt.Workspace.Path != repo || created.Receipt.Policy.PolicyHash == "" || created.Receipt.Before.Workspace.ID == "" {
		t.Fatalf("unexpected create receipt: %+v", created.Receipt)
	}
	if _, err := svc.Status(context.Background(), filepath.Base(repo)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commit(context.Background(), WorkspaceActionInput{ObjectiveID: "objective-1", WorkspaceID: filepath.Base(repo), Message: "Managed workspace change"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Merge(context.Background(), WorkspaceActionInput{ObjectiveID: "objective-1", WorkspaceID: filepath.Base(repo)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Status(context.Background(), filepath.Base(repo)); err == nil {
		t.Fatal("merged workspace must no longer be server-held")
	}

	joined := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		joined = append(joined, strings.Join(call, " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{"create --id " + filepath.Base(repo) + " --branch agent/test --bootstrap --json --session-id session-1", "status " + repo + " --json", "commit " + repo + " --message Managed workspace change", "merge " + repo + " --teardown"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing script delegation %q in:\n%s", want, all)
		}
	}
}

func TestWorkspaceMergeRetryRetainsIdentityAfterAdvancedTarget(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	runner := &workspaceFakeRunner{workspace: repo, failMerge: true}
	svc, err := NewManagedWorkspaceService(filepath.Dir(repo), "dev-workspace.sh", runner, workspaceObjective(t, 5), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: "objective-1", ID: filepath.Base(repo), Branch: "agent/test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Merge(context.Background(), WorkspaceActionInput{ObjectiveID: "objective-1", WorkspaceID: filepath.Base(repo)}); err == nil || !strings.Contains(err.Error(), "target advanced") {
		t.Fatalf("expected truthful advanced-target failure, got %v", err)
	}
	if _, err := svc.Status(context.Background(), filepath.Base(repo)); err != nil {
		t.Fatalf("retry must retain identity: %v", err)
	}
	if _, err := svc.Merge(context.Background(), WorkspaceActionInput{ObjectiveID: "objective-1", WorkspaceID: filepath.Base(repo)}); err != nil {
		t.Fatalf("retry merge: %v", err)
	}
}

func TestWorkspaceRejectsForeignCreateResponse(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	runner := &workspaceFakeRunner{workspace: repo}
	svc, err := NewManagedWorkspaceService(filepath.Dir(repo), "dev-workspace.sh", runner, workspaceObjective(t, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: "objective-1", ID: "not-" + filepath.Base(repo), Branch: "agent/test"})
	if err == nil || !strings.Contains(err.Error(), "different workspace id") {
		t.Fatalf("expected identity rejection, got %v", err)
	}
}

func TestWorkspaceProductionRunnerRequiresTrustedLifecycleScript(t *testing.T) {
	_, err := NewManagedWorkspaceService(filepath.Join(t.TempDir(), ".capsules", "workspaces"), "/tmp/not-dev-workspace.sh", nil, workspaceObjective(t, 1), nil)
	if err == nil || !strings.Contains(err.Error(), "checked-in scripts/dev-workspace.sh") {
		t.Fatalf("production runner accepted an untrusted script: %v", err)
	}
}

type workspaceArgsRunner struct {
	args []string
}

func (r *workspaceArgsRunner) Run(_ context.Context, _ string, args ...string) (WorkspaceCommandResult, error) {
	r.args = append([]string(nil), args...)
	return WorkspaceCommandResult{}, nil
}

func TestWorkspaceProductionCommandsPinTrustedRepoAndRoot(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".capsules", "workspaces")
	script := filepath.Join(repo, "scripts", "dev-workspace.sh")
	makeWorkspaceTestDir(t, root)
	makeWorkspaceTestDir(t, filepath.Dir(script))
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc, err := NewManagedWorkspaceService(root, script, nil, workspaceObjective(t, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &workspaceArgsRunner{}
	svc.runner = runner
	if _, err := svc.runWorkspaceCommand(context.Background(), "status", "demo", "--json"); err != nil {
		t.Fatal(err)
	}
	want := []string{"status", "demo", "--json", "--repo", repo, "--root", root}
	if strings.Join(runner.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("workspace args = %#v, want %#v", runner.args, want)
	}
}

func makeWorkspaceTestDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceToolsRegisterOnlyWhenExplicitlyRequested(t *testing.T) {
	ctx := context.Background()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "workspace-contract-test", Version: "v1"}, nil)
	service, err := NewManagedWorkspaceService(t.TempDir(), "dev-workspace.sh", &workspaceFakeRunner{}, workspaceObjective(t, 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewFSGuard(service)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManagedWorkspaceTools(server, service, guard)
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"workspace.create", "workspace.status", "workspace.commit", "workspace.merge", "workspace.teardown", "workspace.read", "workspace.write", "workspace.patch"} {
		if !names[name] {
			t.Fatalf("explicit registration missing %s", name)
		}
	}
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "workspace.create", Arguments: map[string]any{"objective_id": "objective-1", "id": "safe", "branch": "agent/safe"}})
	if err != nil || !result.IsError {
		t.Fatalf("fake outside-root create must fail safely: %#v, %v", result, err)
	}
	var payload ToolError
	if err := json.Unmarshal([]byte(textResult(result)), &payload); err != nil || payload.Code != ErrBadRequest {
		t.Fatalf("typed tool error: %#v, %v", payload, err)
	}

	defaultServer := NewServer(NewStudioSession(nil))
	defaultTransport, defaultClientTransport := mcpsdk.NewInMemoryTransports()
	if _, err := defaultServer.Connect(ctx, defaultTransport, nil); err != nil {
		t.Fatal(err)
	}
	defaultSession, err := client.Connect(ctx, defaultClientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = defaultSession.Close() })
	defaultTools, err := defaultSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range defaultTools.Tools {
		if strings.HasPrefix(tool.Name, "workspace.") {
			t.Fatalf("workspace tool %s must await final integration", tool.Name)
		}
	}
}

func TestFSGuardReadSearchWritePatchAndRefuseSymlink(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	runner := &workspaceFakeRunner{workspace: repo}
	svc, err := NewManagedWorkspaceService(filepath.Dir(repo), "dev-workspace.sh", runner, workspaceObjective(t, 8), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := filepath.Base(repo)
	if _, err := svc.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: "objective-1", ID: id, Branch: "agent/test"}); err != nil {
		t.Fatal(err)
	}
	guard, err := NewFSGuard(svc)
	if err != nil {
		t.Fatal(err)
	}

	read, err := guard.Read(context.Background(), FSReadInput{WorkspaceID: id, Path: "a.txt"})
	if err != nil || read.Content != "a\n" {
		t.Fatalf("read: %#v, %v", read, err)
	}
	listed, err := guard.List(context.Background(), FSListInput{WorkspaceID: id})
	if err != nil || len(listed.Entries) == 0 {
		t.Fatalf("list: %#v, %v", listed, err)
	}
	search, err := guard.Search(context.Background(), FSSearchInput{WorkspaceID: id, Query: "a"})
	if err != nil || len(search.Matches) == 0 {
		t.Fatalf("search: %#v, %v", search, err)
	}
	write, err := guard.Write(context.Background(), FSWriteInput{ObjectiveID: "objective-1", WorkspaceID: id, Path: "a.txt", Content: "first\n", PreimageSHA256: read.SHA256})
	if err != nil || write.Receipt.BeforeSHA256 != read.SHA256 || write.Receipt.AfterSHA256 == read.SHA256 {
		t.Fatalf("write: %#v, %v", write, err)
	}
	if _, err := guard.Patch(context.Background(), FSPatchInput{ObjectiveID: "objective-1", WorkspaceID: id, Path: "a.txt", Replacement: "second\n", PreimageSHA256: write.Receipt.BeforeSHA256}); !errors.Is(err, ErrFSPreimage) {
		t.Fatalf("stale patch must fail preimage check, got %v", err)
	}
	patched, err := guard.Patch(context.Background(), FSPatchInput{ObjectiveID: "objective-1", WorkspaceID: id, Path: "a.txt", Replacement: "second\n", PreimageSHA256: write.Receipt.AfterSHA256})
	if err != nil || patched.Receipt.ObjectiveReceipt.Type != ReceiptMutationAuthorized {
		t.Fatalf("patch: %#v, %v", patched, err)
	}
	if err := os.Symlink("/tmp", filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	_, err = guard.Read(context.Background(), FSReadInput{WorkspaceID: id, Path: "escape/nope"})
	if !errors.Is(err, ErrFSSymlink) {
		t.Fatalf("symlink traversal must be refused, got %v", err)
	}
}

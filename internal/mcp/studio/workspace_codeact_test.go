package studio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/capsuletest"
	starlarkhost "kitsoki/internal/host/starlark"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func workspaceCodeactCaps(t *testing.T, raw map[string]any) starlarkhost.CapabilitySpec {
	t.Helper()
	caps, err := starlarkhost.ParseCapabilities(raw)
	if err != nil {
		t.Fatal(err)
	}
	return caps
}

func workspaceCodeactFixture(t *testing.T) (*WorkspaceCodeactService, *workspaceFakeRunner, string) {
	t.Helper()
	repo := capsuletest.Open(t, "clean-repo")
	runner := &workspaceFakeRunner{workspace: repo}
	workspaces, err := NewManagedWorkspaceService(filepath.Dir(repo), "dev-workspace.sh", runner, workspaceObjective(t, 8), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := filepath.Base(repo)
	if _, err := workspaces.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: "objective-1", ID: id, Branch: "agent/codeact"}); err != nil {
		t.Fatal(err)
	}
	guard, err := NewFSGuard(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWorkspaceCodeactService(workspaces, guard, workspaceCodeactCaps(t, map[string]any{
		"fs": map[string]any{"read": []any{"**"}, "write": []any{"**"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return service, runner, id
}

func TestWorkspaceCodeactReplayFixtureEditsMultipleFilesWithoutHostOrShell(t *testing.T) {
	service, runner, id := workspaceCodeactFixture(t)
	requested := map[string]any{"fs": map[string]any{
		"read":  []any{"a.txt", "b.txt"},
		"write": []any{"a.txt", "b.txt"},
	}}
	out, err := service.Evaluate(context.Background(), WorkspaceCodeactInput{
		ObjectiveID: "objective-1", WorkspaceID: id, RequestedCapabilities: requested,
		Snippet: `def main(ctx):
    original = ctx.fs.read("a.txt")
    ctx.fs.write("a.txt", "first changed\n")
    if not ctx.fs.exists("b.txt"):
        ctx.fs.write("b.txt", "second changed\n")
    return {"original": original, "edited": ["a.txt", "b.txt"]}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.MutationSummary.Count != 2 || out.InspectionSummary.Count != 4 {
		t.Fatalf("unexpected CodeAct result: %#v", out)
	}
	if out.RequestedCapabilityHash == "" || out.EffectiveCapabilityHash == "" || out.RequestedCapabilityHash != out.EffectiveCapabilityHash {
		t.Fatalf("missing effective capability receipt: %#v", out)
	}
	if len(out.Receipt.MutationReceipts) != 2 || out.Receipt.Workspace.ID != id || out.Receipt.Policy.PolicyHash == "" {
		t.Fatalf("missing guarded mutation receipts: %#v", out.Receipt)
	}
	workspace := out.Receipt.Workspace.Path
	for path, want := range map[string]string{"a.txt": "first changed\n", "b.txt": "second changed\n"} {
		got, err := os.ReadFile(filepath.Join(workspace, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, call := range runner.calls {
		if len(call) > 1 && (strings.Contains(strings.Join(call, " "), "host.run") || strings.Contains(strings.Join(call, " "), "sh -c")) {
			t.Fatalf("CodeAct must not reach host.run or a shell: %q", call)
		}
	}
}

func TestWorkspaceCodeactDeniesCapabilityEscalationAndPathEscape(t *testing.T) {
	service, _, id := workspaceCodeactFixture(t)
	_, err := service.Evaluate(context.Background(), WorkspaceCodeactInput{
		ObjectiveID: "objective-1", WorkspaceID: id, Snippet: `def main(ctx): return {}`,
		RequestedCapabilities: map[string]any{"host": map[string]any{"verbs": []any{"host.graph.load"}}},
	})
	var denied *WorkspaceCodeactError
	if !errors.As(err, &denied) || denied.Code != workspaceCodeactCapabilityDenied || denied.RequestedCapabilityHash == "" || denied.EffectiveCapabilityHash == "" {
		t.Fatalf("expected structured capability denial, got %#v", err)
	}

	_, err = service.Evaluate(context.Background(), WorkspaceCodeactInput{
		ObjectiveID: "objective-1", WorkspaceID: id,
		RequestedCapabilities: map[string]any{"fs": map[string]any{"read": []any{"**"}}},
		Snippet: `def main(ctx):
    return {"secret": ctx.fs.read("../secret")}
`,
	})
	if !errors.As(err, &denied) || denied.Code != workspaceCodeactPathDenied {
		t.Fatalf("expected structured path denial, got %#v", err)
	}
}

func TestWorkspaceCodeactRegistrationIsExplicitAndStructured(t *testing.T) {
	service, _, _ := workspaceCodeactFixture(t)
	ctx := context.Background()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "workspace-codeact-test", Version: "v1"}, nil)
	RegisterWorkspaceCodeactTool(server, service)
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
	if !toolNamed(tools.Tools, "workspace.codeact") {
		t.Fatal("explicit registration omitted workspace.codeact")
	}
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "workspace.codeact", Arguments: map[string]any{
		"objective_id": "objective-1", "workspace_id": "unknown", "snippet": "def main(ctx): return {}",
	}})
	if err != nil || !result.IsError || !strings.Contains(textResult(result), workspaceCodeactWorkspaceDenied) {
		t.Fatalf("expected structured workspace denial: %#v, %v", result, err)
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
	if toolNamed(defaultTools.Tools, "workspace.codeact") {
		t.Fatal("workspace.codeact must await final integration")
	}
}

func toolNamed(tools []*mcpsdk.Tool, want string) bool {
	for _, tool := range tools {
		if tool.Name == want {
			return true
		}
	}
	return false
}

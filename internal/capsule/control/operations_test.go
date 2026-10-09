package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsRequireFreshHandleAndDeclaredCommand(t *testing.T) {
	root := t.TempDir()
	provider := &provider{name: "synthetic"}
	manager := &Manager{Definitions: defs{"d": {ID: "d", Schema: DefinitionSchema, Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"}, Digest: "sha256:d", Policy: Policy{Commands: map[string]Command{"echo": {Argv: []string{"echo", "ok"}}}}}}, Instances: NewMemoryInstanceStore(), Providers: map[string]WorkspaceProvider{"synthetic": provider}, Grant: ScopeGrant{ProjectRoot: root, WorkspaceRoots: []string{filepath.Join(root, "capsules")}, Definitions: []string{"d"}, Executors: []string{"synthetic"}, Effects: []string{"exec"}}}
	ctx := context.Background()
	h, err := manager.Create(ctx, CreateRequest{ID: "x", DefinitionID: "d", Owner: "a"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake provider only reports a path; create it to exercise operations.
	path, err := manager.WorkspacePath(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	next, err := manager.WriteFile(ctx, h, "file.txt", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReadFile(ctx, h, "file.txt"); err == nil {
		t.Fatal("stale read succeeded")
	}
	raw, err := manager.ReadFile(ctx, next, "file.txt")
	if err != nil || string(raw) != "hello" {
		t.Fatalf("read=%q %v", raw, err)
	}
	result, err := manager.RunCommand(ctx, next, "echo", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Output != "ok\n" {
		t.Fatalf("run=%#v", result)
	}
	if _, err := manager.RunCommand(ctx, result.Handle, "", []string{"echo", "no"}, 0); err == nil {
		t.Fatal("raw argv succeeded")
	}
}

func TestRunCommandBoundsCapturedOutput(t *testing.T) {
	root := t.TempDir()
	provider := &provider{name: "synthetic"}
	manager := &Manager{
		Definitions: defs{"d": {
			ID:     "d",
			Schema: DefinitionSchema,
			Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"},
			Digest: "sha256:d",
			Policy: Policy{Commands: map[string]Command{
				"noisy": {Argv: []string{"sh", "-c", "yes x | head -c 1100000"}},
			}},
		}},
		Instances: NewMemoryInstanceStore(),
		Providers: map[string]WorkspaceProvider{"synthetic": provider},
		Grant: ScopeGrant{
			ProjectRoot:    root,
			WorkspaceRoots: []string{filepath.Join(root, "capsules")},
			Definitions:    []string{"d"},
			Executors:      []string{"synthetic"},
			Effects:        []string{"exec"},
		},
	}
	h, err := manager.Create(context.Background(), CreateRequest{ID: "bounded", DefinitionID: "d", Owner: "a"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.WorkspacePath(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RunCommand(context.Background(), h, "noisy", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !result.OutputTruncated || len(result.Output) != commandMaxOutputBytes {
		t.Fatalf("result exit=%d truncated=%t output=%d", result.ExitCode, result.OutputTruncated, len(result.Output))
	}
}

func TestCommitVCSAddsDCOSignoffAndRefreshesInstanceHead(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	workspace := filepath.Join(workspaceRoot, "signed")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runControlGit(t, workspace, "init")
	runControlGit(t, workspace, "config", "user.name", "Capsule Test")
	runControlGit(t, workspace, "config", "user.email", "capsule@example.test")
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runControlGit(t, workspace, "add", "tracked.txt")
	runControlGit(t, workspace, "commit", "--signoff", "-m", "baseline")
	baseline := controlGitOutput(t, workspace, "rev-parse", "HEAD")

	store := NewMemoryInstanceStore()
	in, err := store.Create(context.Background(), Instance{
		ID:           "signed",
		DefinitionID: "d",
		Provider:     "synthetic",
		Path:         workspace,
		Head:         baseline,
		State:        StateDirty,
		Lease:        Lease{Owner: "agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		Definitions: defs{"d": {ID: "d"}},
		Instances:   store,
		Grant: ScopeGrant{
			ProjectRoot:    root,
			WorkspaceRoots: []string{workspaceRoot},
			Effects:        []string{"vcs_commit"},
		},
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handle, err := manager.CommitVCS(context.Background(), Handle{ID: in.ID, Generation: in.Generation}, "managed commit")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := store.Get(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantHead := controlGitOutput(t, workspace, "rev-parse", "HEAD")
	if committed.Head != wantHead || committed.Head == baseline || committed.Generation != handle.Generation {
		t.Fatalf("committed instance=%#v baseline=%q want head=%q handle=%#v", committed, baseline, wantHead, handle)
	}
	message := controlGitOutput(t, workspace, "log", "-1", "--format=%B")
	if !strings.Contains(message, "Signed-off-by: Capsule Test <capsule@example.test>") {
		t.Fatalf("commit is missing DCO signoff:\n%s", message)
	}
}

func controlGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSearchFilesConfinesSkipsAndBoundsAgentVisibleContent(t *testing.T) {
	root := t.TempDir()
	provider := &provider{name: "synthetic"}
	manager := &Manager{
		Definitions: defs{"d": {ID: "d", Schema: DefinitionSchema, Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"}, Digest: "sha256:d"}},
		Instances:   NewMemoryInstanceStore(),
		Providers:   map[string]WorkspaceProvider{"synthetic": provider},
		Grant:       ScopeGrant{Owner: "agent", ProjectRoot: root, WorkspaceRoots: []string{filepath.Join(root, "capsules")}, Definitions: []string{"d"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage", "fs_write"}},
	}
	ctx := context.Background()
	h, err := manager.Create(ctx, CreateRequest{ID: "search", DefinitionID: "d", Owner: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := manager.WorkspacePath(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	write := func(relative string, contents []byte) {
		t.Helper()
		path := filepath.Join(workspace, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("safe.txt", []byte("visible needle\n"))
	write("long.txt", []byte(strings.Repeat("x", searchMaxLineBytes+100)+"needle\n"))
	write(".kitsoki-verifier/secret.txt", []byte("verifier needle\n"))
	write(".git/config", []byte("git needle\n"))
	write("node_modules/dependency.txt", []byte("dependency needle\n"))
	write("binary.dat", []byte("binary needle\x00payload"))
	write("oversized.txt", append([]byte("needle"), make([]byte, writeMaxFileBytes)...))
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("escaped needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "outside-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, ".kitsoki-verifier", "secret.txt"), filepath.Join(workspace, "verifier-link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{".git/config", ".kitsoki-verifier/secret.txt", "verifier-link.txt", "outside-link.txt"} {
		if _, err := manager.ReadFile(ctx, h, hidden); err == nil {
			t.Fatalf("agent-hidden read %q succeeded", hidden)
		}
	}
	for _, hidden := range []string{".git/config", ".kitsoki-verifier/secret.txt", "verifier-link.txt", "outside-link.txt"} {
		if _, err := manager.WriteFile(ctx, h, hidden, []byte("overwrite")); err == nil {
			t.Fatalf("agent-hidden write %q succeeded", hidden)
		}
	}
	if _, err := manager.ListFiles(ctx, h, ".kitsoki-verifier"); err == nil {
		t.Fatal("agent-hidden verifier listing succeeded")
	}
	if _, err := manager.ReadFile(ctx, h, "binary.dat"); err == nil {
		t.Fatal("binary read succeeded")
	}
	if _, err := manager.ReadFile(ctx, h, "oversized.txt"); err == nil {
		t.Fatal("oversized read succeeded")
	}
	if _, err := manager.WriteFile(ctx, h, "too-large.txt", make([]byte, writeMaxFileBytes+1)); err == nil {
		t.Fatal("oversized write succeeded")
	}

	result, err := manager.SearchFiles(ctx, h, "needle", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("matches = %#v, want only safe and long text", result.Matches)
	}
	paths := map[string]bool{}
	for _, match := range result.Matches {
		paths[match.Path] = true
		if len(match.Text) > searchMaxLineBytes {
			t.Fatalf("unbounded match line length = %d", len(match.Text))
		}
	}
	if !paths["safe.txt"] || !paths["long.txt"] {
		t.Fatalf("visible matches = %#v", result.Matches)
	}
	if result.SkippedFiles < 3 {
		t.Fatalf("skipped files = %d, want binary, oversized, and symlink", result.SkippedFiles)
	}

	entries, err := manager.ListFiles(ctx, h, "")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]FileEntry{}
	for _, entry := range entries {
		listed[entry.Path] = entry
	}
	if _, ok := listed[".git"]; ok {
		t.Fatal("fs.list exposed .git")
	}
	if _, ok := listed[".kitsoki-verifier"]; ok {
		t.Fatal("fs.list exposed verifier directory")
	}
	if !listed["outside-link.txt"].Symlink {
		t.Fatalf("symlink entry not identified: %#v", listed["outside-link.txt"])
	}
	nested, err := manager.ListFiles(ctx, h, "node_modules")
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 1 || nested[0].Path != "node_modules/dependency.txt" {
		t.Fatalf("nested paths are not workspace-relative: %#v", nested)
	}

	limited, err := manager.SearchFiles(ctx, h, "needle", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Matches) != 1 || !limited.Truncated {
		t.Fatalf("limited search = %#v", limited)
	}
}

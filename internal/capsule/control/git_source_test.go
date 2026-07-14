package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	legacy "kitsoki/internal/capsule"
	"kitsoki/internal/capsuletest"
)

func TestGitSourceProviderClonesSelfAndPinnedCapsuleCommit(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	head, err := runGit(context.Background(), project, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	provider := GitSourceProvider{ProjectRoot: project}
	self, err := provider.Create(context.Background(), Definition{ID: "self", Source: Source{Kind: SourceSelf}}, Instance{ID: "self", Path: filepath.Join(t.TempDir(), "self")})
	if err != nil {
		t.Fatal(err)
	}
	if self.Head != head[:len(head)-1] {
		t.Fatalf("self head=%q want=%q", self.Head, head)
	}
	if err := provider.Close(context.Background(), Instance{Path: self.Path}); err != nil {
		t.Fatal(err)
	}
	pinned, err := provider.Create(context.Background(), Definition{ID: "pinned", Source: Source{Kind: SourcePinned, Ref: project, Commit: selfHead(head)}}, Instance{ID: "pinned", Path: filepath.Join(t.TempDir(), "pinned")})
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Head != selfHead(head) {
		t.Fatalf("pinned=%q want=%q", pinned.Head, head)
	}
	if _, err := os.Stat(filepath.Join(pinned.Path, instanceSentinel)); err != nil {
		t.Fatal(err)
	}
	manifest, err := legacy.ReadManifest(pinned.Path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CapsuleName != "pinned" || manifest.Workspace != pinned.Path || manifest.Source.Repo != project || manifest.Source.Commit != selfHead(head) || manifest.Source.Head != selfHead(head) {
		t.Fatalf("manifest=%#v", manifest)
	}
	if err := os.WriteFile(filepath.Join(pinned.Path, instancePinSentinel), []byte("debugging\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := strings.TrimSpace(gitOut(t, pinned.Path, "status", "--porcelain")); status != "" {
		t.Fatalf("Capsule metadata dirtied pinned workspace: %q", status)
	}
}

func TestGitSourceProviderPinnedUsesContentAddressedCache(t *testing.T) {
	source := initGitProject(t)
	commit := strings.TrimSpace(gitOut(t, source, "rev-parse", "HEAD"))
	cache := filepath.Join(t.TempDir(), "git-cache")
	provider := GitSourceProvider{ProjectRoot: source, CacheRoot: cache}

	first, err := provider.Create(context.Background(), Definition{ID: "pinned", Source: Source{Kind: SourcePinned, Ref: source, Commit: commit}}, Instance{ID: "one", Path: filepath.Join(t.TempDir(), "one")})
	if err != nil {
		t.Fatal(err)
	}
	if first.Head != commit {
		t.Fatalf("first head=%q want=%q", first.Head, commit)
	}
	if _, err := os.Stat(filepath.Join(cache, commit+".git")); err != nil {
		t.Fatalf("cache was not populated: %v", err)
	}

	removed := source + ".removed"
	if err := os.Rename(source, removed); err != nil {
		t.Fatal(err)
	}
	second, err := provider.Create(context.Background(), Definition{ID: "pinned", Source: Source{Kind: SourcePinned, Ref: source, Commit: commit}}, Instance{ID: "two", Path: filepath.Join(t.TempDir(), "two")})
	if err != nil {
		t.Fatalf("second create should use cache without touching original source: %v", err)
	}
	if second.Head != commit {
		t.Fatalf("second head=%q want=%q", second.Head, commit)
	}
}

func TestGitSourceProviderMaterializesWorkspaceOverlayAndHidesVerifierOverlay(t *testing.T) {
	project := initGitProject(t)
	commit := strings.TrimSpace(gitOut(t, project, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(project, "workspace-overlay", "config.txt"), "workspace visible\n")
	writeFile(t, filepath.Join(project, "hidden-oracle", "answer.txt"), "verifier only\n")

	def := Definition{
		ID:     "pinned",
		Schema: DefinitionSchema,
		Source: Source{Kind: SourcePinned, Ref: project, Commit: commit},
		Digest: "sha256:def",
		Overlays: []Overlay{
			{Path: "workspace-overlay", Visibility: "workspace"},
			{Path: "hidden-oracle", Visibility: "verifier"},
		},
	}
	provider := GitSourceProvider{ProjectRoot: project, CacheRoot: filepath.Join(t.TempDir(), "cache")}
	mat, err := provider.Create(context.Background(), def, Instance{ID: "with-overlays", Path: filepath.Join(t.TempDir(), "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(mat.Path, "workspace-overlay", "config.txt")); got != "workspace visible\n" {
		t.Fatalf("workspace overlay content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(mat.Path, "hidden-oracle")); !os.IsNotExist(err) {
		t.Fatalf("verifier overlay leaked into workspace: %v", err)
	}
	if len(mat.VerifierOverlays) != 1 || mat.VerifierOverlays[0].Path != "hidden-oracle" || !strings.HasPrefix(mat.VerifierOverlays[0].Digest, "sha256:") {
		t.Fatalf("verifier overlay refs = %#v", mat.VerifierOverlays)
	}

	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &Manager{
		Definitions: defs{"pinned": def},
		Instances:   NewMemoryInstanceStore(),
		Providers:   map[string]WorkspaceProvider{"git": provider},
		Grant:       ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"pinned"}, Executors: []string{"git"}},
	}
	handle, err := manager.Create(context.Background(), CreateRequest{ID: "managed", DefinitionID: "pinned", Owner: "test", Provider: "git"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReadFile(context.Background(), handle, "hidden-oracle/answer.txt"); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("agent-facing read reached verifier overlay: %v", err)
	}
	paths, err := manager.VerifierOverlayPaths(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("verifier paths = %#v", paths)
	}
	wantVerifierPath, err := filepath.EvalSymlinks(filepath.Join(project, "hidden-oracle"))
	if err != nil {
		t.Fatal(err)
	}
	gotVerifierPath, err := filepath.EvalSymlinks(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if gotVerifierPath != wantVerifierPath {
		t.Fatalf("verifier paths = %#v", paths)
	}
}

func selfHead(v string) string {
	for len(v) > 0 && (v[len(v)-1] == '\n' || v[len(v)-1] == '\r') {
		v = v[:len(v)-1]
	}
	return v
}

func initGitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.invalid")
	gitRun(t, dir, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	gitRun(t, dir, "add", "README.md")
	gitRun(t, dir, "commit", "-m", "init")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := runGit(context.Background(), dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

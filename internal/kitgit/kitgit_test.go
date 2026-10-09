package kitgit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSource(t *testing.T) {
	cases := []struct {
		src      string
		wantURL  string
		wantRef  string
		wantOK   bool
	}{
		{"git+https://github.com/org/repo@v1.2.3", "https://github.com/org/repo", "v1.2.3", true},
		{"git+https://github.com/org/repo@main", "https://github.com/org/repo", "main", true},
		{"git+https://github.com/org/repo@" + strings.Repeat("a", 40), "https://github.com/org/repo", strings.Repeat("a", 40), true},
		{"@kitsoki/dev-story", "", "", false},
		{"git+https://github.com/org/repo", "", "", false}, // no @ref
		{"./relative/path", "", "", false},
	}
	for _, c := range cases {
		url, ref, ok := ParseSource(c.src)
		if ok != c.wantOK || url != c.wantURL || ref != c.wantRef {
			t.Errorf("ParseSource(%q) = (%q, %q, %v), want (%q, %q, %v)", c.src, url, ref, ok, c.wantURL, c.wantRef, c.wantOK)
		}
	}
}

// requireGit skips the test when git isn't on PATH (mirrors the pattern used
// by internal/host's own git-backed tests).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// newLocalRemote creates a local git repo with one commit and a tag "v1.0.0",
// returning its absolute path (usable directly as a `git clone` URL).
func newLocalRemote(t *testing.T) (repoPath string, commit string) {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("app:\n  id: fixture-kit\n  version: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "app.yaml")
	run("commit", "-q", "-m", "initial")
	run("tag", "v1.0.0")
	commit = run("rev-parse", "HEAD")
	return dir, commit
}

func TestMaterializeByTag(t *testing.T) {
	requireGit(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	remote, wantCommit := newLocalRemote(t)

	res, err := Materialize(context.Background(), DefaultRunner, remote, "v1.0.0")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.Commit != wantCommit {
		t.Errorf("Commit = %q, want %q", res.Commit, wantCommit)
	}
	if res.TreeHash == "" {
		t.Error("TreeHash is empty")
	}
	if _, err := os.Stat(filepath.Join(res.Root, "app.yaml")); err != nil {
		t.Errorf("materialized root missing app.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Root, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should have been stripped from the materialized root, stat err = %v", err)
	}
}

func TestMaterializeByCommitIsOfflineOnCacheHit(t *testing.T) {
	requireGit(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	remote, commit := newLocalRemote(t)

	// First call: real fetch by commit SHA.
	res1, err := Materialize(context.Background(), DefaultRunner, remote, commit)
	if err != nil {
		t.Fatalf("Materialize (populate cache): %v", err)
	}

	// Second call: same commit, but a runner that errors on every invocation
	// — proves the cache short-circuit never shells out.
	failRunner := RunnerFunc(func(_ context.Context, _ string, _ string, args ...string) (string, string, error) {
		t.Fatalf("unexpected git invocation on cache hit: %v", args)
		return "", "", nil
	})
	res2, err := Materialize(context.Background(), failRunner, "http://unreachable.invalid/repo", commit)
	if err != nil {
		t.Fatalf("Materialize (cache hit): %v", err)
	}
	if res2.Root != res1.Root || res2.TreeHash != res1.TreeHash {
		t.Errorf("cache hit result = %+v, want %+v", res2, res1)
	}
}

func TestDirTreeHashDeterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := DirTreeHash(dir)
	if err != nil {
		t.Fatalf("DirTreeHash: %v", err)
	}
	h2, err := DirTreeHash(dir)
	if err != nil {
		t.Fatalf("DirTreeHash (2nd): %v", err)
	}
	if h1 != h2 {
		t.Errorf("DirTreeHash not deterministic: %q != %q", h1, h2)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, err := DirTreeHash(dir)
	if err != nil {
		t.Fatalf("DirTreeHash (3rd): %v", err)
	}
	if h3 == h1 {
		t.Error("DirTreeHash did not change after content edit")
	}
}

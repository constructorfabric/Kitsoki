package kitstage

import (
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
)

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	f, err := Load(Path(t.TempDir()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Version != 1 || len(f.Kits) != 0 {
		t.Fatalf("expected fresh empty file, got %+v", f)
	}
}

func TestStageRoundTripAndByteIdenticalSaves(t *testing.T) {
	root := t.TempDir()
	entry := &Entry{
		Source:   "@kitsoki/dev-story",
		Version:  "0.2.0",
		TreeHash: "b81a",
		From:     Snapshot{Version: "0.1.0", TreeHash: "9f2c"},
		StagedAt: "2026-07-11T18:22:00Z",
	}
	if err := Stage(root, "dev-story", entry); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	f, err := Load(Path(root))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := f.Kits["dev-story"]
	if got == nil {
		t.Fatal("staged entry missing after round trip")
	}
	if got.Version != "0.2.0" || got.From.Version != "0.1.0" || got.From.TreeHash != "9f2c" {
		t.Fatalf("round trip mangled entry: %+v", got)
	}

	first, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := Save(Path(root), f); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	second, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("saves not byte-identical:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

func TestRemoveDropsEntryWorkdirAndEmptyFile(t *testing.T) {
	root := t.TempDir()
	if err := Stage(root, "dev-story", &Entry{Source: "@kitsoki/dev-story", TreeHash: "b81a"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	wd := WorkDir(root, "dev-story")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wd, "plan.yaml"), []byte("x: 1\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	if err := Remove(root, "dev-story"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if Exists(Path(root)) {
		t.Fatal("staged lockfile should be removed once empty")
	}
	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("workdir should be removed, stat err = %v", err)
	}
	// The shared .kitsoki/kit-update parent goes too once it is empty.
	if _, err := os.Stat(filepath.Dir(wd)); !os.IsNotExist(err) {
		t.Fatalf("empty kit-update dir should be removed, stat err = %v", err)
	}
	// Removing again is not an error.
	if err := Remove(root, "dev-story"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestRemoveKeepsOtherEntries(t *testing.T) {
	root := t.TempDir()
	if err := Stage(root, "a", &Entry{Source: "@kitsoki/a", TreeHash: "aa"}); err != nil {
		t.Fatalf("Stage a: %v", err)
	}
	if err := Stage(root, "b", &Entry{Source: "@kitsoki/b", TreeHash: "bb"}); err != nil {
		t.Fatalf("Stage b: %v", err)
	}
	if err := Remove(root, "a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	f, err := Load(Path(root))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Kits["a"] != nil || f.Kits["b"] == nil {
		t.Fatalf("expected only b to remain, got %v", f.SortedNames())
	}
}

func TestFindProjectRootWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := Stage(root, "dev-story", &Entry{Source: "@kitsoki/dev-story", TreeHash: "b81a"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	nested := filepath.Join(root, ".kitsoki", "stories", "proj-dev")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, ok := FindProjectRoot(nested)
	if !ok {
		t.Fatal("expected to find project root")
	}
	// t.TempDir may sit behind a symlink (e.g. /var -> /private/var); compare
	// resolved paths.
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	if _, ok := FindProjectRoot(t.TempDir()); ok {
		t.Fatal("unrelated dir should find no project root")
	}
}

// stageTreeCandidate snapshots a tiny kit dir into the (test-scoped) tree
// cache and stages it, returning the project root and the staged app.yaml
// path inside the cache.
func stageTreeCandidate(t *testing.T, name string) (projectRoot, cachedApp string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	kitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte("app:\n  id: "+name+"\n  version: 0.2.0\n"), 0o644); err != nil {
		t.Fatalf("write kit app.yaml: %v", err)
	}
	root, treeHash, err := kitgit.MaterializeTree(kitDir)
	if err != nil {
		t.Fatalf("MaterializeTree: %v", err)
	}

	projectRoot = t.TempDir()
	entry := &Entry{Source: "@kitsoki/" + name, Version: "0.2.0", TreeHash: treeHash, From: Snapshot{Version: "0.1.0"}}
	if err := Stage(projectRoot, name, entry); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	return projectRoot, filepath.Join(root, "app.yaml")
}

func TestResolveTreeTiers(t *testing.T) {
	projectRoot, cachedApp := stageTreeCandidate(t, "dev-story")
	f, err := Load(Path(projectRoot))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dir, err := ResolveTree(f.Kits["dev-story"])
	if err != nil {
		t.Fatalf("ResolveTree: %v", err)
	}
	if filepath.Join(dir, "app.yaml") != cachedApp {
		t.Fatalf("ResolveTree = %q, want dir of %q", dir, cachedApp)
	}

	// A tree hash that was never materialized is a hard, actionable error.
	if _, err := ResolveTree(&Entry{TreeHash: "deadbeef"}); err == nil {
		t.Fatal("expected cache-miss error for unknown tree hash")
	}
	// Same for a git commit that is not in the commit cache.
	if _, err := ResolveTree(&Entry{Commit: "0123456789012345678901234567890123456789"}); err == nil {
		t.Fatal("expected cache-miss error for unknown commit")
	}
}

func TestWrapResolverStagedSelection(t *testing.T) {
	projectRoot, cachedApp := stageTreeCandidate(t, "dev-story")
	importerDir := filepath.Join(projectRoot, ".kitsoki", "stories", "proj-dev")
	if err := os.MkdirAll(importerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	baseCalls := 0
	base := func(name, _ string, override bool) (string, error) {
		baseCalls++
		return "/base/" + name + "/app.yaml", nil
	}

	t.Run("selected name resolves staged and beats base", func(t *testing.T) {
		r := WrapResolver(base, SelectAll)
		got, err := r("dev-story", importerDir, true)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != cachedApp {
			t.Fatalf("resolved %q, want staged %q", got, cachedApp)
		}
	})

	t.Run("unstaged name falls through under all", func(t *testing.T) {
		r := WrapResolver(base, SelectAll)
		got, err := r("other-kit", importerDir, true)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "/base/other-kit/app.yaml" {
			t.Fatalf("expected base fallthrough, got %q", got)
		}
	})

	t.Run("explicitly named but unstaged is a hard error", func(t *testing.T) {
		r := WrapResolver(base, SelectNames("other-kit"))
		if _, err := r("other-kit", importerDir, true); err == nil {
			t.Fatal("expected hard error for explicit selection with nothing staged")
		}
	})

	t.Run("non-override tier never consults staging", func(t *testing.T) {
		r := WrapResolver(base, SelectAll)
		got, err := r("dev-story", importerDir, false)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "/base/dev-story/app.yaml" {
			t.Fatalf("expected base for override=false, got %q", got)
		}
	})

	t.Run("no selector means base untouched behavior", func(t *testing.T) {
		r := WrapResolver(base, nil)
		got, err := r("dev-story", importerDir, true)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "/base/dev-story/app.yaml" {
			t.Fatalf("expected base with nil selector, got %q", got)
		}
	})
}

func TestParseSelector(t *testing.T) {
	cases := []struct {
		env          string
		name         string
		wantSel      bool
		wantExplicit bool
	}{
		{"", "dev-story", false, false},
		{"all", "dev-story", true, false},
		{"1", "dev-story", true, false},
		{"true", "anything", true, false},
		{"dev-story", "dev-story", true, true},
		{"dev-story", "other", false, false},
		{"a, b", "b", true, true},
		{"a, b", "c", false, false},
	}
	for _, tc := range cases {
		sel, explicit := ParseSelector(tc.env)(tc.name)
		if sel != tc.wantSel || explicit != tc.wantExplicit {
			t.Errorf("ParseSelector(%q)(%q) = (%v,%v), want (%v,%v)", tc.env, tc.name, sel, explicit, tc.wantSel, tc.wantExplicit)
		}
	}
}

func TestSnapshotOfLockAndEqual(t *testing.T) {
	le := &kitlock.Entry{Version: "0.1.0", Commit: "c1", TreeHash: "t1"}
	s := SnapshotOfLock(le)
	if s.Version != "0.1.0" || s.Commit != "c1" || s.TreeHash != "t1" {
		t.Fatalf("SnapshotOfLock = %+v", s)
	}
	if !s.Equal(Snapshot{Version: "different-version-ok", Commit: "c1", TreeHash: "t1"}) {
		t.Fatal("Equal should compare pinned content (commit+tree), not version")
	}
	if s.Equal(Snapshot{Commit: "c1", TreeHash: "t2"}) {
		t.Fatal("Equal should detect tree drift")
	}
	if !SnapshotOfLock(nil).Equal(Snapshot{}) {
		t.Fatal("nil lock entry should map to zero snapshot")
	}
}

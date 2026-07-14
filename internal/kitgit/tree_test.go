package kitgit

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

func TestMaterializeTreeAndCachedTree(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	src := writeTree(t, map[string]string{
		"app.yaml":        "app:\n  id: demo\n",
		"rooms/main.yaml": "id: main\n",
	})

	root, hash, err := MaterializeTree(src)
	if err != nil {
		t.Fatalf("MaterializeTree: %v", err)
	}
	wantHash, err := DirTreeHash(src)
	if err != nil {
		t.Fatalf("DirTreeHash: %v", err)
	}
	if hash != wantHash {
		t.Fatalf("hash = %s, want DirTreeHash %s", hash, wantHash)
	}
	// The snapshot's own hash (sentinel excluded) matches the source's.
	gotHash, err := DirTreeHash(root)
	if err != nil {
		t.Fatalf("DirTreeHash(snapshot): %v", err)
	}
	if gotHash != wantHash {
		t.Fatalf("snapshot content hash %s != source %s", gotHash, wantHash)
	}

	cached, ok, err := CachedTree(hash)
	if err != nil || !ok {
		t.Fatalf("CachedTree = (%q,%v,%v), want hit", cached, ok, err)
	}
	if cached != root {
		t.Fatalf("CachedTree root %q != materialized root %q", cached, root)
	}

	// Idempotent: mutating the SOURCE after snapshotting must not affect the
	// cached tree; re-materializing the mutated source yields a NEW key.
	if err := os.WriteFile(filepath.Join(src, "app.yaml"), []byte("app:\n  id: changed\n"), 0o644); err != nil {
		t.Fatalf("mutate src: %v", err)
	}
	root2, hash2, err := MaterializeTree(src)
	if err != nil {
		t.Fatalf("MaterializeTree(mutated): %v", err)
	}
	if hash2 == hash {
		t.Fatal("mutated source should produce a different tree hash")
	}
	if root2 == root {
		t.Fatal("mutated source should snapshot to a different cache dir")
	}
	if _, ok, _ := CachedTree(hash); !ok {
		t.Fatal("original snapshot should remain cached")
	}

	if _, ok, err := CachedTree("not-a-real-hash"); err != nil || ok {
		t.Fatalf("CachedTree miss = (%v,%v), want clean miss", ok, err)
	}
}

func TestTreeDiff(t *testing.T) {
	oldRoot := writeTree(t, map[string]string{
		"app.yaml":          "v1\n",
		"rooms/keep.yaml":   "same\n",
		"rooms/change.yaml": "old\n",
		"rooms/gone.yaml":   "bye\n",
	})
	newRoot := writeTree(t, map[string]string{
		"app.yaml":          "v2\n",
		"rooms/keep.yaml":   "same\n",
		"rooms/change.yaml": "new\n",
		"rooms/added.yaml":  "hi\n",
	})
	// Sentinels are excluded from the diff like they are from the hash.
	if err := os.WriteFile(filepath.Join(newRoot, ".materialized"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	changes, err := TreeDiff(oldRoot, newRoot)
	if err != nil {
		t.Fatalf("TreeDiff: %v", err)
	}
	want := []FileChange{
		{Path: "app.yaml", Kind: "modified"},
		{Path: "rooms/added.yaml", Kind: "added"},
		{Path: "rooms/change.yaml", Kind: "modified"},
		{Path: "rooms/gone.yaml", Kind: "removed"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("TreeDiff = %+v, want %+v", changes, want)
	}
}

func TestTreeDiffIdenticalTreesIsEmpty(t *testing.T) {
	files := map[string]string{"app.yaml": "same\n", "rooms/a.yaml": "a\n"}
	changes, err := TreeDiff(writeTree(t, files), writeTree(t, files))
	if err != nil {
		t.Fatalf("TreeDiff: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

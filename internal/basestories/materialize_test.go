package basestories

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestMaterialize extracts the embedded library to an isolated cache and checks
// idempotency and content presence. Skips when the library has not been staged
// into the binary (a bare `go test` without `make embed-stories`).
func TestMaterialize(t *testing.T) {
	// Isolate the cache so the test never touches the developer's real
	// ~/.cache/kitsoki and is hermetic across runs.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root, err := Materialize(context.Background())
	if err == ErrNotStaged {
		t.Skip("story library not staged into the test binary; run `make embed-stories`")
	}
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// The materialized root contains each base story as a subdir.
	app := filepath.Join(root, "dev-story", "app.yaml")
	if _, statErr := os.Stat(app); statErr != nil {
		t.Fatalf("expected dev-story/app.yaml under materialized root %q: %v", root, statErr)
	}

	// The cache key is a content hash directory under .../kitsoki/stories/.
	if got := filepath.Base(filepath.Dir(root)); got != "stories" {
		t.Errorf("cache parent dir = %q, want \"stories\"", got)
	}

	// A runtime asset of a base story (a prompt) materializes too, proving the
	// whole tree (not just app.yaml) lands on disk for downstream reads.
	prompts, _ := filepath.Glob(filepath.Join(root, "dev-story", "prompts", "*.md"))
	if len(prompts) == 0 {
		t.Errorf("expected dev-story/prompts/*.md under %q", root)
	}

	// Idempotent: a second call returns the same root without re-extracting.
	root2, err := Materialize(context.Background())
	if err != nil {
		t.Fatalf("Materialize (second call): %v", err)
	}
	if root2 != root {
		t.Errorf("second Materialize root = %q, want %q (idempotent)", root2, root)
	}
}

// TestMaterializeContentHashStable verifies the cache key is a stable content
// hash: two hashTree calls over the same embed agree.
func TestMaterializeContentHashStable(t *testing.T) {
	sum1, files1, err := hashTree()
	if err != nil {
		t.Fatalf("hashTree: %v", err)
	}
	sum2, files2, err := hashTree()
	if err != nil {
		t.Fatalf("hashTree (second): %v", err)
	}
	if sum1 != sum2 {
		t.Errorf("content hash not stable: %q != %q", sum1, sum2)
	}
	if len(files1) != len(files2) {
		t.Errorf("file count not stable: %d != %d", len(files1), len(files2))
	}
}

package host

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/embed"
)

func TestAgentSearchHandler(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()

	// Create 3 small markdown files.
	files := map[string]string{
		"alpha.md": "## Alpha\nThis is about apples and fruits.\n",
		"beta.md":  "## Beta\nThis is about bananas and tropical plants.\n",
		"gamma.md": "## Gamma\nThis is about grapes and vineyards.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fakeEmbedder := embed.NewFakeEmbedder(8)
	store := embed.NewStore(storeDir)
	handler := NewAgentSearchHandler("test-model", dir, fakeEmbedder, store)

	args := map[string]any{
		"query":  "fruit",
		"corpus": "*.md",
		"top_k":  3,
	}

	res, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler returned Result.Error: %s", res.Error)
	}

	hitsRaw, ok := res.Data["hits"]
	if !ok {
		t.Fatal("result missing 'hits' key")
	}
	hits, ok := hitsRaw.([]map[string]any)
	if !ok {
		t.Fatalf("hits has unexpected type %T", hitsRaw)
	}
	if len(hits) < 1 {
		t.Fatal("expected at least 1 hit, got 0")
	}
	for i, h := range hits {
		if _, ok := h["path"]; !ok {
			t.Errorf("hit[%d] missing 'path'", i)
		}
		if _, ok := h["chunk_id"]; !ok {
			t.Errorf("hit[%d] missing 'chunk_id'", i)
		}
		if _, ok := h["text"]; !ok {
			t.Errorf("hit[%d] missing 'text'", i)
		}
		if _, ok := h["score"]; !ok {
			t.Errorf("hit[%d] missing 'score'", i)
		}
	}

	// top should match first hit.
	top, ok := res.Data["top"]
	if !ok {
		t.Fatal("result missing 'top' key")
	}
	if top == nil {
		t.Fatal("expected non-nil top")
	}
}

func TestAgentSearchMinScore(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("## Doc\nSome content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeEmbedder := embed.NewFakeEmbedder(8)
	store := embed.NewStore(storeDir)
	handler := NewAgentSearchHandler("test-model", dir, fakeEmbedder, store)

	args := map[string]any{
		"query":     "anything",
		"corpus":    "*.md",
		"min_score": 2.0, // impossible for cosine similarity (max is 1.0)
	}

	res, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler returned Result.Error: %s", res.Error)
	}

	hitsRaw := res.Data["hits"]
	hits, _ := hitsRaw.([]map[string]any)
	if len(hits) != 0 {
		t.Errorf("expected 0 hits with min_score=2.0, got %d", len(hits))
	}
}

func TestAgentSearchEmptyCorpus(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()
	// No files in dir — glob matches nothing.

	fakeEmbedder := embed.NewFakeEmbedder(8)
	store := embed.NewStore(storeDir)
	handler := NewAgentSearchHandler("test-model", dir, fakeEmbedder, store)

	args := map[string]any{
		"query":  "anything",
		"corpus": "*.md",
	}

	res, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler returned Result.Error: %s", res.Error)
	}

	hitsRaw := res.Data["hits"]
	hits, _ := hitsRaw.([]map[string]any)
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty corpus, got %d", len(hits))
	}
}

// TestAgentSearchRealDocs runs the handler against the actual docs/ tree in
// this repository using FakeEmbedder. It verifies that the globs resolve, the
// markdown chunker produces entries, and the ranking returns hits — without a
// real embedding model or network call.
func TestAgentSearchRealDocs(t *testing.T) {
	// Locate the repo root by walking up from this source file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/host/ → internal/ → repo root
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	docsDir := filepath.Join(repoRoot, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("docs/ not found at %s (not running from repo): %v", docsDir, err)
	}

	storeDir := t.TempDir()
	fakeEmbedder := embed.NewFakeEmbedder(16)
	store := embed.NewStore(storeDir)
	handler := NewAgentSearchHandler("nomic-embed-text-v1.5", repoRoot, fakeEmbedder, store)

	res, err := handler(context.Background(), map[string]any{
		"query": "embedding vector search cosine similarity",
		"corpus": []any{
			"docs/architecture/**/*.md",
			"docs/proposals/*.md",
		},
		"top_k":     5,
		"min_score": 0.0,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler Result.Error: %s", res.Error)
	}

	hits, _ := res.Data["hits"].([]map[string]any)
	if len(hits) == 0 {
		t.Fatal("expected at least one hit from docs/, got 0")
	}
	t.Logf("got %d hits from real docs/", len(hits))
	for i, h := range hits {
		path, _ := h["path"].(string)
		score, _ := h["score"].(float64)
		text, _ := h["text"].(string)
		preview := text
		if len(preview) > 80 {
			preview = preview[:80]
		}
		t.Logf("  [%d] score=%.4f  %s  %q", i, score, path, strings.ReplaceAll(preview, "\n", " "))
	}

	// Every hit must have the required fields and a path under docs/.
	for i, h := range hits {
		path, _ := h["path"].(string)
		if !strings.HasPrefix(path, "docs/") {
			t.Errorf("hit[%d].path %q does not start with docs/", i, path)
		}
		if _, ok := h["chunk_id"]; !ok {
			t.Errorf("hit[%d] missing chunk_id", i)
		}
		if _, ok := h["text"]; !ok {
			t.Errorf("hit[%d] missing text", i)
		}
	}

	// Second call should hit the store cache — result must be identical.
	res2, err := handler(context.Background(), map[string]any{
		"query":  "embedding vector search cosine similarity",
		"corpus": []any{"docs/architecture/**/*.md", "docs/proposals/*.md"},
		"top_k":  5,
	})
	if err != nil || res2.Error != "" {
		t.Fatalf("second call failed: err=%v Result.Error=%s", err, res2.Error)
	}
	hits2, _ := res2.Data["hits"].([]map[string]any)
	if len(hits2) != len(hits) {
		t.Errorf("cache round-trip: want %d hits, got %d", len(hits), len(hits2))
	}
}

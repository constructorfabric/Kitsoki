package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDigestDirs_DeterministicAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "a", "app.yaml"), "id: a\n")
	writeFileT(t, filepath.Join(dir, "b.yaml"), "id: b\n")
	writeFileT(t, filepath.Join(dir, ".hidden", "x.yaml"), "ignored\n")

	first := digestDirs([]string{dir})
	second := digestDirs([]string{dir})
	if first != second {
		t.Fatalf("digest not deterministic: %s vs %s", first, second)
	}
	if len(first) != len("sha256:")+64 {
		t.Errorf("digest %q not sha256-shaped", first)
	}

	// Content change changes the digest.
	writeFileT(t, filepath.Join(dir, "a", "app.yaml"), "id: a-changed\n")
	if changed := digestDirs([]string{dir}); changed == first {
		t.Errorf("digest unchanged after content edit")
	}

	// A dot-directory edit does NOT change the digest.
	base := digestDirs([]string{dir})
	writeFileT(t, filepath.Join(dir, ".hidden", "x.yaml"), "still ignored\n")
	if after := digestDirs([]string{dir}); after != base {
		t.Errorf("dot-directory content leaked into the digest")
	}

	// A new file changes the digest.
	writeFileT(t, filepath.Join(dir, "c.yaml"), "id: c\n")
	if after := digestDirs([]string{dir}); after == base {
		t.Errorf("digest unchanged after adding a file")
	}
}

func TestDigestDirs_EmptyAndMissing(t *testing.T) {
	empty := digestDirs(nil)
	if empty != digestDirs([]string{}) {
		t.Errorf("nil vs empty dirs digests differ")
	}
	// A missing dir digests like an empty one — polling must never error.
	if got := digestDirs([]string{filepath.Join(t.TempDir(), "nope")}); len(got) != len("sha256:")+64 {
		t.Errorf("missing dir digest %q not sha256-shaped", got)
	}
}

func TestDigestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	if got := digestFile(path); got != "" {
		t.Errorf("missing file digest = %q, want \"\"", got)
	}
	writeFileT(t, path, "catalog: {id: x}\n")
	first := digestFile(path)
	if first == "" || first == digestFile(filepath.Join(dir, "absent")) {
		t.Fatalf("file digest = %q", first)
	}
	writeFileT(t, path, "catalog: {id: y}\n")
	if second := digestFile(path); second == first {
		t.Errorf("digest unchanged after edit")
	}
}

func TestHandleAPIConfig(t *testing.T) {
	root := t.TempDir()
	stories := filepath.Join(root, "stories")
	writeFileT(t, filepath.Join(stories, "demo", "app.yaml"), "id: demo\n")
	writeFileT(t, filepath.Join(root, "pog", "catalog.yaml"), "schema: project-object-graph/seed-catalog/v0\n")

	s := &Server{materializeRoot: root, storyDirs: []string{stories}}
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	s.handleAPIConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		BinaryBuild   string `json:"binary_build"`
		StoriesDigest string `json:"stories_digest"`
		CatalogDigest string `json:"catalog_digest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.BinaryBuild == "" {
		t.Errorf("binary_build empty")
	}
	if got.StoriesDigest != digestDirs([]string{stories}) {
		t.Errorf("stories_digest = %q, want the story-dir content digest", got.StoriesDigest)
	}
	if got.CatalogDigest != digestFile(filepath.Join(root, "pog", "catalog.yaml")) || got.CatalogDigest == "" {
		t.Errorf("catalog_digest = %q", got.CatalogDigest)
	}

	// POST is rejected.
	rec2 := httptest.NewRecorder()
	s.handleAPIConfig(rec2, httptest.NewRequest(http.MethodPost, "/api/config", nil))
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec2.Code)
	}
}

func TestHandleAPIConfig_NoCatalogIsEmptyDigest(t *testing.T) {
	s := &Server{materializeRoot: t.TempDir()}
	rec := httptest.NewRecorder()
	s.handleAPIConfig(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["catalog_digest"] != "" {
		t.Errorf("catalog_digest = %v, want \"\" for a repo with no pog/catalog.yaml", got["catalog_digest"])
	}
}

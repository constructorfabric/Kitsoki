package extdocs

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadManifest_DefaultsPromptPublishToSummary(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ManifestFileName), `schema: kitsoki.docs/v1
owner:
  kind: story
  id: story:@local/demo
docs:
  - id: overview
    path: README.md
  - id: prompt
    path: prompts/review.md
`)

	m, err := LoadManifest(filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.Docs[0].Publish; got != "true" {
		t.Fatalf("overview publish = %q, want true", got)
	}
	if got := m.Docs[1].Publish; got != "summary" {
		t.Fatalf("prompt publish = %q, want summary", got)
	}
}

func TestLoadManifest_RejectsEscapingPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ManifestFileName), `schema: kitsoki.docs/v1
owner:
  kind: kit
  id: "@example/demo"
docs:
  - id: leak
    path: ../secret.md
`)

	if _, err := LoadManifest(filepath.Join(dir, ManifestFileName)); err == nil {
		t.Fatal("expected escaping doc path to fail")
	}
}

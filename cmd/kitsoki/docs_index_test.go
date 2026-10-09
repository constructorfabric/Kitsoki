package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_DocsIndexWritesOutputs(t *testing.T) {
	root := t.TempDir()
	storyDir := filepath.Join(root, "stories", "solo")
	if err := os.MkdirAll(storyDir, 0o755); err != nil {
		t.Fatalf("mkdir story: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storyDir, "app.yaml"), []byte(`app:
  id: solo
  version: 0.1.0
  title: Solo
root: idle
states:
  idle:
    description: Idle
`), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	jsonOut := filepath.Join(root, ".artifacts", "docs", "extensions-index.json")
	mdOut := filepath.Join(root, ".artifacts", "docs", "index.md")

	out, err := execRoot(t, "docs", "index", "--root", root, "--json-out", jsonOut, "--markdown-out", mdOut)
	if err != nil {
		t.Fatalf("docs index: %v\n%s", err, out)
	}
	if !strings.Contains(out, "indexed 0 package(s), 1 story/stories, 2 component(s)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	jsonBody, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	if !strings.Contains(string(jsonBody), `"id": "story:@local/solo"`) {
		t.Fatalf("json missing story id:\n%s", jsonBody)
	}
	mdBody, err := os.ReadFile(mdOut)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(string(mdBody), "# Kitsoki Extension Library Index") {
		t.Fatalf("markdown missing heading:\n%s", mdBody)
	}
}

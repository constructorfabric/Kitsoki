package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_POGInitDryRun(t *testing.T) {
	tmp := t.TempDir()
	brief := filepath.Join(tmp, "brief.md")
	if err := os.WriteFile(brief, []byte("# CLI Product\n\n## Requirements\n\n- Work from a brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	out, err := execRoot(t, "pog", "init", repo, "--brief", brief, "--dry-run")
	if err != nil {
		t.Fatalf("pog init --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pog init dry-run plan") || !strings.Contains(out, "pog/catalog.yaml") {
		t.Fatalf("dry-run output missing plan details:\n%s", out)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("dry-run created repo path; stat err=%v", err)
	}
}

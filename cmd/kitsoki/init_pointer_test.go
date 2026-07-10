package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInitOnboardPointer verifies WS-A A1: `kitsoki init` points at the full
// onboarding front door (`onboard .`) when the target repo carries no
// .kitsoki.yaml, and stays quiet when the repo is already onboarded.
func TestInitOnboardPointer(t *testing.T) {
	got := initOnboardPointer(false)
	if got == "" {
		t.Fatal("expected an onboarding pointer when no .kitsoki.yaml exists, got empty")
	}
	for _, want := range []string{"no .kitsoki.yaml", "onboard .", "kitsoki run", "docs/getting-started.md", "MCP"} {
		if !contains(got, want) {
			t.Errorf("pointer missing %q; pointer was:\n%s", want, got)
		}
	}
	if got := initOnboardPointer(true); got != "" {
		t.Errorf("expected no pointer when the repo is already onboarded, got:\n%s", got)
	}
}

// TestHasProjectConfig verifies the .kitsoki.yaml detection init uses to
// decide whether to print the onboarding pointer.
func TestHasProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if hasProjectConfig(dir) {
		t.Fatal("empty dir should not count as onboarded")
	}
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte("story_dirs: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasProjectConfig(dir) {
		t.Fatal("dir with .kitsoki.yaml should count as onboarded")
	}
}

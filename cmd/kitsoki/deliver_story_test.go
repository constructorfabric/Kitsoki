package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeliverDecomposerPolicyBoundsGLMDiscovery(t *testing.T) {
	repo := deliverTestRepoRoot(t)
	appPath := filepath.Join(repo, "stories", "deliver", "app.yaml")
	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}

	var app struct {
		Agents map[string]struct {
			Tools []string `yaml:"tools"`
		} `yaml:"agents"`
	}
	if err := yaml.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	decomposer, ok := app.Agents["decomposer"]
	if !ok {
		t.Fatal("decomposer agent missing")
	}
	for _, tool := range decomposer.Tools {
		if tool == "Bash" || tool == "Agent" || tool == "Task" || tool == "AskUserQuestion" {
			t.Fatalf("decomposer exposes expensive or nested tool %q", tool)
		}
	}

	promptPath := filepath.Join(repo, "stories", "deliver", "prompts", "decompose.md")
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(prompt)
	for _, want := range []string{
		"Do not run shell commands",
		"proposal files under `docs/proposals/` as authoritative task specs",
		"inspect at most 3 directly",
		"Count repeated reads of the same file against",
		"submit next",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("decomposer prompt missing GLM discovery guard %q", want)
		}
	}
}

func deliverTestRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

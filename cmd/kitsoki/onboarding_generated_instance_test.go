package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func python3(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	return path
}

func TestOnboardingGeneratedDevStoryInstanceValidates(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	script := `
import sys
sys.path.insert(0, "stories/dev-story/scripts")
from init_apply import app_yaml

print(app_yaml({
    "project_id": "platform-presentation",
    "project_title": "Platform Presentation",
    "ticket_repo": "",
    "build_command": "go build ./...",
    "test_command": "go test ./...",
}))
`
	cmd := exec.Command(python3(t), "-c", script)
	cmd.Dir = repoRoot
	generated, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate onboarding instance: %v\n%s", err, generated)
	}

	appPath := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(appPath, generated, 0o644); err != nil {
		t.Fatalf("write generated app: %v", err)
	}

	out, err := execRoot(t, "validate", appPath)
	if err != nil {
		t.Fatalf("generated app should validate: %v\n%s\n--- generated ---\n%s", err, out, generated)
	}
}

func TestOnboardingDiscoveryUsesMetaRepoProjectMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "services", "example-service")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "example-service"), 0o755); err != nil {
		t.Fatalf("mkdir project metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects", "example-service", "project.toml"), []byte(`
schema_version = 1
title = "Example Service"
description = "Example service workspace."

[[repos]]
submodule    = "services/example-service"
role         = "primary"
description  = "Go service that composes API responses."
test_command = "go test ./..."

[repos.local_run]
build = "go build -o ./bin/example-service ./cmd/example-service"
start = "./bin/example-service --config configs/example-service.example.yml"
`), 0o644); err != nil {
		t.Fatalf("write project metadata: %v", err)
	}

	cmd := exec.Command(python3(t), "stories/dev-story/scripts/init_discover.py", "onboard "+target, root, root)
	cmd.Dir = filepath.Join("..", "..")
	discovered, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("discover onboarding target: %v\n%s", err, discovered)
	}
	text := string(discovered)
	for _, want := range []string{
		`"project_id": "example-service"`,
		`"project_title": "Example Service"`,
		`"stack": "go project"`,
		`"test_command": "go test ./..."`,
		`"build_command": "go build -o ./bin/example-service ./cmd/example-service"`,
		`"dev_command": "./bin/example-service --config configs/example-service.example.yml"`,
		`"conventions": "project"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("discovery missing %q:\n%s", want, text)
		}
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectProfileValidateProfileJSONEnvelope(t *testing.T) {
	profile := `{
		"schema": "project-profile/v1",
		"id": "acme-rust",
		"title": "Acme Rust",
		"repo": {"root": ".", "vcs": "git"},
		"stack": {"kind": "rust", "languages": ["rust"]},
		"commands": {"dev": "make dev", "test": "make test", "build": "make build"},
		"kitsoki": {
			"story": "dev-story",
			"instance": {
				"id": "acme-rust-dev",
				"path": ".kitsoki/stories/acme-rust-dev/app.yaml",
				"bindings": {
					"ticket": "host.local_files.ticket",
					"vcs": "host.git",
					"ci": "host.local",
					"workspace": "host.git_worktree",
					"transport": "host.append_to_file"
				}
			}
		}
	}`

	out, err := execRoot(t,
		"project-profile", "validate",
		"--json",
		"--envelope",
		"--repo-root", t.TempDir(),
		"--profile-json", profile,
	)
	if err != nil {
		t.Fatalf("validate --profile-json --envelope: %v\n%s", err, out)
	}
	var env struct {
		OK                bool           `json:"ok"`
		Profile           map[string]any `json:"profile"`
		ProfileJSON       string         `json:"profile_json"`
		Schema            []string       `json:"schema"`
		Semantic          []string       `json:"semantic"`
		Warnings          []string       `json:"warnings"`
		ValidatorStdout   string         `json:"validator_stdout"`
		ValidatorStderr   string         `json:"validator_stderr"`
		ValidatorExitCode int            `json:"validator_exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope is not json: %v\n%s", err, out)
	}
	if !env.OK || env.ValidatorExitCode != 0 {
		t.Fatalf("envelope should validate: %+v", env)
	}
	if got := env.Profile["id"]; got != "acme-rust" {
		t.Fatalf("profile id: got %v", got)
	}
	if env.ProfileJSON == "" || !strings.Contains(env.ProfileJSON, `"id":"acme-rust"`) {
		t.Fatalf("profile_json missing compact profile: %q", env.ProfileJSON)
	}
	if len(env.Schema) != 0 || len(env.Semantic) != 0 {
		t.Fatalf("unexpected validation errors: schema=%v semantic=%v", env.Schema, env.Semantic)
	}
	if env.ValidatorStderr != "" {
		t.Fatalf("validator_stderr: %q", env.ValidatorStderr)
	}
	if !strings.Contains(env.ValidatorStdout, `"ok": true`) {
		t.Fatalf("validator_stdout missing validation report: %q", env.ValidatorStdout)
	}
}

func TestProjectProfileValidateEnvelopeReturnsZeroForInvalidProfile(t *testing.T) {
	out, err := execRoot(t,
		"project-profile", "validate",
		"--json",
		"--envelope",
		"--profile-json", `{"schema":"project-profile/v1"}`,
	)
	if err != nil {
		t.Fatalf("invalid envelope should return zero so host.run can bind stdout_json: %v\n%s", err, out)
	}
	var env struct {
		OK                bool     `json:"ok"`
		Schema            []string `json:"schema"`
		ValidatorExitCode int      `json:"validator_exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope is not json: %v\n%s", err, out)
	}
	if env.OK {
		t.Fatalf("invalid profile reported ok: %s", out)
	}
	if env.ValidatorExitCode != 1 {
		t.Fatalf("validator_exit_code: got %d, want 1", env.ValidatorExitCode)
	}
	if len(env.Schema) == 0 {
		t.Fatalf("expected schema errors: %s", out)
	}
}

func TestProjectProfileStoryPacksListSetAndAdd(t *testing.T) {
	repo := t.TempDir()
	profileDir := filepath.Join(repo, ".kitsoki")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	profilePath := filepath.Join(profileDir, "project-profile.yaml")
	initial := []byte(`schema: project-profile/v1
id: focused-engineering
title: Focused Engineering
repo: { root: ".", vcs: git }
stack: { kind: go }
kitsoki:
  story: dev-story
  story_pack: focused-engineering
  enabled_stories: [setup, bugfix, pr-refinement, git-ops]
  instance:
    id: focused-engineering-dev
    path: .kitsoki/stories/focused-engineering-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
onboarding:
  story_pack: focused-engineering
  starter_stories:
    - id: setup
      status: enabled
      summary: setup
`)
	if err := os.WriteFile(profilePath, initial, 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	out, err := execRoot(t, "project-profile", "story-packs", "--target", repo, "list")
	if err != nil {
		t.Fatalf("story-packs list: %v\n%s", err, out)
	}
	for _, want := range []string{"focused-engineering", "core-setup", "stories: setup, bugfix, repo-bakeoff, pr-refinement, git-ops", "enabled_stories: setup, bugfix, pr-refinement, git-ops"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}

	out, err = execRoot(t, "project-profile", "story-packs", "--target", repo, "set", "core")
	if err != nil {
		t.Fatalf("story-packs set: %v\n%s", err, out)
	}
	body, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile after set: %v", err)
	}
	text := string(body)
	for _, want := range []string{"story_pack: core-setup", "- setup", "- git-ops", "story_pack_title: Core setup"} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile after set missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "- bugfix") {
		t.Fatalf("set should replace enabled stories, got:\n%s", text)
	}

	out, err = execRoot(t, "project-profile", "story-packs", "--target", repo, "add", "review-quality")
	if err != nil {
		t.Fatalf("story-packs add: %v\n%s", err, out)
	}
	body, err = os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile after add: %v", err)
	}
	text = string(body)
	for _, want := range []string{"story_pack: core-setup", "- bugfix", "- fix-tests", "- code-review", "- docs-review", "- pr-refinement"} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile after add missing %q:\n%s", want, text)
		}
	}
}

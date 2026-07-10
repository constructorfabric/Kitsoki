package projectprofile

import (
	"strings"
	"testing"
)

func TestValidate_AcceptsCurrentProfileShape(t *testing.T) {
	profile := []byte(`schema: project-profile/v1
id: gears-rust
title: Gears Rust
repo:
  root: "."
  vcs: git
stack:
  kind: rust
  languages: [rust]
commands:
  build: "make build"
  test: "make test"
  check: "make check"
testing:
  mechanisms:
    - kind: build
      runner: command
      command: "make build"
kitsoki:
  story: dev-story
  enabled_stories: [setup, bugfix, pr-refinement, git-ops]
  instance:
    id: gears-rust-dev
    path: .kitsoki/stories/gears-rust-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
dev_story_profile:
  bugfix:
    build_cmd: "make build"
    test_cmd: "make test"
onboarding:
  base_story: dev-story
  base_story_title: Dev-story project workflow
  base_story_reason: Default starter for a normal Rust project.
  starter_stories:
    - id: setup
      title: Project setup
      source_story: dev-story:onboarding
      status: enabled
      summary: Onboard the checkout and run readiness checks.
    - id: bugfix
      title: Bug fixing
      source_story: bugfix
      status: enabled
      summary: Drive a picked bug through fix and validation.
  expansion_policy: Add story ids after focused readiness checks pass.
  repo_patterns:
    - id: toolchain
      source: repo-files
      evidence: Cargo.toml and Makefile were detected.
      recommendation: Reuse make build/test as dev-story gates.
  story_customizations:
    - id: toolchain-gates
      status: applied
      summary: Project build/test commands are projected into dev-story.
      evidence: "build=make build; test=make test"
  baseline_commit: d8513b0
  deterministic_flow: stories/dev-story/flows/init_rust_project.yaml
  recording_policy: gated-live-allowed
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.OK {
		t.Fatalf("profile should validate: schema=%v semantic=%v warnings=%v", res.Schema, res.Semantic, res.Warnings)
	}
}

func TestValidate_ReportsSchemaAndSemanticFailures(t *testing.T) {
	profile := []byte(`schema: project-profile/v1
id: bad-id
title: Bad
repo:
  root: "."
  vcs: git
stack:
  kind: made-up
kitsoki:
  story: other
  instance:
    id: custom
    path: stories/custom/app.yaml
    bindings:
      ticket: host.local_files.ticket
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatalf("profile should fail validation")
	}
	joined := strings.Join(append(res.Schema, res.Semantic...), "\n")
	for _, want := range []string{
		"/stack/kind",
		"kitsoki.story",
		"kitsoki.instance.id",
		"kitsoki.instance.path",
		"kitsoki.instance.bindings.vcs",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("validation output missing %q:\n%s", want, joined)
		}
	}
}

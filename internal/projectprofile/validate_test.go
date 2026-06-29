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

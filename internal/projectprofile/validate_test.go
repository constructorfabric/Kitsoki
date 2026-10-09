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
  default_branch: main
  branch_pattern: "feature/{issue_id}-{slug}"
  branch_issue_id: required
stack:
  kind: rust
  languages: [rust]
tracker:
  sources:
    - id: local
      label: Local
      provider: host.local_files.ticket
      kind: local
      mode: local
      args:
        root: .artifacts
    - id: origin
      label: bsacrobatix/gears-rust
      provider: host.gh.ticket
      kind: github
      mode: remote
      args:
        repo: bsacrobatix/gears-rust
pull_requests:
  provider: github
  repository: bsacrobatix/gears-rust
  base_branch: main
  template: .github/pull_request_template.md
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
      ticket: host.ticket_federation
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
dev_story_profile:
  bugfix:
    build_cmd: "make build"
    test_cmd: "make test"
goals:
  onboarding:
    statement: Make this checkout ready for deterministic dev-story work.
    postconditions:
      - id: tests-runnable
        statement: The canonical tests run deterministically.
        gate: required
        verification: tests
      - id: branch-policy-known
        statement: The base branch and working-branch pattern are explicit.
        gate: required
        verification: branch-policy
      - id: ticket-source-known
        statement: The ordered ticket sources are explicit.
        gate: required
        verification: ticket-source
      - id: pr-policy-known
        statement: The pull-request destination and template are explicit.
        gate: required
        verification: pr-policy
  validation:
    statement: Prove and improve dev-story against a stable independent corpus.
    requires: [onboarding]
    postconditions:
      - id: reference-corpus-frozen
        statement: Corpus Forge produced a frozen corpus receipt.
        gate: advisory
        verification: reference-corpus
      - id: bug-to-pr-proven
        statement: A developer can take a configured ticket through pull request creation.
        gate: recommended
        verification: bug-to-pr
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
  resolutions:
    - field: commands.test
      value: make test
      source: discovered
      evidence: Makefile declares the test target.
      update: ".kitsoki/project-profile.yaml#commands.test"
    - field: repo.branch_pattern
      value: "feature/{issue_id}-{slug}"
      source: default
      evidence: No project branch template was found.
      update: ".kitsoki/project-profile.yaml#repo.branch_pattern"
      notice: Could not determine the working-branch template; using feature/{issue_id}-{slug}.
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
setup_plan:
  writes:
    - path: .kitsoki/stories/gears-rust-dev/app.yaml
      action: create
      summary: Materialize the project-local dev-story wrapper.
  verifications:
    - id: tests
      kind: tests
      command: make test
      gate: required
    - id: branch-policy
      kind: profile
      fields: [repo.default_branch, repo.branch_pattern, repo.branch_issue_id]
      gate: required
    - id: ticket-source
      kind: ticket
      fields: [tracker.sources]
      gate: required
    - id: pr-policy
      kind: pull-request
      fields: [pull_requests.repository, pull_requests.base_branch, pull_requests.template]
      gate: required
    - id: reference-corpus
      kind: corpus
      command: kitsoki test flows stories/corpus-forge/app.yaml
      gate: advisory
    - id: bug-to-pr
      kind: workflow
      command: kitsoki test flows .kitsoki/stories/gears-rust-dev/app.yaml
      gate: recommended
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.OK {
		t.Fatalf("profile should validate: schema=%v semantic=%v warnings=%v", res.Schema, res.Semantic, res.Warnings)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("complete profile should not warn: %v", res.Warnings)
	}
}

func TestValidate_RejectsInvalidTicketSourceComposition(t *testing.T) {
	profile := []byte(`schema: project-profile/v1
id: composed
title: Composed
repo:
  root: "."
  vcs: git
stack:
  kind: generic
commands:
  build: "true"
  test: "true"
tracker:
  sources:
    - id: local
      label: Local
      provider: .kitsoki/providers/ticket.star
      kind: local
      mode: local
      args: {root: .artifacts}
    - id: local
      label: local
      provider: host.gh.ticket
      kind: github
      mode: remote
      args: {repo: example/project}
kitsoki:
  story: dev-story
  instance:
    id: composed-dev
    path: .kitsoki/stories/composed-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.capsule_workspace
      transport: host.append_to_file
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatal("invalid ticket source composition should fail semantic validation")
	}
	joined := strings.Join(res.Semantic, "\n")
	for _, want := range []string{`tracker.sources id "local" is declared more than once`, `tracker.sources label "local" is declared more than once`, `want "host.ticket_federation" when tracker.sources is configured`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("semantic output missing %q:\n%s", want, joined)
		}
	}
	if schema := strings.Join(res.Schema, "\n"); !strings.Contains(schema, "/tracker/sources/0/provider") {
		t.Fatalf("schema output should reject a raw script path as a federated provider:\n%s", schema)
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

func TestValidate_LegacyProfileWarnsWithoutGoalsOrResolutions(t *testing.T) {
	profile := []byte(`schema: project-profile/v1
id: legacy
title: Legacy
repo:
  root: "."
  vcs: git
stack:
  kind: generic
commands:
  build: "true"
  test: "true"
kitsoki:
  story: dev-story
  instance:
    id: legacy-dev
    path: .kitsoki/stories/legacy-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.OK {
		t.Fatalf("legacy profile should remain valid: schema=%v semantic=%v", res.Schema, res.Semantic)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"goals is missing", "onboarding.resolutions is missing"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("legacy warnings missing %q:\n%s", want, joined)
		}
	}
}

func TestValidate_RejectsBrokenGoalAndResolutionContracts(t *testing.T) {
	profile := []byte(`schema: project-profile/v1
id: broken-contract
title: Broken Contract
repo:
  root: "."
  vcs: git
stack:
  kind: generic
commands:
  build: "true"
  test: "true"
kitsoki:
  story: dev-story
  instance:
    id: broken-contract-dev
    path: .kitsoki/stories/broken-contract-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
goals:
  onboarding:
    statement: Establish a measurable project setup.
    postconditions:
      - id: missing-reference
        statement: This points nowhere.
        gate: required
        verification: does-not-exist
      - id: weak-reference
        statement: This required outcome points at an advisory check.
        gate: required
        verification: advisory-check
onboarding:
  resolutions:
    - field: commands.test
      value: "true"
      source: discovered
    - field: commands.test
      value: "true"
      source: operator
    - field: repo.branch_pattern
      value: "feature/{slug}"
      source: default
setup_plan:
  writes:
    - path: .kitsoki/stories/broken-contract-dev/app.yaml
      action: create
      summary: Materialize the wrapper.
  verifications:
    - id: advisory-check
      kind: workflow
      command: "true"
      gate: advisory
`)
	res, err := Validate(profile, "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.OK {
		t.Fatal("broken goal and resolution contracts should fail semantic validation")
	}
	joined := strings.Join(res.Semantic, "\n")
	for _, want := range []string{
		`references unknown setup_plan.verifications id "does-not-exist"`,
		`is required but verification "advisory-check" is advisory`,
		`onboarding.resolutions field "commands.test" is declared more than once`,
		`onboarding.resolutions[2].notice is required`,
		`onboarding.resolutions[2].update is required`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("semantic output missing %q:\n%s", want, joined)
		}
	}
}

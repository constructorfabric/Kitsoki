package webconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootFromCWD walks up from the test's cwd (internal/webconfig) to the
// kitsoki module root so .kitsoki.yaml files written there resolve
// @kitsoki/dev-story (used by the world-key validation path).
func repoRootFromCWD(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func TestLoadRoot_Rung0_NoRootBlock(t *testing.T) {
	// A config with only harness-profile concerns (or none) ⇒ rung 0: Root is
	// nil and RootSpec() returns nil so SynthesizeRoot uses its default.
	dir := t.TempDir()
	path := filepath.Join(dir, ".kitsoki.yaml")
	if err := os.WriteFile(path, []byte("story_dirs: [./stories]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Root != nil {
		t.Fatalf("expected nil Root for rung-0 config, got %+v", cfg.Root)
	}
	if cfg.Root.RootSpec() != nil {
		t.Fatal("RootSpec() of nil Root must be nil")
	}
}

func TestLoadRoot_Rung1_Overrides(t *testing.T) {
	// World-key validation loads dev-story, so write the config under the repo
	// worktree where @kitsoki/dev-story resolves.
	root := repoRootFromCWD(t)
	dir, err := os.MkdirTemp(root, "wc-rung1-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, ".kitsoki.yaml")
	yaml := `root:
  import: dev-story
  overrides:
    bindings:
      transport: host.append_to_file
    world:
      judge_mode: llm_then_human
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load rung-1: %v", err)
	}
	if cfg.Root == nil || cfg.Root.Overrides == nil {
		t.Fatal("expected a populated Root.Overrides")
	}
	spec := cfg.Root.RootSpec()
	if spec.Bindings["transport"] != "host.append_to_file" {
		t.Fatalf("binding not carried into RootSpec: %+v", spec.Bindings)
	}
	if spec.World["judge_mode"] != "llm_then_human" {
		t.Fatalf("world override not carried into RootSpec: %+v", spec.World)
	}
}

func TestLoadRoot_ProjectProfile(t *testing.T) {
	root := repoRootFromCWD(t)
	dir, err := os.MkdirTemp(root, "wc-profile-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".kitsoki.yaml")
	if err := os.WriteFile(path, []byte("story_dirs: [./stories]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := `schema: project-profile/v1
repo:
  root: "."
commands:
  build: "make build"
  test: "make test"
kitsoki:
  instance:
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
  judge_mode: human
dev_story_profile:
  docs:
    publish_durable_path: docs/prd
    prd_doc_filename: PRD
    design_template_dir: docs/spec-templates
    design_durable_path: docs/designs
    design_doc_filename: DESIGN
    design_ticket_dir: ""
    ticket_repo: constructorfabric/Kitsoki
  bugfix:
    build_cmd: "make ci-build"
`
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "project-profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load profile root: %v", err)
	}
	spec := cfg.Root.RootSpec()
	if spec.Bindings["workspace"] != "host.git_worktree" {
		t.Fatalf("profile bindings not carried into RootSpec: %+v", spec.Bindings)
	}
	if spec.World["build_cmd"] != "make ci-build" {
		t.Fatalf("profile bugfix build_cmd should win over commands.build: %+v", spec.World)
	}
	if spec.World["test_cmd"] != "make test" {
		t.Fatalf("commands.test should seed test_cmd: %+v", spec.World)
	}
	if spec.World["design_doc_filename"] != "DESIGN" {
		t.Fatalf("docs profile not carried into RootSpec: %+v", spec.World)
	}
	if spec.World["ticket_repo"] != "constructorfabric/Kitsoki" {
		t.Fatalf("profile ticket_repo not carried into RootSpec: %+v", spec.World)
	}
}

func TestLoadRoot_ProjectProfileExplicitRootWins(t *testing.T) {
	root := repoRootFromCWD(t)
	dir, err := os.MkdirTemp(root, "wc-profile-override-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".kitsoki.yaml")
	cfgYAML := `story_dirs: [./stories]
root:
  overrides:
    world:
      test_cmd: "custom test"
      design_ticket_dir: "issues/features"
`
	if err := os.WriteFile(path, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := `schema: project-profile/v1
commands:
  test: "make test"
dev_story_profile:
  docs:
    design_ticket_dir: ""
`
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "project-profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load profile root override: %v", err)
	}
	spec := cfg.Root.RootSpec()
	if spec.World["test_cmd"] != "custom test" {
		t.Fatalf("explicit root override should win over profile test_cmd: %+v", spec.World)
	}
	if spec.World["design_ticket_dir"] != "issues/features" {
		t.Fatalf("explicit root override should win over profile docs: %+v", spec.World)
	}
}

func TestLoadRoot_FailFast(t *testing.T) {
	root := repoRootFromCWD(t)

	cases := []struct {
		name   string
		yaml   string
		errSub string
	}{
		{
			name:   "unknown import",
			yaml:   "root:\n  import: example-app\n",
			errSub: "not a known base story",
		},
		{
			name:   "unknown binding iface",
			yaml:   "root:\n  import: dev-story\n  overrides:\n    bindings:\n      frobnicate: host.x\n",
			errSub: "is not a host_interface",
		},
		{
			name:   "unknown world key",
			yaml:   "root:\n  import: dev-story\n  overrides:\n    world:\n      not_a_real_key: 1\n",
			errSub: "unknown key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp(root, "wc-fail-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, ".kitsoki.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = Load(path)
			if err == nil {
				t.Fatalf("expected fail-fast error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// repoRoot resolves the kitsoki worktree root so @kitsoki/dev-story resolves and
// emitted files written under it pick up the same go.mod marker.
func testRepoRoot(t *testing.T) string {
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
			t.Fatal("could not find repo root (go.mod) walking up from cwd")
		}
		dir = parent
	}
}

// normalize strips the load-provenance fields that legitimately differ between a
// synthesized def and a file-loaded one (path-rooted) plus the app metadata
// (title/id are cosmetic), so the structural fold result can be deep-compared.
func normalize(def *app.AppDef) {
	def.BaseDir = ""
	def.LoadedManifests = nil
	def.ImportWrappers = nil
	def.App = app.AppMeta{}
	// Hosts is the allow-list — semantically a set. The import fold unions
	// handler names via map iteration, so its slice order is nondeterministic;
	// sort before comparing so the round-trip asserts set-equality, not order.
	sort.Strings(def.Hosts)
}

func TestMaterializeRoundTrip(t *testing.T) {
	root := testRepoRoot(t)
	spec := &app.RootSpec{
		Bindings: map[string]string{"transport": "host.append_to_file"},
		World:    map[string]any{"judge_mode": "llm_then_human"},
	}

	// The synthesized (folded) def is the verification anchor.
	synth, err := app.SynthesizeRoot(spec, root)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	// Emit, write under the repo worktree so @kitsoki/dev-story resolves, reload.
	yamlBytes, err := emitRootYAML(spec, "materialize-roundtrip")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// Write under the repo root but OUTSIDE stories/ so findRepoRoot still
	// resolves @kitsoki/dev-story while cross-package stories/ walkers (which
	// copy stories/ in parallel) never race on this transient dir.
	outDir, err := os.MkdirTemp(root, "mat-rt-")
	if err != nil {
		t.Fatalf("mkdtemp under repo: %v", err)
	}
	defer os.RemoveAll(outDir)
	outPath := filepath.Join(outDir, "app.yaml")
	if err := os.WriteFile(outPath, yamlBytes, 0o644); err != nil {
		t.Fatalf("write emitted: %v", err)
	}

	loaded, err := app.Load(outPath)
	if err != nil {
		t.Fatalf("reload emitted app.yaml: %v\n---\n%s", err, string(yamlBytes))
	}

	normalize(synth)
	normalize(loaded)
	if !reflect.DeepEqual(synth, loaded) {
		t.Fatalf("emitted app.yaml does not round-trip to the synthesized def\nsynth root=%v states=%d world=%d hosts=%d\nloaded root=%v states=%d world=%d hosts=%d",
			synth.Root, len(synth.States), len(synth.World), len(synth.Hosts),
			loaded.Root, len(loaded.States), len(loaded.World), len(loaded.Hosts))
	}
}

func TestMaterializeEmitHasProvenanceHeaderAndDevStorySource(t *testing.T) {
	b, err := emitRootYAML(nil, "provtest")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.HasPrefix(string(b), "# materialized from .kitsoki.yaml root:") {
		t.Fatalf("expected provenance header, got:\n%s", string(b))
	}
	if !strings.Contains(string(b), "@kitsoki/dev-story") {
		t.Fatalf("expected dev-story import source in emitted yaml:\n%s", string(b))
	}
}

// TestMaterializeCmd_RoundTripAndRefuseOverwrite drives the actual command core
// (runMaterialize): a rung-0 .kitsoki.yaml (no root: block) materializes a
// loadable file, and a second run against the same slug refuses to overwrite.
func TestMaterializeCmd_RoundTripAndRefuseOverwrite(t *testing.T) {
	root := testRepoRoot(t)
	// Materialize's write root is a temp dir UNDER the repo root (not real
	// stories/): @kitsoki/dev-story still resolves via findRepoRoot, while the
	// transient .kitsoki/stories/<slug> it writes can't race parallel stories/
	// walkers.
	matRoot, err := os.MkdirTemp(root, "mat-cmd-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(matRoot)
	slug := "proj"

	// Empty config dir → rung 0 (no .kitsoki.yaml). Point --config at a
	// nonexistent path so Load returns the zero WebConfig.
	cfgPath := filepath.Join(t.TempDir(), ".kitsoki.yaml")

	outPath, err := runMaterialize(matRoot, cfgPath, slug)
	if err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	if _, statErr := os.Stat(outPath); statErr != nil {
		t.Fatalf("expected materialized file at %s: %v", outPath, statErr)
	}
	// It re-loaded clean inside runMaterialize; assert again for clarity.
	if _, loadErr := app.Load(outPath); loadErr != nil {
		t.Fatalf("materialized file should be loadable: %v", loadErr)
	}

	// Second run refuses to overwrite.
	if _, err := runMaterialize(matRoot, cfgPath, slug); err == nil {
		t.Fatal("expected refuse-overwrite error on second materialize")
	} else if !strings.Contains(err.Error(), "already") {
		t.Fatalf("expected an 'already materialized' error, got: %v", err)
	}
}

func TestMaterializeCmd_UsesProjectProfile(t *testing.T) {
	root := testRepoRoot(t)
	matRoot, err := os.MkdirTemp(root, "mat-profile-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(matRoot)
	if err := os.MkdirAll(filepath.Join(matRoot, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(matRoot, ".kitsoki.yaml")
	cfg := "project_profile: .kitsoki/project-profile.yaml\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	profile := `schema: project-profile/v1
repo:
  root: "."
commands:
  build: "npm run build"
  test: "npm test"
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
    design_template_dir: docs/proposals/templates
    design_durable_path: docs/proposals
    design_ticket_dir: ""
  bugfix:
    build_cmd: "npm run build"
`
	if err := os.WriteFile(filepath.Join(matRoot, ".kitsoki", "project-profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	outPath, err := runMaterialize(matRoot, cfgPath, "profile-proj")
	if err != nil {
		t.Fatalf("materialize with profile: %v", err)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	body := string(b)
	for _, want := range []string{
		"build_cmd:",
		`default: npm run build`,
		"test_cmd:",
		`default: npm test`,
		"design_template_dir:",
		"docs/proposals/templates",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("materialized profile app missing %q:\n%s", want, body)
		}
	}
}

// TestMaterializeCmd_AbortOnInvalidRoot proves materialize never writes a file
// when synthesis fails (a bad root: block): no partial is left behind.
func TestMaterializeCmd_AbortOnInvalidRoot(t *testing.T) {
	root := testRepoRoot(t)
	matRoot, err := os.MkdirTemp(root, "mat-abort-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(matRoot)
	slug := "proj"

	// A .kitsoki.yaml whose root.import is not the blessed base story fails
	// webconfig.Load — materialize aborts before touching the filesystem.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, ".kitsoki.yaml")
	if err := os.WriteFile(cfgPath, []byte("root:\n  import: not-dev-story\n"), 0o644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	if _, err := runMaterialize(matRoot, cfgPath, slug); err == nil {
		t.Fatal("expected materialize to fail on invalid root.import")
	}
	// No app.yaml should have been written under the slug.
	if _, statErr := os.Stat(filepath.Join(matRoot, ".kitsoki", "stories", slug, "app.yaml")); statErr == nil {
		t.Fatal("materialize left a partial app.yaml after an invalid root")
	}
}

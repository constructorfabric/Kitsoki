package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
)

// kitFixtureManifestV2 is kitFixtureManifest one minor version later with a
// renamed intent — the delta `kit update`'s plan should surface.
const kitFixtureManifestV2 = `app: { id: widget, version: 1.3.0 }
hosts: [host.run]
world:
  count: { type: int, default: 0 }
intents:
  nudge: { description: "bump count (renamed from bump)" }
root: idle
states:
  idle: { view: "x" }
`

// kitFixtureKitYAML declares widget as a kit with a compat.renamed hint for
// the v2 intent rename (story_dirs "." mirrors stories/dev-story/kit.yaml's
// beside-the-app layout).
const kitFixtureKitYAML = `schema: kit/v1
kit: widget
namespace: kitsoki
version: 1.3.0
provides:
  stories: [widget]
  story_dirs:
    widget: "."
compat:
  renamed:
    intents:
      bump: nudge
`

// writeConsumerInstance writes a minimal instance app importing the widget
// kit and lifting the (old-name) intent — the reference ScanRenameHints
// should detect.
func writeConsumerInstance(t *testing.T, projectRoot, source string) {
	t.Helper()
	dir := filepath.Join(projectRoot, ".kitsoki", "stories", "proj-dev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `app: { id: proj-dev, version: 0.1.0 }
imports:
  core:
    source: "` + source + `"
    entry: idle
    intents:
      import: [bump]
root: core
`
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stageWidgetUpdate locks a v1.2.0 widget kit from lib, mutates lib to
// v1.3.0 (with the kit.yaml rename hint), and returns (lib, target).
func stageWidgetUpdate(t *testing.T) (lib, target string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lib = t.TempDir()
	kitDir := filepath.Join(lib, "widget")
	writeFixtureKit(t, kitDir)
	target = t.TempDir()

	if out, err := runKit(t, "add", kitDir, "--name", "widget", "--version", "^1.0.0", "--target", target); err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}

	// Upstream releases v1.3.0: bump version, rename an intent, declare the
	// compat hint.
	if err := os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte(kitFixtureManifestV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kitDir, "kit.yaml"), []byte(kitFixtureKitYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConsumerInstance(t, target, kitDir)
	return lib, target
}

func TestKitUpdate_StagesCandidateWithPlan(t *testing.T) {
	lib, target := stageWidgetUpdate(t)
	_ = lib

	out, err := runKit(t, "update", "widget", "--target", target)
	if err != nil {
		t.Fatalf("kit update: %v\n%s", err, out)
	}

	// The accepted lockfile is untouched.
	lf, err := kitlock.Load(kitlock.Path(target))
	if err != nil {
		t.Fatal(err)
	}
	if lf.Kits["widget"].Version != "1.2.0" {
		t.Errorf("accepted lock version = %q, want 1.2.0 (update must not touch kits.lock)", lf.Kits["widget"].Version)
	}
	if lf.Kits["widget"].Constraint != "^1.0.0" {
		t.Errorf("lock constraint = %q, want the recorded ^1.0.0", lf.Kits["widget"].Constraint)
	}

	// The candidate is staged with a from-snapshot.
	sf, err := kitstage.Load(kitstage.Path(target))
	if err != nil {
		t.Fatal(err)
	}
	staged := sf.Kits["widget"]
	if staged == nil {
		t.Fatal("expected a staged widget candidate")
	}
	if staged.Version != "1.3.0" || staged.From.Version != "1.2.0" {
		t.Errorf("staged from/to = %q/%q, want 1.2.0/1.3.0", staged.From.Version, staged.Version)
	}
	if staged.TreeHash == "" || staged.TreeHash == staged.From.TreeHash {
		t.Errorf("staged tree hash should differ from accepted (%q vs %q)", staged.TreeHash, staged.From.TreeHash)
	}
	// The staged bytes are pinned: resolvable offline even after the source
	// mutates again.
	dir, err := kitstage.ResolveTree(staged)
	if err != nil {
		t.Fatalf("ResolveTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.yaml")); err != nil {
		t.Fatalf("pinned candidate tree lacks app.yaml: %v", err)
	}

	// The plan captures the delta and the applicable rename hint.
	plan, err := kitstage.LoadPlan(target, "widget")
	if err != nil || plan == nil {
		t.Fatalf("LoadPlan: %v (plan=%v)", err, plan)
	}
	var kinds []string
	for _, c := range plan.ChangedFiles {
		kinds = append(kinds, c.Kind+":"+c.Path)
	}
	joined := strings.Join(kinds, " ")
	if !strings.Contains(joined, "modified:app.yaml") || !strings.Contains(joined, "added:kit.yaml") {
		t.Errorf("plan changed files = %v, want modified:app.yaml and added:kit.yaml", kinds)
	}
	foundHint := false
	for _, h := range plan.RenameHints {
		if h.Category == "intents" && h.Old == "bump" && h.New == "nudge" && h.Detected != "" {
			foundHint = true
			if !strings.Contains(h.SuggestedAction, "bump") || !strings.Contains(h.SuggestedAction, "nudge") {
				t.Errorf("suggested action %q should name both intent names", h.SuggestedAction)
			}
		}
	}
	if !foundHint {
		t.Errorf("plan rename hints = %+v, want a detected intents bump->nudge hint", plan.RenameHints)
	}

	// stdout mentions the staged next steps and the hint.
	if !strings.Contains(out, "kit trial") || !strings.Contains(out, "bump -> nudge") {
		t.Errorf("update output = %q, want next-step hints and the rename hint", out)
	}

	// kit list surfaces the staged candidate.
	listOut, err := runKit(t, "list", "--target", target)
	if err != nil {
		t.Fatalf("kit list: %v", err)
	}
	if !strings.Contains(listOut, "staged:") || !strings.Contains(listOut, "1.3.0") {
		t.Errorf("kit list = %q, want a staged: 1.3.0 line", listOut)
	}
}

func TestKitUpdate_ConstraintGate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lib := t.TempDir()
	kitDir := filepath.Join(lib, "widget")
	writeFixtureKit(t, kitDir)
	target := t.TempDir()
	if out, err := runKit(t, "add", kitDir, "--name", "widget", "--version", "^1.0.0", "--target", target); err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}

	// Upstream jumps to 2.0.0 — outside the recorded ^1.0.0.
	if err := os.WriteFile(filepath.Join(kitDir, "app.yaml"),
		[]byte(strings.Replace(kitFixtureManifest, "1.2.0", "2.0.0", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runKit(t, "update", "widget", "--target", target); err == nil {
		t.Fatalf("expected the recorded constraint to reject 2.0.0, got:\n%s", out)
	}
	if kitstage.Exists(kitstage.Path(target)) {
		t.Error("a gated-out update must stage nothing")
	}

	// --to overrides the gate for a deliberate major bump.
	if out, err := runKit(t, "update", "widget", "--target", target, "--to", "^2.0.0"); err != nil {
		t.Fatalf("kit update --to ^2.0.0: %v\n%s", err, out)
	}
	sf, err := kitstage.Load(kitstage.Path(target))
	if err != nil || sf.Kits["widget"] == nil {
		t.Fatalf("expected staged candidate after --to override (err=%v)", err)
	}
	if sf.Kits["widget"].Version != "2.0.0" {
		t.Errorf("staged version = %q, want 2.0.0", sf.Kits["widget"].Version)
	}
}

func TestKitUpdate_AlreadyUpToDate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lib := t.TempDir()
	kitDir := filepath.Join(lib, "widget")
	writeFixtureKit(t, kitDir)
	target := t.TempDir()
	if out, err := runKit(t, "add", kitDir, "--name", "widget", "--target", target); err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}

	out, err := runKit(t, "update", "widget", "--target", target)
	if err != nil {
		t.Fatalf("kit update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("update output = %q, want already up to date", out)
	}
	if kitstage.Exists(kitstage.Path(target)) {
		t.Error("an up-to-date update must stage nothing")
	}
}

func TestKitUpdate_CheckOnlyStagesNothing(t *testing.T) {
	_, target := stageWidgetUpdate(t)

	out, err := runKit(t, "update", "widget", "--target", target, "--check-only")
	if err != nil {
		t.Fatalf("kit update --check-only: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing staged") {
		t.Errorf("check-only output = %q, want nothing staged note", out)
	}
	if kitstage.Exists(kitstage.Path(target)) {
		t.Error("--check-only must not stage")
	}
	if plan, _ := kitstage.LoadPlan(target, "widget"); plan != nil {
		t.Error("--check-only must not write plan.yaml")
	}
}

func TestKitUpdate_RequiresLockedEntry(t *testing.T) {
	target := t.TempDir()
	if _, err := runKit(t, "update", "widget", "--target", target); err == nil {
		t.Fatal("expected an error for a project with no lockfile")
	}
}

func TestKitReject_RemovesAllResidue(t *testing.T) {
	_, target := stageWidgetUpdate(t)
	if out, err := runKit(t, "update", "widget", "--target", target); err != nil {
		t.Fatalf("kit update: %v\n%s", err, out)
	}

	out, err := runKit(t, "reject", "widget", "--target", target)
	if err != nil {
		t.Fatalf("kit reject: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rejected staged widget@1.3.0") {
		t.Errorf("reject output = %q", out)
	}
	if kitstage.Exists(kitstage.Path(target)) {
		t.Error("staged lockfile should be gone after reject")
	}
	if _, err := os.Stat(kitstage.WorkDir(target, "widget")); !os.IsNotExist(err) {
		t.Error("kit-update workdir should be gone after reject")
	}
	// Only the accepted lock (+ the consumer instance fixture) remain under
	// .kitsoki — reject leaves no staging residue.
	entries, err := os.ReadDir(filepath.Join(target, ".kitsoki"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != kitlock.FileName && e.Name() != "stories" {
			t.Errorf("unexpected residue under .kitsoki after reject: %s", e.Name())
		}
	}

	// Rejecting again is an explicit error (nothing staged).
	if _, err := runKit(t, "reject", "widget", "--target", target); err == nil {
		t.Fatal("expected an error rejecting when nothing is staged")
	}
}

// TestKitUpdate_GitSource_ToRef proves the git tier end-to-end: --to
// rewrites the source ref, the candidate pins the new commit, and reject
// restores a clean project — all against a local git remote.
func TestKitUpdate_GitSource_ToRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	remote := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(remote, "app.yaml"), []byte(kitFixtureManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "app.yaml")
	run("commit", "-q", "-m", "v1")
	run("tag", "v1.2.0")
	if err := os.WriteFile(filepath.Join(remote, "app.yaml"), []byte(kitFixtureManifestV2), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "app.yaml")
	run("commit", "-q", "-m", "v2")
	run("tag", "v1.3.0")

	target := t.TempDir()
	if out, err := runKit(t, "add", "git+"+remote+"@v1.2.0", "--name", "widget", "--target", target); err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}

	out, err := runKit(t, "update", "widget", "--target", target, "--to", "v1.3.0")
	if err != nil {
		t.Fatalf("kit update --to v1.3.0: %v\n%s", err, out)
	}
	sf, err := kitstage.Load(kitstage.Path(target))
	if err != nil {
		t.Fatal(err)
	}
	staged := sf.Kits["widget"]
	if staged == nil || staged.Version != "1.3.0" {
		t.Fatalf("staged = %+v, want a 1.3.0 candidate", staged)
	}
	if staged.Commit == "" || staged.Commit == staged.From.Commit {
		t.Errorf("staged commit %q should be a new pin (from %q)", staged.Commit, staged.From.Commit)
	}
	if !strings.Contains(staged.Source, "@v1.3.0") {
		t.Errorf("staged source = %q, want the rewritten @v1.3.0 ref", staged.Source)
	}
	// Offline pinned-tree resolution via the commit cache.
	if _, err := kitstage.ResolveTree(staged); err != nil {
		t.Fatalf("ResolveTree: %v", err)
	}
	// Plan diffs the two commits' trees.
	plan, err := kitstage.LoadPlan(target, "widget")
	if err != nil || plan == nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if len(plan.ChangedFiles) != 1 || plan.ChangedFiles[0].Path != "app.yaml" || plan.ChangedFiles[0].Kind != "modified" {
		t.Errorf("plan changed files = %+v, want exactly modified app.yaml", plan.ChangedFiles)
	}

	if out, err := runKit(t, "reject", "widget", "--target", target); err != nil {
		t.Fatalf("kit reject: %v\n%s", err, out)
	}
}

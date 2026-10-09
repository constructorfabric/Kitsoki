package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestGoalSeekerResolveWorkdir_FromRuntimeBlock is the regression guard for
// elegance item 1 ("workdir from goal, no operator seed"): opening the
// goal-seeker for a goal must need ZERO workdir/worktree seed. The goal
// DECLARES its execution context once, in a top-level `runtime:` block of its
// decomposition.yaml, and bootstrap's gs_resolve.star reads it.
//
// The bug it guards: world.workdir defaults to "." → the inspector roots at the
// MCP process cwd (the MAIN checkout), but a goal's docs/goals/<slug>/
// decomposition.yaml lives only in the goal's git worktree (.worktrees/<name>/),
// so gs_lint fails "no decomposition.yaml at …" unless an operator hand-seeds an
// absolute workdir. This test constructs exactly that layout (a decomposition
// present only under a sibling .worktrees/<name>/, absent under the root) and
// proves:
//   - gs_resolve, with workdir left at the default ".", locates the worktree via
//     the `.worktrees/*` glob and binds workdir + main_worktree_path to it and
//     base_branch from the runtime block; AND
//   - gs_lint then passes rooted at the RESOLVED workdir but FAILS rooted at "."
//     — i.e. the seed genuinely was required before, and is not anymore.
func TestGoalSeekerResolveWorkdir_FromRuntimeBlock(t *testing.T) {
	t.Setenv(AppDirEnv, "")
	resolveScript, lintScript := goalSeekerScriptPaths(t)

	// Build a fake main checkout whose ONLY copy of the goal lives in a sibling
	// worktree — decomposition.yaml is deliberately absent at <root>/docs/goals/gu.
	root := t.TempDir()
	const (
		goalDir      = "docs/goals/gu"
		worktreeName = "gu"
		baseBranch   = "stabilize/gu"
	)
	worktreeRel := filepath.Join(".worktrees", worktreeName)
	decompDir := filepath.Join(root, worktreeRel, goalDir)
	if err := os.MkdirAll(decompDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A minimal, lint-clean decomposition carrying the declared runtime block.
	decomp := "goal_slug: gu\n" +
		"runtime:\n" +
		"  base_branch: " + baseBranch + "\n" +
		"  worktree_path: " + filepath.ToSlash(worktreeRel) + "\n" +
		"changes:\n" +
		"  - id: \"C1\"\n" +
		"    wall: WT\n" +
		"    goal_ref: [G1]\n" +
		"    title: \"c1\"\n" +
		"    pipeline: implementation\n" +
		"    scope: [\"stories/goal-seeker/**\"]\n" +
		"    depends_on: []\n" +
		"    acceptance: [\"it lands\"]\n" +
		"    gate: { class: G-FLOW, cmd: \"true\" }\n"
	if err := os.WriteFile(filepath.Join(decompDir, "decomposition.yaml"), []byte(decomp), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run everything as if the MCP process cwd IS the main checkout root.
	chdir(t, root)

	// ── 1. Baseline: WITHOUT resolution (workdir="."), gs_lint cannot find the
	// decomposition — this is the failure the operator seed used to paper over.
	baseRoute := runGSLint(t, lintScript, ".", goalDir)
	if baseRoute != "fail" {
		t.Fatalf("baseline gs_lint rooted at %q: route=%q, want fail (decomposition absent under root ⇒ seed was required)", ".", baseRoute)
	}

	// ── 2. gs_resolve with workdir at its default "." derives the context.
	res, err := StarlarkRunHandler(
		WithWorldSnapshot(context.Background(), map[string]any{"workdir": "."}),
		map[string]any{
			"script":       resolveScript,
			"capabilities": starlarkTestFSCapabilities(),
			"inputs": map[string]any{
				"goal_dir":           goalDir,
				"workdir":            ".",
				"main_worktree_path": ".",
				"base_branch":        "main",
			},
		},
	)
	if err != nil {
		t.Fatalf("gs_resolve: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("gs_resolve domain error: %s", res.Error)
	}
	if got, want := res.Data["resolved"], "worktree"; got != want {
		t.Fatalf("resolved=%v, want %q", got, want)
	}
	if got := res.Data["workdir"]; got != filepath.ToSlash(worktreeRel) {
		t.Fatalf("workdir=%v, want %q (should resolve to the goal's worktree with no seed)", got, worktreeRel)
	}
	if got := res.Data["main_worktree_path"]; got != filepath.ToSlash(worktreeRel) {
		t.Fatalf("main_worktree_path=%v, want %q", got, worktreeRel)
	}
	if got := res.Data["base_branch"]; got != baseBranch {
		t.Fatalf("base_branch=%v, want %q (from the runtime block)", got, baseBranch)
	}

	// ── 3. gs_lint rooted at the RESOLVED workdir now passes — proving the seed
	// is no longer needed to open the goal-seeker for this goal.
	resolvedWorkdir, _ := res.Data["workdir"].(string)
	if route := runGSLint(t, lintScript, resolvedWorkdir, goalDir); route != "ok" {
		t.Fatalf("gs_lint rooted at resolved workdir %q: route=%q, want ok", resolvedWorkdir, route)
	}
}

// TestGoalSeekerResolveWorkdir_RuntimeWorktreeBeatsDirect guards the mixed
// launcher case: if the process root has a copy of decomposition.yaml but the
// declared runtime.worktree_path also points at a visible checkout carrying the
// goal, the declared worktree is the execution context. This can happen after a
// branch/plan has been copied into the primary checkout; opening the
// goal-seeker should still drive the goal's own worktree, not whatever checkout
// happened to launch the process.
func TestGoalSeekerResolveWorkdir_RuntimeWorktreeBeatsDirect(t *testing.T) {
	t.Setenv(AppDirEnv, "")
	resolveScript, _ := goalSeekerScriptPaths(t)

	root := t.TempDir()
	const (
		goalDir      = "docs/goals/gu"
		worktreeName = "gu"
		baseBranch   = "stabilize/gu"
	)
	worktreeRel := filepath.Join(".worktrees", worktreeName)
	directDir := filepath.Join(root, goalDir)
	worktreeDir := filepath.Join(root, worktreeRel, goalDir)
	if err := os.MkdirAll(directDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	decomp := "goal_slug: gu\n" +
		"runtime:\n" +
		"  base_branch: " + baseBranch + "\n" +
		"  worktree_path: " + filepath.ToSlash(worktreeRel) + "\n" +
		"changes: []\n"
	for _, dir := range []string{directDir, worktreeDir} {
		if err := os.WriteFile(filepath.Join(dir, "decomposition.yaml"), []byte(decomp), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	chdir(t, root)

	res, err := StarlarkRunHandler(
		WithWorldSnapshot(context.Background(), map[string]any{"workdir": "."}),
		map[string]any{
			"script":       resolveScript,
			"capabilities": starlarkTestFSCapabilities(),
			"inputs": map[string]any{
				"goal_dir":           goalDir,
				"workdir":            ".",
				"main_worktree_path": ".",
				"base_branch":        "main",
			},
		},
	)
	if err != nil {
		t.Fatalf("gs_resolve: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("gs_resolve domain error: %s", res.Error)
	}
	if got, want := res.Data["resolved"], "runtime_worktree"; got != want {
		t.Fatalf("resolved=%v, want %q", got, want)
	}
	if got := res.Data["workdir"]; got != filepath.ToSlash(worktreeRel) {
		t.Fatalf("workdir=%v, want declared runtime worktree %q", got, worktreeRel)
	}
	if got := res.Data["main_worktree_path"]; got != filepath.ToSlash(worktreeRel) {
		t.Fatalf("main_worktree_path=%v, want %q", got, worktreeRel)
	}
	if got := res.Data["base_branch"]; got != baseBranch {
		t.Fatalf("base_branch=%v, want %q", got, baseBranch)
	}
}

// TestGoalSeekerResolveWorkdir_SeedWins verifies an explicit operator workdir
// override always wins: gs_resolve echoes it back untouched and does not
// re-derive from disk (principle of least surprise — a deliberate seed is not
// clobbered by the default).
func TestGoalSeekerResolveWorkdir_SeedWins(t *testing.T) {
	resolveScript, _ := goalSeekerScriptPaths(t)
	res, err := StarlarkRunHandler(
		WithWorldSnapshot(context.Background(), map[string]any{}),
		map[string]any{
			"script":       resolveScript,
			"capabilities": starlarkTestFSCapabilities(),
			"inputs": map[string]any{
				"goal_dir":           "docs/goals/gu",
				"workdir":            "/abs/seeded/worktree",
				"main_worktree_path": "/abs/seeded/worktree",
				"base_branch":        "seeded-branch",
			},
		},
	)
	if err != nil {
		t.Fatalf("gs_resolve: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("gs_resolve domain error: %s", res.Error)
	}
	if got, want := res.Data["resolved"], "seeded"; got != want {
		t.Fatalf("resolved=%v, want %q", got, want)
	}
	if got := res.Data["workdir"]; got != "/abs/seeded/worktree" {
		t.Fatalf("workdir=%v, want the seeded override untouched", got)
	}
	if got := res.Data["base_branch"]; got != "seeded-branch" {
		t.Fatalf("base_branch=%v, want the seeded value untouched", got)
	}
}

// runGSLint runs the real gs_lint.star rooted at workdir and returns its route.
func runGSLint(t *testing.T, lintScript, workdir, goalDir string) string {
	t.Helper()
	res, err := StarlarkRunHandler(
		WithWorldSnapshot(context.Background(), map[string]any{"workdir": workdir}),
		map[string]any{
			"script":       lintScript,
			"capabilities": starlarkTestFSCapabilities(),
			"inputs":       map[string]any{"goal_dir": goalDir},
		},
	)
	if err != nil {
		t.Fatalf("gs_lint: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("gs_lint domain error: %s", res.Error)
	}
	route, _ := res.Data["route"].(string)
	return route
}

// goalSeekerScriptPaths returns absolute paths to the real committed
// gs_resolve.star and gs_lint.star, resolved from the repo root (two levels up
// from this package dir) BEFORE any chdir.
func goalSeekerScriptPaths(t *testing.T) (resolve, lint string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join(cwd, "..", "..")
	scripts := filepath.Join(repoRoot, "stories", "goal-seeker", "scripts")
	resolve = filepath.Join(scripts, "gs_resolve.star")
	lint = filepath.Join(scripts, "gs_lint.star")
	for _, p := range []string{resolve, resolve + ".yaml", lint, lint + ".yaml"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected script/sidecar at %s: %v", p, err)
		}
	}
	return resolve, lint
}

// chdir switches the process cwd for the duration of the test and restores it
// afterwards (mirrors the pattern in starlark_run_inspector_test.go).
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

package main

// resolver_bug1187_repro_test.go — reproducer for bf-1187:
// "studio session.new/story.graph cannot load a downstream root story that
// imports @kitsoki/dev-story — --stories-dir override hard-errors instead of
// falling back to embedded library / $KITSOKI_REPO".
//
// BUG: studioImportResolver (cmd/kitsoki/mcp.go) builds a wrapper around the
// base resolver that, when --stories-dir is set and override=true (the
// explicit-repo check), hard-errors for ANY story not found in storiesDir
// — even @kitsoki/* library stories like dev-story that should be resolved
// from $KITSOKI_REPO or the embedded library. The error looks like:
//
//	--stories-dir=<dir>: story "dev-story" not found (looked for <dir>/dev-story/app.yaml): ...
//
// The correct behaviour: when the story is not in storiesDir, the resolver
// falls through to the base resolver (which checks $KITSOKI_REPO, kitdev
// overrides, and ultimately the embedded library) by returning ("", nil) or
// delegating to base() instead of returning a hard error.
//
// This test asserts the end-to-end OUTCOME: app.LoadWithResolver on a
// downstream story that imports @kitsoki/dev-story SUCCEEDS when
// --stories-dir is set but does not contain dev-story.  It uses
// $KITSOKI_REPO pointing at this checkout so the fix's fall-through path
// resolves dev-story from the repo (no embedded-library staging required).
//
// Status: RED before the fix (fails with "--stories-dir=… story not found"),
//         GREEN after the fix (story loads via KITSOKI_REPO fall-through).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/kitrepo"
)

// TestStudioImportResolver_StoriesDirFallsThruForLibraryStory is the bf-1187
// end-to-end reproducer.
//
// Scenario: a user runs `kitsoki mcp --stories-dir /path/to/my/stories` where
// their project stories do NOT include dev-story (it lives in the kitsoki
// library). They create a downstream wrapper story that imports
// @kitsoki/dev-story. When they call session.new or story.graph with that
// downstream story, app.LoadWithResolver fires and calls
// studioImportResolver(storiesDir)("dev-story", …, override=true).
//
// Expected (after fix): the resolver returns ("", nil) or delegates to the
// base resolver, allowing resolveImportSource to fall through to $KITSOKI_REPO
// or the embedded library and load the story successfully.
//
// Actual (before fix): the resolver returns a hard error containing
// "--stories-dir=… story not found", and app.LoadWithResolver propagates it
// as a terminal failure — the session never opens.
func TestStudioImportResolver_StoriesDirFallsThruForLibraryStory(t *testing.T) {
	// Point KITSOKI_REPO at this kitsoki checkout so the fixed code can
	// resolve dev-story from the repo without needing the embedded library to
	// be staged.  The key invariant: dev-story MUST NOT be in storiesDir — only
	// in $KITSOKI_REPO — so the resolver is forced through the fall-through path.
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)
	t.Setenv(kitrepo.EnvVar, repoRoot)
	// No kit-dev override for dev-story.
	t.Setenv("KITSOKI_KIT_DEV_DEV_STORY", "")

	// storiesDir contains a user project story but NOT dev-story.
	storiesDir := t.TempDir()
	customDir := filepath.Join(storiesDir, "my-project")
	require.NoError(t, os.MkdirAll(customDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(customDir, "app.yaml"),
		[]byte("app:\n  id: my-project\n  version: \"1\"\nroot: idle\nworld: {}\nstates:\n  idle:\n    description: Idle\n    terminal: true\n"),
		0o644,
	))

	// The downstream story imports @kitsoki/dev-story.  It lives in a temp dir
	// outside the kitsoki checkout, so on-disk discovery (findRepoRoot) will
	// fail and the resolver must fall through to KITSOKI_REPO.
	downstreamDir := t.TempDir()
	downstreamApp := filepath.Join(downstreamDir, "app.yaml")
	require.NoError(t, os.WriteFile(downstreamApp, []byte(`app:
  id: my-downstream
  version: "1"
imports:
  core:
    source: "@kitsoki/dev-story"
root: main
states:
  main:
    view: "downstream"
`), 0o644))

	// Build the MCP import resolver exactly as `kitsoki mcp --stories-dir=...`
	// does (mcp.go: studioImportResolver).
	resolver := studioImportResolver(storiesDir)

	// End-to-end assertion: loading the downstream story must succeed.
	// With the bug, this fails with:
	//   imports.core: source "@kitsoki/dev-story": --stories-dir=<dir>: story "dev-story" not found (looked for <dir>/dev-story/app.yaml): ...
	// After the fix, the resolver falls through to KITSOKI_REPO and loads dev-story
	// from this checkout.
	def, loadErr := app.LoadWithResolver(downstreamApp, nil, resolver)
	require.NoError(t, loadErr,
		"downstream story importing @kitsoki/dev-story must load when --stories-dir "+
			"is set but does not contain dev-story; the resolver must fall through to "+
			"KITSOKI_REPO instead of hard-erroring")
	require.NotNil(t, def, "loaded AppDef must not be nil")
	// dev-story folds under alias 'core'; its states are namespaced to core.*.
	require.Contains(t, def.States, "core",
		"dev-story must be folded under the 'core' alias in the loaded def")
}

// TestStudioImportResolver_StoriesDirStillOverridesWhenPresent confirms that the
// fix does NOT break the intended behaviour: a story that IS in storiesDir
// should still be returned from storiesDir (not from KITSOKI_REPO).
//
// This is the complementary GREEN-after-fix gate alongside the RED reproducer
// above — ensuring the fall-through only fires for missing stories, not all.
func TestStudioImportResolver_StoriesDirStillOverridesWhenPresent(t *testing.T) {
	// Put a local "my-story" in storiesDir.
	storiesDir := t.TempDir()
	localDir := filepath.Join(storiesDir, "my-story")
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	localApp := filepath.Join(localDir, "app.yaml")
	require.NoError(t, os.WriteFile(localApp,
		[]byte("app:\n  id: my-story\n  version: \"1\"\nroot: idle\nworld: {}\nstates:\n  idle:\n    description: Idle\n    terminal: true\n"),
		0o644,
	))

	resolver := studioImportResolver(storiesDir)

	// "my-story" is IN storiesDir — must return the storiesDir path directly.
	got, err := resolver("my-story", "", true)
	require.NoError(t, err)
	require.Equal(t, localApp, got,
		"a story present in storiesDir must be resolved from storiesDir (not fallen through)")
}

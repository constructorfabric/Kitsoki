package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// TestStudioHarnessBuilder_LiveOpensWithoutDirectAPIKey locks the unlock that
// makes MCP-first maker dogfooding work on subscription auth alone: the studio's
// live routing harness is the claude-CLI harness (like `kitsoki web`), so a live
// session opens with NO direct-API ANTHROPIC_* credential. Before this it built
// the SDK LiveHarness, which hard-failed without a key — blocking
// session.new(harness:live) on an OAuth-only machine even though the maker only
// needs explicit-intent driving plus profile-routed host.agent dispatch.
func TestStudioHarnessBuilder_LiveOpensWithoutDirectAPIKey(t *testing.T) {
	// Empty the direct-API env so a regression to the SDK path would fail here.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	h, err := studioHarnessBuilder(studio.HarnessLive, "", "../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err, "a live session must open on subscription auth without a direct-API key")
	require.NotNil(t, h)
	_ = h.Close()
}

// TestStudioHarnessBuilder_ReplayUnchanged confirms the non-live path still
// delegates to the in-package default (no-LLM replay), untouched by the live
// change.
func TestStudioHarnessBuilder_ReplayUnchanged(t *testing.T) {
	// Replay with no recording is a fail-fast error from the default builder —
	// proving the replay branch is reached (not the live one).
	_, err := studioHarnessBuilder(studio.HarnessReplay, "", "")
	require.Error(t, err)
}

func TestStudioImportResolverStoriesDir(t *testing.T) {
	storiesDir := t.TempDir()
	storyDir := filepath.Join(storiesDir, "child")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	appPath := filepath.Join(storyDir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte("app:\n  id: child\n  version: \"1\"\nroot: idle\nworld: {}\nstates:\n  idle:\n    description: Idle\n    terminal: true\n"), 0o644))

	resolver := studioImportResolver(storiesDir)

	// A story present in storiesDir resolves from there.
	got, err := resolver("child", "", true)
	require.NoError(t, err)
	require.Equal(t, appPath, got)

	// A story absent from storiesDir must NOT hard-error with a --stories-dir
	// message; instead the resolver delegates to base so that $KITSOKI_REPO and
	// the embedded library can satisfy @kitsoki/* imports (e.g. dev-story).
	// Clear KITSOKI_REPO so the base returns ("",nil) — "no override set, fall
	// through" — giving us a hermetic result independent of the local checkout.
	t.Setenv("KITSOKI_REPO", "")
	path, err := resolver("missing", "", true)
	require.NoError(t, err, "absent story must not error with --stories-dir message; base says no override configured")
	require.Empty(t, path, "base returns empty path when no override is configured")
}

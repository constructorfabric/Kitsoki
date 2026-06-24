package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/webconfig"
)

// TestWebCmd_RejectsPositionalArg proves the multi-story `kitsoki web` no longer
// accepts a positional <app.yaml>: it starts story-less and discovers stories
// from the configured dirs. Cobra's NoArgs validator rejects any positional
// before RunE ever runs (so no server is bound).
func TestWebCmd_RejectsPositionalArg(t *testing.T) {
	cmd := webCmd()
	require.NotNil(t, cmd.Args, "web should declare an Args validator")
	// NoArgs accepts zero positionals and rejects one.
	require.NoError(t, cmd.Args(cmd, []string{}))
	require.Error(t, cmd.Args(cmd, []string{"stories/prd/app.yaml"}),
		"web must reject a positional app.yaml (story-less startup)")
}

// TestWebCmd_RepeatableStoriesDir proves --stories-dir is repeatable: parsing
// two of them yields both values, which webconfig.Resolve then takes over the
// .kitsoki.yaml story_dirs.
func TestWebCmd_RepeatableStoriesDir(t *testing.T) {
	cmd := webCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"--stories-dir", "stories",
		"--stories-dir", "testdata/apps",
	}))
	got, err := cmd.Flags().GetStringArray("stories-dir")
	require.NoError(t, err)
	assert.Equal(t, []string{"stories", "testdata/apps"}, got)
}

// TestWebCmd_DeterministicFlags proves the no-LLM flags survive the rewrite:
// both --flow and --host-cassette are still registered so a Playwright demo can
// run every session with no LLM. (The threading of these into runtimeBase is
// exercised by TestWebStartup_WiresStoriesAndDeterministicBase below.)
func TestWebCmd_DeterministicFlags(t *testing.T) {
	cmd := webCmd()
	for _, name := range []string{"flow", "host-cassette", "config", "stories-dir", "addr", "mode", "db", "harness", "claude-model", "recording", "record"} {
		assert.NotNilf(t, cmd.Flags().Lookup(name), "web must keep the --%s flag", name)
	}
}

// TestWebStartup_WiresStoriesAndDeterministicBase exercises the startup wiring
// the RunE performs short of binding the HTTP server: a temp .kitsoki.yaml +
// stories dir resolve to a registry whose seeded catalogue lists the story, and
// a session started from that story runs with NO LLM (the deterministic flow
// posture threaded through runtimeBase). This is the smoke test from the brief's
// test_plan — no server bind needed, no LLM.
func TestWebStartup_WiresStoriesAndDeterministicBase(t *testing.T) {
	// A stories dir holding one valid minimal story (helpers in registry_test.go).
	storiesDir := t.TempDir()
	storyDir := filepath.Join(storiesDir, "mini")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	appPath := filepath.Join(storyDir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(minimalStory), 0o644))
	absAppPath, err := filepath.Abs(appPath)
	require.NoError(t, err)

	// A .kitsoki.yaml pointing story_dirs at that dir — the same Load/Resolve the
	// entrypoint runs.
	configPath := filepath.Join(t.TempDir(), webconfig.DefaultConfigFile)
	require.NoError(t, os.WriteFile(configPath, []byte("story_dirs:\n  - "+storiesDir+"\n"), 0o644))

	cfg, err := webconfig.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, []string{storiesDir}, cfg.StoryDirs)

	// No --stories-dir flag override, so resolution falls through to the config.
	dirs := webconfig.Resolve(nil, cfg)
	require.Equal(t, []string{storiesDir}, dirs)

	// deterministicBase (registry_test.go) is the no-LLM flow posture the
	// entrypoint builds from --flow / --host-cassette.
	reg := NewRegistry(cfg, dirs, deterministicBase(t))
	t.Cleanup(reg.Close)

	// Seed the catalogue exactly as the entrypoint's registry.Rescan() does.
	stories, err := reg.Rescan()
	require.NoError(t, err)
	require.Len(t, stories, 1)
	assert.Equal(t, absAppPath, stories[0].Path)
	assert.Equal(t, "mini-story", stories[0].AppID)
	assert.Equal(t, "Mini Story", stories[0].Title)
	assert.Empty(t, stories[0].ActiveSessions)

	// And a session started from the discovered story runs deterministically
	// (no harness, no LLM): NewSession succeeds and routes.
	id, err := reg.NewSession(context.Background(), stories[0].Path)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	_, ok := reg.Get(id)
	assert.True(t, ok, "the new session must be routable by its id")

	// The story's active-session count reflects the live session.
	after := reg.ListStories()
	require.Len(t, after, 1)
	assert.Equal(t, []string{id}, after[0].ActiveSessions)
}

// TestWebStartup_FlagDirsOverrideConfig proves the resolution precedence the
// entrypoint relies on: repeatable --stories-dir overrides .kitsoki.yaml's
// story_dirs.
func TestWebStartup_FlagDirsOverrideConfig(t *testing.T) {
	cfg := webconfig.WebConfig{StoryDirs: []string{"from-config"}}
	dirs := webconfig.Resolve([]string{"from-flag-a", "from-flag-b"}, cfg)
	assert.Equal(t, []string{"from-flag-a", "from-flag-b"}, dirs)
}

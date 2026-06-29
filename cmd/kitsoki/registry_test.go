package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// deterministicBase returns a runtimeBase in the NO-LLM flow posture: an empty
// flow fixture carries no host_handlers (cloak has no host calls) so
// buildSessionRuntime builds a nil-harness, stub-backed sessionRuntime. The DB
// path is a per-test temp file so each test is isolated. This is the same
// deterministic posture a Playwright demo drives the live web UI with.
func deterministicBase(t *testing.T) runtimeBase {
	t.Helper()
	return runtimeBase{
		DBPath:   filepath.Join(t.TempDir(), "sessions.db"),
		ExecMode: orchestrator.ExecStaged,
		Flow:     &testrunner.FlowFixture{},
	}
}

// minimalStory is a self-contained, host-free, template-free story: string
// views (no `extends`), no intent includes, no host calls. It loads and renders
// from any temp dir, so tests can mutate it on disk and exercise Reload's
// re-render without dragging sibling .pongo / intents/*.yaml files along. Its
// root state is `foyer` (so Reload tests can assert prev-state-exists semantics).
const minimalStory = `app:
  id: mini-story
  version: 0.1.0
  title: "Mini Story"
world:
  visited: { type: bool, default: false }
intents:
  go:
    title: "Go"
    description: "Move to the hall."
    examples: ["go", "move"]
root: foyer
states:
  foyer:
    view: "You are in the foyer."
    on:
      go:
        - target: hall
  hall:
    view: "You are in the hall."
`

const terminalQuitStory = `app:
  id: quit-story
  version: 0.1.0
  title: "Quit Story"
world: {}
intents:
  quit:
    title: "Quit"
    description: "End the session."
    examples: ["quit"]
root: active
states:
  active:
    view: "Running."
    on:
      quit:
        - target: ended
  ended:
    view: "Done."
    terminal: true
`

// writeStory writes body to <tempDir>/<name>/app.yaml and returns the stories
// dir (the temp root) and the absolute app.yaml path. Tests that mutate the
// story (Reload) own this private copy.
func writeStory(t *testing.T, name string, body []byte) (storiesDir, appPath string) {
	t.Helper()
	storiesDir = t.TempDir()
	dir := filepath.Join(storiesDir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	appPath = filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, body, 0o644))
	abs, err := filepath.Abs(appPath)
	require.NoError(t, err)
	return storiesDir, abs
}

// TestRegistry_NewSessionRoundTrip proves NewSession builds a deterministic
// sessionRuntime (no LLM), returns a routable UUID, and List/Get/ListStories
// reflect the live session — including the active-session count on its story.
func TestRegistry_NewSessionRoundTrip(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	// Seed the catalogue so ListStories can map the new session onto its story.
	_, err := reg.Rescan()
	require.NoError(t, err)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)
	require.NotEmpty(t, sid)

	// Get routes to a real entry exposing the live Source + Driver.
	entry, ok := reg.Get(sid)
	require.True(t, ok, "the returned id must resolve via Get")
	require.NotNil(t, entry.Source)
	require.NotNil(t, entry.Driver)

	snap, err := entry.Source.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, "mini-story", snap.App.App.ID)
	assert.Equal(t, "foyer", snap.Session.CurrentState, "fresh session starts in the root state")

	// List returns one header for the live session.
	list := reg.List()
	require.Len(t, list, 1)
	assert.Equal(t, "mini-story", list[0].AppID)
	// The listed SessionID must be the registry's routable id (what Get/Open
	// navigate on), not the orchestrator's internal snapshot id.
	assert.Equal(t, sid, list[0].SessionID, "sessions.list must report the routable id")
	_, ok = reg.Get(list[0].SessionID)
	assert.True(t, ok, "the listed id must resolve via Get")

	// ListStories reflects the active session on the story.
	stories := reg.ListStories()
	require.Len(t, stories, 1)
	assert.Equal(t, appPath, stories[0].Path)
	assert.Equal(t, "Mini Story", stories[0].Title)
	assert.Equal(t, []string{sid}, stories[0].ActiveSessions, "the live session must show on its story")
}

// TestRegistry_TerminalSessionNotListedAsActive reproduces bug 23: after a
// user quits a session by driving the story into a terminal state, the web home
// catalogue must stop reporting that session under the story's active_sessions.
func TestRegistry_TerminalSessionNotListedAsActive(t *testing.T) {
	storiesDir, appPath := writeStory(t, "quit", []byte(terminalQuitStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	_, err := reg.Rescan()
	require.NoError(t, err)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	entry, ok := reg.Get(sid)
	require.True(t, ok)
	out, err := entry.Driver.SubmitDirect(ctx, "quit", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode, "quit must reach the terminal state")

	snap, err := entry.Source.Snapshot()
	require.NoError(t, err)
	require.True(t, snap.Session.Terminal, "session snapshot should be terminal after quit")

	stories := reg.ListStories()
	require.Len(t, stories, 1)
	assert.Empty(t, stories[0].ActiveSessions, "terminal sessions must not be reported as active")
}

// TestRegistry_NewSessionHonorsFlowSeed proves GAP 1: a `kitsoki web --flow`
// session (a base whose Flow fixture carries initial_state / initial_world)
// STARTS at the seeded state with the seeded world, exactly as `test flows` and
// `record` do — while the empty-fixture base still starts at the root.
func TestRegistry_NewSessionHonorsFlowSeed(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))

	base := deterministicBase(t)
	base.Flow = &testrunner.FlowFixture{
		InitialState: "hall",
		InitialWorld: map[string]any{"visited": true},
	}
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, base)
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	entry, ok := reg.Get(sid)
	require.True(t, ok)
	snap, err := entry.Source.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, "hall", snap.Session.CurrentState, "flow session must START at fixture initial_state")

	// World is not on the snapshot header; assert the seed via the journey.
	reg.mu.Lock()
	re := reg.sessions[sid]
	reg.mu.Unlock()
	require.NotNil(t, re)
	journey, err := re.rt.Orch.LoadJourney(re.sid)
	require.NoError(t, err)
	assert.Equal(t, true, journey.World.Vars["visited"], "flow session must seed initial_world")
}

// TestRegistry_NewSessionNoFlowSeedStartsAtRoot proves the default (empty
// fixture) web session is unchanged by GAP 1: it still starts at the root state
// with default world.
func TestRegistry_NewSessionNoFlowSeedStartsAtRoot(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	sid, err := reg.NewSession(context.Background(), appPath)
	require.NoError(t, err)
	entry, ok := reg.Get(sid)
	require.True(t, ok)
	snap, err := entry.Source.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, "foyer", snap.Session.CurrentState, "no-flow session starts at root")
}

// TestRegistry_GetUnknown proves an unknown id resolves to ok=false (the server
// turns this into a structured not-found error rather than a nil-deref).
func TestRegistry_GetUnknown(t *testing.T) {
	reg := NewRegistry(webconfig.WebConfig{}, nil, deterministicBase(t))
	t.Cleanup(reg.Close)
	_, ok := reg.Get("ghost")
	assert.False(t, ok)
}

// TestRegistry_NewSessionInvalidStory proves session.new fails fast with a
// structured error on a malformed story YAML — no session is registered
// (decided lean: the UI surfaces the error before navigating).
func TestRegistry_NewSessionInvalidStory(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("app: {{ not valid yaml"), 0o644))

	reg := NewRegistry(webconfig.WebConfig{}, []string{dir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	_, err := reg.NewSession(context.Background(), bad)
	require.Error(t, err, "an invalid story must fail fast")
	assert.Empty(t, reg.List(), "no session must be registered on a failed build")
}

// TestRegistry_Reload mirrors the TUI /reload path: a benign edit to the story
// (no state removed) reloads the def into the orchestrator and reports
// prev_state_exists. This is the registry analogue of
// orchestrator.TestReload_PrevStateStillExists.
func TestRegistry_Reload(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	reg.mu.Lock()
	orch := reg.sessions[sid].rt.Orch
	reg.mu.Unlock()
	before := orch.AppDef()

	// Append an unrelated comment — a benign edit, no state removed.
	require.NoError(t, os.WriteFile(appPath, append([]byte("# touched by reload test\n"), []byte(minimalStory)...), 0o644))

	prevExists, err := reg.Reload(ctx, sid)
	require.NoError(t, err)
	assert.True(t, prevExists, "foyer still exists after a no-op edit")

	after := orch.AppDef()
	assert.NotSame(t, before, after, "Reload must swap a fresh def into the orchestrator")
	assert.Equal(t, "mini-story", after.App.ID)
}

// TestRegistry_ReloadPrevStateRemoved proves prev_state_exists is false when the
// edit removes the session's current state, mirroring Orchestrator.Reload
// semantics (the UI then shows the "staying put" warning).
func TestRegistry_ReloadPrevStateRemoved(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	// Build a valid replacement story that does NOT declare `foyer` (the
	// session's current state). prev_state_exists must come back false.
	replacement := []byte(`app:
  id: mini-story
  version: 0.1.0
  title: "Mini Story"
world: {}
root: lobby
states:
  lobby:
    view: "A bare lobby."
`)
	require.NoError(t, os.WriteFile(appPath, replacement, 0o644))

	prevExists, err := reg.Reload(ctx, sid)
	require.NoError(t, err)
	assert.False(t, prevExists, "removed current state (foyer) must report prev_state_exists:false")
}

// TestRegistry_Rescan proves a story added between scans appears, and live
// sessions are left untouched across a rescan.
func TestRegistry_Rescan(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	first, err := reg.Rescan()
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Start a live session before adding the second story.
	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	// Add a second story under the same stories dir.
	second := filepath.Join(storiesDir, "mini2")
	require.NoError(t, os.MkdirAll(second, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(second, "app.yaml"), []byte(minimalStory), 0o644))

	after, err := reg.Rescan()
	require.NoError(t, err)
	assert.Len(t, after, 2, "the newly-added story must appear after rescan")

	// The live session survived the rescan untouched.
	_, ok := reg.Get(sid)
	assert.True(t, ok, "rescan must not disturb live sessions")
}

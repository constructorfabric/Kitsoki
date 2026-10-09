package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	studio "kitsoki/internal/mcp/studio"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// stubReplayHarness is a no-LLM harness whose RunTurn is never expected to fire
// in the server-core tests (driving lands in a later slice). It exists so a
// default-mode handle has a non-nil harness without touching an LLM.
type stubReplayHarness struct{}

func (stubReplayHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcpsdk.CallToolParams, error) {
	return mcpsdk.CallToolParams{}, nil
}
func (stubReplayHarness) Close() error { return nil }

// failingLiveHarness fails the moment it is constructed. Injected behind the
// default so the no-live-fallthrough test can prove a default-mode open never
// reaches the live branch (it would error if it did).
var errLiveForbidden = errors.New("live harness must not be constructed for a default-mode handle")

// stubBuilder returns a HarnessBuilder that yields a no-LLM stub for replay mode
// and FAILS for live mode. A default-mode (replay) open succeeds with the stub;
// any live build errors with errLiveForbidden.
func stubBuilder() studio.HarnessBuilder {
	return func(mode studio.HarnessMode, recordingPath, _ string) (harness.Harness, error) {
		if mode == studio.HarnessLive {
			return nil, errLiveForbidden
		}
		return stubReplayHarness{}, nil
	}
}

// connectInProcess wires an in-process MCP client/server pair over the SDK's
// InMemoryTransports and returns the client session.
func connectInProcess(ctx context.Context, t *testing.T, srv *studio.Server) *mcpsdk.ClientSession {
	t.Helper()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err, "server connect")
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err, "client connect")
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool issues a tool call with a (possibly empty) argument map.
func callTool(ctx context.Context, cs *mcpsdk.ClientSession, name string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	return cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
}

// contentText returns the text content of the first content block.
func contentText(res *mcpsdk.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

// ─── 2.1 ping ────────────────────────────────────────────────────────────────

// TestStudioPing constructs the server in-process and calls studio.ping; it must
// return {ok:true, version}. Proves the transport + dotted tool name + registry.
func TestStudioPing(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "studio.ping", nil)
	require.NoError(t, err, "studio.ping call")
	require.False(t, res.IsError, "studio.ping should not be an error: %s", contentText(res))

	var ok studio.PingOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	assert.True(t, ok.OK, "ping ok")
	assert.Equal(t, studio.Version, ok.Version, "ping version")
	assert.NotEmpty(t, ok.GoVersion, "ping should report Go build identity")
	assert.NotEmpty(t, ok.Executable, "ping should identify the attached executable")
	assert.NotEmpty(t, ok.WorkingDir, "ping should identify the attached working directory")
	assert.NotEmpty(t, ok.Revision, "ping should identify the attached git revision")
	assert.NotEmpty(t, ok.Modified, "ping should report whether the attached checkout is modified")
}

// TestStudioToolsListed confirms both server-core tools register under their
// dotted names (the open-question lean: keep family.verb). If the SDK rejected a
// dot, ListTools would not surface them and this fails — the canary for the
// fallback note.
func TestStudioToolsListed(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	cs := connectInProcess(ctx, t, srv)

	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["studio.ping"], "studio.ping registered (dotted name accepted)")
	assert.True(t, names["studio.handles"], "studio.handles registered (dotted name accepted)")
	assert.True(t, names["studio.work"], "studio.work registered (dotted name accepted)")
	assert.True(t, names["session.command"], "session.command registered (dotted name accepted)")
	assert.True(t, names["session.drive_operation"], "session.drive_operation registered (dotted name accepted)")
	assert.True(t, names["inbox.sync_github"], "inbox.sync_github registered (dotted name accepted)")
	assert.True(t, names["story.write"], "story.write registered on a read-write server")
	assert.True(t, names["host.patch"], "host.patch registered on a read-write server")
}

func TestStrictOperatingSystemProfileRegistersOnlyItsPreviewPlane(t *testing.T) {
	ctx := context.Background()
	services, err := studio.NewOperatingSystemServices(
		studio.StudioOperatingProfileStrict,
		"../../../.capsules/workspaces",
		"../../../scripts/dev-workspace.sh",
	)
	require.NoError(t, err)
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()),
		studio.WithOperatingSystemServices(studio.StudioOperatingProfileStrict, services),
	)
	cs := connectInProcess(ctx, t, srv)
	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, required := range []string{
		"objective.open", "objective.close", "workspace.create", "workspace.codeact", "gate.run",
		"studio.diagnose", "session.new", "session.submit", "session.status", "session.inspect", "session.world", "session.trace", "session.answer", "session.close", "session.explain", "trace.explain",
	} {
		assert.Truef(t, names[required], "strict profile must expose %s", required)
	}
	for _, forbidden := range []string{"host.run", "host.patch", "vcs.status", "worktree.create", "story.write", "session.drive", "story.read"} {
		assert.Falsef(t, names[forbidden], "strict profile must not expose %s", forbidden)
	}
}

// TestReadOnlyOmitsStoryWrite confirms a server built with ReadOnly() drops
// story.write (the only story-tree mutation) while keeping the read tools and
// the replay-driving tools — the meta-mode Q&A surface.
func TestReadOnlyOmitsStoryWrite(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()), studio.ReadOnly())
	cs := connectInProcess(ctx, t, srv)

	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	assert.False(t, names["story.write"], "story.write must be omitted in read-only mode")
	assert.False(t, names["host.patch"], "host.patch must be omitted in read-only mode")
	assert.True(t, names["story.read"], "story.read stays available in read-only mode")
	assert.True(t, names["story.validate"], "story.validate stays available in read-only mode")
	assert.True(t, names["session.drive"], "replay session driving stays available in read-only mode")
	assert.True(t, names["session.drive_operation"], "operation driving stays available in read-only mode")
}

// ─── 2.2 handle lifecycle ────────────────────────────────────────────────────

// TestHandleLifecycle opens, lists, and closes session handles through the store
// and confirms studio.handles reflects the state. An unknown handle close is a
// structured tool error (never a panic / silent no-op).
func TestHandleLifecycle(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(stubBuilder())
	srv := studio.NewServer(sess)
	cs := connectInProcess(ctx, t, srv)

	// Empty session: no handles, no workspace.
	snap := callHandles(ctx, t, cs)
	assert.Empty(t, snap.Sessions, "no sessions initially")
	assert.Nil(t, snap.Workspace, "no workspace initially")

	// Open two replay (default-mode) handles via the store seam.
	h1, err := sess.OpenSession(studio.OpenSessionParams{})
	require.NoError(t, err)
	h2, err := sess.OpenSession(studio.OpenSessionParams{TracePath: "/tmp/trace-2.jsonl"})
	require.NoError(t, err)
	assert.Equal(t, "s1", h1.Key)
	assert.Equal(t, "s2", h2.Key)

	// studio.handles lists both, in numeric order, mode replay.
	snap = callHandles(ctx, t, cs)
	require.Len(t, snap.Sessions, 2)
	assert.Equal(t, "s1", snap.Sessions[0].Handle)
	assert.Equal(t, "s2", snap.Sessions[1].Handle)
	assert.Equal(t, string(studio.HarnessReplay), snap.Sessions[0].Mode)
	assert.Equal(t, "/tmp/trace-2.jsonl", snap.Sessions[1].TracePath)

	// Resolve fail-fast: unknown handle → typed error, never nil-without-error.
	_, rerr := sess.ResolveSession("nope")
	require.Error(t, rerr)
	code, _ := studio.AsToolError(rerr)
	assert.Equal(t, studio.ErrUnknownHandle, code)

	// Close one; it drops from the listing.
	require.NoError(t, sess.CloseSession("s1"))
	snap = callHandles(ctx, t, cs)
	require.Len(t, snap.Sessions, 1)
	assert.Equal(t, "s2", snap.Sessions[0].Handle)

	// Closing an unknown handle is a structured error.
	cerr := sess.CloseSession("s1")
	require.Error(t, cerr)
	ccode, _ := studio.AsToolError(cerr)
	assert.Equal(t, studio.ErrUnknownHandle, ccode)
}

func TestSnapshotDoesNotBlockWhileDrivingSessionOpens(t *testing.T) {
	enteredBuild := make(chan struct{})
	releaseBuild := make(chan struct{})
	buildErr := errors.New("release blocked builder")
	sess := studio.NewStudioSession(func(mode studio.HarnessMode, recordingPath, _ string) (harness.Harness, error) {
		close(enteredBuild)
		<-releaseBuild
		return nil, buildErr
	})

	openDone := make(chan error, 1)
	go func() {
		_, err := sess.OpenDrivingSession(context.Background(), studio.OpenDrivingSessionParams{
			Mode:          studio.HarnessReplay,
			RecordingPath: "recording.yaml",
			StoryPath:     "stories/bugfix/app.yaml",
			TracePath:     "trace.jsonl",
		})
		openDone <- err
	}()

	select {
	case <-enteredBuild:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("OpenDrivingSession did not reach the blocking harness builder")
	}

	snapshotDone := make(chan studio.HandlesSnapshot, 1)
	go func() {
		snapshotDone <- sess.Snapshot()
	}()

	select {
	case snap := <-snapshotDone:
		require.Len(t, snap.Sessions, 1)
		assert.Equal(t, "s1", snap.Sessions[0].Handle)
		assert.Equal(t, "replay", snap.Sessions[0].Mode)
		assert.Equal(t, "trace.jsonl", snap.Sessions[0].TracePath)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Snapshot blocked behind an in-progress session open")
	}

	close(releaseBuild)
	err := <-openDone
	require.Error(t, err)
	code, msg := studio.AsToolError(err)
	assert.Equal(t, studio.ErrHarness, code)
	assert.Contains(t, msg, buildErr.Error())
}

// callHandles calls studio.handles and decodes the snapshot.
func callHandles(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession) studio.HandlesSnapshot {
	t.Helper()
	res, err := callTool(ctx, cs, "studio.handles", nil)
	require.NoError(t, err, "studio.handles call")
	require.False(t, res.IsError, "studio.handles error: %s", contentText(res))
	var snap studio.HandlesSnapshot
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &snap))
	return snap
}

// ─── workspace handle ────────────────────────────────────────────────────────

// TestWorkspaceHandle binds an authoring workspace and confirms it surfaces in
// studio.handles, and that binding a second workspace fails fast.
func TestWorkspaceHandle(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(stubBuilder())
	srv := studio.NewServer(sess)
	cs := connectInProcess(ctx, t, srv)

	_, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: "/tmp/story-x"})
	require.NoError(t, err)

	snap := callHandles(ctx, t, cs)
	require.NotNil(t, snap.Workspace)
	assert.Equal(t, "/tmp/story-x", snap.Workspace.Dir)
	assert.False(t, snap.Workspace.Valid, "no Def cached → not valid")

	// Second workspace without close → ErrWorkspaceExists.
	_, err2 := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: "/tmp/story-y"})
	require.Error(t, err2)
	code, _ := studio.AsToolError(err2)
	assert.Equal(t, studio.ErrWorkspaceExists, code)

	// Empty dir → ErrBadRequest.
	_, err3 := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: ""})
	require.Error(t, err3)
	code3, _ := studio.AsToolError(err3)
	assert.Equal(t, studio.ErrBadRequest, code3)
}

// ─── 2.3 no-live-fallthrough ─────────────────────────────────────────────────

// TestNoLiveFallthrough proves a default-mode open never invokes the live branch
// of the harness builder. The injected builder FAILS for live; a default open
// (no Mode set) must succeed (it took the replay branch), and an explicit live
// open must fail (it reached the live branch) — together pinning the no-LLM
// default in place.
func TestNoLiveFallthrough(t *testing.T) {
	sess := studio.NewStudioSession(stubBuilder())

	// Default mode (Mode unset) → replay branch → succeeds, never touches live.
	h, err := sess.OpenSession(studio.OpenSessionParams{})
	require.NoError(t, err, "default-mode open must not hit the live branch")
	assert.Equal(t, studio.HarnessReplay, h.Mode, "default normalizes to replay")
	assert.NotNil(t, h.Harness, "replay handle has a harness")

	// An unknown/garbage mode also normalizes to replay (never live).
	h2, err := sess.OpenSession(studio.OpenSessionParams{Mode: studio.HarnessMode("bogus")})
	require.NoError(t, err)
	assert.Equal(t, studio.HarnessReplay, h2.Mode)

	// Explicit live → live branch → the injected failing builder errors,
	// surfaced as a structured ErrHarness (proves live IS the only path that
	// reaches the live builder).
	_, liveErr := sess.OpenSession(studio.OpenSessionParams{Mode: studio.HarnessLive})
	require.Error(t, liveErr)
	code, msg := studio.AsToolError(liveErr)
	assert.Equal(t, studio.ErrHarness, code)
	assert.Contains(t, msg, errLiveForbidden.Error())
}

// TestDefaultHarnessBuilder_NoLLM confirms the production builder never produces
// a live harness in the server core and that replay requires a recording.
func TestDefaultHarnessBuilder_NoLLM(t *testing.T) {
	// Live is unavailable in the in-package default (wired by a production
	// builder in cmd/kitsoki, never silently no-op'd here).
	_, err := studio.DefaultHarnessBuilder(studio.HarnessLive, "", "")
	require.Error(t, err, "live harness must not be constructible in the in-package default")

	// Replay without a recording fails fast rather than silently no-LLM-no-op.
	_, err = studio.DefaultHarnessBuilder(studio.HarnessReplay, "", "")
	require.Error(t, err, "replay requires a recording path")
}

package studio_test

// session_tools_test.go — slice-7 verification (no LLM). Drives the cloak app
// over its recording.yaml under harness:replay through the MCP facade and
// asserts the proposal's teeth:
//
//   2.1 a known cassette under replay → identical TurnOutcome + Frame as a golden
//   2.2 no-live-fallthrough: a replay session never calls an injected failing live
//   2.3 inspect after a drive matches the state/world/intents for that state
//   2.4 render.tui frame == the slice-1 composer; render.web (STUB) renders no-LLM
//   2.5 render.* on a handle leave the session state/turn unchanged
//
// Every test runs against a replay cassette: no real LLM, no Chromium, no
// kitsoki web — the webshot seam is stubbed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	studio "kitsoki/internal/mcp/studio"
)

const (
	cloakApp      = "../../../testdata/apps/cloak/app.yaml"
	cloakCassette = "../../../testdata/apps/cloak/recording.yaml"
)

// failingLive is a live harness that fails loudly if RunTurn is ever called.
// Wired behind a none-mode VCR over the cloak cassette in the no-live-fallthrough
// test: a replay miss must error WITHOUT calling it.
type failingLive struct{ calls int }

func (f *failingLive) RunTurn(_ context.Context, _ harness.TurnInput) (mcpsdk.CallToolParams, error) {
	f.calls++
	return mcpsdk.CallToolParams{}, errors.New("studio: live harness MUST NOT be called under replay")
}
func (f *failingLive) Close() error { return nil }

// replayBuilder is the production-equivalent harness builder for these tests: it
// builds a real no-LLM ReplayHarness for replay mode (so orch.Turn replays the
// cassette) and FAILS for live, proving a default-mode handle never reaches live.
func replayBuilder() studio.HarnessBuilder {
	return func(mode studio.HarnessMode, recordingPath, _ string) (harness.Harness, error) {
		if mode == studio.HarnessLive {
			return nil, errLiveForbidden
		}
		return harness.NewReplay(recordingPath)
	}
}

// newReplaySession opens a studio session whose builder is the replay builder.
func newReplayServer(t *testing.T) (*studio.Server, *studio.StudioSession) {
	t.Helper()
	sess := studio.NewStudioSession(replayBuilder())
	return studio.NewServer(sess), sess
}

// driveResult decodes a session.drive / submit / continue {outcome, frame} reply.
func driveResult(t *testing.T, res *mcpsdk.CallToolResult) studio.TurnResponse {
	t.Helper()
	require.False(t, res.IsError, "tool call errored: %s", contentText(res))
	var tr studio.TurnResponse
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &tr))
	return tr
}

// openCloak opens a replay-backed cloak driving session and returns its handle.
func openCloak(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession) string {
	t.Helper()
	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new errored: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.True(t, ok.OK)
	require.Equal(t, "replay", ok.Mode, "default/explicit replay → no LLM")
	require.Equal(t, "foyer", ok.State, "cloak starts in the foyer")
	return ok.Handle
}

// ─── 2.1 golden drive ────────────────────────────────────────────────────────

// TestSessionDrive_GoldenTranscript drives the known cloak transcript over MCP
// under harness:replay and asserts the routed intents, resting states, and the
// rendered Frame — the structured TurnOutcome AND the screen — match the golden.
func TestSessionDrive_GoldenTranscript(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	type step struct {
		input string
		state string
	}
	golden := []step{
		{"go west", "cloakroom"},
		{"hang the cloak", "cloakroom"},
		{"go east", "foyer"},
		{"go south", "bar.lit"},
	}

	for i, g := range golden {
		res, err := callTool(ctx, cs, "session.drive", map[string]any{
			"handle": handle, "input": g.input, "cols": 100, "rows": 30,
		})
		require.NoError(t, err)
		tr := driveResult(t, res)

		assert.True(t, tr.OK, "step %d (%q) ok", i, g.input)
		assert.Empty(t, tr.Outcome.Error, "step %d no turn error", i)
		assert.Equal(t, g.state, tr.Outcome.State, "step %d resting state", i)
		assert.Equal(t, g.state, tr.Frame.Metadata.State, "step %d frame state", i)

		// The Frame is the slice-1 screen: Text is the ANSI-stripped twin and
		// carries the room body a human sees, at the requested width.
		require.NotEmpty(t, tr.Frame.Text, "step %d frame text", i)
		assert.Equal(t, 100, tr.Frame.Width, "step %d frame width", i)
	}

	// "go south" lit the bar (the cloak was hung), the canonical cloak win path.
	require.Equal(t, "bar.lit", golden[len(golden)-1].state)
}

// ─── 2.2 no-live-fallthrough ─────────────────────────────────────────────────

// TestSessionDrive_NoLiveFallthrough is the teeth. A replay session driven with
// an utterance that is nowhere in the cassette must surface a turn error (the
// recording miss) WITHOUT the injected failing live harness ever firing.
func TestSessionDrive_NoLiveFallthrough(t *testing.T) {
	ctx := context.Background()

	// Inject a builder whose replay harness is a VCR(none) wrapping a FAILING
	// live — a miss under none-mode must error and never call live.
	fail := &failingLive{}
	sess := studio.NewStudioSession(func(mode studio.HarnessMode, rec, _ string) (harness.Harness, error) {
		if mode == studio.HarnessLive {
			return nil, errLiveForbidden
		}
		return harness.NewVCR(harness.VCRModeNone, rec, fail)
	})
	srv := studio.NewServer(sess)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))

	res, err = callTool(ctx, cs, "session.drive", map[string]any{
		"handle": ok.Handle,
		"input":  "an utterance that is nowhere in the cassette",
	})
	require.NoError(t, err)
	tr := driveResult(t, res)

	assert.False(t, tr.OK, "a replay miss is a turn-level failure")
	assert.Equal(t, "error", tr.Outcome.Mode, "miss surfaces as mode=error, not a silent live route")
	assert.NotEmpty(t, tr.Outcome.Error, "the miss is reported")
	assert.Equal(t, 0, fail.calls, "replay+none miss must NEVER call the live harness")
}

func TestSessionNew_ReplayWithoutCassetteAllowsDirectSubmitOnly(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new without cassette should open for direct submit: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.Equal(t, "foyer", ok.State)

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": ok.Handle,
		"intent": "go",
		"slots":  map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	submitted := driveResult(t, res)
	assert.True(t, submitted.OK)
	assert.Equal(t, "cloakroom", submitted.Outcome.State)

	res, err = callTool(ctx, cs, "session.drive", map[string]any{
		"handle": ok.Handle,
		"input":  "go east",
	})
	require.NoError(t, err)
	driven := driveResult(t, res)
	assert.False(t, driven.OK)
	assert.Equal(t, "error", driven.Outcome.Mode)
	assert.Contains(t, driven.Outcome.Error, "noRouteHarness")
}

func TestSessionSubmit_StreamsProgressNotifications(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)

	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err, "server connect")

	progress := make(chan string, 8)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, &mcpsdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcpsdk.ProgressNotificationClientRequest) {
			progress <- req.Params.Message
		},
	})
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err, "client connect")
	t.Cleanup(func() { _ = cs.Close() })

	handle := openCloak(ctx, t, cs)
	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "session.submit",
		Arguments: map[string]any{
			"handle": handle,
			"intent": "go",
			"slots":  map[string]any{"direction": "west"},
		},
		Meta: mcpsdk.Meta{"progressToken": "submit-1"},
	})
	require.NoError(t, err)
	submitted := driveResult(t, res)
	require.True(t, submitted.OK)
	require.Equal(t, "cloakroom", submitted.Outcome.State)

	var got []string
	require.Eventually(t, func() bool {
		for {
			select {
			case msg := <-progress:
				got = append(got, msg)
			default:
				return len(got) >= 2
			}
		}
	}, time.Second, 10*time.Millisecond)
	assert.Contains(t, got, "session.submit: started for "+handle)
	assert.Contains(t, got, "session.submit: completed for "+handle+" at cloakroom")
}

// TestSessionNew_LiveIsOptIn confirms harness:live takes the live builder branch
// (the only path that reaches it), surfaced as a structured ErrHarness here
// because the test builder forbids live — proving live is never the default.
func TestSessionNew_LiveIsOptIn(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "live",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "explicit live reaches the (forbidden) live builder")
	var te studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &te))
	assert.Equal(t, studio.ErrHarness, te.Code)
}

// ─── 2.3 inspect after a drive ───────────────────────────────────────────────

// TestSessionInspect_MatchesState drives one turn then inspects: the snapshot's
// state, world, and allowed intents must match what the orchestrator reports for
// that state (the same reads buildInspectOutput uses).
func TestSessionInspect_MatchesState(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive into the cloakroom.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{"handle": handle, "input": "go west"})
	require.NoError(t, err)
	driven := driveResult(t, res)
	require.Equal(t, "cloakroom", driven.Outcome.State)

	res, err = callTool(ctx, cs, "session.inspect", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "inspect: %s", contentText(res))
	var ins studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ins))

	assert.True(t, ins.OK)
	// inspect's state/intents match the drive outcome for the same state.
	assert.Equal(t, "cloakroom", ins.State, "inspect state == driven state")
	assert.ElementsMatch(t, driven.Outcome.AllowedIntents, ins.AllowedIntents,
		"inspect allowed_intents == the turn outcome's menu for that state")
	assert.NotEmpty(t, ins.LastView, "inspect carries the rendered view")
	require.NotEmpty(t, ins.LastTurns, "inspect summarises the turn just driven")
	last := ins.LastTurns[len(ins.LastTurns)-1]
	assert.Equal(t, "go", last.Intent, "last turn routed the free text to go")
	assert.Equal(t, "cloakroom", last.ToState)
}

// ─── 2.4 render.tui == composer; render.web stub no-LLM ──────────────────────

// TestRenderTUI_EqualsComposer asserts render.tui returns the EXACT slice-1
// Frame for a driven state: identical text/ansi/metadata to the frame the drive
// turn already produced (both are tui.ComposeFrame at the same geometry — the
// composer is the single source, never a re-derived lookalike).
func TestRenderTUI_EqualsComposer(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive a turn; capture the frame it composed.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west", "cols": 100, "rows": 30,
	})
	require.NoError(t, err)
	driveFrame := driveResult(t, res).Frame

	// render.tui at the SAME geometry must reproduce that frame byte-for-byte.
	res, err = callTool(ctx, cs, "render.tui", map[string]any{
		"handle": handle, "cols": 100, "rows": 30,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.tui: %s", contentText(res))
	var rt studio.RenderTUIResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rt))

	assert.Equal(t, driveFrame.Text, rt.Frame.Text, "render.tui text == the composed drive frame")
	assert.Equal(t, driveFrame.ANSI, rt.Frame.ANSI, "render.tui ansi == the composed drive frame")
	assert.Equal(t, driveFrame.Metadata.State, rt.Frame.Metadata.State)
	assert.Equal(t, driveFrame.Width, rt.Frame.Width)
}

// TestRenderTUI_SpecForm renders an explicit {story_path, state} spec — a state
// the agent never drove to — headlessly, WITHOUT opening any session. The frame
// must carry that state's metadata and screen, all no-LLM.
func TestRenderTUI_SpecForm(t *testing.T) {
	ctx := context.Background()
	srv, sess := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "render.tui", map[string]any{
		"story_path": cloakApp,
		"state":      "cloakroom",
		"cols":       100, "rows": 30,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.tui spec: %s", contentText(res))
	var rt studio.RenderTUIResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rt))

	assert.True(t, rt.OK)
	assert.Equal(t, "cloakroom", rt.Frame.Metadata.State, "spec renders the named state")
	assert.NotEmpty(t, rt.Frame.Text, "spec frame carries the screen")

	// A spec render opens no session handle (it uses a throwaway runtime).
	assert.Empty(t, sess.Snapshot().Sessions, "spec render must not leave a session handle")
}

// TestRenderTUIPNG_RasterisesFrame asserts render.tui_png returns a valid PNG
// image block AND the textual frame, all with no LLM.
func TestRenderTUIPNG_RasterisesFrame(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	res, err := callTool(ctx, cs, "render.tui_png", map[string]any{
		"handle": handle, "cols": 80, "rows": 24,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.tui_png: %s", contentText(res))

	// Always a textual frame; plus an image block for the (default capable) client.
	require.GreaterOrEqual(t, len(res.Content), 2, "text + image content blocks")
	img := imageContent(t, res)
	_, err = png.Decode(bytes.NewReader(img.Data))
	require.NoError(t, err, "render.tui_png produced a decodable PNG")
}

// TestRenderWeb_StubNoLLM renders a known state through render.web with a STUB
// webshot seam — no browser, no kitsoki web, no LLM. The stub records the spec
// it was handed and returns a synthetic PNG, which must come back as an image
// block alongside the text.
func TestRenderWeb_StubNoLLM(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)

	var gotSpec studio.WebRenderSpec
	stubPNG := synthPNG(t)
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		gotSpec = spec
		return stubPNG, nil
	})

	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	res, err := callTool(ctx, cs, "render.web", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.web: %s", contentText(res))

	img := imageContent(t, res)
	assert.Equal(t, stubPNG, img.Data, "render.web returns the stub PNG as an image block")
	assert.NotEmpty(t, gotSpec.SessionID, "the stub saw the handle's live session id")
}

// ─── 2.5 render.* are read-only ──────────────────────────────────────────────

// TestRender_ReadOnly proves render.tui / render.tui_png / render.web leave the
// session's state and turn unchanged — looking at the screen never advances the
// machine.
func TestRender_ReadOnly(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		return synthPNG(t), nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive to a known resting point.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{"handle": handle, "input": "go west"})
	require.NoError(t, err)
	require.Equal(t, "cloakroom", driveResult(t, res).Outcome.State)

	before := inspectSnapshot(ctx, t, cs, handle)
	traceBefore := traceLastTurn(ctx, t, cs, handle)

	// Read-only renders in every form.
	for _, tool := range []string{"render.tui", "render.tui_png", "render.web"} {
		r, e := callTool(ctx, cs, tool, map[string]any{"handle": handle})
		require.NoError(t, e)
		require.False(t, r.IsError, "%s: %s", tool, contentText(r))
	}

	after := inspectSnapshot(ctx, t, cs, handle)
	traceAfter := traceLastTurn(ctx, t, cs, handle)

	assert.Equal(t, before.State, after.State, "render.* must not change state")
	assert.Equal(t, before.World, after.World, "render.* must not change world")
	assert.Equal(t, traceBefore, traceAfter, "render.* must not append a turn to the trace")
}

// TestWebRenderSpec_ToWebshotSpec pins the studio→webshot adapter the production
// render.web wiring uses: a live handle maps to the webshot SessionID (live
// form); a spec maps to StoryPath/State/World (spec form). The two forms are
// mutually exclusive — exactly webshot.Spec's "exactly one source" rule.
func TestWebRenderSpec_ToWebshotSpec(t *testing.T) {
	live := studio.WebRenderSpec{StoryPath: "stories/cloak", SessionID: "sid-123"}.ToWebshotSpec()
	assert.Equal(t, "sid-123", live.SessionID, "live form → webshot SessionID")
	assert.Empty(t, live.StoryPath, "live form omits StoryPath")

	spec := studio.WebRenderSpec{StoryPath: "stories/cloak", State: "bar.lit", World: map[string]any{"lit": true}}.ToWebshotSpec()
	assert.Equal(t, "stories/cloak", spec.StoryPath, "spec form → webshot StoryPath")
	assert.Equal(t, "bar.lit", spec.State)
	assert.Empty(t, spec.SessionID, "spec form omits SessionID")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func inspectSnapshot(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, handle string) studio.InspectResult {
	t.Helper()
	res, err := callTool(ctx, cs, "session.inspect", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "inspect: %s", contentText(res))
	var ins studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ins))
	return ins
}

func traceLastTurn(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, handle string) int64 {
	t.Helper()
	res, err := callTool(ctx, cs, "session.trace", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "trace: %s", contentText(res))
	var tr studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &tr))
	return tr.LastTurn
}

// imageContent returns the first ImageContent block in a tool result.
func imageContent(t *testing.T, res *mcpsdk.CallToolResult) *mcpsdk.ImageContent {
	t.Helper()
	for _, c := range res.Content {
		if ic, ok := c.(*mcpsdk.ImageContent); ok {
			return ic
		}
	}
	t.Fatalf("no image content in result (%d blocks)", len(res.Content))
	return nil
}

// synthPNG builds a 1x1 PNG so a stub webshot returns valid image bytes without
// a browser.
func synthPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

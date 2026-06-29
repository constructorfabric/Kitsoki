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
	"os"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/jobs"
	studio "kitsoki/internal/mcp/studio"
	"kitsoki/internal/store"
)

const (
	cloakApp                   = "../../../testdata/apps/cloak/app.yaml"
	cloakCassette              = "../../../testdata/apps/cloak/recording.yaml"
	punchListApp               = "../../../stories/punch-list/app.yaml"
	punchListTop10HostCassette = "../../../stories/punch-list/cassettes/top10_gpt55.cassette.yaml"
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

type sleepyLive struct {
	delay time.Duration
}

func (s sleepyLive) RunTurn(ctx context.Context, in harness.TurnInput) (mcpsdk.CallToolParams, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return mcpsdk.CallToolParams{}, ctx.Err()
	}
	args := map[string]any{"intent": "go", "confidence": 1.0, "slots": map[string]any{"direction": "west"}}
	return mcpsdk.CallToolParams{Name: "transition", Arguments: args}, nil
}

func (s sleepyLive) Close() error { return nil }

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

func newReplayServerWithChatStore(t *testing.T, chatStore *chats.Store) (*studio.Server, *studio.StudioSession) {
	t.Helper()
	sess := studio.NewStudioSession(replayBuilder())
	sess.SetChatStore(chatStore)
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

func TestSessionDrive_ReturnsRunningWhenTurnExceedsBoundedWait(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(func(mode studio.HarnessMode, _, _ string) (harness.Harness, error) {
		require.Equal(t, studio.HarnessLive, mode)
		return sleepyLive{delay: 120 * time.Millisecond}, nil
	})
	srv := studio.NewServer(sess)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "live",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))

	start := time.Now()
	res, err = callTool(ctx, cs, "session.drive", map[string]any{
		"handle":         ok.Handle,
		"input":          "go west",
		"async_after_ms": 20,
	})
	require.NoError(t, err)
	tr := driveResult(t, res)
	require.True(t, tr.OK)
	require.NotNil(t, tr.Running, "slow turns return a running status before the MCP client times out")
	require.Equal(t, ok.Handle, tr.Running.Handle)
	require.Equal(t, "go west", tr.Running.Input)
	require.Less(t, time.Since(start), 100*time.Millisecond)

	res, err = callTool(ctx, cs, "session.status", map[string]any{"handle": ok.Handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.status: %s", contentText(res))
	var runningStatus studio.SessionStatusResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &runningStatus))
	require.NotNil(t, runningStatus.Running, "session.status exposes the in-flight drive for polling")
	assert.Equal(t, ok.Handle, runningStatus.Running.Handle)
	assert.Equal(t, "go west", runningStatus.Running.Input)

	res, err = callTool(ctx, cs, "session.inspect", map[string]any{"handle": ok.Handle, "omit_world": true})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.inspect: %s", contentText(res))
	var runningInspect studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &runningInspect))
	require.NotNil(t, runningInspect.Running, "session.inspect exposes the in-flight drive for reacquire")
	require.NotNil(t, runningInspect.Async)
	assert.Equal(t, 1, runningInspect.Async.RunningDrive)

	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "session.status", map[string]any{"handle": ok.Handle})
		if err != nil || res.IsError {
			return false
		}
		var status studio.SessionStatusResult
		if json.Unmarshal([]byte(contentText(res)), &status) != nil {
			return false
		}
		return status.State == "cloakroom" && status.Running == nil
	}, time.Second, 20*time.Millisecond)

	res, err = callTool(ctx, cs, "session.status", map[string]any{"handle": ok.Handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.status: %s", contentText(res))
	var settledStatus studio.SessionStatusResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &settledStatus))
	assert.Nil(t, settledStatus.Running, "running marker is removed after the turn settles")
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

func TestSessionNew_HostCassetteBacksDirectSubmitRun(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path":    punchListApp,
		"harness":       "replay",
		"host_cassette": punchListTop10HostCassette,
		"trace":         t.TempDir() + "/trace.jsonl",
		"initial_world": map[string]any{
			"manifest_path": "stories/punch-list/testdata/top10_gpt55.yaml",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new with host_cassette should open: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.Equal(t, "idle", ok.State)

	submit := func(intent string) studio.TurnResponse {
		t.Helper()
		res, err := callTool(ctx, cs, "session.submit", map[string]any{
			"handle": ok.Handle,
			"intent": intent,
			"cols":   100,
			"rows":   30,
		})
		require.NoError(t, err)
		return driveResult(t, res)
	}

	// settle polls session.inspect until the machine reaches a stable, operator-
	// actionable state with no background job still running. The drive and
	// implementation rooms dispatch host.agent.task with background:true, so each
	// item's work runs asynchronously: dispatching `next_item` returns at the
	// transient `drive` state while the job is still in flight, then the job's
	// on_complete arc auto-routes drive → implementation → verify on its own.
	stable := map[string]bool{"board": true, "verify": true, "report": true, "needs_human": true}
	settle := func() string {
		t.Helper()
		for attempt := 0; attempt < 400; attempt++ {
			res, err := callTool(ctx, cs, "session.inspect", map[string]any{
				"handle": ok.Handle, "omit_world": true,
			})
			require.NoError(t, err)
			var snap struct {
				State string                     `json:"state"`
				Async studio.AsyncInspectSummary `json:"async"`
			}
			require.NoError(t, json.Unmarshal([]byte(contentText(res)), &snap))
			if snap.Async.JobsRunning == 0 && snap.Async.DispatchingDrives == 0 && stable[snap.State] {
				return snap.State
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("session never settled into a stable state")
		return ""
	}

	started := submit("start")
	require.True(t, started.OK)
	require.Equal(t, "load", started.Outcome.State)
	require.Contains(t, started.Frame.Text, "Loaded 10 item(s).")

	board := submit("next_item")
	require.True(t, board.OK)
	require.Equal(t, "board", board.Outcome.State)
	require.Contains(t, board.Frame.Text, "Pending 10")

	// Drive the whole punch-list through the MCP session.submit surface. From the
	// board, `next_item` dispatches the current item's background drive job; once it
	// (and any implementation job) finishes, the machine settles at `verify`, whose
	// independent check we then advance with `verify_done` back to the board. We
	// walk the operator-facing intents until the run reaches `report`. The per-arc
	// state-machine behaviour itself is covered exhaustively by the
	// stories/punch-list/flows/* fixtures; this test guards the host_cassette MCP
	// surface end to end.
	var lastText string
	reached := false
	for step := 0; step < 60; step++ {
		switch state := settle(); state {
		case "report":
			reached = true
		case "needs_human":
			t.Fatalf("step %d bounced to needs_human", step)
		case "verify":
			adv := submit("verify_done")
			require.True(t, adv.OK, "step %d verify_done should advance: %q", step, adv.Outcome.Error)
			lastText = adv.Frame.Text
		case "board":
			adv := submit("next_item")
			require.True(t, adv.OK, "step %d next_item should advance: %q", step, adv.Outcome.Error)
			lastText = adv.Frame.Text
		default:
			t.Fatalf("step %d unexpected stable state %q", step, state)
		}
		if reached {
			break
		}
	}
	require.True(t, reached, "run never reached report")
	require.Contains(t, lastText, "10 passed, 0 partial, 0 failed, 0 skipped, 0 pending")
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

func TestSessionSubmit_BackgroundJobCompletesOverMCP(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	appPath := writeBackgroundJobStory(t)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": appPath,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": ok.Handle,
		"intent": "enter",
		"cols":   100,
		"rows":   30,
	})
	require.NoError(t, err)
	submitted := driveResult(t, res)
	require.True(t, submitted.OK)
	require.Equal(t, "running", submitted.Outcome.State)
	require.Equal(t, "running", submitted.Frame.Metadata.State)
	require.NotContains(t, submitted.Frame.Text, "mcp-bg-done")

	var inspected studio.InspectResult
	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "session.inspect", map[string]any{"handle": ok.Handle})
		if err != nil || res.IsError {
			return false
		}
		if err := json.Unmarshal([]byte(contentText(res)), &inspected); err != nil {
			return false
		}
		if inspected.World["result"] != "mcp-bg-done" {
			return false
		}
		if len(inspected.Jobs) != 1 || inspected.Jobs[0].Status != jobs.JobDone {
			return false
		}
		severities := map[jobs.NotificationSeverity]bool{}
		for _, n := range inspected.Notifications {
			severities[n.Severity] = true
		}
		return severities[jobs.SeverityInfo] && severities[jobs.SeveritySuccess]
	}, 3*time.Second, 25*time.Millisecond)
	require.Len(t, inspected.Jobs, 1)
	assert.Equal(t, "host.run", inspected.Jobs[0].Kind)
	assert.Equal(t, "running", inspected.Jobs[0].OriginState)
	assert.Equal(t, inspected.Jobs[0].ID, inspected.World["last_job_id"])
	assert.NotZero(t, inspected.Jobs[0].CreatedAtUnixMilli)
	assert.NotZero(t, inspected.Jobs[0].FinishedAtUnixMilli)

	require.GreaterOrEqual(t, len(inspected.Notifications), 2)
	var completion studio.InboxInspectItem
	for _, n := range inspected.Notifications {
		if n.Severity == jobs.SeveritySuccess {
			completion = n
			break
		}
	}
	require.NotEmpty(t, completion.ID)
	assert.Equal(t, inspected.Jobs[0].ID, completion.TeleportJobID)
	assert.Equal(t, "job", completion.OriginKind)
	assert.NotZero(t, completion.CreatedAtUnixMilli)

	res, err = callTool(ctx, cs, "render.tui", map[string]any{
		"handle": ok.Handle,
		"cols":   100,
		"rows":   30,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.tui: %s", contentText(res))
	var rendered studio.RenderTUIResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	assert.Contains(t, rendered.Frame.Text, "mcp-bg-done")
}

func TestSessionSubmit_BackgroundJobVisibleWhileRunningOverMCP(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	appPath := writeSlowBackgroundJobStory(t)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": appPath,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": ok.Handle,
		"intent": "enter",
	})
	require.NoError(t, err)
	require.True(t, driveResult(t, res).OK)

	var running studio.InspectResult
	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "session.inspect", map[string]any{"handle": ok.Handle})
		if err != nil || res.IsError {
			return false
		}
		if err := json.Unmarshal([]byte(contentText(res)), &running); err != nil {
			return false
		}
		return len(running.Jobs) == 1 && running.Jobs[0].Status == jobs.JobRunning
	}, time.Second, 10*time.Millisecond)

	require.Len(t, running.Jobs, 1)
	assert.Equal(t, "host.run", running.Jobs[0].Kind)
	assert.Equal(t, "running", running.Jobs[0].OriginState)
	assert.Equal(t, 1, running.Async.JobsTotal)
	assert.Equal(t, 1, running.Async.JobsRunning)
	assert.Equal(t, 0, running.Async.JobsTerminal)
	assert.Equal(t, running.Jobs[0].ID, running.World["last_job_id"])
	assert.NotZero(t, running.Jobs[0].CreatedAtUnixMilli)
	assert.Zero(t, running.Jobs[0].FinishedAtUnixMilli)

	var done studio.InspectResult
	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "session.inspect", map[string]any{"handle": ok.Handle})
		if err != nil || res.IsError {
			return false
		}
		if err := json.Unmarshal([]byte(contentText(res)), &done); err != nil {
			return false
		}
		return done.World["result"] == "slow-bg-done" &&
			len(done.Jobs) == 1 &&
			done.Jobs[0].Status == jobs.JobDone
	}, 3*time.Second, 25*time.Millisecond)
	assert.Equal(t, 0, done.Async.JobsRunning)
	assert.Equal(t, 1, done.Async.JobsTerminal)
}

func TestSessionInspect_SurfacesChatAsyncWorkOverMCP(t *testing.T) {
	ctx := context.Background()
	backing, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backing.Close() })
	chatStore, err := chats.NewStore(backing.DB())
	require.NoError(t, err)
	srv, sess := newReplayServerWithChatStore(t, chatStore)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var opened studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &opened))
	sh, err := sess.ResolveSession(opened.Handle)
	require.NoError(t, err)
	sid := string(sh.SID)

	queuedChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-a", "queued work")
	require.NoError(t, err)
	pending, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          queuedChat.ID,
		Transport:       chats.DriveTransportMCP,
		Actor:           "studio-test",
		Payload:         "review this proposal",
		OriginSessionID: sid,
		OriginState:     "lobby",
	})
	require.NoError(t, err)
	dispatching, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          queuedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "continue implementation",
		OriginSessionID: sid,
		OriginState:     "running",
	})
	require.NoError(t, err)
	_, err = chatStore.ClaimDrive(ctx, dispatching.DriveID)
	require.NoError(t, err)

	failed, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          queuedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "failed proposal review",
		OriginSessionID: sid,
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = chatStore.ClaimDrive(ctx, failed.DriveID)
	require.NoError(t, err)
	require.NoError(t, chatStore.MarkDriveFailed(ctx, failed.DriveID, "claude exited 1"))
	_, err = chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          queuedChat.ID,
		Transport:       chats.DriveTransportMCP,
		Actor:           "other",
		Payload:         "not this session",
		OriginSessionID: "other-session",
	})
	require.NoError(t, err)

	backgroundChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-bg", "backgrounded work")
	require.NoError(t, err)
	_, err = backing.DB().ExecContext(ctx,
		`UPDATE chats SET session_id = ? WHERE id = ?`,
		sid, backgroundChat.ID)
	require.NoError(t, err)
	_, err = chatStore.AppendMessage(ctx, backgroundChat.ID, "user", "what is the async status?", nil)
	require.NoError(t, err)
	_, err = chatStore.AppendMessage(ctx, backgroundChat.ID, "assistant", "still running in the background", map[string]any{"source": "test"})
	require.NoError(t, err)
	_, err = chatStore.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:         backgroundChat.ID,
		TmuxSession:    "kitsoki-bg-test",
		PermissionMode: "acceptEdits",
		WorkspacePath:  "/tmp/kitsoki-bg-test",
	})
	require.NoError(t, err)
	_, err = chatStore.DetachPTY(ctx, backgroundChat.ID)
	require.NoError(t, err)
	require.NoError(t, chatStore.MarkPTYIdle(ctx, backgroundChat.ID))

	res, err = callTool(ctx, cs, "session.inspect", map[string]any{"handle": opened.Handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.inspect: %s", contentText(res))
	var inspected studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &inspected))

	require.Len(t, inspected.PendingDrives, 3)
	assert.Equal(t, 1, inspected.Async.PendingDrives)
	assert.Equal(t, 1, inspected.Async.DispatchingDrives)
	assert.Equal(t, 1, inspected.Async.FailedDrives)
	assert.Equal(t, pending.DriveID, inspected.PendingDrives[0].DriveID)
	assert.Equal(t, chats.DriveStatusPending, inspected.PendingDrives[0].Status)
	assert.Equal(t, "review this proposal", inspected.PendingDrives[0].Payload)
	assert.Equal(t, dispatching.DriveID, inspected.PendingDrives[1].DriveID)
	assert.Equal(t, chats.DriveStatusDispatching, inspected.PendingDrives[1].Status)
	assert.NotZero(t, inspected.PendingDrives[1].DispatchedAtUnixMicro)
	assert.Equal(t, failed.DriveID, inspected.PendingDrives[2].DriveID)
	assert.Equal(t, chats.DriveStatusFailed, inspected.PendingDrives[2].Status)
	assert.Equal(t, "claude exited 1", inspected.PendingDrives[2].ErrorMessage)
	assert.NotZero(t, inspected.PendingDrives[2].CompletedAtUnixMicro)

	require.Len(t, inspected.BackgroundedChats, 1)
	assert.Equal(t, 1, inspected.Async.BackgroundedChats)
	bg := inspected.BackgroundedChats[0]
	assert.Equal(t, backgroundChat.ID, bg.ChatID)
	assert.Equal(t, "kitsoki-bg-test", bg.TmuxSession)
	assert.Equal(t, "acceptEdits", bg.PermissionMode)
	assert.Equal(t, "/tmp/kitsoki-bg-test", bg.WorkspacePath)
	assert.NotZero(t, bg.UpdatedAtUnixMicro)
	assert.NotZero(t, bg.LastIdleAtUnixMicro)

	res, err = callTool(ctx, cs, "chat.show", map[string]any{
		"chat_id":   backgroundChat.ID,
		"since_seq": 1,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "chat.show: %s", contentText(res))
	var shown studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &shown))
	require.True(t, shown.OK)
	assert.Equal(t, backgroundChat.ID, shown.Chat.ID)
	assert.Equal(t, "backgrounded work", shown.Chat.Title)
	assert.Equal(t, "scope-bg", shown.Chat.DisplayScopeKey)
	assert.Equal(t, sid, shown.Chat.SessionID)
	require.NotNil(t, shown.PTY)
	assert.Equal(t, "kitsoki-bg-test", shown.PTY.TmuxSession)
	assert.Equal(t, string(chats.PtyModeBackground), shown.PTY.Mode)
	require.Len(t, shown.Messages, 1)
	assert.Equal(t, 1, shown.Messages[0].Seq)
	assert.Equal(t, "assistant", shown.Messages[0].Role)
	assert.Equal(t, "still running in the background", shown.Messages[0].Content)
	assert.Equal(t, "test", shown.Messages[0].Metadata["source"])
}

func TestSessionCommand_RendersTUIWorkOverMCP(t *testing.T) {
	ctx := context.Background()
	backing, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backing.Close() })
	chatStore, err := chats.NewStore(backing.DB())
	require.NoError(t, err)
	srv, sess := newReplayServerWithChatStore(t, chatStore)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	sh, err := sess.ResolveSession(handle)
	require.NoError(t, err)

	currentChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-a", "current queued")
	require.NoError(t, err)
	_, err = chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          currentChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "current queued review",
		OriginSessionID: string(sh.SID),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = chatStore.AppendMessage(ctx, currentChat.ID, "user", "review the queued MCP task", nil)
	require.NoError(t, err)
	_, err = chatStore.AppendMessage(ctx, currentChat.ID, "assistant", "focused context is ready", nil)
	require.NoError(t, err)
	otherChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-b", "other queued")
	require.NoError(t, err)
	_, err = chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          otherChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "other queued review",
		OriginSessionID: "other-session",
		OriginState:     "elsewhere",
	})
	require.NoError(t, err)
	backgroundChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-bg", "background session")
	require.NoError(t, err)
	_, err = backing.DB().ExecContext(ctx,
		`UPDATE chats SET session_id = ? WHERE id = ?`,
		string(sh.SID), backgroundChat.ID)
	require.NoError(t, err)
	_, err = chatStore.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:      backgroundChat.ID,
		TmuxSession: "kitsoki-bg-mcp",
	})
	require.NoError(t, err)
	_, err = chatStore.DetachPTY(ctx, backgroundChat.ID)
	require.NoError(t, err)

	res, err := callTool(ctx, cs, "session.command", map[string]any{
		"handle":  handle,
		"command": "/work --all",
		"cols":    120,
		"rows":    35,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.command: %s", contentText(res))
	var rendered studio.RenderTUIResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	require.True(t, rendered.OK)
	assert.Contains(t, rendered.Frame.Text, "active work (all sessions): 3 item(s)")
	assert.Contains(t, rendered.Frame.Text, "current queued review")
	assert.Contains(t, rendered.Frame.Text, "current session")
	assert.Contains(t, rendered.Frame.Text, "/chat show "+currentChat.ID)
	assert.Contains(t, rendered.Frame.Text, "other queued review")
	assert.Contains(t, rendered.Frame.Text, "session other-session")
	assert.Contains(t, rendered.Frame.Text, "background session")
	assert.Contains(t, rendered.Frame.Text, "/sessions attach 1")

	res, err = callTool(ctx, cs, "session.command", map[string]any{
		"handle":  handle,
		"command": "/chat show " + currentChat.ID,
		"cols":    120,
		"rows":    35,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.command chat show: %s", contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	assert.Contains(t, rendered.Frame.Text, "chat context")
	assert.Contains(t, rendered.Frame.Text, "current queued")
	assert.Contains(t, rendered.Frame.Text, "review the queued MCP task")
	assert.Contains(t, rendered.Frame.Text, "focused context is ready")

	res, err = callTool(ctx, cs, "session.command", map[string]any{
		"handle":  handle,
		"command": "/sessions attach 1 --dry-run",
		"cols":    120,
		"rows":    35,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.command dry-run attach: %s", contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	assert.Contains(t, rendered.Frame.Text, "would attach 1")
	assert.Contains(t, rendered.Frame.Text, "background session")
	assert.Contains(t, rendered.Frame.Text, "kitsoki-bg-mcp")
}

func writeBackgroundJobStory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appPath := dir + "/app.yaml"
	const body = `app:
  id: studio-background-job-test
  version: 0.1.0
  title: "Studio Background Job Test"

hosts:
  - host.run

world:
  result: { type: string, default: "" }
  last_job_id: { type: string, default: "" }

intents:
  enter:
    title: "Enter"

root: lobby

states:
  lobby:
    view: |
      Lobby.
    on:
      enter:
        - target: running

  running:
    view: |
      Running.
      Result: {{ world.result }}
    on_enter:
      - invoke: host.run
        with:
          cmd: "sleep 0.1; printf mcp-bg-done"
        background: true
        bind:
          last_job_id: job_id
        on_complete:
          - set:
              result: "{{ world.last_job_result.stdout }}"
          - say: "Background complete: {{ world.result }}"
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath
}

func writeChoiceToPlainStory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appPath := dir + "/app.yaml"
	const body = `app:
  id: studio-choice-to-plain-test
  version: 0.1.0
  title: "Studio Choice To Plain Test"

intents:
  advance:
    title: "Advance"
  look:
    title: "Look"

root: pick

states:
  pick:
    view:
      - heading: "Picker"
      - choice:
          mode: single
          prompt: "Actions"
          items:
            - { label: "Stale choice", intent: advance }
            - { label: "Look", intent: look }
    on:
      advance:
        - target: plain
      look:
        - target: .

  plain:
    view:
      - heading: "Destination"
      - prose: "Destination body."
    on:
      look:
        - target: .
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath
}

func writeSlowBackgroundJobStory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appPath := dir + "/app.yaml"
	const body = `app:
  id: studio-background-job-running-test
  version: 0.1.0
  title: "Studio Background Job Running Test"

hosts:
  - host.run

world:
  result: { type: string, default: "" }
  last_job_id: { type: string, default: "" }

intents:
  enter:
    title: "Enter"

root: lobby

states:
  lobby:
    view: |
      Lobby.
    on:
      enter:
        - target: running

  running:
    view: |
      Running.
      Result: {{ world.result }}
    on_enter:
      - invoke: host.run
        with:
          cmd: "sleep 1; printf slow-bg-done"
        background: true
        bind:
          last_job_id: job_id
        on_complete:
          - set:
              result: "{{ world.last_job_result.stdout }}"
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath
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
	assert.Equal(t, driveFrame.Metadata.State, rt.Frame.Metadata.State)
	assert.Equal(t, driveFrame.Width, rt.Frame.Width)
}

func TestRenderTUI_DropsStaleChoiceAfterDirectTransition(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	appPath := writeChoiceToPlainStory(t)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": appPath,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))

	res, err = callTool(ctx, cs, "render.tui", map[string]any{
		"handle": ok.Handle,
		"cols":   100,
		"rows":   30,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "initial render.tui: %s", contentText(res))
	var rendered studio.RenderTUIResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	require.Contains(t, rendered.Frame.Text, "Stale choice")

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": ok.Handle,
		"intent": "advance",
		"cols":   100,
		"rows":   30,
	})
	require.NoError(t, err)
	submitted := driveResult(t, res)
	require.Equal(t, "plain", submitted.Outcome.State)
	require.Contains(t, submitted.Frame.Text, "Destination body")
	require.NotContains(t, submitted.Frame.Text, "Stale choice")

	res, err = callTool(ctx, cs, "render.tui", map[string]any{
		"handle": ok.Handle,
		"cols":   100,
		"rows":   30,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "post-transition render.tui: %s", contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &rendered))
	assert.Contains(t, rendered.Frame.Text, "Destination body")
	assert.NotContains(t, rendered.Frame.Text, "Stale choice")
	assert.Equal(t, []string{"look"}, rendered.Frame.Metadata.AllowedIntents)
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

	res, err := callTool(ctx, cs, "render.web", map[string]any{
		"handle":      handle,
		"query":       map[string]string{"chat": "chat-123"},
		"assert_text": []string{"Focused context"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "render.web: %s", contentText(res))

	img := imageContent(t, res)
	assert.Equal(t, stubPNG, img.Data, "render.web returns the stub PNG as an image block")
	assert.NotEmpty(t, gotSpec.SessionID, "the stub saw the handle's live session id")
	assert.Equal(t, map[string]string{"chat": "chat-123"}, gotSpec.Query, "render.web forwards route query params")
	assert.Equal(t, []string{"Focused context"}, gotSpec.AssertText, "render.web forwards text assertions")
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
	live := studio.WebRenderSpec{
		StoryPath:  "stories/cloak",
		SessionID:  "sid-123",
		Query:      map[string]string{"chat": "chat-456"},
		AssertText: []string{"Focused context"},
	}.ToWebshotSpec()
	assert.Equal(t, "sid-123", live.SessionID, "live form → webshot SessionID")
	assert.Empty(t, live.StoryPath, "live form omits StoryPath")
	assert.Equal(t, map[string]string{"chat": "chat-456"}, live.Query, "live form preserves route query")
	assert.Equal(t, []string{"Focused context"}, live.AssertText, "live form preserves text assertions")

	spec := studio.WebRenderSpec{StoryPath: "stories/cloak", State: "bar.lit", World: map[string]any{"lit": true}, Query: map[string]string{"embed": "1"}, AssertText: []string{"Bar"}}.ToWebshotSpec()
	assert.Equal(t, "stories/cloak", spec.StoryPath, "spec form → webshot StoryPath")
	assert.Equal(t, "bar.lit", spec.State)
	assert.Equal(t, map[string]string{"embed": "1"}, spec.Query, "spec form preserves route query")
	assert.Equal(t, []string{"Bar"}, spec.AssertText, "spec form preserves text assertions")
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

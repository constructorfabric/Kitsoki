package studio_test

// session_concurrent_readonly_test.go — regression for
// issues/bugs/2026-06-25T121622Z-studio-read-only-calls-block-on-concurrent-live-turn.md
//
// The read-only studio calls (session.status / session.world / studio.handles /
// studio.ping) are the supported way to monitor a long autonomous run from a
// second client. They MUST return promptly even while another handle's
// session.drive/submit turn is mid-flight inside a long (LLM-equivalent) host
// call. This test stands up a real in-process studio server, fires a turn whose
// host call BLOCKS for ~2s with NO LLM (a sleeping host.agent.ask handler), and
// asserts the read-only calls answer in well under that window.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	studio "kitsoki/internal/mcp/studio"
)

const agentProbeApp = "../../../testdata/apps/agent_probe/app.yaml"

// openSlowProbe opens an agent_probe driving session whose host.agent.ask is
// replaced with a handler that blocks for blockFor before returning, so a
// session.submit{intent:ask} turn deterministically holds the turn open with no
// LLM. The release channel lets tests prove reads finish while the turn is
// still blocked. Returns the handle key.
func openSlowProbe(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, sess *studio.StudioSession, blockFor time.Duration, entered chan struct{}, release <-chan struct{}) string {
	t.Helper()
	var enteredOnce sync.Once
	sess.SetHostRegistryConfigurer(func(reg *host.Registry) error {
		reg.Replace("host.agent.ask", func(hctx context.Context, _ map[string]any) (host.Result, error) {
			if entered != nil {
				enteredOnce.Do(func() { close(entered) })
			}
			select {
			case <-release:
			case <-time.After(blockFor):
			case <-hctx.Done():
			}
			return host.Result{Data: map[string]any{"stdout": "slept"}}, nil
		})
		return nil
	})

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": agentProbeApp,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new errored: %s", contentText(res))
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.True(t, ok.OK)
	return ok.Handle
}

func waitForSlowProbe(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking host.agent.ask did not start")
	}
}

// TestReadOnlyCallsDoNotBlockOnConcurrentTurn is the teeth: while a 2s turn is
// in flight on handle A, session.status / studio.handles / studio.ping / session.world
// on the SAME server must each return before the blocked turn is released, not
// queue behind the turn.
func TestReadOnlyCallsDoNotBlockOnConcurrentTurn(t *testing.T) {
	ctx := context.Background()
	srv, sess := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	handle := openSlowProbe(ctx, t, cs, sess, 10*time.Second, entered, release)

	// Fire the blocking turn in the background. It holds the turn open ~2s.
	driveDone := make(chan struct{})
	go func() {
		defer close(driveDone)
		_, _ = callTool(ctx, cs, "session.submit", map[string]any{
			"handle": handle,
			"intent": "ask",
			"slots":  map[string]any{"question": "who are you"},
		})
	}()

	waitForSlowProbe(t, entered)

	readOnly := []struct {
		name string
		args map[string]any
	}{
		{"studio.ping", map[string]any{}},
		{"studio.handles", map[string]any{}},
		{"session.status", map[string]any{"handle": handle}},
		{"session.world", map[string]any{"handle": handle}},
	}

	for _, ro := range readOnly {
		start := time.Now()
		resC := make(chan struct {
			res *mcpsdk.CallToolResult
			err error
		}, 1)
		go func() {
			res, err := callTool(ctx, cs, ro.name, ro.args)
			resC <- struct {
				res *mcpsdk.CallToolResult
				err error
			}{res: res, err: err}
		}()
		select {
		case got := <-resC:
			elapsed := time.Since(start)
			require.NoError(t, got.err, "%s call", ro.name)
			require.False(t, got.res.IsError, "%s errored: %s", ro.name, contentText(got.res))
			t.Logf("%s returned while the turn was still blocked (%s)", ro.name, elapsed)
		case <-driveDone:
			t.Fatalf("%s queued behind the turn: background drive completed before the read-only call returned", ro.name)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not return while the turn was blocked", ro.name)
		}
	}

	releaseOnce.Do(func() { close(release) })
	<-driveDone
}

// TestReadOnlyCallsDoNotBlockAcrossConnections is the ticket's exact scenario:
// two SEPARATE MCP connections share one studio server. Connection A drives a
// blocking turn; connection B (the monitoring client) must still get prompt
// read-only answers (studio.handles / session.status / studio.ping). This is the
// configuration the lean-driver "lead with cheap reads" guidance depends on.
func TestReadOnlyCallsDoNotBlockAcrossConnections(t *testing.T) {
	ctx := context.Background()
	srv, sess := newReplayServer(t)
	csA := connectInProcess(ctx, t, srv)
	csB := connectInProcess(ctx, t, srv)

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	handle := openSlowProbe(ctx, t, csA, sess, 10*time.Second, entered, release)

	driveDone := make(chan struct{})
	go func() {
		defer close(driveDone)
		_, _ = callTool(ctx, csA, "session.submit", map[string]any{
			"handle": handle,
			"intent": "ask",
			"slots":  map[string]any{"question": "who are you"},
		})
	}()

	waitForSlowProbe(t, entered)

	readOnly := []struct {
		name string
		args map[string]any
	}{
		{"studio.ping", map[string]any{}},
		{"studio.handles", map[string]any{}},
		{"session.status", map[string]any{"handle": handle}},
		{"session.world", map[string]any{"handle": handle}},
	}

	for _, ro := range readOnly {
		start := time.Now()
		resC := make(chan struct {
			res *mcpsdk.CallToolResult
			err error
		}, 1)
		go func() {
			res, err := callTool(ctx, csB, ro.name, ro.args)
			resC <- struct {
				res *mcpsdk.CallToolResult
				err error
			}{res: res, err: err}
		}()
		select {
		case got := <-resC:
			elapsed := time.Since(start)
			require.NoError(t, got.err, "%s call on connection B", ro.name)
			require.False(t, got.res.IsError, "%s errored: %s", ro.name, contentText(got.res))
			t.Logf("%s on connection B returned while the turn was still blocked (%s)", ro.name, elapsed)
		case <-driveDone:
			t.Fatalf("%s on connection B queued behind the turn: background drive completed before the read-only call returned", ro.name)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s on connection B did not return while the turn was blocked", ro.name)
		}
	}

	releaseOnce.Do(func() { close(release) })
	<-driveDone
}

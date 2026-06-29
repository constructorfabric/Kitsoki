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
// LLM. Returns the handle key.
func openSlowProbe(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, sess *studio.StudioSession, blockFor time.Duration) string {
	t.Helper()
	sess.SetHostRegistryConfigurer(func(reg *host.Registry) error {
		reg.Replace("host.agent.ask", func(hctx context.Context, _ map[string]any) (host.Result, error) {
			select {
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

// TestReadOnlyCallsDoNotBlockOnConcurrentTurn is the teeth: while a 2s turn is
// in flight on handle A, session.status / studio.handles / studio.ping / session.world
// on the SAME server must each return well within that window (< 500ms), not
// queue behind the turn.
func TestReadOnlyCallsDoNotBlockOnConcurrentTurn(t *testing.T) {
	ctx := context.Background()
	srv, sess := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openSlowProbe(ctx, t, cs, sess, 2*time.Second)

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

	// Give the turn a moment to actually enter the blocking host call.
	time.Sleep(150 * time.Millisecond)

	readOnly := []struct {
		name string
		args map[string]any
	}{
		{"studio.ping", map[string]any{}},
		{"studio.handles", map[string]any{}},
		{"session.status", map[string]any{"handle": handle}},
		{"session.world", map[string]any{"handle": handle}},
	}

	const bound = 500 * time.Millisecond
	for _, ro := range readOnly {
		start := time.Now()
		res, err := callTool(ctx, cs, ro.name, ro.args)
		elapsed := time.Since(start)
		require.NoError(t, err, "%s call", ro.name)
		require.False(t, res.IsError, "%s errored: %s", ro.name, contentText(res))
		require.Less(t, elapsed, bound,
			"%s must return promptly while a turn is in flight; took %s (the concurrent drive blocks for ~2s)",
			ro.name, elapsed)
	}

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

	handle := openSlowProbe(ctx, t, csA, sess, 2*time.Second)

	driveDone := make(chan struct{})
	go func() {
		defer close(driveDone)
		_, _ = callTool(ctx, csA, "session.submit", map[string]any{
			"handle": handle,
			"intent": "ask",
			"slots":  map[string]any{"question": "who are you"},
		})
	}()

	time.Sleep(150 * time.Millisecond)

	readOnly := []struct {
		name string
		args map[string]any
	}{
		{"studio.ping", map[string]any{}},
		{"studio.handles", map[string]any{}},
		{"session.status", map[string]any{"handle": handle}},
		{"session.world", map[string]any{"handle": handle}},
	}

	const bound = 500 * time.Millisecond
	for _, ro := range readOnly {
		start := time.Now()
		res, err := callTool(ctx, csB, ro.name, ro.args)
		elapsed := time.Since(start)
		require.NoError(t, err, "%s call on connection B", ro.name)
		require.False(t, res.IsError, "%s errored: %s", ro.name, contentText(res))
		require.Less(t, elapsed, bound,
			"%s on connection B must return promptly while connection A's turn is in flight; took %s",
			ro.name, elapsed)
	}

	<-driveDone
}

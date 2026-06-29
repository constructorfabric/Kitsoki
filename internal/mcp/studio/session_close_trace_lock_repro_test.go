package studio_test

// session_close_trace_lock_repro_test.go — RED reproduction for bug
// 2026-06-25T000000Z-mcp-no-session-close-trace-lock-squat:
//
//   The MCP studio exposes session.new / attach / drive / submit / continue /
//   answer / status / teleport / inspect / world / command / trace, but NO
//   session.close (or release/end). A driving session opens its JSONL trace via
//   store.OpenJSONL, which takes an exclusive flock (LOCK_EX|LOCK_NB) on the
//   trace path and only releases it in JSONLSink.Close — reached solely through
//   StudioSession.CloseSession, which has no MCP tool wired to it. So an external
//   agent that opens a session on a trace path can never release it over MCP:
//   the lock is squatted for the life of the server process, and any rerun on
//   that same trace path is bricked with "trace file is locked by another
//   writer".
//
// This test asserts the CORRECT behaviour an MCP client must have: it can
// release a session it opened and then reuse the same trace path. It is RED on
// the unfixed tree (no close tool exists) and passes for ANY fix that exposes an
// MCP-driven way to release the session and free the lock — it does not pin the
// tool name (close|release|end accepted) nor any internal symbol or error
// string.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// newCloakOnTrace opens a replay-backed cloak session bound to an explicit trace
// path (so store.OpenJSONL takes the exclusive flock) and returns the handle.
func newCloakOnTrace(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, trace string) (string, *mcpsdk.CallToolResult) {
	t.Helper()
	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"trace":      trace,
	})
	require.NoError(t, err)
	if res.IsError {
		return "", res
	}
	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	return ok.Handle, res
}

// findSessionReleaseTool scans the live MCP tool registry for a session
// close/release/end tool — mechanism-agnostic: any session.* tool whose verb
// reads as a teardown qualifies.
func findSessionReleaseTool(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession) string {
	t.Helper()
	lt, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	for _, tl := range lt.Tools {
		n := strings.ToLower(tl.Name)
		if !strings.HasPrefix(n, "session.") {
			continue
		}
		if strings.Contains(n, "close") || strings.Contains(n, "release") || strings.Contains(n, "end") {
			return tl.Name
		}
	}
	return ""
}

// TestMCPSessionClose_FreesTraceLockForRerun is the reproduction. An MCP client
// must be able to release a session and rerun on the same trace path without
// restarting the server.
func TestMCPSessionClose_FreesTraceLockForRerun(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	trace := t.TempDir() + "/trace.jsonl"

	// 1. Open a driving session on an explicit trace path → takes the flock.
	h1, res1 := newCloakOnTrace(ctx, t, cs, trace)
	require.False(t, res1.IsError, "first session.new must succeed: %s", contentText(res1))

	// Evidence the lock is real and held: reopening the SAME path WITHOUT
	// releasing fails (invariant true before and after any fix).
	_, resHeld := newCloakOnTrace(ctx, t, cs, trace)
	require.True(t, resHeld.IsError,
		"sanity: while the first session is open it must hold the trace-path lock")
	require.Contains(t, strings.ToLower(contentText(resHeld)), "lock",
		"reopen while held should report the lock contention")

	// 2. The GATING assertion: the MCP surface must expose a way to release the
	//    session. On the unfixed tree there is none → RED here.
	releaseTool := findSessionReleaseTool(ctx, t, cs)
	require.NotEmpty(t, releaseTool,
		"BUG: MCP studio exposes no session close/release tool — a live session "+
			"squats its trace-path exclusive lock forever and bricks any rerun on that path")

	// 3. Release the session over MCP.
	relRes, err := callTool(ctx, cs, releaseTool, map[string]any{"handle": h1})
	require.NoError(t, err)
	require.False(t, relRes.IsError,
		"%s must release the session, got: %s", releaseTool, contentText(relRes))

	// 4. Rerun on the SAME trace path must now succeed — the lock was freed.
	_, resReuse := newCloakOnTrace(ctx, t, cs, trace)
	require.False(t, resReuse.IsError,
		"after releasing the session, a rerun on the same trace path must succeed, got: %s",
		contentText(resReuse))
}

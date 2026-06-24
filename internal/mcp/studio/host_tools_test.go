package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// host_tools_test.go — verification for the standalone gate-runner
// (issues/bugs/2026-06-23T092410Z-mcp-no-standalone-gate-runner.md). Every test
// is deterministic and LLM-free: it runs trivial shell commands against a temp
// dir and asserts the {ok, exit_code, stdout} contract — including the crucial
// "a non-zero exit is DATA, not a tool error" gate semantic.

// newStudioHostRunner builds a studio server with no workspace bound (host.run
// names its own dir, so it needs none) and returns an in-process client.
func newStudioHostRunner(ctx context.Context, t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	sess := studio.NewStudioSession(stubBuilder())
	srv := studio.NewServer(sess)
	return connectInProcess(ctx, t, srv)
}

// TestHostRun_GreenGate is the happy path: a passing command returns ok:true,
// exit_code 0, and its combined output. This is the gate-runner re-confirming a
// committed tip is GREEN outside any session.
func TestHostRun_GreenGate(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := callTool(ctx, cs, "host.run", map[string]any{
		"dir": t.TempDir(),
		"cmd": "echo gate-green",
	})
	require.NoError(t, err, "host.run call")
	require.False(t, res.IsError, "a successful command must not be a tool error: %s", contentText(res))

	var got studio.HostRunOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK, "exit 0 → ok")
	assert.Equal(t, 0, got.ExitCode)
	assert.Contains(t, got.Stdout, "gate-green")
}

// TestHostRun_RedGateIsData is the load-bearing assertion: a NON-ZERO exit is a
// normal result ({ok:false, exit_code}), NOT a transport/tool error. The whole
// point of an independent gate is to read a RED result as data and act on it —
// if it surfaced as a tool error the caller couldn't distinguish "gate failed"
// from "the tool broke".
func TestHostRun_RedGateIsData(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := callTool(ctx, cs, "host.run", map[string]any{
		"dir": t.TempDir(),
		"cmd": "echo failing-test >&2; exit 3",
	})
	require.NoError(t, err, "host.run call")
	require.False(t, res.IsError, "a non-zero exit must be DATA, not a tool error: %s", contentText(res))

	var got studio.HostRunOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.False(t, got.OK, "exit 3 → not ok")
	assert.Equal(t, 3, got.ExitCode)
	assert.Contains(t, got.Stdout, "failing-test", "combined output captures stderr")
}

// TestHostRun_ArgsMode exercises the no-shell argv path: cmd run directly with
// positional args, no word-splitting/glob expansion.
func TestHostRun_ArgsMode(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := callTool(ctx, cs, "host.run", map[string]any{
		"dir":  t.TempDir(),
		"cmd":  "echo",
		"args": []any{"one two", "$HOME"}, // a single argv element; no expansion
	})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))

	var got studio.HostRunOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	// $HOME stays literal (no shell), and the spaced element is one argv token.
	assert.Contains(t, got.Stdout, "$HOME")
	assert.Equal(t, "one two $HOME", strings.TrimSpace(got.Stdout))
}

// TestHostRun_MissingDir rejects a call with no dir — a gate must name the tree
// it gates, never silently run against the server's cwd. `dir` is a required
// schema property, so the SDK rejects the call at the transport layer (the
// handler's own guard is belt-and-suspenders for direct callers).
func TestHostRun_MissingDir(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	_, err := callTool(ctx, cs, "host.run", map[string]any{"cmd": "echo hi"})
	require.Error(t, err, "missing dir must be rejected")
	assert.Contains(t, err.Error(), "dir", "rejection names the missing required field")
}

// TestHostRun_BadDir rejects a dir that is not an accessible directory.
func TestHostRun_BadDir(t *testing.T) {
	ctx := context.Background()
	cs := newStudioHostRunner(ctx, t)

	res, err := callTool(ctx, cs, "host.run", map[string]any{
		"dir": t.TempDir() + "/does-not-exist",
		"cmd": "echo hi",
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "non-existent dir must be a tool error")
	assert.Contains(t, contentText(res), "not an accessible directory")
}

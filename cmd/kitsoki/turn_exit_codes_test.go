package main

// turn_exit_codes_test.go — integration tests for kitsoki turn exit codes.
//
// Finding 2.2/2.4: missing app file → exit 3, malformed slot → exit 3,
// intent that doesn't exist → exit 1 (true rejection), success → exit 0.

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// cmdNoopHarness is a zero-behavior harness for tests that drive via SubmitDirect.
type cmdNoopHarness struct{}

func (cmdNoopHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (cmdNoopHarness) Close() error { return nil }

// runTurnCmd calls runTraceTurn with a cobra.Command that captures stdout/stderr
// and returns the error (which encodes the exit code).
func runTurnCmdCapture(t *testing.T, appPath, tracePath, intentName string, slotPairs []string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var stdoutBuf, stderrBuf bytes.Buffer
	root.SetOut(&stdoutBuf)
	root.SetErr(&stderrBuf)

	args := []string{"turn", "--app", appPath, "--trace", tracePath, "--intent", intentName}
	for _, sp := range slotPairs {
		args = append(args, "--slot", sp)
	}
	root.SetArgs(args)
	err = root.Execute()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// TestTurnExitCode_MissingApp verifies that a missing app file → exit 3 (infra error).
func TestTurnExitCode_MissingApp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runTurnCmdCapture(t,
		filepath.Join(dir, "nonexistent_app.yaml"),
		filepath.Join(dir, "trace.jsonl"),
		"go",
		nil,
	)
	code, ok := IsTurnExitError(err)
	require.True(t, ok, "missing app must return a turnExitError, got: %v", err)
	require.Equal(t, turnExitInfraError, code,
		"missing app file must exit 3 (infra error), got exit %d", code)
}

// TestTurnExitCode_MalformedSlot verifies that a malformed --slot → exit 3 (infra error).
func TestTurnExitCode_MalformedSlot(t *testing.T) {
	t.Parallel()
	appPath := "../../testdata/apps/cloak/app.yaml"
	dir := t.TempDir()

	_, _, err := runTurnCmdCapture(t, appPath, filepath.Join(dir, "trace.jsonl"), "go",
		[]string{"noequalssign"}) // malformed: no "=" character
	code, ok := IsTurnExitError(err)
	require.True(t, ok, "malformed slot must return a turnExitError, got: %v", err)
	require.Equal(t, turnExitInfraError, code,
		"malformed slot must exit 3 (infra error), got exit %d", code)
}

// TestTurnExitCode_RejectedIntent verifies that an intent not allowed in the
// current state → exit 1 (true rejection, not infra error).
func TestTurnExitCode_RejectedIntent(t *testing.T) {
	t.Parallel()
	appPath := "../../testdata/apps/cloak/app.yaml"
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")

	_, _, err := runTurnCmdCapture(t, appPath, tracePath, "nonexistent_intent_xyz", nil)
	code, ok := IsTurnExitError(err)
	require.True(t, ok, "rejected intent must return a turnExitError, got: %v", err)
	require.Equal(t, turnExitRejected, code,
		"rejected intent must exit 1, got exit %d", code)
}

// TestTurnExitCode_Success verifies that an accepted turn → exit 0.
func TestTurnExitCode_Success(t *testing.T) {
	t.Parallel()
	appPath := "../../testdata/apps/cloak/app.yaml"
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")

	_, _, err := runTurnCmdCapture(t, appPath, tracePath, "go",
		[]string{"direction=west"})
	require.NoError(t, err, "accepted intent must exit 0 (nil error)")
}

// TestTurnExitCode_Terminal verifies that reaching a terminal state → exit 2.
// We drive the cloak story to the ending via SubmitDirect and then do one more
// turn that lands in the terminal state. We confirm exit 2 is returned.
func TestTurnExitCode_Terminal(t *testing.T) {
	t.Parallel()

	// Build a cloak session via SubmitDirect to the terminal state,
	// write the trace, then run one more turn that was already in-terminal.
	// (After 'ended', any further intent should be... let's check what cloak does.)
	// Actually the simplest path: run the full winning sequence via SubmitDirect
	// and confirm the last turn returns exit 2 via runTurnCmdCapture.

	appPath := "../../testdata/apps/cloak/app.yaml"
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "cloak_terminal.jsonl")

	def, err := app.Load(appPath)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, cmdNoopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Drive to terminal state: foyer→cloakroom→hang_cloak→foyer→bar.lit→read_message(ended)
	steps := []struct {
		intent string
		slots  map[string]any
	}{
		{"go", map[string]any{"direction": "west"}},
		{"hang_cloak", nil},
		{"go", map[string]any{"direction": "east"}},
		{"go", map[string]any{"direction": "south"}},
	}
	for _, step := range steps {
		out, err := orch.SubmitDirect(ctx, sid, step.intent, step.slots)
		require.NoError(t, err)
		require.Equal(t, orchestrator.ModeTransitioned, out.Mode,
			"step %q must transition", step.intent)
	}
	require.NoError(t, sink.Close())

	// The final 'read_message' turn via the CLI should return exit 2 (terminal).
	_, _, err = runTurnCmdCapture(t, appPath, tracePath, "read_message", nil)
	code, ok := IsTurnExitError(err)
	require.True(t, ok, "terminal turn must return turnExitError, got: %v", err)
	require.Equal(t, turnExitTerminal, code,
		"terminal turn must exit 2, got exit %d", code)
}

package mcp_test

// validator_statefile_test.go — coverage for the optional StateFilePath
// persistence layer. Each test stands up a validator with a state file,
// drives some submit calls, then re-instantiates a *new* ValidatorServer
// with the same StateFilePath to verify the counters were resumed.
//
// This is the mechanism host.oracle.ask_with_mcp uses to make a logical
// validator session span multiple `claude --resume` re-engagements: the
// orchestrator's MCP-config tells the validator subprocess to read/write
// the same state file across iterations.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitsokimcp "kitsoki/internal/mcp"
)

// TestValidator_StateFile_PersistsAcrossInstances drives a fail-then-success
// across two ValidatorServer instances that share the same state file. The
// second instance must observe attempts==1 at start, then advance to
// attempts==2 / successfulSubmits==1 after its own successful call.
func TestValidator_StateFile_PersistsAcrossInstances(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")

	// Iteration 1: one schema-failing submit.
	cs1, _, done1 := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		StateFilePath: stateFile,
	})
	r1, err := cs1.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: map[string]any{"bogus": "x"},
	})
	require.NoError(t, err)
	require.True(t, r1.IsError)
	done1()

	// State file must now exist with attempts=1, successful=0.
	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	var st1 struct {
		Attempts          int    `json:"attempts"`
		SuccessfulSubmits int    `json:"successful_submits"`
		LastError         string `json:"last_error"`
	}
	require.NoError(t, json.Unmarshal(raw, &st1))
	assert.Equal(t, 1, st1.Attempts)
	assert.Equal(t, 0, st1.SuccessfulSubmits)
	assert.NotEmpty(t, st1.LastError)

	// Iteration 2: a fresh ValidatorServer reading the same state file.
	// The counters from iteration 1 must be restored.
	cs2, srv2, done2 := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		StateFilePath: stateFile,
	})
	defer done2()

	attemptsAtStart, successAtStart, lastErrAtStart := srv2.Stats()
	assert.Equal(t, 1, attemptsAtStart, "fresh server must seed counters from state file")
	assert.Equal(t, 0, successAtStart)
	assert.NotEmpty(t, lastErrAtStart)

	// One successful submit drives outcome to Success.
	r2, err := cs2.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, r2.IsError)

	attemptsEnd, successEnd, _ := srv2.Stats()
	assert.Equal(t, 2, attemptsEnd, "iteration 2 increments the resumed counter")
	assert.Equal(t, 1, successEnd)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv2.Outcome())

	// State file is rewritten with the latest values.
	raw, err = os.ReadFile(stateFile)
	require.NoError(t, err)
	var st2 struct {
		Attempts          int `json:"attempts"`
		SuccessfulSubmits int `json:"successful_submits"`
	}
	require.NoError(t, json.Unmarshal(raw, &st2))
	assert.Equal(t, 2, st2.Attempts)
	assert.Equal(t, 1, st2.SuccessfulSubmits)
}

// TestValidator_StateFile_MissingFileIsFreshSession — pointing at a path
// that does not yet exist must not be fatal; the validator starts with
// zero counters and creates the file on the first submit.
func TestValidator_StateFile_MissingFileIsFreshSession(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "does-not-exist-yet.json")

	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		StateFilePath: stateFile,
	})
	defer done()

	attempts, success, _ := srv.Stats()
	assert.Equal(t, 0, attempts, "missing file = fresh session")
	assert.Equal(t, 0, success)

	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)

	// File created and contains attempts=1.
	_, err = os.Stat(stateFile)
	require.NoError(t, err, "state file must be created on first submit")
}

// TestValidator_StateFile_MalformedFileTreatedAsFresh — a corrupt or
// non-JSON state file must not crash the server. We just start fresh.
func TestValidator_StateFile_MalformedFileTreatedAsFresh(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "garbage.json")
	require.NoError(t, os.WriteFile(stateFile, []byte("not json"), 0o644))

	_, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		StateFilePath: stateFile,
	})
	defer done()

	attempts, success, lastErr := srv.Stats()
	assert.Equal(t, 0, attempts)
	assert.Equal(t, 0, success)
	assert.Empty(t, lastErr, "malformed state must be silently treated as fresh")
}

// TestValidator_StateFile_EmptyPathIsVolatile — when StateFilePath is
// empty, no file is created. This is the default behaviour and must not
// regress.
func TestValidator_StateFile_EmptyPathIsVolatile(t *testing.T) {
	dir := t.TempDir()
	cs, _, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{})
	defer done()
	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no files must be written when StateFilePath is empty")
}

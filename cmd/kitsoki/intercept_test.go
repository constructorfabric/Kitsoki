package main

// intercept_test.go — integration tests for the `kitsoki intercept` pre-LLM
// gate command. All deterministic and free: no LLM, no host calls, no
// persistence (the gate classifies via the no-LLM tiers and executes a
// confident match through an in-memory OneShot).
//
// Cases:
//   - a synonym for a command intent -> matched:true, right intent, exit 0;
//   - unrelated gibberish            -> matched:false, reason "no_match", exit 10;
//   - stdin JSON {"prompt":<synonym>} (no --input) -> same as the matched case,
//     proving the Stage-3 hook path (Claude's UserPromptSubmit JSON piped in).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// interceptTestApp is a routing-enabled app whose `start` room allows one
// no-slot command intent ("go_north", synonym "head north") that transitions to
// a non-terminal room (so the OneShot lands ModeTransitioned -> exit 0). It uses
// only an effects/say body — no host calls — so no host registry or cassette is
// needed and nothing can reach an LLM.
const interceptTestApp = `
app:
  id: intercept-test
  version: 0.1.0

world: {}

routing:
  enabled: true

intents:
  go_north:
    title: "Go north"
    examples: ["go north"]
    synonyms: ["head north"]

root: start

states:
  start:
    view: "compass rose"
    on:
      go_north:
        - target: clearing
          effects:
            - say: "You head north into a clearing."

  clearing:
    view: "a quiet clearing"
`

// writeInterceptApp writes interceptTestApp into a temp dir and returns its path.
func writeInterceptApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte(interceptTestApp), 0o644))
	return path
}

// runInterceptCmd drives interceptCmd via a captured root command. stdin is the
// raw bytes fed on the command's input stream (used for the stdin-JSON case).
func runInterceptCmd(t *testing.T, appPath string, args []string, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var stdoutBuf, stderrBuf bytes.Buffer
	root.SetOut(&stdoutBuf)
	root.SetErr(&stderrBuf)
	if stdin != "" {
		root.SetIn(strings.NewReader(stdin))
	} else {
		// Empty reader so a missing --input doesn't block on the real stdin.
		root.SetIn(strings.NewReader(""))
	}

	// Point --config at a guaranteed-absent path so the bar-fallback Load (which
	// fires because --bar is unset) never picks up an ambient cwd .kitsoki.yaml.
	absentCfg := filepath.Join(t.TempDir(), "no-such.kitsoki.yaml")
	full := append([]string{"intercept", "--app", appPath, "--room", "start", "--config", absentCfg}, args...)
	root.SetArgs(full)
	err = root.Execute()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// TestIntercept_SynonymMatchExecutes — a synonym for go_north clears the gate,
// executes the transition, and exits 0 with matched:true and the right intent.
func TestIntercept_SynonymMatchExecutes(t *testing.T) {
	t.Parallel()
	appPath := writeInterceptApp(t)

	stdout, _, err := runInterceptCmd(t, appPath, []string{"--input", "head north"}, "")
	require.NoError(t, err, "a matched, executed transition must exit 0 (nil error)")

	var out interceptOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	require.True(t, out.Matched, "a synonym hit above the bar must intercept")
	require.Equal(t, "go_north", out.Intent)
	require.Equal(t, 0, out.Exit)
	require.NotNil(t, out.Result)
	require.Equal(t, "transitioned", out.Result.Mode.String())
}

// TestIntercept_GibberishPassesThrough — unrelated input matches no no-LLM tier,
// so the gate passes through: matched:false, reason "no_match", exit 10.
func TestIntercept_GibberishPassesThrough(t *testing.T) {
	t.Parallel()
	appPath := writeInterceptApp(t)

	stdout, stderr, err := runInterceptCmd(t, appPath, []string{"--input", "totally unrelated gibberish"}, "")

	code, ok := IsInterceptExitError(err)
	require.True(t, ok, "pass-through must return an interceptExitError, got: %v", err)
	require.Equal(t, interceptExitPassThrough, code, "pass-through must exit 10")
	require.NotContains(t, stderr, "error:", "pass-through must not print an error line")

	var out interceptOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	require.False(t, out.Matched)
	require.Equal(t, "no_match", out.Reason)
	require.Equal(t, interceptExitPassThrough, out.Exit)
}

// TestIntercept_StdinPromptJSON — with no --input, the gate reads stdin; the
// UserPromptSubmit JSON shape {"prompt":<synonym>} is unwrapped and routed,
// proving the Stage-3 hook path.
func TestIntercept_StdinPromptJSON(t *testing.T) {
	t.Parallel()
	appPath := writeInterceptApp(t)

	stdin := `{"prompt":"head north","session_id":"abc"}`
	stdout, _, err := runInterceptCmd(t, appPath, nil, stdin)
	require.NoError(t, err, "the stdin-JSON synonym must intercept and exit 0")

	var out interceptOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	require.True(t, out.Matched, "stdin {\"prompt\"} must be unwrapped and routed")
	require.Equal(t, "go_north", out.Intent)
	require.Equal(t, 0, out.Exit)
}

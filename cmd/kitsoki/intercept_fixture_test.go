package main

// intercept_fixture_test.go — CLI smoke tests for `kitsoki intercept` against
// the on-disk stories/intercept-demo fixture. Unlike intercept_test.go (which
// writes a minimal inline app with no host calls), these exercise the REAL
// fixture story end to end: a natural dev-command phrasing routes to its intent
// and the EXECUTE path fires host.run (a benign `echo`, safe and deterministic
// for a real run), while an unrelated question passes through to the LLM.
//
// All deterministic and free: no LLM, no cassette. The host.run cmd in the
// fixture is an `echo`, so the real shell invocation is harmless in CI.
//
// Cases:
//   - "rebase this onto main"        -> matched:true, intent "rebase", exit 0,
//     and the result carries the executed host.run host_call;
//   - "what does this function do?"  -> matched:false, reason "no_match", exit 10.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// interceptFixtureApp resolves stories/intercept-demo/app.yaml relative to the
// cmd/kitsoki package directory. Tests run with cwd == the package dir, so two
// `..` segments reach the repo root (mirroring the testdata/apps resolution used
// across this package's other tests).
func interceptFixtureApp(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "stories", "intercept-demo", "app.yaml")
}

// runInterceptFixtureCmd drives interceptCmd against the fixture app, pointing
// --config at a guaranteed-absent path so the bar-fallback config Load (which
// fires because --bar is unset) never picks up an ambient .kitsoki.yaml.
func runInterceptFixtureCmd(t *testing.T, appPath, input string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var stdoutBuf, stderrBuf bytes.Buffer
	root.SetOut(&stdoutBuf)
	root.SetErr(&stderrBuf)
	root.SetIn(strings.NewReader(""))

	absentCfg := filepath.Join(t.TempDir(), "no-such.kitsoki.yaml")
	root.SetArgs([]string{
		"intercept",
		"--app", appPath,
		"--room", "commands",
		"--config", absentCfg,
		"--input", input,
	})
	err = root.Execute()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// TestInterceptFixture_CommandMatchExecutes — a natural dev-command phrasing
// ("rebase this onto main") clears the gate, executes the `rebase` arc, exits 0,
// and the result JSON shows the host.run host_call actually ran.
func TestInterceptFixture_CommandMatchExecutes(t *testing.T) {
	t.Parallel()
	appPath := interceptFixtureApp(t)

	stdout, _, err := runInterceptFixtureCmd(t, appPath, "rebase this onto main")
	require.NoError(t, err, "a matched, executed command must exit 0 (nil error)")

	var out interceptOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	require.True(t, out.Matched, "a declared example must intercept")
	require.Equal(t, "rebase", out.Intent)
	require.Equal(t, 0, out.Exit)

	require.NotNil(t, out.Result, "an intercept must carry the OneShot result")
	require.Equal(t, "transitioned", out.Result.Mode.String())

	// The EXECUTE path must have actually fired host.run (no LLM, no mock — the
	// fixture's echo ran for real). Find the host.run summary in the result.
	var ran bool
	for _, hc := range out.Result.HostCalls {
		if hc.Namespace == "host.run" {
			ran = true
			break
		}
	}
	require.True(t, ran, "the intercepted rebase arc must execute a host.run call; host_calls=%+v", out.Result.HostCalls)
}

// TestInterceptFixture_QuestionPassesThrough — a free-text question that matches
// no command intent passes through so the prompt reaches the LLM untouched:
// matched:false, reason "no_match", exit 10, and no "error:" line on stderr.
func TestInterceptFixture_QuestionPassesThrough(t *testing.T) {
	t.Parallel()
	appPath := interceptFixtureApp(t)

	stdout, stderr, err := runInterceptFixtureCmd(t, appPath, "what does this function do?")

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

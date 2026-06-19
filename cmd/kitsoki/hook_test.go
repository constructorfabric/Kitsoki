package main

// hook_test.go — integration tests for `kitsoki hook run` and `kitsoki hook
// install`. All deterministic and free: no LLM, no real host calls, no
// persistence. `hook run` reuses the in-process intercept engine over an
// in-memory OneShot; `hook install` only touches a temp settings.json.
//
// Cases:
//   - run: a synonym prompt -> {"decision":"block","reason":…} carrying the
//     marked prefix and the intent (the Stage-3 happy path);
//   - run: an unrelated prompt -> empty stdout, exit 0 (silent pass-through);
//   - run: a binding at a non-existent app -> FAIL-OPEN: empty stdout, exit 0;
//   - install (no --write): prints a diff, leaves the file unchanged;
//   - install --write: writes the hook; a second --write is idempotent.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/webconfig"
)

// hookTestApp mirrors interceptTestApp: a routing-enabled story whose `start`
// room allows one no-slot command intent ("go_north", synonym "head north")
// transitioning to a non-terminal room. Effects/say only — no host calls — so
// nothing can reach an LLM and the engine's OneShot lands ModeTransitioned.
const hookTestApp = `
app:
  id: hook-test
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

// writeHookFixture builds a temp dir holding the app + a .kitsoki.yaml whose
// intercept block points at it (app path relative to the dir), and returns the
// dir. The dir doubles as the prompt's cwd in the run-hook JSON.
func writeHookFixture(t *testing.T, appName string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(hookTestApp), 0o644))
	cfg := "intercept:\n  enabled: true\n  app: " + appName + "\n  room: start\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, webconfig.DefaultConfigFile), []byte(cfg), 0o644))
	return dir
}

// runHookRun drives `hook run --agent claude` with the given stdin JSON through
// a captured root command, returning stdout/stderr and the Execute error.
func runHookRun(t *testing.T, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs([]string{"hook", "run", "--agent", "claude"})
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestHookRun_SynonymBlocks — a synonym prompt clears the gate, executes the
// transition in-process, and the shim blocks the prompt with a marked report
// naming the intent (the Stage-3 UserPromptSubmit happy path).
func TestHookRun_SynonymBlocks(t *testing.T) {
	t.Parallel()
	dir := writeHookFixture(t, "app.yaml")

	stdinJSON := mustJSON(t, hookPromptInput{Prompt: "head north", SessionID: "s1", Cwd: dir})
	stdout, _, err := runHookRun(t, stdinJSON)
	require.NoError(t, err, "a clean intercept must exit 0 with a block decision")

	var dec hookBlockDecision
	require.NoError(t, json.Unmarshal([]byte(stdout), &dec), "stdout must be a block decision JSON")
	require.Equal(t, "block", dec.Decision)
	require.Contains(t, dec.Reason, "⌁ kitsoki handled this (no LLM)", "report must carry the marked attribution prefix")
	require.Contains(t, dec.Reason, "go_north", "report must name the intercepted intent")
	require.Contains(t, dec.Reason, "⟲ recorded in the kitsoki trace", "report must carry the trace note")
}

// TestHookRun_UnrelatedPassesThrough — an unrelated prompt matches no no-LLM
// tier, so the shim passes through silently: empty stdout, exit 0.
func TestHookRun_UnrelatedPassesThrough(t *testing.T) {
	t.Parallel()
	dir := writeHookFixture(t, "app.yaml")

	stdinJSON := mustJSON(t, hookPromptInput{Prompt: "totally unrelated gibberish", Cwd: dir})
	stdout, _, err := runHookRun(t, stdinJSON)
	require.NoError(t, err, "a pass-through must exit 0 (nil error)")
	require.Empty(t, stdout, "pass-through must print nothing (the prompt proceeds to the model)")
}

// TestHookRun_MissingAppFailsOpen — the binding points at a non-existent app,
// forcing an engine infra error. The shim must FAIL OPEN: empty stdout, exit 0
// (a broken interceptor never wedges the agent).
func TestHookRun_MissingAppFailsOpen(t *testing.T) {
	t.Parallel()
	dir := writeHookFixture(t, "does-not-exist.yaml")

	stdinJSON := mustJSON(t, hookPromptInput{Prompt: "head north", Cwd: dir})
	stdout, _, err := runHookRun(t, stdinJSON)
	require.NoError(t, err, "a failing interceptor must still exit 0 (fail-open)")
	require.Empty(t, stdout, "fail-open must print nothing so the prompt proceeds untouched")
}

// TestHookRun_EscapePrefixPassesThrough — a prompt starting with the configured
// escape_prefix is a forced pass-through: even though the rest is a synonym, the
// shim emits nothing and exits 0 (the user opted this prompt out of the gate).
func TestHookRun_EscapePrefixPassesThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(hookTestApp), 0o644))
	cfg := "intercept:\n  enabled: true\n  app: app.yaml\n  room: start\n  escape_prefix: \"!\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, webconfig.DefaultConfigFile), []byte(cfg), 0o644))

	stdinJSON := mustJSON(t, hookPromptInput{Prompt: "!head north", Cwd: dir})
	stdout, _, err := runHookRun(t, stdinJSON)
	require.NoError(t, err, "an escape-prefixed prompt must exit 0")
	require.Empty(t, stdout, "the escape prefix forces a silent pass-through")
}

// TestHookRun_NoInterceptBlockPassesThrough — a config with no intercept block
// is a no-op: the shim passes through silently rather than erroring.
func TestHookRun_NoInterceptBlockPassesThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, webconfig.DefaultConfigFile), []byte("default_profile: \"\"\n"), 0o644))

	stdinJSON := mustJSON(t, hookPromptInput{Prompt: "head north", Cwd: dir})
	stdout, _, err := runHookRun(t, stdinJSON)
	require.NoError(t, err)
	require.Empty(t, stdout, "no intercept binding means a silent pass-through")
}

// runHookInstall drives `hook install` with the given args through a captured
// root command, returning stdout and the Execute error.
func runHookInstall(t *testing.T, args []string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"hook", "install"}, args...))
	err = root.Execute()
	return outBuf.String(), err
}

// TestHookInstall_DryRunThenWriteIdempotent — install --agent claude with no
// --write prints a diff and leaves the file absent; with --write it creates the
// hook; a second --write does not duplicate it (idempotent).
func TestHookInstall_DryRunThenWriteIdempotent(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(t.TempDir(), ".claude", "settings.json")

	// Dry-run: prints a diff, writes nothing.
	out, err := runHookInstall(t, []string{"--agent", "claude", "--settings", settings})
	require.NoError(t, err)
	require.Contains(t, out, "dry-run", "no-write install must announce a dry-run")
	require.Contains(t, out, hookCommandString, "the diff must show the hook command")
	_, statErr := os.Stat(settings)
	require.True(t, os.IsNotExist(statErr), "dry-run must not create the settings file")

	// Write: creates the file + dirs and installs the hook.
	out, err = runHookInstall(t, []string{"--agent", "claude", "--settings", settings, "--write"})
	require.NoError(t, err)
	require.Contains(t, out, "Wrote")
	require.Equal(t, 1, countHookEntries(t, settings), "the hook must be present exactly once")

	// Second write: idempotent — no duplicate entry.
	out, err = runHookInstall(t, []string{"--agent", "claude", "--settings", settings, "--write"})
	require.NoError(t, err)
	require.Contains(t, out, "already contains", "a repeat write must report nothing to do")
	require.Equal(t, 1, countHookEntries(t, settings), "a repeat write must not duplicate the hook")
}

// TestHookInstall_PreservesUnrelatedKeys — writing into a settings file with
// other top-level keys and a different hook event leaves them intact and adds
// UserPromptSubmit alongside.
func TestHookInstall_PreservesUnrelatedKeys(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(t.TempDir(), "settings.json")
	const seed = `{
  "model": "sonnet",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo pre"}]}
    ]
  }
}`
	require.NoError(t, os.WriteFile(settings, []byte(seed), 0o644))

	_, err := runHookInstall(t, []string{"--agent", "claude", "--settings", settings, "--write"})
	require.NoError(t, err)

	b, err := os.ReadFile(settings)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	require.Equal(t, "sonnet", m["model"], "unrelated top-level keys must survive the merge")
	hooks, _ := m["hooks"].(map[string]any)
	require.Contains(t, hooks, "PreToolUse", "an unrelated hook event must survive the merge")
	require.Contains(t, hooks, "UserPromptSubmit", "the kitsoki hook must be added alongside")
	require.Equal(t, 1, countHookEntries(t, settings))
}

// TestHookInstall_CodexNoHook — install --agent codex writes nothing and prints
// the honest "no pre-model hook" message pointing at the docs.
func TestHookInstall_CodexNoHook(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(t.TempDir(), "settings.json")
	out, err := runHookInstall(t, []string{"--agent", "codex", "--settings", settings})
	require.NoError(t, err)
	require.Contains(t, out, "Codex has no pre-model interception hook")
	require.Contains(t, out, "docs/architecture/prompt-intercept.md")
	_, statErr := os.Stat(settings)
	require.True(t, os.IsNotExist(statErr), "codex install must write nothing")
}

// countHookEntries parses the settings file and counts inner command hooks whose
// command equals hookCommandString — the duplication check.
func countHookEntries(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	hooks, _ := m["hooks"].(map[string]any)
	events, _ := hooks["UserPromptSubmit"].([]any)
	n := 0
	for _, ev := range events {
		evMap, _ := ev.(map[string]any)
		inner, _ := evMap["hooks"].([]any)
		for _, h := range inner {
			hMap, _ := h.(map[string]any)
			if cmd, _ := hMap["command"].(string); cmd == hookCommandString {
				n++
			}
		}
	}
	return n
}

// mustJSON marshals v to a compact JSON string or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

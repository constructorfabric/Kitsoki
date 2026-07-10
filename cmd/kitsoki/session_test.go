package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cloakAppFlag() string {
	return filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
}

// runKitsoki executes the root cobra command in-process and returns stdout.
func runKitsoki(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := rootForTest()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return out.String(), err
}

// rootForTest mirrors main()'s root construction without calling Execute.
func rootForTest() *cobra.Command {
	root := &cobra.Command{Use: "kitsoki"}
	root.AddCommand(versionCmd())
	root.AddCommand(sessionCmd())
	root.AddCommand(inboxCmd())
	root.AddCommand(turnCmd())
	root.AddCommand(chatCmd())
	root.AddCommand(workflowCmd())
	return root
}

// TestSession_CreateAndShowByKey creates a session, binds an external key,
// and verifies `session show --key` reads it back.
func TestSession_CreateAndShowByKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	stdout, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:CLOAK-1",
	)
	require.NoError(t, err)

	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	assert.NotEmpty(t, created["session_id"])
	assert.Equal(t, "cloak-of-darkness", created["app_id"])
	assert.Equal(t, "jira:CLOAK-1", created["key"])
	assert.Equal(t, "jira", created["transport"])
	assert.Equal(t, "CLOAK-1", created["thread"])

	// Show via key.
	stdout, err = runKitsoki(t, "session", "show",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:CLOAK-1",
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, created["session_id"], shown["session_id"])
	assert.Equal(t, "foyer", shown["state"])
	keys, _ := shown["external_keys"].([]any)
	require.Len(t, keys, 1)
}

// TestSession_ContinueDirectIntent runs a single continue with --intent and
// verifies the session advances; a second show reflects the new state.
func TestSession_ContinueDirectIntent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	_, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:CLOAK-2",
	)
	require.NoError(t, err)

	stdout, err := runKitsoki(t, "session", "continue",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:CLOAK-2",
		"--intent", "go",
		"--slots", `{"direction":"west"}`,
	)
	require.NoError(t, err)
	var outcome map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &outcome))
	assert.Equal(t, "transitioned", outcome["mode"])
	assert.Equal(t, "cloakroom", outcome["new_state"])

	// Show should reflect the new state.
	stdout, err = runKitsoki(t, "session", "show",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:CLOAK-2",
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, "cloakroom", shown["state"])
}

func TestSession_ContinueAutoDrivesBackgroundOperation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	appPath := writeSessionOperationDriveApp(t)

	_, err := runKitsoki(t, "session", "create",
		"--app", appPath,
		"--db", dbPath,
		"--key", "jira:OP-1",
	)
	require.NoError(t, err)

	stdout, err := runKitsoki(t, "session", "continue",
		"--app", appPath,
		"--db", dbPath,
		"--key", "jira:OP-1",
		"--intent", "begin",
	)
	require.NoError(t, err)
	var outcome map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &outcome))
	assert.Equal(t, "completed", outcome["mode"])
	assert.Equal(t, "done", outcome["new_state"])
	op, ok := outcome["operation_drive"].(map[string]any)
	require.True(t, ok, "operation_drive summary should be present: %#v", outcome["operation_drive"])
	assert.Equal(t, float64(1), op["turns"])
	assert.Equal(t, "operation-completed", op["stop_reason"])
	assert.Equal(t, "accept", op["last_intent"])

	stdout, err = runKitsoki(t, "session", "show",
		"--app", appPath,
		"--db", dbPath,
		"--key", "jira:OP-1",
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, "done", shown["state"])
}

// TestSession_List filters by transport and returns only matching sessions.
func TestSession_List(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	for _, k := range []string{"jira:A-1", "jira:A-2", "bitbucket:repo/pulls/3"} {
		_, err := runKitsoki(t, "session", "create",
			"--app", cloakAppFlag(),
			"--db", dbPath,
			"--key", k,
		)
		require.NoError(t, err)
	}

	stdout, err := runKitsoki(t, "session", "list",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--transport", "jira",
	)
	require.NoError(t, err)
	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed))
	rows, _ := listed["sessions"].([]any)
	assert.Len(t, rows, 2)

	stdout, err = runKitsoki(t, "session", "list",
		"--app", cloakAppFlag(),
		"--db", dbPath,
	)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed))
	rows, _ = listed["sessions"].([]any)
	assert.Len(t, rows, 3)
}

func writeSessionOperationDriveApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	const body = `app:
  id: session-operation-drive-test
  version: 0.1.0
  title: "Session Operation Drive Test"

operations:
  demo_run:
    title: "Demo Run"
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    terminal_artifact: done_artifact

world:
  done_artifact: { type: any, default: null }

intents:
  begin:
    title: "Begin"
  accept:
    title: "Accept"

root: idle

states:
  idle:
    view: "Idle."
    on:
      begin:
        - target: work
          operation: demo_run

  work:
    view: "Working."
    on:
      accept:
        - target: done

  done:
    terminal: true
    view: "Done."
    on_enter:
      - set:
          done_artifact:
            summary_title: "Done"
            summary_markdown: "Operation finished."
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath
}

// TestSession_BindKey adds a second external key to an existing session.
func TestSession_BindKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	stdout, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:LINK-1",
	)
	require.NoError(t, err)
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	sid, _ := created["session_id"].(string)
	require.NotEmpty(t, sid)

	_, err = runKitsoki(t, "session", "bind-key",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--id", sid,
		"--key", "bitbucket:repo/pulls/77",
	)
	require.NoError(t, err)

	// show via the second key resolves to the same session.
	stdout, err = runKitsoki(t, "session", "show",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "bitbucket:repo/pulls/77",
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, sid, shown["session_id"])
}

// TestSession_ForgetEOFStdinAborts verifies the destructive `session forget`
// confirmation prompt does NOT silently proceed (or report a bare "Aborted.")
// when stdin is at EOF (piped/automated input with no answer). On EOF the
// unchecked scanner.Scan() pattern returns "" which is correctly treated as a
// non-confirmation, but the fix additionally surfaces the I/O condition so the
// caller can tell an EOF apart from a deliberate "n". The session must remain
// intact.
func TestSession_ForgetEOFStdinAborts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	stdout, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "jira:FORGET-1",
	)
	require.NoError(t, err)
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	sid, _ := created["session_id"].(string)
	require.NotEmpty(t, sid)

	// Run `session forget` (no --yes) with an EOF stdin: empty reader returns
	// io.EOF on the first read, so scanner.Scan() is false.
	cmd := rootForTest()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(bytes.NewReader(nil)) // immediate EOF
	cmd.SetArgs([]string{"session", "forget",
		"--db", dbPath,
		"--id", sid,
	})
	cmd.SetContext(context.Background())
	runErr := cmd.Execute()

	// Safe abort path: no error, and the I/O condition is surfaced on stderr
	// (not a bare "Aborted.").
	require.NoError(t, runErr)
	stderr := errBuf.String()
	assert.Contains(t, stderr, "EOF",
		"EOF abort must surface the I/O condition, got: %q", stderr)

	// The session must still exist (delete did not run).
	stdout, err = runKitsoki(t, "session", "show",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--id", sid,
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, sid, shown["session_id"])
}

// TestSession_ParseExternalKey covers the "transport:thread" parser edges.
func TestSession_ParseExternalKey(t *testing.T) {
	good := map[string][2]string{
		"jira:PLTFRM-12345":      {"jira", "PLTFRM-12345"},
		"bitbucket:repo/pulls/4": {"bitbucket", "repo/pulls/4"},
	}
	for in, want := range good {
		gotT, gotR, err := parseExternalKey(in)
		require.NoError(t, err, in)
		assert.Equal(t, want[0], gotT)
		assert.Equal(t, want[1], gotR)
	}
	bad := []string{"", "no-colon", ":missing-transport", "missing-thread:"}
	for _, in := range bad {
		_, _, err := parseExternalKey(in)
		assert.Error(t, err, in)
	}
}

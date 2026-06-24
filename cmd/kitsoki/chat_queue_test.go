package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// TestChatQueue_AddThenListThenDismiss covers the storage-only verbs
// without invoking claude.
func TestChatQueue_AddThenListThenDismiss(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")

	cs, cleanup := openChatStoreForTest(t, dbPath)
	c, err := cs.Create(context.Background(), "dev-story", "live", "PROJ-1", "live chat")
	require.NoError(t, err)
	cleanup()

	// add
	addOut, err := runKitsoki(t, "chat", "queue", "add", c.ID,
		"--db", dbPath,
		"--payload", "please look at this",
		"--transport", "jira",
		"--thread", "PROJ-1#42",
	)
	require.NoError(t, err)
	var addRow map[string]any
	require.NoError(t, json.Unmarshal([]byte(addOut), &addRow))
	driveID, _ := addRow["drive_id"].(string)
	require.NotEmpty(t, driveID, "drive_id should be populated")
	assert.Equal(t, "pending", addRow["status"])
	assert.Equal(t, "jira", addRow["transport"])

	// list
	listOut, err := runKitsoki(t, "chat", "queue", "list", c.ID, "--db", dbPath)
	require.NoError(t, err)
	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	rows, _ := listed["drives"].([]any)
	require.Len(t, rows, 1)
	first, _ := rows[0].(map[string]any)
	assert.Equal(t, driveID, first["drive_id"])

	// dismiss
	dismissOut, err := runKitsoki(t, "chat", "queue", "dismiss", driveID, "--db", dbPath)
	require.NoError(t, err)
	var dismissed map[string]any
	require.NoError(t, json.Unmarshal([]byte(dismissOut), &dismissed))
	assert.Equal(t, "dismissed", dismissed["status"])

	// list now filtered by status returns the dismissed row.
	listOut2, err := runKitsoki(t, "chat", "queue", "list", c.ID,
		"--db", dbPath,
		"--status", "dismissed",
	)
	require.NoError(t, err)
	var listed2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(listOut2), &listed2))
	rows2, _ := listed2["drives"].([]any)
	require.Len(t, rows2, 1)
}

// TestChatQueue_AddRequiresPayload exercises the flag-validation path.
func TestChatQueue_AddRequiresPayload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	c, err := cs.Create(context.Background(), "dev-story", "live", "", "live")
	require.NoError(t, err)
	cleanup()

	_, err = runKitsoki(t, "chat", "queue", "add", c.ID, "--db", dbPath)
	require.Error(t, err)
}

// TestChatQueue_AddRejectsUnknownChat surfaces a clean error rather
// than enqueuing an orphan drive.
func TestChatQueue_AddRejectsUnknownChat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	// Touch the DB by opening a store, but don't add any chats.
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "queue", "add", "NOPE",
		"--db", dbPath,
		"--payload", "x",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestChatQueue_Dispatch_RunsAndMarksDone wires the fake agent binary
// through the dispatcher CLI path. The drive transitions pending →
// dispatching → done and the assistant reply is persisted.
func TestChatQueue_Dispatch_RunsAndMarksDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-agent.sh requires bash")
	}
	t.Setenv(host.AgentBinEnv, fakeAgentBinForCmdTest(t))

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	c, err := cs.Create(context.Background(), "dev-story", "live", "", "live")
	require.NoError(t, err)
	cleanup()

	addOut, err := runKitsoki(t, "chat", "queue", "add", c.ID,
		"--db", dbPath,
		"--payload", "hello",
		"--transport", "tui",
	)
	require.NoError(t, err)
	var addRow map[string]any
	require.NoError(t, json.Unmarshal([]byte(addOut), &addRow))
	driveID, _ := addRow["drive_id"].(string)
	require.NotEmpty(t, driveID)

	dispOut, err := runKitsoki(t, "chat", "queue", "dispatch", driveID, "--db", dbPath)
	require.NoError(t, err)
	var disp map[string]any
	require.NoError(t, json.Unmarshal([]byte(dispOut), &disp))
	assert.Equal(t, "done", disp["status"])
	assert.NotEmpty(t, disp["answer"], "answer should be populated on done")
	assert.Equal(t, c.ID, disp["chat_id"])

	// Re-list with --status done — the drive shows up there.
	listOut, err := runKitsoki(t, "chat", "queue", "list", c.ID,
		"--db", dbPath,
		"--status", "done",
	)
	require.NoError(t, err)
	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	rows, _ := listed["drives"].([]any)
	require.Len(t, rows, 1)
	first, _ := rows[0].(map[string]any)
	assert.Equal(t, "done", first["status"])
	// result_seq should be present in the JSON view.
	if _, ok := first["result_seq"]; !ok {
		t.Errorf("result_seq missing from done-drive view: %+v", first)
	}
}

// TestChatQueue_Dispatch_DriveNotFound returns a non-tempfail error.
func TestChatQueue_Dispatch_DriveNotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "queue", "dispatch", "NOPE", "--db", dbPath)
	require.Error(t, err)
	assert.False(t, IsTempFail(err), "missing drive must not be reported as EX_TEMPFAIL")
}

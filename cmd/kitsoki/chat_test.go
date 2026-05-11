package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// openChatStoreForTest opens the SQLite session store at dbPath, applies the
// chats schema and returns a *chats.Store the test can use to seed/inspect
// rows directly. Caller must defer the cleanup function.
func openChatStoreForTest(t *testing.T, dbPath string) (*chats.Store, func()) {
	t.Helper()
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	return cs, func() { _ = s.Close() }
}

// fakeOracleBinForCmdTest returns the path to internal/host/testdata/fake-oracle.sh
// so `kitsoki chat continue` can be exercised in-process without a real `claude`.
func fakeOracleBinForCmdTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	// thisFile is .../cmd/kitsoki/chat_test.go ; go up two levels to repo root.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "internal", "host", "testdata", "fake-oracle.sh")
}

// ─── chat new ────────────────────────────────────────────────────────────────

func TestChat_New_HappyPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")

	stdout, err := runKitsoki(t, "chat", "new",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
		"--title", "test chat",
	)
	require.NoError(t, err)

	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	assert.NotEmpty(t, created["id"], "expected non-empty chat id")
	assert.Equal(t, "test chat", created["title"])
	assert.Equal(t, "dev-story", created["app_id"])
	assert.Equal(t, "oracle", created["room"])
	assert.Equal(t, "active", created["status"])

	// Verify the row exists in the DB.
	cs, cleanup := openChatStoreForTest(t, dbPath)
	defer cleanup()
	got, err := cs.Get(context.Background(), created["id"].(string))
	require.NoError(t, err)
	assert.Equal(t, "test chat", got.Title)
}

func TestChat_New_DefaultTitle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")

	stdout, err := runKitsoki(t, "chat", "new",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
	)
	require.NoError(t, err)
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	assert.Equal(t, "dev-story/oracle", created["title"], "default title should be app/room")
}

// ─── chat list ───────────────────────────────────────────────────────────────

func TestChat_List_HappyPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)

	ctx := context.Background()
	c1, err := cs.Create(ctx, "dev-story", "oracle", "", "first")
	require.NoError(t, err)
	c2, err := cs.Create(ctx, "dev-story", "oracle", "", "second")
	require.NoError(t, err)
	// Different room — should not appear in the filter.
	_, err = cs.Create(ctx, "dev-story", "bugfix", "", "other")
	require.NoError(t, err)
	cleanup() // close so the CLI can open a fresh handle.

	stdout, err := runKitsoki(t, "chat", "list",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
	)
	require.NoError(t, err)

	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed))
	rows, _ := listed["chats"].([]any)
	require.Len(t, rows, 2)

	// Ordered by last_active_at DESC — c2 first.
	first, _ := rows[0].(map[string]any)
	assert.Equal(t, c2.ID, first["id"])
	second, _ := rows[1].(map[string]any)
	assert.Equal(t, c1.ID, second["id"])
}

func TestChat_List_ScopeKeyFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	cMatch, err := cs.Create(ctx, "dev-story", "oracle", "scope-X", "match")
	require.NoError(t, err)
	_, err = cs.Create(ctx, "dev-story", "oracle", "scope-Y", "miss")
	require.NoError(t, err)
	cleanup()

	stdout, err := runKitsoki(t, "chat", "list",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
		"--scope", "scope-X",
	)
	require.NoError(t, err)
	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed))
	rows, _ := listed["chats"].([]any)
	require.Len(t, rows, 1)
	first, _ := rows[0].(map[string]any)
	assert.Equal(t, cMatch.ID, first["id"])
}

func TestChat_List_AllStatusFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	active, err := cs.Create(ctx, "dev-story", "oracle", "", "active")
	require.NoError(t, err)
	archived, err := cs.Create(ctx, "dev-story", "oracle", "", "archived")
	require.NoError(t, err)
	require.NoError(t, cs.Archive(ctx, archived.ID))
	cleanup()

	// Without --all-status: archived is filtered out.
	stdout, err := runKitsoki(t, "chat", "list",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
	)
	require.NoError(t, err)
	var listed map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed))
	rows, _ := listed["chats"].([]any)
	require.Len(t, rows, 1)
	first, _ := rows[0].(map[string]any)
	assert.Equal(t, active.ID, first["id"])

	// With --all-status: both visible.
	stdout, err = runKitsoki(t, "chat", "list",
		"--db", dbPath,
		"--app", "dev-story",
		"--room", "oracle",
		"--all-status",
	)
	require.NoError(t, err)
	var listed2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &listed2))
	rows2, _ := listed2["chats"].([]any)
	require.Len(t, rows2, 2)
}

// ─── chat show ───────────────────────────────────────────────────────────────

func TestChat_Show_JSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "show-test")
	require.NoError(t, err)
	for _, body := range []string{"hello", "world", "third"} {
		_, err := cs.AppendMessage(ctx, c.ID, "user", body, nil)
		require.NoError(t, err)
	}
	cleanup()

	stdout, err := runKitsoki(t, "chat", "show",
		"--db", dbPath,
		c.ID,
	)
	require.NoError(t, err)
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &shown))
	assert.Equal(t, c.ID, shown["id"])
	msgs, _ := shown["messages"].([]any)
	assert.Len(t, msgs, 3)

	// --since 1 returns only seq>=1 (i.e. 2 messages).
	stdout, err = runKitsoki(t, "chat", "show",
		"--db", dbPath,
		"--since", "1",
		c.ID,
	)
	require.NoError(t, err)
	var sinceShown map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &sinceShown))
	sinceMsgs, _ := sinceShown["messages"].([]any)
	assert.Len(t, sinceMsgs, 2)
}

func TestChat_Show_Markdown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "md-test")
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, c.ID, "user", "hello", nil)
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, c.ID, "assistant", "hi", nil)
	require.NoError(t, err)
	cleanup()

	stdout, err := runKitsoki(t, "chat", "show",
		"--db", dbPath,
		"--format", "markdown",
		c.ID,
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "# md-test")
	assert.Contains(t, stdout, "**You**")
	assert.Contains(t, stdout, "**Claude**")
	assert.Contains(t, stdout, "hello")
	assert.Contains(t, stdout, "hi")
}

func TestChat_Show_NotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	// Open + close once to apply schema.
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "show",
		"--db", dbPath,
		"NONEXISTENT",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── chat fork ───────────────────────────────────────────────────────────────

func TestChat_Fork(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	parent, err := cs.Create(ctx, "dev-story", "oracle", "", "parent")
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, parent.ID, "user", "Q", nil)
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, parent.ID, "assistant", "A", nil)
	require.NoError(t, err)
	require.NoError(t, cs.SetClaudeSessionID(ctx, parent.ID, "11111111-2222-4333-8444-555555555555"))
	cleanup()

	stdout, err := runKitsoki(t, "chat", "fork",
		"--db", dbPath,
		"--title", "child",
		parent.ID,
	)
	require.NoError(t, err)
	var forked map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &forked))
	forkID, _ := forked["id"].(string)
	require.NotEmpty(t, forkID)
	require.NotEqual(t, parent.ID, forkID)
	assert.Equal(t, parent.ID, forked["parent_chat_id"])
	assert.Equal(t, "child", forked["title"])
	// Fork has empty claude_session_id; chatView omits empty values.
	_, hasClaude := forked["claude_session_id"]
	assert.False(t, hasClaude, "fork should not carry parent's claude_session_id")

	// Verify messages copied.
	cs2, cleanup2 := openChatStoreForTest(t, dbPath)
	defer cleanup2()
	msgs, err := cs2.Transcript(ctx, forkID, 0)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
}

// ─── chat archive ────────────────────────────────────────────────────────────

func TestChat_Archive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "archiveme")
	require.NoError(t, err)
	cleanup()

	stdout, err := runKitsoki(t, "chat", "archive",
		"--db", dbPath,
		c.ID,
	)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, true, out["archived"])
	assert.Equal(t, c.ID, out["chat_id"])

	cs2, cleanup2 := openChatStoreForTest(t, dbPath)
	defer cleanup2()
	got, err := cs2.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, string(chats.ChatArchived), got.Status)
}

// ─── chat unlock ─────────────────────────────────────────────────────────────

func TestChat_Unlock_Force(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "u")
	require.NoError(t, err)

	// Insert a stale lock row directly via the underlying sql.DB.  The
	// chat_locks schema is applied by chats.NewStore above.
	_, err = s.DB().ExecContext(ctx,
		`INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
		 VALUES (?, 99999, 'other.example.com', 0, 0)`,
		c.ID)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	stdout, err := runKitsoki(t, "chat", "unlock",
		"--db", dbPath,
		"--force",
		c.ID,
	)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, true, out["unlocked"])
	assert.Equal(t, c.ID, out["chat_id"])

	// Verify the lock row is gone.
	s2, err := store.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = s2.Close() }()
	var n int
	require.NoError(t, s2.DB().QueryRow(`SELECT COUNT(*) FROM chat_locks WHERE chat_id = ?`, c.ID).Scan(&n))
	assert.Equal(t, 0, n, "lock row should have been deleted")
}

func TestChat_Unlock_NoForce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "unlock",
		"--db", dbPath,
		"some-id",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force is required")
}

// ─── chat continue: lock contention → EX_TEMPFAIL=75 ─────────────────────────

// TestChat_Continue_LockContention seeds a chat_locks row with owner_host set
// to a different host so the cross-host busy path fires unconditionally
// (see internal/chats/lock.go). The handler returns ErrChatBusy via Result.Error;
// `chat continue` translates that into the errTempFail sentinel; the test
// asserts the error chain matches.
func TestChat_Continue_LockContention(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBinForCmdTest(t))

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "busy-test")
	require.NoError(t, err)

	// Seed a lock owned by a different host.  Cross-host locks are always
	// treated as busy (no liveness probe).
	_, err = s.DB().ExecContext(ctx,
		`INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
		 VALUES (?, 12345, 'remote.example.com', 0, 0)`,
		c.ID)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = runKitsoki(t, "chat", "continue",
		"--db", dbPath,
		"--raw", "hi",
		c.ID,
	)
	require.Error(t, err, "expected non-nil error on lock contention")
	require.True(t, IsTempFail(err), "expected errTempFail sentinel; got %v", err)
	require.True(t, errors.Is(err, errTempFail), "errors.Is(err, errTempFail) should be true")
}

// TestChat_Continue_HappyPath drives one chat turn end-to-end against the
// fake-oracle.sh binary. The handler appends a user message, executes the fake
// claude, appends an assistant message, and returns the answer.  The test
// asserts the JSON output contains the answer + chat_id, and that the DB
// gained the expected two rows.
func TestChat_Continue_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBinForCmdTest(t))

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cs, cleanup := openChatStoreForTest(t, dbPath)
	ctx := context.Background()

	c, err := cs.Create(ctx, "dev-story", "oracle", "", "happy")
	require.NoError(t, err)
	cleanup()

	stdout, err := runKitsoki(t, "chat", "continue",
		"--db", dbPath,
		"--raw", "what is up",
		c.ID,
	)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, c.ID, out["chat_id"])
	answer, _ := out["answer"].(string)
	assert.Contains(t, answer, "what is up", "fake-oracle echoes the question into the answer")

	// Verify two messages landed in the DB (user + assistant).
	cs2, cleanup2 := openChatStoreForTest(t, dbPath)
	defer cleanup2()
	msgs, err := cs2.Transcript(ctx, c.ID, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "expected user + assistant messages")
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "what is up", msgs[0].Content)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.True(t, strings.Contains(msgs[1].Content, "what is up"))

	// claude_session_id was assigned and persisted.
	got, err := cs2.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ClaudeSessionID, "expected claude_session_id to be set after first turn")
}

// TestChat_Continue_MissingRaw asserts that --raw is required.
func TestChat_Continue_MissingRaw(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "continue",
		"--db", dbPath,
		"some-id",
	)
	require.Error(t, err)
}

// TestChat_Continue_NotFound asserts that an unknown chat id surfaces a
// helpful error.
func TestChat_Continue_NotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	_, cleanup := openChatStoreForTest(t, dbPath)
	cleanup()

	_, err := runKitsoki(t, "chat", "continue",
		"--db", dbPath,
		"--raw", "hi",
		"DOES-NOT-EXIST",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestChat_Continue_SignalCleanup is intended to verify cross-process SIGINT
// cleanup of chat_locks rows: spawn `go run ./cmd/kitsoki chat continue <id>
// --raw "..."` against a real DB, send SIGINT mid-call, reopen the DB, and
// assert no chat_locks row remains.
//
// This is currently SKIPPED because cross-process testing in Go's test
// framework is genuinely tricky:
//
//  1. `go run ./cmd/kitsoki` spawns a build helper that itself fork/execs the
//     compiled binary; SIGINT to the `go run` PID does not always reach the
//     final `kitsoki` PID, so the cleanup path under test may never fire
//     (process group setup also matters here).
//  2. To reliably exercise the lock-cleanup code, we'd need to spawn the
//     compiled kitsoki binary directly (not via `go run`) and send SIGINT to
//     the right process; that adds a build step + binary path discovery.
//  3. The fake-oracle stub used by the other Continue tests exits ~immediately,
//     so there's a small window in which to deliver SIGINT. A reliable test
//     would need a fake that blocks on a sentinel file the test removes after
//     verifying the lock row is present.
//
// The lock-cleanup path itself is exercised by:
//   - TestChat_Continue_LockContention (verifies cross-host busy → EX_TEMPFAIL)
//   - TestChat_Unlock_Force (verifies the operator escape hatch)
//   - internal/chats/lock_test.go (in-process WithLock release semantics)
//
// TODO: revisit this when the harness gains a binary-build helper, or when
// one of the existing in-process tests can be tightened to exercise the
// signal-handler path.
func TestChat_Continue_SignalCleanup(t *testing.T) {
	t.Skip("TODO: cross-process SIGINT cleanup test — see comment above for why this is hard")
}

// ─── runKitsoki helper that returns combined err/stderr ───────────────────────

// runKitsokiCapturingStderr is a variant of runKitsoki for cases that need both
// stdout and stderr. Currently unused but available for future tests.
func runKitsokiCapturingStderr(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := rootForTest()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

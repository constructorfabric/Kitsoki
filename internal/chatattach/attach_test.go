package chatattach_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/chatattach"
	"kitsoki/internal/chats"
	"kitsoki/internal/store"
	"kitsoki/internal/tmux"
)

// fakeTmuxBin returns the path to internal/tmux/testdata/fake-tmux.sh
// so the suite stays hermetic — no real tmux needed.
func fakeTmuxBin(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is internal/chatattach/attach_test.go — walk up two
	// dirs to repo root, then into internal/tmux/testdata.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(repoRoot, "internal", "tmux", "testdata", "fake-tmux.sh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fake-tmux.sh not found: %v", err)
	}
	return path
}

// setupEnv wires the fake-tmux binary, a per-test state dir, and an
// isolated XDG_STATE_HOME so DefaultSocketPath lands inside the
// sandbox.
func setupEnv(t *testing.T) (stateDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-tmux.sh requires bash")
	}
	t.Setenv(tmux.TmuxBinEnv, fakeTmuxBin(t))
	stateDir = t.TempDir()
	t.Setenv("KITSOKI_FAKE_TMUX_STATE_DIR", stateDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return stateDir
}

// openTestStore opens a fresh on-disk SQLite store with the chats
// schema and returns the *chats.Store plus a cleanup. We use the same
// helper pattern as the other CLI tests so chats.NewStore handles the
// migration.
func openTestStore(t *testing.T) (*chats.Store, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		_ = s.Close()
		t.Fatalf("chats.NewStore: %v", err)
	}
	return cs, func() { _ = s.Close() }
}

func newTmuxClient(t *testing.T) *tmux.Client {
	t.Helper()
	c, err := tmux.New(tmux.DefaultSocketPath())
	if err != nil {
		t.Fatalf("tmux.New: %v", err)
	}
	return c
}

// TestRun_FreshChatUsesSessionIDFlag is the regression for the
// /meta → /attach crash: when a chat has no claude_session_id yet,
// chatattach mints one and MUST pass it via --session-id (not
// --resume), otherwise claude bails with "No conversation found".
// fake-tmux records the in-pane command to $state_dir/<name>.cmd so
// we can assert on the argv directly.
func TestRun_FreshChatUsesSessionIDFlag(t *testing.T) {
	stateDir := setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, err := cs.Create(ctx, "bugfix", "live", "", "fresh chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Sanity: chat starts with no claude_session_id.
	got, _ := cs.Get(ctx, chat.ID)
	if got.ClaudeSessionID != "" {
		t.Fatalf("fresh chat should have empty claude_session_id; got %q", got.ClaudeSessionID)
	}

	if err := chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		ClaudeBin: "/bin/true",
	}, func(_ string) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cmdBytes, err := os.ReadFile(filepath.Join(stateDir, chatattach.TmuxSessionPrefix+chat.ID+".cmd"))
	if err != nil {
		t.Fatalf("read recorded tmux pane command: %v", err)
	}
	cmd := string(cmdBytes)
	if !contains(cmd, "--session-id") {
		t.Errorf("fresh-chat attach must use --session-id; got cmd: %q", cmd)
	}
	if contains(cmd, "--resume") {
		t.Errorf("fresh-chat attach must NOT use --resume; got cmd: %q", cmd)
	}
}

// TestRun_ResumingChatUsesResumeFlag is the converse: when the chat
// already has a claude_session_id, attach uses --resume.
func TestRun_ResumingChatUsesResumeFlag(t *testing.T) {
	stateDir := setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, err := cs.Create(ctx, "bugfix", "live", "", "resumable")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate a chat that's already been driven once.
	const existingSID = "11111111-2222-4333-8444-555555555555"
	if err := cs.SetClaudeSessionID(ctx, chat.ID, existingSID); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}

	if err := chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		ClaudeBin: "/bin/true",
	}, func(_ string) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cmdBytes, err := os.ReadFile(filepath.Join(stateDir, chatattach.TmuxSessionPrefix+chat.ID+".cmd"))
	if err != nil {
		t.Fatalf("read recorded tmux pane command: %v", err)
	}
	cmd := string(cmdBytes)
	if !contains(cmd, "--resume") {
		t.Errorf("resumable-chat attach must use --resume; got cmd: %q", cmd)
	}
	if contains(cmd, "--session-id") {
		t.Errorf("resumable-chat attach must NOT re-pass --session-id; got cmd: %q", cmd)
	}
	if !contains(cmd, existingSID) {
		t.Errorf("attach must reference the existing session id %q; got cmd: %q", existingSID, cmd)
	}
}

// TestRun_PassesKitsokiTmuxConfig ensures the embedded
// kitsoki-tmux.conf actually reaches `tmux new-session -f`. The
// fake-tmux script copies whatever -f argument it sees to
// $state_dir/.last-conf so this test can assert on the contents.
func TestRun_PassesKitsokiTmuxConfig(t *testing.T) {
	stateDir := setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	chat, err := cs.Create(context.Background(), "bugfix", "live", "", "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := chatattach.Run(context.Background(), chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
	}, func(_ string) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	confPath := filepath.Join(stateDir, ".last-conf")
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("expected kitsoki-tmux.conf to be passed via -f; missing %s: %v", confPath, err)
	}
	got := string(body)
	for _, marker := range []string{
		"status on",
		"status-left",
		"kitsoki",
	} {
		if !contains(got, marker) {
			t.Errorf("kitsoki-tmux.conf missing marker %q; got:\n%s", marker, got)
		}
	}
}

// TestRun_HappyPath_SpawnsAndFlipsToBackground covers the fresh-attach
// flow: no prior pty_sessions row, runTmux is invoked once with the
// kitsoki-chat-<id> session name, on success the row lands in
// pty_background. The claude_session_id is minted on first attach.
func TestRun_HappyPath_SpawnsAndFlipsToBackground(t *testing.T) {
	stateDir := setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, err := cs.Create(ctx, "bugfix", "live", "", "live chat")
	if err != nil {
		t.Fatalf("Create chat: %v", err)
	}

	var calledWith string
	runTmux := func(sessionName string) error {
		calledWith = sessionName
		return nil
	}

	err = chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
		ClaudeBin: "/bin/true",
	}, runTmux)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSession := chatattach.TmuxSessionPrefix + chat.ID
	if calledWith != wantSession {
		t.Errorf("runTmux called with %q, want %q", calledWith, wantSession)
	}

	// fake-tmux created a state file for the session.
	if _, err := os.Stat(filepath.Join(stateDir, wantSession)); err != nil {
		t.Errorf("fake tmux session file should exist: %v", err)
	}

	// Row landed in pty_background.
	pty, err := cs.GetPTY(ctx, chat.ID)
	if err != nil {
		t.Fatalf("GetPTY: %v", err)
	}
	if pty.Mode != chats.PtyModeBackground {
		t.Errorf("mode = %q, want pty_background", pty.Mode)
	}

	// Claude session id minted on first attach.
	got, _ := cs.Get(ctx, chat.ID)
	if got.ClaudeSessionID == "" {
		t.Error("claude_session_id should be allocated on first attach")
	}
}

// TestRun_RunTmuxErrorPropagates verifies that an error from the
// callback (claude crash, tmux session vanished mid-attach) is
// returned to the caller verbatim. The row still flips to
// pty_background — chatattach treats the row state as orthogonal to
// the attach exit code, matching the proposal §10 recovery semantics.
func TestRun_RunTmuxErrorPropagates(t *testing.T) {
	setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	chat, _ := cs.Create(context.Background(), "bugfix", "live", "", "x")

	wantErr := fmt.Errorf("synthetic claude crash")
	err := chatattach.Run(context.Background(), chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
	}, func(_ string) error {
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("Run returned %v, want %v", err, wantErr)
	}
	// Row still flipped to background (the user can re-attach to
	// inspect what's left over).
	pty, err := cs.GetPTY(context.Background(), chat.ID)
	if err != nil {
		t.Fatalf("GetPTY: %v", err)
	}
	if pty.Mode != chats.PtyModeBackground {
		t.Errorf("mode = %q, want pty_background even on runTmux error", pty.Mode)
	}
}

// TestRun_ReusesPtyBackgroundSession is the re-attach path: a row
// already exists in pty_background and tmux still has the session.
// Run should NOT call NewSession (would error with "duplicate
// session" on fake-tmux); it should flip the row to pty_attached, run
// the callback, and end back in pty_background.
func TestRun_ReusesPtyBackgroundSession(t *testing.T) {
	stateDir := setupEnv(t)
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	cs, cleanup := openTestStore(t)
	defer cleanup()

	ctx := context.Background()
	chat, _ := cs.Create(ctx, "bugfix", "live", "", "x")

	// First attach: spawns the session and flips to pty_background.
	if err := chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
	}, func(_ string) error { return nil }); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second attach: tmux still has the session (fake-tmux state-dir
	// file still exists), DB row still says pty_background.
	if _, err := os.Stat(filepath.Join(stateDir, chatattach.TmuxSessionPrefix+chat.ID)); err != nil {
		t.Fatalf("session file should still exist between attaches: %v", err)
	}
	if err := chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
	}, func(_ string) error { return nil }); err != nil {
		t.Fatalf("second Run (reuse path): %v", err)
	}
	pty, _ := cs.GetPTY(ctx, chat.ID)
	if pty.Mode != chats.PtyModeBackground {
		t.Errorf("mode = %q after re-attach, want pty_background", pty.Mode)
	}
}

// TestRun_ChatBusyReturnsErrChatBusy: another process holds the lock,
// Run should return ErrChatBusy without calling runTmux. We seed the
// lock row directly via the underlying *sql.DB — opening a second
// chats.Store handle would race the migration probe.
func TestRun_ChatBusyReturnsErrChatBusy(t *testing.T) {
	setupEnv(t)

	// Open the underlying DB ourselves so we can seed the lock row
	// before chats.NewStore is involved. Mirrors the pattern in
	// cmd/kitsoki/chat_attach_test.go's TestChatAttach_LockBusy.
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		t.Fatalf("chats.NewStore: %v", err)
	}
	ctx := context.Background()
	chat, _ := cs.Create(ctx, "bugfix", "live", "", "x")

	host, _ := os.Hostname()
	if _, err := s.DB().Exec(`
		INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
		VALUES (?, ?, ?, 0, 0)`,
		chat.ID, os.Getpid(), host,
	); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	called := false
	err = chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      newTmuxClient(t),
		Workspace: t.TempDir(),
	}, func(_ string) error {
		called = true
		return nil
	})
	if !errors.Is(err, chats.ErrChatBusy) {
		t.Errorf("expected ErrChatBusy, got %v", err)
	}
	if called {
		t.Error("runTmux must not run when the lock is busy")
	}
}

// TestRun_HeartbeatSurvivesContextCancel is the regression for the
// stale-lock bug: the heartbeat goroutine must NOT share the context the
// caller passes to Run. The CLI attach path derives that context from
// SIGINT, so an interrupted attach cancels it while runTmux is still
// blocking inside tmux. If the heartbeat rode that context it would stop
// the instant the user hit Ctrl-C, letting the lock's heartbeat_at go
// stale even though the tmux session (and thus the lock) is still live.
//
// We hold runTmux open past a ctx cancellation and assert heartbeat_at
// keeps advancing during the window between cancel and release.
func TestRun_HeartbeatSurvivesContextCancel(t *testing.T) {
	setupEnv(t)
	chatattach.HeartbeatInterval = 5 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	// Open the DB directly so we can poll chat_locks.heartbeat_at.
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		t.Fatalf("chats.NewStore: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	chat, _ := cs.Create(context.Background(), "bugfix", "live", "", "x")

	readHeartbeat := func() int64 {
		var hb int64
		if err := s.DB().QueryRow(
			`SELECT heartbeat_at FROM chat_locks WHERE chat_id = ?`, chat.ID,
		).Scan(&hb); err != nil {
			t.Fatalf("read heartbeat_at: %v", err)
		}
		return hb
	}

	// runTmux signals once it's running, then blocks until released.
	running := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	var runErr error
	done := make(chan struct{})
	go func() {
		runErr = chatattach.Run(ctx, chatattach.Options{
			ChatID:    chat.ID,
			Store:     cs,
			Tmux:      newTmuxClient(t),
			Workspace: t.TempDir(),
		}, func(_ string) error {
			once.Do(func() { close(running) })
			<-release
			return nil
		})
		close(done)
	}()

	// Wait until we're inside the lock + runTmux.
	select {
	case <-running:
	case <-time.After(2 * time.Second):
		t.Fatal("runTmux never started")
	}

	// Simulate the interrupted CLI attach: cancel the caller's context
	// while runTmux is still blocking.
	cancel()

	// Let several heartbeat intervals elapse post-cancel, then confirm
	// the heartbeat is still advancing. With the bug (heartbeat on the
	// caller's ctx) it would have stopped at cancel().
	before := readHeartbeat()
	deadline := time.After(500 * time.Millisecond)
	advanced := false
	for !advanced {
		select {
		case <-deadline:
			t.Fatalf("heartbeat_at did not advance after context cancel (stuck at %d) — heartbeat stopped with the caller's ctx", before)
		default:
		}
		if readHeartbeat() > before {
			advanced = true
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Genuine release: runTmux returns, heartbeat must stop.
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after release")
	}
	if runErr != nil {
		t.Fatalf("Run returned error: %v", runErr)
	}

	// After release the lock is gone; further heartbeats are impossible.
	var n int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM chat_locks WHERE chat_id = ?`, chat.ID).Scan(&n)
	if n != 0 {
		t.Errorf("lock row should be released after Run returns, found %d", n)
	}
}

// TestRun_RejectsInvalidOptions covers the missing-input guards.
func TestRun_RejectsInvalidOptions(t *testing.T) {
	setupEnv(t)
	cs, cleanup := openTestStore(t)
	defer cleanup()

	cases := []struct {
		name string
		opts chatattach.Options
		run  func(string) error
		want string
	}{
		{"no store", chatattach.Options{ChatID: "X", Tmux: newTmuxClient(t)}, func(string) error { return nil }, "nil chat store"},
		{"no tmux", chatattach.Options{ChatID: "X", Store: cs}, func(string) error { return nil }, "nil tmux client"},
		{"no chat id", chatattach.Options{Store: cs, Tmux: newTmuxClient(t)}, func(string) error { return nil }, "empty chat ID"},
		{"no callback", chatattach.Options{ChatID: "X", Store: cs, Tmux: newTmuxClient(t)}, nil, "nil runTmux"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := chatattach.Run(context.Background(), tc.opts, tc.run)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			// Just confirm the named guard fired; substring match
			// keeps the test resilient to error-message tweaks.
			if got := err.Error(); !contains(got, tc.want) {
				t.Errorf("error %q does not mention %q", got, tc.want)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && (haystack == needle || hasSubstring(haystack, needle)))
}
func hasSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

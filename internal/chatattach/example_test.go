// Runnable godoc example for the [Run] surface. The Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/chatattach/...`.
//
// The example is hermetic: it points [tmux.TmuxBinEnv] at the same
// testdata/fake-tmux.sh emulator the unit tests use and backs the chat
// store with an in-sandbox SQLite file, so it runs on machines without
// a real tmux or claude and never touches the user's sessions.
package chatattach_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"kitsoki/internal/chatattach"
	"kitsoki/internal/chats"
	"kitsoki/internal/store"
	"kitsoki/internal/tmux"
)

// exampleEnv wires the fake-tmux emulator and an isolated state dir,
// mirroring setupEnv but usable from an Example (which has no
// *testing.T). It returns the configured tmux client, the chat store,
// and a cleanup func.
func exampleEnv() (*tmux.Client, *chats.Store, func()) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	fakeBin := filepath.Join(repoRoot, "internal", "tmux", "testdata", "fake-tmux.sh")

	stateDir, _ := os.MkdirTemp("", "chatattach-example-state")
	sockDir, _ := os.MkdirTemp("", "chatattach-example-sock")
	dbDir, _ := os.MkdirTemp("", "chatattach-example-db")

	os.Setenv(tmux.TmuxBinEnv, fakeBin)
	os.Setenv("KITSOKI_FAKE_TMUX_STATE_DIR", stateDir)

	tc, err := tmux.New(filepath.Join(sockDir, "tmux.sock"))
	if err != nil {
		panic(err)
	}

	s, err := store.Open(filepath.Join(dbDir, "sessions.db"))
	if err != nil {
		panic(err)
	}
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		panic(err)
	}

	cleanup := func() {
		_ = s.Close()
		os.Unsetenv(tmux.TmuxBinEnv)
		os.Unsetenv("KITSOKI_FAKE_TMUX_STATE_DIR")
		os.RemoveAll(stateDir)
		os.RemoveAll(sockDir)
		os.RemoveAll(dbDir)
	}
	return tc, cs, cleanup
}

// ExampleRun is the runnable form of the package-doc worked example: a
// brand-new chat with id "bugfix" is attached, and the runTmux callback
// observes the tmux session name chatattach hands it. The callback
// returns immediately (as if the user detached at once), so Run flips
// the row to pty_background and returns nil.
func ExampleRun() {
	// Speed the heartbeat down so the example never has to wait on the
	// 5s default while the callback is "blocked."
	chatattach.HeartbeatInterval = 10 * time.Millisecond
	defer func() { chatattach.HeartbeatInterval = 5 * time.Second }()

	tc, cs, cleanup := exampleEnv()
	defer cleanup()
	ctx := context.Background()

	chat, err := cs.Create(ctx, "bugfix", "live", "", "worked example")
	if err != nil {
		panic(err)
	}

	// The chat ID is a generated ULID, so assert the handed session
	// name matches the [chatattach.TmuxSessionPrefix]+id convention
	// rather than printing the non-deterministic raw ID.
	want := chatattach.TmuxSessionPrefix + chat.ID
	err = chatattach.Run(ctx, chatattach.Options{
		ChatID:    chat.ID,
		Store:     cs,
		Tmux:      tc,
		ClaudeBin: "/bin/true",
	}, func(sessionName string) error {
		fmt.Println("session name matches kitsoki-chat-<id>:", sessionName == want)
		return nil
	})
	fmt.Println("Run error:", err)
	// Output:
	// session name matches kitsoki-chat-<id>: true
	// Run error: <nil>
}

// Runnable godoc examples for the [Client] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/tmux/...`.
//
// The examples are hermetic: they point [TmuxBinEnv] at the same
// testdata/fake-tmux.sh emulator the unit tests use, so they run on
// machines without a real tmux and never touch the user's sessions.
package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"kitsoki/internal/tmux"
)

// exampleClient wires a Client to the fake-tmux emulator in a private
// temp state dir, mirroring newTestClient but usable from an Example
// (which has no *testing.T). It returns the client and a cleanup func.
func exampleClient() (*tmux.Client, func()) {
	_, thisFile, _, _ := runtime.Caller(0)
	fakeBin := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-tmux.sh")

	stateDir, _ := os.MkdirTemp("", "tmux-example-state")
	sockDir, _ := os.MkdirTemp("", "tmux-example-sock")

	os.Setenv(tmux.TmuxBinEnv, fakeBin)
	os.Setenv("KITSOKI_FAKE_TMUX_STATE_DIR", stateDir)

	c, err := tmux.New(filepath.Join(sockDir, "tmux.sock"))
	if err != nil {
		panic(err)
	}
	if err := c.EnsureSocketDir(); err != nil {
		panic(err)
	}
	cleanup := func() {
		os.Unsetenv(tmux.TmuxBinEnv)
		os.Unsetenv("KITSOKI_FAKE_TMUX_STATE_DIR")
		os.RemoveAll(stateDir)
		os.RemoveAll(sockDir)
	}
	return c, cleanup
}

// ExampleClient is the canonical chat-host worked example: create a
// detached session, confirm it exists, list it, then kill it — and see
// that a second kill reports ErrSessionNotFound. This is the runnable
// form of the trace in the package doc.
func ExampleClient() {
	c, cleanup := exampleClient()
	defer cleanup()
	ctx := context.Background()

	_ = c.NewSession(ctx, tmux.NewSessionOptions{
		Name:       "kitsoki-chat-7",
		WorkingDir: "/work/7",
		Command:    "claude --resume 7",
	})

	present, _ := c.HasSession(ctx, "kitsoki-chat-7")
	absent, _ := c.HasSession(ctx, "kitsoki-chat-404")
	names, _ := c.ListSessions(ctx)

	first := c.KillSession(ctx, "kitsoki-chat-7")
	second := c.KillSession(ctx, "kitsoki-chat-7")

	fmt.Println("present:", present)
	fmt.Println("absent: ", absent)
	fmt.Println("listed: ", names)
	fmt.Println("kill #1:", first)
	fmt.Println("kill #2:", errors.Is(second, tmux.ErrSessionNotFound))
	// Output:
	// present: true
	// absent:  false
	// listed:  [kitsoki-chat-7]
	// kill #1: <nil>
	// kill #2: true
}

// ExampleClient_ListSessions shows the empty-server contract: a freshly
// created socket with no sessions returns an empty slice and a nil
// error, not an "I/O failed" error — so the GC sweep needs no special
// case for fresh deployments.
func ExampleClient_ListSessions() {
	c, cleanup := exampleClient()
	defer cleanup()
	ctx := context.Background()

	names, err := c.ListSessions(ctx)
	fmt.Printf("names=%v err=%v\n", names, err)
	// Output:
	// names=[] err=<nil>
}

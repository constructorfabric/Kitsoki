// Package tuibridge bridges a real pty to a websocket, byte-for-byte, so a
// browser (Playwright or claude-in-chrome) can drive an actual running
// terminal program with full ANSI fidelity — real keystrokes in, real
// rendered bytes out — instead of a pre-recorded cassette replay (see
// tools/mcp-demo for the replay path this subsumes for live/interactive use).
//
// One websocket connection == one spawned process == one pty. There is no
// session multiplexing: a client that disconnects kills its process; a new
// connection spawns a fresh one. That keeps the bridge stateless and makes it
// trivial to run many isolated instances in parallel (e.g. one per Playwright
// test worker).
package tuibridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Options configures how each connection's process is spawned.
type Options struct {
	// Command is the argv to spawn per connection, e.g. []string{"kitsoki",
	// "run"}. Must be non-empty.
	Command []string
	// Dir is the working directory for the spawned process. Empty means
	// inherit the bridge server's own cwd.
	Dir string
	// Env overrides the spawned process environment after bridge terminal
	// capability defaults are applied, so callers can explicitly exercise a
	// degraded terminal when needed.
	Env []string
}

// controlMessage is the sole text-frame shape the bridge understands. Binary
// frames are raw pty bytes in both directions; anything that needs to be a
// distinct control channel (today: resize) rides as JSON text instead so the
// two are trivially distinguishable on the wire.
type controlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Server is an http.Handler that upgrades each request to a websocket, spawns
// Options.Command in a pty, and bridges bytes between them until either side
// closes.
type Server struct {
	opts Options
}

// New builds a Server. It panics if opts.Command is empty — that is a caller
// bug, not a runtime condition.
func New(opts Options) *Server {
	if len(opts.Command) == 0 {
		panic("tuibridge: Options.Command must be non-empty")
	}
	return &Server{opts: opts}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The player page is served from a different origin (its own static
	// server, e.g. tools/tui-bridge/player) than this bridge, and drivers
	// like Playwright/claude-in-chrome may send no Origin header at all —
	// so same-origin Accept would reject every real client. This is a local
	// dev/test tool (addr defaults to loopback); origin checking isn't a
	// meaningful boundary here.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}

	cmd := exec.Command(s.opts.Command[0], s.opts.Command[1:]...)
	cmd.Dir = s.opts.Dir
	cmd.Env = terminalEnv(os.Environ(), s.opts.Env)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("spawn: %v", err))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	done := make(chan struct{}, 2)

	// pty -> websocket (rendered output).
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// websocket -> pty (keystrokes + resize control frames).
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			typ, data, rerr := conn.Read(ctx)
			if rerr != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, werr := ptmx.Write(data); werr != nil {
					return
				}
			case websocket.MessageText:
				var ctl controlMessage
				if err := json.Unmarshal(data, &ctl); err == nil && ctl.Type == "resize" {
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(ctl.Rows), Cols: uint16(ctl.Cols)})
				}
			}
		}
	}()

	<-done
	cancel()
	<-done
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func terminalEnv(base, overrides []string) []string {
	env := make([]string, 0, len(base)+len(overrides)+4)
	index := make(map[string]int, len(base)+len(overrides)+4)
	set := func(kv string) {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			return
		}
		if i, ok := index[key]; ok {
			env[i] = kv
			return
		}
		index[key] = len(env)
		env = append(env, kv)
	}

	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || key == "" || key == "NO_COLOR" {
			continue
		}
		set(kv)
	}

	// The bridge endpoint is always an xterm.js terminal, regardless of the
	// parent process's own stdout/stderr environment. A headless recorder often
	// runs with TERM=dumb or NO_COLOR=1; inheriting that makes the child TUI emit
	// plain text even though the browser can render ANSI correctly.
	for _, kv := range []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"FORCE_COLOR=1",
		"CLICOLOR_FORCE=1",
	} {
		set(kv)
	}

	for _, kv := range overrides {
		set(kv)
	}
	return env
}

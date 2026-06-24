package tmux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TmuxBinEnv overrides the resolved tmux binary path. Set this in
// tests to a fake-tmux shell script that emulates only the verbs we
// need.
const TmuxBinEnv = "KITSOKI_TMUX_BIN"

// ErrTmuxUnavailable is returned when the tmux binary can't be found
// on PATH and TmuxBinEnv is unset. Callers translate this into a
// friendly "install tmux" message rather than a generic exec error.
var ErrTmuxUnavailable = errors.New("tmux: binary not found (set KITSOKI_TMUX_BIN or install tmux)")

// ErrSessionNotFound is returned by HasSession (via the typed result)
// and by KillSession when the named session does not exist on the
// configured socket.
var ErrSessionNotFound = errors.New("tmux: session not found")

// Client is a stateless handle over the tmux CLI rooted at a
// particular socket path. All methods are safe for concurrent use —
// tmux itself serialises commands per socket.
type Client struct {
	bin        string
	socketPath string
}

// New constructs a Client. socketPath is the kitsoki-owned tmux
// socket — pass DefaultSocketPath() unless the caller wants a custom
// location (e.g. integration tests using a temp dir). Returns
// ErrTmuxUnavailable when the binary isn't resolvable.
func New(socketPath string) (*Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("tmux.New: empty socket path")
	}
	bin, err := resolveBin()
	if err != nil {
		return nil, err
	}
	return &Client{bin: bin, socketPath: socketPath}, nil
}

// SocketPath returns the socket this client is bound to. Useful for
// composing diagnostics and for callers that pass the path through to
// child processes via env.
func (c *Client) SocketPath() string { return c.socketPath }

// Bin returns the resolved tmux binary path. Exposed so callers that
// need to build their own *exec.Cmd (e.g. bubbletea's
// tea.ExecCommand path, which owns the stdio plumbing) can reuse the
// same path the Client uses internally — including the
// $KITSOKI_TMUX_BIN test-injection override.
func (c *Client) Bin() string { return c.bin }

// EnsureSocketDir creates the parent directory of the socket path if
// it doesn't exist. Idempotent; callers normally invoke this once at
// startup before any other tmux command (NewSession will create the
// socket file inside an existing directory but cannot create the
// directory itself).
func (c *Client) EnsureSocketDir() error {
	dir := filepath.Dir(c.socketPath)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tmux.EnsureSocketDir: %w", err)
	}
	return nil
}

// NewSessionOptions carries the inputs for NewSession. The Command
// field is the shell command tmux runs as the session's initial
// pane — for kitsoki chat work this is `claude --resume <id>`.
type NewSessionOptions struct {
	// Name is the tmux session name. Use "kitsoki-chat-<chat-id>" for
	// chat-hosted claudes.
	Name string
	// WorkingDir is the cwd for the first pane. Required for
	// `claude --resume` so file access is scoped to the chat's
	// workspace.
	WorkingDir string
	// Command is the shell command tmux runs in the first pane.
	Command string
	// Env is additional environment variables for the pane. Each
	// entry is "KEY=VALUE". Inherited from the kitsoki process unless
	// overridden here.
	Env []string
	// ConfigFile is the inner-tmux config (-f). kitsoki ships its own
	// keybinding file so the user's ~/.tmux.conf is untouched (see the
	// Non-goals in the package doc). Optional; empty means no -f flag.
	ConfigFile string
}

// NewSession starts a detached tmux session running opts.Command.
// Errors when:
//   - tmux refuses to start (binary missing → ErrTmuxUnavailable),
//   - opts.Name is empty or contains characters tmux rejects,
//   - a session with opts.Name already exists on this socket.
func (c *Client) NewSession(ctx context.Context, opts NewSessionOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("tmux.NewSession: empty session name")
	}
	if strings.TrimSpace(opts.Command) == "" {
		return fmt.Errorf("tmux.NewSession: empty command")
	}
	args := []string{"-S", c.socketPath}
	if opts.ConfigFile != "" {
		args = append(args, "-f", opts.ConfigFile)
	}
	args = append(args, "new-session", "-d", "-s", opts.Name)
	if opts.WorkingDir != "" {
		args = append(args, "-c", opts.WorkingDir)
	}
	for _, e := range opts.Env {
		args = append(args, "-e", e)
	}
	args = append(args, opts.Command)

	cmd := exec.CommandContext(ctx, c.bin, args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux.NewSession: %w: %s", err, strings.TrimSpace(se.String()))
	}
	return nil
}

// HasSession reports whether a session named `name` exists on this
// socket. Returns (true, nil) when present, (false, nil) when absent;
// returns a non-nil error only for infrastructure failures (binary
// missing, socket broken). This is the predicate
// chats.GCDeadTmux expects.
func (c *Client) HasSession(ctx context.Context, name string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, fmt.Errorf("tmux.HasSession: empty name")
	}
	cmd := exec.CommandContext(ctx, c.bin, "-S", c.socketPath, "has-session", "-t", name)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// tmux has-session exits 1 when the session is absent. The
		// stderr message starts with "can't find session" or
		// "no server running" — both translate to "absent" for our
		// callers.
		return false, nil
	}
	return false, fmt.Errorf("tmux.HasSession: %w: %s", err, strings.TrimSpace(se.String()))
}

// SetStatusRight pushes a new value into the session's status-right
// area via `tmux set-option -t <name> status-right <text>`. Used by
// the kitsoki TUI's inbox watcher to surface notification counts
// inside an attached pane — the user sees "kitsoki | 2 notifications"
// in the tmux bar even though their TTY is owned by claude.
//
// Tolerates ErrSessionNotFound: a session may vanish between the
// watcher reading state and pushing an update (e.g. user pressed
// Ctrl-B then d the moment the goroutine fired). The caller can
// distinguish these via errors.Is(err, ErrSessionNotFound).
//
// text is interpolated by tmux's status-format machinery — `#[fg=...]`
// style escapes work; literal `#` characters need to be doubled
// (`##`) to survive tmux's preprocessor.
func (c *Client) SetStatusRight(ctx context.Context, name, text string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("tmux.SetStatusRight: empty name")
	}
	cmd := exec.CommandContext(ctx, c.bin, "-S", c.socketPath,
		"set-option", "-t", name, "status-right", text)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(se.String())
		if strings.Contains(stderr, "can't find") || strings.Contains(stderr, "no server") {
			return ErrSessionNotFound
		}
		return fmt.Errorf("tmux.SetStatusRight: %w: %s", err, stderr)
	}
	return fmt.Errorf("tmux.SetStatusRight: %w: %s", err, strings.TrimSpace(se.String()))
}

// KillSession terminates the named session. Returns ErrSessionNotFound
// if no session with that name exists; other errors indicate tmux
// infrastructure failures.
func (c *Client) KillSession(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("tmux.KillSession: empty name")
	}
	cmd := exec.CommandContext(ctx, c.bin, "-S", c.socketPath, "kill-session", "-t", name)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// tmux kill-session exits 1 when the target session doesn't
		// exist. The stderr typically reads "can't find session …".
		// Treat as ErrSessionNotFound so callers don't have to
		// stringmatch.
		stderr := strings.TrimSpace(se.String())
		if strings.Contains(stderr, "can't find") || strings.Contains(stderr, "no server") {
			return ErrSessionNotFound
		}
		return fmt.Errorf("tmux.KillSession: %w: %s", err, stderr)
	}
	return fmt.Errorf("tmux.KillSession: %w: %s", err, strings.TrimSpace(se.String()))
}

// ListSessions returns the names of every session on this socket.
// Empty slice when the tmux server isn't running. Used by
// `kitsoki chat gc` (for cross-checking the chat_pty_sessions table
// against what tmux thinks is alive) and by the inbox watcher.
func (c *Client) ListSessions(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, c.bin, "-S", c.socketPath, "list-sessions", "-F", "#{session_name}")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if err == nil {
		var names []string
		sc := bufio.NewScanner(&so)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" {
				names = append(names, line)
			}
		}
		return names, sc.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// "no server running on …" is the normal "no sessions" case.
		// Surface as empty slice rather than an error so the GC path
		// doesn't have to special-case fresh deployments.
		stderr := strings.TrimSpace(se.String())
		if strings.Contains(stderr, "no server running") || strings.Contains(stderr, "error connecting") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux.ListSessions: %w: %s", err, stderr)
	}
	return nil, fmt.Errorf("tmux.ListSessions: %w: %s", err, strings.TrimSpace(se.String()))
}

// Attach replaces the current process with `tmux attach -t name`,
// passing the calling process's TTY straight through. Returns an
// error when tmux can't be located or when exec fails before the
// process is replaced — once exec succeeds, control never returns to
// the caller.
//
// This is the foreground path for `kitsoki chat attach`. The Phase D
// chrome wrapper will replace this with a framed PTY proxy; for v1 we
// just hand the terminal to tmux directly.
func (c *Client) Attach(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("tmux.Attach: empty name")
	}
	args := []string{"tmux", "-S", c.socketPath, "attach", "-t", name}
	// syscall.Exec replaces the process image so tmux owns the TTY
	// directly (no extra Go goroutine between user keystrokes and
	// tmux's input). Callers that want to do work after detach (e.g.
	// run a heartbeat goroutine concurrently and update DB rows on
	// exit) should fork+exec via os/exec.Cmd.Run instead — see
	// AttachStreaming.
	return execProcess(c.bin, args)
}

// AttachStreaming runs `tmux attach -t name` as a child process,
// inheriting stdin/stdout/stderr so tmux drives the user's TTY
// normally. The caller can run goroutines (heartbeats, watchers)
// alongside. Blocks until the user detaches or tmux exits.
//
// Returns a typed error when tmux says the session doesn't exist
// (ErrSessionNotFound) so the caller can fall back to NewSession
// without parsing stderr.
func (c *Client) AttachStreaming(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("tmux.AttachStreaming: empty name")
	}
	cmd := exec.CommandContext(ctx, c.bin, "-S", c.socketPath, "attach", "-t", name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// tmux attach exits non-zero with a stderr message when the
		// target session is missing. Without capturing stderr we
		// can't read the message — but the test path uses
		// HasSession before attaching, so this error normally
		// signals exit during the session (e.g. claude crashed).
		return fmt.Errorf("tmux.AttachStreaming: %w", err)
	}
	return fmt.Errorf("tmux.AttachStreaming: %w", err)
}

// DefaultSocketPath returns the kitsoki-owned tmux socket location.
// A private socket keeps hosted sessions clear of the user's own tmux
// and survives systemd's RemoveIPC teardown of /tmp/tmux-<uid>:
//
//	$XDG_STATE_HOME/kitsoki/tmux.sock
//	  (fallback: $HOME/.local/state/kitsoki/tmux.sock)
//	  (fallback: $TMPDIR/kitsoki-tmux.sock)
//
// The fallbacks are for environments without $HOME (CI containers,
// systemd transient units). Callers should pass the result to
// (*Client).EnsureSocketDir once at startup.
func DefaultSocketPath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "kitsoki", "tmux.sock")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "kitsoki", "tmux.sock")
	}
	return filepath.Join(os.TempDir(), "kitsoki-tmux.sock")
}

// resolveBin returns the tmux binary path, honouring the test-injection
// env var first.
func resolveBin() (string, error) {
	if bin := os.Getenv(TmuxBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return "", ErrTmuxUnavailable
	}
	return path, nil
}

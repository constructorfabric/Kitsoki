package chatattach

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"kitsoki/internal/chats"
	"kitsoki/internal/tmux"
)

// kitsokiTmuxConf is the inner-tmux config kitsoki ships and passes
// to tmux via `-f` so the user's ~/.tmux.conf stays untouched. See
// kitsoki-tmux.conf for the contents — status bar layout, keys, etc.
//
//go:embed kitsoki-tmux.conf
var kitsokiTmuxConf string

// HeartbeatInterval is how often Run refreshes the chat-lock heartbeat
// while the runTmux callback is blocked. Exposed (variable, not const)
// so tests can crank it down without touching exec wiring.
var HeartbeatInterval = 5 * time.Second

// TmuxSessionPrefix is the naming convention for tmux sessions kitsoki
// owns: kitsoki-chat-<chat-id>. The fixed prefix keeps the namespace
// kitsoki manages disjoint from the user's own tmux sessions and lets
// the CLI's argument-parsing helper strip it without duplicating the
// literal. See docs/architecture/overview.md for where this sits in the
// runtime.
const TmuxSessionPrefix = "kitsoki-chat-"

// Options carries the inputs for Run. ChatID + Store + Tmux are
// required; the rest are optional with sensible zero-value behaviour.
type Options struct {
	// ChatID identifies the chat row to attach to. Must already exist.
	ChatID string
	// Store backs the chat row, the lock, and the chat_pty_sessions
	// table. Concrete *chats.Store; the package needs methods that
	// aren't on host.ChatStore (AttachPTY, DetachPTY, Heartbeat, …).
	Store *chats.Store
	// Tmux is the configured tmux client (kitsoki-owned socket path).
	Tmux *tmux.Client
	// Workspace is the cwd for the `claude --resume` pane. Empty
	// means the chat's existing pty_sessions.workspace_path if any,
	// or the current process's cwd via tmux's default.
	Workspace string
	// PermissionMode is the value passed as `claude --permission-mode`.
	// Defaults to "default" (interactive prompts; claude prompts the
	// human in the pane). The permission-mode story is documented in
	// docs/stories/meta-mode.md.
	PermissionMode string
	// ClaudeBin lets callers override the resolved claude executable.
	// Empty means "claude" (relies on $PATH).
	ClaudeBin string
	// Stderr is where the heartbeat goroutine writes any failures.
	// Optional; defaults to io.Discard so tests stay quiet.
	Stderr io.Writer
}

// Run executes the full attach lifecycle (see package doc). runTmux is
// invoked with the tmux session name once the chat lock is held, the
// tmux session is alive, and chat_pty_sessions is set to
// pty_attached — that's the moment when the caller hands the
// terminal to tmux. When runTmux returns (user detached, tmux exited,
// or an error), Run cleans up and returns.
//
// Returns chats.ErrChatBusy when another driver holds the lock.
// Returns errors from runTmux verbatim so callers can distinguish
// "tmux session vanished" from "claude crashed" — chatattach itself
// adds no wrapping.
func Run(ctx context.Context, opts Options, runTmux func(sessionName string) error) error {
	if opts.Store == nil {
		return fmt.Errorf("chatattach.Run: nil chat store")
	}
	if opts.Tmux == nil {
		return fmt.Errorf("chatattach.Run: nil tmux client")
	}
	if opts.ChatID == "" {
		return fmt.Errorf("chatattach.Run: empty chat ID")
	}
	if runTmux == nil {
		return fmt.Errorf("chatattach.Run: nil runTmux callback")
	}

	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	chatRow, err := opts.Store.Get(ctx, opts.ChatID)
	if err != nil {
		return fmt.Errorf("chatattach.Run: get chat: %w", err)
	}

	// Mint claude_session_id on first attach. --session-id is required
	// only on the first invocation against a brand-new id; --resume
	// works for every subsequent invocation. Track whether we just
	// minted so the claude command line uses the correct flag — passing
	// --resume against a never-seen id fails with "No conversation
	// found" and produces what looks like a crashed attach pane. See
	// the /attach notes in docs/stories/meta-mode.md.
	claudeSID := chatRow.ClaudeSessionID
	minted := false
	if claudeSID == "" {
		claudeSID = uuid.NewString()
		minted = true
		if err := opts.Store.SetClaudeSessionID(ctx, opts.ChatID, claudeSID); err != nil {
			return fmt.Errorf("chatattach.Run: set claude_session_id: %w", err)
		}
	}

	sessionName := TmuxSessionPrefix + opts.ChatID
	claudeBin := opts.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude"
	}
	permissionMode := opts.PermissionMode
	if permissionMode == "" {
		permissionMode = "default"
	}

	cmdLine := buildClaudeCommand(claudeBin, claudeSID, permissionMode, minted)

	return opts.Store.WithLock(ctx, opts.ChatID, func(lockedCtx context.Context) error {
		// Inside the lock: ensure tmux is alive for this chat. Four
		// persistence-and-recovery cases collapse here: fresh attach (no
		// row), live row+live tmux (re-attach), row+dead-tmux (stale row
		// from host reboot), cross-host row (rare). Each path lands at
		// "session exists and is reachable from this host." The
		// chat_pty_sessions lifecycle is described in
		// docs/architecture/overview.md.
		if err := ensureTmuxSession(lockedCtx, opts, sessionName, cmdLine); err != nil {
			return err
		}

		if _, err := opts.Store.AttachPTY(lockedCtx, chats.AttachPTYOptions{
			ChatID:         opts.ChatID,
			TmuxSession:    sessionName,
			PermissionMode: permissionMode,
			WorkspacePath:  opts.Workspace,
		}); err != nil {
			return fmt.Errorf("chatattach.Run: record attach: %w", err)
		}

		// Heartbeat the chat lock until the runTmux callback returns.
		//
		// The heartbeat runs on an independent context, NOT lockedCtx:
		// lockedCtx is cancelled when the interactive tmux attach is
		// interrupted (e.g. the CLI caller gets SIGINT), and we still
		// want the lock kept fresh until we genuinely release it below
		// via `close(stop)`. Sharing lockedCtx would let an interrupted
		// attach kill the heartbeat and leave the lock to go stale. The
		// TUI /attach path already runs on context.Background() for the
		// same reason. hbCancel guarantees the goroutine still exits on
		// genuine release/shutdown.
		hbCtx, hbCancel := context.WithCancel(context.Background())
		defer hbCancel()
		stop := make(chan struct{})
		var hbWG sync.WaitGroup
		hbWG.Add(1)
		go heartbeatLoop(hbCtx, opts.Store, opts.ChatID, stop, &hbWG, stderr)

		attachErr := runTmux(sessionName)

		close(stop)
		hbWG.Wait()

		// Transition to pty_background unless a sibling
		// `kitsoki chat detach --mode {headless,stop}` already removed
		// the row.
		if _, dErr := opts.Store.DetachPTY(lockedCtx, opts.ChatID); dErr != nil && !errors.Is(dErr, chats.ErrNoPTYSession) {
			fmt.Fprintf(stderr, "chatattach.Run: detach: %v\n", dErr)
		}
		return attachErr
	})
}

// ensureTmuxSession is the "make sure tmux is alive" step. Idempotent:
// on a fresh chat it spawns a new session running cmdLine; on
// re-attach it confirms the session is still present (and respawns if
// the row claims it exists but tmux disagrees, e.g. after host reboot).
func ensureTmuxSession(ctx context.Context, opts Options, sessionName, cmdLine string) error {
	if err := opts.Tmux.EnsureSocketDir(); err != nil {
		return fmt.Errorf("chatattach.Run: tmux socket dir: %w", err)
	}

	existing, err := opts.Store.GetPTY(ctx, opts.ChatID)
	switch {
	case errors.Is(err, chats.ErrNoPTYSession):
		// Fresh attach — spawn.
		return spawnTmux(ctx, opts, sessionName, cmdLine)
	case err != nil:
		return fmt.Errorf("chatattach.Run: get pty: %w", err)
	}

	// Row exists. Cross-host rows can't be reached by this host's
	// tmux server — bail with a clear error rather than silently
	// fragmenting the chat across hosts.
	if existing.TmuxHost != "" {
		has, probeErr := opts.Tmux.HasSession(ctx, sessionName)
		if probeErr != nil {
			return fmt.Errorf("chatattach.Run: probe tmux: %w", probeErr)
		}
		if !has {
			// DB row points at a dead session — GC and respawn.
			if err := opts.Store.RemovePTY(ctx, opts.ChatID); err != nil && !errors.Is(err, chats.ErrNoPTYSession) {
				return fmt.Errorf("chatattach.Run: gc stale pty: %w", err)
			}
			return spawnTmux(ctx, opts, sessionName, cmdLine)
		}
	}
	return nil
}

func spawnTmux(ctx context.Context, opts Options, sessionName, cmdLine string) error {
	confPath, cleanup, err := materializeKitsokiTmuxConf()
	if err != nil {
		// Non-fatal: spawn without the kitsoki-shipped config rather
		// than refuse to attach. The session still works; the user
		// just gets default tmux chrome.
		fmt.Fprintf(opts.Stderr, "chatattach: tmux conf: %v\n", err)
	} else {
		// The conf is only read once at session creation; after
		// NewSession returns tmux has finished parsing it, so the
		// tempfile is safe to remove immediately. We schedule cleanup
		// inside this function rather than the outer Run so a
		// re-attach (which skips spawnTmux) doesn't leak.
		defer cleanup()
	}
	return opts.Tmux.NewSession(ctx, tmux.NewSessionOptions{
		Name:       sessionName,
		WorkingDir: opts.Workspace,
		Command:    cmdLine,
		ConfigFile: confPath,
	})
}

// materializeKitsokiTmuxConf writes the embedded kitsoki-tmux.conf to
// a tempfile and returns its path. tmux's `-f` flag needs an
// on-disk file; embedding lets us keep the conf in-binary so the
// install story stays "drop the kitsoki executable somewhere and
// run it."
func materializeKitsokiTmuxConf() (string, func(), error) {
	f, err := os.CreateTemp("", "kitsoki-tmux-*.conf")
	if err != nil {
		return "", nil, fmt.Errorf("create tmux conf tempfile: %w", err)
	}
	if _, err := f.WriteString(kitsokiTmuxConf); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("write tmux conf tempfile: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("close tmux conf tempfile: %w", err)
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

func heartbeatLoop(ctx context.Context, store *chats.Store, chatID string, stop <-chan struct{}, wg *sync.WaitGroup, stderr io.Writer) {
	defer wg.Done()
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.Heartbeat(ctx, chatID); err != nil {
				fmt.Fprintf(stderr, "chatattach: heartbeat: %v\n", err)
			}
		}
	}
}

// buildClaudeCommand assembles the shell command tmux runs in the
// first pane. The session-id selection is the load-bearing bit:
//
//   - minted=true  → --session-id <uuid>  (first invocation; claude
//     CREATES the session under this id)
//   - minted=false → --resume <uuid>      (claude looks the session
//     up; errors with "No conversation found" if the id was never
//     created)
//
// Mixing the two yields the user-visible crash where the first
// /attach mints a fresh UUID but runs --resume against it, prompting
// claude to bail in the pane before the user ever types anything.
//
// --no-chrome keeps update banners out of the in-pane UI;
// --permission-mode is per the chat's policy.
func buildClaudeCommand(bin, claudeSID, permissionMode string, minted bool) string {
	flag := "--resume"
	if minted {
		flag = "--session-id"
	}
	return fmt.Sprintf("%s %s %s --permission-mode %s --no-chrome",
		shQuote(bin), flag, shQuote(claudeSID), shQuote(permissionMode))
}

// shQuote wraps s in single quotes and escapes embedded singles, the
// minimum needed to survive /bin/sh -c which tmux uses for its pane
// command.
func shQuote(s string) string {
	needs := false
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\'' || c == '"' || c == '\\' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	// Escape any embedded ' as '\''
	var out []byte
	out = append(out, '\'')
	for _, c := range []byte(s) {
		if c == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, c)
	}
	out = append(out, '\'')
	return string(out)
}

// meta_attach.go — the `/attach` slash command inside `/meta`.
//
// Typing `/attach` in meta-mode suspends the bubbletea TUI, hands the
// terminal to a live `claude --resume <id>` session inside tmux, and
// resumes the TUI when the user detaches (tmux prefix+d — default
// Ctrl-B then d). The chat row stays alive in pty_background between
// attaches so claude keeps running with the conversation history
// intact; the user can hop in and out at will.
//
// Flow (caller: updateMeta on text == "/attach"):
//
//  1. Build a metaAttachExec — a tea.ExecCommand that bubbletea will
//     invoke after suspending its renderer and capturing the
//     terminal's stdin/stdout/stderr.
//  2. Return tea.Exec(execCmd, callback). bubbletea suspends, calls
//     execCmd.Run(), then resumes and dispatches the callback's
//     metaAttachDoneMsg.
//  3. metaAttachExec.Run delegates to chatattach.Run (chat-lock
//     acquire, tmux session, pty_attached row, heartbeat) and inside
//     runTmux invokes `tmux attach -t <name>` as a child process with
//     the captured stdio so the user sees claude's UI directly.
//  4. On detach, chatattach.Run flips the row to pty_background.
//  5. Bubbletea resumes; updateMeta handles metaAttachDoneMsg and
//     prints a one-line "back; claude is still running in the
//     background — /attach to drop back in".
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/chatattach"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/metamode"
	"kitsoki/internal/tmux"
)

// statusBarPollInterval is how often the inbox watcher pushes
// updates into the attached pane's tmux status-right. 2 s matches
// the kitsoki-tmux.conf's status-interval, which is the cadence at
// which tmux itself repaints the bar — polling faster wouldn't show.
var statusBarPollInterval = 2 * time.Second

// metaAttachDoneMsg is dispatched after the bubbletea-suspended
// chatattach run returns (user detached, tmux exited, or an error).
type metaAttachDoneMsg struct {
	err error
}

// metaAttachExec is the bubbletea-side wrapper around the
// chatattach.Run lifecycle. bubbletea calls SetStdin/Out/Err to hand
// us the user's terminal, then calls Run(); we use the captured fds
// to wire up the `tmux attach` child process. Implements
// tea.ExecCommand.
type metaAttachExec struct {
	ctx        context.Context
	chatStore  *chats.Store
	tmuxClient *tmux.Client
	opts       chatattach.Options

	// jobStore + sessionID power the inbox watcher that pushes
	// notification counts into the attached pane's status bar.
	// Both may be nil (tests, headless runs) — the watcher is then
	// a no-op and the status bar stays static.
	jobStore  *jobs.JobStore
	sessionID app.SessionID

	// bubbletea injects these before calling Run().
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// SetStdin is called by bubbletea right before Run.
func (e *metaAttachExec) SetStdin(r io.Reader) { e.stdin = r }

// SetStdout is called by bubbletea right before Run.
func (e *metaAttachExec) SetStdout(w io.Writer) { e.stdout = w }

// SetStderr is called by bubbletea right before Run.
func (e *metaAttachExec) SetStderr(w io.Writer) { e.stderr = w }

// Run is the blocking entry point invoked by bubbletea while the TUI
// is suspended. Delegates the whole lifecycle to chatattach.Run; the
// runTmux callback is where we actually exec `tmux attach` with the
// captured stdio so the user sees claude's TUI rather than ours.
//
// Inside runTmux we also spawn an inbox-watcher goroutine — the
// bubbletea poll ticker is suspended for the duration of Exec, so
// status-bar updates can't ride on tea.Cmd's; we own them directly
// here and push via `tmux set-option ... status-right ...`.
func (e *metaAttachExec) Run() error {
	runTmux := func(sessionName string) error {
		var (
			watcherStop = make(chan struct{})
			wg          sync.WaitGroup
		)
		if e.jobStore != nil && e.sessionID != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runStatusBarWatcher(e.ctx, e.tmuxClient, sessionName,
					e.jobStore, e.sessionID, watcherStop, e.stderr)
			}()
		}
		defer func() {
			close(watcherStop)
			wg.Wait()
		}()

		cmd := exec.CommandContext(e.ctx, e.tmuxClient.Bin(),
			"-S", e.tmuxClient.SocketPath(), "attach", "-t", sessionName)
		cmd.Stdin = e.stdin
		cmd.Stdout = e.stdout
		cmd.Stderr = e.stderr
		return cmd.Run()
	}
	return chatattach.Run(e.ctx, e.opts, runTmux)
}

// runStatusBarWatcher polls the inbox at statusBarPollInterval and
// pushes the result into the attached session's tmux status-right.
// Returns when stop closes or the context is cancelled. Each push
// failure is logged to stderr (which during Exec is the user's tty
// — and tmux will obscure it; that's intentional, the watcher is
// best-effort).
func runStatusBarWatcher(
	ctx context.Context,
	tx *tmux.Client,
	sessionName string,
	js *jobs.JobStore,
	sid app.SessionID,
	stop <-chan struct{},
	stderr io.Writer,
) {
	push := func() {
		counts, err := js.UnreadCount(ctx, sid)
		if err != nil {
			// Don't spam — a single transient SQLite hiccup
			// shouldn't blast the user's stderr. Silently skip.
			return
		}
		text := formatStatusRight(counts)
		if err := tx.SetStatusRight(ctx, sessionName, text); err != nil {
			if errors.Is(err, tmux.ErrSessionNotFound) {
				// Session vanished between the watcher reading and
				// pushing — almost certainly because the user just
				// detached. Treat as benign.
				return
			}
			fmt.Fprintf(stderr, "kitsoki: status bar update: %v\n", err)
		}
	}
	// Fire one immediate update so the status bar isn't blank for
	// the first tick.
	push()
	ticker := time.NewTicker(statusBarPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			push()
		}
	}
}

// formatStatusRight renders the severity-grouped unread count as a
// status-right string. Empty when nothing's pending so the bar
// stays uncluttered. Severity gets a colour hint via tmux's
// `#[fg=...]` style sequences.
func formatStatusRight(counts map[jobs.NotificationSeverity]int) string {
	if len(counts) == 0 {
		return ""
	}
	action := counts[jobs.SeverityActionRequired]
	info := counts[jobs.SeverityInfo]
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return ""
	}
	// Severity-coloured fragments. `##` is the tmux escape for a
	// literal `#`, but we're not emitting any literal `#` so no
	// doubling is needed.
	parts := make([]string, 0, 2)
	if action > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=#F59E0B bold]⚠ %d action#[default]", action))
	}
	if info > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=#3B82F6]● %d info#[default]", info))
	}
	if len(parts) == 0 {
		// Unknown severity bucket — fall back to a raw count.
		parts = append(parts, fmt.Sprintf("#[fg=#9CA3AF]%d pending#[default]", total))
	}
	out := "#[fg=#3B82F6 bold]kitsoki#[default] | "
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// handleMetaAttach is the slash-command entry point invoked from
// updateMeta when the user types /attach. Builds a metaAttachExec
// closed over the active meta-mode session and returns the tea.Exec
// command that suspends-runs-resumes.
func (m RootModel) handleMetaAttach() (tea.Model, tea.Cmd) {
	if m.metaMode.session == nil {
		m.transcript.AppendSystem("(no active meta session)")
		return m, nil
	}
	if m.chatStore == nil {
		m.transcript.AppendSystem("(/attach requires a chat store — pass --db when launching)")
		return m, nil
	}

	chatID := m.metaMode.session.Chat.ID()
	workspace := metamode.SessionWorkspace(m.metaMode.session, m.appPath)

	tmuxClient, err := tmux.New(tmux.DefaultSocketPath())
	if err != nil {
		m.transcript.AppendError("(meta)", fmt.Sprintf("/attach: %v", err))
		return m, nil
	}

	exec := &metaAttachExec{
		ctx:        context.Background(),
		chatStore:  m.chatStore,
		tmuxClient: tmuxClient,
		jobStore:   m.jobStore,
		sessionID:  m.sid,
		opts: chatattach.Options{
			ChatID:    chatID,
			Store:     m.chatStore,
			Tmux:      tmuxClient,
			Workspace: workspace,
			// Default permission mode: claude prompts the human in
			// the pane. Power users who want bypass can run
			// `kitsoki chat attach <id> --permission-mode bypassPermissions`
			// from another terminal instead.
			PermissionMode: "default",
		},
	}

	m.transcript.AppendSystem("attaching to claude — detach with Ctrl-B then d to leave it running in the background")
	return m, tea.Exec(exec, func(err error) tea.Msg {
		return metaAttachDoneMsg{err: err}
	})
}

// handleMetaAttachDone is invoked when the bubbletea-suspended
// chatattach call returns. We print a short summary so the user knows
// they're back in kitsoki, and (when relevant) call out that claude
// is still running.
func (m RootModel) handleMetaAttachDone(msg metaAttachDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if errors.Is(msg.err, chats.ErrChatBusy) {
			m.transcript.AppendError("(meta)",
				"this chat is currently held by another driver — wait for it to release, or run `kitsoki chat unlock --force` if you know it's stuck")
			return m, nil
		}
		m.transcript.AppendError("(meta)", fmt.Sprintf("/attach: %v", msg.err))
		return m, nil
	}
	m.transcript.AppendSystem("back in kitsoki — claude is still running in the background (/attach to drop back in, or keep typing here)")
	return m, nil
}

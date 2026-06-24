// sessions.go — /sessions slash-command family.
//
// /sessions list  — print a numbered list of active chat_pty_sessions
//
//	rows on this host (chats with claude alive in
//	tmux right now, attached or background).
//
// /sessions attach <N> [--dry-run]
//
//	— re-attach to the Nth row from the most recent
//	   /sessions list output. No chat IDs typed. The TUI
//	   suspends, the user lands in the live
//	   `claude --resume` pane; Ctrl-B then d returns
//	   them to kitsoki.
//	   With --dry-run, prints the resolved target without
//	   attaching; useful for MCP/headless smoke tests.
//
// The numbering is cached on RootModel between list and attach so
// the user can `list`, eyeball, `attach 3` with no typing of opaque
// IDs. The cache is invalidated by every fresh `list` call.
//
// Works in both on-path and meta-mode by being routed from
// handleSlashCommand AND updateMeta — see the wiring in tui.go.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/chatattach"
	"kitsoki/internal/chats"
	"kitsoki/internal/tmux"
)

// handleSessionsSlash dispatches /sessions and its subcommands.
// Returns (model, cmd) per the tea.Model.Update contract.
func (m RootModel) handleSessionsSlash(args []string) (tea.Model, tea.Cmd) {
	if m.chatStore == nil {
		m.transcript.AppendSystem("(/sessions requires a chat store — pass --db when launching)")
		return m, nil
	}
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch verb {
	case "", "list":
		return m.handleSessionsList()
	case "attach":
		if len(args) < 2 {
			m.transcript.AppendSystem("(/sessions attach: usage — /sessions attach <N> [--dry-run])")
			return m, nil
		}
		return m.handleSessionsAttach(args[1], sessionsAttachDryRun(args[2:]))
	default:
		m.transcript.AppendSystem(fmt.Sprintf(
			"(/sessions: unknown subcommand %q — try 'list' or 'attach <N>')", verb))
		return m, nil
	}
}

// handleSessionsList queries chat_pty_sessions, caches the result on
// the model for /sessions attach, and renders a numbered table to
// the transcript using the same blue styling as /meta list. Empty
// result emits a friendly "nothing running" note via AppendSystem
// (consistent with /meta list's empty-state path).
func (m RootModel) handleSessionsList() (tea.Model, tea.Cmd) {
	ctx := context.Background()
	rows, err := m.chatStore.ListPTYForHost(ctx)
	if err != nil {
		m.transcript.AppendError("(sessions)", err.Error())
		return m, nil
	}
	if len(rows) == 0 {
		m.transcript.AppendSystem("(no active claude sessions on this host)")
		// Clear any stale cache so a subsequent attach doesn't
		// point at gone-by rows.
		m.sessionList = nil
		return m, nil
	}

	m.sessionList = rows

	cells := make([][]string, 0, len(rows))
	for i, p := range rows {
		cells = append(cells, sessionsListingCells(i+1, p, m.chatStore))
	}
	m.transcript.AppendStyledTable(
		"claude sessions",
		sessionsListColumns(),
		cells,
		"(no active claude sessions on this host)",
	)
	// Footer hint, rendered through AppendSystem so it picks up the
	// blue slash-output style — same look /meta uses for ambient
	// guidance lines.
	m.transcript.AppendSystem("(use /sessions attach <N> to re-enter, or Ctrl-B then d after attaching to leave it running)")
	return m, nil
}

// sessionsListColumns is the header row for /sessions list. Order
// matches sessionsListingCells. Kept compact so the table fits in
// narrow terminals — the chat title is the visually-prominent
// column, IDLE / SCOPE help disambiguate sibling rows.
func sessionsListColumns() []string {
	return []string{"#", "CHAT", "MODE", "IDLE", "SCOPE"}
}

// sessionsListingCells renders one /sessions list row. n is the
// 1-indexed position the user types into /sessions attach. The chat
// title is read from the underlying chats row when possible; on a
// lookup failure we fall back to the chat id so the listing still
// works.
func sessionsListingCells(n int, p chats.PtySession, cs *chats.Store) []string {
	title := p.ChatID
	scope := ""
	if cs != nil {
		if c, err := cs.Get(context.Background(), p.ChatID); err == nil {
			title = c.Title
			scope = c.ScopeKey
		}
	}
	if scope == "" {
		scope = "(no scope)"
	}

	// Compact mode label — strip the "pty_" prefix so the column
	// width stays narrow while still distinguishing attached vs
	// background.
	mode := string(p.Mode)
	switch p.Mode {
	case chats.PtyModeAttached:
		mode = "attached"
	case chats.PtyModeBackground:
		mode = "background"
	}

	idle := "—"
	if p.LastIdleAt != nil {
		idle = p.LastIdleAt.Local().Format("15:04:05")
		// Append a hint when idle is more than a few minutes old so
		// the user can spot stale sessions at a glance.
		if since := time.Since(*p.LastIdleAt); since > 5*time.Minute {
			idle = fmt.Sprintf("%s (%s ago)", idle, formatShortDuration(since))
		}
	}

	return []string{
		fmt.Sprintf("%d", n),
		title,
		mode,
		idle,
		scope,
	}
}

// formatShortDuration renders a time.Duration in a one-or-two-token
// form suitable for the IDLE column. We don't need millisecond
// precision; minutes/hours/days are enough.
func formatShortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// handleSessionsAttach resolves the Nth row from the cached list and
// fires the same tea.Exec-based attach the meta-mode /attach uses.
// The cache is the only state — re-listing rebuilds it.
func sessionsAttachDryRun(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--dry-run", "-n", "dry-run":
			return true
		}
	}
	return false
}

func (m RootModel) handleSessionsAttach(arg string, dryRun bool) (tea.Model, tea.Cmd) {
	if len(m.sessionList) == 0 {
		m.transcript.AppendSystem("(no cached sessions list — run /sessions list first)")
		return m, nil
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > len(m.sessionList) {
		m.transcript.AppendSystem(fmt.Sprintf(
			"(/sessions attach: invalid index %q — run /sessions list for the current numbering)", arg))
		return m, nil
	}
	picked := m.sessionList[n-1]
	if dryRun {
		m.transcript.AppendSystem(sessionsAttachDryRunLine(n, picked, m.chatStore))
		return m, nil
	}

	tmuxClient, err := tmux.New(tmux.DefaultSocketPath())
	if err != nil {
		m.transcript.AppendError("(sessions)", err.Error())
		return m, nil
	}

	execCmd := &metaAttachExec{
		ctx:        context.Background(),
		chatStore:  m.chatStore,
		tmuxClient: tmuxClient,
		jobStore:   m.jobStore,
		sessionID:  m.sid,
		opts: chatattach.Options{
			ChatID:         picked.ChatID,
			Store:          m.chatStore,
			Tmux:           tmuxClient,
			Workspace:      picked.WorkspacePath,
			PermissionMode: picked.PermissionMode,
		},
	}

	m.transcript.AppendSystem(fmt.Sprintf(
		"attaching to session %d — Ctrl-B then d to leave it running in the background", n))
	return m, tea.Exec(execCmd, func(err error) tea.Msg {
		return metaAttachDoneMsg{err: err}
	})
}

func sessionsAttachDryRunLine(n int, picked chats.PtySession, cs *chats.Store) string {
	title := picked.ChatID
	if cs != nil {
		if c, err := cs.Get(context.Background(), picked.ChatID); err == nil && c.Title != "" {
			title = c.Title
		}
	}
	tmuxName := picked.TmuxSession
	if tmuxName == "" {
		tmuxName = "(no tmux session)"
	}
	mode := string(picked.Mode)
	if mode == "" {
		mode = "(unknown mode)"
	}
	return fmt.Sprintf("(/sessions attach: would attach %d to %s [%s] via tmux %s, %s)",
		n, title, picked.ChatID, tmuxName, mode)
}

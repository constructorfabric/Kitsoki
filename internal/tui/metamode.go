// metamode.go — the /meta overlay (LLM-driven sidebar conversation).
//
// Shape mirrors offpath.go and edit.go:
//
//   - metaModel is a tiny state record (active flag, banner, in-flight
//     flag, session pointer). The transcript and prompt live on the
//     root model, exactly like edit mode.
//   - tea.Cmd factories below wrap metamode.Controller.Enter/Send into
//     async messages (metaEnterDoneMsg, metaSendDoneMsg) so the root
//     can react on the main bubbletea goroutine.
//   - Exit is synchronous (Controller.Exit is a no-op in Phase A) so
//     no cmd factory is needed.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/metamode"
)

// metaModel holds the per-session state for one /meta overlay. Reset
// on Exit(); created fresh on every Enter().
type metaModel struct {
	active   bool
	session  *metamode.Session
	banner   string
	inFlight bool
	// turns counts every successful Send during this overlay; edits
	// counts the subset that produced a SendResult.ReloadRequested
	// (i.e. the agent touched a file in the story tree). changedFiles
	// accumulates the per-turn ChangedFiles lists so the exit summary
	// can list everything the agent touched.
	turns        int
	edits        int
	changedFiles []string
}

func newMetaModel() metaModel { return metaModel{} }

// Active reports whether the overlay currently owns the screen.
func (m metaModel) Active() bool { return m.active }

// Banner returns the resolved banner string for the active session.
func (m metaModel) Banner() string { return m.banner }

// View renders the styled banner. Returns empty when inactive — the
// root composes the banner with the transcript.
func (m metaModel) View() string {
	if !m.active || m.banner == "" {
		return ""
	}
	return metaBannerStyle.Render(m.banner)
}

// Enter activates the overlay against the supplied session. The banner
// is resolved from session.Mode.Banner, falling back to a synthesized
// "*** meta:<name> ***" string when the mode declares no banner.
func (m *metaModel) Enter(session *metamode.Session) {
	m.active = true
	m.session = session
	m.banner = resolveMetaBanner(session)
}

// Exit clears all overlay state. Safe to call when already inactive.
func (m *metaModel) Exit() {
	m.active = false
	m.session = nil
	m.banner = ""
	m.inFlight = false
	m.turns = 0
	m.edits = 0
	m.changedFiles = nil
}

// resolveMetaBanner returns the banner string for a session: the mode's
// Banner if set, else a synthesized "*** meta:<name> ***".
func resolveMetaBanner(s *metamode.Session) string {
	if s == nil || s.Mode == nil {
		return ""
	}
	if b := s.Mode.Banner; b != "" {
		return b
	}
	// Synthesize from the room key (e.g. "meta:story") so the user
	// sees what mode they're in even when the YAML omits Banner.
	name := s.Chat.Room()
	return "*** " + name + " ***"
}

// metaBannerStyle is the styled wrapper around the resolved banner.
// Borrows the off-path banner styling so the two overlay surfaces feel
// visually related.
var metaBannerStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

// MenuView renders the side-panel replacement shown while ModeMeta is
// active. The on-path actions menu (`m.menu.View()`) is irrelevant in
// meta mode — those intents fire FSM transitions the overlay paused.
// Show the meta-specific slash commands instead so users see what
// they can actually do.
func (m metaModel) MenuView(width, height int) string {
	const title = "Meta mode"
	rule := strings.Repeat("─", maxInt(width-4, 4))

	header := lipgloss.NewStyle().Bold(true).Foreground(colorInfo).Render(title)
	rows := []struct{ cmd, desc string }{
		{"/onpath", "return to the story (keep chat)"},
		{"/meta done", "archive this chat + exit"},
		{"/meta list", "list chats"},
		{"/meta new", "fresh chat"},
		{"/meta resume <id>", "jump to a chat by id"},
	}

	cmdStyle := lipgloss.NewStyle().Foreground(colorInfo)
	descStyle := lipgloss.NewStyle().Foreground(colorMuted)

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(rule)
	sb.WriteString("\n")
	for _, r := range rows {
		sb.WriteString(cmdStyle.Render(r.cmd))
		sb.WriteString("\n  ")
		sb.WriteString(descStyle.Render(r.desc))
		sb.WriteString("\n")
	}
	if m.session != nil && m.session.Chat != nil {
		sb.WriteString("\n")
		id := m.session.Chat.ID()
		if len(id) > 8 {
			id = id[:8]
		}
		sb.WriteString(descStyle.Render("chat: " + id))
	}

	w := width - 2
	if w < 10 {
		w = 10
	}
	return menuStyle.Width(w).Height(height).Render(sb.String())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// metaEnterDoneMsg is emitted by metaEnterCmd when Controller.Enter
// returns (success or error).
type metaEnterDoneMsg struct {
	session  *metamode.Session
	modeName string
	err      error
}

// metaSendDoneMsg is emitted by metaSendCmd when Controller.Send
// returns (success or error). Carries the SendResult so the root can
// trigger an orchestrator reload when ReloadRequested is true.
type metaSendDoneMsg struct {
	userText string
	result   metamode.SendResult
	err      error
}

// metaListDoneMsg is emitted by metaListCmd when Controller.ListChats
// returns. The TUI renders each listing as a transcript line.
type metaListDoneMsg struct {
	listings []metamode.ChatListing
	err      error
}

// metaNewDoneMsg is emitted by metaNewCmd when Controller.NewChat
// returns. On success the TUI swaps Session.Chat without changing
// mode (we stay in ModeMeta).
type metaNewDoneMsg struct {
	session *metamode.Session
	err     error
}

// metaDoneDoneMsg is emitted by metaDoneCmd when Controller.Done
// returns. On success the TUI archives the chat, exits meta mode,
// and prints a confirmation with the (truncated) chat ID so the
// user can resume by id-prefix if they later regret the close.
type metaDoneDoneMsg struct {
	archivedID string
	err        error
}

// metaEnterCmd asynchronously calls Controller.Enter for modeName.
// Falls into metaEnterDoneMsg on completion.
func metaEnterCmd(ctx context.Context, ctrl *metamode.Controller, snap metamode.Snapshot, modeName string) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaEnterDoneMsg{modeName: modeName, err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		sess, err := ctrl.Enter(ctx, snap, modeName)
		return metaEnterDoneMsg{session: sess, modeName: modeName, err: err}
	}
}

// metaSendCmd asynchronously calls Controller.Send and emits
// metaSendDoneMsg on completion. turn carries the per-turn ambient
// state (state path, app file, rendered view, world snapshot) the
// controller injects into the agent's user message — see
// metamode.TurnContext.
func metaSendCmd(ctx context.Context, ctrl *metamode.Controller, sess *metamode.Session, userText string, turn metamode.TurnContext) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaSendDoneMsg{userText: userText, err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		res, err := ctrl.Send(ctx, sess, userText, turn)
		return metaSendDoneMsg{userText: userText, result: res, err: err}
	}
}

// metaListCmd asynchronously enumerates the app's meta chats.
func metaListCmd(ctx context.Context, ctrl *metamode.Controller, appID string) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaListDoneMsg{err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		listings, err := ctrl.ListChats(ctx, appID)
		return metaListDoneMsg{listings: listings, err: err}
	}
}

// metaNewCmd asynchronously archives the active chat and opens a
// fresh one in the same (mode, scope).
func metaNewCmd(ctx context.Context, ctrl *metamode.Controller, sess *metamode.Session) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaNewDoneMsg{err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		s, err := ctrl.NewChat(ctx, sess)
		return metaNewDoneMsg{session: s, err: err}
	}
}

// metaDoneCmd asynchronously archives the active chat without opening
// a replacement, then emits metaDoneDoneMsg. The TUI's handler is
// responsible for exiting meta mode and printing the confirmation —
// keeping mode flips off the io goroutine.
func metaDoneCmd(ctx context.Context, ctrl *metamode.Controller, sess *metamode.Session) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaDoneDoneMsg{err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		id, err := ctrl.Done(ctx, sess)
		return metaDoneDoneMsg{archivedID: id, err: err}
	}
}

// metaResumeCmd asynchronously resumes a meta chat by full ID. The
// caller is responsible for ID-prefix → ID resolution (the
// disambiguation surface lives in the TUI).
func metaResumeCmd(ctx context.Context, ctrl *metamode.Controller, snap metamode.Snapshot, modeName, chatID string) tea.Cmd {
	return func() tea.Msg {
		if ctrl == nil {
			return metaEnterDoneMsg{modeName: modeName, err: fmt.Errorf("meta mode unavailable: controller not wired")}
		}
		sess, err := ctrl.EnterByChatID(ctx, snap, modeName, chatID)
		return metaEnterDoneMsg{session: sess, modeName: modeName, err: err}
	}
}

// firstMetaModeName returns the lexicographically-first meta-mode name
// declared on the AppDef, or "" if none are declared. Sort order is
// the same one the loader's validator uses (sortedKeys) so error
// messages and runtime selection agree.
//
// With grouped keys (`story.edit`, `kitsoki.bug`, …) the naive
// "first lex key" would surface `kitsoki.ask` as the default for
// `/meta` (no args) — surprising, since `ask` is the read-only verb.
// The grouped-aware variant `firstMetaModeNameForDef` (preferred
// entry-point below) prefers the lex-first GROUP's default verb. This
// bare helper is kept for callers that already have just the sorted
// name slice and accept the lex-first result; the TUI's startMetaMode
// uses the def-aware variant.
func firstMetaModeName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

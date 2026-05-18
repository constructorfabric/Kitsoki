package tui

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/tui/blocks"
)

// commands_jump.go — single-pane-tui §"/jump": navigate to a recent
// background-completion event. /jump (or /jump 0) goes to the latest;
// /jump <n> is the n-th most recent, 0-indexed newest-first.
//
// Background-completion entries are pushed onto backgroundCompletions
// from the inbox-arrival path (see the inboxRefreshed case in tui.go).
// Each entry carries the originating notification's ID + teleport
// target so /jump can re-dispatch the same path the action_required
// Enter keybinding hits.

// queueDepthLabel formats the per-input queue depth message in the
// in-flight-enqueue acknowledgement block. Singular vs plural so the
// message reads naturally at depth 1 too.
func queueDepthLabel(n int) string {
	if n == 1 {
		return "1 queued"
	}
	return strconv.Itoa(n) + " queued"
}

// backgroundCompletion is one row in the recent-background-completions
// log. Stored newest-first; cap at recentBackgroundCap so a long
// session doesn't grow the slice without bound.
type backgroundCompletion struct {
	// NotificationID is the inbox notification this completion came
	// from. /jump uses it to look the notification up and dispatch
	// the same path the action_required Enter keybinding hits.
	NotificationID string
	// Room is the teleport target advertised by the notification
	// (TeleportState), or "inbox" when none is set.
	Room string
	// Summary is the notification title — printed in the friendly
	// confirmation block.
	Summary string
}

// HandleJumpCommand implements /jump and /jump <n>. Returns the chat
// block to append and the next model (plus a tea.Cmd when /jump
// actually teleports).
func HandleJumpCommand(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	idx := 0
	if len(args) >= 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			return r.SlashOutput("(jump: usage: /jump [<n>]   — 0-indexed, newest first)"), m, nil
		}
		idx = n
	}
	if idx >= len(m.backgroundCompletions) {
		if len(m.backgroundCompletions) == 0 {
			return r.SlashOutput("(jump: no background events yet — completions print here when bg work finishes in another room)"), m, nil
		}
		return r.SlashOutput("(jump: index out of range — only the latest few completions are kept)"), m, nil
	}
	bc := m.backgroundCompletions[idx]

	// Find the notification by ID and dispatch through the
	// same path the action_required-banner Enter key uses. If the
	// notification is gone (dismissed in another flow), the entry is
	// stale — print a notice rather than fail.
	for _, n := range m.lastNotifications {
		if n.ID != bc.NotificationID {
			continue
		}
		updated, cmd := m.handleInboxItemSelected(inboxItemSelected{notification: n})
		if rm, ok := updated.(RootModel); ok {
			return r.SlashOutput("(jump · " + bc.Room + " · " + bc.Summary + ")"), rm, cmd
		}
		return r.SlashOutput("(jump · " + bc.Room + " · " + bc.Summary + ")"), m, cmd
	}
	return r.SlashOutput("(jump: notification " + bc.NotificationID + " is no longer in the inbox — entry skipped)"), m, nil
}

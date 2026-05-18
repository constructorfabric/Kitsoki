package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"kitsoki/internal/jobs"
	"kitsoki/internal/tui/blocks"
)

// commands_inbox.go — single-pane-tui §"/inbox": prints inbox state as
// a chat block (instead of toggling the side panel) and also emits a
// transcript line for every newly-arrived notification so the user
// sees them without watching a separate pane.
//
// Phase 1 keeps the legacy inbox panel updating in parallel; phase 3
// removes the panel entirely.

// renderInboxBlock renders /inbox output as a chat block. With no
// args it prints the unread items; `/inbox <n>` opens (teleports to)
// the n-th notification. `/inbox all` prints every notification
// regardless of read state.
func renderInboxBlock(m RootModel, args []string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if m.jobStore == nil {
		return r.SlashOutput("(inbox: no job store wired — running in tests or headless)")
	}

	notifications := m.lastNotifications

	if len(args) == 1 {
		if strings.EqualFold(args[0], "all") {
			return renderInboxList(r, notifications, false)
		}
		if n, err := strconv.Atoi(args[0]); err == nil {
			return openInboxItem(r, notifications, n)
		}
	}

	// Default: unread only.
	return renderInboxList(r, notifications, true)
}

func renderInboxList(r *blocks.Renderer, notifs []jobs.Notification, unreadOnly bool) string {
	var visible []jobs.Notification
	for _, n := range notifs {
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		visible = append(visible, n)
	}
	if len(visible) == 0 {
		if unreadOnly {
			return r.SlashOutput("(inbox: no unread notifications — /inbox all to see read items)")
		}
		return r.SlashOutput("(inbox: empty)")
	}
	var sb strings.Builder
	header := "unread notifications"
	if !unreadOnly {
		header = "all notifications"
	}
	sb.WriteString(r.SlashOutput("  " + header + ":"))
	sb.WriteString("\n")
	for i, n := range visible {
		age := humanAge(time.Since(n.CreatedAt))
		idx := fmt.Sprintf("  %d. ", i+1)
		sb.WriteString(idx)
		sb.WriteString(r.Inbox(blocks.InboxNotification{
			ID:       n.ID,
			Title:    n.Title,
			Severity: string(n.Severity),
			Age:      age,
		}))
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func openInboxItem(r *blocks.Renderer, notifs []jobs.Notification, n int) string {
	if n < 1 || n > len(notifs) {
		return r.SlashOutput("(inbox: index out of range)")
	}
	// Opening sends an inboxItemSelected through the same path the
	// legacy panel uses — done by the caller via a follow-up cmd. For
	// now Phase 1 just prints "would open" so we don't double-wire
	// dispatch; phase 3 splits openInboxItem properly.
	notif := notifs[n-1]
	return r.SlashOutput(fmt.Sprintf("(inbox: open %s — teleport handled by the existing inbox panel path for now)", notif.Title))
}

// newInboxNotifications returns the notifications in fresh that are not
// in prior. Used to decide which transcript lines to print on each
// poll. Comparison is by ID — IDs are stable across polls.
func newInboxNotifications(prior, fresh []jobs.Notification) []jobs.Notification {
	known := make(map[string]struct{}, len(prior))
	for _, n := range prior {
		known[n.ID] = struct{}{}
	}
	var out []jobs.Notification
	for _, n := range fresh {
		if _, ok := known[n.ID]; ok {
			continue
		}
		out = append(out, n)
	}
	return out
}

// humanAge returns a short relative time label suitable for the inbox
// block age column: "just now" / "32s" / "5m" / "2h" / "3d". Not
// internationalised — the surrounding pongo2 layer can do that later.
func humanAge(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

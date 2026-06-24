// Package tui — Inbox panel sub-model.
//
// The inbox panel sits below the Actions menu in the right column and shows
// background-job notifications fetched from a *jobs.JobStore via a polling
// ticker.
//
// # Three views
//
//   - Hidden — no notifications; renders as a single line "Inbox: empty".
//   - Compact (default) — latest 5 notifications (read + unread), one line each
//     with severity glyph, title, and relative timestamp.
//   - Expanded — latest 20 notifications with full body text.
//
// # Keyspace
//
//   - i1–i9   select inbox item N (1-indexed, 0-padded if fewer exist).
//   - /inbox  slash command handled by the parent RootModel to toggle Expanded.
//
// The panel never blocks the UI: if jobStore is nil the ticker is a no-op
// and the panel stays hidden so headless tests work without a database.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/jobs"
)

// inboxView is the display mode for the inbox panel.
type inboxView int

const (
	// inboxHidden collapses the panel to a single "Inbox: empty" line.
	inboxHidden inboxView = iota
	// inboxCompact shows the latest 5 notifications, one line each.
	inboxCompact
	// inboxExpanded shows the latest 20 notifications with full body text.
	inboxExpanded
)

// inboxRefreshed is a tea.Msg dispatched by the polling ticker with a fresh
// snapshot of notifications for the current session.
type inboxRefreshed struct {
	notifications []jobs.Notification
}

// inboxItemSelected is a tea.Msg emitted when the user presses i<N> and a
// notification at that index exists. The parent RootModel handles teleport.
type inboxItemSelected struct {
	notification jobs.Notification
}

// inboxModel renders the Inbox panel.
type inboxModel struct {
	notifications []jobs.Notification
	selected      int
	width         int
	height        int
	view          inboxView
}

// newInboxModel constructs an inboxModel at the given dimensions.
func newInboxModel(width, height int) inboxModel {
	return inboxModel{
		width:  width,
		height: height,
		view:   inboxHidden,
	}
}

// Init implements tea.Model.
func (m inboxModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m inboxModel) Update(msg tea.Msg) (inboxModel, tea.Cmd) {
	switch msg := msg.(type) {
	case inboxRefreshed:
		m.notifications = msg.notifications
		// Auto-promote from hidden to compact once notifications arrive.
		if m.view == inboxHidden && len(m.notifications) > 0 {
			m.view = inboxCompact
		}
		// Auto-hide when all are gone.
		if len(m.notifications) == 0 {
			m.view = inboxHidden
		}
		// Clamp selection.
		if m.selected >= len(m.notifications) {
			m.selected = max(0, len(m.notifications)-1)
		}

	case tea.KeyMsg:
		s := msg.String()
		// i1–i9: select inbox item.
		if len(s) == 2 && s[0] == 'i' {
			digit := int(s[1] - '0')
			if digit >= 1 && digit <= 9 {
				idx := digit - 1
				if idx < len(m.notifications) {
					m.selected = idx
					return m, func() tea.Msg {
						return inboxItemSelected{notification: m.notifications[idx]}
					}
				}
			}
		}
	}
	return m, nil
}

// ToggleExpanded switches between inboxCompact and inboxExpanded views.
// A no-op when hidden.
func (m *inboxModel) ToggleExpanded() {
	switch m.view {
	case inboxCompact:
		m.view = inboxExpanded
	case inboxExpanded:
		m.view = inboxCompact
	}
}

// UnreadCount returns the number of notifications without a ReadAt timestamp.
func (m inboxModel) UnreadCount() int {
	n := 0
	for _, notif := range m.notifications {
		if notif.ReadAt == nil {
			n++
		}
	}
	return n
}

// ActionRequiredNotification returns the first unread action_required
// notification, or nil if none.
func (m inboxModel) ActionRequiredNotification() *jobs.Notification {
	for i := range m.notifications {
		n := &m.notifications[i]
		if n.ReadAt == nil && n.Severity == jobs.SeverityActionRequired {
			return n
		}
	}
	return nil
}

// View implements tea.Model.
func (m inboxModel) View() string {
	if m.view == inboxHidden || len(m.notifications) == 0 {
		w := m.width - 2
		if w < 10 {
			w = 10
		}
		emptyLine := lipgloss.NewStyle().Foreground(colorMuted).Render("Inbox: empty")
		return menuStyle.Width(w).Render(emptyLine)
	}

	var sb strings.Builder

	// Title row: "Inbox    ●●○ 2 / 5"
	unread := m.UnreadCount()
	total := len(m.notifications)
	titleRight := inboxBadgeStr(unread, total)
	titleStr := lipgloss.NewStyle().Bold(true).Render("Inbox") + "  " +
		lipgloss.NewStyle().Foreground(colorMuted).Render(titleRight)
	sb.WriteString(titleStr)
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", max(m.width-4, 4)))
	sb.WriteString("\n")

	// Determine how many items to show.
	limit := 5
	if m.view == inboxExpanded {
		limit = 20
	}
	shown := m.notifications
	if len(shown) > limit {
		shown = shown[:limit]
	}

	now := time.Now()
	for i, n := range shown {
		glyph, glyphStyle := severityGlyph(n.Severity)
		relTime := humanizeDuration(now.Sub(n.CreatedAt))

		// Dim read notifications.
		titleStyle := lipgloss.NewStyle().Foreground(colorText)
		if n.ReadAt != nil {
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}

		prefix := fmt.Sprintf("%s ", glyphStyle.Render(glyph))
		timeStr := lipgloss.NewStyle().Foreground(colorMuted).Render(relTime)

		line := prefix + titleStyle.Render(truncate(n.Title, m.width-12)) + " " + timeStr
		if i == m.selected {
			line = menuItemSelectedStyle.Render(prefix+truncate(n.Title, m.width-12)) + " " + timeStr
		}
		sb.WriteString(line)
		sb.WriteString("\n")

		if m.view == inboxExpanded && n.Body != "" {
			bodyLine := "   " + lipgloss.NewStyle().Foreground(colorMuted).Render(truncate(n.Body, m.width-7))
			sb.WriteString(bodyLine)
			sb.WriteString("\n")
		}
	}

	w := m.width - 2
	if w < 10 {
		w = 10
	}
	return menuStyle.Width(w).Height(m.height).MaxHeight(m.height).Render(sb.String())
}

// ActionRequiredBanner returns a one-line banner for the first unread
// action_required notification, or "" if there is none.
// The banner format: "[enter] open · [esc] later  →  <title>"
func (m inboxModel) ActionRequiredBanner() string {
	n := m.ActionRequiredNotification()
	if n == nil {
		return ""
	}
	bannerStyle := lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	keybind := lipgloss.NewStyle().Foreground(colorMuted).Render("[enter] open · [esc] later")
	arrow := lipgloss.NewStyle().Foreground(colorWarning).Render("  →  ")
	title := bannerStyle.Render(truncate(n.Title, 60))
	return keybind + arrow + title
}

// inboxBadgeStr formats the "●●○ 2 / 5" summary string.
func inboxBadgeStr(unread, total int) string {
	if total == 0 {
		return "empty"
	}
	// Dots: filled for unread, hollow for read.
	dots := strings.Repeat("●", unread) + strings.Repeat("○", max(0, total-unread))
	if len(dots) > 5 {
		dots = dots[:5] + "…"
	}
	return fmt.Sprintf("%s %d / %d", dots, unread, total)
}

// severityGlyph returns the display glyph and its lipgloss style for a severity.
func severityGlyph(sev jobs.NotificationSeverity) (string, lipgloss.Style) {
	switch sev {
	case jobs.SeveritySuccess:
		return "✓", lipgloss.NewStyle().Foreground(colorAccent) // green
	case jobs.SeverityError:
		return "✗", lipgloss.NewStyle().Foreground(colorError) // red
	case jobs.SeverityWarn:
		return "⚠", lipgloss.NewStyle().Foreground(colorWarning) // amber
	case jobs.SeverityActionRequired:
		return "⋯", lipgloss.NewStyle().Foreground(colorWarning) // amber/orange
	case jobs.SeverityInfo:
		return "ℹ", lipgloss.NewStyle().Foreground(colorPrimary) // blue-violet
	default:
		return "·", lipgloss.NewStyle().Foreground(colorMuted)
	}
}

// humanizeDuration formats a duration as a short human-readable string
// such as "3 m ago", "2 h ago", "just now".
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d m ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
}

// truncate clips s to maxLen runes, appending "…" if trimmed.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

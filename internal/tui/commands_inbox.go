package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	inboxmodel "kitsoki/internal/inbox"
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
func renderInboxBlock(m RootModel, args []string) (RootModel, string, tea.Cmd) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if m.jobStore == nil {
		return m, r.SlashOutput("(inbox: no job store wired — running in tests or headless)"), nil
	}

	notifications := m.lastNotifications

	if len(args) == 1 {
		if strings.EqualFold(args[0], "all") {
			return m, renderInboxList(r, notifications, false), nil
		}
		if n, err := strconv.Atoi(args[0]); err == nil {
			return openInboxItem(m, r, notifications, n)
		}
	}
	if len(args) >= 1 && strings.EqualFold(args[0], "sync-github") {
		next, block := syncGitHubInbox(m, r, args[1:])
		return next, block, nil
	}

	// Default: unread only.
	return m, renderInboxList(r, notifications, true), nil
}

func syncGitHubInbox(m RootModel, r *blocks.Renderer, args []string) (RootModel, string) {
	repo := ""
	if len(args) > 0 {
		repo = strings.TrimSpace(args[0])
	}
	ctx := context.Background()
	result, err := syncGitHubInboxNotifications(ctx, m.jobStore, m.sid, repo)
	if err != nil {
		return m, r.SlashOutput("(inbox sync-github: " + err.Error() + ")")
	}

	ns, err := m.jobStore.ListNotifications(ctx, m.sid, 20)
	if err != nil {
		return m, r.SlashOutput("(inbox sync-github: refresh notifications: " + err.Error() + ")")
	}
	m.lastNotifications = ns
	m.inbox, _ = m.inbox.Update(inboxRefreshed{notifications: ns})

	var sb strings.Builder
	sb.WriteString(r.SlashOutput(fmt.Sprintf("  github sync: fetched %d, inserted %d, skipped %d", result.Fetched, result.Inserted, result.Skipped)))
	if list := renderInboxList(r, ns, true); list != "" {
		sb.WriteString("\n")
		sb.WriteString(list)
	}
	return m, strings.TrimRight(sb.String(), "\n")
}

type githubInboxSyncResult struct {
	Fetched  int
	Inserted int
	Skipped  int
}

func syncGitHubInboxNotifications(ctx context.Context, store *jobs.JobStore, sid app.SessionID, repo string) (githubInboxSyncResult, error) {
	items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo:          repo,
		IncludeIssues: true,
		IncludePRs:    true,
		Limit:         100,
	})
	if err != nil {
		return githubInboxSyncResult{}, err
	}

	result := githubInboxSyncResult{Fetched: len(items)}
	for _, item := range items {
		n := inboxmodel.NewGitHubNotification(sid, repo, "inbox", item)
		ok, err := store.InsertExternalNotificationOnce(ctx, n)
		if err != nil {
			return githubInboxSyncResult{}, fmt.Errorf("insert %s #%s: %w", item.Kind, item.Number, err)
		}
		if ok {
			result.Inserted++
		} else {
			result.Skipped++
		}
	}
	return result, nil
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
		if hint := inboxNotificationHint(n); hint != "" {
			sb.WriteString("     ")
			sb.WriteString(hint)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func openInboxItem(m RootModel, r *blocks.Renderer, notifs []jobs.Notification, n int) (RootModel, string, tea.Cmd) {
	visible := unreadNotifications(notifs)
	if n < 1 || n > len(visible) {
		return m, r.SlashOutput("(inbox: index out of range)"), nil
	}
	notif := visible[n-1]
	updated, cmd := m.handleInboxItemSelected(inboxItemSelected{notification: notif})
	next, ok := updated.(RootModel)
	if !ok {
		next = m
	}
	return next, r.SlashOutput(fmt.Sprintf("(inbox: opening %s)", notif.Title)), cmd
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

func unreadNotifications(notifs []jobs.Notification) []jobs.Notification {
	out := make([]jobs.Notification, 0, len(notifs))
	for _, n := range notifs {
		if n.ReadAt == nil {
			out = append(out, n)
		}
	}
	return out
}

func inboxNotificationHint(n jobs.Notification) string {
	body := strings.TrimSpace(n.Body)
	if body != "" {
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			if text := strings.TrimSpace(line); text != "" {
				body = text
				break
			}
		}
	}
	if len(body) > 120 {
		body = strings.TrimSpace(body[:117]) + "..."
	}
	url := strings.TrimSpace(n.OriginURL)
	switch {
	case body != "" && url != "" && !strings.Contains(body, url):
		return body + " - " + url
	case body != "":
		return body
	default:
		return url
	}
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

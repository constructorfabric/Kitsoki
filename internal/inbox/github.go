package inbox

import (
	"fmt"
	"net/url"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// NewGitHubNotification converts a GitHub inbox item into the notification
// shape used by Kitsoki inbox surfaces.
func NewGitHubNotification(sid app.SessionID, repo, teleportState string, item host.GitHubInboxItem) *jobs.Notification {
	title := fmt.Sprintf("GitHub %s #%s", item.Kind, item.Number)
	body := item.Title
	slots := map[string]any{}
	switch item.Kind {
	case "pr":
		title = fmt.Sprintf("PR #%s needs review: %s", item.Number, item.Title)
		slots["pr_id"] = item.Number
		slots["pr_title"] = item.Title
		slots["pr_author"] = item.Author
	case "issue":
		title = fmt.Sprintf("Issue #%s assigned: %s", item.Number, item.Title)
		slots["ticket_id"] = item.Number
		slots["ticket_title"] = item.Title
		slots["ticket_author"] = item.Author
	}
	if item.URL != "" {
		body = strings.TrimSpace(body + "\n\n" + item.URL)
	}
	return &jobs.Notification{
		SessionID:     sid,
		Severity:      jobs.SeverityActionRequired,
		Title:         title,
		Body:          body,
		TeleportState: teleportState,
		TeleportSlots: slots,
		OriginKind:    "external",
		OriginRef:     GitHubOriginRef(repo, item),
		OriginURL:     item.URL,
	}
}

// GitHubOriginRef returns the stable dedupe key for a GitHub inbox item.
func GitHubOriginRef(repo string, item host.GitHubInboxItem) string {
	base := "github:"
	if repo = strings.TrimSpace(repo); repo == "" {
		repo = githubRepoFromURL(item.URL)
	}
	if repo != "" {
		base += repo + "/"
	}
	return fmt.Sprintf("%s%s/%s", base, item.Kind, item.Number)
}

func githubRepoFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

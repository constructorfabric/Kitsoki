package ghagent

import (
	"strings"

	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
)

// DefaultMentionTrigger is the literal scanned in the issue/PR title (and, as an
// extension point, body) to accept a request.
const DefaultMentionTrigger = "@kitsoki"

// Mention is an accepted @kitsoki request derived from a GitHubInboxItem.
type Mention struct {
	Item      host.GitHubInboxItem // the source object (issue|pr)
	Repo      string
	OriginRef string // inbox.GitHubOriginRef(repo, item) — the natural key
	Trigger   string
}

// FilterMentions keeps only the items whose Title contains trigger (the round-1
// producer carries the title; a body sniff is a no-op extension point). repo is
// the owner/repo slug used to compute each OriginRef dedupe key. An empty
// trigger falls back to DefaultMentionTrigger.
func FilterMentions(items []host.GitHubInboxItem, repo, trigger string) []Mention {
	if strings.TrimSpace(trigger) == "" {
		trigger = DefaultMentionTrigger
	}
	var out []Mention
	for _, it := range items {
		if !strings.Contains(strings.ToLower(it.Title), strings.ToLower(trigger)) {
			continue
		}
		out = append(out, Mention{
			Item:      it,
			Repo:      repo,
			OriginRef: inbox.GitHubOriginRef(repo, it),
			Trigger:   trigger,
		})
	}
	return out
}

package ghagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
)

// BugReportEvent is the slice of a GitHub `issues` webhook the deck trigger
// cares about.
type BugReportEvent struct {
	Repo    string
	Number  string
	Title   string
	Body    string
	URL     string // issue html_url
	FiledBy string // from the issue's ```kitsoki metadata block, when present
}

// deckTriggerActions are the issue actions that may warrant a deck. We act on a
// freshly-filed issue (opened), a re-surfaced one (reopened), or one that just
// gained a label (labeled) — never on edits/comments/closes.
var deckTriggerActions = map[string]bool{"opened": true, "reopened": true, "labeled": true}

// ParseIssueOpenedBugReport extracts a [BugReportEvent] from a GitHub `issues`
// webhook payload. ok is true only for a non-PR issue with a deck-worthy action
// ([deckTriggerActions]). Whether the agent actually has deckable evidence for
// the issue is decided later by [DeckTrigger.Handle] against its EvidenceStore —
// that on-agent evidence is the real "this is a kitsoki bug report" signal, so
// this parser stays permissive and cheap.
func ParseIssueOpenedBugReport(payload []byte, fallbackRepo string) (BugReportEvent, bool, error) {
	var p struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue struct {
			Number      int       `json:"number"`
			Title       string    `json:"title"`
			HTMLURL     string    `json:"html_url"`
			Body        string    `json:"body"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return BugReportEvent{}, false, fmt.Errorf("parse issues webhook: %w", err)
	}
	if !deckTriggerActions[p.Action] {
		return BugReportEvent{}, false, nil
	}
	if p.Issue.Number == 0 || p.Issue.PullRequest != nil {
		return BugReportEvent{}, false, nil // not an issue we can deck
	}
	repo := strings.TrimSpace(p.Repository.FullName)
	if repo == "" {
		repo = strings.TrimSpace(fallbackRepo)
	}
	if repo == "" {
		return BugReportEvent{}, false, nil
	}
	ev := BugReportEvent{
		Repo:   repo,
		Number: fmt.Sprintf("%d", p.Issue.Number),
		Title:  strings.TrimSpace(p.Issue.Title),
		Body:   p.Issue.Body,
		URL:    p.Issue.HTMLURL,
	}
	// Best-effort: surface filed_by from the ```kitsoki metadata block the bug
	// filer writes, for logging/framing.
	if meta := host.GHParseMetadata(p.Issue.Body); meta != nil {
		if fb, _ := meta["filed_by"].(string); strings.TrimSpace(fb) != "" {
			ev.FiledBy = strings.TrimSpace(fb)
		}
	}
	return ev, true, nil
}

// DeckTrigger reacts to a filed bug-report issue by producing + hosting the
// no-LLM deck, but ONLY when the agent already holds that issue's evidence (so
// it never reaches out to GitHub for assets). It builds a fresh [DeckReactor]
// per event so it is safe to call concurrently across repos.
type DeckTrigger struct {
	Evidence      *EvidenceStore
	Store         *DeckStore
	Renderer      bugdeck.Renderer
	Ticket        host.Handler
	PublicBaseURL string
	// FallbackRepo is used when a payload omits repository.full_name.
	FallbackRepo string
}

// Handle runs the deck pipeline for an `issues` webhook payload. It returns
// (deckURL, handled, err):
//   - handled=false, err=nil  → not applicable (wrong action, a PR, or the agent
//     holds no evidence for this issue yet, or the deck already exists).
//   - handled=true            → a deck was produced + hosted (and commented if a
//     Ticket handler is set); err reports any comment failure.
func (t *DeckTrigger) Handle(ctx context.Context, payload []byte) (string, bool, error) {
	if t.Store == nil || t.Evidence == nil || t.Renderer == nil {
		return "", false, fmt.Errorf("deck trigger: not fully configured")
	}
	ev, ok, err := ParseIssueOpenedBugReport(payload, t.FallbackRepo)
	if err != nil || !ok {
		return "", false, err
	}
	id := DeckID(ev.Repo, ev.Number)

	// Idempotency: a GitHub redelivery (or a reopened/labeled echo) must not
	// re-post the deck comment.
	if t.Store.Exists(id) {
		return "", false, nil
	}

	// The real gate: only act when this issue's evidence already lives on the
	// agent. Missing evidence → not a deckable bug report (yet); skip quietly.
	evidence, err := t.Evidence.Load(id)
	if err != nil {
		return "", false, nil
	}
	evidence.Title = ev.Title
	evidence.IssueNumber = ev.Number
	evidence.IssueURL = ev.URL
	if strings.TrimSpace(evidence.IssueURL) == "" {
		evidence.IssueURL = fmt.Sprintf("https://github.com/%s/issues/%s", ev.Repo, ev.Number)
	}

	reactor := &DeckReactor{
		Ticket:        t.Ticket,
		Renderer:      t.Renderer,
		Store:         t.Store,
		Repo:          ev.Repo,
		PublicBaseURL: t.PublicBaseURL,
	}
	url, err := reactor.React(ctx, ev.Number, evidence)
	return url, true, err
}

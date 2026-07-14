package ghagent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
)

// DeckReactor turns a filed bug report's on-agent evidence into a hosted,
// in-browser-viewable slidey deck and comments the link back on the issue.
//
// It is download-free: the rrweb + HAR assets already live on the agent (see
// [EvidenceStore]), so React takes the evidence bytes directly. The whole path
// is deterministic and LLM-free — that fact is stated in the posted comment.
type DeckReactor struct {
	// Ticket is the host.gh.ticket seam (op=comment). Default in prod:
	// host.GitHubTicketHandler; a cassette dispatcher in tests.
	Ticket host.Handler
	// Renderer renders the slidey spec to a self-contained HTML deck.
	Renderer bugdeck.Renderer
	// Store hosts the rendered deck on the agent.
	Store *DeckStore
	// Repo is the owner/repo the issue lives on (for the deck id + comment).
	Repo string
	// PublicBaseURL is the agent's externally-reachable base, e.g.
	// https://agent.example.com — the deck link is <base>/decks/<id>.
	PublicBaseURL string
}

// React produces + hosts the deck for issue number n using ev, posts the link
// comment, and returns the public deck URL. ev is read from the agent's evidence
// store by the caller; React performs no GitHub asset download.
func (d *DeckReactor) React(ctx context.Context, n string, ev bugdeck.Evidence) (deckURL string, err error) {
	if d.Store == nil {
		return "", fmt.Errorf("deck reactor: nil Store")
	}
	if d.Renderer == nil {
		return "", fmt.Errorf("deck reactor: nil Renderer")
	}
	n = strings.TrimSpace(n)
	if n == "" {
		return "", fmt.Errorf("deck reactor: issue number is required")
	}

	work, err := os.MkdirTemp("", "kitsoki-bugdeck-")
	if err != nil {
		return "", fmt.Errorf("deck reactor: workdir: %w", err)
	}
	defer os.RemoveAll(work)

	htmlPath, err := bugdeck.Produce(ctx, ev, d.Renderer, work)
	if err != nil {
		return "", err
	}

	id := DeckID(d.Repo, n)
	if _, err := d.Store.Put(id, htmlPath); err != nil {
		return "", err
	}
	deckURL = d.deckURL(id)

	if d.Ticket != nil {
		if err := d.postComment(ctx, n, deckURL); err != nil {
			// The deck is hosted; a comment failure shouldn't lose that. Surface it
			// but still return the URL so the caller can report/log it.
			return deckURL, fmt.Errorf("deck hosted at %s but comment failed: %w", deckURL, err)
		}
	}
	return deckURL, nil
}

// postComment posts the no-LLM deck-link comment on issue n.
func (d *DeckReactor) postComment(ctx context.Context, n, deckURL string) error {
	res, err := d.Ticket(ctx, map[string]any{
		"op":   "comment",
		"repo": d.Repo,
		"id":   n,
		"body": DeckComment(deckURL),
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Error) != "" {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}

// deckURL composes the public URL for a hosted deck id.
func (d *DeckReactor) deckURL(id string) string {
	base := strings.TrimRight(strings.TrimSpace(d.PublicBaseURL), "/")
	return base + "/decks/" + SanitizeDeckID(id)
}

// DeckComment is the issue comment body linking the hosted deck. It states
// plainly that no LLM was used, and echoes a ```kitsoki metadata block.
func DeckComment(deckURL string) string {
	prose := strings.Join([]string{
		"🎬 **Session-replay deck** — the rrweb replay plays right in your browser, no download:",
		"",
		deckURL,
		"",
		"Built deterministically on the kitsoki agent from the captured rrweb + HAR evidence. " +
			"**No LLM was used** to produce this deck.",
	}, "\n")
	meta := "```kitsoki\n" +
		"artifact: bug-report-deck\n" +
		"source: rrweb+har\n" +
		"llm_used: false\n" +
		"```"
	return prose + "\n\n" + meta + "\n"
}

package ghagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/host"
)

// CommentStore posts and edits the rolling-status/ack comment on a GitHub thread
// via host.gh.ticket. Exec is the host.Handler bound to host.gh.ticket (the DI
// seam — a cassette dispatcher in tests, host.GitHubTicketHandler in prod).
type CommentStore struct {
	Exec              host.Handler
	Repo              string
	MaxUpdateAttempts int
	RetryDelay        time.Duration
}

// Meta is the fenced ```kitsoki block payload echoed in every status comment.
// Its field tags mirror the key names ghParseMetadata recovers.
type Meta struct {
	JobID     string `json:"job_id"`
	OriginRef string `json:"origin_ref"`
	Story     string `json:"story"`
	State     string `json:"state"`
	RunURL    string `json:"run_url,omitempty"`
}

// RenderMeta renders the fenced ```kitsoki metadata block as `key: value` lines,
// matching host.ghAppendMetadata's convention so host.ghParseMetadata
// round-trips it.
func RenderMeta(m Meta) string {
	type kv struct{ k, v string }
	fields := []kv{
		{"job_id", m.JobID},
		{"origin_ref", m.OriginRef},
		{"story", m.Story},
		{"state", m.State},
		{"run_url", m.RunURL},
	}
	var lines []string
	for _, f := range fields {
		if strings.TrimSpace(f.v) != "" {
			lines = append(lines, f.k+": "+f.v)
		}
	}
	return "```kitsoki\n" + strings.Join(lines, "\n") + "\n```"
}

// renderBody composes the prose plus the fenced metadata block.
func renderBody(prose string, meta Meta) string {
	prose = strings.TrimRight(prose, "\n")
	if prose == "" {
		return RenderMeta(meta)
	}
	return prose + "\n\n" + RenderMeta(meta) + "\n"
}

// Post creates the FIRST status comment (op=comment) and returns the comment id
// captured from the host.gh.ticket result's data.comment_id. body carries the
// prose; the fenced metadata block is appended automatically.
func (c *CommentStore) Post(ctx context.Context, issueID, body string, meta Meta) (string, error) {
	rendered := renderBody(body, meta)
	if existingID := c.findExisting(ctx, issueID, meta); existingID != "" {
		return c.Update(ctx, issueID, existingID, body, meta)
	}
	res, err := c.Exec(ctx, map[string]any{
		"op":   "comment",
		"id":   issueID,
		"repo": c.Repo,
		"body": rendered,
	})
	if err != nil {
		return "", fmt.Errorf("ghagent: post comment: %w", err)
	}
	if res.Error != "" {
		return "", fmt.Errorf("ghagent: post comment: %s", res.Error)
	}
	commentID, _ := res.Data["comment_id"].(string)
	return commentID, nil
}

func (c *CommentStore) findExisting(ctx context.Context, issueID string, meta Meta) string {
	if strings.TrimSpace(meta.JobID) == "" && strings.TrimSpace(meta.OriginRef) == "" {
		return ""
	}
	res, err := c.Exec(ctx, map[string]any{
		"op":   "get",
		"id":   issueID,
		"repo": c.Repo,
	})
	if err != nil || res.Error != "" {
		return ""
	}
	comments, _ := res.Data["comments"].([]any)
	for _, raw := range comments {
		comment, _ := raw.(map[string]any)
		body, _ := comment["body"].(string)
		if !metaMatches(host.GHParseMetadata(body), meta) {
			continue
		}
		for _, key := range []string{"html_url", "url", "comment_id", "id"} {
			if v, ok := comment[key]; ok {
				if id := strings.TrimSpace(fmt.Sprint(v)); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

func metaMatches(got map[string]any, want Meta) bool {
	if got == nil {
		return false
	}
	if strings.TrimSpace(want.JobID) != "" && strings.TrimSpace(fmt.Sprint(got["job_id"])) == want.JobID {
		return true
	}
	if strings.TrimSpace(want.OriginRef) != "" && strings.TrimSpace(fmt.Sprint(got["origin_ref"])) == want.OriginRef {
		return true
	}
	return false
}

// Update edits the existing status comment in place. It deliberately does not
// fall back to posting a new comment: production duplicate control is more
// important than noisy progress spam when GitHub edit calls flap.
func (c *CommentStore) Update(ctx context.Context, issueID, commentID, body string, meta Meta) (string, error) {
	rendered := renderBody(body, meta)
	if strings.TrimSpace(commentID) == "" {
		return commentID, fmt.Errorf("ghagent: update comment: comment_id is required for issue %s", issueID)
	}
	attempts := c.MaxUpdateAttempts
	if attempts <= 0 {
		attempts = 3
	}
	delay := c.RetryDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		res, err := c.Exec(ctx, map[string]any{
			"op":         "comment_edit",
			"comment_id": commentID,
			"repo":       c.Repo,
			"body":       rendered,
		})
		if err == nil && res.Error == "" {
			if nextID, _ := res.Data["comment_id"].(string); strings.TrimSpace(nextID) != "" {
				return nextID, nil
			}
			return commentID, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s", res.Error)
		}
		if attempt < attempts {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return commentID, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return commentID, fmt.Errorf("ghagent: update comment after %d attempt(s): %w", attempts, lastErr)
}

package ghagent

// routingfeedback.go — the gh-agent surface of WS-C C4 (the routing-
// dissatisfaction substrate). @kitsoki's label->story dispatch (router.go
// Classify) is a routing decision exactly like the TUI/web free-text tiers,
// just resolved from issue/PR labels+title instead of a phrase. A thumbs-down
// (👎) REACTION left on the agent's ack comment (comment.go's CommentStore)
// is the operator's dissatisfaction signal here — GitHub has no free-text
// input to react to, so the reaction on the ack IS the verdict.
//
// This journals through the SAME event shape the TUI's `/route up|down` and
// the web chat's thumbs control write (journal.KindRoutingFeedback /
// journal.RoutingFeedbackEvent) — no second schema — but appends it directly
// via an injected journal.Writer rather than through
// Orchestrator.RecordRoutingFeedback: a dispatched gh-agent job has no live
// Orchestrator/session bound to it by the time an operator reacts (the run
// may have long since completed and the process exited), so there is no
// orchestrator instance to call. The caller supplies whatever journal.Writer
// its job store already has open — wiring that writer into the production
// poll loop is a follow-up (see remainder).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
)

// ReadAckReactionVerdict reads the reactions left on a GitHub comment (the
// gh-agent's ack) via host.gh.ticket (op=comment_reactions) and reports the
// routing-feedback verdict they carry: "down" when a 👎 (thumbsdown) reaction
// is present (checked first — dissatisfaction always wins over a simultaneous
// 👍), "up" when only a 👍 (thumbsup) is present, and ok=false when neither
// reaction is present (nothing to record yet).
func ReadAckReactionVerdict(ctx context.Context, ticket host.Handler, repo, commentID string) (verdict string, ok bool, err error) {
	if ticket == nil {
		return "", false, fmt.Errorf("ghagent: ReadAckReactionVerdict: nil ticket handler")
	}
	if strings.TrimSpace(commentID) == "" {
		return "", false, fmt.Errorf("ghagent: ReadAckReactionVerdict: commentID is required")
	}
	res, err := ticket(ctx, map[string]any{
		"op":         "comment_reactions",
		"repo":       repo,
		"comment_id": commentID,
	})
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(res.Error) != "" {
		return "", false, fmt.Errorf("%s", res.Error)
	}
	if down, _ := res.Data["has_thumbsdown"].(bool); down {
		return "down", true, nil
	}
	if up, _ := res.Data["has_thumbsup"].(bool); up {
		return "up", true, nil
	}
	return "", false, nil
}

// RecordAckReactionFeedback checks job's ack comment for a routing-feedback
// reaction and, if one is present, journals it through w — phrase is the
// routed issue/comment text (the caller's best recovery of what was routed;
// the issue title is the natural choice), and Intent carries job.Story (the
// route this mention dispatched to; there is no orchestrator intent name for
// a label-routed GitHub mention). Tier is always "label-router" — the
// classification tier this surface uses (router.go's label/title Classify),
// distinct from the TUI/web's semantic/deterministic/LLM tiers. Returns
// recorded=false (no error) when the ack carries neither reaction yet, so a
// polling caller can simply re-check later without treating "no verdict yet"
// as a failure.
func RecordAckReactionFeedback(ctx context.Context, ticket host.Handler, w journal.Writer, job jobs.GHJob, phrase string) (recorded bool, err error) {
	if w == nil {
		return false, fmt.Errorf("ghagent: RecordAckReactionFeedback: nil journal.Writer")
	}
	if strings.TrimSpace(job.CommentID) == "" {
		return false, fmt.Errorf("ghagent: RecordAckReactionFeedback: job %q has no ack comment_id", job.JobID)
	}
	verdict, ok, err := ReadAckReactionVerdict(ctx, ticket, job.Repo, job.CommentID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	body := journal.RoutingFeedbackEvent{
		Phrase:  phrase,
		State:   job.Repo + "#" + job.ObjectNumber,
		Intent:  job.Story,
		Tier:    "label-router",
		Verdict: verdict,
	}
	entry := journal.Entry{
		Ts:      time.Now(),
		Session: app.SessionID(job.JobID),
		Kind:    journal.KindRoutingFeedback,
		Body:    mustMarshalRoutingFeedback(body),
	}
	if err := w.Append(entry); err != nil {
		return false, fmt.Errorf("ghagent: RecordAckReactionFeedback: journal append: %w", err)
	}
	return true, nil
}

func mustMarshalRoutingFeedback(b journal.RoutingFeedbackEvent) []byte {
	raw, err := json.Marshal(b)
	if err != nil {
		// RoutingFeedbackEvent is a plain string-field struct — Marshal cannot
		// fail on it. Panicking here would be worse than swallowing a
		// theoretical error, so fall back to an empty body rather than lose
		// the whole feedback record to a marshal edge case.
		return []byte("{}")
	}
	return raw
}

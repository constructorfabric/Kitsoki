// Package orchestrator — routing-feedback recording (WS-C C4: the
// routing-dissatisfaction substrate; see docs/testing/routing-tuning.md and
// .context/dev-workflows-surface-matrix-plan.md WS-C).
//
// RecordRoutingFeedback is the one runtime hook every surface's affordance
// (TUI keybinding/command today; web chat control, VS Code, gh-agent 👎
// reaction — all deferred) calls to journal an operator's up/down verdict on
// a routed turn's outcome. It never touches world or machine state: like
// KindTimeoutArmed/KindTimeoutCancelled (timeout.go), this is a standalone,
// out-of-band journal write plus a trace breadcrumb, mined later by
// routing-tuning (WS-C C2) to prioritize which phrasings need better
// examples/synonyms — the tuning loop this substrate feeds is "minimize
// negative feedback over time," not a static misroute floor.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/trace"
)

// RoutingFeedbackVerdict is the operator's up/down signal on a routed turn.
type RoutingFeedbackVerdict string

const (
	// RoutingFeedbackUp records "this route was correct."
	RoutingFeedbackUp RoutingFeedbackVerdict = "up"
	// RoutingFeedbackDown records "this route was wrong" (dissatisfaction).
	RoutingFeedbackDown RoutingFeedbackVerdict = "down"
)

// RecordRoutingFeedback journals an operator verdict on a previously-routed
// turn. phrase is the original free-text input; state is the state path the
// turn routed FROM; intent is the resolved intent name; tier is the routing
// tier that resolved it ("deterministic" | "semantic" | "local-llm" |
// "main-llm" | "" when unknown) — all four are supplied by the CALLING
// surface, which already rendered them for that turn (see the TUI's
// lastRouted* snapshot in internal/tui/tui.go); RecordRoutingFeedback does
// not look up session history itself.
//
// Returns an error for an unrecognised verdict (never "up" or "down") or
// when state/intent are empty (nothing to attach the feedback to — the
// caller should decline to offer the affordance rather than journal a
// dangling verdict). A nil journalWriter (tests without one wired) makes
// this a silent no-op, matching every other standalone journal helper.
func (o *Orchestrator) RecordRoutingFeedback(ctx context.Context, sid app.SessionID, statePath app.StatePath, intent, phrase, tier string, verdict RoutingFeedbackVerdict) error {
	if verdict != RoutingFeedbackUp && verdict != RoutingFeedbackDown {
		return fmt.Errorf("orchestrator: RecordRoutingFeedback: verdict must be %q or %q, got %q", RoutingFeedbackUp, RoutingFeedbackDown, verdict)
	}
	if strings.TrimSpace(string(statePath)) == "" || strings.TrimSpace(intent) == "" {
		return fmt.Errorf("orchestrator: RecordRoutingFeedback: no routed turn to attach feedback to (state=%q intent=%q)", statePath, intent)
	}

	body := journal.RoutingFeedbackEvent{
		Phrase:  phrase,
		State:   string(statePath),
		Intent:  intent,
		Tier:    tier,
		Verdict: string(verdict),
	}
	o.appendJournal(journalEntry(sid, 0, 0, time.Now(), journal.KindRoutingFeedback, "", body))

	o.logger.InfoContext(ctx, trace.EvTurnRoutingFeedback,
		slog.String("session_id", string(sid)),
		slog.String("state", string(statePath)),
		slog.String("intent", intent),
		slog.String("tier", tier),
		slog.String("verdict", string(verdict)),
	)
	return nil
}

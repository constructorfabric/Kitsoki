package inbound

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"kitsoki/internal/transport"
)

// Reply is one inbound message read from an external thread (a Jira comment, a
// Bitbucket PR comment). ID is the transport-native identifier the bridge uses
// to de-duplicate across polls — it must be stable and unique within the thread.
type Reply struct {
	// ID is the stable, thread-unique identifier (e.g. the Jira comment id).
	ID string
	// Author is the external author handle (e.g. a Jira account id / name).
	Author string
	// Body is the raw reply text, including any leading BotMarker the poster
	// added. The bridge strips/filters the marker itself.
	Body string
}

// Source reads new inbound replies for one external (transport, thread). It is
// the read counterpart to transport.Transport.Post. Concrete implementations
// (Jira, Bitbucket) call the respective REST API; the package ships only this
// interface and a fake (see fake_source_test.go) so the bridge can be tested
// without a network. Implementations need not de-duplicate or filter — the
// [Bridge] handles BotMarker self-filtering, author filtering, and dedup.
type Source interface {
	// Key identifies the thread this source reads, for logging / correlation.
	Key() transport.SessionKey
	// Poll returns replies not seen on a prior poll. Implementations may return
	// the full thread every time (the bridge dedups by Reply.ID) or track their
	// own cursor — either is correct.
	Poll(ctx context.Context) ([]Reply, error)
}

// Classification is the structured result of interpreting a reply: the intent to
// submit and the slots that accompany it. The author is added by the bridge, not
// the classifier.
type Classification struct {
	// Intent is the resolved intent name (e.g. "continue", "refine").
	Intent string
	// Slots are the intent's slots (e.g. {refine_feedback: "..."}).
	Slots map[string]any
}

// Classifier turns a reply body into a [Classification]. ok is false when the
// reply matches no intent and should be skipped (e.g. idle chatter on the
// ticket). A classifier MUST NOT mutate shared state — the bridge may call it
// concurrently in future.
type Classifier interface {
	Classify(body string) (Classification, bool)
}

// Driver advances one turn against the session the bridge drives. The cmd layer
// implements it by binding the persisted orchestrator + session id and wrapping
// the call in the per-session writer lock, so a bridge turn serialises with a
// concurrent browser turn on the same session. author is the resolved external
// author, injected as slots.author so the story records a real principal — the
// same identity contract the web Driver honours.
type Driver interface {
	SubmitIntent(ctx context.Context, intent string, slots map[string]any, author string) error
}

// Bridge ties a [Source], a [Classifier], and a [Driver] together with the
// inbound filters. It is the only inbound code path; transports stay
// output-only.
type Bridge struct {
	Source     Source
	Classifier Classifier
	Driver     Driver

	// BotMarker is the prefix the bridge's own posts carry, so a reply that is
	// actually kitsoki's echoed output is skipped. Defaults to
	// transport.DefaultBotMarker.
	BotMarker string

	// AllowAuthors, when non-empty, is the allow-list of external authors whose
	// replies drive the session. A reply from any other author is skipped.
	// Empty means "accept any author".
	AllowAuthors []string

	// seen tracks Reply.IDs already processed, so a Source that returns the full
	// thread on every poll does not re-drive old replies.
	seen map[string]struct{}
}

// PollOnce performs one poll cycle: read new replies, filter (BotMarker, author,
// dedup), classify, and drive a turn per surviving reply. It returns the number
// of replies that advanced the session and the first driver error encountered
// (later replies in the same cycle are still attempted; this is best-effort so
// one bad reply does not stall the thread). It is the unit a Run loop calls on a
// timer; exposing it directly keeps the bridge testable with no goroutines.
func (b *Bridge) PollOnce(ctx context.Context) (int, error) {
	if b.seen == nil {
		b.seen = map[string]struct{}{}
	}
	marker := b.BotMarker
	if marker == "" {
		marker = transport.DefaultBotMarker
	}

	replies, err := b.Source.Poll(ctx)
	if err != nil {
		return 0, fmt.Errorf("inbound: poll %s: %w", keyString(b.Source.Key()), err)
	}

	driven := 0
	var firstErr error
	for _, r := range replies {
		if r.ID != "" {
			if _, ok := b.seen[r.ID]; ok {
				continue
			}
		}
		// Self-filter: a reply that is kitsoki's own posted output (carries the
		// BotMarker) must never be fed back as a turn — that is the single
		// most-bitten gotcha when wiring an external transport.
		if strings.Contains(r.Body, marker) {
			b.markSeen(r)
			continue
		}
		if !b.authorAllowed(r.Author) {
			b.markSeen(r)
			continue
		}
		class, ok := b.Classifier.Classify(r.Body)
		if !ok {
			// Unclassifiable chatter — record as seen so we don't re-evaluate it,
			// but don't drive a turn.
			b.markSeen(r)
			continue
		}
		if derr := b.Driver.SubmitIntent(ctx, class.Intent, class.Slots, r.Author); derr != nil {
			// Do NOT mark the reply seen on a drive failure: a transient error
			// (e.g. the writer lock is held — EX_TEMPFAIL) must be retried on the
			// next poll, not silently dropped.
			if firstErr == nil {
				firstErr = fmt.Errorf("inbound: drive reply %q as %q: %w", r.ID, class.Intent, derr)
			}
			continue
		}
		b.markSeen(r)
		driven++
	}
	return driven, firstErr
}

// Run polls the source on the given interval until ctx is cancelled, driving a
// turn per surviving reply each cycle. A per-cycle poll/drive error is logged
// (best-effort) and does not stop the loop — a transient failure (network blip,
// writer-lock contention) is retried on the next tick. interval <= 0 falls back
// to 30s. Run blocks; callers run it in a goroutine and cancel ctx to stop it.
func (b *Bridge) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := b.PollOnce(ctx); err != nil {
				slog.Warn("inbound bridge poll cycle failed",
					"thread", keyString(b.Source.Key()), "err", err)
			}
		}
	}
}

func (b *Bridge) markSeen(r Reply) {
	if r.ID == "" {
		return
	}
	b.seen[r.ID] = struct{}{}
}

func (b *Bridge) authorAllowed(author string) bool {
	if len(b.AllowAuthors) == 0 {
		return true
	}
	for _, a := range b.AllowAuthors {
		if a == author {
			return true
		}
	}
	return false
}

func keyString(k transport.SessionKey) string {
	return k.Transport + ":" + k.Thread
}

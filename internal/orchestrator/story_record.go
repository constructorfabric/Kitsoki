package orchestrator

// story_record.go records the effective story into the session's JSONL trace
// so a trace is a self-contained, deterministic replay. See
// internal/store/story.go for the capture/reconstruction helpers and
// store.StorySnapshot / store.StoryChanged for the wire format.
//
// The single entry point is RecordEffectiveStory: idempotent, it writes a base
// snapshot the first time and a diff whenever the effective story's content
// hash drifts (a /reload or /meta edit). It is called at session bootstrap
// (before the first turn), after AttachSession (reconciling disk-vs-trace drift
// across restarts), and after each Reload (the /reload and /meta funnel).
//
// Story events are written ONLY to the JSONL event sink — never to the legacy
// SQLite event log. Self-containment is a property of the JSONL trace (the
// path replay/branching reads), and the JSONL sink continues per-turn seq
// numbering across appends, so a snapshot can ride turn 0 alongside the
// initial on_enter events and a diff can ride the latest turn without the
// one-batch-per-turn seq collision the SQLite store enforces. A session with
// no JSONL sink has no self-contained trace to populate, so recording no-ops.

import (
	"context"
	"fmt"
	"log/slog"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// RecordEffectiveStory ensures the current effective story (o.def) is recorded
// in the session's JSONL trace. It is idempotent and cheap to call repeatedly:
//
//   - No JSONL sink configured → no-op (nothing to make self-contained).
//   - No story recorded yet → append a base store.StorySnapshot at turn 0.
//   - A story is recorded and the content hash differs → append a
//     store.StoryChanged diff at the latest turn.
//   - Hash unchanged → no-op.
//
// It is best-effort with respect to defs that were not produced by app.Load
// (no LoadedManifests — e.g. hand-built test defs): such defs cannot be
// captured, so recording is skipped with a debug log rather than failing the
// session. A genuine sink append failure IS returned, since a trace that
// silently drops the story is no longer self-contained.
//
// Not safe to call concurrently with Turn/Reload for the same session; callers
// invoke it between turns (bootstrap, post-attach, post-reload), the same
// windows Reload itself requires.
func (o *Orchestrator) RecordEffectiveStory(ctx context.Context, sid app.SessionID) error {
	if o.eventSink == nil {
		return nil
	}

	o.mu.Lock()
	def := o.def
	o.mu.Unlock()
	if def == nil {
		return nil
	}

	es, err := store.CollectEffectiveStory(def)
	if err != nil {
		// Def not loaded via app.Load (no manifests on disk to capture).
		// Nothing to record; don't fail the session over it.
		o.logger.DebugContext(ctx, "orchestrator: skipping story snapshot",
			slog.String("session_id", string(sid)),
			slog.String("reason", err.Error()),
		)
		return nil
	}

	history := o.eventSink.History()
	latest := store.LatestStoryHash(history)

	// First story for this session: base snapshot at turn 0. The JSONL sink
	// continues turn-0 seq after it, so a following RunInitialOnEnter slots in
	// cleanly behind the snapshot.
	if latest == "" {
		ev := newOrchestratorEvent(store.StorySnapshot,
			store.StorySnapshotPayload(def.App.ID, es), 0)
		ev.Turn = 0
		if appendErr := o.eventSink.Append(ev); appendErr != nil {
			return fmt.Errorf("orchestrator.RecordEffectiveStory: append snapshot: %w", appendErr)
		}
		return nil
	}

	// Story unchanged since the last recorded snapshot/diff.
	if latest == es.Hash {
		return nil
	}

	// Story changed: diff against the story as it stood at the latest turn,
	// and stamp the diff on that turn (it continues that turn's seq).
	maxTurn := app.TurnNumber(0)
	for _, e := range history {
		if e.Turn > maxTurn {
			maxTurn = e.Turn
		}
	}
	oldFiles, _, err := store.StoryAtTurn(history, maxTurn)
	if err != nil {
		return fmt.Errorf("orchestrator.RecordEffectiveStory: reconstruct previous story: %w", err)
	}
	payload, changed := store.StoryChangedPayload(latest, oldFiles, es)
	if !changed {
		// Hashes differed but the file sets are byte-identical — shouldn't
		// happen (hash is over the same bytes), but stay safe and no-op.
		return nil
	}
	ev := newOrchestratorEvent(store.StoryChanged, payload, maxTurn)
	ev.Turn = maxTurn
	if appendErr := o.eventSink.Append(ev); appendErr != nil {
		return fmt.Errorf("orchestrator.RecordEffectiveStory: append diff: %w", appendErr)
	}
	return nil
}

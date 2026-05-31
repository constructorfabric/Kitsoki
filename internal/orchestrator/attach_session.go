// Package orchestrator — resume read path for continue-mode.
//
// AttachSession is the public entry point for the --continue TUI path.  It
// returns a ResumeBundle that carries everything the TUI needs to reconstruct
// the full session state without calling any harness, host handler, transport,
// or LLM (the resume determinism contract).
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// ResumeBundle is the result of AttachSession: the full rehydration set for a
// resumed TUI session.
//
// Determinism contract: every field in this struct is derived from persisted
// journal or store data — no LLM call, no host handler dispatch, no transport
// lookup, and no time.Now() reads happen during construction.
type ResumeBundle struct {
	// Journey is the reconstructed (state, world, turn) — same shape as
	// LoadJourney returns.  Derived from the events/snapshots tables (phase A
	// authoritative source for state+world).
	Journey *store.JourneyState

	// PendingClarify is the in-flight slot-fill state, or nil if no foreground
	// clarify was outstanding when the session was last active.  Derived from
	// the journal's typed-entry stream (clarify.requested / clarify.answered).
	PendingClarify *PendingClarify

	// TranscriptEntries is the ordered set of journal entries relevant to
	// transcript rehydration: view.rendered, offpath.question, offpath.answer,
	// disambig.presented, disambig.chosen.  Ordered by (turn, seq).
	TranscriptEntries []journal.Entry

	// InitialView is the body.view_text of the most recent view.rendered entry.
	// The TUI's first frame uses this verbatim; no view template is re-evaluated.
	// Empty string if no view.rendered entry exists (e.g. empty session).
	InitialView string

	// AwaitingJobs lists background jobs that were in awaiting_input status
	// when the session was last active.  The TUI surfaces a
	// clarification UI for each on first frame.  Empty when no jobs await input
	// or when no JobStore is wired into the orchestrator.
	AwaitingJobs []AwaitingJob

	// PendingDrives lists chat-input-queue rows in pending or dispatching
	// status whose origin_session_id is this session.  These survive a
	// kitsoki restart in SQLite; resume surfaces them so the TUI can
	// re-dispatch, dismiss, or surface a pending-work indicator.  Empty
	// when no drives are pending or no concrete chats.Store is wired.
	PendingDrives []PendingDrive

	// BackgroundedChats lists chat_pty_sessions rows in pty_background
	// status whose chat is owned by this session.  Means: a chat had a
	// tmux PTY running claude when the session was last active, and the
	// PTY is still alive (or its row still exists — chat_pty_sessions
	// only gets cleaned up by an explicit detach / GC).  Resume can
	// offer to reattach.  Empty when no backgrounded chats or no
	// concrete chats.Store is wired.
	BackgroundedChats []BackgroundedChat
}

// AwaitingJob describes one background job paused waiting for user input.
type AwaitingJob struct {
	JobID  jobs.JobID
	Kind   string // the host handler kind that produced the job
	Schema any    // ClarificationSchema as stored — TUI knows how to render
}

// PendingDrive describes one chat-input-queue row that survived restart in
// pending or dispatching status — work the user (or an upstream orchestrator)
// had enqueued against a chat that the resumed TUI should know about.
type PendingDrive struct {
	DriveID    string
	ChatID     string
	Transport  chats.DriveTransport
	Status     chats.DriveStatus // pending | dispatching
	Payload    string
	ReceivedAt time.Time
}

// BackgroundedChat describes one chat whose tmux PTY was running claude in
// pty_background mode when the session was last active. Resume can offer to
// reattach (`kitsoki chat attach <chat_id>`).
type BackgroundedChat struct {
	ChatID      string
	TmuxSession string
	TmuxHost    string
	LastIdleAt  *time.Time // nil if the PTY never reported idle
}

// PendingClarify is the exported version of the in-memory pendingClarify
// struct, carried in ResumeBundle so the TUI can rehydrate slot-fill mode.
type PendingClarify struct {
	// IntentName is the intent that was being clarified.
	IntentName string
	// SlotsSoFar are the slots already collected before the session was interrupted.
	SlotsSoFar map[string]any
}

// WithJournalReader wires a journal.Reader for the resume read path (symmetric
// to WithJournalWriter).  When nil (the default), AttachSession falls back to
// LoadJourney-only: transcript and clarify rehydration are skipped.  Tests
// that do not need full rehydration can omit this option.
func WithJournalReader(r journal.Reader) Option {
	return func(o *Orchestrator) {
		o.journalReader = r
	}
}

// transcriptKinds is the set of journal entry kinds that are relevant for
// transcript rehydration.  Only these are included in ResumeBundle.TranscriptEntries.
var transcriptKinds = map[string]struct{}{
	journal.KindViewRendered:      {},
	journal.KindOffPathQuestion:   {},
	journal.KindOffPathAnswer:     {},
	journal.KindDisambigPresented: {},
	journal.KindDisambigChosen:    {},
}

// AttachSession rebuilds the full session state from persisted data and
// returns a ResumeBundle the TUI uses to reconstruct the session.
//
// Phase A authoritative sources:
//   - (state, world, turn): events/snapshots tables via LoadJourney
//   - pending clarify: journal typed-entry stream (clarify.requested/answered)
//   - transcript entries: journal typed-entry stream (view.rendered etc.)
//   - initial view: most recent view.rendered body.view_text
//
// Determinism contract: no harness, no host registry dispatch, no transport
// registry, no time.Now() during replay.
func (o *Orchestrator) AttachSession(sid app.SessionID) (*ResumeBundle, error) {
	// ── 1. Reconstruct (state, world, turn) from events/snapshots ─────────────
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator.AttachSession: load journey: %w", err)
	}

	bundle := &ResumeBundle{
		Journey: journey,
	}

	// ── 2. Journal-based enrichment ───────────────────────────────────────────
	// If no journal reader is wired, we return the journey-only bundle.  Tests
	// that don't configure a reader still work; they just lack transcript and
	// clarify rehydration.
	if o.journalReader == nil {
		slog.Debug("orchestrator.AttachSession: no journal reader wired; skipping transcript/clarify rehydration",
			"session_id", string(sid))
		return bundle, nil
	}

	// Walk the typed-entry stream in (turn, seq) order to:
	//   a) find the most recent unmatched clarify.requested (origin=foreground)
	//   b) collect transcript-relevant entries
	var (
		pendingClarifyBody *clarifyRequestedBody
		transcriptEntries  []journal.Entry
		latestViewText     string
	)

	typedSeq, typedErr := o.journalReader.ReplayTyped(sid)
	for e := range typedSeq {
		switch e.Kind {
		case journal.KindClarifyRequested:
			var body clarifyRequestedBody
			if err := json.Unmarshal(e.Body, &body); err != nil {
				slog.Debug("orchestrator.AttachSession: failed to decode clarify.requested body",
					"session_id", string(sid), "turn", e.Turn, "seq", e.Seq, "err", err)
				continue
			}
			if body.Origin == "foreground" {
				pendingClarifyBody = &body
			}

		case journal.KindClarifyAnswered:
			var body struct {
				Origin string `json:"origin"`
			}
			if err := json.Unmarshal(e.Body, &body); err == nil && body.Origin == "foreground" {
				// Answered: clear any outstanding clarify.
				pendingClarifyBody = nil
			}
		}

		// Collect transcript-relevant entries.
		if _, ok := transcriptKinds[e.Kind]; ok {
			transcriptEntries = append(transcriptEntries, e)
			if e.Kind == journal.KindViewRendered {
				var body viewRenderedBody
				if err := json.Unmarshal(e.Body, &body); err == nil {
					latestViewText = body.ViewText
				}
			}
		}
	}
	if err := typedErr(); err != nil {
		// A truncated typed-entry stream means the rehydrated transcript and
		// pending-clarify state are incomplete — surface it rather than
		// resuming the session from a partial replay.
		return nil, fmt.Errorf("orchestrator.AttachSession: replay typed entries: %w", err)
	}

	// ── 3. Populate pending clarify (rehydrates o.pending[sid]) ───────────────
	if pendingClarifyBody != nil {
		o.mu.Lock()
		o.pending[sid] = &pendingClarify{
			intentName: pendingClarifyBody.Intent,
			slots:      pendingClarifyBody.SlotsSoFar,
		}
		o.mu.Unlock()

		bundle.PendingClarify = &PendingClarify{
			IntentName: pendingClarifyBody.Intent,
			SlotsSoFar: pendingClarifyBody.SlotsSoFar,
		}
	}

	// ── 3a. Chat-doc patch entries (chats.append) ────────────────────────────
	// Patch entries live outside ReplayTyped's stream because they target the
	// chats/<id> document.  We pick them up here so the transcript can render
	// the conversation rows the user saw inline with view.rendered.  Sorted
	// into the transcript list by timestamp (chats entries carry Turn=0 today
	// because chats.Store methods don't thread the kitsoki turn — see the Z
	// wave report).  Time-based ordering gives the right user-visible sequence.
	for _, doc := range o.journalReader.ListLiveDocs(sid) {
		if !isChatsDoc(doc) {
			continue
		}
		chatsSeq, chatsErr := o.journalReader.ReplayFrom(sid, doc, 0)
		for e := range chatsSeq {
			if e.Kind == journal.KindChatsAppend {
				transcriptEntries = append(transcriptEntries, e)
			}
		}
		if err := chatsErr(); err != nil {
			return nil, fmt.Errorf("orchestrator.AttachSession: replay chats doc %q: %w", doc, err)
		}
	}
	sortByTs(transcriptEntries)

	bundle.TranscriptEntries = transcriptEntries
	bundle.InitialView = latestViewText

	// ── 4. Background jobs in awaiting_input state ───────────────────────────
	// The jobs table is its own source of truth; status survives restart
	// natively.  AttachSession surfaces awaiting-input jobs so the TUI can
	// open their clarification UI immediately.
	if o.jobStore != nil {
		awaiting, err := o.jobStore.ListJobsByStatus(context.Background(), sid, jobs.JobAwaitingInput)
		if err != nil {
			slog.Debug("orchestrator.AttachSession: ListJobsByStatus failed",
				"session_id", string(sid), "err", err)
		} else {
			for _, j := range awaiting {
				bundle.AwaitingJobs = append(bundle.AwaitingJobs, AwaitingJob{
					JobID:  j.ID,
					Kind:   j.Kind,
					Schema: j.ClarificationSchema,
				})
			}
		}
	}

	// ── 5. Pending chat drives + backgrounded PTY chats ───────────────────────
	// claude-code-sessions adds chat_input_queue (drives) and chat_pty_sessions
	// (tmux-hosted claude). Both survive restart in SQLite. Resume surfaces them
	// so the TUI can re-dispatch / dismiss / reattach.
	if o.chatsConcrete != nil {
		ctx := context.Background()

		// Drives: pending or dispatching, owned by this session.
		drives, err := o.chatsConcrete.ListDrivesBySession(ctx, string(sid),
			[]chats.DriveStatus{chats.DriveStatusPending, chats.DriveStatusDispatching})
		if err != nil {
			slog.Debug("orchestrator.AttachSession: ListDrivesBySession failed",
				"session_id", string(sid), "err", err)
		} else {
			for _, d := range drives {
				bundle.PendingDrives = append(bundle.PendingDrives, PendingDrive{
					DriveID:    d.DriveID,
					ChatID:     d.ChatID,
					Transport:  d.Transport,
					Status:     d.Status,
					Payload:    d.Payload,
					ReceivedAt: d.ReceivedAt,
				})
			}
		}

		// Backgrounded PTY chats: pty_background mode, this host only
		// (ListPTYForHost is already host-scoped). Cross-host PTYs can't
		// be reattached from here so they're filtered out at the source.
		ptyRows, err := o.chatsConcrete.ListPTYForHost(ctx)
		if err != nil {
			slog.Debug("orchestrator.AttachSession: ListPTYForHost failed",
				"session_id", string(sid), "err", err)
		} else {
			for _, p := range ptyRows {
				if p.Mode != chats.PtyModeBackground {
					continue
				}
				// Filter to chats owned by this session.
				ch, err := o.chatsConcrete.Get(ctx, p.ChatID)
				if err != nil || ch == nil || ch.SessionID != string(sid) {
					continue
				}
				bundle.BackgroundedChats = append(bundle.BackgroundedChats, BackgroundedChat{
					ChatID:      p.ChatID,
					TmuxSession: p.TmuxSession,
					TmuxHost:    p.TmuxHost,
					LastIdleAt:  p.LastIdleAt,
				})
			}
		}
	}

	return bundle, nil
}

// clarifyRequestedBody is the JSON shape of a clarify.requested entry body.
type clarifyRequestedBody struct {
	Origin      string         `json:"origin"`
	Intent      string         `json:"intent"`
	SlotsSoFar  map[string]any `json:"slots_so_far"`
	SlotsNeeded []string       `json:"slots_needed"`
}

// viewRenderedBody is the JSON shape of a view.rendered entry body.
type viewRenderedBody struct {
	ViewText  string `json:"view_text"`
	StatePath string `json:"state_path"`
}

// isChatsDoc returns true for DocIDs that name a chats/<id> document.
func isChatsDoc(doc journal.DocID) bool {
	return len(doc) > 6 && doc[:6] == "chats/"
}

// sortByTs sorts a slice of entries by Ts ascending, stable. Used when
// interleaving turn-anchored entries (view.rendered etc.) with chats.append
// entries that today carry Turn=0 because the chats package doesn't thread the
// kitsoki turn into its store methods.
func sortByTs(entries []journal.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Ts.Before(entries[j].Ts)
	})
}

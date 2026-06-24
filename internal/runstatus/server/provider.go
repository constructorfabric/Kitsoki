package server

import (
	"context"
	"encoding/json"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/video"
)

// SessionProvider is the multi-session seam the [Server] dispatches against.
// Where v1 served a single [Source] + [Driver], the provider owns a set of live
// sessions keyed by id and a catalogue of discovered stories, so one server can
// route every RPC to the right session and expose the story browser / session
// lifecycle the SPA home screen needs.
//
// It is the inversion forced by Go's import rules: the concrete registry that
// fulfils this interface lives in package main (it must call
// buildSessionRuntime), so the server cannot import it — it defines the
// interface and the registry depends on it. The read-only `kitsoki status
// serve` surface still works via [singleEntryProvider], a one-session adapter
// whose lifecycle methods report [codeReadOnly].
//
// Implementations MUST be safe for concurrent use: the SSE pollers and the RPC
// handlers call them from many goroutines at once.
type SessionProvider interface {
	// Get resolves a live session by id. ok is false for an unknown id; callers
	// turn that into a structured not-found error rather than a nil-deref.
	Get(sessionID string) (entry Entry, ok bool)
	// List returns a header per live session, for runstatus.sessions.list. This
	// replaces v1's "the one trace's session" answer. It reuses the shared
	// [runstatus.SessionHeader] (snapshot.go) rather than a parallel type.
	List() []runstatus.SessionHeader
	// NewSession starts a fresh session for the story at storyPath and returns
	// its id. It fails fast with a structured error on an invalid story so the
	// UI can surface it before navigating.
	NewSession(ctx context.Context, storyPath string) (sessionID string, err error)
	// Reload re-reads the session's story and swaps it in, mirroring the TUI
	// /reload. prevStateExists is false when the session's current state was
	// removed by the edit (the UI shows the "staying put" warning).
	Reload(ctx context.Context, sessionID string) (prevStateExists bool, err error)
	// Staleness checks whether the session's app.yaml on disk differs from the
	// version that was loaded (or last reloaded). stale is false when the bytes
	// are identical. diff is a unified-diff string suitable for display; empty
	// when stale is false.
	Staleness(ctx context.Context, sessionID string) (stale bool, diff string, err error)
	// ListStories returns the discovered story catalogue with per-story
	// active-session counts.
	ListStories() []StoryHeader
	// Rescan re-walks the configured story dirs and returns the refreshed
	// catalogue, leaving live sessions untouched.
	Rescan() ([]StoryHeader, error)
}

// ExternalAttachProvider is an optional extension of [SessionProvider]: a
// provider that can attach a live session to an EXISTING persisted session
// addressed by an external key (`transport:thread`, e.g. `jira:PLTFRM-12345`),
// or create-and-bind one when the key is unknown. The session is driven against
// the shared persisted store under the session writer lock, so a browser and an
// inbound bridge (or a separate `kitsoki session continue` process) co-drive one
// session without interleaving writes. The server type-asserts this interface
// for the `runstatus.session.attach` RPC; a provider that does not implement it
// reports codeReadOnly for that method (e.g. the single-entry adapter).
type ExternalAttachProvider interface {
	// AttachExternal returns the live session id for the story at storyPath bound
	// to the external key, attaching to the persisted session if one exists and
	// creating+binding it otherwise. It fails fast on an invalid story / key.
	AttachExternal(ctx context.Context, storyPath, key string) (sessionID string, err error)
}

// CurrentSessionProvider is an optional extension of [SessionProvider]: a
// provider that tracks which of its live sessions is "current" — the one most
// recently created (session.new) or attached (session.attach). Trace-only and
// graph-only surfaces, which have no chat to start a session, read this via the
// runstatus.session.current RPC to discover and follow the active session. A
// provider that does not implement it (e.g. the single-entry adapter) makes the
// server fall back to the last value pushed onto the current-session feed.
type CurrentSessionProvider interface {
	// CurrentSession returns the id of the current live session. ok is false when
	// there is no current session (returns a null session_id on the wire).
	CurrentSession() (sessionID string, ok bool)
}

// ArtifactResolver looks up a media artifact by its opaque handle ID and
// returns the absolute path and MIME type of the file on disk.  It is set on
// [Entry] by the live registry for sessions whose orchestrator wires a journal
// writer; the server uses it to serve the `GET /artifact/{id}` route.
// Nil is a safe sentinel: the server returns 404 for that session.
type ArtifactResolver interface {
	// LookupArtifact scans the journal for the named handle ID and returns the
	// absolute file path and MIME type. ok is false when the id is unknown or the
	// journal has no record of it.
	LookupArtifact(id string) (path, mime string, ok bool)
}

// Entry is one live session as seen by the [Server]: its read [Source] and its
// write [Driver]. The provider owns the lifecycle; the server only reads these
// two seams per routed RPC. Driver may be nil for a read-only session (e.g. the
// `status serve` adapter), in which case write RPCs return [codeReadOnly].
//
// Meta is the optional meta-mode seam (the `runstatus.meta.*` overlay chat). It
// is nil for read-only surfaces and for the single-entry adapter, in which case
// meta RPCs return [codeReadOnly]. Only the live multi-session registry stamps
// it, because meta mode needs a chat store the read-only surfaces don't own.
//
// Artifacts is the optional artifact-lookup seam for the `GET /artifact/{id}`
// route. It is nil for read-only surfaces and single-entry adapters.
// Frames is the optional still-recorder seam for the `runstatus.video.frame`
// RPC (slice 2 of the mockup-video-studio epic). Given an absolute PNG path
// freshly produced by [video.Frame], it records the still through the same
// substrate the rest of the run uses and returns the opaque handle the
// `/artifact/{id}` route serves. It is nil for read-only surfaces and the
// single-entry adapter, in which case `video.frame` returns [codeReadOnly].
//
// Feedback is the optional feedback-note sink for `runstatus.feedback.add`.
// It appends one structured note to the session's append-only `feedback.jsonl`
// (and, in a future fast path, dispatches into a live authoring session). It is
// nil for surfaces without a writable feedback sidecar.
type Entry struct {
	Source    Source
	Driver    Driver
	Meta      MetaDriver
	Artifacts ArtifactResolver
	Frames    FrameRecorder
	Feedback  FeedbackSink
	// FrameRunner is the command runner video.frame injects into [video.Frame]
	// for this session. Production leaves it nil → video.Frame shells ffmpeg via
	// its DefaultRunner; a test injects a fixture-copying fake here (per-entry,
	// so parallel video tests never race a shared global).
	FrameRunner video.Runner
}

// FrameRecorder records a still PNG (produced by [video.Frame]) through the
// run's artifact substrate and returns the opaque handle the `/artifact/{id}`
// route resolves. The web RPC never invents a path: it hands the recorder the
// temp PNG that the one ffmpeg extractor wrote, and the recorder owns where it
// lives and how it is journalled — keeping the single-write-site contract.
type FrameRecorder interface {
	// RecordFrame records the PNG at pngPath (an absolute path produced by
	// video.Frame) as a still artifact and returns its handle. label is a
	// human caption (e.g. "frame @ 0:14"). The recorder may move/copy the file
	// under the artifacts root; the caller does not reuse pngPath afterward.
	RecordFrame(pngPath, label string) (handle string, err error)
}

// FeedbackSink persists one structured feedback note. The shape mirrors epic
// shared decision 3: a capture-and-dispatch note, never an edit. The default
// implementation appends to an append-only `feedback.jsonl`; a live authoring
// session may later also drain it.
type FeedbackSink interface {
	// AddFeedback appends one note and returns nothing on success. It must be
	// safe for concurrent use (the server calls it from request goroutines).
	AddFeedback(note FeedbackNote) error
}

// FeedbackNote is the structured feedback-note shape `runstatus.feedback.add`
// persists and dispatches. It is the recorded, source-targeted instruction the
// slice-3 refine step consumes — the web tier captures it, it never edits.
type FeedbackNote struct {
	// VideoHandle is the artifact handle of the reviewed video.
	VideoHandle string `json:"video_handle"`
	// SourceRef resolves the flagged moment back to its producing unit
	// (slidey scene / tour step). Mirrors video.SourceRef's JSON shape; carried
	// opaquely here so the server need not import package video for the note.
	SourceRef map[string]any `json:"source_ref,omitempty"`
	// TimeRange is the flagged [start_ms, end_ms] window (end omitted for a
	// point flag).
	TimeRange map[string]any `json:"time_range,omitempty"`
	// FrameHandle is the captured still's artifact handle (from video.frame).
	FrameHandle string `json:"frame_handle,omitempty"`
	// Instruction is the operator's free-text note.
	Instruction string `json:"instruction"`
	// Ts is the capture time (UTC), set by the server.
	Ts time.Time `json:"ts"`
}

// ── JournalArtifactResolver ───────────────────────────────────────────────────

// JournalArtifactResolver implements [ArtifactResolver] by scanning the typed
// journal entries for session sid, filtering for [journal.KindArtifactEmitted]
// entries, and returning the first whose ID matches the requested handle.
//
// Scanning is O(n) over the session's typed journal entries on every lookup.
// For the PoC this is acceptable: artifact lookups are browser-fetch-triggered,
// rare, and the journal is small. A future optimisation would build an in-memory
// index at start-up and invalidate it on journal append.
type JournalArtifactResolver struct {
	Reader journal.Reader
	SID    app.SessionID
}

// LookupArtifact scans the session's typed journal entries for an
// [journal.ArtifactEvent] whose ID equals handle. Returns (path, mime, true) on
// the first match, or ("", "", false) when not found or on any scan error.
func (r *JournalArtifactResolver) LookupArtifact(handle string) (path, mime string, ok bool) {
	seq, errFn := r.Reader.ReplayTyped(r.SID)
	for entry := range seq {
		if entry.Kind != journal.KindArtifactEmitted {
			continue
		}
		var ev journal.ArtifactEvent
		if err := json.Unmarshal(entry.Body, &ev); err != nil {
			continue
		}
		if ev.ID == handle {
			_ = errFn() // scan complete; ignore trailing error
			return ev.Path, ev.Mime, true
		}
	}
	if err := errFn(); err != nil {
		// scan ended on a DB error — treat as not found
		return "", "", false
	}
	return "", "", false
}

// singleEntryProvider adapts one [Source] (+ optional [Driver]) to the FULL
// [SessionProvider] interface, so the read-only `kitsoki status serve` and the
// legacy single-session surfaces route through the same dispatch path as the
// multi-session server. The session_id param is accepted but ignored: every
// Get resolves to the one entry. The lifecycle methods (NewSession / Reload /
// ListStories / Rescan) have no orchestrator behind them and report a structured
// read-only error rather than nil-derefing.
type singleEntryProvider struct {
	entry Entry
}

func (p *singleEntryProvider) Get(string) (Entry, bool) { return p.entry, true }

func (p *singleEntryProvider) List() []runstatus.SessionHeader {
	// 0 entries until the run has emitted at least one event; else the one
	// session this source records. Preserves v1 sessions.list semantics.
	snap, err := p.entry.Source.Snapshot()
	if err != nil || len(snap.Events) == 0 {
		return []runstatus.SessionHeader{}
	}
	return []runstatus.SessionHeader{snap.Session}
}

func (p *singleEntryProvider) NewSession(context.Context, string) (string, error) {
	return "", errReadOnlySurface
}

func (p *singleEntryProvider) Reload(context.Context, string) (bool, error) {
	return false, errReadOnlySurface
}

func (p *singleEntryProvider) Staleness(context.Context, string) (bool, string, error) {
	return false, "", errReadOnlySurface
}

func (p *singleEntryProvider) ListStories() []StoryHeader { return nil }

func (p *singleEntryProvider) Rescan() ([]StoryHeader, error) {
	return nil, errReadOnlySurface
}

// StoryHeader is one discovered story as the SPA home screen's story browser
// renders it. The provider maps its internal story metadata
// (webconfig.StoryMeta) onto this shape — the server never imports webconfig.
//
//   - Path is the ABSOLUTE path to the story's app.yaml (the canonical key
//     session.new takes; app.id is display-only).
//   - AppID / Title are display fields from the loaded app definition.
//   - ActiveSessions lists the ids of live sessions started from this story, so
//     the browser can show a count badge and an open-existing affordance.
type StoryHeader struct {
	Path           string   `json:"path"`
	AppID          string   `json:"app_id"`
	Title          string   `json:"title"`
	ActiveSessions []string `json:"active_sessions"`
}

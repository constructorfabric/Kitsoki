package server

import (
	"context"

	"kitsoki/internal/runstatus"
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
	// ListStories returns the discovered story catalogue with per-story
	// active-session counts.
	ListStories() []StoryHeader
	// Rescan re-walks the configured story dirs and returns the refreshed
	// catalogue, leaving live sessions untouched.
	Rescan() ([]StoryHeader, error)
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
type Entry struct {
	Source Source
	Driver Driver
	Meta   MetaDriver
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

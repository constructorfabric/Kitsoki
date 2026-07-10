package server

import "context"

// meta.go — the meta-mode seam the [Server] dispatches the `runstatus.meta.*`
// RPCs against. Meta mode is the web surface for kitsoki's overlay agents
// (story edit / story Q&A / kitsoki help): a persistent multi-turn chat with a
// named agent, backed by internal/metamode.
//
// Like [Driver], the concrete implementation lives in package main (it needs a
// metamode.Controller + chat store, which depend on buildSessionRuntime), so
// the server defines the interface and the registry depends on it. Read-only
// surfaces leave [Entry.Meta] nil; the dispatch handlers then return
// [codeReadOnly], exactly like the write RPCs do without a [Driver].

// MetaDriver is the per-session (or per-self) meta-mode seam. One MetaDriver is
// bound to one chat scope: a live session's current state for the story.* modes,
// or the cross-app SelfAppID for the kitsoki.* modes. The mode key
// ("story.edit", "story.ask", "story.improve", "kitsoki.ask", …) selects
// the agent within that scope.
type MetaDriver interface {
	// Modes lists the meta modes available in this scope (from the bound
	// AppDef's injected meta_modes), so the SPA can build its dropdown.
	Modes(ctx context.Context) ([]MetaModeInfo, error)
	// Enter resolves (or resumes) the chat for mode. When chatID is non-empty
	// it resumes that specific row; otherwise it resolves by (mode, scope),
	// resuming the persistent conversation. Returns the session handle plus the
	// existing transcript so the SPA can rehydrate after a page reload.
	Enter(ctx context.Context, mode, chatID string) (MetaSession, error)
	// Send issues one turn against mode's chat and returns the assistant reply
	// plus the edit/reload outcome (ReloadRequested + ChangedFiles when the turn
	// edited the story tree).
	Send(ctx context.Context, mode, chatID, input string) (MetaSendResult, error)
	// NewChat archives mode's active chat and opens a fresh one in the same
	// scope, returning the new (empty) session.
	NewChat(ctx context.Context, mode, chatID string) (MetaSession, error)
	// Transcript returns the full message list for a chat row, for rehydration.
	Transcript(ctx context.Context, chatID string) ([]MetaMessage, error)
}

// MetaModeInfo is one selectable mode in the SPA's meta dropdown.
type MetaModeInfo struct {
	Key      string `json:"key"`       // "story.edit", "story.ask", "kitsoki.ask"
	Label    string `json:"label"`     // human label
	Banner   string `json:"banner"`    // one-line description shown atop the overlay
	Agent    string `json:"agent"`     // resolved agent name
	ReadOnly bool   `json:"read_only"` // true when the mode cannot edit files
	Group    string `json:"group"`     // "story" | "kitsoki"
}

// MetaMessage is one transcript turn (role: "user" | "assistant" | "system").
type MetaMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// MetaSession is the handle returned by Enter / NewChat: the chat row id, its
// mode, and the transcript so far.
type MetaSession struct {
	ChatID   string        `json:"chat_id"`
	ModeKey  string        `json:"mode_key"`
	Messages []MetaMessage `json:"messages"`
}

// MetaSendResult is the outcome of one meta turn. The reload fields drive the
// SPA's in-place content refresh (NOT a browser reload): when ReloadRequested
// is true the SPA calls runstatus.session.reload and re-hydrates the run store.
type MetaSendResult struct {
	Assistant       string   `json:"assistant"`
	ChatID          string   `json:"chat_id"`
	ReloadRequested bool     `json:"reload_requested"`
	ChangedFiles    []string `json:"changed_files"`
	CommitSHA       string   `json:"commit_sha,omitempty"`
}

// MetaSelfProvider is the optional hook a [SessionProvider] implements to serve
// the home screen's session-less ("self") meta chat — the cross-app kitsoki.*
// modes that need no running story. The [Server] type-asserts its provider for
// it and routes meta RPCs with an empty session_id here. Providers that don't
// implement it (the read-only single-entry adapter) make home-screen meta
// report not-available.
type MetaSelfProvider interface {
	// MetaSelf returns the session-less meta driver, or ok=false when the
	// surface has none (read-only, or no agent registry available).
	MetaSelf() (MetaDriver, bool)
}

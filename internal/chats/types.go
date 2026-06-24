package chats

import (
	"strings"
	"time"
)

// ChatStatus is the lifecycle status of a chat, stored verbatim in the
// chats.status column. The four values are the only ones the store writes;
// callers compare against the typed constants rather than bare strings.
type ChatStatus string

const (
	// ChatActive is the default status of a freshly resolved or created
	// chat — it is eligible for [Store.Resolve] reuse.
	ChatActive ChatStatus = "active"
	// ChatPaused marks a chat the operator has parked; it is not currently
	// written by the store but is reserved so a paused chat reads
	// distinctly from an archived (soft-deleted) one.
	ChatPaused ChatStatus = "paused"
	// ChatCompleted marks a chat whose work is finished; like ChatPaused it
	// is a reserved terminal label rather than a status the store sets.
	ChatCompleted ChatStatus = "completed"
	// ChatArchived is the soft-delete status set by [Store.Archive]. Archived
	// rows are invisible to [Store.Resolve], so "/meta new" mints a fresh chat.
	ChatArchived ChatStatus = "archived"
)

// Chat is a persistent conversation thread within a room. It is a plain
// value snapshot of one chats row; mutating a Chat does not write back —
// use the [Store] methods. Time fields are whole-microsecond UTC, the
// precision SQLite stores.
type Chat struct {
	// ID is the chat's unique ULID, allocated at creation. Lexical ULID
	// order is creation order.
	ID string
	// AppID is the app the owning room belongs to.
	AppID string
	// Room is the state path that owns the chat (e.g. "bugfix.phase_3").
	Room string
	// ScopeKey is a free-form disambiguator (e.g. a ticket id "PROJ-123")
	// so a single room can hold per-ticket / per-workspace threads. Empty
	// when the room keeps a single thread.
	ScopeKey string
	// Title is the human-readable label shown in pickers; set at creation
	// and updated by [Store.Rename].
	Title string
	// Status is the lifecycle status — one of the [ChatStatus] values,
	// held as a string for direct scanning.
	Status string
	// ClaudeSessionID is the Claude-side session id passed to
	// `claude -p --session-id`. Empty until the first turn allocates one;
	// always empty on a fresh fork (a fork starts a clean Claude session).
	ClaudeSessionID string
	// ParentChatID is set only on forks and points at the source chat;
	// empty (NULL) for an original chat. Resolve ignores rows with a
	// parent, so forks never satisfy a get-or-create.
	ParentChatID string
	// SessionID is the last kitsoki session that drove the chat. Audit-only —
	// chats are global and outlive any single session.
	SessionID string
	// CreatedAt is when the chat row was inserted.
	CreatedAt time.Time
	// UpdatedAt is the last time any chat field changed.
	UpdatedAt time.Time
	// LastActiveAt is the last time a message was appended; List and Resolve
	// order by this so the most recently used chat surfaces first.
	LastActiveAt time.Time
}

// Message is one turn in a chat transcript — a plain value snapshot of a
// chat_messages row.
type Message struct {
	// ChatID is the owning chat's ULID.
	ChatID string
	// Seq is the 0-based, dense, monotonic position within the chat,
	// assigned atomically by [Store.AppendMessage].
	Seq int
	// Role is the speaker: user, assistant, system, or tool.
	Role string
	// Content is the message body, stored verbatim (no redaction).
	Content string
	// Metadata is arbitrary structured side-data, persisted as JSON. Nil
	// when the row carries none.
	Metadata map[string]any
	// CreatedAt is when the message was appended.
	CreatedAt time.Time
}

// DisplayScopeKey returns the operator-facing scope label for a persisted chat
// scope key. Session-scoped chat resolution stores an internal sentinel prefix
// so independent Kitsoki sessions never adopt each other's chats; that prefix
// is not useful in active-work reacquire surfaces.
func DisplayScopeKey(scope string) string {
	const sessionPrefix = "\x00session="
	if strings.HasPrefix(scope, sessionPrefix) {
		if idx := strings.LastIndex(scope, "\x00"); idx >= len(sessionPrefix) {
			return scope[idx+1:]
		}
	}
	return scope
}

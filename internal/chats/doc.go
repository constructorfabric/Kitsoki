// Package chats implements persistent agent-room chat threads for kitsoki.
//
// # Prompt-injection threat model (LLM picker)
//
// The natural-language chat picker (host.chat.resolve_ref's LLM fallback)
// treats all chat content — user query, titles, and transcript bodies — as
// untrusted data via XML-tag delimitation, with a system-style preamble
// that instructs the model to ignore instructions found inside the tags
// and with closing-tag sequences neutralised in interpolated payloads.
// This raises the bar against prompt injection but is not a complete
// defence: sufficiently-determined injection in transcript content (e.g.
// content the LLM is highly motivated to follow, or non-Latin obfuscation
// of closing tags) can still influence picker behaviour. Treat picker
// results as advisory — they are wired into a confirmation step in the
// caller (Oracle's "open <ref>" flow) rather than directly mutating state.
//
// # Model
//
// A chat is a conversation thread within a room (e.g. "oracle",
// "bugfix.phase_3"). Chats are global: they are not tied to a kitsoki session
// and survive process restarts. The same chat can be driven from the TUI,
// from `kitsoki chat continue`, or as a background job.
//
// Each chat row carries:
//
//   - id                ULID
//   - app_id            the app the room belongs to
//   - room              the state path that owns the chat
//   - scope_key         a free-form disambiguator ("PROJ-123") so a single
//                       room can hold per-ticket / per-workspace threads
//   - title             human-readable label
//   - status            active|paused|completed|archived
//   - claude_session_id the Claude-side session ID for `claude -p --session-id`
//   - parent_chat_id    set on forks (otherwise NULL)
//   - session_id        last kitsoki session that drove the chat (audit only)
//
// Messages are stored in chat_messages (chat_id, seq, role, content,
// metadata) with monotonically-increasing seq per chat, computed atomically
// in AppendMessage.
//
// # Resolve semantics
//
// Resolve(app, room, scope_key, title) implements get-or-create on the
// (app, room, scope_key, parent_chat_id IS NULL) tuple — the newest
// non-fork chat wins. Forks intentionally produce additional chats with
// the same tuple but a parent pointer; they are excluded from Resolve and
// listed only via the chats_parent index.
//
// # Fork
//
// Fork copies all messages from the parent atomically into a new chat with
// parent_chat_id set and a fresh (empty) claude_session_id. The next turn
// against the fork allocates a new Claude session and the room is expected
// to wrap the prompt with a summary of the prior transcript. The fork's
// claude_session_id is intentionally cleared so Claude starts a clean
// session — there is no shared state with the parent.
//
// # Singleton lock
//
// WithLock guards a per-chat lock so exactly one driver runs a turn at a
// time. The lock row in chat_locks records owner_pid + owner_host +
// heartbeat_at; cross-host locks are always treated as busy (we cannot
// probe liveness across hosts) and same-host stale locks are reaped only
// when both heartbeat_at is older than 30s and the owner pid is no longer
// alive (kill -0 check). Operators have an escape hatch via
// `kitsoki chat unlock --force`.
//
// On lock contention the call returns ErrChatBusy. CLI wrappers (loop.py,
// kitsoki chat continue) translate this into exit code 75 (EX_TEMPFAIL).
//
// # Construction
//
// NewStore takes an existing *sql.DB (shared with the session/jobs stores
// so the entire app stays in one SQLite file) and applies the embedded
// schema migration idempotently. Pass WithClock to inject a virtual clock
// for stale-lock-reaping tests.
//
//	cs, err := chats.NewStore(s.DB(), chats.WithClock(clk))
//
// The store has no goroutines and is safe for concurrent use across both
// foreground (TUI / chat continue) and background (orchestrator-dispatched
// jobs) callers — the SQLite-level singleton lock provides the
// serialization.
package chats

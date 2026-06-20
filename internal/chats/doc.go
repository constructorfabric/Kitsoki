// Package chats is the SQLite-backed persistence tier for kitsoki's
// agent-room chat threads. It sits below the host layer: the
// host.chat.* handlers (documented in docs/architecture/hosts.md) and the
// `kitsoki chat` CLI call [Store] methods; the [Store] never reaches up
// into the orchestrator and owns no goroutines of its own.
//
// A chat is a conversation thread within a room (e.g. "agent",
// "bugfix.phase_3"). Chats are global: they are not tied to a kitsoki
// session and survive process restarts, so the same chat can be driven
// from the TUI, from `kitsoki chat continue`, or as a background job.
//
// # Algorithm
//
// The package is four cooperating concerns over one SQLite file (shared
// with the session/jobs stores), each in its own source file:
//
//   - Chat rows + transcripts (store.go): get-or-create [Store.Resolve],
//     append-only [Store.AppendMessage] with a monotonic per-chat seq,
//     and [Store.Fork] which deep-copies a transcript into a child chat.
//   - The singleton lock (lock.go): [Store.WithLock] serialises turns so
//     exactly one driver runs against a chat at a time.
//   - PTY sessions (pty.go): the lifecycle of a tmux-hosted claude
//     attached to a chat, recorded as chat_pty_sessions rows.
//   - The input queue (queue.go): a FIFO of drives (turn requests)
//     against a chat, with a CAS claim so at most one dispatcher runs a
//     given drive.
//
// Identity is by ULID throughout (chat ids, drive ids), allocated by
// [kitsoki/internal/ulid]; lexical ULID order is creation order, which is
// why FIFO queries break ties on the id column.
//
// # Invariants
//
//   - Per-chat seq is dense and monotonic from 0. [Store.AppendMessage]
//     computes the next seq inside the same transaction as the INSERT, so
//     concurrent appends cannot collide on a seq.
//   - Resolve operates on the (app_id, room, scope_key, parent_chat_id IS
//     NULL, status != 'archived') tuple — the newest match wins. Forks
//     (parent_chat_id set) and archived rows are invisible to Resolve, so
//     "/meta new" can always mint a fresh row.
//   - A chat holds at most one chat_pty_sessions row. The row's tmux_host
//     pins it to a single host — tmux is per-host, so a cross-host attach
//     or detach is rejected rather than silently migrated.
//   - A drive moves pending → dispatching → {done|failed}, or pending →
//     dismissed. Every transition is a CAS UPDATE guarded by the expected
//     from-status, so a lost race surfaces as a sentinel error rather than
//     a double-dispatch.
//
// # Worked example
//
//	cs, _ := chats.NewStore(db)                       // schema applied
//	c, created, _ := cs.Resolve(ctx,                  // get-or-create
//	    "bugfix", "phase_3", "PROJ-123", "triage")
//	// created == true (first call), c.ID == <ULID>, c.Status == "active"
//
//	m, _ := cs.AppendMessage(ctx, c.ID,               // first turn
//	    "user", "what changed?", nil)
//	// m.Seq == 0
//	m2, _ := cs.AppendMessage(ctx, c.ID,
//	    "assistant", "the lockfile", nil)
//	// m2.Seq == 1
//
//	got, _ := cs.Transcript(ctx, c.ID, 0)             // read it back
//	// len(got) == 2, got[0].Role == "user", got[1].Role == "assistant"
//
//	again, created2, _ := cs.Resolve(ctx,             // same tuple
//	    "bugfix", "phase_3", "PROJ-123", "triage")
//	// created2 == false, again.ID == c.ID
//
// A runnable form of this trace lives in [ExampleStore].
//
// # Lifecycle
//
// [NewStore] takes an existing *sql.DB (shared so the whole app stays in
// one SQLite file) and applies the embedded schema migration idempotently;
// pass [WithClock] to drive time deterministically in tests and
// [WithJournalWriter] to mirror mutations into the journal. The schema is
// forward-only: each [expectedSchemaVersion] bump pairs a DDL change with
// a migration step, and a DB stamped at an unknown version is refused
// rather than silently re-migrated (see [NewStore] and Non-goals).
//
// # Contracts
//
// The zero [Store] is not usable — always construct through [NewStore].
// Store is safe for concurrent use across foreground (TUI / chat continue)
// and background (orchestrator-dispatched job) callers: SQLite's serialised
// writer plus the per-chat singleton lock provide the serialisation, and
// the Store holds no mutable in-process state. [Store.WithLock] checks
// ctx.Err() before touching chat_locks, so a pre-cancelled context returns
// ctx.Err() without acquiring; callers own any lock-acquisition timeout via
// the ctx they pass.
//
// Errors are sentinels — match with errors.Is, never by string:
//
//   - [ErrChatNotFound] — no chat row for the id.
//   - [ErrChatBusy] — the per-chat lock is held by a live owner.
//   - [ErrNoPTYSession] — no chat_pty_sessions row for the chat.
//   - [ErrPTYCrossHost] — a PTY row exists, but on a different tmux host.
//   - [ErrNoPendingDrive] — no pending drive to dequeue.
//   - [ErrDriveNotFound] / [ErrDriveStateMismatch] — the drive is absent,
//     or present in a status the requested transition did not expect.
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
// caller (Agent's "open <ref>" flow) rather than directly mutating state.
//
// # Non-goals
//
//   - No schema downgrades. Migrations are forward-only: a DB stamped at a
//     version this build doesn't recognise is refused, never rolled back —
//     a downgrade path would need to reason about data the newer build
//     wrote, which this PoC deliberately does not attempt.
//   - No chat merging. Fork is one-directional (parent → child with a
//     cleared Claude session); there is no operation that folds two
//     transcripts back together, because seq density and the single
//     parent pointer make a merge ambiguous.
//   - No payload encryption or redaction. Transcript bodies and drive
//     payloads are stored verbatim; confidentiality is the embedding
//     deployment's concern, not this tier's.
//   - No cross-host tmux orchestration. A PTY row is pinned to the host
//     that owns its tmux session; this package rejects cross-host
//     transitions rather than attempting remote tmux control.
//   - No background goroutines or timers. The Store reacts only to method
//     calls; idle detection and stale-lock reaping happen inline on the
//     calls that care, so there is nothing to start or stop.
//
// # Reference
//
// The host handlers that sit on top of this tier (host.chat.resolve,
// host.chat.drive, host.chat.fork, …) are documented in
// docs/architecture/hosts.md. The drive transports that originate queue
// rows (tui, jira, mcp, …) are described in docs/architecture/transports.md.
package chats

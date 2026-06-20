// Package chathost is the one wiring seam that bridges the concrete
// [chats.Store] to the [host.ChatStore] interface the orchestrator depends
// on. It sits at the composition root (cmd/kitsoki wiring): it is the only
// package that imports both internal/chats and internal/host, so the rest of
// the codebase can program against host.ChatStore without ever importing
// chats — which is what keeps the chats↔host dependency from becoming a
// cycle.
//
// # Algorithm
//
// The adapter is a thin passthrough. Each [host.ChatStore] method forwards
// directly to the matching [chats.Store] method; the only work the adapter
// performs is translation at the boundary, in two flavours:
//
//  1. Type translation. chats and host each define their own record shapes
//     ([chats.Chat] vs [host.ChatRecord], [chats.Message] vs
//     [host.ChatMessage], [chats.Drive] vs [host.ChatDrive], and the
//     option/filter structs). The adapter copies field-for-field through the
//     unexported toRecord/toMessage/toDrive helpers and re-wraps the option
//     and filter structs. Enum-like strings (transport, drive status) cross
//     as plain strings on the host side and as their chats typed aliases on
//     the chats side.
//  2. Error translation. chats returns package-private sentinels; the host
//     side must not import chats to test them. The adapter maps each chats
//     sentinel to its host equivalent so callers use errors.Is against the
//     host sentinel — see the contract notes below.
//
// No value is reinterpreted, cached, retried, or reordered. If chats returns
// an error, the adapter returns it (possibly re-typed); if chats succeeds,
// the adapter returns the translated value.
//
// # Invariants
//
//   - One-to-one. Every host.ChatStore method maps to exactly one chats.Store
//     call. There are no composite operations that issue two store calls.
//   - Sentinel correspondence. For every chats sentinel a caller might branch
//     on (ErrChatBusy, ErrNoPendingDrive, ErrDriveNotFound,
//     ErrDriveStateMismatch) there is a host sentinel the adapter translates
//     it to, and errors.Is keeps matching across the translation.
//   - Message preservation on translated state errors. When chats reports
//     ErrDriveStateMismatch the adapter threads the host sentinel into the
//     chain while preserving the original Error() text (which carries the
//     actual-vs-expected status), so logs stay diagnostic.
//
// # Worked example
//
// Wiring the adapter and driving one create→get round-trip:
//
//	s, _   := chats.NewStore(db)          // concrete store over SQLite
//	a      := chathost.NewAdapter(s)      // a is a host.ChatStore
//
//	rec, _ := a.Create(ctx, "my-app", "agent", "", "Test Chat")
//	got, _ := a.Get(ctx, rec.ID)
//
//	in:  Create(app="my-app", room="agent", scope="", title="Test Chat")
//	out: *host.ChatRecord{ ID:"chat_…", Title:"Test Chat", Status:"active", … }
//	in:  Get(chatID=rec.ID)
//	out: *host.ChatRecord{ ID:"chat_…" (same id), Title:"Test Chat", … }
//
// The adapter's only contribution to the trace is the chats.Chat →
// host.ChatRecord copy; the id, title, and status come straight from the
// store. A runnable form of this trace lives in [ExampleNewAdapter].
//
// # Lifecycle
//
// [NewAdapter] is called once at startup, immediately after [chats.NewStore],
// and the returned value is handed to the orchestrator (for example via an
// orchestrator option). The adapter holds only the *chats.Store pointer and
// adds no state of its own, so it is safe for concurrent use exactly insofar
// as the wrapped chats.Store is — all concurrency guarantees (and the per-chat
// lock behind [host.ChatStore.WithLock]) live in chats, and the adapter
// neither adds nor removes any. Context cancellation is whatever chats
// honours; the adapter passes ctx through untouched.
//
// # Non-goals
//
//   - No business logic. The adapter never decides anything — no retry, no
//     fallback, no validation beyond what chats already does. Adding policy
//     here would split chat-store behaviour across two packages and defeat
//     the single-seam design.
//   - No caching layer. Every call flows through to chats.Store on every
//     invocation. Caching would introduce coherence questions (when does a
//     forked or archived chat invalidate?) that belong in the store, not the
//     adapter.
//   - No async coordination. The adapter exposes the same synchronous surface
//     chats does; queue draining, dispatch, and concurrency are the caller's
//     concern, mediated through WithLock and the drive queue methods.
//   - Not instantiated directly. The adapter struct is unexported on purpose;
//     callers go through [NewAdapter] so the seam stays a single, typed entry
//     point and the host package never sees a chats.Store.
//
// # Reference
//
// The chats store this adapter wraps — its record shapes, the drive queue,
// the per-chat lock, and the sentinel errors translated here — is documented
// in [kitsoki/internal/chats]. The host.ChatStore interface the adapter
// satisfies, and the host sentinels it translates to, live in
// [kitsoki/internal/host].
package chathost

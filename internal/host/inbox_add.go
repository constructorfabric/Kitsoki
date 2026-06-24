// Package host — host.inbox.add — always-on local notification sink.
//
// Implements the bare `inbox` op (see docs/architecture/hosts.md).
// `host.inbox.add` is **always-on**: even in pure-autonomous mode with
// no TUI attached, the call must persist a notification so a
// `--continue` reattach sees it.
//
// The handler accepts an `InboxAdder` injected via context.  When the
// orchestrator wires the inbox in production it installs a real
// adapter that fans the call into internal/jobs.JobStore.
// InsertNotification (the path inbox.PostJobNotification uses).  Tests
// (and the standalone judge harness) install a no-op or in-memory
// fake.
//
// If no adapter is installed the handler does NOT fail: it returns
// Result{Data: {ok: true, persisted: false}} — that way story flows
// stay flow-test-runnable even when no jobs store is wired.  This
// mirrors the always-on contract: the call never blocks execution.
package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// InboxNotification is the payload host.inbox.add accepts.  Field
// names match the bugfix-story YAML shape verbatim so a re-export of
// args into this struct is mechanical for handler authors.
type InboxNotification struct {
	Title  string
	Body   string
	Kind   string // checkpoint | ack | info | action_required
	Thread string
	State  string
}

// InboxAdder is the seam the orchestrator (or a test) supplies to
// persist the notification.  Returning ("", nil) is allowed for
// stand-alone modes that don't care about a stable ID.
type InboxAdder interface {
	AddInbox(ctx context.Context, n InboxNotification) (id string, err error)
}

// inboxAdderKey is the context key for the injected InboxAdder.
type inboxAdderKey struct{}

// WithInboxAdder injects an InboxAdder into ctx.  Orchestrator startup
// calls this with an adapter that bridges into internal/jobs.JobStore.
func WithInboxAdder(ctx context.Context, a InboxAdder) context.Context {
	return context.WithValue(ctx, inboxAdderKey{}, a)
}

// InboxAdderFromContext retrieves the injected adapter or nil.
func InboxAdderFromContext(ctx context.Context) InboxAdder {
	v, _ := ctx.Value(inboxAdderKey{}).(InboxAdder)
	return v
}

// InboxAddHandler implements host.inbox.add.
//
// Required args:
//   - title (string): short heading.
//   - body  (string): markdown body.
//
// Optional args:
//   - kind   (string): one of checkpoint / ack / info / action_required.
//     Defaults to "info" — the always-on contract has
//     no required severity.
//   - thread (string): correlation thread (e.g. the bug file path or
//     ticket id) so a future re-attach can wire the
//     notification back to its source.
//   - state  (string): destination state for the teleport target.  Bound
//     verbatim into the notification so the inbox UI
//     can deep-link.
//
// Returns Result.Data with:
//   - ok        (bool):  true when the call was accepted (even if the
//     adapter was absent — see package doc).
//   - id        (string): the adapter-assigned notification id, "" if
//     no adapter was installed.
//   - persisted (bool):  true iff an adapter accepted the write.
//
// Domain errors (returned via Result.Error) are reserved for malformed
// args; an adapter failure surfaces as Result.Error with the adapter's
// message so the YAML on_error: arc fires.
func InboxAddHandler(ctx context.Context, args map[string]any) (Result, error) {
	title, _ := args["title"].(string)
	body, _ := args["body"].(string)
	if strings.TrimSpace(title) == "" {
		return Result{Error: "host.inbox.add: title argument is required"}, nil
	}
	if strings.TrimSpace(body) == "" {
		return Result{Error: "host.inbox.add: body argument is required"}, nil
	}
	kind, _ := args["kind"].(string)
	if kind == "" {
		kind = "info"
	}
	thread, _ := args["thread"].(string)
	state, _ := args["state"].(string)

	n := InboxNotification{
		Title:  title,
		Body:   body,
		Kind:   kind,
		Thread: thread,
		State:  state,
	}

	adder := InboxAdderFromContext(ctx)
	if adder == nil {
		// Always-on contract: no adapter means we still report
		// success so the state machine advances.  Callers that care
		// about persistence install an adapter (the orchestrator
		// does this in production).
		return Result{Data: map[string]any{
			"ok":        true,
			"id":        "",
			"persisted": false,
			"note":      "no inbox adapter installed; notification dropped",
		}}, nil
	}
	id, err := adder.AddInbox(ctx, n)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.inbox.add: %v", err)}, nil
	}
	return Result{Data: map[string]any{
		"ok":        true,
		"id":        id,
		"persisted": true,
	}}, nil
}

// ─── In-memory adapter ──────────────────────────────────────────────────────

// MemInboxAdder is a fully-in-process InboxAdder used by tests and by
// stand-alone runs where no JobStore is wired.  It keeps a slice of
// accepted notifications so callers can assert on them.  Thread-safe
// for concurrent Add calls.
type MemInboxAdder struct {
	mu    sync.Mutex
	items []MemInboxItem
	next  int
	clk   func() time.Time
}

// MemInboxItem is one stored entry plus its assigned ID and arrival
// timestamp.  Sorted in insertion order.
type MemInboxItem struct {
	ID           string
	Notification InboxNotification
	CreatedAt    time.Time
}

// NewMemInboxAdder builds an in-memory adder.  If clk is nil, time.Now
// is used.
func NewMemInboxAdder(clk func() time.Time) *MemInboxAdder {
	if clk == nil {
		clk = time.Now
	}
	return &MemInboxAdder{clk: clk}
}

// AddInbox satisfies InboxAdder.
func (m *MemInboxAdder) AddInbox(_ context.Context, n InboxNotification) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	id := fmt.Sprintf("mem-%d", m.next)
	m.items = append(m.items, MemInboxItem{
		ID:           id,
		Notification: n,
		CreatedAt:    m.clk().UTC(),
	})
	return id, nil
}

// Items returns a snapshot of recorded entries (caller owns the slice).
func (m *MemInboxAdder) Items() []MemInboxItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MemInboxItem, len(m.items))
	copy(out, m.items)
	return out
}

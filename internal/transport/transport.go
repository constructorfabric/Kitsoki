// Package transport defines the output-only Transport.Post abstraction
// described in the bug-fix room proposal §4.
//
// A Transport is an output adapter onto an external surface — Jira ticket
// comments, Bitbucket PR comments, the TUI transcript pane. Phase templates
// invoke `transport.post` with a target transport key; the transport renders
// and delivers the message.
//
// v1 is output-only. There is no inbound `Open(handler)` loop; inbound is
// the orchestrator's job (`loop.py` polling, or a future webhook receiver).
package transport

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SessionKey identifies the external thread a Post targets.
//
// Examples:
//
//	{Transport: "jira",      Thread: "PLTFRM-12345"}
//	{Transport: "bitbucket", Thread: "DBI/repo/pulls/42"}
//	{Transport: "tui",       Thread: "<session-uuid>"}
type SessionKey struct {
	Transport string
	Thread    string
}

// String returns the canonical "transport:thread" form.
func (k SessionKey) String() string { return k.Transport + ":" + k.Thread }

// Message is the payload of a single Post.
type Message struct {
	// PhaseID identifies the phase this message originated from.
	// Used by orchestrators for de-dup and bot-output filtering.
	PhaseID string
	// Title is a short heading; transports may use it as a section header.
	Title string
	// Body is the rendered content. Transports may post it verbatim or wrap
	// it in transport-specific markup (Jira wiki, markdown, ANSI for TUI).
	Body string
	// Attachments are optional inline assets (file references, screenshots).
	Attachments []Attachment
	// BotMarker is prepended to the body so polling orchestrators can filter
	// their own output. Defaults to "[kitsoki]" when empty (set per-transport
	// in app.yaml).
	BotMarker string
	// Timestamp is the wall-clock time at which the message was created.
	// Zero-valued is fine; the transport may set it on Post if needed.
	Timestamp time.Time
}

// Attachment is an inline asset referenced from a Message.
type Attachment struct {
	// Name is the display name (e.g. "diff.patch").
	Name string
	// MIMEType is the IANA media type (e.g. "text/plain", "image/png").
	MIMEType string
	// Content is the inline byte payload. Empty for path-only attachments.
	Content []byte
	// Path is an alternative to Content: a local filesystem path the
	// transport may read on demand. Mutually exclusive with Content.
	Path string
}

// Transport is the output-only side of an external surface.
type Transport interface {
	// ID returns the transport key, e.g. "jira", "bitbucket", "tui".
	// Must match the Transport field of any SessionKey delivered to Post.
	ID() string

	// Post sends msg to the external thread. The returned string is an
	// opaque, transport-specific message ID that orchestrators may store
	// for traceability (Jira returns a comment ID; the TUI transport
	// returns an ULID).
	Post(ctx context.Context, key SessionKey, msg Message) (string, error)

	// Close releases any underlying resources. Safe to call multiple times.
	Close() error
}

// Registry holds registered Transport instances keyed by ID().
// The registry is safe for concurrent reads after initialization.
type Registry struct {
	mu sync.RWMutex
	tx map[string]Transport
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tx: make(map[string]Transport)}
}

// Register adds a Transport under its ID. Panics if a Transport with the
// same ID is already registered (init-time contract).
func (r *Registry) Register(t Transport) {
	if t == nil {
		panic("transport: Register called with nil Transport")
	}
	id := t.ID()
	if id == "" {
		panic("transport: Transport.ID() must be non-empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tx[id]; exists {
		panic(fmt.Sprintf("transport: %q already registered", id))
	}
	r.tx[id] = t
}

// Get returns the Transport registered under id, if any.
func (r *Registry) Get(id string) (Transport, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tx[id]
	return t, ok
}

// IDs returns the set of registered transport IDs in deterministic order.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tx))
	for id := range r.tx {
		out = append(out, id)
	}
	// Stable order for diagnostics.
	sortStrings(out)
	return out
}

// Post resolves key.Transport to a Transport and dispatches msg. Returns
// ErrTransportNotFound if no transport is registered under that ID.
func (r *Registry) Post(ctx context.Context, key SessionKey, msg Message) (string, error) {
	t, ok := r.Get(key.Transport)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrTransportNotFound, key.Transport)
	}
	if msg.BotMarker == "" {
		msg.BotMarker = DefaultBotMarker
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	return t.Post(ctx, key, msg)
}

// Close closes every registered transport, returning the first error.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, t := range r.tx {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.tx = nil
	return firstErr
}

// ErrTransportNotFound is returned by Registry.Post when key.Transport has
// no registered Transport.
var ErrTransportNotFound = fmt.Errorf("transport: not found")

// DefaultBotMarker is the prefix every transport prepends to its Post bodies
// so orchestrators can filter their own output on inbound polling. Per
// proposal §4.3.
const DefaultBotMarker = "[kitsoki]"

// registryKey is the context key used by WithRegistry / FromContext.
type registryKey struct{}

// WithRegistry returns a derived context carrying r so handlers (e.g. the
// `host.transport.post` bridge) can dispatch into the registry without
// being wired through every effect call.
func WithRegistry(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, r)
}

// FromContext extracts the Registry previously installed via WithRegistry.
// Returns nil if no registry is installed.
func FromContext(ctx context.Context) *Registry {
	if v, ok := ctx.Value(registryKey{}).(*Registry); ok {
		return v
	}
	return nil
}

// sortStrings is a no-import helper to keep this file dependency-free.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

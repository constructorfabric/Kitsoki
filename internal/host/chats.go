// Package host — ChatStore interface and context helpers for persistent
// agent-room chats. Defined here (not in internal/chats) so the host package
// stays free of a chats import, avoiding import cycles in tests / mocks.
// *chats.Store does NOT satisfy this interface directly — use the adapter in
// internal/chathost to bridge between the two packages.
package host

import (
	"context"
	"errors"
	"time"
)

// ErrChatBusy is returned by ChatStore.WithLock when another process holds the
// per-chat lock. The concrete error from chats.Store is a *chatBusyError; the
// adapter in internal/chathost translates it to this sentinel so callers in
// host package only need to import host, not chats.
var ErrChatBusy = errors.New("chats: chat busy")

// ErrNoPendingDrive mirrors chats.ErrNoPendingDrive at the host-package
// boundary. Returned by ChatStore.Dequeue when no pending drive exists.
var ErrNoPendingDrive = errors.New("chats: no pending drive")

// ErrDriveNotFound mirrors chats.ErrDriveNotFound at the host-package
// boundary. Returned by GetDrive / MarkDrive* when the drive_id is unknown.
var ErrDriveNotFound = errors.New("chats: drive not found")

// ErrDriveStateMismatch mirrors chats.ErrDriveStateMismatch at the
// host-package boundary. Returned by MarkDrive* when the row is in a
// status incompatible with the requested transition.
var ErrDriveStateMismatch = errors.New("chats: drive state mismatch")

// chatBusyHostError wraps an underlying lock error while implementing
// errors.Is(target == ErrChatBusy) == true.
type chatBusyHostError struct{ cause error }

func (e *chatBusyHostError) Error() string   { return e.cause.Error() }
func (e *chatBusyHostError) Unwrap() error   { return e.cause }
func (e *chatBusyHostError) Is(t error) bool { return t == ErrChatBusy }

// NewChatBusyError wraps cause as a host.ErrChatBusy-compatible error.
// The adapter in internal/chathost calls this when it detects chats.ErrChatBusy.
func NewChatBusyError(cause error) error { return &chatBusyHostError{cause: cause} }

// ChatRecord mirrors chats.Chat at the host-package boundary.
// Same fields, no methods — conversion is the adapter's responsibility.
type ChatRecord struct {
	ID, AppID, Room, ScopeKey, Title, Status string
	ClaudeSessionID, ParentChatID, SessionID string
	CreatedAt, UpdatedAt, LastActiveAt       time.Time
}

// ChatMessage mirrors chats.Message at the host-package boundary.
type ChatMessage struct {
	ChatID    string
	Seq       int
	Role      string
	Content   string
	Metadata  map[string]any
	CreatedAt time.Time
}

// ChatDrive mirrors chats.Drive at the host-package boundary. The string
// fields for Transport and Status carry the same well-known values
// chats package exposes (DriveStatusPending, DriveTransportTUI, …) —
// the host package keeps them as strings so it does not import chats.
type ChatDrive struct {
	DriveID         string
	ChatID          string
	Transport       string
	Thread          string
	Actor           string
	CorrelationID   string
	Payload         string
	Status          string
	ReceivedAt      time.Time
	DispatchedAt    *time.Time
	CompletedAt     *time.Time
	ResultSeq       *int
	ErrorMessage    string
	OnCompleteJSON  string
	OriginSessionID string
	OriginState     string
}

// EnqueueDriveOptions carries the inputs for ChatStore.Enqueue. The
// fields mirror chats.EnqueueOptions; same package-boundary
// duplication as ChatRecord vs chats.Chat.
type EnqueueDriveOptions struct {
	ChatID          string
	Transport       string
	Thread          string
	Actor           string
	CorrelationID   string
	Payload         string
	OnCompleteJSON  string
	OriginSessionID string
	OriginState     string
}

// ListDrivesFilter mirrors chats.ListDrivesFilter. Statuses is the
// list of statuses to include; an empty slice means all statuses.
type ListDrivesFilter struct {
	Statuses []string
	Limit    int
}

// ChatStore is the subset of *chats.Store the host package needs.
// Defined here to avoid a host → chats import.
// Use internal/chathost.NewAdapter to wrap a *chats.Store.
type ChatStore interface {
	Get(ctx context.Context, chatID string) (*ChatRecord, error)
	// GetOrEnsure returns the chat with the given ID. If no row exists, it
	// inserts a minimal placeholder (blank app/room/scopeKey, title "untitled
	// chat") and returns it. Used by host.agent.converse under --harness replay
	// where the preceding host.chat.resolve cassette skips the real Resolve so
	// no row was ever inserted.
	GetOrEnsure(ctx context.Context, chatID string) (*ChatRecord, error)
	// Resolve performs a transactional get-or-create on (app, room, scopeKey).
	// The bool reports whether the chat was newly created (true) or returned
	// from existing rows (false).
	Resolve(ctx context.Context, app, room, scopeKey, title string) (*ChatRecord, bool, error)
	Create(ctx context.Context, app, room, scopeKey, title string) (*ChatRecord, error)
	List(ctx context.Context, app, room, scopeKey string) ([]ChatRecord, error)
	Fork(ctx context.Context, parentID, newTitle string) (*ChatRecord, error)
	Archive(ctx context.Context, chatID string) error
	Rename(ctx context.Context, chatID, title string) error
	SetClaudeSessionID(ctx context.Context, chatID, claudeID string) error
	AppendMessage(ctx context.Context, chatID, role, content string, metadata map[string]any) (ChatMessage, error)
	Transcript(ctx context.Context, chatID string, sinceSeq int) ([]ChatMessage, error)
	LatestSeq(ctx context.Context, chatID string) (int, error)
	WithLock(ctx context.Context, chatID string, fn func(context.Context) error) error

	// Enqueue inserts a fresh pending drive into the chat input queue.
	Enqueue(ctx context.Context, opts EnqueueDriveOptions) (*ChatDrive, error)
	// Dequeue atomically claims the oldest pending drive for the chat
	// (CAS pending → dispatching). Returns ErrNoPendingDrive when empty.
	Dequeue(ctx context.Context, chatID string) (*ChatDrive, error)
	// ClaimDrive transitions a specific pending drive to dispatching by
	// drive_id. Used by host.chat.drive's await:true path and by
	// `kitsoki chat queue dispatch <drive-id>` (operator promotion).
	ClaimDrive(ctx context.Context, driveID string) (*ChatDrive, error)
	// MarkDriveDone transitions dispatching → done with resultSeq.
	MarkDriveDone(ctx context.Context, driveID string, resultSeq int) error
	// MarkDriveFailed transitions dispatching → failed with errorMessage.
	MarkDriveFailed(ctx context.Context, driveID, errorMessage string) error
	// MarkDriveDismissed transitions pending → dismissed.
	MarkDriveDismissed(ctx context.Context, driveID string) error
	// GetDrive returns the drive row, or ErrDriveNotFound.
	GetDrive(ctx context.Context, driveID string) (*ChatDrive, error)
	// ListDrives returns drives for a chat in FIFO order, optionally
	// filtered by status and capped by Limit.
	ListDrives(ctx context.Context, chatID string, filter ListDrivesFilter) ([]ChatDrive, error)
}

// chatStoreKey is the unexported context key for the injected ChatStore.
type chatStoreKey struct{}

// WithChatStore injects a ChatStore into ctx so that chat-aware host handlers
// can access the store without importing the chats package.
// The orchestrator calls this before dispatching host effects when a chat store
// has been wired via orchestrator.WithChatStore.
func WithChatStore(ctx context.Context, cs ChatStore) context.Context {
	return context.WithValue(ctx, chatStoreKey{}, cs)
}

// ChatStoreFromContext retrieves the ChatStore from ctx, or nil if none was
// injected (e.g. the orchestrator was not configured with a chat store).
func ChatStoreFromContext(ctx context.Context) ChatStore {
	v, _ := ctx.Value(chatStoreKey{}).(ChatStore)
	return v
}

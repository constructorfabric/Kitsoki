package chathost

import (
	"context"
	"errors"

	"kitsoki/internal/chats"
	"kitsoki/internal/host"
)

// NewAdapter wraps s as a [host.ChatStore], the only supported way to obtain
// one — the underlying adapter type is unexported so the seam stays a single
// typed entry point and the host package never has to import chats. The
// returned value carries no state beyond s and is safe for concurrent use to
// exactly the degree s is; it adds no locking, caching, or retry of its own.
// s must be non-nil: NewAdapter does not check, and a nil store panics on
// first use rather than here.
func NewAdapter(s *chats.Store) host.ChatStore {
	return &adapter{s: s}
}

type adapter struct{ s *chats.Store }

func (a *adapter) Get(ctx context.Context, chatID string) (*host.ChatRecord, error) {
	c, err := a.s.Get(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return toRecord(c), nil
}

func (a *adapter) GetOrEnsure(ctx context.Context, chatID string) (*host.ChatRecord, error) {
	c, err := a.s.GetOrEnsure(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return toRecord(c), nil
}

func (a *adapter) Resolve(ctx context.Context, appID, room, scopeKey, title string) (*host.ChatRecord, bool, error) {
	c, created, err := a.s.Resolve(ctx, appID, room, scopeKey, title)
	if err != nil {
		return nil, false, err
	}
	return toRecord(c), created, nil
}

func (a *adapter) Create(ctx context.Context, appID, room, scopeKey, title string) (*host.ChatRecord, error) {
	c, err := a.s.Create(ctx, appID, room, scopeKey, title)
	if err != nil {
		return nil, err
	}
	return toRecord(c), nil
}

func (a *adapter) List(ctx context.Context, appID, room, scopeKey string) ([]host.ChatRecord, error) {
	cs, err := a.s.List(ctx, appID, room, scopeKey)
	if err != nil {
		return nil, err
	}
	out := make([]host.ChatRecord, len(cs))
	for i := range cs {
		// Use &cs[i] (not &c) so we don't take the address of the loop
		// variable. toRecord copies fields today, but addressing the slice
		// element directly avoids a footgun for any future change.
		out[i] = *toRecord(&cs[i])
	}
	return out, nil
}

func (a *adapter) Fork(ctx context.Context, parentID, newTitle string) (*host.ChatRecord, error) {
	c, err := a.s.Fork(ctx, parentID, newTitle)
	if err != nil {
		return nil, err
	}
	return toRecord(c), nil
}

func (a *adapter) Archive(ctx context.Context, chatID string) error {
	return a.s.Archive(ctx, chatID)
}

func (a *adapter) Rename(ctx context.Context, chatID, title string) error {
	return a.s.Rename(ctx, chatID, title)
}

func (a *adapter) SetClaudeSessionID(ctx context.Context, chatID, claudeID string) error {
	return a.s.SetClaudeSessionID(ctx, chatID, claudeID)
}

func (a *adapter) AppendMessage(ctx context.Context, chatID, role, content string, metadata map[string]any) (host.ChatMessage, error) {
	m, err := a.s.AppendMessage(ctx, chatID, role, content, metadata)
	if err != nil {
		return host.ChatMessage{}, err
	}
	return toMessage(m), nil
}

func (a *adapter) Transcript(ctx context.Context, chatID string, sinceSeq int) ([]host.ChatMessage, error) {
	msgs, err := a.s.Transcript(ctx, chatID, sinceSeq)
	if err != nil {
		return nil, err
	}
	out := make([]host.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = toMessage(m)
	}
	return out, nil
}

func (a *adapter) LatestSeq(ctx context.Context, chatID string) (int, error) {
	return a.s.LatestSeq(ctx, chatID)
}

// WithLock wraps chats.ErrChatBusy into host.ErrChatBusy so callers in the
// host package can use errors.Is(err, host.ErrChatBusy) without importing chats.
func (a *adapter) WithLock(ctx context.Context, chatID string, fn func(context.Context) error) error {
	err := a.s.WithLock(ctx, chatID, fn)
	if err != nil && errors.Is(err, chats.ErrChatBusy) {
		return host.NewChatBusyError(err)
	}
	return err
}

// ─── chat input queue ─────────────────────────────────────────────────────────

func (a *adapter) Enqueue(ctx context.Context, opts host.EnqueueDriveOptions) (*host.ChatDrive, error) {
	d, err := a.s.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          opts.ChatID,
		Transport:       chats.DriveTransport(opts.Transport),
		Thread:          opts.Thread,
		Actor:           opts.Actor,
		CorrelationID:   opts.CorrelationID,
		Payload:         opts.Payload,
		OnCompleteJSON:  opts.OnCompleteJSON,
		OriginSessionID: opts.OriginSessionID,
		OriginState:     opts.OriginState,
	})
	if err != nil {
		return nil, err
	}
	return toDrive(d), nil
}

func (a *adapter) Dequeue(ctx context.Context, chatID string) (*host.ChatDrive, error) {
	d, err := a.s.Dequeue(ctx, chatID)
	if err != nil {
		return nil, translateDriveErr(err)
	}
	return toDrive(d), nil
}

func (a *adapter) ClaimDrive(ctx context.Context, driveID string) (*host.ChatDrive, error) {
	d, err := a.s.ClaimDrive(ctx, driveID)
	if err != nil {
		return nil, translateDriveErr(err)
	}
	return toDrive(d), nil
}

func (a *adapter) MarkDriveDone(ctx context.Context, driveID string, resultSeq int) error {
	return translateDriveErr(a.s.MarkDriveDone(ctx, driveID, resultSeq))
}

func (a *adapter) MarkDriveFailed(ctx context.Context, driveID, errorMessage string) error {
	return translateDriveErr(a.s.MarkDriveFailed(ctx, driveID, errorMessage))
}

func (a *adapter) MarkDriveDismissed(ctx context.Context, driveID string) error {
	return translateDriveErr(a.s.MarkDriveDismissed(ctx, driveID))
}

func (a *adapter) GetDrive(ctx context.Context, driveID string) (*host.ChatDrive, error) {
	d, err := a.s.GetDrive(ctx, driveID)
	if err != nil {
		return nil, translateDriveErr(err)
	}
	return toDrive(d), nil
}

func (a *adapter) ListDrives(ctx context.Context, chatID string, filter host.ListDrivesFilter) ([]host.ChatDrive, error) {
	statuses := make([]chats.DriveStatus, len(filter.Statuses))
	for i, s := range filter.Statuses {
		statuses[i] = chats.DriveStatus(s)
	}
	ds, err := a.s.ListDrives(ctx, chatID, chats.ListDrivesFilter{
		Statuses: statuses,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]host.ChatDrive, len(ds))
	for i := range ds {
		out[i] = *toDrive(&ds[i])
	}
	return out, nil
}

// translateDriveErr maps chats package sentinel errors to their
// host-package equivalents so callers in host (and below) can use
// errors.Is against host.ErrNoPendingDrive / host.ErrDriveNotFound /
// host.ErrDriveStateMismatch without importing chats.
func translateDriveErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, chats.ErrNoPendingDrive):
		return host.ErrNoPendingDrive
	case errors.Is(err, chats.ErrDriveNotFound):
		return host.ErrDriveNotFound
	case errors.Is(err, chats.ErrDriveStateMismatch):
		// Preserve the original error's message (it carries the actual
		// vs. expected status) so logs stay useful; just thread the
		// host sentinel into the chain.
		return errJoin(host.ErrDriveStateMismatch, err)
	default:
		return err
	}
}

// errJoin wraps a sentinel + an inner error so errors.Is matches the
// sentinel and Error() reports the inner message. We can't use the
// stdlib %w idiom directly because the inner error already wraps its
// own sentinel.
func errJoin(sentinel, inner error) error {
	return &joinedErr{sentinel: sentinel, inner: inner}
}

type joinedErr struct {
	sentinel error
	inner    error
}

func (e *joinedErr) Error() string { return e.inner.Error() }
func (e *joinedErr) Is(target error) bool {
	return errors.Is(e.sentinel, target) || errors.Is(e.inner, target)
}
func (e *joinedErr) Unwrap() error { return e.inner }

// ─── conversion helpers ───────────────────────────────────────────────────────

func toRecord(c *chats.Chat) *host.ChatRecord {
	return &host.ChatRecord{
		ID:              c.ID,
		AppID:           c.AppID,
		Room:            c.Room,
		ScopeKey:        c.ScopeKey,
		Title:           c.Title,
		Status:          c.Status,
		ClaudeSessionID: c.ClaudeSessionID,
		ParentChatID:    c.ParentChatID,
		SessionID:       c.SessionID,
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
		LastActiveAt:    c.LastActiveAt,
	}
}

func toMessage(m chats.Message) host.ChatMessage {
	return host.ChatMessage{
		ChatID:    m.ChatID,
		Seq:       m.Seq,
		Role:      m.Role,
		Content:   m.Content,
		Metadata:  m.Metadata,
		CreatedAt: m.CreatedAt,
	}
}

func toDrive(d *chats.Drive) *host.ChatDrive {
	if d == nil {
		return nil
	}
	return &host.ChatDrive{
		DriveID:         d.DriveID,
		ChatID:          d.ChatID,
		Transport:       string(d.Transport),
		Thread:          d.Thread,
		Actor:           d.Actor,
		CorrelationID:   d.CorrelationID,
		Payload:         d.Payload,
		Status:          string(d.Status),
		ReceivedAt:      d.ReceivedAt,
		DispatchedAt:    d.DispatchedAt,
		CompletedAt:     d.CompletedAt,
		ResultSeq:       d.ResultSeq,
		ErrorMessage:    d.ErrorMessage,
		OnCompleteJSON:  d.OnCompleteJSON,
		OriginSessionID: d.OriginSessionID,
		OriginState:     d.OriginState,
	}
}

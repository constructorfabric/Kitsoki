// Package chathost provides an adapter that bridges *chats.Store and the
// host.ChatStore interface. This package is the only place that imports both
// internal/chats and internal/host; everything else in the codebase uses
// host.ChatStore directly so there is no import cycle.
//
// Usage (in cmd/kitsoki/main.go or similar wiring):
//
//	chatStore, err := chats.NewStore(db, chats.WithClock(clk))
//	adapter := chathost.NewAdapter(chatStore)
//	orch := orchestrator.New(..., orchestrator.WithChatStore(adapter))
package chathost

import (
	"context"
	"errors"

	"kitsoki/internal/chats"
	"kitsoki/internal/host"
)

// NewAdapter wraps s as a host.ChatStore.
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

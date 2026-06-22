package server

import (
	"context"
	"errors"
	"fmt"

	"kitsoki/internal/chats"
)

// ChatShowResult is the read-only focused context for one async chat thread.
type ChatShowResult struct {
	OK       bool              `json:"ok"`
	Context  *ChatShowContext  `json:"context,omitempty"`
	Chat     ChatInspectItem   `json:"chat"`
	PTY      *ChatPTYItem      `json:"pty,omitempty"`
	Messages []ChatMessageItem `json:"messages,omitempty"`
}

// ChatShowContext identifies the browser-facing session that focused this chat.
type ChatShowContext struct {
	SessionID string `json:"session_id,omitempty"`
}

// ChatInspectItem is a compact projection of chat metadata.
type ChatInspectItem struct {
	ID                    string `json:"id"`
	AppID                 string `json:"app_id"`
	Room                  string `json:"room"`
	ScopeKey              string `json:"scope_key"`
	DisplayScopeKey       string `json:"display_scope_key,omitempty"`
	Title                 string `json:"title"`
	Status                string `json:"status"`
	ClaudeSessionID       string `json:"claude_session_id,omitempty"`
	ParentChatID          string `json:"parent_chat_id,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	CreatedAtUnixMicro    int64  `json:"created_at_unix_micro"`
	UpdatedAtUnixMicro    int64  `json:"updated_at_unix_micro"`
	LastActiveAtUnixMicro int64  `json:"last_active_at_unix_micro"`
}

// ChatPTYItem describes the tmux-backed chat process when one is recorded.
type ChatPTYItem struct {
	ChatID              string `json:"chat_id"`
	TmuxSession         string `json:"tmux_session"`
	TmuxHost            string `json:"tmux_host"`
	Mode                string `json:"mode"`
	PermissionMode      string `json:"permission_mode,omitempty"`
	WorkspacePath       string `json:"workspace_path,omitempty"`
	CreatedAtUnixMicro  int64  `json:"created_at_unix_micro"`
	UpdatedAtUnixMicro  int64  `json:"updated_at_unix_micro"`
	LastIdleAtUnixMicro int64  `json:"last_idle_at_unix_micro,omitempty"`
}

// ChatMessageItem is one transcript row.
type ChatMessageItem struct {
	ChatID             string         `json:"chat_id"`
	Seq                int            `json:"seq"`
	Role               string         `json:"role"`
	Content            string         `json:"content"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CreatedAtUnixMicro int64          `json:"created_at_unix_micro"`
}

// ShowChat returns focused context for a chat surfaced by active-work rows.
func (d OrchestratorDriver) ShowChat(ctx context.Context, chatID string, sinceSeq int) (ChatShowResult, error) {
	if chatID == "" {
		return ChatShowResult{}, fmt.Errorf("chat.show: chat_id is required")
	}
	if d.Chats == nil {
		return ChatShowResult{}, fmt.Errorf("chat.show: no chat store configured")
	}
	chat, err := d.Chats.Get(ctx, chatID)
	if errors.Is(err, chats.ErrChatNotFound) {
		return ChatShowResult{}, fmt.Errorf("chat.show: unknown chat %q", chatID)
	}
	if err != nil {
		return ChatShowResult{}, fmt.Errorf("chat.show: get chat: %w", err)
	}
	messages, err := d.Chats.Transcript(ctx, chatID, sinceSeq)
	if err != nil {
		return ChatShowResult{}, fmt.Errorf("chat.show: transcript: %w", err)
	}
	pty, err := d.Chats.GetPTY(ctx, chatID)
	if errors.Is(err, chats.ErrNoPTYSession) {
		pty = nil
	} else if err != nil {
		return ChatShowResult{}, fmt.Errorf("chat.show: pty: %w", err)
	}
	return ChatShowResult{
		OK:       true,
		Chat:     inspectChat(chat),
		PTY:      inspectChatPTY(pty),
		Messages: inspectChatMessages(messages),
	}, nil
}

func inspectChat(c *chats.Chat) ChatInspectItem {
	if c == nil {
		return ChatInspectItem{}
	}
	return ChatInspectItem{
		ID:                    c.ID,
		AppID:                 c.AppID,
		Room:                  c.Room,
		ScopeKey:              c.ScopeKey,
		DisplayScopeKey:       chats.DisplayScopeKey(c.ScopeKey),
		Title:                 c.Title,
		Status:                c.Status,
		ClaudeSessionID:       c.ClaudeSessionID,
		ParentChatID:          c.ParentChatID,
		SessionID:             c.SessionID,
		CreatedAtUnixMicro:    c.CreatedAt.UnixMicro(),
		UpdatedAtUnixMicro:    c.UpdatedAt.UnixMicro(),
		LastActiveAtUnixMicro: c.LastActiveAt.UnixMicro(),
	}
}

func inspectChatPTY(p *chats.PtySession) *ChatPTYItem {
	if p == nil {
		return nil
	}
	item := &ChatPTYItem{
		ChatID:             p.ChatID,
		TmuxSession:        p.TmuxSession,
		TmuxHost:           p.TmuxHost,
		Mode:               string(p.Mode),
		PermissionMode:     p.PermissionMode,
		WorkspacePath:      p.WorkspacePath,
		CreatedAtUnixMicro: p.CreatedAt.UnixMicro(),
		UpdatedAtUnixMicro: p.UpdatedAt.UnixMicro(),
	}
	if p.LastIdleAt != nil {
		item.LastIdleAtUnixMicro = p.LastIdleAt.UnixMicro()
	}
	return item
}

func inspectChatMessages(in []chats.Message) []ChatMessageItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]ChatMessageItem, 0, len(in))
	for _, m := range in {
		out = append(out, ChatMessageItem{
			ChatID:             m.ChatID,
			Seq:                m.Seq,
			Role:               m.Role,
			Content:            m.Content,
			Metadata:           m.Metadata,
			CreatedAtUnixMicro: m.CreatedAt.UnixMicro(),
		})
	}
	return out
}

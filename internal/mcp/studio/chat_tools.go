package studio

import (
	"context"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/chats"
)

// registerChatTools wires read-only chat drill-down tools onto the studio MCP
// server. Session inspect is the async dashboard; chat.show is the focused
// reacquisition view once a client chooses a chat_id from backgrounded_chats or
// pending_drives.
func (srv *Server) registerChatTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "chat.show",
		Description: "Show a chat's focused context for async reacquisition. {chat_id, handle?, session_id?, since_seq?, last_n? (default 5; <=0 means all), offset? (skip from the tail)} -> {chat, pty?, messages[]}. Returns only the last_n transcript rows by default to keep the payload small; raise last_n or set it to 0 for the full transcript. Read-only; requires kitsoki mcp --db.",
	}, srv.handleChatShow)
}

// defaultChatShowLastN caps how many trailing transcript rows chat.show returns
// when last_n is omitted, keeping the reacquisition payload small. A caller that
// wants the full transcript passes last_n<=0.
const defaultChatShowLastN = 5

// ChatShowArgs is the input to chat.show.
type ChatShowArgs struct {
	ChatID    string `json:"chat_id"`
	Handle    string `json:"handle,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	SinceSeq  int    `json:"since_seq,omitempty"`
	// LastN caps the returned transcript to the last N rows (default
	// defaultChatShowLastN). Zero is treated as the default; a negative value
	// returns the full transcript.
	LastN *int `json:"last_n,omitempty"`
	// Offset skips this many rows from the tail before applying LastN, paginating
	// backwards through the transcript.
	Offset int `json:"offset,omitempty"`
}

// ChatShowResult is the read-only focused context for one chat thread.
type ChatShowResult struct {
	OK       bool              `json:"ok"`
	Context  *ChatShowContext  `json:"context,omitempty"`
	Chat     ChatInspectItem   `json:"chat"`
	PTY      *ChatPTYItem      `json:"pty,omitempty"`
	Messages []ChatMessageItem `json:"messages,omitempty"`
}

// ChatShowContext echoes the Kitsoki session context used to focus a chat from
// a global work queue. Some pending chat-drive rows carry the session on the
// drive rather than on the chat record, so chat.show preserves the explicit
// reacquire args instead of making clients rediscover them from chat metadata.
type ChatShowContext struct {
	Handle    string `json:"handle,omitempty"`
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

func (srv *Server) handleChatShow(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args ChatShowArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.ChatID == "" {
		return buildToolError(ErrBadRequest, "chat.show: chat_id is required"), nil, nil
	}
	store := srv.chatStore()
	if store == nil {
		return buildToolError(ErrBadRequest, "chat.show: no chat store configured; start kitsoki mcp with --db"), nil, nil
	}
	chat, err := store.Get(ctx, args.ChatID)
	if errors.Is(err, chats.ErrChatNotFound) {
		return buildToolError(ErrBadRequest, fmt.Sprintf("chat.show: unknown chat %q", args.ChatID)), nil, nil
	}
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("chat.show: get chat: %v", err)), nil, nil
	}
	reacquireContext, err := srv.chatShowContext(args, chat)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	messages, err := store.Transcript(ctx, args.ChatID, args.SinceSeq)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("chat.show: transcript: %v", err)), nil, nil
	}
	messages = paginateTranscript(messages, args.LastN, args.Offset)
	pty, err := store.GetPTY(ctx, args.ChatID)
	if errors.Is(err, chats.ErrNoPTYSession) {
		pty = nil
	} else if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("chat.show: pty: %v", err)), nil, nil
	}
	return nil, ChatShowResult{
		OK:       true,
		Context:  reacquireContext,
		Chat:     inspectChat(chat),
		PTY:      inspectChatPTY(pty),
		Messages: inspectChatMessages(messages),
	}, nil
}

// paginateTranscript trims a transcript to a tail window. With lastN==nil the
// default window (defaultChatShowLastN) applies; a value <=0 returns everything
// (after offset). offset skips that many rows from the tail first, so a caller
// can page backwards through history.
func paginateTranscript(in []chats.Message, lastN *int, offset int) []chats.Message {
	if len(in) == 0 {
		return in
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[:len(in)-offset]
	}
	n := defaultChatShowLastN
	if lastN != nil {
		n = *lastN
	}
	if n <= 0 {
		return in
	}
	if len(in) > n {
		in = in[len(in)-n:]
	}
	return in
}

func (srv *Server) chatShowContext(args ChatShowArgs, chat *chats.Chat) (*ChatShowContext, error) {
	ctx := &ChatShowContext{
		Handle:    args.Handle,
		SessionID: args.SessionID,
	}
	if ctx.Handle != "" {
		sh, err := srv.sess.ResolveSession(ctx.Handle)
		if err != nil {
			return nil, fmt.Errorf("chat.show: resolve handle %q: %v", ctx.Handle, err)
		}
		resolvedSID := string(sh.SID)
		if ctx.SessionID != "" && ctx.SessionID != resolvedSID {
			return nil, fmt.Errorf("chat.show: handle %q is session %q, not %q", ctx.Handle, resolvedSID, ctx.SessionID)
		}
		ctx.SessionID = resolvedSID
	}
	if ctx.SessionID == "" && chat != nil {
		ctx.SessionID = chat.SessionID
	}
	if chat != nil && chat.SessionID != "" && ctx.SessionID != "" && chat.SessionID != ctx.SessionID {
		return nil, fmt.Errorf("chat.show: chat %q belongs to session %q, not %q", chat.ID, chat.SessionID, ctx.SessionID)
	}
	if ctx.Handle == "" && ctx.SessionID == "" {
		return nil, nil
	}
	return ctx, nil
}

func (srv *Server) chatStore() *chats.Store {
	srv.sess.mu.Lock()
	defer srv.sess.mu.Unlock()
	return srv.sess.chatStore
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

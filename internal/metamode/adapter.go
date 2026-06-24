package metamode

// This file holds the package-internal adapters that satisfy ChatStore
// and AgentCaller against the real internal/chats and internal/host
// symbols. See the package doc (doc.go, "# Lifecycle") for why these
// seams exist and the three constraints host.AgentAskWithMCPHandler
// imposes that the AgentCaller adapter bridges.
//
// Session resume is the open seam: the adapter captures and forwards a
// claude session id, but the handler's non-chat path does not yet
// honour it (see the package "# Non-goals"). Wiring it up means growing
// host.AgentAskWithMCPHandler to accept a claude_session_id arg on the
// non-chat path.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/render/sourcecolor"
)

// ─── ChatStore adapter ───────────────────────────────────────────────────────

// NewChatStoreAdapter wraps a *chats.Store as a ChatStore.
func NewChatStoreAdapter(s *chats.Store) ChatStore { return &chatStoreAdapter{s: s} }

type chatStoreAdapter struct{ s *chats.Store }

func (a *chatStoreAdapter) ResolveMeta(ctx context.Context, appID, room, scopeKey, title string) (ChatHandle, error) {
	if a == nil || a.s == nil {
		return nil, fmt.Errorf("metamode.ResolveMeta: nil store")
	}
	row, _, err := a.s.Resolve(ctx, appID, room, scopeKey, title)
	if err != nil {
		return nil, err
	}
	return &chatHandle{
		ctx:   ctx,
		store: a.s,
		row:   row,
	}, nil
}

// GetMeta resolves a chat by full ID. Wraps chats.ErrChatNotFound as
// a typed error message so the controller's "chat not found" hint
// flows through unchanged.
func (a *chatStoreAdapter) GetMeta(ctx context.Context, chatID string) (ChatHandle, error) {
	if a == nil || a.s == nil {
		return nil, fmt.Errorf("metamode.GetMeta: nil store")
	}
	row, err := a.s.Get(ctx, chatID)
	if err != nil {
		if errors.Is(err, chats.ErrChatNotFound) {
			return nil, fmt.Errorf("metamode.GetMeta: chat %q not found", chatID)
		}
		return nil, fmt.Errorf("metamode.GetMeta: %w", err)
	}
	return &chatHandle{ctx: ctx, store: a.s, row: row}, nil
}

// ListMeta returns active meta-room chats for appID. We use the
// underlying List with an empty room filter and post-filter for the
// "meta:" prefix here — chats.Store.List takes an exact-match room,
// not a prefix, so the cheap path is "list all, filter in Go". Meta
// chats per app are few (one active session per (mode, scope) — see
// docs/stories/meta-mode.md), so the post-filter scan stays O(small).
func (a *chatStoreAdapter) ListMeta(ctx context.Context, appID string) ([]ChatHandle, error) {
	if a == nil || a.s == nil {
		return nil, fmt.Errorf("metamode.ListMeta: nil store")
	}
	rows, err := a.s.List(ctx, appID, "", "")
	if err != nil {
		return nil, fmt.Errorf("metamode.ListMeta: %w", err)
	}
	out := make([]ChatHandle, 0, len(rows))
	for i := range rows {
		row := rows[i] // copy: rows is []Chat, we need a stable pointer
		if !strings.HasPrefix(row.Room, "meta:") {
			continue
		}
		if row.Status == string(chats.ChatArchived) {
			continue
		}
		out = append(out, &chatHandle{ctx: ctx, store: a.s, row: &row})
	}
	return out, nil
}

// ArchiveMeta soft-deletes a chat by ID. Wraps chats.ErrChatNotFound
// so the controller can surface a "not found" message without
// importing internal/chats.
func (a *chatStoreAdapter) ArchiveMeta(ctx context.Context, chatID string) error {
	if a == nil || a.s == nil {
		return fmt.Errorf("metamode.ArchiveMeta: nil store")
	}
	if err := a.s.Archive(ctx, chatID); err != nil {
		if errors.Is(err, chats.ErrChatNotFound) {
			return fmt.Errorf("metamode.ArchiveMeta: chat %q not found", chatID)
		}
		return fmt.Errorf("metamode.ArchiveMeta: %w", err)
	}
	return nil
}

// WithLock acquires the per-chat singleton lock through the underlying
// chats.Store, translating chats.ErrChatBusy into metamode.ErrChatBusy
// so the controller (and TUI) can use errors.Is(err,
// metamode.ErrChatBusy) without importing internal/chats.
func (a *chatStoreAdapter) WithLock(ctx context.Context, chatID string, fn func(context.Context) error) error {
	if a == nil || a.s == nil {
		return fmt.Errorf("metamode.WithLock: nil store")
	}
	err := a.s.WithLock(ctx, chatID, fn)
	if err != nil && errors.Is(err, chats.ErrChatBusy) {
		return fmt.Errorf("%w: %v", ErrChatBusy, err)
	}
	return err
}

// chatHandle binds a chat row to the operations the controller calls
// on it. Persistence goes through *chats.Store so the SQLite row stays
// authoritative; the in-memory row.ClaudeSessionID is updated to match.
type chatHandle struct {
	ctx   context.Context
	store *chats.Store
	row   *chats.Chat
}

func (h *chatHandle) ID() string              { return h.row.ID }
func (h *chatHandle) AppID() string           { return h.row.AppID }
func (h *chatHandle) Room() string            { return h.row.Room }
func (h *chatHandle) ScopeKey() string        { return h.row.ScopeKey }
func (h *chatHandle) Title() string           { return h.row.Title }
func (h *chatHandle) UpdatedAt() time.Time    { return h.row.UpdatedAt }
func (h *chatHandle) ClaudeSessionID() string { return h.row.ClaudeSessionID }

func (h *chatHandle) SetClaudeSessionID(id string) error {
	if err := h.store.SetClaudeSessionID(h.ctx, h.row.ID, id); err != nil {
		return err
	}
	h.row.ClaudeSessionID = id
	return nil
}

func (h *chatHandle) AppendMessage(role, text string) error {
	_, err := h.store.AppendMessage(h.ctx, h.row.ID, role, text, nil)
	return err
}

// FirstUserMessage reads the transcript and returns the content of
// the first "user"-role message. Empty string + nil error when no
// user turn has been recorded yet. Loads all messages today because
// chats.Store.Transcript has no role filter and meta chats are
// short — if this ever becomes hot, add a role-filtered query.
func (h *chatHandle) FirstUserMessage() (string, error) {
	msgs, err := h.store.Transcript(h.ctx, h.row.ID, 0)
	if err != nil {
		return "", fmt.Errorf("metamode.FirstUserMessage: %w", err)
	}
	for _, m := range msgs {
		if m.Role == "user" {
			return m.Content, nil
		}
	}
	return "", nil
}

// ─── AgentCaller adapter ────────────────────────────────────────────────────

// NewAgentCallerAdapter returns an AgentCaller that dispatches to
// host.AgentAskWithMCPHandler. See the package doc above for the
// impedance-mismatch notes.
func NewAgentCallerAdapter() AgentCaller { return &agentAdapter{} }

type agentAdapter struct{}

func (a *agentAdapter) Ask(ctx context.Context, in AskInput) (AskOutput, error) {
	// Materialise the rendered prompt to a tempfile so the handler
	// can read it back via prompt_path. We control both ends so we
	// can pick a stable, expr-free template body (the handler runs
	// expr.Render on the file, but our content has no `{{...}}`
	// placeholders so render is an identity transform).
	//
	// The write/cleanup mechanics are shared with the per-call
	// `agent:` path in host.agent.ask_with_mcp via
	// host.WritePromptTempFile; the composition step (prepending
	// system prompt to user text) stays here because it is a
	// metamode-specific semantic decision.
	// Compose the agent's system prompt + user message, then wrap the
	// whole thing in pongo2's `{% verbatim %}…{% endverbatim %}` block
	// so the downstream host.agent.ask_with_mcp's mandatory pongo2
	// render pass treats the body as literal text — not a template.
	//
	// The body routinely contains pongo2 syntax (the system prompt
	// documents `{% if world.X %}…{% endif %}` patterns; the user's
	// free-form message often pastes YAML view bodies or expressions
	// straight from the story being edited). Without the verbatim
	// wrap, any embedded `{{`, `{%`, or even a multi-line string
	// inside what pongo2's lexer thinks is a string literal crashes
	// render with a parser/lexer error. The wrap is the canonical
	// fix — pongo2's `verbatim` tag is the language's "treat this
	// region as plain text" escape hatch.
	//
	// Limitation: pongo2/v6 doesn't support named-verbatim
	// (`{% verbatim NAME %}…{% endverbatim NAME %}`) so if the body
	// happens to contain a literal `{% endverbatim %}` token it
	// would terminate the block early. That's vanishingly unlikely
	// in metamode chat content (the operator would have to be
	// literally discussing pongo2 verbatim syntax); not worth
	// guarding for. If it ever comes up, escape the body's `{% e`
	// runs before wrapping.
	body := "{% verbatim %}" + composePromptBody(in.SystemPrompt, in.UserMessage) + "{% endverbatim %}"
	promptPath, cleanup, err := host.WritePromptTempFile(body)
	if err != nil {
		return AskOutput{}, fmt.Errorf("metamode.AgentAdapter: %w", err)
	}
	defer cleanup()

	args := map[string]any{
		"prompt_path": promptPath,
		// Empty args map → no template variables to substitute.
		"args": map[string]any{},
		// Opt into streaming so each tool-use / assistant chunk lands
		// in the slog trace in real time (see
		// internal/host/agent_ask_with_mcp.go's stream-json branch).
		// Only metamode sets this — every other agent caller still
		// uses the buffered "text" path.
		"output_format": "stream-json",
	}
	if in.Cwd != "" {
		args["working_dir"] = expandCwd(in.Cwd)
	}
	// We surface the tool allowlist for visibility, even though the
	// handler today does not gate by name. Listed as a no-op note
	// for a future harness change.
	if len(in.ToolAllowlist) > 0 {
		args["__meta_tool_allowlist"] = append([]string(nil), in.ToolAllowlist...)
	}
	// Attach the studio MCP server(s) the controller scoped to the story
	// tree. The handler materialises this into a --mcp-config file for the
	// duration of the call. Empty/nil → no --mcp-config (the stub path).
	if len(in.MCPServers) > 0 {
		args["mcp_servers"] = in.MCPServers
	}
	// Thread the per-chat claude session id so turns share Claude-side
	// memory. The handler mints one when this is empty (e.g. first turn
	// of a fresh chat) and returns the resolved id in Data, which we
	// pluck out below and surface to Controller.Send for persistence.
	if in.ClaudeSessionID != "" {
		args["claude_session_id"] = in.ClaudeSessionID
	}

	res, err := host.AgentAskWithMCPHandler(ctx, args)
	if err != nil {
		return AskOutput{}, fmt.Errorf("metamode.AgentAdapter: handler: %w", err)
	}
	if res.Error != "" {
		return AskOutput{}, fmt.Errorf("metamode.AgentAdapter: %s", res.Error)
	}
	// Strip source-color sentinels: the controller persists Reply to
	// the meta chat and re-feeds it to claude on subsequent turns,
	// where sentinels would leak into the LLM prompt. The display
	// path for meta-mode replies is handled separately by the meta
	// stream observer, which can re-wrap if it wants the warm bg.
	stdout, _ := res.Data["stdout"].(string)
	stdout = sourcecolor.Strip(stdout)
	newSID, _ := res.Data["claude_session_id"].(string)
	if newSID == "" {
		// Fallback: keep whatever we had so Controller.Send's
		// "did the id change?" check stays a no-op rather than
		// clobbering a real id with empty.
		newSID = in.ClaudeSessionID
	}
	return AskOutput{
		Reply:              stdout,
		NewClaudeSessionID: newSID,
	}, nil
}

// composePromptBody prepends the agent's system prompt to the user
// message with a clear separator. Empty system prompt is allowed —
// the result is just the user text.
func composePromptBody(systemPrompt, userMessage string) string {
	sp := strings.TrimSpace(systemPrompt)
	if sp == "" {
		return userMessage
	}
	return sp + "\n\n---\n\n" + userMessage
}

// expandCwd resolves "${ENV}" prefixes the YAML loader leaves on
// MetaModeDef.Cwd. The agents registry already env-expands
// DefaultCwd, but mode.Cwd lands here in raw form, so we expand it on
// the way to the handler.
func expandCwd(cwd string) string {
	expanded := os.ExpandEnv(cwd)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	// Relative cwd is forwarded as-is — the handler resolves it
	// against its own resolvePromptPath rules.
	return expanded
}

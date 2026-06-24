package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/chats"
	"kitsoki/internal/tui/blocks"
)

const chatShowMessageLimit = 5

// ChatCommand implements read-only chat transcript inspection for async work.
// It gives queued/dispatching/failed chat drives a TUI reacquire path comparable
// to web/runstatus chat.show without attaching to a tmux PTY.
type ChatCommand struct{}

func (ChatCommand) Name() string { return "/chat" }

func (ChatCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if m.chatStore == nil {
		return r.SlashOutput("(chat: no chat store wired - pass --db for async chat context)"), m, nil
	}
	if len(args) == 0 {
		return r.SlashOutput("(chat: usage - /chat show <chat-id>)"), m, nil
	}
	if args[0] == "show" {
		if len(args) < 2 {
			return r.SlashOutput("(chat show: usage - /chat show <chat-id>)"), m, nil
		}
		return renderChatShowBlock(m, args[1]), m, nil
	}
	if len(args) == 1 {
		return renderChatShowBlock(m, args[0]), m, nil
	}
	return r.SlashOutput(fmt.Sprintf("(chat: unknown subcommand %q - try /chat show <chat-id>)", args[0])), m, nil
}

func renderChatShowBlock(m RootModel, chatID string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	ctx := context.Background()
	chat, err := m.chatStore.Get(ctx, chatID)
	if err != nil {
		return r.SlashOutput(fmt.Sprintf("(chat show: %v)", err))
	}
	msgs, err := m.chatStore.Transcript(ctx, chatID, 0)
	if err != nil {
		return r.SlashOutput(fmt.Sprintf("(chat show: %v)", err))
	}

	var pty *chats.PtySession
	if got, err := m.chatStore.GetPTY(ctx, chatID); err == nil {
		pty = got
	}

	var sb strings.Builder
	title := chat.Title
	if title == "" {
		title = chat.ID
	}
	sb.WriteString(r.SlashOutput("chat context"))
	sb.WriteByte('\n')
	sb.WriteString(fmt.Sprintf("  title: %s\n", title))
	sb.WriteString(fmt.Sprintf("  id: %s\n", chat.ID))
	sb.WriteString(fmt.Sprintf("  status: %s\n", chat.Status))
	if scope := chats.DisplayScopeKey(chat.ScopeKey); scope != "" {
		sb.WriteString(fmt.Sprintf("  scope: %s\n", scope))
	}
	if chat.SessionID != "" {
		sb.WriteString(fmt.Sprintf("  session: %s\n", chat.SessionID))
	}
	if pty != nil {
		sb.WriteString(fmt.Sprintf("  tmux: %s (%s)\n", pty.TmuxSession, pty.Mode))
	}
	sb.WriteString(fmt.Sprintf("  messages: %d", len(msgs)))
	if len(msgs) > chatShowMessageLimit {
		sb.WriteString(fmt.Sprintf(" total, showing last %d", chatShowMessageLimit))
	}
	sb.WriteByte('\n')

	start := len(msgs) - chatShowMessageLimit
	if start < 0 {
		start = 0
	}
	for _, msg := range msgs[start:] {
		sb.WriteString(fmt.Sprintf("  #%d %s: %s\n", msg.Seq, msg.Role, chatMessagePreview(msg.Content)))
	}
	if len(msgs) == 0 {
		sb.WriteString("  (no messages yet)\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func chatMessagePreview(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	const limit = 180
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "..."
}

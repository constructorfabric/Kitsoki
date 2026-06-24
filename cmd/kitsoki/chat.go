// chat.go — implements `kitsoki chat ...` subcommands.
//
// Chats are persistent conversation threads within a room. They are global
// (not session-scoped) and outlive individual sessions.
//
// `kitsoki chat continue` drives one turn synchronously by invoking the
// chat-aware host.agent.talk handler. The chat-singleton lock guarantees
// that a TUI driving the same chat will not race; on lock contention the
// command exits 75 (EX_TEMPFAIL) so wrappers can back off and retry.
//
// Output is JSON to stdout for orchestrator-friendliness; human-readable
// summaries are written to stderr where applicable.
// On chats.ErrChatBusy, the command exits with EX_TEMPFAIL=75.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/render/sourcecolor"
)

// errTempFail is a sentinel returned from RunE when the operation should exit
// with EX_TEMPFAIL=75. main() detects this with errors.Is and translates it
// to the correct exit code.  Returning a sentinel rather than calling
// os.Exit(75) directly keeps the cobra command testable in-process.
var errTempFail = errors.New("EX_TEMPFAIL")

// IsTempFail reports whether err originated from a chat-busy / session-busy
// path that should map to BSD sysexits EX_TEMPFAIL=75.  Used by main() and by
// in-process tests of `kitsoki chat continue` / `kitsoki session continue`.
func IsTempFail(err error) bool {
	return errors.Is(err, errTempFail)
}

// chatCmd is the parent of chat subcommands.
func chatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Manage persistent agent-room chat threads",
		Long: `Chats are persistent conversation threads within a room. They are global
(not session-scoped) and survive process restarts.

Subcommands:
  kitsoki chat list      [--db <path>] [--app <id>] [--room <name>] [--scope <key>] [--all-status]
  kitsoki chat new       [--db <path>] --app <id> --room <name> [--scope <key>] [--title "..."]
  kitsoki chat show      [--db <path>] <chat-id> [--since <seq>] [--format json|markdown]
  kitsoki chat continue  [--db <path>] <chat-id> --raw "<question>" [--working-dir <path>]
  kitsoki chat fork      [--db <path>] <chat-id> [--title "..."]
  kitsoki chat archive   [--db <path>] <chat-id>
  kitsoki chat unlock    [--db <path>] <chat-id> --force
  kitsoki chat queue     [add|list|dispatch|dismiss] (see 'kitsoki chat queue --help')
  kitsoki chat attach    [--db <path>] <chat-id> [--workspace <path>] [--permission-mode <mode>]
  kitsoki chat detach    [--db <path>] <chat-id> --mode background|headless|stop
  kitsoki chat gc        [--db <path>]

Exit codes:
  0   success
  1   generic error
  75  EX_TEMPFAIL: another process holds the chat lock. Back off and retry.`,
	}
	cmd.AddCommand(chatListCmd())
	cmd.AddCommand(chatNewCmd())
	cmd.AddCommand(chatShowCmd())
	cmd.AddCommand(chatContinueCmd())
	cmd.AddCommand(chatForkCmd())
	cmd.AddCommand(chatArchiveCmd())
	cmd.AddCommand(chatUnlockCmd())
	cmd.AddCommand(chatQueueCmd())
	cmd.AddCommand(chatAttachCmd())
	cmd.AddCommand(chatDetachCmd())
	cmd.AddCommand(chatGCCmd())
	return cmd
}

// ─── chat list ────────────────────────────────────────────────────────────────

func chatListCmd() *cobra.Command {
	var (
		dbPath    string
		appID     string
		room      string
		scopeKey  string
		allStatus bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chat threads, optionally filtered by app, room, or scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			ctx := cmd.Context()
			all, err := cs.List(ctx, appID, room, scopeKey)
			if err != nil {
				return fmt.Errorf("list chats: %w", err)
			}

			rows := make([]map[string]any, 0, len(all))
			for _, c := range all {
				if !allStatus && c.Status == string(chats.ChatArchived) {
					continue
				}
				rows = append(rows, chatView(&c))
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"chats": rows})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&appID, "app", "", "filter by app ID")
	cmd.Flags().StringVar(&room, "room", "", "filter by room name")
	cmd.Flags().StringVar(&scopeKey, "scope", "", "filter by scope key")
	cmd.Flags().BoolVar(&allStatus, "all-status", false, "include archived chats")
	return cmd
}

// ─── chat new ─────────────────────────────────────────────────────────────────

func chatNewCmd() *cobra.Command {
	var (
		dbPath   string
		appID    string
		room     string
		scopeKey string
		title    string
	)
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new chat thread",
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				// Defensive: replace any internal '/' in the room with '.'
				// so the default title doesn't end up with ambiguous slashes
				// like "app/sub/room/scope". The actual room argument flowing
				// into the DB is unchanged — only the human-readable default
				// title is rewritten.
				roomForTitle := strings.ReplaceAll(room, "/", ".")
				title = appID + "/" + roomForTitle
				if scopeKey != "" {
					title += "/" + scopeKey
				}
			}

			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			c, err := cs.Create(cmd.Context(), appID, room, scopeKey, title)
			if err != nil {
				return fmt.Errorf("create chat: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), chatView(c))
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&appID, "app", "", "app ID (required)")
	cmd.Flags().StringVar(&room, "room", "", "room name (required)")
	cmd.Flags().StringVar(&scopeKey, "scope", "", "scope key (e.g. ticket ID)")
	cmd.Flags().StringVar(&title, "title", "", "chat title (default: app/room[/scope])")
	_ = cmd.MarkFlagRequired("app")
	_ = cmd.MarkFlagRequired("room")
	return cmd
}

// ─── chat show ────────────────────────────────────────────────────────────────

func chatShowCmd() *cobra.Command {
	var (
		dbPath   string
		sinceSeq int
		format   string
	)
	cmd := &cobra.Command{
		Use:   "show <chat-id>",
		Short: "Show a chat's metadata and transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]

			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			ctx := cmd.Context()
			c, err := cs.Get(ctx, chatID)
			if errors.Is(err, chats.ErrChatNotFound) {
				return fmt.Errorf("chat %q not found", chatID)
			}
			if err != nil {
				return fmt.Errorf("get chat: %w", err)
			}

			msgs, err := cs.Transcript(ctx, chatID, sinceSeq)
			if err != nil {
				return fmt.Errorf("transcript: %w", err)
			}

			if format == "markdown" {
				return renderMarkdown(cmd, c, msgs)
			}

			msgsView := make([]map[string]any, 0, len(msgs))
			for i := range msgs {
				msgsView = append(msgsView, messageView(&msgs[i]))
			}
			out := chatView(c)
			out["messages"] = msgsView
			return writeJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().IntVar(&sinceSeq, "since", 0, "only return messages with seq >= this value")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json|markdown")
	return cmd
}

// ─── chat continue ────────────────────────────────────────────────────────────

func chatContinueCmd() *cobra.Command {
	var (
		dbPath     string
		raw        string
		workingDir string
	)
	cmd := &cobra.Command{
		Use:   "continue <chat-id>",
		Short: "Drive one chat turn synchronously: append <raw> as the user message and run Claude",
		Long: `Drive one chat turn synchronously. Appends the supplied --raw text as the
user message, runs ` + "`" + `claude -p --session-id <chat.claude_session_id>` + "`" + ` (allocating
a session ID if the chat has none), appends the assistant reply, and prints the
answer to stdout.

The per-chat singleton lock is acquired for the duration of the turn, so a TUI
or other driver attached to the same chat will not race. If another driver
holds the lock, this command exits 75 (EX_TEMPFAIL) without writing anything.

Examples:
  kitsoki chat continue ABC123 --raw "what does Foo do?"
  kitsoki chat continue ABC123 --raw "$(cat question.txt)" --working-dir /repo

Exit codes:
  0   success
  1   generic error (chat not found, claude binary missing, etc.)
  75  EX_TEMPFAIL: another process holds the chat lock`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]
			if raw == "" {
				return fmt.Errorf("--raw is required")
			}

			// Open the underlying session store (so we can keep the *sql.DB
			// across the chat-store + handler invocation) and build a chats.Store
			// over it.
			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			rawStore, err := chats.NewStore(s.DB())
			if err != nil {
				return fmt.Errorf("open chat store: %w", err)
			}

			// Verify the chat exists up-front for a clearer error message.
			ctx := cmd.Context()
			if _, getErr := rawStore.Get(ctx, chatID); getErr != nil {
				if errors.Is(getErr, chats.ErrChatNotFound) {
					return fmt.Errorf("chat %q not found", chatID)
				}
				return fmt.Errorf("get chat: %w", getErr)
			}

			// Wire the chat store into context so AgentConverseHandler picks it up.
			adapter := chathost.NewAdapter(rawStore)
			handlerCtx := host.WithChatStore(ctx, adapter)

			res, hostErr := host.AgentConverseHandler(handlerCtx, map[string]any{
				"question":    raw,
				"chat_id":     chatID,
				"working_dir": workingDir,
			})
			if hostErr != nil {
				return fmt.Errorf("agent talk: %w", hostErr)
			}

			// ErrChatBusy surfaces as Result.Error. Translate to EX_TEMPFAIL
			// so wrappers (loop.py et al.) can back off and retry.  We return
			// a sentinel (errTempFail) rather than calling os.Exit so the
			// command stays testable in-process; main() inspects the returned
			// error and exits 75 there.
			if res.Error != "" {
				if isChatBusyError(res.Error) {
					fmt.Fprintln(cmd.ErrOrStderr(), res.Error)
					return errTempFail
				}
				return fmt.Errorf("%s", res.Error)
			}

			// Print the answer to stdout for shell consumption; the JSON
			// summary goes through writeJSON like the other subcommands so
			// orchestrators can parse it. Strip source-color sentinels so
			// shell consumers see plain text — sentinels are a render
			// concern, not a wire format.
			answer, _ := res.Data["answer"].(string)
			out := map[string]any{
				"chat_id": chatID,
				"answer":  sourcecolor.Strip(answer),
			}
			if cs, ok := res.Data["claude_session_id"].(string); ok {
				out["claude_session_id"] = cs
			}
			if seq, ok := res.Data["transcript_seq"].(int); ok {
				out["transcript_seq"] = seq
			}
			return writeJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&raw, "raw", "", "the user message text to send (required)")
	cmd.Flags().StringVar(&workingDir, "working-dir", "", "cwd passed to the claude subprocess (scopes tool access)")
	_ = cmd.MarkFlagRequired("raw")
	return cmd
}

// isChatBusyError detects whether a Result.Error originated from a chat-lock
// contention. We match on the chats.ErrChatBusy message because the host-side
// error chain has been flattened to a string by the time it lands in
// Result.Error.
func isChatBusyError(msg string) bool {
	return strings.Contains(msg, "chats: chat busy")
}

// ─── chat fork ────────────────────────────────────────────────────────────────

func chatForkCmd() *cobra.Command {
	var (
		dbPath string
		title  string
	)
	cmd := &cobra.Command{
		Use:   "fork <chat-id>",
		Short: "Fork a chat: copy messages to a new chat with a fresh Claude session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]

			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			fork, err := cs.Fork(cmd.Context(), chatID, title)
			if errors.Is(err, chats.ErrChatNotFound) {
				return fmt.Errorf("chat %q not found", chatID)
			}
			if err != nil {
				return fmt.Errorf("fork chat: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), chatView(fork))
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&title, "title", "", "title for the fork (default: '<parent title> (fork)')")
	return cmd
}

// ─── chat archive ─────────────────────────────────────────────────────────────

func chatArchiveCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "archive <chat-id>",
		Short: "Archive a chat (soft-delete: excluded from list by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]

			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			err = cs.Archive(cmd.Context(), chatID)
			if errors.Is(err, chats.ErrChatNotFound) {
				return fmt.Errorf("chat %q not found", chatID)
			}
			if err != nil {
				return fmt.Errorf("archive chat: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"archived": true,
				"chat_id":  chatID,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	return cmd
}

// ─── chat unlock ──────────────────────────────────────────────────────────────

func chatUnlockCmd() *cobra.Command {
	var (
		dbPath string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "unlock <chat-id>",
		Short: "Force-remove a stuck chat lock (operator escape hatch)",
		Long: `Delete the lock row for a chat, regardless of which process holds it.
This is an operator escape hatch for situations where a process died without
releasing the lock and automatic stale-lock reaping has not yet fired.

The --force flag is required to prevent accidental use.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !force {
				return fmt.Errorf("--force is required for 'chat unlock' to prevent accidental use")
			}
			chatID := args[0]

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			_, err = s.DB().ExecContext(cmd.Context(),
				`DELETE FROM chat_locks WHERE chat_id = ?`, chatID)
			if err != nil {
				return fmt.Errorf("unlock chat: %w", err)
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "chat lock removed for %s\n", chatID)
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"unlocked": true,
				"chat_id":  chatID,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().BoolVar(&force, "force", false, "required: confirm you want to forcibly remove the lock")
	return cmd
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// openChatStore opens the session store and wraps it in a chats.Store.
// Returns a cleanup function that must be deferred.
func openChatStore(dbPath string) (*chats.Store, func(), error) {
	s, err := openSessionStore(dbPath)
	if err != nil {
		return nil, func() {}, err
	}
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		_ = s.Close()
		return nil, func() {}, fmt.Errorf("open chat store: %w", err)
	}
	cleanup := func() { _ = s.Close() }
	return cs, cleanup, nil
}

// chatView projects a Chat into a JSON-friendly map.
func chatView(c *chats.Chat) map[string]any {
	m := map[string]any{
		"id":             c.ID,
		"app_id":         c.AppID,
		"room":           c.Room,
		"scope_key":      c.ScopeKey,
		"title":          c.Title,
		"status":         c.Status,
		"created_at":     c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"updated_at":     c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"last_active_at": c.LastActiveAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if c.ClaudeSessionID != "" {
		m["claude_session_id"] = c.ClaudeSessionID
	}
	if c.ParentChatID != "" {
		m["parent_chat_id"] = c.ParentChatID
	}
	if c.SessionID != "" {
		m["session_id"] = c.SessionID
	}
	return m
}

// messageView projects a Message into a JSON-friendly map.
func messageView(msg *chats.Message) map[string]any {
	m := map[string]any{
		"chat_id":    msg.ChatID,
		"seq":        msg.Seq,
		"role":       msg.Role,
		"content":    msg.Content,
		"created_at": msg.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if msg.Metadata != nil {
		m["metadata"] = msg.Metadata
	}
	return m
}

// renderMarkdown writes a human-readable transcript to stdout.
func renderMarkdown(cmd *cobra.Command, c *chats.Chat, msgs []chats.Message) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "# %s\n\n", c.Title)
	fmt.Fprintf(w, "**App:** %s  **Room:** %s  **Status:** %s\n\n", c.AppID, c.Room, c.Status)
	fmt.Fprintf(w, "---\n\n")
	for _, m := range msgs {
		role := m.Role
		switch role {
		case "user":
			role = "**You**"
		case "assistant":
			role = "**Claude**"
		case "system":
			role = "_System_"
		case "tool":
			role = "_Tool_"
		}
		fmt.Fprintf(w, "%s (seq %d):\n\n%s\n\n---\n\n", role, m.Seq, m.Content)
	}
	return nil
}

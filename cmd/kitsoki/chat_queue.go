// chat_queue.go — implements `kitsoki chat queue ...` subcommands.
//
// The queue is the chat_input_queue table (see
// docs/proposals/claude-code-sessions-proposal.md §6.3): a FIFO of
// pending turn requests against a chat. Subcommands here are the
// power-user / scripting surface; state-machine apps use
// host.chat.drive instead.
//
// Verbs:
//
//	kitsoki chat queue add      <chat-id> --payload "..." [--transport ...] [--thread ...] [--actor ...] [--correlation-id ...]
//	kitsoki chat queue list     <chat-id> [--status pending,done,failed,...] [--limit N]
//	kitsoki chat queue dispatch <drive-id> [--working-dir <path>]
//	kitsoki chat queue dismiss  <drive-id>
//
// All output is JSON on stdout. Errors print to stderr; a chat-busy
// dispatch exits 75 (EX_TEMPFAIL) so wrappers can back off and retry.
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

// chatQueueCmd is the parent of `kitsoki chat queue *` subcommands.
func chatQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and drive the per-chat input queue",
		Long: `Manage the per-chat input queue — the FIFO of pending turn requests.

Subcommands:
  kitsoki chat queue add       <chat-id> --payload "..." [--transport <id>] [--thread <s>] [--actor <s>] [--correlation-id <s>]
  kitsoki chat queue list      <chat-id> [--status <list>] [--limit <n>]
  kitsoki chat queue dispatch  <drive-id> [--working-dir <path>]
  kitsoki chat queue dismiss   <drive-id>

Exit codes:
  0   success
  1   generic error
  75  EX_TEMPFAIL: the chat lock is held by another process (dispatch only).`,
	}
	cmd.AddCommand(chatQueueAddCmd())
	cmd.AddCommand(chatQueueListCmd())
	cmd.AddCommand(chatQueueDispatchCmd())
	cmd.AddCommand(chatQueueDismissCmd())
	return cmd
}

// ─── kitsoki chat queue add ────────────────────────────────────────────────────

func chatQueueAddCmd() *cobra.Command {
	var (
		dbPath        string
		payload       string
		transport     string
		thread        string
		actor         string
		correlationID string
	)
	cmd := &cobra.Command{
		Use:   "add <chat-id>",
		Short: "Enqueue a pending turn request against the chat",
		Long: `Insert a new pending drive into the chat input queue. The drive is NOT
run by this command — use 'kitsoki chat queue dispatch <drive-id>' to run it.

The transport flag identifies which surface originated the drive: 'tui',
'jira', 'bitbucket', 'mcp', 'job', 'state_machine'. Defaults to 'cli'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]
			if strings.TrimSpace(payload) == "" {
				return fmt.Errorf("--payload is required")
			}
			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			// Confirm the chat exists for a clearer error.
			ctx := cmd.Context()
			if _, getErr := cs.Get(ctx, chatID); getErr != nil {
				if errors.Is(getErr, chats.ErrChatNotFound) {
					return fmt.Errorf("chat %q not found", chatID)
				}
				return fmt.Errorf("get chat: %w", getErr)
			}

			drive, err := cs.Enqueue(ctx, chats.EnqueueOptions{
				ChatID:        chatID,
				Transport:     chats.DriveTransport(transport),
				Thread:        thread,
				Actor:         actor,
				CorrelationID: correlationID,
				Payload:       payload,
			})
			if err != nil {
				return fmt.Errorf("enqueue: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), driveView(drive))
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&payload, "payload", "", "user-message text to send to claude on this turn (required)")
	cmd.Flags().StringVar(&transport, "transport", "cli", "originating surface id (tui|jira|bitbucket|mcp|job|state_machine|cli)")
	cmd.Flags().StringVar(&thread, "thread", "", "optional correlation thread")
	cmd.Flags().StringVar(&actor, "actor", "", "optional actor id")
	cmd.Flags().StringVar(&correlationID, "correlation-id", "", "optional caller-side correlation token")
	_ = cmd.MarkFlagRequired("payload")
	return cmd
}

// ─── kitsoki chat queue list ───────────────────────────────────────────────────

func chatQueueListCmd() *cobra.Command {
	var (
		dbPath   string
		statuses []string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list <chat-id>",
		Short: "List drives for a chat in FIFO order",
		Long: `List drives queued against a chat, in received_at order.

The --status flag accepts one or more comma-separated statuses:
pending, dispatching, done, failed, dismissed. Default: all statuses.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chatID := args[0]
			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			filter := chats.ListDrivesFilter{Limit: limit}
			for _, s := range statuses {
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				filter.Statuses = append(filter.Statuses, chats.DriveStatus(s))
			}
			drives, err := cs.ListDrives(cmd.Context(), chatID, filter)
			if err != nil {
				return fmt.Errorf("list drives: %w", err)
			}
			rows := make([]map[string]any, 0, len(drives))
			for i := range drives {
				rows = append(rows, driveView(&drives[i]))
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"chat_id": chatID,
				"drives":  rows,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "filter by status (comma-separated): pending,dispatching,done,failed,dismissed")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap result count; 0 = no limit")
	return cmd
}

// ─── kitsoki chat queue dispatch ───────────────────────────────────────────────

func chatQueueDispatchCmd() *cobra.Command {
	var (
		dbPath     string
		workingDir string
	)
	cmd := &cobra.Command{
		Use:   "dispatch <drive-id>",
		Short: "Promote and run a pending drive (claims pending → dispatching, runs the turn, marks terminal)",
		Long: `Dispatch a specific drive: acquire the chat lock, claim the drive,
run claude headlessly with the drive's payload, and mark the row done or
failed. If another process holds the chat lock the command exits 75
(EX_TEMPFAIL) and the drive stays pending.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			driveID := args[0]

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			rawStore, err := chats.NewStore(s.DB())
			if err != nil {
				return fmt.Errorf("open chat store: %w", err)
			}
			adapter := chathost.NewAdapter(rawStore)

			res, dispErr := host.DispatchDrive(cmd.Context(), adapter, driveID, workingDir)
			if errors.Is(dispErr, host.ErrChatBusy) {
				fmt.Fprintln(cmd.ErrOrStderr(), dispErr.Error())
				return errTempFail
			}
			if errors.Is(dispErr, host.ErrDriveNotFound) {
				return fmt.Errorf("drive %q not found", driveID)
			}
			if errors.Is(dispErr, host.ErrDriveStateMismatch) {
				return fmt.Errorf("drive %q is not pending: %v", driveID, dispErr)
			}
			if dispErr != nil {
				return fmt.Errorf("dispatch: %w", dispErr)
			}

			out := map[string]any{
				"drive_id": res.DriveID,
				"chat_id":  res.ChatID,
				"status":   res.Status,
			}
			if res.Status == "done" {
				out["result_seq"] = res.ResultSeq
				// Strip source-color sentinels from the JSON wire output —
				// shell consumers see plain text.
				out["answer"] = sourcecolor.Strip(res.Answer)
				if res.ClaudeSessionID != "" {
					out["claude_session_id"] = res.ClaudeSessionID
				}
			} else {
				out["error"] = res.ErrorMessage
			}
			return writeJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&workingDir, "working-dir", "", "cwd passed to the claude subprocess (scopes tool access)")
	return cmd
}

// ─── kitsoki chat queue dismiss ────────────────────────────────────────────────

func chatQueueDismissCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "dismiss <drive-id>",
		Short: "Mark a pending drive as dismissed; the drive will not run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			driveID := args[0]
			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := cs.MarkDriveDismissed(cmd.Context(), driveID); err != nil {
				if errors.Is(err, chats.ErrDriveNotFound) {
					return fmt.Errorf("drive %q not found", driveID)
				}
				if errors.Is(err, chats.ErrDriveStateMismatch) {
					return fmt.Errorf("drive %q is not pending: %v", driveID, err)
				}
				return fmt.Errorf("dismiss: %w", err)
			}
			drive, err := cs.GetDrive(cmd.Context(), driveID)
			if err != nil {
				return fmt.Errorf("post-dismiss get: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), driveView(drive))
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	return cmd
}

// ─── helpers ───────────────────────────────────────────────────────────────────

// driveView projects a Drive into a JSON-friendly map. Optional
// timestamps are omitted when nil so the output is shaped to the
// drive's actual lifecycle stage.
func driveView(d *chats.Drive) map[string]any {
	m := map[string]any{
		"drive_id":    d.DriveID,
		"chat_id":     d.ChatID,
		"transport":   string(d.Transport),
		"status":      string(d.Status),
		"payload":     d.Payload,
		"received_at": d.ReceivedAt.UTC().Format("2006-01-02T15:04:05.000000Z"),
	}
	if d.Thread != "" {
		m["thread"] = d.Thread
	}
	if d.Actor != "" {
		m["actor"] = d.Actor
	}
	if d.CorrelationID != "" {
		m["correlation_id"] = d.CorrelationID
	}
	if d.DispatchedAt != nil {
		m["dispatched_at"] = d.DispatchedAt.UTC().Format("2006-01-02T15:04:05.000000Z")
	}
	if d.CompletedAt != nil {
		m["completed_at"] = d.CompletedAt.UTC().Format("2006-01-02T15:04:05.000000Z")
	}
	if d.ResultSeq != nil {
		m["result_seq"] = *d.ResultSeq
	}
	if d.ErrorMessage != "" {
		m["error_message"] = d.ErrorMessage
	}
	return m
}

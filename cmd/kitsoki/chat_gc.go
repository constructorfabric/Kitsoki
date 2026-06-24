// chat_gc.go — implements `kitsoki chat gc`, the sweeper that
// removes chat_pty_sessions rows whose tmux session is no longer
// alive (host reboot, tmux server kill, systemd RemoveIPC, etc.).
//
// The proposal §6.6 lists this as a cheap, idempotent step that
// could even run at the start of every kitsoki CLI invocation. v1
// is a manual verb.
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"kitsoki/internal/tmux"
)

func chatGCCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove chat_pty_sessions rows whose tmux session no longer exists",
		Long: `Walk every chat_pty_sessions row on this host, probe tmux for the named
session, and delete rows whose session is gone.

Idempotent; safe to call at any time. Exits 0 even when nothing changed.

Output is a JSON summary of the rows actually removed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, cleanup, err := openChatStore(dbPath)
			if err != nil {
				return err
			}
			defer cleanup()

			tmuxClient, err := tmux.New(tmux.DefaultSocketPath())
			if err != nil {
				return fmt.Errorf("tmux init: %w", err)
			}

			// Cache HasSession-style queries in one cheap
			// ListSessions call so the probe doesn't exec tmux N
			// times for N rows. The ListSessions failure mode (no
			// server running) returns an empty slice, which makes
			// the closure correctly mark every row as dead.
			alive, err := tmuxClient.ListSessions(cmd.Context())
			if err != nil {
				return fmt.Errorf("tmux list-sessions: %w", err)
			}
			aliveSet := make(map[string]struct{}, len(alive))
			for _, name := range alive {
				aliveSet[name] = struct{}{}
			}
			probe := func(name string) bool {
				_, ok := aliveSet[name]
				return ok
			}

			ctx := cmd.Context()
			before, err := cs.ListPTYForHost(ctx)
			if err != nil {
				return fmt.Errorf("list pty rows: %w", err)
			}
			removed, err := cs.GCDeadTmux(ctx, probe)
			if err != nil {
				return fmt.Errorf("gc: %w", err)
			}

			// Identify which rows actually went away so the JSON
			// summary is useful for operators tailing logs. Compute
			// from before-list minus current set.
			after, err := cs.ListPTYForHost(ctx)
			if err != nil {
				return fmt.Errorf("post-list: %w", err)
			}
			afterSet := make(map[string]struct{}, len(after))
			for _, p := range after {
				afterSet[p.ChatID] = struct{}{}
			}
			var removedRows []map[string]any
			for _, p := range before {
				if _, kept := afterSet[p.ChatID]; kept {
					continue
				}
				removedRows = append(removedRows, map[string]any{
					"chat_id":      p.ChatID,
					"tmux_session": p.TmuxSession,
					"mode":         string(p.Mode),
				})
			}

			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"removed_count": removed,
				"removed":       removedRows,
				"alive_count":   len(alive),
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "chat SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	return cmd
}


package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
	inboxmodel "kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
)

func inboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Manage inbox notifications",
	}
	cmd.AddCommand(inboxSyncGitHubCmd())
	return cmd
}

func inboxSyncGitHubCmd() *cobra.Command {
	var (
		dbPath          string
		idFlag          string
		key             string
		repo            string
		includeIssues   bool
		includePRs      bool
		assignee        string
		reviewRequested string
		limit           int
		teleportState   string
	)
	cmd := &cobra.Command{
		Use:   "sync-github",
		Short: "Import GitHub issues and PR review requests into a session inbox",
		Long: `Import GitHub issues assigned to the operator and PRs awaiting review into
the Kitsoki inbox for one persistent session. The command is safe to run from a
poll loop: each GitHub object is inserted once and later runs report it as
skipped.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (key == "") == (idFlag == "") {
				return fmt.Errorf("exactly one of --key or --id must be set")
			}
			if !includeIssues && !includePRs {
				return fmt.Errorf("at least one of --issues or --prs must be enabled")
			}
			if limit <= 0 {
				return fmt.Errorf("--limit must be positive")
			}
			if strings.TrimSpace(teleportState) == "" {
				return fmt.Errorf("--teleport-state must not be empty")
			}

			s, err := openSessionStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			ctx := cmd.Context()
			sid, err := resolveSessionID(ctx, s, key, idFlag)
			if err != nil {
				return err
			}
			js, err := jobs.NewJobStore(s.DB())
			if err != nil {
				return err
			}

			items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
				Repo:            repo,
				IncludeIssues:   includeIssues,
				IncludePRs:      includePRs,
				Assignee:        assignee,
				ReviewRequested: reviewRequested,
				Limit:           limit,
			})
			if err != nil {
				return err
			}

			reportItems := make([]map[string]any, 0, len(items))
			insertedCount := 0
			for _, item := range items {
				n := inboxmodel.NewGitHubNotification(sid, repo, teleportState, item)
				inserted, err := js.InsertExternalNotificationOnce(ctx, n)
				if err != nil {
					return fmt.Errorf("insert notification for %s #%s: %w", item.Kind, item.Number, err)
				}
				if inserted {
					insertedCount++
				}
				reportItems = append(reportItems, map[string]any{
					"id":              n.ID,
					"kind":            item.Kind,
					"number":          item.Number,
					"title":           item.Title,
					"url":             item.URL,
					"inserted":        inserted,
					"origin_ref":      n.OriginRef,
					"teleport_state":  n.TeleportState,
					"teleport_slots":  n.TeleportSlots,
					"notification_id": n.ID,
				})
			}

			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"ok":         true,
				"session_id": string(sid),
				"fetched":    len(items),
				"inserted":   insertedCount,
				"skipped":    len(items) - insertedCount,
				"items":      reportItems,
			})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "session SQLite database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().StringVar(&idFlag, "id", "", "session ID to receive notifications")
	cmd.Flags().StringVar(&key, "key", "", "external session key transport:thread")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub owner/repo slug; omitted lets gh infer from cwd")
	cmd.Flags().BoolVar(&includeIssues, "issues", true, "import open issues assigned to --assignee")
	cmd.Flags().BoolVar(&includePRs, "prs", true, "import open PRs awaiting --review-requested")
	cmd.Flags().StringVar(&assignee, "assignee", "@me", "GitHub issue assignee filter")
	cmd.Flags().StringVar(&reviewRequested, "review-requested", "@me", "GitHub PR review-requested filter")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum issues and PRs to fetch per query")
	cmd.Flags().StringVar(&teleportState, "teleport-state", "inbox", "state to open when reacquiring imported notifications")
	return cmd
}

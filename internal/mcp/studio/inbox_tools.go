package studio

import (
	"context"
	"fmt"
	"strings"

	"kitsoki/internal/host"
	inboxmodel "kitsoki/internal/inbox"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GitHubInboxSyncArgs is the input to inbox.sync_github.
type GitHubInboxSyncArgs struct {
	Handle          string `json:"handle"`
	Repo            string `json:"repo,omitempty"`
	IncludeIssues   *bool  `json:"include_issues,omitempty"`
	IncludePRs      *bool  `json:"include_prs,omitempty"`
	Assignee        string `json:"assignee,omitempty"`
	ReviewRequested string `json:"review_requested,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	TeleportState   string `json:"teleport_state,omitempty"`
	// IncludeSkipped also returns items that were already present (inserted:false).
	// Off by default: skipped rows duplicate prior syncs and only inflate the
	// payload; the fetched/inserted/skipped counts still report the full picture.
	IncludeSkipped bool `json:"include_skipped,omitempty"`
}

// GitHubInboxSyncResult is the structured result from inbox.sync_github.
type GitHubInboxSyncResult struct {
	OK        bool                  `json:"ok"`
	Handle    string                `json:"handle"`
	SessionID string                `json:"session_id"`
	Fetched   int                   `json:"fetched"`
	Inserted  int                   `json:"inserted"`
	Skipped   int                   `json:"skipped"`
	Items     []GitHubInboxSyncItem `json:"items"`
}

// GitHubInboxSyncItem is one imported or skipped GitHub item.
type GitHubInboxSyncItem struct {
	NotificationID string `json:"notification_id"`
	Kind           string `json:"kind,omitempty"`
	Number         string `json:"number,omitempty"`
	Title          string `json:"title"`
	URL            string `json:"url,omitempty"`
	// Inserted is omitempty: by default only inserted items are returned, so the
	// field is implicitly true; it appears (false) only when include_skipped adds
	// already-present rows.
	Inserted      bool           `json:"inserted,omitempty"`
	OriginRef     string         `json:"origin_ref,omitempty"`
	TeleportState string         `json:"teleport_state,omitempty"`
	TeleportSlots map[string]any `json:"teleport_slots,omitempty"`
}

func (srv *Server) registerInboxTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "inbox.sync_github",
		Description: "Sync assigned GitHub issues and requested PR reviews into an open driving handle's inbox. Uses the native GitHub API, inserts each GitHub object once, and returns fetched/inserted/skipped counts. Returns only newly-inserted items by default (counts still report the full picture); pass include_skipped to also echo already-present rows.",
	}, srv.handleGitHubInboxSync)
}

func (srv *Server) handleGitHubInboxSync(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args GitHubInboxSyncArgs,
) (*mcpsdk.CallToolResult, any, error) {
	out, err := srv.githubInboxSync(ctx, args)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, out, nil
}

func (srv *Server) githubInboxSync(ctx context.Context, args GitHubInboxSyncArgs) (GitHubInboxSyncResult, error) {
	if strings.TrimSpace(args.Handle) == "" {
		return GitHubInboxSyncResult{}, fmt.Errorf("inbox.sync_github: handle is required")
	}
	sh, err := srv.sess.ResolveSession(args.Handle)
	if err != nil {
		return GitHubInboxSyncResult{}, err
	}
	if sh.Runtime == nil || sh.Runtime.jobStore == nil {
		return GitHubInboxSyncResult{}, fmt.Errorf("inbox.sync_github: handle %q has no inbox store; open a driving session with --db-backed runtime", args.Handle)
	}
	includeIssues := true
	if args.IncludeIssues != nil {
		includeIssues = *args.IncludeIssues
	}
	includePRs := true
	if args.IncludePRs != nil {
		includePRs = *args.IncludePRs
	}
	if !includeIssues && !includePRs {
		return GitHubInboxSyncResult{}, fmt.Errorf("inbox.sync_github: at least one of include_issues or include_prs must be true")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}
	teleportState := strings.TrimSpace(args.TeleportState)
	if teleportState == "" {
		teleportState = "inbox"
	}

	items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo:            args.Repo,
		IncludeIssues:   includeIssues,
		IncludePRs:      includePRs,
		Assignee:        args.Assignee,
		ReviewRequested: args.ReviewRequested,
		Limit:           limit,
	})
	if err != nil {
		return GitHubInboxSyncResult{}, err
	}
	out := GitHubInboxSyncResult{
		OK:        true,
		Handle:    sh.Key,
		SessionID: string(sh.SID),
		Fetched:   len(items),
		Items:     make([]GitHubInboxSyncItem, 0, len(items)),
	}
	for _, item := range items {
		n := inboxmodel.NewGitHubNotification(sh.SID, args.Repo, teleportState, item)
		inserted, err := sh.Runtime.jobStore.InsertExternalNotificationOnce(ctx, n)
		if err != nil {
			return GitHubInboxSyncResult{}, fmt.Errorf("inbox.sync_github: insert notification for %s #%s: %w", item.Kind, item.Number, err)
		}
		if inserted {
			out.Inserted++
		} else {
			out.Skipped++
		}
		if !inserted && !args.IncludeSkipped {
			// Skipped rows duplicate prior syncs; the counts already capture them.
			continue
		}
		out.Items = append(out.Items, GitHubInboxSyncItem{
			NotificationID: n.ID,
			Kind:           item.Kind,
			Number:         item.Number,
			Title:          item.Title,
			URL:            item.URL,
			Inserted:       inserted,
			OriginRef:      n.OriginRef,
			TeleportState:  n.TeleportState,
			TeleportSlots:  n.TeleportSlots,
		})
	}
	return out, nil
}

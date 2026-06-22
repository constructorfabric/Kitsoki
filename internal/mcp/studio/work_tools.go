package studio

import (
	"context"
	"fmt"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
)

// WorkArgs is the input to studio.work.
type WorkArgs struct {
	// IncludeQuiet includes read notifications and terminal non-failed jobs.
	// By default studio.work returns only active operator work.
	IncludeQuiet bool `json:"include_quiet,omitempty"`
	// Limit caps returned items. Zero defaults to 50. Negative means no limit.
	Limit int `json:"limit,omitempty"`
}

// WorkResult is the global read-only async/reacquisition queue.
type WorkResult struct {
	OK       bool                 `json:"ok"`
	Summary  WorkSummary          `json:"summary"`
	Sessions []WorkSessionSummary `json:"sessions,omitempty"`
	Items    []WorkItem           `json:"items,omitempty"`
}

// WorkSummary gives clients a cheap global signal before choosing an item.
type WorkSummary struct {
	Sessions                    int `json:"sessions"`
	Items                       int `json:"items"`
	NeedsAttention              int `json:"needs_attention"`
	JobsRunning                 int `json:"jobs_running"`
	JobsAwaitingInput           int `json:"jobs_awaiting_input"`
	JobsTerminal                int `json:"jobs_terminal"`
	NotificationsUnread         int `json:"notifications_unread"`
	NotificationsActionRequired int `json:"notifications_action_required"`
	PendingDrives               int `json:"pending_drives"`
	DispatchingDrives           int `json:"dispatching_drives"`
	BackgroundedChats           int `json:"backgrounded_chats"`
}

// WorkSessionSummary is one open driving session's async headline.
type WorkSessionSummary struct {
	Handle    string              `json:"handle"`
	SessionID string              `json:"session_id"`
	StoryPath string              `json:"story_path,omitempty"`
	State     string              `json:"state,omitempty"`
	Async     AsyncInspectSummary `json:"async"`
}

// WorkItem is one global work-queue row. Higher Priority sorts first.
type WorkItem struct {
	Kind                string                    `json:"kind"`
	Priority            int                       `json:"priority"`
	Handle              string                    `json:"handle"`
	SessionID           string                    `json:"session_id"`
	StoryPath           string                    `json:"story_path,omitempty"`
	State               string                    `json:"state,omitempty"`
	Title               string                    `json:"title,omitempty"`
	Body                string                    `json:"body,omitempty"`
	Status              string                    `json:"status,omitempty"`
	NotificationID      string                    `json:"notification_id,omitempty"`
	JobID               string                    `json:"job_id,omitempty"`
	DriveID             string                    `json:"drive_id,omitempty"`
	ChatID              string                    `json:"chat_id,omitempty"`
	Severity            jobs.NotificationSeverity `json:"severity,omitempty"`
	CreatedAtUnixMilli  int64                     `json:"created_at_unix_milli,omitempty"`
	UpdatedAtUnixMilli  int64                     `json:"updated_at_unix_milli,omitempty"`
	ReadAtUnixMilli     int64                     `json:"read_at_unix_milli,omitempty"`
	TeleportState       string                    `json:"teleport_state,omitempty"`
	TeleportSlots       map[string]any            `json:"teleport_slots,omitempty"`
	TeleportJobID       string                    `json:"teleport_job_id,omitempty"`
	OriginKind          string                    `json:"origin_kind,omitempty"`
	OriginRef           string                    `json:"origin_ref,omitempty"`
	OriginURL           string                    `json:"origin_url,omitempty"`
	OriginState         string                    `json:"origin_state,omitempty"`
	Actor               string                    `json:"actor,omitempty"`
	Thread              string                    `json:"thread,omitempty"`
	TmuxSession         string                    `json:"tmux_session,omitempty"`
	TmuxHost            string                    `json:"tmux_host,omitempty"`
	WorkspacePath       string                    `json:"workspace_path,omitempty"`
	ReceivedAtUnixMicro int64                     `json:"received_at_unix_micro,omitempty"`
	UpdatedAtUnixMicro  int64                     `json:"updated_at_unix_micro,omitempty"`
	Reacquire           WorkReacquire             `json:"reacquire"`
}

// WorkReacquire names the next MCP tool call for focusing the selected item.
type WorkReacquire struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

func (srv *Server) handleWork(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkArgs,
) (*mcpsdk.CallToolResult, any, error) {
	out, err := srv.work(ctx, args)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, out, nil
}

func (srv *Server) work(ctx context.Context, args WorkArgs) (WorkResult, error) {
	sessions := srv.sess.DrivingSessions()
	out := WorkResult{
		OK:       true,
		Summary:  WorkSummary{Sessions: len(sessions)},
		Sessions: make([]WorkSessionSummary, 0, len(sessions)),
	}
	for _, sh := range sessions {
		rt := sh.Runtime
		j, err := rt.orch.LoadJourney(rt.sid)
		if err != nil {
			return WorkResult{}, fmt.Errorf("studio.work: load journey for %s: %w", sh.Key, err)
		}
		jobRows, notifications, unread, pendingDrives, backgroundedChats, err := rt.inspectAsync(ctx)
		if err != nil {
			return WorkResult{}, err
		}
		async := summarizeAsync(jobRows, notifications, unread, pendingDrives, backgroundedChats)
		out.Sessions = append(out.Sessions, WorkSessionSummary{
			Handle:    sh.Key,
			SessionID: string(sh.SID),
			StoryPath: sh.StoryPath,
			State:     string(j.State),
			Async:     async,
		})
		addSummary(&out.Summary, async)
		out.Items = append(out.Items, workItemsForNotifications(sh, string(j.State), notifications, args.IncludeQuiet)...)
		out.Items = append(out.Items, workItemsForJobs(sh, string(j.State), jobRows, args.IncludeQuiet)...)
		out.Items = append(out.Items, workItemsForPendingDrives(sh, string(j.State), pendingDrives)...)
		out.Items = append(out.Items, workItemsForBackgroundedChats(sh, string(j.State), backgroundedChats)...)
	}

	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if itemUpdatedAt(a) != itemUpdatedAt(b) {
			return itemUpdatedAt(a) > itemUpdatedAt(b)
		}
		if a.Handle != b.Handle {
			return a.Handle < b.Handle
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return itemID(a) < itemID(b)
	})
	if args.Limit == 0 {
		args.Limit = 50
	}
	if args.Limit > 0 && len(out.Items) > args.Limit {
		out.Items = out.Items[:args.Limit]
	}
	out.Summary.Items = len(out.Items)
	for _, item := range out.Items {
		if workItemNeedsAttention(item) {
			out.Summary.NeedsAttention++
		}
	}
	return out, nil
}

func addSummary(sum *WorkSummary, async AsyncInspectSummary) {
	sum.JobsRunning += async.JobsRunning
	sum.JobsAwaitingInput += async.JobsAwaitingInput
	sum.JobsTerminal += async.JobsTerminal
	sum.NotificationsUnread += async.NotificationsUnread
	sum.NotificationsActionRequired += async.NotificationsActionRequired
	sum.PendingDrives += async.PendingDrives
	sum.DispatchingDrives += async.DispatchingDrives
	sum.BackgroundedChats += async.BackgroundedChats
}

func workItemsForNotifications(sh *SessionHandle, state string, notifications []InboxInspectItem, includeQuiet bool) []WorkItem {
	out := make([]WorkItem, 0, len(notifications))
	for _, n := range notifications {
		if n.ReadAtUnixMilli != 0 && !includeQuiet {
			continue
		}
		out = append(out, WorkItem{
			Kind:               "notification",
			Priority:           notificationPriority(n),
			Handle:             sh.Key,
			SessionID:          string(sh.SID),
			StoryPath:          sh.StoryPath,
			State:              state,
			Title:              n.Title,
			Body:               n.Body,
			Status:             "unread",
			NotificationID:     n.ID,
			Severity:           n.Severity,
			CreatedAtUnixMilli: n.CreatedAtUnixMilli,
			UpdatedAtUnixMilli: n.CreatedAtUnixMilli,
			ReadAtUnixMilli:    n.ReadAtUnixMilli,
			TeleportState:      n.TeleportState,
			TeleportSlots:      n.TeleportSlots,
			TeleportJobID:      n.TeleportJobID,
			OriginKind:         n.OriginKind,
			OriginRef:          n.OriginRef,
			OriginURL:          n.OriginURL,
			Reacquire: WorkReacquire{
				Tool: "session.teleport",
				Args: map[string]any{"handle": sh.Key, "notification_id": n.ID},
			},
		})
		if n.ReadAtUnixMilli != 0 {
			out[len(out)-1].Status = "read"
		}
	}
	return out
}

func notificationPriority(n InboxInspectItem) int {
	if n.ReadAtUnixMilli != 0 {
		return 20
	}
	switch n.Severity {
	case jobs.SeverityActionRequired:
		return 100
	case jobs.SeverityError:
		return 92
	case jobs.SeverityWarn:
		return 88
	case jobs.SeveritySuccess:
		return 84
	default:
		return 80
	}
}

func workItemNeedsAttention(item WorkItem) bool {
	switch item.Kind {
	case "notification":
		return item.ReadAtUnixMilli == 0 && item.Severity == jobs.SeverityActionRequired
	case "job":
		return item.Status == string(jobs.JobAwaitingInput) || item.Status == string(jobs.JobFailed)
	default:
		return false
	}
}

func workItemsForJobs(sh *SessionHandle, state string, jobRows []JobInspectItem, includeQuiet bool) []WorkItem {
	out := make([]WorkItem, 0, len(jobRows))
	for _, j := range jobRows {
		priority := jobPriority(j)
		if priority < 80 && !isActiveJob(j) && !includeQuiet {
			continue
		}
		out = append(out, WorkItem{
			Kind:               "job",
			Priority:           priority,
			Handle:             sh.Key,
			SessionID:          string(sh.SID),
			StoryPath:          sh.StoryPath,
			State:              state,
			Title:              j.Kind,
			Status:             string(j.Status),
			JobID:              j.ID,
			CreatedAtUnixMilli: j.CreatedAtUnixMilli,
			UpdatedAtUnixMilli: j.UpdatedAtUnixMilli,
			OriginState:        j.OriginState,
			Reacquire: WorkReacquire{
				Tool: "session.inspect",
				Args: map[string]any{"handle": sh.Key},
			},
		})
	}
	return out
}

func isActiveJob(j JobInspectItem) bool {
	return j.Status == jobs.JobRunning || j.Status == jobs.JobAwaitingInput || j.Status == jobs.JobFailed
}

func jobPriority(j JobInspectItem) int {
	switch j.Status {
	case jobs.JobAwaitingInput:
		return 96
	case jobs.JobFailed:
		return 90
	case jobs.JobRunning:
		return 70
	case jobs.JobCancelled:
		return 30
	default:
		return 25
	}
}

func workItemsForPendingDrives(sh *SessionHandle, state string, drives []PendingDriveItem) []WorkItem {
	out := make([]WorkItem, 0, len(drives))
	for _, d := range drives {
		priority := 65
		if d.Status == chats.DriveStatusDispatching {
			priority = 68
		}
		out = append(out, WorkItem{
			Kind:                "pending_drive",
			Priority:            priority,
			Handle:              sh.Key,
			SessionID:           string(sh.SID),
			StoryPath:           sh.StoryPath,
			State:               state,
			Title:               d.Payload,
			Status:              string(d.Status),
			DriveID:             d.DriveID,
			ChatID:              d.ChatID,
			OriginState:         d.OriginState,
			Actor:               d.Actor,
			Thread:              d.Thread,
			ReceivedAtUnixMicro: d.ReceivedAtUnixMicro,
			Reacquire: WorkReacquire{
				Tool: "chat.show",
				Args: map[string]any{
					"chat_id":    d.ChatID,
					"handle":     sh.Key,
					"session_id": string(sh.SID),
				},
			},
		})
	}
	return out
}

func workItemsForBackgroundedChats(sh *SessionHandle, state string, chats []BackgroundedChatItem) []WorkItem {
	out := make([]WorkItem, 0, len(chats))
	for _, ch := range chats {
		out = append(out, WorkItem{
			Kind:               "backgrounded_chat",
			Priority:           60,
			Handle:             sh.Key,
			SessionID:          string(sh.SID),
			StoryPath:          sh.StoryPath,
			State:              state,
			Title:              ch.ChatID,
			Status:             "backgrounded",
			ChatID:             ch.ChatID,
			TmuxSession:        ch.TmuxSession,
			TmuxHost:           ch.TmuxHost,
			WorkspacePath:      ch.WorkspacePath,
			UpdatedAtUnixMicro: ch.UpdatedAtUnixMicro,
			Reacquire: WorkReacquire{
				Tool: "chat.show",
				Args: map[string]any{
					"chat_id":    ch.ChatID,
					"handle":     sh.Key,
					"session_id": string(sh.SID),
				},
			},
		})
	}
	return out
}

func itemUpdatedAt(item WorkItem) int64 {
	switch {
	case item.UpdatedAtUnixMilli != 0:
		return item.UpdatedAtUnixMilli
	case item.CreatedAtUnixMilli != 0:
		return item.CreatedAtUnixMilli
	case item.UpdatedAtUnixMicro != 0:
		return item.UpdatedAtUnixMicro / 1000
	case item.ReceivedAtUnixMicro != 0:
		return item.ReceivedAtUnixMicro / 1000
	default:
		return 0
	}
}

func itemID(item WorkItem) string {
	switch {
	case item.NotificationID != "":
		return item.NotificationID
	case item.JobID != "":
		return item.JobID
	case item.DriveID != "":
		return item.DriveID
	case item.ChatID != "":
		return item.ChatID
	default:
		return item.Kind
	}
}

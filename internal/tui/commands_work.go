package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/tui/blocks"
)

// commands_work.go - single-pane TUI "/work": print the active async work
// queue for this session. This is the terminal counterpart to web
// runstatus.work.list and studio.work: one compact place to see unread
// notifications, active background jobs, queued chat drives, and backgrounded
// Claude PTYs without leaving the current flow.
func renderWorkBlock(m RootModel, _ []string) (RootModel, string) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if m.jobStore == nil && m.chatStore == nil {
		return m, r.SlashOutput("(work: no job or chat store wired - pass --db for async work tracking)")
	}

	ctx := context.Background()
	var rows []workRow
	var errs []string
	var attachTargets []chats.PtySession

	if m.jobStore != nil {
		jobRows, err := m.jobStore.ListBySession(ctx, m.sid)
		if err != nil {
			errs = append(errs, "jobs: "+err.Error())
		} else {
			rows = append(rows, workRowsForJobs(jobRows)...)
		}

		notifs, err := m.jobStore.ListNotifications(ctx, m.sid, 20)
		if err != nil {
			errs = append(errs, "notifications: "+err.Error())
		} else {
			rows = append(rows, workRowsForNotifications(notifs)...)
		}
	}

	if m.chatStore != nil {
		drives, err := m.chatStore.ListDrivesBySession(ctx, string(m.sid),
			[]chats.DriveStatus{chats.DriveStatusPending, chats.DriveStatusDispatching})
		if err != nil {
			errs = append(errs, "drives: "+err.Error())
		} else {
			rows = append(rows, workRowsForDrives(drives)...)
		}

		ptys, err := m.chatStore.ListPTYForHost(ctx)
		if err != nil {
			errs = append(errs, "sessions: "+err.Error())
		} else {
			var ptyRows []workRow
			ptyRows, attachTargets = workRowsForPTYs(ctx, m.chatStore, string(m.sid), ptys)
			rows = append(rows, ptyRows...)
		}
	}

	if len(errs) > 0 {
		return m, r.SlashOutput("(work: " + strings.Join(errs, "; ") + ")")
	}
	m.sessionList = attachTargets
	if len(rows) == 0 {
		return m, r.SlashOutput("(work: no active async work)")
	}

	var sb strings.Builder
	sb.WriteString(r.SlashOutput(fmt.Sprintf("  active work: %d item(s)", len(rows))))
	sb.WriteByte('\n')
	for i, row := range rows {
		sb.WriteString(fmt.Sprintf("  %d. %-12s %-15s %s", i+1, row.Kind, row.Status, row.Title))
		if row.Hint != "" {
			sb.WriteString(" - ")
			sb.WriteString(row.Hint)
		}
		if row.Age != "" {
			sb.WriteString(" ")
			sb.WriteString(row.Age)
		}
		sb.WriteByte('\n')
	}
	if len(attachTargets) > 0 {
		sb.WriteString(r.SlashOutput("  use /inbox <n> for notifications or /sessions attach <n> for chat rows"))
	} else {
		sb.WriteString(r.SlashOutput("  use /inbox <n> for notifications; run /sessions list to attach other Claude sessions"))
	}
	return m, strings.TrimRight(sb.String(), "\n")
}

type workRow struct {
	Kind   string
	Status string
	Title  string
	Hint   string
	Age    string
}

func workRowsForJobs(jobRows []jobs.Job) []workRow {
	out := make([]workRow, 0, len(jobRows))
	for _, j := range jobRows {
		switch j.Status {
		case jobs.JobRunning, jobs.JobAwaitingInput, jobs.JobFailed:
		default:
			continue
		}
		title := j.Kind
		if title == "" {
			title = j.ID
		}
		hint := "job " + j.ID
		if j.OriginState != "" {
			hint += " from " + string(j.OriginState)
		}
		out = append(out, workRow{
			Kind:   "job",
			Status: string(j.Status),
			Title:  title,
			Hint:   hint,
			Age:    humanAge(time.Since(j.UpdatedAt)),
		})
	}
	return out
}

func workRowsForNotifications(notifs []jobs.Notification) []workRow {
	out := make([]workRow, 0, len(notifs))
	for _, n := range notifs {
		if n.ReadAt != nil {
			continue
		}
		title := n.Title
		if title == "" {
			title = n.ID
		}
		hint := n.OriginRef
		if n.OriginURL != "" {
			if hint != "" {
				hint += " "
			}
			hint += n.OriginURL
		}
		out = append(out, workRow{
			Kind:   "notification",
			Status: string(n.Severity),
			Title:  title,
			Hint:   hint,
			Age:    humanAge(time.Since(n.CreatedAt)),
		})
	}
	return out
}

func workRowsForDrives(drives []chats.Drive) []workRow {
	out := make([]workRow, 0, len(drives))
	for _, d := range drives {
		title := d.Payload
		if title == "" {
			title = d.DriveID
		}
		out = append(out, workRow{
			Kind:   "queued",
			Status: string(d.Status),
			Title:  title,
			Hint:   "chat " + d.ChatID,
			Age:    humanAge(time.Since(d.ReceivedAt)),
		})
	}
	return out
}

func workRowsForPTYs(ctx context.Context, cs *chats.Store, sid string, ptys []chats.PtySession) ([]workRow, []chats.PtySession) {
	out := make([]workRow, 0, len(ptys))
	attachTargets := make([]chats.PtySession, 0, len(ptys))
	for _, p := range ptys {
		if p.Mode != chats.PtyModeBackground {
			continue
		}
		chat, err := cs.Get(ctx, p.ChatID)
		if err != nil || chat == nil || chat.SessionID != sid {
			continue
		}
		title := chat.Title
		if title == "" {
			title = p.ChatID
		}
		hint := "tmux " + p.TmuxSession
		if p.LastIdleAt != nil {
			hint += ", idle " + humanAge(time.Since(*p.LastIdleAt))
		}
		attachTargets = append(attachTargets, p)
		attachIndex := len(attachTargets)
		if hint != "" {
			hint += "; "
		}
		hint += fmt.Sprintf("/sessions attach %d", attachIndex)
		out = append(out, workRow{
			Kind:   "chat",
			Status: string(p.Mode),
			Title:  title,
			Hint:   hint,
			Age:    humanAge(time.Since(p.UpdatedAt)),
		})
	}
	return out, attachTargets
}

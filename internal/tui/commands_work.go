package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/store"
	"kitsoki/internal/tui/blocks"
)

// commands_work.go - single-pane TUI "/work": print the active async work
// queue for this session. "/work --all" broadens jobs, notifications, queued,
// dispatching, or failed drives, and background Claude PTYs across sessions.
// Proposal-review rows stay scoped to this TUI session. This is the terminal
// counterpart to web runstatus.work.list and studio.work: one compact place to
// see unread notifications, active background jobs, queued/dispatching/failed
// chat drives, backgrounded Claude PTYs, and proposal-review work without
// leaving the current flow.
func renderWorkBlock(m RootModel, args []string) (RootModel, string) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	proposalRows := workRowsForProposals(m.mineState())
	traceProposalRows, traceErr := workRowsForTraceMiningProposals(m.traceHistory, workRowIDs(proposalRows))
	if traceErr != nil {
		traceProposalRows = nil
	}
	proposalRows = append(proposalRows, traceProposalRows...)
	if m.jobStore == nil && m.chatStore == nil && m.traceHistory == nil && len(proposalRows) == 0 {
		return m, r.SlashOutput("(work: no job or chat store wired - pass --db for async work tracking)")
	}
	allSessions := workAllSessions(args)

	ctx := context.Background()
	var rows []workRow
	var errs []string
	if traceErr != nil {
		errs = append(errs, "trace proposals: "+traceErr.Error())
	}

	if m.jobStore != nil {
		var notifs []jobs.Notification
		if allSessions {
			var err error
			notifs, err = m.jobStore.ListNotificationsAll(ctx, 20)
			if err != nil {
				errs = append(errs, "notifications: "+err.Error())
			}
		} else {
			var err error
			notifs, err = m.jobStore.ListNotifications(ctx, m.sid, 20)
			if err != nil {
				errs = append(errs, "notifications: "+err.Error())
			}
		}

		var jobRows []jobs.Job
		var err error
		if allSessions {
			jobRows, err = m.jobStore.ListByStatus(ctx, []jobs.JobStatus{jobs.JobRunning, jobs.JobAwaitingInput, jobs.JobFailed})
		} else {
			jobRows, err = m.jobStore.ListBySession(ctx, m.sid)
		}
		if err != nil {
			errs = append(errs, "jobs: "+err.Error())
		} else {
			rows = append(rows, workRowsForJobs(jobRows, notifs, m.sid, allSessions)...)
		}

		if len(notifs) > 0 {
			rows = append(rows, workRowsForNotifications(notifs, m.sid, allSessions)...)
		}
	}

	if m.chatStore != nil {
		statuses := []chats.DriveStatus{chats.DriveStatusPending, chats.DriveStatusDispatching, chats.DriveStatusFailed}
		var drives []chats.Drive
		var err error
		if allSessions {
			drives, err = m.chatStore.ListDrivesByOrigin(ctx, statuses)
		} else {
			drives, err = m.chatStore.ListDrivesBySession(ctx, string(m.sid), statuses)
		}
		if err != nil {
			errs = append(errs, "drives: "+err.Error())
		} else {
			rows = append(rows, workRowsForDrives(drives, string(m.sid), allSessions)...)
		}

		ptys, err := m.chatStore.ListPTYForHost(ctx)
		if err != nil {
			errs = append(errs, "sessions: "+err.Error())
		} else {
			var ptyRows []workRow
			ptyRows = workRowsForPTYs(ctx, m.chatStore, string(m.sid), ptys, allSessions)
			rows = append(rows, ptyRows...)
		}
	}
	rows = append(rows, proposalRows...)

	if len(errs) > 0 {
		return m, r.SlashOutput("(work: " + strings.Join(errs, "; ") + ")")
	}
	if len(rows) == 0 {
		if allSessions {
			return m, r.SlashOutput("(work: no active async work across sessions)")
		}
		return m, r.SlashOutput("(work: no active async work)")
	}
	sortWorkRows(rows)
	attachTargets := assignWorkAttachTargets(rows)
	m.sessionList = attachTargets

	var sb strings.Builder
	label := "active work"
	if allSessions {
		label = "active work (all sessions)"
	}
	sb.WriteString(r.SlashOutput(fmt.Sprintf("  %s: %d item(s)", label, len(rows))))
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

func workAllSessions(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--all", "-a", "all":
			return true
		}
	}
	return false
}

type workRow struct {
	Kind         string
	Status       string
	Title        string
	Hint         string
	Age          string
	Priority     int
	UpdatedAt    time.Time
	ID           string
	AttachTarget *chats.PtySession
}

func workRowsForJobs(jobRows []jobs.Job, notifs []jobs.Notification, sid app.SessionID, allSessions bool) []workRow {
	out := make([]workRow, 0, len(jobRows))
	jobInboxIndexes := workJobInboxIndexes(notifs, sid)
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
		if idx := jobInboxIndexes[j.ID]; idx > 0 {
			hint += fmt.Sprintf("; /inbox %d", idx)
		}
		if prompt := jobClarificationPrompt(j); prompt != "" {
			hint += "; " + prompt
		}
		if allSessions {
			hint += workSessionHint(j.SessionID, sid)
		}
		out = append(out, workRow{
			Kind:      "job",
			Status:    string(j.Status),
			Title:     title,
			Hint:      hint,
			Age:       humanAge(time.Since(j.UpdatedAt)),
			Priority:  workJobPriority(j.Status),
			UpdatedAt: j.UpdatedAt,
			ID:        j.ID,
		})
	}
	return out
}

func jobClarificationPrompt(j jobs.Job) string {
	if j.Status != jobs.JobAwaitingInput {
		return ""
	}
	schema := clarificationSchema(j.ClarificationSchema)
	if schema == nil {
		return ""
	}
	return schema.Prompt
}

func clarificationSchema(raw any) *jobs.ClarificationSchema {
	switch v := raw.(type) {
	case nil:
		return nil
	case jobs.ClarificationSchema:
		return &v
	case *jobs.ClarificationSchema:
		return v
	case map[string]any:
		schema := jobs.ClarificationSchema{Fields: map[string]string{}}
		if prompt, ok := v["prompt"].(string); ok {
			schema.Prompt = prompt
		}
		if fields, ok := v["fields"].(map[string]any); ok {
			for name, typ := range fields {
				if text, ok := typ.(string); ok {
					schema.Fields[name] = text
				}
			}
		}
		return &schema
	default:
		return nil
	}
}

func workJobInboxIndexes(notifs []jobs.Notification, sid app.SessionID) map[string]int {
	indexes := make(map[string]int)
	selected := make(map[string]jobs.Notification)
	next := 1
	for _, n := range notifs {
		if n.SessionID != sid || n.ReadAt != nil {
			continue
		}
		if jobID := notificationJobID(n); jobID != "" {
			if existing, exists := selected[jobID]; !exists || jobNotificationRank(n.Severity) > jobNotificationRank(existing.Severity) {
				indexes[jobID] = next
				selected[jobID] = n
			}
		}
		next++
	}
	return indexes
}

func jobNotificationRank(severity jobs.NotificationSeverity) int {
	switch severity {
	case jobs.SeverityActionRequired:
		return 4
	case jobs.SeverityError:
		return 3
	case jobs.SeverityWarn:
		return 2
	default:
		return 1
	}
}

func notificationJobID(n jobs.Notification) string {
	if n.TeleportJobID != "" {
		return n.TeleportJobID
	}
	if strings.HasPrefix(n.OriginRef, "job:") {
		return strings.TrimPrefix(n.OriginRef, "job:")
	}
	return ""
}

func workRowsForNotifications(notifs []jobs.Notification, sid app.SessionID, allSessions bool) []workRow {
	out := make([]workRow, 0, len(notifs))
	inboxIndexes := workInboxIndexes(notifs, sid)
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
		if idx := inboxIndexes[n.ID]; idx > 0 {
			if hint != "" {
				hint += "; "
			}
			hint += fmt.Sprintf("/inbox %d", idx)
		}
		if allSessions {
			hint += workSessionHint(n.SessionID, sid)
		}
		out = append(out, workRow{
			Kind:      "notification",
			Status:    string(n.Severity),
			Title:     title,
			Hint:      hint,
			Age:       humanAge(time.Since(n.CreatedAt)),
			Priority:  workNotificationPriority(n.Severity),
			UpdatedAt: n.CreatedAt,
			ID:        n.ID,
		})
	}
	return out
}

func workInboxIndexes(notifs []jobs.Notification, sid app.SessionID) map[string]int {
	indexes := make(map[string]int)
	next := 1
	for _, n := range notifs {
		if n.SessionID != sid || n.ReadAt != nil {
			continue
		}
		indexes[n.ID] = next
		next++
	}
	return indexes
}

func workSessionHint(rowSID, currentSID app.SessionID) string {
	switch {
	case rowSID == "":
		return ""
	case rowSID == currentSID:
		return ", current session"
	default:
		return ", session " + string(rowSID)
	}
}

func workRowsForDrives(drives []chats.Drive, sid string, allSessions bool) []workRow {
	out := make([]workRow, 0, len(drives))
	for _, d := range drives {
		title := d.Payload
		if title == "" {
			title = d.DriveID
		}
		hint := "chat " + d.ChatID
		if allSessions {
			if d.OriginSessionID == sid {
				hint += ", current session"
			} else if d.OriginSessionID != "" {
				hint += ", session " + d.OriginSessionID
			}
		}
		if d.Status == chats.DriveStatusFailed && d.ErrorMessage != "" {
			hint += "; " + d.ErrorMessage
		}
		hint += "; /chat show " + d.ChatID
		out = append(out, workRow{
			Kind:      workDriveKind(d.Status),
			Status:    string(d.Status),
			Title:     title,
			Hint:      hint,
			Age:       humanAge(time.Since(d.ReceivedAt)),
			Priority:  workDrivePriority(d.Status),
			UpdatedAt: d.ReceivedAt,
			ID:        d.DriveID,
		})
	}
	return out
}

func workDriveKind(status chats.DriveStatus) string {
	switch status {
	case chats.DriveStatusDispatching:
		return "dispatching"
	case chats.DriveStatusFailed:
		return "failed"
	default:
		return "queued"
	}
}

func workRowsForPTYs(ctx context.Context, cs *chats.Store, sid string, ptys []chats.PtySession, allSessions bool) []workRow {
	out := make([]workRow, 0, len(ptys))
	for _, p := range ptys {
		if p.Mode != chats.PtyModeBackground {
			continue
		}
		chat, err := cs.Get(ctx, p.ChatID)
		if err != nil || chat == nil {
			continue
		}
		if !allSessions && chat.SessionID != sid {
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
		if allSessions {
			if chat.SessionID == sid {
				hint += ", current session"
			} else if chat.SessionID != "" {
				hint += ", session " + chat.SessionID
			}
		}
		attachTarget := p
		out = append(out, workRow{
			Kind:         "chat",
			Status:       string(p.Mode),
			Title:        title,
			Hint:         hint,
			Age:          humanAge(time.Since(p.UpdatedAt)),
			Priority:     60,
			UpdatedAt:    p.UpdatedAt,
			ID:           p.ChatID,
			AttachTarget: &attachTarget,
		})
	}
	return out
}

func workRowsForProposals(state MineState) []workRow {
	if !state.Enabled || len(state.Queue) == 0 {
		return nil
	}
	out := make([]workRow, 0, len(state.Queue))
	now := time.Now()
	for i, p := range state.Queue {
		title := p.Title
		if title == "" {
			title = p.ID
		}
		hintParts := []string{}
		if p.Target != "" {
			hintParts = append(hintParts, p.Target)
		}
		if p.Detail != "" {
			hintParts = append(hintParts, p.Detail)
		}
		hintParts = append(hintParts,
			fmt.Sprintf("/mine accept %s", p.ID),
			fmt.Sprintf("/mine refine %s", p.ID),
			fmt.Sprintf("/mine dismiss %s", p.ID),
		)
		out = append(out, workRow{
			Kind:      "proposal",
			Status:    string(p.Kind),
			Title:     title,
			Hint:      strings.Join(hintParts, "; "),
			Priority:  workProposalPriority(p.Kind),
			UpdatedAt: now.Add(-time.Duration(i) * time.Nanosecond),
			ID:        p.ID,
		})
	}
	return out
}

func workRowIDs(rows []workRow) map[string]bool {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.ID != "" {
			out[row.ID] = true
		}
	}
	return out
}

func workRowsForTraceMiningProposals(historyFn func() (store.History, error), skip map[string]bool) ([]workRow, error) {
	if historyFn == nil {
		return nil, nil
	}
	history, err := historyFn()
	if err != nil {
		return nil, err
	}
	proposals := pendingTraceMiningProposals(history)
	if len(proposals) == 0 {
		return nil, nil
	}
	out := make([]workRow, 0, len(proposals))
	for _, p := range proposals {
		if skip != nil && skip[p.RecipeID] {
			continue
		}
		title := strings.TrimSpace(fmt.Sprintf("%s proposal", p.Kind))
		if title == "proposal" {
			title = p.RecipeID
		}
		hintParts := []string{}
		if p.Target != "" {
			hintParts = append(hintParts, p.Target)
		}
		if p.Rung != 0 {
			hintParts = append(hintParts, fmt.Sprintf("rung %d", p.Rung))
		}
		if p.DraftPath != "" {
			hintParts = append(hintParts, p.DraftPath)
		}
		out = append(out, workRow{
			Kind:      "proposal",
			Status:    p.Kind,
			Title:     title,
			Hint:      strings.Join(hintParts, "; "),
			Priority:  40,
			UpdatedAt: p.RaisedAt,
			ID:        p.RecipeID,
		})
	}
	return out, nil
}

type traceMiningProposal struct {
	RecipeID  string
	Kind      string
	Target    string
	Rung      int
	DraftPath string
	RaisedAt  time.Time
}

func pendingTraceMiningProposals(history store.History) []traceMiningProposal {
	if len(history) == 0 {
		return nil
	}
	byRecipe := make(map[string]traceMiningProposal)
	var order []string
	for _, ev := range history {
		switch ev.Kind {
		case store.MiningProposalRaised:
			var payload store.MiningProposalRaisedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			if _, exists := byRecipe[payload.RecipeID]; !exists {
				order = append(order, payload.RecipeID)
			}
			byRecipe[payload.RecipeID] = traceMiningProposal{
				RecipeID:  payload.RecipeID,
				Kind:      payload.Kind,
				Target:    payload.Target,
				Rung:      payload.Rung,
				DraftPath: payload.DraftPath,
				RaisedAt:  ev.Ts,
			}
		case store.MiningProposalDecided:
			var payload store.MiningProposalDecidedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			delete(byRecipe, payload.RecipeID)
		}
	}
	out := make([]traceMiningProposal, 0, len(byRecipe))
	for _, recipeID := range order {
		if item, ok := byRecipe[recipeID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func sortWorkRows(rows []workRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
}

func assignWorkAttachTargets(rows []workRow) []chats.PtySession {
	targets := make([]chats.PtySession, 0)
	for i := range rows {
		if rows[i].AttachTarget == nil {
			continue
		}
		targets = append(targets, *rows[i].AttachTarget)
		if rows[i].Hint != "" {
			rows[i].Hint += "; "
		}
		rows[i].Hint += fmt.Sprintf("/sessions attach %d", len(targets))
	}
	return targets
}

func workJobPriority(status jobs.JobStatus) int {
	switch status {
	case jobs.JobAwaitingInput:
		return 96
	case jobs.JobFailed:
		return 90
	case jobs.JobRunning:
		return 70
	default:
		return 25
	}
}

func workNotificationPriority(severity jobs.NotificationSeverity) int {
	switch severity {
	case jobs.SeverityActionRequired:
		return 100
	case jobs.SeverityError:
		return 92
	case jobs.SeverityWarn:
		return 88
	case jobs.SeveritySuccess:
		return 50
	default:
		return 45
	}
}

func workDrivePriority(status chats.DriveStatus) int {
	switch status {
	case chats.DriveStatusFailed:
		return 94
	case chats.DriveStatusDispatching:
		return 68
	default:
		return 65
	}
}

func workProposalPriority(kind MineProposalKind) int {
	if kind == MineKindWriteMode {
		return 97
	}
	return 40
}

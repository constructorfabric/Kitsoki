package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	kitsokimcp "kitsoki/internal/mcp"
)

// WorkListResult is the cross-session operator work queue returned by
// runstatus.work.list.
type WorkListResult struct {
	Summary  WorkSummary         `json:"summary"`
	Sessions []WorkSessionResult `json:"sessions,omitempty"`
	Items    []WorkItem          `json:"items,omitempty"`
}

// WorkSessionResult is one live session's async headline.
type WorkSessionResult struct {
	SessionID    string      `json:"session_id"`
	AppID        string      `json:"app_id,omitempty"`
	CurrentState string      `json:"current_state,omitempty"`
	Work         WorkSummary `json:"work"`
}

func (s *Server) listWork(ctx context.Context) (WorkListResult, error) {
	headers := s.provider.List()
	out := WorkListResult{
		Sessions: make([]WorkSessionResult, 0, len(headers)),
	}
	sessionIndex := map[string]int{}
	for _, hdr := range headers {
		sessionIndex[hdr.SessionID] = len(out.Sessions)
		entry, ok := s.provider.Get(hdr.SessionID)
		if !ok || entry.Driver == nil {
			out.Sessions = append(out.Sessions, WorkSessionResult{
				SessionID:    hdr.SessionID,
				AppID:        hdr.AppID,
				CurrentState: hdr.CurrentState,
			})
			continue
		}
		wl, ok := entry.Driver.(WorkLister)
		if !ok {
			out.Sessions = append(out.Sessions, WorkSessionResult{
				SessionID:    hdr.SessionID,
				AppID:        hdr.AppID,
				CurrentState: hdr.CurrentState,
			})
			continue
		}
		work, err := wl.ListWork(ctx)
		if err != nil {
			return WorkListResult{}, err
		}
		for i := range work.Items {
			work.Items[i].SessionID = hdr.SessionID
			if work.Items[i].ReacquireSessionID != "" {
				work.Items[i].ReacquireSessionID = hdr.SessionID
			}
		}
		out.Sessions = append(out.Sessions, WorkSessionResult{
			SessionID:    hdr.SessionID,
			AppID:        hdr.AppID,
			CurrentState: hdr.CurrentState,
			Work:         work.Summary,
		})
		addWorkSummary(&out.Summary, work.Summary)
		out.Items = append(out.Items, work.Items...)
	}
	for _, q := range s.qreg.snapshot() {
		if q.SessionID == "" {
			continue
		}
		item := workItemForOperatorQuestion(q)
		out.Items = append(out.Items, item)
		out.Summary.OperatorQuestions++
		if idx, ok := sessionIndex[q.SessionID]; ok {
			out.Sessions[idx].Work.OperatorQuestions++
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.Kind < b.Kind
	})
	out.Summary.Items = len(out.Items)
	out.Summary.NeedsAttention = 0
	for _, item := range out.Items {
		if workItemNeedsAttention(item) {
			out.Summary.NeedsAttention++
		}
	}
	return out, nil
}

func addWorkSummary(dst *WorkSummary, src WorkSummary) {
	dst.JobsRunning += src.JobsRunning
	dst.JobsAwaitingInput += src.JobsAwaitingInput
	dst.JobsTerminal += src.JobsTerminal
	dst.NotificationsUnread += src.NotificationsUnread
	dst.NotificationsActionRequired += src.NotificationsActionRequired
	dst.PendingDrives += src.PendingDrives
	dst.DispatchingDrives += src.DispatchingDrives
	dst.FailedDrives += src.FailedDrives
	dst.BackgroundedChats += src.BackgroundedChats
	dst.OperatorQuestions += src.OperatorQuestions
	dst.MiningProposals += src.MiningProposals
}

func workItemForOperatorQuestion(q pendingQuestion) WorkItem {
	title := "Agent question"
	body := ""
	if len(q.Questions) > 0 {
		if q.Questions[0].Header != "" {
			title = q.Questions[0].Header
		}
		body = q.Questions[0].Question
		if len(q.Questions) > 1 {
			body = strings.TrimSpace(fmt.Sprintf("%s (+%d more)", body, len(q.Questions)-1))
		}
	}
	return WorkItem{
		Kind:               "operator_question",
		Priority:           98,
		SessionID:          q.SessionID,
		Title:              title,
		Body:               body,
		Status:             "awaiting_answer",
		CreatedAt:          q.CreatedAt,
		UpdatedAt:          q.CreatedAt,
		QuestionID:         q.ID,
		Questions:          append([]kitsokimcp.OperatorAskQuestion(nil), q.Questions...),
		ReacquireTool:      "operator_question",
		ReacquireSessionID: q.SessionID,
	}
}

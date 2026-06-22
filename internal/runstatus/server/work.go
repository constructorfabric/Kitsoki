package server

import (
	"context"
	"sort"
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
	for _, hdr := range headers {
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
	dst.BackgroundedChats += src.BackgroundedChats
}

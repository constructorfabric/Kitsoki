package artifactjob

import (
	"context"
	"sort"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/ulid"
)

// MemoryStore is a deterministic fake for unit tests and no-LLM flow harnesses.
type MemoryStore struct {
	mu        sync.Mutex
	now       func() time.Time
	jobs      map[JobID]Job
	runs      map[JobID]Run
	artifacts map[JobID]map[string]Artifact
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		now:       time.Now,
		jobs:      make(map[JobID]Job),
		runs:      make(map[JobID]Run),
		artifacts: make(map[JobID]map[string]Artifact),
	}
}

func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

func (s *MemoryStore) Register(_ context.Context, req RegisterRequest) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	j := Job{
		ID:                     req.ID,
		SessionID:              req.SessionID,
		AppID:                  req.AppID,
		Story:                  req.Story,
		Origin:                 req.Origin,
		Status:                 req.Status,
		RunURL:                 req.RunURL,
		TracePath:              req.TracePath,
		WorkspaceInstanceID:    req.WorkspaceInstanceID,
		TerminalArtifactHandle: req.TerminalArtifactHandle,
		Summary:                req.Summary,
		Phase:                  req.Phase,
		Visibility:             req.Visibility,
		Owner:                  req.Owner,
		CreatedAt:              req.CreatedAt,
	}
	if j.ID == "" {
		j.ID = JobID(ulid.New())
	}
	if j.Status == "" {
		j.Status = StatusRunning
	}
	if j.Visibility == "" {
		j.Visibility = VisibilityLocal
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	s.jobs[j.ID] = j
	return j, nil
}

func (s *MemoryStore) BindRun(_ context.Context, id JobID, sessionID string, runURL string, tracePath string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	j.SessionID = app.SessionID(sessionID)
	j.RunURL = runURL
	j.TracePath = tracePath
	j.UpdatedAt = s.now().UTC()
	s.jobs[id] = j
	return j, nil
}

func (s *MemoryStore) Update(_ context.Context, id JobID, update Update) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	applyUpdate(&j, update)
	j.UpdatedAt = s.now().UTC()
	s.jobs[id] = j
	return j, nil
}

func (s *MemoryStore) Attach(_ context.Context, id JobID, sessionID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	j.SessionID = app.SessionID(sessionID)
	j.UpdatedAt = s.now().UTC()
	s.jobs[id] = j
	return j, nil
}

func (s *MemoryStore) Get(_ context.Context, id JobID) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return j, nil
}

func (s *MemoryStore) List(_ context.Context, filter ListFilter) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		if !jobMatches(j, filter) {
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) Archive(ctx context.Context, id JobID) (Job, error) {
	status := StatusArchived
	return s.Update(ctx, id, Update{Status: &status})
}

func (s *MemoryStore) SweepInterrupted(_ context.Context, reason string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	now := s.now().UTC()
	for id, j := range s.jobs {
		if j.Status != StatusRunning && j.Status != StatusAwaitingInput {
			continue
		}
		j.Status = StatusInterrupted
		j.InterruptedReason = reason
		j.UpdatedAt = now
		s.jobs[id] = j
		n++
	}
	return n, nil
}

func (s *MemoryStore) UpsertRun(_ context.Context, run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.JobID] = run
	return nil
}

func (s *MemoryStore) UpsertArtifact(_ context.Context, a Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifacts[a.JobID] == nil {
		s.artifacts[a.JobID] = make(map[string]Artifact)
	}
	s.artifacts[a.JobID][a.Handle] = a
	return nil
}

func (s *MemoryStore) GetRun(_ context.Context, id JobID) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) ListRuns(_ context.Context, filter ListFilter) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.runs))
	for _, r := range s.runs {
		if filter.Story != "" && r.Story != filter.Story {
			continue
		}
		if filter.SessionID != "" && r.SessionID != filter.SessionID {
			continue
		}
		if !statusAllowed(r.Status, filter.Status) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) Artifacts(_ context.Context, id JobID) ([]Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.artifacts[id]
	out := make([]Artifact, 0, len(rows))
	for _, a := range rows {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ResolveArtifact(_ context.Context, id JobID, handle string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.artifacts[id][handle]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return a, nil
}

func applyUpdate(j *Job, u Update) {
	if u.Status != nil {
		j.Status = *u.Status
	}
	if u.RunURL != nil {
		j.RunURL = *u.RunURL
	}
	if u.TracePath != nil {
		j.TracePath = *u.TracePath
	}
	if u.WorkspaceInstanceID != nil {
		j.WorkspaceInstanceID = *u.WorkspaceInstanceID
	}
	if u.TerminalArtifactHandle != nil {
		j.TerminalArtifactHandle = *u.TerminalArtifactHandle
	}
	if u.Summary != nil {
		j.Summary = *u.Summary
	}
	if u.Phase != nil {
		j.Phase = *u.Phase
	}
	if u.Visibility != nil {
		j.Visibility = *u.Visibility
	}
	if u.Owner != nil {
		j.Owner = *u.Owner
	}
	if u.FinishedAt != nil {
		t := u.FinishedAt.UTC()
		j.FinishedAt = &t
	}
	if u.InterruptedReason != nil {
		j.InterruptedReason = *u.InterruptedReason
	}
}

func jobMatches(j Job, f ListFilter) bool {
	if f.AppID != "" && j.AppID != f.AppID {
		return false
	}
	if f.Story != "" && j.Story != f.Story {
		return false
	}
	if f.SessionID != "" && j.SessionID != f.SessionID {
		return false
	}
	if !f.IncludeArchived && j.Status == StatusArchived {
		return false
	}
	return statusAllowed(j.Status, f.Status)
}

func statusAllowed(status Status, allowed []Status) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, s := range allowed {
		if status == s {
			return true
		}
	}
	return false
}

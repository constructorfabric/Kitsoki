// Package proposal implements the Proposal pattern (§3).
//
// A proposal is a named, typed lifecycle: drafting → reviewing → executing →
// reviewing_result → done/failed/cancelled. It is compiled from the app's
// YAML proposals: block and attached to states via proposal: <kind>.
//
// # Lifecycle
//
//	drafting        — LLM produces initial draft (host.run is NOT called here)
//	reviewing       — user sees draft, can refine/edit/accept/cancel
//	executing       — execute effect fires; transitions automatically
//	reviewing_result— user sees result, can retry/rerun/new
//	done            — terminal success
//	failed          — terminal failure (retry always available)
//	cancelled       — terminal cancel
//
// # Built-in intents
//
// The runtime registers: refine, edit, accept (alias run), cancel, retry,
// rerun, modify_and_rerun, new. Authors add extra intents on top.
//
// # World state
//
// The active proposal is stored in $proposal on the world snapshot (§3.1).
// Kind-level metadata (schema, prompts, execute config) lives in the app def.
package proposal

import (
	"fmt"
	"time"

	"kitsoki/internal/app"
)

// Status is the lifecycle phase of a proposal.
type Status string

const (
	StatusDrafting        Status = "drafting"
	StatusReviewing       Status = "reviewing"
	StatusExecuting       Status = "executing"
	StatusReviewingResult Status = "reviewing_result"
	StatusDone            Status = "done"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
)

// HistoryEntry is one version snapshot in the proposal's draft history.
type HistoryEntry struct {
	Version    int    `json:"version"`
	Draft      map[string]any `json:"draft"`
	Feedback   string `json:"feedback,omitempty"` // user feedback that produced the NEXT version
	ProducedAt string `json:"produced_at"`        // RFC3339
}

// Result holds the outcome of a proposal execution.
type Result struct {
	OK         bool           `json:"ok"`
	Data       map[string]any `json:"data,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at,omitempty"`
}

// Proposal is the runtime state of one active proposal instance (§3.1 $proposal shape).
type Proposal struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	Status       Status         `json:"status"`
	Current      map[string]any `json:"current"`
	History      []HistoryEntry `json:"history"`
	Result       *Result        `json:"result,omitempty"`
	JobRef       string         `json:"job_ref,omitempty"`
	SeededFrom   string         `json:"seeded_from,omitempty"`
	OwnerSession string         `json:"owner_session"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
}

// WorldKey is the reserved world variable name for the active proposal.
const WorldKey = "$proposal"

// New creates a new Proposal in drafting status.
func New(id, kind, ownerSession string) *Proposal {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Proposal{
		ID:           id,
		Kind:         kind,
		Status:       StatusDrafting,
		Current:      make(map[string]any),
		History:      []HistoryEntry{},
		OwnerSession: ownerSession,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// SetDraft sets the current draft and appends a history entry.
// feedback is the user feedback that prompted this draft (empty for first draft).
func (p *Proposal) SetDraft(draft map[string]any, feedback string) {
	// If there are history entries, set the feedback on the previous entry.
	if len(p.History) > 0 && feedback != "" {
		p.History[len(p.History)-1].Feedback = feedback
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p.Current = cloneDraft(draft)
	p.History = append(p.History, HistoryEntry{
		Version:    len(p.History) + 1,
		Draft:      cloneDraft(draft),
		ProducedAt: now,
	})
	// Soft cap: summarize if > 100 entries (elide oldest, keep last 100).
	if len(p.History) > 100 {
		p.History = p.History[len(p.History)-100:]
	}
	p.UpdatedAt = now
}

// EditField mutates one field in the current draft (for the edit intent).
func (p *Proposal) EditField(key string, value any) {
	if p.Current == nil {
		p.Current = make(map[string]any)
	}
	p.Current[key] = value
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// Transition moves the proposal to a new status.
func (p *Proposal) Transition(s Status) {
	p.Status = s
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// SetResult records the execution result.
func (p *Proposal) SetResult(r Result) {
	p.Result = &r
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// ToMap converts the proposal to a map[string]any for world-state storage.
// This is what gets stored under $proposal in world.Vars.
func (p *Proposal) ToMap() map[string]any {
	m := map[string]any{
		"id":            p.ID,
		"kind":          p.Kind,
		"status":        string(p.Status),
		"current":       p.Current,
		"owner_session": p.OwnerSession,
		"created_at":    p.CreatedAt,
		"updated_at":    p.UpdatedAt,
	}
	// History.
	hist := make([]any, len(p.History))
	for i, h := range p.History {
		he := map[string]any{
			"version":     h.Version,
			"draft":       h.Draft,
			"produced_at": h.ProducedAt,
		}
		if h.Feedback != "" {
			he["feedback"] = h.Feedback
		}
		hist[i] = he
	}
	m["history"] = hist
	// Optional fields.
	if p.Result != nil {
		rm := map[string]any{
			"ok":         p.Result.OK,
			"started_at": p.Result.StartedAt,
		}
		if p.Result.Data != nil {
			rm["data"] = p.Result.Data
		}
		if p.Result.Error != "" {
			rm["error"] = p.Result.Error
		}
		if p.Result.FinishedAt != "" {
			rm["finished_at"] = p.Result.FinishedAt
		}
		m["result"] = rm
	}
	if p.JobRef != "" {
		m["job_ref"] = p.JobRef
	}
	if p.SeededFrom != "" {
		m["seeded_from"] = p.SeededFrom
	}
	return m
}

// FromMap reconstructs a Proposal from a world-state map.
// Returns nil if the map is nil or malformed.
func FromMap(raw any) *Proposal {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	p := &Proposal{}
	p.ID, _ = m["id"].(string)
	p.Kind, _ = m["kind"].(string)
	if s, ok := m["status"].(string); ok {
		p.Status = Status(s)
	}
	if c, ok := m["current"].(map[string]any); ok {
		p.Current = c
	} else {
		p.Current = make(map[string]any)
	}
	p.OwnerSession, _ = m["owner_session"].(string)
	p.CreatedAt, _ = m["created_at"].(string)
	p.UpdatedAt, _ = m["updated_at"].(string)
	p.JobRef, _ = m["job_ref"].(string)
	p.SeededFrom, _ = m["seeded_from"].(string)
	// History.
	if hist, ok := m["history"].([]any); ok {
		for _, item := range hist {
			he, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entry := HistoryEntry{}
			if v, ok := he["version"].(int); ok {
				entry.Version = v
			} else if v, ok := he["version"].(float64); ok {
				entry.Version = int(v)
			}
			if d, ok := he["draft"].(map[string]any); ok {
				entry.Draft = d
			}
			entry.Feedback, _ = he["feedback"].(string)
			entry.ProducedAt, _ = he["produced_at"].(string)
			p.History = append(p.History, entry)
		}
	}
	// Result.
	if rm, ok := m["result"].(map[string]any); ok {
		r := &Result{}
		r.OK, _ = rm["ok"].(bool)
		r.StartedAt, _ = rm["started_at"].(string)
		r.FinishedAt, _ = rm["finished_at"].(string)
		r.Error, _ = rm["error"].(string)
		if d, ok := rm["data"].(map[string]any); ok {
			r.Data = d
		}
		p.Result = r
	}
	return p
}

// ValidateAgainstSchema checks that all required fields in the kind schema
// are present in the draft. Returns nil if valid.
func ValidateAgainstSchema(draft map[string]any, kind *app.ProposalKind) error {
	if kind == nil || len(kind.Schema) == 0 {
		return nil
	}
	for field := range kind.Schema {
		if _, ok := draft[field]; !ok {
			return fmt.Errorf("proposal: draft missing required field %q", field)
		}
	}
	return nil
}

// cloneDraft makes a shallow copy of a draft map.
func cloneDraft(draft map[string]any) map[string]any {
	if draft == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(draft))
	for k, v := range draft {
		out[k] = v
	}
	return out
}

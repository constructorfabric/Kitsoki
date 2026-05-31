package proposal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"kitsoki/internal/app"
)

// Status is the lifecycle phase of a proposal. Its string values are the
// stable, wire-visible phase names stored under "status" in world state and
// matched by the orchestrator's intent wiring, so they must not be renamed
// casually. See the package # Lifecycle section for what each phase means.
type Status string

// The seven lifecycle phases. drafting/reviewing/executing/reviewing_result
// are transient; done/failed/cancelled are terminal (failed still admits a
// retry intent). See the package # Lifecycle section.
const (
	StatusDrafting        Status = "drafting"
	StatusReviewing       Status = "reviewing"
	StatusExecuting       Status = "executing"
	StatusReviewingResult Status = "reviewing_result"
	StatusDone            Status = "done"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
)

// MaxHistoryEntries caps the draft-version log kept on a [Proposal]. A long
// refine loop would otherwise grow [Proposal.History] without bound and bloat
// the serialized world snapshot; once the log would exceed this many entries
// [Proposal.SetDraft] elides the oldest, keeping the most recent window. The
// cap is generous enough that ordinary sessions never hit it.
const MaxHistoryEntries = 100

// HistoryEntry is one version snapshot in the proposal's draft history.
// Feedback is stored on the entry it changed FROM, not the one it produced:
// it is the user feedback that prompted the NEXT version, so the log reads as
// "draft → feedback → next draft."
type HistoryEntry struct {
	Version    int            `json:"version"`
	Draft      map[string]any `json:"draft"`
	Feedback   string         `json:"feedback,omitempty"` // feedback that produced the NEXT version
	ProducedAt string         `json:"produced_at"`        // RFC3339
}

// Result holds the outcome of a proposal execution. OK distinguishes a
// successful run from a failure; Error carries the failure message for both
// host-domain errors (a non-empty [host.Result.Error]) and infra errors, so a
// caller can render one without inspecting which kind it was. The zero Result
// is a not-yet-run / failed state (OK false, no message).
type Result struct {
	OK         bool           `json:"ok"`
	Data       map[string]any `json:"data,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at,omitempty"`
}

// Proposal is the runtime state of one active proposal instance — the mutable
// per-session counterpart to the static [app.ProposalKind] that declares its
// shape. It is stored under [WorldKey] in world state via [Proposal.ToMap].
//
// A Proposal is NOT safe for concurrent use: every mutator writes
// [Proposal.UpdatedAt] and most touch [Proposal.Current] or
// [Proposal.History]. It assumes a single owner ([Proposal.OwnerSession]);
// the caller serializes access (the orchestrator already holds the world lock
// for the turn). The zero value is not usable — construct via [New] or
// [FromMap].
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

// WorldKey is the reserved world variable name under which the active
// proposal's [Proposal.ToMap] form is stored. The leading "$" marks it as a
// runtime-owned key authors reference but do not assign directly.
const WorldKey = "$proposal"

// New mints a Proposal in [StatusDrafting] with an empty (non-nil) draft and
// history and the create/update timestamps stamped to now. It is the only
// way besides [FromMap] to obtain a usable value; the zero [Proposal] is not.
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

// SetDraft replaces the current draft and appends a [HistoryEntry] for it.
// feedback is the user feedback that prompted THIS draft (empty for the first
// one); when present it is recorded on the previous entry, since it explains
// the move into the new version rather than the new version itself. The draft
// is shallow-copied on the way in and again into history, so the two snapshots
// do not alias each other or the caller's map. The history log is soft-capped
// at [MaxHistoryEntries].
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
	// Soft cap: elide oldest, keep the most recent MaxHistoryEntries.
	if len(p.History) > MaxHistoryEntries {
		p.History = p.History[len(p.History)-MaxHistoryEntries:]
	}
	p.UpdatedAt = now
}

// EditField sets one field of the current draft in place, backing the edit
// intent that tweaks a single value without a full redraft. Unlike
// [Proposal.SetDraft] it does NOT append a history entry, so a string of
// edits collapses into the current version rather than flooding the log. It
// lazily allocates [Proposal.Current] if nil.
func (p *Proposal) EditField(key string, value any) {
	if p.Current == nil {
		p.Current = make(map[string]any)
	}
	p.Current[key] = value
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// Transition records a new [Status] and stamps [Proposal.UpdatedAt]. It does
// not validate the move: legality of phase ordering is owned by the
// orchestrator's intent wiring (see the package # Non-goals), so this method
// stays a dumb recorder that cannot disagree with that single authority.
func (p *Proposal) Transition(s Status) {
	p.Status = s
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// SetResult stores the execution outcome (copied by value) and stamps
// [Proposal.UpdatedAt]. [Execute] calls it; callers rarely need to. It does
// not itself transition status — the caller decides done vs. failed.
func (p *Proposal) SetResult(r Result) {
	p.Result = &r
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// ToMap renders the proposal as the plain map[string]any stored under
// [WorldKey] in world state. It exists because world vars are an untyped
// JSON-shaped bag, not Go structs, so the proposal must flatten to that shape
// to travel through a snapshot and back via [FromMap]. Optional fields
// (result, job_ref, seeded_from, per-entry feedback) are omitted when empty,
// matching the omitempty JSON tags so the round-trip is stable.
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

// FromMap reconstructs a Proposal from its [Proposal.ToMap] world-state form,
// the inverse of that method. It returns nil when raw is nil or not a
// map[string]any (the signal for "no active proposal"), and otherwise tolerates
// missing or wrongly-typed fields by leaving them at their zero value rather
// than failing — world state may have been written by an older schema. Because
// JSON decodes numbers as float64, it accepts both int and float64 for
// [HistoryEntry.Version].
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

// ValidateAgainstSchema checks a draft against the kind's declared field
// schema before it is accepted for execution, so a malformed draft is caught
// at review time rather than blowing up the host handler. It compiles the
// kind's shorthand schema to a draft-2020-12 JSON Schema and validates the
// draft against it, returning a multi-line error (one "location: reason" per
// leaf failure, mirroring internal/mcp/validator.go) so the user sees every
// problem at once. A nil kind or an empty schema means "nothing to check" and
// returns nil; non-nil errors are wrapped with a "proposal:" prefix naming
// the failing stage.
func ValidateAgainstSchema(draft map[string]any, kind *app.ProposalKind) error {
	if kind == nil || len(kind.Schema) == 0 {
		return nil
	}
	schemaBytes, err := app.ShorthandToJSONSchema(kind.Schema)
	if err != nil {
		return fmt.Errorf("proposal: build schema: %w", err)
	}
	var doc any
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		return fmt.Errorf("proposal: parse schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("proposal-schema.json", doc); err != nil {
		return fmt.Errorf("proposal: register schema: %w", err)
	}
	sch, err := compiler.Compile("proposal-schema.json")
	if err != nil {
		return fmt.Errorf("proposal: compile schema: %w", err)
	}
	if err := sch.Validate(any(draft)); err != nil {
		return fmt.Errorf("proposal: draft does not match schema:\n%s", formatSchemaErrors(err))
	}
	return nil
}

// formatSchemaErrors flattens a jsonschema.ValidationError into one
// "instance/location: reason" line per leaf failure.
func formatSchemaErrors(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	var lines []string
	collectSchemaLeaves(ve.BasicOutput(), &lines)
	if len(lines) == 0 {
		return ve.Error()
	}
	return "  - " + strings.Join(lines, "\n  - ")
}

func collectSchemaLeaves(unit *jsonschema.OutputUnit, out *[]string) {
	if unit == nil || unit.Valid {
		return
	}
	if len(unit.Errors) == 0 && unit.Error != nil {
		loc := unit.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		*out = append(*out, loc+": "+unit.Error.String())
		return
	}
	for i := range unit.Errors {
		collectSchemaLeaves(&unit.Errors[i], out)
	}
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

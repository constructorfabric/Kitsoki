package artifactjob

import (
	"time"

	"kitsoki/internal/app"
)

// JobID is the stable, user-facing identity for a durable artifact job.
type JobID string

// InstanceID names an optional editable artifact workspace attached to a job.
type InstanceID string

// Status is the durable product-state vocabulary for artifact jobs. It is
// intentionally close to internal/jobs, with interrupted and archived added for
// records that can outlive a process or their active workspace.
type Status string

const (
	StatusRunning       Status = "running"
	StatusAwaitingInput Status = "awaiting_input"
	StatusInterrupted   Status = "interrupted"
	StatusDone          Status = "done"
	StatusFailed        Status = "failed"
	StatusCancelled     Status = "cancelled"
	StatusArchived      Status = "archived"
)

// Visibility describes who can see a job record. V1 is local/private first;
// shared/auth-gated values are stored now so hosted backends do not need a
// schema rewrite later.
type Visibility string

const (
	VisibilityLocal     Visibility = "local"
	VisibilityPrivate   Visibility = "private"
	VisibilityShared    Visibility = "shared"
	VisibilityAuthGated Visibility = "auth_gated"
)

// Origin describes the front door that created a job.
type Origin struct {
	Kind string
	Ref  string
	URL  string
}

// Job is the durable artifact-job registry row. It is a product record, not a
// scheduler goroutine: it may point at an internal/jobs row, an operation run, a
// dev-story workspace, or a GitHub dispatch.
type Job struct {
	ID                     JobID
	SessionID              app.SessionID
	AppID                  string
	Story                  string
	Origin                 Origin
	Status                 Status
	RunURL                 string
	TracePath              string
	WorkspaceInstanceID    InstanceID
	TerminalArtifactHandle string
	Summary                string
	Phase                  string
	Visibility             Visibility
	Owner                  string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	FinishedAt             *time.Time
	InterruptedReason      string
}

// RegisterRequest is the minimum input needed to create a job. Empty ID,
// Status, Visibility, and timestamps are filled by the store.
type RegisterRequest struct {
	ID                     JobID
	SessionID              app.SessionID
	AppID                  string
	Story                  string
	Origin                 Origin
	Status                 Status
	RunURL                 string
	TracePath              string
	WorkspaceInstanceID    InstanceID
	TerminalArtifactHandle string
	Summary                string
	Phase                  string
	Visibility             Visibility
	Owner                  string
	CreatedAt              time.Time
}

// Update records a deterministic lifecycle fact about an existing job. Nil
// pointer fields mean "leave unchanged" so callers can update one projection
// without re-supplying the full row.
type Update struct {
	Status                 *Status
	RunURL                 *string
	TracePath              *string
	WorkspaceInstanceID    *InstanceID
	TerminalArtifactHandle *string
	Summary                *string
	Phase                  *string
	Visibility             *Visibility
	Owner                  *string
	FinishedAt             *time.Time
	InterruptedReason      *string
}

// ListFilter narrows registry queries. Zero values return recent non-archived
// rows for all apps/stories.
type ListFilter struct {
	AppID           string
	Story           string
	SessionID       app.SessionID
	Status          []Status
	IncludeArchived bool
	Limit           int
}

// Run is the queryable run row derived from immutable trace bytes and bound to
// an artifact job.
type Run struct {
	JobID     JobID
	SessionID app.SessionID
	Story     string
	Status    Status
	StartedAt time.Time
	EndedAt   *time.Time
	LastTurn  int
	TracePath string
}

// Artifact is a by-handle artifact index row for a stored run.
type Artifact struct {
	Handle    string
	JobID     JobID
	Kind      string
	MIME      string
	Label     string
	Path      string
	SizeBytes int64
	CreatedAt time.Time
}

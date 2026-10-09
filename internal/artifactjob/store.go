package artifactjob

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a requested artifact job or indexed run is absent.
var ErrNotFound = errors.New("artifactjob: not found")

// Store persists durable artifact-job rows. Implementations must be safe for
// concurrent use when their underlying database handle is safe for concurrent
// use.
type Store interface {
	Register(context.Context, RegisterRequest) (Job, error)
	BindRun(ctx context.Context, id JobID, sessionID string, runURL string, tracePath string) (Job, error)
	Update(ctx context.Context, id JobID, update Update) (Job, error)
	Attach(ctx context.Context, id JobID, sessionID string) (Job, error)
	Get(context.Context, JobID) (Job, error)
	List(context.Context, ListFilter) ([]Job, error)
	Archive(context.Context, JobID) (Job, error)
	SweepInterrupted(context.Context, string) (int64, error)
}

// RunIndex is the durable projection over immutable trace JSONL and artifact
// events. Dropping and rebuilding this index from trace files must reproduce the
// same rows.
type RunIndex interface {
	UpsertRun(context.Context, Run) error
	UpsertArtifact(context.Context, Artifact) error
	GetRun(context.Context, JobID) (Run, error)
	ListRuns(context.Context, ListFilter) ([]Run, error)
	Artifacts(context.Context, JobID) ([]Artifact, error)
	ResolveArtifact(ctx context.Context, jobID JobID, handle string) (Artifact, error)
}

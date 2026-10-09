package artifactjob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/ulid"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS artifact_jobs (
  id                       TEXT PRIMARY KEY,
  session_id               TEXT NOT NULL DEFAULT '',
  app_id                   TEXT NOT NULL DEFAULT '',
  story                    TEXT NOT NULL DEFAULT '',
  origin_kind              TEXT NOT NULL DEFAULT '',
  origin_ref               TEXT NOT NULL DEFAULT '',
  origin_url               TEXT NOT NULL DEFAULT '',
  status                   TEXT NOT NULL,
  run_url                  TEXT NOT NULL DEFAULT '',
  trace_path               TEXT NOT NULL DEFAULT '',
  workspace_instance_id    TEXT NOT NULL DEFAULT '',
  terminal_artifact_handle TEXT NOT NULL DEFAULT '',
  summary                  TEXT NOT NULL DEFAULT '',
  phase                    TEXT NOT NULL DEFAULT '',
  visibility               TEXT NOT NULL DEFAULT 'local',
  owner                    TEXT NOT NULL DEFAULT '',
  interrupted_reason       TEXT NOT NULL DEFAULT '',
  created_at               INTEGER NOT NULL,
  updated_at               INTEGER NOT NULL,
  finished_at              INTEGER
) STRICT;

CREATE INDEX IF NOT EXISTS artifact_jobs_app_story_updated ON artifact_jobs(app_id, story, updated_at DESC);
CREATE INDEX IF NOT EXISTS artifact_jobs_session_status ON artifact_jobs(session_id, status);
CREATE INDEX IF NOT EXISTS artifact_jobs_origin ON artifact_jobs(origin_kind, origin_ref);

CREATE TABLE IF NOT EXISTS artifact_runs (
  job_id      TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL,
  story       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER,
  last_turn   INTEGER NOT NULL DEFAULT 0,
  trace_path  TEXT NOT NULL,
  FOREIGN KEY(job_id) REFERENCES artifact_jobs(id) ON DELETE CASCADE
) STRICT;

CREATE INDEX IF NOT EXISTS artifact_runs_session ON artifact_runs(session_id);
CREATE INDEX IF NOT EXISTS artifact_runs_status_started ON artifact_runs(status, started_at DESC);

CREATE TABLE IF NOT EXISTS artifact_run_artifacts (
  job_id      TEXT NOT NULL,
  handle      TEXT NOT NULL,
  kind        TEXT NOT NULL,
  mime        TEXT NOT NULL,
  label       TEXT NOT NULL DEFAULT '',
  path        TEXT NOT NULL,
  size_bytes  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY(job_id, handle),
  FOREIGN KEY(job_id) REFERENCES artifact_runs(job_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX IF NOT EXISTS artifact_run_artifacts_job_created ON artifact_run_artifacts(job_id, created_at);
`

// SQLiteStore stores artifact jobs and run/artifact indexes in the same SQLite
// database used by the local session store.
type SQLiteStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, fmt.Errorf("artifactjob.NewSQLiteStore: nil db")
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		return nil, fmt.Errorf("artifactjob.NewSQLiteStore: schema migration: %w", err)
	}
	return &SQLiteStore{db: db, now: time.Now}, nil
}

func (s *SQLiteStore) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *SQLiteStore) Register(ctx context.Context, req RegisterRequest) (Job, error) {
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

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifact_jobs
		  (id, session_id, app_id, story, origin_kind, origin_ref, origin_url,
		   status, run_url, trace_path, workspace_instance_id, terminal_artifact_handle,
		   summary, phase, visibility, owner, interrupted_reason, created_at, updated_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, string(j.SessionID), j.AppID, j.Story, j.Origin.Kind, j.Origin.Ref, j.Origin.URL,
		string(j.Status), j.RunURL, j.TracePath, string(j.WorkspaceInstanceID), j.TerminalArtifactHandle,
		j.Summary, j.Phase, string(j.Visibility), j.Owner, j.InterruptedReason,
		unixMillis(j.CreatedAt), unixMillis(j.UpdatedAt), nullableTimeMillis(j.FinishedAt),
	)
	if err != nil {
		return Job{}, fmt.Errorf("artifactjob.Register: %w", err)
	}
	return j, nil
}

func (s *SQLiteStore) BindRun(ctx context.Context, id JobID, sessionID string, runURL string, tracePath string) (Job, error) {
	now := s.now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE artifact_jobs
		SET session_id=?, run_url=?, trace_path=?, updated_at=?
		WHERE id=?`, sessionID, runURL, tracePath, unixMillis(now), id)
	if err != nil {
		return Job{}, fmt.Errorf("artifactjob.BindRun: %w", err)
	}
	if err := requireAffected(res); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *SQLiteStore) Update(ctx context.Context, id JobID, update Update) (Job, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	applyUpdate(&j, update)
	j.UpdatedAt = s.now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE artifact_jobs SET
		  status=?, run_url=?, trace_path=?, workspace_instance_id=?, terminal_artifact_handle=?,
		  summary=?, phase=?, visibility=?, owner=?, interrupted_reason=?, updated_at=?, finished_at=?
		WHERE id=?`,
		string(j.Status), j.RunURL, j.TracePath, string(j.WorkspaceInstanceID), j.TerminalArtifactHandle,
		j.Summary, j.Phase, string(j.Visibility), j.Owner, j.InterruptedReason, unixMillis(j.UpdatedAt), nullableTimeMillis(j.FinishedAt), id)
	if err != nil {
		return Job{}, fmt.Errorf("artifactjob.Update: %w", err)
	}
	if err := requireAffected(res); err != nil {
		return Job{}, err
	}
	return j, nil
}

func (s *SQLiteStore) Attach(ctx context.Context, id JobID, sessionID string) (Job, error) {
	now := s.now().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE artifact_jobs SET session_id=?, updated_at=? WHERE id=?`, sessionID, unixMillis(now), id)
	if err != nil {
		return Job{}, fmt.Errorf("artifactjob.Attach: %w", err)
	}
	if err := requireAffected(res); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *SQLiteStore) Get(ctx context.Context, id JobID) (Job, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, app_id, story, origin_kind, origin_ref, origin_url,
		       status, run_url, trace_path, workspace_instance_id, terminal_artifact_handle,
		       summary, phase, visibility, owner, interrupted_reason, created_at, updated_at, finished_at
		FROM artifact_jobs WHERE id=?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

func (s *SQLiteStore) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	where, args := buildJobWhere(filter)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, session_id, app_id, story, origin_kind, origin_ref, origin_url,
		       status, run_url, trace_path, workspace_instance_id, terminal_artifact_handle,
		       summary, phase, visibility, owner, interrupted_reason, created_at, updated_at, finished_at
		FROM artifact_jobs ` + where + ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("artifactjob.List: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *SQLiteStore) Archive(ctx context.Context, id JobID) (Job, error) {
	status := StatusArchived
	return s.Update(ctx, id, Update{Status: &status})
}

func (s *SQLiteStore) SweepInterrupted(ctx context.Context, reason string) (int64, error) {
	now := s.now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE artifact_jobs
		SET status=?, interrupted_reason=?, updated_at=?
		WHERE status IN (?, ?)`,
		string(StatusInterrupted), reason, unixMillis(now), string(StatusRunning), string(StatusAwaitingInput))
	if err != nil {
		return 0, fmt.Errorf("artifactjob.SweepInterrupted: %w", err)
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) UpsertRun(ctx context.Context, run Run) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifact_runs (job_id, session_id, story, status, started_at, ended_at, last_turn, trace_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
		  session_id=excluded.session_id,
		  story=excluded.story,
		  status=excluded.status,
		  started_at=excluded.started_at,
		  ended_at=excluded.ended_at,
		  last_turn=excluded.last_turn,
		  trace_path=excluded.trace_path`,
		run.JobID, string(run.SessionID), run.Story, string(run.Status), unixMillis(run.StartedAt), nullableTimeMillis(run.EndedAt), run.LastTurn, run.TracePath)
	if err != nil {
		return fmt.Errorf("artifactjob.UpsertRun: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpsertArtifact(ctx context.Context, a Artifact) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifact_run_artifacts (job_id, handle, kind, mime, label, path, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, handle) DO UPDATE SET
		  kind=excluded.kind,
		  mime=excluded.mime,
		  label=excluded.label,
		  path=excluded.path,
		  size_bytes=excluded.size_bytes,
		  created_at=excluded.created_at`,
		a.JobID, a.Handle, a.Kind, a.MIME, a.Label, a.Path, a.SizeBytes, unixMillis(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("artifactjob.UpsertArtifact: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, id JobID) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT job_id, session_id, story, status, started_at, ended_at, last_turn, trace_path FROM artifact_runs WHERE job_id=?`, id)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

func (s *SQLiteStore) ListRuns(ctx context.Context, filter ListFilter) ([]Run, error) {
	where, args := buildRunWhere(filter)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, session_id, story, status, started_at, ended_at, last_turn, trace_path FROM artifact_runs `+where+` ORDER BY started_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("artifactjob.ListRuns: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) Artifacts(ctx context.Context, id JobID) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT handle, job_id, kind, mime, label, path, size_bytes, created_at FROM artifact_run_artifacts WHERE job_id=? ORDER BY created_at, handle`, id)
	if err != nil {
		return nil, fmt.Errorf("artifactjob.Artifacts: %w", err)
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ResolveArtifact(ctx context.Context, id JobID, handle string) (Artifact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT handle, job_id, kind, mime, label, path, size_bytes, created_at FROM artifact_run_artifacts WHERE job_id=? AND handle=?`, id, handle)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	return a, err
}

type rowScanner interface{ Scan(...any) error }

type rowsScanner interface {
	Scan(...any) error
	Next() bool
	Err() error
}

func scanJob(row rowScanner) (Job, error) {
	var j Job
	var sessionID, status, visibility, workspaceID string
	var created, updated int64
	var finished sql.NullInt64
	err := row.Scan(&j.ID, &sessionID, &j.AppID, &j.Story, &j.Origin.Kind, &j.Origin.Ref, &j.Origin.URL,
		&status, &j.RunURL, &j.TracePath, &workspaceID, &j.TerminalArtifactHandle,
		&j.Summary, &j.Phase, &visibility, &j.Owner, &j.InterruptedReason, &created, &updated, &finished)
	if err != nil {
		return Job{}, err
	}
	j.SessionID = app.SessionID(sessionID)
	j.Status = Status(status)
	j.Visibility = Visibility(visibility)
	j.WorkspaceInstanceID = InstanceID(workspaceID)
	j.CreatedAt = time.UnixMilli(created).UTC()
	j.UpdatedAt = time.UnixMilli(updated).UTC()
	j.FinishedAt = timeFromNullMillis(finished)
	return j, nil
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanRun(row rowScanner) (Run, error) {
	var r Run
	var sessionID, status string
	var started int64
	var ended sql.NullInt64
	err := row.Scan(&r.JobID, &sessionID, &r.Story, &status, &started, &ended, &r.LastTurn, &r.TracePath)
	if err != nil {
		return Run{}, err
	}
	r.SessionID = app.SessionID(sessionID)
	r.Status = Status(status)
	r.StartedAt = time.UnixMilli(started).UTC()
	r.EndedAt = timeFromNullMillis(ended)
	return r, nil
}

func scanArtifact(row rowScanner) (Artifact, error) {
	var a Artifact
	var created int64
	err := row.Scan(&a.Handle, &a.JobID, &a.Kind, &a.MIME, &a.Label, &a.Path, &a.SizeBytes, &created)
	if err != nil {
		return Artifact{}, err
	}
	a.CreatedAt = time.UnixMilli(created).UTC()
	return a, nil
}

func buildJobWhere(f ListFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.AppID != "" {
		clauses = append(clauses, "app_id=?")
		args = append(args, f.AppID)
	}
	if f.Story != "" {
		clauses = append(clauses, "story=?")
		args = append(args, f.Story)
	}
	if f.SessionID != "" {
		clauses = append(clauses, "session_id=?")
		args = append(args, string(f.SessionID))
	}
	if !f.IncludeArchived {
		clauses = append(clauses, "status<>?")
		args = append(args, string(StatusArchived))
	}
	appendStatusWhere(&clauses, &args, f.Status)
	return whereSQL(clauses), args
}

func buildRunWhere(f ListFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.Story != "" {
		clauses = append(clauses, "story=?")
		args = append(args, f.Story)
	}
	if f.SessionID != "" {
		clauses = append(clauses, "session_id=?")
		args = append(args, string(f.SessionID))
	}
	appendStatusWhere(&clauses, &args, f.Status)
	return whereSQL(clauses), args
}

func appendStatusWhere(clauses *[]string, args *[]any, statuses []Status) {
	if len(statuses) == 0 {
		return
	}
	parts := make([]string, len(statuses))
	for i, s := range statuses {
		parts[i] = "?"
		*args = append(*args, string(s))
	}
	*clauses = append(*clauses, "status IN ("+strings.Join(parts, ",")+")")
}

func whereSQL(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(clauses, " AND ")
}

func requireAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func unixMillis(t time.Time) int64 { return t.UTC().UnixMilli() }

func nullableTimeMillis(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().UnixMilli()
}

func timeFromNullMillis(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

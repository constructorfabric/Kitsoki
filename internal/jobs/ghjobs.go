package jobs

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/ulid"

	_ "modernc.org/sqlite"
)

//go:embed ghjobs.sql
var ghJobsSchemaDDL string

// GitHub-job lifecycle states. A job advances
// queued -> claimed -> running -> (done | failed), or parks at
// awaiting_guidance when the router cannot classify the mention.
const (
	GHQueued           = "queued"
	GHClaimed          = "claimed"
	GHRunning          = "running"
	GHAwaitingGuidance = "awaiting_guidance"
	GHDone             = "done"
	GHFailed           = "failed"
)

// GHJob is one @kitsoki mention promoted to a unit of work. Its identity is the
// mention's origin_ref (github:<repo>/<kind>/<number>), which makes Claim
// idempotent: a re-mention of the same issue/PR attaches to the existing row
// rather than spawning a second run.
type GHJob struct {
	JobID        string
	OriginRef    string
	Repo         string
	ObjectKind   string // issue | pr
	ObjectNumber string
	Story        string
	State        string
	WorkerID     string
	RunID        string
	RunURL       string
	CommentID    string
	AttemptCount int
	IncidentURL  string
	Metadata     map[string]string
	ErrMsg       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GHJobEvent records an operator-facing lifecycle transition for a GitHub job.
type GHJobEvent struct {
	ID        int64
	JobID     string
	State     string
	Message   string
	CreatedAt time.Time
}

// GHJobAsset records a file asset associated with a GitHub job.
type GHJobAsset struct {
	ID        int64
	JobID     string
	Name      string
	MimeType  string
	SizeBytes int64
	CreatedAt time.Time
}

// GHMention is the minimal mention shape the store needs to mint a job row.
// internal/ghagent.Mention satisfies it via its exported fields; keeping the
// store's dependency a tiny local interface avoids an import cycle
// (ghagent imports jobs, not the reverse).
type GHMention struct {
	OriginRef    string
	Repo         string
	ObjectKind   string
	ObjectNumber string
}

// GHJobStore is the SQLite-backed claim/lifecycle store for GitHub jobs. It is
// purely additive to JobStore: a separate gh_jobs table, applied idempotently,
// sharing the same *sql.DB and modernc.org/sqlite driver. The natural key is
// origin_ref, which the session-scoped jobs table does not carry — hence a
// distinct table rather than an overload of UpsertJob (which is INSERT OR
// REPLACE and so cannot serve an idempotent-attach Claim).
type GHJobStore struct {
	db      *sql.DB
	DataDir string // root directory for on-disk asset blobs; empty disables Put/Get
}

// NewGHJobStore applies the gh_jobs DDL idempotently and configures WAL +
// busy_timeout on the connection so the BEGIN IMMEDIATE used by Claim
// serializes writers (in-process) and cross-process workers back off rather
// than erroring SQLITE_BUSY. Mirrors NewJobStore's construction idiom.
func NewGHJobStore(db *sql.DB) (*GHJobStore, error) {
	// Best-effort pragmas; :memory: ignores WAL but accepts busy_timeout.
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	_, _ = db.Exec("PRAGMA busy_timeout=5000")
	if _, err := db.Exec(ghJobsSchemaDDL); err != nil {
		return nil, fmt.Errorf("jobs.NewGHJobStore: schema migration: %w", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE gh_jobs ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE gh_jobs ADD COLUMN incident_url TEXT`,
		`ALTER TABLE gh_jobs ADD COLUMN metadata_json TEXT`,
	} {
		if _, err := db.Exec(stmt); err != nil && !isSQLiteDuplicateColumn(err) {
			return nil, fmt.Errorf("jobs.NewGHJobStore: compatibility migration: %w", err)
		}
	}
	return &GHJobStore{db: db}, nil
}

// Claim atomically attaches a worker to the mention's job. It inserts a queued
// row if none exists, then performs a guarded queued->claimed CAS scoped to
// origin_ref. Exactly one concurrent caller wins the CAS (won=true); a row
// already past queued means a re-mention, so won=false and the caller attaches
// to the existing run. The whole sequence runs inside a BEGIN IMMEDIATE tx,
// which under WAL takes a write lock up front and serializes the CAS.
func (s *GHJobStore) Claim(ctx context.Context, m GHMention, workerID string) (job *GHJob, won bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("jobs.Claim: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	// BEGIN IMMEDIATE: promote to a write lock immediately so concurrent
	// claimers serialize on the CAS rather than racing a deferred upgrade.
	if _, err = tx.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		// database/sql already opened a (deferred) tx; an explicit BEGIN
		// errors. Tolerate it — the guarded UPDATE below is still atomic
		// within this tx. (modernc sqlite returns "cannot start a
		// transaction within a transaction".)
		err = nil
	}

	now := time.Now().UnixMilli()
	jobID := ulid.New()
	if _, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO gh_jobs
		   (job_id, origin_ref, repo, object_kind, object_number, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, m.OriginRef, m.Repo, m.ObjectKind, m.ObjectNumber, GHQueued, now, now,
	); err != nil {
		return nil, false, fmt.Errorf("jobs.Claim: insert: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE gh_jobs SET state=?, worker_id=?, updated_at=?
		   WHERE origin_ref=? AND state=?`,
		GHClaimed, workerID, time.Now().UnixMilli(), m.OriginRef, GHQueued,
	)
	if err != nil {
		return nil, false, fmt.Errorf("jobs.Claim: cas: %w", err)
	}
	affected, _ := res.RowsAffected()
	won = affected == 1

	job, err = scanGHJobTx(ctx, tx, m.OriginRef)
	if err != nil {
		return nil, false, fmt.Errorf("jobs.Claim: read-back: %w", err)
	}
	if won {
		if err = insertGHJobEventTx(ctx, tx, job.JobID, GHClaimed, "claimed by "+workerID); err != nil {
			return nil, false, fmt.Errorf("jobs.Claim: event: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("jobs.Claim: commit: %w", err)
	}
	return job, won, nil
}

// Enqueue inserts a queued GitHub-agent job without claiming it. It is the
// native handoff for producers that have already filed or discovered a GitHub
// issue and want the long-running gh-agent worker to drain it later. The
// origin_ref natural key keeps enqueue idempotent: if the job already exists,
// the existing row is returned unchanged.
func (s *GHJobStore) Enqueue(ctx context.Context, m GHMention, story string) (job *GHJob, created bool, err error) {
	if strings.TrimSpace(m.OriginRef) == "" {
		return nil, false, fmt.Errorf("jobs.Enqueue: origin_ref is required")
	}
	if strings.TrimSpace(m.Repo) == "" {
		return nil, false, fmt.Errorf("jobs.Enqueue: repo is required")
	}
	if strings.TrimSpace(m.ObjectKind) == "" {
		return nil, false, fmt.Errorf("jobs.Enqueue: object_kind is required")
	}
	if strings.TrimSpace(m.ObjectNumber) == "" {
		return nil, false, fmt.Errorf("jobs.Enqueue: object_number is required")
	}
	now := time.Now().UnixMilli()
	jobID := ulid.New()
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO gh_jobs
		   (job_id, origin_ref, repo, object_kind, object_number, story, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, m.OriginRef, m.Repo, m.ObjectKind, m.ObjectNumber, story, GHQueued, now, now,
	)
	if err != nil {
		return nil, false, fmt.Errorf("jobs.Enqueue: insert: %w", err)
	}
	affected, _ := res.RowsAffected()
	created = affected == 1
	job, err = s.GetByOriginRef(ctx, m.OriginRef)
	if err != nil {
		return nil, created, fmt.Errorf("jobs.Enqueue: read-back: %w", err)
	}
	if created {
		if err := s.RecordEvent(ctx, job.JobID, GHQueued, "queued by producer"); err != nil {
			return nil, created, err
		}
		if strings.TrimSpace(story) != "" {
			if err := s.RecordEvent(ctx, job.JobID, "story", story); err != nil {
				return nil, created, err
			}
		}
	}
	return job, created, nil
}

// MergeMetadata overlays producer-supplied context onto a job without
// disturbing existing keys not mentioned by the caller.
func (s *GHJobStore) MergeMetadata(ctx context.Context, jobID string, metadata map[string]string) error {
	cleaned := cleanGHJobMetadata(metadata)
	if len(cleaned) == 0 {
		return nil
	}
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("jobs.MergeMetadata: read-back: %w", err)
	}
	merged := map[string]string{}
	for k, v := range job.Metadata {
		merged[k] = v
	}
	for k, v := range cleaned {
		merged[k] = v
	}
	encoded, err := encodeGHJobMetadata(merged)
	if err != nil {
		return fmt.Errorf("jobs.MergeMetadata: encode: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET metadata_json=?, updated_at=? WHERE job_id=?`,
		encoded, time.Now().UnixMilli(), jobID); err != nil {
		return fmt.Errorf("jobs.MergeMetadata: update: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "metadata", fmt.Sprintf("%d key(s)", len(cleaned))); err != nil {
		return err
	}
	return nil
}

// GetByOriginRef returns the job for an origin_ref, or sql.ErrNoRows.
func (s *GHJobStore) GetByOriginRef(ctx context.Context, originRef string) (*GHJob, error) {
	return scanGHJob(ctx, s.db, "origin_ref", originRef)
}

// GetJob returns the job for a job_id, or sql.ErrNoRows.
func (s *GHJobStore) GetJob(ctx context.Context, jobID string) (*GHJob, error) {
	return scanGHJob(ctx, s.db, "job_id", jobID)
}

// ListRecent returns recent GitHub-agent jobs ordered by most recent update.
func (s *GHJobStore) ListRecent(ctx context.Context, limit int) ([]*GHJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ghJobCols+` FROM gh_jobs
		  ORDER BY updated_at DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("jobs.ListRecent: %w", err)
	}
	defer rows.Close()
	var out []*GHJob
	for rows.Next() {
		job, err := scanGHJobScanner(rows)
		if err != nil {
			return nil, fmt.Errorf("jobs.ListRecent: scan: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs.ListRecent: rows: %w", err)
	}
	return out, nil
}

// Advance transitions a job to newState, recording errMsg (typically only on
// the failed transition).
func (s *GHJobStore) Advance(ctx context.Context, jobID, newState, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET state=?, err_msg=?, updated_at=? WHERE job_id=?`,
		newState, errMsg, time.Now().UnixMilli(), jobID)
	if err != nil {
		return fmt.Errorf("jobs.Advance: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, newState, errMsg); err != nil {
		return err
	}
	return nil
}

// SetStory records the chosen story path for a claimed job.
func (s *GHJobStore) SetStory(ctx context.Context, jobID, story string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET story=?, updated_at=? WHERE job_id=?`,
		story, time.Now().UnixMilli(), jobID)
	if err != nil {
		return fmt.Errorf("jobs.SetStory: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "story", story); err != nil {
		return err
	}
	return nil
}

// SetComment captures the rolling-status comment id on first Post.
func (s *GHJobStore) SetComment(ctx context.Context, jobID, commentID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET comment_id=?, updated_at=? WHERE job_id=?`,
		commentID, time.Now().UnixMilli(), jobID)
	if err != nil {
		return fmt.Errorf("jobs.SetComment: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "comment", commentID); err != nil {
		return err
	}
	return nil
}

// SetRunURL records the spawned run's id + url for the ack.
func (s *GHJobStore) SetRunURL(ctx context.Context, jobID, runID, runURL string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET run_id=?, run_url=?, updated_at=? WHERE job_id=?`,
		runID, runURL, time.Now().UnixMilli(), jobID)
	if err != nil {
		return fmt.Errorf("jobs.SetRunURL: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "run_url", runURL); err != nil {
		return err
	}
	return nil
}

// BumpAttempt increments the durable retry/escalation counter.
func (s *GHJobStore) BumpAttempt(ctx context.Context, jobID string) (int, error) {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET attempt_count=attempt_count+1, updated_at=? WHERE job_id=?`,
		time.Now().UnixMilli(), jobID)
	if err != nil {
		return 0, fmt.Errorf("jobs.BumpAttempt: %w", err)
	}
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return 0, fmt.Errorf("jobs.BumpAttempt: read-back: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "attempt", fmt.Sprintf("%d", job.AttemptCount)); err != nil {
		return 0, err
	}
	return job.AttemptCount, nil
}

// ListQueued returns durable jobs waiting to be claimed by the gh-agent worker.
func (s *GHJobStore) ListQueued(ctx context.Context, limit int) ([]*GHJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ghJobCols+` FROM gh_jobs
		  WHERE state=?
		  ORDER BY updated_at ASC LIMIT ?`,
		GHQueued, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs.ListQueued: %w", err)
	}
	defer rows.Close()
	var out []*GHJob
	for rows.Next() {
		job, err := scanGHJobScanner(rows)
		if err != nil {
			return nil, fmt.Errorf("jobs.ListQueued: scan: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs.ListQueued: rows: %w", err)
	}
	return out, nil
}

// SetIncidentURL records the operator incident created for a non-recoverable job.
func (s *GHJobStore) SetIncidentURL(ctx context.Context, jobID, incidentURL string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE gh_jobs SET incident_url=?, updated_at=? WHERE job_id=?`,
		incidentURL, time.Now().UnixMilli(), jobID)
	if err != nil {
		return fmt.Errorf("jobs.SetIncidentURL: %w", err)
	}
	if err := s.RecordEvent(ctx, jobID, "incident", incidentURL); err != nil {
		return err
	}
	return nil
}

// ListStuck returns active jobs that have not updated since cutoff.
func (s *GHJobStore) ListStuck(ctx context.Context, cutoff time.Time, limit int) ([]*GHJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ghJobCols+` FROM gh_jobs
		  WHERE state IN (?, ?) AND updated_at < ?
		  ORDER BY updated_at ASC LIMIT ?`,
		GHClaimed, GHRunning, cutoff.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("jobs.ListStuck: %w", err)
	}
	defer rows.Close()
	var out []*GHJob
	for rows.Next() {
		job, err := scanGHJobScanner(rows)
		if err != nil {
			return nil, fmt.Errorf("jobs.ListStuck: scan: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs.ListStuck: rows: %w", err)
	}
	return out, nil
}

// Events returns lifecycle events for jobID in insertion order.
func (s *GHJobStore) Events(ctx context.Context, jobID string) ([]GHJobEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, job_id, state, message, created_at
		   FROM gh_job_events WHERE job_id=? ORDER BY id ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs.Events: %w", err)
	}
	defer rows.Close()
	var events []GHJobEvent
	for rows.Next() {
		var ev GHJobEvent
		var createdMs int64
		if err := rows.Scan(&ev.ID, &ev.JobID, &ev.State, &ev.Message, &createdMs); err != nil {
			return nil, fmt.Errorf("jobs.Events: scan: %w", err)
		}
		ev.CreatedAt = time.UnixMilli(createdMs)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs.Events: rows: %w", err)
	}
	return events, nil
}

// RecordEvent appends an operator-visible lifecycle note.
func (s *GHJobStore) RecordEvent(ctx context.Context, jobID, state, message string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO gh_job_events (job_id, state, message, created_at) VALUES (?, ?, ?, ?)`,
		jobID, state, message, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("jobs.RecordEvent: %w", err)
	}
	return nil
}

type ghRowScanner interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const ghJobCols = `job_id, origin_ref, repo, object_kind, object_number,
	COALESCE(story,''), state, COALESCE(worker_id,''), COALESCE(run_id,''),
	COALESCE(run_url,''), COALESCE(comment_id,''), attempt_count,
	COALESCE(incident_url,''), COALESCE(metadata_json,''), COALESCE(err_msg,''),
	created_at, updated_at`

func scanGHJob(ctx context.Context, q ghRowScanner, col, val string) (*GHJob, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+ghJobCols+` FROM gh_jobs WHERE `+col+`=?`, val)
	return scanGHJobRow(row)
}

func scanGHJobTx(ctx context.Context, tx *sql.Tx, originRef string) (*GHJob, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+ghJobCols+` FROM gh_jobs WHERE origin_ref=?`, originRef)
	return scanGHJobRow(row)
}

func scanGHJobRow(row *sql.Row) (*GHJob, error) {
	return scanGHJobScanner(row)
}

type ghJobScanner interface {
	Scan(dest ...any) error
}

func scanGHJobScanner(row ghJobScanner) (*GHJob, error) {
	var j GHJob
	var createdMs, updatedMs int64
	var metadataJSON string
	if err := row.Scan(
		&j.JobID, &j.OriginRef, &j.Repo, &j.ObjectKind, &j.ObjectNumber,
		&j.Story, &j.State, &j.WorkerID, &j.RunID, &j.RunURL, &j.CommentID,
		&j.AttemptCount, &j.IncidentURL, &metadataJSON, &j.ErrMsg, &createdMs, &updatedMs,
	); err != nil {
		return nil, err
	}
	metadata, err := decodeGHJobMetadata(metadataJSON)
	if err != nil {
		return nil, err
	}
	j.Metadata = metadata
	j.CreatedAt = time.UnixMilli(createdMs)
	j.UpdatedAt = time.UnixMilli(updatedMs)
	return &j, nil
}

func cleanGHJobMetadata(metadata map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range metadata {
		key := strings.TrimSpace(k)
		value := strings.TrimSpace(v)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func encodeGHJobMetadata(metadata map[string]string) (string, error) {
	cleaned := cleanGHJobMetadata(metadata)
	if len(cleaned) == 0 {
		return "", nil
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeGHJobMetadata(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return cleanGHJobMetadata(out), nil
}

func insertGHJobEventTx(ctx context.Context, tx *sql.Tx, jobID, state, message string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO gh_job_events (job_id, state, message, created_at) VALUES (?, ?, ?, ?)`,
		jobID, state, message, time.Now().UnixMilli())
	return err
}

func isSQLiteDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

// PutAsset saves a binary asset file to disk associated with a job ID,
// and records its metadata in the sqlite DB.
func (s *GHJobStore) PutAsset(ctx context.Context, jobID, name, mimeType string, data []byte) error {
	if s.DataDir == "" {
		return fmt.Errorf("jobs.GHJobStore.PutAsset: DataDir is not configured")
	}

	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM gh_jobs WHERE job_id = ?", jobID).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("jobs.GHJobStore.PutAsset: job not found: %s", jobID)
		}
		return fmt.Errorf("jobs.GHJobStore.PutAsset: %w", err)
	}

	dir := filepath.Join(s.DataDir, "assets", jobID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("jobs.GHJobStore.PutAsset: %w", err)
	}

	tmpPath := filepath.Join(dir, name+".tmp")
	destPath := filepath.Join(dir, name)
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("jobs.GHJobStore.PutAsset: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("jobs.GHJobStore.PutAsset: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO gh_job_assets (job_id, name, mime_type, size_bytes, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		jobID, name, mimeType, len(data), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("jobs.GHJobStore.PutAsset: %w", err)
	}

	return nil
}

// GetAssetData returns the binary file content and its recorded MIME type.
func (s *GHJobStore) GetAssetData(ctx context.Context, jobID, name string) ([]byte, string, error) {
	if s.DataDir == "" {
		return nil, "", fmt.Errorf("jobs.GHJobStore.GetAssetData: DataDir is not configured")
	}

	var mimeType string
	err := s.db.QueryRowContext(ctx,
		"SELECT mime_type FROM gh_job_assets WHERE job_id = ? AND name = ?",
		jobID, name).Scan(&mimeType)
	if err != nil {
		return nil, "", fmt.Errorf("jobs.GHJobStore.GetAssetData: %w", err)
	}

	path := filepath.Join(s.DataDir, "assets", jobID, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("jobs.GHJobStore.GetAssetData: %w", err)
	}

	return data, mimeType, nil
}

// ListAssets lists all metadata for assets associated with a job ID.
func (s *GHJobStore) ListAssets(ctx context.Context, jobID string) ([]GHJobAsset, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, job_id, name, mime_type, size_bytes, created_at FROM gh_job_assets WHERE job_id = ? ORDER BY name",
		jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs.GHJobStore.ListAssets: %w", err)
	}
	defer rows.Close()

	var assets []GHJobAsset
	for rows.Next() {
		var a GHJobAsset
		var createdMs int64
		if err := rows.Scan(&a.ID, &a.JobID, &a.Name, &a.MimeType, &a.SizeBytes, &createdMs); err != nil {
			return nil, fmt.Errorf("jobs.GHJobStore.ListAssets: %w", err)
		}
		a.CreatedAt = time.UnixMilli(createdMs)
		assets = append(assets, a)
	}

	return assets, nil
}

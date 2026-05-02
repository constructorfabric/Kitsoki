// Package jobs — SQLite-backed persistence for jobs and notifications.
package jobs

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"hally/internal/app"
	"hally/internal/ulid"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var jobsSchemaDDL string

// NotificationSeverity is the severity level of a notification.
type NotificationSeverity string

const (
	SeverityInfo           NotificationSeverity = "info"
	SeveritySuccess        NotificationSeverity = "success"
	SeverityWarn           NotificationSeverity = "warn"
	SeverityError          NotificationSeverity = "error"
	SeverityActionRequired NotificationSeverity = "action_required"
)

// Notification is one inbox entry (§4.1).
type Notification struct {
	ID                 string
	SessionID          app.SessionID
	CreatedAt          time.Time
	ReadAt             *time.Time
	DismissedAt        *time.Time
	SnoozedUntil       *time.Time
	Severity           NotificationSeverity
	Title              string
	Body               string
	TeleportState      string
	TeleportSlots      map[string]any
	TeleportProposalID string
	TeleportJobID      string
	OriginKind         string // "job" | "external"
	OriginRef          string
	OriginURL          string
}

// JobStore provides SQLite-backed persistence for jobs and notifications.
// It operates on an existing *sql.DB (opened by the parent store package).
type JobStore struct {
	db *sql.DB
}

// NewJobStore creates a JobStore and applies the jobs/notifications schema migration.
func NewJobStore(db *sql.DB) (*JobStore, error) {
	if _, err := db.Exec(jobsSchemaDDL); err != nil {
		return nil, fmt.Errorf("jobs.NewJobStore: schema migration: %w", err)
	}
	return &JobStore{db: db}, nil
}

// UpsertJob inserts or replaces a job row.
func (js *JobStore) UpsertJob(ctx context.Context, j *Job) error {
	payloadJSON, err := json.Marshal(j.Payload)
	if err != nil {
		return fmt.Errorf("jobs.UpsertJob: marshal payload: %w", err)
	}
	var progressJSON []byte
	if j.Progress != nil {
		progressJSON, err = json.Marshal(j.Progress)
		if err != nil {
			return fmt.Errorf("jobs.UpsertJob: marshal progress: %w", err)
		}
	}
	var resultJSON []byte
	if j.Result != nil {
		resultJSON, err = json.Marshal(j.Result)
		if err != nil {
			return fmt.Errorf("jobs.UpsertJob: marshal result: %w", err)
		}
	}

	var startedAtMs *int64
	if j.StartedAt != nil {
		ms := j.StartedAt.UnixMilli()
		startedAtMs = &ms
	}
	var finishedAtMs *int64
	if j.FinishedAt != nil {
		ms := j.FinishedAt.UnixMilli()
		finishedAtMs = &ms
	}

	_, err = js.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO jobs
		  (id, session_id, kind, status, origin_state, origin_proposal_id,
		   payload, progress, result, error, retry_count,
		   created_at, updated_at, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, string(j.SessionID), j.Kind, string(j.Status),
		string(j.OriginState), j.OriginProposalID,
		string(payloadJSON),
		nullableBytes(progressJSON),
		nullableBytes(resultJSON),
		j.Error,
		j.RetryCount,
		j.CreatedAt.UnixMilli(),
		j.UpdatedAt.UnixMilli(),
		startedAtMs,
		finishedAtMs,
	)
	return err
}

// UpdateJobStatus updates the status, error, result, and timestamps of a job.
func (js *JobStore) UpdateJobStatus(ctx context.Context, id JobID, status JobStatus, errMsg string, result any, finishedAt *time.Time) error {
	var resultJSON []byte
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		resultJSON = b
	}
	var finishedAtMs *int64
	if finishedAt != nil {
		ms := finishedAt.UnixMilli()
		finishedAtMs = &ms
	}
	now := time.Now().UnixMilli()
	_, err := js.db.ExecContext(ctx, `
		UPDATE jobs SET status=?, error=?, result=?, finished_at=?, updated_at=? WHERE id=?`,
		string(status), errMsg, nullableBytes(resultJSON), finishedAtMs, now, id)
	return err
}

// GetJob returns the job with the given ID, or ErrJobNotFound if it does not exist.
func (js *JobStore) GetJob(ctx context.Context, id JobID) (*Job, error) {
	rows, err := js.db.QueryContext(ctx, `
		SELECT id, kind, status, origin_state, origin_proposal_id,
		       payload, error, retry_count, created_at, updated_at, started_at, finished_at
		FROM jobs WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, ErrJobNotFound
	}
	return &jobs[0], nil
}

// ListJobsByStatus returns jobs for a session matching a status.
func (js *JobStore) ListJobsByStatus(ctx context.Context, sessionID app.SessionID, status JobStatus) ([]Job, error) {
	rows, err := js.db.QueryContext(ctx, `
		SELECT id, kind, status, origin_state, origin_proposal_id,
		       payload, error, retry_count, created_at, updated_at, started_at, finished_at
		FROM jobs WHERE session_id=? AND status=? ORDER BY created_at DESC`,
		string(sessionID), string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		var (
			j               Job
			status          string
			originProposalID sql.NullString
			payloadJSON     string
			errStr          sql.NullString
			createdAtMs     int64
			updatedAtMs     int64
			startedAtMs     sql.NullInt64
			finishedAtMs    sql.NullInt64
		)
		if err := rows.Scan(
			&j.ID, &j.Kind, &status, (*string)(&j.OriginState), &originProposalID,
			&payloadJSON, &errStr, &j.RetryCount,
			&createdAtMs, &updatedAtMs, &startedAtMs, &finishedAtMs,
		); err != nil {
			return nil, err
		}
		j.Status = JobStatus(status)
		j.OriginProposalID = originProposalID.String
		_ = json.Unmarshal([]byte(payloadJSON), &j.Payload)
		j.Error = errStr.String
		j.CreatedAt = time.UnixMilli(createdAtMs)
		j.UpdatedAt = time.UnixMilli(updatedAtMs)
		if startedAtMs.Valid {
			t := time.UnixMilli(startedAtMs.Int64)
			j.StartedAt = &t
		}
		if finishedAtMs.Valid {
			t := time.UnixMilli(finishedAtMs.Int64)
			j.FinishedAt = &t
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// InsertNotification inserts a new notification row.
func (js *JobStore) InsertNotification(ctx context.Context, n *Notification) error {
	if n.ID == "" {
		n.ID = ulid.New()
	}
	var teleportSlotsJSON []byte
	if n.TeleportSlots != nil {
		b, err := json.Marshal(n.TeleportSlots)
		if err != nil {
			return err
		}
		teleportSlotsJSON = b
	}

	_, err := js.db.ExecContext(ctx, `
		INSERT INTO notifications
		  (id, session_id, created_at, severity, title, body,
		   teleport_state, teleport_slots, teleport_proposal_id, teleport_job_id,
		   origin_kind, origin_ref, origin_url)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, string(n.SessionID), n.CreatedAt.UnixMilli(),
		string(n.Severity), n.Title, n.Body,
		n.TeleportState, nullableBytes(teleportSlotsJSON),
		n.TeleportProposalID, n.TeleportJobID,
		n.OriginKind, n.OriginRef, n.OriginURL,
	)
	return err
}

// MarkNotificationRead sets read_at on a notification.
func (js *JobStore) MarkNotificationRead(ctx context.Context, id string) error {
	_, err := js.db.ExecContext(ctx,
		`UPDATE notifications SET read_at=? WHERE id=?`,
		time.Now().UnixMilli(), id)
	return err
}

// UnreadCount returns the count of unread, non-dismissed, non-snoozed notifications
// for a session, grouped by severity.
func (js *JobStore) UnreadCount(ctx context.Context, sessionID app.SessionID) (map[NotificationSeverity]int, error) {
	now := time.Now().UnixMilli()
	rows, err := js.db.QueryContext(ctx, `
		SELECT severity, COUNT(*) FROM notifications
		WHERE session_id=? AND read_at IS NULL AND dismissed_at IS NULL
		  AND (snoozed_until IS NULL OR snoozed_until < ?)
		GROUP BY severity`,
		string(sessionID), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[NotificationSeverity]int)
	for rows.Next() {
		var sev string
		var cnt int
		if err := rows.Scan(&sev, &cnt); err != nil {
			return nil, err
		}
		out[NotificationSeverity(sev)] = cnt
	}
	return out, rows.Err()
}

// ListNotifications returns unread, non-dismissed notifications for a session.
func (js *JobStore) ListNotifications(ctx context.Context, sessionID app.SessionID, limit int) ([]Notification, error) {
	q := `
		SELECT id, session_id, created_at, severity, title, body,
		       teleport_state, teleport_slots, teleport_proposal_id, teleport_job_id,
		       origin_kind, origin_ref
		FROM notifications
		WHERE session_id=? AND dismissed_at IS NULL
		ORDER BY created_at DESC`
	args := []any{string(sessionID)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := js.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		var createdAtMs int64
		var teleportSlotsJSON sql.NullString
		var teleportProposalID, teleportJobID sql.NullString
		if err := rows.Scan(
			&n.ID, (*string)(&n.SessionID), &createdAtMs,
			(*string)(&n.Severity), &n.Title, &n.Body,
			&n.TeleportState, &teleportSlotsJSON,
			&teleportProposalID, &teleportJobID,
			&n.OriginKind, &n.OriginRef,
		); err != nil {
			return nil, err
		}
		n.CreatedAt = time.UnixMilli(createdAtMs)
		n.TeleportProposalID = teleportProposalID.String
		n.TeleportJobID = teleportJobID.String
		if teleportSlotsJSON.Valid {
			_ = json.Unmarshal([]byte(teleportSlotsJSON.String), &n.TeleportSlots)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

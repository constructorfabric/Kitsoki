package jobs

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/ulid"

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

// Notification is one inbox entry.
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
// It operates on an existing *sql.DB (opened by the parent store package), so
// it shares that connection's concurrency guarantees: methods are safe for
// concurrent use to the extent the underlying *sql.DB is. The zero value is not
// usable — construct via [NewJobStore], which applies the schema migration. A
// nil *JobStore must not be called.
type JobStore struct {
	db            *sql.DB
	journalWriter journal.Writer
}

type notificationExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// JobStoreOption is a functional option for constructing a JobStore.
type JobStoreOption func(*JobStore)

// WithJobJournalWriter injects a journal.Writer into the JobStore. When non-nil,
// job and notification mutations emit typed journal entries alongside the SQLite
// writes — the dual-write described in docs/tracing. When nil (the default), no
// journal entries are written, which preserves backward compatibility.
func WithJobJournalWriter(jw journal.Writer) JobStoreOption {
	return func(js *JobStore) {
		js.journalWriter = jw
	}
}

// NewJobStore creates a JobStore and applies the jobs/notifications schema migration.
func NewJobStore(db *sql.DB, opts ...JobStoreOption) (*JobStore, error) {
	if _, err := db.Exec(jobsSchemaDDL); err != nil {
		return nil, fmt.Errorf("jobs.NewJobStore: schema migration: %w", err)
	}
	js := &JobStore{db: db}
	for _, o := range opts {
		o(js)
	}
	return js, nil
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
	if err != nil {
		return err
	}
	// Site 24: emit jobs.update for the upserted job row.
	if js.journalWriter != nil {
		body := mustJobJSON(map[string]any{
			"ops": []map[string]any{
				{"op": "add", "path": "", "value": map[string]any{
					"id":                 j.ID,
					"kind":               j.Kind,
					"status":             string(j.Status),
					"origin_state":       string(j.OriginState),
					"origin_proposal_id": j.OriginProposalID,
				}},
			},
		})
		_ = js.journalWriter.Append(journal.Entry{
			Ts:      time.Now(),
			Session: j.SessionID,
			Kind:    journal.KindJobsUpdate,
			Doc:     journal.DocID("jobs/" + j.ID),
			Body:    body,
		})
	}
	return nil
}

// SweepStaleJobs marks any row whose status is "running" or "awaiting_input"
// as failed with error=ErrProcessDied. Intended to be called once at scheduler
// construction: when a fresh process starts, no in-memory goroutine can own
// those rows, so by definition they are orphans from a prior crashed or
// killed process. Returns the number of rows affected.
func (js *JobStore) SweepStaleJobs(ctx context.Context) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := js.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, error = ?, finished_at = ?, updated_at = ?
		WHERE status IN (?, ?)`,
		string(JobFailed), ErrProcessDied, now, now,
		string(JobRunning), string(JobAwaitingInput))
	if err != nil {
		return 0, fmt.Errorf("jobs.SweepStaleJobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("jobs.SweepStaleJobs: rows affected: %w", err)
	}
	return n, nil
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
	if err != nil {
		return err
	}
	// Site 25: emit jobs.update for the status transition.
	// Checkpoint policy ("checkpoint on every status transition") is the
	// responsibility of a higher-level driver; we emit only the patch entry here.
	// See docs/tracing for how these entries feed checkpoints and replay.
	if js.journalWriter != nil {
		// Fetch the job's session_id so the journal entry can be attributed.
		sid := jobSessionID(ctx, js.db, id)
		ops := []map[string]any{
			{"op": "replace", "path": "/status", "value": string(status)},
		}
		if errMsg != "" {
			ops = append(ops, map[string]any{"op": "replace", "path": "/error", "value": errMsg})
		}
		if resultJSON != nil {
			var resultVal any
			if uerr := json.Unmarshal(resultJSON, &resultVal); uerr != nil {
				slog.Warn("jobs.UpdateJobStatus: unmarshal result for journal entry failed", "id", id, "err", uerr)
			}
			ops = append(ops, map[string]any{"op": "replace", "path": "/result", "value": resultVal})
		}
		body := mustJobJSON(map[string]any{"ops": ops})
		_ = js.journalWriter.Append(journal.Entry{
			Ts:      time.Now(),
			Session: sid,
			Kind:    journal.KindJobsUpdate,
			Doc:     journal.DocID("jobs/" + id),
			Body:    body,
		})
	}
	return nil
}

// GetJob returns the job with the given ID, or ErrJobNotFound if it does not exist.
func (js *JobStore) GetJob(ctx context.Context, id JobID) (*Job, error) {
	rows, err := js.db.QueryContext(ctx, `
		SELECT id, kind, status, origin_state, origin_proposal_id,
		       payload, error, clarification_schema, retry_count, created_at, updated_at, started_at, finished_at
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
		       payload, error, clarification_schema, retry_count, created_at, updated_at, started_at, finished_at
		FROM jobs WHERE session_id=? AND status=? ORDER BY created_at DESC`,
		string(sessionID), string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListBySession returns every job row for a session ordered by created_at ASC.
// Used by the flow testrunner's expect_jobs assertion to diff pre-turn vs
// post-turn snapshots and detect which jobs newly reached a terminal status.
// Order is creation-time so callers can index by "the N-th job to be dispatched
// this turn" reliably.
func (js *JobStore) ListBySession(ctx context.Context, sessionID app.SessionID) ([]Job, error) {
	rows, err := js.db.QueryContext(ctx, `
		SELECT id, kind, status, origin_state, origin_proposal_id,
		       payload, error, clarification_schema, retry_count, created_at, updated_at, started_at, finished_at
		FROM jobs WHERE session_id=? ORDER BY created_at ASC`,
		string(sessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListByStatus returns jobs across every session that match one of statuses.
// It is intended for operator-level work queues that need to surface active
// background work independent of the currently focused session.
func (js *JobStore) ListByStatus(ctx context.Context, statuses []JobStatus) ([]Job, error) {
	if len(statuses) == 0 {
		return nil, fmt.Errorf("jobs.ListByStatus: at least one status is required")
	}
	q := `SELECT id, session_id, kind, status, origin_state, origin_proposal_id,
	             payload, error, clarification_schema, retry_count, created_at, updated_at, started_at, finished_at
	      FROM jobs
	      WHERE status IN (` + placeholders(len(statuses)) + `)
	      ORDER BY updated_at DESC, created_at DESC`
	args := make([]any, 0, len(statuses))
	for _, st := range statuses {
		args = append(args, string(st))
	}
	rows, err := js.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobsWithSession(rows)
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	return scanJobRows(rows, false)
}

func scanJobsWithSession(rows *sql.Rows) ([]Job, error) {
	return scanJobRows(rows, true)
}

func scanJobRows(rows *sql.Rows, includeSession bool) ([]Job, error) {
	var out []Job
	for rows.Next() {
		var (
			j                Job
			status           string
			originProposalID sql.NullString
			payloadJSON      string
			errStr           sql.NullString
			clarificationStr sql.NullString
			createdAtMs      int64
			updatedAtMs      int64
			startedAtMs      sql.NullInt64
			finishedAtMs     sql.NullInt64
		)
		var err error
		if includeSession {
			err = rows.Scan(
				&j.ID, (*string)(&j.SessionID), &j.Kind, &status, (*string)(&j.OriginState), &originProposalID,
				&payloadJSON, &errStr, &clarificationStr, &j.RetryCount,
				&createdAtMs, &updatedAtMs, &startedAtMs, &finishedAtMs,
			)
		} else {
			err = rows.Scan(
				&j.ID, &j.Kind, &status, (*string)(&j.OriginState), &originProposalID,
				&payloadJSON, &errStr, &clarificationStr, &j.RetryCount,
				&createdAtMs, &updatedAtMs, &startedAtMs, &finishedAtMs,
			)
		}
		if err != nil {
			return nil, err
		}
		j.Status = JobStatus(status)
		j.OriginProposalID = originProposalID.String
		if uerr := json.Unmarshal([]byte(payloadJSON), &j.Payload); uerr != nil {
			slog.Warn("jobs.scanJobs: unmarshal payload failed; leaving payload empty", "id", j.ID, "err", uerr)
		}
		if clarificationStr.Valid && clarificationStr.String != "" {
			var schema ClarificationSchema
			if uerr := json.Unmarshal([]byte(clarificationStr.String), &schema); uerr != nil {
				slog.Warn("jobs.scanJobs: unmarshal clarification schema failed; leaving empty", "id", j.ID, "err", uerr)
			} else {
				j.ClarificationSchema = schema
			}
		}
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
// Site 26 (continue-mode): no journal entry is emitted here. Notifications are
// part of the world via the "$inbox" world var; the orchestrator's EffectApplied
// handler writes the world.patch entry (via the Y agent / oncomplete.go path)
// that captures the refreshed $inbox snapshot. A standalone inbox.item.created
// entry from this site would be redundant and risk ordering issues with the
// world.patch that follows. The notifications table persists independently and
// is the canonical source of truth for the notification row itself.
func (js *JobStore) InsertNotification(ctx context.Context, n *Notification) error {
	return insertNotification(ctx, js.db, n)
}

// InsertExternalNotificationOnce inserts an external notification unless the
// same session already has a row with the same origin_kind + origin_ref. It is
// intended for polling integrations (GitHub issues, PR review requests, etc.)
// where every refresh sees the same external object again.
//
// The returned bool is true only when a new row was inserted. On a duplicate,
// n.ID is populated with the existing row id so callers can still correlate the
// poll result with stored inbox state.
func (js *JobStore) InsertExternalNotificationOnce(ctx context.Context, n *Notification) (bool, error) {
	if n.OriginKind == "" {
		n.OriginKind = "external"
	}
	if n.OriginKind != "external" {
		return false, fmt.Errorf("jobs.InsertExternalNotificationOnce: origin_kind must be external, got %q", n.OriginKind)
	}
	if n.OriginRef == "" {
		return false, fmt.Errorf("jobs.InsertExternalNotificationOnce: origin_ref is required")
	}

	tx, err := js.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existingID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM notifications
		WHERE session_id=? AND origin_kind=? AND origin_ref=?
		ORDER BY created_at DESC
		LIMIT 1`,
		string(n.SessionID), n.OriginKind, n.OriginRef,
	).Scan(&existingID)
	if err == nil {
		n.ID = existingID
		return false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err := insertNotification(ctx, tx, n); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func insertNotification(ctx context.Context, exec notificationExecer, n *Notification) error {
	if n.ID == "" {
		n.ID = ulid.New()
	}
	// Stamp CreatedAt when the caller left it zero. The row is ordered by
	// created_at DESC everywhere (ListNotifications, the SSE relay's newest-row
	// read), so an unset CreatedAt — UnixMilli() of the zero time is a large
	// negative — would sort the row as the OLDEST and hide it behind earlier
	// notifications. PostJobNotification (the background-completion path) does
	// not set it, so default it here.
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	var teleportSlotsJSON []byte
	if n.TeleportSlots != nil {
		b, err := json.Marshal(n.TeleportSlots)
		if err != nil {
			return err
		}
		teleportSlotsJSON = b
	}

	_, err := exec.ExecContext(ctx, `
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

// DismissNotification sets dismissed_at on a notification, dropping it from
// ListNotifications and the unread counts. Mirrors MarkNotificationRead — a
// dismiss is a terminal "I'm done with this" action distinct from "read".
func (js *JobStore) DismissNotification(ctx context.Context, id string) error {
	_, err := js.db.ExecContext(ctx,
		`UPDATE notifications SET dismissed_at=? WHERE id=?`,
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

// ListNotifications returns non-dismissed notifications for a session.
func (js *JobStore) ListNotifications(ctx context.Context, sessionID app.SessionID, limit int) ([]Notification, error) {
	q := `
		SELECT id, session_id, created_at, read_at, severity, title, body,
		       teleport_state, teleport_slots, teleport_proposal_id, teleport_job_id,
		       origin_kind, origin_ref, origin_url
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
	return scanNotificationRows(rows)
}

// ListNotificationsAll returns non-dismissed notifications for every session.
// It is the all-session counterpart to ListNotifications for operator work
// queues. Use limit <= 0 for no limit.
func (js *JobStore) ListNotificationsAll(ctx context.Context, limit int) ([]Notification, error) {
	q := `
		SELECT id, session_id, created_at, read_at, severity, title, body,
		       teleport_state, teleport_slots, teleport_proposal_id, teleport_job_id,
		       origin_kind, origin_ref, origin_url
		FROM notifications
		WHERE dismissed_at IS NULL
		ORDER BY created_at DESC`
	args := []any{}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := js.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotificationRows(rows)
}

func scanNotificationRows(rows *sql.Rows) ([]Notification, error) {
	var out []Notification
	for rows.Next() {
		var n Notification
		var createdAtMs int64
		var readAtMs sql.NullInt64
		var teleportSlotsJSON sql.NullString
		var teleportProposalID, teleportJobID sql.NullString
		var originURL sql.NullString
		if err := rows.Scan(
			&n.ID, (*string)(&n.SessionID), &createdAtMs, &readAtMs,
			(*string)(&n.Severity), &n.Title, &n.Body,
			&n.TeleportState, &teleportSlotsJSON,
			&teleportProposalID, &teleportJobID,
			&n.OriginKind, &n.OriginRef, &originURL,
		); err != nil {
			return nil, err
		}
		n.CreatedAt = time.UnixMilli(createdAtMs)
		if readAtMs.Valid {
			t := time.UnixMilli(readAtMs.Int64)
			n.ReadAt = &t
		}
		n.TeleportProposalID = teleportProposalID.String
		n.TeleportJobID = teleportJobID.String
		if teleportSlotsJSON.Valid {
			if uerr := json.Unmarshal([]byte(teleportSlotsJSON.String), &n.TeleportSlots); uerr != nil {
				slog.Warn("jobs.ListNotifications: unmarshal teleport_slots failed; leaving empty", "id", n.ID, "err", uerr)
			}
		}
		n.OriginURL = originURL.String
		out = append(out, n)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, 2*n-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

// GetNotification loads a single notification row by id, regardless of its
// read/dismissed state (teleport resolution needs the teleport fields even for
// an already-read item). Returns (nil, nil) when no row matches.
func (js *JobStore) GetNotification(ctx context.Context, id string) (*Notification, error) {
	row := js.db.QueryRowContext(ctx, `
		SELECT id, session_id, created_at, read_at, severity, title, body,
		       teleport_state, teleport_slots, teleport_proposal_id, teleport_job_id,
		       origin_kind, origin_ref, origin_url
		FROM notifications
		WHERE id=?`, id)

	var n Notification
	var createdAtMs int64
	var readAtMs sql.NullInt64
	var teleportSlotsJSON sql.NullString
	var teleportProposalID, teleportJobID sql.NullString
	var originURL sql.NullString
	err := row.Scan(
		&n.ID, (*string)(&n.SessionID), &createdAtMs, &readAtMs,
		(*string)(&n.Severity), &n.Title, &n.Body,
		&n.TeleportState, &teleportSlotsJSON,
		&teleportProposalID, &teleportJobID,
		&n.OriginKind, &n.OriginRef, &originURL,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.CreatedAt = time.UnixMilli(createdAtMs)
	if readAtMs.Valid {
		t := time.UnixMilli(readAtMs.Int64)
		n.ReadAt = &t
	}
	n.TeleportProposalID = teleportProposalID.String
	n.TeleportJobID = teleportJobID.String
	n.OriginURL = originURL.String
	if teleportSlotsJSON.Valid {
		if uerr := json.Unmarshal([]byte(teleportSlotsJSON.String), &n.TeleportSlots); uerr != nil {
			slog.Warn("jobs.GetNotification: unmarshal teleport_slots failed; leaving empty", "id", n.ID, "err", uerr)
		}
	}
	return &n, nil
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// jobSessionID fetches the kitsoki session_id for a job row.
// Returns an empty SessionID if the row is not found (best-effort).
func jobSessionID(ctx context.Context, db *sql.DB, id JobID) app.SessionID {
	var sid string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(session_id,'') FROM jobs WHERE id = ?`, id).Scan(&sid)
	return app.SessionID(sid)
}

// mustJobJSON marshals v to JSON, returning an empty object on error.
// Only used for journal entry bodies where marshalling failure is
// vanishingly unlikely.
func mustJobJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

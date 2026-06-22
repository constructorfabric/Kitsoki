package chats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/ulid"
)

// journalPayloadSnippetMax caps the number of payload characters copied
// into a chat.drive.submitted journal entry. The full payload can be an
// entire turn's prompt; truncating keeps the journal compact while still
// making the entry identifiable. Truncated snippets get a trailing "…".
const journalPayloadSnippetMax = 256

// ErrNoPendingDrive is returned by Dequeue when no pending row exists
// for the requested chat. Callers loop until ErrNoPendingDrive, then
// stop dispatching.
var ErrNoPendingDrive = errors.New("chats: no pending drive")

// ErrDriveNotFound is returned by GetDrive / MarkDrive* when the
// drive_id doesn't exist.
var ErrDriveNotFound = errors.New("chats: drive not found")

// ErrDriveStateMismatch is returned by MarkDrive* when a row exists
// but its status is not the one the caller expected (e.g. attempting
// to mark a dismissed drive as done).
var ErrDriveStateMismatch = errors.New("chats: drive state mismatch")

// DriveStatus is the lifecycle status of a chat_input_queue row.
type DriveStatus string

const (
	// DriveStatusPending — newly enqueued; awaiting Dequeue.
	DriveStatusPending DriveStatus = "pending"
	// DriveStatusDispatching — claimed by a dispatcher; a turn is in
	// flight. A completed turn moves to Done or Failed; a crashed
	// dispatcher leaves the row stuck in Dispatching until GC or a
	// manual re-dispatch.
	DriveStatusDispatching DriveStatus = "dispatching"
	// DriveStatusDone — turn ran to completion; result_seq points at
	// the resulting chat_messages.seq.
	DriveStatusDone DriveStatus = "done"
	// DriveStatusFailed — turn errored after retries; error_message
	// carries the failure text.
	DriveStatusFailed DriveStatus = "failed"
	// DriveStatusDismissed — operator-suppressed; the row never
	// dispatched. Stays visible in ListDrives for audit.
	DriveStatusDismissed DriveStatus = "dismissed"
)

// DriveTransport identifies which surface originated a drive. The set
// is open — additional transports register their own short ids — but
// the well-known values are listed here for callers to reference.
type DriveTransport string

const (
	// DriveTransportTUI is a drive submitted from the interactive TUI.
	DriveTransportTUI DriveTransport = "tui"
	// DriveTransportJira is a drive originated by the Jira transport.
	DriveTransportJira DriveTransport = "jira"
	// DriveTransportBitbucket is a drive originated by the Bitbucket transport.
	DriveTransportBitbucket DriveTransport = "bitbucket"
	// DriveTransportMCP is a drive submitted through the MCP surface.
	DriveTransportMCP DriveTransport = "mcp"
	// DriveTransportJob is a drive enqueued by a background job.
	DriveTransportJob DriveTransport = "job"
	// DriveTransportStateMachine is a drive enqueued by a host.chat.drive
	// effect inside a running state machine.
	DriveTransportStateMachine DriveTransport = "state_machine"
)

// Drive is one row of chat_input_queue.
type Drive struct {
	DriveID       string
	ChatID        string
	Transport     DriveTransport
	Thread        string
	Actor         string
	CorrelationID string
	Payload       string
	Status        DriveStatus
	ReceivedAt    time.Time
	DispatchedAt  *time.Time
	CompletedAt   *time.Time
	// ResultSeq is the chat_messages.seq of the resulting assistant
	// message. Non-nil only after a successful dispatch.
	ResultSeq    *int
	ErrorMessage string
	// OnCompleteJSON is the serialized []app.Effect chain that should
	// fire when the drive transitions terminal (done or failed). Empty
	// when the drive carries no chain. The chats package treats it as
	// opaque; the orchestrator deserializes and runs it. See the drive
	// transports in docs/architecture/transports.md.
	OnCompleteJSON string
	// OriginSessionID identifies which kitsoki session originated the
	// drive. Used by the on_complete consumer to route the
	// on_complete chain back to the right session listener. Empty when
	// the drive was submitted outside an orchestrated session (CLI add,
	// indirect transport, etc.).
	OriginSessionID string
	// OriginState is the state path that owned the host.chat.drive
	// effect when the drive was enqueued. Passed back as the state
	// argument to machine.RunEffects when on_complete fires.
	OriginState string
}

// EnqueueOptions carries the inputs for Enqueue.
type EnqueueOptions struct {
	ChatID          string
	Transport       DriveTransport
	Thread          string
	Actor           string
	CorrelationID   string
	Payload         string
	OnCompleteJSON  string
	OriginSessionID string
	OriginState     string
}

// Enqueue inserts a fresh drive row in DriveStatusPending and returns
// it. Allocates a new ULID for drive_id. Enqueue is unsynchronized
// from the caller's perspective: multiple producers may insert
// concurrently against the same chat; FIFO order is by received_at.
func (s *Store) Enqueue(ctx context.Context, opts EnqueueOptions) (*Drive, error) {
	if opts.ChatID == "" {
		return nil, fmt.Errorf("chats.Enqueue: empty chat ID")
	}
	if opts.Transport == "" {
		return nil, fmt.Errorf("chats.Enqueue: empty transport")
	}
	if opts.Payload == "" {
		return nil, fmt.Errorf("chats.Enqueue: empty payload")
	}
	driveID := ulid.New()
	now := s.clock.Now().UnixMicro()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_input_queue
		  (drive_id, chat_id, transport, thread, actor, correlation_id,
		   payload, status, received_at,
		   dispatched_at, completed_at, result_seq, error_message,
		   on_complete_json, origin_session_id, origin_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, '', ?, ?, ?)`,
		driveID, opts.ChatID, string(opts.Transport),
		opts.Thread, opts.Actor, opts.CorrelationID,
		opts.Payload, string(DriveStatusPending), now,
		opts.OnCompleteJSON, opts.OriginSessionID, opts.OriginState,
	)
	if err != nil {
		return nil, fmt.Errorf("chats.Enqueue: insert: %w", err)
	}
	// Journal: chat.drive.submitted. Post-commit; OK to be a separate write —
	// the queue row is already durable. payload_snippet is the first
	// journalPayloadSnippetMax chars (full payload would balloon the journal
	// for large turns).
	if s.journalWriter != nil {
		snippet := opts.Payload
		if len(snippet) > journalPayloadSnippetMax {
			snippet = snippet[:journalPayloadSnippetMax] + "…"
		}
		appendJournalEntry(s.journalWriter, journal.Entry{
			Ts:      s.clock.Now(),
			Session: app.SessionID(opts.OriginSessionID),
			Kind:    journal.KindChatDriveSubmitted,
			Body: mustJSON(map[string]any{
				"drive_id":        driveID,
				"chat_id":         opts.ChatID,
				"transport":       string(opts.Transport),
				"actor":           opts.Actor,
				"payload_snippet": snippet,
			}),
		})
	}
	return s.GetDrive(ctx, driveID)
}

// Dequeue claims the oldest pending drive for the chat, transitions
// it pending → dispatching atomically, and returns the row. Returns
// ErrNoPendingDrive when no pending row exists. The CAS UPDATE uses
// SQLite's serialized writer to win at most one claim under
// concurrent dequeue attempts.
func (s *Store) Dequeue(ctx context.Context, chatID string) (*Drive, error) {
	if chatID == "" {
		return nil, fmt.Errorf("chats.Dequeue: empty chat ID")
	}
	now := s.clock.Now().UnixMicro()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("chats.Dequeue: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Pick the oldest pending row inside the txn so the subsequent
	// UPDATE can name it by primary key — SQLite serialises writes
	// across transactions, so the row we read is the row we own.
	var driveID string
	err = tx.QueryRowContext(ctx, `
		SELECT drive_id FROM chat_input_queue
		WHERE chat_id = ? AND status = ?
		ORDER BY received_at ASC, drive_id ASC
		LIMIT 1`, chatID, string(DriveStatusPending),
	).Scan(&driveID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingDrive
	}
	if err != nil {
		return nil, fmt.Errorf("chats.Dequeue: pick: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE chat_input_queue
		SET status = ?, dispatched_at = ?
		WHERE drive_id = ? AND status = ?`,
		string(DriveStatusDispatching), now,
		driveID, string(DriveStatusPending),
	)
	if err != nil {
		return nil, fmt.Errorf("chats.Dequeue: claim: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		// Lost the race; another dispatcher claimed it between SELECT
		// and UPDATE. Surface as "nothing to do" — the caller will
		// loop or stop.
		return nil, ErrNoPendingDrive
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chats.Dequeue: commit: %w", err)
	}
	return s.GetDrive(ctx, driveID)
}

// ClaimDrive transitions a specific drive pending → dispatching by
// drive_id rather than by oldest-pending position. Used by
// `kitsoki chat queue dispatch <drive-id>` (operator promotion) and
// by host.chat.drive's await:true path after a fresh Enqueue.
//
// Returns ErrDriveNotFound when no row exists, or ErrDriveStateMismatch
// when the row exists but is not pending.
func (s *Store) ClaimDrive(ctx context.Context, driveID string) (*Drive, error) {
	if driveID == "" {
		return nil, fmt.Errorf("chats.ClaimDrive: empty drive ID")
	}
	now := s.clock.Now().UnixMicro()
	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_input_queue
		SET status = ?, dispatched_at = ?
		WHERE drive_id = ? AND status = ?`,
		string(DriveStatusDispatching), now,
		driveID, string(DriveStatusPending),
	)
	if err != nil {
		return nil, fmt.Errorf("chats.ClaimDrive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return s.GetDrive(ctx, driveID)
	}
	// Disambiguate: did the row not exist, or was it in the wrong state?
	var got string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM chat_input_queue WHERE drive_id = ?`, driveID,
	).Scan(&got); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDriveNotFound
	} else if err != nil {
		return nil, fmt.Errorf("chats.ClaimDrive: read status: %w", err)
	}
	return nil, fmt.Errorf("%w: drive %s is %s, expected %s",
		ErrDriveStateMismatch, driveID, got, DriveStatusPending)
}

// MarkDriveDone transitions a drive dispatching → done, setting
// completed_at and result_seq. Returns ErrDriveNotFound /
// ErrDriveStateMismatch when the row is missing or not in
// dispatching.
func (s *Store) MarkDriveDone(ctx context.Context, driveID string, resultSeq int) error {
	return s.markDriveTerminal(ctx, driveID, DriveStatusDispatching, DriveStatusDone, &resultSeq, "")
}

// MarkDriveFailed transitions a drive dispatching → failed, recording
// the supplied error message.
func (s *Store) MarkDriveFailed(ctx context.Context, driveID, errorMessage string) error {
	return s.markDriveTerminal(ctx, driveID, DriveStatusDispatching, DriveStatusFailed, nil, errorMessage)
}

// MarkDriveDismissed transitions a drive pending → dismissed. Only
// pending drives are dismissable — a drive already in flight or
// terminal stays where it is and the call returns
// ErrDriveStateMismatch.
func (s *Store) MarkDriveDismissed(ctx context.Context, driveID string) error {
	return s.markDriveTerminal(ctx, driveID, DriveStatusPending, DriveStatusDismissed, nil, "")
}

func (s *Store) markDriveTerminal(
	ctx context.Context,
	driveID string,
	fromStatus, toStatus DriveStatus,
	resultSeq *int,
	errorMessage string,
) error {
	if driveID == "" {
		return fmt.Errorf("chats.markDriveTerminal: empty drive ID")
	}
	now := s.clock.Now().UnixMicro()

	var seqArg any
	if resultSeq != nil {
		seqArg = *resultSeq
	} else {
		seqArg = nil
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_input_queue
		SET status = ?, completed_at = ?, result_seq = ?, error_message = ?
		WHERE drive_id = ? AND status = ?`,
		string(toStatus), now, seqArg, errorMessage,
		driveID, string(fromStatus),
	)
	if err != nil {
		return fmt.Errorf("chats.markDriveTerminal: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.emitDriveTerminalJournal(ctx, driveID, toStatus, resultSeq, errorMessage)
		return nil
	}

	// Distinguish "no row" from "wrong state" so callers can log
	// usefully.
	var gotStatus string
	err = s.db.QueryRowContext(ctx,
		`SELECT status FROM chat_input_queue WHERE drive_id = ?`, driveID,
	).Scan(&gotStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDriveNotFound
	}
	if err != nil {
		return fmt.Errorf("chats.markDriveTerminal: read status: %w", err)
	}
	return fmt.Errorf("%w: drive %s is %s, expected %s",
		ErrDriveStateMismatch, driveID, gotStatus, fromStatus)
}

// emitDriveTerminalJournal writes a chat.drive.{completed,failed,dismissed}
// journal entry after a successful markDriveTerminal UPDATE. Post-commit;
// silently no-ops when no journal writer is wired. We fetch the row to learn
// origin_session_id and chat_id without changing the markDriveTerminal
// signature.
func (s *Store) emitDriveTerminalJournal(ctx context.Context, driveID string, toStatus DriveStatus, resultSeq *int, errorMessage string) {
	if s.journalWriter == nil {
		return
	}
	d, err := s.GetDrive(ctx, driveID)
	if err != nil || d == nil {
		return
	}
	var kind string
	body := map[string]any{
		"drive_id": driveID,
		"chat_id":  d.ChatID,
	}
	switch toStatus {
	case DriveStatusDone:
		kind = journal.KindChatDriveCompleted
		if resultSeq != nil {
			body["result_seq"] = *resultSeq
		}
	case DriveStatusFailed:
		kind = journal.KindChatDriveFailed
		body["error_message"] = errorMessage
	case DriveStatusDismissed:
		kind = journal.KindChatDriveDismissed
	default:
		return
	}
	appendJournalEntry(s.journalWriter, journal.Entry{
		Ts:      s.clock.Now(),
		Session: app.SessionID(d.OriginSessionID),
		Kind:    kind,
		Body:    mustJSON(body),
	})
}

// GetDrive returns a drive by id, or ErrDriveNotFound.
func (s *Store) GetDrive(ctx context.Context, driveID string) (*Drive, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT drive_id, chat_id, transport, thread, actor, correlation_id,
		       payload, status, received_at,
		       dispatched_at, completed_at, result_seq, error_message,
		       on_complete_json, origin_session_id, origin_state
		FROM chat_input_queue WHERE drive_id = ?`, driveID)
	d, err := scanDrive(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDriveNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chats.GetDrive: %w", err)
	}
	return d, nil
}

// ListDrivesFilter narrows ListDrives. An empty Statuses slice means
// "all statuses." Pass []DriveStatus{DriveStatusPending} for the
// per-chat queue-tail view; pass []DriveStatus{DriveStatusFailed} for
// the re-dispatch popup.
type ListDrivesFilter struct {
	Statuses []DriveStatus
	// Limit caps the result count. <= 0 means no cap.
	Limit int
}

// ListDrives returns drives for a chat, ordered by received_at ASC
// (FIFO), optionally filtered by status.
func (s *Store) ListDrives(ctx context.Context, chatID string, filter ListDrivesFilter) ([]Drive, error) {
	if chatID == "" {
		return nil, fmt.Errorf("chats.ListDrives: empty chat ID")
	}
	q := `SELECT drive_id, chat_id, transport, thread, actor, correlation_id,
	             payload, status, received_at,
	             dispatched_at, completed_at, result_seq, error_message,
	             on_complete_json, origin_session_id, origin_state
	      FROM chat_input_queue
	      WHERE chat_id = ?`
	args := []any{chatID}
	if len(filter.Statuses) > 0 {
		q += ` AND status IN (` + placeholders(len(filter.Statuses)) + `)`
		for _, st := range filter.Statuses {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY received_at ASC, drive_id ASC`
	if filter.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("chats.ListDrives: %w", err)
	}
	defer rows.Close()

	var out []Drive
	for rows.Next() {
		d, err := scanDriveRow(rows)
		if err != nil {
			return nil, fmt.Errorf("chats.ListDrives: scan: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListDrivesBySession returns every drive whose origin_session_id matches
// the given kitsoki session, optionally narrowed by status. Used by the
// continue-mode AttachSession path to surface pending/in-flight drives the
// resumed TUI should be aware of.
//
// Ordered by received_at ASC, drive_id ASC. An empty statuses slice returns
// drives in every status (audit view).
func (s *Store) ListDrivesBySession(ctx context.Context, sessionID string, statuses []DriveStatus) ([]Drive, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("chats.ListDrivesBySession: empty session ID")
	}
	q := `SELECT drive_id, chat_id, transport, thread, actor, correlation_id,
	             payload, status, received_at,
	             dispatched_at, completed_at, result_seq, error_message,
	             on_complete_json, origin_session_id, origin_state
	      FROM chat_input_queue
	      WHERE origin_session_id = ?`
	args := []any{sessionID}
	if len(statuses) > 0 {
		q += ` AND status IN (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY received_at ASC, drive_id ASC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("chats.ListDrivesBySession: %w", err)
	}
	defer rows.Close()

	var out []Drive
	for rows.Next() {
		d, err := scanDriveRow(rows)
		if err != nil {
			return nil, fmt.Errorf("chats.ListDrivesBySession: scan: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListDrivesByOrigin returns every drive that is tied to a kitsoki origin
// session, optionally narrowed by status. It is the all-session counterpart to
// ListDrivesBySession for operator work queues that need to show in-progress
// state-machine chat drives across the local DB.
//
// Ordered by origin_session_id ASC, received_at ASC, drive_id ASC. An empty
// statuses slice returns drives in every status (audit view).
func (s *Store) ListDrivesByOrigin(ctx context.Context, statuses []DriveStatus) ([]Drive, error) {
	q := `SELECT drive_id, chat_id, transport, thread, actor, correlation_id,
	             payload, status, received_at,
	             dispatched_at, completed_at, result_seq, error_message,
	             on_complete_json, origin_session_id, origin_state
	      FROM chat_input_queue
	      WHERE origin_session_id != ''`
	args := []any{}
	if len(statuses) > 0 {
		q += ` AND status IN (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY origin_session_id ASC, received_at ASC, drive_id ASC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("chats.ListDrivesByOrigin: %w", err)
	}
	defer rows.Close()

	var out []Drive
	for rows.Next() {
		d, err := scanDriveRow(rows)
		if err != nil {
			return nil, fmt.Errorf("chats.ListDrivesByOrigin: scan: %w", err)
		}
		out = append(out, *d)
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

func scanDrive(row scanner) (*Drive, error) {
	return scanDriveCommon(row.Scan)
}

func scanDriveRow(rows *sql.Rows) (*Drive, error) {
	return scanDriveCommon(rows.Scan)
}

func scanDriveCommon(scan func(...any) error) (*Drive, error) {
	var (
		d                         Drive
		transportStr, statusStr   string
		receivedAt                int64
		dispatchedAt, completedAt sql.NullInt64
		resultSeq                 sql.NullInt64
	)
	if err := scan(
		&d.DriveID, &d.ChatID, &transportStr, &d.Thread, &d.Actor, &d.CorrelationID,
		&d.Payload, &statusStr, &receivedAt,
		&dispatchedAt, &completedAt, &resultSeq, &d.ErrorMessage,
		&d.OnCompleteJSON, &d.OriginSessionID, &d.OriginState,
	); err != nil {
		return nil, err
	}
	d.Transport = DriveTransport(transportStr)
	d.Status = DriveStatus(statusStr)
	d.ReceivedAt = time.UnixMicro(receivedAt)
	if dispatchedAt.Valid {
		t := time.UnixMicro(dispatchedAt.Int64)
		d.DispatchedAt = &t
	}
	if completedAt.Valid {
		t := time.UnixMicro(completedAt.Int64)
		d.CompletedAt = &t
	}
	if resultSeq.Valid {
		v := int(resultSeq.Int64)
		d.ResultSeq = &v
	}
	return &d, nil
}

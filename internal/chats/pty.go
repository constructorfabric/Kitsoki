package chats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrNoPTYSession is returned by GetPTY / DetachPTY / MarkPTYIdle when no
// chat_pty_sessions row exists for the chat. Use errors.Is to check.
var ErrNoPTYSession = errors.New("chats: no pty session for chat")

// ErrPTYCrossHost is returned by AttachPTY when a row for the chat already
// exists with a different tmux_host than this process's hostname. tmux is
// per-host: re-attaching from another host is not possible without first
// removing the original row from the host that owns it.
var ErrPTYCrossHost = errors.New("chats: pty session exists on a different host")

// PtyMode is the lifecycle phase of a tmux-hosted claude. Only the two
// "owned" modes are persisted as rows; the idle phase is represented by
// row absence.
type PtyMode string

const (
	// PtyModeAttached — a kitsoki chat attach process is currently
	// forwarding bytes from the chat's tmux session. The chat lock is
	// held for the duration.
	PtyModeAttached PtyMode = "pty_attached"
	// PtyModeBackground — tmux + claude are alive but no kitsoki client
	// is forwarding bytes. The chat lock is released; the row reserves
	// the chat against headless drives.
	PtyModeBackground PtyMode = "pty_background"
)

// PtySession is one row of chat_pty_sessions.
type PtySession struct {
	ChatID         string
	TmuxSession    string
	TmuxHost       string
	Mode           PtyMode
	PermissionMode string
	WorkspacePath  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// LastIdleAt is non-nil only after idle-detection has fired at least
	// once for this row (see [Store.MarkPTYIdle]).
	LastIdleAt *time.Time
}

// AttachPTYOptions carries the inputs for AttachPTY. The tmux host is
// taken from os.Hostname() — callers don't set it.
type AttachPTYOptions struct {
	ChatID         string
	TmuxSession    string
	PermissionMode string
	WorkspacePath  string
}

// AttachPTY records that a tmux-hosted claude is attached for the chat.
// Insert-or-update: a fresh chat gets a new row in PtyModeAttached; an
// existing row in PtyModeBackground (re-attach from a background tmux)
// flips back to PtyModeAttached and refreshes the supplied fields.
//
// Returns ErrPTYCrossHost when an existing row's tmux_host does not
// match this process — tmux is per-host, so a cross-host attach is not
// a valid transition through storage alone.
func (s *Store) AttachPTY(ctx context.Context, opts AttachPTYOptions) (*PtySession, error) {
	if opts.ChatID == "" {
		return nil, fmt.Errorf("chats.AttachPTY: empty chat ID")
	}
	if opts.TmuxSession == "" {
		return nil, fmt.Errorf("chats.AttachPTY: empty tmux session")
	}
	host, _ := os.Hostname()
	now := s.clock.Now().UnixMicro()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("chats.AttachPTY: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Probe for an existing row to disambiguate cross-host vs. local
	// transition. SELECT first so the error case (ErrPTYCrossHost)
	// short-circuits before we touch any rows.
	var existingHost string
	err = tx.QueryRowContext(ctx,
		`SELECT tmux_host FROM chat_pty_sessions WHERE chat_id = ?`,
		opts.ChatID,
	).Scan(&existingHost)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Fresh row.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_pty_sessions
			  (chat_id, tmux_session, tmux_host, mode,
			   permission_mode, workspace_path,
			   created_at, updated_at, last_idle_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			opts.ChatID, opts.TmuxSession, host, string(PtyModeAttached),
			opts.PermissionMode, opts.WorkspacePath,
			now, now,
		); err != nil {
			return nil, fmt.Errorf("chats.AttachPTY: insert: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("chats.AttachPTY: read host: %w", err)
	default:
		// Row exists.
		if existingHost != host {
			return nil, ErrPTYCrossHost
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE chat_pty_sessions
			SET tmux_session = ?, mode = ?,
			    permission_mode = ?, workspace_path = ?,
			    updated_at = ?
			WHERE chat_id = ?`,
			opts.TmuxSession, string(PtyModeAttached),
			opts.PermissionMode, opts.WorkspacePath,
			now, opts.ChatID,
		); err != nil {
			return nil, fmt.Errorf("chats.AttachPTY: update: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chats.AttachPTY: commit: %w", err)
	}
	return s.GetPTY(ctx, opts.ChatID)
}

// DetachPTY flips an existing row to PtyModeBackground. Used when the
// human leaves the attached tmux session but tmux + claude stay alive
// (the default `prefix + d` tmux detach keybinding).
//
// Returns ErrNoPTYSession when no row exists for the chat. Returns
// ErrPTYCrossHost when the row's tmux_host is not this process —
// detach is meaningful only on the host that owns the tmux session.
func (s *Store) DetachPTY(ctx context.Context, chatID string) (*PtySession, error) {
	if chatID == "" {
		return nil, fmt.Errorf("chats.DetachPTY: empty chat ID")
	}
	host, _ := os.Hostname()
	now := s.clock.Now().UnixMicro()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("chats.DetachPTY: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingHost string
	err = tx.QueryRowContext(ctx,
		`SELECT tmux_host FROM chat_pty_sessions WHERE chat_id = ?`,
		chatID,
	).Scan(&existingHost)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPTYSession
	}
	if err != nil {
		return nil, fmt.Errorf("chats.DetachPTY: read host: %w", err)
	}
	if existingHost != host {
		return nil, ErrPTYCrossHost
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE chat_pty_sessions SET mode = ?, updated_at = ? WHERE chat_id = ?`,
		string(PtyModeBackground), now, chatID,
	); err != nil {
		return nil, fmt.Errorf("chats.DetachPTY: update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chats.DetachPTY: commit: %w", err)
	}
	return s.GetPTY(ctx, chatID)
}

// RemovePTY deletes the chat's chat_pty_sessions row, transitioning the
// chat back to idle. Used by `kitsoki chat detach --mode headless` and
// `--mode stop` after the tmux session has been killed, and by
// GCDeadTmux when a row's tmux session is no longer alive.
//
// Returns ErrNoPTYSession when no row exists for the chat. Cross-host
// rows are not removable from this host (GCDeadTmux only walks
// matching hosts).
func (s *Store) RemovePTY(ctx context.Context, chatID string) error {
	if chatID == "" {
		return fmt.Errorf("chats.RemovePTY: empty chat ID")
	}
	host, _ := os.Hostname()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM chat_pty_sessions WHERE chat_id = ? AND tmux_host = ?`,
		chatID, host,
	)
	if err != nil {
		return fmt.Errorf("chats.RemovePTY: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "no row" from "cross-host row" so the caller can
		// tell GC paths apart from genuinely missing state.
		var existingHost string
		err := s.db.QueryRowContext(ctx,
			`SELECT tmux_host FROM chat_pty_sessions WHERE chat_id = ?`,
			chatID,
		).Scan(&existingHost)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoPTYSession
		}
		if err != nil {
			return fmt.Errorf("chats.RemovePTY: read host: %w", err)
		}
		return ErrPTYCrossHost
	}
	return nil
}

// GetPTY returns the chat's chat_pty_sessions row, or ErrNoPTYSession
// if no row exists. Cross-host rows are returned (so the caller can
// produce the "this chat is attached on another host" message).
func (s *Store) GetPTY(ctx context.Context, chatID string) (*PtySession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT chat_id, tmux_session, tmux_host, mode,
		       permission_mode, workspace_path,
		       created_at, updated_at, last_idle_at
		FROM chat_pty_sessions WHERE chat_id = ?`, chatID)
	p, err := scanPTYSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPTYSession
	}
	if err != nil {
		return nil, fmt.Errorf("chats.GetPTY: %w", err)
	}
	return p, nil
}

// ListPTYForHost returns every chat_pty_sessions row whose tmux_host
// matches this process. Cross-host rows are filtered out because tmux
// commands invoked from this host cannot reach them. Order is by
// updated_at DESC.
func (s *Store) ListPTYForHost(ctx context.Context) ([]PtySession, error) {
	host, _ := os.Hostname()
	rows, err := s.db.QueryContext(ctx, `
		SELECT chat_id, tmux_session, tmux_host, mode,
		       permission_mode, workspace_path,
		       created_at, updated_at, last_idle_at
		FROM chat_pty_sessions
		WHERE tmux_host = ?
		ORDER BY updated_at DESC`, host)
	if err != nil {
		return nil, fmt.Errorf("chats.ListPTYForHost: %w", err)
	}
	defer rows.Close()

	var out []PtySession
	for rows.Next() {
		p, err := scanPTYSessionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("chats.ListPTYForHost: scan: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// MarkPTYIdle bumps the chat's last_idle_at to now. Used by the
// idle-detection watcher when it observes a turn complete in a
// pty_background tmux session. Returns ErrNoPTYSession when no row
// exists.
func (s *Store) MarkPTYIdle(ctx context.Context, chatID string) error {
	if chatID == "" {
		return fmt.Errorf("chats.MarkPTYIdle: empty chat ID")
	}
	now := s.clock.Now().UnixMicro()
	res, err := s.db.ExecContext(ctx,
		`UPDATE chat_pty_sessions SET last_idle_at = ?, updated_at = ? WHERE chat_id = ?`,
		now, now, chatID,
	)
	if err != nil {
		return fmt.Errorf("chats.MarkPTYIdle: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNoPTYSession
	}
	return nil
}

// GCDeadTmux walks every chat_pty_sessions row on this host and calls
// the supplied probe with the row's tmux session name. When probe
// returns false the row is deleted — the tmux session has died (host
// reboot, tmux kill-server, systemd RemoveIPC, …) so the chat is
// effectively back to idle. Returns the number of rows removed.
//
// The probe is dependency-injected so the storage layer does not pull
// in os/exec("tmux has-session"). The caller in package tmux (phase C)
// supplies the real implementation; tests substitute a map-backed
// predicate.
func (s *Store) GCDeadTmux(ctx context.Context, probe func(tmuxSession string) bool) (int, error) {
	if probe == nil {
		return 0, fmt.Errorf("chats.GCDeadTmux: nil probe")
	}
	sessions, err := s.ListPTYForHost(ctx)
	if err != nil {
		return 0, fmt.Errorf("chats.GCDeadTmux: list: %w", err)
	}
	removed := 0
	for _, p := range sessions {
		if probe(p.TmuxSession) {
			continue
		}
		if err := s.RemovePTY(ctx, p.ChatID); err != nil {
			if errors.Is(err, ErrNoPTYSession) {
				// Raced with another remover; treat as already-removed.
				continue
			}
			return removed, fmt.Errorf("chats.GCDeadTmux: remove %s: %w", p.ChatID, err)
		}
		removed++
	}
	return removed, nil
}

func scanPTYSession(row scanner) (*PtySession, error) {
	return scanPTYSessionCommon(row.Scan)
}

func scanPTYSessionRow(rows *sql.Rows) (*PtySession, error) {
	return scanPTYSessionCommon(rows.Scan)
}

func scanPTYSessionCommon(scan func(...any) error) (*PtySession, error) {
	var (
		p                     PtySession
		modeStr               string
		createdAt, updatedAt  int64
		lastIdleAt            sql.NullInt64
	)
	if err := scan(
		&p.ChatID, &p.TmuxSession, &p.TmuxHost, &modeStr,
		&p.PermissionMode, &p.WorkspacePath,
		&createdAt, &updatedAt, &lastIdleAt,
	); err != nil {
		return nil, err
	}
	p.Mode = PtyMode(modeStr)
	p.CreatedAt = time.UnixMicro(createdAt)
	p.UpdatedAt = time.UnixMicro(updatedAt)
	if lastIdleAt.Valid {
		t := time.UnixMicro(lastIdleAt.Int64)
		p.LastIdleAt = &t
	}
	return &p, nil
}

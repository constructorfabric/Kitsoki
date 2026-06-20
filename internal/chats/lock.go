package chats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrChatBusy is a sentinel returned when another live process holds the lock.
// The actual error value returned by WithLock will be a *chatBusyError that
// wraps this sentinel; use errors.Is(err, ErrChatBusy) to detect it.
var ErrChatBusy = errors.New("chats: chat busy")

// chatBusyError carries richer context about who holds the lock while still
// satisfying errors.Is(err, ErrChatBusy).
type chatBusyError struct {
	pid int
	ts  time.Time
}

func (e *chatBusyError) Error() string {
	return fmt.Sprintf("chats: chat busy: held by pid %d since %s", e.pid, e.ts.Format(time.RFC3339))
}

func (e *chatBusyError) Is(target error) bool {
	return target == ErrChatBusy
}

// WithLock acquires the per-chat lock, runs fn(ctx), then releases the lock.
// Returns ErrChatBusy if another live process holds the lock.
//
// Stale-lock reaping: same-host locks owned by a dead PID are reaped
// immediately — no time-based threshold is required, because a dead PID on
// our host means the previous owner has definitely released the lock at
// the OS level. Cross-host locks are always treated as busy: we cannot
// probe liveness across hosts, and the chat-aware agent path does not
// emit heartbeats, so a 30-second timer would wedge other hosts waiting
// for activity that never comes.
//
// If ctx is already cancelled when WithLock is called, it returns ctx.Err()
// immediately without writing to chat_locks.
func (s *Store) WithLock(ctx context.Context, chatID string, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chatID == "" {
		return fmt.Errorf("chats.WithLock: empty chat ID")
	}
	if err := s.acquireChatLock(ctx, chatID); err != nil {
		return err
	}
	defer func() {
		_ = s.releaseChatLock(context.Background(), chatID)
	}()
	return fn(ctx)
}

// Heartbeat updates heartbeat_at for the lock owned by this process.
// Returns an error if this process does not own the lock (misuse guard).
func (s *Store) Heartbeat(ctx context.Context, chatID string) error {
	host, _ := os.Hostname()
	pid := os.Getpid()
	now := s.clock.Now().UnixMicro()

	res, err := s.db.ExecContext(ctx,
		`UPDATE chat_locks SET heartbeat_at = ?
		 WHERE chat_id = ? AND owner_pid = ? AND owner_host = ?`,
		now, chatID, pid, host,
	)
	if err != nil {
		return fmt.Errorf("chats.Heartbeat: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("chats.Heartbeat: this process does not own the lock for chat %s", chatID)
	}
	return nil
}

func (s *Store) acquireChatLock(ctx context.Context, chatID string) error {
	host, _ := os.Hostname()
	pid := os.Getpid()
	now := s.clock.Now().UnixMicro()

	for attempt := 0; attempt < 2; attempt++ {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO chat_locks (chat_id, owner_pid, owner_host, acquired_at, heartbeat_at)
			 VALUES (?, ?, ?, ?, ?)`,
			chatID, pid, host, now, now,
		)
		if err == nil {
			return nil
		}
		if !isUniqueViolation(err) {
			return fmt.Errorf("chats.acquireChatLock: insert: %w", err)
		}

		// Row exists — inspect ownership.
		var (
			ownerPID    int
			ownerHost   string
			heartbeatAt int64
		)
		if err := s.db.QueryRowContext(ctx,
			`SELECT owner_pid, owner_host, heartbeat_at FROM chat_locks WHERE chat_id = ?`,
			chatID,
		).Scan(&ownerPID, &ownerHost, &heartbeatAt); err != nil {
			return fmt.Errorf("chats.acquireChatLock: read owner: %w", err)
		}

		ownerTS := time.UnixMicro(heartbeatAt)

		// Same process re-entering: treat as busy (we don't support recursion).
		if ownerPID == pid && ownerHost == host {
			return &chatBusyError{pid: ownerPID, ts: ownerTS}
		}

		// Cross-host: cannot probe liveness; treat as busy.
		if ownerHost != host {
			return &chatBusyError{pid: ownerPID, ts: ownerTS}
		}

		// Same host, different PID: probe liveness. A dead PID on this host
		// means the prior owner has been torn down by the OS, so its lock
		// row is safe to reap regardless of how recently it heartbeated —
		// the chat-aware agent path doesn't heartbeat at all, and a crash
		// mid-call would otherwise wedge the chat indefinitely.
		if processAlive(ownerPID) {
			return &chatBusyError{pid: ownerPID, ts: ownerTS}
		}

		// Stale lock — reap and retry.
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM chat_locks WHERE chat_id = ? AND owner_pid = ? AND owner_host = ?`,
			chatID, ownerPID, ownerHost,
		); err != nil {
			return fmt.Errorf("chats.acquireChatLock: reap stale: %w", err)
		}
		// Loop and re-INSERT.
	}
	return ErrChatBusy
}

func (s *Store) releaseChatLock(ctx context.Context, chatID string) error {
	host, _ := os.Hostname()
	pid := os.Getpid()
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM chat_locks WHERE chat_id = ? AND owner_pid = ? AND owner_host = ?`,
		chatID, pid, host,
	)
	if err != nil {
		// Callers (defer in WithLock) discard the returned error, so a Warn
		// here is what makes a stuck DELETE visible in production logs.
		// TODO: test slog warning — capturing slog output cleanly is fiddly.
		slog.Warn("chats: releaseChatLock", "chat_id", chatID, "err", err)
		return fmt.Errorf("chats.releaseChatLock: %w", err)
	}
	return nil
}

// processAlive reports whether the given pid corresponds to a running process
// on this host. Uses signal 0 which is a no-op kill that just checks
// permission and existence.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true
	} else if errors.Is(err, os.ErrPermission) {
		// Permission denied means the process exists.
		return true
	}
	return false
}

// isUniqueViolation reports whether err is a SQLite uniqueness constraint
// violation. modernc.org/sqlite returns errors whose message contains
// "UNIQUE constraint failed".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}

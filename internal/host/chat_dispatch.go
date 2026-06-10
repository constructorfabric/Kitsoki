// Package host — the chat-input-queue dispatcher.
//
// The dispatcher is the seam between an already-enqueued drive (a row
// in chat_input_queue) and an actually-run claude turn. Two callers:
//
//  1. host.chat.drive with await: true — after Enqueue, the handler
//     immediately Dispatches the just-enqueued drive synchronously so
//     the result text and chat_messages.seq can be returned to the
//     state-machine effect.
//  2. kitsoki chat queue dispatch <drive-id> — operator-driven
//     promotion of a specific drive ahead of any other pending rows.
//
// Flow (with the chat lock held throughout the chat-state mutations):
//
//	WithLock(chat_id):
//	    ClaimDrive(drive_id)            pending → dispatching
//	    doOracleChatTurn(chat_id, …)    runs claude, appends messages
//	    MarkDriveDone(drive_id, seq)    dispatching → done
//	    (or MarkDriveFailed on error)
//
// The drive is claimed inside the lock so an aborted dispatch (e.g.
// chat busy somewhere) never strands a row in 'dispatching' without a
// matching process. If ClaimDrive errors (someone else promoted it),
// the lock unwinds without claude having been spawned.
package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// DispatchDriveRetryInterval is the poll cadence DispatchDriveWithTimeout
// uses while waiting for a busy chat lock to free. Matches the
// default heartbeat (5 s) — the lock holder refreshes its row at the
// same cadence, so polling more frequently is wasted DB load. Exposed
// (lower-cased copy on the package) for tests to drive faster.
var dispatchDriveRetryInterval = 1 * time.Second

// DispatchResult is the outcome of DispatchDrive — the dispatched
// drive's terminal state plus a copy of the chat-turn answer (when
// the turn ran to completion).
type DispatchResult struct {
	DriveID         string
	ChatID          string
	Status          string // "done" or "failed"
	Answer          string // assistant reply text on success
	ResultSeq       int    // chat_messages.seq of the new assistant message (when Status == "done")
	ClaudeSessionID string
	ErrorMessage    string // populated when Status == "failed"
}

// DispatchDriveWithTimeout is DispatchDrive plus an optional
// lock-contention retry budget. When the chat lock is held by another
// process, the dispatcher waits up to `timeout` (polling on
// dispatchDriveRetryInterval) before reporting ErrChatBusy. A
// non-positive timeout collapses to a single attempt — identical to
// DispatchDrive.
//
// Used by host.chat.drive's await:true path when the caller supplied
// timeout_seconds. Other callers (kitsoki chat queue dispatch) use
// DispatchDrive directly and report busy on the first miss.
func DispatchDriveWithTimeout(ctx context.Context, cs ChatStore, driveID, workingDir string, timeout time.Duration) (*DispatchResult, error) {
	if timeout <= 0 {
		return DispatchDrive(ctx, cs, driveID, workingDir)
	}
	clk := ClockFromContext(ctx)
	deadline := clk.Now().Add(timeout)
	for {
		out, err := DispatchDrive(ctx, cs, driveID, workingDir)
		if !errors.Is(err, ErrChatBusy) {
			return out, err
		}
		now := clk.Now()
		if !now.Before(deadline) {
			return nil, err
		}
		// Wait, but no longer than what remains of the budget. We block
		// on either the clock tick or ctx cancellation so a caller
		// abandoning the wait surfaces ctx.Err() promptly.
		remaining := deadline.Sub(now)
		wait := dispatchDriveRetryInterval
		if remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-clk.After(wait):
		}
	}
}

// DispatchDrive runs the named drive as a headless claude turn against
// its chat. The drive must currently be in DriveStatusPending; the
// dispatcher claims, runs, and marks terminal — all inside the chat
// lock.
//
// Returns ErrChatBusy when the chat lock is held by another process.
// Returns ErrDriveNotFound / ErrDriveStateMismatch when the drive is
// missing or not in the pending state.
//
// On a non-zero claude exit or persistence failure, the drive is
// transitioned to failed and DispatchResult.Status == "failed" is
// returned with ErrorMessage populated — the caller does NOT see a
// Go error in that case (matches the established host pattern).
//
// workingDir is the cwd passed to the claude subprocess. Empty string
// means the directory of the resolved prompt (whatever doOracleChatTurn
// chooses).
func DispatchDrive(ctx context.Context, cs ChatStore, driveID, workingDir string) (*DispatchResult, error) {
	if cs == nil {
		return nil, fmt.Errorf("host.DispatchDrive: no chat store wired")
	}
	if driveID == "" {
		return nil, fmt.Errorf("host.DispatchDrive: empty drive ID")
	}

	// Pre-lock read: we need chat_id to call WithLock. Reading the
	// drive outside the lock is safe because chat_input_queue rows are
	// only inserted by Enqueue (which we don't compete with) and
	// transitioned by ClaimDrive (which we do, but inside the lock).
	drive, err := cs.GetDrive(ctx, driveID)
	if err != nil {
		return nil, err
	}

	var out DispatchResult
	out.DriveID = driveID
	out.ChatID = drive.ChatID

	lockErr := cs.WithLock(ctx, drive.ChatID, func(lockedCtx context.Context) error {
		// Inside the lock: claim the drive. On a state mismatch (someone
		// raced us via ClaimDrive / Dequeue), bail without running.
		if _, claimErr := cs.ClaimDrive(lockedCtx, driveID); claimErr != nil {
			return claimErr
		}

		// Run the turn through the same code path host.oracle.converse uses
		// for chat-aware turns. doOracleChatTurn assumes it is being
		// called inside WithLock — which we are.
		//
		// systemPrompt/model are left empty: a drive's payload is
		// already the fully-rendered user message (with whatever
		// preamble the enqueuer chose to attach), and there is no
		// per-drive agent override yet. When future revisions plumb
		// agent metadata onto the drive row, thread it through here.
		turn, runErr := doConverseChatTurn(lockedCtx, cs, drive.ChatID, drive.Payload, workingDir, "", "", "", "bypassPermissions", nil, nil, false)
		if runErr != nil {
			// Infra failure: mark the drive failed with the underlying
			// error so the row carries forensics, then re-surface to the
			// caller as a Go error (the dispatcher itself is broken,
			// not the drive). If the failed-mark itself errors we cannot
			// recover the row here, but we must not silently swallow it:
			// a stranded 'dispatching' row blocks the chat indefinitely,
			// so log it at error level with enough context to find it.
			if markErr := cs.MarkDriveFailed(lockedCtx, driveID, runErr.Error()); markErr != nil {
				slog.ErrorContext(lockedCtx, "host.DispatchDrive: mark failed after infra error; drive may be stranded in 'dispatching'",
					slog.String("drive_id", driveID),
					slog.String("chat_id", drive.ChatID),
					slog.String("run_err", runErr.Error()),
					slog.String("mark_err", markErr.Error()))
			}
			return runErr
		}

		// Capture identifying fields from Result.Data regardless of
		// success/failure so the caller's DispatchResult always has
		// chat / claude_session context.
		if turn.Data != nil {
			if v, ok := turn.Data["claude_session_id"].(string); ok {
				out.ClaudeSessionID = v
			}
		}

		if turn.Error != "" {
			// Domain-level failure (claude exited non-zero, persistence
			// error, etc.). Mark the drive failed with the same message.
			if err := cs.MarkDriveFailed(lockedCtx, driveID, turn.Error); err != nil {
				return fmt.Errorf("host.DispatchDrive: mark failed: %w", err)
			}
			out.Status = "failed"
			out.ErrorMessage = turn.Error
			return nil
		}

		// Success path. The Result.Data carries answer + transcript_seq.
		if turn.Data == nil {
			return fmt.Errorf("host.DispatchDrive: doOracleChatTurn succeeded but returned nil Data")
		}
		answer, _ := turn.Data["answer"].(string)
		seq, _ := turn.Data["transcript_seq"].(int)
		if err := cs.MarkDriveDone(lockedCtx, driveID, seq); err != nil {
			return fmt.Errorf("host.DispatchDrive: mark done: %w", err)
		}
		out.Status = "done"
		out.Answer = answer
		out.ResultSeq = seq
		return nil
	})

	if errors.Is(lockErr, ErrChatBusy) {
		return nil, lockErr
	}
	if lockErr != nil {
		return nil, lockErr
	}
	return &out, nil
}

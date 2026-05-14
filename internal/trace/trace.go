// Package trace provides structured JSONL tracing for kitsoki sessions (§11).
//
// Usage pattern: every component (orchestrator, harness, machine) receives a
// *slog.Logger at construction time. When --trace is active the caller installs
// a slog handler that writes to a JSONL file and/or a human-readable sink.
// When no --trace flag is given the default logger is slog.Default() at ERROR
// level, making all DebugContext calls effectively free.
//
// Event name taxonomy — dotted strings used as the slog msg field:
//
//	turn.start, turn.routed, turn.stepped, turn.persisted, turn.done
//	harness.request, harness.response.raw, harness.response.parsed
//	harness.retry, harness.error, harness.exec, harness.oracle_hit, harness.oracle_miss
//	machine.guard.eval, machine.guard.winner, machine.effect.applied
//	machine.transition, machine.validation.rejected
//	expr.compile_error, expr.eval_error
//	store.events.appended
//	offpath.enter, offpath.exit, offpath.ask.start, offpath.ask.done,
//	offpath.ask.error, offpath.chat.resolved
//	timeout.armed, timeout.cancelled, timeout.fired, timeout.error, timeout.rearmed
//	teleport.start, teleport.done
//	job.submitted, job.terminal, job.awaiting_input,
//	job.clarification_answered, job.on_complete.run, job.error
//	slotfill.requested, slotfill.continued
//	disambig.presented, disambig.chosen
//	inbox.notification.posted, inbox.item.opened, inbox.item.dismissed
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
)

// ─── Event name constants ─────────────────────────────────────────────────────

const (
	// Orchestrator turn lifecycle.
	EvTurnStart     = "turn.start"
	EvTurnRouted    = "turn.routed"
	EvTurnStepped   = "turn.stepped"
	EvTurnPersisted = "turn.persisted"
	EvTurnDone      = "turn.done"

	// Harness.
	EvHarnessRequest        = "harness.request"
	EvHarnessResponseRaw    = "harness.response.raw"
	EvHarnessResponseParsed = "harness.response.parsed"
	EvHarnessRetry          = "harness.retry"
	EvHarnessError          = "harness.error"
	EvHarnessExec           = "harness.exec"
	EvHarnessRecordingHit   = "harness.recording_hit"
	EvHarnessRecordingMiss  = "harness.recording_miss"

	// Machine guard / effect / transition.
	EvMachineGuardEval          = "machine.guard.eval"
	EvMachineGuardWinner        = "machine.guard.winner"
	EvMachineEffectApplied      = "machine.effect.applied"
	EvMachineTransition         = "machine.transition"
	EvMachineValidationRejected = "machine.validation.rejected"

	// Synthetic-intent dispatch (emit_intent effect; see machine.applyEffectsTraced).
	// EvIntentEmitted records each successful self-dispatch; EvIntentEmitDepthCap
	// records a depth-cap abort (machine.EmitIntentMaxDepth).
	EvIntentEmitted       = "machine.intent.emitted"
	EvIntentEmitDepthCap  = "machine.intent.emit.depth_cap"

	// Expr.
	EvExprCompileError = "expr.compile_error"
	EvExprEvalError    = "expr.eval_error"

	// Store.
	EvStoreEventsAppended = "store.events.appended"

	// Deterministic routing.
	EvTurnDeterministicHit  = "turn.deterministic_hit"
	EvTurnDeterministicMiss = "turn.deterministic_miss"

	// Off-path side-channel (§7.7).  The off-path runtime is intentionally
	// orthogonal to the state machine — no Turn() fires, no transition events
	// land on the journey.  These trace constants are the only structured
	// breadcrumb of that activity in --trace-pretty output.
	EvOffPathEnter        = "offpath.enter"
	EvOffPathExit         = "offpath.exit"
	EvOffPathAskStart     = "offpath.ask.start"
	EvOffPathAskDone      = "offpath.ask.done"
	EvOffPathAskError     = "offpath.ask.error"
	EvOffPathChatResolved = "offpath.chat.resolved"

	// Timeout dispatcher (§9.5).  arm / cancel / fire / rearm cover every
	// dispatcher-side state change; error covers persistence and dispatch
	// failures.
	EvTimeoutArmed     = "timeout.armed"
	EvTimeoutCancelled = "timeout.cancelled"
	EvTimeoutFired     = "timeout.fired"
	EvTimeoutError     = "timeout.error"
	EvTimeoutRearmed   = "timeout.rearmed"

	// Teleport (used by inbox, off-path return, oracle return, etc.).
	// The synthetic turn it appends is already covered by turn.* but
	// teleport.* records the user-visible "I jumped sideways" intent.
	EvTeleportStart = "teleport.start"
	EvTeleportDone  = "teleport.done"

	// host.bind.error fires when a templated `bind:` value fails to render
	// after a successful host call. The bind is skipped (world unchanged)
	// rather than failing the turn.
	EvHostBindError = "host.bind.error"

	// Background-job lifecycle (orchestrator-side view; the scheduler has its
	// own job-table events but the user-visible mode transitions go here).
	EvJobSubmitted             = "job.submitted"
	EvJobTerminal              = "job.terminal" // done/failed/cancelled
	EvJobAwaitingInput         = "job.awaiting_input"
	EvJobClarificationAnswered = "job.clarification_answered"
	EvJobOnCompleteRun         = "job.on_complete.run"
	EvJobError                 = "job.error"

	// Slot-fill / disambiguation (orchestrator + TUI cooperate).
	EvSlotFillRequested = "slotfill.requested"
	EvSlotFillContinued = "slotfill.continued"
	EvDisambigPresented = "disambig.presented"
	EvDisambigChosen    = "disambig.chosen"

	// Inbox.
	EvInboxNotificationPosted = "inbox.notification.posted"
	EvInboxItemOpened         = "inbox.item.opened"
	EvInboxItemDismissed      = "inbox.item.dismissed"
)

// ─── Logger context key ───────────────────────────────────────────────────────

type ctxKey struct{}

// WithLogger stores a logger in the context. Retrieve it with FromContext.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger stored by WithLogger, or slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// ─── Span helper ──────────────────────────────────────────────────────────────

// Span emits a DebugContext at entry; the returned function emits at exit with
// duration. Call it as: defer trace.Span(ctx, logger, "op.name")(nil).
// Pass an error pointer to include err in the exit log.
func Span(ctx context.Context, logger *slog.Logger, name string, attrs ...slog.Attr) func(errp *error) {
	start := time.Now()
	if logger.Enabled(ctx, slog.LevelDebug) {
		args := make([]any, 0, len(attrs)+2)
		args = append(args, slog.String("span", name))
		for _, a := range attrs {
			args = append(args, a)
		}
		logger.DebugContext(ctx, name+".enter", args...)
	}
	return func(errp *error) {
		if !logger.Enabled(ctx, slog.LevelDebug) {
			return
		}
		dur := time.Since(start)
		args := []any{
			slog.String("span", name),
			slog.Duration("dur", dur),
		}
		if errp != nil && *errp != nil {
			args = append(args, slog.String("error", (*errp).Error()))
		}
		logger.DebugContext(ctx, name+".exit", args...)
	}
}

// ─── TurnLogger ──────────────────────────────────────────────────────────────

// TurnLogger is a thin helper that pre-attaches session/turn/state attributes
// to every log call, so emission points don't repeat them.
type TurnLogger struct {
	l *slog.Logger
}

// NewTurnLogger creates a TurnLogger pre-populated with common attributes.
func NewTurnLogger(base *slog.Logger, sid app.SessionID, turn app.TurnNumber, state app.StatePath) *TurnLogger {
	return &TurnLogger{
		l: base.With(
			slog.String("session_id", string(sid)),
			slog.Int64("turn", int64(turn)),
			slog.String("state_path", string(state)),
		),
	}
}

// Debug emits a debug event.
func (t *TurnLogger) Debug(ctx context.Context, msg string, args ...any) {
	t.l.DebugContext(ctx, msg, args...)
}

// Info emits an info event.
func (t *TurnLogger) Info(ctx context.Context, msg string, args ...any) {
	t.l.InfoContext(ctx, msg, args...)
}

// Enabled returns whether debug-level logging is enabled (for cheap guard).
func (t *TurnLogger) Enabled(ctx context.Context) bool {
	return t.l.Enabled(ctx, slog.LevelDebug)
}

// ─── Truncation helper ────────────────────────────────────────────────────────

// TruncateCap is the default max byte length for large fields like prompts.
const TruncateCap = 2048

// Truncate returns s if len(s) <= cap, otherwise s[:cap] + " … (truncated N bytes)".
func Truncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	omitted := len(s) - cap
	return fmt.Sprintf("%s … (truncated %d bytes)", s[:cap], omitted)
}

// ─── ReplayResult (kept for backward compat) ─────────────────────────────────

// ReplayResult summarises the outcome of replaying a session's event history.
type ReplayResult struct {
	TurnsReplayed int
	FinalState    app.StatePath
	FinalWorld    interface{} // world.World — avoid import cycle; callers cast
	Diffs         []SnapshotDiff
}

// SnapshotDiff describes a mismatch between a replayed state and a stored snapshot.
type SnapshotDiff struct {
	Turn interface{} // app.TurnNumber
}

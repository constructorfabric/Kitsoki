// Package trace provides structured JSONL tracing for hally sessions (§11).
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
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"hally/internal/app"
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
	EvHarnessRequest       = "harness.request"
	EvHarnessResponseRaw   = "harness.response.raw"
	EvHarnessResponseParsed = "harness.response.parsed"
	EvHarnessRetry         = "harness.retry"
	EvHarnessError         = "harness.error"
	EvHarnessExec          = "harness.exec"
	EvHarnessOracleHit     = "harness.oracle_hit"
	EvHarnessOracleMiss    = "harness.oracle_miss"

	// Machine guard / effect / transition.
	EvMachineGuardEval          = "machine.guard.eval"
	EvMachineGuardWinner        = "machine.guard.winner"
	EvMachineEffectApplied      = "machine.effect.applied"
	EvMachineTransition         = "machine.transition"
	EvMachineValidationRejected = "machine.validation.rejected"

	// Expr.
	EvExprCompileError = "expr.compile_error"
	EvExprEvalError    = "expr.eval_error"

	// Store.
	EvStoreEventsAppended = "store.events.appended"

	// Deterministic routing.
	EvTurnDeterministicHit  = "turn.deterministic_hit"
	EvTurnDeterministicMiss = "turn.deterministic_miss"
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

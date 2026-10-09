// Package trace provides structured JSONL tracing for kitsoki sessions
// (see docs/tracing/trace-format.md for the on-disk schema).
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
//	harness.retry, harness.error, harness.exec, harness.agent_hit, harness.agent_miss
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
	// EvTurnError fires when a turn aborts because machine.Turn itself
	// returned an error (e.g. an effect's set/when expression failed to
	// compile or evaluate). It guarantees the failure is recorded in the
	// trace — the orchestrator also journals a store.MachineError event so
	// the persisted session trace has a row for the failed turn.
	EvTurnError = "turn.error"

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
	// EvIntentEmitParallelDropped records a parallel-state emit_intent that was
	// dropped per the W2.8 limitation (parallel regions ride a separate
	// event-bus via propagateEmits; mixing the two muddles depth-cap accounting).
	// Fires at any of the three sites that previously disagreed:
	// machine.dispatchEmittedIntents, parallel.turnParallel (transition or
	// on_enter), and machine.DispatchPostBindEmits.
	EvIntentEmitted             = "machine.intent.emitted"
	EvIntentEmitDepthCap        = "machine.intent.emit.depth_cap"
	EvIntentEmitParallelDropped = "machine.intent.emit.parallel_dropped"

	// Expr.
	EvExprCompileError = "expr.compile_error"
	EvExprEvalError    = "expr.eval_error"

	// Store.
	EvStoreEventsAppended = "store.events.appended"

	// Deterministic routing.
	EvTurnDeterministicHit  = "turn.deterministic_hit"
	EvTurnDeterministicMiss = "turn.deterministic_miss"

	// Pre-LLM intercept (kitsoki intercept). EvInterceptMatched fires when the
	// side-effect-free Orchestrator.Classify resolves a verdict via a no-LLM
	// tier (deterministic / synonym / embedding) and the external gate may
	// execute; EvInterceptPassed fires when no no-LLM tier matched and the
	// gate lets the turn proceed to the LLM untouched. Classify itself never
	// emits these — the intercept gate that calls Classify does — so Classify
	// stays a pure read.
	EvInterceptMatched = "intercept.matched"
	EvInterceptPassed  = "intercept.passed"

	// Multi-turn intercept drive (conflict-capable intercept; see
	// docs/architecture/prompt-intercept.md §"Multi-turn commands"). When a
	// matched command's flow enters a room flagged intercept_drive: rest, the
	// gate escalates from the stateless OneShot to a budgeted, persisted,
	// synchronous drive-to-rest. EvInterceptEscalated opens that record (the
	// durable pointer the agent's ephemeral block report references and the
	// live-watch surface keys on); EvInterceptResolved / EvInterceptAborted close
	// it (the flow settled at a non-flagged resting place, or was safe-aborted
	// leaving a clean tree). The live, round-by-round feed while the drive runs is
	// the persisted session's OWN native events (agent.call.*, transitions) — the
	// drive needs no bespoke per-round event; `rounds` on the closing event is
	// derived from that same event log.
	//
	// Field schema:
	//   - intercept.escalated: input, intent, session_id, reason ("multi_turn")
	//   - intercept.resolved:  session_id, outcome, rounds, final_state
	//   - intercept.aborted:   session_id, outcome, rounds, final_state
	EvInterceptEscalated = "intercept.escalated"
	EvInterceptResolved  = "intercept.resolved"
	EvInterceptAborted   = "intercept.aborted"

	// Semantic routing (semroute; see
	// docs/architecture/semantic-routing.md). EvTurnSemanticHit
	// fires when [semroute.Matcher.Match] returns a single-intent
	// verdict above the configured high-bar; the orchestrator's
	// SubmitDirect path runs immediately after. EvTurnSemanticMiss
	// fires when the matcher returns zero confidence and we fall
	// through to the LLM. EvTurnSemanticAmbiguous fires when the
	// matcher returned a 0.50 tie and the orchestrator surfaces the
	// disambiguation card.
	//
	// Field schema (locked by docs/architecture/semantic-routing.md —
	// TUI route badges read these names directly):
	//   - semantic_hit:       intent, reason, confidence, state_path
	//   - semantic_miss:      state_path
	//   - semantic_ambiguous: candidates, state_path
	//   - llm_routed:         intent, confidence, state_path, model
	//   - near_miss:          input, confidence, threshold, state_path
	EvTurnSemanticHit       = "turn.semantic_hit"
	EvTurnSemanticMiss      = "turn.semantic_miss"
	EvTurnSemanticAmbiguous = "turn.semantic_ambiguous"

	// EvTurnNearMiss fires when the semantic tier's confidence-band switch
	// resolves a verdict that is neither a confident hit (>= HighBar), a
	// clarification band (>= MidBar), nor a tie (== ConfidenceTie) — i.e.
	// strictly between 0 (a genuine miss already returned earlier and never
	// reaches this switch) and MidBar. See never-silent-runtime.md: this
	// band must route to the S1 workbench, never silently fall through to
	// the LLM interpreter guessing the nearest authored intent (the
	// adjacent-command misroute class). The `threshold` field records
	// MidBar so the trace shows exactly why the verdict missed the accept
	// band, alongside the same `confidence` field the other semantic-tier
	// events record.
	EvTurnNearMiss = "turn.near_miss"

	// EvTurnDefaultRouted fires when deterministic + semantic (+ turn-cache)
	// all missed and the current state declares a free-text default_intent:
	// the whole utterance is routed deterministically to that intent's single
	// required string slot, with no main-turn LLM. The `intent` and `slot`
	// attrs name where the text landed. This is the conversational-room sink
	// (e.g. a discovery room's `discuss`) so plain prose never has to survive
	// an LLM classification that can mis-pick a command intent.
	EvTurnDefaultRouted = "turn.default_routed"

	// EvTurnLLMRouted fires once on the orchestrator side after the
	// harness resolves an intent via the LLM. Phase 5's cache
	// writeback hooks into the same event (see the turn-cache tier in
	// docs/architecture/semantic-routing.md); Phase 2 only emits the
	// trace breadcrumb.
	EvTurnLLMRouted = "turn.llm_routed"

	// EvTurnLLMMiss fires when the local-model routing tier (agent.local on a
	// semantic no_match) was tried but did not produce a usable verdict — it
	// returned "none"/low-confidence or errored — so routing falls through to
	// the main-turn LLM. The `model` attr names the backend that missed. Lets
	// the routing pipeline mark the local-LLM layer as tried-and-missed rather
	// than inferring it only when the cloud model later wins.
	EvTurnLLMMiss = "turn.llm_miss"

	// Off-path side-channel.  The off-path runtime is intentionally
	// orthogonal to the state machine — no Turn() fires, no transition events
	// land on the journey.  These trace constants are the structured slog
	// breadcrumb of that activity.
	EvOffPathEnter        = "offpath.enter"
	EvOffPathExit         = "offpath.exit"
	EvOffPathAskStart     = "offpath.ask.start"
	EvOffPathAskDone      = "offpath.ask.done"
	EvOffPathAskError     = "offpath.ask.error"
	EvOffPathChatResolved = "offpath.chat.resolved"

	// Timeout dispatcher.  arm / cancel / fire / rearm cover every
	// dispatcher-side state change; error covers persistence and dispatch
	// failures.
	EvTimeoutArmed     = "timeout.armed"
	EvTimeoutCancelled = "timeout.cancelled"
	EvTimeoutFired     = "timeout.fired"
	EvTimeoutError     = "timeout.error"
	EvTimeoutRearmed   = "timeout.rearmed"

	// Teleport (used by inbox, off-path return, agent return, etc.).
	// The synthetic turn it appends is already covered by turn.* but
	// teleport.* records the user-visible "I jumped sideways" intent.
	EvTeleportStart = "teleport.start"
	EvTeleportDone  = "teleport.done"

	// host.bind.error fires when a templated `bind:` value fails to render
	// after a successful host call. The bind is skipped (world unchanged)
	// rather than failing the turn.
	EvHostBindError = "host.bind.error"

	// host.on_error.redirect fires when a host call's `on_error:` arc
	// supersedes machine.Turn's resolved state and bounces the session
	// to a redirect target. Logs `namespace`, `error`, `from`, `to` so
	// a "why did we land back at idle?" diagnosis from the JSONL trace
	// doesn't need to dig into the (separate) store-event journal.
	EvHostOnErrorRedirect = "host.on_error.redirect"

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

// Warn emits a warning event. Used for turn-fatal faults (e.g. a turn
// that aborted because an effect expression failed to compile) that must
// surface in the trace at a level callers don't suppress.
func (t *TurnLogger) Warn(ctx context.Context, msg string, args ...any) {
	t.l.WarnContext(ctx, msg, args...)
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

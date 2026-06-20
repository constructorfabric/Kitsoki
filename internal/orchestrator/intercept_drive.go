// intercept_drive.go — the synchronous "drive a matched command to rest"
// execution path for the pre-LLM intercept gate (conflict-capable intercept;
// see docs/architecture/prompt-intercept.md §"Multi-turn commands").
//
// The stateless OneShot fast path settles a single self-contained command in
// one round. A command whose real execution is a multi-turn, oracle-in-the-loop
// loop — the canonical case is "rebase, and resolve any conflicts" — cannot be
// driven that way: it enters a room flagged intercept_drive: rest and would
// strand the working tree mid-rebase if abandoned after one round.
//
// DriveToRest is the escalation: it drives a real, PERSISTED session
// SYNCHRONOUSLY to a settled resting place under the caller's budget (the
// context deadline), then classifies the outcome. The driver itself is the
// orchestrator's existing settle machinery — SubmitDirect already runs the whole
// conflict → resolve → rebase_continue → conflict_resolved → branch_ops loop via
// settlePostBindEmits in a single call — so DriveToRest adds no new driving
// logic; it adds the gate-level concerns the settle loop has no opinion about:
//
//   - escalation detection — a settle that rests AT a flagged room means the
//     sub-flow could not complete (the resolver escalated, resolved:false);
//   - SAFE-ABORT — any non-success exit (escalation, budget exhaustion, error,
//     panic) fires the room's abort arc with a FRESH context so the tree is
//     never left mid-rebase, even when the original budget is already blown;
//   - the gate-level trace record — intercept.escalated (opened) paired with
//     intercept.resolved / intercept.aborted (closed).

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// DefaultInterceptAbortIntent is the intent DriveToRest fires to safe-abort a
// stranded sub-flow when the caller declares none. The git-ops conflict room's
// `abort` arc runs `git rebase --abort` and routes back to the branch hub.
const DefaultInterceptAbortIntent = "abort"

// interceptAbortBudget caps the safe-abort drive. The abort is a single git
// command plus a routing hop — it must be quick, and it runs on a fresh context
// precisely because the original budget may already be exhausted, so it needs a
// small independent deadline of its own.
const interceptAbortBudget = 30 * time.Second

// DriveOptions tunes a DriveToRest call. The zero value is valid: AbortIntent
// defaults to DefaultInterceptAbortIntent and Input is used only for the opening
// trace event's `input` field.
type DriveOptions struct {
	// Input is the original free-text prompt that matched, recorded on the
	// intercept.escalated event so the trace ties back to what the user typed.
	Input string
	// AbortIntent is the intent fired to safe-abort a stranded sub-flow. Empty
	// ⇒ DefaultInterceptAbortIntent.
	AbortIntent string
	// InitialWorld seeds world keys on the fresh session AFTER its boot on_enter
	// but BEFORE the command drive — the off-path side-channel pattern. The gate
	// uses it to pin the binding's runtime context (e.g. working_dir to the
	// prompt's repo); tests use it to disable the build gate. Empty ⇒ the app's
	// schema defaults stand.
	InitialWorld map[string]any
}

// DriveOutcome reports how a synchronous intercept drive settled. It is the
// structured record the gate composes its block report from and the trace
// closes its escalated/resolved/aborted loop with.
type DriveOutcome struct {
	// SessionID is the persisted session that was driven (the durable pointer a
	// live-watch surface keys on).
	SessionID app.SessionID
	// Intent is the command that was driven.
	Intent string
	// FinalState is the resting state after the drive (post-abort when aborted).
	FinalState app.StatePath
	// Resolved is true when the flow settled at a non-flagged resting place —
	// the command completed (a clean single command, or a conflict actually
	// resolved through to the branch hub).
	Resolved bool
	// Aborted is true when safe-abort ran (escalation / budget / error / panic).
	Aborted bool
	// Outcome is the machine-readable disposition: "resolved" | "escalation" |
	// "budget" | "error" | "panic".
	Outcome string
	// Rounds is the number of state hops the drive took (best-effort, derived
	// from the settle turn's TransitionApplied events) — 1 for a clean single
	// command, several for a resolved conflict loop.
	Rounds int
	// View is the rendered view of the final resting state.
	View string
	// Last is the final TurnOutcome (the resolved settle, or the abort settle).
	Last *TurnOutcome
}

// HasInterceptDriveRoom reports whether the loaded app declares ANY room flagged
// intercept_drive: rest. The gate uses it as the structural trigger: a binding
// whose app contains a multi-turn room escalates matches to DriveToRest instead
// of the stateless OneShot. A binding with no flagged room keeps the fast path
// unchanged.
func (o *Orchestrator) HasInterceptDriveRoom() bool {
	found := false
	var walk func(states map[string]*app.State)
	walk = func(states map[string]*app.State) {
		for _, s := range states {
			if s == nil {
				continue
			}
			if s.InterceptDrive == app.InterceptDriveRest {
				found = true
				return
			}
			walk(s.States)
		}
	}
	walk(o.def.States)
	return found
}

// isInterceptDriveRoom reports whether the given state path is a room flagged
// intercept_drive: rest. A drive that rests at such a room is an escalation.
func (o *Orchestrator) isInterceptDriveRoom(state app.StatePath) bool {
	s := lookupStateByPath(o.def, state)
	return s != nil && s.InterceptDrive == app.InterceptDriveRest
}

// DriveToRest creates a new persisted session, drives the matched intent
// synchronously to a settled resting place under ctx's budget, and classifies
// the outcome. On any non-success exit it runs the safe-abort arc on a fresh
// context so the working tree is never left mid-flow. It emits the
// intercept.escalated (opened) and intercept.resolved / intercept.aborted
// (closed) trace events. The returned error is reserved for an infrastructure
// failure that prevents even creating the session; a blown budget, a resolver
// escalation, or a drive error are NORMAL outcomes reported in DriveOutcome
// (with safe-abort already run), not errors.
func (o *Orchestrator) DriveToRest(ctx context.Context, intent string, slots map[string]any, opts DriveOptions) (out DriveOutcome, err error) {
	abortIntent := opts.AbortIntent
	if abortIntent == "" {
		abortIntent = DefaultInterceptAbortIntent
	}

	sid, err := o.NewSession(ctx)
	if err != nil {
		return DriveOutcome{}, fmt.Errorf("orchestrator: DriveToRest: new session: %w", err)
	}
	out.SessionID = sid
	out.Intent = intent

	// Boot the session into its routed hub (idle on_enter) before the command —
	// the matched command (e.g. rebase) is an arc on a hub, not on idle.
	if bootErr := o.RunInitialOnEnter(ctx, sid); bootErr != nil {
		// A boot failure can't have started a rebase, so there is nothing to
		// abort; surface it as an infra error so the gate fails open.
		return DriveOutcome{}, fmt.Errorf("orchestrator: DriveToRest: boot session: %w", bootErr)
	}

	// Seed the binding's runtime world AFTER boot, BEFORE the command — the
	// off-path side-channel appender (fresh turn = journey.Turn+1) so the next
	// SubmitDirect recomputes its turn from a clean load and never PK-collides.
	if len(opts.InitialWorld) > 0 {
		if seedErr := o.seedInterceptWorld(sid, opts.InitialWorld); seedErr != nil {
			return DriveOutcome{}, fmt.Errorf("orchestrator: DriveToRest: seed world: %w", seedErr)
		}
	}

	o.logger.Info(trace.EvInterceptEscalated,
		slog.String("input", opts.Input),
		slog.String("intent", intent),
		slog.String("session_id", string(sid)),
		slog.String("reason", "multi_turn"),
	)

	// Drive synchronously. A panic anywhere in the settle loop must still
	// safe-abort — the cardinal "never strand the tree" invariant.
	var (
		last      *TurnOutcome
		driveErr  error
		panicked  bool
		panicInfo any
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				panicInfo = r
			}
		}()
		last, driveErr = o.SubmitDirect(ctx, sid, intent, slots)
	}()

	switch {
	case panicked:
		out.Outcome = "panic"
		o.logger.Error("orchestrator: DriveToRest: panic in drive",
			slog.String("session_id", string(sid)), slog.Any("panic", panicInfo))
	case driveErr != nil:
		// A blown budget surfaces as a context error; everything else is a real
		// drive error. Both safe-abort.
		if ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled {
			out.Outcome = "budget"
		} else {
			out.Outcome = "error"
		}
		o.logger.Warn("orchestrator: DriveToRest: drive error",
			slog.String("session_id", string(sid)), slog.String("error", driveErr.Error()),
			slog.String("outcome", out.Outcome))
	case last != nil && last.Mode == ModeRejected:
		// The command was rejected (guard failed / not allowed in the booted
		// hub — e.g. a feature-branch command while HEAD is on main). It started
		// NOTHING, so there is nothing to abort and nothing was resolved: report
		// "rejected" and let the caller fail open to the model. No safe-abort.
		out.Outcome = "rejected"
	case last != nil && o.isInterceptDriveRoom(last.NewState):
		// Settled AT a flagged room ⇒ the sub-flow escalated (resolver could not
		// resolve). Safe-abort to clean the tree.
		out.Outcome = "escalation"
	default:
		// Settled at a non-flagged resting place ⇒ the command completed.
		out.Outcome = "resolved"
		out.Resolved = true
	}

	if last != nil {
		out.Rounds = countTransitions(last.Events)
		out.FinalState = last.NewState
		out.View = last.View
		out.Last = last
	}

	if out.Resolved {
		o.logger.Info(trace.EvInterceptResolved,
			slog.String("session_id", string(sid)),
			slog.String("outcome", out.Outcome),
			slog.Int("rounds", out.Rounds),
			slog.String("final_state", string(out.FinalState)),
		)
		return out, nil
	}

	// A rejected command started nothing — there is no tree to abort. Close the
	// record without an abort and let the caller fail open.
	if out.Outcome == "rejected" {
		o.logger.Info(trace.EvInterceptAborted,
			slog.String("session_id", string(sid)),
			slog.String("outcome", out.Outcome),
			slog.String("final_state", string(out.FinalState)),
		)
		return out, nil
	}

	// Non-success that may have started work (escalation / budget / error /
	// panic): safe-abort on a FRESH context (the original budget may be blown)
	// so the tree returns to a clean tip.
	abortCtx, cancel := context.WithTimeout(context.Background(), interceptAbortBudget)
	defer cancel()
	abortOut, abortErr := o.SubmitDirect(abortCtx, sid, abortIntent, nil)
	out.Aborted = true
	if abortErr != nil {
		o.logger.Error("orchestrator: DriveToRest: safe-abort failed",
			slog.String("session_id", string(sid)), slog.String("error", abortErr.Error()))
	} else if abortOut != nil {
		out.FinalState = abortOut.NewState
		out.View = abortOut.View
		out.Last = abortOut
	}

	o.logger.Info(trace.EvInterceptAborted,
		slog.String("session_id", string(sid)),
		slog.String("outcome", out.Outcome),
		slog.Int("rounds", out.Rounds),
		slog.String("final_state", string(out.FinalState)),
	)
	return out, nil
}

// seedInterceptWorld writes each key as an off-path EffectApplied event at a
// fresh turn (journey.Turn+1), mirroring the flow runner's world-override
// appender: the events' PRIMARY KEY is (session_id, turn, seq), so using a turn
// past the current max keeps them from colliding with the boot events, and the
// next SubmitDirect recomputes its turn from a fresh load so it cannot collide
// either.
func (o *Orchestrator) seedInterceptWorld(sid app.SessionID, vars map[string]any) error {
	j, err := o.LoadJourney(sid)
	if err != nil {
		return fmt.Errorf("load journey: %w", err)
	}
	turn := j.Turn + 1
	events := make([]store.Event, 0, len(vars))
	for k, v := range vars {
		events = append(events, store.Event{
			Kind:    store.EffectApplied,
			Turn:    turn,
			Payload: mustInterceptJSON(map[string]any{"set": map[string]any{k: v}}),
		})
	}
	return store.NewStoreSinkAdapter(o.store, sid).AppendBatch(events)
}

// mustInterceptJSON marshals a small, always-serialisable payload (a
// map[string]any of scalars), panicking only on a programmer error.
func mustInterceptJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("orchestrator: intercept seed: marshal: " + err.Error())
	}
	return b
}

// countTransitions returns the number of TransitionApplied events in a turn's
// event slice — the gate's best-effort "rounds" / hop count for a drive.
func countTransitions(events []store.Event) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == store.TransitionApplied {
			n++
		}
	}
	return n
}

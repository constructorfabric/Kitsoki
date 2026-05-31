package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/semroute"
	"kitsoki/internal/trace"
	"kitsoki/internal/turncache"
)

// Matcher returns the per-app semantic-routing [*semroute.Matcher],
// compiled lazily on first call. A nil return is a valid "no semantic
// routing on this app" sentinel — callers should treat it like an
// empty matcher and fall through.
//
// A nil return happens in three cases:
//
//  1. app.Routing != nil && !app.Routing.Enabled — author opted out.
//  2. Compile returned an error — the orchestrator logs once via the
//     supplied logger and treats subsequent calls as "no matcher."
//     Callers that want the error can call MatcherError.
//  3. The compiled matcher reports IsEmpty() — no synonyms or
//     examples to index, so there's nothing to match against.
//
// Callers MUST tolerate a nil return — it is not a programming
// error.
func (o *Orchestrator) Matcher() *semroute.Matcher {
	o.compileMatcher()
	if o.matcher == nil || o.matcher.IsEmpty() {
		return nil
	}
	return o.matcher
}

// MatcherError returns the (cached) error from the most recent
// matcher compile attempt, or nil if compilation succeeded or has not
// yet run. Surfacing the error this way mirrors how guard/view
// compile errors are surfaced by [machine.New] — the orchestrator
// stays alive on a malformed synonym so the rest of the app keeps
// working, but inspection callers can ask "did the semroute tier
// have a problem at load?"
func (o *Orchestrator) MatcherError() error {
	o.compileMatcher()
	return o.matcherErr
}

// compileMatcher builds the matcher exactly once per orchestrator.
// All consumers go through this; the sync.Once guard makes Matcher()
// safe to call from multiple goroutines without holding o.mu.
func (o *Orchestrator) compileMatcher() {
	o.matcherOnce.Do(func() {
		// Skip compile when routing is explicitly disabled — both
		// cheaper at startup and a useful "kill switch" if the
		// matcher misbehaves in production.
		if o.def != nil && o.def.Routing != nil && !o.def.Routing.Enabled {
			return
		}
		m, err := semroute.Compile(o.def)
		if err != nil {
			o.matcherErr = err
			o.logger.Warn(trace.EvTurnSemanticMiss,
				slog.String("phase", "compile"),
				slog.String("err", err.Error()),
			)
			return
		}
		o.matcher = m
	})
}

// routingEnabled is true iff the app's routing config either is nil
// (defaults apply) or has Enabled=true. Used by Turn to decide
// whether to call TrySemantic between deterministic and the LLM.
func (o *Orchestrator) routingEnabled() bool {
	if o.def == nil {
		return false
	}
	if o.def.Routing == nil {
		// app.DefaultRoutingConfig has Enabled=true; we honour that
		// even when the YAML omitted the block.
		return true
	}
	return o.def.Routing.Enabled
}

// RequiresUnfilledSlot returns true when the intent definition (looked
// up via [lookupIntentByPath]) declares ≥1 required slot that the
// supplied prefill map does not cover. Used by [TrySemantic] to
// abdicate Phase-2 matches that would otherwise reject with
// MISSING_SLOTS — Phase 4 fills those via template capture.
//
// Exported so the replay-routing CLI and the Phase-7 calibration test
// can apply the same gate TrySemantic enforces in production. Counting
// a verdict as "semroute-routed" when production would fall through to
// the LLM understates the LLM cost; calibration must apply the same
// guard or its published number drifts from reality.
func RequiresUnfilledSlot(def *app.AppDef, state app.StatePath, intentID string, prefilled map[string]any) bool {
	if def == nil {
		return false
	}
	ix, ok := lookupIntentByPath(def, state, intentID)
	if !ok {
		// Without an intent definition we can't decide; let the
		// downstream layers (machine.Validate) handle it.
		return false
	}
	for name, slot := range ix.Slots {
		if !slot.Required {
			continue
		}
		if _, has := prefilled[name]; has {
			continue
		}
		return true
	}
	return false
}

// recordSynonymHit notes a synonym hit against the cache when the
// orchestrator has one. Nil-cache (orchestrator built without
// WithTurnCache), empty MatchPattern/MatchKind (a tie verdict, or a
// match that never reached the matcher's bare/template branches), and
// nil AppDef all short-circuit. Errors are logged rather than
// returned — a hit-tracking write must not abort a successful turn.
func (o *Orchestrator) recordSynonymHit(ctx context.Context, verdict semroute.Verdict) {
	if o.cache == nil {
		return
	}
	if verdict.MatchPattern == "" || verdict.MatchKind == "" {
		return
	}
	if verdict.Intent == "" {
		return
	}
	if err := o.cache.RecordSynonymHit(ctx, turncache.SynonymKey{
		AppHash: o.appHash(),
		Intent:  verdict.Intent,
		Pattern: verdict.MatchPattern,
		Kind:    verdict.MatchKind,
	}, time.Now()); err != nil {
		o.logger.Warn(trace.EvTurnSemanticHit,
			slog.String("phase", "record_synonym_hit"),
			slog.String("err", err.Error()),
		)
	}
}

// semanticBars returns the (high, mid) confidence floors for the
// app, honouring app.Routing overrides and falling back to the
// built-in defaults from app.DefaultRoutingConfig.
func (o *Orchestrator) semanticBars() (high, mid float64) {
	if o.def != nil && o.def.Routing != nil {
		return o.def.Routing.SemanticHighBar, o.def.Routing.SemanticMidBar
	}
	d := app.DefaultRoutingConfig()
	return d.SemanticHighBar, d.SemanticMidBar
}

// TrySemantic attempts to route input via the semantic-routing tier
// without calling the LLM. It is the sibling of [TryDeterministic]:
// the orchestrator runs deterministic first, semantic second, LLM
// last (see docs/architecture/semantic-routing.md "The four tiers").
//
// After the oracle-split Phase 5, this method dispatches through
// [host.RunExtractForRouting] — making the semantic router one consumer
// of the host.oracle.extract tiered-resolver (oracle-split D13).
// The transport-level routing tests are unaffected: they test Turn()
// outcomes, not the internal routing path.
//
// Returns:
//
//   - (outcome, true, nil)  — verdict resolved; outcome is ready to
//     use (submitted, clarification queued, or disambiguation card).
//   - (nil, false, nil)     — no semantic match (or the tier is
//     disabled for this app); caller should call Turn.
//   - (nil, false, err)     — load-journey or SubmitDirect error.
//     A matcher compile error is NOT returned here — it is logged
//     once on first Matcher() call and the tier acts as a no-op.
//
// The behaviour by verdict band:
//
//   - Confidence ≥ HighBar (default 0.80) → SubmitDirect immediately.
//   - HighBar > Confidence ≥ MidBar (default 0.65) → ComputeClarification
//     for the matched intent (Phase 4 path; Phase 2 emits no verdicts
//     in this range, but the wiring is here so Phase 4 lands without
//     re-touching the orchestrator).
//   - Confidence == ConfidenceTie (0.50) → AMBIGUOUS_INTENT outcome
//     carrying the candidate list; the TUI surfaces the existing
//     disambiguation card.
//   - Otherwise → no match.
func (o *Orchestrator) TrySemantic(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, bool, error) {
	if !o.routingEnabled() {
		return nil, false, nil
	}
	m := o.Matcher()
	if m == nil {
		return nil, false, nil
	}

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: TrySemantic: load journey: %w", err)
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)

	allowedIntents := o.machine.AllowedIntents(journey.State, journey.World)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	// Phase 5 (oracle-split D13): dispatch through host.oracle.extract's
	// tiered resolver so the semantic router is one consumer of the extract
	// handler rather than a standalone path. RunExtractForRouting injects the
	// compiled Matcher into a context and calls the synonyms tier, returning
	// the full semroute.Verdict so the confidence-band logic below is unchanged.
	extractRes, extractErr := host.RunExtractForRouting(ctx, m, host.RoutingExtractArgs{
		Input:   input,
		State:   string(journey.State),
		Allowed: allowedNames,
	})
	if extractErr != nil {
		return nil, false, extractErr
	}

	verdict := extractRes.Verdict
	if extractRes.ResolvedBy == host.ResolvedByNoMatch() {
		// No hit from the extract tier — fall through to LLM.
		tl.Debug(ctx, trace.EvTurnSemanticMiss,
			slog.String("input", input),
		)
		return nil, false, nil
	}

	highBar, midBar := o.semanticBars()

	switch {
	case verdict.Confidence == semroute.ConfidenceTie:
		// Build the candidate-name list deterministically (Match
		// already sorts; we copy for trace + outcome).
		names := make([]string, 0, len(verdict.Candidates))
		for _, c := range verdict.Candidates {
			names = append(names, c.Intent)
		}
		tl.Debug(ctx, trace.EvTurnSemanticAmbiguous,
			slog.Any("candidates", names),
		)
		// Use the existing AMBIGUOUS_INTENT shape so the TUI's
		// disambiguation card renders without a new code path.
		outcome := &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			AllowedIntents: allowedNames,
			ErrorCode:      "AMBIGUOUS_INTENT",
			ErrorMessage:   "Multiple intents matched. Pick one.",
			TurnNumber:     turnNum,
		}
		return outcome, true, nil

	case verdict.Confidence >= highBar:
		// Phase 2 caveat: a bare-string synonym match cannot fill
		// slots (that needs Phase 4's template syntax).
		// If the matched intent declares ANY required slot that the
		// verdict didn't fill, fall through to the LLM so the slot
		// gets extracted. Without this guard a synonym like
		// "go south" (an Example treated as an implicit synonym)
		// would resolve to intent=go with no direction slot, and
		// the machine would reject with MISSING_SLOTS. The LLM is
		// the natural Phase-2 fallback for "named the verb, didn't
		// name the value."
		if RequiresUnfilledSlot(o.def, journey.State, verdict.Intent, verdict.Slots) {
			tl.Debug(ctx, trace.EvTurnSemanticMiss,
				slog.String("input", input),
				slog.String("intent", verdict.Intent),
				slog.String("reason", "matched intent has required slot the matcher cannot fill (Phase 2)"),
			)
			return nil, false, nil
		}
		tl.Debug(ctx, trace.EvTurnSemanticHit,
			slog.String("intent", verdict.Intent),
			slog.String("reason", verdict.MatchReason),
			slog.Float64("confidence", verdict.Confidence),
		)
		slots := verdict.Slots
		if slots == nil {
			slots = map[string]any{}
		}
		// Record the per-synonym hit so inspect surfaces
		// (--unused-synonyms, --routing-stats, --synonym-suggestions)
		// see real production data. Cache may be nil (orchestrator
		// constructed without WithTurnCache); guard accordingly. We
		// only record when the matcher surfaced a pattern + kind —
		// both are populated for every non-tie hit by semroute.
		o.recordSynonymHit(ctx, verdict)
		// Use SubmitDirectFromInput so the original user text — not a
		// "[direct] intent=…" marker — survives onto the TurnStarted
		// audit record and the view.rendered journal entry. Operators
		// reading inspect.LastTurns[].Input and replay-from-journal both
		// need the verbatim text.
		outcome, err := o.SubmitDirectFromInput(ctx, sid, verdict.Intent, slots, input)
		if err != nil {
			return nil, false, err
		}
		return outcome, true, nil

	case verdict.Confidence >= midBar:
		// Phase 2 cannot reach here — bare-string match always emits
		// either 0.90 or 0.50 or 0. The branch is wired so Phase 4
		// (template + slot-fill) drops in without further orchestrator
		// edits. Trace the event so the path is visible if it ever
		// fires unexpectedly.
		tl.Debug(ctx, trace.EvTurnSemanticHit,
			slog.String("intent", verdict.Intent),
			slog.String("reason", verdict.MatchReason),
			slog.Float64("confidence", verdict.Confidence),
			slog.String("note", "mid-band: clarification (Phase 4)"),
		)
		// Treat as a soft hit for now: trigger the clarification
		// for any missing slots. With no slots declared in Phase 2
		// this degrades to the same shape as a regular MISSING_SLOTS
		// rejection.
		clarification := ComputeClarification(o.def, journey.State, verdict.Intent, verdict.MissingSlots)
		return &TurnOutcome{
			Mode:           ModeClarify,
			NewState:       journey.State,
			PendingIntent:  verdict.Intent,
			PendingSlots:   verdict.Slots,
			SlotsNeeded:    clarification.Slots,
			AllowedIntents: allowedNames,
			TurnNumber:     turnNum,
		}, true, nil

	default:
		// Below mid-bar but non-zero. Today the matcher never emits
		// these (only 0.90 / 0.50 / 0). Log and fall through so an
		// over-eager future tier can't accidentally hijack a turn
		// the LLM would handle better.
		tl.Debug(ctx, trace.EvTurnSemanticMiss,
			slog.String("input", input),
			slog.String("note", "below mid-bar"),
			slog.Float64("confidence", verdict.Confidence),
		)
		return nil, false, nil
	}
}

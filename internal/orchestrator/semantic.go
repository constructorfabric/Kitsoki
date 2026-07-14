package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/roomchat"
	"kitsoki/internal/semroute"
	"kitsoki/internal/trace"
	"kitsoki/internal/turncache"
	"kitsoki/internal/world"
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

// semanticStackEnabled reports whether the deterministic semantic-routing stack
// (semroute + turn-cache + default_intent sink + free-form fallback) should run
// for this turn. A process-level override (WithSemanticRouting, wired from the
// CLI / KITSOKI_SEMANTIC_ROUTING) wins when set; otherwise it defers to the
// per-app routing.enabled config via routingEnabled. The exact display/example
// match (TryDeterministic) is intentionally NOT gated here — it is zero-cost and
// always runs so typed menu labels resolve without an LLM hop. When this returns
// false the turn falls straight through to the main-model interpreter
// (harness.RunTurn), keeping routing a distinct, isolated decision.
func (o *Orchestrator) semanticStackEnabled() bool {
	if o.semanticOverride != nil {
		return *o.semanticOverride
	}
	return o.routingEnabled()
}

// extractLLMOnNoMatch reports whether the app opted the semantic router into
// invoking the host.agent.extract LLM tier on a no_match (RoutingConfig.
// ExtractLLMOnNoMatch). Default false: a nil Routing block leaves it off, and
// DefaultRoutingConfig does not set it. The point of the opt-in is to back that
// LLM tier with a cheap local model (agent: agent.local) so an unrouted turn
// gets a schema-bounded, offline routing attempt before the main-turn LLM. The
// deterministic tiers always run first; this only changes what happens AFTER a
// deterministic no_match.
func (o *Orchestrator) extractLLMOnNoMatch() bool {
	if o.def == nil || o.def.Routing == nil {
		return false
	}
	return o.def.Routing.ExtractLLMOnNoMatch
}

// extractLLMAgent is the agent_plugins alias the no_match LLM routing tier
// dispatches to. It honours RoutingConfig.ExtractLLMAgent and defaults to
// "agent.local" (the local-model backend convention).
func (o *Orchestrator) extractLLMAgent() string {
	if o.def != nil && o.def.Routing != nil && o.def.Routing.ExtractLLMAgent != "" {
		return o.def.Routing.ExtractLLMAgent
	}
	return "agent.local"
}

// routeViaLLM runs the LLM tier of the semantic router on a deterministic
// no_match: it dispatches host.RunRoutingLLM through the configured agent
// plugin (typically agent.local) and returns the resulting verdict. The caller
// feeds a successful verdict through the same confidence-band switch as the
// deterministic tiers. Returns ok=false (and the caller falls through to the
// main-turn LLM) when no registry is wired, no intent fits, or the call errors.
func (o *Orchestrator) routeViaLLM(ctx context.Context, sid app.SessionID, turn app.TurnNumber, state app.StatePath, input string, allowed []string) (semroute.Verdict, bool, error) {
	if o.agentRegistry == nil {
		return semroute.Verdict{}, false, nil
	}
	llmCtx := host.WithAgentRegistry(ctx, o.agentRegistry)
	llmCtx = host.WithAgentPluginName(llmCtx, o.extractLLMAgent())
	llmCtx = host.WithAgentCallCtx(llmCtx, host.AgentCallCtx{
		SessionID: sid,
		Turn:      turn,
		StatePath: state,
	})
	llmCtx = host.WithAgentUsageBox(llmCtx)
	return host.RunRoutingLLM(llmCtx, input, string(state), allowed)
}

// pendingPlan resolves a dotted world path (e.g. "landing_note.plan") and
// reports whether a non-empty map is present at that path. Supports exactly
// two components (key.field); a single-component path treats the whole key as
// the value. Returns (nil, false) for an empty path, a missing key, or a
// non-map / empty-map value.
func pendingPlan(w world.World, path string) (map[string]any, bool) {
	if path == "" {
		return nil, false
	}
	dot := strings.IndexByte(path, '.')
	if dot < 0 {
		v, ok := w.Get(path).(map[string]any)
		return v, ok && len(v) > 0
	}
	topKey := path[:dot]
	restKey := path[dot+1:]
	parent, ok := w.Get(topKey).(map[string]any)
	if !ok || len(parent) == 0 {
		return nil, false
	}
	plan, ok := parent[restKey].(map[string]any)
	return plan, ok && len(plan) > 0
}

// routeViaContextualRouter fires when the active room has ContextualRouting.Enabled.
// It dispatches host.RunContextRouteLLM through the configured agent plugin and
// parses the resulting verdict:
//   - class=intent: adapts to semroute.Verdict, calls SubmitDirectRouted, returns outcome.
//   - class=help/room_request/meta_edit: slice-1 stub — emits decided trace event, no advance.
//
// Returns (outcome, true, nil) on a handled dispatch; (nil, false, nil) on a miss or
// when contextual routing is not applicable to this room.
func (o *Orchestrator) routeViaContextualRouter(
	ctx context.Context,
	sid app.SessionID,
	turnNum app.TurnNumber,
	tl *trace.TurnLogger,
	state app.StatePath,
	input string,
	allowedNames []string,
) (*TurnOutcome, bool, error) {
	if o.agentRegistry == nil {
		return nil, false, nil
	}
	stateDef := lookupStateByPath(o.def, state)
	if stateDef == nil || stateDef.ContextualRouting == nil || !stateDef.ContextualRouting.Enabled {
		return nil, false, nil
	}

	cr := stateDef.ContextualRouting

	// Pending-plan guard (CRR slice 3): when a plan is present at the configured
	// path, classify the utterance deterministically — no LLM call — so the
	// affirmation→accept / content→refine hot path is replayable and recorded.
	// This fires BEFORE the LLM call below, so default_intent: work never grabs
	// an affirmation while a plan is pending (the guard short-circuits TrySemantic
	// with a hit, preventing routeViaDefaultIntent from running).
	if planPath := cr.PendingPlanPath; planPath != "" {
		journey, journeyErr := o.loadJourney(sid)
		if journeyErr != nil {
			return nil, false, fmt.Errorf("routeViaContextualRouter: load journey: %w", journeyErr)
		}
		if _, hasPlan := pendingPlan(journey.World, planPath); hasPlan {
			acceptIntent := cr.PlanAcceptIntent
			if acceptIntent == "" {
				acceptIntent = "accept_plan"
			}
			refineIntent := cr.PlanRefineIntent
			if refineIntent == "" {
				refineIntent = "work"
			}

			if IsAffirmation(input) {
				// Affirmation: route to plan_accept_intent — advances the machine.
				prov := RouteProvenance{
					Source:     "pending_plan_affirmation",
					MatchType:  "deterministic",
					Confidence: 1.0,
				}
				tl.Debug(ctx, trace.EvTurnContextRouteDecided,
					slog.String("class", string(ClassIntent)),
					slog.String("intent", acceptIntent),
					slog.Float64("confidence", 1.0),
					slog.String("reason", "plan_affirmation"),
				)
				outcome, err := o.SubmitDirectRouted(ctx, sid, acceptIntent, map[string]any{}, input, prov)
				if err != nil {
					return nil, false, err
				}
				return outcome, true, nil
			}

			// Content-bearing follow-up: route to plan_refine_intent, capturing
			// the utterance into its single required string slot.
			slotName := ""
			if ix, ok := lookupIntentByPath(o.def, state, refineIntent); ok {
				slotName, _ = singleRequiredStringSlot(ix)
			}
			slots := map[string]any{}
			if slotName != "" {
				slots[slotName] = input
			}
			prov := RouteProvenance{
				Source:     "pending_plan_refine",
				MatchType:  "deterministic",
				Confidence: 1.0,
			}
			tl.Debug(ctx, trace.EvTurnContextRouteDecided,
				slog.String("class", string(ClassIntent)),
				slog.String("intent", refineIntent),
				slog.Float64("confidence", 1.0),
				slog.String("reason", "plan_refine"),
			)
			outcome, err := o.SubmitDirectRouted(ctx, sid, refineIntent, slots, input, prov)
			if err != nil {
				return nil, false, err
			}
			return outcome, true, nil
		}
	}

	lanes := make(map[string]string, 3)
	if cr.HelpChat != "" {
		lanes["help_chat"] = cr.HelpChat
	}
	if cr.RoomChat != "" {
		lanes["room_chat"] = cr.RoomChat
	}
	if cr.MetaChat != "" {
		lanes["meta_chat"] = cr.MetaChat
	}

	crCtx := host.WithAgentRegistry(ctx, o.agentRegistry)
	crCtx = host.WithAgentPluginName(crCtx, o.extractLLMAgent())
	crCtx = host.WithAgentCallCtx(crCtx, host.AgentCallCtx{
		SessionID: sid,
		Turn:      turnNum,
		StatePath: state,
	})
	crCtx = host.WithAgentUsageBox(crCtx)

	raw, ok, err := host.RunContextRouteLLM(crCtx, input, string(state), allowedNames, lanes)
	if err != nil {
		tl.Debug(ctx, trace.EvTurnContextRouteDecided,
			slog.String("reason", "error"),
			slog.String("err", err.Error()),
		)
		return nil, false, nil
	}
	if !ok {
		return nil, false, nil
	}

	verdict, parseErr := ParseContextRouteVerdict(raw)
	if parseErr != nil {
		tl.Debug(ctx, trace.EvTurnContextRouteDecided,
			slog.String("reason", "parse_error"),
			slog.String("err", parseErr.Error()),
		)
		return nil, false, nil
	}

	tl.Debug(ctx, trace.EvTurnContextRouteDecided,
		slog.String("class", string(verdict.Class)),
		slog.String("intent", verdict.Intent),
		slog.Float64("confidence", verdict.Confidence),
		slog.String("reason", verdict.Reason),
	)

	switch verdict.Class {
	case ClassIntent:
		if verdict.Intent == "" {
			return nil, false, nil
		}
		// Guard: intent must be in the allowed set (soundness invariant).
		inAllowed := false
		for _, a := range allowedNames {
			if a == verdict.Intent {
				inAllowed = true
				break
			}
		}
		if !inAllowed {
			return nil, false, nil
		}
		slots := verdict.Slots
		if slots == nil {
			slots = map[string]any{}
		}
		prov := RouteProvenance{
			Source:            "context_route",
			MatchType:         "contextual",
			Confidence:        verdict.Confidence,
			ContextRouteClass: string(verdict.Class),
		}
		outcome, err := o.SubmitDirectRouted(ctx, sid, verdict.Intent, slots, input, prov)
		if err != nil {
			return nil, false, err
		}
		outcome.ContextRoute = &ContextRouteReceipt{
			Class:        string(ClassIntent),
			Intent:       verdict.Intent,
			Reason:       verdict.Reason,
			Confidence:   verdict.Confidence,
			Alternatives: verdict.Alternatives,
			DecisionID:   fmt.Sprintf("%s:%d", sid, outcome.TurnNumber),
		}
		return outcome, true, nil

	default:
		// help / room_request / meta_edit — resolve the active room lane, append
		// the utterance, and return a non-advancing outcome (CRR slice 2).
		// Falls through to the no-advance stub when no chat store is wired.
		if o.chatStore == nil {
			return nil, false, nil
		}
		var kind roomchat.LaneKind
		switch verdict.Class {
		case ClassHelp:
			kind = roomchat.LaneHelp
		case ClassRoomRequest:
			kind = roomchat.LaneWork
		default: // ClassMetaEdit
			kind = roomchat.LaneMeta
		}
		resolver := roomchat.Resolver{Store: o.chatStore}
		laneTitle := string(verdict.Class) + " lane"
		chat, _, resolveErr := resolver.Active(ctx, o.def.App.ID, kind, string(state), laneTitle)
		if resolveErr != nil {
			tl.Debug(ctx, trace.EvTurnContextRouteDecided,
				slog.String("reason", "lane_resolve_error"),
				slog.String("err", resolveErr.Error()),
			)
			return nil, false, nil
		}
		if appendErr := resolver.Append(ctx, chat.ID, "user", input); appendErr != nil {
			tl.Debug(ctx, trace.EvTurnContextRouteDecided,
				slog.String("reason", "lane_append_error"),
				slog.String("err", appendErr.Error()),
			)
			return nil, false, nil
		}
		tl.Debug(ctx, trace.EvTurnContextRouteApplied,
			slog.String("lane", string(kind)),
			slog.String("chat_id", chat.ID),
		)
		return &TurnOutcome{
			Mode:     ModeOffPath,
			NewState: state,
			ContextRoute: &ContextRouteReceipt{
				Class:        string(verdict.Class),
				Reason:       verdict.Reason,
				Confidence:   verdict.Confidence,
				Alternatives: verdict.Alternatives,
				TargetChatID: chat.ID,
				TargetLane:   string(kind),
				DecisionID:   fmt.Sprintf("%s:%d", sid, turnNum),
			},
		}, true, nil
	}
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

// droppedSlotContent reports whether a slot-INCAPABLE semantic match (a
// bare-string synonym/example or an embedding hit — anything but a slot-filling
// `template` match) routed to an intent that declares a slot the verdict left
// unfilled, WHILE the user's utterance carried content beyond the matched
// pattern. That is the signature of a lossy route: the matcher recognised the
// verb but silently dropped the value the user supplied (e.g. "get `go test …`
// green" → configure with NO goal slot). Per the trace-accuracy principle the
// recorded route must reflect what the user actually said, so TrySemantic
// abdicates to the interpreter (LLM live, or the replay recording in a no-LLM
// demo) — the only tier that extracts slots — rather than firing an
// information-losing call. A BARE invocation (the input is essentially the
// matched pattern, no extra content) is not lossy and still fires fast.
//
// Distinct from RequiresUnfilledSlot, which defers on an unfilled REQUIRED slot
// at any confidence (a hard MISSING_SLOTS guard); this defers on an unfilled
// OPTIONAL slot only when content was demonstrably dropped.
func (o *Orchestrator) droppedSlotContent(state app.StatePath, v semroute.Verdict, input string) bool {
	// Template matches fill slots from {slot} capture, so they are not lossy.
	if v.MatchKind == "template" {
		return false
	}
	ix, ok := lookupIntentByPath(o.def, state, v.Intent)
	if !ok || len(ix.Slots) == 0 {
		return false
	}
	hasUnfilled := false
	for name := range ix.Slots {
		if _, filled := v.Slots[name]; !filled {
			hasUnfilled = true
			break
		}
	}
	if !hasUnfilled {
		return false
	}
	// Did the utterance carry content beyond the matched pattern? MatchReason is
	// "<kind>:<pattern>" (e.g. "synonym:head north", "example:go north"); an
	// embedding hit has no literal pattern, so it never equals the input and is
	// always treated as content-bearing (it matched semantics, not the verb).
	pattern := v.MatchReason
	if i := strings.IndexByte(pattern, ':'); i >= 0 {
		pattern = pattern[i+1:]
	}
	return normalizeForCompare(input) != normalizeForCompare(pattern)
}

// normalizeForCompare lower-cases, trims, and collapses internal whitespace so a
// bare-verb utterance compares equal to the synonym/example it matched.
func normalizeForCompare(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
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
// After the agent-split Phase 5, this method dispatches through
// [host.RunExtractForRouting] — making the semantic router one consumer
// of the host.agent.extract tiered-resolver (agent-split D13).
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
//   - Otherwise (0 < Confidence < MidBar) → near_miss (see
//     docs/architecture/semantic-routing.md "Near-miss band"). A verdict
//     this weak must never fall through to the LLM interpreter, which
//     would pick the closest authored intent by alphabet-of-commands —
//     that guess is the adjacent-command misroute this band exists to
//     stop. The verdict is recorded on the trace as near_miss with its
//     resolved destination: when the current room (or the app's
//     free_form_fallback floor) is a room-workbench (internal/app's
//     `workbench:` block), the whole utterance is routed straight into
//     that room's synthesized capture intent — the governed free-form
//     work floor — instead of the interpreter. Otherwise (no workbench
//     reachable) it records destination "interpreter_fallback" and
//     returns a miss so the caller advances to the next routing tier
//     exactly as before (nearMissWorkbenchDestination is the resolver;
//     see its doc comment). Confidence == 0 (a genuine miss) never
//     reaches this switch: every path above already returned
//     (nil, false, nil) for that case, so "reject floor" and "0"
//     coincide here without a new tunable.
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

	// Phase 5 (agent-split D13): dispatch through host.agent.extract's
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
		// The deterministic extract tiers (synonyms / slot_template) missed.
		// Record it NOW — before the (potentially multi-second) local-model
		// call — so the routing pipeline advances to the local-LLM layer live
		// instead of looking stuck on "semantic" for the whole call.
		tl.Debug(ctx, trace.EvTurnSemanticMiss, slog.String("input", input))

		// Embedding tier: if enabled and the Slice 3 substrate is in place, try
		// an embedding-based match before the LLM hop.
		embedHit := false
		if o.embedTier != nil {
			specs := make([]IntentSpec, len(allowedIntents))
			for i, ai := range allowedIntents {
				specs[i] = IntentSpec{Name: ai.Name}
			}
			embedVerdict, embedOK, embedErr := o.embedTier.Match(ctx, specs, input)
			if embedErr != nil {
				tl.Debug(ctx, trace.EvTurnSemanticMiss,
					slog.String("reason", "embed_tier_error"),
					slog.String("err", embedErr.Error()),
				)
			} else if embedOK {
				verdict = embedVerdict
				embedHit = true
			}
		}

		if !embedHit {
			// Contextual routing: fire BEFORE the LLM tier when the active room
			// opted into contextual_routing.enabled. On a successful verdict the
			// contextual router dispatches and returns; on a miss or error it falls
			// through to the existing LLM tier below.
			crOutcome, crOK, crErr := o.routeViaContextualRouter(ctx, sid, turnNum, tl, journey.State, input, allowedNames)
			if crErr != nil {
				return nil, false, crErr
			}
			if crOK {
				return crOutcome, true, nil
			}

			// When the app opted into ExtractLLMOnNoMatch, run the LLM tier — backed
			// by a cheap local model via agent: agent.local — for a schema-bounded
			// routing attempt before the main-turn LLM. A "none"/out-of-list verdict,
			// no registry, or an error falls through to the main-turn LLM.
			if !o.extractLLMOnNoMatch() {
				return nil, false, nil
			}
			llmVerdict, ok, llmErr := o.routeViaLLM(ctx, sid, turnNum, journey.State, input, allowedNames)
			if llmErr != nil {
				// A local-model failure must never abort the turn — record the
				// local-LLM miss (so the pipeline marks that layer) and fall through
				// to the main-turn LLM.
				tl.Debug(ctx, trace.EvTurnLLMMiss,
					slog.String("model", o.extractLLMAgent()),
					slog.String("reason", "error"),
					slog.String("err", llmErr.Error()),
				)
				return nil, false, nil
			}
			if !ok {
				tl.Debug(ctx, trace.EvTurnLLMMiss,
					slog.String("model", o.extractLLMAgent()),
					slog.String("reason", "no_match"),
				)
				return nil, false, nil
			}
			// LLM tier hit: adopt the verdict and fall through to the band switch,
			// which emits EvTurnLLMRouted naming the backend so the pipeline
			// attributes the hit to the local-LLM layer.
			verdict = llmVerdict
		}
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
		// AllowedIntents carries only the tied candidates so the web UI
		// and TUI show a short disambiguation list, not every intent in
		// the room.
		outcome := &TurnOutcome{
			Mode:           ModeRejected,
			NewState:       journey.State,
			AllowedIntents: names,
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
		// Trace-accuracy guard: a slot-incapable match that routed to a
		// slot-bearing intent while the utterance carried content beyond the
		// matched verb dropped the value the user supplied. Abdicate to the
		// interpreter, which extracts the slot, so the recorded route reflects
		// what the user actually said rather than an information-losing call.
		if o.droppedSlotContent(journey.State, verdict, input) {
			tl.Debug(ctx, trace.EvTurnSemanticMiss,
				slog.String("input", input),
				slog.String("intent", verdict.Intent),
				slog.String("reason", "matched intent has an unfilled slot and the utterance carried dropped content — deferring to the interpreter for slot extraction"),
			)
			return nil, false, nil
		}
		if verdict.MatchKind == "llm" {
			// The local-model LLM tier resolved this. Emit the LLM-routed event
			// (model = the backend plugin) so the routing pipeline marks the LLM
			// layer as the winner and names the backend, distinct from a
			// deterministic synonym hit.
			tl.Debug(ctx, trace.EvTurnLLMRouted,
				slog.String("intent", verdict.Intent),
				slog.String("model", o.extractLLMAgent()),
				slog.Float64("confidence", verdict.Confidence),
			)
		} else {
			tl.Debug(ctx, trace.EvTurnSemanticHit,
				slog.String("intent", verdict.Intent),
				slog.String("reason", verdict.MatchReason),
				slog.Float64("confidence", verdict.Confidence),
			)
		}
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
		// Use SubmitDirectRouted so the original user text — not a
		// "[direct] intent=…" marker — survives onto the TurnStarted
		// audit record and the view.rendered journal entry, AND so the
		// trace records WHY this intent fired (tier + match reason +
		// confidence). Operators reading inspect.LastTurns[].Input,
		// replay-from-journal, and anyone diagnosing an unexpected
		// transition all need this. See RouteProvenance.
		// Record WHICH tier routed. The LLM tier (verdict.MatchKind=="llm")
		// reports source "llm" and names the backend plugin (e.g. agent.local)
		// so the trace shows a local-model route is distinct from a deterministic
		// synonym — and from a main-turn claude route. (The matching agent.call.*
		// events also carry the plugin name.)
		prov := RouteProvenance{Source: "semantic", MatchType: verdict.MatchReason, Confidence: verdict.Confidence}
		if verdict.MatchKind == "llm" {
			prov.Source = "llm"
			prov.MatchType = o.extractLLMAgent()
		}
		outcome, err := o.SubmitDirectRouted(ctx, sid, verdict.Intent, slots, input, prov)
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
		// near_miss band: strictly between 0 and midBar. Confidence == 0
		// (a genuine miss) never reaches here — every path above already
		// returned (nil, false, nil) for that case — so this branch only
		// fires for a verdict that scored above the reject floor but
		// below the accept floor. Resolve a workbench destination (if
		// any) BEFORE recording the trace event so the decision — routed
		// to a named capture intent, or falling back to the interpreter
		// — is visible on the same turn.near_miss record, never silently
		// absorbed into "no match" (docs/architecture/semantic-routing.md
		// "Near-miss band").
		destIntent, destSlot, destOK := o.nearMissWorkbenchDestination(journey.State, allowedNames)
		destination := "interpreter_fallback"
		if destOK {
			destination = destIntent
		}
		tl.Debug(ctx, trace.EvTurnNearMiss,
			slog.String("input", input),
			slog.Float64("confidence", verdict.Confidence),
			slog.Float64("threshold", midBar),
			slog.String("destination", destination),
		)
		if destOK {
			prov := RouteProvenance{Source: "near_miss_workbench", MatchType: "workbench_capture"}
			outcome, err := o.SubmitDirectRouted(ctx, sid, destIntent, map[string]any{destSlot: input}, input, prov)
			if err != nil {
				return nil, false, err
			}
			return outcome, true, nil
		}
		// No workbench reachable from here — fall back to today's
		// off-ramp / no-match handling: the caller advances to the next
		// routing tier exactly as it did before this band was named.
		return nil, false, nil
	}
}

// nearMissWorkbenchDestination resolves the room-workbench capture intent
// (internal/app's `workbench:` block — see internal/app/workbench.go) a
// near_miss verdict should escalate into, if any exists. It checks, in
// order:
//
//  1. The current room itself: a state with a `workbench:` block has its
//     DefaultIntent set to the synthesized `<room>_capture` intent
//     (expandOneWorkbench sets both together, and State.Workbench is left
//     populated as the marker that this DefaultIntent is workbench-owned
//     rather than a hand-authored conversational sink like `discuss`).
//  2. The app's canonical free-form floor (routing.free_form_fallback),
//     when it names a DIFFERENT room that is itself a workbench and that
//     room's capture intent is currently allowed from here — the same
//     cross-room reachability [routeViaFreeFormFallback] relies on (the
//     loader's applyFreeFormFallback pass already clones that intent's
//     arc onto every non-workbench, non-off-ramp room at load time).
//
// Returns ("", "", false) when neither applies (no workbench reachable,
// or a hand-authored default_intent/off-ramp room with no workbench at
// all) so the caller keeps today's fallthrough.
func (o *Orchestrator) nearMissWorkbenchDestination(state app.StatePath, allowedNames []string) (intentName, slotName string, ok bool) {
	if o.def == nil {
		return "", "", false
	}

	cur := lookupStateByPath(o.def, state)

	// 1. The current room is itself a workbench floor.
	if cur != nil && cur.Workbench != nil {
		name := resolveDefaultIntentName(o.def, state, cur, allowedNames)
		if name != "" {
			if intentDef, found := lookupIntentByPath(o.def, state, name); found {
				if slot, single := singleRequiredStringSlot(intentDef); single {
					return name, slot, true
				}
			}
		}
	}

	// 2. The app's free-form fallback floor is a different workbench room.
	cfg, hasFallback := o.effectiveFreeFormFallback()
	if !hasFallback {
		return "", "", false
	}
	fallbackState := strings.TrimSpace(cfg.State)
	if fallbackState == "" || fallbackState == string(state) {
		return "", "", false
	}
	target := lookupStateByPath(o.def, app.StatePath(fallbackState))
	if target == nil || target.Workbench == nil {
		return "", "", false
	}
	fallbackIntent := resolveIntentAlias(o.def, state, cur, strings.TrimSpace(cfg.Intent))
	if fallbackIntent == "" || !containsString(allowedNames, fallbackIntent) {
		return "", "", false
	}
	intentDef, found := lookupIntentByPath(o.def, state, fallbackIntent)
	if !found {
		return "", "", false
	}
	slot, single := singleRequiredStringSlot(intentDef)
	if !single {
		return "", "", false
	}
	return fallbackIntent, slot, true
}

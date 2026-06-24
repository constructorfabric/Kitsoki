package orchestrator

import (
	"context"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/semroute"
	"kitsoki/internal/world"
)

// Classify runs the no-LLM routing tiers against an explicit (state, world)
// and returns the resulting verdict WITHOUT applying any effect, writing any
// event, or calling any LLM. It is the side-effect-free half of routing: an
// external gate (kitsoki intercept) classifies first, then executes only when
// a conservative gate decides to. Matching is not mutating.
//
// Tiers, in order (all no-LLM): deterministic display/example exact match
// (confidence 1.00) -> semantic synonym/template via the extract resolver ->
// optional embedding tier. The extract-LLM and main-turn LLM tiers are NEVER
// reached: a verdict unreachable without the LLM is a no-match here.
//
// Returns:
//   - (verdict, true, nil)  — a no-LLM tier resolved a verdict. The verdict
//     carries the matched intent, confidence band, and (when the matcher
//     surfaced them) MissingSlots so the gate can decide whether the match is
//     executable as-is or needs slot-fill.
//   - (zero, false, nil)    — no no-LLM tier matched; the gate should let the
//     turn proceed to the LLM untouched.
//   - (zero, false, err)    — the extract resolver errored.
//
// Classify touches NO store and writes NO events — it is pure over
// (o.def, o.machine, matcher, embedTier) plus the passed (state, world, input).
// It deliberately takes no session id so it CANNOT mutate session state.
func (o *Orchestrator) Classify(ctx context.Context, state app.StatePath, w world.World, input string) (semroute.Verdict, bool, error) {
	// 1. Deterministic tier: display / example exact match (confidence 1.00).
	menu := ComputeMenu(o.def, o.machine, state, w)
	if len(menu.Primary) > 0 {
		lookup := buildDeterministicLookup(o, state, menu.Primary)
		norm := normalizeInput(input)

		if idx, ok := lookup.byDisplay[norm]; ok && idx >= 0 {
			return deterministicVerdict(menu.Primary[idx], "deterministic:display"), true, nil
		}
		if idx, ok := lookup.byExample[norm]; ok && idx >= 0 {
			return deterministicVerdict(menu.Primary[idx], "deterministic:example"), true, nil
		}
	}

	// 2. Semantic deterministic tier: synonym / template via the extract
	//    resolver. This carries synonym hits at 0.90 AND ties at 0.50 — the
	//    gate inspects them. Disabled-routing apps and apps with no compiled
	//    matcher short-circuit to the embedding tier below.
	allowedIntents := o.machine.AllowedIntents(state, w)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	if o.routingEnabled() {
		if m := o.Matcher(); m != nil {
			res, err := host.RunExtractForRouting(ctx, m, host.RoutingExtractArgs{
				Input:   input,
				State:   string(state),
				Allowed: allowedNames,
			})
			if err != nil {
				return semroute.Verdict{}, false, err
			}
			if res.ResolvedBy != host.ResolvedByNoMatch() {
				return res.Verdict, true, nil
			}
		}
	}

	// 3. Embedding tier (still no-LLM, opt-in): only reached when the
	//    deterministic extract reported no-match. nil tier (or disabled
	//    config) returns (zero, false, nil) so this falls through.
	if o.embedTier != nil {
		specs := make([]IntentSpec, len(allowedIntents))
		for i, ai := range allowedIntents {
			specs[i] = IntentSpec{Name: ai.Name}
		}
		if verdict, ok, err := o.embedTier.Match(ctx, specs, input); err != nil {
			return semroute.Verdict{}, false, err
		} else if ok {
			return verdict, true, nil
		}
	}

	// 4. No no-LLM tier matched. NEVER call o.routeViaLLM, the harness, or any
	//    LLM tier here — a verdict unreachable without the LLM is a no-match.
	return semroute.Verdict{}, false, nil
}

// deterministicVerdict builds a Confidence==1.00 verdict from a matched primary
// MenuEntry. Slots come from the entry's PrefilledSlots (empty map when nil);
// MissingSlots carries the names of any required slots the menu could not
// pre-fill, so the gate can tell an executable display match from one that
// still needs clarification. matchReason is "deterministic:display" or
// "deterministic:example"; MatchPattern is the entry's Display label.
func deterministicVerdict(entry MenuEntry, matchReason string) semroute.Verdict {
	slots := entry.PrefilledSlots
	if slots == nil {
		slots = map[string]any{}
	}
	var missing []string
	for _, ref := range entry.MissingSlots {
		missing = append(missing, ref.Name)
	}
	return semroute.Verdict{
		Intent:       entry.Intent,
		Slots:        slots,
		MissingSlots: missing,
		Confidence:   semroute.ConfidenceExact,
		MatchReason:  matchReason,
		MatchPattern: entry.Display,
	}
}

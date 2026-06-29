package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/trace"
)

// routeViaDefaultIntent is the deterministic free-text tier. It runs after the
// deterministic, semantic, and turn-cache tiers have all missed: if the current
// state declares a `default_intent`, the whole utterance is routed straight to
// that intent with the input filling its single required string slot — no
// main-turn LLM classification.
//
// Why this exists: a conversational/discovery room's plain prose ("this doc",
// "what about the auth flow") matches no command intent deterministically, and
// the main-turn LLM router can mis-classify it into a near-miss command (a real
// dogfood bug: prose landed on `look` instead of the room's `discuss` arc). The
// default_intent contract — one intent, one required string slot — lets the
// engine sink that prose deterministically into the conversation. Commands the
// operator does name ("ready", "quit", "see docs/…") still win earlier, in the
// deterministic/semantic tiers; only genuinely unmatched text reaches here.
//
// Returns (outcome, true, nil) when it fired, (nil, false, nil) when the state
// declares no usable default (no default_intent, the named intent is not
// currently allowed, or it does not have exactly one required string slot) so
// the caller falls through to the main-turn LLM exactly as before.
func (o *Orchestrator) routeViaDefaultIntent(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, bool, error) {
	if !o.routingEnabled() {
		return nil, false, nil
	}
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: routeViaDefaultIntent: load journey: %w", err)
	}

	st := lookupStateByPath(o.def, journey.State)
	if st == nil || strings.TrimSpace(st.DefaultIntent) == "" {
		return nil, false, nil
	}

	allowedIntents := o.machine.AllowedIntents(journey.State, journey.World)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}

	intentName := resolveDefaultIntentName(o.def, journey.State, st, allowedNames)
	if intentName == "" {
		// Declared but not currently allowed (e.g. a guard is false this turn).
		return nil, false, nil
	}

	intentDef, ok := lookupIntentByPath(o.def, journey.State, intentName)
	if !ok {
		return nil, false, nil
	}
	slotName, ok := singleRequiredStringSlot(intentDef)
	if !ok {
		// The default-intent contract is one required string slot to receive
		// the free text; if the intent doesn't have exactly that, fall through
		// to the main LLM rather than guess where the text goes.
		return nil, false, nil
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnDefaultRouted,
		slog.String("intent", intentName),
		slog.String("slot", slotName),
	)

	prov := RouteProvenance{Source: "default", MatchType: "free_text"}
	outcome, err := o.SubmitDirectRouted(ctx, sid, intentName, map[string]any{slotName: input}, input, prov)
	if err != nil {
		return nil, false, err
	}
	return outcome, true, nil
}

func (o *Orchestrator) routeViaFreeFormFallback(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, bool, error) {
	if !o.routingEnabled() {
		return nil, false, nil
	}
	cfg, ok := o.effectiveFreeFormFallback()
	if !ok {
		return nil, false, nil
	}
	intentName := strings.TrimSpace(cfg.Intent)
	if intentName == "" || strings.TrimSpace(input) == "" {
		return nil, false, nil
	}
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: routeViaFreeFormFallback: load journey: %w", err)
	}
	st := lookupStateByPath(o.def, journey.State)
	if st == nil || st.DefaultIntent != "" || st.AgentOffRamp != nil {
		return nil, false, nil
	}
	if strings.TrimSpace(cfg.State) == string(journey.State) {
		return nil, false, nil
	}

	allowedIntents := o.machine.AllowedIntents(journey.State, journey.World)
	allowedNames := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowedNames[i] = ai.Name
	}
	intentName = resolveIntentAlias(o.def, journey.State, st, intentName)
	if !containsString(allowedNames, intentName) {
		return nil, false, nil
	}

	intentDef, ok := lookupIntentByPath(o.def, journey.State, intentName)
	if !ok {
		return nil, false, nil
	}
	slotName, ok := singleRequiredStringSlot(intentDef)
	if !ok {
		return nil, false, nil
	}

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	tl.Debug(ctx, trace.EvTurnDefaultRouted,
		slog.String("intent", intentName),
		slog.String("slot", slotName),
		slog.String("fallback_state", cfg.State),
	)

	prov := RouteProvenance{Source: "fallback", MatchType: "free_text"}
	outcome, err := o.SubmitDirectRouted(ctx, sid, intentName, map[string]any{slotName: input}, input, prov)
	if err != nil {
		return nil, false, err
	}
	return outcome, true, nil
}

func (o *Orchestrator) effectiveFreeFormFallback() (app.FreeFormFallbackConfig, bool) {
	if o.def == nil {
		return app.FreeFormFallbackConfig{}, false
	}
	if o.def.Routing != nil && o.def.Routing.FreeFormFallback != nil {
		cfg := *o.def.Routing.FreeFormFallback
		return cfg, strings.TrimSpace(cfg.State) != "" || strings.TrimSpace(cfg.Intent) != ""
	}
	type candidate struct {
		path   string
		intent string
	}
	var candidates []candidate
	walkOrchestratorStates(o.def.States, "", func(path string, s *app.State) {
		if s == nil || baseOrchestratorStateName(path) != "landing" {
			return
		}
		if _, ok := s.On["work"]; ok {
			candidates = append(candidates, candidate{path: path, intent: "work"})
			return
		}
		for intentName := range s.On {
			if strings.HasSuffix(intentName, "__work") {
				candidates = append(candidates, candidate{path: path, intent: intentName})
			}
		}
	})
	if len(candidates) == 0 {
		return app.FreeFormFallbackConfig{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].path == candidates[j].path {
			return candidates[i].intent < candidates[j].intent
		}
		return candidates[i].path < candidates[j].path
	})
	return app.FreeFormFallbackConfig{State: candidates[0].path, Intent: candidates[0].intent}, true
}

func walkOrchestratorStates(states map[string]*app.State, prefix string, fn func(path string, s *app.State)) {
	for name, s := range states {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fn(path, s)
		if s != nil && len(s.States) > 0 {
			walkOrchestratorStates(s.States, path, fn)
		}
	}
}

func baseOrchestratorStateName(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// resolveDefaultIntentName maps a state's authored default_intent to the intent
// key actually present on the (possibly import-folded) machine and confirms it
// is allowed this turn. The authored name may be bare ("discuss") while the
// folded machine uses an import-prefixed key ("core__discuss"); the rewriter
// records that mapping in IntentAliases, so we resolve through it. Returns ""
// when the resolved name is not in the allowed set.
func resolveDefaultIntentName(def *app.AppDef, state app.StatePath, st *app.State, allowed []string) string {
	di := strings.TrimSpace(st.DefaultIntent)
	if di == "" {
		return ""
	}
	di = resolveIntentAlias(def, state, st, di)
	for _, a := range allowed {
		if a == di {
			return di
		}
	}
	return ""
}

func resolveIntentAlias(def *app.AppDef, state app.StatePath, st *app.State, name string) string {
	if def == nil {
		return strings.TrimSpace(name)
	}
	name = strings.TrimSpace(name)
	if st == nil {
		return name
	}

	path := strings.TrimSpace(string(state))
	if idx := strings.Index(path, "#"); idx >= 0 {
		path = path[:idx]
	}
	if path == "" {
		path = string(state)
	}
	for {
		n := lookupStateByPath(def, app.StatePath(path))
		if n != nil {
			if mapped, ok := n.IntentAliases[name]; ok && strings.TrimSpace(mapped) != "" {
				return mapped
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}

	if mapped, ok := st.IntentAliases[name]; ok && strings.TrimSpace(mapped) != "" {
		return mapped
	}
	return name
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// singleRequiredStringSlot returns the name of the intent's sole required slot
// when there is exactly one and it is a string (the default-intent contract:
// one required string slot to receive the free text). Optional slots are
// ignored — they take their declared defaults. Returns ("", false) otherwise.
func singleRequiredStringSlot(intent app.Intent) (string, bool) {
	name := ""
	count := 0
	for n, s := range intent.Slots {
		if !s.Required {
			continue
		}
		// A non-string required slot can't take a raw utterance.
		if s.Type != "" && s.Type != "string" {
			return "", false
		}
		name = n
		count++
	}
	if count != 1 {
		return "", false
	}
	return name, true
}

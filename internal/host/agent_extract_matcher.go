// Package host — compiled-matcher injection for transport-level routing.
//
// The orchestrator's TrySemantic calls the extract handler with an in-memory
// compiled Matcher (from internal/semroute) rather than a YAML file path. This
// context key and the RunExtractWithMatcher function implement that seam:
//
//  1. The orchestrator injects the compiled Matcher via WithExtractMatcher.
//  2. RunExtractWithMatcher runs the same tiered-resolver logic as AgentExtractHandler
//     but uses the injected matcher for the synonyms/slot_template tiers.
//  3. All existing transport-level routing tests pass because they test
//     orchestrator.Turn(), which calls TrySemantic, which calls RunExtractWithMatcher.
//
// The seam is below the transport tests: they observe the same TurnOutcome
// regardless of whether the resolver goes through a YAML file or an in-memory
// Matcher.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kitsoki/internal/semroute"
)

// extractMatcherKey is the context key for an injected semroute Matcher.
type extractMatcherKey struct{}

// WithExtractMatcher returns a child context that carries m as the in-process
// resolver for extract calls made from the transport routing tier. The handler
// reads it via extractMatcherFromContext.
func WithExtractMatcher(ctx context.Context, m *semroute.Matcher, state string, allowed []string) context.Context {
	return context.WithValue(ctx, extractMatcherKey{}, &extractMatcherCtx{
		matcher: m,
		state:   state,
		allowed: allowed,
	})
}

type extractMatcherCtx struct {
	matcher *semroute.Matcher
	state   string
	allowed []string
}

// extractMatcherFromContext returns the injected matcher context, or nil if
// none is installed.
func extractMatcherFromContext(ctx context.Context) *extractMatcherCtx {
	v, _ := ctx.Value(extractMatcherKey{}).(*extractMatcherCtx)
	return v
}

// tryMatcherSynonyms uses an injected semroute.Matcher to resolve input.
// Returns (verdict, true, nil) on any non-zero confidence verdict (including
// ties). Returns (Verdict{}, false, nil) on a miss.
func tryMatcherSynonyms(ctx context.Context, mc *extractMatcherCtx, input string) (verdict semroute.Verdict, ok bool, err error) {
	if mc == nil || mc.matcher == nil {
		return semroute.Verdict{}, false, nil
	}
	v, matchErr := mc.matcher.Match(ctx, mc.state, mc.allowed, input)
	if matchErr != nil {
		return semroute.Verdict{}, false, matchErr
	}
	if v.Confidence == 0 {
		return semroute.Verdict{}, false, nil
	}
	return v, true, nil
}

// RoutingExtractArgs are the args synthesised by the orchestrator for a
// transport-level routing call to the extract handler.
type RoutingExtractArgs struct {
	Input   string
	State   string
	Allowed []string
}

// RoutingExtractResult is the result of a transport-level routing extract call.
// It wraps the semroute.Verdict so the orchestrator can use the full verdict
// (including Confidence, Candidates, Slots) to drive SubmitDirect / disambiguation.
type RoutingExtractResult struct {
	Verdict    semroute.Verdict
	ResolvedBy string
}

// RunExtractForRouting is the transport-routing entry point. It injects the
// compiled matcher into ctx and calls the extract synonyms tier, returning the
// semroute.Verdict so the orchestrator can route based on confidence bands.
//
// When no hit is found, ResolvedBy == "no_match" and Verdict is zero.
func RunExtractForRouting(ctx context.Context, m *semroute.Matcher, args RoutingExtractArgs) (RoutingExtractResult, error) {
	if m == nil || m.IsEmpty() {
		return RoutingExtractResult{ResolvedBy: resolvedByNoMatch}, nil
	}

	mctx := &extractMatcherCtx{
		matcher: m,
		state:   args.State,
		allowed: args.Allowed,
	}

	verdict, ok, err := tryMatcherSynonyms(
		context.WithValue(ctx, extractMatcherKey{}, mctx),
		mctx,
		args.Input,
	)
	if err != nil {
		return RoutingExtractResult{ResolvedBy: resolvedByNoMatch}, err
	}
	if !ok {
		return RoutingExtractResult{ResolvedBy: resolvedByNoMatch}, nil
	}

	kind := resolvedBySynonyms
	if verdict.MatchKind == "template" {
		kind = resolvedBySlotTemplate
	}
	return RoutingExtractResult{Verdict: verdict, ResolvedBy: kind}, nil
}

// RunRoutingLLM is the LLM tier of the semantic router: it asks an agent
// (typically a cheap local model via agent: agent.local) to classify input
// into one of the allowed intents, returning a semroute.Verdict the caller
// feeds through the same confidence-band logic as the deterministic tiers.
//
// It dispatches through the agent plugin contract, so the caller MUST have
// injected the registry (WithAgentRegistry), the plugin alias
// (WithAgentPluginName), and an AgentCallCtx. When no plugin is named the
// dispatch is a no-op and this returns ok=false (the caller falls through to
// the main-turn LLM exactly as before).
//
// The schema pins {intent ∈ allowed∪"none", confidence ∈ [0,1]} — flat, in the
// grammar subset, so a grammar-capable backend constrains the decode. The model
// is interpretive; ValidateSubmission remains the structural guarantee, and a
// verdict naming "none" or an out-of-list intent is treated as a miss.
func RunRoutingLLM(ctx context.Context, input, state string, allowed []string) (semroute.Verdict, bool, error) {
	if len(allowed) == 0 {
		return semroute.Verdict{}, false, nil
	}

	// Build a flat, in-subset schema: intent enum (allowed + "none") + confidence.
	enum := make([]string, 0, len(allowed)+1)
	enum = append(enum, allowed...)
	enum = append(enum, "none")
	schema, err := json.Marshal(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"intent", "confidence"},
		"properties": map[string]any{
			"intent": map[string]any{"type": "string", "enum": enum},
			// No min/max: llama.cpp's grammar constrains the JSON type but NOT a
			// numeric range, so a small model commonly emits confidence as a
			// percentage (e.g. 95). Bounding it here would make ValidateSubmission
			// reject every such verdict and defeat the whole tier; we accept any
			// number and clamp in code instead.
			"confidence": map[string]any{"type": "number"},
		},
	})
	if err != nil {
		return semroute.Verdict{}, false, err
	}

	var b strings.Builder
	b.WriteString("You are an intent router. Map the user's command to EXACTLY ONE intent id from the list below, or \"none\" if none fits. ")
	b.WriteString("Respond only with the structured verdict {intent, confidence}, where confidence is your certainty from 0 to 1 (a decimal, NOT a percentage).\n\nAllowed intents:\n")
	for _, name := range allowed {
		fmt.Fprintf(&b, "- %s\n", name)
	}
	fmt.Fprintf(&b, "\nUser command: %s\n", input)

	res, handled, derr := TryDispatchVerb(ctx, "extract", b.String(), "", "", "", map[string]any{
		"routing_state": state,
	}, json.RawMessage(schema))
	if derr != nil {
		return semroute.Verdict{}, false, derr
	}
	if !handled {
		return semroute.Verdict{}, false, nil
	}

	sub, _ := res.Data["submission"].(map[string]any)
	if sub == nil {
		return semroute.Verdict{}, false, nil
	}
	intent, _ := sub["intent"].(string)
	if intent == "" || intent == "none" {
		return semroute.Verdict{}, false, nil
	}
	// Guard: the model must name an allowed intent (the enum should enforce this,
	// but a fail-open backend can drift — keep the router sound).
	inAllowed := false
	for _, a := range allowed {
		if a == intent {
			inAllowed = true
			break
		}
	}
	if !inAllowed {
		return semroute.Verdict{}, false, nil
	}

	conf, _ := sub["confidence"].(float64)
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}

	return semroute.Verdict{
		Intent:      intent,
		Slots:       map[string]any{},
		Confidence:  conf,
		MatchReason: "llm-routing",
		MatchKind:   "llm",
	}, true, nil
}

// RunContextRouteLLM is the contextual-routing tier's host helper: it asks an
// agent (typically a cheap local model via agent: agent.local) to classify input
// into one of the four context-route classes (intent|help|room_request|meta_edit),
// returning the raw submission map for the caller to parse via
// orchestrator.ParseContextRouteVerdict.
//
// It dispatches through the same agent plugin seam as RunRoutingLLM, so the
// caller MUST have injected the registry (WithAgentRegistry), the plugin alias
// (WithAgentPluginName), and an AgentCallCtx. When no plugin is named the
// dispatch is a no-op and this returns ok=false.
func RunContextRouteLLM(ctx context.Context, input, state string, allowedIntents []string, lanes map[string]string) (map[string]any, bool, error) {
	classes := []string{"intent", "help", "room_request", "meta_edit"}

	schema, err := json.Marshal(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"class", "confidence"},
		"properties": map[string]any{
			"class":      map[string]any{"type": "string", "enum": classes},
			"intent":     map[string]any{"type": "string"},
			"confidence": map[string]any{"type": "number"},
			"reason":     map[string]any{"type": "string"},
			// alternatives carries lower-confidence competing verdicts so the
			// operator can inspect what the router considered before deciding.
			"alternatives": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"class", "confidence"},
					"properties": map[string]any{
						"class":      map[string]any{"type": "string"},
						"intent":     map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number"},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, false, err
	}

	var b strings.Builder
	b.WriteString("You are a contextual router. Classify the user's input into exactly one class:\n")
	b.WriteString("- intent: the input is a command matching one of the allowed intents\n")
	b.WriteString("- help: the user is asking for help or documentation\n")
	b.WriteString("- room_request: the user wants to navigate to a different room\n")
	b.WriteString("- meta_edit: the user wants to edit or configure the application\n")
	if len(allowedIntents) > 0 {
		b.WriteString("\nAllowed intents (for class=intent only):\n")
		for _, name := range allowedIntents {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	fmt.Fprintf(&b, "\nUser input: %s\n", input)

	res, handled, derr := TryDispatchVerb(ctx, "extract", b.String(), "", "", "", map[string]any{
		"routing_state": state,
	}, json.RawMessage(schema))
	if derr != nil {
		return nil, false, derr
	}
	if !handled {
		return nil, false, nil
	}

	sub, _ := res.Data["submission"].(map[string]any)
	if sub == nil {
		return nil, false, nil
	}
	return sub, true, nil
}

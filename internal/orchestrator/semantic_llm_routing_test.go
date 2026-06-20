// Tests the LLM tier of the semantic router (RoutingConfig.ExtractLLMOnNoMatch):
// on a deterministic no_match, TrySemantic must dispatch to the configured
// agent plugin (agent.local), map its {intent, confidence} verdict onto the
// confidence bands, and route accordingly — instead of falling through to the
// main-turn LLM. A fake agent stands in for the local model so the test is
// fast and offline.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// stubRoutingAgent records its calls and returns a fixed routing verdict.
type stubRoutingAgent struct {
	calls      int32
	submission string
}

func (s *stubRoutingAgent) Ask(ctx context.Context, req agent.AskRequest) (agent.AskResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	return agent.AskResponse{
		Submission: json.RawMessage(s.submission),
		Meta:       map[string]any{"model": "stub-local", "grammar": true},
	}, nil
}

func (s *stubRoutingAgent) Close() error { return nil }

// TestSemanticLLMTier_RoutesOnNoMatch proves the no_match → local-model routing
// wiring end to end. The app has two intents going to DISTINCT terminal states.
// The fake agent.local returns go_west; the harness (if ever reached) would
// return go_south. The input shares no tokens with any intent, so the
// deterministic tiers miss and the LLM tier must fire.
//
// Test rigor: it FAILS without the wiring — without the LLM-tier call the turn
// falls through to the harness and lands in south_end (and the agent is never
// called). The assertions on west_end AND calls==1 both break in that case.
func TestSemanticLLMTier_RoutesOnNoMatch(t *testing.T) {
	// No t.Parallel(): shared machine eventSeq counter (see sibling tests).
	const appYAML = `
app:
  id: semroute-llm-tier-test
  version: 0.1.0

world: {}

routing:
  enabled: true
  extract_llm_on_no_match: true
  extract_llm_agent: agent.local

intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
  go_south:
    title: "Go south"
    examples: ["go south"]

root: start

states:
  start:
    view: "start"
    on:
      go_west:
        - target: west_end
      go_south:
        - target: south_end
  west_end:
    terminal: true
    view: "west"
  south_end:
    terminal: true
    view: "south"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubRoutingAgent{submission: `{"intent":"go_west","confidence":0.95}`}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	// The harness would route to go_south if the turn ever reached the
	// main-turn LLM — so landing in west_end proves the local tier decided.
	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Input shares no tokens with either intent → deterministic no_match.
	out, err := orch.Turn(ctx, sid, "qqzzx wibble frob")
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&stub.calls),
		"the local-model agent must be called exactly once on a deterministic no_match")
	require.Equal(t, app.StatePath("west_end"), out.NewState,
		"routing must follow the local model's go_west verdict, not the harness's go_south")

	// The trace must say WHICH tier/backend routed: routed_by=="llm" and the
	// match_type names the backend plugin (agent.local). This is what tells an
	// operator a local-model route apart from a deterministic synonym or a
	// main-turn claude route.
	var routedBy, matchType string
	for _, e := range out.Events {
		if e.Kind == store.TurnStarted {
			var p map[string]any
			require.NoError(t, json.Unmarshal(e.Payload, &p))
			routedBy, _ = p["routed_by"].(string)
			matchType, _ = p["match_type"].(string)
		}
	}
	require.Equal(t, "llm", routedBy, "TurnStarted must record routed_by=llm for a local-model route")
	require.Equal(t, "agent.local", matchType, "TurnStarted must record the backend plugin name")
}

// TestSemanticLLMTier_NoneVerdictFallsThrough proves a "none" verdict from the
// local model is a miss: the turn falls through to the main-turn LLM (harness)
// rather than fabricating a route. Without that guard a "none" would wrongly
// route or error.
func TestSemanticLLMTier_NoneVerdictFallsThrough(t *testing.T) {
	const appYAML = `
app:
  id: semroute-llm-none-test
  version: 0.1.0
world: {}
routing:
  enabled: true
  extract_llm_on_no_match: true
intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
  go_south:
    title: "Go south"
    examples: ["go south"]
root: start
states:
  start:
    view: "start"
    on:
      go_west:
        - target: west_end
      go_south:
        - target: south_end
  west_end:
    terminal: true
    view: "west"
  south_end:
    terminal: true
    view: "south"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubRoutingAgent{submission: `{"intent":"none","confidence":0.2}`}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "qqzzx wibble frob")
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&stub.calls), "agent still consulted once")
	require.Equal(t, app.StatePath("south_end"), out.NewState,
		"a 'none' verdict must fall through to the main-turn LLM (harness → go_south)")
}

// Slice-1 gate for contextual-room-routing (docs/proposals/contextual-room-routing.md).
//
// A room that opts into contextual_routing must, on a deterministic miss,
// dispatch the contextual router (NOT the main-turn LLM); the verdict schema
// must accept only the four route classes and reject a fifth; load-time
// validation must reject enabling a lane with no backing intent; and a
// recorded/stubbed verdict must replay with no live model call.
//
// Modeled on semantic_llm_routing_test.go — same package, same stub-agent +
// countingHarness/staticHarness rig.
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

// stubContextRouter records calls and returns a fixed context_route verdict.
// The submission JSON is the new contextual-router shape (class-bearing), NOT
// the flat {intent,confidence} of the intent router.
type stubContextRouter struct {
	calls      int32
	submission string
}

func (s *stubContextRouter) Ask(ctx context.Context, req agent.AskRequest) (agent.AskResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	return agent.AskResponse{
		Submission: json.RawMessage(s.submission),
		Meta:       map[string]any{"model": "stub-local", "grammar": true},
	}, nil
}

func (s *stubContextRouter) Close() error { return nil }

// A room that declares contextual_routing.enabled with a room_chat lane bound to
// an on-path intent (go_west). go_south is the harness fallback, reached only if
// the contextual tier never fires.
const ctxRouteAppYAML = `
app:
  id: crr-slice1-test
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
    contextual_routing:
      enabled: true
      room_chat: go_west
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

// 1.2 + 1.4: a contextual-routing room, on a deterministic miss, dispatches the
// contextual router exactly once and routes by the recorded {class:intent}
// verdict — never reaching the main-turn LLM (harness go_south). Proves the new
// final tier fires AND that a stubbed verdict replays with no live model call.
func TestContextualRouter_IntentClassRoutesOnMiss(t *testing.T) {
	def, err := app.LoadBytes([]byte(ctxRouteAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubContextRouter{
		submission: `{"class":"intent","intent":"go_west","confidence":0.95,"reason":"stub"}`,
	}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "qqzzx wibble frob") // deterministic miss
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&stub.calls),
		"contextual router must be dispatched exactly once on a deterministic miss")
	require.Equal(t, int64(0), h.calls.Load(),
		"main-turn LLM (harness) must NOT be reached when the contextual router decides")
	require.Equal(t, app.StatePath("west_end"), out.NewState,
		"the {class:intent,go_west} verdict must advance the machine")

	// 1.4: the route class must be recorded in the trace for replay. Tolerant of
	// the maker's event design: accept the class on any event payload under
	// either "class" or "context_route_class".
	var decidedClass string
	for _, e := range out.Events {
		var p map[string]any
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		if c, ok := p["class"].(string); ok && c == "intent" {
			decidedClass = c
		}
		if c, ok := p["context_route_class"].(string); ok && c == "intent" {
			decidedClass = c
		}
	}
	require.Equal(t, "intent", decidedClass,
		"trace must record the contextual route class for replay")
}

// 1.1: the verdict schema accepts the four classes and rejects a fifth.
func TestContextRouteVerdict_AcceptsFourClassesRejectsFifth(t *testing.T) {
	for _, ok := range []string{"intent", "help", "room_request", "meta_edit"} {
		_, err := orchestrator.ParseContextRouteVerdict(map[string]any{
			"class": ok, "confidence": 0.9,
		})
		require.NoErrorf(t, err, "class %q must be accepted", ok)
	}
	_, err := orchestrator.ParseContextRouteVerdict(map[string]any{
		"class": "delete_everything", "confidence": 0.9,
	})
	require.Error(t, err, "a fifth class must be rejected (no invented classes)")
}

// 1.3: load-time validation fails when contextual_routing is enabled but its
// declared lane has no backing intent (the can't-enable-without-backing
// invariant). Mirrors the default_intent cross-reference validation.
func TestContextualRouting_LoadValidation_RequiresBackingLane(t *testing.T) {
	const badYAML = `
app:
  id: crr-bad
  version: 0.1.0
world: {}
routing:
  enabled: true
intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
root: start
states:
  start:
    view: "start"
    contextual_routing:
      enabled: true
      room_chat: nonexistent_intent
    on:
      go_west:
        - target: west_end
  west_end:
    terminal: true
    view: "west"
`
	_, err := app.LoadBytes([]byte(badYAML))
	require.Error(t, err,
		"enabling a contextual_routing lane with no backing intent must fail load")
}

// Slice-3 gate for contextual-room-routing plan continuation: with a plan
// pending, a free-text AFFIRMATION must deterministically route to the
// plan-accept intent (advance to applying) WITHOUT calling the router LLM, and
// content-bearing follow-up must route to the refine intent. Reuses the slice-1
// rig (stubContextRouter, countingHarness, staticHarness in context_route_test.go).
package orchestrator_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// A room with contextual_routing + a pending plan in world. accept_plan advances
// to applying; work is the refine sink (self-loop, captures request).
const planContinuationAppYAML = `
app:
  id: crr-slice3-test
  version: 0.1.0
world:
  landing_note:
    plan:
      goal: "Migrate issues to GitHub."
      step: { kind: run, description: "create issues", mutating: true }
      verify: { mode: script, reason: "issues exist" }
routing:
  enabled: true
  extract_llm_on_no_match: true
  extract_llm_agent: agent.local
intents:
  accept_plan: { title: "Accept plan", examples: ["accept plan"] }
  work:
    title: "Work"
    examples: ["do some work"]
    slots: { request: { type: string, required: true } }
  go_south: { title: "Go south", examples: ["go south"] }
root: landing
states:
  landing:
    view: "landing"
    default_intent: work
    contextual_routing:
      enabled: true
      room_chat: work
      plan_accept_intent: accept_plan
      plan_refine_intent: work
      pending_plan_path: landing_note.plan
    on:
      accept_plan:
        - when: "len(world.landing_note.plan ?? '') > 0"
          target: applying
      work:
        - target: landing
          effects:
            - set: { landing_request: "{{ slots.request }}" }
      go_south:
        - target: south_end
  applying:
    terminal: true
    view: "applying"
  south_end:
    terminal: true
    view: "south"
`

// 3.2: an affirmation while a plan is pending routes to accept_plan (advances to
// applying) WITHOUT calling the contextual router LLM. The stub is registered but
// must record ZERO calls — the deterministic affirmation guard short-circuits.
func TestPlanContinuation_AffirmationRoutesToAccept(t *testing.T) {
	def, err := app.LoadBytes([]byte(planContinuationAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubContextRouter{
		submission: `{"class":"room_request","confidence":0.9,"reason":"should not be used"}`,
	}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "ok go ahead") // deterministic miss + affirmation
	require.NoError(t, err)

	require.Equal(t, app.StatePath("applying"), out.NewState,
		"an affirmation with a pending plan must route to accept_plan → applying")
	require.Equal(t, int32(0), atomic.LoadInt32(&stub.calls),
		"the affirmation guard is deterministic — the router LLM must NOT be called")
	require.Equal(t, int64(0), h.calls.Load(),
		"the main-turn LLM must NOT be reached")
}

// 3.2: a content-bearing follow-up while a plan is pending routes to the refine
// intent (work), NOT accept — it self-loops landing and captures the request.
func TestPlanContinuation_ContentRoutesToRefine(t *testing.T) {
	def, err := app.LoadBytes([]byte(planContinuationAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubContextRouter{submission: `{"class":"room_request","confidence":0.9}`}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)
	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "dry-run first, don't write anything yet")
	require.NoError(t, err)

	require.Equal(t, app.StatePath("landing"), out.NewState,
		"a content follow-up with a pending plan must route to work (refine), self-looping landing")
	// The refine path captured the utterance into the request slot.
	j, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "dry-run first, don't write anything yet", j.World.Get("landing_request"),
		"the refine route must capture the follow-up as the work request slot")
}

// 3.1 unit: the affirmation lexicon recognises affirmations and rejects content.
func TestIsAffirmation(t *testing.T) {
	for _, yes := range []string{"ok", "ok go ahead", "do it", "apply it", "yes", "proceed", "LGTM"} {
		require.Truef(t, orchestrator.IsAffirmation(yes), "%q must be an affirmation", yes)
	}
	for _, no := range []string{"ok but skip closed issues", "dry-run first", "change the verify gate", ""} {
		require.Falsef(t, orchestrator.IsAffirmation(no), "%q must NOT be an affirmation", no)
	}
}

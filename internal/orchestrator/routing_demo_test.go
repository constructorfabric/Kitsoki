// End-to-end routing test for the stories/routing-demo teaching story, driven
// through orchestrator.Turn — the SAME path the TUI uses (deterministic →
// semantic → local-LLM → main-turn LLM). Note: the stateless `kitsoki turn
// --input` probe uses OneShot, which routes straight through the harness and
// does NOT exercise these tiers — so the router is tested here, not via that CLI.
//
// The harness is a sentinel (`look`, a harmless no-op valid in every room): if
// the router ever MISSES and falls through to the main-turn LLM, the turn lands
// on `look` and the tier assertions fail. The fake oracle.local returns a
// verdict the deterministic tier would NOT produce, so a local-tier pass proves
// the model decided rather than a synonym.
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/oracle"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestRoutingDemo_AllTiers(t *testing.T) {
	// No t.Parallel(): shared machine eventSeq counter (see sibling tests).
	def, err := app.Load("../../stories/routing-demo/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Fake local model: always answers read_email. The deterministic tier could
	// never produce that for an umbrella query, so routing to read_email proves
	// the local tier (oracle.local) decided.
	reg := oracle.NewRegistry()
	reg.Register("oracle.local", &stubRoutingOracle{submission: `{"intent":"read_email","confidence":0.95}`})

	// Sentinel harness: a router miss falls through to `look` (no-op), which
	// changes neither last_action nor the room — so any missed tier fails below.
	h := &staticHarness{intentName: "look"}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithOracleRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	route := func(input string) *orchestrator.TurnOutcome {
		t.Helper()
		out, err := orch.Turn(ctx, sid, input)
		require.NoError(t, err)
		return out
	}
	world := func() map[string]any {
		t.Helper()
		j, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		return j.World.Vars
	}
	state := func() app.StatePath {
		t.Helper()
		j, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		return j.State
	}

	// ── navigation routes deterministically (an exact example) ──
	route("deterministic")
	require.Equal(t, app.StatePath("deterministic"), state(), "nav 'deterministic' should route via the example tier")

	// ── TIER 1: a bare synonym ──
	route("illuminate")
	require.Equal(t, "lamp_on", world()["last_action"], "'illuminate' is a bare synonym for lamp_on (deterministic)")

	// ── TIER 2: a slot_template fills the amount slot ──
	route("budget 350")
	require.Equal(t, "350", world()["budget"], "'budget 350' matches 'budget {amount}' and fills amount=350")
	require.Equal(t, "set_budget", world()["last_action"])

	route("back")
	require.Equal(t, app.StatePath("hub"), state())

	// ── TIER 4: a paraphrase misses deterministic → oracle.local decides ──
	route("local model")
	require.Equal(t, app.StatePath("local_model"), state())
	route("should I bring an umbrella today")
	require.Equal(t, "read_email (via local model)", world()["last_action"],
		"a paraphrase must be routed by oracle.local (which we stubbed to read_email), not by a synonym")

	route("back")
	require.Equal(t, app.StatePath("hub"), state())

	// ── TIER 3: a generic 'save {x}' ties across save_doc + save_game ──
	route("ambiguous")
	require.Equal(t, app.StatePath("ambiguous"), state())
	out := route("save report")
	require.Equal(t, orchestrator.ModeRejected, out.Mode, "a tie must NOT silently route — it rejects with a disambiguation")
	require.Equal(t, "AMBIGUOUS_INTENT", string(out.ErrorCode))
}

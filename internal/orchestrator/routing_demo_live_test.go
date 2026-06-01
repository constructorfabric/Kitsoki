// Live routing proof for stories/routing-demo: drives a real paraphrase through
// orchestrator.Turn with the REAL oracle.local (llama.cpp) wired from the app's
// oracle_plugins — no fake. It confirms the no_match → local-model tier actually
// fires end to end (the thing the stateless `turn --input` probe can NOT show,
// since OneShot bypasses the router).
//
// Gated behind KITSOKI_LLM_E2E=1 (downloads/needs the model); never in the
// default suite. The harness is a sentinel ("look"): if the local tier fails to
// route, the turn falls through to it and last_action stays unset, failing the
// assertion — so a pass proves oracle.local did the routing.
package orchestrator_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/oracle"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestRoutingDemo_LiveLocalModel(t *testing.T) {
	if os.Getenv("KITSOKI_LLM_E2E") != "1" {
		t.Skip("set KITSOKI_LLM_E2E=1 to route a real paraphrase through oracle.local (needs the local model)")
	}
	if os.Getenv("KITSOKI_CACHE_DIR") == "" {
		t.Setenv("KITSOKI_CACHE_DIR", os.Getenv("HOME")+"/.cache/kitsoki")
	}

	def, err := app.Load("../../stories/routing-demo/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Sentinel main-turn harness: a router miss falls through to a harmless
	// "look", which never sets last_action — so reaching lamp_on proves the
	// local tier (oracle.local) routed, not the harness.
	sentinel := &staticHarness{intentName: "look"}
	reg, err := oracle.BuildRegistryFromDef(def, sentinel)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })

	orch := orchestrator.New(def, m, s, sentinel, orchestrator.WithOracleRegistry(reg))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Navigate to the deterministic room (exact example → deterministic tier).
	_, err = orch.Turn(ctx, sid, "deterministic")
	require.NoError(t, err)

	// A paraphrase with no example/synonym overlap → matcher returns 0.00
	// (no_match) → the local model classifies it among the room's intents.
	_, err = orch.Turn(ctx, sid, "could you brighten this place up")
	require.NoError(t, err)

	j, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	la, _ := j.World.Vars["last_action"].(string)
	t.Logf("paraphrase routed to last_action=%q", la)
	require.Equal(t, "lamp_on", la,
		"the local model should route 'brighten this place up' to lamp_on; if it stayed unset the turn fell through to the sentinel harness")
}

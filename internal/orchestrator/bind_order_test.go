package orchestrator_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestBindEmissionOrderIsDeterministic guards the determinism fix in
// dispatchHostCalls: when a single host invocation declares more than one
// `bind:`, the per-binding EffectApplied events must be emitted in a stable
// (sorted-by-world-key) order rather than raw Go map-iteration order.
//
// Go randomizes map iteration, so iterating `hc.Bind` directly would emit
// these events in a different order on different live runs with identical
// host results — a non-deterministic trace, and a latent value bug for a
// template `bind:` that reads a sibling key bound in the same block. The fix
// iterates `slices.Sorted(maps.Keys(hc.Bind))`.
//
// The test runs the same dispatch across many fresh sessions and asserts the
// order is sorted every time. Pre-fix, a 6-key bind has only a ~1/720 chance
// of landing sorted by luck, so this fails with overwhelming probability;
// post-fix it always passes.
func TestBindEmissionOrderIsDeterministic(t *testing.T) {
	// World keys chosen so their sorted order differs from the order they
	// are written in the YAML/map literal below.
	bind := map[string]string{
		"w_charlie": "f3",
		"w_alpha":   "f1",
		"w_echo":    "f5",
		"w_bravo":   "f2",
		"w_delta":   "f4",
		"w_foxtrot": "f6",
	}
	wantOrder := slices.Sorted(maps.Keys(bind)) // w_alpha, w_bravo, ... w_foxtrot

	worldDefs := map[string]app.VarDef{}
	for wkey := range bind {
		worldDefs[wkey] = app.VarDef{Type: "string", Default: ""}
	}

	def := &app.AppDef{
		App:   app.AppMeta{ID: "bind-order-test"},
		Root:  "init",
		Hosts: []string{"host.test.multi"},
		World: worldDefs,
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"done":  {Title: "Done"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On:   map[string][]app.Transition{"enter": {{Target: "room"}}},
			},
			"room": {
				View: app.LegacyView("room"),
				OnEnter: []app.Effect{
					{
						Invoke: "host.test.multi",
						Bind:   bind,
					},
				},
				On: map[string][]app.Transition{"done": {{Target: "end"}}},
			},
			"end": {Terminal: true, View: app.LegacyView("ended")},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	// Synchronous handler returning a Data field per bound result key.
	reg := host.NewRegistry()
	reg.Register("host.test.multi", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"f1": "1", "f2": "2", "f3": "3", "f4": "4", "f5": "5", "f6": "6",
		}}, nil
	})

	ctx := context.Background()

	// Repeat across fresh sessions so Go's per-range map randomization gets
	// many independent draws; the fix must hold on every one.
	const runs = 25
	for i := 0; i < runs; i++ {
		s, err := store.OpenMemory()
		require.NoError(t, err)

		orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"},
			orchestrator.WithHostRegistry(reg))

		sid, err := orch.NewSession(ctx)
		require.NoError(t, err)

		out, err := orch.Turn(ctx, sid, "enter")
		require.NoError(t, err)
		require.Equal(t, app.StatePath("room"), out.NewState)

		history, err := s.LoadHistory(sid)
		require.NoError(t, err)

		var gotOrder []string
		for _, ev := range history {
			if ev.Kind != store.EffectApplied {
				continue
			}
			var payload struct {
				Set map[string]any `json:"set"`
			}
			if json.Unmarshal(ev.Payload, &payload) != nil {
				continue
			}
			for k := range payload.Set {
				if _, ok := bind[k]; ok {
					gotOrder = append(gotOrder, k)
				}
			}
		}

		require.Equal(t, wantOrder, gotOrder,
			"run %d: bind EffectApplied events must be emitted sorted by world key", i)

		_ = s.Close()
	}
}

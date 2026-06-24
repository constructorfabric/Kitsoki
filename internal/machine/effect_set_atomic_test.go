package machine_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// TestSetBlockReadsPreBlockSnapshot is the deterministic gate for the
// intra-`set:` map-ordering flake (see .context/effect-ordering-flake-rootcause.md).
//
// A single `set:` block both READS world.note.summary (into prior_summary) and
// CLEARS world.note in the same map. The keys of one `set:` are a Go map, so the
// engine iterates them in randomized order; if the engine renders each key
// against the progressively-mutated world, the preserve template sometimes runs
// AFTER the clear and reads empty. A single `set:` block must be atomic: every
// key renders against the SAME pre-block snapshot.
//
// The 200-iteration loop defeats Go's randomized map iteration: pre-fix this
// fails within a handful of iterations; post-fix it is invariant.
func TestSetBlockReadsPreBlockSnapshot(t *testing.T) {
	def := &app.AppDef{
		App:     app.AppMeta{ID: "set-atomic-test"},
		Root:    "s",
		Intents: map[string]app.Intent{},
		World: map[string]app.VarDef{
			"note":          {Type: "object"},
			"prior_summary": {Type: "string", Default: ""},
			"prior_details": {Type: "string", Default: ""},
		},
		States: map[string]*app.State{
			"s": {View: app.LegacyView("state s")},
		},
	}
	m := mustNew(t, def)

	// One set: block that reads the note AND clears it in the same map.
	effects := []app.Effect{
		{Set: map[string]any{
			"prior_summary": "{{ world.note.summary ?? '' }}",
			"prior_details": "{{ world.note.details ?? '' }}",
			"note":          map[string]any{}, // clears the note read above
		}},
	}

	for i := 0; i < 200; i++ {
		w := world.New()
		w.Vars["note"] = map[string]any{"summary": "S", "details": "D"}
		w.Vars["prior_summary"] = ""
		w.Vars["prior_details"] = ""

		nw, _, _, _, err := m.RunEffects(context.Background(), "s", w, effects)
		require.NoError(t, err)
		require.Equal(t, "S", nw.Vars["prior_summary"],
			"iter %d: a single set: block must read the pre-block snapshot, not the cleared note", i)
		require.Equal(t, "D", nw.Vars["prior_details"],
			"iter %d: a single set: block must read the pre-block snapshot, not the cleared note", i)
	}
}

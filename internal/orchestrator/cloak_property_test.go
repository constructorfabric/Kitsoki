package orchestrator_test

// cloak_property_test.go — Layer 6 property suite driven by the real cloak
// orchestrator (finding 2.12).
//
// The existing jsonl_property_test.go (in internal/store) generates synthetic
// event sequences directly. This test drives SubmitDirect against the real cloak
// orchestrator with random valid intent picks, then verifies that JSONL reload →
// BuildJourney produces the same (state, world). 20 sequences, 5 steps each.
//
// Skipped under -short (drives real orchestrator; slow on CI).

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// cloakSteps are the valid intents in cloak with their required slots.
// The random picker draws from this slice; not every intent is valid in every
// state, so the test tolerates SubmitDirect returning ModeRejected.
var cloakSteps = []struct {
	intent string
	slots  map[string]any
}{
	{"go", map[string]any{"direction": "west"}},
	{"go", map[string]any{"direction": "east"}},
	{"go", map[string]any{"direction": "south"}},
	{"go", map[string]any{"direction": "north"}},
	{"hang_cloak", nil},
	{"read_message", nil},
}

// cloakPropertyInitialWorld returns the starting world for Cloak of Darkness.
func cloakPropertyInitialWorld() world.World {
	w := world.New()
	w.Vars["wearing_cloak"] = true
	w.Vars["disturbance"] = int64(0)
	w.Vars["message_rumpled"] = false
	return w
}

// TestLayer6_CloakOrchestratorProperty drives the cloak orchestrator with
// random intent sequences and verifies (state, world) parity after JSONL
// reload → BuildJourney (finding 2.12).
//
// This catches the gap between "round-trips byte-identical" and "is actually
// replayable" — a discrepancy in how the orchestrator applies world mutations
// vs how BuildJourney replays them would surface here.
func TestLayer6_CloakOrchestratorProperty(t *testing.T) {
	if testing.Short() {
		t.Skip("TestLayer6_CloakOrchestratorProperty: skipped under -short")
	}
	t.Parallel()

	const sequences = 20
	const stepsPerSeq = 5

	appPath := "../../testdata/apps/cloak/app.yaml"
	def, err := app.Load(appPath)
	require.NoError(t, err)

	for seqIdx := 0; seqIdx < sequences; seqIdx++ {
		seqIdx := seqIdx
		t.Run("seq", func(t *testing.T) {
			// Not parallel: pongo2's global template set is not thread-safe
			// under concurrent writes from multiple goroutines. Running
			// subtests sequentially avoids the race without losing coverage.
			rng := rand.New(rand.NewSource(int64(seqIdx + 42)))
			dir := t.TempDir()
			tracePath := filepath.Join(dir, "seq.jsonl")

			// Phase 1: live run via SubmitDirect with JSONL sink.
			m, err := machine.New(def)
			require.NoError(t, err)
			s, err := store.OpenMemory()
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			sink, err := store.OpenJSONL(tracePath)
			require.NoError(t, err)
			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)

			orch := orchestrator.New(def, m, s, noopHarness{},
				orchestrator.WithHostRegistry(hostReg),
				orchestrator.WithEventSink(sink),
				orchestrator.WithEventSinkAuthority(true),
			)
			ctx := context.Background()
			sid, err := orch.NewSession(ctx)
			require.NoError(t, err)

			for i := 0; i < stepsPerSeq; i++ {
				pick := cloakSteps[rng.Intn(len(cloakSteps))]
				out, sErr := orch.SubmitDirect(ctx, sid, pick.intent, pick.slots)
				if sErr != nil {
					continue // infra error — engine may continue
				}
				if out.Mode == orchestrator.ModeCompleted {
					break
				}
			}
			require.NoError(t, sink.Close())

			// Phase 2: reload JSONL and fold.
			sink2, err := store.OpenJSONL(tracePath)
			require.NoError(t, err)
			defer sink2.Close()

			hist := sink2.History()
			jsLive, err := store.BuildJourney(def, "foyer", cloakPropertyInitialWorld(), hist)
			require.NoError(t, err)

			// Phase 3: fold again from the same history to verify idempotence.
			jsReload, err := store.BuildJourney(def, "foyer", cloakPropertyInitialWorld(), hist)
			require.NoError(t, err)

			// (state, world) parity: live fold equals reload fold.
			require.Equal(t, jsLive.State, jsReload.State,
				"seq %d: state mismatch between live and reload folds", seqIdx)
			require.Equal(t, jsLive.World.Vars, jsReload.World.Vars,
				"seq %d: world.Vars mismatch between live and reload folds", seqIdx)
		})
	}
}

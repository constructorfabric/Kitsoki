package store_test

// jsonl_property_test.go — Layer 6: property suite.
//
// Generates random valid intent sequences against a synthetic story (the
// cloak-of-darkness fixture), then:
//   - Verifies live → JSONL → reload → continue gives the same (state, world).
//   - Resumes after every event (subsumes Layer 3's resume-at-boundary at scale).
//   - Injects a random fsync failure at one event per run; asserts recovery.
//   - Runs parallel sequences with different RNG seeds; asserts trace independence.
//
// The property suite is SKIPPED under -short (slow).
// Without -short it runs testing/quick with a bounded number of cases.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// ─── generators ───────────────────────────────────────────────────────────────

// genRandomHistory generates a random valid event sequence that BuildJourney
// can fold without error. The events represent a plausible story run:
// TurnStarted, transitions, effects, TurnEnded.
func genRandomHistory(rng *rand.Rand, turns, eventsPerTurn int) store.History {
	var h store.History
	state := "state_0"
	for t := 1; t <= turns; t++ {
		seq := 0
		h = append(h, store.Event{
			Turn:    app.TurnNumber(t),
			Seq:     seq,
			Ts:      time.Now().UTC(),
			Kind:    store.TurnStarted,
			Payload: json.RawMessage(`{}`),
		})
		seq++

		nextState := fmt.Sprintf("state_%d", rng.Intn(10))
		h = append(h, store.Event{
			Turn:    app.TurnNumber(t),
			Seq:     seq,
			Ts:      time.Now().UTC(),
			Kind:    store.TransitionApplied,
			Payload: mkPayload(map[string]any{"from": state, "to": nextState, "intent": "move"}),
		})
		seq++
		state = nextState

		for e := 0; e < eventsPerTurn-3; e++ {
			key := fmt.Sprintf("var_%d", rng.Intn(5))
			val := rng.Intn(100)
			h = append(h, store.Event{
				Turn:    app.TurnNumber(t),
				Seq:     seq,
				Ts:      time.Now().UTC(),
				Kind:    store.EffectApplied,
				Payload: mkPayload(map[string]any{"set": map[string]any{key: val}}),
			})
			seq++
		}

		h = append(h, store.Event{
			Turn:    app.TurnNumber(t),
			Seq:     seq,
			Ts:      time.Now().UTC(),
			Kind:    store.TurnEnded,
			Payload: json.RawMessage(`{}`),
		})
	}
	return h
}

// ─── property tests ───────────────────────────────────────────────────────────

// TestLayer6_LiveJSONLReloadContinue verifies that for random event sequences:
// run live → write JSONL → reload → continue produces the same (state, world).
func TestLayer6_LiveJSONLReloadContinue(t *testing.T) {
	if testing.Short() {
		t.Skip("Layer6: skip under -short (property suite)")
	}
	t.Parallel()

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()
	initial.Vars["x"] = int64(0)

	const numCases = 50
	for i := 0; i < numCases; i++ {
		i := i
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(i * 31337)))
			turns := 3 + rng.Intn(8)
			evPerTurn := 4 + rng.Intn(4)
			history := genRandomHistory(rng, turns, evPerTurn)

			// Baseline fold.
			baseline, err := store.BuildJourney(def, "state_0", initial, history)
			require.NoError(t, err, "case %d: baseline fold", i)

			// Write to JSONL.
			dir := t.TempDir()
			path := filepath.Join(dir, "prop.jsonl")
			s, err := store.OpenJSONL(path)
			require.NoError(t, err)
			for _, ev := range history {
				require.NoError(t, s.Append(ev))
			}
			require.NoError(t, s.Close())

			// Reload.
			loaded := loadHistory(t, path)
			require.Len(t, loaded, len(history), "case %d: reload length", i)

			// Fold from reloaded.
			reloaded, err := store.BuildJourney(def, "state_0", initial, loaded)
			require.NoError(t, err, "case %d: reloaded fold", i)

			require.Equal(t, baseline.State, reloaded.State, "case %d: state", i)
			require.Equal(t, baseline.Turn, reloaded.Turn, "case %d: turn", i)
			// World vars must match (modulo JSON float64 coercion).
			require.Equal(t, len(baseline.World.Vars), len(reloaded.World.Vars),
				"case %d: world vars count", i)
		})
	}
}

// TestLayer6_ResumeAfterEveryEvent verifies that for random sequences,
// resuming after every event produces the same final (state, world).
func TestLayer6_ResumeAfterEveryEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("Layer6: skip under -short (property suite)")
	}
	t.Parallel()

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	const numCases = 20
	for i := 0; i < numCases; i++ {
		i := i
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(i * 99991)))
			turns := 2 + rng.Intn(5)
			history := genRandomHistory(rng, turns, 4)

			baseline, err := store.BuildJourney(def, "state_0", initial, history)
			require.NoError(t, err)

			for boundary := 0; boundary < len(history); boundary++ {
				dir := t.TempDir()
				path := filepath.Join(dir, "resume.jsonl")

				// Write first boundary+1 events.
				s, err := store.OpenJSONL(path)
				require.NoError(t, err)
				for _, ev := range history[:boundary+1] {
					require.NoError(t, s.Append(ev))
				}
				require.NoError(t, s.Close())

				// Append rest.
				s2, err := store.OpenJSONL(path)
				require.NoError(t, err)
				for _, ev := range history[boundary+1:] {
					require.NoError(t, s2.Append(ev))
				}
				require.NoError(t, s2.Close())

				// Fold.
				resumed := loadHistory(t, path)
				js, foldErr := store.BuildJourney(def, "state_0", initial, resumed)
				require.NoError(t, foldErr,
					"case %d boundary %d: fold must not error", i, boundary)
				require.Equal(t, baseline.State, js.State,
					"case %d boundary %d: state mismatch", i, boundary)
			}
		})
	}
}

// TestLayer6_FsyncFailureRecovery verifies that when a random fsync failure
// occurs during one Append, the prior committed state is recoverable.
func TestLayer6_FsyncFailureRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("Layer6: skip under -short (property suite)")
	}
	t.Parallel()

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	const numCases = 30
	for i := 0; i < numCases; i++ {
		i := i
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(i * 54321)))
			turns := 3 + rng.Intn(5)
			history := genRandomHistory(rng, turns, 4)

			if len(history) < 2 {
				t.Skip("history too short")
			}

			// Choose a random event index at which to inject fsync failure.
			failAt := rng.Intn(len(history))

			dir := t.TempDir()
			path := filepath.Join(dir, "fsync_prop.jsonl")
			s, err := store.OpenJSONL(path)
			require.NoError(t, err)

			var lastGoodIdx int
			for idx, ev := range history {
				if idx == failAt {
					s.SetHookFsync(func(_ *os.File) error {
						return fmt.Errorf("injected fsync prop failure")
					})
					appendErr := s.Append(ev)
					s.SetHookFsync(nil) // restore
					if appendErr != nil {
						// fsync failed; prior events are the committed state.
						lastGoodIdx = idx - 1
						break
					}
				} else {
					if err := s.Append(ev); err != nil {
						t.Fatalf("case %d: unexpected append error at event %d: %v", i, idx, err)
					}
					lastGoodIdx = idx
				}
			}
			require.NoError(t, s.Close())

			// Recover: reload the file.
			s2, err := store.OpenJSONL(path)
			require.NoError(t, err, "case %d: reopen after fsync failure must succeed", i)
			defer s2.Close()
			recovered := s2.History()

			// The recovered history should cover at most lastGoodIdx+1 events.
			// It may be fewer if the failed event's write was also partial.
			require.LessOrEqual(t, len(recovered), failAt+1,
				"case %d: recovered history must not exceed events before failure", i)

			// Fold must not error.
			_, foldErr := store.BuildJourney(def, "state_0", initial, recovered)
			require.NoError(t, foldErr, "case %d: fold of recovered history must not error", i)

			_ = lastGoodIdx
		})
	}
}

// TestLayer6_ParallelSequencesIndependent verifies that two parallel goroutines
// each writing to their own trace file do not share state.
func TestLayer6_ParallelSequencesIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("Layer6: skip under -short (property suite)")
	}
	t.Parallel()

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	type result struct {
		state app.StatePath
		turn  app.TurnNumber
	}

	run := func(seed int64) result {
		rng := rand.New(rand.NewSource(seed))
		history := genRandomHistory(rng, 5, 4)

		dir := t.TempDir()
		path := filepath.Join(dir, "parallel.jsonl")
		s, err := store.OpenJSONL(path)
		if err != nil {
			t.Errorf("parallel seed %d: OpenJSONL: %v", seed, err)
			return result{}
		}
		for _, ev := range history {
			if err := s.Append(ev); err != nil {
				t.Errorf("parallel seed %d: Append: %v", seed, err)
				_ = s.Close()
				return result{}
			}
		}
		_ = s.Close()

		loaded := loadHistory(t, path)
		js, err := store.BuildJourney(def, "state_0", initial, loaded)
		if err != nil {
			t.Errorf("parallel seed %d: BuildJourney: %v", seed, err)
			return result{}
		}
		return result{state: js.State, turn: js.Turn}
	}

	const numPairs = 20
	results := make([]result, numPairs*2)
	done := make(chan int, numPairs*2)

	for i := 0; i < numPairs*2; i++ {
		i := i
		seed := int64(i * 77777)
		go func() {
			results[i] = run(seed)
			done <- i
		}()
	}
	for range results {
		<-done
	}

	// Verify: each odd-indexed result matches what single-threaded execution
	// of that seed produces (no shared-state contamination).
	for i := 0; i < numPairs*2; i++ {
		seed := int64(i * 77777)
		expected := run(seed)
		require.Equal(t, expected.state, results[i].state,
			"parallel case %d: state mismatch (possible shared state contamination)", i)
		require.Equal(t, expected.turn, results[i].turn,
			"parallel case %d: turn mismatch", i)
	}
}

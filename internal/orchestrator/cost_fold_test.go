package orchestrator

import (
	"encoding/json"
	"testing"

	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// effectSet extracts the {key: value} written by a single-key EffectApplied
// event, so a test can assert the reserved cost vars are journaled.
func effectSet(t *testing.T, ev store.Event) map[string]any {
	t.Helper()
	if ev.Kind != store.EffectApplied {
		t.Fatalf("event kind = %q, want EffectApplied", ev.Kind)
	}
	var p struct {
		Set map[string]any `json:"set"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return p.Set
}

func TestFoldAgentCost(t *testing.T) {
	t.Run("accumulates session and sets turn, journaling both", func(t *testing.T) {
		w := world.New()
		w.Vars["session_cost_usd"] = 0.0
		w.Vars["turn_cost_usd"] = 0.0

		evs := foldAgentCost(&w, 0.02)

		if got := w.Vars["turn_cost_usd"].(float64); got != 0.02 {
			t.Errorf("turn_cost_usd = %v, want 0.02", got)
		}
		if got := w.Vars["session_cost_usd"].(float64); got != 0.02 {
			t.Errorf("session_cost_usd = %v, want 0.02", got)
		}
		if len(evs) != 2 {
			t.Fatalf("emitted %d events, want 2 (turn + session)", len(evs))
		}
		if v := effectSet(t, evs[0])["turn_cost_usd"]; v != 0.02 {
			t.Errorf("turn event value = %v, want 0.02", v)
		}
		if v := effectSet(t, evs[1])["session_cost_usd"]; v != 0.02 {
			t.Errorf("session event value = %v, want 0.02", v)
		}
	})

	t.Run("second batch adds to session", func(t *testing.T) {
		w := world.New()
		w.Vars["session_cost_usd"] = 0.02
		w.Vars["turn_cost_usd"] = 0.02

		foldAgentCost(&w, 0.05)

		if got := w.Vars["session_cost_usd"].(float64); got != 0.07 {
			t.Errorf("session_cost_usd = %v, want 0.07 (0.02 + 0.05)", got)
		}
		if got := w.Vars["turn_cost_usd"].(float64); got != 0.05 {
			t.Errorf("turn_cost_usd = %v, want 0.05 (latest batch)", got)
		}
	})

	t.Run("zero-cost batch resets turn but leaves session, no session event", func(t *testing.T) {
		w := world.New()
		w.Vars["session_cost_usd"] = 0.07
		w.Vars["turn_cost_usd"] = 0.05

		evs := foldAgentCost(&w, 0)

		if got := w.Vars["turn_cost_usd"].(float64); got != 0 {
			t.Errorf("turn_cost_usd = %v, want 0 (reset on host.run-only batch)", got)
		}
		if got := w.Vars["session_cost_usd"].(float64); got != 0.07 {
			t.Errorf("session_cost_usd = %v, want 0.07 (unchanged)", got)
		}
		// Only the turn reset is journaled; session is untouched.
		if len(evs) != 1 {
			t.Fatalf("emitted %d events, want 1 (turn reset only)", len(evs))
		}
		if v := effectSet(t, evs[0])["turn_cost_usd"]; v != 0.0 {
			t.Errorf("turn reset value = %v, want 0", v)
		}
	})

	t.Run("no-op batch emits nothing", func(t *testing.T) {
		w := world.New()
		w.Vars["session_cost_usd"] = 0.07
		w.Vars["turn_cost_usd"] = 0.0

		if evs := foldAgentCost(&w, 0); len(evs) != 0 {
			t.Fatalf("emitted %d events, want 0 (turn already 0, no spend)", len(evs))
		}
	})
}

func TestWorldFloat(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want float64
	}{
		{"float64", float64(1.5), 1.5},
		{"float32", float32(2), 2},
		{"int default", int(3), 3},
		{"int64 replay", int64(4), 4},
		{"missing", nil, 0},
		{"non-numeric", "x", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worldFloat(tc.in); got != tc.want {
				t.Errorf("worldFloat(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

package app

import "testing"

func TestReadsWorldKeyInGuard(t *testing.T) {
	mkState := func(s *State) *AppDef {
		return &AppDef{States: map[string]*State{"room": s}}
	}

	t.Run("declared but never read returns false", func(t *testing.T) {
		// Mirrors stories/bugfix: the world key exists but no guard reads it.
		d := &AppDef{
			World: map[string]VarDef{"allowed_authors": {}},
			States: map[string]*State{
				"room": {On: map[string][]Transition{
					"accept": {{Target: "next", When: "slots.author != ''"}},
				}},
			},
		}
		if d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("declared-only key must not count as a guard")
		}
	})

	t.Run("transition when guard", func(t *testing.T) {
		d := mkState(&State{On: map[string][]Transition{
			"accept": {{Target: "next", When: "slots.author in world.allowed_authors"}},
		}})
		if !d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("transition guard reading the key must count")
		}
	})

	t.Run("effect when guard", func(t *testing.T) {
		d := mkState(&State{OnEnter: []Effect{
			{When: "world.allowed_authors != ''", Say: "x"},
		}})
		if !d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("effect guard reading the key must count")
		}
	})

	t.Run("slot validator", func(t *testing.T) {
		d := mkState(&State{Intents: map[string]Intent{
			"accept": {Slots: map[string]Slot{
				"author": {Type: "string", Validator: "author in world.allowed_authors"},
			}},
		}})
		if !d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("slot validator reading the key must count")
		}
	})

	t.Run("nested child state", func(t *testing.T) {
		d := mkState(&State{States: map[string]*State{
			"child": {On: map[string][]Transition{
				"go": {{Target: "x", When: "world.allowed_authors"}},
			}},
		}})
		if !d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("guard in a nested child state must count")
		}
	})

	t.Run("identifier boundary — no substring false-positive", func(t *testing.T) {
		d := mkState(&State{On: map[string][]Transition{
			"accept": {{Target: "next", When: "world.allowed_authors_extra != ''"}},
		}})
		if d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("must not match a longer identifier that merely contains the key")
		}
	})

	t.Run("nil and empty", func(t *testing.T) {
		var d *AppDef
		if d.ReadsWorldKeyInGuard("allowed_authors") {
			t.Fatal("nil AppDef must be false")
		}
		if (&AppDef{}).ReadsWorldKeyInGuard("") {
			t.Fatal("empty key must be false")
		}
	})
}

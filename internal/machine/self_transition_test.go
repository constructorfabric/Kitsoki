// self_transition_test.go — covers the "explicit self-transition
// fires on_enter" semantics that the bugfix story's `refine:` arcs
// depend on. See machine.go:752 for the rule:
//
//   target: <name>  where <name> == cur  → fires on_enter
//   target: .       (stay-here idiom)    → does NOT fire on_enter
//
// Pre-2026-05-20 the check was just `resolvedTarget != cur` which
// silently swallowed both cases — the bugfix `refine:` arcs hit it.
// The user reported "the refinement came back immediately and didn't
// change the artifact text" because the agent in the refined state's
// on_enter wasn't re-invoked.

package machine_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/world"
)

// TestSelfTransition_ExplicitTargetFiresOnEnter is the positive
// regression guard: `target: <name>` where name == cur fires the
// state's on_enter chain. We assert by detecting that an on_enter
// `say` effect appended its text — the cheapest observable side
// effect that doesn't depend on world-var typing.
func TestSelfTransition_ExplicitTargetFiresOnEnter(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "self-explicit"},
		Root: "room",
		Intents: map[string]app.Intent{
			"refine": {},
		},
		States: map[string]*app.State{
			"room": {
				View: app.LegacyView("the room view"),
				OnEnter: []app.Effect{
					{Say: "on_enter fired"},
				},
				On: map[string][]app.Transition{
					"refine": {{Target: "room"}}, // explicit self-name
				},
			},
		},
	}

	m := mustNew(t, def)

	res, err := m.Turn(context.Background(), "room", world.New(), intent.IntentCall{Intent: "refine"})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("room"), res.NewState, "stays in room")

	// The View is built from the room's view template prepended with
	// any `say:` text accumulated during the transition. A successful
	// on_enter re-fire leaves "on_enter fired" in the rendered view.
	require.Contains(t, res.View, "on_enter fired",
		"explicit-self target must fire on_enter; view = %q", res.View)
}

// TestSelfTransition_DotTargetSkipsOnEnter is the negative regression
// guard: `target: .` (the stay-here idiom) MUST NOT fire on_enter.
// Otherwise a `look:` arc would inadvertently re-run heavy on_enter
// chains (e.g. an agent invoke) every time the user re-rendered.
func TestSelfTransition_DotTargetSkipsOnEnter(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "self-dot"},
		Root: "room",
		Intents: map[string]app.Intent{
			"look": {},
		},
		States: map[string]*app.State{
			"room": {
				View: app.LegacyView("the room view"),
				OnEnter: []app.Effect{
					{Say: "on_enter fired"},
				},
				On: map[string][]app.Transition{
					"look": {{Target: "."}}, // stay-here idiom
				},
			},
		},
	}

	m := mustNew(t, def)

	res, err := m.Turn(context.Background(), "room", world.New(), intent.IntentCall{Intent: "look"})
	require.NoError(t, err)
	require.Nil(t, res.ValidationError)
	require.Equal(t, app.StatePath("room"), res.NewState)

	require.NotContains(t, res.View, "on_enter fired",
		"target: . must NOT fire on_enter; view = %q", res.View)
}

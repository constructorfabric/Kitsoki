package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

// TestResolveIntentAlias_WalksAncestorStates verifies that default intent
// alias resolution works when the authored bare name was rewritten on an
// ancestor state, not the leaf state. This is the import-fold shape where only
// the ancestor carries the alias map entry.
func TestResolveIntentAlias_WalksAncestorStates(t *testing.T) {
	t.Parallel()

	root := &app.State{
		IntentAliases: map[string]string{"talk": "wrapped__talk"},
		States: map[string]*app.State{
			"inner": {
				DefaultIntent: "talk",
				On: map[string][]app.Transition{
					"wrapped__talk": nil,
				},
			},
		},
	}

	def := &app.AppDef{
		App:  app.AppMeta{ID: "ancestor-intent-alias", Version: "0.1.0"},
		Root: "outer.inner",
		States: map[string]*app.State{
			"outer": root,
		},
		Intents: map[string]app.Intent{
			"wrapped__talk": {
				Slots: map[string]app.Slot{
					"message": {Type: "string", Required: true},
				},
			},
		},
	}

	st := lookupStateByPath(def, app.StatePath("outer.inner"))
	resolved := resolveIntentAlias(def, app.StatePath("outer.inner"), st, "talk")
	require.Equal(t, "wrapped__talk", resolved, "ancestor alias should resolve through state chain")

	allowed := []string{"wrapped__talk", "talk"}
	require.Equal(t, "wrapped__talk", resolveDefaultIntentName(def, app.StatePath("outer.inner"), st, allowed))
}

func TestResolveIntentAlias_ParallelStatePath(t *testing.T) {
	t.Parallel()

	root := &app.State{
		IntentAliases: map[string]string{"talk": "wrapped__talk"},
		States: map[string]*app.State{
			"inner": {
				DefaultIntent: "talk",
				On: map[string][]app.Transition{
					"wrapped__talk": nil,
				},
			},
		},
	}

	def := &app.AppDef{
		App:  app.AppMeta{ID: "ancestor-intent-alias", Version: "0.1.0"},
		Root: "outer.inner",
		States: map[string]*app.State{
			"outer": root,
		},
		Intents: map[string]app.Intent{
			"wrapped__talk": {
				Slots: map[string]app.Slot{
					"message": {Type: "string", Required: true},
				},
			},
		},
	}

	st := lookupStateByPath(def, app.StatePath("outer.inner"))
	require.Equal(t, "wrapped__talk", resolveIntentAlias(def, app.StatePath("outer.inner#2"), st, "talk"))
}

package orchestrator

import (
	"hally/internal/app"
	"hally/internal/expr"
	"hally/internal/world"
)

// renderStateView renders the view template for a state using the given world.
func renderStateView(def *app.AppDef, state app.StatePath, w world.World) (string, error) {
	s := lookupStateByPath(def, state)
	if s == nil || s.View == "" {
		return "", nil
	}
	env := expr.Env{
		Slots: make(map[string]any),
		World: w.Vars,
		Event: make(map[string]any),
	}
	return expr.Render(s.View, env)
}

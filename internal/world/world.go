// Package world defines the runtime world snapshot: the typed collection of
// persistent context variables declared by the app author in YAML §3.
package world

import "encoding/json"

// World is an immutable snapshot of all world variables at a point in time.
// It is passed to guard evaluations, view template rendering, and effect
// application. World is never mutated in place; effects produce a new World.
// JSON tags allow the snapshot to cross the MCP boundary and be stored in SQLite.
type World struct {
	// Vars holds the current value of every declared world variable.
	// Keys match the names declared in the YAML world schema.
	Vars map[string]any `json:"vars"`
}

// New returns an empty World with an initialized Vars map.
func New() World {
	return World{Vars: make(map[string]any)}
}

// Get returns the value of a world variable by name.
// Returns nil if the variable is not set.
func (w World) Get(name string) any {
	return w.Vars[name]
}

// With returns a new World with the given variable set to value.
// The original World is not modified.
func (w World) With(name string, value any) World {
	next := World{Vars: make(map[string]any, len(w.Vars)+1)}
	for k, v := range w.Vars {
		next.Vars[k] = v
	}
	next.Vars[name] = value
	return next
}

// Slots is the typed collection of slot values extracted by the LLM harness
// for a single intent call. Keys are slot names; values are typed JSON values.
type Slots map[string]any

// MarshalJSON encodes the slot map as a JSON object.
func (s Slots) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}

package world

import "encoding/json"

// World is an immutable snapshot of every world variable at one point in a
// run. It is passed by value to guard evaluation, view rendering, and effect
// application so those readers cannot accidentally mutate shared state — the
// only way to "change" a World is [World.With], which returns a fresh copy.
// JSON tags let the snapshot cross the MCP boundary and persist in SQLite.
//
// The zero World (Vars == nil) is read-safe: [World.Get] returns nil and
// JSON-marshals as {"vars":null}. Use [New] when you intend to mutate via
// [World.With], which always allocates its own map and never aliases the
// receiver's. A World is not safe for concurrent mutation, but since it is
// never mutated in place, concurrent readers of one snapshot are fine.
type World struct {
	// Vars holds the current value of every declared world variable.
	// Keys match the names declared in the YAML world schema
	// (docs/embedded/app-schema.md); values are whatever effects have
	// written, untyped at this layer.
	Vars map[string]any `json:"vars"`
}

// New returns a World with an allocated (empty) Vars map. Prefer it over the
// zero value at the start of a run so the first [World.With] copies an empty
// map rather than ranging over nil — behaviour is identical either way, this
// just states the intent to build up state.
func New() World {
	return World{Vars: make(map[string]any)}
}

// Get returns the current value of a world variable, or nil if it was never
// set. Get does not distinguish "absent" from "set to nil" — callers that
// need that distinction must inspect Vars directly. Safe on the zero World.
func (w World) Get(name string) any {
	return w.Vars[name]
}

// With returns a new World with name set to value, leaving the receiver
// untouched. This copy-on-write step is the package's whole point: effects
// thread state forward by chaining With calls, so a guard or view that read
// an earlier snapshot can never observe a later write. Cost is O(n) in the
// number of variables per call; world maps are small (tens of keys), so the
// copy is deliberately preferred over the aliasing hazard of in-place edits.
// Safe to call on the zero World.
func (w World) With(name string, value any) World {
	next := World{Vars: make(map[string]any, len(w.Vars)+1)}
	for k, v := range w.Vars {
		next.Vars[k] = v
	}
	next.Vars[name] = value
	return next
}

// Slots is the per-call collection of slot values the LLM harness extracted
// for one intent invocation. Unlike [World], which persists across a run,
// Slots are ephemeral inputs to a single turn — kept distinct so effects can
// promote a slot into a world variable explicitly rather than the two pools
// silently merging. Keys are slot names; values are typed JSON scalars.
type Slots map[string]any

// MarshalJSON encodes Slots as a plain JSON object. The explicit method
// pins the wire shape to the underlying map's semantics: an empty (non-nil)
// Slots marshals as {}, a populated one as {"name":value}, and a nil Slots
// as null. It exists so the map type — not some future field set — owns the
// encoding, keeping the MCP payload identical to a bare map[string]any.
func (s Slots) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}
